// Package model is an in-memory koan tree that applies the rules doc's
// changes literally. It is the oracle the property tests check koan
// against, so it favours plainness over speed: every change works on a
// copy of the whole tree and keeps it only if nothing refuses.
package model

import (
	"maps"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/core"
)

// Task is a task's ten fields and where it sits.
type Task struct {
	ID          int64
	Gate        core.End
	Folder      int64 // core.RootID for the root
	Title       string
	Priority    *int64
	CreatedAt   string
	CompletedAt *string
	UpdatedAt   string
	BlockedBy   map[int64]bool
	Tags        map[string]bool
	Extra       map[string]any
}

// Open reports whether the task is open.
func (t *Task) Open() bool { return t.CompletedAt == nil }

// Folder is a folder's four fields, its place, and the top-level entries of
// its material.
type Folder struct {
	ID       int64
	Parent   int64
	Name     string
	Title    *string
	Extra    map[string]any
	Material map[string]bool
}

// Claim is a claim on a task.
type Claim struct {
	Task      int64
	ClaimedAt string
	Extra     map[string]any
}

// Archived is one archiving: what left, with every edge it had.
type Archived struct {
	ID      int64 // the task or folder archived
	Tasks   []Task
	Folders []Folder
	Edges   []core.Edge
}

// Tree is a koan tree: the root, its folders and tasks, the claims, and the
// archive beside it.
type Tree struct {
	Tasks   map[int64]*Task
	Folders map[int64]*Folder
	Claims  map[int64]*Claim
	// RootMaterial is the top-level material in the root.
	RootMaterial map[string]bool
	// Index maps each archived ID to the archiving it left in.
	Index    map[int64]int
	Archives []Archived
	// Issued is the last ID issued; IDs are never issued twice.
	Issued int64
	// First is the root's first ID.
	First int64
	// Now gives the time for each change, as a timestamp.
	Now func() string

	now string // the time of the change in progress
}

// New makes an empty tree whose first ID will be first.
func New(first int64, now func() string) *Tree {
	return &Tree{
		Tasks: map[int64]*Task{}, Folders: map[int64]*Folder{}, Claims: map[int64]*Claim{},
		RootMaterial: map[string]bool{}, Index: map[int64]int{},
		Issued: first - 1, First: first, Now: now,
	}
}

// Warning is a warning a change gives.
type Warning struct {
	Kind string
	IDs  []int64
}

// GateMade is a gate a change made because an edge named it.
type GateMade struct {
	ID     int64
	Folder int64
	Path   string
	End    core.End
}

// FolderAt is a folder made, with its path.
type FolderAt struct {
	ID   int64
	Path string
}

// clone copies the tree deeply enough that a change to the copy never
// touches the original. Extra objects are never changed in place, so they
// are shared.
func (t *Tree) clone() *Tree {
	c := *t
	c.Tasks = make(map[int64]*Task, len(t.Tasks))
	for id, x := range t.Tasks {
		y := *x
		y.BlockedBy = maps.Clone(x.BlockedBy)
		y.Tags = maps.Clone(x.Tags)
		c.Tasks[id] = &y
	}
	c.Folders = make(map[int64]*Folder, len(t.Folders))
	for id, x := range t.Folders {
		y := *x
		y.Material = maps.Clone(x.Material)
		c.Folders[id] = &y
	}
	c.Claims = make(map[int64]*Claim, len(t.Claims))
	for id, x := range t.Claims {
		y := *x
		c.Claims[id] = &y
	}
	c.RootMaterial = maps.Clone(t.RootMaterial)
	c.Index = maps.Clone(t.Index)
	c.Archives = slices.Clone(t.Archives)
	return &c
}

// change runs f on a copy of the tree, and keeps the copy only if f
// succeeds: a change is whole (I10).
func (t *Tree) change(f func(c *Tree) error) error {
	c := t.clone()
	c.now = t.Now()
	if err := f(c); err != nil {
		return err
	}
	c.now = ""
	*t = *c
	return nil
}

func (t *Tree) issue() int64 {
	t.Issued++
	return t.Issued
}

// The core.Tree interface, so references resolve against the model.

func (t *Tree) Node(id int64) (core.Node, bool) {
	if x, ok := t.Tasks[id]; ok {
		return core.Node{ID: id, Kind: core.NodeTask, Gate: x.Gate}, true
	}
	if _, ok := t.Folders[id]; ok {
		return core.Node{ID: id, Kind: core.NodeFolder}, true
	}
	return core.Node{}, false
}

func (t *Tree) Child(parent int64, name string) (int64, bool) {
	for _, f := range t.Folders {
		if f.Parent == parent && f.Name == name {
			return f.ID, true
		}
	}
	return 0, false
}

func (t *Tree) ChildFold(parent int64, name string) (int64, string, bool) {
	for _, f := range t.Folders {
		if f.Parent == parent && strings.EqualFold(f.Name, name) {
			return f.ID, f.Name, true
		}
	}
	return 0, "", false
}

func (t *Tree) Gate(folder int64, end core.End) (int64, bool) {
	for _, x := range t.Tasks {
		if x.Folder == folder && x.Gate == end && end != core.EndNone {
			return x.ID, true
		}
	}
	return 0, false
}

func (t *Tree) Path(folder int64) string {
	if folder == core.RootID {
		return "/"
	}
	p := ""
	for id := folder; id != core.RootID; id = t.Folders[id].Parent {
		p = "/" + t.Folders[id].Name + p
	}
	return p
}

func (t *Tree) Archived(id int64) bool {
	_, ok := t.Index[id]
	return ok
}

// Deleted reports whether an ID was issued and is now neither live nor
// archived.
func (t *Tree) Deleted(id int64) bool {
	_, task := t.Tasks[id]
	_, folder := t.Folders[id]
	return id >= t.First && id <= t.Issued && !task && !folder && !t.Archived(id)
}

func (t *Tree) FirstID() int64 { return t.First }

var (
	_ core.Tree      = (*Tree)(nil)
	_ core.Archive   = (*Tree)(nil)
	_ core.Deletions = (*Tree)(nil)
	_ core.Origin    = (*Tree)(nil)
	_ core.Graph     = (*Tree)(nil)
)

// The core.Graph interface, so readiness reads the model.

func (t *Tree) Open(id int64) bool {
	x, ok := t.Tasks[id]
	return ok && x.Open()
}

func (t *Tree) BlockedBy(id int64) []int64 {
	x, ok := t.Tasks[id]
	if !ok {
		return nil
	}
	return sortedKeys(x.BlockedBy)
}

// Ready reports whether a task is ready.
func (t *Tree) Ready(id int64) bool { return core.Ready(t, id) }

// Under reports whether id, a task or folder, sits in folder at any depth.
// Everything is under the root.
func (t *Tree) Under(id, folder int64) bool {
	parent := core.RootID
	if x, ok := t.Tasks[id]; ok {
		parent = x.Folder
	} else if f, ok := t.Folders[id]; ok {
		parent = f.Parent
	}
	for {
		if parent == folder {
			return true
		}
		if parent == core.RootID {
			return false
		}
		parent = t.Folders[parent].Parent
	}
}

// Frontier is the ready tasks with no claim, in work order.
func (t *Tree) Frontier() []int64 {
	var cands []core.Ranked
	for _, x := range t.Tasks {
		cands = append(cands, core.Ranked{ID: x.ID, Priority: x.Priority})
	}
	return rankedIDs(core.Frontier(t, t.claimed, cands))
}

// TreeOrder lists every folder and task in tree order.
func (t *Tree) TreeOrder() []int64 {
	var entries []core.Placed
	for _, f := range t.Folders {
		entries = append(entries, core.Placed{ID: f.ID, Folder: true, Parent: f.Parent, Name: f.Name})
	}
	for _, x := range t.Tasks {
		entries = append(entries, core.Placed{ID: x.ID, Parent: x.Folder})
	}
	return core.TreeOrder(entries)
}

// Finished reports whether every task under a folder, at any depth, is
// complete.
func (t *Tree) Finished(folder int64) bool {
	for _, x := range t.Tasks {
		if t.Under(x.ID, folder) && x.Open() {
			return false
		}
	}
	return true
}

// PutMaterial adds a top-level material entry to a folder, or to the root.
// It is not a change: material is the person's, and koan never makes it.
func (t *Tree) PutMaterial(folder int64, name string) {
	if folder == core.RootID {
		t.RootMaterial[name] = true
		return
	}
	t.Folders[folder].Material[name] = true
}

func (t *Tree) claimed(id int64) bool {
	_, ok := t.Claims[id]
	return ok
}

func (t *Tree) readySet() map[int64]bool {
	out := map[int64]bool{}
	for id := range t.Tasks {
		if t.Ready(id) {
			out[id] = true
		}
	}
	return out
}

// released is the open tasks that were blocked before and are ready now.
func (t *Tree) released(before map[int64]bool, existed map[int64]bool) []int64 {
	var out []int64
	for id := range t.Tasks {
		if existed[id] && !before[id] && t.Ready(id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// reblocked is the tasks that were ready before and wait now.
func (t *Tree) reblocked(before map[int64]bool) []int64 {
	var out []int64
	for id := range before {
		if core.Blocked(t, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func (t *Tree) claimedBlocked(reblocked []int64) []Warning {
	var ids []int64
	for _, id := range reblocked {
		if t.claimed(id) {
			ids = append(ids, id)
		}
	}
	if ids == nil {
		return nil
	}
	return []Warning{{Kind: "claimed-blocked", IDs: ids}}
}

func (t *Tree) taskIDs() map[int64]bool {
	out := map[int64]bool{}
	for id := range t.Tasks {
		out[id] = true
	}
	return out
}

func sortedKeys[K int64 | string](m map[K]bool) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func rankedIDs(rs []core.Ranked) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}
