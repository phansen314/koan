// Package graph answers questions about the blocked_by graph on tasks
// already loaded: readiness, cycles, and what a task waits on. It does no
// I/O (spec/implementation.md, The graph).
package graph

import (
	"cmp"
	"maps"
	"slices"

	"github.com/phansen314/koan/internal/model"
)

// Node is what the graph needs to know of one task.
type Node struct {
	Open      bool
	Priority  *int64
	BlockedBy []model.ID
}

// Lookup returns a task's node; ok is false when the task is missing or its
// file is unusable. A lookup may load the file, so the functions here call it
// only for tasks they reach.
type Lookup func(model.ID) (n Node, ok bool)

// Readiness is a task's readiness and the blockers that block it now, in
// ascending order: every one that is open, missing, or unusable.
func Readiness(n Node, look Lookup) (model.Readiness, []model.ID) {
	open := []model.ID{}
	if !n.Open {
		return model.Done, open
	}
	for _, b := range n.BlockedBy {
		if bn, ok := look(b); !ok || bn.Open {
			open = append(open, b)
		}
	}
	slices.Sort(open)
	if len(open) > 0 {
		return model.Blocked, open
	}
	return model.Ready, open
}

// Path is a shortest path from `from` to `to` along blocked_by, through
// open and done tasks alike, both ends included; nil when there is none.
// Adding "to is blocked by from" closes a cycle exactly when one exists.
func Path(from, to model.ID, look Lookup) []model.ID {
	parent := map[model.ID]model.ID{from: from}
	queue := []model.ID{from}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n == to {
			path := []model.ID{n}
			for n != from {
				n = parent[n]
				path = append(path, n)
			}
			slices.Reverse(path)
			return path
		}
		node, _ := look(n)
		for _, m := range node.BlockedBy {
			if _, seen := parent[m]; !seen {
				parent[m] = n
				queue = append(queue, m)
			}
		}
	}
	return nil
}

// Cycles returns one cycle for each group of tasks that block each other,
// each task blocked by the next and the last by the first. Groups are in
// order of their lowest ID, and each cycle starts at it.
func Cycles(edges map[model.ID][]model.ID) [][]model.ID {
	var cycles [][]model.ID
	for _, group := range components(edges) {
		in := map[model.ID]bool{}
		for _, n := range group {
			in[n] = true
		}
		start := group[0]
		look := func(n model.ID) (Node, bool) {
			var bs []model.ID
			for _, m := range edges[n] {
				if in[m] {
					bs = append(bs, m)
				}
			}
			return Node{BlockedBy: bs}, true
		}
		// The shortest way back to start from one of its blockers in the group.
		var best []model.ID
		first, _ := look(start)
		for _, b := range first.BlockedBy {
			if p := Path(b, start, look); p != nil && (best == nil || len(p) < len(best)) {
				best = p
			}
		}
		cycles = append(cycles, append([]model.ID{start}, best[:len(best)-1]...))
	}
	return cycles
}

// components are the strongly connected components of more than one task,
// each sorted, in order of their lowest ID (Tarjan's algorithm, iterative).
func components(edges map[model.ID][]model.ID) [][]model.ID {
	index := map[model.ID]int{}
	low := map[model.ID]int{}
	onStack := map[model.ID]bool{}
	var stack []model.ID
	var groups [][]model.ID

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

// WorkOrder compares two tasks: higher priority first, none last, then
// lower ID first.
func WorkOrder(aID model.ID, aPri *int64, bID model.ID, bPri *int64) int {
	switch {
	case aPri != nil && bPri != nil && *aPri != *bPri:
		return cmp.Compare(*bPri, *aPri)
	case aPri != nil && bPri == nil:
		return -1
	case aPri == nil && bPri != nil:
		return 1
	}
	return cmp.Compare(aID, bID)
}

// Why follows a task's blockers, and theirs, through open tasks. ready are
// the ready tasks it waits on, in work order (the task itself when it is
// ready); stuck are the IDs no work will clear, ascending: blockers that are
// missing or unusable, and tasks that block each other.
func Why(id model.ID, look Lookup) (ready, stuck []model.ID) {
	ready, stuck = []model.ID{}, []model.ID{}
	nodes := map[model.ID]Node{}
	edges := map[model.ID][]model.ID{}
	seen := map[model.ID]bool{id: true}
	queue := []model.ID{id}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		node, ok := look(n)
		if !ok {
			stuck = append(stuck, n)
			continue
		}
		if !node.Open {
			continue
		}
		nodes[n] = node
		_, open := Readiness(node, look)
		if len(open) == 0 {
			ready = append(ready, n)
		}
		edges[n] = open
		for _, b := range open {
			if !seen[b] {
				seen[b] = true
				queue = append(queue, b)
			}
		}
	}
	for _, group := range components(edges) {
		stuck = append(stuck, group...)
	}
	slices.Sort(stuck)
	slices.SortFunc(ready, func(a, b model.ID) int {
		return WorkOrder(a, nodes[a].Priority, b, nodes[b].Priority)
	})
	return ready, stuck
}
