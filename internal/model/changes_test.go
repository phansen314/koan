package model

import (
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
		{TaskInput{Title: "t", Folder: "@2"}, core.KindNotFound, nil}, // a task where a folder is taken
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
	if !r.Changed || !reflect.DeepEqual(r.Released, []int64{2}) || tr.claimed(1) {
		t.Errorf("complete: %+v", r)
	}
	if r := must[CompleteResult](t)(tr.Complete("1")); r.Changed {
		t.Error("completing twice changed something")
	}
	r = must[CompleteResult](t)(tr.Reopen("1"))
	if !r.Changed || !reflect.DeepEqual(r.Reblocked, []int64{2}) ||
		!reflect.DeepEqual(r.Warnings, []Warning{{"claimed-blocked", []int64{2}}}) {
		t.Errorf("reopen: %+v", r)
	}
	// A reopened task starts unclaimed.
	if tr.claimed(1) {
		t.Error("reopened task is claimed")
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
	must[Claim](t)(tr.ClaimTask("3", nil))
	ready := tr.readySet()

	changed, warnings, err := tr.MoveTask("3", "/b")
	if err != nil || !changed || !reflect.DeepEqual(warnings, []Warning{{"moved-claimed", []int64{3}}}) {
		t.Errorf("move: %v %v %v", changed, warnings, err)
	}
	// No edge changes, so no readiness does.
	if !tr.Tasks[4].BlockedBy[3] || !reflect.DeepEqual(tr.readySet(), ready) {
		t.Error("a move changed edges or readiness")
	}
	_, _, err = tr.MoveTask("@1.done", "/b")
	if e := wantErr(t, err, core.KindConflict, core.RuleGate); !reflect.DeepEqual(e.IDs, []int64{4}) {
		t.Errorf("ids %v", e.IDs)
	}
	if changed, _, _ := tr.MoveTask("3", "@2"); changed {
		t.Error("a move to where it is changed something")
	}
	checked(t, tr)
}

func TestMoveFolder(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/work/api", nil, nil))                     // @1, @2
	must[[]FolderAt](t)(tr.CreateFolder("/other", nil, nil))                        // @3
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "t", Folder: "/work/api"})) // @4
	must[Claim](t)(tr.ClaimTask("4", nil))

	r := must[FolderMoved](t)(tr.MoveFolder("/work", "/archive/old/work2"))
	if !r.Changed || r.Path != "/archive/old/work2" || len(r.Created) != 2 ||
		!reflect.DeepEqual(r.Warnings, []Warning{{"moved-claimed", []int64{4}}}) {
		t.Errorf("move: %+v", r)
	}
	if tr.Path(2) != "/archive/old/work2/api" {
		t.Errorf("child path %s", tr.Path(2))
	}

	_, err := tr.MoveFolder("@1", "/archive/old/work2/api/x")
	wantErr(t, err, core.KindConflict, core.RuleIntoItself)
	_, err = tr.MoveFolder("@1", "/other")
	wantErr(t, err, core.KindConflict, core.RuleExists)
	_, err = tr.MoveFolder("@1", "/OTHER")
	wantErr(t, err, core.KindConflict, core.RuleCaseMismatch)
	_, err = tr.MoveFolder("/", "/x")
	wantErr(t, err, core.KindInvalidInput, "")

	// A case-only rename is a move: the one input naming both spellings.
	r = must[FolderMoved](t)(tr.MoveFolder("/other", "/Other"))
	if !r.Changed || tr.Folders[3].Name != "Other" {
		t.Errorf("case-only rename: %+v", r)
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

	_, err := tr.DeleteFolder("/f", false, false)
	if e := wantErr(t, err, core.KindConflict, core.RuleNotEmpty); !reflect.DeepEqual(e.IDs, []int64{2, 3}) {
		t.Errorf("not-empty ids %v", e.IDs)
	}
	_, err = tr.DeleteFolder("/f", true, false)
	if e := wantErr(t, err, core.KindConflict, core.RuleHasMaterial); !reflect.DeepEqual(e.Paths, []string{"/f:B", "/f:b", "/f/sub:notes.txt"}) {
		t.Errorf("has-material paths %v", e.Paths)
	}
	r := must[Removed](t)(tr.DeleteFolder("/f", true, true))
	if !reflect.DeepEqual(r, Removed{Removed: []int64{1, 2, 3, 4}, Released: []int64{5}}) {
		t.Errorf("delete: %+v", r)
	}
	// A folder holding only its gates is empty.
	must[[]FolderAt](t)(tr.CreateFolder("/g", nil, nil))
	must[EdgeResult](t)(tr.Block("5", []string{"/g.start"}, nil))
	must[Removed](t)(tr.DeleteFolder("/g", false, false))
	_, err = tr.DeleteFolder("/", true, true)
	wantErr(t, err, core.KindInvalidInput, "")
	checked(t, tr)
}

func TestArchive(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/f", nil, nil))                                             // @1
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "t", Folder: "/f", Blocks: []string{"/f"}})) // @2, gate @3
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "w", BlockedBy: []string{"2"}}))             // @4
	tr.PutMaterial(1, ".git")

	_, err := tr.ArchiveTask("2")
	wantErr(t, err, core.KindConflict, core.RuleNotFinished)
	must[CompleteResult](t)(tr.Complete("2"))
	_, err = tr.ArchiveFolder("/f")
	if e := wantErr(t, err, core.KindConflict, core.RuleNotFinished); !reflect.DeepEqual(e.IDs, []int64{3}) {
		t.Errorf("open ids %v", e.IDs)
	}
	must[CompleteResult](t)(tr.Complete("@1.done"))
	ready := tr.readySet()
	left := must[[]int64](t)(tr.ArchiveFolder("/f"))
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
	_, err = tr.ArchiveTask("2")
	if e := wantErr(t, err, core.KindNotFound, ""); !reflect.DeepEqual(e.Archived, []int64{2}) {
		t.Errorf("archived %v", e.Archived)
	}
	if id := must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "n"})).ID; id != 5 {
		t.Errorf("issued %d", id)
	}
	checked(t, tr)
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

	if !must[bool](t)(tr.Release("4")) || tr.claimed(4) {
		t.Error("release")
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
	// Changing a folder's case is a move.
	must[FolderMoved](t)(tr.MoveFolder("/work", "/Work"))
	if tr.Path(2) != "/Work/a" {
		t.Errorf("path %s", tr.Path(2))
	}
	checked(t, tr)
}
