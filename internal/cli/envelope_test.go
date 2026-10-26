package cli

import (
	"reflect"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

func enum(t *testing.T, id, pointer string) []any {
	t.Helper()
	n := testrun.Load(t).Doc(id).At(pointer)
	if n == nil {
		t.Fatalf("no %s#%s", id, pointer)
	}
	var out []any
	for _, item := range n.Items {
		out = append(out, item.Value)
	}
	return out
}

func strs(ss []string) []any {
	var out []any
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

// The kinds and rules are exactly the error schema's, in its order.
func TestKindsAndRulesMatchSchema(t *testing.T) {
	if got, want := strs(Kinds), enum(t, "error", "/properties/kind/enum"); !reflect.DeepEqual(got, want) {
		t.Errorf("kinds %v, schema %v", got, want)
	}
	if got, want := append(strs(Rules), nil), enum(t, "error", "/properties/rule/enum"); !reflect.DeepEqual(got, want) {
		t.Errorf("rules %v, schema %v", got, want)
	}
}

// Every error and warning this package prints passes the schemas, with
// its fields in order.
func TestErrorsPassSchema(t *testing.T) {
	s := testrun.Load(t)
	var errs []*Error
	for _, k := range Kinds {
		if k != KindConflict {
			errs = append(errs, NewError(k, "m"))
		}
	}
	for _, r := range Rules {
		e := Conflict(r, "m")
		e.IDs, e.Paths = []int64{1}, []string{"/a"}
		errs = append(errs, e)
	}
	nf := NewError(KindNotFound, "m")
	dir := "/h/koan-root/archived/@5_f"
	nf.Archived = []Archived{{ID: 4, NotesPath: "/h/koan-root/archived/@4.md"}, {ID: 5, NotesPath: dir + "/@notes.md", Dir: &dir}}
	errs = append(errs, nf, Usage("--x", "unknown option --x"), InvalidInput("m", Problem{"/a", "r"}))
	warnings := []Warning{{Kind: "stuck-step", Message: "m", Paths: []string{"/a", "/b"}}}
	for _, e := range errs {
		line, ie := encode(nil, e, warnings)
		if ie != e {
			t.Fatalf("%s: encoded as %+v", e.Kind, ie)
		}
		if err := s.Validate("envelope", "", string(line)); err != nil {
			t.Errorf("%s: %v\n%s", e.Kind, err, line)
		}
		if err := s.Order("envelope", "", string(line)); err != nil {
			t.Errorf("%s: %v\n%s", e.Kind, err, line)
		}
	}
	// The schema refuses a conflict without a rule, and a rule on anything else.
	bad := Conflict(RuleCycle, "m")
	bad.Kind = KindIO
	line, _ := encode(nil, bad, nil)
	if s.Validate("envelope", "", string(line)) == nil {
		t.Errorf("schema took a rule on io: %s", line)
	}
}
