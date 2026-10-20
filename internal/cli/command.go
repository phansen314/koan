package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Kind says how an option's text on the command line becomes the value
// of its input field.
type Kind int

const (
	// String is the text as typed.
	String Kind = iota
	// Integer is a JSON integer when the text is one, else the text, for
	// the command's own check to refuse.
	Integer
	// Bool is true when the option is given bare; --name=false is false
	// and --name=true true.
	Bool
	// List is comma-separated, and the option can repeat.
	List
	// Each is a list that takes one value per occurrence, since a value
	// can hold a comma.
	Each
	// JSON is one JSON value in one argument; text that isn't JSON is
	// kept as text, for the command's own check to refuse.
	JSON
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
	return "--" + strings.ReplaceAll(o.Field, "_", "-")
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
	Run     func(env *Env, in Input) (result any, warnings []Warning, err *Error)
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
	return f
}

// line is a command line, read but not yet turned into an input.
type line struct {
	input    string // the -i file, or "" when not given
	hasInput bool
	other    string // the first argument other than -i, as typed
	in       Input
}

// parse reads the arguments after the command's name.
func (c *Command) parse(args []string) (*line, *Error) {
	l := &line{in: Input{}}
	var subjects []string
	set := map[string]bool{}
	noteOther := func(a string) {
		if l.other == "" {
			l.other = a
		}
	}
	options := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if options || a == "-" || !strings.HasPrefix(a, "-") {
			subjects = append(subjects, a)
			noteOther(a)
			continue
		}
		if a == "--" {
			options = true
			continue
		}
		name, value, hasValue := strings.Cut(a, "=")
		if !strings.HasPrefix(name, "--") && name != "-i" || name == "-i" && hasValue {
			return nil, Usage(a, "unknown option "+a)
		}
		if name == "-i" || name == "--input" {
			if l.hasInput {
				return nil, Usage(a, "--input given twice")
			}
			if !hasValue {
				if i+1 == len(args) {
					return nil, Usage(a, a+" needs a value")
				}
				i++
				value = args[i]
			}
			l.input, l.hasInput = value, true
			continue
		}
		opt := c.option(name)
		if opt == nil {
			return nil, Usage(a, "unknown option "+name)
		}
		noteOther(a)
		if opt.Kind == Bool {
			if !hasValue {
				value = "true"
			}
		} else if !hasValue {
			if i+1 == len(args) {
				return nil, Usage(a, name+" needs a value")
			}
			i++
			value = args[i]
		}
		if set[opt.Field] && opt.Kind != List && opt.Kind != Each {
			return nil, Usage(a, name+" given twice")
		}
		set[opt.Field] = true
		opt.set(l.in, value)
	}
	if l.hasInput {
		if l.other != "" {
			return nil, Usage(l.other, "--input can't go with anything else")
		}
		return l, nil
	}
	if c.Subject == nil && len(subjects) > 0 {
		return nil, Usage(subjects[0], c.Name+" takes no subject")
	}
	if len(subjects) > 1 {
		return nil, Usage(subjects[1], "a second subject: "+c.Name+" takes one")
	}
	if len(subjects) == 1 {
		c.Subject.set(l.in, subjects[0])
	} else if c.Subject != nil && c.Subject.Required {
		return nil, Usage("", c.Name+" needs a subject: "+c.Subject.Field)
	}
	return l, nil
}

func (c *Command) option(name string) *Option {
	for i := range c.Options {
		if c.Options[i].Name() == name {
			return &c.Options[i]
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
		// Only text written as a JSON integer is one, so --priority 007
		// is refused as it would be through --input.
		if n, err := strconv.ParseInt(text, 10, 64); err == nil && jsonInteger(text) {
			return json.Number(strconv.FormatInt(n, 10))
		}
	case Bool:
		if text == "true" || text == "false" {
			return text == "true"
		}
	case JSON:
		if v, err := decodeOne(strings.NewReader(text)); err == nil {
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
func decodeOne(r io.Reader) (any, error) {
	dec := json.NewDecoder(r)
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

// readInput reads the -i file, or standard input for -, as one JSON
// object.
func readInput(env *Env, name string) (Input, *Error) {
	var data []byte
	var err error
	if name == "-" {
		data, err = io.ReadAll(env.Stdin)
		if err != nil {
			return nil, IO("could not read standard input: " + err.Error())
		}
	} else {
		data, err = os.ReadFile(name)
		if err != nil {
			abs, aerr := filepath.Abs(name)
			if aerr != nil {
				abs = name
			}
			return nil, IO("could not read the input file: "+err.Error(), abs)
		}
	}
	v, err := decodeOne(bytes.NewReader(data))
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
