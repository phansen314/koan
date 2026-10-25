package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// Main runs one command line and gives the exit code. Whatever happens,
// it prints exactly one envelope, unless the line asks for help or
// completion, which print cobra's text for people, or names a plain
// command, which prints its text, or on failure only the standard-error
// line.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer, commands []*Command) (code int) {
	var (
		warnings []Warning
		c        *Command
		l        *line
	)
	out := func(result any, e *Error, warnings []Warning) int {
		if c != nil && c.Plain {
			return writePlain(stdout, stderr, result, e, warnings)
		}
		return write(stdout, stderr, result, e, warnings)
	}
	defer func() {
		if r := recover(); r != nil {
			e := NewError(KindInternal, fmt.Sprintf("koan failed: %v", r))
			code = out(nil, e, warnings)
		}
	}()

	env := &Env{Stdin: stdin, Stderr: stderr}
	completing := len(args) > 0 && strings.HasPrefix(args[0], cobra.ShellCompRequestCmd)
	root, named := tree(commands, stdout, env, completing, func(cmd *Command, ln *line) { c, l = cmd, ln })
	root.SetIn(stdin)
	root.SetErr(stderr)
	// A nil slice would have cobra read os.Args.
	root.SetArgs(append([]string{}, args...))
	if cmd, err := root.ExecuteC(); err != nil {
		c = named[cmd]
		return out(nil, usage(err), nil)
	}
	if c == nil {
		return ExitOK
	}

	in := l.in
	var e *Error
	if l.hasInput {
		if in, e = readInput(env, l.input); e != nil {
			return out(nil, e, nil)
		}
	} else if e := c.readFiles(env, l); e != nil {
		return out(nil, e, nil)
	}
	if e := c.unknownFields(in); e != nil {
		return out(nil, e, nil)
	}
	result, warnings, e := c.Run(env, in)
	return out(result, e, warnings)
}

// tree is the cobra command tree: a command of two words sits under a
// group named by its first. The root and each group refuse a line that
// names no command of theirs as usage, where cobra would print help.
// Help and completion print to stdout; completing is whether the line
// completes one. A value with nothing to complete offers nothing, never
// file names. named maps each command's cobra command back to it.
func tree(commands []*Command, stdout io.Writer, env *Env, completing bool, ran func(*Command, *line)) (root *cobra.Command, named map[*cobra.Command]*Command) {
	named = map[*cobra.Command]*Command{}
	root = &cobra.Command{
		Use:           "koan",
		Short:         "Task management for one person on one machine",
		Args:          cobra.ArbitraryArgs,
		RunE:          unknown(""),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	directive := noFiles
	root.CompletionOptions.DefaultShellCompDirective = &directive
	groups := map[string]*cobra.Command{}
	for _, c := range commands {
		parent := root
		if g, _, ok := strings.Cut(c.Name, " "); ok {
			if groups[g] == nil {
				groups[g] = &cobra.Command{Use: g, Short: groupShorts[g], Args: cobra.ArbitraryArgs, RunE: unknown(g)}
				root.AddCommand(groups[g])
			}
			parent = groups[g]
		}
		cc := c.cobra(env, completing, ran)
		named[cc] = c
		parent.AddCommand(cc)
	}
	// cobra's completion command prints its help, exit 0, for a shell it
	// doesn't know; an unknown shell is usage instead. It keeps the
	// output it is made with, so that is set first.
	root.SetOut(stdout)
	root.InitDefaultCompletionCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "completion" {
			cmd.Args = cobra.ArbitraryArgs
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return cmd.Help()
				}
				return Usage(args[0], "unknown shell "+args[0])
			}
		}
	}
	return root, named
}

// groupShorts are each group's line in help.
var groupShorts = map[string]string{
	"task":   "Make, change and find tasks",
	"folder": "Make, change and find folders",
	"marker": "Bind directories outside the tree, such as worktrees, to folders",
}

// unknown refuses a line that names no command under group, "" for the
// root.
func unknown(group string) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			if group == "" {
				return Usage("", "no command given")
			}
			return Usage("", "no command given after "+group)
		}
		return Usage(args[0], "unknown command "+strings.TrimSpace(group+" "+args[0]))
	}
}
