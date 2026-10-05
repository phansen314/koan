package ops

import (
	"fmt"
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

// unblock runs unblock with input over f, checking the envelope and, on
// success, unblock-output. It returns the result in short — "blocked_by …
// removed …" — or the error as "kind details"; the home as "~".
func (f *fixture) unblock(input string) string {
	f.t.Helper()
	e := Run("unblock", parse(f.t, input), nil, f.env)
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
	if ok, fl := schematest.Check(f.t, "unblock-output", []byte(b)); !ok {
		f.t.Errorf("unblock-output rejects at %s: %s", fl, b)
	}
	r := e.Result.(UnblockOutput)
	return fmt.Sprintf("blocked_by %v removed %v", r.BlockedBy, r.Removed)
}

// The whole output and file after removing blockers, byte for byte: only
// blocked_by changes.
func TestUnblock(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/proj/7.json", richTask(false)) // blocked by 1 and 3
	f.write("tasks/proj/7.md", "notes\n")
	e := Run("unblock", parse(t, `{"id": 7, "blockers": [3, 9]}`), nil, f.env)
	got := f.rel(line(t, e))
	want := `{"ok":true,"result":{"schema":1,"id":7,"title":"Pack bags","priority":-2,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-28T12:00:00Z","blocked_by":[1],"tags":["travel"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"folder":"/proj","notes_path":"~/tasks/proj/7.md","removed":[3]},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	wantFile := strings.Replace(stamped(richTask(false)), "\"blocked_by\": [\n    1,\n    3\n  ]", "\"blocked_by\": [\n    1\n  ]", 1)
	if got := f.read("tasks/proj/7.json"); got != wantFile {
		t.Errorf("task file:\n%s\nwant\n%s", got, wantFile)
	}
	if got := f.read("tasks/proj/7.md"); got != "notes\n" {
		t.Errorf(".md %q", got)
	}
}

func TestUnblockCases(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want        string
		intact      bool // tasks/1.json not rewritten
	}{
		{"remove one", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false, 2, 3); f.task("", 2, false); f.task("", 3, false) },
			"blocked_by [3] removed [2]", false},
		{"remove all", `{"id": 1, "blockers": [3, 2]}`, func(f *fixture) { f.task("", 1, true, 2, 3) },
			"blocked_by [] removed [2 3]", false},
		{"not there: nothing removed", `{"id": 1, "blockers": [4, 1]}`, func(f *fixture) { f.task("", 1, false, 2) },
			"blocked_by [2] removed []", true},
		{"a dangling reference cleared", `{"id": 1, "blockers": [99]}`, func(f *fixture) { f.task("", 1, false, 2, 99); f.task("", 2, false) },
			"blocked_by [2] removed [99]", false},
		{"blockers' files never read", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false, 2, 3)
			f.write("tasks/2.json", "{")
			f.write("tasks/3.json", "{")
		},
			"blocked_by [3] removed [2]", false},
		{"a duplicated blocker is removed like any", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, false); f.task("p", 2, false) },
			"blocked_by [] removed [2]", false},

		// Finding the one task.
		{"not found", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 2, false) },
			`not-found {"folders":[],"ids":[1],"paths":[]}`, true},
		{"corrupt", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.write("tasks/1.json", "{") },
			`corrupt {"path":"~/tasks/1.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, true},
		{"duplicate", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false, 2); f.task("p", 1, false, 2) },
			`conflict {"rule":"duplicate-id","ids":[1]}`, true},
		{"unlistable folder", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false, 2)
			f.mkdir("tasks/p")
			f.fail(fsys.OpReadDir, "p", syscall.EACCES)
		},
			`io {"path":"~/tasks/p","code":"EACCES"}`, true},
		{"replace fails", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false, 2); f.failAt(fsys.OpRename, "1.json", syscall.ENOSPC) },
			`io {"path":"~/tasks/1.json","code":"ENOSPC"}`, true},
		{"empty blockers", `{"id": 1, "blockers": []}`, func(f *fixture) { f.task("", 1, false, 2) },
			`invalid-input {"problems":[{"field":"/blockers","reason":"must list at least one ID"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			before := f.read("tasks/1.json")
			if got := f.unblock(tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if after := f.read("tasks/1.json"); tc.intact && after != before {
				t.Errorf("task file changed:\n%s", after)
			}
		})
	}
}

// Removing nothing doesn't touch the file: its modification time stays.
func TestUnblockNothingRemovedNotWritten(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false, 2)
	p := filepath.Join(f.root, "1.json")
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	f.unblock(`{"id": 1, "blockers": [3]}`)
	if fi, err := os.Stat(p); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("task file rewritten: %v %v", fi.ModTime(), err)
	}
}

// Another write holding the lock: busy, and nothing written.
func TestUnblockBusy(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false, 2)
	before := f.read("tasks/1.json")
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
	if got := f.unblock(`{"id": 1, "blockers": [2]}`); got != "busy {}" {
		t.Errorf("got %s", got)
	}
	if got := f.read("tasks/1.json"); got != before {
		t.Errorf("task file changed:\n%s", got)
	}
}
