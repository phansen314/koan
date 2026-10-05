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

// list runs list with input over f twice, checking that both runs give the
// same bytes (a read is deterministic), the envelope, and on success
// list-output and that truncated means total is more than the tasks
// returned. It returns the result in short — "folders […]" when present,
// then each task as "folder:id readiness blocking", then " of total" if the
// limit cut them — or the error as "kind details", then each warning as
// "kind ids paths"; the home as "~".
func (f *fixture) list(input string) string {
	f.t.Helper()
	e := Run("list", parse(f.t, input), nil, f.env)
	first := line(f.t, e)
	if again := line(f.t, Run("list", parse(f.t, input), nil, f.env)); again != first {
		f.t.Errorf("second run differs:\n%s\n%s", first, again)
	}
	var parts []string
	if e.OK {
		b, err := jsonio.MarshalLine(e.Result)
		if err != nil {
			f.t.Fatal(err)
		}
		if ok, fl := schematest.Check(f.t, "list-output", b); !ok {
			f.t.Errorf("list-output rejects at %s: %s", fl, b)
		}
		r := e.Result.(ListOutput)
		if r.Folders != nil {
			parts = append(parts, fmt.Sprintf("folders %v", *r.Folders))
		}
		var tasks []string
		for _, v := range r.Tasks.Views {
			tasks = append(tasks, fmt.Sprintf("%s:%d %s %v", v.Folder, v.ID, v.Readiness, v.Blocking))
		}
		s := "tasks [" + strings.Join(tasks, ", ") + "]"
		if r.Truncated != (r.Total > len(r.Tasks.Views)) || r.Total < len(r.Tasks.Views) {
			f.t.Errorf("total %d, truncated %v, for %d tasks", r.Total, r.Truncated, len(r.Tasks.Views))
		}
		if r.Truncated {
			s += fmt.Sprintf(" of %d", r.Total)
		}
		parts = append(parts, s)
	} else {
		d, err := jsonio.MarshalLine(e.Error.Details)
		if err != nil {
			f.t.Fatal(err)
		}
		parts = append(parts, string(e.Error.Kind)+" "+strings.TrimSpace(string(d)))
	}
	for _, w := range e.Warnings {
		parts = append(parts, fmt.Sprintf("%s %v %v", w.Kind, w.IDs, w.Paths))
	}
	return f.rel(strings.Join(parts, "; "))
}

// The whole output for one task, byte for byte: a task view, as show gives.
func TestList(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, false)
	got := f.rel(line(t, Run("list", parse(t, `{}`), nil, f.env)))
	want := `{"ok":true,"result":{"tasks":[{"schema":1,"id":7,"title":"task 7","priority":null,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":[],"tags":[],"extra":{},"folder":"/proj","notes_path":"~/tasks/proj/7.md","readiness":"ready","blocking":[]}],"total":1,"truncated":false},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Projected, byte for byte: only the fields asked for, and id, in the task
// view's order.
func TestListFields(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, false, 8)
	f.task("proj", 8, false)
	got := f.rel(line(t, Run("list", parse(t, `{"fields": ["blocking", "title"], "limit": 1}`), nil, f.env)))
	want := `{"ok":true,"result":{"tasks":[{"id":7,"title":"task 7","blocking":[8]}],"total":2,"truncated":true},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestListCases(t *testing.T) {
	// tree: / has 1 (open, blocked by 2) and 2 (complete); /proj has 3
	// (open, blocked by 4); /proj/travel has 4 (open); /proj-b has 5
	// (complete); /empty and /proj/travel/far are empty.
	tree := func(f *fixture) {
		f.task("", 1, false, 2)
		f.task("", 2, true)
		f.task("proj", 3, false, 4)
		f.task("proj/travel", 4, false)
		f.task("proj-b", 5, true)
		f.mkdir("tasks/empty")
		f.mkdir("tasks/proj/travel/far")
	}
	// tagged: / has 1 [a], 2 [b], 3 [a b], 4 [].
	tagged := func(f *fixture) {
		f.tagged("", 1, "a")
		f.tagged("", 2, "b")
		f.tagged("", 3, "a", "b")
		f.tagged("", 4)
	}
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want        string
	}{
		// Scope, in tree order: /proj/travel before /proj-b.
		{"every open task", `{}`, tree, "tasks [/:1 ready [], /proj:3 blocked [4], /proj/travel:4 ready []]"},
		{"with complete", `{"readiness": ["ready", "blocked", "complete"]}`, tree,
			"tasks [/:1 ready [], /:2 complete [], /proj:3 blocked [4], /proj/travel:4 ready [], /proj-b:5 complete []]"},
		{"a folder", `{"folder": "/proj"}`, tree, "tasks [/proj:3 blocked [4], /proj/travel:4 ready []]"},
		{"not recursive", `{"folder": "/proj", "recursive": false}`, tree, "tasks [/proj:3 blocked [4]]"},
		{"a blocker outside scope still counts", `{"folder": "/proj", "recursive": false}`, func(f *fixture) { f.task("proj", 3, false, 9); f.task("x", 9, false) },
			"tasks [/proj:3 blocked [9]]"},
		{"root, not recursive", `{"recursive": false, "readiness": ["complete", "ready"]}`, tree, "tasks [/:1 ready [], /:2 complete []]"},
		{"nothing in scope", `{"folder": "/empty"}`, tree, "tasks []"},
		{"an empty tree", `{}`, nil, "tasks []"},

		// Readiness.
		{"only complete", `{"readiness": ["complete"]}`, tree, "tasks [/:2 complete [], /proj-b:5 complete []]"},
		{"only blocked", `{"readiness": ["blocked"]}`, tree, "tasks [/proj:3 blocked [4]]"},
		{"only ready", `{"readiness": ["ready"]}`, tree, "tasks [/:1 ready [], /proj/travel:4 ready []]"},

		// Narrowing: a prefix of tree order, after the filters.
		{"limit", `{"limit": 2}`, tree, "tasks [/:1 ready [], /proj:3 blocked [4]] of 3"},
		{"limit not reached", `{"limit": 3}`, tree, "tasks [/:1 ready [], /proj:3 blocked [4], /proj/travel:4 ready []]"},
		{"limit 0: the count alone", `{"limit": 0}`, tree, "tasks [] of 3"},
		{"limit 0 of none", `{"limit": 0, "folder": "/empty"}`, tree, "tasks []"},
		{"limit after readiness", `{"readiness": ["complete"], "limit": 1}`, tree, "tasks [/:2 complete []] of 2"},
		{"tags", `{"tags_any": ["a", "b"]}`, tagged, "tasks [/:1 ready [], /:2 ready [], /:3 ready []]"},
		{"tags, all", `{"tags_all": ["a", "b"]}`, tagged, "tasks [/:3 ready []]"},
		{"tags, both", `{"tags_any": ["c", "b"], "tags_all": ["a"]}`, tagged, "tasks [/:3 ready []]"},
		{"tags, none match", `{"tags_any": ["z"]}`, tagged, "tasks []"},
		{"tags, then limit", `{"tags_any": ["a"], "limit": 1}`, tagged, "tasks [/:1 ready []] of 2"},
		{"folders never narrowed", `{"include_folders": true, "limit": 0, "tags_any": ["z"]}`, tree,
			"folders [/ /empty /proj /proj/travel /proj/travel/far /proj-b]; tasks []"},

		// Folders.
		{"folders", `{"include_folders": true}`, tree,
			"folders [/ /empty /proj /proj/travel /proj/travel/far /proj-b]; tasks [/:1 ready [], /proj:3 blocked [4], /proj/travel:4 ready []]"},
		{"folders, not recursive", `{"include_folders": true, "recursive": false}`, tree,
			"folders [/ /empty /proj /proj-b]; tasks [/:1 ready []]"},
		{"folders of a folder, not recursive", `{"folder": "/proj", "include_folders": true, "recursive": false}`, tree,
			"folders [/proj /proj/travel]; tasks [/proj:3 blocked [4]]"},
		{"folders of an empty folder", `{"folder": "/empty", "include_folders": true}`, tree, "folders [/empty]; tasks []"},

		// Warnings: never narrowed away.
		{"warnings about tasks filtered out", `{"readiness": ["complete"], "tags_any": ["z"], "limit": 0}`, func(f *fixture) {
			f.task("a", 1, false, 9)
			f.task("b", 2, false)
			f.task("c", 2, false)
			f.write("tasks/3.json", "{")
		}, "tasks []; dangling-reference [1 9] [~/tasks/a/1.json]; duplicate-id [2] [~/tasks/b/2.json ~/tasks/c/2.json]; unusable-file [3] [~/tasks/3.json]"},
		{"warnings about tasks past the limit", `{"limit": 1}`, func(f *fixture) {
			f.task("a", 1, false)
			f.task("b", 2, false, 9)
		}, "tasks [/a:1 ready []] of 2; dangling-reference [2 9] [~/tasks/b/2.json]"},
		{"unusable file in scope", `{}`, func(f *fixture) { f.task("", 1, false); f.write("tasks/2.json", "{") },
			"tasks [/:1 ready []]; unusable-file [2] [~/tasks/2.json]"},
		{"unusable file out of scope: silent", `{"folder": "/a"}`, func(f *fixture) { f.task("a", 1, false); f.write("tasks/2.json", "{") },
			"tasks [/a:1 ready []]"},
		{"duplicate in scope", `{}`, func(f *fixture) { f.task("a", 1, false); f.task("b", 1, false) },
			"tasks [/a:1 ready [], /b:1 ready []]; duplicate-id [1] [~/tasks/a/1.json ~/tasks/b/1.json]"},
		{"duplicate, one copy complete and not listed", `{}`, func(f *fixture) { f.task("a", 1, false); f.task("b", 1, true) },
			"tasks [/a:1 ready []]; duplicate-id [1] [~/tasks/a/1.json ~/tasks/b/1.json]"},
		{"duplicate, other copy out of scope: silent", `{"folder": "/a"}`, func(f *fixture) { f.task("a", 1, false); f.task("b", 1, false) },
			"tasks [/a:1 ready []]"},
		{"duplicate, only the copies in scope", `{"folder": "/a"}`, func(f *fixture) { f.task("a", 1, false); f.task("a/x", 1, false); f.task("b", 1, false) },
			"tasks [/a:1 ready [], /a/x:1 ready []]; duplicate-id [1] [~/tasks/a/1.json ~/tasks/a/x/1.json]"},
		{"dangling blocker", `{}`, func(f *fixture) { f.task("", 1, false, 9) },
			"tasks [/:1 blocked [9]]; dangling-reference [1 9] [~/tasks/1.json]"},
		{"a complete task's blockers aren't read", `{"readiness": ["complete"]}`, func(f *fixture) { f.task("", 1, true, 9) },
			"tasks [/:1 complete []]"},
		{"duplicated blocker", `{"folder": "/a"}`, func(f *fixture) { f.task("a", 1, false, 2); f.task("b", 2, true); f.task("c", 2, true) },
			"tasks [/a:1 blocked [2]]; duplicate-id [2] [~/tasks/b/2.json ~/tasks/c/2.json]"},
		{"duplicated blocker, one copy can't be looked at", `{}`, func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("", 2, true)
			f.task("a", 2, true)
			f.failFile("a/2.json", syscall.EACCES)
		}, "tasks [/:1 blocked [2]]; duplicate-id [2] [~/tasks/2.json ~/tasks/a/2.json]; unusable-file [2] [~/tasks/a/2.json]"},
		{"unusable blocker", `{"folder": "/a"}`, func(f *fixture) { f.task("a", 1, false, 2); f.write("tasks/b/2.json", "{") },
			"tasks [/a:1 blocked [2]]; unusable-file [2] [~/tasks/b/2.json]"},
		{"unreadable folder: warned, its tasks missing, no dangling warning", `{}`, func(f *fixture) {
			f.task("a", 1, false, 2)
			f.task("b", 2, false)
			f.fail(fsys.OpReadDir, "b", syscall.EACCES)
		}, "tasks [/a:1 blocked [2]]; unreadable-folder [] [~/tasks/b]"},
		{"unreadable folder out of scope is still warned", `{"folder": "/a", "include_folders": true}`, func(f *fixture) {
			f.task("a", 1, false)
			f.mkdir("tasks/b")
			f.fail(fsys.OpReadDir, "b", syscall.EACCES)
		}, "folders [/a]; tasks [/a:1 ready []]; unreadable-folder [] [~/tasks/b]"},
		{"unreadable folder in scope is still a folder", `{"include_folders": true}`, func(f *fixture) {
			f.mkdir("tasks/b/c")
			f.fail(fsys.OpReadDir, "b", syscall.EACCES)
		}, "folders [/ /b]; tasks []; unreadable-folder [] [~/tasks/b]"},
		{"task vanished mid-read: skipped", `{}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false)
			f.fail(fsys.OpReadFile, "2.json", syscall.ENOENT)
		},
			"tasks [/:1 ready []]"},

		// Errors.
		{"folder missing", `{"folder": "/a/b"}`, nil, `not-found {"folders":["/a"],"ids":[],"paths":[]}`},
		{"folder a file", `{"folder": "/a"}`, func(f *fixture) { f.write("tasks/a", "") }, `corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`},
		{"folder a symlink", `{"folder": "/a"}`, func(f *fixture) {
			f.mkdir("real")
			if err := os.Symlink(filepath.Join(f.home, "real"), filepath.Join(f.root, "a")); err != nil {
				f.t.Fatal(err)
			}
		}, `corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`},
		{"corrupt koan.json", `{}`, func(f *fixture) { f.write("tasks/koan.json", "{") }, `corrupt {"path":"~/tasks/koan.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"bad input", `{"recursive": "no"}`, nil, `invalid-input {"problems":[{"field":"/recursive","reason":"expected a boolean"}]}`},
		{"include_complete is gone", `{"include_complete": true}`, nil, `invalid-input {"problems":[{"field":"/include_complete","reason":"unknown field"}]}`},
		{"bad readiness", `{"readiness": ["ready", "done", "ready"]}`, nil,
			`invalid-input {"problems":[{"field":"/readiness/1","reason":"must be one of ready, blocked, complete"},{"field":"/readiness/2","reason":"duplicate of item 0"}]}`},
		{"empty readiness", `{"readiness": []}`, nil, `invalid-input {"problems":[{"field":"/readiness","reason":"must list at least one readiness value"}]}`},
		{"bad narrowing", `{"limit": -1, "fields": ["title", "name"], "tags_any": [], "tags_all": ["A"]}`, nil,
			`invalid-input {"problems":[{"field":"/fields/1","reason":"must be one of schema, id, title, priority, created_at, completed_at, updated_at, blocked_by, tags, extra, folder, notes_path, readiness, blocking"},{"field":"/limit","reason":"must be between 0 and 9007199254740991"},{"field":"/tags_all/0","reason":"must be 1-64 lowercase letters, digits, and hyphens, not starting or ending with a hyphen"},{"field":"/tags_any","reason":"must list at least one tag"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.list(tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
