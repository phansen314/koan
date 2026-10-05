package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
)

// The design spec's example task file, in File format.
const exampleTask = `{
  "schema": 1,
  "id": 42,
  "title": "Book flights",
  "priority": 2,
  "created_at": "2026-09-20T18:31:51Z",
  "completed_at": null,
  "updated_at": "2026-09-20T18:31:51Z",
  "blocked_by": [],
  "tags": [
    "travel"
  ],
  "extra": {
    "status": "waiting on quote"
  }
}
`

// problemStrings renders problems as "field: reason", for matching.
func problemStrings(ps []errs.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Field+": "+p.Reason)
	}
	return out
}

func decodeTask(t *testing.T, s string, filenameID ID) (TaskFile, FileResult) {
	t.Helper()
	obj, repeated, err := jsonio.ParseObject([]byte(s))
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return DecodeTaskFile(obj, repeated, filenameID)
}

func TestTaskFileRoundTrip(t *testing.T) {
	tf, r := decodeTask(t, exampleTask, 42)
	if r.Status != FileOK {
		t.Fatalf("status %v: %v", r.Status, problemStrings(r.Problems))
	}
	got, err := tf.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != exampleTask {
		t.Errorf("got:\n%s\nwant:\n%s", got, exampleTask)
	}
}

func TestTaskFileNormalized(t *testing.T) {
	in := strings.NewReplacer(`"blocked_by": []`, `"blocked_by": [9, 3, 41]`, `"travel"`, `"travel", "b-side", "a"`).Replace(exampleTask)
	tf, r := decodeTask(t, in, 42)
	if r.Status != FileOK {
		t.Fatalf("status %v: %v", r.Status, problemStrings(r.Problems))
	}
	got, _ := jsonio.MarshalLine(struct {
		B []ID  `json:"b"`
		T []Tag `json:"t"`
	}{tf.BlockedBy, tf.Tags})
	if want := `{"b":[3,9,41],"t":["a","b-side","travel"]}` + "\n"; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestTaskFileValidity(t *testing.T) {
	edit := func(old, new string) string {
		if !strings.Contains(exampleTask, old) {
			panic(old)
		}
		return strings.Replace(exampleTask, old, new, 1)
	}
	for _, tc := range []struct {
		name    string
		in      string
		status  FileStatus
		found   int64  // the schema version, when step 1 passed
		problem string // a prefix of one problem, for corrupt
	}{
		{"valid", exampleTask, FileOK, 1, ""},
		{"complete", edit(`"completed_at": null`, `"completed_at": "2026-09-21T08:00:00Z"`), FileOK, 1, ""},
		{"no priority", edit(`"priority": 2`, `"priority": null`), FileOK, 1, ""},
		{"schema missing", edit(`"schema": 1,`, ``), FileCorrupt, 0, "/schema: required"},
		{"schema 1.0", edit(`"schema": 1`, `"schema": 1.0`), FileCorrupt, 0, "/schema: must be an integer"},
		{"schema string", edit(`"schema": 1`, `"schema": "1"`), FileCorrupt, 0, "/schema: expected an integer"},
		{"schema repeated", edit(`"schema": 1,`, `"schema": 1, "schema": 1,`), FileCorrupt, 0, "/schema: repeated"},
		{"schema huge", edit(`"schema": 1`, `"schema": 99999999999999999999`), FileCorrupt, 0, "/schema: must be between"},
		{"schema past 2^53", edit(`"schema": 1`, `"schema": 9007199254740992`), FileCorrupt, 0, "/schema: must be between"},
		{"schema at -(2^53-1)", edit(`"schema": 1`, `"schema": -9007199254740991`), FileUnsupported, -9007199254740991, ""},
		{"schema 2", edit(`"schema": 1`, `"schema": 2`), FileUnsupported, 2, ""},
		{"schema 0", edit(`"schema": 1`, `"schema": 0`), FileUnsupported, 0, ""},
		{"unsupported beats corrupt", edit(`"schema": 1,`, `"schema": 2, "title": "x", "bogus": 1.5,`), FileUnsupported, 2, ""},
		{"repeated key", edit(`"title": "Book flights",`, `"title": "Book flights", "title": "x",`), FileCorrupt, 1, "/title: repeated key"},
		{"repeated key in extra", edit(`"status": "waiting on quote"`, `"status": 1, "status": 2`), FileCorrupt, 1, "/extra/status: repeated key"},
		{"id mismatch", exampleTask, FileCorrupt, 1, "/id: must match the ID in the filename (43)"},
		{"id 0", edit(`"id": 42`, `"id": 0`), FileCorrupt, 1, "/id: must be between"},
		{"own ID blocks", edit(`"blocked_by": []`, `"blocked_by": [7, 42]`), FileCorrupt, 1, "/blocked_by/1: must not be the task's own ID"},
		{"blocked_by duplicate", edit(`"blocked_by": []`, `"blocked_by": [7, 7]`), FileCorrupt, 1, "/blocked_by/1: duplicate"},
		{"blocked_by 2.0", edit(`"blocked_by": []`, `"blocked_by": [2.0]`), FileCorrupt, 1, "/blocked_by/0: must be an integer"},
		{"unknown field", edit(`"schema": 1,`, `"schema": 1, "status": "x",`), FileCorrupt, 1, "/status: unknown field"},
		{"missing field", edit(`"priority": 2,`, ``), FileCorrupt, 1, "/priority: required"},
		{"untrimmed title", edit(`"Book flights"`, `"Book flights "`), FileCorrupt, 1, "/title: must not start or end"},
		{"not a real date", edit(`2026-09-20T18:31:51Z`, `2026-02-30T18:31:51Z`), FileCorrupt, 1, "/created_at: is not a real date"},
		{"completed_at number", edit(`"completed_at": null`, `"completed_at": 1`), FileCorrupt, 1, "/completed_at: expected a timestamp"},
		{"priority 2.5", edit(`"priority": 2`, `"priority": 2.5`), FileCorrupt, 1, "/priority: must be an integer"},
		{"priority too big", edit(`"priority": 2`, `"priority": 9007199254740992`), FileCorrupt, 1, "/priority: must be between"},
		{"extra array", edit(`{
    "status": "waiting on quote"
  }`, `[]`), FileCorrupt, 1, "/extra: expected a JSON object"},
		{"bad tag", edit(`"travel"`, `"Travel"`), FileCorrupt, 1, "/tags/0: must be 1-64"},
		{"extra is not validated", edit(`"status": "waiting on quote"`, `"n": 1.5e3, "deep": {"x": [null, 2.0]}`), FileOK, 1, ""},
	} {
		filenameID := ID(42)
		if tc.name == "id mismatch" {
			filenameID = 43
		}
		tf, r := decodeTask(t, tc.in, filenameID)
		versioned := !strings.HasPrefix(tc.problem, "/schema:")
		if r.Versioned != versioned {
			t.Errorf("%s: Versioned %v, want %v", tc.name, r.Versioned, versioned)
		}
		if r.Status != tc.status || r.Found != tc.found {
			t.Errorf("%s: status %v found %d, want %v %d (%v)", tc.name, r.Status, r.Found, tc.status, tc.found, problemStrings(r.Problems))
			continue
		}
		if r.Status != FileOK && tf.Title != "" {
			t.Errorf("%s: content returned with status %v", tc.name, r.Status)
		}
		if tc.problem == "" {
			continue
		}
		found := false
		for _, pr := range problemStrings(r.Problems) {
			found = found || strings.HasPrefix(pr, tc.problem)
		}
		if !found {
			t.Errorf("%s: no problem starting %q in %q", tc.name, tc.problem, problemStrings(r.Problems))
		}
	}
}

// Exact problem lists: nothing extra, and no field reported twice.
func TestTaskFileExactProblems(t *testing.T) {
	for _, tc := range []struct {
		name       string
		in         string
		filenameID ID
		want       []string
	}{
		{"several missing fields",
			`{"schema": 1, "id": 42, "title": "Book flights", "created_at": "2026-09-20T18:31:51Z", "completed_at": null,"updated_at": "2026-09-20T18:31:51Z", "blocked_by": []}`, 42,
			[]string{"/priority: required", "/tags: required", "/extra: required"}},
		{"unknown and repeated keys",
			strings.Replace(exampleTask, `"title": "Book flights",`, `"title": "Book flights", "title": "x", "status": "a", "status": "b",`, 1), 42,
			[]string{"/title: repeated key", "/status: repeated key", "/status: unknown field"}},
		{"ID mismatch and own ID both reported",
			strings.Replace(exampleTask, `"blocked_by": []`, `"blocked_by": [42]`, 1), 43,
			[]string{"/id: must match the ID in the filename (43)", "/blocked_by/0: must not be the task's own ID"}},
		{"bad id hides the own-ID check",
			strings.Replace(strings.Replace(exampleTask, `"blocked_by": []`, `"blocked_by": [42]`, 1), `"id": 42`, `"id": 0`, 1), 42,
			[]string{"/id: must be between 1 and 999999999999999"}},
		{"bad item and duplicates",
			strings.Replace(exampleTask, `"blocked_by": []`, `"blocked_by": [3, 1, 3, "x", 1]`, 1), 42,
			[]string{"/blocked_by/2: duplicate of item 0", "/blocked_by/3: expected an integer", "/blocked_by/4: duplicate of item 1"}},
		{"own ID reported with a duplicate",
			strings.Replace(exampleTask, `"blocked_by": []`, `"blocked_by": [42, 42]`, 1), 42,
			[]string{"/blocked_by/0: must not be the task's own ID", "/blocked_by/1: duplicate of item 0"}},
		{"own ID reported with a bad item",
			strings.Replace(exampleTask, `"blocked_by": []`, `"blocked_by": ["x", 42]`, 1), 42,
			[]string{"/blocked_by/0: expected an integer", "/blocked_by/1: must not be the task's own ID"}},
	} {
		_, r := decodeTask(t, tc.in, tc.filenameID)
		got := slices.Clone(problemStrings(r.Problems))
		slices.Sort(got)
		want := slices.Clone(tc.want)
		slices.Sort(want)
		if r.Status != FileCorrupt || !slices.Equal(got, want) {
			t.Errorf("%s: status %v, problems %q; want %q", tc.name, r.Status, problemStrings(r.Problems), tc.want)
		}
	}
}

func TestTaskViewNormalize(t *testing.T) {
	v := TaskView{Blocking: []ID{9, 3}}
	v.Normalize()
	got, err := jsonio.MarshalLine(struct {
		B, BB []ID
		T     []Tag
		E     *jsonio.Object
	}{v.Blocking, v.BlockedBy, v.Tags, v.Extra})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"B":[3,9],"BB":[],"T":[],"E":{}}` + "\n"; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
	v = TaskView{}
	v.Normalize()
	if v.Blocking == nil {
		t.Error("nil Blocking left nil")
	}
}

func TestRootFile(t *testing.T) {
	for _, tc := range []struct {
		in     string
		status FileStatus
		lastID int64
	}{
		{`{"schema": 1, "last_id": 0}`, FileOK, 0},
		{`{"schema": 1, "last_id": 999999999999999}`, FileOK, IDMax},
		{`{"schema": 1, "last_id": 1000000000000000}`, FileCorrupt, 0},
		{`{"schema": 1, "last_id": -1}`, FileCorrupt, 0},
		{`{"schema": 1, "last_id": 3.0}`, FileCorrupt, 0},
		{`{"schema": 1}`, FileCorrupt, 0},
		{`{"schema": 1, "last_id": 3, "x": 1}`, FileCorrupt, 0},
		{`{"schema": 1, "last_id": 3, "last_id": 3}`, FileCorrupt, 0},
		{`{"schema": 7, "last_id": "x"}`, FileUnsupported, 0},
		{`{"last_id": 3}`, FileCorrupt, 0},
	} {
		obj, repeated, err := jsonio.ParseObject([]byte(tc.in))
		if err != nil {
			t.Fatal(err)
		}
		rf, r := DecodeRootFile(obj, repeated)
		if r.Status != tc.status || rf.LastID != tc.lastID {
			t.Errorf("%s: status %v last_id %d, want %v %d (%v)", tc.in, r.Status, rf.LastID, tc.status, tc.lastID, problemStrings(r.Problems))
		}
	}
	got, _ := RootFile{Schema: 1, LastID: 7}.Encode()
	if want := "{\n  \"schema\": 1,\n  \"last_id\": 7\n}\n"; string(got) != want {
		t.Errorf("Encode = %q", got)
	}
}

// Embedding puts added keys after the task's, in schema order.
func TestTaskViewKeyOrder(t *testing.T) {
	tf, _ := decodeTask(t, exampleTask, 42)
	v := TaskView{Task: Task{TaskFile: tf, Folder: "/proj", NotesPath: "/r/proj/42.md"}, Readiness: Ready, Blocking: []ID{}}
	got, err := jsonio.MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":1,"id":42,"title":"Book flights","priority":2,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":[],"tags":["travel"],"extra":{"status":"waiting on quote"},"folder":"/proj","notes_path":"/r/proj/42.md","readiness":"ready","blocking":[]}` + "\n"
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// A projection of every field is the whole view, byte for byte; a smaller
// one keeps the view's order and always id.
func TestTaskViewProject(t *testing.T) {
	tf, _ := decodeTask(t, exampleTask, 42)
	v := TaskView{Task: Task{TaskFile: tf, Folder: "/proj", NotesPath: "/r/proj/42.md"}, Readiness: Ready, Blocking: []ID{}}
	whole, err := jsonio.MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		fields []string
		want   string
	}{
		{ViewFields, strings.TrimSuffix(string(whole), "\n")},
		{[]string{"folder", "title"}, `{"id":42,"title":"Book flights","folder":"/proj"}`},
		{[]string{"id"}, `{"id":42}`},
		{[]string{"blocking", "extra", "priority"}, `{"id":42,"priority":2,"extra":{"status":"waiting on quote"},"blocking":[]}`},
	} {
		got, err := jsonio.MarshalLine(v.Project(tc.fields))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want+"\n" {
			t.Errorf("%v:\ngot  %s\nwant %s", tc.fields, got, tc.want)
		}
	}
}

// ViewFields are the task-field schema's names, in task-view's order.
func TestViewFieldsAgreeWithSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(schematest.Dir(), "task-field.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct{ Enum []string }
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Enum, ViewFields) {
		t.Errorf("task-field lists %v, ViewFields %v", s.Enum, ViewFields)
	}
}
