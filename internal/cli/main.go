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
		return out(nil, hinted(c, usage(err)), nil)
	}
	if c == nil {
		return ExitOK
	}

	in := l.in
	if !l.hasInput {
		env.Term = c.term
	}
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
	return out(result, c.boolHinted(l, e), warnings)
}

// siblings are the options a command doesn't take but a sibling does,
// with what to say about each: an option the command's subject or object
// stands in for, or another option for it. A spelling an earlier version
// took has no entry.
var siblings = map[string]map[string]string{
	"task list":   {"--folder": "the folder is a word of its own, as in koan task list /work"},
	"frontier":    {"--folder": "the folder is a word of its own, as in koan frontier /work"},
	"claim-next":  {"--folder": "the folder is a word of its own, as in koan claim-next /work"},
	"folder list": {"--folder": "the folder is a word of its own, as in koan folder list /work"},
	"context":     {"--folder": "the folder is a word of its own, as in koan context /work"},
	"export":      {"--folder": "the folder is a word of its own, as in koan export /work"},
	"join":        {"--join": "name the folders to join after the task as words of their own, as in koan join 42 /work/a /work/b"},
	"update":      {"--tags": "update takes --tags-add, --tags-remove and --tags-replace-all"},
	"task create": {"--tags-add": "task create takes --tags", "--title": "the title is a word of its own, as in koan task create 'Write it'"},
}

// boolHinted adds to an invalid-input or not-found whose first problem is
// at a position what to write when a boolean's value stood in it as a word
// of its own: --claimed=false. The word is the one the fault quotes.
func (c *Command) boolHinted(l *line, e *Error) *Error {
	if e == nil || l == nil || len(l.boolValues) == 0 || len(e.Problems) == 0 ||
		(e.Kind != KindInvalidInput && e.Kind != KindNotFound) {
		return e
	}
	at := e.Problems[0].Field
	for _, p := range []*Option{c.Subject, c.Object} {
		if p == nil || at != Pointer(p.Field) && !strings.HasPrefix(at, Pointer(p.Field)+"/") {
			continue
		}
		for word, hint := range l.boolValues {
			if strings.Contains(e.Problems[0].Reason, `"`+word+`"`) {
				e.Message += hint
				e.Problems[0].Reason += hint
				return e
			}
		}
	}
	return e
}

// hinted adds to the usage error for an unknown option what a sibling
// command's option of that name does here, if the command has an entry
// for it.
func hinted(c *Command, e *Error) *Error {
	if c == nil || e.Kind != KindUsage || len(e.Problems) != 1 {
		return e
	}
	hint, ok := siblings[c.Name][e.Problems[0].Field]
	if !ok || !strings.HasPrefix(e.Message, "unknown flag") {
		return e
	}
	e.Message += "; " + hint
	e.Problems[0].Reason = e.Message
	return e
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
		RunE:          unknown("", nil),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	directive := noFiles
	root.CompletionOptions.DefaultShellCompDirective = &directive
	groups := map[string]*cobra.Command{}
	top := map[string]bool{}
	for _, c := range commands {
		if !strings.Contains(c.Name, " ") {
			top[c.Name] = true
		}
	}
	for _, c := range commands {
		parent := root
		if g, _, ok := strings.Cut(c.Name, " "); ok {
			if groups[g] == nil {
				// Only task and folder ever held the commands now
				// top-level.
				var moved map[string]bool
				if g == "task" || g == "folder" {
					moved = top
				}
				groups[g] = &cobra.Command{Use: g, Short: groupShorts[g], Args: cobra.ArbitraryArgs, RunE: unknown(g, moved)}
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
	"task":   "Make and list tasks",
	"folder": "Make and list folders",
}

// unknown refuses a line that names no command under group, "" for the
// root. When the word after a group is a command of its own, one of
// top, the message says so.
func unknown(group string, top map[string]bool) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			if group == "" {
				return Usage("", "no command given")
			}
			return Usage("", "no command given after "+group)
		}
		message := "unknown command " + strings.TrimSpace(group+" "+args[0])
		if top[args[0]] {
			message += ": " + args[0] + " is koan " + args[0]
		}
		return Usage(args[0], message)
	}
}
