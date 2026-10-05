package pick

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/schematest"
)

func TestStatusLine(t *testing.T) {
	ok := func(id model.ID) outcome { return outcome{id: id, done: "done", what: "done"} }
	failed := func(id model.ID, e *errs.Error) outcome {
		return outcome{id: id, what: "done " + string(rune('0'+id)), err: e}
	}
	busy := &errs.Error{Kind: errs.KindBusy, Message: "busy"}
	dup := &errs.Error{Kind: errs.KindConflict, Message: "duplicated", Details: map[string]any{"rule": "duplicate-id"}}
	var many []outcome
	for i := range 38 {
		many = append(many, ok(model.ID(100+i)))
	}
	for _, tc := range []struct {
		name string
		outs []outcome
		want string
	}{
		{"none", nil, ""},
		{"one", []outcome{ok(42)}, "✓ done 42"},
		{"one failed", []outcome{failed(4, dup)}, "✗ done 4: conflict (duplicate-id): duplicated"},
		{"several", []outcome{ok(1), ok(2)}, "✓ done 2: 1, 2"},
		{"groups, successes first", []outcome{failed(3, busy), ok(1), {id: 5, done: "reopened"}, ok(2), failed(4, dup)},
			"✓ done 2: 1, 2 · ✓ reopened 1: 5 · ✗ 2 failed: 3 busy, 4 conflict (duplicate-id)"},
		{"five IDs, then +N", many, "✓ done 38: 100, 101, 102, 103, 104 +33"},
	} {
		if got := statusLine(tc.outs); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The status line is cut to the terminal, less the list's margin, with …;
// cells, not bytes, and whole when the width is unknown.
func TestFooterCut(t *testing.T) {
	s := testSession(t)
	s.Write(textPrefix+"footer", []byte("✓ done 38: 100, 101, 102 · 2 warnings"))
	for cols, want := range map[string]string{
		"":   "✓ done 38: 100, 101, 102 · 2 warnings",
		"0":  "✓ done 38: 100, 101, 102 · 2 warnings",
		"20": "✓ done 38: 100, 1…",
		"99": "✓ done 38: 100, 101, 102 · 2 warnings",
	} {
		env := Env{Sys: System{Environ: func() []string { return []string{"FZF_COLUMNS=" + cols} }}}
		if out, _ := text(s, []string{"footer"}, env); string(out) != want {
			t.Errorf("%q columns: %q", cols, out)
		}
	}
}

func testSession(t *testing.T) *Session {
	t.Helper()
	s, e := newSession(fsys.OS{}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Remove)
	return s
}

// withAction registers a for the test.
func withAction(t *testing.T, a action) {
	t.Helper()
	saved := actions
	actions = append(slices.Clone(actions), a)
	t.Cleanup(func() { actions = saved })
}

// completing is a test action: mark each target done, as one call each.
func completing(key string, ar arity) action {
	return action{key: key, arity: ar, run: func(r *actionRun, targets []shownLine) {
		for _, tg := range targets {
			in := &jsonio.Object{}
			in.Set("id", idNumber(tg.ID))
			r.call(tg.ID, "done", in, "done", "done "+itoa(tg.ID))
		}
	}}
}

func itoa(id model.ID) string { return string(idNumber(id)) }

// An action through pick whole, with fzf faked: targets in line order, one
// logged call each, then a reload, cleared marks and the status line; every
// call in the output's actions.
func TestAct(t *testing.T) {
	withAction(t, completing("z", anyTargets))
	withAction(t, completing("y", oneTarget))
	withAction(t, action{key: "w", arity: noTargets, run: func(r *actionRun, targets []shownLine) {
		if targets != nil {
			t.Errorf("noTargets got %v", targets)
		}
		r.status = "✓ nothing to do"
	}})
	tr := newTestTree(t)
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	footer := func(helper func(...string) string) string { return helper("text", "footer") }
	reloaded := "clear-selection+reload-sync('/bin/koan' __pick lines)+transform-header('/bin/koan' __pick text 'header')+transform-footer('/bin/koan' __pick text 'footer')"

	out, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		// One target, refused with several: nothing runs, nothing reloads.
		if got := helper("act", "y", "3@/", "1@/"); got != "transform-footer('/bin/koan' __pick text 'footer')" {
			t.Errorf("y printed %q", got)
		}
		if got := footer(helper); got != "✗ y takes one task: 2 marked" {
			t.Errorf("footer %q", got)
		}
		// Marked 3 then 1: run in line order.
		if got := helper("act", "z", "3@/", "1@/"); got != reloaded {
			t.Errorf("z printed %q", got)
		}
		if got := footer(helper); got != "✓ done 2: 1, 3" {
			t.Errorf("footer %q", got)
		}
		// The reload's lines: the open scope has only 2 left.
		if got := helper("lines"); strings.Count(got, "\n") != 1 || !strings.HasPrefix(got, "2@/\t") {
			t.Errorf("lines %q", got)
		}
		// No target: nothing at all.
		if got := helper("act", "z"); got != "" {
			t.Errorf("no target printed %q", got)
		}
		// A failed call is reported, and the list reloaded.
		tr.run("delete", map[string]any{"id": 2})
		if got := helper("act", "y", "2@/"); got != reloaded {
			t.Errorf("failing y printed %q", got)
		}
		if got := footer(helper); !strings.HasPrefix(got, "✗ done 2: not-found: ") {
			t.Errorf("footer %q", got)
		}
		// An action with no targets ignores the cursor, and runs nothing
		// here, so nothing reloads.
		if got := helper("act", "w", "1@/"); got != "clear-selection+transform-footer('/bin/koan' __pick text 'footer')" {
			t.Errorf("w printed %q", got)
		}
		if got := footer(helper); got != "✓ nothing to do" {
			t.Errorf("footer %q", got)
		}
		helper("quit")
	}})
	if !out.OK {
		t.Fatalf("%s", line)
	}
	res := result(t, line)
	var got struct {
		Actions []struct {
			Operation string         `json:"operation"`
			Input     map[string]any `json:"input"`
			Output    struct {
				OK    bool `json:"ok"`
				Error *struct {
					Kind string `json:"kind"`
				} `json:"error"`
			} `json:"output"`
		} `json:"actions"`
	}
	json.Unmarshal(res, &got)
	var summary []string
	for _, a := range got.Actions {
		s := a.Operation + " " + itoa(model.ID(a.Input["id"].(float64)))
		if a.Output.Error != nil {
			s += " " + a.Output.Error.Kind
		}
		summary = append(summary, s)
	}
	if want := []string{"done 1", "done 3", "done 2 not-found"}; !slices.Equal(summary, want) {
		t.Errorf("actions %q, want %q: %s", summary, want, res)
	}
	if ok, f := schematest.Check(t, "pick-output", res); !ok {
		t.Errorf("output rejected at %s", f)
	}
}

// A key pressed before fzf shows a reload's list names a line the session's
// load has dropped: it is still the target, as the load before showed it.
func TestActBeforeReloadShows(t *testing.T) {
	withAction(t, completing("z", anyTargets))
	tr := newTestTree(t)
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("act", "z", "1@/") // 1 leaves the open list
		// Pressed while fzf still shows 1: dropped lines come after the
		// shown ones.
		helper("act", "d", "1@/", "3@/")
		if got := helper("text", "footer"); got != "✓ done 1: 3 · ✓ already done 1: 1" {
			t.Errorf("footer %q", got)
		}
		helper("quit")
	}})
}

// A load that fails after an action leaves the list as it was: the status
// line says so. The marks are cleared all the same: the action is over.
func TestActReloadFails(t *testing.T) {
	withAction(t, completing("z", anyTargets))
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/a"})
	tr.run("create", map[string]any{"title": "one", "folder": "/a"})
	tr.pick(map[string]any{"folder": "/a"}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		// The task survives, by ID; the scope folder doesn't.
		tr.run("create-folder", map[string]any{"folder": "/b"})
		tr.run("move", map[string]any{"id": 1, "to": "/b"})
		tr.run("delete-folder", map[string]any{"folder": "/a"})
		if got := helper("act", "z", "1@/a"); got != "clear-selection+transform-footer('/bin/koan' __pick text 'footer')" {
			t.Errorf("printed %q", got)
		}
		if got := helper("text", "footer"); !strings.HasPrefix(got, "✓ done 1 · ✗ reload: not-found: ") {
			t.Errorf("footer %q", got)
		}
		helper("quit")
	}})
}

// A call whose helper died before its envelope was written is reported with
// a null output, in cancelled's actions too.
func TestActionsUnknownOutcome(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one"})
	out, line := tr.pick(map[string]any{}, fzfDoes{status: 130, do: func(t *testing.T, helper func(...string) string) {
		s, e := openSession(fsys.OS{}, []string{SessionVar + "=" + tr.session})
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		s.Write(logFile, []byte(`[{"operation":"done","input":{"id":1},"output":null}]`))
	}})
	if out.OK || out.Error.Kind != errs.KindCancelled {
		t.Fatalf("%s", line)
	}
	if !strings.Contains(string(line), `"details":{"actions":[{"operation":"done","input":{"id":1},"output":null}]}`) {
		t.Errorf("%s", line)
	}
	d, _ := json.Marshal(out.Error.Details)
	if ok, f := schematest.Check(t, "pick-error-details#/$defs/cancelled", d); !ok {
		t.Errorf("details rejected at %s: %s", f, d)
	}
}

func TestActUsage(t *testing.T) {
	s := testSession(t)
	writeJSON(s, shownFile, shown{})
	for _, args := range [][]string{nil, {"nope"}} {
		if _, e := act(s, args, Env{}); e == nil || e.Kind != errs.KindUsage {
			t.Errorf("%q: %v", args, e)
		}
	}
	if _, e := linesVerb(s, []string{"x"}, Env{}); e == nil || e.Kind != errs.KindUsage {
		t.Errorf("lines x: %v", e)
	}
}

// Actions' keys are bound to act, and are command keys.
func TestActionBindings(t *testing.T) {
	withAction(t, completing("z", anyTargets))
	args := picker{exe: "/f", scope: Scope{Folder: "/", Recursive: true}}.args()
	if !slices.Contains(args, "z:transform:'/f' __pick act 'z' {+1}") {
		t.Errorf("no binding: %q", args)
	}
	if !slices.Contains(args, "start:unbind(load,"+strings.Join(commandKeys(), ",")+")") || !strings.HasSuffix(strings.Join(commandKeys(), ","), ",z") {
		t.Errorf("not unbound at start: %q", args)
	}
	if keys := commandKeys(); keys[len(keys)-1] != "z" {
		t.Errorf("command keys %q", keys)
	}
}

// A line's task is found as the final read finds it: by its key, else the
// one task with its ID; never one copy of several picked at random.
func TestFindLine(t *testing.T) {
	at := func(id model.ID, folder model.FolderPath) model.TaskView {
		var v model.TaskView
		v.ID, v.Folder = id, folder
		return v
	}
	tasks := []model.TaskView{at(1, "/a"), at(2, "/a"), at(2, "/b")}
	for _, tc := range []struct {
		line   shownLine
		i      int
		failed string
	}{
		{shownLine{Key: "1@/a", ID: 1}, 0, ""},
		{shownLine{Key: "1@/moved", ID: 1}, 0, ""},
		{shownLine{Key: "2@/b", ID: 2}, 2, ""},
		{shownLine{Key: "2@/c", ID: 2}, -1, "has 2 copies, none in /c"},
		{shownLine{Key: "3@/a", ID: 3}, -1, "is gone"},
	} {
		if i, failed := findLine(tasks, tc.line); i != tc.i || failed != tc.failed {
			t.Errorf("%s: %d, %q, want %d, %q", tc.line.Key, i, failed, tc.i, tc.failed)
		}
	}
}
