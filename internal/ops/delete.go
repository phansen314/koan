package ops

import (
	"slices"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

func decodeDelete(f *model.Fields, p *model.Problems) any { return decodeID(f, p) }

// DeleteOutput is delete's result (delete-output).
type DeleteOutput struct {
	ID         model.ID         `json:"id"`
	Folder     model.FolderPath `json:"folder"`
	Dependents []model.ID       `json:"dependents"`
}

// DependentsPartial is delete's and delete-folder's partial result
// (delete-partial, delete-folder-partial): the dependents rewritten before
// the failure. Nothing was removed.
type DependentsPartial struct {
	Dependents []model.ID `json:"dependents"`
}

// runDelete removes the one task with id, under the write lock: first its ID
// from every blocked_by that holds it, then its task file — the moment it is
// gone — then its .md, whose removal is cleanup and cannot fail the
// operation. The task file itself is never read, so an unusable task can be
// deleted too.
func runDelete(env Env, in IDInput, w *errs.Collector) (any, *errs.Error) {
	var out DeleteOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		if e := tx.RequireWholeTree(tx.Index()); e != nil {
			return e
		}
		locs := tx.Copies(in.ID)
		switch {
		case len(locs) == 0:
			return errs.NotFound(nil, []int64{int64(in.ID)}, nil)
		case len(locs) > 1:
			return errs.Conflict(errs.RuleDuplicateID, []int64{int64(in.ID)})
		case int64(in.ID) > tx.Meta().LastID:
			return errs.Conflict(errs.RuleIDAboveLastID, []int64{int64(in.ID)})
		}
		target := locs[0]
		deps, e := removeReferences(tx, []model.ID{in.ID}, func(l store.Location) bool { return l == target }, model.TimestampOf(env.Clock()))
		if e != nil {
			return e
		}
		if err := tx.Remove(target.Rel()); err != nil {
			if errno, ok := errs.ErrnoOf(err); ok && errno == syscall.ENOENT {
				// Gone since the walk: only an outside change can, under the lock.
				return partialDependents(errs.NotFound(nil, []int64{int64(in.ID)}, nil), deps)
			}
			return partialDependents(tx.OSError(target.Rel(), err), deps)
		}
		tx.Remove(target.NotesRel())
		out = DeleteOutput{ID: in.ID, Folder: target.Folder, Dependents: deps}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// removeReferences removes ids from the blocked_by of every task file not
// being removed (skip), one file at a time, in tree order, stamping each
// updated_at now, and returns the IDs of the tasks it rewrote, ascending. An
// unusable task file may hold a reference that can't be read, so every one
// is an unusable-file warning. An error after at least one rewrite carries
// the partial.
func removeReferences(tx *store.Tx, ids []model.ID, skip func(store.Location) bool, now model.Timestamp) ([]model.ID, *errs.Error) {
	deps := []model.ID{}
	for _, l := range tx.Index().Tasks {
		if skip(l) {
			continue
		}
		ld := tx.Load(l)
		switch ld.State {
		case store.Vanished:
			continue
		case store.Usable:
		default:
			if e := tx.Relevant(ld); e != nil {
				return nil, partialDependents(e, deps)
			}
			continue
		}
		kept := slices.DeleteFunc(slices.Clone(ld.Task.BlockedBy), func(b model.ID) bool { return slices.Contains(ids, b) })
		if len(kept) == len(ld.Task.BlockedBy) {
			continue
		}
		t := ld.Task
		t.BlockedBy = kept
		if e := replaceTask(tx, l.Rel(), &t, now); e != nil {
			return nil, partialDependents(e, deps)
		}
		deps = append(deps, l.ID)
	}
	slices.Sort(deps)
	return slices.Compact(deps), nil
}

// partialDependents is e with the dependents rewritten so far as its
// partial, if there are any.
func partialDependents(e *errs.Error, deps []model.ID) *errs.Error {
	if len(deps) == 0 {
		return e
	}
	sorted := slices.Compact(slices.Sorted(slices.Values(deps)))
	return e.WithPartial(DependentsPartial{Dependents: sorted})
}

// DeleteFolderInput is delete-folder's input.
type DeleteFolderInput struct {
	Folder    model.FolderPath
	Recursive bool
}

func decodeDeleteFolder(f *model.Fields, p *model.Problems) any {
	var in DeleteFolderInput
	if v, ok := f.Required("folder"); ok {
		var valid bool
		if in.Folder, valid = p.FolderPath(v, "/folder"); valid && in.Folder == model.RootFolder {
			p.AddAdditional("/folder", "the root can never be deleted")
		}
	}
	in.Recursive = optionalBool(f, p, "recursive", false)
	return in
}

// DeleteFolderOutput is delete-folder's result (delete-folder-output).
type DeleteFolderOutput struct {
	Folder     model.FolderPath   `json:"folder"`
	Folders    []model.FolderPath `json:"folders"`
	IDs        []model.ID         `json:"ids"`
	Dependents []model.ID         `json:"dependents"`
}

// runDeleteFolder removes folder and everything under it, under the write
// lock: first the IDs of the tasks under it from every blocked_by outside
// it, then the folder, renamed aside in one step and then removed
// (store.Tx.Discard). The task files under it are never read — only their
// names — so unusable ones go with it. The rest of the tree is walked only
// when there are references to find.
func runDeleteFolder(env Env, in DeleteFolderInput, w *errs.Collector) (any, *errs.Error) {
	var out DeleteFolderOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		n, e := tx.WalkFolder(in.Folder)
		if e != nil {
			return e
		}
		if n < len(in.Folder.Segments()) {
			return errs.NotFound([]string{string(folderPrefix(in.Folder, n+1))}, nil, nil)
		}
		x := tx.Index()
		under := func(g model.FolderPath) bool {
			return g == in.Folder || strings.HasPrefix(string(g), string(in.Folder)+"/")
		}
		for _, u := range x.Unreadable {
			if under(u.Folder) {
				return tx.OSError(store.FolderRel(u.Folder), u.Err)
			}
		}
		folders, tasks := x.InScope(in.Folder, true)
		ids := make([]model.ID, 0, len(tasks))
		for _, l := range tasks {
			ids = append(ids, l.ID)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		if !in.Recursive {
			if len(tasks) > 0 || len(folders) > 1 {
				return errs.Conflict(errs.RuleNotEmpty, int64s(ids))
			}
			if other, e := tx.HoldsOther(in.Folder); e != nil {
				return e
			} else if other {
				return errs.Conflict(errs.RuleNotEmpty, int64s(ids))
			}
		}
		deps := []model.ID{}
		if len(ids) > 0 {
			if e := tx.RequireWholeTree(x); e != nil {
				return e
			}
			var dup, above []int64
			for _, id := range ids {
				if len(tx.Copies(id)) > 1 {
					dup = append(dup, int64(id))
				}
				if int64(id) > tx.Meta().LastID {
					above = append(above, int64(id))
				}
			}
			switch {
			case dup != nil:
				return errs.Conflict(errs.RuleDuplicateID, dup)
			case above != nil:
				return errs.Conflict(errs.RuleIDAboveLastID, above)
			}
			if deps, e = removeReferences(tx, ids, func(l store.Location) bool { return under(l.Folder) }, model.TimestampOf(env.Clock())); e != nil {
				return e
			}
		}
		if e := tx.Discard(store.FolderRel(in.Folder)); e != nil {
			return partialDependents(e, deps)
		}
		out = DeleteFolderOutput{Folder: in.Folder, Folders: folders, IDs: ids, Dependents: deps}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
