package core

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeTree is the rules doc's example tree: /work (@1, done gate @55),
// /work/phase1 (@7, gates @51 and @52), /work/phase2 (@9, gates @53 and @54),
// and the tasks @4, @8 and @10. @99 was archived.
type fakeTree struct {
	nodes    map[int64]Node
	parent   map[int64]int64
	names    map[int64]string
	gates    map[int64]map[End]int64
	archived map[int64]bool
}

func newFakeTree() *fakeTree {
	f := &fakeTree{
		nodes: map[int64]Node{}, parent: map[int64]int64{}, names: map[int64]string{},
		gates: map[int64]map[End]int64{}, archived: map[int64]bool{99: true},
	}
	folder := func(id, parent int64, name string) {
		f.nodes[id] = Node{ID: id, Kind: NodeFolder}
		f.parent[id], f.names[id] = parent, name
		f.gates[id] = map[End]int64{}
	}
	task := func(id, parent int64, gate End) {
		f.nodes[id] = Node{ID: id, Kind: NodeTask, Gate: gate}
		f.parent[id] = parent
		if gate != EndNone {
			f.gates[parent][gate] = id
		}
	}
	folder(1, RootID, "work")
	folder(7, 1, "phase1")
	folder(9, 1, "phase2")
	task(4, 1, EndNone)
	task(8, 7, EndNone)
	task(10, 9, EndNone)
	task(51, 7, EndStart)
	task(52, 7, EndDone)
	task(53, 9, EndStart)
	task(54, 9, EndDone)
	task(55, 1, EndDone)
	return f
}

func (f *fakeTree) Node(id int64) (Node, bool) { n, ok := f.nodes[id]; return n, ok }

func (f *fakeTree) Child(parent int64, name string) (int64, bool) {
	for id, n := range f.names {
		if f.parent[id] == parent && n == name {
			return id, true
		}
	}
	return 0, false
}

func (f *fakeTree) ChildFold(parent int64, name string) (int64, string, bool) {
	for id, n := range f.names {
		if f.parent[id] == parent && strings.EqualFold(n, name) {
			return id, n, true
		}
	}
	return 0, "", false
}

func (f *fakeTree) Gate(folder int64, end End) (int64, bool) {
	id, ok := f.gates[folder][end]
	return id, ok
}

func (f *fakeTree) Path(folder int64) string {
	if folder == RootID {
		return "/"
	}
	p := ""
	for id := folder; id != RootID; id = f.parent[id] {
		p = "/" + f.names[id] + p
	}
	return p
}

func (f *fakeTree) Archived(id int64) bool { return f.archived[id] }

var _ Archive = (*fakeTree)(nil)

func TestResolve(t *testing.T) {
	labels := map[string]int64{"$a": 200}
	tests := []struct {
		name string
		in   string
		want Want
		res  Resolved
	}{
		// Step 3: an ID names whichever has it.
		{"id names a task", "4", WantTask, Resolved{Kind: ResolvedTask, ID: 4}},
		{"id names a folder", "@9", WantFolder, Resolved{Kind: ResolvedFolder, ID: 9}},
		{"id names either", "@9", WantAny, Resolved{Kind: ResolvedFolder, ID: 9}},
		{"a gate by its own id", "@52", WantTask, Resolved{Kind: ResolvedTask, ID: 52}},
		// Step 3: a path, followed from the root by exact names.
		{"path", "/work/phase2", WantFolder, Resolved{Kind: ResolvedFolder, ID: 9}},
		{"anchored", "@1/phase2", WantFolder, Resolved{Kind: ResolvedFolder, ID: 9}},
		{"anchored bare", "1/phase1", WantFolder, Resolved{Kind: ResolvedFolder, ID: 7}},
		{"root", "/", WantFolder, Resolved{Kind: ResolvedRoot}},
		{"label", "$a", WantEdge, Resolved{Kind: ResolvedTask, ID: 200}},
		// Step 4: an end on a folder names its gate.
		{"start gate by id", "@9.start", WantTask, Resolved{Kind: ResolvedTask, ID: 53, Folder: 9, End: EndStart}},
		{"done gate by path", "/work/phase2.done", WantTask, Resolved{Kind: ResolvedTask, ID: 54, Folder: 9, End: EndDone}},
		{"gate by anchored path", "@1/phase1.start", WantEdge, Resolved{Kind: ResolvedTask, ID: 51, Folder: 7, End: EndStart}},
		{"edge to a folder is its done gate", "@9", WantEdge, Resolved{Kind: ResolvedTask, ID: 54, Folder: 9, End: EndDone}},
		{"edge to a path is its done gate", "/work", WantEdge, Resolved{Kind: ResolvedTask, ID: 55, Folder: 1, End: EndDone}},
		{"edge to a missing gate", "@1.start", WantEdge, Resolved{Kind: ResolvedTask, ID: 0, Folder: 1, End: EndStart}},
		{"gate with WantAny", "@7.done", WantAny, Resolved{Kind: ResolvedTask, ID: 52, Folder: 7, End: EndDone}},
		// Step 5: material in a folder or the root.
		{"material by id", "@9:docs/plan.md", WantAny, Resolved{Kind: ResolvedFolder, ID: 9, Material: "docs/plan.md", HasMaterial: true}},
		{"material by path", "/work/phase2:docs/plan.md", WantAny, Resolved{Kind: ResolvedFolder, ID: 9, Material: "docs/plan.md", HasMaterial: true}},
		{"material by anchored path", "@1/phase2:docs/plan.md", WantAny, Resolved{Kind: ResolvedFolder, ID: 9, Material: "docs/plan.md", HasMaterial: true}},
		{"material in the root", "/:x", WantAny, Resolved{Kind: ResolvedRoot, Material: "x", HasMaterial: true}},
		{"material that looks like an end", "9:notes.done", WantAny, Resolved{Kind: ResolvedFolder, ID: 9, Material: "notes.done", HasMaterial: true}},
	}
	tree := newFakeTree()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveString(tree, tt.in, tt.want, labels)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.res) {
				t.Errorf("Resolve(%q) = %+v, want %+v", tt.in, got, tt.res)
			}
		})
	}
}

func TestResolveRefusals(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   Want
		labels map[string]int64
		kind   string
		rule   string
		ids    []int64
		paths  []string
	}{
		{"id names nothing", "@300", WantTask, nil, KindNotFound, "", []int64{300}, nil},
		{"a task where a folder is taken", "@4", WantFolder, nil, KindNotFound, "", []int64{4}, nil},
		{"a folder where a task is taken", "@9", WantTask, nil, KindNotFound, "", []int64{9}, nil},
		{"path names nothing", "/work/phase3", WantFolder, nil, KindNotFound, "", nil, []string{"/work/phase3"}},
		{"path matches only ignoring case", "/Work/phase2", WantFolder, nil, KindConflict, RuleCaseMismatch, []int64{1}, []string{"/Work", "/work"}},
		{"deep part matches only ignoring case", "/work/Phase2", WantFolder, nil, KindConflict, RuleCaseMismatch, []int64{9}, []string{"/work/Phase2", "/work/phase2"}},
		{"anchored part ignoring case", "@1/PHASE1", WantFolder, nil, KindConflict, RuleCaseMismatch, []int64{7}, []string{"@1/PHASE1", "/work/phase1"}},
		{"anchored without @, as written", "1/PHASE1/x.start", WantTask, nil, KindConflict, RuleCaseMismatch, []int64{7}, []string{"1/PHASE1", "/work/phase1"}},
		{"anchored on a task", "@4/x", WantFolder, nil, KindInvalidInput, "", nil, nil},
		{"anchored on nothing", "@300/x", WantFolder, nil, KindNotFound, "", []int64{300}, nil},
		{"the root in an edge", "/", WantEdge, nil, KindInvalidInput, "", nil, nil},
		{"the root's gate", "/.done", WantEdge, nil, KindInvalidInput, "", nil, nil},
		{"label outside a batch", "$a", WantEdge, nil, KindInvalidInput, "", nil, nil},
		{"label never defined", "$b", WantEdge, map[string]int64{"$a": 200}, KindInvalidInput, "", nil, nil},
		{"label where a folder is taken", "$a", WantFolder, map[string]int64{"$a": 200}, KindInvalidInput, "", nil, nil},
		{"label where a task is taken", "$a", WantTask, map[string]int64{"$a": 200}, KindInvalidInput, "", nil, nil},
		{"end on a task", "@4.done", WantEdge, nil, KindInvalidInput, "", nil, nil},
		{"end on a gate", "@52.done", WantEdge, nil, KindInvalidInput, "", nil, nil},
		{"end where a folder is taken", "@9.start", WantFolder, nil, KindInvalidInput, "", nil, nil},
		{"missing gate outside an edge", "@1.start", WantTask, nil, KindNotFound, "", nil, []string{"/work.start"}},
		{"material where none is taken", "@9:x", WantFolder, nil, KindInvalidInput, "", nil, nil},
		{"material in a task", "@4:x", WantAny, nil, KindInvalidInput, "", nil, nil},
		// What the task-ref and folder-ref patterns refuse is the input's
		// shape, refused before any reference resolves.
		{"a path with no end where a task is taken", "/work/phase2", WantTask, nil, KindInvalidInput, "", nil, nil},
		{"an anchored path with no end where a task is taken", "@1/phase2", WantTask, nil, KindInvalidInput, "", nil, nil},
		{"an anchored path without @ where a task is taken", "1/phase2", WantTask, nil, KindInvalidInput, "", nil, nil},
		{"a path to nothing with no end where a task is taken", "/nope/x", WantTask, nil, KindInvalidInput, "", nil, nil},
		{"the root where a task is taken", "/", WantTask, nil, KindInvalidInput, "", nil, nil},
		{"an end on a path to nothing where a folder is taken", "/nope.start", WantFolder, nil, KindInvalidInput, "", nil, nil},
		{"an end on an ID of nothing where a folder is taken", "@300.done", WantFolder, nil, KindInvalidInput, "", nil, nil},
	}
	tree := newFakeTree()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveString(tree, tt.in, tt.want, tt.labels)
			if !IsKind(err, tt.kind, tt.rule) {
				t.Fatalf("Resolve(%q) = %v, want %s %s", tt.in, err, tt.kind, tt.rule)
			}
			e := err.(*Error)
			if tt.ids != nil && !reflect.DeepEqual(e.IDs, tt.ids) {
				t.Errorf("ids = %v, want %v", e.IDs, tt.ids)
			}
			if tt.paths != nil && !reflect.DeepEqual(e.Paths, tt.paths) {
				t.Errorf("paths = %v, want %v", e.Paths, tt.paths)
			}
		})
	}
}

func TestResolveArchived(t *testing.T) {
	_, err := ResolveString(newFakeTree(), "@99", WantTask, nil)
	if !IsKind(err, KindNotFound, "") {
		t.Fatalf("got %v, want not-found", err)
	}
	if e := err.(*Error); !reflect.DeepEqual(e.Archived, []int64{99}) || e.Message != "@99 was archived" {
		t.Errorf("archived = %v, %q, want [99]", e.Archived, e.Message)
	}
	// With the index, the message says where it went.
	_, err = ResolveString(indexedTree{newFakeTree()}, "@99", WantTask, nil)
	if e := err.(*Error); e.Message != "@99 was archived, to /r/archived/@99.md" {
		t.Errorf("message %q", e.Message)
	}
}

// indexedTree knows where each archived ID went.
type indexedTree struct{ *fakeTree }

func (indexedTree) ArchivedAt(id int64) string { return fmt.Sprintf("/r/archived/@%d.md", id) }

var _ ArchiveIndex = indexedTree{}

// deletedTree is a tree that deleted @98, in a root whose first ID is
// first.
type deletedTree struct {
	*fakeTree
	first int64
}

func (deletedTree) Deleted(id int64) bool { return id == 98 }
func (d deletedTree) FirstID() int64      { return d.first }

// A not-found message says an ID was deleted, as against never issued,
// and that one below the root's first ID came from an earlier root.
func TestResolveDeleted(t *testing.T) {
	tr := deletedTree{newFakeTree(), 1}
	_, err := ResolveString(tr, "@98", WantTask, nil)
	if e, ok := err.(*Error); !ok || e.Kind != KindNotFound || e.Message != "@98 was deleted" || !reflect.DeepEqual(e.IDs, []int64{98}) {
		t.Errorf("deleted: %v", err)
	}
	_, err = ResolveString(tr, "@97", WantTask, nil)
	if e, ok := err.(*Error); !ok || e.Message != "@97 was never issued" || !reflect.DeepEqual(e.IDs, []int64{97}) {
		t.Errorf("never issued: %v", err)
	}
	tr.first = 97
	for ref, want := range map[string]string{
		"@97": "@97 was never issued",
		"@96": "@96 is below this root's first ID, 97, so came from an earlier root",
	} {
		_, err = ResolveString(tr, ref, WantFolder, nil)
		if e, ok := err.(*Error); !ok || e.Kind != KindNotFound || e.Message != want {
			t.Errorf("%s: %v, want %q", ref, err, want)
		}
	}
}
