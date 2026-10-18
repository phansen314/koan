package ops

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// folderResult is what create-folder and move-folder return.
type folderResult struct {
	Folder         model.FolderRecord `json:"folder"`
	FoldersCreated []model.Folder     `json:"folders_created"`
	Changed        *bool              `json:"changed,omitempty"`
}

// shownFolder is what show-folder and update-folder return.
type shownFolder struct {
	Folder  model.FolderRecord `json:"folder"`
	Changed *bool              `json:"changed,omitempty"`
}

// record is folder f's record. A folder file koan can't use is the error,
// with the record as far as it goes; a folder with none has an ID of null.
func (c *call) record(f model.Folder) (model.FolderRecord, *errs.Error) {
	r := model.FolderRecord{Path: f, Dir: c.t.Abs(f.Rel()), Extra: map[string]any{}}
	if f == model.Root {
		return r, nil
	}
	notes := c.t.Abs(store.FolderNotesRel(f))
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
		c.warn(errs.WarnUnusableFile, e.Message, []model.ID{}, c.t.Abs(store.FolderFileRel(f)))
	}
	return r
}

func cmdCreateFolder(c *call) (any, *errs.Error) {
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
	res := folderResult{FoldersCreated: []model.Folder{}}
	if len(missing) == 0 {
		if hasTitle || hasExtra || hasNotes {
			return nil, errs.NewConflict(errs.RuleDestinationExists,
				"%s already exists: set its title and extra with update-folder, and edit its notes in its @folder.md", f).
				WithFolders(string(f)).WithPaths(c.t.Abs(f.Rel()))
		}
		res.Folder = c.passFolder(f)
		return res, nil
	}
	first, e := c.issue(len(missing))
	if e != nil {
		return nil, e
	}
	if res.FoldersCreated, e = c.makeFolders(missing, first, &newFolder{folder: f, title: title, extra: extra, notes: notes}); e != nil {
		return nil, e
	}
	// Only an outside change can have put a folder there meanwhile.
	if !slices.Contains(res.FoldersCreated, f) && (hasTitle || hasExtra || hasNotes) {
		return nil, errs.NewConflict(errs.RuleDestinationExists, "%s appeared while it was being made: its title, extra, and notes were not set", f).
			WithFolders(string(f)).WithPaths(c.t.Abs(f.Rel()))
	}
	res.Folder = c.passFolder(f)
	return res, nil
}

func cmdShowFolder(c *call) (any, *errs.Error) {
	f := c.in.top().folder("folder", true, model.Root)
	if e := c.begin(false); e != nil {
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

func cmdUpdateFolder(c *call) (any, *errs.Error) {
	in := c.in.top()
	f := in.folder("folder", true, "")
	title, hasTitle := in.optTitle("title")
	edit := readExtraEdit(c, in)
	if f == model.Root {
		c.in.bad("/folder", "may not be the root, which has no folder file")
	}
	if len(c.in.m) < 2 && c.in.has("folder") {
		c.in.bad("", "gives nothing to change")
	}
	if e := c.begin(true); e != nil {
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
		return nil, errs.New(errs.NotFound, "folder %s has no %s: `koan repair` writes one", f, store.FolderFileName).
			WithFolders(string(f)).WithPaths(c.t.Abs(store.FolderFileRel(f)))
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
	r, e := c.record(f)
	if e != nil {
		return nil, e
	}
	return shownFolder{Folder: r, Changed: &changed}, nil
}

// extraEdit is a change to an extra object, as update and update-folder
// take it.
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

func cmdMoveFolder(c *call) (any, *errs.Error) {
	in := c.in.top()
	from := in.folder("folder", true, "")
	to := in.folder("to", true, "")
	if from == model.Root {
		c.in.bad("/folder", "may not be the root")
	}
	if to == model.Root {
		c.in.bad("/to", "may not be the root")
	}
	if from != "" && to != "" && from != to && from.Holds(to) {
		c.in.bad("/to", "may not be under the folder being moved")
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(from); e != nil {
		return nil, e
	}
	changed := from != to
	res := folderResult{FoldersCreated: []model.Folder{}, Changed: &changed}
	if !changed {
		res.Folder = c.passFolder(to)
		return res, nil
	}
	exists := func() *errs.Error {
		return errs.NewConflict(errs.RuleDestinationExists, "%s already exists", to).
			WithFolders(string(to)).WithPaths(c.t.Abs(to.Rel()))
	}
	if _, err := c.t.Stat(to.Rel()); err == nil {
		return nil, exists()
	}
	created, e := c.ensure(to.Parent())
	res.FoldersCreated = created
	if e != nil {
		return nil, e
	}
	taken, e := c.t.Move(from.Rel(), to.Rel())
	if e != nil {
		return nil, e
	}
	if taken {
		return nil, exists()
	}
	res.Folder = c.passFolder(to)
	return res, nil
}

type deleteFolderResult struct {
	Folder     model.Folder `json:"folder"`
	Deleted    []model.ID   `json:"deleted"`
	Dependents []model.ID   `json:"dependents"`
}

func cmdDeleteFolder(c *call) (any, *errs.Error) {
	in := c.in.top()
	f := in.folder("folder", true, "")
	recursive := in.boolean("recursive", false)
	force := in.boolean("force", false)
	if f == model.Root {
		c.in.bad("/folder", "may not be the root")
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	if !recursive {
		entries, e := c.t.List(f)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			name := entry.Name()
			own := false
			switch name {
			case store.FolderFileName:
				fi, err := c.t.Stat(store.FolderFileRel(f))
				own = err == nil && fi.Mode().IsRegular()
			case store.FolderNotesName:
				fi, err := c.t.Stat(store.FolderNotesRel(f))
				own = err == nil && fi.Mode().IsRegular() && fi.Size() == 0
			}
			if !own && !strings.HasPrefix(name, ".") {
				return nil, errs.NewConflict(errs.RuleNotEmpty, "%s is not empty: pass recursive to remove everything in it", f).
					WithFolders(string(f))
			}
		}
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	if recursive && !force {
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
	res := deleteFolderResult{Folder: f, Deleted: []model.ID{}}
	for _, id := range x.IDs() {
		if f.Holds(x.Tasks[id]) {
			res.Deleted = append(res.Deleted, id)
		}
	}
	// The folder goes first, for the reason delete gives.
	if e := c.t.Discard(f.Rel()); e != nil {
		return nil, e
	}
	if res.Dependents, e = c.unreference(res.Deleted); e != nil {
		return nil, e
	}
	if e := c.dropClaims(res.Deleted...); e != nil {
		return nil, e
	}
	return res, nil
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
