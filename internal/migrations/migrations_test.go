package migrations

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
)

func parse(t *testing.T, s string) (*jsonio.Object, []string) {
	t.Helper()
	obj, repeated, err := jsonio.ParseObject([]byte(s))
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return obj, repeated
}

func TestStepsInOrder(t *testing.T) {
	for i, s := range Steps() {
		if s.Number != int64(i+1) {
			t.Errorf("step %d has number %d", i, s.Number)
		}
		if s.Name == "" {
			t.Errorf("step %d has no name", s.Number)
		}
		if s.Task == nil && s.Root == nil {
			t.Errorf("step %d covers no kind", s.Number)
		}
	}
	if Latest() != 1 || Steps()[0].Name != "tree-marker" {
		t.Errorf("latest %d, first %q", Latest(), Steps()[0].Name)
	}
}

func TestReads(t *testing.T) {
	for _, tc := range []struct {
		kind   Kind
		schema int64
		want   bool
	}{
		{Root, 1, true},
		{Root, 0, false},
		{Root, 2, false},
		{Root, -1, false},
		{Task, 1, false},
	} {
		if got := Reads(tc.kind, tc.schema); got != tc.want {
			t.Errorf("Reads(%s, %d) = %v", tc.kind, tc.schema, got)
		}
	}
}

func TestConvertRoot(t *testing.T) {
	in, _ := parse(t, `{"schema": 1, "last_id": 42}`)
	out, err := Convert(Root, 1, in)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := jsonio.MarshalLine(out)
	if want := `{"schema":2,"migration":0}` + "\n"; string(got) != want {
		t.Errorf("got %s", got)
	}
	if again, _ := jsonio.MarshalLine(in); string(again) != `{"schema":1,"last_id":42}`+"\n" {
		t.Errorf("input changed: %s", again)
	}
}

// A file already in the latest format, or of a kind no step covers, is left
// alone.
func TestConvertLeavesCurrent(t *testing.T) {
	cur, _ := parse(t, `{"schema": 2, "migration": 1}`)
	if out, err := Convert(Root, 2, cur); err != nil || out != cur {
		t.Errorf("Convert(root 2) = %v, %v", out, err)
	}
	task, _ := parse(t, `{"schema": 1}`)
	if out, err := Convert(Task, 1, task); err != nil || out != task {
		t.Errorf("Convert(task 1) = %v, %v", out, err)
	}
}

func TestConvertRootWithoutLastID(t *testing.T) {
	in, _ := parse(t, `{"schema": 1}`)
	if _, err := Convert(Root, 1, in); err == nil || !strings.Contains(err.Error(), "last_id") {
		t.Errorf("err = %v", err)
	}
}

// Every older format's checker agrees with the format's JSON Schema.
func TestFormatsAgreeWithSchemas(t *testing.T) {
	for _, s := range Steps() {
		f := s.Root
		if f == nil {
			continue
		}
		docs := append(schematest.Mutations(t, `{"schema": 1, "last_id": 42}`), `{}`, `{"last_id": 0}`, `{"schema": 1}`)
		for _, doc := range docs {
			obj, repeated := parse(t, doc)
			if v, _ := obj.Get("schema"); v != json.Number("1") {
				continue // the schema is checked before the format's rules
			}
			problems := Check(Root, f.From, obj, repeated)
			libOK, lib := schematest.CheckSchema(t, "root-file-1", f.Schema, []byte(doc))
			var at []string
			for _, p := range problems {
				at = append(at, p.Field)
			}
			switch {
			case libOK != (len(problems) == 0):
				t.Errorf("schema accepts %v (%s), checker problems %q\n  %s", libOK, lib, at, doc)
			case !libOK && !lib.Matches(at):
				t.Errorf("schema rejects at %s, checker at %q\n  %s", lib, at, doc)
			}
		}
	}
}

func TestCheckRepeatedKey(t *testing.T) {
	obj, repeated := parse(t, `{"schema": 1, "last_id": 1, "last_id": 2}`)
	ps := Check(Root, 1, obj, repeated)
	if len(ps) != 1 || ps[0].Field != "/last_id" || ps[0].Reason != "repeated key" {
		t.Errorf("problems %v", ps)
	}
}
