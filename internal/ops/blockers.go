package ops

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

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
	var m absent
	out, e := c.gather(items, anyID, &m)
	if e == nil {
		e = m.err()
	}
	if e != nil {
		return nil, e
	}
	return out, nil
}

// gather is blockers for one field of an input with several: what is
// missing is added to m, for one not-found over all of them, and any other
// error is returned at once.
func (c *call) gather(items []item, anyID bool, m *absent) ([]resolved, *errs.Error) {
	if _, e := c.folders(); e != nil {
		return nil, e
	}
	var out []resolved
	for _, it := range items {
		id := it.id
		if id == 0 {
			if ok, e := c.t.FolderExists(it.path); e != nil {
				return nil, e
			} else if !ok {
				m.folder(it.path, "")
				continue
			}
			// The folder file holds the ID a blocked_by stores: one with
			// none is missing too, and one koan can't use fails with its
			// own error.
			switch info := c.fx.at[it.path]; {
			case info.err != nil:
				return nil, info.err
			case info.file == nil:
				m.folder(it.path, c.t.Abs(model.FolderFileRel(it.path)))
				continue
			default:
				id = info.file.ID
			}
		} else if !anyID {
			_, isTask := c.x.Tasks[id]
			switch n := len(c.fx.byID[id]); {
			case !isTask && n == 0:
				m.id(id)
				continue
			case isTask && n > 0 || n > 1:
				return nil, c.duplicateID(id)
			}
		}
		if !slices.ContainsFunc(out, func(r resolved) bool { return r.id == id }) {
			out = append(out, resolved{id, it.at})
		}
	}
	return out, nil
}

// alsoAbsent is the not-found error gone, with e's added when e is
// not-found too: what blockers found missing, beside the task or folder the
// command was asked for.
func alsoAbsent(gone, e *errs.Error) *errs.Error {
	if e != nil && e.Kind == errs.NotFound {
		gone.Message += "; " + e.Message
		gone.WithIDs(e.IDs...).WithFolders(e.Folders...).WithPaths(e.Paths...)
	}
	return gone
}

// absent is what an input names that does not exist, gathered over all of
// its fields, since not-found names everything missing at once.
type absent struct {
	ids     []model.ID
	folders []absentFolder
}

// absentFolder is a folder path an input names that has no ID: no folder
// is there, or one is, with no folder file, whose path is file.
type absentFolder struct {
	path model.Folder
	file string
}

func (m *absent) id(id model.ID) {
	if !slices.Contains(m.ids, id) {
		m.ids = append(m.ids, id)
	}
}

// folder adds folder f: one that is not there, or, with file, one that has
// no folder file at that path.
func (m *absent) folder(f model.Folder, file string) {
	if !slices.ContainsFunc(m.folders, func(a absentFolder) bool { return a.path == f }) {
		m.folders = append(m.folders, absentFolder{f, file})
	}
}

// err is the not-found error naming everything missing, or nil.
func (m *absent) err() *errs.Error {
	var parts []string
	switch len(m.ids) {
	case 0:
	case 1:
		parts = append(parts, fmt.Sprintf("no task or folder has ID %d", m.ids[0]))
	default:
		parts = append(parts, fmt.Sprintf("no task or folder has IDs %v", m.ids))
	}
	for _, f := range m.folders {
		if f.file == "" {
			parts = append(parts, fmt.Sprintf("folder %s does not exist", f.path))
		} else {
			parts = append(parts, fmt.Sprintf("folder %s has no %s, so it has no ID to wait on: `koan repair` writes one", f.path, model.FolderFileName))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	e := errs.New(errs.NotFound, "%s", strings.Join(parts, "; ")).WithIDs(m.ids...)
	for _, f := range m.folders {
		e.WithFolders(string(f.path))
		if f.file != "" {
			e.WithPaths(f.file)
		}
	}
	return e
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

// waitIn makes the dependent wait on id in the overlay o.
func (d dependent) waitIn(o *overlay, id model.ID) {
	if d.task != nil {
		o.blockTask(d.id, id)
		return
	}
	o.blockFolder(d.folder, d.id, id)
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
func (c *call) nextID() model.ID { return c.t.Meta.LastID + 1 }
