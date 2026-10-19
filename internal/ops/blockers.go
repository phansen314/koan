package ops

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
)

// resolved is an item of the input as an ID, with where the input named it.
type resolved struct {
	id model.ID
	at string
}

// idsOf are the IDs of rs, in order.
func idsOf(rs []resolved) []model.ID {
	out := make([]model.ID, len(rs))
	for i, r := range rs {
		out[i] = r.id
	}
	return out
}

// blockers resolves the tasks and folders an input names to IDs, each once,
// in the order given (spec/operations.md, References). A path must name a
// folder with an ID. An ID must name a task or a folder, every missing one
// reported at once, unless anyID is set, as in task unblock and folder
// unblock, where an ID need not exist.
func (c *call) blockers(items []item, anyID bool) ([]resolved, *errs.Error) {
	if _, e := c.folders(); e != nil {
		return nil, e
	}
	var out []resolved
	var gone []model.ID
	for _, it := range items {
		id := it.id
		if id == 0 {
			var e *errs.Error
			if id, e = c.folderIDAt(it.path); e != nil {
				return nil, e
			}
		} else if !anyID {
			_, isTask := c.x.Tasks[id]
			switch n := len(c.fx.byID[id]); {
			case !isTask && n == 0:
				gone = append(gone, id)
				continue
			case isTask && n > 0 || n > 1:
				return nil, c.duplicateID(id)
			}
		}
		if !slices.ContainsFunc(out, func(r resolved) bool { return r.id == id }) {
			out = append(out, resolved{id, it.at})
		}
	}
	if len(gone) > 0 {
		if len(gone) == 1 {
			return nil, errs.New(errs.NotFound, "no task or folder has ID %d", gone[0]).WithIDs(gone...)
		}
		return nil, errs.New(errs.NotFound, "no task or folder has IDs %v", gone).WithIDs(gone...)
	}
	return out, nil
}

// folderIDAt is the ID of the folder at path f, which a blocked_by stores
// in its place.
func (c *call) folderIDAt(f model.Folder) (model.ID, *errs.Error) {
	if e := c.folderOrNotFound(f); e != nil {
		return 0, e
	}
	info := c.fx.at[f]
	switch {
	case info.file != nil:
		return info.file.ID, nil
	case info.err != nil:
		return 0, info.err
	}
	return 0, errs.New(errs.NotFound, "folder %s has no %s, so it has no ID to wait on: `koan repair` writes one", f, model.FolderFileName).
		WithFolders(string(f)).WithPaths(c.t.Abs(model.FolderFileRel(f)))
}

// folderFile is the folder file of folder f, which a command that changes
// it needs: one with none is not-found, and one that can't be used fails
// with its error.
func (c *call) folderFile(f model.Folder) (*model.FolderFile, *errs.Error) {
	if _, e := c.folders(); e != nil {
		return nil, e
	}
	info := c.fx.at[f]
	switch {
	case info.err != nil:
		return nil, info.err
	case info.file == nil:
		return nil, errs.New(errs.NotFound, "folder %s has no %s: `koan repair` writes one", f, model.FolderFileName).
			WithFolders(string(f)).WithPaths(c.t.Abs(model.FolderFileRel(f)))
	}
	return info.file, nil
}

// dependent is a task or folder in a blocks, which a create makes wait on
// what it makes.
type dependent struct {
	id     model.ID
	at     string
	folder model.Folder      // the task's folder, or the folder's own path
	task   *model.TaskFile   // for a task
	file   *model.FolderFile // for a folder
}

// ref is the node of the dependent that gets the new edge: a task's, or a
// folder's wait node.
func (d dependent) ref() graph.Ref {
	if d.task != nil {
		return graph.TaskRef(d.id)
	}
	return graph.WaitOf(d.folder)
}

// dependents reads everything in a blocks, ascending, before an ID is used:
// each is rewritten, so a file that can't be used refuses the command.
func (c *call) dependents(rs []resolved) ([]dependent, *errs.Error) {
	rs = slices.Clone(rs)
	slices.SortFunc(rs, func(a, b resolved) int { return cmp.Compare(a.id, b.id) })
	var deps []dependent
	for _, r := range rs {
		if f, isTask := c.x.Tasks[r.id]; isTask {
			_, tf, e := c.find(r.id)
			if e != nil {
				return nil, e
			}
			deps = append(deps, dependent{id: r.id, at: r.at, folder: f, task: tf})
			continue
		}
		f := c.fx.byID[r.id][0]
		ff, e := c.folderFile(f)
		if e != nil {
			return nil, e
		}
		deps = append(deps, dependent{id: r.id, at: r.at, folder: f, file: ff})
	}
	return deps, nil
}

// block makes each dependent wait on id, in ascending order, before what
// has id exists: a kill between leaves them waiting on an ID with nothing,
// a dangling reference doctor finds. It returns the IDs of those rewritten;
// a failure names those rewritten before it, and says what was not made.
func (c *call) block(deps []dependent, id model.ID, what string) ([]model.ID, *errs.Error) {
	rewritten := []model.ID{}
	for _, d := range deps {
		var e *errs.Error
		if d.task != nil {
			d.task.BlockedBy = append(d.task.BlockedBy, id)
			d.task.UpdatedAt = c.now
			e = c.t.WriteTask(d.folder, d.task, false)
		} else {
			d.file.BlockedBy = append(d.file.BlockedBy, id)
			e = c.t.WriteFolderFile(d.folder, d.file, false)
		}
		if e != nil {
			if len(rewritten) > 0 {
				e.Message = fmt.Sprintf("%s %d was not created, but %v were made to wait on it before %d could not be: %s", what, id, rewritten, d.id, e.Message)
			}
			return nil, e.WithIDs(rewritten...)
		}
		rewritten = append(rewritten, d.id)
	}
	return rewritten, nil
}

// nextID is the first ID issue would issue now, without issuing it: what a
// change is checked with before an ID is used.
func (c *call) nextID() (model.ID, *errs.Error) {
	top, _, e := c.highest()
	if e != nil {
		return 0, e
	}
	return max(top, c.t.State.LastID) + 1, nil
}
