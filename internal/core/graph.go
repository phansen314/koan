package core

import (
	"slices"
	"sort"
	"strings"
)

// Graph is the waits-on graph of tasks, as readiness reads it.
type Graph interface {
	// Open reports whether a task exists and is open.
	Open(id int64) bool
	// BlockedBy is the set of tasks a task waits on.
	BlockedBy(id int64) []int64
}

// Ready reports whether a task is ready: open, with nothing in its
// blocked_by open. A complete task is neither ready nor blocked.
func Ready(g Graph, id int64) bool {
	if !g.Open(id) {
		return false
	}
	for _, b := range g.BlockedBy(id) {
		if g.Open(b) {
			return false
		}
	}
	return true
}

// Blocked reports whether a task is open and waits on an open task.
func Blocked(g Graph, id int64) bool { return g.Open(id) && !Ready(g, id) }

// Ranked is a task as work order sees it.
type Ranked struct {
	ID       int64
	Priority *int64
}

// WorkOrderLess orders by priority, highest first, null last; then by ID,
// lowest first.
func WorkOrderLess(a, b Ranked) bool {
	switch {
	case a.Priority != nil && b.Priority == nil:
		return true
	case a.Priority == nil && b.Priority != nil:
		return false
	case a.Priority != nil && *a.Priority != *b.Priority:
		return *a.Priority > *b.Priority
	}
	return a.ID < b.ID
}

// SortWorkOrder sorts tasks into work order.
func SortWorkOrder(tasks []Ranked) {
	sort.SliceStable(tasks, func(i, j int) bool { return WorkOrderLess(tasks[i], tasks[j]) })
}

// Frontier is the ready tasks among candidates that have no claim, in work
// order.
func Frontier(g Graph, claimed func(id int64) bool, candidates []Ranked) []Ranked {
	var out []Ranked
	for _, c := range candidates {
		if Ready(g, c.ID) && !claimed(c.ID) {
			out = append(out, c)
		}
	}
	SortWorkOrder(out)
	return out
}

// Placed is a task or folder as tree order sees it. Parent is RootID for
// something in the root.
type Placed struct {
	ID     int64
	Folder bool
	Parent int64
	Name   string // a folder's name
}

// TreeOrder lists IDs with a folder before everything under it. Within a
// folder its own tasks come first, gates included, by ID; then its folders,
// by name compared byte for byte.
func TreeOrder(entries []Placed) []int64 {
	tasks := map[int64][]Placed{}
	folders := map[int64][]Placed{}
	for _, e := range entries {
		if e.Folder {
			folders[e.Parent] = append(folders[e.Parent], e)
		} else {
			tasks[e.Parent] = append(tasks[e.Parent], e)
		}
	}
	out := make([]int64, 0, len(entries))
	var visit func(parent int64)
	visit = func(parent int64) {
		ts := tasks[parent]
		slices.SortFunc(ts, func(a, b Placed) int { return cmpInt(a.ID, b.ID) })
		for _, t := range ts {
			out = append(out, t.ID)
		}
		fs := folders[parent]
		slices.SortFunc(fs, func(a, b Placed) int { return strings.Compare(a.Name, b.Name) })
		for _, f := range fs {
			out = append(out, f.ID)
			visit(f.ID)
		}
	}
	visit(RootID)
	return out
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Edge is one entry of a task's blocked_by: From waits on To.
type Edge struct {
	From, To int64
}

// FindCycle adds edges, in order, to the graph blockedBy describes, and
// reports the first that closes a cycle. The cycle starts at the task whose
// blocked_by gains the closing edge, then the task it now waits on, then
// each task the one before waits on, up to the last, which waits on the
// first. It returns a nil cycle and -1 when no edge closes one.
//
// An edge from a task to itself is a cycle of one; a change refuses it as
// invalid-input before it gets here.
func FindCycle(blockedBy func(id int64) []int64, edges []Edge) (cycle []int64, closing int) {
	added := map[int64][]int64{}
	next := func(id int64) []int64 {
		out := slices.Concat(blockedBy(id), added[id])
		slices.Sort(out)
		return slices.Compact(out)
	}
	for i, e := range edges {
		if path := shortestPath(next, e.To, e.From); path != nil {
			// path runs e.To … e.From; the cycle is e.From, then e.To … the
			// task just before e.From.
			return append([]int64{e.From}, path[:len(path)-1]...), i
		}
		added[e.From] = append(added[e.From], e.To)
	}
	return nil, -1
}

// shortestPath finds the shortest waits-on path from one task to another,
// trying lower IDs first, so the answer doesn't depend on map order.
func shortestPath(next func(int64) []int64, from, to int64) []int64 {
	if from == to {
		return []int64{from}
	}
	prev := map[int64]int64{from: from}
	queue := []int64{from}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, n := range next(at) {
			if _, seen := prev[n]; seen {
				continue
			}
			prev[n] = at
			if n == to {
				path := []int64{to}
				for p := at; p != from; p = prev[p] {
					path = append(path, p)
				}
				path = append(path, from)
				slices.Reverse(path)
				return path
			}
			queue = append(queue, n)
		}
	}
	return nil
}
