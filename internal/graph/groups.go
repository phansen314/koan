package graph

import (
	"cmp"
	"maps"
	"slices"

	"github.com/phansen314/koan/internal/model"
)

// CycleGroups is the groups of tasks that block each other: the strongly
// connected components of the dependency graph with more than one task
// (implementation-spec.md, Checks). edges is each node's edges, built by
// Edges; a node absent from it has no edges. Each group is ascending, and the
// groups are sorted by their lowest ID.
//
// It is Tarjan's algorithm, written with an explicit stack so a long chain of
// blockers can't overflow the goroutine stack.
func CycleGroups(edges map[model.ID][]model.ID) [][]model.ID {
	index := map[model.ID]int{}
	low := map[model.ID]int{}
	onStack := map[model.ID]bool{}
	var stack []model.ID
	var groups [][]model.ID

	// frame is one node being visited, and how far through its edges.
	type frame struct {
		n    model.ID
		next int
	}
	visit := func(n model.ID, calls *[]frame) {
		index[n] = len(index)
		low[n] = index[n]
		stack = append(stack, n)
		onStack[n] = true
		*calls = append(*calls, frame{n: n})
	}
	for _, root := range slices.Sorted(maps.Keys(edges)) {
		if _, seen := index[root]; seen {
			continue
		}
		var calls []frame
		visit(root, &calls)
		for len(calls) > 0 {
			f := &calls[len(calls)-1]
			if f.next < len(edges[f.n]) {
				m := edges[f.n][f.next]
				f.next++
				if _, seen := index[m]; !seen {
					visit(m, &calls)
				} else if onStack[m] {
					low[f.n] = min(low[f.n], index[m])
				}
				continue
			}
			n := f.n
			calls = calls[:len(calls)-1]
			if len(calls) > 0 {
				p := calls[len(calls)-1].n
				low[p] = min(low[p], low[n])
			}
			if low[n] != index[n] {
				continue
			}
			i := len(stack) - 1 // n's group is the top of the stack
			for stack[i] != n {
				i--
			}
			group := slices.Clone(stack[i:])
			stack = stack[:i]
			for _, m := range group {
				onStack[m] = false
			}
			if len(group) > 1 {
				slices.Sort(group)
				groups = append(groups, group)
			}
		}
	}
	slices.SortFunc(groups, func(a, b []model.ID) int { return cmp.Compare(a[0], b[0]) })
	return groups
}

// ExampleCycle is the cycle doctor reports for a group from CycleGroups: the
// shortest cycle through the group's lowest ID L, ties broken by the
// lexicographically smallest ID sequence, written [L, …, L]: each task
// blocked by the next. It is the cycle check's breadth-first search over the
// group's own edges, from L until an edge leads back to L.
func ExampleCycle(group []model.ID, edges map[model.ID][]model.ID) []model.ID {
	sub := make(map[model.ID][]model.ID, len(group))
	for _, n := range group {
		for _, m := range edges[n] {
			if _, in := slices.BinarySearch(group, m); in {
				sub[n] = append(sub[n], m)
			}
		}
	}
	l := group[0]
	return append(shortestPath(l, l, sub), l)
}
