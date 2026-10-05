package schematest

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/jsonio"
)

// Candidates are the values Mutations puts in place of a field. They leave
// out integral numbers not written as integer literals (2.0), which the
// schemas accept and the adapters reject, and repeated keys, which the library
// cannot see. Every other rule beyond a schema — Additional validation, and a
// file's real-date, filename-ID, and own-ID rules — is marked by the adapter
// and left out of the comparison (model.Problems.SchemaList), so candidates
// may break it freely.
var Candidates = []string{
	`null`, `true`, `false`,
	`0`, `-0`, `1`, `-1`, `1.5`, `-0.5`, `1e400`,
	`999999999999999`, `1000000000000000`,
	`9007199254740991`, `9007199254740992`, `-9007199254740991`, `-9007199254740992`,
	`""`, `"x"`, `" x"`, `"x "`, `"\u00a0x"`, `"\u1680x"`, `"x\u200a"`, `"\u202fx"`, `"x\u205f"`, `"\u3000x"`,
	`"a\u2028b"`, `"a\u0085b"`, `"a\nb"`, `"a\tb"`, `"a\u007fb"`, `"a\u009fb"`, `"a\u200db"`,
	`"Travel"`, `"travel"`, `"a-"`, `"-a"`, `"a--b"`, `"` + strings.Repeat("a", 64) + `"`, `"` + strings.Repeat("a", 65) + `"`,
	`"/"`, `"/a"`, `"/a/b-c/d"`, `"/a/"`, `"//"`, `"/a//b"`, `"/A"`, `"/a-"`, `"a/b"`,
	`"/` + strings.Repeat("a", 64) + `"`, `"/` + strings.Repeat("a", 65) + `"`, `"/a/` + strings.Repeat("a", 65) + `"`,
	`"2026-09-20T18:31:51Z"`, `"2026-09-20T18:31:51"`, `"2026-09-20 18:31:51Z"`, `"2026-09-20T18:31:51z"`,
	`"2026-09-20T18:31:51.5Z"`, `"2026-09-20T18:31:51+00:00"`,
	`"` + strings.Repeat("é", 200) + `"`, `"` + strings.Repeat("é", 201) + `"`,
	`"` + strings.Repeat(`\ud83d\ude00`, 200) + `"`, `"` + strings.Repeat(`\ud83d\ude00`, 201) + `"`, // 400, 402 UTF-16 units
	`[]`, `[1]`, `[1, 1]`, `[1, 1, 1]`, `[0]`, `[1, "x"]`, `[1.5]`, `[null]`, `[[]]`,
	`["travel"]`, `["travel", "travel"]`, `["a", "b", "a", "b"]`, `["Travel"]`, `["Travel", "Travel"]`, `["a", ""]`,
	`["` + strings.Repeat("a", 64) + `"]`, `["` + strings.Repeat("a", 65) + `"]`,
	`{}`, `{"a": 1}`, `{"status": "x", "n": 2.0, "deep": {"k": [1e2]}}`, `{"extra": []}`,
}

// Mutations returns variants of base, a valid JSON object, each differing
// from it in one place: for every member of every object (nested ones
// included, but not objects inside arrays), the member removed, its value
// replaced by each candidate (Candidates, then extra), and, for an array
// value, its last item replaced by each candidate; plus each object with an
// unknown member added.
func Mutations(t testing.TB, base string, extra ...string) []string {
	t.Helper()
	root, _, err := jsonio.ParseObject([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	var values []any
	for _, c := range append(slices.Clone(Candidates), extra...) {
		v, _, err := jsonio.ParseValue([]byte(c))
		if err != nil {
			t.Fatalf("candidate %s: %v", c, err)
		}
		values = append(values, v)
	}
	var out []string
	emit := func(doc any) {
		b, err := jsonio.MarshalLine(doc)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.TrimSuffix(string(b), "\n"))
	}
	var visit func(obj *jsonio.Object, path []string)
	visit = func(obj *jsonio.Object, path []string) {
		for _, m := range obj.Members {
			at := append(slices.Clone(path), m.Key)
			emit(edit(root, at, nil, true))
			for _, v := range values {
				emit(edit(root, at, v, false))
				if arr, ok := m.Value.([]any); ok && len(arr) > 0 {
					items := slices.Clone(arr)
					items[len(items)-1] = v
					emit(edit(root, at, items, false))
				}
			}
			if child, ok := m.Value.(*jsonio.Object); ok {
				visit(child, at)
			}
		}
		emit(edit(root, append(slices.Clone(path), "unknown"), true, false))
	}
	visit(root, nil)
	emit(edit(root, []string{"Schema"}, json.Number("1"), false))
	return out
}

// edit returns a copy of obj with the member at path (a key per object
// level) set to v, or deleted; obj itself is unchanged, since Set and Delete
// never write to the members slice they share with it.
func edit(obj *jsonio.Object, path []string, v any, del bool) *jsonio.Object {
	cp := &jsonio.Object{Members: obj.Members}
	if len(path) == 1 {
		if del {
			cp.Delete(path[0])
		} else {
			cp.Set(path[0], v)
		}
		return cp
	}
	child, _ := obj.Get(path[0])
	cp.Set(path[0], edit(child.(*jsonio.Object), path[1:], v, del))
	return cp
}
