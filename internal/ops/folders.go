package ops

import (
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

type folderResult struct {
	Folder         model.Folder   `json:"folder"`
	FoldersCreated []model.Folder `json:"folders_created"`
	Changed        *bool          `json:"changed,omitempty"`
}

func cmdCreateFolder(c *call) (any, *errs.Error) {
	f := c.in.top().folder("folder", true, model.Root)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	created, e := c.ensure(f)
	if e != nil {
		return nil, e
	}
	return folderResult{Folder: f, FoldersCreated: created}, nil
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
	res := folderResult{Folder: to, FoldersCreated: []model.Folder{}, Changed: &changed}
	if !changed {
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
			own := name == store.FolderFileName
			if name == store.FolderNotesName {
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
