package graph

import (
	"slices"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

// tasks is a little graph: each task's blockers, with done tasks listed.
type tasks struct {
	blockedBy map[model.ID][]model.ID
	done      []model.ID
	priority  map[model.ID]int64
}

func (g tasks) look(id model.ID) (Node, bool) {
	bs, ok := g.blockedBy[id]
	if !ok {
		return Node{}, false
	}
	n := Node{Open: !slices.Contains(g.done, id), BlockedBy: bs}
	if p, ok := g.priority[id]; ok {
		n.Priority = &p
	}
	return n, true
}

func TestReadiness(t *testing.T) {
	g := tasks{blockedBy: map[model.ID][]model.ID{1: {}, 2: {1}, 3: {9, 2, 1}, 4: {1}}, done: []model.ID{1, 4}}
	for id, want := range map[model.ID]model.Readiness{1: model.Done, 2: model.Ready, 3: model.Blocked, 4: model.Done} {
		n, _ := g.look(id)
		if got, _ := Readiness(n, g.look); got != want {
			t.Errorf("task %d: %s, want %s", id, got, want)
		}
	}
	n, _ := g.look(3)
	if _, blocking := Readiness(n, g.look); !slices.Equal(blocking, []model.ID{2, 9}) {
		t.Errorf("blocking %v: want the open and the missing blocker, ascending", blocking)
	}
}

func TestPath(t *testing.T) {
	g := tasks{blockedBy: map[model.ID][]model.ID{1: {}, 2: {1}, 3: {2}, 4: {3, 1}}, done: []model.ID{2}}
	if p := Path(4, 1, g.look); !slices.Equal(p, []model.ID{4, 1}) {
		t.Errorf("shortest path %v", p)
	}
	if p := Path(3, 1, g.look); !slices.Equal(p, []model.ID{3, 2, 1}) {
		t.Errorf("path through a done task %v", p)
	}
	if p := Path(1, 4, g.look); p != nil {
		t.Errorf("path against the edges %v", p)
	}
}

func TestCycles(t *testing.T) {
	edges := map[model.ID][]model.ID{1: {2}, 2: {3}, 3: {1}, 4: {1}, 5: {6}, 6: {5}, 7: {}}
	got := Cycles(edges)
	if len(got) != 2 || !slices.Equal(got[0], []model.ID{1, 2, 3}) || !slices.Equal(got[1], []model.ID{5, 6}) {
		t.Errorf("cycles %v", got)
	}
	if got := Cycles(map[model.ID][]model.ID{1: {2}, 2: {}}); len(got) != 0 {
		t.Errorf("cycles in a graph without one: %v", got)
	}
}

func TestWhy(t *testing.T) {
	g := tasks{
		blockedBy: map[model.ID][]model.ID{1: {2, 3, 4}, 2: {5}, 3: {}, 4: {}, 5: {}, 6: {7, 99}, 7: {6}},
		done:      []model.ID{4},
		priority:  map[model.ID]int64{5: 9},
	}
	ready, stuck := Why(1, g.look)
	if !slices.Equal(ready, []model.ID{5, 3}) || len(stuck) != 0 {
		t.Errorf("why 1: ready %v (want 5 then 3, by priority), stuck %v", ready, stuck)
	}
	if ready, stuck := Why(3, g.look); !slices.Equal(ready, []model.ID{3}) || len(stuck) != 0 {
		t.Errorf("why of a ready task: %v %v", ready, stuck)
	}
	if ready, stuck := Why(4, g.look); len(ready) != 0 || len(stuck) != 0 {
		t.Errorf("why of a done task: %v %v", ready, stuck)
	}
	if ready, stuck := Why(6, g.look); len(ready) != 0 || !slices.Equal(stuck, []model.ID{6, 7, 99}) {
		t.Errorf("why of a task in a cycle with a missing blocker: %v %v", ready, stuck)
	}
}
