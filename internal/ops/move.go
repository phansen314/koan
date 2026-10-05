package ops

import (
	"os"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// MoveInput is move's input.
type MoveInput struct {
	ID      model.ID
	To      model.FolderPath
	Parents bool
}

func decodeMove(f *model.Fields, p *model.Problems) any {
	in := MoveInput{ID: requiredID(f, p, "id")}
	if v, ok := f.Required("to"); ok {
		in.To, _ = p.FolderPath(v, "/to")
	}
	in.Parents = optionalBool(f, p, "parents", false)
	return in
}

// MoveOutput is move's result (move-output): the task after the operation,
// plus where it was, the folders created, and whether it moved.
type MoveOutput struct {
	model.Task
	From    model.FolderPath   `json:"from"`
	Created []model.FolderPath `json:"created"`
	Changed bool               `json:"changed"`
}

// CreatedPartial is move's and move-folder's partial result (move-partial,
// move-folder-partial): the folders created before the failure, which stay.
// Nothing moved.
type CreatedPartial struct {
	Created []model.FolderPath `json:"created"`
}

// runMove moves the one task with id into in.To, under the write lock. Its
// two files can't move in one step, so its notes are hard-linked into place
// first (store.Tx.LinkOver), then the task file is renamed — the moment it
// moves — then the old .md is removed, which is cleanup and cannot fail the
// operation. A task with no .md has any stray one in to removed instead, so
// the stray can't become its notes. A stray with text is a conflict.
func runMove(env Env, in MoveInput, w *errs.Collector) (any, *errs.Error) {
	out := MoveOutput{Created: []model.FolderPath{}}
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		n, e := tx.WalkFolder(in.To)
		if e != nil {
			return e
		}
		segs := len(in.To.Segments())
		var missingFolder []string
		if n < segs && !in.Parents {
			missingFolder = []string{string(folderPrefix(in.To, n+1))}
		}
		// loadCopies fails on an unusable copy, which comes after not-found in
		// precedence; any io comes first.
		found, le := loadCopies(tx, in.ID)
		if le != nil && le.Kind == errs.KindIO {
			return le
		}
		var missingIDs []int64
		if le == nil && len(found) == 0 {
			missingIDs = []int64{int64(in.ID)}
		}
		if missingFolder != nil || missingIDs != nil {
			return errs.NotFound(missingFolder, missingIDs, nil)
		}
		if le != nil {
			return le
		}
		if len(found) > 1 {
			return errs.Conflict(errs.RuleDuplicateID, []int64{int64(in.ID)})
		}
		ld := found[0]
		out.From = ld.Loc.Folder
		if ld.Loc.Folder == in.To {
			out.Task = tx.Task(ld)
			return nil
		}
		for i := n + 1; i <= segs; i++ {
			f := folderPrefix(in.To, i)
			created, e := mkdirFolder(tx, f)
			if e != nil {
				return partialCreated(e, out.Created)
			}
			if created {
				out.Created = append(out.Created, f)
			}
		}
		dst := store.Location{Folder: in.To, ID: in.ID}
		if e := moveTask(tx, ld.Loc, dst); e != nil {
			return partialCreated(e, out.Created)
		}
		moved := *ld
		moved.Loc = dst
		out.Task, out.Changed = tx.Task(&moved), true
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// moveTask moves the task at src to dst, notes first, per runMove.
func moveTask(tx *store.Tx, src, dst store.Location) *errs.Error {
	fi, err := tx.Lstat(src.NotesRel())
	notes := err == nil && fi.Mode().IsRegular()
	// A .md with text at dst, other than a second name for these notes, may
	// hold edits saved after an earlier move: never overwritten.
	if dfi, derr := tx.Lstat(dst.NotesRel()); derr == nil && dfi.Mode().IsRegular() && dfi.Size() > 0 &&
		!(notes && os.SameFile(fi, dfi)) {
		return errs.Conflict(errs.RuleDestinationExists, []int64{int64(dst.ID)})
	}
	switch {
	case notes:
		if e := tx.LinkOver(src.NotesRel(), dst.NotesRel()); e != nil {
			return e
		}
	case err != nil && !isENOENT(err):
		return tx.OSError(src.NotesRel(), err)
	default:
		if err := tx.Remove(dst.NotesRel()); err != nil && !isENOENT(err) {
			return tx.OSError(dst.NotesRel(), err)
		}
	}
	if e := tx.Move(src.Rel(), dst.Rel()); e != nil {
		return e
	}
	if notes {
		tx.Remove(src.NotesRel())
	}
	return nil
}

func isENOENT(err error) bool {
	errno, ok := errs.ErrnoOf(err)
	return ok && errno == syscall.ENOENT
}

// partialCreated is e with the folders created so far as its partial, if
// there are any.
func partialCreated(e *errs.Error, created []model.FolderPath) *errs.Error {
	if len(created) == 0 {
		return e
	}
	return e.WithPartial(CreatedPartial{Created: created})
}

// MoveFolderInput is move-folder's input.
type MoveFolderInput struct {
	Folder  model.FolderPath
	To      model.FolderPath
	Parents bool
}

func decodeMoveFolder(f *model.Fields, p *model.Problems) any {
	var in MoveFolderInput
	var folderOK, toOK bool
	if v, ok := f.Required("folder"); ok {
		if in.Folder, folderOK = p.FolderPath(v, "/folder"); folderOK && in.Folder == model.RootFolder {
			p.AddAdditional("/folder", "the root can never be moved")
			folderOK = false
		}
	}
	if v, ok := f.Required("to"); ok {
		in.To, toOK = p.FolderPath(v, "/to")
	}
	if folderOK && toOK && under(in.To, in.Folder) {
		p.AddAdditional("/to", "a folder can't be moved into itself")
	}
	in.Parents = optionalBool(f, p, "parents", false)
	return in
}

// under reports whether g is f or a folder below it.
func under(g, f model.FolderPath) bool {
	return g == f || f == model.RootFolder || strings.HasPrefix(string(g), string(f)+"/")
}

// MoveFolderOutput is move-folder's result (move-folder-output).
type MoveFolderOutput struct {
	Folder  model.FolderPath   `json:"folder"`
	From    model.FolderPath   `json:"from"`
	Created []model.FolderPath `json:"created"`
	Changed bool               `json:"changed"`
}

// runMoveFolder moves folder to its target, like mv: into to when to is an
// existing folder, otherwise to the path to. It is one no-replace rename, so
// a folder is never merged, and it reads no task file: tasks are named by
// ID, so no reference changes.
func runMoveFolder(env Env, in MoveFolderInput, w *errs.Collector) (any, *errs.Error) {
	out := MoveFolderOutput{From: in.Folder, Created: []model.FolderPath{}}
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		var missing []string
		n, e := tx.WalkFolder(in.Folder)
		if e != nil {
			return e
		}
		if n < len(in.Folder.Segments()) {
			missing = append(missing, string(folderPrefix(in.Folder, n+1)))
		}
		m, e := tx.WalkFolder(in.To)
		if e != nil {
			if missing != nil {
				return errs.NotFound(missing, nil, nil)
			}
			return e
		}
		segs := in.To.Segments()
		target := in.To
		switch {
		case m == len(segs):
			target = childFolder(in.To, lastSegment(in.Folder))
		case m < len(segs)-1 && !in.Parents:
			missing = append(missing, string(folderPrefix(in.To, m+1)))
		}
		if missing != nil {
			return errs.NotFound(missing, nil, nil)
		}
		out.Folder = target
		if target == in.Folder {
			return nil
		}
		if target != in.To {
			if e := tx.CaseClash(target); e != nil {
				return e
			}
		}
		if _, err := tx.Lstat(store.FolderRel(target)); err == nil {
			return errs.Conflict(errs.RuleDestinationExists, nil)
		} else if !isENOENT(err) {
			return tx.OSError(store.FolderRel(target), err)
		}
		for i := m + 1; i < len(segs); i++ {
			f := folderPrefix(in.To, i)
			created, e := mkdirFolder(tx, f)
			if e != nil {
				return partialCreated(e, out.Created)
			}
			if created {
				out.Created = append(out.Created, f)
			}
		}
		if e := tx.Move(store.FolderRel(in.Folder), store.FolderRel(target)); e != nil {
			return partialCreated(e, out.Created)
		}
		out.Changed = true
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

func lastSegment(f model.FolderPath) string {
	s := f.Segments()
	return s[len(s)-1]
}

func childFolder(f model.FolderPath, name string) model.FolderPath {
	if f == model.RootFolder {
		return model.FolderPath("/" + name)
	}
	return f + model.FolderPath("/"+name)
}
