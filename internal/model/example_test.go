package model

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/phansen314/koan/internal/core"
)

// clock gives a new second for each change.
func clock() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("2026-10-20T10:%02d:%02dZ", n/60, n%60)
	}
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// at sets the next ID a change will issue, as an earlier root's IDs would.
func at(tr *Tree, next int64) { tr.Issued = next - 1 }

// The gates the example makes, in the order exampleTree names them.
const (
	start7 int64 = 51
	done7  int64 = 52
	start9 int64 = 53
	done9  int64 = 54
	done1  int64 = 55
)

// exampleTree builds the rules doc's "The example, as text" through the
// model's own changes.
func exampleTree(t *testing.T) *Tree {
	t.Helper()
	tr := New(1, clock())
	folder := func(id int64, path string) {
		at(tr, id)
		made := must[[]FolderAt](t)(tr.CreateFolder(path, nil, nil))
		if got := made[len(made)-1].ID; got != id {
			t.Fatalf("%s got @%d, want @%d", path, got, id)
		}
	}
	task := func(id int64, folder string) {
		at(tr, id)
		if got := must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: fmt.Sprintf("Task %d", id), Folder: folder})).ID; got != id {
			t.Fatalf("task got @%d, want @%d", got, id)
		}
	}
	folder(1, "/work")
	task(4, "@1")
	folder(7, "/work/phase1")
	task(8, "@7")
	folder(9, "/work/phase2")
	task(10, "@9")

	at(tr, 51)
	block := func(task string, blockedBy, blocks []string) {
		must[EdgeResult](t)(tr.Block(task, blockedBy, blocks))
	}
	block("8", []string{"@7.start"}, nil)            // @8 → @7.start, making @51
	block("8", nil, []string{"@7"})                  // @7.done → @8, making @52
	block("10", []string{"/work/phase2.start"}, nil) // @10 → @9.start, making @53
	block("10", nil, []string{"/work/phase2"})       // @9.done → @10, making @54
	block("@9.start", []string{"@7"}, nil)           // @9.start → @7.done
	block("@7.done", nil, []string{"@1"})            // @1.done → @7.done, making @55
	block("4", []string{"/work/phase1.done"}, nil)   // @4 → @7.done
	return tr
}

func TestExampleTree(t *testing.T) {
	tr := exampleTree(t)
	if bad := tr.Check(); bad != nil {
		t.Fatalf("invariants: %v", bad)
	}

	// Each item, and where it is.
	folders := map[int64]struct {
		path   string
		parent int64
	}{1: {"/work", core.RootID}, 7: {"/work/phase1", 1}, 9: {"/work/phase2", 1}}
	if len(tr.Folders) != len(folders) {
		t.Errorf("%d folders, want %d", len(tr.Folders), len(folders))
	}
	for id, want := range folders {
		if f := tr.Folders[id]; f == nil || f.Parent != want.parent || tr.Path(id) != want.path {
			t.Errorf("folder @%d: %+v, want %s", id, f, want.path)
		}
	}
	tasks := map[int64]struct {
		folder int64
		gate   core.End
	}{
		4: {1, ""}, 8: {7, ""}, 10: {9, ""},
		start7: {7, core.EndStart}, done7: {7, core.EndDone},
		start9: {9, core.EndStart}, done9: {9, core.EndDone},
		done1: {1, core.EndDone},
	}
	if len(tr.Tasks) != len(tasks) {
		t.Errorf("%d tasks, want %d", len(tr.Tasks), len(tasks))
	}
	for id, want := range tasks {
		x := tr.Tasks[id]
		if x == nil || x.Folder != want.folder || x.Gate != want.gate || !x.Open() {
			t.Errorf("task @%d: %+v, want in @%d gate %q", id, x, want.folder, want.gate)
		}
	}
	// @1 has a done gate only.
	if _, ok := tr.Gate(1, core.EndStart); ok {
		t.Error("@1 has a start gate")
	}
	// A made gate's fields.
	g := tr.Tasks[done1]
	if g.Title != "Done" || g.Priority != nil || len(g.Tags) != 0 || len(g.Extra) != 0 {
		t.Errorf("made gate: %+v", g)
	}
	if tr.Tasks[start7].Title != "Start" {
		t.Errorf("start gate title %q", tr.Tasks[start7].Title)
	}

	// Its edges, and no others.
	want := map[int64][]int64{
		8: {start7}, done7: {8}, 10: {start9}, done9: {10},
		start9: {done7}, done1: {done7}, 4: {done7},
	}
	for id := range tr.Tasks {
		if got := tr.BlockedBy(id); !reflect.DeepEqual(got, want[id]) && !(len(got) == 0 && want[id] == nil) {
			t.Errorf("@%d blocked_by %v, want %v", id, got, want[id])
		}
	}

	// Only @7.start waits on nothing, so it alone is ready.
	if got := tr.Frontier(); !reflect.DeepEqual(got, []int64{start7}) {
		t.Errorf("frontier = %v, want [@7.start]", got)
	}
	// Containment never orders: no edge joins @1's gates to @9's.
	for _, id := range []int64{start9, done9} {
		for _, b := range tr.BlockedBy(id) {
			if b == done1 {
				t.Errorf("@%d waits on @1.done", id)
			}
		}
	}
	if got := tr.TreeOrder(); !reflect.DeepEqual(got, []int64{1, 4, done1, 7, 8, start7, done7, 9, 10, start9, done9}) {
		t.Errorf("tree order = %v", got)
	}
}

// Each row of "Changes, in the example above".
func TestExampleChanges(t *testing.T) {
	tests := []struct {
		change    string
		task      string
		blockedBy string
		cycle     []int64
	}{
		{"task block 8 --blocked-by 10", "8", "10", []int64{8, 10, start9, done7}},
		{"task block @7.start --blocked-by @9", "@7.start", "@9", []int64{start7, done9, 10, start9, done7, 8}},
		{"task block 8 --blocked-by @7", "8", "@7", []int64{8, done7}},
		{"task block 4 --blocked-by @1", "4", "@1", nil},
	}
	for _, tt := range tests {
		t.Run(tt.change, func(t *testing.T) {
			tr := exampleTree(t)
			before := tr.clone()
			res, err := tr.Block(tt.task, []string{tt.blockedBy}, nil)
			if tt.cycle == nil {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if !res.Changed || !tr.Tasks[4].BlockedBy[done1] {
					t.Errorf("edge not written: %+v", res)
				}
				if bad := tr.Check(); bad != nil {
					t.Errorf("invariants: %v", bad)
				}
				return
			}
			if !core.IsKind(err, core.KindConflict, core.RuleCycle) {
				t.Fatalf("got %v, want a cycle", err)
			}
			e := err.(*core.Error)
			if !reflect.DeepEqual(e.IDs, tt.cycle) {
				t.Errorf("cycle = %v, want %v", e.IDs, tt.cycle)
			}
			if !reflect.DeepEqual(e.Problems, []string{"/blocked_by/0"}) {
				t.Errorf("problems = %v", e.Problems)
			}
			if !reflect.DeepEqual(tr.Tasks, before.Tasks) || tr.Issued != before.Issued {
				t.Error("a refused change changed the tree")
			}
		})
	}
}
