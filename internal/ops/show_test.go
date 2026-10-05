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

// show runs show for id over f, checking the envelope and, on success,
// show-output.
func (f *fixture) show(id model.ID) Envelope {
	f.t.Helper()
	e := Run("show", parse(f.t, fmt.Sprintf(`{"id": %d}`, id)), nil, f.env)
	line(f.t, e)
	if e.OK {
		b, err := jsonio.MarshalLine(e.Result)
		if err != nil {
			f.t.Fatal(err)
		}
		if ok, fl := schematest.Check(f.t, "show-output", b); !ok {
			f.t.Errorf("show-output rejects at %s: %s", fl, b)
		}
	}
	return e
}

// summary is e in short: each task as "folder id readiness blocking", or the
// error as "kind details", then each warning as "kind ids paths", with the
// home as "~".
func (f *fixture) summary(e Envelope) string {
	f.t.Helper()
	var parts []string
	if e.OK {
		for _, v := range e.Result.(ShowOutput).Tasks {
			parts = append(parts, fmt.Sprintf("%s %d %s %v", v.Folder, v.ID, v.Readiness, v.Blocking))
		}
	} else {
		d, err := jsonio.MarshalLine(e.Error.Details)
		if err != nil {
			f.t.Fatal(err)
		}
		parts = append(parts, string(e.Error.Kind)+" "+strings.TrimSpace(string(d)))
	}
	for _, w := range e.Warnings {
		s := fmt.Sprintf("%s %v %v", w.Kind, w.IDs, w.Paths)
		if w.Reason != "" {
			s += " " + string(w.Reason) + " " + w.Code
		}
		parts = append(parts, strings.TrimSpace(s))
	}
	return f.rel(strings.Join(parts, "; "))
}

// The whole output of a plain task, byte for byte.
func TestShow(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, false)
	got := f.rel(line(t, f.show(7)))
	want := `{"ok":true,"result":{"tasks":[{"schema":1,"id":7,"title":"task 7","priority":null,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":[],"tags":[],"extra":{},"folder":"/proj","notes_path":"~/tasks/proj/7.md","readiness":"ready","blocking":[]}]},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestShowCases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		want  string
	}{
		// Readiness, from the task's own blocked_by.
		{"no blockers", func(f *fixture) { f.task("", 1, false) },
			"/ 1 ready []"},
		{"open blocker", func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, false) },
			"/ 1 blocked [2]"},
		{"done blocker", func(f *fixture) { f.task("", 1, false, 2); f.task("a", 2, true) },
			"/ 1 ready []"},
		{"every blocker looked at", func(f *fixture) { f.task("", 1, false, 2, 3, 4); f.task("", 2, false); f.task("", 3, true) },
			"/ 1 blocked [2 4]; dangling-reference [1 4] [~/tasks/1.json]"},
		{"done task reads no blocker", func(f *fixture) { f.task("", 1, true, 2, 3); f.write("tasks/2.json", "{}") },
			"/ 1 done []"},

		// A blocker's problems: it blocks, with a warning.
		{"dangling blocker", func(f *fixture) { f.task("", 1, false, 2) },
			"/ 1 blocked [2]; dangling-reference [1 2] [~/tasks/1.json]"},
		{"duplicated blocker", func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, true); f.task("a", 2, true) },
			"/ 1 blocked [2]; duplicate-id [2] [~/tasks/2.json ~/tasks/a/2.json]"},
		{"duplicated blocker, one copy can't be looked at", func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("", 2, true)
			f.task("a", 2, true)
			f.failFile("a/2.json", syscall.EACCES)
		}, "/ 1 blocked [2]; duplicate-id [2] [~/tasks/2.json ~/tasks/a/2.json]"},
		{"corrupt blocker", func(f *fixture) { f.task("", 1, false, 2); f.write("tasks/2.json", "{}") },
			"/ 1 blocked [2]; unusable-file [2] [~/tasks/2.json] corrupt"},
		{"unsupported blocker", func(f *fixture) { f.task("", 1, false, 2); f.write("tasks/2.json", `{"schema": 2}`) },
			"/ 1 blocked [2]; unusable-file [2] [~/tasks/2.json] unsupported-format"},
		{"unreadable blocker", func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("", 2, true)
			f.fail(fsys.OpReadFile, "2.json", syscall.EACCES)
		}, "/ 1 blocked [2]; unusable-file [2] [~/tasks/2.json] unreadable EACCES"},
		{"blocker vanished mid-read", func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("", 2, true)
			f.fail(fsys.OpReadFile, "2.json", syscall.ENOENT)
		}, "/ 1 blocked [2]; dangling-reference [1 2] [~/tasks/1.json]"},

		// The task itself.
		{"duplicated, each copy its own readiness", func(f *fixture) { f.task("a", 1, false, 2); f.task("", 1, false); f.task("", 2, false) },
			"/ 1 ready []; /a 1 blocked [2]; duplicate-id [1] [~/tasks/1.json ~/tasks/a/1.json]"},
		{"one copy vanished mid-read", func(f *fixture) {
			f.task("", 1, false)
			f.task("a", 1, false)
			f.fail(fsys.OpReadFile, "1.json", syscall.ENOENT)
		}, "/a 1 ready []"},
		{"not found", func(f *fixture) { f.task("", 2, false) },
			`not-found {"folders":[],"ids":[1],"paths":[]}`},
		{"vanished mid-read", func(f *fixture) { f.task("", 1, false); f.fail(fsys.OpReadFile, "1.json", syscall.ENOENT) },
			`not-found {"folders":[],"ids":[1],"paths":[]}`},
		{"corrupt", func(f *fixture) { f.write("tasks/1.json", "not json") },
			`corrupt {"path":"~/tasks/1.json","reason":"not-json","detail":"not valid JSON: invalid character 'o' in literal null (expecting 'u') (at byte 2)"}`},
		{"one copy corrupt", func(f *fixture) { f.task("", 1, false); f.write("tasks/a/1.json", "{}") },
			`corrupt {"path":"~/tasks/a/1.json","reason":"invalid","problems":[{"field":"/schema","reason":"required"}]}`},
		{"unsupported", func(f *fixture) { f.write("tasks/1.json", `{"schema": 2}`) },
			`unsupported-format {"path":"~/tasks/1.json","found":2,"supported":[1]}`},
		{"unreadable", func(f *fixture) { f.task("", 1, false); f.fail(fsys.OpReadFile, "1.json", syscall.EACCES) },
			`io {"path":"~/tasks/1.json","code":"EACCES"}`},
		{"unlistable folder", func(f *fixture) {
			f.task("", 1, false)
			f.task("a", 2, false)
			f.fail(fsys.OpReadDir, "a", syscall.EACCES)
		},
			`io {"path":"~/tasks/a","code":"EACCES"}`},

		// Root states, through store.Read.
		{"no config directory", func(f *fixture) { f.env.ConfigDir = "" },
			`environment {"variable":"HOME"}`},
		{"missing config", func(f *fixture) { f.remove("cfg") },
			`not-initialized {"missing":"config"}`},
		{"missing root", func(f *fixture) { f.remove("tasks") },
			`not-initialized {"missing":"root"}`},
		{"missing koan.json", func(f *fixture) { f.remove("tasks/koan.json") },
			`not-initialized {"missing":"metadata"}`},
		{"corrupt config", func(f *fixture) { f.write("cfg/config.toml", "root = 1\n") },
			`corrupt {"path":"~/cfg/config.toml","reason":"invalid","detail":"line 1: root must be a double-quoted string"}`},
		{"unsupported koan.json", func(f *fixture) { f.write("tasks/koan.json", `{"schema": 2}`) },
			`unsupported-format {"path":"~/tasks/koan.json","found":2,"supported":[1]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			if got := f.summary(f.show(1)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
