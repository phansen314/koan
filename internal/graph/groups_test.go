package graph

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/phansen314/ftask/internal/model"
)

func TestCycleGroups(t *testing.T) {
	edges := map[model.ID][]model.ID{
		1: {2},
		2: {3, 9}, // 9 has no task file
		3: {1},
		4: {5},    // 4 and 5 block each other
		5: {4, 6}, // 6 is reached, but reaches nothing back
		7: {8},    // no cycle
	}
	groups := CycleGroups(edges)
	if want := [][]model.ID{{1, 2, 3}, {4, 5}}; !reflect.DeepEqual(groups, want) {
		t.Fatalf("got %v, want %v", groups, want)
	}
	if got, want := ExampleCycle(groups[0], edges), []model.ID{1, 2, 3, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("example: got %v, want %v", got, want)
	}
	if got := CycleGroups(map[model.ID][]model.ID{1: {2}}); got != nil {
		t.Errorf("acyclic: got %v", got)
	}
}

// A chain far longer than a recursive search could follow on a goroutine's
// stack, closed into one cycle.
func TestCycleGroupsLongChain(t *testing.T) {
	const n = 1_000_000
	edges := make(map[model.ID][]model.ID, n)
	for i := model.ID(1); i < n; i++ {
		edges[i] = []model.ID{i + 1}
	}
	edges[n] = []model.ID{1}
	groups := CycleGroups(edges)
	if len(groups) != 1 || len(groups[0]) != n {
		t.Fatalf("got %d groups", len(groups))
	}
	if c := ExampleCycle(groups[0], edges); len(c) != n+1 || c[0] != 1 || c[n] != 1 {
		t.Errorf("example cycle of length %d", len(c))
	}
}

// A long chain with no cycle has no groups, and takes linear time: each
// node's group is found at the top of the stack, not by scanning the whole
// path below it.
func TestCycleGroupsLongAcyclicChain(t *testing.T) {
	const n = 1_000_000
	edges := make(map[model.ID][]model.ID, n)
	for i := model.ID(1); i < n; i++ {
		edges[i] = []model.ID{i + 1}
	}
	if groups := CycleGroups(edges); len(groups) != 0 {
		t.Fatalf("got %d groups", len(groups))
	}
}

// On many small random graphs, CycleGroups agrees with the transitive
// closure — two IDs share a group exactly when each reaches the other — and
// ExampleCycle with enumerating every simple cycle through the group's lowest
// ID and picking the shortest, then lexicographically smallest.
func TestCycleGroupsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 5000 {
		n := 1 + rng.IntN(8)
		edges := map[model.ID][]model.ID{}
		for i := 1; i <= n; i++ {
			if rng.IntN(5) == 0 {
				continue // no task file
			}
			var bb []model.ID
			for j := 1; j <= n+1; j++ { // n+1: an ID with no task file
				if j != i && rng.IntN(3) == 0 {
					bb = append(bb, model.ID(j))
				}
			}
			edges[model.ID(i)] = bb
		}

		reach := func(a, b model.ID) bool {
			seen := map[model.ID]bool{a: true}
			queue := []model.ID{a}
			for len(queue) > 0 {
				x := queue[0]
				queue = queue[1:]
				for _, y := range edges[x] {
					if y == b {
						return true
					}
					if !seen[y] {
						seen[y] = true
						queue = append(queue, y)
					}
				}
			}
			return false
		}
		var want [][]model.ID
		grouped := map[model.ID]bool{}
		for a := model.ID(1); a <= model.ID(n); a++ {
			if grouped[a] {
				continue
			}
			g := []model.ID{a}
			for b := a + 1; b <= model.ID(n); b++ {
				if reach(a, b) && reach(b, a) {
					g = append(g, b)
				}
			}
			if len(g) > 1 {
				for _, b := range g {
					grouped[b] = true
				}
				want = append(want, g)
			}
		}

		got := CycleGroups(edges)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("edges %v:\ngot  %v\nwant %v", edges, got, want)
		}
		for _, g := range got {
			l := g[0]
			if c, w := ExampleCycle(g, edges), append([]model.ID{l}, append(bruteForce(l, l, edges), l)[1:]...); !reflect.DeepEqual(c, w) {
				t.Fatalf("edges %v, group %v:\ngot  %v\nwant %v", edges, g, c, w)
			}
		}
	}
}

func TestExampleCycleIsACycle(t *testing.T) {
	edges := map[model.ID][]model.ID{1: {3, 2}, 2: {1}, 3: {2}}
	c := ExampleCycle([]model.ID{1, 2, 3}, edges)
	for i := 0; i+1 < len(c); i++ {
		if !slices.Contains(edges[c[i]], c[i+1]) {
			t.Fatalf("%v: %d is not blocked by %d", c, c[i], c[i+1])
		}
	}
	if want := []model.ID{1, 2, 1}; !reflect.DeepEqual(c, want) {
		t.Errorf("got %v, want %v", c, want)
	}
}
