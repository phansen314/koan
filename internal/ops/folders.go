package ops

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// folderWritten is what folder update, folder block, and folder unblock
// return: the folder's identity.
type folderWritten struct {
	ID      *model.ID    `json:"id"`
	Path    model.Folder `json:"path"`
	Changed bool         `json:"changed"`
}

// folderMade is what folder move returns.
type folderMade struct {
	folderWritten
	FoldersCreated []model.Folder `json:"folders_created"`
}

// folderCreated is what folder create returns.
type folderCreated struct {
	folderMade
	Dependents []model.ID `json:"dependents"`
}

// made is the result of a folder create or folder move that left folder f
// at its path. Its ID is read from its folder file: one koan can't use is a
// warning.
func (c *call) made(f model.Folder, changed bool, created []model.Folder) folderMade {
	var id *model.ID
	if f != model.Root {
		ff, _, e := c.t.LoadFolder(f)
		if e != nil {
			c.warn(errs.WarnUnusableFile, e.Message, []model.ID{}, c.t.Abs(model.FolderFileRel(f)))
		} else if ff != nil {
			id = &ff.ID
		}
	}
	return folderMade{folderWritten{id, f, changed}, created}
}

type shownFolder struct {
	Folder model.FolderRecord `json:"folder"`
}

// record is folder f's record, with its state. A folder file koan can't use
// is the error, with the record as far as it goes; a folder with none has
// an ID and a blocked_by of null.
func (c *call) record(f model.Folder) (model.FolderRecord, *errs.Error) {
	r := model.FolderRecord{Path: f, Dir: c.t.Abs(f.Rel()), Extra: map[string]any{}}
	fx, e := c.folders()
	if e != nil {
		return r, e
	}
	g := c.graph()
	st := g.FolderState(f)
	r.Readiness, r.OpenBlockers, r.Inherited = st.Readiness, st.Open, st.Inherited
	r.Empty = len(g.Under(f)) == 0
	if f == model.Root {
		r.BlockedBy = []model.ID{}
		return r, nil
	}
	notes := c.t.Abs(model.FolderNotesRel(f))
	r.NotesPath = &notes
	info := fx.at[f]
	if info.err != nil {
		return r, info.err
	}
	if ff := info.file; ff != nil {
		r.ID, r.Title, r.BlockedBy, r.Extra = &ff.ID, ff.Title, ff.BlockedBy, ff.Extra
	}
	return r, nil
}

// passFolder is folder f's record for a command that only passes it: a
// folder file koan can't use is a warning.
func (c *call) passFolder(f model.Folder) model.FolderRecord {
	r, e := c.record(f)
	if e != nil {
		c.warn(errs.WarnUnusableFile, e.Message, []model.ID{}, c.t.Abs(model.FolderFileRel(f)))
	}
	return r
}

func cmdFolderCreate(c *call) (any, *errs.Error) {
	in := c.in.top()
	f := in.folder("folder", true, model.Root)
	title, hasTitle := in.optTitle("title")
	extra, hasExtra := in.object("extra")
	notes, hasNotes := in.text("notes")
	blockedBy := in.items("blocked_by", false)
	blocks := in.items("blocks", false)
	asked := hasTitle || hasExtra || hasNotes || c.in.has("blocked_by") || c.in.has("blocks")
	if e := c.begin(true); e != nil {
		return nil, e
	}
	missing, e := c.plan(f)
	if e != nil {
		return nil, e
	}
	if len(missing) == 0 {
		if asked {
			return nil, c.destinationExists("already exists: set its title and extra with folder update and its blockers with folder block, and edit its notes in its @folder.md", f)
		}
		return folderCreated{c.made(f, false, []model.Folder{}), []model.ID{}}, nil
	}
	bs, e := c.blockers(blockedBy, false)
	if e != nil {
		return nil, e
	}
	ds, e := c.blockers(blocks, false)
	if e != nil {
		return nil, e
	}
	deps, e := c.dependents(ds)
	if e != nil {
		return nil, e
	}
	next, e := c.nextID()
	if e != nil {
		return nil, e
	}
	id := next + model.ID(slices.Index(missing, f))
	if len(bs) > 0 || len(deps) > 0 {
		o := c.overlay()
		for i, m := range missing {
			o.addFolder(m, next+model.ID(i), nil)
		}
		o.folders[f].blockedBy = idsOf(bs)
		k := c.newCheck(o)
		k.changed = []graph.Ref{graph.WaitOf(f)}
		for _, b := range bs {
			k.wait(graph.WaitOf(f), b.id, b.at)
		}
		for _, d := range deps {
			o.block(d.id, id)
			k.wait(d.ref(), id, d.at)
		}
		if p := f.Parent(); p != model.Root {
			k.edge(graph.WaitOf(f), graph.WaitOf(p), "/folder")
			k.edge(graph.DoneOf(p), graph.DoneOf(f), "/folder")
		}
		if e := c.refuse(k); e != nil {
			return nil, e
		}
	}
	first, e := c.issue(len(missing))
	if e != nil {
		return nil, e
	}
	if first != next {
		return nil, errs.New(errs.Internal, "the IDs checked, from %d, are not the IDs issued, from %d", next, first)
	}
	// What waits on the new folder is rewritten before the folder is made,
	// as task create does it.
	dependents, e := c.block(deps, id, "folder")
	if e != nil {
		return nil, e
	}
	// A folder with nothing to set may appear meanwhile without harm.
	var want []newFolder
	if hasTitle || hasExtra || hasNotes || len(bs) > 0 {
		want = append(want, newFolder{folder: f, title: title, extra: extra, notes: notes, blockedBy: idsOf(bs)})
	}
	created, e := c.makeFolders(missing, first, want...)
	if e != nil {
		if len(dependents) > 0 {
			e.Message = fmt.Sprintf("%v wait on folder %d, which was not made: %s", dependents, id, e.Message)
			e.WithIDs(dependents...)
		}
		return nil, e
	}
	return folderCreated{c.made(f, len(created) > 0, created), dependents}, nil
}

func cmdFolderShow(c *call) (any, *errs.Error) {
	ref := c.in.top().folderRef("folder", true, folderRef{})
	if e := c.begin(false); e != nil {
		return nil, e
	}
	f, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	r, e := c.record(f)
	if e != nil {
		return nil, e
	}
	return shownFolder{Folder: r}, nil
}

func cmdFolderUpdate(c *call) (any, *errs.Error) {
	in := c.in.top()
	ref := in.folderRef("folder", true, folderRef{})
	title, hasTitle := in.optTitle("title")
	edit := readExtraEdit(c, in)
	if ref.path == model.Root {
		c.in.bad("/folder", "may not be the root, which has no folder file")
	}
	if len(c.in.m) < 2 && c.in.has("folder") {
		c.in.bad("", "gives nothing to change")
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	f, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	// Like every command that names a folder, it reads every folder file.
	if _, e := c.folders(); e != nil {
		return nil, e
	}
	ff, gone, e := c.t.LoadFolder(f)
	if e != nil {
		return nil, e
	}
	if gone {
		return nil, errs.New(errs.NotFound, "folder %s has no %s: `koan repair` writes one", f, model.FolderFileName).
			WithFolders(string(f)).WithPaths(c.t.Abs(model.FolderFileRel(f)))
	}
	before := *ff
	before.Extra = cloneExtra(ff.Extra)
	if hasTitle {
		ff.Title = title
	}
	ff.Extra = edit.apply(ff.Extra)
	changed := !reflect.DeepEqual(&before, ff)
	if changed {
		if e := c.t.WriteFolderFile(f, ff, false); e != nil {
			return nil, e
		}
	}
	return folderWritten{ID: &ff.ID, Path: f, Changed: changed}, nil
}

// extraEdit is a change to an extra object, as task update and folder
// update take it.
type extraEdit struct {
	merge  map[string]any
	drop   []string
	all    map[string]any
	hasAll bool
}

func readExtraEdit(c *call, in fields) extraEdit {
	var x extraEdit
	x.merge, _ = in.object("extra_merge")
	x.drop, _ = in.strs("extra_remove")
	x.all, x.hasAll = in.object("extra_replace_all")
	if x.hasAll && (c.in.has("extra_merge") || c.in.has("extra_remove")) {
		c.in.bad("/extra_replace_all", "may not be given with extra_merge or extra_remove")
	}
	for i, k := range x.drop {
		if _, both := x.merge[k]; both {
			c.in.bad(fmt.Sprintf("/extra_remove/%d", i), "is also in extra_merge")
		}
	}
	return x
}

// apply makes the change to m, and returns the result.
func (x extraEdit) apply(m map[string]any) map[string]any {
	if x.hasAll {
		m = x.all
	}
	for k, v := range x.merge {
		m[k] = v
	}
	for _, k := range x.drop {
		delete(m, k)
	}
	return m
}

func cmdFolderMove(c *call) (any, *errs.Error) {
	in := c.in.top()
	ref := in.folderRef("folder", true, folderRef{})
	to := in.folder("to", true, "")
	if ref.path == model.Root {
		c.in.bad("/folder", "may not be the root")
	}
	if to == model.Root {
		c.in.bad("/to", "may not be the root")
	}
	if ref.path != "" && to != "" && ref.path != to && ref.path.Holds(to) {
		c.in.bad("/to", "may not be under the folder being moved")
	}
	// Changing the case of the folder's own name is the one way two paths of
	// a move may differ only in case.
	if ref.path != "" && to != "" && !(ref.path.Parent() == to.Parent() && model.SameButCase(ref.path.Name(), to.Name())) {
		if i, q := model.CaseClash([]model.Folder{ref.path, to}); i >= 0 {
			c.in.bad("/to", "differs only in case from %s: rename that folder first", q)
		}
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	from, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	// A folder given by ID is known only now.
	if ref.id != 0 && from != to && from.Holds(to) {
		return nil, errs.Invalid([]errs.Problem{{Field: "/to", Reason: "may not be under the folder being moved, " + string(from)}})
	}
	if e := c.folderOrNotFound(from); e != nil {
		return nil, e
	}
	if from == to {
		return c.made(to, false, []model.Folder{}), nil
	}
	// A rename to another case is the one move whose to may be the folder
	// itself, on a filesystem that ignores case.
	recase := from.Parent() == to.Parent() && model.SameButCase(from.Name(), to.Name())
	if taken, e := c.t.Taken(to, from); e != nil {
		return nil, e
	} else if taken {
		return nil, c.destinationExists("already exists", to)
	}
	if e := c.moveCheck(graph.Ref{}, from, to); e != nil {
		return nil, e
	}
	created, e := c.ensure(to.Parent())
	if e != nil {
		return nil, e
	}
	move := c.t.Move
	if recase {
		move = c.t.MoveCase
	}
	taken, e := move(from.Rel(), to.Rel())
	if e != nil {
		return nil, e
	}
	if taken {
		return nil, c.destinationExists("already exists", to)
	}
	return c.made(to, true, created), nil
}

// moveCheck refuses a move that would close a cycle: of task t, when its
// Kind is a task's, to folder to; or else of folder from to path to. Under
// its new parents what is moved inherits their blockers, and they hold it
// (spec/operations.md, task move and folder move). It walks and reads every
// folder file.
func (c *call) moveCheck(t graph.Ref, from, to model.Folder) *errs.Error {
	into := to.Parent()
	if t.Kind == graph.TaskNode && t.ID != 0 {
		into = to
	}
	if _, e := c.folders(); e != nil {
		return e
	}
	missing, e := c.plan(into)
	if e != nil {
		return e
	}
	next, e := c.nextID()
	if e != nil {
		return e
	}
	o := c.overlay()
	for i, m := range missing {
		o.addFolder(m, next+model.ID(i), nil)
	}
	k := c.newCheck(o)
	if t.Kind == graph.TaskNode && t.ID != 0 {
		o.moveTask(t.ID, to)
		k.changed = []graph.Ref{t}
		n, _ := o.Task(t.ID)
		for _, b := range n.BlockedBy {
			k.wait(t, b, "/to")
		}
		if to != model.Root {
			k.edge(t, graph.WaitOf(to), "/to")
			k.edge(graph.DoneOf(to), t, "/to")
		}
	} else {
		o.from, o.to = from, to
		k.changed = []graph.Ref{graph.WaitOf(to), graph.DoneOf(to)}
		k.edge(graph.WaitOf(to), graph.WaitOf(into), "/to")
		k.edge(graph.DoneOf(into), graph.DoneOf(to), "/to")
		// What is under the folder, against the blockers of its new
		// ancestors, and they against what is under it.
		var walk func(f model.Folder)
		walk = func(f model.Folder) {
			bs, _, _ := o.Blockers(f)
			for _, b := range bs {
				k.wait(graph.WaitOf(f), b, "/to")
			}
			fo := o.Folder(f)
			for _, id := range fo.Tasks {
				if n, ok := o.Task(id); ok {
					for _, b := range n.BlockedBy {
						k.wait(graph.TaskRef(id), b, "/to")
					}
				}
			}
			for _, sub := range fo.Folders {
				walk(sub)
			}
		}
		walk(to)
	}
	for _, a := range graph.Chain(into) {
		bs, _, _ := o.Blockers(a)
		for _, b := range bs {
			k.wait(graph.WaitOf(a), b, "/to")
		}
	}
	return c.refuse(k)
}

type deleteFolderResult struct {
	Folder     model.FolderRecord `json:"folder"`
	Deleted    []model.ID         `json:"deleted"`
	Dependents []model.ID         `json:"dependents"`
}

func cmdFolderDelete(c *call) (any, *errs.Error) {
	in := c.in.top()
	ref := in.folderRef("folder", true, folderRef{})
	contents := in.boolean("contents", false)
	force := in.boolean("force", false)
	if ref.path == model.Root {
		c.in.bad("/folder", "may not be the root")
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	f, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	if !contents {
		entries, e := c.t.List(f)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			name := entry.Name()
			own := false
			switch name {
			case model.FolderFileName:
				fi, err := c.t.Stat(model.FolderFileRel(f))
				own = err == nil && fi.Mode().IsRegular()
			case model.FolderNotesName:
				fi, err := c.t.Stat(model.FolderNotesRel(f))
				own = err == nil && fi.Mode().IsRegular() && fi.Size() == 0
			}
			if !own && !store.Disposable(entry) {
				return nil, errs.NewConflict(errs.RuleNotEmpty, "%s is not empty: pass contents to remove everything in it", f).
					WithFolders(string(f))
			}
		}
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	if contents && !force {
		var material []string
		for _, g := range x.Folders {
			if !f.Holds(g) {
				continue
			}
			m, e := c.t.Material(g)
			if e != nil {
				return nil, e
			}
			for _, rel := range m {
				material = append(material, c.t.Abs(rel))
			}
		}
		if len(material) > 0 {
			return nil, errs.NewConflict(errs.RuleHasMaterial, "%s holds %d files or folders koan did not make: pass force to remove them too", f, len(material)).
				WithFolders(string(f)).WithPaths(material...)
		}
	}
	res := deleteFolderResult{Folder: c.passFolder(f), Deleted: []model.ID{}}
	for _, id := range x.IDs() {
		if f.Holds(x.Tasks[id]) {
			res.Deleted = append(res.Deleted, id)
		}
	}
	// The IDs of the folder and those under it leave every blocked_by too.
	// A folder whose file can't be used has no ID to remove: references to
	// it stay, as dangling references doctor finds.
	gone := slices.Clone(res.Deleted)
	for _, g := range x.Folders {
		if id := c.fx.folderID(g); id != 0 && within(f, g) {
			gone = append(gone, id)
		}
	}
	// The folder goes first, for the reason task delete gives.
	if e := c.t.Discard(f.Rel()); e != nil {
		return nil, c.discardFailed(e, f, res.Deleted)
	}
	if res.Dependents, e = c.unreference(gone, f); e != nil {
		return nil, e
	}
	if e := c.dropClaims(res.Deleted...); e != nil {
		return nil, e
	}
	return res, nil
}

// discardFailed finishes a folder delete of f whose removal failed partway,
// for the tasks and folders it did remove: their IDs leave every blocked_by
// and the tasks' claims end, as for a removal that succeeded. It returns the
// removal's error, naming those tasks; a failure of its own is added to the
// message. The index still holds: nothing but what was removed has moved.
func (c *call) discardFailed(e *errs.Error, f model.Folder, deleted []model.ID) *errs.Error {
	gone := []model.ID{}
	for _, id := range deleted {
		if _, err := c.t.Stat(model.TaskRel(c.x.Tasks[id], id)); errs.IsErrno(err, syscall.ENOENT) {
			gone = append(gone, id)
		}
	}
	tasks := len(gone)
	for _, g := range c.x.Folders {
		if id := c.fx.folderID(g); id != 0 && within(f, g) {
			if _, err := c.t.Stat(model.FolderFileRel(g)); errs.IsErrno(err, syscall.ENOENT) {
				gone = append(gone, id)
			}
		}
	}
	if len(gone) == 0 {
		return e
	}
	e.WithIDs(gone[:tasks]...)
	e.Message += fmt.Sprintf("; it removed %d of the tasks", tasks)
	dependents, ce := c.unreference(gone, f)
	if ce == nil {
		ce = c.dropClaims(gone[:tasks]...)
	}
	if ce != nil {
		e.Message += fmt.Sprintf(", and cleaning up after them failed too, which doctor finds: %s", ce.Message)
		return e
	}
	others := "tasks and folders"
	if len(dependents) == 1 {
		others = "task or folder"
	}
	e.Message += fmt.Sprintf(", and %d other %s no longer wait on them", len(dependents), others)
	return e
}

// readFolderBlockers reads the input folder block and folder unblock share.
func readFolderBlockers(c *call) (folderRef, []item) {
	in := c.in.top()
	ref := in.folderRef("folder", true, folderRef{})
	items := in.items("blocked_by", true)
	if ref.path == model.Root {
		c.in.bad("/folder", "may not be the root, which has no folder file")
	}
	if items != nil && len(items) == 0 && c.in.has("blocked_by") {
		c.in.bad("/blocked_by", "must hold at least one task or folder")
	}
	return ref, items
}

// blockedFolder is the folder a folder block or unblock changes, with its
// folder file.
func (c *call) blockedFolder(ref folderRef) (model.Folder, *model.FolderFile, *errs.Error) {
	f, e := c.resolve(ref)
	if e != nil {
		return "", nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return "", nil, e
	}
	ff, e := c.folderFile(f)
	return f, ff, e
}

func cmdFolderBlock(c *call) (any, *errs.Error) {
	ref, items := readFolderBlockers(c)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	f, ff, e := c.blockedFolder(ref)
	if e != nil {
		return nil, e
	}
	bs, e := c.blockers(items, false)
	if e != nil {
		return nil, e
	}
	var added []resolved
	for _, b := range bs {
		if !slices.Contains(ff.BlockedBy, b.id) {
			added = append(added, b)
		}
	}
	if len(added) == 0 {
		return folderWritten{ID: &ff.ID, Path: f, Changed: false}, nil
	}
	o := c.overlay()
	o.block(ff.ID, idsOf(added)...)
	k := c.newCheck(o)
	k.changed = []graph.Ref{graph.WaitOf(f)}
	for _, b := range added {
		k.wait(graph.WaitOf(f), b.id, b.at)
	}
	if e := c.refuse(k); e != nil {
		return nil, e
	}
	ff.BlockedBy = union(ff.BlockedBy, idsOf(added))
	if e := c.t.WriteFolderFile(f, ff, false); e != nil {
		return nil, e
	}
	return folderWritten{ID: &ff.ID, Path: f, Changed: true}, nil
}

func cmdFolderUnblock(c *call) (any, *errs.Error) {
	ref, items := readFolderBlockers(c)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	f, ff, e := c.blockedFolder(ref)
	if e != nil {
		return nil, e
	}
	bs, e := c.blockers(items, true)
	if e != nil {
		return nil, e
	}
	drop := idsOf(bs)
	before := len(ff.BlockedBy)
	ff.BlockedBy = slices.DeleteFunc(ff.BlockedBy, func(b model.ID) bool { return slices.Contains(drop, b) })
	changed := len(ff.BlockedBy) != before
	if changed {
		if e := c.t.WriteFolderFile(f, ff, false); e != nil {
			return nil, e
		}
	}
	return folderWritten{ID: &ff.ID, Path: f, Changed: changed}, nil
}

func cmdFolderWhy(c *call) (any, *errs.Error) {
	ref := c.in.top().folderRef("folder", true, folderRef{})
	if e := c.begin(false); e != nil {
		return nil, e
	}
	f, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	fx, e := c.folders()
	if e != nil {
		return nil, e
	}
	if err := fx.at[f].err; err != nil {
		return nil, err
	}
	g := c.graph()
	res := whyResult{Readiness: g.FolderState(f).Readiness, Ready: []model.ID{}, Stuck: []model.ID{}, StuckFolders: []model.Folder{}}
	if res.Readiness != model.Done {
		res.Ready, res.Stuck, res.StuckFolders = g.Why(nil, []model.Folder{f})
	}
	return res, nil
}

type folderListing struct {
	Folders   []json.RawMessage `json:"folders"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
}

func cmdFolderList(c *call) (any, *errs.Error) {
	in := c.in.top()
	ref := in.folderRef("folder", false, folderRef{path: model.Root})
	recursive := in.boolean("recursive", true)
	n := readNarrowing(in, model.FolderFields)
	if e := c.begin(false); e != nil {
		return nil, e
	}
	f, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	// Every folder in scope is read before the limit, so that warnings do
	// not depend on narrowing.
	records := []model.FolderRecord{}
	for _, g := range x.Folders {
		if g != f && (g.Parent() == f || (recursive && f.Holds(g))) {
			records = append(records, c.passFolder(g))
		}
	}
	l := folderListing{Total: len(records)}
	l.Folders, l.Truncated, e = narrow(n, records, model.FolderFields, "id", "path")
	return l, e
}

// unreference removes the IDs in gone from the blocked_by of every task
// file and folder file left, and returns the IDs of the tasks and folders it
// rewrote, ascending. A folder file under removed, a folder being deleted,
// is rewritten only while it is still there.
func (c *call) unreference(gone []model.ID, removed model.Folder) ([]model.ID, *errs.Error) {
	dependents := []model.ID{}
	if len(gone) == 0 {
		return dependents, nil
	}
	gone = slices.Sorted(slices.Values(gone))
	isGone := func(id model.ID) bool { _, ok := slices.BinarySearch(gone, id); return ok }
	for _, id := range c.x.IDs() {
		if isGone(id) {
			continue
		}
		tf := c.pass(id)
		if tf == nil || !slices.ContainsFunc(tf.BlockedBy, isGone) {
			continue
		}
		tf.BlockedBy = slices.DeleteFunc(tf.BlockedBy, isGone)
		tf.UpdatedAt = c.now
		if e := c.t.WriteTask(c.x.Tasks[id], tf, false); e != nil {
			return nil, e
		}
		dependents = append(dependents, id)
	}
	for _, f := range c.x.Folders {
		ff := c.fx.at[f].file
		if ff == nil || isGone(ff.ID) || !slices.ContainsFunc(ff.BlockedBy, isGone) {
			continue
		}
		if removed != "" && within(removed, f) {
			if _, err := c.t.Stat(model.FolderFileRel(f)); err != nil {
				continue
			}
		}
		ff.BlockedBy = slices.DeleteFunc(ff.BlockedBy, isGone)
		if e := c.t.WriteFolderFile(f, ff, false); e != nil {
			return nil, e
		}
		dependents = append(dependents, ff.ID)
	}
	slices.Sort(dependents)
	return dependents, nil
}

// folderIDs maps each folder ID to the folders that have it, from the
// folder index.
func (c *call) folderIDs() (map[model.ID][]model.Folder, *errs.Error) {
	fx, e := c.folders()
	if e != nil {
		return nil, e
	}
	return fx.byID, nil
}

// eachFolderFile reads the folder file of every folder in x that has one,
// in tree order, and calls fn with it, or with the error that makes it
// unusable. A file that is gone by the time it is read is skipped.
func eachFolderFile(t *store.Tree, x *store.Index, fn func(f model.Folder, ff *model.FolderFile, e *errs.Error)) {
	for _, f := range x.Folders {
		if !x.Filed[f] {
			continue
		}
		if ff, _, e := t.LoadFolder(f); ff != nil || e != nil {
			fn(f, ff, e)
		}
	}
}

// loadFolderFiles reads every folder file in x: the usable ones by folder,
// and the error of each that can't be used.
func loadFolderFiles(t *store.Tree, x *store.Index) (map[model.Folder]*model.FolderFile, map[model.Folder]*errs.Error) {
	files, bad := map[model.Folder]*model.FolderFile{}, map[model.Folder]*errs.Error{}
	eachFolderFile(t, x, func(f model.Folder, ff *model.FolderFile, e *errs.Error) {
		if e != nil {
			bad[f] = e
		} else {
			files[f] = ff
		}
	})
	return files, bad
}

// highestID is the highest ID in the tree: of a task, of a folder whose file
// is in folders, or of notes, which hold their ID when their task is gone.
func highestID(x *store.Index, folders map[model.Folder]*model.FolderFile) model.ID {
	var highest model.ID
	for id := range x.Tasks {
		highest = max(highest, id)
	}
	for id := range x.Notes {
		highest = max(highest, id)
	}
	for _, ff := range folders {
		highest = max(highest, ff.ID)
	}
	return highest
}

// highest is the highest ID in the tree, as highestID counts it, and a file
// that holds it. A folder file koan can't use is a warning, as for
// folderIDs.
func (c *call) highest() (model.ID, string, *errs.Error) {
	byID, e := c.folderIDs()
	if e != nil {
		return 0, "", e
	}
	var top model.ID
	var rel string
	for id, f := range c.x.Tasks {
		if id > top {
			top, rel = id, model.TaskRel(f, id)
		}
	}
	for id, fs := range c.x.Notes {
		if id > top {
			top, rel = id, model.NotesRel(fs[0], id)
		}
	}
	for id, fs := range byID {
		if id > top {
			top, rel = id, model.FolderFileRel(fs[0])
		}
	}
	return top, rel, nil
}

// holder finds what has ID id: a task, in folder f, or folder f itself.
// Nothing is not-found; more than one is duplicate-id.
func (c *call) holder(id model.ID) (f model.Folder, isTask bool, e *errs.Error) {
	byID, e := c.folderIDs()
	if e != nil {
		return "", false, e
	}
	tf, isTask := c.x.Tasks[id]
	folders := byID[id]
	switch {
	case isTask && len(folders) == 0:
		return tf, true, nil
	case !isTask && len(folders) == 1:
		return folders[0], false, nil
	case !isTask && len(folders) == 0:
		return "", false, errs.New(errs.NotFound, "no task or folder has ID %d", id).WithIDs(id)
	}
	return "", false, c.duplicateID(id)
}

// resolve is the folder r names. One named by ID must exist.
func (c *call) resolve(r folderRef) (model.Folder, *errs.Error) {
	if r.id == 0 {
		return r.path, nil
	}
	f, isTask, e := c.holder(r.id)
	if e != nil {
		if e.Kind == errs.NotFound {
			e.Message = fmt.Sprintf("no folder has ID %d", r.id)
		}
		return "", e
	}
	if isTask {
		return "", errs.New(errs.NotFound, "%d is the ID of a task, in %s, not of a folder", r.id, f).WithIDs(r.id)
	}
	return f, nil
}

// folderOrNotFound checks that folder f exists.
func (c *call) folderOrNotFound(f model.Folder) *errs.Error {
	ok, e := c.t.FolderExists(f)
	if e != nil {
		return e
	}
	if !ok {
		return errs.New(errs.NotFound, "folder %s does not exist", f).WithFolders(string(f))
	}
	return nil
}

// plan lists the folders missing on the paths to the folders fs, each
// once, in tree order: the order they are made in, and take IDs in.
func (c *call) plan(fs ...model.Folder) ([]model.Folder, *errs.Error) {
	var missing []model.Folder
	for _, f := range fs {
		m, e := c.t.Missing(f)
		if e != nil {
			return nil, e
		}
		for _, g := range m {
			if !slices.Contains(missing, g) {
				missing = append(missing, g)
			}
		}
	}
	slices.SortFunc(missing, model.TreeOrder)
	return missing, nil
}

// newFolder is a folder asked for by folder create or a batch folder
// entry, with what it is made with.
type newFolder struct {
	folder    model.Folder
	at        string // where it is in a batch's input
	title     *string
	extra     map[string]any
	notes     string
	items     []item     // a batch entry's blocked_by, as the input names it
	blockedBy []model.ID // its blocked_by, as IDs
}

// makeFolders makes the planned folders, parents first, the first taking
// the ID first and each the next. Each folder asked for gets its title,
// extra, and notes; one that is already there, which only an outside change
// can have put there, stops it with destination-exists. It returns the
// folders it made.
func (c *call) makeFolders(missing []model.Folder, first model.ID, asked ...newFolder) ([]model.Folder, *errs.Error) {
	made := []model.Folder{}
	for i, f := range missing {
		ff, notes := model.NewFolderFile(first+model.ID(i)), ""
		j := slices.IndexFunc(asked, func(n newFolder) bool { return n.folder == f })
		if j >= 0 {
			ff.Title, notes = asked[j].title, asked[j].notes
			if asked[j].extra != nil {
				ff.Extra = asked[j].extra
			}
			if asked[j].blockedBy != nil {
				ff.BlockedBy = slices.Clone(asked[j].blockedBy)
			}
		}
		ok, e := c.t.MakeFolder(f, ff, notes)
		if ok {
			made = append(made, f)
		}
		if e != nil {
			return made, e
		}
		if !ok && j >= 0 {
			return made, c.destinationExists("appeared while it was being made: its title, extra, and notes were not set", f)
		}
	}
	return made, nil
}

// destinationExists is the conflict over folders fs, which are already
// there: msg follows their names.
func (c *call) destinationExists(msg string, fs ...model.Folder) *errs.Error {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = string(f)
	}
	e := errs.NewConflict(errs.RuleDestinationExists, "%s %s", strings.Join(names, ", "), msg).WithFolders(names...)
	for _, f := range fs {
		e.WithPaths(c.t.Abs(f.Rel()))
	}
	return e
}

// ensure makes folder f and any missing folders above it, each with an ID
// of its own, and returns the folders it made.
func (c *call) ensure(f model.Folder) ([]model.Folder, *errs.Error) {
	missing, e := c.plan(f)
	if e != nil || len(missing) == 0 {
		return []model.Folder{}, e
	}
	first, e := c.issue(len(missing))
	if e != nil {
		return []model.Folder{}, e
	}
	return c.makeFolders(missing, first)
}
