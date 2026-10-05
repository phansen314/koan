package store

import (
	"io/fs"
	"path"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// Lstat reports the entry at rel without following a final symlink.
func (tx *Tx) Lstat(rel string) (fs.FileInfo, error) { return tx.root.Lstat(rel) }

// ReadFile reads the file at rel as it is, without checking it: doctor's
// comparison of duplicate copies.
func (tx *Tx) ReadFile(rel string) ([]byte, error) { return tx.root.ReadFile(rel) }

// Remove removes the file at rel, returning the OS error: whether one is an
// error is the caller's call.
func (tx *Tx) Remove(rel string) error {
	if e := tx.mustWrite("remove " + rel); e != nil {
		return e
	}
	return tx.root.Remove(rel)
}

// Move moves the file or folder at oldRel to newRel, which must be free:
// anything already there is corrupt (unexpected-file), since the caller
// checked under the lock that nothing is, so only an outside change can have
// put it there.
func (tx *Tx) Move(oldRel, newRel string) *errs.Error {
	if e := tx.mustWrite("move " + oldRel); e != nil {
		return e
	}
	err := tx.root.RenameNoReplace(oldRel, newRel)
	switch {
	case err == nil:
		return nil
	case isErrno(err, syscall.EEXIST), isErrno(err, syscall.ENOTEMPTY):
		return errs.Corrupt(tx.Path(newRel), errs.CorruptUnexpectedFile)
	}
	return tx.OSError(oldRel, err)
}

// LinkOver makes newRel a hard link to the file at oldRel, replacing any
// file already at newRel: linked to a temp name in newRel's folder, then
// renamed over it, so newRel is never missing and never partial. The folder
// is then flushed, so a system crash can't keep a later step (the task
// file's rename, the old name's removal) and lose the link. The temp link
// is removed afterwards, whether the rename did anything or not.
func (tx *Tx) LinkOver(oldRel, newRel string) *errs.Error {
	if e := tx.mustWrite("link " + oldRel); e != nil {
		return e
	}
	tmp := fsys.TempName(path.Dir(newRel))
	if err := tx.root.Link(oldRel, tmp); err != nil {
		return tx.OSError(oldRel, err)
	}
	if err := tx.root.Rename(tmp, newRel); err != nil {
		tx.root.Remove(tmp)
		return tx.OSError(newRel, err)
	}
	tx.root.SyncDir(path.Dir(newRel)) // a failure is ignored, as in Publish
	// Renaming onto another name of the same file does nothing (POSIX) and
	// leaves tmp behind; remove it. A failure leaves a temp-leftover.
	tx.root.Remove(tmp)
	return nil
}

// Discard removes a folder in one step as far as the tree is concerned: it
// is renamed aside, to a hidden temp name in the root, then removed with
// everything under it. Once renamed, it is gone from the tree, and Discard
// cannot fail: a removal that fails or is interrupted leaves a hidden
// leftover, which reads ignore and doctor reports.
func (tx *Tx) Discard(rel string) *errs.Error {
	tmp := fsys.TempName(".")
	if e := tx.Move(rel, tmp); e != nil {
		return e
	}
	tx.root.RemoveAll(tmp)
	return nil
}

// HoldsOther reports whether folder f holds an entry that is neither hidden
// nor an empty .md named like a task's notes: one that may be the user's own
// data, which only a recursive delete-folder removes.
func (tx *Tx) HoldsOther(f model.FolderPath) (bool, *errs.Error) {
	rel := FolderRel(f)
	entries, err := tx.root.ReadDir(rel)
	if err != nil {
		return false, tx.OSError(rel, err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if notesFileName.MatchString(name) && e.Type().IsRegular() {
			if fi, err := tx.root.Lstat(joinPath(rel, name)); err == nil && fi.Mode().IsRegular() && fi.Size() == 0 {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}
