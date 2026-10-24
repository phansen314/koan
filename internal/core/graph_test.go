package core

import (
	"reflect"
	"slices"
	"testing"
)

// fakeGraph is a waits-on graph: open tasks and their blocked_by.
type fakeGraph struct {
	open      map[int64]bool
	blockedBy map[int64][]int64
}

func (g fakeGraph) Open(id int64) bool         { return g.open[id] }
func (g fakeGraph) BlockedBy(id int64) []int64 { return g.blockedBy[id] }

// exampleGraph is the edges of the rules doc's example, every task open,
// with the gate IDs of newFakeTree: @51 = @7.start, @52 = @7.done,
// @53 = @9.start, @54 = @9.done, @55 = @1.done.
func exampleGraph() fakeGraph {
	g := fakeGraph{open: map[int64]bool{}, blockedBy: map[int64][]int64{
		8:  {51}, // @8 → @7.start
		52: {8},  // @7.done → @8
		10: {53}, // @10 → @9.start
		54: {10}, // @9.done → @10
		53: {52}, // @9.start → @7.done
		55: {52}, // @1.done → @7.done
		4:  {52}, // @4 → @7.done
	}}
	for _, id := range []int64{4, 8, 10, 51, 52, 53, 54, 55} {
		g.open[id] = true
	}
	return g
}

// Each row of "Changes, in the example above".
func TestCycleExample(t *testing.T) {
	tests := []struct {
		change string
		edge   Edge
		cycle  []int64
	}{
		// @8 → @10 → @9.start → @7.done → @8
		{"task block 8 10", Edge{8, 10}, []int64{8, 10, 53, 52}},
		// @7.start → @9.done → @10 → @9.start → @7.done → @8 → @7.start
		{"task block @7.start @9", Edge{51, 54}, []int64{51, 54, 10, 53, 52, 8}},
		// @8 → @7.done → @8
		{"task block 8 @7", Edge{8, 52}, []int64{8, 52}},
		// Allowed.
		{"task block 4 @1", Edge{4, 55}, nil},
	}
	g := exampleGraph()
	for _, tt := range tests {
		t.Run(tt.change, func(t *testing.T) {
			cycle, closing := FindCycle(g.BlockedBy, []Edge{tt.edge})
			if !reflect.DeepEqual(cycle, tt.cycle) {
				t.Errorf("cycle = %v, want %v", cycle, tt.cycle)
			}
			if wantClosing := map[bool]int{true: -1, false: 0}[tt.cycle == nil]; closing != wantClosing {
				t.Errorf("closing = %d, want %d", closing, wantClosing)
			}
		})
	}
}

func TestCycleAcrossEdges(t *testing.T) {
	g := fakeGraph{blockedBy: map[int64][]int64{}}
	// 1 → 2, then 2 → 3, then 3 → 1 closes 3 → 1 → 2 → 3.
	cycle, closing := FindCycle(g.BlockedBy, []Edge{{1, 2}, {2, 3}, {3, 1}})
	if closing != 2 || !reflect.DeepEqual(cycle, []int64{3, 1, 2}) {
		t.Errorf("got %v at %d", cycle, closing)
	}
	// An edge to itself is a cycle of one.
	cycle, closing = FindCycle(g.BlockedBy, []Edge{{5, 5}})
	if closing != 0 || !reflect.DeepEqual(cycle, []int64{5}) {
		t.Errorf("got %v at %d", cycle, closing)
	}
	// A diamond is no cycle.
	cycle, _ = FindCycle(g.BlockedBy, []Edge{{1, 2}, {1, 3}, {2, 4}, {3, 4}})
	if cycle != nil {
		t.Errorf("diamond: got %v", cycle)
	}
}

func TestReadinessExample(t *testing.T) {
	g := exampleGraph()
	var ready []int64
	for id := range g.open {
		if Ready(g, id) {
			ready = append(ready, id)
		}
	}
	slices.Sort(ready)
	// Only @7.start waits on nothing.
	if !reflect.DeepEqual(ready, []int64{51}) {
		t.Errorf("ready = %v, want [51]", ready)
	}
	if !Blocked(g, 8) {
		t.Error("@8 should be blocked by @7.start")
	}

	// Closing @7.start readies @8; a complete task is neither.
	g.open[51] = false
	if !Ready(g, 8) || Ready(g, 51) || Blocked(g, 51) {
		t.Error("after completing @7.start")
	}
	// @4 waits on @7.done, so it still blocks.
	if !Blocked(g, 4) {
		t.Error("@4 should still wait on @7.done")
	}
}

func p(n int64) *int64 { return &n }

func TestWorkOrder(t *testing.T) {
	tasks := []Ranked{{5, nil}, {3, p(1)}, {9, p(5)}, {2, nil}, {4, p(1)}, {1, p(-3)}}
	SortWorkOrder(tasks)
	var ids []int64
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	if want := []int64{9, 3, 4, 1, 2, 5}; !reflect.DeepEqual(ids, want) {
		t.Errorf("work order = %v, want %v", ids, want)
	}
}

func TestFrontier(t *testing.T) {
	g := fakeGraph{
		open:      map[int64]bool{1: true, 2: true, 3: true, 4: true, 6: true},
		blockedBy: map[int64][]int64{2: {1}, 4: {5}},
	}
	claimed := func(id int64) bool { return id == 3 }
	// 1 is ready; 2 waits on 1; 3 is claimed; 4 waits on 5, which is
	// complete; 5 is complete; 6 is ready with a priority.
	cands := []Ranked{{1, nil}, {2, nil}, {3, nil}, {4, p(0)}, {5, nil}, {6, p(2)}}
	var ids []int64
	for _, r := range Frontier(g, claimed, cands) {
		ids = append(ids, r.ID)
	}
	if want := []int64{6, 4, 1}; !reflect.DeepEqual(ids, want) {
		t.Errorf("frontier = %v, want %v", ids, want)
	}
}

func TestTreeOrder(t *testing.T) {
	// The example: /work with @4 and its done gate, then phase1 and phase2.
	entries := []Placed{
		{ID: 9, Folder: true, Parent: 1, Name: "phase2"},
		{ID: 1, Folder: true, Parent: RootID, Name: "work"},
		{ID: 7, Folder: true, Parent: 1, Name: "phase1"},
		{ID: 55, Parent: 1}, {ID: 4, Parent: 1},
		{ID: 52, Parent: 7}, {ID: 8, Parent: 7}, {ID: 51, Parent: 7},
		{ID: 54, Parent: 9}, {ID: 53, Parent: 9}, {ID: 10, Parent: 9},
	}
	want := []int64{1, 4, 55, 7, 8, 51, 52, 9, 10, 53, 54}
	if got := TreeOrder(entries); !reflect.DeepEqual(got, want) {
		t.Errorf("tree order = %v, want %v", got, want)
	}

	// Root tasks first; names byte for byte, so Zeta before alpha.
	entries = []Placed{
		{ID: 2, Folder: true, Parent: RootID, Name: "alpha"},
		{ID: 3, Folder: true, Parent: RootID, Name: "Zeta"},
		{ID: 6, Parent: RootID}, {ID: 4, Parent: 3}, {ID: 5, Parent: 2},
	}
	want = []int64{6, 3, 4, 2, 5}
	if got := TreeOrder(entries); !reflect.DeepEqual(got, want) {
		t.Errorf("tree order = %v, want %v", got, want)
	}
}
