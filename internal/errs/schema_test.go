package errs_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/schematest"
)

// A separate package, so errs itself still imports none of koan's packages.

func encode(t *testing.T, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Every error constructor's output is a valid error object (usage details,
// which error.json leaves open, against usage-details).
func TestErrorsMatchSchema(t *testing.T) {
	arg := ""
	for _, tc := range []struct {
		name string
		e    *errs.Error
	}{
		{"invalid-input", errs.InvalidInput([]errs.Problem{{Field: "/title", Reason: "required"}, {Field: "", Reason: "x"}})},
		{"invalid-input truncated", errs.InvalidInput(manyProblems(25))},
		{"environment", errs.Environment("HOME")},
		{"not-initialized", errs.NotInitialized(errs.MissingMetadata)},
		{"not-found", errs.NotFound([]string{"/a"}, []int64{3}, nil)},
		{"not-found empty", errs.NotFound(nil, nil, nil)},
		{"conflict", errs.Conflict(errs.RuleIDExhausted, nil)},
		{"conflict duplicate-id", errs.Conflict(errs.RuleDuplicateID, []int64{4})},
		{"acyclic", errs.Acyclic([]int64{3, 5}, [][]int64{{1, 3}, {1, 5, 2}})},
		{"busy", errs.Busy()},
		{"corrupt", errs.Corrupt("/r/5.json", errs.CorruptUnexpectedFile)},
		{"corrupt problems", errs.CorruptBy("/r/5.json", errs.CorruptCause{Reason: errs.CorruptInvalid, Problems: []errs.Problem{{Field: "/updated_at", Reason: "required"}}})},
		{"corrupt problems truncated", errs.CorruptBy("/r/5.json", errs.CorruptCause{Reason: errs.CorruptInvalid, Problems: manyProblems(25)})},
		{"corrupt not-json", errs.CorruptBy("/r/5.json", errs.CorruptCause{Reason: errs.CorruptNotJSON, Detail: "empty"})},
		{"corrupt config", errs.CorruptBy("/c/config.toml", errs.CorruptCause{Reason: errs.CorruptInvalid, Detail: "line 1: no closing quote"})},
		{"corrupt partial", errs.Corrupt("/r/5.json", errs.CorruptUnexpectedFile).WithPartial(map[string]int{"id": 5})},
		{"io", errs.IO("/r/5.json", "ENOSPC")},
		{"unsupported-format", errs.UnsupportedFormat("/r/koan.json", 2, []int64{1})},
		{"unsupported-format migration", errs.UnsupportedMigration("/r/koan.json", 3, 1)},
		{"migration-pending", errs.MigrationPending(0, 1)},
		{"internal", errs.Internal("bug")},
		{"usage", errs.Usage([]errs.UsageProblem{{Argument: &arg, Reason: "empty"}, {Reason: "missing command"}})},
	} {
		data := encode(t, tc.e)
		if ok, f := schematest.Check(t, "error", data); !ok {
			t.Errorf("%s: %s fails error: %s", tc.name, data, f)
		}
		if tc.e.Kind == errs.KindUsage {
			if ok, f := schematest.Check(t, "usage-details", encode(t, tc.e.Details)); !ok {
				t.Errorf("%s: details fail usage-details: %s", tc.name, f)
			}
		}
	}
}

func manyProblems(n int) []errs.Problem {
	ps := make([]errs.Problem, n)
	for i := range ps {
		ps[i] = errs.Problem{Field: fmt.Sprintf("/k%02d", i), Reason: "unknown field"}
	}
	return ps
}

// The corrupt schema rejects details that say too little or mix the shapes.
func TestCorruptSchemaRejects(t *testing.T) {
	const pr = `[{"field":"/x","reason":"r"}]`
	for _, details := range []string{
		`{"path":"/p","reason":"invalid"}`,
		`{"path":"/p","reason":"not-json"}`,
		`{"path":"/p","reason":"not-json","problems":` + pr + `,"detail":"d"}`,
		`{"path":"/p","reason":"invalid","problems":` + pr + `,"detail":"d"}`,
		`{"path":"/p","reason":"invalid","detail":"d","problems_truncated":true}`,
		`{"path":"/p","reason":"invalid","problems":` + pr + `,"problems_truncated":false}`,
		`{"path":"/p","reason":"invalid","problems":[]}`,
		`{"path":"/p","reason":"invalid","detail":""}`,
		`{"path":"/p","reason":"unexpected-file","detail":"d"}`,
		`{"path":"/p","reason":"unexpected-file","problems":` + pr + `}`,
	} {
		data := []byte(`{"kind":"corrupt","message":"m","details":` + details + `}`)
		if ok, _ := schematest.Check(t, "error", data); ok {
			t.Errorf("error accepts %s", details)
		}
	}
	ps := string(bytes.TrimSpace(encode(t, manyProblems(21))))
	for _, data := range []string{
		`{"kind":"corrupt","message":"m","details":{"path":"/p","reason":"invalid","problems":` + ps + `}}`,
		`{"kind":"invalid-input","message":"m","details":{"problems":` + ps + `}}`,
		`{"kind":"invalid-input","message":"m","details":{"problems":[{"field":"/x","reason":"r"}],"problems_truncated":false}}`,
	} {
		if ok, _ := schematest.Check(t, "error", []byte(data)); ok {
			t.Errorf("error accepts %s", data)
		}
	}
}

// Every warning constructor's output is a valid warning.
func TestWarningsMatchSchema(t *testing.T) {
	for _, w := range []errs.Warning{
		errs.UnreadableFile("/r/1.json", 1, "EACCES"),
		errs.CorruptFile("/r/1.json", 1),
		errs.UnsupportedFile("/r/1.json", 1),
		errs.DuplicateID(4, []string{"/r/a/4.json", "/r/b/4.json"}),
		errs.DuplicateID(4, nil),
		errs.DanglingReference("/r/4.json", 4, 9),
		errs.UnreadableFolder("/r/p", "EACCES"),
		errs.NotesMissing("/r/5.md", 5, "ENOSPC"),
		errs.Migrated("/c/ftask/config.toml", "/c/koan/config.toml"),
	} {
		data := encode(t, w)
		if ok, f := schematest.Check(t, "warning", data); !ok {
			t.Errorf("%s fails warning: %s", data, f)
		}
	}
}
