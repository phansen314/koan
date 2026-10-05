package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/schematest"
)

// changed runs op (complete or reopen) for id over f, checking the envelope
// and, on success, op's output schema. It returns the result in short —
// "folder id completed_at changed=…" — or the error as "kind details", the
// home as "~".
func (f *fixture) changed(op string, id int) string {
	f.t.Helper()
	e := Run(op, parse(f.t, fmt.Sprintf(`{"id": %d}`, id)), nil, f.env)
	line(f.t, e)
	b, err := jsonio.MarshalLine(e.Result)
	if err != nil {
		f.t.Fatal(err)
	}
	if !e.OK {
		d, err := jsonio.MarshalLine(e.Error.Details)
		if err != nil {
			f.t.Fatal(err)
		}
		return f.rel(string(e.Error.Kind) + " " + strings.TrimSpace(string(d)))
	}
	if ok, fl := schematest.Check(f.t, op+"-output", b); !ok {
		f.t.Errorf("%s-output rejects at %s: %s", op, fl, b)
	}
	r := e.Result.(ChangedOutput)
	at := "null"
	if r.CompletedAt != nil {
		at = string(*r.CompletedAt)
	}
	return fmt.Sprintf("%s %d %s changed=%v", r.Folder, r.ID, at, r.Changed)
}

// A rich task file, as ftask writes it: open, or completed (and last
// updated) at 2026-09-21T10:00:00Z.
func richTask(completed bool) string {
	at, updated := "null", `"2026-09-20T18:31:51Z"`
	if completed {
		at, updated = `"2026-09-21T10:00:00Z"`, `"2026-09-21T10:00:00Z"`
	}
	return "{\n  \"schema\": 1,\n  \"id\": 7,\n  \"title\": \"Pack bags\",\n  \"priority\": -2,\n  \"created_at\": \"2026-09-20T18:31:51Z\",\n  \"completed_at\": " + at +
		",\n  \"updated_at\": " + updated + ",\n  \"blocked_by\": [\n    1,\n    3\n  ],\n  \"tags\": [\n    \"travel\"\n  ],\n  \"extra\": {\n    \"n\": 1.50,\n    \"deep\": {\n      \"k\": [\n        true,\n        null\n      ]\n    }\n  }\n}\n"
}

// Only completed_at changes: the rest of the file, byte for byte, and the .md
// are as they were.
func TestComplete(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/proj/7.json", richTask(false))
	f.write("tasks/proj/7.md", "notes\n")
	e := Run("complete", parse(t, `{"id": 7}`), nil, f.env)
	got := f.rel(line(t, e))
	want := `{"ok":true,"result":{"schema":1,"id":7,"title":"Pack bags","priority":-2,"created_at":"2026-09-20T18:31:51Z","completed_at":"2026-09-28T12:00:00Z","updated_at":"2026-09-28T12:00:00Z","blocked_by":[1,3],"tags":["travel"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"folder":"/proj","notes_path":"~/tasks/proj/7.md","changed":true},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	wantFile := strings.Replace(stamped(richTask(false)), `"completed_at": null`, `"completed_at": "2026-09-28T12:00:00Z"`, 1)
	if got := f.read("tasks/proj/7.json"); got != wantFile {
		t.Errorf("task file:\n%s\nwant\n%s", got, wantFile)
	}
	if got := f.read("tasks/proj/7.md"); got != "notes\n" {
		t.Errorf(".md %q", got)
	}
}

// A task file in another layout keeps every value, rewritten in ftask's.
func TestCompleteHandFormatted(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/7.json", `{"schema":1,"id":7,"title":"t","priority":null,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":[],"tags":[],"extra":{"n":1e2}}`)
	if got := f.changed("complete", 7); got != "/ 7 2026-09-28T12:00:00Z changed=true" {
		t.Errorf("got %s", got)
	}
	want := "{\n  \"schema\": 1,\n  \"id\": 7,\n  \"title\": \"t\",\n  \"priority\": null,\n  \"created_at\": \"2026-09-20T18:31:51Z\",\n  \"completed_at\": \"2026-09-28T12:00:00Z\",\n  \"updated_at\": \"2026-09-28T12:00:00Z\",\n  \"blocked_by\": [],\n  \"tags\": [],\n  \"extra\": {\n    \"n\": 1e2\n  }\n}\n"
	if got := f.read("tasks/7.json"); got != want {
		t.Errorf("task file:\n%s\nwant\n%s", got, want)
	}
}

func TestCompletedAtCases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		op    string
		setup func(f *fixture)
		want  string
		file  string // want tasks/7.json afterwards; "" to skip
	}{
		// complete.
		{"complete an open task", "complete", func(f *fixture) { f.write("tasks/7.json", richTask(false)) },
			"/ 7 2026-09-28T12:00:00Z changed=true", ""},
		{"complete a complete task", "complete", func(f *fixture) { f.write("tasks/7.json", richTask(true)) },
			"/ 7 2026-09-21T10:00:00Z changed=false", richTask(true)},
		{"open blockers don't stop it", "complete", func(f *fixture) { f.task("", 7, false, 8); f.task("", 8, false) },
			"/ 7 2026-09-28T12:00:00Z changed=true", ""},
		{"missing blockers don't stop it", "complete", func(f *fixture) { f.task("", 7, false, 99) },
			"/ 7 2026-09-28T12:00:00Z changed=true", ""},

		// reopen.
		{"reopen a complete task", "reopen", func(f *fixture) { f.write("tasks/7.json", richTask(true)) },
			"/ 7 null changed=true", stamped(richTask(false))},
		{"reopen an open task", "reopen", func(f *fixture) { f.write("tasks/7.json", richTask(false)) },
			"/ 7 null changed=false", richTask(false)},
		{"reopen: duplicate", "reopen", func(f *fixture) { f.write("tasks/7.json", richTask(true)); f.task("p", 7, true) },
			`conflict {"rule":"duplicate-id","ids":[7]}`, richTask(true)},
		{"reopen: not found", "reopen", func(f *fixture) {}, `not-found {"folders":[],"ids":[7],"paths":[]}`, ""},
		{"reopen: replace fails", "reopen", func(f *fixture) {
			f.write("tasks/7.json", richTask(true))
			f.failAt(fsys.OpRename, "7.json", syscall.ENOSPC)
		},
			`io {"path":"~/tasks/7.json","code":"ENOSPC"}`, richTask(true)},

		// Finding the one task, in precedence order.
		{"not found", "complete", func(f *fixture) { f.task("", 8, false) },
			`not-found {"folders":[],"ids":[7],"paths":[]}`, ""},
		{"corrupt", "complete", func(f *fixture) { f.write("tasks/7.json", "{") },
			`corrupt {"path":"~/tasks/7.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, "{"},
		{"invalid", "complete", func(f *fixture) { f.write("tasks/7.json", `{"schema": 1}`) },
			`corrupt {"path":"~/tasks/7.json","reason":"invalid","problems":[{"field":"/blocked_by","reason":"required"},{"field":"/completed_at","reason":"required"},{"field":"/created_at","reason":"required"},{"field":"/extra","reason":"required"},{"field":"/id","reason":"required"},{"field":"/priority","reason":"required"},{"field":"/tags","reason":"required"},{"field":"/title","reason":"required"},{"field":"/updated_at","reason":"required"}]}`, ""},
		{"unsupported", "complete", func(f *fixture) { f.write("tasks/7.json", `{"schema": 2}`) },
			`unsupported-format {"path":"~/tasks/7.json","found":2,"supported":[1]}`, `{"schema": 2}`},
		{"unreadable", "complete", func(f *fixture) { f.task("", 7, false); f.fail(fsys.OpReadFile, "7.json", syscall.EACCES) },
			`io {"path":"~/tasks/7.json","code":"EACCES"}`, ""},
		{"duplicate", "complete", func(f *fixture) { f.write("tasks/7.json", richTask(false)); f.task("p", 7, false) },
			`conflict {"rule":"duplicate-id","ids":[7]}`, richTask(false)},
		{"duplicate with a corrupt copy: corrupt first", "complete", func(f *fixture) { f.task("", 7, false); f.write("tasks/p/7.json", "{") },
			`corrupt {"path":"~/tasks/p/7.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, ""},
		{"duplicate, one copy vanished", "complete", func(f *fixture) {
			f.task("", 7, false)
			f.task("p", 7, false)
			f.fail(fsys.OpReadFile, "p/7.json", syscall.ENOENT)
		}, "/ 7 2026-09-28T12:00:00Z changed=true", ""},
		{"unlistable folder", "complete", func(f *fixture) {
			f.task("", 7, false)
			f.mkdir("tasks/p")
			f.fail(fsys.OpReadDir, "p", syscall.EACCES)
		},
			`io {"path":"~/tasks/p","code":"EACCES"}`, ""},
		{"corrupt ftask.json", "complete", func(f *fixture) { f.task("", 7, false); f.write("tasks/ftask.json", "{") },
			`corrupt {"path":"~/tasks/ftask.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, ""},

		// The write fails: all-or-nothing.
		{"replace fails", "complete", func(f *fixture) {
			f.write("tasks/7.json", richTask(false))
			f.failAt(fsys.OpRename, "7.json", syscall.ENOSPC)
		},
			`io {"path":"~/tasks/7.json","code":"ENOSPC"}`, richTask(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			if got := f.changed(tc.op, 7); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if tc.file != "" {
				if got := f.read("tasks/7.json"); got != tc.file {
					t.Errorf("task file:\n%s\nwant\n%s", got, tc.file)
				}
			}
		})
	}
}

// A task already as asked is not written at all: its file keeps its
// modification time.
func TestCompletedAtUnchangedNotWritten(t *testing.T) {
	for op, completed := range map[string]bool{"complete": true, "reopen": false} {
		f := newFixture(t)
		f.write("tasks/7.json", richTask(completed))
		p := filepath.Join(f.root, "7.json")
		old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		f.changed(op, 7)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(old) {
			t.Errorf("%s: task file rewritten: modified %v", op, fi.ModTime())
		}
	}
}

// complete then reopen gives back the file as it was, byte for byte, but for
// updated_at.
func TestCompleteReopenRoundTrip(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/7.json", richTask(false))
	f.changed("complete", 7)
	if got := f.changed("reopen", 7); got != "/ 7 null changed=true" {
		t.Errorf("reopen: %s", got)
	}
	if got := f.read("tasks/7.json"); got != stamped(richTask(false)) {
		t.Errorf("task file:\n%s", got)
	}
}

// Another write holding the lock: busy, and nothing written.
func TestCompleteBusy(t *testing.T) {
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
	if got := f.changed("complete", 7); got != "busy {}" {
		t.Errorf("got %s", got)
	}
	if got := f.read("tasks/7.json"); got != richTask(false) {
		t.Errorf("task file changed:\n%s", got)
	}
}
