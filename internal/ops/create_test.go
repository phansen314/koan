package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/schematest"
)

// create runs create with input over f, checking the envelope, and the task
// schema on success or create-partial when there is a partial.
func (f *fixture) create(input string) Envelope {
	f.t.Helper()
	e := Run("create", parse(f.t, input), nil, f.env)
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
		check("task", e.Result)
	case e.Error.Partial != nil:
		check("create-partial", e.Error.Partial)
	}
	return e
}

// createSummary is e in short: the task as "folder id", or the error as
// "kind details", with " partial <json>" when there is one; then each warning
// as "kind ids paths [code]"; the home as "~".
func (f *fixture) createSummary(e Envelope) string {
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
		t := e.Result.(model.Task)
		parts = append(parts, fmt.Sprintf("%s %d", t.Folder, t.ID))
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

func (f *fixture) mkdir(rel string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.home, rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// failAt fails op when its target — NewPath for link and rename, else Path —
// is rel, relative to the root.
func (f *fixture) failAt(op, rel string, errno syscall.Errno) {
	f.hook(func(o fsys.Op) error {
		target := o.Path
		if o.Name == fsys.OpLink || o.Name == fsys.OpRename {
			target = o.NewPath
		}
		if o.Name == op && target == rel {
			return errno
		}
		return nil
	})
}

// The whole output and every file written, byte for byte.
func TestCreate(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 1, false)
	f.task("proj", 3, true)
	got := f.rel(line(t, f.create(`{"title": "  Book flights ", "folder": "/proj", "priority": 2, "tags": ["urgent", "travel"],
		"blocked_by": [3, 1], "extra": {"status": "waiting", "n": 1.50}, "notes": "call first\n"}`)))
	want := `{"ok":true,"result":{"schema":1,"id":101,"title":"Book flights","priority":2,"created_at":"2026-09-28T12:00:00Z","completed_at":null,"updated_at":"2026-09-28T12:00:00Z","blocked_by":[1,3],"tags":["travel","urgent"],"extra":{"status":"waiting","n":1.50},"folder":"/proj","notes_path":"~/tasks/proj/101.md"},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	for rel, want := range map[string]string{
		"tasks/proj/101.json": "{\n  \"schema\": 1,\n  \"id\": 101,\n  \"title\": \"Book flights\",\n  \"priority\": 2,\n  \"created_at\": \"2026-09-28T12:00:00Z\",\n  \"completed_at\": null,\n  \"updated_at\": \"2026-09-28T12:00:00Z\",\n  \"blocked_by\": [\n    1,\n    3\n  ],\n  \"tags\": [\n    \"travel\",\n    \"urgent\"\n  ],\n  \"extra\": {\n    \"status\": \"waiting\",\n    \"n\": 1.50\n  }\n}\n",
		"tasks/proj/101.md":   "call first\n",
		"tasks/ftask.json":    "{\n  \"schema\": 1,\n  \"last_id\": 101\n}\n",
	} {
		if got := f.read(rel); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", rel, got, want)
		}
	}
}

// Defaults: the root folder, no priority, empty sets, empty notes, and the
// .md written even so.
func TestCreateDefaults(t *testing.T) {
	f := newFixture(t)
	e := f.create(`{"title": "x"}`)
	got := f.rel(line(t, e))
	want := `{"ok":true,"result":{"schema":1,"id":101,"title":"x","priority":null,"created_at":"2026-09-28T12:00:00Z","completed_at":null,"updated_at":"2026-09-28T12:00:00Z","blocked_by":[],"tags":[],"extra":{},"folder":"/","notes_path":"~/tasks/101.md"},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := f.read("tasks/101.md"); got != "" {
		t.Errorf(".md holds %q", got)
	}
}

func TestCreateCases(t *testing.T) {
	const exhausted = "{\"schema\": 1, \"last_id\": 999999999999999}\n"
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		input string
		want  string
		files map[string]string // under the home, afterwards; "<none>" for absent
	}{
		// The folder's path walk.
		{"folder missing", nil, `{"title": "x", "folder": "/a/b"}`,
			`not-found {"folders":["/a"],"ids":[],"paths":[]}`, map[string]string{"tasks/ftask.json": "{\"schema\": 1, \"last_id\": 100}\n"}},
		{"folder's last segment missing", func(f *fixture) { f.mkdir("tasks/a") }, `{"title": "x", "folder": "/a/b"}`,
			`not-found {"folders":["/a/b"],"ids":[],"paths":[]}`, nil},
		{"folder a file", func(f *fixture) { f.write("tasks/a", "") }, `{"title": "x", "folder": "/a/b"}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, nil},
		{"folder a symlink", func(f *fixture) {
			f.mkdir("real")
			if err := os.Symlink(filepath.Join(f.home, "real"), filepath.Join(f.root, "a")); err != nil {
				f.t.Fatal(err)
			}
		}, `{"title": "x", "folder": "/a"}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, map[string]string{"real/101.json": "<none>"}},
		{"existing folder", func(f *fixture) { f.mkdir("tasks/a/b") }, `{"title": "x", "folder": "/a/b"}`,
			`/a/b 101`, map[string]string{"tasks/a/b/101.json": "", "tasks/a/b/101.md": ""}},

		// Blockers.
		{"open and complete blockers", func(f *fixture) { f.task("", 1, false); f.task("x", 2, true) }, `{"title": "x", "blocked_by": [2, 1]}`,
			`/ 101`, nil},
		{"missing blockers", func(f *fixture) { f.task("", 1, false) }, `{"title": "x", "blocked_by": [9, 1, 5]}`,
			`not-found {"folders":[],"ids":[5,9],"paths":[]}`, map[string]string{"tasks/ftask.json": "{\"schema\": 1, \"last_id\": 100}\n"}},
		{"missing folder and blockers in one not-found", nil, `{"title": "x", "folder": "/a", "blocked_by": [9]}`,
			`not-found {"folders":["/a"],"ids":[9],"paths":[]}`, nil},
		{"corrupt folder before missing blockers", func(f *fixture) { f.write("tasks/a", "") }, `{"title": "x", "folder": "/a", "blocked_by": [9]}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, nil},
		{"not-found before a corrupt blocker", func(f *fixture) { f.write("tasks/1.json", "{") }, `{"title": "x", "blocked_by": [1, 9]}`,
			`not-found {"folders":[],"ids":[9],"paths":[]}`, nil},
		{"missing folder before a corrupt blocker", func(f *fixture) { f.write("tasks/1.json", "{") }, `{"title": "x", "folder": "/a", "blocked_by": [1]}`,
			`not-found {"folders":["/a"],"ids":[],"paths":[]}`, nil},
		{"corrupt blocker", func(f *fixture) { f.write("tasks/1.json", "{") }, `{"title": "x", "blocked_by": [1]}`,
			`corrupt {"path":"~/tasks/1.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, nil},
		{"unsupported blocker", func(f *fixture) { f.write("tasks/1.json", `{"schema": 2}`) }, `{"title": "x", "blocked_by": [1]}`,
			`unsupported-format {"path":"~/tasks/1.json","found":2,"supported":[1]}`, nil},
		{"blockers above last_id", func(f *fixture) { f.task("x", 102, false); f.task("x", 101, false); f.task("", 1, false) },
			`{"title": "x", "blocked_by": [1, 102, 101]}`,
			`conflict {"rule":"id-above-last-id","ids":[101,102]}`,
			map[string]string{"tasks/ftask.json": "{\"schema\": 1, \"last_id\": 100}\n", "tasks/101.json": "<none>"}},
		{"unreadable blocker", func(f *fixture) { f.task("", 1, false); f.fail(fsys.OpReadFile, "1.json", syscall.EACCES) }, `{"title": "x", "blocked_by": [1]}`,
			`io {"path":"~/tasks/1.json","code":"EACCES"}`, nil},
		{"first unusable blocker in tree order", func(f *fixture) { f.write("tasks/p/1.json", "{"); f.write("tasks/2.json", "{") }, `{"title": "x", "blocked_by": [1, 2]}`,
			`corrupt {"path":"~/tasks/2.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, nil},
		{"duplicated blocker", func(f *fixture) { f.task("", 1, false); f.task("p", 1, true) }, `{"title": "x", "blocked_by": [1]}`,
			`/ 101; duplicate-id [1] [~/tasks/1.json ~/tasks/p/1.json]`, nil},
		{"duplicated blocker with a corrupt copy", func(f *fixture) { f.task("", 1, false); f.write("tasks/p/1.json", "{") }, `{"title": "x", "blocked_by": [1]}`,
			`corrupt {"path":"~/tasks/p/1.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, nil},
		{"blocker vanished", func(f *fixture) { f.task("", 1, false); f.fail(fsys.OpReadFile, "1.json", syscall.ENOENT) }, `{"title": "x", "blocked_by": [1]}`,
			`not-found {"folders":[],"ids":[1],"paths":[]}`, nil},
		{"one copy vanished", func(f *fixture) {
			f.task("", 1, false)
			f.task("p", 1, false)
			f.fail(fsys.OpReadFile, "p/1.json", syscall.ENOENT)
		}, `{"title": "x", "blocked_by": [1]}`,
			`/ 101`, nil},
		{"unlistable folder with blockers", func(f *fixture) {
			f.task("", 1, false)
			f.mkdir("tasks/p")
			f.fail(fsys.OpReadDir, "p", syscall.EACCES)
		}, `{"title": "x", "blocked_by": [1]}`,
			`io {"path":"~/tasks/p","code":"EACCES"}`, nil},
		{"unlistable folder, no blockers: no walk", func(f *fixture) { f.mkdir("tasks/p"); f.fail(fsys.OpReadDir, "p", syscall.EACCES) }, `{"title": "x"}`,
			`/ 101`, nil},

		// The ID.
		{"id exhausted", func(f *fixture) { f.write("tasks/ftask.json", exhausted) }, `{"title": "x"}`,
			`conflict {"rule":"id-exhausted","ids":[]}`, map[string]string{"tasks/ftask.json": exhausted}},
		{"not-found before id exhausted", func(f *fixture) { f.write("tasks/ftask.json", exhausted) }, `{"title": "x", "blocked_by": [1]}`,
			`not-found {"folders":[],"ids":[1],"paths":[]}`, nil},
		{"last id below the ceiling", func(f *fixture) { f.write("tasks/ftask.json", `{"schema": 1, "last_id": 999999999999998}`) }, `{"title": "x"}`,
			`/ 999999999999999`, nil},
		{"task file already there", func(f *fixture) { f.task("", 101, false) }, `{"title": "x"}`,
			`corrupt {"path":"~/tasks/101.json","reason":"unexpected-file"} partial {"id":101}`,
			map[string]string{"tasks/ftask.json": "{\n  \"schema\": 1,\n  \"last_id\": 101\n}\n", "tasks/101.md": "<none>"}},
		{"task file already there in another folder", func(f *fixture) { f.task("p", 101, false) }, `{"title": "x"}`,
			`/ 101`, nil},
		{"stray .md replaced", func(f *fixture) { f.write("tasks/101.md", "stale") }, `{"title": "x", "notes": "new"}`,
			`/ 101`, map[string]string{"tasks/101.md": "new"}},

		// Root states come first.
		{"corrupt ftask.json", func(f *fixture) { f.write("tasks/ftask.json", "{") }, `{"title": "x", "folder": "/a"}`,
			`corrupt {"path":"~/tasks/ftask.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, nil},
		{"no config", func(f *fixture) { f.remove("cfg") }, `{"title": "x"}`,
			`not-initialized {"missing":"config"}`, nil},

		// Failures midway.
		{"ftask.json not written", func(f *fixture) { f.failAt(fsys.OpRename, "ftask.json", syscall.ENOSPC) }, `{"title": "x"}`,
			`io {"path":"~/tasks/ftask.json","code":"ENOSPC"}`,
			map[string]string{"tasks/ftask.json": "{\"schema\": 1, \"last_id\": 100}\n", "tasks/101.json": "<none>"}},
		{"task file not written", func(f *fixture) { f.failAt(fsys.OpLink, "101.json", syscall.ENOSPC) }, `{"title": "x"}`,
			`io {"path":"~/tasks/101.json","code":"ENOSPC"} partial {"id":101}`,
			map[string]string{"tasks/ftask.json": "{\n  \"schema\": 1,\n  \"last_id\": 101\n}\n", "tasks/101.json": "<none>", "tasks/101.md": "<none>"}},
		{".md not written", func(f *fixture) { f.failAt(fsys.OpRename, "101.md", syscall.ENOSPC) }, `{"title": "x", "notes": "n"}`,
			`/ 101; notes-missing [101] [~/tasks/101.md] ENOSPC`, map[string]string{"tasks/101.md": "<none>"}},
		{".md not written, no errno name", func(f *fixture) { f.failAt(fsys.OpRename, "101.md", syscall.Errno(4000)) }, `{"title": "x", "notes": "n"}`,
			`/ 101; notes-missing [101] [~/tasks/101.md]`, map[string]string{"tasks/101.md": "<none>"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.createSummary(f.create(tc.input)); got != tc.want {
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

// After a partial, a retry is safe and uses the next ID.
func TestCreateRetryAfterPartial(t *testing.T) {
	f := newFixture(t)
	f.task("", 101, false)
	if e := f.create(`{"title": "x"}`); e.OK || e.Error.Partial == nil {
		t.Fatalf("first create: %s", line(t, e))
	}
	if got := f.createSummary(f.create(`{"title": "x"}`)); got != "/ 102" {
		t.Errorf("retry: %s", got)
	}
}

// Another write holding the lock: busy, and nothing written.
func TestCreateBusy(t *testing.T) {
	f := newFixture(t)
	r, err := fsys.OS{}.OpenRoot(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, err := r.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if got := f.createSummary(f.create(`{"title": "x"}`)); got != "busy {}" {
		t.Errorf("got %s", got)
	}
	if got := f.read("tasks/ftask.json"); got != "{\"schema\": 1, \"last_id\": 100}\n" {
		t.Errorf("ftask.json %q", got)
	}
	if got := f.read("tasks/101.json"); got != "<none>" {
		t.Errorf("task written: %q", got)
	}
	l.Unlock()
	if got := f.createSummary(f.create(`{"title": "x"}`)); got != "/ 101" {
		t.Errorf("after unlock: %s", got)
	}
}

// Concurrent creates, each retrying on busy, lose no update: every ID is
// distinct and last_id counts them all. flock locks per open file, so
// goroutines contend as processes do; e2e runs the same across processes.
func TestCreateConcurrent(t *testing.T) {
	const writers, each = 8, 15
	f := newFixture(t)
	var wg sync.WaitGroup
	ids := make(chan int64, writers*each)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				for {
					e := Run("create", parse(t, `{"title": "x"}`), nil, f.env)
					if e.OK {
						ids <- int64(e.Result.(model.Task).ID)
						break
					}
					if e.Error.Kind != errs.KindBusy {
						t.Errorf("create: %s %s", e.Error.Kind, e.Error.Message)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("ID %d issued twice", id)
		}
		seen[id] = true
	}
	if len(seen) != writers*each {
		t.Errorf("%d tasks created, want %d", len(seen), writers*each)
	}
	want := fmt.Sprintf("{\n  \"schema\": 1,\n  \"last_id\": %d\n}\n", 100+writers*each)
	if got := f.read("tasks/ftask.json"); got != want {
		t.Errorf("ftask.json %q, want %q", got, want)
	}
}
