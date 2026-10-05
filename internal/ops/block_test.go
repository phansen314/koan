package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/schematest"
)

// block runs block with input over f, checking the envelope and, on success,
// block-output. It returns the result in short — "blocked_by … added …" — or
// the error as "kind details", then each warning as "kind ids paths"; the
// home as "~".
func (f *fixture) block(input string) string {
	f.t.Helper()
	e := Run("block", parse(f.t, input), nil, f.env)
	line(f.t, e)
	enc := func(v any) string {
		b, err := jsonio.MarshalLine(v)
		if err != nil {
			f.t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	var parts []string
	if e.OK {
		b := enc(e.Result)
		if ok, fl := schematest.Check(f.t, "block-output", []byte(b)); !ok {
			f.t.Errorf("block-output rejects at %s: %s", fl, b)
		}
		r := e.Result.(BlockOutput)
		parts = append(parts, fmt.Sprintf("blocked_by %v added %v", r.BlockedBy, r.Added))
	} else {
		parts = append(parts, string(e.Error.Kind)+" "+enc(e.Error.Details))
	}
	for _, w := range e.Warnings {
		parts = append(parts, fmt.Sprintf("%s %v %v", w.Kind, w.IDs, w.Paths))
	}
	return f.rel(strings.Join(parts, "; "))
}

// The whole output and file after adding blockers, byte for byte: only
// blocked_by changes.
func TestBlock(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/proj/7.json", richTask(false)) // blocked by 1 and 3
	f.write("tasks/proj/7.md", "notes\n")
	f.task("", 1, false)
	f.task("", 2, true)
	f.task("", 3, false)
	f.task("x", 10, false)
	e := Run("block", parse(t, `{"id": 7, "blockers": [10, 3, 2]}`), nil, f.env)
	got := f.rel(line(t, e))
	want := `{"ok":true,"result":{"schema":1,"id":7,"title":"Pack bags","priority":-2,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-28T12:00:00Z","blocked_by":[1,2,3,10],"tags":["travel"],"extra":{"n":1.50,"deep":{"k":[true,null]}},"folder":"/proj","notes_path":"~/tasks/proj/7.md","added":[2,10]},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	wantFile := strings.Replace(stamped(richTask(false)), "\"blocked_by\": [\n    1,\n    3\n  ]", "\"blocked_by\": [\n    1,\n    2,\n    3,\n    10\n  ]", 1)
	if got := f.read("tasks/proj/7.json"); got != wantFile {
		t.Errorf("task file:\n%s\nwant\n%s", got, wantFile)
	}
	if got := f.read("tasks/proj/7.md"); got != "notes\n" {
		t.Errorf(".md %q", got)
	}
}

func TestBlockCases(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want        string
	}{
		// Adding.
		{"one blocker", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.task("", 2, false) },
			"blocked_by [2] added [2]"},
		{"complete task, complete blocker", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, true); f.task("", 2, true) },
			"blocked_by [2] added [2]"},
		{"some already present", `{"id": 1, "blockers": [3, 2]}`, func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, false); f.task("", 3, false) },
			"blocked_by [2 3] added [3]"},

		// Blockers already present are not checked at all.
		{"all present, one missing and one cyclic", `{"id": 1, "blockers": [2, 9]}`, func(f *fixture) { f.task("", 1, false, 2, 9); f.task("", 2, false, 1) },
			"blocked_by [2 9] added []"},
		{"present blocker's file corrupt", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false, 2); f.write("tasks/2.json", "{") },
			"blocked_by [2] added []"},

		// not-found: id and every missing new blocker, together.
		{"missing blockers", `{"id": 1, "blockers": [9, 2, 8]}`, func(f *fixture) { f.task("", 1, false); f.task("", 2, false) },
			`not-found {"folders":[],"ids":[8,9],"paths":[]}`},
		{"missing id and blockers", `{"id": 5, "blockers": [9, 2]}`, func(f *fixture) { f.task("", 2, false) },
			`not-found {"folders":[],"ids":[5,9],"paths":[]}`},
		{"blocker vanished", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false)
			f.fail(fsys.OpReadFile, "2.json", syscall.ENOENT)
		}, `not-found {"folders":[],"ids":[2],"paths":[]}`},

		// id's own copies come first.
		{"corrupt id before a missing blocker", `{"id": 1, "blockers": [9]}`, func(f *fixture) { f.write("tasks/1.json", "{") },
			`corrupt {"path":"~/tasks/1.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"duplicate id", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.task("p", 1, false); f.task("", 2, false) },
			`conflict {"rule":"duplicate-id","ids":[1]}`},
		{"missing blocker before duplicate id", `{"id": 1, "blockers": [9]}`, func(f *fixture) { f.task("", 1, false); f.task("p", 1, false) },
			`not-found {"folders":[],"ids":[9],"paths":[]}`},
		{"a copy's blocked_by makes a blocker present", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.task("p", 1, false, 2) },
			`conflict {"rule":"duplicate-id","ids":[1]}`},
		{"corrupt reachable file before duplicate id", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("p", 1, false)
			f.task("", 2, false, 3)
			f.write("tasks/3.json", "{")
		}, `corrupt {"path":"~/tasks/3.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},

		// Blockers' own files.
		{"duplicated blocker: a warning", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.task("", 2, false); f.task("p", 2, true) },
			"blocked_by [2] added [2]; duplicate-id [2] [~/tasks/2.json ~/tasks/p/2.json]"},
		{"corrupt blocker", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.write("tasks/2.json", "{") },
			`corrupt {"path":"~/tasks/2.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"unsupported blocker", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.write("tasks/2.json", `{"schema": 2}`) },
			`unsupported-format {"path":"~/tasks/2.json","found":2,"supported":[1]}`},
		{"not-found before a corrupt blocker", `{"id": 1, "blockers": [2, 9]}`, func(f *fixture) { f.task("", 1, false); f.write("tasks/2.json", "{") },
			`not-found {"folders":[],"ids":[9],"paths":[]}`},

		// Cycles.
		{"direct cycle", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.task("", 2, false, 1) },
			`conflict {"rule":"acyclic","ids":[2],"cycles":[[1,2]]}`},
		{"long cycle", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false, 3)
			f.task("a", 3, false, 4)
			f.task("b", 4, true, 1)
		}, `conflict {"rule":"acyclic","ids":[2],"cycles":[[1,2,3,4]]}`},
		{"through complete tasks", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, true); f.task("", 2, true, 3); f.task("", 3, true, 1) },
			`conflict {"rule":"acyclic","ids":[2],"cycles":[[1,2,3]]}`},
		{"every offending blocker, and nothing written", `{"id": 1, "blockers": [4, 3, 2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false, 1)
			f.task("", 3, false)
			f.task("", 4, false, 5)
			f.task("", 5, false, 1)
		}, `conflict {"rule":"acyclic","ids":[2,4],"cycles":[[1,2],[1,4,5]]}`},
		{"shortest cycle, then smallest", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false, 6, 4, 3)
			f.task("", 3, false, 7)
			f.task("", 4, false, 5)
			f.task("", 5, false, 1)
			f.task("", 6, false, 1)
			f.task("", 7, false, 1)
		}, `conflict {"rule":"acyclic","ids":[2],"cycles":[[1,2,6]]}`},
		{"equal length: the smallest IDs", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false, 4, 3)
			f.task("", 3, false, 1)
			f.task("", 4, false, 1)
		}, `conflict {"rule":"acyclic","ids":[2],"cycles":[[1,2,3]]}`},
		{"through a duplicate's second copy", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false, 3)
			f.task("", 3, false)
			f.task("p", 3, false, 1)
		}, `conflict {"rule":"acyclic","ids":[2],"cycles":[[1,2,3]]}`},
		{"missing ID down the chain: no edges, no warning", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.task("", 2, false, 99) },
			"blocked_by [2] added [2]"},
		{"reaching id's dependents is no cycle", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false, 3)
			f.task("", 3, false)
			f.task("", 4, false, 1, 2)
		}, "blocked_by [2] added [2]"},

		// Every reachable file is needed: a corrupt one beats a cycle, even
		// one ordered after id (implementation-spec.md, Cycle-check tests).
		{"corrupt file after id beats acyclic", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false, 1, 3)
			f.write("tasks/z/3.json", "{")
		}, `corrupt {"path":"~/tasks/z/3.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"first unusable in tree order", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false, 3, 4)
			f.write("tasks/z/3.json", "{")
			f.write("tasks/a/4.json", "{")
		}, `corrupt {"path":"~/tasks/a/4.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"unreachable corrupt file ignored", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false); f.task("", 2, false); f.write("tasks/3.json", "{") },
			"blocked_by [2] added [2]"},
		{"id is never expanded", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false, 3); f.task("", 2, false); f.write("tasks/3.json", "{") },
			"blocked_by [2 3] added [2]"},
		{"id reached but not expanded", `{"id": 1, "blockers": [2]}`, func(f *fixture) { f.task("", 1, false, 3); f.task("", 2, false, 1); f.write("tasks/3.json", "{") },
			`conflict {"rule":"acyclic","ids":[2],"cycles":[[1,2]]}`},
		{"unlistable folder", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false)
			f.mkdir("tasks/p")
			f.fail(fsys.OpReadDir, "p", syscall.EACCES)
		}, `io {"path":"~/tasks/p","code":"EACCES"}`},
		{"replace fails", `{"id": 1, "blockers": [2]}`, func(f *fixture) {
			f.task("", 1, false)
			f.task("", 2, false)
			f.failAt(fsys.OpRename, "1.json", syscall.ENOSPC)
		},
			`io {"path":"~/tasks/1.json","code":"ENOSPC"}`},

		// Input.
		{"a task can't block itself", `{"id": 1, "blockers": [2, 1]}`, func(f *fixture) {},
			`invalid-input {"problems":[{"field":"/blockers/1","reason":"a task cannot block itself"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			before := f.read("tasks/1.json")
			got := f.block(tc.input)
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if !strings.HasPrefix(got, "blocked_by") || strings.Contains(got, "added []") {
				if after := f.read("tasks/1.json"); after != before {
					t.Errorf("task file changed:\n%s", after)
				}
			}
		})
	}
}

// Adding nothing new doesn't touch the file: its modification time stays.
func TestBlockNothingNewNotWritten(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false, 2)
	f.task("", 2, false)
	p := filepath.Join(f.root, "1.json")
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	f.block(`{"id": 1, "blockers": [2]}`)
	if fi, err := os.Stat(p); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("task file rewritten: %v %v", fi.ModTime(), err)
	}
}

// Another write holding the lock: busy, and nothing written.
func TestBlockBusy(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	f.task("", 2, false)
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
	if got := f.block(`{"id": 1, "blockers": [2]}`); got != "busy {}" {
		t.Errorf("got %s", got)
	}
	if got := f.read("tasks/1.json"); got != before {
		t.Errorf("task file changed:\n%s", got)
	}
}

// Racing blocks (implementation-spec.md, Lock, 6): "block A by B" and "block
// B by A" at once, each retrying on busy: in every round exactly one
// succeeds and the other is refused as a cycle. flock locks per open file,
// so goroutines contend as processes do; e2e will run it across processes.
func TestBlockRacing(t *testing.T) {
	for round := range 50 {
		f := newFixture(t)
		f.task("", 1, false)
		f.task("", 2, false)
		results := make([]Envelope, 2)
		var wg sync.WaitGroup
		for i, input := range []string{`{"id": 1, "blockers": [2]}`, `{"id": 2, "blockers": [1]}`} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					e := Run("block", parse(t, input), nil, f.env)
					if e.OK || e.Error.Kind != errs.KindBusy {
						results[i] = e
						return
					}
				}
			}()
		}
		wg.Wait()
		ok, acyclic := 0, 0
		for _, e := range results {
			switch {
			case e.OK:
				ok++
			case e.Error.Kind == errs.KindConflict && e.Error.Details.(errs.ConflictDetails).Rule == errs.RuleAcyclic:
				acyclic++
			}
		}
		if ok != 1 || acyclic != 1 {
			t.Fatalf("round %d: %d succeeded, %d refused as a cycle: %s / %s", round, ok, acyclic, line(t, results[0]), line(t, results[1]))
		}
	}
}
