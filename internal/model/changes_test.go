package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/phansen314/koan/internal/core"
)

func wantErr(t *testing.T, err error, kind, rule string) *core.Error {
	t.Helper()
	if !core.IsKind(err, kind, rule) {
		t.Fatalf("got %v, want %s %s", err, kind, rule)
	}
	return err.(*core.Error)
}

func checked(t *testing.T, tr *Tree) {
	t.Helper()
	if bad := tr.Check(); bad != nil {
		t.Fatalf("invariants: %v", bad)
	}
}

func newTree() *Tree { return New(1, clock()) }

func ptr[T any](v T) *T { return &v }

func TestCreateFolder(t *testing.T) {
	tr := newTree()
	made := must[[]FolderAt](t)(tr.CreateFolder("/work/api", ptr("  API  "), map[string]any{"k": 1.0}))
	if want := []FolderAt{{1, "/work"}, {2, "/work/api"}}; !reflect.DeepEqual(made, want) {
		t.Errorf("made %v, want %v", made, want)
	}
	if f := tr.Folders[2]; *f.Title != "API" || f.Extra["k"] != 1.0 || tr.Folders[1].Title != nil {
		t.Errorf("fields: %+v, parent %+v", f, tr.Folders[1])
	}
	// No gates until one is named.
	if len(tr.Tasks) != 0 {
		t.Error("a new folder has gates")
	}

	_, err := tr.CreateFolder("/work/api", nil, nil)
	e := wantErr(t, err, core.KindConflict, core.RuleExists)
	if !reflect.DeepEqual(e.Paths, []string{"/work/api"}) {
		t.Errorf("paths %v", e.Paths)
	}
	_, err = tr.CreateFolder("/work/API", nil, nil)
	e = wantErr(t, err, core.KindConflict, core.RuleCaseMismatch)
	if !reflect.DeepEqual(e.IDs, []int64{2}) || !reflect.DeepEqual(e.Paths, []string{"/work/API", "/work/api"}) {
		t.Errorf("ids %v paths %v", e.IDs, e.Paths)
	}
	_, err = tr.CreateFolder("/Work/x", nil, nil)
	wantErr(t, err, core.KindConflict, core.RuleCaseMismatch)
	for _, bad := range []string{"/", "work", "@1/x", "/a.b", "/work.start", "/x:y"} {
		_, err = tr.CreateFolder(bad, nil, nil)
		wantErr(t, err, core.KindInvalidInput, "")
	}
	_, err = tr.CreateFolder("/t", ptr(" "), nil)
	wantErr(t, err, core.KindInvalidInput, "")
	if len(tr.Folders) != 2 || tr.Issued != 2 {
		t.Error("a refused change made something")
	}
	checked(t, tr)
}

func TestCreateTask(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/work", nil, nil)) // @1
	r := must[TaskCreated](t)(tr.CreateTask(TaskInput{
		Title: " Write ", Folder: "/work", Priority: ptr[int64](2), Tags: []string{"a", "b"},
		BlockedBy: []string{"/work.start"}, Blocks: []string{"/work"},
	}))
	// The task is @2; its edges make @3 (start) and @4 (done).
	if r.ID != 2 || len(r.GatesMade) != 2 ||
		r.GatesMade[0] != (GateMade{3, 1, "/work", core.EndStart}) ||
		r.GatesMade[1] != (GateMade{4, 1, "/work", core.EndDone}) {
		t.Fatalf("result %+v", r)
	}
	x := tr.Tasks[2]
	if x.Title != "Write" || *x.Priority != 2 || !x.Tags["a"] || !x.BlockedBy[3] || !tr.Tasks[4].BlockedBy[2] ||
		x.CreatedAt != x.UpdatedAt || x.CompletedAt != nil {
		t.Errorf("task %+v", x)
	}
	// The done gate's blocked_by gained an entry, so its updated_at moved.
	if tr.Tasks[4].UpdatedAt != x.CreatedAt {
		t.Error("gate's updated_at")
	}

	// Waiting on a task that waits on it closes a cycle.
	_, err := tr.CreateTask(TaskInput{Title: "c", BlockedBy: []string{"/work"}, Blocks: []string{"2"}})
	e := wantErr(t, err, core.KindConflict, core.RuleCycle)
	if !reflect.DeepEqual(e.Problems, []string{"/blocks/0", "/blocked_by/0"}) {
		t.Errorf("problems %v", e.Problems)
	}
	for _, c := range []struct {
		in       TaskInput
		kind     string
		problems []string
	}{
		{TaskInput{Title: ""}, core.KindInvalidInput, []string{"/title"}},
		{TaskInput{Title: "a\nb"}, core.KindInvalidInput, []string{"/title"}},
		{TaskInput{Title: "t", Tags: []string{"Bad"}}, core.KindInvalidInput, []string{"/tags/0"}},
		{TaskInput{Title: "t", Tags: []string{"a", "a"}}, core.KindInvalidInput, []string{"/tags/1"}},
		{TaskInput{Title: "t", Priority: ptr(core.MaxInt + 1)}, core.KindInvalidInput, []string{"/priority"}},
		{TaskInput{Title: "t", BlockedBy: []string{"$a"}}, core.KindInvalidInput, []string{"/blocked_by/0"}},
		{TaskInput{Title: "t", BlockedBy: []string{"/"}}, core.KindInvalidInput, []string{"/blocked_by/0"}},
		{TaskInput{Title: "t", Folder: "@2"}, core.KindInvalidInput, []string{"/folder"}}, // a task where a folder is taken
		{TaskInput{Title: "t", BlockedBy: []string{"2.start"}}, core.KindInvalidInput, []string{"/blocked_by/0"}},
	} {
		_, err := tr.CreateTask(c.in)
		if e := wantErr(t, err, c.kind, ""); !reflect.DeepEqual(e.Problems, c.problems) {
			t.Errorf("%+v: problems %q, want %q", c.in, e.Problems, c.problems)
		}
	}
	if tr.Issued != 4 {
		t.Errorf("refused changes issued IDs: %d", tr.Issued)
	}
	checked(t, tr)
}

func TestUpdate(t *testing.T) {
	tr := newTree()
	id := must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "t", Tags: []string{"a"}, Extra: map[string]any{"x": 1.0}})).ID
	stamp := tr.Tasks[id].UpdatedAt
	changed := must[bool](t)(tr.UpdateTask("1", Update{SetPriority: true, Priority: nil}))
	if changed || tr.Tasks[id].UpdatedAt != stamp {
		t.Error("an update that changes nothing moved updated_at")
	}
	changed = must[bool](t)(tr.UpdateTask("1", Update{
		SetTitle: true, Title: ptr("u"), TagsAdd: []string{"b"}, TagsRemove: []string{"a"},
		ExtraMerge: map[string]any{"y": 2.0}, ExtraRemove: []string{"x"},
	}))
	x := tr.Tasks[id]
	if !changed || x.Title != "u" || !reflect.DeepEqual(x.Tags, map[string]bool{"b": true}) ||
		!reflect.DeepEqual(x.Extra, map[string]any{"y": 2.0}) || x.UpdatedAt == stamp {
		t.Errorf("task %+v", x)
	}
	_, err := tr.UpdateTask("1", Update{TagsReplaceAll: []string{}, TagsAdd: []string{"c"}})
	wantErr(t, err, core.KindInvalidInput, "")
	_, err = tr.UpdateTask("1", Update{SetTitle: true, Title: nil})
	wantErr(t, err, core.KindInvalidInput, "")
	// Added and removed at once, refused at the removing entry.
	_, err = tr.UpdateTask("1", Update{TagsAdd: []string{"c", "d"}, TagsRemove: []string{"e", "d"}, ExtraMerge: map[string]any{"k": 1.0}, ExtraRemove: []string{"k"}})
	if e, _ := err.(*core.Error); e == nil || !reflect.DeepEqual(e.Problems, []string{"/tags_remove/1", "/extra_remove/0"}) {
		t.Errorf("added and removed: %v", err)
	}
	// extra changes when it would print differently: 1.0 and 1 are
	// different spellings, so replacing one with the other is a change.
	for i, c := range []struct {
		n    string
		want bool
	}{{"1.0", true}, {"1.0", false}, {"1", true}} {
		if got := must[bool](t)(tr.UpdateTask("1", Update{ExtraMerge: map[string]any{"n": json.Number(c.n)}})); got != c.want {
			t.Errorf("extra spelling %d (%s): changed %v", i, c.n, got)
		}
	}

	must[[]FolderAt](t)(tr.CreateFolder("/f", nil, nil))
	if !must[bool](t)(tr.UpdateFolder("/f", Update{SetTitle: true, Title: ptr("F")})) || *tr.Folders[2].Title != "F" {
		t.Error("folder title")
	}
	if !must[bool](t)(tr.UpdateFolder("/f", Update{SetTitle: true, Title: nil})) || tr.Folders[2].Title != nil {
		t.Error("folder title back to null")
	}
	_, err = tr.UpdateFolder("/", Update{SetTitle: true, Title: ptr("R")})
	wantErr(t, err, core.KindInvalidInput, "")
	checked(t, tr)
}

func TestBlockUnblock(t *testing.T) {
	tr := newTree()
	a := must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "a"})).ID // @1
	b := must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "b"})).ID // @2
	must[Claim](t)(tr.ClaimTask("2", nil))

	// An added edge can block a claimed task; the claim stays.
	r := must[EdgeResult](t)(tr.Block("2", []string{"1"}, nil))
	if !r.Changed || !reflect.DeepEqual(r.Reblocked, []int64{b}) ||
		!reflect.DeepEqual(r.Warnings, []Warning{{"claimed-blocked", []int64{b}}}) || !tr.claimed(b) {
		t.Errorf("block: %+v", r)
	}
	// The same edge again changes nothing.
	if r := must[EdgeResult](t)(tr.Block("2", []string{"@1"}, nil)); r.Changed {
		t.Error("an edge written twice")
	}
	// Itself: invalid-input. A cycle: conflict.
	_, err := tr.Block("1", []string{"1"}, nil)
	wantErr(t, err, core.KindInvalidInput, "")
	_, err = tr.Block("1", []string{"2"}, nil)
	if e := wantErr(t, err, core.KindConflict, core.RuleCycle); !reflect.DeepEqual(e.IDs, []int64{a, b}) {
		t.Errorf("cycle %v", e.IDs)
	}
	_, err = tr.Block("1", nil, nil)
	wantErr(t, err, core.KindInvalidInput, "")

	// Writing into a complete task warns, and doesn't reopen it.
	must[CompleteResult](t)(tr.Complete("2"))
	c := must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "c"})).ID
	r = must[EdgeResult](t)(tr.Block("2", []string{"3"}, nil))
	if !reflect.DeepEqual(r.Warnings, []Warning{{"complete-blocker", []int64{b}}}) || tr.Tasks[b].Open() {
		t.Errorf("complete-blocker: %+v", r)
	}

	// Unblock never makes a gate.
	must[[]FolderAt](t)(tr.CreateFolder("/f", nil, nil))
	_, err = tr.Unblock("1", []string{"/f"}, nil)
	wantErr(t, err, core.KindNotFound, "")
	if _, ok := tr.Gate(4, core.EndDone); ok {
		t.Error("unblock made a gate")
	}
	// Unblocking releases.
	d := must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "d", BlockedBy: []string{"3"}})).ID
	u := must[EdgeResult](t)(tr.Unblock(fmt.Sprintf("@%d", d), []string{fmt.Sprintf("@%d", c)}, nil))
	if !u.Changed || !reflect.DeepEqual(u.Released, []int64{d}) {
		t.Errorf("unblock: %+v", u)
	}
	checked(t, tr)
}

func TestCompleteReopen(t *testing.T) {
	tr := newTree()
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "a"}))                           // @1
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "b", BlockedBy: []string{"1"}})) // @2
	must[Claim](t)(tr.ClaimTask("1", nil))
	must[Claim](t)(tr.ClaimTask("2", nil)) // a blocked task can be claimed

	r := must[CompleteResult](t)(tr.Complete("1"))
	if !reflect.DeepEqual(r.Changed, []int64{1}) || !reflect.DeepEqual(r.Released, []int64{2}) || tr.claimed(1) {
		t.Errorf("complete: %+v", r)
	}
	if r := must[CompleteResult](t)(tr.Complete("1")); len(r.Changed) != 0 {
		t.Error("completing twice changed something")
	}
	r = must[CompleteResult](t)(tr.Reopen("1"))
	if !reflect.DeepEqual(r.Changed, []int64{1}) || !reflect.DeepEqual(r.Reblocked, []int64{2}) ||
		!reflect.DeepEqual(r.Warnings, []Warning{{"claimed-blocked", []int64{2}}}) {
		t.Errorf("reopen: %+v", r)
	}
	// A reopened task starts unclaimed.
	if tr.claimed(1) {
		t.Error("reopened task is claimed")
	}
	checked(t, tr)
}

// Complete and reopen over a list are one change, all or none. Only the
// tasks whose state changed are listed, in input order; one completed
// in the same call is never released, and one reopened in the same call
// never reblocked. A task named twice, however it is written, is
// invalid-input at its later occurrence.
func TestCompleteReopenSeveral(t *testing.T) {
	tr := newTree()
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "a"}))                           // @1
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "b", BlockedBy: []string{"1"}})) // @2
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "c", BlockedBy: []string{"1"}})) // @3
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "d"}))                           // @4

	// A blocker and its waiter together: @2 is completed, so only @3 is
	// released.
	r := must[CompleteResult](t)(tr.Complete("2", "1"))
	if !reflect.DeepEqual(r.Changed, []int64{2, 1}) || !reflect.DeepEqual(r.Released, []int64{3}) {
		t.Errorf("blocker and waiter: %+v", r)
	}
	// Only the open ones are listed.
	r = must[CompleteResult](t)(tr.Complete("4", "@1", "3"))
	if !reflect.DeepEqual(r.Changed, []int64{4, 3}) || len(r.Released) != 0 {
		t.Errorf("some complete: %+v", r)
	}
	// A bad entry mid-list changes nothing.
	_, err := tr.Reopen("1", "99", "2")
	wantErr(t, err, core.KindNotFound, "")
	if tr.Tasks[1].Open() || tr.Tasks[2].Open() {
		t.Error("a refused reopen changed something")
	}
	// A duplicate, spelled differently, at its later occurrence.
	_, err = tr.Reopen("1", "2", "@1")
	if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/tasks/2"}) {
		t.Errorf("duplicate: %v", e.Problems)
	}
	// Reopening the blocker and a waiter: @2 was complete, so not ready,
	// and only @3, ready before, waits again.
	must[CompleteResult](t)(tr.Reopen("3"))
	r = must[CompleteResult](t)(tr.Reopen("1", "2"))
	if !reflect.DeepEqual(r.Changed, []int64{1, 2}) || !reflect.DeepEqual(r.Reblocked, []int64{3}) {
		t.Errorf("reopen several: %+v", r)
	}
	checked(t, tr)
}

func TestBatch(t *testing.T) {
	tr := newTree()
	r := must[BatchResult](t)(tr.Batch(BatchInput{
		Folders: []BatchFolder{{Path: "/work/api", Title: ptr("API rework")}},
		Folder:  "/work/api",
		Tasks: []BatchTask{
			// $build is used before it is defined.
			{Label: "$review", TaskInput: TaskInput{Title: "Review", Folder: "/work", BlockedBy: []string{"/work/api", "$build"}}},
			{Label: "$design", TaskInput: TaskInput{Title: "Design", BlockedBy: []string{"/work/api.start"}}},
			{Label: "$build", TaskInput: TaskInput{Title: "Build", BlockedBy: []string{"$design"}, Blocks: []string{"/work/api"}}},
		},
	}))
	// Folders @1 and @2, tasks @3–@5, then the gates: one done gate though
	// two parts name it.
	if !reflect.DeepEqual(r.Folders, []FolderAt{{1, "/work"}, {2, "/work/api"}}) ||
		!reflect.DeepEqual(r.Tasks, []int64{3, 4, 5}) ||
		!reflect.DeepEqual(r.Labels, map[string]int64{"$review": 3, "$design": 4, "$build": 5}) ||
		len(r.GatesMade) != 2 {
		t.Fatalf("batch: %+v", r)
	}
	done, _ := tr.Gate(2, core.EndDone)
	start, _ := tr.Gate(2, core.EndStart)
	if tr.Tasks[3].Folder != 1 || tr.Tasks[4].Folder != 2 ||
		!reflect.DeepEqual(tr.BlockedBy(3), []int64{5, done}) ||
		!reflect.DeepEqual(tr.BlockedBy(done), []int64{5}) ||
		!reflect.DeepEqual(tr.BlockedBy(4), []int64{start}) {
		t.Errorf("edges: 3 %v, done %v, 4 %v", tr.BlockedBy(3), tr.BlockedBy(done), tr.BlockedBy(4))
	}
	checked(t, tr)

	// Folders are made parents first, in tree order, not input order.
	r = must[BatchResult](t)(tr.Batch(BatchInput{Folders: []BatchFolder{{Path: "/z/b"}, {Path: "/z/a/x"}, {Path: "/Z2"}}}))
	if want := []FolderAt{{8, "/Z2"}, {9, "/z"}, {10, "/z/a"}, {11, "/z/a/x"}, {12, "/z/b"}}; !reflect.DeepEqual(r.Folders, want) {
		t.Errorf("folders %v, want %v", r.Folders, want)
	}

	issued := tr.Issued
	refused := []struct {
		in         BatchInput
		kind, rule string
		problems   []string
	}{
		{BatchInput{}, core.KindInvalidInput, "", []string{""}},
		{BatchInput{Tasks: []BatchTask{{Label: "$a", TaskInput: TaskInput{Title: "a"}}, {Label: "$a", TaskInput: TaskInput{Title: "b"}}}},
			core.KindInvalidInput, "", []string{"/tasks/1/label"}},
		{BatchInput{Tasks: []BatchTask{{TaskInput: TaskInput{Title: "a", BlockedBy: []string{"$nope"}}}}},
			core.KindInvalidInput, "", []string{"/tasks/0/blocked_by/0"}},
		{BatchInput{Tasks: []BatchTask{{Label: "$a", TaskInput: TaskInput{Title: "a", BlockedBy: []string{"$a.start"}}}}},
			core.KindInvalidInput, "", []string{"/tasks/0/blocked_by/0"}},
		{BatchInput{Folders: []BatchFolder{{Path: "/work"}}}, core.KindConflict, core.RuleExists, []string{"/folders/0/path"}},
		{BatchInput{Folders: []BatchFolder{{Path: "/new"}, {Path: "/new"}}}, core.KindConflict, core.RuleExists, []string{"/folders/1/path"}},
		{BatchInput{Folders: []BatchFolder{{Path: "/new/x"}, {Path: "/New/y"}}}, core.KindInvalidInput, "", []string{"/folders/0/path", "/folders/1/path"}},
		// The batch's folder resolves though no task takes it.
		{BatchInput{Folders: []BatchFolder{{Path: "/fresh"}}, Folder: "/nope"}, core.KindNotFound, "", []string{"/folder"}},
		// A cycle over the whole batch, across parts.
		{BatchInput{Tasks: []BatchTask{
			{Label: "$a", TaskInput: TaskInput{Title: "a", BlockedBy: []string{"$b"}}},
			{Label: "$b", TaskInput: TaskInput{Title: "b", BlockedBy: []string{"$a"}}},
		}}, core.KindConflict, core.RuleCycle, []string{"/tasks/1/blocked_by/0", "/tasks/0/blocked_by/0"}},
	}
	for i, c := range refused {
		_, err := tr.Batch(c.in)
		if e := wantErr(t, err, c.kind, c.rule); !reflect.DeepEqual(e.Problems, c.problems) {
			t.Errorf("batch %d: problems %q, want %q", i, e.Problems, c.problems)
		}
	}
	if tr.Issued != issued || len(tr.Folders) != 7 {
		t.Error("a refused batch made something")
	}
	_, err := tr.Batch(refused[len(refused)-1].in)
	if e := wantErr(t, err, core.KindConflict, core.RuleCycle); !reflect.DeepEqual(e.Problems, []string{"/tasks/1/blocked_by/0", "/tasks/0/blocked_by/0"}) {
		t.Errorf("cycle %v problems %v", e.IDs, e.Problems)
	}
	// Labels are only for batches.
	_, err = tr.Block("3", []string{"$build"}, nil)
	wantErr(t, err, core.KindInvalidInput, "")
}

func TestMoveTask(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/a", nil, nil))                                             // @1
	must[[]FolderAt](t)(tr.CreateFolder("/b", nil, nil))                                             // @2
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "x", Folder: "/a", Blocks: []string{"/a"}})) // @3, gate @4
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "y", Folder: "/b"}))                         // @5
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "z", Folder: "/a"}))                         // @6
	must[Claim](t)(tr.ClaimTask("3", nil))
	ready := tr.readySet()

	out, err := tr.MoveTask("/b", "3")
	if err != nil || !reflect.DeepEqual(out.Moved, []FolderAt{{3, "/b"}}) || !reflect.DeepEqual(out.Warnings, []Warning{{"moved-claimed", []int64{3}}}) {
		t.Errorf("move: %v %v", out, err)
	}
	// No edge changes, so no readiness does.
	if !tr.Tasks[4].BlockedBy[3] || !reflect.DeepEqual(tr.readySet(), ready) {
		t.Error("a move changed edges or readiness")
	}
	_, err = tr.MoveTask("/b", "@1.done")
	if e := wantErr(t, err, core.KindConflict, core.RuleGate); !reflect.DeepEqual(e.IDs, []int64{4}) {
		t.Errorf("ids %v", e.IDs)
	}
	if out, _ := tr.MoveTask("@2", "3"); len(out.Moved) != 0 {
		t.Error("a move to where it is changed something")
	}
	// Several, in input order; one already there isn't listed.
	out, err = tr.MoveTask("/", "6", "5", "3")
	if err != nil || !reflect.DeepEqual(out.Moved, []FolderAt{{6, "/"}, {5, "/"}, {3, "/"}}) {
		t.Errorf("several: %v %v", out, err)
	}
	out, err = tr.MoveTask("/a", "5", "6")
	if err != nil || !reflect.DeepEqual(out.Moved, []FolderAt{{5, "/a"}, {6, "/a"}}) || out.Warnings != nil {
		t.Errorf("several: %v %v", out, err)
	}
	// One refused, none moves.
	_, err = tr.MoveTask("/b", "5", "/a.done")
	wantErr(t, err, core.KindConflict, core.RuleGate)
	_, err = tr.MoveTask("/b", "5", "99")
	wantErr(t, err, core.KindNotFound, "")
	if tr.Tasks[5].Folder != 1 {
		t.Error("a refused move moved a task")
	}
	// Given twice, however written, at the later occurrence.
	_, err = tr.MoveTask("/b", "5", "@5")
	if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/refs/1"}) {
		t.Errorf("problems %v", e.Problems)
	}
	_, err = tr.MoveTask("/b")
	wantErr(t, err, core.KindInvalidInput, "")
	checked(t, tr)
}

func TestMoveFolder(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/work/api", nil, nil))                     // @1, @2
	must[[]FolderAt](t)(tr.CreateFolder("/other", nil, nil))                        // @3
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "t", Folder: "/work/api"})) // @4
	must[Claim](t)(tr.ClaimTask("4", nil))

	// Into a folder that exists; otherwise renamed.
	r := must[MoveResult](t)(tr.MoveFolder(MvDest, "/other", "/work"))
	if !reflect.DeepEqual(r.Moved, []FolderAt{{1, "/other/work"}}) ||
		!reflect.DeepEqual(r.Warnings, []Warning{{"moved-claimed", []int64{4}}}) {
		t.Errorf("into: %+v", r)
	}
	if tr.Path(2) != "/other/work/api" {
		t.Errorf("child path %s", tr.Path(2))
	}
	r = must[MoveResult](t)(tr.MoveFolder(MvDest, "/other/work2", "/other/work"))
	if !reflect.DeepEqual(r.Moved, []FolderAt{{1, "/other/work2"}}) {
		t.Errorf("renamed: %+v", r)
	}
	// No parent is made.
	_, err := tr.MoveFolder(MvRename, "/archive/work", "@1")
	wantErr(t, err, core.KindNotFound, "")
	_, err = tr.MoveFolder(MvDest, "/archive/work", "@1")
	wantErr(t, err, core.KindNotFound, "")

	_, err = tr.MoveFolder(MvRename, "/other/work2/api/x", "@1")
	wantErr(t, err, core.KindConflict, core.RuleIntoItself)
	if e := err.(*core.Error); !reflect.DeepEqual(e.IDs, []int64{1}) || !reflect.DeepEqual(e.Paths, []string{"/other/work2/api/x"}) {
		t.Errorf("into-itself names the folder and the destination: %v %v", e.IDs, e.Paths)
	}
	_, err = tr.MoveFolder(MvRename, "/other", "@2")
	wantErr(t, err, core.KindConflict, core.RuleExists)
	_, err = tr.MoveFolder(MvRename, "/OTHER", "@2")
	wantErr(t, err, core.KindConflict, core.RuleCaseMismatch)
	_, err = tr.MoveFolder(MvDest, "/x", "/")
	wantErr(t, err, core.KindInvalidInput, "")
	_, err = tr.MoveFolder(MvRename, "/", "/other")
	wantErr(t, err, core.KindInvalidInput, "")

	// An anchored path, whose last name is the new one.
	r = must[MoveResult](t)(tr.MoveFolder(MvRename, "@3/api2", "@2"))
	if !reflect.DeepEqual(r.Moved, []FolderAt{{2, "/other/api2"}}) {
		t.Errorf("anchored: %+v", r)
	}
	// A bare ID names no new path.
	_, err = tr.MoveFolder(MvRename, "@99", "@2")
	wantErr(t, err, core.KindNotFound, "")

	// A case-only rename: the one input naming both spellings.
	r = must[MoveResult](t)(tr.MoveFolder(MvDest, "/Other", "/other"))
	if len(r.Moved) != 1 || tr.Folders[3].Name != "Other" {
		t.Errorf("case-only rename: %+v", r)
	}
	checked(t, tr)
}

// Onto itself, as mv: into itself is into-itself; renamed to its own path,
// or into the folder it is in, nothing changes.
func TestMoveFolderOntoItself(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/a/b", nil, nil)) // @1, @2
	_, err := tr.MoveFolder(MvDest, "/a", "/a")
	wantErr(t, err, core.KindConflict, core.RuleIntoItself)
	_, err = tr.MoveFolder(MvInto, "/a/b", "/a")
	wantErr(t, err, core.KindConflict, core.RuleIntoItself)
	for _, c := range []struct {
		mv          Mv
		dest, which string
	}{{MvRename, "/a", "/a"}, {MvRename, "@1", "/a"}, {MvDest, "/a", "/a/b"}, {MvInto, "/", "/a"}, {MvRename, "/a/b", "@2"}} {
		if r := must[MoveResult](t)(tr.MoveFolder(c.mv, c.dest, c.which)); len(r.Moved) != 0 {
			t.Errorf("%v %s %s: %+v", c.mv, c.dest, c.which, r)
		}
	}
	checked(t, tr)
}

// Several folders move as one change, judged against the tree as the
// whole move would leave it.
func TestMoveFolders(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/a/x", nil, nil)) // @1, @2
	must[[]FolderAt](t)(tr.CreateFolder("/b/x", nil, nil)) // @3, @4
	must[[]FolderAt](t)(tr.CreateFolder("/d/X", nil, nil)) // @5, @6
	must[[]FolderAt](t)(tr.CreateFolder("/c", nil, nil))   // @7

	_, err := tr.MoveFolder(MvDest, "/c", "/a/x", "/b/x")
	wantErr(t, err, core.KindConflict, core.RuleExists)
	_, err = tr.MoveFolder(MvInto, "/c", "/a/x", "/d/X", "/b/x")
	e := wantErr(t, err, core.KindConflict, core.RuleCaseMismatch)
	if !reflect.DeepEqual(e.Paths, []string{"/c/X", "/c/x"}) {
		t.Errorf("case-mismatch before exists: %v", e.Paths)
	}
	_, err = tr.MoveFolder(MvDest, "/z", "/a/x", "/d/X")
	wantErr(t, err, core.KindNotFound, "")
	_, err = tr.MoveFolder(MvDest, "/c", "/a", "/a/x")
	if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/refs/1"}) {
		t.Errorf("nested: %v", e.Problems)
	}
	_, err = tr.MoveFolder(MvDest, "/c", "/a/x", "@2")
	if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/refs/1"}) {
		t.Errorf("twice: %v", e.Problems)
	}
	_, err = tr.MoveFolder(MvRename, "/c/y", "/a/x", "/d/X")
	if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/refs/1"}) {
		t.Errorf("rename of two: %v", e.Problems)
	}
	if tr.Path(2) != "/a/x" {
		t.Fatalf("a refused move moved something")
	}
	r := must[MoveResult](t)(tr.MoveFolder(MvDest, "/c", "/d/X", "/b"))
	if !reflect.DeepEqual(r.Moved, []FolderAt{{6, "/c/X"}, {3, "/c/b"}}) {
		t.Errorf("several: %+v", r)
	}
	checked(t, tr)
}

func TestDeleteTask(t *testing.T) {
	tr := newTree()
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "a"}))                                // @1
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "b", BlockedBy: []string{"1"}}))      // @2
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "c", BlockedBy: []string{"2"}}))      // @3
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "d", BlockedBy: []string{"2", "1"}})) // @4
	must[Claim](t)(tr.ClaimTask("2", nil))
	stamp := tr.Tasks[3].UpdatedAt

	r := must[Removed](t)(tr.DeleteTask("2"))
	// @3 lost its last open blocker; @4 still waits on @1. No splice: @3
	// doesn't come to wait on @1.
	if !reflect.DeepEqual(r, Removed{Removed: []int64{2}, Released: []int64{3}}) {
		t.Errorf("delete: %+v", r)
	}
	if len(tr.BlockedBy(3)) != 0 || tr.claimed(2) || tr.Tasks[3].UpdatedAt == stamp {
		t.Error("edges, claim or updated_at")
	}
	_, err := tr.DeleteTask("2")
	wantErr(t, err, core.KindNotFound, "")
	checked(t, tr)
}

func TestDeleteFolder(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/f/sub", nil, nil))                                         // @1, @2
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "t", Folder: "/f", Blocks: []string{"/f"}})) // @3, gate @4
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "w", BlockedBy: []string{"/f"}}))            // @5
	tr.PutMaterial(2, "notes.txt")
	tr.PutMaterial(1, "b")
	tr.PutMaterial(1, "B")

	_, err := tr.DeleteFolder([]string{"/f"}, false, false)
	if e := wantErr(t, err, core.KindConflict, core.RuleNotEmpty); !reflect.DeepEqual(e.IDs, []int64{2, 3}) {
		t.Errorf("not-empty ids %v", e.IDs)
	}
	_, err = tr.DeleteFolder([]string{"/f"}, true, false)
	if e := wantErr(t, err, core.KindConflict, core.RuleHasMaterial); !reflect.DeepEqual(e.Paths, []string{"/f:B", "/f:b", "/f/sub:notes.txt"}) {
		t.Errorf("has-material paths %v", e.Paths)
	}
	r := must[Removed](t)(tr.DeleteFolder([]string{"/f"}, true, true))
	if !reflect.DeepEqual(r, Removed{Removed: []int64{1, 2, 3, 4}, Released: []int64{5}}) {
		t.Errorf("delete: %+v", r)
	}
	// A folder holding only its gates is empty.
	must[[]FolderAt](t)(tr.CreateFolder("/g", nil, nil))
	must[EdgeResult](t)(tr.Block("5", []string{"/g.start"}, nil))
	must[Removed](t)(tr.DeleteFolder([]string{"/g"}, false, false))
	_, err = tr.DeleteFolder([]string{"/"}, true, true)
	wantErr(t, err, core.KindInvalidInput, "")
	checked(t, tr)
}

// Deletes over a list are one change: a duplicate, a folder inside
// another given, or a refusal for any changes nothing.
func TestDeleteSeveral(t *testing.T) {
	tr := newTree()
	for _, title := range []string{"a", "b", "c"} {
		must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: title})) // @1 to @3
	}
	must[EdgeResult](t)(tr.Block("2", []string{"1"}, []string{"3"}))
	_, err := tr.DeleteTask("1", "@1")
	if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/refs/1"}) {
		t.Errorf("duplicate %v", e.Problems)
	}
	_, err = tr.DeleteTask("1", "99")
	wantErr(t, err, core.KindNotFound, "")
	r := must[Removed](t)(tr.DeleteTask("2", "1"))
	if !reflect.DeepEqual(r, Removed{Removed: []int64{1, 2}, Released: []int64{3}}) {
		t.Errorf("tasks: %+v", r)
	}

	must[[]FolderAt](t)(tr.CreateFolder("/f/sub", nil, nil)) // @4, @5
	must[[]FolderAt](t)(tr.CreateFolder("/g", nil, nil))     // @6
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "t", Folder: "/g"}))
	tr.PutMaterial(5, "x")
	tr.PutMaterial(6, "y")
	_, err = tr.DeleteFolder([]string{"/f/sub", "/g", "/f"}, true, true)
	if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/refs/0"}) {
		t.Errorf("inside %v", e.Problems)
	}
	_, err = tr.DeleteFolder([]string{"/g", "6"}, true, true)
	if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/refs/1"}) {
		t.Errorf("twice %v", e.Problems)
	}
	_, err = tr.DeleteFolder([]string{"/f", "/g"}, false, false)
	if e := wantErr(t, err, core.KindConflict, core.RuleNotEmpty); !reflect.DeepEqual(e.IDs, []int64{5}) {
		t.Errorf("not-empty ids %v", e.IDs)
	}
	_, err = tr.DeleteFolder([]string{"/g", "/f"}, true, false)
	if e := wantErr(t, err, core.KindConflict, core.RuleHasMaterial); !reflect.DeepEqual(e.Paths, []string{"/g:y", "/f/sub:x"}) {
		t.Errorf("has-material paths %v", e.Paths)
	}
	r = must[Removed](t)(tr.DeleteFolder([]string{"/g", "/f"}, true, true))
	if !reflect.DeepEqual(r.Removed, []int64{4, 5, 6, 7}) || len(r.Released) != 0 {
		t.Errorf("folders: %+v", r)
	}
	checked(t, tr)
}

func TestArchive(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/f", nil, nil))                                             // @1
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "t", Folder: "/f", Blocks: []string{"/f"}})) // @2, gate @3
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "w", BlockedBy: []string{"2"}}))             // @4
	tr.PutMaterial(1, ".git")

	_, err := tr.Archive("2")
	wantErr(t, err, core.KindConflict, core.RuleNotFinished)
	must[CompleteResult](t)(tr.Complete("2"))
	_, err = tr.Archive("/f")
	if e := wantErr(t, err, core.KindConflict, core.RuleNotFinished); !reflect.DeepEqual(e.IDs, []int64{3}) {
		t.Errorf("open ids %v", e.IDs)
	}
	must[CompleteResult](t)(tr.Complete("@1.done"))
	ready := tr.readySet()
	left := must[[]int64](t)(tr.Archive("/f"))
	if !reflect.DeepEqual(left, []int64{1, 2, 3}) {
		t.Errorf("left %v", left)
	}
	// What stays keeps its readiness; the edge to what left is removed and
	// kept in the archive.
	if len(tr.BlockedBy(4)) != 0 || !reflect.DeepEqual(tr.readySet(), ready) {
		t.Error("archiving changed readiness")
	}
	a := tr.Archives[0]
	if a.ID != 1 || len(a.Tasks) != 2 || len(a.Folders) != 1 || !a.Folders[0].Material[".git"] ||
		!reflect.DeepEqual(a.Edges, []core.Edge{{From: 3, To: 2}, {From: 4, To: 2}}) {
		t.Errorf("archive %+v", a)
	}
	// Retired IDs never resolve again, and are never issued again.
	_, err = tr.Archive("2")
	if e := wantErr(t, err, core.KindNotFound, ""); !reflect.DeepEqual(e.Archived, []int64{2}) {
		t.Errorf("archived %v", e.Archived)
	}
	if id := must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "n"})).ID; id != 5 {
		t.Errorf("issued %d", id)
	}
	checked(t, tr)
	// The root can't be archived, and material isn't taken.
	for _, ref := range []string{"/", "@4:x"} {
		_, err = tr.Archive(ref)
		if e := wantErr(t, err, core.KindInvalidInput, ""); !reflect.DeepEqual(e.Problems, []string{"/ref"}) {
			t.Errorf("%s: problems %v", ref, e.Problems)
		}
	}
}

func TestClaims(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/f/sub", nil, nil))                                                                    // @1, @2
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "a", Priority: ptr[int64](1)}))                                         // @3
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "b", Folder: "/f/sub", Tags: []string{"x"}}))                           // @4
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "c", Folder: "/f", Tags: []string{"x", "y"}, Priority: ptr[int64](0)})) // @5

	cl := must[Claim](t)(tr.ClaimTask("3", map[string]any{"who": "me"}))
	if cl.Task != 3 || cl.Extra["who"] != "me" || cl.ClaimedAt == "" {
		t.Errorf("claim %+v", cl)
	}
	_, err := tr.ClaimTask("3", nil)
	wantErr(t, err, core.KindConflict, core.RuleClaimed)
	// A claim takes the task off the frontier, and nothing else.
	if got := tr.Frontier(); !reflect.DeepEqual(got, []int64{5, 4}) {
		t.Errorf("frontier %v", got)
	}
	must[bool](t)(tr.UpdateTask("3", Update{SetTitle: true, Title: ptr("a2")}))

	next := func(s Scope) int64 {
		c := must[*Claim](t)(tr.ClaimNext(s, nil))
		if c == nil {
			return 0
		}
		return c.Task
	}
	if got := next(Scope{Folder: "/f", Recursive: false, TagsAll: []string{"x"}}); got != 5 {
		t.Errorf("claim-next direct in /f: %d", got)
	}
	if got := next(Scope{Folder: "/f", Recursive: true, TagsAny: []string{"x"}}); got != 4 {
		t.Errorf("claim-next under /f: %d", got)
	}
	if got := next(Scope{Recursive: true}); got != 0 {
		t.Errorf("claim-next with nothing left: %d", got)
	}

	if !must[bool](t)(tr.Unclaim("4")) || tr.claimed(4) {
		t.Error("unclaim")
	}
	must[CompleteResult](t)(tr.Complete("4"))
	_, err = tr.ClaimTask("4", nil)
	wantErr(t, err, core.KindConflict, core.RuleComplete)
	checked(t, tr)
}

// The rows of the Case table the model decides.
func TestCase(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/work", nil, nil))
	// Siblings never differ only in case.
	_, err := tr.CreateFolder("/Work", nil, nil)
	wantErr(t, err, core.KindConflict, core.RuleCaseMismatch)
	// A path names a folder only in its exact case.
	_, err = tr.CreateTask(TaskInput{Title: "t", Folder: "/Work"})
	if e := wantErr(t, err, core.KindConflict, core.RuleCaseMismatch); e.Paths[1] != "/work" {
		t.Errorf("paths %v", e.Paths)
	}
	// One input never names two spellings of a path.
	must[[]FolderAt](t)(tr.CreateFolder("/work/a", nil, nil))
	_, err = tr.CreateTask(TaskInput{Title: "t", Folder: "/work/a", Blocks: []string{"/Work/b"}})
	wantErr(t, err, core.KindInvalidInput, "")
	// Names are kept as given and compared byte for byte.
	must[[]FolderAt](t)(tr.CreateFolder("/Zeta", nil, nil))
	if tr.Folders[3].Name != "Zeta" || tr.TreeOrder()[0] != 3 {
		t.Errorf("tree order %v", tr.TreeOrder())
	}
	// Changing a folder's case is a rename.
	must[MoveResult](t)(tr.MoveFolder(MvDest, "/Work", "/work"))
	if tr.Path(2) != "/Work/a" {
		t.Errorf("path %s", tr.Path(2))
	}
	checked(t, tr)
}

// Block makes any gate it names, its subject included: the subject's gate
// gets its ID before the gates the edges name. Unblock never makes one,
// and a refused block makes none.
func TestBlockSubjectGate(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/f", nil, nil))       // @1
	must[[]FolderAt](t)(tr.CreateFolder("/g", nil, nil))       // @2
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "x"})) // @3

	_, err := tr.Unblock("/f.start", []string{"3"}, nil)
	wantErr(t, err, core.KindNotFound, "")
	_, err = tr.Block("/f.start", []string{"/f.start"}, nil)
	wantErr(t, err, core.KindInvalidInput, "")
	_, err = tr.Block("@1", []string{"3"}, nil)
	wantErr(t, err, core.KindInvalidInput, "")
	if tr.Issued != 3 {
		t.Fatalf("a refused change issued IDs: %d", tr.Issued)
	}

	r := must[EdgeResult](t)(tr.Block("/f.start", []string{"/g"}, []string{"3"}))
	want := []GateMade{{ID: 4, Folder: 1, Path: "/f", End: core.EndStart}, {ID: 5, Folder: 2, Path: "/g", End: core.EndDone}}
	if r.ID != 4 || !r.Changed || !reflect.DeepEqual(r.GatesMade, want) {
		t.Errorf("block: %+v", r)
	}
	if !tr.Tasks[4].BlockedBy[5] || !tr.Tasks[3].BlockedBy[4] {
		t.Error("edges not written")
	}
	// Named again, it is there.
	if r := must[EdgeResult](t)(tr.Block("@1.start", []string{"/g"}, nil)); r.Changed || len(r.GatesMade) != 0 {
		t.Errorf("block again: %+v", r)
	}
	checked(t, tr)
}

// Any move of a gate is refused, even to its own folder.
func TestMoveGateToItsFolder(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/a", nil, nil))
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "x", Blocks: []string{"/a"}}))
	_, err := tr.MoveTask("/a", "/a.done")
	wantErr(t, err, core.KindConflict, core.RuleGate)
}

// mv checks into-itself, then case-mismatch, then exists. A path
// names a folder only in its exact case, so into-itself follows exact
// names.
func TestMoveFolderOrder(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/a/b", nil, nil)) // @1, @2
	must[[]FolderAt](t)(tr.CreateFolder("/c/d", nil, nil)) // @3, @4
	_, err := tr.MoveFolder(MvRename, "/a/b", "@1")
	wantErr(t, err, core.KindConflict, core.RuleIntoItself)
	_, err = tr.MoveFolder(MvRename, "/a/B/x", "@1")
	wantErr(t, err, core.KindConflict, core.RuleIntoItself)
	_, err = tr.MoveFolder(MvDest, "/a/B/x", "@1")
	wantErr(t, err, core.KindConflict, core.RuleIntoItself)
	// Into itself names the new path from the root, however it is written.
	for dest, path := range map[string]string{"@2/x": "/a/b/x", "@1/x": "/a/x", "2": "/a/b"} {
		_, err = tr.MoveFolder(MvRename, dest, "@1")
		wantErr(t, err, core.KindConflict, core.RuleIntoItself)
		if e := err.(*core.Error); len(e.Paths) != 1 || e.Paths[0] != path {
			t.Errorf("%s: into-itself at %v, want %s", dest, e.Paths, path)
		}
	}
	_, err = tr.MoveFolder(MvRename, "/C/d", "@1")
	wantErr(t, err, core.KindConflict, core.RuleCaseMismatch)
	_, err = tr.MoveFolder(MvRename, "/A/x", "@1")
	wantErr(t, err, core.KindConflict, core.RuleCaseMismatch)
	_, err = tr.MoveFolder(MvRename, "/c/d", "@1")
	wantErr(t, err, core.KindConflict, core.RuleExists)
	_, err = tr.MoveFolder(MvRename, "@4", "@1")
	wantErr(t, err, core.KindConflict, core.RuleExists)
	checked(t, tr)
}

// .DS_Store is never material to a delete.
func TestDeleteFolderDSStore(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/f/sub", nil, nil)) // @1, @2
	tr.PutMaterial(2, ".DS_Store")
	tr.PutMaterial(1, ".DS_Store")
	must[Removed](t)(tr.DeleteFolder([]string{"/f"}, true, false))
	checked(t, tr)
}
