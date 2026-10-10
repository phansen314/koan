package migrations

import (
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
)

// Kind is a kind of versioned file.
type Kind string

const (
	// Task is a task file.
	Task Kind = "task"
	// Root is koan.json.
	Root Kind = "root"
)

// Format is what a step does with one kind of file: it reads schema From, in
// a format with the given JSON Schema and rules, and converts it.
type Format struct {
	// From is the schema this step reads.
	From int64
	// Schema is the JSON Schema of the format read, as the specs gave it when
	// that format was current. Only tests compile it: at runtime Check is the
	// same rules in code, and a test holds the two in agreement.
	Schema string
	// Check returns every rule the file, parsed with its repeated keys,
	// breaks in the format read; none means it is valid there. It runs
	// after the file's schema has been found to be From.
	Check func(obj *jsonio.Object, repeated []string) []errs.Problem
	// Convert returns the file converted to the schema this step writes. It
	// never changes obj, sees nothing else, and leaves `extra` as it finds it.
	Convert func(obj *jsonio.Object) (*jsonio.Object, error)
}

// Step is one migration step. A kind a step does not cover is nil.
type Step struct {
	Number int64
	Name   string
	Task   *Format
	Root   *Format
}

// For returns the step's format for kind, nil when it doesn't cover it.
func (s Step) For(kind Kind) *Format {
	switch kind {
	case Task:
		return s.Task
	case Root:
		return s.Root
	}
	return nil
}

// steps are the released steps, in order. A released step is never edited.
var steps = []Step{
	{
		Number: 1,
		Name:   "tree-marker",
		Root: &Format{
			From:    1,
			Schema:  rootSchema1,
			Check:   checkRoot1,
			Convert: convertRoot1,
		},
	},
}

// Steps returns the steps in order.
func Steps() []Step { return slices.Clone(steps) }

// Latest is the number of the last step: what version reports and a new
// tree's koan.json records.
func Latest() int64 { return steps[len(steps)-1].Number }

// Reads reports whether some step reads kind at schema: whether it is an
// older format this binary can check and convert.
func Reads(kind Kind, schema int64) bool {
	return format(kind, schema) != nil
}

// format is the first step's format that reads kind at schema.
func format(kind Kind, schema int64) *Format {
	for _, s := range steps {
		if f := s.For(kind); f != nil && f.From == schema {
			return f
		}
	}
	return nil
}

// Check returns the rules obj breaks in the older format of kind at schema,
// which must be one Reads accepts.
func Check(kind Kind, schema int64, obj *jsonio.Object, repeated []string) []errs.Problem {
	f := format(kind, schema)
	if f == nil {
		return []errs.Problem{{Field: "/schema", Reason: fmt.Sprintf("no older %s format has schema %d", kind, schema)}}
	}
	return f.Check(obj, repeated)
}

// Convert applies to obj, a file of kind at schema, every step from its
// schema on, in order: each step's output is the next one's input. A step
// that doesn't cover kind, or reads another schema than the file has reached,
// leaves the file alone. A file whose schema no step reads is returned as it
// is.
func Convert(kind Kind, schema int64, obj *jsonio.Object) (*jsonio.Object, error) {
	for _, s := range steps {
		f := s.For(kind)
		if f == nil || f.From != schema {
			continue
		}
		var err error
		if obj, err = f.Convert(obj); err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", s.Number, s.Name, err)
		}
		schema = f.From + 1
	}
	return obj, nil
}

// repeatedProblems are the problems of the repeated keys a file had.
func repeatedProblems(repeated []string) []errs.Problem {
	var out []errs.Problem
	for _, r := range repeated {
		out = append(out, errs.Problem{Field: r, Reason: "repeated key"})
	}
	return out
}

// UseSteps replaces the steps with list until the returned function is
// called. It is for tests, which need a step that covers task files, of which
// no released step yet does; it is not safe for concurrent use.
func UseSteps(list []Step) (restore func()) {
	saved := steps
	steps = list
	return func() { steps = saved }
}
