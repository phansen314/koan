package ops

import (
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/schematest"
)

// frontier runs frontier with input over f twice, checking that both runs
// give the same bytes, the envelope, and on success frontier-output and
// that truncated means total is more than the tasks returned. It returns the
// tasks in short — "folder:id" each, or "folder:id pN" with a priority, then
// " of total" if the limit cut them — or the error as "kind details", then
// each warning as "kind ids paths"; the home as "~".
func (f *fixture) frontier(input string) string {
	f.t.Helper()
	e := Run("frontier", parse(f.t, input), nil, f.env)
	first := line(f.t, e)
	if again := line(f.t, Run("frontier", parse(f.t, input), nil, f.env)); again != first {
		f.t.Errorf("second run differs:\n%s\n%s", first, again)
	}
	var parts []string
	if e.OK {
		b, err := jsonio.MarshalLine(e.Result)
		if err != nil {
			f.t.Fatal(err)
		}
		if ok, fl := schematest.Check(f.t, "frontier-output", b); !ok {
			f.t.Errorf("frontier-output rejects at %s: %s", fl, b)
		}
		r := e.Result.(FrontierOutput)
		var tasks []string
		for _, v := range r.Tasks.Views {
			if v.Readiness != model.Ready || len(v.Blocking) != 0 {
				f.t.Errorf("task %d: readiness %s, blocking %v", v.ID, v.Readiness, v.Blocking)
			}
			s := fmt.Sprintf("%s:%d", v.Folder, v.ID)
			if v.Priority != nil {
				s += fmt.Sprintf(" p%d", *v.Priority)
			}
			tasks = append(tasks, s)
		}
		s := "[" + strings.Join(tasks, ", ") + "]"
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

// prioritized writes an open task id in folder with priority p, blocked by
// blockedBy.
func (f *fixture) prioritized(folder string, id model.ID, p int64, blockedBy ...model.ID) {
	f.t.Helper()
	tf := model.TaskFile{
		Schema: model.TaskSchema, ID: id, Title: model.Title(fmt.Sprintf("task %d", id)), Priority: &p,
		CreatedAt: "2026-09-20T18:31:51Z", UpdatedAt: "2026-09-20T18:31:51Z", BlockedBy: blockedBy, Extra: &jsonio.Object{},
	}
	data, err := tf.Encode()
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(fmt.Sprintf("tasks/%s/%d.json", folder, id), string(data))
}

func TestFrontier(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want        string
	}{
		// Order: priority highest first, none last; then ID; copies of one ID
		// in tree order.
		{"by priority, then ID", `{}`, func(f *fixture) {
			f.task("", 1, false)
			f.prioritized("", 2, -5)
			f.prioritized("", 3, 0)
			f.prioritized("z", 4, 3)
			f.prioritized("a", 5, 3)
			f.task("a", 6, false)
			f.prioritized("", 7, 10)
		}, "[/:7 p10, /z:4 p3, /a:5 p3, /:3 p0, /:2 p-5, /:1, /a:6]"},
		{"copies in tree order", `{}`, func(f *fixture) {
			f.prioritized("z", 2, 1)
			f.prioritized("a", 2, 1)
			f.prioritized("", 2, 1)
			f.prioritized("", 1, 1)
		}, "[/:1 p1, /:2 p1, /a:2 p1, /z:2 p1]; duplicate-id [2] [~/tasks/2.json ~/tasks/a/2.json ~/tasks/z/2.json]"},
		{"copies judged separately", `{}`, func(f *fixture) {
			f.task("a", 2, false, 9)
			f.prioritized("b", 2, 5)
			f.task("c", 2, true)
			f.task("", 9, false)
		}, "[/b:2 p5, /:9]; duplicate-id [2] [~/tasks/a/2.json ~/tasks/b/2.json ~/tasks/c/2.json]"},

		// Only ready tasks.
		{"blocked left out, done blocker doesn't block", `{}`, func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("", 2, false)
			f.task("", 3, false, 4)
			f.task("", 4, true)
		}, "[/:2, /:3]"},
		{"done tasks never", `{"recursive": true}`, func(f *fixture) { f.task("", 1, true); f.task("", 2, false) }, "[/:2]"},
		{"a blocker outside scope still blocks", `{"folder": "/proj"}`, func(f *fixture) {
			f.task("proj", 1, false, 2)
			f.task("infra", 2, false)
			f.task("proj", 3, false)
		}, "[/proj:3]"},
		{"not recursive", `{"folder": "/proj", "recursive": false}`, func(f *fixture) { f.task("proj", 1, false); f.task("proj/x", 2, false) }, "[/proj:1]"},
		{"empty", `{}`, func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, false, 1) }, "[]"},
		{"empty tree", `{}`, nil, "[]"},

		// Narrowing: a prefix of frontier order, after the filters.
		{"limit", `{"limit": 2}`, func(f *fixture) {
			f.task("", 1, false)
			f.prioritized("", 2, 1)
			f.prioritized("", 3, 5)
		}, "[/:3 p5, /:2 p1] of 3"},
		{"limit 0", `{"limit": 0}`, func(f *fixture) { f.task("", 1, false); f.task("", 2, false) }, "[] of 2"},
		{"limit past the end", `{"limit": 9}`, func(f *fixture) { f.task("", 1, false) }, "[/:1]"},
		{"blocked tasks don't count", `{"limit": 1}`, func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, false) }, "[/:2]"},
		{"tags, then limit", `{"tags_any": ["a"], "tags_all": ["b"], "limit": 1}`, func(f *fixture) {
			f.tagged("", 1, "a", "b")
			f.tagged("", 2, "a")
			f.tagged("", 3, "b", "a")
			f.prioritized("", 4, 9)
		}, "[/:1] of 2"},
		{"warnings about tasks cut", `{"limit": 0}`, func(f *fixture) { f.task("", 1, false, 9); f.task("", 2, false) },
			"[] of 1; dangling-reference [1 9] [~/tasks/1.json]"},

		// Every task-file problem is a warning.
		{"unusable file in scope", `{}`, func(f *fixture) { f.task("", 1, false); f.write("tasks/2.json", "{") },
			"[/:1]; unusable-file [2] [~/tasks/2.json]"},
		{"dangling blocker", `{}`, func(f *fixture) { f.task("", 1, false, 9); f.task("", 2, false) },
			"[/:2]; dangling-reference [1 9] [~/tasks/1.json]"},
		{"duplicated blocker", `{"folder": "/a"}`, func(f *fixture) { f.task("a", 1, false, 2); f.task("b", 2, true); f.task("c", 2, true) },
			"[]; duplicate-id [2] [~/tasks/b/2.json ~/tasks/c/2.json]"},
		{"duplicated blocker, one copy can't be looked at", `{}`, func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("", 2, true)
			f.task("a", 2, true)
			f.failFile("a/2.json", syscall.EACCES)
		}, "[]; duplicate-id [2] [~/tasks/2.json ~/tasks/a/2.json]; unusable-file [2] [~/tasks/a/2.json]"},
		{"unusable blocker", `{"folder": "/a"}`, func(f *fixture) { f.task("a", 1, false, 2); f.write("tasks/b/2.json", `{"schema": 2}`) },
			"[]; unusable-file [2] [~/tasks/b/2.json]"},
		{"blocker in an unreadable folder", `{"folder": "/a"}`, func(f *fixture) {
			f.task("a", 1, false, 2)
			f.task("a", 3, false)
			f.task("b", 2, true)
			f.fail(fsys.OpReadDir, "b", syscall.EACCES)
		}, "[/a:3]; unreadable-folder [] [~/tasks/b]"},

		// Errors.
		{"folder missing", `{"folder": "/a"}`, nil, `not-found {"folders":["/a"],"ids":[],"paths":[]}`},
		{"folder a file", `{"folder": "/a"}`, func(f *fixture) { f.write("tasks/a", "") }, `corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`},
		{"no config", `{}`, func(f *fixture) { f.remove("cfg") }, `not-initialized {"missing":"config"}`},
		{"list's options aren't frontier's", `{"readiness": ["done"]}`, nil, `invalid-input {"problems":[{"field":"/readiness","reason":"unknown field"}]}`},
		{"bad limit", `{"limit": 1.5}`, nil, `invalid-input {"problems":[{"field":"/limit","reason":"must be an integer written without a fraction or exponent (2, not 2.0 or 2e0)"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.frontier(tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// Projected, byte for byte, in frontier order.
func TestFrontierFields(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, false)
	f.prioritized("", 8, 2)
	got := f.rel(line(t, Run("frontier", parse(t, `{"fields": ["folder", "priority", "title"]}`), nil, f.env)))
	want := `{"ok":true,"result":{"tasks":[{"id":8,"title":"task 8","priority":2,"folder":"/"},{"id":7,"title":"task 7","priority":null,"folder":"/proj"}],"total":2,"truncated":false},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Every input the adapters know is an implemented operation's, except
// pick's, which runs none.
func TestEveryOperationImplemented(t *testing.T) {
	for op := range decoders {
		if runners[op] == nil && op != "pick" {
			t.Errorf("operation %s has no runner", op)
		}
	}
}
