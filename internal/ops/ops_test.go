package ops

import (
	"sort"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// Each command's subject and options are exactly its schema's input
// fields, the subject being the field described as such.
func TestCommandsMatchSchemas(t *testing.T) {
	s := testrun.Load(t)
	seen := map[string]bool{}
	for _, c := range Commands() {
		id := strings.ReplaceAll(c.Name, " ", "-")
		if seen[id] {
			t.Errorf("%s registered twice", c.Name)
		}
		seen[id] = true
		input := s.Doc(id).At("/$defs/input")
		if input == nil {
			t.Errorf("%s: no schema %s with an input", c.Name, id)
			continue
		}
		var want, subjects, objects []string
		// An input's fields are its own and those of the fragments it
		// builds on through allOf, such as task list's filter, or of the
		// input it is through $ref, such as task why's.
		at := func(doc, ref string) (string, *testrun.Node) {
			name, ptr, _ := strings.Cut(ref, "#")
			if name == "" {
				name = doc
			}
			return name, s.Doc(name).At(ptr)
		}
		doc := id
		for input.Str("$ref") != "" {
			doc, input = at(doc, input.Str("$ref"))
		}
		parts := []*testrun.Node{input}
		for _, a := range input.Get("allOf").List() {
			if ref := a.Str("$ref"); ref != "" {
				_, part := at(doc, ref)
				parts = append(parts, part)
			}
		}
		for _, part := range parts {
			props := part.Get("properties")
			if props == nil {
				continue
			}
			want = append(want, props.Keys...)
			for _, k := range props.Keys {
				switch d := props.Get(k).Str("description"); {
				case strings.HasPrefix(d, "Subject.") || strings.HasPrefix(d, "Subject:"):
					subjects = append(subjects, k)
				case strings.HasPrefix(d, "Object.") || strings.HasPrefix(d, "Object:"):
					objects = append(objects, k)
				}
			}
		}
		var got []string
		if c.Subject != nil {
			got = append(got, c.Subject.Field)
		}
		if c.Object != nil && c.Object.Field != c.Instead {
			got = append(got, c.Object.Field)
		}
		for _, o := range c.Options {
			got = append(got, o.Field)
		}
		got = append(got, c.Fields...)
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: fields %v, schema %v", c.Name, got, want)
		}
		var subject []string
		if c.Subject != nil {
			subject = []string{c.Subject.Field}
		}
		if strings.Join(subject, ",") != strings.Join(subjects, ",") {
			t.Errorf("%s: subject %v, schema %v", c.Name, subject, subjects)
		}
		var object []string
		if c.Object != nil {
			object = []string{c.Object.Field}
		}
		if strings.Join(object, ",") != strings.Join(objects, ",") {
			t.Errorf("%s: object %v, schema %v", c.Name, object, objects)
		}
		if c.Run == nil {
			t.Errorf("%s: no Run", c.Name)
		}
	}
}

// Every input is closed: the schema refuses a field the command doesn't
// take, as the command does.
func TestInputsClosed(t *testing.T) {
	s := testrun.Load(t)
	for _, c := range Commands() {
		id := strings.ReplaceAll(c.Name, " ", "-")
		if err := s.Validate(id, "/$defs/input", `{"no_such_field":1}`); err == nil {
			t.Errorf("%s: schema takes an unknown field", c.Name)
		}
	}
}
