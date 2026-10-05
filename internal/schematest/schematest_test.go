package schematest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAllSchemasCompile(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		Schema(t, id)
	}
}

// The integer-literal check assumes every number outside extra, in every
// schema, is an integer (implementation-spec.md, Order of checks). Fail if a
// schema ever allows a non-integer number.
func TestNoNonIntegerNumbers(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(Dir(), id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		var walk func(v any, path string)
		walk = func(v any, path string) {
			switch v := v.(type) {
			case map[string]any:
				for k, x := range v {
					if k == "type" && (x == "number" || containsNumber(x)) {
						t.Errorf("%s%s: allows a non-integer number", id, path)
					}
					if k == "multipleOf" {
						t.Errorf("%s%s: uses multipleOf", id, path)
					}
					walk(x, path+"/"+k)
				}
			case []any:
				for _, x := range v {
					walk(x, path+"/[]")
				}
			}
		}
		walk(doc, "")
	}
}

// A value schema without a type would let a non-integer number through
// unnoticed by TestNoNonIntegerNumbers, so every one outside extra must say
// what it holds.
func TestNoUntypedValues(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(Dir(), id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		checkTyped(t, id, "", doc, !onlyDefs(doc))
	}
}

// onlyDefs reports whether a schema document is a container of $defs alone
// (pick-error-details), which validates no value itself.
func onlyDefs(doc any) bool {
	m, _ := doc.(map[string]any)
	for k := range m {
		if k != "$schema" && k != "$id" && k != "$defs" {
			return false
		}
	}
	_, ok := m["$defs"]
	return ok
}

// checkTyped checks s, a schema at path, and the value schemas within it. A
// value schema (value) must constrain its type. A branch — anyOf, oneOf,
// allOf, then, else — is one only when its parent is a value schema that does
// not; otherwise it refines a value typed elsewhere, as do its members.
func checkTyped(t *testing.T, id, path string, s any, value bool) {
	m, ok := s.(map[string]any)
	if !ok {
		if s != false {
			t.Errorf("%s%s: untyped schema %v", id, path, s)
		}
		return
	}
	typed := false
	for _, k := range []string{"type", "$ref", "const", "enum"} {
		if _, ok := m[k]; ok {
			typed = true
		}
	}
	branches := map[string]any{} // by path
	for _, k := range []string{"anyOf", "oneOf", "allOf"} {
		for i, b := range asSlice(m[k]) {
			branches[fmt.Sprintf("%s/%s/%d", path, k, i)] = b
		}
	}
	for _, k := range []string{"then", "else"} {
		if b, ok := m[k]; ok {
			branches[path+"/"+k] = b
		}
	}
	if value && !typed && len(branches) == 0 {
		t.Errorf("%s%s: untyped schema", id, path)
	}
	for bpath, b := range branches {
		checkTyped(t, id, bpath, b, value && !typed)
	}
	for _, k := range []string{"properties", "$defs"} {
		props, _ := m[k].(map[string]any)
		for name, p := range props {
			if k == "properties" && name == "extra" {
				continue
			}
			checkTyped(t, id, path+"/"+k+"/"+name, p, value || k == "$defs")
		}
	}
	for _, k := range []string{"items", "additionalProperties"} {
		if x, ok := m[k]; ok {
			checkTyped(t, id, path+"/"+k, x, value)
		}
	}
}

func asSlice(v any) []any {
	a, _ := v.([]any)
	return a
}

func containsNumber(v any) bool {
	a, ok := v.([]any)
	if !ok {
		return false
	}
	for _, x := range a {
		if x == "number" {
			return true
		}
	}
	return false
}

func TestECMAEngine(t *testing.T) {
	for _, tc := range []struct {
		pattern, in string
		match       bool
	}{
		{`^[^\u0000-\u001F]+$`, "abc", true},
		{`^[^\u0000-\u001F]+$`, "a\nb", false},
		{`^[^\u2028]$`, "\u2028", false},
		{`^\\u0041$`, `\u0041`, true}, // an escaped backslash, then a literal u0041
		{`^\\u0041$`, "A", false},
		{`^a\.b$`, "a.b", true},
		{`^[.]$`, ".", true},
		{`^[.]$`, "a", false},
	} {
		re, err := ecmaEngine(tc.pattern)
		if err != nil {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		if got := re.MatchString(tc.in); got != tc.match {
			t.Errorf("%s on %q: %v, want %v", tc.pattern, tc.in, got, tc.match)
		}
	}
}

// Syntax whose meaning differs between ECMA-262 and RE2 is refused.
func TestECMAEngineRefuses(t *testing.T) {
	for _, pattern := range []string{`^a.b$`, `^[a]..$`, `^\s$`, `^[\S]$`} {
		if _, err := ecmaEngine(pattern); err == nil {
			t.Errorf("%s: compiled", pattern)
		}
	}
}

// Parent-reported errors land at the child.
func TestLocations(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{`{"schema": 1, "last_id": 0}`, nil},
		{`{"schema": 1}`, []string{"/last_id"}},
		{`{"schema": 1, "last_id": 0, "x": 1, "a/b": 2}`, []string{"/a~1b", "/x"}},
		{`{"schema": 2, "last_id": -1}`, []string{"/last_id", "/schema"}},
	} {
		ok, f := Check(t, "root-file", []byte(tc.in))
		if got := f.Fields; ok != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) || f.Alternatives != nil {
			t.Errorf("%s: ok %v, locations %q; want %q", tc.in, ok, got, tc.want)
		}
	}
	// Every item equal to an earlier one, not just the first the library finds.
	for _, tc := range []struct {
		blockedBy, tags string
		want            []string
	}{
		{`[3, 4, 3]`, `["a", "a"]`, []string{"/blocked_by/2", "/tags/1"}},
		{`[3, 3, 3]`, `["a", "b", "a", "b"]`, []string{"/blocked_by/1", "/blocked_by/2", "/tags/2", "/tags/3"}},
		{`[3, 3.0]`, `[]`, []string{"/blocked_by/1"}}, // equal numbers, however written
	} {
		doc := `{"schema":1,"id":1,"title":"t","priority":null,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":` + tc.blockedBy + `,"tags":` + tc.tags + `,"extra":{}}`
		ok, f := Check(t, "task-file", []byte(doc))
		if got := f.Fields; ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("duplicates %s %s: ok %v, locations %q; want %q", tc.blockedBy, tc.tags, ok, got, tc.want)
		}
	}
}

// Each alternative of a failing anyOf or oneOf is kept apart; an adapter must
// match exactly one.
func TestAlternatives(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		matches [][]string
		misses  [][]string
	}{
		{`{"id": 1}`, `[] + one of [[""] [""] [""] [""]] at ""`,
			[][]string{{""}}, [][]string{{"/title"}, nil}},
		{`{"id": 1, "x": 1}`, `["/x"] + one of [[""] [""] [""] [""]] at ""`,
			[][]string{{"", "/x"}}, [][]string{{""}, {"/x"}}},
		{`{"id": 1, "tags": {"add": ["Urgent"]}}`, `[] + one of [["/tags" "/tags/add"] ["/tags/add/0"]] at "/tags"`,
			[][]string{{"/tags/add/0"}, {"/tags", "/tags/add"}}, [][]string{{"/tags"}, {"/tags/add"}, {"/tags/add/0", "/tags"}}},
		{`{"id": 1, "tags": {"replace_all": ["a"], "add": ["b"]}}`, `[] + one of [["/tags/add"] ["/tags/replace_all"]] at "/tags"`,
			[][]string{{"/tags/add"}}, [][]string{{"/tags"}}},
		{`{"id": 1, "tags": {}}`, `[] + one of [["/tags"] ["/tags"]] at "/tags"`,
			[][]string{{"/tags"}}, [][]string{{"/tags/add"}}},
	} {
		_, f := Check(t, "update-input", []byte(tc.in))
		if got := f.String(); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.in, got, tc.want)
		}
		for _, m := range tc.matches {
			if !f.Matches(m) {
				t.Errorf("%s: %q should match", tc.in, m)
			}
		}
		for _, m := range tc.misses {
			if f.Matches(m) {
				t.Errorf("%s: %q should not match", tc.in, m)
			}
		}
	}
}
