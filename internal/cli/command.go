package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Kind says how an option's text on the command line becomes the value
// of its input field.
type Kind int

const (
	// String is the text as typed.
	String Kind = iota
	// Integer is a JSON integer, written as JSON writes one; other text
	// is refused as usage. One too big for an int64 is kept as text, for
	// the command's own check to refuse.
	Integer
	// Bool is true when the option is given bare; --name=false is false
	// and --name=true true. Other text is refused as usage.
	Bool
	// List is comma-separated, and the option can repeat.
	List
	// Each is a list that takes one value per occurrence, since a value
	// can hold a comma.
	Each
	// JSON is one JSON value in one argument; text that isn't JSON is
	// kept as text, for the command's own check to refuse.
	JSON
	// Path is a path on disk, made absolute from the working directory
	// when it is relative, as the command line becomes an input. Empty
	// text is no path, and is kept, for the command's own check to
	// refuse.
	Path
)

// Option is one input field and how the command line sets it. Its
// option name is the field in kebab-case.
type Option struct {
	Field string
	Kind  Kind
	// Nullable: the text null is JSON null. Elsewhere it is the word.
	Nullable bool
	// Required, for a subject: the command line must give one.
	Required bool
}

// Name is the option as typed, such as --blocked-by.
func (o Option) Name() string {
	return "--" + o.flag()
}

// flag is the option's name without its dashes, as pflag knows it.
func (o Option) flag() string {
	return strings.ReplaceAll(o.Field, "_", "-")
}

// File is a convenience that sets an input field from a file read whole,
// or from standard input for -, such as --notes-file for notes. It isn't
// an input field: through --input, the field itself is given.
type File struct {
	Name  string // as typed, such as --notes-file
	Field string
}

// Here is a convenience that sets an input field to the working
// directory when the command line gives neither it nor any of Unless,
// such as context's file when no folder is given.
type Here struct {
	Field  string
	Unless []string
}

// Input is a command's input: one JSON object, with numbers kept as
// json.Number.
type Input map[string]any

// Env is what a command may know about where it runs.
type Env struct {
	Stdin io.Reader
}

// Home is ~, from HOME. A command that uses the root finds it as part of
// checking the root's state, after the input's shape; HOME unset, empty
// or not an absolute path is io, with no path.
func (e *Env) Home() (string, *Error) {
	home := os.Getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		return "", IO(fmt.Sprintf("could not find the home directory: HOME is %q, not an absolute path", home))
	}
	return filepath.Clean(home), nil
}

// Command is one koan command: its name, its subject and options, and
// what it does with an input whose fields are all its own.
type Command struct {
	Name    string
	Subject *Option
	Options []Option
	Files   []File
	Here    *Here
	// Fields are input fields only --input gives, with no option.
	Fields []string
	Run    func(env *Env, in Input) (result any, warnings []Warning, err *Error)
}

// fields is every input field the command takes.
func (c *Command) fields() map[string]bool {
	f := map[string]bool{}
	if c.Subject != nil {
		f[c.Subject.Field] = true
	}
	for _, o := range c.Options {
		f[o.Field] = true
	}
	for _, name := range c.Fields {
		f[name] = true
	}
	return f
}

// line is a command line, read but not yet turned into an input.
type line struct {
	input    string // the -i file, or "" when not given
	hasInput bool
	in       Input
	// files maps a field to the file a convenience reads it from.
	files map[string]string
	// paths are the fields of Path options given.
	paths []string
}

func newLine() *line {
	return &line{in: Input{}, files: map[string]string{}}
}

var errTwice = errors.New("given twice")

// value is an option's pflag.Value: it puts the option's text into the
// line's input as pflag reads it, and refuses a second occurrence of an
// option that isn't a list, and text its kind can't take.
type value struct {
	opt  *Option
	l    *line
	seen bool
}

func (v *value) String() string { return "" }

func (v *value) Type() string {
	switch v.opt.Kind {
	case Integer:
		return "int"
	case Bool:
		return "bool"
	case List:
		return "a,b,..."
	case JSON:
		return "json"
	case Path:
		return "path"
	}
	return "string"
}

func (v *value) Set(text string) error {
	if v.seen && v.opt.Kind != List && v.opt.Kind != Each {
		return errTwice
	}
	v.seen = true
	if err := v.opt.check(text); err != nil {
		return err
	}
	v.opt.set(v.l.in, text)
	if v.opt.Kind == Path {
		v.l.paths = append(v.l.paths, v.opt.Field)
	}
	return nil
}

// fileValue is a convenience's pflag.Value: it names the file its field
// is read from.
type fileValue struct {
	f    *File
	l    *line
	seen bool
}

func (v *fileValue) String() string { return "" }
func (v *fileValue) Type() string   { return "file|-" }

func (v *fileValue) Set(text string) error {
	if v.seen {
		return errTwice
	}
	v.seen = true
	v.l.files[v.f.Field] = text
	return nil
}

// inputValue is -i, --input.
type inputValue struct{ l *line }

func (v *inputValue) String() string { return "" }
func (v *inputValue) Type() string   { return "file|-" }

func (v *inputValue) Set(text string) error {
	if v.l.hasInput {
		return errTwice
	}
	v.l.input, v.l.hasInput = text, true
	return nil
}

// cobra makes the command's cobra command. It reads its command line
// into a line of its own, then hands both to ran.
func (c *Command) cobra(ran func(*Command, *line)) *cobra.Command {
	l := newLine()
	words := strings.Fields(c.Name)
	use := words[len(words)-1]
	if s := c.Subject; s != nil {
		if s.Required {
			use += " <" + s.Field + ">"
		} else {
			use += " [<" + s.Field + ">]"
		}
	}
	cc := &cobra.Command{
		Use:  use,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if e := c.finish(cmd, l, args); e != nil {
				return e
			}
			ran(c, l)
			return nil
		},
	}
	flags := cc.Flags()
	for i := range c.Options {
		o := &c.Options[i]
		flags.Var(&value{opt: o, l: l}, o.flag(), "")
		if o.Kind == Bool {
			flags.Lookup(o.flag()).NoOptDefVal = "true"
		}
	}
	for i := range c.Files {
		f := &c.Files[i]
		name := strings.TrimPrefix(f.Name, "--")
		flags.Var(&fileValue{f: f, l: l}, name, "")
		for _, o := range c.Options {
			if o.Field == f.Field {
				cc.MarkFlagsMutuallyExclusive(o.flag(), name)
			}
		}
	}
	flags.VarP(&inputValue{l: l}, "input", "i", "the whole input as one JSON object")
	return cc
}

// finish checks what pflag can't: --input alone, and the subject.
func (c *Command) finish(cmd *cobra.Command, l *line, args []string) *Error {
	if l.hasInput {
		other := ""
		if len(args) > 0 {
			other = args[0]
		} else {
			cmd.Flags().Visit(func(f *pflag.Flag) {
				if other == "" && f.Name != "input" {
					other = "--" + f.Name
				}
			})
		}
		if other != "" {
			return Usage(other, "--input can't go with anything else")
		}
		return nil
	}
	if c.Subject == nil && len(args) > 0 {
		return Usage(args[0], c.Name+" takes no subject")
	}
	if len(args) > 1 {
		return Usage(args[1], "a second subject: "+c.Name+" takes one")
	}
	if len(args) == 1 {
		c.Subject.set(l.in, args[0])
		if c.Subject.Kind == Path {
			l.paths = append(l.paths, c.Subject.Field)
		}
	} else if c.Subject != nil && c.Subject.Required {
		return Usage("", c.Name+" needs a subject: "+c.Subject.Field)
	}
	return nil
}

// parse reads the arguments after the command's name.
func (c *Command) parse(args []string) (*line, *Error) {
	var got *line
	cc := c.cobra(func(_ *Command, l *line) { got = l })
	cc.SilenceErrors, cc.SilenceUsage = true, true
	cc.SetOut(io.Discard)
	cc.SetErr(io.Discard)
	cc.SetArgs(append([]string{}, args...))
	if err := cc.Execute(); err != nil {
		return nil, usage(err)
	}
	if got == nil {
		return nil, Usage("", "help, not a command line")
	}
	return got, nil
}

// usage is the usage error for what cobra or pflag refused: an option by
// its long name, an unknown short option as typed, or "" when nothing
// single is at fault.
func usage(err error) *Error {
	var (
		e        *Error
		notExist *pflag.NotExistError
		required *pflag.ValueRequiredError
		invalid  *pflag.InvalidValueError
		syntax   *pflag.InvalidSyntaxError
	)
	switch {
	case errors.As(err, &e):
		return e
	case errors.As(err, &notExist):
		if notExist.GetSpecifiedShortnames() != "" {
			return Usage("-"+notExist.GetSpecifiedName(), err.Error())
		}
		return Usage("--"+notExist.GetSpecifiedName(), err.Error())
	case errors.As(err, &required):
		return Usage("--"+required.GetFlag().Name, err.Error())
	case errors.As(err, &invalid):
		return Usage("--"+invalid.GetFlag().Name, err.Error())
	case errors.As(err, &syntax):
		return Usage(syntax.GetSpecifiedFlag(), err.Error())
	}
	return Usage("", err.Error())
}

// check refuses text the option's kind can't take.
func (o *Option) check(text string) error {
	if o.Nullable && text == "null" {
		return nil
	}
	switch o.Kind {
	case Integer:
		if !jsonInteger(text) {
			return errors.New("not an integer")
		}
	case Bool:
		if text != "true" && text != "false" {
			return errors.New("not true or false")
		}
	}
	return nil
}

// set puts the option's text into the input.
func (o *Option) set(in Input, text string) {
	switch o.Kind {
	case List:
		list, _ := in[o.Field].([]any)
		for _, s := range strings.Split(text, ",") {
			list = append(list, s)
		}
		in[o.Field] = list
	case Each:
		list, _ := in[o.Field].([]any)
		in[o.Field] = append(list, text)
	default:
		in[o.Field] = o.value(text)
	}
}

func (o *Option) value(text string) any {
	if o.Nullable && text == "null" {
		return nil
	}
	switch o.Kind {
	case Integer:
		if n, err := strconv.ParseInt(text, 10, 64); err == nil && jsonInteger(text) {
			return json.Number(strconv.FormatInt(n, 10))
		}
	case Bool:
		if text == "true" || text == "false" {
			return text == "true"
		}
	case JSON:
		if v, err := decodeOne([]byte(text)); err == nil {
			return v
		}
	}
	return text
}

// jsonInteger reports whether text is an integer as JSON writes one: an
// optional "-", then digits with no leading zero.
func jsonInteger(text string) bool {
	digits := strings.TrimPrefix(text, "-")
	if digits == "" || digits[0] == '0' && len(digits) > 1 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// decodeOne reads exactly one JSON value, numbers kept as json.Number.
// JSON is UTF-8, so other bytes aren't JSON, though Go's decoder would
// quietly replace them.
func decodeOne(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errNotUTF8
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errTrailing
	}
	return v, nil
}

type trailingError struct{}

func (trailingError) Error() string { return "more than one JSON value" }

var errTrailing = trailingError{}

var errNotUTF8 = errors.New("it isn't UTF-8")

// readFile reads a file whole, or standard input for -. A file it can't
// read is io with its absolute path; standard input, io with no path.
func readFile(env *Env, name, what string) ([]byte, *Error) {
	if name == "-" {
		data, err := io.ReadAll(env.Stdin)
		if err != nil {
			return nil, IO("could not read standard input: " + err.Error())
		}
		return data, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		abs, aerr := filepath.Abs(name)
		if aerr != nil {
			abs = name
		}
		return nil, IO("could not read the "+what+": "+err.Error(), abs)
	}
	return data, nil
}

// workingDir is the working directory as the shell gave it. One koan
// can't resolve is io, with no path to give.
func workingDir() (string, *Error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", IO("could not resolve the working directory: " + err.Error())
	}
	return wd, nil
}

// readFiles sets each field a convenience names: from its file, or from
// the working directory.
func (c *Command) readFiles(env *Env, l *line) *Error {
	for _, field := range l.paths {
		p := l.in[field].(string)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			wd, e := workingDir()
			if e != nil {
				return e
			}
			p = filepath.Join(wd, p)
		}
		l.in[field] = filepath.Clean(p)
	}
	if h := c.Here; h != nil {
		given := false
		for _, f := range append([]string{h.Field}, h.Unless...) {
			_, ok := l.in[f]
			given = given || ok
		}
		if !given {
			wd, e := workingDir()
			if e != nil {
				return e
			}
			l.in[h.Field] = wd
		}
	}
	for _, f := range c.Files {
		name, ok := l.files[f.Field]
		if !ok {
			continue
		}
		data, e := readFile(env, name, f.Field+" file")
		if e != nil {
			return e
		}
		l.in[f.Field] = string(data)
	}
	return nil
}

// readInput reads the -i file, or standard input for -, as one JSON
// object.
func readInput(env *Env, name string) (Input, *Error) {
	data, e := readFile(env, name, "input file")
	if e != nil {
		return nil, e
	}
	v, err := decodeOne(data)
	if err != nil {
		return nil, InvalidInput("the input is not JSON: "+err.Error(), Problem{Field: "", Reason: "not JSON: " + err.Error()})
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, InvalidInput("the input is not a JSON object", Problem{Field: "", Reason: "not a JSON object"})
	}
	return Input(obj), nil
}

// unknownFields refuses fields the command doesn't take, since every
// input is closed.
func (c *Command) unknownFields(in Input) *Error {
	known := c.fields()
	var problems []Problem
	for k := range in {
		if !known[k] {
			problems = append(problems, Problem{Field: Pointer(k), Reason: "unknown field"})
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Field < problems[j].Field })
	return InvalidInput(c.Name+" takes no field "+strings.TrimPrefix(problems[0].Field, "/"), problems...)
}

// Pointer is the JSON Pointer to a top-level field.
func Pointer(field string) string {
	return "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(field)
}
