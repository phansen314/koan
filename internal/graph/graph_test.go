package graph

import (
	"slices"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

// task is one task of a little tree.
type task struct {
	folder    model.Folder
	blockedBy []model.ID
	done      bool
	unusable  bool
	priority  *int64
}

// folder is one folder of a little tree, the root aside.
type folder struct {
	id        model.ID
	blockedBy []model.ID
	missing   bool // no folder file: no ID, blockers unknown
	unusable  bool // a folder file that can't be used
}

// tree is a little tree. Every folder of a task must be in folders, but the
// root.
type tree struct {
	tasks   map[model.ID]task
	folders map[model.Folder]folder
}

func (t tree) Resolve(id model.ID) Item {
	if tk, ok := t.tasks[id]; ok {
		return Item{Kind: TaskKind, Folder: tk.folder}
	}
	for f, fo := range t.folders {
		if fo.id == id && !fo.missing && !fo.unusable {
			return Item{Kind: FolderKind, Folder: f}
		}
	}
	return Item{}
}

func (t tree) TaskFolder(id model.ID) (model.Folder, bool) {
	tk, ok := t.tasks[id]
	return tk.folder, ok
}

func (t tree) Task(id model.ID) (Node, bool) {
	tk, ok := t.tasks[id]
	if !ok || tk.unusable {
		return Node{}, false
	}
	return Node{Open: !tk.done, Priority: tk.priority, BlockedBy: tk.blockedBy}, true
}

func (t tree) Folder(f model.Folder) Folder {
	var fo Folder
	if x, ok := t.folders[f]; ok && !x.missing && !x.unusable {
		fo.ID = x.id
	}
	for id, tk := range t.tasks {
		if tk.folder == f {
			fo.Tasks = append(fo.Tasks, id)
		}
	}
	for g := range t.folders {
		if g.Parent() == f && g != f {
			fo.Folders = append(fo.Folders, g)
		}
	}
	slices.Sort(fo.Tasks)
	slices.SortFunc(fo.Folders, model.TreeOrder)
	return fo
}

func (t tree) Blockers(f model.Folder) ([]model.ID, bool, bool) {
	if f == model.Root {
		return nil, true, false
	}
	fo := t.folders[f]
	if fo.missing || fo.unusable {
		return nil, false, fo.unusable
	}
	return fo.blockedBy, true, false
}

func TestReadiness(t *testing.T) {
	tr := tree{tasks: map[model.ID]task{1: {folder: "/"}, 2: {folder: "/", blockedBy: []model.ID{1}}, 3: {folder: "/", blockedBy: []model.ID{9, 2, 1}}, 4: {folder: "/", blockedBy: []model.ID{1}, done: true}}}
	tr.tasks[1] = task{folder: "/", done: true}
	g := New(tr)
	for id, want := range map[model.ID]model.Readiness{1: model.Done, 2: model.Ready, 3: model.Blocked, 4: model.Done} {
		n, _ := tr.Task(id)
		if got := g.TaskState(n, "/").Readiness; got != want {
			t.Errorf("task %d: %s, want %s", id, got, want)
		}
	}
	n, _ := tr.Task(3)
	if st := g.TaskState(n, "/"); !slices.Equal(st.Open, []model.ID{2, 9}) || len(st.Inherited) != 0 {
		t.Errorf("open blockers %v %v: want the open and the missing blocker, ascending", st.Open, st.Inherited)
	}
}

func TestInherited(t *testing.T) {
	tr := tree{
		tasks: map[model.ID]task{1: {folder: "/"}, 2: {folder: "/a/b"}, 3: {folder: "/x"}, 4: {folder: "/a/b/c"}, 5: {folder: "/"}},
		folders: map[model.Folder]folder{
			"/a": {id: 10, blockedBy: []model.ID{1}}, "/a/b": {id: 11, blockedBy: []model.ID{20}},
			"/a/b/c": {missing: true}, "/x": {id: 12, blockedBy: []model.ID{5}},
			"/y": {id: 20},
		},
	}
	g := New(tr)
	n, _ := tr.Task(2)
	st := g.TaskState(n, "/a/b")
	if st.Readiness != model.Blocked || !slices.Equal(st.Open, []model.ID{1}) || len(st.Inherited) != 1 || st.Inherited[0].Folder != "/a" {
		t.Errorf("task under a blocked folder: %+v (the empty /y, a blocker of /a/b, is done)", st)
	}
	n, _ = tr.Task(4)
	st = g.TaskState(n, "/a/b/c")
	if st.Readiness != model.Blocked || len(st.Inherited) != 2 || st.Inherited[1].Folder != "/a/b/c" || st.Inherited[1].IDs != nil {
		t.Errorf("task in a folder with unknown blockers: %+v", st)
	}
	// A folder's own blockers are its own; the folders above are inherited.
	if st := g.FolderState("/a/b"); st.Readiness != model.Blocked || !slices.Equal(st.Open, []model.ID{1}) || len(st.Inherited) != 1 {
		t.Errorf("folder /a/b: %+v", st)
	}
	if st := g.FolderState("/a/b/c"); st.Readiness != model.Blocked || len(st.Open) != 1 {
		t.Errorf("folder with no folder file: %+v", st)
	}
	if st := g.FolderState("/"); st.Readiness != model.Ready {
		t.Errorf("the root is never blocked: %+v", st)
	}
	// Done ignores blockers.
	tr.tasks[3] = task{folder: "/x", done: true}
	if st := New(tr).FolderState("/x"); st.Readiness != model.Done {
		t.Errorf("a folder whose tasks are done, blocked or not: %+v", st)
	}
}

func TestDone(t *testing.T) {
	tr := tree{
		tasks:   map[model.ID]task{1: {folder: "/a", done: true}, 2: {folder: "/a/b", done: true}, 3: {folder: "/c", unusable: true}},
		folders: map[model.Folder]folder{"/a": {id: 10}, "/a/b": {id: 11}, "/c": {id: 12}, "/e": {id: 13}},
	}
	g := New(tr)
	for f, want := range map[model.Folder]bool{"/a": true, "/a/b": true, "/c": false, "/e": true, "/": false} {
		if got := g.Done(f); got != want {
			t.Errorf("%s done: %v, want %v", f, got, want)
		}
	}
	tr.tasks[4] = task{folder: "/a/b"}
	if New(tr).Done("/a") {
		t.Error("a task added to a done folder: still done")
	}
}

// cycleIn is the cycle the graph finds through changed, collapsed.
func cycleIn(tr tree, changed ...Ref) []Ref {
	c, _ := New(tr).CycleThrough(changed)
	return Collapse(c)
}

func TestCycleThrough(t *testing.T) {
	// /p waits on task 7; new task 8 in /p, blocking 7: 8 inherits 7.
	tr := tree{
		tasks:   map[model.ID]task{7: {folder: "/", blockedBy: []model.ID{8}}, 8: {folder: "/p"}},
		folders: map[model.Folder]folder{"/p": {id: 1, blockedBy: []model.ID{7}}},
	}
	if c := cycleIn(tr, TaskRef(8)); len(c) != 3 {
		t.Errorf("a new task inheriting what it blocks: %v", c)
	}
	// /p waits on 7; new folder /p/n blocking 7: a task added to /p/n would
	// wait on itself.
	tr = tree{
		tasks:   map[model.ID]task{7: {folder: "/", blockedBy: []model.ID{2}}},
		folders: map[model.Folder]folder{"/p": {id: 1, blockedBy: []model.ID{7}}, "/p/n": {id: 2}},
	}
	if c := cycleIn(tr, WaitOf("/p/n")); len(c) == 0 {
		t.Error("a new folder blocking what its parent waits on")
	}
	// 7 waits on /p being done; new folder /p/n blocked by 7.
	tr = tree{
		tasks:   map[model.ID]task{7: {folder: "/", blockedBy: []model.ID{1}}},
		folders: map[model.Folder]folder{"/p": {id: 1}, "/p/n": {id: 2, blockedBy: []model.ID{7}}},
	}
	if c := cycleIn(tr, WaitOf("/p/n")); len(c) == 0 {
		t.Error("a new folder blocked by what waits on its parent being done")
	}
	// A folder with no folder file still sits in the graph by its place:
	// /a holds /a/b, which holds 9; /c waits on 9; /a waiting on /c closes it.
	tr = tree{
		tasks:   map[model.ID]task{9: {folder: "/a/b"}},
		folders: map[model.Folder]folder{"/a": {id: 5, blockedBy: []model.ID{6}}, "/a/b": {missing: true}, "/c": {id: 6, blockedBy: []model.ID{9}}},
	}
	c := cycleIn(tr, WaitOf("/a"))
	if !slices.Contains(c, WaitOf("/a/b")) {
		t.Errorf("a cycle through a folder with no folder file: %v", c)
	}
	// Valid: a task waiting on a folder under its own, and a folder waiting
	// on a task in its parent.
	tr = tree{
		tasks:   map[model.ID]task{1: {folder: "/a", blockedBy: []model.ID{11}}, 2: {folder: "/a"}},
		folders: map[model.Folder]folder{"/a": {id: 10}, "/a/x": {id: 11, blockedBy: []model.ID{2}}},
	}
	if c := cycleIn(tr, TaskRef(1), WaitOf("/a/x")); c != nil {
		t.Errorf("no cycle: %v", c)
	}
	// Two folders that wait on each other, empty or not.
	tr = tree{folders: map[model.Folder]folder{"/a": {id: 1, blockedBy: []model.ID{2}}, "/b": {id: 2, blockedBy: []model.ID{1}}}}
	if c := cycleIn(tr, WaitOf("/a")); len(c) != 2 {
		t.Errorf("folders waiting on each other: %v", c)
	}
	// An unusable file met is reported when no cycle is found.
	tr = tree{
		tasks:   map[model.ID]task{1: {folder: "/"}, 2: {folder: "/", unusable: true}},
		folders: map[model.Folder]folder{},
	}
	tr.tasks[1] = task{folder: "/", blockedBy: []model.ID{2}}
	if c, broken := New(tr).CycleThrough([]Ref{TaskRef(1)}); c != nil || broken == nil || *broken != TaskRef(2) {
		t.Errorf("cycle %v, broken %v", c, broken)
	}
}

func TestCollapse(t *testing.T) {
	c := []Ref{WaitOf("/a"), TaskRef(1), DoneOf("/a"), TaskRef(2)}
	if got := Collapse(c); len(got) != 4 {
		t.Errorf("a folder at two places is at each: %v", got)
	}
	c = []Ref{DoneOf("/a"), WaitOf("/a"), TaskRef(1)}
	if got := Collapse(c); len(got) != 2 || got[0] != DoneOf("/a") {
		t.Errorf("a folder's two nodes together are one: %v", got)
	}
	c = []Ref{WaitOf("/a"), TaskRef(1), DoneOf("/a")}
	if got := Collapse(c); len(got) != 2 {
		t.Errorf("the last and the first together are one: %v", got)
	}
}

func TestCycles(t *testing.T) {
	tr := tree{tasks: map[model.ID]task{
		1: {folder: "/", blockedBy: []model.ID{2}}, 2: {folder: "/", blockedBy: []model.ID{3}}, 3: {folder: "/", blockedBy: []model.ID{1}},
		4: {folder: "/", blockedBy: []model.ID{1}}, 5: {folder: "/", blockedBy: []model.ID{6}}, 6: {folder: "/", blockedBy: []model.ID{5}}, 7: {folder: "/"},
	}}
	var nodes []Ref
	for id := model.ID(1); id <= 7; id++ {
		nodes = append(nodes, TaskRef(id))
	}
	got := New(tr).Cycles(nodes, func(r Ref) model.ID { return r.ID })
	if len(got) != 2 || !slices.Equal(got[0], []Ref{TaskRef(1), TaskRef(2), TaskRef(3)}) || !slices.Equal(got[1], []Ref{TaskRef(5), TaskRef(6)}) {
		t.Errorf("cycles %v", got)
	}
}

func TestWhy(t *testing.T) {
	nine := int64(9)
	tr := tree{
		tasks: map[model.ID]task{
			1: {folder: "/", blockedBy: []model.ID{2, 3, 4}}, 2: {folder: "/", blockedBy: []model.ID{5}}, 3: {folder: "/"}, 4: {folder: "/", done: true},
			5: {folder: "/", priority: &nine}, 6: {folder: "/", blockedBy: []model.ID{7, 99}}, 7: {folder: "/", blockedBy: []model.ID{6}},
			// Folder /f, blocking 8, holds a ready task and an unusable one.
			8: {folder: "/", blockedBy: []model.ID{20}}, 9: {folder: "/f"}, 10: {folder: "/f/g", unusable: true},
			// A task in a folder with unknown blockers.
			11: {folder: "/u"},
		},
		folders: map[model.Folder]folder{"/f": {id: 20}, "/f/g": {id: 21}, "/u": {missing: true}},
	}
	g := New(tr)
	if ready, stuck, _ := g.Why([]model.ID{1}, nil); !slices.Equal(ready, []model.ID{5, 3}) || len(stuck) != 0 {
		t.Errorf("why 1: ready %v (want 5 then 3, by priority), stuck %v", ready, stuck)
	}
	if ready, stuck, _ := g.Why([]model.ID{3}, nil); !slices.Equal(ready, []model.ID{3}) || len(stuck) != 0 {
		t.Errorf("why of a ready task: %v %v", ready, stuck)
	}
	if ready, stuck, _ := g.Why([]model.ID{6}, nil); len(ready) != 0 || !slices.Equal(stuck, []model.ID{6, 7, 99}) {
		t.Errorf("why of a task in a cycle with a missing blocker: %v %v", ready, stuck)
	}
	if ready, stuck, _ := g.Why([]model.ID{8}, nil); !slices.Equal(ready, []model.ID{9}) || !slices.Equal(stuck, []model.ID{10}) {
		t.Errorf("why of a task blocked by a folder: %v %v", ready, stuck)
	}
	if ready, stuck, sf := g.Why([]model.ID{11}, nil); len(ready) != 0 || len(stuck) != 0 || !slices.Equal(sf, []model.Folder{"/u"}) {
		t.Errorf("why of a task in a folder with no folder file: %v %v %v", ready, stuck, sf)
	}
	if ready, stuck, _ := g.Why(nil, []model.Folder{"/f"}); !slices.Equal(ready, []model.ID{9}) || !slices.Equal(stuck, []model.ID{10}) {
		t.Errorf("why of a folder: %v %v", ready, stuck)
	}
}
