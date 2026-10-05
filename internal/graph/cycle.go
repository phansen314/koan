package graph

import (
	"slices"

	"github.com/phansen314/ftask/internal/model"
)

// Cycles is the cycle check's phase 2 (implementation-spec.md, Cycle check):
// whether adding "id is blocked by B" creates a cycle, for each B in
// blockers. edges is the subgraph phase 1 loaded, each node's edges built by
// Edges; a node absent from it has no edges. blockers must not contain id.
//
// It returns the offending blockers, ascending, and for each the shortest
// cycle through it, ties broken by the lexicographically smallest ID
// sequence, written [id, B, …]: each task blocked by the next, the last
// blocked by id. Both are empty when no blocker creates a cycle.
func Cycles(id model.ID, blockers []model.ID, edges map[model.ID][]model.ID) (ids []model.ID, cycles [][]model.ID) {
	for _, b := range slices.Sorted(slices.Values(blockers)) {
		if path := shortestPath(b, id, edges); path != nil {
			ids = append(ids, b)
			cycles = append(cycles, append([]model.ID{id}, path...))
		}
	}
	return ids, cycles
}

// Edges is a node's edges in the dependency graph, which is keyed by ID: the
// union of every copy's blocked_by, ascending, without repeats.
func Edges(copies ...[]model.ID) []model.ID {
	e := slices.Concat(copies...)
	slices.Sort(e)
	return slices.Compact(e)
}

// shortestPath is the lexicographically smallest shortest path from b to id,
// excluding id itself, or nil if id is unreachable. It is a breadth-first
// search with a FIFO queue, visiting each node's edges in ascending order and
// never expanding id, so the first time id is discovered is through the
// required path (implementation-spec.md, Why the first path found is the one
// required).
func shortestPath(b, id model.ID, edges map[model.ID][]model.ID) []model.ID {
	parent := map[model.ID]model.ID{b: b}
	queue := []model.ID{b}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, m := range edges[n] {
			if m == id {
				return pathTo(n, b, parent)
			}
			if _, seen := parent[m]; !seen {
				parent[m] = n
				queue = append(queue, m)
			}
		}
	}
	return nil
}

// pathTo follows parent pointers from n back to b, returning b … n.
func pathTo(n, b model.ID, parent map[model.ID]model.ID) []model.ID {
	path := []model.ID{n}
	for n != b {
		n = parent[n]
		path = append(path, n)
	}
	slices.Reverse(path)
	return path
}
