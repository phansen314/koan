package ops

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
)

// update runs update with input over f, checking the envelope and, on
// success, update-output. It returns the result in short — changed, then the
// four fields update owns — or the error as "kind details".
func (f *fixture) update(input string) string {
	f.t.Helper()
	e := Run("update", parse(f.t, input), nil, f.env)
	line(f.t, e)
	enc := func(v any) string {
		b, err := jsonio.MarshalLine(v)
		if err != nil {
			f.t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	if !e.OK {
		return f.rel(string(e.Error.Kind) + " " + enc(e.Error.Details))
	}
	b := enc(e.Result)
	if ok, fl := schematest.Check(f.t, "update-output", []byte(b)); !ok {
		f.t.Errorf("update-output rejects at %s: %s", fl, b)
	}
	r := e.Result.(UpdateOutput)
	return enc(map[string]any{"changed": r.Changed, "title": r.Title, "priority": r.Priority, "tags": r.Tags, "extra": r.Extra})
}

// The whole output and file after a change, byte for byte: only the changed
// fields differ, and new extra keys are appended in the order given.
func TestUpdate(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/proj/7.json", richTask(false))
	f.write("tasks/proj/7.md", "notes\n")
	e := Run("update", parse(t, `{"id": 7, "title": " Pack light ", "tags": {"add": ["packing"]}, "extra": {"merge": {"z": 1, "a": {"b": 2}}}}`), nil, f.env)
	got := f.rel(line(t, e))
	want := `{"ok":true,"result":{"schema":1,"id":7,"title":"Pack light","priority":-2,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-28T12:00:00Z","blocked_by":[1,3],"tags":["packing","travel"],"extra":{"n":1.50,"deep":{"k":[true,null]},"z":1,"a":{"b":2}},"folder":"/proj","notes_path":"~/tasks/proj/7.md","changed":["title","tags","extra"]},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	wantFile := strings.NewReplacer(
		`"title": "Pack bags"`, `"title": "Pack light"`,
		"\"tags\": [\n    \"travel\"\n  ]", "\"tags\": [\n    \"packing\",\n    \"travel\"\n  ]",
		"        null\n      ]\n    }\n  }", "        null\n      ]\n    },\n    \"z\": 1,\n    \"a\": {\n      \"b\": 2\n    }\n  }",
	).Replace(stamped(richTask(false)))
	if got := f.read("tasks/proj/7.json"); got != wantFile {
		t.Errorf("task file:\n%s\nwant\n%s", got, wantFile)
	}
	if got := f.read("tasks/proj/7.md"); got != "notes\n" {
		t.Errorf(".md %q", got)
	}
}

func TestUpdateCases(t *testing.T) {
	// The base task's fields, as the summary shows them after changed.
	const base = `"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":-2,"tags":["travel"],"title":"Pack bags"}`
	for _, tc := range []struct {
		name, input, want string
	}{
		// title.
		{"title", `{"id": 7, "title": "New"}`,
			`{"changed":["title"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":-2,"tags":["travel"],"title":"New"}`},
		{"same title, untrimmed", `{"id": 7, "title": "  Pack bags  "}`,
			`{"changed":[],` + base},

		// priority.
		{"priority", `{"id": 7, "priority": 3}`, `{"changed":["priority"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":3,"tags":["travel"],"title":"Pack bags"}`},
		{"priority cleared", `{"id": 7, "priority": null}`, `{"changed":["priority"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":null,"tags":["travel"],"title":"Pack bags"}`},
		{"same priority", `{"id": 7, "priority": -2}`, `{"changed":[],` + base},

		// tags.
		{"tags added", `{"id": 7, "tags": {"add": ["b", "a"]}}`, `{"changed":["tags"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":-2,"tags":["a","b","travel"],"title":"Pack bags"}`},
		{"tags removed", `{"id": 7, "tags": {"remove": ["travel"]}}`, `{"changed":["tags"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":-2,"tags":[],"title":"Pack bags"}`},
		{"tags added and removed", `{"id": 7, "tags": {"add": ["x"], "remove": ["travel"]}}`, `{"changed":["tags"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":-2,"tags":["x"],"title":"Pack bags"}`},
		{"adding a present tag", `{"id": 7, "tags": {"add": ["travel"]}}`, `{"changed":[],` + base},
		{"removing an absent tag", `{"id": 7, "tags": {"remove": ["nope"]}}`, `{"changed":[],` + base},
		{"tags replaced", `{"id": 7, "tags": {"replace_all": ["b", "a"]}}`, `{"changed":["tags"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":-2,"tags":["a","b"],"title":"Pack bags"}`},
		{"tags cleared", `{"id": 7, "tags": {"replace_all": []}}`, `{"changed":["tags"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":-2,"tags":[],"title":"Pack bags"}`},
		{"tags replaced by the same set", `{"id": 7, "tags": {"replace_all": ["travel"]}}`, `{"changed":[],` + base},

		// extra.
		{"key merged", `{"id": 7, "extra": {"merge": {"n": 2}}}`, `{"changed":["extra"],"extra":{"n":2,"deep":{"k":[true,null]}},"priority":-2,"tags":["travel"],"title":"Pack bags"}`},
		{"object replaced whole, not merged into", `{"id": 7, "extra": {"merge": {"deep": {"j": 1}}}}`, `{"changed":["extra"],"extra":{"n":1.50,"deep":{"j":1}},"priority":-2,"tags":["travel"],"title":"Pack bags"}`},
		{"null is a value", `{"id": 7, "extra": {"merge": {"n": null}}}`, `{"changed":["extra"],"extra":{"n":null,"deep":{"k":[true,null]}},"priority":-2,"tags":["travel"],"title":"Pack bags"}`},
		{"key removed", `{"id": 7, "extra": {"remove": ["n"]}}`, `{"changed":["extra"],"extra":{"deep":{"k":[true,null]}},"priority":-2,"tags":["travel"],"title":"Pack bags"}`},
		{"merge and remove", `{"id": 7, "extra": {"merge": {"m": "x"}, "remove": ["deep"]}}`, `{"changed":["extra"],"extra":{"n":1.50,"m":"x"},"priority":-2,"tags":["travel"],"title":"Pack bags"}`},
		{"removing an absent key", `{"id": 7, "extra": {"remove": ["nope"]}}`, `{"changed":[],` + base},
		{"merging an equal number", `{"id": 7, "extra": {"merge": {"n": 1.5}}}`, `{"changed":[],` + base},
		{"merging an equal object", `{"id": 7, "extra": {"merge": {"deep": {"k": [true, null]}}}}`, `{"changed":[],` + base},
		{"extra replaced", `{"id": 7, "extra": {"replace_all": {"q": 1}}}`, `{"changed":["extra"],"extra":{"q":1},"priority":-2,"tags":["travel"],"title":"Pack bags"}`},
		{"extra cleared", `{"id": 7, "extra": {"replace_all": {}}}`, `{"changed":["extra"],"extra":{},"priority":-2,"tags":["travel"],"title":"Pack bags"}`},
		{"extra replaced, reordered only", `{"id": 7, "extra": {"replace_all": {"deep": {"k": [true, null]}, "n": 1.500}}}`, `{"changed":[],` + base},

		// Several at once: changed in the order title, priority, tags, extra;
		// a field that compares equal keeps its old form.
		{"all four", `{"id": 7, "extra": {"merge": {"x": 1}}, "tags": {"add": ["y"]}, "priority": 1, "title": "T"}`,
			`{"changed":["title","priority","tags","extra"],"extra":{"n":1.50,"deep":{"k":[true,null]},"x":1},"priority":1,"tags":["travel","y"],"title":"T"}`},
		{"equal extra keeps its form", `{"id": 7, "title": "T", "extra": {"merge": {"n": 1.5}}}`,
			`{"changed":["title"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"priority":-2,"tags":["travel"],"title":"T"}`},

		// Finding the one task.
		{"not found", `{"id": 8, "title": "x"}`, `not-found {"folders":[],"ids":[8],"paths":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.write("tasks/7.json", richTask(false))
			if got := f.update(tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if strings.HasPrefix(tc.want, `{"changed":[],`) {
				if got := f.read("tasks/7.json"); got != richTask(false) {
					t.Errorf("task file rewritten:\n%s", got)
				}
			}
		})
	}
}

// A field left as it was keeps its exact form in a rewritten file.
func TestUpdateKeepsUnchangedForm(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/7.json", `{"schema":1,"id":7,"title":"t","priority":null,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":[],"tags":[],"extra":{"b":1e2,"a":1}}`)
	f.update(`{"id": 7, "title": "u", "extra": {"replace_all": {"a": 1.0, "b": 100}}}`)
	want := "{\n  \"schema\": 1,\n  \"id\": 7,\n  \"title\": \"u\",\n  \"priority\": null,\n  \"created_at\": \"2026-09-20T18:31:51Z\",\n  \"completed_at\": null,\n  \"updated_at\": \"2026-09-28T12:00:00Z\",\n  \"blocked_by\": [],\n  \"tags\": [],\n  \"extra\": {\n    \"b\": 1e2,\n    \"a\": 1\n  }\n}\n"
	if got := f.read("tasks/7.json"); got != want {
		t.Errorf("task file:\n%s\nwant\n%s", got, want)
	}
}

// Errors, in precedence order, and the write failing. intact: the task file
// at tasks/7.json is still the base task afterwards.
func TestUpdateErrors(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want        string
		intact      bool
	}{
		{"corrupt", `{"id": 7, "title": "x"}`, func(f *fixture) { f.write("tasks/7.json", "{") },
			`corrupt {"path":"~/tasks/7.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, false},
		{"duplicate", `{"id": 7, "title": "x"}`, func(f *fixture) { f.write("tasks/7.json", richTask(false)); f.task("p", 7, false) },
			`conflict {"rule":"duplicate-id","ids":[7]}`, true},
		{"duplicate with a corrupt copy", `{"id": 7, "title": "x"}`, func(f *fixture) { f.write("tasks/7.json", richTask(false)); f.write("tasks/p/7.json", "{") },
			`corrupt {"path":"~/tasks/p/7.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, true},
		{"unlistable folder", `{"id": 7, "title": "x"}`, func(f *fixture) {
			f.write("tasks/7.json", richTask(false))
			f.mkdir("tasks/p")
			f.fail(fsys.OpReadDir, "p", syscall.EACCES)
		}, `io {"path":"~/tasks/p","code":"EACCES"}`, true},
		{"replace fails", `{"id": 7, "title": "x"}`, func(f *fixture) {
			f.write("tasks/7.json", richTask(false))
			f.failAt(fsys.OpRename, "7.json", syscall.ENOSPC)
		},
			`io {"path":"~/tasks/7.json","code":"ENOSPC"}`, true},
		{"no field to change", `{"id": 7}`, func(f *fixture) { f.write("tasks/7.json", richTask(false)) },
			`invalid-input {"problems":[{"field":"","reason":"give at least one field to change: title, priority, tags, or extra"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			if got := f.update(tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if got := f.read("tasks/7.json"); tc.intact && got != richTask(false) {
				t.Errorf("task file changed:\n%s", got)
			}
		})
	}
}

// A no-op update doesn't touch the file: its modification time stays.
func TestUpdateNoOpNotWritten(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/7.json", richTask(false))
	p := filepath.Join(f.root, "7.json")
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	f.update(`{"id": 7, "title": "Pack bags", "tags": {"add": ["travel"]}, "extra": {"merge": {"n": 1.5}}}`)
	if fi, err := os.Stat(p); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("task file rewritten: %v %v", fi.ModTime(), err)
	}
}

// Another write holding the lock: busy, and nothing written.
func TestUpdateBusy(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/7.json", richTask(false))
	r, err := fsys.OS{}.OpenRoot(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, err := r.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if got := f.update(`{"id": 7, "title": "x"}`); got != "busy {}" {
		t.Errorf("got %s", got)
	}
	if got := f.read("tasks/7.json"); got != richTask(false) {
		t.Errorf("task file changed:\n%s", got)
	}
}
