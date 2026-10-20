package core

import (
	"fmt"
	"strings"
)

// NodeKind tells a task from a folder. They share one ID counter.
type NodeKind int

const (
	NodeTask NodeKind = iota + 1
	NodeFolder
)

// RootID stands for the root, which has no ID of its own, wherever a parent
// is asked for.
const RootID int64 = 0

// Node is what an ID names.
type Node struct {
	ID   int64
	Kind NodeKind
	Gate End // a task's gate end; EndNone for an ordinary task or a folder
}

// Tree is what resolution reads. The store implements it over SQLite; the
// model implements it in memory.
type Tree interface {
	// Node looks up an ID.
	Node(id int64) (Node, bool)
	// Child finds the folder in parent named exactly name.
	Child(parent int64, name string) (int64, bool)
	// ChildFold finds the folder in parent whose name equals name ignoring
	// case, and returns it with its name as spelled.
	ChildFold(parent int64, name string) (id int64, spelled string, ok bool)
	// Gate finds a folder's start or done gate.
	Gate(folder int64, end End) (int64, bool)
	// Path is a folder's path from the root.
	Path(folder int64) string
}

// Archive is implemented by a Tree that can tell an archived ID from one
// that was never issued or was deleted.
type Archive interface {
	Archived(id int64) bool
}

// Want is what a command takes in the place a reference is written.
type Want int

const (
	// WantFolder is a folder-ref: a folder or the root, with no end.
	WantFolder Want = iota
	// WantTask is a task-ref: a task, or a folder's gate by its end.
	WantTask
	// WantEdge is an edge-ref: a task, or a folder's gate, its done gate
	// when no end is given. A gate the folder lacks resolves as missing,
	// for the change to make or refuse.
	WantEdge
	// WantAny takes a folder, a task, the root or material.
	WantAny
)

// ResolvedKind is what a reference resolved to.
type ResolvedKind int

const (
	ResolvedRoot ResolvedKind = iota + 1
	ResolvedFolder
	ResolvedTask
)

// Resolved is a reference resolved against a tree.
type Resolved struct {
	Kind ResolvedKind
	// ID is the folder or task; 0 for the root, or for a gate not yet made.
	ID int64
	// Folder and End are set when the reference named a folder's gate.
	Folder int64
	End    End

	Material    string
	HasMaterial bool
}

// MissingGate reports a gate an edge names that its folder doesn't have yet.
func (r Resolved) MissingGate() bool { return r.Kind == ResolvedTask && r.ID == 0 }

// Resolve resolves a parsed reference, as Resolution steps 3 to 5 say.
// labels maps the labels of the batch the reference is in; nil means it is
// in no batch.
func Resolve(t Tree, ref Ref, want Want, labels map[string]int64) (Resolved, error) {
	if ref.HasMaterial && want != WantAny {
		return Resolved{}, invalid("%q: material isn't taken here", ref.Raw)
	}
	// What the task-ref and folder-ref patterns refuse is the input's
	// shape, refused before anything resolves.
	path := ref.Kind == TargetPath || ref.Kind == TargetAnchored
	switch {
	case want == WantTask && path && ref.End == EndNone:
		return Resolved{}, invalid("%q: a path names a task only as a folder's gate, ending .start or .done", ref.Raw)
	case want == WantFolder && ref.End != EndNone:
		return Resolved{}, invalid("%q: an end isn't taken where a folder is", ref.Raw)
	}

	// Step 3: the target.
	var res Resolved
	var node Node
	switch ref.Kind {
	case TargetLabel:
		if labels == nil {
			return Resolved{}, invalid("%q: a label names a task only within its batch", ref.Raw)
		}
		id, ok := labels[ref.Label]
		if !ok {
			return Resolved{}, invalid("%q: no task in this batch has that label", ref.Raw)
		}
		if want != WantEdge && want != WantTask {
			return Resolved{}, invalid("%q: a label names a task, and a task isn't taken here", ref.Raw)
		}
		return Resolved{Kind: ResolvedTask, ID: id}, nil

	case TargetID:
		n, err := lookup(t, ref.ID)
		if err != nil {
			return Resolved{}, err
		}
		node = n

	case TargetPath:
		if ref.IsRoot() {
			res.Kind = ResolvedRoot
			break
		}
		id, err := walk(t, ref, RootID, "")
		if err != nil {
			return Resolved{}, err
		}
		node = Node{ID: id, Kind: NodeFolder}

	case TargetAnchored:
		n, err := lookup(t, ref.ID)
		if err != nil {
			return Resolved{}, err
		}
		if n.Kind != NodeFolder {
			return Resolved{}, invalid("%q: @%d is a task, and a task holds no folders", ref.Raw, ref.ID)
		}
		id, err := walk(t, ref, n.ID, t.Path(n.ID))
		if err != nil {
			return Resolved{}, err
		}
		node = Node{ID: id, Kind: NodeFolder}
	}
	if res.Kind == 0 {
		res.ID = node.ID
		if node.Kind == NodeFolder {
			res.Kind = ResolvedFolder
		} else {
			res.Kind = ResolvedTask
		}
	}

	// Step 4: the end.
	switch res.Kind {
	case ResolvedRoot:
		if ref.End != EndNone {
			return Resolved{}, invalid("%q: the root has no gates", ref.Raw)
		}
		if want == WantTask || want == WantEdge {
			return Resolved{}, invalid("%q: the root is not a task, and no edge can name it", ref.Raw)
		}

	case ResolvedTask:
		if ref.End != EndNone {
			return Resolved{}, invalid("%q: @%d is a task, and a task has no gates", ref.Raw, res.ID)
		}
		if want == WantFolder {
			return Resolved{}, NotFound([]int64{res.ID}, nil, "@%d is a task, not a folder", res.ID)
		}

	case ResolvedFolder:
		end := ref.End
		switch {
		case want == WantTask && end == EndNone:
			return Resolved{}, NotFound([]int64{res.ID}, nil, "@%d is a folder, not a task", res.ID)
		case want == WantEdge && end == EndNone:
			end = EndDone
		}
		if end != EndNone {
			folder := res.ID
			gate, ok := t.Gate(folder, end)
			if !ok && want != WantEdge {
				return Resolved{}, NotFound(nil, []string{t.Path(folder) + "." + string(end)},
					"@%d (%s) has no %s gate", folder, t.Path(folder), end)
			}
			res = Resolved{Kind: ResolvedTask, ID: gate, Folder: folder, End: end}
		}
	}

	// Step 5: the material.
	if ref.HasMaterial {
		if res.Kind == ResolvedTask {
			return Resolved{}, invalid("%q: @%d is a task, and a task has no directory", ref.Raw, res.ID)
		}
		res.Material, res.HasMaterial = ref.Material, true
	}
	return res, nil
}

// ResolveString parses and resolves a reference.
func ResolveString(t Tree, s string, want Want, labels map[string]int64) (Resolved, error) {
	ref, err := ParseRef(s)
	if err != nil {
		return Resolved{}, err
	}
	return Resolve(t, ref, want, labels)
}

func lookup(t Tree, id int64) (Node, error) {
	if n, ok := t.Node(id); ok {
		return n, nil
	}
	if a, ok := t.(Archive); ok && a.Archived(id) {
		e := NotFound([]int64{id}, nil, "@%d was archived", id)
		e.Archived = []int64{id}
		return Node{}, e
	}
	return Node{}, NotFound([]int64{id}, nil, "@%d names nothing", id)
}

// walk follows ref's names from the folder at, whose path is atPath, one
// exact name at a time.
func walk(t Tree, ref Ref, at int64, atPath string) (int64, error) {
	given := strings.TrimSuffix(ref.TargetString(), "/"+strings.Join(ref.Names, "/"))
	for _, name := range ref.Names {
		given += "/" + name
		if id, ok := t.Child(at, name); ok {
			at, atPath = id, atPath+"/"+name
			continue
		}
		if id, spelled, ok := t.ChildFold(at, name); ok {
			is := atPath + "/" + spelled
			return 0, Conflict(RuleCaseMismatch, []int64{id}, []string{given, is},
				"%s matches %s only ignoring case", given, is)
		}
		return 0, NotFound(nil, []string{given}, "no folder at %s", given)
	}
	return at, nil
}

// String writes what a reference resolved to, for messages.
func (r Resolved) String() string {
	switch {
	case r.Kind == ResolvedRoot:
		return "/"
	case r.End != EndNone:
		return fmt.Sprintf("@%d.%s", r.Folder, r.End)
	default:
		return fmt.Sprintf("@%d", r.ID)
	}
}
