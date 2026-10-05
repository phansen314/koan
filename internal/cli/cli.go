package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/pick"
)

// Env is what one invocation reads and writes besides the operation's own
// environment.
type Env struct {
	Ops    ops.Env
	Stdin  io.Reader              // read only when a value names it (--input -)
	Stdout io.Writer              // closed after the one write, if it is an io.Closer
	Stderr io.Writer              // only for one line after the result, or the notice when it is not delivered
	Getwd  func() (string, error) // the working directory, for a relative path
	Pick   pick.System            // finding and running fzf, for pick only
}

// Exit codes (cli-spec.md, Exit codes).
const (
	ExitOK           = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitNotDelivered = 3
)

// Main runs koan as this process: the real environment and the standard
// streams. args exclude the program name.
func Main(args []string) int {
	return Run(args, ProcessEnv())
}

// ProcessEnv is this process's environment: the real filesystem, clock, and
// standard streams.
func ProcessEnv() Env {
	return Env{
		Ops:    ops.NewEnv(os.Getenv, runtime.GOOS),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getwd:  os.Getwd, // $PWD when it names the working directory: as the shell reports it
		Pick:   pick.OSSystem(),
	}
}

// Run runs the command line args, without the program name, and returns the
// exit code. It writes exactly one envelope line, or help text, to stdout,
// and at most one line to stderr.
func Run(args []string, env Env) int {
	if len(args) > 0 && args[0] == pick.HelperCommand {
		out, code, note := helper(args[1:], env)
		return deliver(env, out, code, note)
	}
	out, code, note := execute(commands, args, env)
	return deliver(env, out, code, note)
}

// helper runs pick's hidden helper, which fzf's callbacks run. It is no
// command of the table, so that help, completion and suggestions never show
// it. It prints what fzf reads, not an envelope, unless it fails without
// a session or a verb; a verb's failure it reports in what fzf reads, and
// exits as the envelope would, with no stderr line.
func helper(args []string, env Env) ([]byte, int, string) {
	out, reported, e := pick.Helper(args, pick.Env{Ops: env.Ops, Sys: env.Pick})
	switch {
	case e == nil:
		return out, ExitOK, ""
	case reported:
		_, code, _ := envelopeLine(ops.Failed(e))
		return out, code, ""
	}
	return envelopeLine(ops.Failed(e))
}

// execute runs args against cmds and returns what to write, the exit code,
// and the note for stderr ("" for none).
func execute(cmds []Command, args []string, env Env) ([]byte, int, string) {
	var help bytes.Buffer
	var result *ops.Envelope
	root := newRoot(cmds, env, &result)
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(&help)
	root.SetErr(io.Discard)
	err := root.Execute()
	switch {
	case result != nil:
		return envelopeLine(*result)
	case err != nil:
		return envelopeLine(ops.Failed(usage(err)))
	}
	return help.Bytes(), ExitOK, "" // --help
}

func newRoot(cmds []Command, env Env, result **ops.Envelope) *cobra.Command {
	root := &cobra.Command{
		Use:           "koan",
		Short:         "Local, file-based task management with JSON output",
		SilenceErrors: true,
		SilenceUsage:  true,
		// Bare koan is a usage error, and an unknown command is reported
		// here rather than by cobra so its problem names the token.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageErr(nil, "missing command")
			}
			if cmd.ArgsLenAtDash() == 0 && isCommand(cmd, args[0]) {
				return usageErr(&args[0], "the command must come before --")
			}
			reason := "unknown command"
			if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
				reason += "; did you mean " + strings.Join(s, " or ") + "?"
			}
			return usageErr(&args[0], reason)
		},
		RunE:                       func(*cobra.Command, []string) error { return nil },
		SuggestionsMinimumDistance: 2,
		// No completion command: it is not in cli-spec.md, and prints no
		// envelope.
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	// Help is --help only. cobra always adds a help command, so this one
	// stands in for it, hidden and failing as an unknown command would.
	root.SetHelpCommand(&cobra.Command{
		Use:                "help",
		Hidden:             true,
		DisableFlagParsing: true,
		Args: func(cmd *cobra.Command, _ []string) error {
			name := cmd.Name()
			return usageErr(&name, "unknown command; for help, use --help")
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return err })
	for i := range cmds {
		root.AddCommand(newCommand(&cmds[i], env, result))
	}
	return root
}

func newCommand(c *Command, env Env, result **ops.Envelope) *cobra.Command {
	use := c.Name
	for _, a := range c.Args {
		use += " <" + a.Name + ">"
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   c.Summary,
		Example: c.Example,
		Args: func(cmd *cobra.Command, args []string) error {
			want := len(c.Args)
			if cmd.Flags().Changed("input") {
				want = 0
			}
			switch {
			case len(args) > want && cmd.Flags().Changed("input"):
				return usageErr(&args[0], "--input cannot be combined with arguments")
			case len(args) > want:
				return usageErr(&args[want], "unexpected argument")
			case len(args) < want:
				return usageErr(nil, "missing argument <"+c.Args[len(args)].Name+">")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if e := checkShape(c, cmd); e != nil {
				return e
			}
			env := runCommand(c, cmd, args, env)
			*result = &env
			return nil
		},
	}
	fs := cmd.Flags()
	fs.SortFlags = false
	for _, o := range c.Options {
		switch o.Type {
		case Bool:
			fs.BoolP(o.Name, o.Short, false, o.Help)
		case IDList, StringList, Repeated:
			fs.StringArrayP(o.Name, o.Short, nil, o.Help)
		default:
			fs.StringP(o.Name, o.Short, "", o.Help)
		}
	}
	fs.StringP("input", "i", "", "read the whole operation input from `file` (- for stdin)")
	return cmd
}

// isCommand reports whether name is one of root's commands, as typed.
func isCommand(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if !c.Hidden && c.Name() == name {
			return true
		}
	}
	return false
}

// checkShape reports what cobra cannot: mutually exclusive options given
// together, naming the one given later; --input together with a field
// option; and a missing required option, --input included.
func checkShape(c *Command, cmd *cobra.Command) error {
	for _, g := range c.Exclusive {
		// Visit goes in the order options were first given, since
		// SortFlags is off.
		var given []string
		cmd.Flags().Visit(func(f *pflag.Flag) {
			if slices.Contains(g, f.Name) {
				given = append(given, f.Name)
			}
		})
		if len(given) > 1 {
			arg := "--" + given[1]
			return usageErr(&arg, arg+" cannot be combined with --"+given[0])
		}
	}
	input := cmd.Flags().Changed("input")
	if c.InputRequired && !input {
		return usageErr(nil, "missing required option --input")
	}
	for _, o := range c.Options {
		given := cmd.Flags().Changed(o.Name)
		switch {
		case input && given && o.Field != "":
			arg := "--" + o.Name
			return usageErr(&arg, "--input cannot be combined with options that set input fields")
		case !input && !given && o.Required:
			return usageErr(nil, "missing required option --"+o.Name)
		}
	}
	return nil
}

// runCommand builds the operation's input and runs it.
func runCommand(c *Command, cmd *cobra.Command, args []string, env Env) ops.Envelope {
	var in *jsonio.Object
	var problems []errs.Problem
	if cmd.Flags().Changed("input") {
		path, _ := cmd.Flags().GetString("input")
		var e *errs.Error
		if in, e = readInput(path, env); e != nil {
			return ops.Failed(e)
		}
	} else {
		var e *errs.Error
		if in, problems, e = buildInput(c, cmd, args, env); e != nil {
			return ops.Failed(e)
		}
	}
	if c.Resolve != nil {
		ps, e := c.Resolve(in, env)
		if e != nil {
			return ops.Failed(e)
		}
		problems = append(problems, ps...)
	}
	if c.Run != nil {
		return c.Run(in, problems, env)
	}
	return runOp(c.Op, in, problems, env.Ops)
}

// runOp runs an operation; tests replace it to see the input built.
var runOp = ops.Run

// usageError is a usage problem found by koan rather than cobra.
type usageError struct{ problem errs.UsageProblem }

func (e *usageError) Error() string { return e.problem.Reason }

func usageErr(arg *string, reason string) error {
	return &usageError{errs.UsageProblem{Argument: arg, Reason: reason}}
}

// usage turns an error from cobra's Execute into a usage error, naming the
// offending token where the error says which it was.
func usage(err error) *errs.Error {
	var ue *usageError
	if errors.As(err, &ue) {
		return errs.Usage([]errs.UsageProblem{ue.problem})
	}
	p := errs.UsageProblem{Reason: err.Error()}
	var notExist *pflag.NotExistError
	var noValue *pflag.ValueRequiredError
	var badValue *pflag.InvalidValueError
	switch {
	case errors.As(err, &notExist):
		p.Argument = flagToken(notExist.GetSpecifiedName(), notExist.GetSpecifiedShortnames())
	case errors.As(err, &noValue):
		p.Argument = flagToken(noValue.GetSpecifiedName(), noValue.GetSpecifiedShortnames())
	case errors.As(err, &badValue):
		arg := "--" + badValue.GetFlag().Name
		p.Argument = &arg
	}
	return errs.Usage([]errs.UsageProblem{p})
}

// flagToken is the token pflag faults: a group of short options (without
// its "-"), else a long option's name.
func flagToken(name, shorts string) *string {
	t := "--" + name
	if shorts != "" {
		t = "-" + shorts
	}
	return &t
}

// envelopeLine encodes env as the one output line, with its exit code and
// its note for stderr.
func envelopeLine(env ops.Envelope) ([]byte, int, string) {
	b, err := jsonio.MarshalLine(env)
	if err != nil {
		env = ops.Failed(errs.Internal("encoding the envelope: " + err.Error()))
		if b, err = jsonio.MarshalLine(env); err != nil {
			panic(err) // a crash: outcome unknown
		}
	}
	switch {
	case env.OK:
		return b, ExitOK, warningsNote(len(env.Warnings))
	case env.Error.Kind == errs.KindUsage:
		return b, ExitUsage, errorNote(env.Error)
	}
	return b, ExitError, errorNote(env.Error)
}

// errorNote is the stderr line for a failure (cli-spec.md, Output), so it
// stays visible when stdout goes into a pipeline. A usage message's own
// "usage: " is dropped, since the kind already says it.
func errorNote(e *errs.Error) string {
	msg := e.Message
	if e.Kind == errs.KindUsage {
		msg = strings.TrimPrefix(msg, "usage: ")
	}
	return oneLine("koan: " + string(e.Kind) + ": " + msg)
}

// warningsNote is the stderr line for a success with n warnings; they are
// counted, never listed.
func warningsNote(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "koan: 1 warning (see .warnings in the output)"
	}
	return fmt.Sprintf("koan: %d warnings (see .warnings in the output)", n)
}

// oneLine is the one-line form of the stderr line (errs.OneLine).
func oneLine(s string) string { return errs.OneLine(s) }

func isLineControl(r rune) bool { return errs.IsLineControl(r) }

// deliver writes out in one write and closes stdout; exit codes 0-2 are
// reported only once both succeed (implementation-spec.md, Writing the
// envelope). Only then is note, if any, written to stderr, as one line whose
// own failure is ignored: the result was delivered.
func deliver(env Env, out []byte, code int, note string) int {
	_, err := env.Stdout.Write(out)
	if c, ok := env.Stdout.(io.Closer); ok && err == nil {
		err = c.Close()
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "koan: result not delivered: %v\n", err)
		return ExitNotDelivered
	}
	if note != "" {
		_, _ = io.WriteString(env.Stderr, note+"\n")
	}
	return code
}
