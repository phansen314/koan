// Package graph answers questions about the blocked_by graph on tasks and
// folders already loaded: readiness, done-ness, cycles, and what a task or
// folder waits on. It does no I/O (spec/implementation.md, The graph).
package graph

import (
	"cmp"
	"maps"
	"slices"

	"github.com/phansen314/koan/internal/model"
)

// Kind is what an ID names.
type Kind uint8

const (
	None       Kind = iota // nothing: a dangling reference
	TaskKind               // a task, usable or not
	FolderKind             // a folder
)

// Item is what an ID names, and where: a folder's own path, or the folder
// holding a task.
type Item struct {
	Kind   Kind
	Folder model.Folder
}

// Node is what the graph needs to know of one task.
type Node struct {
	Open      bool
	Priority  *int64
	BlockedBy []model.ID
}

// Folder is what the graph needs to know of one folder's contents.
type Folder struct {
	ID      model.ID       // 0 when it has none
	Tasks   []model.ID     // the task files directly in it, usable or not
	Folders []model.Folder // the folders directly in it
}

// Tree is the graph's view of the tree. Its methods may load files, so the
// functions here call them only for what they reach.
type Tree interface {
	// Resolve is what the blocker id names.
	Resolve(id model.ID) Item
	// TaskFolder is the folder holding task id's file, usable or not; ok
	// is false when there is none.
	TaskFolder(id model.ID) (f model.Folder, ok bool)
	// Task is task id's node; ok is false when no task has the ID, or its
	// file can't be used.
	Task(id model.ID) (n Node, ok bool)
	// Folder is folder f's contents, the root's included.
	Folder(f model.Folder) Folder
	// Blockers is folder f's blocked_by. known is false when its folder
	// file is missing or can't be used, and broken when it can't be used.
	// The root's are known, and empty.
	Blockers(f model.Folder) (bs []model.ID, known, broken bool)
}

// Graph answers questions about one Tree, computing each folder's
// done-ness once.
type Graph struct {
	t    Tree
	done map[model.Folder]bool
}

// New is the graph of t.
func New(t Tree) *Graph { return &Graph{t: t, done: map[model.Folder]bool{}} }

// Done reports whether folder f is done: every task under it, at any depth,
// done. An empty folder is done, and a task file that can't be used counts
// as not done.
func (g *Graph) Done(f model.Folder) bool {
	if d, ok := g.done[f]; ok {
		return d
	}
	fo := g.t.Folder(f)
	d := true
	for _, id := range fo.Tasks {
		if n, ok := g.t.Task(id); !ok || n.Open {
			d = false
			break
		}
	}
	for _, sub := range fo.Folders {
		if !d {
			break
		}
		d = g.Done(sub)
	}
	g.done[f] = d
	return d
}

// Blocks reports whether the blocker id blocks now: an open task, a folder
// not done, or an ID that names nothing or a task whose file can't be used.
func (g *Graph) Blocks(id model.ID) bool {
	it := g.t.Resolve(id)
	switch it.Kind {
	case TaskKind:
		n, ok := g.t.Task(id)
		return !ok || n.Open
	case FolderKind:
		return !g.Done(it.Folder)
	}
	return true
}

// blocking is the blockers of bs that block now, ascending.
func (g *Graph) blocking(bs []model.ID) []model.ID {
	out := []model.ID{}
	for _, b := range bs {
		if g.Blocks(b) {
			out = append(out, b)
		}
	}
	slices.Sort(out)
	return out
}

// State is the derived state of a task or folder.
type State struct {
	Readiness model.Readiness
	Open      []model.ID        // every ID that blocks it now, own and inherited, ascending
	Inherited []model.Inherited // the folders above it that block it now, outermost first
}

func done() State {
	return State{Readiness: model.Done, Open: []model.ID{}, Inherited: []model.Inherited{}}
}

// inherited is what the folders from the root's child down to f, f
// included, block with: each that has a blocker that blocks now, or
// unknown blockers. unknown reports whether any has.
func (g *Graph) inherited(f model.Folder) (inh []model.Inherited, unknown bool) {
	inh = []model.Inherited{}
	for _, a := range Chain(f) {
		bs, known, _ := g.t.Blockers(a)
		if !known {
			inh = append(inh, model.Inherited{Folder: a})
			unknown = true
			continue
		}
		if ids := g.blocking(bs); len(ids) > 0 {
			inh = append(inh, model.Inherited{Folder: a, IDs: ids})
		}
	}
	return inh, unknown
}

// Chain lists the folders from the root's child down to f, f included: the
// folders whose blockers what is in f inherits. It is empty for the root.
func Chain(f model.Folder) []model.Folder {
	var fs []model.Folder
	for ; f != model.Root; f = f.Parent() {
		fs = append(fs, f)
	}
	slices.Reverse(fs)
	return fs
}

// state puts own and inherited blockers together.
func state(own []model.ID, inh []model.Inherited, unknown bool) State {
	open := slices.Clone(own)
	for _, in := range inh {
		open = append(open, in.IDs...)
	}
	slices.Sort(open)
	open = slices.Compact(open)
	if len(open) == 0 && !unknown {
		return State{Readiness: model.Ready, Open: []model.ID{}, Inherited: []model.Inherited{}}
	}
	return State{Readiness: model.Blocked, Open: open, Inherited: inh}
}

// TaskState is the state of task n, in folder f.
func (g *Graph) TaskState(n Node, f model.Folder) State {
	if !n.Open {
		return done()
	}
	inh, unknown := g.inherited(f)
	return state(g.blocking(n.BlockedBy), inh, unknown)
}

// FolderState is the state of folder f: done, or else blocked by its own
// blockers and those of the folders above it, which it inherits. The root is
// never blocked.
func (g *Graph) FolderState(f model.Folder) State {
	if g.Done(f) {
		return done()
	}
	if f == model.Root {
		return state(nil, nil, false)
	}
	bs, known, _ := g.t.Blockers(f)
	inh, unknown := g.inherited(f.Parent())
	return state(g.blocking(bs), inh, unknown || !known)
}

// NodeKind is which node of the cycle graph a Ref is: a task's, or one of a
// folder's two.
type NodeKind uint8

const (
	TaskNode NodeKind = iota // a task
	WaitNode                 // what a folder's contents wait on
	DoneNode                 // what a folder waits on to be done
)

// Ref is one node of the cycle graph: a task by its ID, a folder's by its
// path, since a folder with no folder file has no ID and is in the graph all
// the same.
type Ref struct {
	Kind   NodeKind
	ID     model.ID
	Folder model.Folder
}

func TaskRef(id model.ID) Ref   { return Ref{Kind: TaskNode, ID: id} }
func WaitOf(f model.Folder) Ref { return Ref{Kind: WaitNode, Folder: f} }
func DoneOf(f model.Folder) Ref { return Ref{Kind: DoneNode, Folder: f} }

// IsFolder reports whether r is one of a folder's nodes.
func (r Ref) IsFolder() bool { return r.Kind != TaskNode }

func (r Ref) same(s Ref) bool { return r.IsFolder() && s.IsFolder() && r.Folder == s.Folder }

// folderNode reports whether folder f has nodes: every folder but the root.
func folderNode(f model.Folder) bool { return f != model.Root }

// target is the node a blocker ID leads to: a task's, or a folder's done
// node. ok is false for an ID that names nothing.
func (g *Graph) target(id model.ID) (Ref, bool) {
	it := g.t.Resolve(id)
	switch it.Kind {
	case TaskKind:
		return TaskRef(id), true
	case FolderKind:
		return DoneOf(it.Folder), folderNode(it.Folder)
	}
	return Ref{}, false
}

// Edges are the nodes r waits on, in the graph of spec/design.md
// (Invariants). broken is true when r's file can't be used, so its edges
// are unknown.
func (g *Graph) Edges(r Ref) (out []Ref, broken bool) {
	add := func(bs []model.ID) {
		for _, b := range bs {
			if t, ok := g.target(b); ok {
				out = append(out, t)
			}
		}
	}
	switch r.Kind {
	case TaskNode:
		f, exists := g.t.TaskFolder(r.ID)
		n, ok := g.t.Task(r.ID)
		if !ok {
			return nil, exists
		}
		add(n.BlockedBy)
		if folderNode(f) {
			out = append(out, WaitOf(f))
		}
	case WaitNode:
		bs, _, broken := g.t.Blockers(r.Folder)
		if broken {
			return nil, true
		}
		add(bs)
		if p := r.Folder.Parent(); folderNode(p) {
			out = append(out, WaitOf(p))
		}
	case DoneNode:
		fo := g.t.Folder(r.Folder)
		for _, id := range fo.Tasks {
			out = append(out, TaskRef(id))
		}
		for _, sub := range fo.Folders {
			out = append(out, DoneOf(sub))
		}
		out = append(out, WaitOf(r.Folder))
	}
	return out, false
}

// CycleThrough finds a cycle through one of the nodes changed, the first
// that has one: a shortest path from it back to itself, starting at it. A
// node met whose file can't be used is returned as broken, the first one
// met, when no cycle is found: a cycle through it can't be ruled out.
func (g *Graph) CycleThrough(changed []Ref) (cycle []Ref, broken *Ref) {
	for _, c := range changed {
		parent := map[Ref]Ref{c: c}
		queue := []Ref{c}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			out, bad := g.Edges(n)
			if bad && broken == nil {
				b := n
				broken = &b
			}
			for _, m := range out {
				if m == c {
					path := []Ref{n}
					for n != c {
						n = parent[n]
						path = append(path, n)
					}
					slices.Reverse(path)
					return path, nil
				}
				if _, seen := parent[m]; !seen {
					parent[m] = n
					queue = append(queue, m)
				}
			}
		}
	}
	return nil, broken
}

// Collapse is a cycle as its tasks and folders: a folder's two nodes next to
// each other, the last and the first included, are one entry, the first of
// them. A folder the cycle passes at two places is at each.
func Collapse(cycle []Ref) []Ref {
	var out []Ref
	for _, r := range cycle {
		if len(out) > 0 && out[len(out)-1].same(r) {
			continue
		}
		out = append(out, r)
	}
	if len(out) > 1 && out[0].same(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out
}

// Cycles finds a cycle in each group of nodes that wait on each other,
// among the nodes given and the edges between them. key is what orders the
// groups and picks where each cycle starts: the lowest key in the group,
// 0 counting as none; ties and groups with none go in the order of nodes.
func (g *Graph) Cycles(nodes []Ref, key func(Ref) model.ID) [][]Ref {
	index := map[Ref]model.ID{}
	for i, n := range nodes {
		index[n] = model.ID(i)
	}
	edges := map[model.ID][]model.ID{}
	for i, n := range nodes {
		out, _ := g.Edges(n)
		es := []model.ID{}
		for _, m := range out {
			if j, ok := index[m]; ok {
				es = append(es, j)
			}
		}
		edges[model.ID(i)] = es
	}
	type found struct {
		key, first model.ID
		cycle      []Ref
	}
	var all []found
	for _, group := range components(edges) {
		in := map[Ref]bool{}
		start := group[0]
		for _, i := range group {
			in[nodes[i]] = true
			if k := key(nodes[i]); k != 0 && (key(nodes[start]) == 0 || k < key(nodes[start])) {
				start = i
			}
		}
		cycle := g.cycleWithin(nodes[start], in)
		all = append(all, found{key(nodes[start]), group[0], cycle})
	}
	slices.SortStableFunc(all, func(a, b found) int {
		switch {
		case a.key != 0 && b.key != 0:
			return cmp.Or(cmp.Compare(a.key, b.key), cmp.Compare(a.first, b.first))
		case a.key != 0:
			return -1
		case b.key != 0:
			return 1
		}
		return cmp.Compare(a.first, b.first)
	})
	cycles := make([][]Ref, len(all))
	for i, f := range all {
		cycles[i] = f.cycle
	}
	return cycles
}

// cycleWithin is a shortest cycle through start that stays among the nodes
// in.
func (g *Graph) cycleWithin(start Ref, in map[Ref]bool) []Ref {
	parent := map[Ref]Ref{start: start}
	queue := []Ref{start}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		out, _ := g.Edges(n)
		for _, m := range out {
			if !in[m] {
				continue
			}
			if m == start {
				path := []Ref{n}
				for n != start {
					n = parent[n]
					path = append(path, n)
				}
				slices.Reverse(path)
				return path
			}
			if _, seen := parent[m]; !seen {
				parent[m] = n
				queue = append(queue, m)
			}
		}
	}
	return []Ref{start}
}

// components are the strongly connected components of more than one node,
// each sorted, in order of their lowest node (Tarjan's algorithm,
// iterative).
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

// Why follows what the tasks given wait on, and what the folders given wait
// on to be done, through open tasks and folders not done: a folder is
// followed into the open tasks under it, at any depth. ready are the ready
// tasks reached, in work order; stuck are the IDs no work will clear,
// ascending: blockers that are missing or unusable, tasks under a folder
// followed whose files are unusable, and tasks and folders that wait on each
// other; stuckFolders are the folders reached whose blockers are unknown, in
// tree order.
func (g *Graph) Why(tasks []model.ID, folders []model.Folder) (ready, stuck []model.ID, stuckFolders []model.Folder) {
	ready = []model.ID{}
	// Nodes of the followed graph are Refs: a task's, or a folder's done
	// node, numbered for components.
	num := map[Ref]model.ID{}
	var refs []Ref
	var queue []Ref
	id := func(r Ref) model.ID {
		if n, ok := num[r]; ok {
			return n
		}
		num[r] = model.ID(len(refs))
		refs = append(refs, r)
		queue = append(queue, r)
		return num[r]
	}
	edges := map[model.ID][]model.ID{}
	priority := map[model.ID]*int64{}
	isStuck := map[model.ID]bool{}
	unknown := map[model.Folder]bool{}
	for _, t := range tasks {
		id(TaskRef(t))
	}
	for _, f := range folders {
		id(DoneOf(f))
	}
	for len(queue) > 0 {
		r := queue[0]
		queue = queue[1:]
		from := num[r]
		if r.Kind == DoneNode {
			if g.Done(r.Folder) {
				continue
			}
			for _, t := range g.Under(r.Folder) {
				if n, ok := g.t.Task(t); !ok {
					isStuck[t] = true
				} else if n.Open {
					edges[from] = append(edges[from], id(TaskRef(t)))
				}
			}
			continue
		}
		n, ok := g.t.Task(r.ID)
		if !ok {
			isStuck[r.ID] = true
			continue
		}
		if !n.Open {
			continue
		}
		priority[r.ID] = n.Priority
		f, _ := g.t.TaskFolder(r.ID)
		st := g.TaskState(n, f)
		if st.Readiness == model.Ready {
			ready = append(ready, r.ID)
		}
		for _, in := range st.Inherited {
			if in.IDs == nil {
				unknown[in.Folder] = true
			}
		}
		for _, b := range st.Open {
			if t, ok := g.target(b); ok {
				edges[from] = append(edges[from], id(t))
			} else {
				isStuck[b] = true
			}
		}
	}
	for i := range refs {
		if _, ok := edges[model.ID(i)]; !ok {
			edges[model.ID(i)] = nil
		}
	}
	for _, group := range components(edges) {
		for _, i := range group {
			switch r := refs[i]; r.Kind {
			case TaskNode:
				isStuck[r.ID] = true
			default:
				if fid := g.t.Folder(r.Folder).ID; fid != 0 {
					isStuck[fid] = true
				}
			}
		}
	}
	stuck = slices.Sorted(maps.Keys(isStuck))
	if stuck == nil {
		stuck = []model.ID{}
	}
	stuckFolders = slices.SortedFunc(maps.Keys(unknown), model.TreeOrder)
	if stuckFolders == nil {
		stuckFolders = []model.Folder{}
	}
	slices.SortFunc(ready, func(a, b model.ID) int { return WorkOrder(a, priority[a], b, priority[b]) })
	return ready, stuck, stuckFolders
}

// Under lists the task files under folder f, at any depth, usable or not.
func (g *Graph) Under(f model.Folder) []model.ID {
	fo := g.t.Folder(f)
	ids := slices.Clone(fo.Tasks)
	for _, sub := range fo.Folders {
		ids = append(ids, g.Under(sub)...)
	}
	return ids
}
