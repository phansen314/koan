package pick

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/jsonio"
)

// blocking is a test action with a choose list of the other shown tasks:
// the chosen block each target.
var blocking = action{key: "z", arity: anyTargets,
	run: func(r *actionRun, targets []shownLine) {
		var sh shown
		if r.err = readJSON(r.s, shownFile, &sh); r.err != nil {
			return
		}
		var lines []string
		for _, l := range sh.Lines {
			if l.Key != targets[0].Key {
				lines = append(lines, l.Key+"\t"+string(idNumber(l.ID)))
			}
		}
		r.openChoose("z", targetLabel("blockers of", targets), lines, false, targets)
	},
	choose: func(r *actionRun, chosen []string, targets []shownLine) {
		var blockers []any
		for _, k := range chosen {
			id, _, _ := strings.Cut(k, "@")
			blockers = append(blockers, json.Number(id))
		}
		for _, t := range targets {
			in := &jsonio.Object{}
			in.Set("id", idNumber(t.ID))
			in.Set("blockers", blockers)
			r.call(t.ID, "block", in, "blocked", "block "+string(idNumber(t.ID)))
		}
	}}

// moving is a test action with a single-choice list of folders.
var moving = action{key: "y", arity: anyTargets,
	run: func(r *actionRun, targets []shownLine) {
		r.openChoose("y", "move to> ", []string{"/\t/", "/a\t/a", "/b\t/b"}, true, targets)
	},
	choose: func(r *actionRun, chosen []string, targets []shownLine) {
		for _, t := range targets {
			in := &jsonio.Object{}
			in.Set("id", idNumber(t.ID))
			in.Set("to", chosen[0])
			r.call(t.ID, "move", in, "moved", "move "+string(idNumber(t.ID)))
		}
	}}

func TestChoose(t *testing.T) {
	withAction(t, blocking)
	withAction(t, moving)
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/a"})
	tr.run("create-folder", map[string]any{"folder": "/b"})
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	keys := strings.Join(commandKeys(), ",")
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		text := func(name string) string { return helper("text", name) }
		helper("command", "o")

		got := helper("act", "z", "1@/")
		want := "show-input+unbind(" + keys + ")+transform-prompt('/bin/ftask' __pick text 'prompt')+change-query()+clear-selection+rebind(load)+reload-sync('/bin/ftask' __pick choices)+transform-header('/bin/ftask' __pick text 'header')"
		if got != want {
			t.Errorf("z printed\n%q\nwant\n%q", got, want)
		}
		if p, h, c := text("prompt"), text("header"), helper("choices"); p != "blockers of 1> " ||
			h != "[blockers of 1]\n/\n"+chooseHint || c != "~2@/\t2\n~3@/\t3\n" {
			t.Errorf("prompt %q, header %q, choices %q", p, h, c)
		}
		// Enter on two marked: the action applies, then back to the task
		// list, in command mode, with the search query back.
		got = helper("enter", "", "~3@/", "~2@/")
		for _, part := range []string{
			"enable-search+transform-query('/bin/ftask' __pick text 'query')+transform-prompt(",
			"+rebind(" + keys + ",tab)+",
			"+rebind(load)+clear-selection+reload-sync('/bin/ftask' __pick lines)+",
		} {
			if !strings.Contains(got, part) {
				t.Errorf("enter printed %q, without %q", got, part)
			}
		}
		if f, q, h := text("footer"), text("query"), text("header"); f != "✓ blocked 1" || q != "o" || !strings.HasPrefix(h, "[cmd] query: o\n") {
			t.Errorf("footer %q, query %q, header %q", f, q, h)
		}
		// Back on the target, 1, now blocked, so after the ready 2 and 3.
		if got := helper("on-load"); got != "hide-input+pos(3)+unbind(load)" {
			t.Errorf("on-load %q", got)
		}

		// Nothing to choose: no change.
		helper("act", "z", "2@/")
		helper("enter", "")
		if f := text("footer"); f != "no change" {
			t.Errorf("none chosen: %q", f)
		}

		// Cancelled, by Esc and by ctrl-space: the task list as it was.
		for _, cancel := range []string{"esc", "command"} {
			helper("act", "z", "2@/")
			got := helper(cancel, "1")
			if !strings.HasPrefix(got, "enable-search+transform-query(") || !strings.HasSuffix(got, "+rebind(load)+clear-selection+reload-sync('/bin/ftask' __pick lines)") {
				t.Errorf("%s printed %q", cancel, got)
			}
			if h := text("header"); !strings.HasPrefix(h, "[cmd] query: o\n") {
				t.Errorf("%s: header %q", cancel, h)
			}
		}

		// Single choice: Tab unbound, one line taken.
		if got := helper("act", "y", "3@/"); !strings.HasPrefix(got, "show-input+unbind("+keys+",tab)+") {
			t.Errorf("y printed %q", got)
		}
		if h := text("header"); h != "[move to]\n/\n"+pickOneHint {
			t.Errorf("single header %q", h)
		}
		helper("enter", "", "~/b")
		if f := text("footer"); f != "✓ moved 3" {
			t.Errorf("moved: %q", f)
		}
		helper("enter", "", "1@/")
	}})
	res := string(line)
	for _, want := range []string{
		`"input":{"id":1,"blockers":[2,3]}`,
		`"input":{"id":3,"to":"/b"}`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("no %s in %s", want, res)
		}
	}
}

// A key pressed while fzf still shows the other list passes that list's
// keys: it does nothing, and says so. Enter or an action just after a
// choose list closes passes the choose list's; Enter just after one opens,
// the task list's.
func TestStaleKeys(t *testing.T) {
	withAction(t, blocking)
	tr := newTestTree(t)
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		footer := "transform-footer('/bin/ftask' __pick text 'footer')"
		helper("command", "")
		helper("act", "z", "1@/")
		// Enter with the task list's key: the choose list stays.
		if got := helper("enter", "", "2@/"); got != footer {
			t.Errorf("enter, task key, choosing: %q", got)
		}
		if f, h := helper("text", "footer"), helper("text", "header"); f != stillLoading || !strings.HasPrefix(h, "[blockers of 1]") {
			t.Errorf("footer %q, header %q", f, h)
		}
		helper("enter", "", "~2@/")
		// Back in command mode, with the choose list's keys.
		for _, args := range [][]string{{"enter", "", "~3@/"}, {"act", "c", "~3@/"}} {
			if got := helper(args...); got != footer {
				t.Errorf("%q printed %q", args, got)
			}
			if f := helper("text", "footer"); f != stillLoading {
				t.Errorf("%q: footer %q", args, f)
			}
		}
		helper("enter", "", "1@/")
	}})
	res := string(line)
	if !strings.Contains(res, `"tasks":[{"schema":1,"id":1,`) || strings.Count(res, `"operation"`) != 1 || !strings.Contains(res, `"input":{"id":1,"blockers":[2]}`) {
		t.Errorf("%s", res)
	}
}

// A session failure after leaving a choose list for command mode still
// takes fzf there, with the failure in the status line: the session's mode
// and fzf's agree.
func TestLeaveFailsAfterCommit(t *testing.T) {
	withAction(t, blocking)
	tr := newTestTree(t)
	for _, title := range []string{"one", "two"} {
		tr.run("create", map[string]any{"title": title})
	}
	tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		helper("act", "z", "1@/")
		// Arming the cursor, back on the target, fails.
		helper("on-load")
		if err := os.MkdirAll(filepath.Join(tr.session, cursorFile, "x"), 0o700); err != nil {
			t.Fatal(err)
		}
		got := helper("enter", "", "~2@/")
		if !strings.HasPrefix(got, "enable-search+") || !strings.Contains(got, "+rebind(load)+clear-selection+reload-sync(") ||
			!strings.HasSuffix(got, "+transform-footer('/bin/ftask' __pick text 'footer')") {
			t.Errorf("enter printed %q", got)
		}
		if f := helper("text", "footer"); !strings.HasPrefix(f, "✗ io: ") {
			t.Errorf("footer %q", f)
		}
		if h := helper("text", "header"); !strings.HasPrefix(h, "[cmd]") {
			t.Errorf("header %q", h)
		}
		helper("quit")
	}})
}
