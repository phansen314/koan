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
	// is refused as usage. One out of range is the command's to refuse,
	// as through --input.
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
	// Required, for a position: the command line must give one.
	Required bool
	// Short is the option's one letter, such as t for -t, if it has one.
	Short string
	// Usage is the option's line in help.
	Usage string
	// Complete, for an argument that takes a reference, is what it
	// completes to at a shell's Tab.
	Complete Completer
	// Arg, for a position, is what help calls each of its words; the
	// field when not set. For an option that takes a reference, it is
	// what help calls its value, such as folder.
	Arg string
	// Holds, for a position, is what it holds in a message in the command
	// line's terms, such as "the folders to join after the task or folder"; made
	// from Arg when not set.
	Holds string
}

// flag is the option's name without its dashes, as pflag knows it:
// the field in kebab-case, such as blocked-by.
func (o Option) flag() string {
	return strings.ReplaceAll(o.Field, "_", "-")
}

// holds is what a position holds, in a message.
func (o Option) holds() string {
	switch {
	case o.Holds != "":
		return o.Holds
	case o.Kind == List:
		return "the " + o.arg() + "s"
	}
	return "the " + o.arg()
}

// arg is what help calls each word of a position.
func (o Option) arg() string {
	if o.Arg != "" {
		return o.Arg
	}
	return o.Field
}

// File is a convenience that sets an input field from a file read whole,
// or from standard input for -, such as --notes-file for notes. It isn't
// an input field: through --input, the field itself is given.
type File struct {
	Name  string // as typed, such as --notes-file
	Field string
	Usage string // its line in help
}

// Here is a convenience that sets an input field to the working
// directory: when the command line doesn't give the field, such as
// bind's dir, or with Flag, only when that option is given, such as
// context's --here.
type Here struct {
	Field string
	Flag  string // as typed, such as --here
	Usage string // the flag's line in help
	One   bool   // the subject, Field and Flag take exactly one, as where's do
}

// Input is a command's input: one JSON object, with numbers kept as
// json.Number.
type Input map[string]any

// Env is what a command may know about where it runs.
type Env struct {
	Stdin io.Reader
	// Stderr is for what a program a command runs writes there, such as
	// fzf's own complaints; koan's own line is Main's.
	Stderr io.Writer
	// Term names an input field in a message, in the terms the input
	// came in: an option as typed, a position by what it holds. It is
	// nil for --input, whose messages name the field itself.
	Term func(field string) string
}

// Name is the name of field in a message: env's, or the field itself.
func (e *Env) Name(field string) string {
	if e == nil || e.Term == nil {
		return field
	}
	return e.Term(field)
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

// Command is one koan command: its name, its subject, object and
// options, and what it does with an input whose fields are all its own.
type Command struct {
	Name  string
	Short string // its line in help
	// Uses are help's usage lines, each after the command's name, where
	// its positions alone don't show its forms.
	Uses []string
	// Subject is the first position: one word, or with Kind List the
	// words before the object, each read as a List option's text is.
	Subject *Option
	// Object, for a command whose verb takes one, is the last position:
	// one word, or with Kind List every word after the subject. A list
	// subject's object is required, so the last word is always it; its
	// Complete then offers what either position takes, since any word
	// after the first can be the last.
	Object *Option
	// Instead, for a command an option can take the object's place on,
	// is that option's field: given, every positional is a subject.
	Instead string
	// Single, for a command a Bool option limits to one subject, is that
	// option's field. It changes only completion: given true, the first
	// word completes to SingleSubject and the second to SingleObject, each
	// if set, and no word after them completes. The line still reads as
	// a list of subjects before an object, for the command to refuse more
	// than one.
	Single string
	// SingleSubject and SingleObject, with Single, are what the subject
	// and the object complete to once Single is given true, if not the
	// positions' own Complete.
	SingleSubject Completer
	SingleObject  Completer
	Options       []Option
	Files         []File
	Here          *Here
	// Fields are input fields only --input gives, with no option.
	Fields []string
	// Plain marks a command for people, which prints plain text rather
	// than an envelope: its result is a Text, and a failure prints only
	// the standard-error line.
	Plain bool
	Run   func(env *Env, in Input) (result any, warnings []Warning, err *Error)
}

// term names an input field as the command line gives it: an option as
// typed, a position by what it holds.
func (c *Command) term(field string) string {
	for _, o := range c.Options {
		if o.Field == field {
			return "--" + o.flag()
		}
	}
	for _, p := range []*Option{c.Subject, c.Object} {
		if p != nil && p.Field == field {
			return p.holds()
		}
	}
	for _, f := range c.Files {
		if f.Field == field {
			return f.Name
		}
	}
	return field
}

// fields is every input field the command takes.
func (c *Command) fields() map[string]bool {
	f := map[string]bool{}
	if c.Subject != nil {
		f[c.Subject.Field] = true
	}
	if c.Object != nil {
		f[c.Object.Field] = true
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
	// here is whether a Here convenience's Flag was given true.
	here bool
	// boolValues are the hints for positional words that read as a
	// boolean's value, by the word, for a fault found in the input.
	boolValues map[string]string
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
	if v.opt.Arg != "" {
		return v.opt.Arg
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

// hereValue is a Here convenience's Flag: true or false, bare for true.
type hereValue struct {
	l    *line
	seen bool
}

func (v *hereValue) String() string { return "" }
func (v *hereValue) Type() string   { return "bool" }

func (v *hereValue) Set(text string) error {
	if v.seen {
		return errTwice
	}
	v.seen = true
	if text != "true" && text != "false" {
		return errors.New("not true or false")
	}
	v.l.here = text == "true"
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
// into a line of its own, then hands both to ran. While a line is
// completed, its options take any text, and options it doesn't know are
// passed over.
func (c *Command) cobra(env *Env, completing bool, ran func(*Command, *line)) *cobra.Command {
	l := newLine()
	words := strings.Fields(c.Name)
	use := words[len(words)-1]
	for _, p := range []*Option{c.Subject, c.Object} {
		if p == nil {
			continue
		}
		word := "<" + p.arg() + ">"
		if p.Kind == List {
			word += "..."
		}
		if !p.Required {
			word = "[" + word + "]"
		}
		use += " " + word
	}
	cc := &cobra.Command{
		Use:   use,
		Short: c.Short,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if e := c.finish(cmd, l, args); e != nil {
				return e
			}
			ran(c, l)
			return nil
		},
	}
	if len(c.Uses) > 0 {
		lines := make([]string, len(c.Uses))
		for i, u := range c.Uses {
			lines[i] = "koan " + c.Name + " " + u + " [flags]"
		}
		cc.SetUsageTemplate(strings.Replace(cc.UsageTemplate(), "{{.UseLine}}", strings.Join(lines, "\n  "), 1))
	}
	cc.ValidArgsFunction = c.subjectCompletion(env, l)
	flags := cc.Flags()
	add := func(v pflag.Value) pflag.Value {
		if completing {
			return lenient{v}
		}
		return v
	}
	if completing {
		cc.FParseErrWhitelist.UnknownFlags = true
	}
	for i := range c.Options {
		o := &c.Options[i]
		flags.VarP(add(&value{opt: o, l: l}), o.flag(), o.Short, o.Usage)
		if o.Kind == Bool {
			flags.Lookup(o.flag()).NoOptDefVal = "true"
		}
		switch {
		case o.Kind == Path:
			cc.RegisterFlagCompletionFunc(o.flag(), fixed(fileNames))
		case o.Complete != nil:
			cc.RegisterFlagCompletionFunc(o.flag(), completion(env, o.Complete))
		}
	}
	for i := range c.Files {
		f := &c.Files[i]
		name := strings.TrimPrefix(f.Name, "--")
		flags.Var(add(&fileValue{f: f, l: l}), name, f.Usage)
		cc.RegisterFlagCompletionFunc(name, fixed(fileNames))
	}
	if h := c.Here; h != nil && h.Flag != "" {
		name := strings.TrimPrefix(h.Flag, "--")
		flags.Var(add(&hereValue{l: l}), name, h.Usage)
		flags.Lookup(name).NoOptDefVal = "true"
	}
	flags.VarP(add(&inputValue{l: l}), "input", "i", "the whole input as one JSON object")
	cc.RegisterFlagCompletionFunc("input", fixed(fileNames))
	return cc
}

// finish checks what pflag can't: a convenience with its option, --input
// alone, and the positions.
func (c *Command) finish(cmd *cobra.Command, l *line, args []string) *Error {
	for _, f := range c.Files {
		for _, o := range c.Options {
			if o.Field == f.Field && cmd.Flags().Changed(o.flag()) && cmd.Flags().Changed(strings.TrimPrefix(f.Name, "--")) {
				return Usage("", "--"+o.flag()+" and "+f.Name+" can't go together")
			}
		}
	}
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
	if c.Subject == nil {
		if len(args) > 0 {
			return Usage(args[0], c.extra(cmd, args[0], c.Name+" takes no words"))
		}
		return nil
	}
	object := c.Object
	if c.Instead != "" && cmd.Flags().Changed(strings.ReplaceAll(c.Instead, "_", "-")) {
		object = nil
	}
	list := c.Subject.Kind == List
	// The subject's words, then the object's: one subject before a list,
	// or a list of subjects before one object.
	n := len(args)
	if object != nil && n > 1 {
		n = 1
		if list {
			n = len(args) - 1
		}
	}
	subjects, objects := args[:n], args[n:]
	for _, word := range args {
		if h := c.boolValue(cmd, word); h != "" {
			if l.boolValues == nil {
				l.boolValues = map[string]string{}
			}
			l.boolValues[word] = h
		}
	}
	if !list && len(subjects) > 1 {
		return Usage(subjects[1], c.extra(cmd, subjects[1], c.Name+" takes one word for "+c.Subject.holds()))
	}
	if object != nil && object.Kind != List && len(objects) > 1 {
		return Usage(objects[1], c.extra(cmd, objects[1], c.Name+" takes one word for "+object.holds()))
	}
	for _, p := range []struct {
		opt   *Option
		words []string
	}{{c.Subject, subjects}, {object, objects}} {
		for _, word := range p.words {
			p.opt.set(l.in, word)
		}
		if len(p.words) > 0 && p.opt.Kind == Path {
			l.paths = append(l.paths, p.opt.Field)
		}
	}
	return nil
}

// extra is the message for a word past the positions: it says what the
// positions hold, and, for true, false or null, that a boolean's value
// goes after =, as the boolean options given are written.
func (c *Command) extra(cmd *cobra.Command, word, message string) string {
	return "unexpected word " + word + ": " + message + c.boolValue(cmd, word)
}

// boolValue is what to add to a fault in a positional word that is true,
// false or null, when a boolean option was given bare: its value goes
// after =. It is "" for any other word.
func (c *Command) boolValue(cmd *cobra.Command, word string) string {
	if word != "true" && word != "false" && word != "null" {
		return ""
	}
	var given []string
	for _, o := range c.Options {
		if o.Kind == Bool && cmd.Flags().Changed(o.flag()) {
			given = append(given, "--"+o.flag()+"="+word)
		}
	}
	if len(given) == 0 {
		return ""
	}
	return "; a boolean's value goes after =, as " + strings.Join(given, " or ")
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
		if jsonInteger(text) {
			return json.Number(text)
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
	if h := c.Here; h != nil && h.Flag != "" {
		if l.here {
			if _, ok := l.in[h.Field]; ok {
				count := "at most"
				if h.One {
					count = "exactly"
				}
				message := "give " + count + " one of " + c.term(c.Subject.Field) + ", " + c.term(h.Field) + " and " + h.Flag
				return InvalidInput(message, Problem{Field: "", Reason: message})
			}
			wd, e := workingDir()
			if e != nil {
				return e
			}
			l.in[h.Field] = wd
		}
	} else if h != nil {
		if _, ok := l.in[h.Field]; !ok {
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
// input is closed. A required subject that isn't given is a fault found
// too, as when an old field name stands in its place.
func (c *Command) unknownFields(in Input) *Error {
	known := c.fields()
	var problems []Problem
	for k := range in {
		if !known[k] {
			problems = append(problems, Problem{Field: Pointer(k), Reason: "unknown field " + k})
		}
	}
	if len(problems) == 0 {
		return nil
	}
	byField := func(i, j int) bool { return problems[i].Field < problems[j].Field }
	sort.Slice(problems, byField)
	message := problems[0].Reason
	if c.Subject != nil && c.Subject.Required {
		if _, ok := in[c.Subject.Field]; !ok {
			problems = append(problems, Problem{Field: Pointer(c.Subject.Field), Reason: c.Subject.Field + " is required"})
			sort.Slice(problems, byField)
		}
	}
	return InvalidInput(message, problems...)
}

// Pointer is the JSON Pointer to a top-level field.
func Pointer(field string) string {
	return "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(field)
}
