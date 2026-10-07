package ops

import (
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
)

// why runs why on input over f, checking the envelope and, on success,
// why-output.
func (f *fixture) why(input string) Envelope {
	f.t.Helper()
	e := Run("why", parse(f.t, input), nil, f.env)
	line(f.t, e)
	if e.OK {
		b, err := jsonio.MarshalLine(e.Result)
		if err != nil {
			f.t.Fatal(err)
		}
		if ok, fl := schematest.Check(f.t, "why-output", b); !ok {
			f.t.Errorf("why-output rejects at %s: %s", fl, b)
		}
	}
	return e
}

// whySummary is e in short: "readiness ready=… stuck=…", then each task as
// "id:blocking", or the error as "kind details"; then each warning as "kind
// ids paths", with the home as "~".
func (f *fixture) whySummary(e Envelope) string {
	f.t.Helper()
	var parts []string
	if e.OK {
		out := e.Result.(WhyOutput)
		parts = append(parts, fmt.Sprintf("%s ready=%v stuck=%v", out.Readiness, out.Ready, out.Stuck))
		if out.Tasks != nil {
			var ts []string
			for _, v := range out.Tasks.Views {
				ts = append(ts, fmt.Sprintf("%d:%v", v.ID, v.Blocking))
			}
			parts = append(parts, "tasks "+strings.Join(ts, " "))
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

// The whole output, byte for byte: without tasks, and with them projected.
func TestWhy(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 1, false, 2)
	f.task("", 2, false)
	if got, want := line(t, f.why(`{"id": 1}`)), `{"ok":true,"result":{"readiness":"blocked","ready":[2],"stuck":[]},"warnings":[]}`+"\n"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	got := line(t, f.why(`{"id": 1, "include_tasks": true, "fields": ["readiness", "blocking"]}`))
	want := `{"ok":true,"result":{"readiness":"blocked","ready":[2],"stuck":[],"tasks":[{"id":1,"readiness":"blocked","blocking":[2]},{"id":2,"readiness":"ready","blocking":[]}]},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestWhyCases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		want  string
	}{
		// The task itself.
		{"ready", func(f *fixture) { f.task("", 1, false) },
			"ready ready=[1] stuck=[]; tasks 1:[]"},
		{"ready, done blockers not followed", func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, true, 3); f.task("", 3, false) },
			"ready ready=[1] stuck=[]; tasks 1:[]"},
		{"done reads no blocker", func(f *fixture) { f.task("", 1, true, 2, 3); f.write("tasks/2.json", "{}") },
			"done ready=[] stuck=[]; tasks 1:[]"},
		{"duplicated", func(f *fixture) { f.task("", 1, false); f.task("a", 1, false) },
			`conflict {"rule":"duplicate-id","ids":[1]}`},
		{"one copy vanished mid-read", func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("a", 1, false)
			f.task("", 2, false)
			f.fail(fsys.OpReadFile, "1.json", syscall.ENOENT)
		}, "ready ready=[1] stuck=[]; tasks 1:[]"},
		{"not found", func(f *fixture) { f.task("", 2, false) },
			`not-found {"folders":[],"ids":[1],"paths":[]}`},
		{"corrupt", func(f *fixture) { f.write("tasks/1.json", `{"schema": 2}`) },
			`unsupported-format {"path":"~/tasks/1.json","found":2,"supported":[1]}`},
		{"unlistable folder", func(f *fixture) {
			f.task("", 1, false)
			f.task("a", 2, false)
			f.fail(fsys.OpReadDir, "a", syscall.EACCES)
		}, `io {"path":"~/tasks/a","code":"EACCES"}`},

		// Following blockers.
		{"chain", func(f *fixture) { f.task("", 1, false, 2); f.task("a", 2, false, 3); f.task("b", 3, false) },
			"blocked ready=[3] stuck=[]; tasks 1:[2] 2:[3] 3:[]"},
		{"done blocker on the way", func(f *fixture) { f.task("", 1, false, 2, 3); f.task("", 2, true); f.task("", 3, false) },
			"blocked ready=[3] stuck=[]; tasks 1:[3] 3:[]"},
		{"diamond, each task once", func(f *fixture) {
			f.task("", 1, false, 2, 3)
			f.task("", 2, false, 4)
			f.task("", 3, false, 4)
			f.task("", 4, false)
		}, "blocked ready=[4] stuck=[]; tasks 1:[2 3] 2:[4] 3:[4] 4:[]"},
		{"nearest first, then by ID", func(f *fixture) {
			f.task("", 1, false, 5, 2)
			f.task("", 5, false, 3)
			f.task("", 2, false, 9)
			f.task("", 3, false)
			f.task("", 9, false, 3)
		}, "blocked ready=[3] stuck=[]; tasks 1:[2 5] 2:[9] 5:[3] 3:[] 9:[3]"},
		{"ready in frontier order", func(f *fixture) {
			f.task("", 1, false, 2, 3, 4, 5)
			f.task("", 2, false)
			f.prioritized("", 3, 1)
			f.prioritized("", 4, 5)
			f.task("", 5, false)
		}, "blocked ready=[4 3 2 5] stuck=[]; tasks 1:[2 3 4 5] 2:[] 3:[] 4:[] 5:[]"},

		// Stuck: blockers no work clears.
		{"dangling blocker", func(f *fixture) { f.task("", 1, false, 2, 3); f.task("", 3, false) },
			"blocked ready=[3] stuck=[2]; tasks 1:[2 3] 3:[]; dangling-reference [1 2] [~/tasks/1.json]"},
		{"deep dangling blocker", func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, false, 7) },
			"blocked ready=[] stuck=[7]; tasks 1:[2] 2:[7]; dangling-reference [2 7] [~/tasks/2.json]"},
		{"duplicated blocker not followed", func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("", 2, false, 3)
			f.task("a", 2, false)
			f.task("", 3, false)
		},
			"blocked ready=[] stuck=[2]; tasks 1:[2]; duplicate-id [2] [~/tasks/2.json ~/tasks/a/2.json]"},
		{"unusable blocker", func(f *fixture) { f.task("", 1, false, 2); f.write("tasks/2.json", "{}") },
			"blocked ready=[] stuck=[2]; tasks 1:[2]; unusable-file [2] [~/tasks/2.json] corrupt"},
		{"stuck reached twice, listed once", func(f *fixture) { f.task("", 1, false, 2, 3); f.task("", 2, false, 9); f.task("", 3, false, 9) },
			"blocked ready=[] stuck=[9]; tasks 1:[2 3] 2:[9] 3:[9]; dangling-reference [2 9] [~/tasks/2.json]; dangling-reference [3 9] [~/tasks/3.json]"},
		{"cycle below the task", func(f *fixture) {
			f.task("", 1, false, 2)
			f.task("", 2, false, 3)
			f.task("", 3, false, 4)
			f.task("", 4, false, 2, 5)
			f.task("", 5, false)
		}, "blocked ready=[5] stuck=[2 3 4]; tasks 1:[2] 2:[3] 3:[4] 4:[2 5] 5:[]; cycle [2 3 4 2] []"},
		{"task in a cycle", func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, false, 1) },
			"blocked ready=[] stuck=[1 2]; tasks 1:[2] 2:[1]; cycle [1 2 1] []"},
		{"cycle through a done task is no cycle", func(f *fixture) { f.task("", 1, false, 2); f.task("", 2, true, 1) },
			"ready ready=[1] stuck=[]; tasks 1:[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			if got := f.whySummary(f.why(`{"id": 1, "include_tasks": true}`)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// include_tasks and fields change only what is returned: the answer and the
// warnings are the same.
func TestWhyTasksChangeNothingElse(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false, 2, 3)
	f.task("", 2, false, 1)
	f.task("", 3, false, 4)
	without := f.whySummary(f.why(`{"id": 1}`))
	with := f.whySummary(f.why(`{"id": 1, "include_tasks": true, "fields": ["title"]}`))
	if want := "blocked ready=[] stuck=[1 2 4]; cycle [1 2 1] []; dangling-reference [3 4] [~/tasks/3.json]"; without != want {
		t.Errorf("without tasks: got %s\nwant %s", without, want)
	}
	if want := "blocked ready=[] stuck=[1 2 4]; tasks 1:[2 3] 2:[1] 3:[4]; cycle [1 2 1] []; dangling-reference [3 4] [~/tasks/3.json]"; with != want {
		t.Errorf("with tasks: got %s\nwant %s", with, want)
	}
}

// fields without include_tasks is Additional validation, at /fields.
func TestWhyFieldsNeedTasks(t *testing.T) {
	for _, doc := range []string{`{"id": 1, "fields": ["title"]}`, `{"id": 1, "include_tasks": false, "fields": ["title"]}`} {
		if ps := problems(t, "why", doc); len(ps) != 1 || ps[0].Field != "/fields" {
			t.Errorf("%s: got %v, want one problem at /fields", doc, ps)
		}
	}
}
