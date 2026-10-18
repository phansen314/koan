// Package cli maps the koan command line onto the commands in ops: it
// builds one command's input from the arguments, runs it, and prints its
// envelope (spec/cli.md). It adds no behavior of its own.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/store"
)

// Env is what one invocation reads and writes besides the command's own
// environment.
type Env struct {
	Ops    store.Env
	Stdin  io.Reader // read only when an option names it with -
	Stdout io.Writer // closed after the one write, if it is an io.Closer
	Stderr io.Writer // at most one line
}

// Exit codes.
const (
	ExitOK           = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitNotDelivered = 3
)

// ProcessEnv is this process's environment: the real filesystem, clock, and
// standard streams.
func ProcessEnv() Env {
	return Env{
		Ops:    store.Env{Home: os.Getenv("HOME"), Now: time.Now, FS: fsys.OS{}, LockWait: store.DefaultLockWait},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

// kind is how an option's value becomes a JSON value.
type kind int

const (
	text     kind = iota // a string
	integer              // an integer
	boolean              // true, or =false
	ids                  // comma-separated task IDs, repeatable
	names                // comma-separated strings, repeatable
	keys                 // one string per occurrence
	object               // one JSON value
	priority             // an integer, or null
	nullText             // a string, or null
)

type option struct {
	name  string // the option, and in snake_case the input field
	kind  kind
	usage string
	short string // a one-letter form, for the few options that have one
}

type command struct {
	name    string
	short   string
	subject string // the input field the one positional argument sets; "" for none
	subjKin kind
	options []option
}

var scopeOptions = []option{
	{"folder", text, "look only in this folder (default /)", ""},
	{"recursive", boolean, "include the folders under it (default true)", ""},
	{"tags-any", names, "only tasks with at least one of these tags", ""},
	{"tags-all", names, "only tasks with all of these tags", ""},
}

var narrowOptions = append(append([]option{}, scopeOptions...),
	option{"limit", integer, "return at most this many", ""},
	option{"fields", names, "return only these fields of each task", ""},
)

var commands = []command{
	{name: "init", short: "Create the tree and the state file, or whichever is missing"},
	{name: "version", short: "Report the binary's version and format"},
	{name: "info", short: "Report the root's state"},
	{name: "doctor", short: "Report everything wrong with the tree"},
	{name: "repair", short: "Fix what can be fixed safely", options: []option{
		{"kinds", names, "only these finding kinds, and every fix of them (default: all, but not dangling-reference, nor a stale-claim on a task that is gone)", ""}}},
	{name: "migrate", short: "Convert the tree to this binary's format", options: []option{
		{"dry-run", boolean, "report what would change and write nothing", ""}}},
	{name: "create-folder", short: "Create a folder", subject: "folder", options: []option{
		{"title", nullText, "the folder's title", ""},
		{"extra", object, "a JSON object stored with the folder", ""},
		{"notes", text, "the initial notes", ""},
		{"notes-file", text, "read the initial notes from a file, or - for stdin", ""}}},
	{name: "show-folder", short: "Return one folder", subject: "folder"},
	{name: "update-folder", short: "Change a folder's title or extra", subject: "folder", options: []option{
		{"title", nullText, "the new title, or null to clear it", ""},
		{"extra-merge", object, "a JSON object of keys to set", ""},
		{"extra-remove", keys, "a key to delete (repeatable)", ""},
		{"extra-replace-all", object, "the whole new extra object", ""}}},
	{name: "move-folder", short: "Move or rename a folder", subject: "folder", options: []option{
		{"to", text, "the folder's new path (required)", ""}}},
	{name: "delete-folder", short: "Remove a folder and everything in it", subject: "folder", options: []option{
		{name: "recursive", short: "r", kind: boolean, usage: "remove a folder that is not empty"},
		{"force", boolean, "with --recursive, remove material too: files and folders koan did not make", ""}}},
	{name: "create", short: "Create a task", subject: "title", options: []option{
		{"folder", text, "where the task goes (default /)", ""},
		{"priority", priority, "an integer; higher is more important", ""},
		{"tags", names, "tags", ""},
		{"blocked-by", ids, "IDs of the tasks it waits on", ""},
		{"blocks", ids, "IDs of the tasks that wait on it", ""},
		{"extra", object, "a JSON object stored with the task", ""},
		{"notes", text, "the initial notes", ""},
		{"notes-file", text, "read the initial notes from a file, or - for stdin", ""}}},
	{name: "create-batch", short: "Create several tasks that may wait on each other (input only)"},
	{name: "show", short: "Return one task", subject: "id", subjKin: integer},
	{name: "update", short: "Change a task's title, priority, tags, or extra", subject: "id", subjKin: integer, options: []option{
		{"title", text, "the new title", ""},
		{"priority", priority, "an integer, or null to clear it", ""},
		{"tags-add", names, "tags to add", ""},
		{"tags-remove", names, "tags to remove", ""},
		{"tags-replace-all", names, "the whole new set of tags", ""},
		{"extra-merge", object, "a JSON object of keys to set", ""},
		{"extra-remove", keys, "a key to delete (repeatable)", ""},
		{"extra-replace-all", object, "the whole new extra object", ""}}},
	{name: "done", short: "Mark a task done", subject: "id", subjKin: integer},
	{name: "reopen", short: "Make a done task open again", subject: "id", subjKin: integer},
	{name: "block", short: "Add blockers to a task", subject: "id", subjKin: integer, options: []option{
		{"blockers", ids, "IDs of the tasks to wait on (required)", ""}}},
	{name: "unblock", short: "Remove blockers from a task", subject: "id", subjKin: integer, options: []option{
		{"blockers", ids, "IDs to stop waiting on (required)", ""}}},
	{name: "move", short: "Move a task to another folder", subject: "id", subjKin: integer, options: []option{
		{"to", text, "the folder to move it into (required)", ""}}},
	{name: "delete", short: "Remove a task", subject: "id", subjKin: integer},
	{name: "frontier", short: "Return the ready, unclaimed tasks, in work order", options: narrowOptions},
	{name: "list", short: "Return the tasks, and optionally folders, in scope", options: append(append([]option{}, narrowOptions...),
		option{"claimed", boolean, "only claimed tasks, or with =false only unclaimed ones", ""},
		option{"readiness", names, "which of ready, blocked, done to return (default ready,blocked)", ""},
		option{"include-folders", boolean, "also return the folders in scope", ""})},
	{name: "why", short: "Say what a task is waiting on", subject: "id", subjKin: integer},
	{name: "claim", short: "Claim a task", subject: "id", subjKin: integer, options: []option{
		{"extra", object, "a JSON object stored with the claim", ""}}},
	{name: "claim-next", short: "Claim the first ready, unclaimed task", options: append(append([]option{}, scopeOptions...),
		option{"extra", object, "a JSON object stored with the claim", ""})},
	{name: "release", short: "End a claim", subject: "id", subjKin: integer},
}

// usageError is a command line koan can't turn into an input.
type usageError string

func (u usageError) Error() string { return string(u) }

func usagef(format string, args ...any) error { return usageError(fmt.Sprintf(format, args...)) }

// Run runs the command line args, without the program name, and returns the
// exit code. It writes exactly one envelope line, or help text, to stdout,
// and at most one line to stderr.
func Run(args []string, env Env) int {
	// cobra answers these hidden commands itself, with shell-completion
	// text: koan has no completion, and prints nothing but envelopes.
	if len(args) > 0 && strings.HasPrefix(args[0], "__complete") {
		return deliver(env, ops.Failure(errs.New(errs.Usage, "unknown command %q: see koan --help", args[0])))
	}
	var help bytes.Buffer
	var result *ops.Envelope
	root := newRoot(env, &result)
	root.SetArgs(args)
	root.SetOut(&help)
	root.SetErr(io.Discard)
	err := root.Execute()
	switch {
	case result != nil:
		return deliver(env, *result)
	case err != nil:
		return deliver(env, ops.Failure(errs.New(errs.Usage, "%s", err.Error())))
	}
	return write(env, help.Bytes(), ExitOK, "") // --help
}

// deliver prints an envelope and picks the exit code and the stderr line.
func deliver(env Env, e ops.Envelope) int {
	line, err := oneLine(e)
	if err != nil {
		e = ops.Failure(errs.New(errs.Internal, "encode the result: %v", err))
		line, _ = oneLine(e)
	}
	code, note := ExitOK, ""
	switch {
	case !e.OK && e.Error.Kind == errs.Usage:
		code, note = ExitUsage, fmt.Sprintf("koan: %s: %s", e.Error.Kind, e.Error.Message)
	case !e.OK:
		code, note = ExitError, fmt.Sprintf("koan: %s: %s", e.Error.Kind, e.Error.Message)
	case len(e.Warnings) == 1:
		note = "koan: 1 warning"
	case len(e.Warnings) > 1:
		note = fmt.Sprintf("koan: %d warnings", len(e.Warnings))
	}
	return write(env, line, code, note)
}

// oneLine encodes an envelope as one line, newline included, with <, >, and
// & left as they are.
func oneLine(e ops.Envelope) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// write makes the one write to stdout and checks that it landed. Only then
// is the exit code the command's, and only then is the stderr line written.
func write(env Env, out []byte, code int, note string) int {
	_, err := env.Stdout.Write(out)
	if c, ok := env.Stdout.(io.Closer); ok {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "koan: could not write the result: %v\n", err)
		return ExitNotDelivered
	}
	if note != "" {
		fmt.Fprintln(env.Stderr, note)
	}
	return code
}

func newRoot(env Env, result **ops.Envelope) *cobra.Command {
	root := &cobra.Command{
		Use:           "koan <command>",
		Short:         "Local, file-based task management with JSON output",
		Long:          "Local, file-based task management. Every command prints one JSON envelope on stdout.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usagef("missing command: see koan --help")
			}
			return usagef("unknown command %q: see koan --help", args[0])
		},
		RunE:              func(*cobra.Command, []string) error { return nil },
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	// Help is --help only: cobra always adds a help command, so this one
	// stands in for it, hidden and failing as an unknown command would.
	root.SetHelpCommand(&cobra.Command{Use: "help", Hidden: true, Args: cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error { return usagef(`unknown command "help": use --help`) }})
	root.Flags().SortFlags = false
	for _, c := range commands {
		root.AddCommand(newCommand(c, env, result))
	}
	return root
}

func newCommand(c command, env Env, result **ops.Envelope) *cobra.Command {
	use := c.name
	if c.subject != "" {
		use += " <" + c.subject + ">"
	}
	cmd := &cobra.Command{
		Use:                   use + " [options]",
		Short:                 c.short,
		Args:                  cobra.ArbitraryArgs,
		DisableFlagsInUseLine: true,
	}
	fs := cmd.Flags()
	fs.SortFlags = false
	for _, o := range c.options {
		switch o.kind {
		case boolean:
			// A string underneath, so that a value that is not true or
			// false reaches the command, as it would through --input.
			fs.StringP(o.name, o.short, "", o.usage)
			fs.Lookup(o.name).NoOptDefVal = "true"
		case ids, names, keys:
			fs.StringArray(o.name, nil, o.usage)
		default:
			fs.String(o.name, "", o.usage)
		}
	}
	fs.StringP("input", "i", "", "read the whole input as one JSON object from a file, or - for stdin")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		raw, err := buildInput(c, cmd, args, env)
		if err != nil {
			return err
		}
		e := ops.Run(env.Ops, c.name, raw)
		*result = &e
		return nil
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err.Error()) })
	return cmd
}

// buildInput turns a command line into the command's input object.
func buildInput(c command, cmd *cobra.Command, args []string, env Env) ([]byte, error) {
	fs := cmd.Flags()
	if fs.Changed("input") {
		if len(args) > 0 || fs.NFlag() > 1 {
			return nil, usagef("--input takes the whole input: give no other argument or option with it")
		}
		name, _ := fs.GetString("input")
		return readFile(name, env)
	}
	in := map[string]any{}
	switch {
	case c.subject == "" && len(args) > 0:
		return nil, usagef("%s takes no argument, got %q", c.name, args[0])
	case c.subject != "" && len(args) == 0:
		return nil, usagef("%s needs a <%s>", c.name, c.subject)
	case len(args) > 1:
		return nil, usagef("%s takes one <%s>, got %d arguments: quote a value with spaces", c.name, c.subject, len(args))
	case c.subject != "":
		in[c.subject] = scalar(c.subjKin, args[0])
	}
	if fs.Changed("notes") && fs.Changed("notes-file") {
		return nil, usagef("give --notes or --notes-file, not both")
	}
	for _, o := range c.options {
		if !fs.Changed(o.name) {
			continue
		}
		field := strings.ReplaceAll(o.name, "-", "_")
		switch o.kind {
		case boolean:
			s, _ := fs.GetString(o.name)
			in[field] = scalar(boolean, s)
		case ids:
			list := []any{}
			for _, v := range split(fs, o.name) {
				list = append(list, scalar(integer, v))
			}
			in[field] = list
		case names:
			in[field] = split(fs, o.name)
		case keys:
			in[field], _ = fs.GetStringArray(o.name)
		case object:
			s, _ := fs.GetString(o.name)
			dec := json.NewDecoder(strings.NewReader(s))
			dec.UseNumber()
			var v any
			if err := dec.Decode(&v); err != nil || dec.More() {
				return nil, usagef("--%s is not one JSON value", o.name)
			}
			in[field] = v
		default:
			s, _ := fs.GetString(o.name)
			if o.name == "notes-file" {
				data, err := readFile(s, env)
				if err != nil {
					return nil, err
				}
				in["notes"] = string(data)
				continue
			}
			in[field] = scalar(o.kind, s)
		}
	}
	return json.Marshal(in)
}

// scalar turns one argument into a JSON value. A value that is not what the
// kind asks for is passed on as a string, for the command to refuse as
// invalid-input, exactly as it would through --input.
func scalar(k kind, s string) any {
	switch k {
	case boolean:
		if b, err := strconv.ParseBool(s); err == nil && (s == "true" || s == "false") {
			return b
		}
	case nullText:
		if s == "null" {
			return nil
		}
		return s
	case priority:
		if s == "null" {
			return nil
		}
		fallthrough
	case integer:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	}
	return s
}

// split is a list option's values: every occurrence, each split at commas.
// An empty value gives an empty list.
func split(fs *pflag.FlagSet, name string) []string {
	vals, _ := fs.GetStringArray(name)
	out := []string{}
	for _, v := range vals {
		if v != "" {
			out = append(out, strings.Split(v, ",")...)
		}
	}
	return out
}

// readFile reads a file an option names, or stdin for -.
func readFile(name string, env Env) ([]byte, error) {
	if name == "-" {
		data, err := io.ReadAll(env.Stdin)
		if err != nil {
			return nil, usagef("read stdin: %v", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, usagef("read %s: %v", name, err)
	}
	return data, nil
}
