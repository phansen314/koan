package ops

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// folderWritten is what folder update returns: the folder's identity.
type folderWritten struct {
	ID      *model.ID    `json:"id"`
	Path    model.Folder `json:"path"`
	Changed bool         `json:"changed"`
}

// folderMade is what folder create and folder move return.
type folderMade struct {
	folderWritten
	FoldersCreated []model.Folder `json:"folders_created"`
}

// made is the result of a folder create or folder move that left folder f
// at its path. Its ID is read from its folder file: one koan can't use is a
// warning.
func (c *call) made(f model.Folder, changed bool, created []model.Folder) folderMade {
	return folderMade{folderWritten{c.passFolder(f).ID, f, changed}, created}
}

type shownFolder struct {
	Folder model.FolderRecord `json:"folder"`
}

// record is folder f's record. A folder file koan can't use is the error,
// with the record as far as it goes; a folder with none has an ID of null.
func (c *call) record(f model.Folder) (model.FolderRecord, *errs.Error) {
	r := model.FolderRecord{Path: f, Dir: c.t.Abs(f.Rel()), Extra: map[string]any{}}
	if f == model.Root {
		return r, nil
	}
	notes := c.t.Abs(model.FolderNotesRel(f))
	r.NotesPath = &notes
	ff, _, e := c.t.LoadFolder(f)
	if e != nil {
		return r, e
	}
	if ff != nil {
		r.ID, r.Title, r.Extra = &ff.ID, ff.Title, ff.Extra
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
	if e := c.begin(true); e != nil {
		return nil, e
	}
	missing, e := c.plan(f)
	if e != nil {
		return nil, e
	}
	if len(missing) == 0 {
		if hasTitle || hasExtra || hasNotes {
			return nil, c.destinationExists("already exists: set its title and extra with folder update, and edit its notes in its @folder.md", f)
		}
		return c.made(f, false, []model.Folder{}), nil
	}
	first, e := c.issue(len(missing))
	if e != nil {
		return nil, e
	}
	// A folder with nothing to set may appear meanwhile without harm.
	var asked []newFolder
	if hasTitle || hasExtra || hasNotes {
		asked = append(asked, newFolder{folder: f, title: title, extra: extra, notes: notes})
	}
	created, e := c.makeFolders(missing, first, asked...)
	if e != nil {
		return nil, e
	}
	return c.made(f, len(created) > 0, created), nil
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
	if _, err := c.t.Stat(to.Rel()); err == nil {
		return nil, c.destinationExists("already exists", to)
	}
	created, e := c.ensure(to.Parent())
	if e != nil {
		return nil, e
	}
	taken, e := c.t.Move(from.Rel(), to.Rel())
	if e != nil {
		return nil, e
	}
	if taken {
		return nil, c.destinationExists("already exists", to)
	}
	return c.made(to, true, created), nil
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
	// The folder goes first, for the reason task delete gives.
	if e := c.t.Discard(f.Rel()); e != nil {
		return nil, c.discardFailed(e, res.Deleted)
	}
	if res.Dependents, e = c.unreference(res.Deleted); e != nil {
		return nil, e
	}
	if e := c.dropClaims(res.Deleted...); e != nil {
		return nil, e
	}
	return res, nil
}

// discardFailed finishes a folder delete whose removal failed partway, for
// the tasks it did remove: their IDs leave every blocked_by and their
// claims end, as for a removal that succeeded. It returns the removal's
// error, naming those tasks; a failure of its own is added to the message.
// The index still holds: nothing but the removed tasks has moved.
func (c *call) discardFailed(e *errs.Error, deleted []model.ID) *errs.Error {
	gone := []model.ID{}
	for _, id := range deleted {
		if _, err := c.t.Stat(model.TaskRel(c.x.Tasks[id], id)); errs.IsErrno(err, syscall.ENOENT) {
			gone = append(gone, id)
		}
	}
	if len(gone) == 0 {
		return e
	}
	e.WithIDs(gone...)
	e.Message += fmt.Sprintf("; it removed %d of the tasks", len(gone))
	dependents, ce := c.unreference(gone)
	if ce == nil {
		ce = c.dropClaims(gone...)
	}
	if ce != nil {
		e.Message += fmt.Sprintf(", and cleaning up after them failed too, which doctor finds: %s", ce.Message)
		return e
	}
	others := "tasks"
	if len(dependents) == 1 {
		others = "task"
	}
	e.Message += fmt.Sprintf(", and %d other %s no longer wait on them", len(dependents), others)
	return e
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

// unreference removes the IDs in gone from the blocked_by of every other
// task, and returns the IDs of the tasks it rewrote. It needs the index.
func (c *call) unreference(gone []model.ID) ([]model.ID, *errs.Error) {
	dependents := []model.ID{}
	if len(gone) == 0 {
		return dependents, nil
	}
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
	return dependents, nil
}

// folderIDs maps each folder ID to the folders that have it, reading every
// folder file once per command. A folder file koan can't use is a warning:
// it may have held the ID asked for.
func (c *call) folderIDs() (map[model.ID][]model.Folder, *errs.Error) {
	if c.byID != nil {
		return c.byID, nil
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	c.byID = map[model.ID][]model.Folder{}
	eachFolderFile(c.t, x, func(f model.Folder, ff *model.FolderFile, e *errs.Error) {
		if e != nil {
			c.warn(errs.WarnUnusableFile, e.Message, []model.ID{}, c.t.Abs(model.FolderFileRel(f)))
			return
		}
		c.byID[ff.ID] = append(c.byID[ff.ID], f)
	})
	return c.byID, nil
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
	e = errs.NewConflict(errs.RuleDuplicateID, "ID %d is held by more than one task or folder", id).WithIDs(id)
	if isTask {
		e.WithPaths(c.t.Abs(model.TaskRel(tf, id)))
	}
	for _, f := range folders {
		e.WithPaths(c.t.Abs(model.FolderFileRel(f)))
	}
	return "", false, e
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
	folder model.Folder
	title  *string
	extra  map[string]any
	notes  string
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
