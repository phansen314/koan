package ops

import (
	"fmt"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
)

// folderInfo is one folder's file, as the folder index read it: the file
// when it is usable, the reason when it is not, and neither when the folder
// has none.
type folderInfo struct {
	file *model.FolderFile
	err  *errs.Error
}

// folderIndex is every folder file, read once per command after the walk
// (spec/implementation.md, Walking the tree), with what the walk found in
// each folder.
type folderIndex struct {
	at     map[model.Folder]folderInfo // every folder but the root
	byID   map[model.ID][]model.Folder // the folders with each ID
	tasks  map[model.Folder][]model.ID // the task files directly in each folder, ascending
	subs   map[model.Folder][]model.Folder
	broken []model.Folder // the folders whose file can't be used, in tree order
}

// folders reads every folder file, once per command. A folder file that is
// missing or can't be used is a warning: the folder has no ID, and its
// blockers are unknown.
func (c *call) folders() (*folderIndex, *errs.Error) {
	if c.fx != nil {
		return c.fx, nil
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	fx := &folderIndex{
		at: map[model.Folder]folderInfo{}, byID: map[model.ID][]model.Folder{},
		tasks: map[model.Folder][]model.ID{}, subs: map[model.Folder][]model.Folder{},
	}
	for _, f := range x.Folders {
		if f == model.Root {
			continue
		}
		fx.subs[f.Parent()] = append(fx.subs[f.Parent()], f)
		if !x.Filed[f] {
			fx.at[f] = folderInfo{}
			c.warn(errs.WarnUnusableFile, fmt.Sprintf("folder %s has no %s, so its blockers are unknown and everything in it is held back: `koan repair` writes one", f, model.FolderFileName),
				[]model.ID{}, c.t.Abs(f.Rel()))
			continue
		}
		ff, _, e := c.t.LoadFolder(f)
		switch {
		case e != nil:
			fx.at[f] = folderInfo{err: e}
			fx.broken = append(fx.broken, f)
			c.warn(errs.WarnUnusableFile, e.Message, []model.ID{}, c.t.Abs(model.FolderFileRel(f)))
		case ff != nil:
			fx.at[f] = folderInfo{file: ff}
			fx.byID[ff.ID] = append(fx.byID[ff.ID], f)
		default:
			// Gone since the walk, which only an outside change can do.
			fx.at[f] = folderInfo{}
		}
	}
	for _, id := range x.IDs() {
		fx.tasks[x.Tasks[id]] = append(fx.tasks[x.Tasks[id]], id)
	}
	c.fx = fx
	return fx, nil
}

// folderID is folder f's ID, 0 when it has none.
func (fx *folderIndex) folderID(f model.Folder) model.ID {
	if ff := fx.at[f].file; ff != nil {
		return ff.ID
	}
	return 0
}

// baseTree is the graph's view of the tree as it is. A duplicated ID met as
// a blocker is kept in err, which fails the command.
type baseTree struct {
	c   *call
	fx  *folderIndex
	err *errs.Error
}

// graph is the tree's graph, as it is, for readiness. It needs the folder
// index.
func (c *call) graph() *graph.Graph {
	if c.g == nil {
		c.bt = &baseTree{c: c, fx: c.fx}
		c.g = graph.New(c.bt)
	}
	return c.g
}

// met is the duplicated ID the graph has met as a blocker, if any. Run
// fails the command with it only once the command returns, so a write that
// evaluates readiness checks it before its first write.
func (c *call) met() *errs.Error {
	if c.bt == nil {
		return nil
	}
	return c.bt.err
}

func (b *baseTree) Resolve(id model.ID) graph.Item {
	f, isTask := b.c.x.Tasks[id]
	folders := b.fx.byID[id]
	if (isTask && len(folders) > 0) || len(folders) > 1 {
		if b.err == nil {
			b.err = b.c.duplicateID(id)
		}
	}
	switch {
	case isTask:
		return graph.Item{Kind: graph.TaskKind, Folder: f}
	case len(folders) > 0:
		return graph.Item{Kind: graph.FolderKind, Folder: folders[0]}
	}
	return graph.Item{Kind: graph.None}
}

func (b *baseTree) TaskFolder(id model.ID) (model.Folder, bool) {
	f, ok := b.c.x.Tasks[id]
	return f, ok
}

func (b *baseTree) Task(id model.ID) (graph.Node, bool) {
	tf := b.c.pass(id)
	if tf == nil {
		return graph.Node{}, false
	}
	b.c.dangling(fmt.Sprintf("task %d", id), id, tf.BlockedBy, b.c.t.Abs(model.TaskRel(b.c.x.Tasks[id], id)))
	return graph.Node{Open: tf.Open(), Priority: tf.Priority, BlockedBy: tf.BlockedBy}, true
}

func (b *baseTree) Folder(f model.Folder) graph.Folder {
	return graph.Folder{ID: b.fx.folderID(f), Tasks: b.fx.tasks[f], Folders: b.fx.subs[f]}
}

func (b *baseTree) Blockers(f model.Folder) ([]model.ID, bool, bool) {
	if f == model.Root {
		return nil, true, false
	}
	info := b.fx.at[f]
	if info.file == nil {
		return nil, false, info.err != nil
	}
	b.c.dangling("folder "+string(f), info.file.ID, info.file.BlockedBy, b.c.t.Abs(model.FolderFileRel(f)))
	return info.file.BlockedBy, true, false
}

// exists reports whether a task or a folder has ID id.
func (c *call) exists(id model.ID) bool {
	_, isTask := c.x.Tasks[id]
	return isTask || len(c.fx.byID[id]) > 0
}

// dangling reports the blockers in bs, of the task or folder named who,
// that name nothing, as a warning about the file at p. While a folder file
// can't be used, one of them may be that folder's.
func (c *call) dangling(who string, id model.ID, bs []model.ID, p string) {
	var gone []model.ID
	for _, b := range bs {
		if !c.exists(b) {
			gone = append(gone, b)
		}
	}
	if len(gone) == 0 {
		return
	}
	msg := fmt.Sprintf("%s is blocked by %v, which do not exist", who, gone)
	if len(c.fx.broken) > 0 {
		var files []string
		for _, f := range c.fx.broken {
			files = append(files, c.t.Abs(model.FolderFileRel(f)))
		}
		msg += fmt.Sprintf(", unless one is the ID of a folder whose file can't be used: %s", strings.Join(files, ", "))
	}
	c.warn(errs.WarnDanglingReference, msg, append([]model.ID{id}, gone...), p)
}

// duplicateID is the conflict over an ID held by more than one task file
// or folder file.
func (c *call) duplicateID(id model.ID) *errs.Error {
	e := errs.NewConflict(errs.RuleDuplicateID, "ID %d is held by more than one task or folder", id).WithIDs(id)
	if f, isTask := c.x.Tasks[id]; isTask {
		e.WithPaths(c.t.Abs(model.TaskRel(f, id)))
	}
	for _, f := range c.fx.byID[id] {
		e.WithPaths(c.t.Abs(model.FolderFileRel(f)))
	}
	return e
}

// overlay is the tree with a change made, which nothing has written yet:
// what a cycle check searches (spec/implementation.md, The graph).
type overlay struct {
	base     *baseTree
	tasks    map[model.ID]*taskEdit       // tasks made, and tasks moved or blocked
	folders  map[model.Folder]*folderEdit // folders made, and folders blocked, by their path after the change
	made     map[model.ID]model.Folder    // the IDs of the folders made
	from, to model.Folder                 // a folder move: what was at from is at to
}

// taskEdit is a task as the change leaves it.
type taskEdit struct {
	folder model.Folder
	node   graph.Node
	made   bool   // a task the command makes, which has no ID yet
	name   string // how a message names a task made
}

// folderEdit is a folder as the change leaves it.
type folderEdit struct {
	id        model.ID
	blockedBy []model.ID
	made      bool
}

func (c *call) overlay() *overlay {
	c.graph()
	return &overlay{base: c.bt, tasks: map[model.ID]*taskEdit{}, folders: map[model.Folder]*folderEdit{}, made: map[model.ID]model.Folder{}}
}

// within reports whether f is a or under it.
func within(a, f model.Folder) bool { return f == a || a.Holds(f) }

// rebase is path f, under prefix a, under prefix b instead.
func rebase(f, a, b model.Folder) model.Folder {
	if a == model.Root {
		return b.Join(strings.TrimPrefix(string(f), "/"))
	}
	return model.Folder(string(b) + strings.TrimPrefix(string(f), string(a)))
}

// gone reports whether f is where a moved folder was, and so holds nothing.
func (o *overlay) gone(f model.Folder) bool { return o.from != "" && within(o.from, f) }

// old is the path the folder at f had before the change.
func (o *overlay) old(f model.Folder) model.Folder {
	if o.to != "" && within(o.to, f) {
		return rebase(f, o.to, o.from)
	}
	return f
}

// now is the path the folder at g has after the change.
func (o *overlay) now(g model.Folder) model.Folder {
	if o.from != "" && within(o.from, g) {
		return rebase(g, o.from, o.to)
	}
	return g
}

// addFolder adds a folder the change makes, with ID id.
func (o *overlay) addFolder(f model.Folder, id model.ID, blockedBy []model.ID) {
	o.folders[f] = &folderEdit{id: id, blockedBy: blockedBy, made: true}
	o.made[id] = f
}

// addTask adds a task the change makes, with ID id.
func (o *overlay) addTask(id model.ID, f model.Folder, blockedBy []model.ID, name string) {
	o.tasks[id] = &taskEdit{folder: f, node: graph.Node{Open: true, BlockedBy: blockedBy}, made: true, name: name}
}

// blockTask makes the existing task id wait on blockers too. The caller
// knows id is a task's, so it is never resolved: a folder file that holds
// it too fails only a command that meets it as a blocker.
func (o *overlay) blockTask(id model.ID, blockers ...model.ID) {
	if te, ok := o.tasks[id]; ok {
		te.node.BlockedBy = union(te.node.BlockedBy, blockers)
		return
	}
	f, _ := o.TaskFolder(id)
	n, _ := o.base.Task(id)
	n.BlockedBy = union(n.BlockedBy, blockers)
	o.tasks[id] = &taskEdit{folder: f, node: n}
}

// blockFolder makes the existing folder at path f, with ID id, wait on
// blockers too.
func (o *overlay) blockFolder(f model.Folder, id model.ID, blockers ...model.ID) {
	now := o.now(f)
	if fe, ok := o.folders[now]; ok {
		fe.blockedBy = union(fe.blockedBy, blockers)
		return
	}
	bs, _, _ := o.base.Blockers(f)
	o.folders[now] = &folderEdit{id: id, blockedBy: union(bs, blockers)}
}

// moveTask puts the existing task id in folder f.
func (o *overlay) moveTask(id model.ID, f model.Folder) {
	n, _ := o.base.Task(id)
	o.tasks[id] = &taskEdit{folder: f, node: n}
}

func union(a, b []model.ID) []model.ID {
	out := slices.Clone(a)
	for _, id := range b {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (o *overlay) Resolve(id model.ID) graph.Item {
	if te, ok := o.tasks[id]; ok {
		return graph.Item{Kind: graph.TaskKind, Folder: te.folder}
	}
	if f, ok := o.made[id]; ok {
		return graph.Item{Kind: graph.FolderKind, Folder: f}
	}
	it := o.base.Resolve(id)
	it.Folder = o.now(it.Folder)
	return it
}

func (o *overlay) TaskFolder(id model.ID) (model.Folder, bool) {
	if te, ok := o.tasks[id]; ok {
		return te.folder, true
	}
	f, ok := o.base.TaskFolder(id)
	return o.now(f), ok
}

func (o *overlay) Task(id model.ID) (graph.Node, bool) {
	if te, ok := o.tasks[id]; ok {
		return te.node, true
	}
	return o.base.Task(id)
}

func (o *overlay) Folder(f model.Folder) graph.Folder {
	var fo graph.Folder
	switch fe := o.folders[f]; {
	case fe != nil && fe.made:
		fo.ID = fe.id
	case o.gone(f):
	default:
		b := o.base.Folder(o.old(f))
		fo.ID = b.ID
		for _, id := range b.Tasks {
			if _, edited := o.tasks[id]; !edited {
				fo.Tasks = append(fo.Tasks, id)
			}
		}
		for _, s := range b.Folders {
			if s != o.from {
				fo.Folders = append(fo.Folders, o.now(s))
			}
		}
	}
	for id, te := range o.tasks {
		if te.folder == f {
			fo.Tasks = append(fo.Tasks, id)
		}
	}
	for g, fe := range o.folders {
		if fe.made && g.Parent() == f && g != f {
			fo.Folders = append(fo.Folders, g)
		}
	}
	if o.to != "" && o.to.Parent() == f && !slices.Contains(fo.Folders, o.to) {
		fo.Folders = append(fo.Folders, o.to)
	}
	slices.Sort(fo.Tasks)
	slices.SortFunc(fo.Folders, model.TreeOrder)
	return fo
}

func (o *overlay) Blockers(f model.Folder) ([]model.ID, bool, bool) {
	if fe, ok := o.folders[f]; ok {
		return fe.blockedBy, true, false
	}
	if o.gone(f) {
		return nil, true, false
	}
	return o.base.Blockers(o.old(f))
}

// cycleCheck is a change to check for a cycle before it is written.
type cycleCheck struct {
	o       *overlay
	changed []graph.Ref             // the nodes the change touches: every cycle it closes passes one
	pairs   []waits                 // the new waits, for the containment check
	fields  map[[2]graph.Ref]string // the input field that adds each new edge
}

// waits is one thing that waits on a blocker, as the change has it: a task,
// or a folder's wait node.
type waits struct {
	waiter  graph.Ref
	blocker model.ID
	field   string
}

func (c *call) newCheck(o *overlay) *cycleCheck {
	return &cycleCheck{o: o, fields: map[[2]graph.Ref]string{}}
}

// wait records that waiter waits on blocker, by the input field at field.
func (k *cycleCheck) wait(waiter graph.Ref, blocker model.ID, field string) {
	k.pairs = append(k.pairs, waits{waiter, blocker, field})
	if t, ok := k.o.target(blocker); ok {
		k.fields[[2]graph.Ref{waiter, t}] = field
	}
}

// edge records that the input field at field adds the edge from a to b.
func (k *cycleCheck) edge(a, b graph.Ref, field string) { k.fields[[2]graph.Ref{a, b}] = field }

// target is the node a blocker leads to: a task's, or a folder's done
// node.
func (o *overlay) target(id model.ID) (graph.Ref, bool) {
	it := o.Resolve(id)
	switch it.Kind {
	case graph.TaskKind:
		return graph.TaskRef(id), true
	case graph.FolderKind:
		return graph.DoneOf(it.Folder), it.Folder != model.Root
	}
	return graph.Ref{}, false
}

// refuse checks the change for a cycle, in the two steps of
// spec/operations.md (Cycles): containment, then the graph.
func (c *call) refuse(k *cycleCheck) *errs.Error {
	o := k.o
	for _, p := range k.pairs {
		e := c.contains(o, p)
		if o.base.err != nil {
			return o.base.err
		}
		if e != nil {
			return e
		}
	}
	g := graph.New(o)
	cycle, broken := g.CycleThrough(k.changed)
	if o.base.err != nil {
		return o.base.err
	}
	if cycle != nil {
		return c.cycleError(o, k, cycle)
	}
	if broken != nil {
		return c.brokenError(o, *broken)
	}
	return nil
}

// contains refuses a wait that containment alone shows to be a cycle.
func (c *call) contains(o *overlay, p waits) *errs.Error {
	it := o.Resolve(p.blocker)
	var outer, inner string // the one that holds, and the one held
	var why string
	switch w := p.waiter; {
	case w.Kind == graph.TaskNode && it.Kind == graph.FolderKind:
		if f, _ := o.TaskFolder(w.ID); within(it.Folder, f) {
			outer, inner, why = string(it.Folder), c.nameTask(o, w.ID), "nothing can wait on a folder that holds it"
		}
	case w.Kind == graph.WaitNode && it.Kind == graph.FolderKind:
		switch f := w.Folder; {
		case f == it.Folder:
			outer, inner, why = string(f), string(f), "a folder can't wait on itself"
		case it.Folder.Holds(f):
			outer, inner, why = string(it.Folder), string(f), "a folder can't wait on a folder that holds it"
		case f.Holds(it.Folder):
			outer, inner, why = string(f), string(it.Folder), "a folder can't wait on what is in it"
		}
	case w.Kind == graph.WaitNode && it.Kind == graph.TaskKind:
		if within(w.Folder, it.Folder) {
			outer, inner, why = string(w.Folder), c.nameTask(o, p.blocker), "a folder can't wait on what is in it"
		}
	}
	if outer == "" {
		return nil
	}
	e := errs.NewConflict(errs.RuleCycle, "%s contains %s: %s", outer, inner, why)
	for _, r := range []graph.Ref{p.waiter, {Kind: kindOf(it), ID: p.blocker, Folder: it.Folder}} {
		if id := c.knownID(o, r); id != 0 && !slices.Contains(e.IDs, id) {
			e.WithIDs(id)
		}
		if r.IsFolder() && !slices.Contains(e.Folders, string(r.Folder)) {
			e.WithFolders(string(r.Folder))
		}
	}
	return e.WithProblems(errs.Problem{Field: p.field, Reason: "closes a cycle"})
}

func kindOf(it graph.Item) graph.NodeKind {
	if it.Kind == graph.FolderKind {
		return graph.DoneNode
	}
	return graph.TaskNode
}

// knownID is the ID of a task or folder node, or 0 for one with none yet:
// a folder with no folder file, or a task or folder the command would make.
func (c *call) knownID(o *overlay, r graph.Ref) model.ID {
	if r.Kind == graph.TaskNode {
		if te, ok := o.tasks[r.ID]; ok && te.made {
			return 0
		}
		return r.ID
	}
	if fe, ok := o.folders[r.Folder]; ok && fe.made {
		return 0
	}
	return o.Folder(r.Folder).ID
}

// nameTask is how a message names a task.
func (c *call) nameTask(o *overlay, id model.ID) string {
	if te, ok := o.tasks[id]; ok && te.made {
		return te.name
	}
	return fmt.Sprintf("task %d", id)
}

// cycleError is the conflict over a cycle the change would close: its
// tasks and folders in order, each step in words, and the input fields
// that add its edges.
func (c *call) cycleError(o *overlay, k *cycleCheck, cycle []graph.Ref) *errs.Error {
	var steps []string
	var problems []errs.Problem
	for i, a := range cycle {
		b := cycle[(i+1)%len(cycle)]
		steps = append(steps, c.step(o, a, b))
		if f, ok := k.fields[[2]graph.Ref{a, b}]; ok && !slices.ContainsFunc(problems, func(p errs.Problem) bool { return p.Field == f }) {
			problems = append(problems, errs.Problem{Field: f, Reason: "closes a cycle"})
		}
	}
	e := errs.NewConflict(errs.RuleCycle, "this would make a cycle: %s", strings.Join(steps, "; "))
	for _, r := range graph.Collapse(cycle) {
		if id := c.knownID(o, r); id != 0 {
			e.WithIDs(id)
		}
		if r.IsFolder() {
			e.WithFolders(string(r.Folder))
		}
	}
	return e.WithProblems(problems...)
}

// step is the edge from a to b in words.
func (c *call) step(o *overlay, a, b graph.Ref) string {
	g := graph.New(o)
	return stepWords(a, b, func(r graph.Ref) string {
		if r.Kind == graph.TaskNode {
			return c.nameTask(o, r.ID)
		}
		return string(r.Folder)
	}, func(f model.Folder) bool { return len(g.Under(f)) == 0 })
}

// stepWords is the edge from a to b in words, each task or folder named by
// name. The edge from a folder's done node to its wait node stands for a
// task added to it, unless it holds some already.
func stepWords(a, b graph.Ref, name func(graph.Ref) string, empty func(model.Folder) bool) string {
	switch {
	case a.Kind == graph.DoneNode && b.Kind == graph.WaitNode && empty(a.Folder):
		return fmt.Sprintf("a task added to %s would wait on what %s waits on", a.Folder, a.Folder)
	case a.Kind == graph.DoneNode && b.Kind == graph.WaitNode:
		return fmt.Sprintf("what is in %s waits on what %s waits on", a.Folder, a.Folder)
	case a.Kind == graph.DoneNode:
		return fmt.Sprintf("%s holds %s", a.Folder, name(b))
	case b.Kind == graph.WaitNode:
		return fmt.Sprintf("%s is in %s", name(a), b.Folder)
	case a.Kind == graph.WaitNode && b.Kind == graph.DoneNode:
		return fmt.Sprintf("everything in %s waits on %s being done", a.Folder, b.Folder)
	case a.Kind == graph.WaitNode:
		return fmt.Sprintf("everything in %s waits on %s", a.Folder, name(b))
	case b.Kind == graph.DoneNode:
		return fmt.Sprintf("%s waits on %s being done", name(a), b.Folder)
	}
	return fmt.Sprintf("%s waits on %s", name(a), name(b))
}

// brokenError is the error of a file a cycle search met and could not
// read: a cycle through it can't be ruled out.
func (c *call) brokenError(o *overlay, r graph.Ref) *errs.Error {
	if r.Kind == graph.TaskNode {
		f, _ := o.base.TaskFolder(r.ID)
		return c.t.Load(f, r.ID).Err
	}
	return o.base.fx.at[o.old(r.Folder)].err
}
