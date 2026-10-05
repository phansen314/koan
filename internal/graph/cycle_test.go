package graph

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

func TestCycles(t *testing.T) {
	// Adding blockers to task 1; each node's blocked_by edges. 9 has no task
	// file.
	edges := map[model.ID][]model.ID{
		1:  {3}, // id's own edges are never expanded
		2:  {1}, // directly: [1 2]
		3:  {4, 5},
		4:  {1}, // 3 reaches 1 through 4 or 5, same length: 4 is smaller
		5:  {1},
		6:  {7, 9},
		7:  {8},
		8:  {1, 6}, // 6 reaches 1 only the long way: [1 6 7 8]
		10: {9},    // a dangling edge, no cycle
		11: {12, 13},
		12: {14}, // 12 is smaller than 13, but its path is longer
		13: {1},
		14: {1},
	}
	ids, cycles := Cycles(1, []model.ID{11, 10, 6, 3, 2, 9}, edges)
	wantIDs := []model.ID{2, 3, 6, 11}
	wantCycles := [][]model.ID{{1, 2}, {1, 3, 4}, {1, 6, 7, 8}, {1, 11, 13}}
	if !reflect.DeepEqual(ids, wantIDs) || !reflect.DeepEqual(cycles, wantCycles) {
		t.Errorf("got %v %v, want %v %v", ids, cycles, wantIDs, wantCycles)
	}
	if ids, cycles := Cycles(1, []model.ID{10, 9}, edges); ids != nil || cycles != nil {
		t.Errorf("no cycle: got %v %v", ids, cycles)
	}
}

// On many small random graphs — with duplicated IDs (several copies, whose
// edges are unioned), IDs with no task file, and edges out of id — Cycles
// agrees with enumerating every simple path from each blocker to id and
// picking the shortest, then lexicographically smallest. Whether a task is
// complete plays no part here: phase 2 follows every loaded node.
func TestCyclesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 5000 {
		n := 2 + rng.IntN(7)
		edges := map[model.ID][]model.ID{}
		for i := 1; i <= n; i++ {
			var copies [][]model.ID
			for range rng.IntN(3) { // 0: no task file
				var bb []model.ID
				for j := 1; j <= n; j++ {
					if j != i && rng.IntN(3) == 0 {
						bb = append(bb, model.ID(j))
					}
				}
				copies = append(copies, bb)
			}
			if copies != nil {
				edges[model.ID(i)] = Edges(copies...)
			}
		}
		id := model.ID(1 + rng.IntN(n))
		var blockers []model.ID
		for _, j := range rng.Perm(n) {
			if b := model.ID(j + 1); b != id && rng.IntN(2) == 0 {
				blockers = append(blockers, b)
			}
		}
		if blockers == nil {
			continue
		}

		var wantIDs []model.ID
		var wantCycles [][]model.ID
		for _, b := range slices.Sorted(slices.Values(blockers)) {
			if p := bruteForce(b, id, edges); p != nil {
				wantIDs = append(wantIDs, b)
				wantCycles = append(wantCycles, append([]model.ID{id}, p...))
			}
		}
		ids, cycles := Cycles(id, blockers, edges)
		if !reflect.DeepEqual(ids, wantIDs) || !reflect.DeepEqual(cycles, wantCycles) {
			t.Fatalf("id %d, blockers %v, edges %v:\ngot  %v %v\nwant %v %v", id, blockers, edges, ids, cycles, wantIDs, wantCycles)
		}
	}
}

// bruteForce enumerates every simple path from b to id and returns the
// shortest, then lexicographically smallest, without id; nil if none.
func bruteForce(b, id model.ID, edges map[model.ID][]model.ID) []model.ID {
	var best []model.ID
	path := []model.ID{b}
	var dfs func(n model.ID)
	dfs = func(n model.ID) {
		for _, m := range edges[n] {
			switch {
			case m == id:
				if best == nil || len(path) < len(best) || len(path) == len(best) && slices.Compare(path, best) < 0 {
					best = slices.Clone(path)
				}
			case !slices.Contains(path, m):
				path = append(path, m)
				dfs(m)
				path = path[:len(path)-1]
			}
		}
	}
	dfs(b)
	return best
}

func TestEdges(t *testing.T) {
	got := Edges([]model.ID{5, 2}, nil, []model.ID{2, 9, 1})
	if want := []model.ID{1, 2, 5, 9}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := Edges(); len(got) != 0 {
		t.Errorf("no copies: %v", got)
	}
}
