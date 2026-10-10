package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
)

// createBatch runs create-batch with input over f, checking the envelope,
// and create-batch-output on success or create-batch-partial when there is
// a partial.
func (f *fixture) createBatch(input string) Envelope {
	f.t.Helper()
	e := Run("create-batch", parse(f.t, input), nil, f.env)
	line(f.t, e)
	check := func(schema string, v any) {
		b, err := jsonio.MarshalLine(v)
		if err != nil {
			f.t.Fatal(err)
		}
		if ok, fl := schematest.Check(f.t, schema, b); !ok {
			f.t.Errorf("%s rejects at %s: %s", schema, fl, b)
		}
	}
	switch {
	case e.OK:
		check("create-batch-output", e.Result)
	case e.Error.Partial != nil:
		check("create-batch-partial", e.Error.Partial)
	}
	return e
}

// batchSummary is e in short: the result as JSON, or the error as "kind
// details", with " partial <json>" when there is one; then each warning as
// "kind ids paths [code]"; the home as "~".
func (f *fixture) batchSummary(e Envelope) string {
	f.t.Helper()
	enc := func(v any) string {
		b, err := jsonio.MarshalLine(v)
		if err != nil {
			f.t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	var parts []string
	if e.OK {
		parts = append(parts, enc(e.Result))
	} else {
		s := string(e.Error.Kind) + " " + enc(e.Error.Details)
		if e.Error.Partial != nil {
			s += " partial " + enc(e.Error.Partial)
		}
		parts = append(parts, s)
	}
	for _, w := range e.Warnings {
		parts = append(parts, strings.TrimSpace(fmt.Sprintf("%s %v %v %s", w.Kind, w.IDs, w.Paths, w.Code)))
	}
	return f.rel(strings.Join(parts, "; "))
}

// The whole output and every file written, byte for byte: refs resolved to
// the IDs allocated in input order, existing blockers kept, missing folders
// created, one timestamp for all.
func TestCreateBatch(t *testing.T) {
	f := newFixture(t)
	f.task("", 7, false)
	f.mkdir("tasks/work")
	got := f.batchSummary(f.createBatch(`{"folder": "/work/api", "tasks": [
		{"ref": "schema", "title": " Design schema ", "priority": 2, "tags": ["db"], "notes": "draft\n"},
		{"ref": "migrate", "title": "Write migrations", "blocked_by": ["schema"]},
		{"title": "Deploy", "folder": "/ops/prod", "blocked_by": [7, "migrate", "schema"], "extra": {"n": 1.50}}
	]}`))
	want := `{"ids":[101,102,103],"refs":{"schema":101,"migrate":102},"folders_created":["/ops","/ops/prod","/work/api"]}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	task := func(id int, title, priority, blockedBy, tags, extra string) string {
		return fmt.Sprintf("{\n  \"schema\": 1,\n  \"id\": %d,\n  \"title\": %q,\n  \"priority\": %s,\n  \"created_at\": \"2026-09-28T12:00:00Z\",\n  \"completed_at\": null,\n  \"updated_at\": \"2026-09-28T12:00:00Z\",\n  \"blocked_by\": %s,\n  \"tags\": %s,\n  \"extra\": %s\n}\n",
			id, title, priority, blockedBy, tags, extra)
	}
	for rel, want := range map[string]string{
		"tasks/work/api/101.json": task(101, "Design schema", "2", "[]", "[\n    \"db\"\n  ]", "{}"),
		"tasks/work/api/101.md":   "draft\n",
		"tasks/work/api/102.json": task(102, "Write migrations", "null", "[\n    101\n  ]", "[]", "{}"),
		"tasks/work/api/102.md":   "",
		"tasks/ops/prod/103.json": task(103, "Deploy", "null", "[\n    7,\n    101,\n    102\n  ]", "[]", "{\n    \"n\": 1.50\n  }"),
		"cfg/state.json":          st(103),
	} {
		if got := f.read(rel); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", rel, got, want)
		}
	}
}

func TestCreateBatchCases(t *testing.T) {
	fresh := st(100)
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		input string
		want  string
		files map[string]string // under the home, afterwards; "<none>" for absent, "" for present
	}{
		// Defaults, and folders.
		{"defaults", nil, `{"tasks": [{"title": "x"}]}`,
			`{"ids":[101],"refs":{},"folders_created":[]}`, map[string]string{"tasks/101.json": "", "tasks/101.md": ""}},
		{"batch folder exists", func(f *fixture) { f.mkdir("tasks/a") }, `{"folder": "/a", "tasks": [{"title": "x"}, {"title": "y"}]}`,
			`{"ids":[101,102],"refs":{},"folders_created":[]}`, map[string]string{"tasks/a/101.json": "", "tasks/a/102.json": ""}},
		{"shared missing parent created once", nil, `{"tasks": [{"title": "x", "folder": "/a/c"}, {"title": "y", "folder": "/a/b"}, {"title": "z", "folder": "/a"}]}`,
			`{"ids":[101,102,103],"refs":{},"folders_created":["/a","/a/b","/a/c"]}`, map[string]string{"tasks/a/c/101.json": "", "tasks/a/b/102.json": "", "tasks/a/103.json": ""}},
		{"new folders differing only in case", nil, `{"tasks": [{"title": "x", "folder": "/s/JIRA-1/a"}, {"title": "y", "folder": "/s/jira-1/b"}]}`,
			`conflict {"rule":"case-clash","ids":[]}`, map[string]string{"cfg/state.json": fresh, "tasks/s": "<none>"}},
		{"a new folder differing only in case from one there", func(f *fixture) { f.mkdir("tasks/s/JIRA-1") }, `{"tasks": [{"title": "x", "folder": "/s/jira-1/b"}]}`,
			`conflict {"rule":"case-clash","ids":[]}`, map[string]string{"cfg/state.json": fresh, "tasks/s/jira-1": "<none>"}},
		{"folder a file", func(f *fixture) { f.write("tasks/a", "") }, `{"tasks": [{"title": "x", "folder": "/a/b"}]}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, map[string]string{"cfg/state.json": fresh}},
		{"first bad folder in tree order", func(f *fixture) { f.write("tasks/b", ""); f.write("tasks/a/c", "") },
			`{"tasks": [{"title": "x", "folder": "/b"}, {"title": "y", "folder": "/a/c"}, {"title": "z", "folder": "/n"}]}`,
			`corrupt {"path":"~/tasks/a/c","reason":"unexpected-file"}`, map[string]string{"tasks/n": "<none>"}},
		{"folder a symlink", func(f *fixture) {
			f.mkdir("real")
			if err := os.Symlink(filepath.Join(f.home, "real"), filepath.Join(f.root, "a")); err != nil {
				f.t.Fatal(err)
			}
		}, `{"tasks": [{"title": "x", "folder": "/a"}]}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, map[string]string{"real/101.json": "<none>"}},

		// Existing blockers.
		{"missing blockers across tasks", func(f *fixture) { f.task("", 1, false) }, `{"tasks": [{"title": "x", "folder": "/n", "blocked_by": [9, 1]}, {"title": "y", "blocked_by": [5, 9]}]}`,
			`not-found {"folders":[],"ids":[5,9],"paths":[]}`, map[string]string{"cfg/state.json": fresh, "tasks/n": "<none>"}},
		{"corrupt folder before missing blockers", func(f *fixture) { f.write("tasks/a", "") }, `{"tasks": [{"title": "x", "folder": "/a", "blocked_by": [9]}]}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, nil},
		{"corrupt blocker", func(f *fixture) { f.write("tasks/1.json", "{") }, `{"tasks": [{"title": "x", "blocked_by": [1]}]}`,
			`corrupt {"path":"~/tasks/1.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, nil},
		{"duplicated blocker warned once", func(f *fixture) { f.task("", 1, false); f.task("p", 1, true) }, `{"tasks": [{"title": "x", "blocked_by": [1]}, {"title": "y", "blocked_by": [1]}]}`,
			`{"ids":[101,102],"refs":{},"folders_created":[]}; duplicate-id [1] [~/tasks/1.json ~/tasks/p/1.json]`, nil},
		{"unlistable folder with blockers", func(f *fixture) {
			f.task("", 1, false)
			f.mkdir("tasks/p")
			f.fail(fsys.OpReadDir, "p", syscall.EACCES)
		}, `{"tasks": [{"title": "x", "blocked_by": [1]}]}`,
			`io {"path":"~/tasks/p","code":"EACCES"}`, nil},
		{"unlistable folder, refs only: no walk", func(f *fixture) { f.mkdir("tasks/p"); f.fail(fsys.OpReadDir, "p", syscall.EACCES) },
			`{"tasks": [{"ref": "a", "title": "x"}, {"title": "y", "blocked_by": ["a"]}]}`,
			`{"ids":[101,102],"refs":{"a":101},"folders_created":[]}`, nil},

		// The IDs.
		{"blockers above last_id", func(f *fixture) { f.task("x", 101, false); f.task("", 1, false) },
			`{"tasks": [{"title": "x", "folder": "/n", "blocked_by": [1]}, {"title": "y", "blocked_by": [101]}]}`,
			`conflict {"rule":"id-above-last-id","ids":[101]}`, map[string]string{"cfg/state.json": fresh, "tasks/101.json": "<none>", "tasks/n": "<none>"}},
		{"batch past the ceiling", func(f *fixture) {
			f.setLastID(999999999999998)
		},
			`{"tasks": [{"title": "x"}, {"title": "y"}]}`,
			`conflict {"rule":"id-exhausted","ids":[]}`, nil},
		{"batch up to the ceiling", func(f *fixture) {
			f.setLastID(999999999999997)
		},
			`{"tasks": [{"title": "x"}, {"title": "y"}]}`,
			`{"ids":[999999999999998,999999999999999],"refs":{},"folders_created":[]}`, nil},
		{"ceiling checked before folders are made", func(f *fixture) {
			f.setLastID(999999999999999)
		},
			`{"tasks": [{"title": "x", "folder": "/n"}]}`,
			`conflict {"rule":"id-exhausted","ids":[]}`, map[string]string{"tasks/n": "<none>"}},
		{"task file already there", func(f *fixture) { f.task("", 102, false) },
			`{"tasks": [{"ref": "a", "title": "x"}, {"title": "y"}, {"title": "z"}]}`,
			`corrupt {"path":"~/tasks/102.json","reason":"unexpected-file"} partial {"folders_created":[],"consumed":[101,102,103],"ids":[101],"refs":{"a":101}}`,
			map[string]string{"cfg/state.json": st(103), "tasks/101.json": "", "tasks/103.json": "<none>"}},

		// Root states come first.
		{"no config", func(f *fixture) { f.remove("cfg") }, `{"tasks": [{"title": "x"}]}`,
			`not-initialized {"missing":"config"}`, nil},

		// Failures midway.
		{"folder not made", func(f *fixture) { f.failAt(fsys.OpMkdir, "b", syscall.EACCES) },
			`{"tasks": [{"title": "x", "folder": "/a"}, {"title": "y", "folder": "/b"}]}`,
			`io {"path":"~/tasks/b","code":"EACCES"} partial {"folders_created":["/a"],"consumed":[],"ids":[],"refs":{}}`,
			map[string]string{"cfg/state.json": fresh}},
		{"first folder not made", func(f *fixture) { f.failAt(fsys.OpMkdir, "a", syscall.EACCES) }, `{"tasks": [{"title": "x", "folder": "/a"}]}`,
			`io {"path":"~/tasks/a","code":"EACCES"}`, nil},
		{"state file not written after folders", func(f *fixture) { f.failAt(fsys.OpRename, "state.json", syscall.ENOSPC) }, `{"tasks": [{"title": "x", "folder": "/a"}]}`,
			`io {"path":"~/cfg/state.json","code":"ENOSPC"} partial {"folders_created":["/a"],"consumed":[],"ids":[],"refs":{}}`,
			map[string]string{"cfg/state.json": fresh}},
		{"state file not written", func(f *fixture) { f.failAt(fsys.OpRename, "state.json", syscall.ENOSPC) }, `{"tasks": [{"title": "x"}]}`,
			`io {"path":"~/cfg/state.json","code":"ENOSPC"}`, nil},
		{"second task not written", func(f *fixture) { f.failAt(fsys.OpLink, "102.json", syscall.ENOSPC) },
			`{"tasks": [{"ref": "a", "title": "x"}, {"ref": "b", "title": "y", "blocked_by": ["a"]}, {"title": "z", "blocked_by": ["b"]}]}`,
			`io {"path":"~/tasks/102.json","code":"ENOSPC"} partial {"folders_created":[],"consumed":[101,102,103],"ids":[101],"refs":{"a":101}}`,
			map[string]string{"tasks/101.md": "", "tasks/102.json": "<none>", "tasks/103.json": "<none>"}},
		{".md not written", func(f *fixture) { f.failAt(fsys.OpRename, "101.md", syscall.ENOSPC) }, `{"tasks": [{"title": "x"}, {"title": "y"}]}`,
			`{"ids":[101,102],"refs":{},"folders_created":[]}; notes-missing [101] [~/tasks/101.md] ENOSPC`,
			map[string]string{"tasks/101.md": "<none>", "tasks/102.md": ""}},
		{".md not written, no errno name", func(f *fixture) { f.failAt(fsys.OpRename, "102.md", syscall.Errno(4000)) },
			`{"tasks": [{"title": "x"}, {"title": "y", "notes": "n"}]}`,
			`{"ids":[101,102],"refs":{},"folders_created":[]}; notes-missing [102] [~/tasks/102.md]`,
			map[string]string{"tasks/101.md": "", "tasks/102.md": "<none>"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.batchSummary(f.createBatch(tc.input)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			for rel, want := range tc.files {
				got := f.read(rel)
				if want == "" && got != "<none>" {
					continue // only that it exists
				}
				if got != want {
					t.Errorf("%s: %q, want %q", rel, got, want)
				}
			}
		})
	}
}

// At most 1000 tasks; every problem in every task is reported, before
// anything is read.
func TestCreateBatchLimits(t *testing.T) {
	f := newFixture(t)
	tasks := strings.Repeat(`{"title": "x"},`, 1000)
	if got := f.batchSummary(f.createBatch(`{"tasks": [` + tasks[:len(tasks)-1] + `]}`)); !strings.HasPrefix(got, `{"ids":[101,`) {
		t.Errorf("1000 tasks: %.80s", got)
	}
	f = newFixture(t)
	if got := f.batchSummary(f.createBatch(`{"tasks": [` + tasks + `{"title": "x"}]}`)); got != `invalid-input {"problems":[{"field":"/tasks","reason":"must list at most 1000 tasks"}]}` {
		t.Errorf("1001 tasks: %s", got)
	}
	if got := f.read(stateRel); got != st(100) {
		t.Errorf("koan.json %q", got)
	}
}
