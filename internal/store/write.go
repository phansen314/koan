package store

import (
	"path"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
)

// FolderMode is the mode of every folder koan creates, before the umask.
const FolderMode = 0o755

// Publish writes data to rel, a path within r, through a temp file in rel's
// directory (implementation-spec.md, Writing files): created exclusively,
// written in full and flushed to disk, then published — by rename when
// replace is set, else by link, which never clobbers an existing file — and
// the directory flushed, so the file survives a system crash whole and before
// any later step. The temp file is then removed. Failing to flush the
// directory or to remove the temp file is not an error: the file is already
// published (doctor finds leftovers). Any other failure returns the OS error,
// with nothing published; atLink reports that it came from link.
func Publish(r fsys.Root, rel string, data []byte, replace bool) (atLink bool, err error) {
	f, tmp, err := r.CreateTemp(path.Dir(rel))
	if err != nil {
		return false, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		r.Remove(tmp)
		return false, err
	}
	if replace {
		if err := r.Rename(tmp, rel); err != nil {
			r.Remove(tmp)
			return false, err
		}
		r.SyncDir(path.Dir(rel))
		return false, nil
	}
	if err := r.Link(tmp, rel); err != nil {
		r.Remove(tmp)
		return true, err
	}
	r.SyncDir(path.Dir(rel))
	r.Remove(tmp)
	return false, nil
}

// Create publishes a new file at rel. A file already there is corrupt
// (unexpected-file): under koan's invariants none can exist.
func (tx *Tx) Create(rel string, data []byte) *errs.Error {
	if e := tx.mustWrite("create " + rel); e != nil {
		return e
	}
	atLink, err := Publish(tx.root, rel, data, false)
	switch {
	case err == nil:
		return nil
	case atLink && isErrno(err, syscall.EEXIST):
		return errs.Corrupt(tx.Path(rel), errs.CorruptUnexpectedFile)
	}
	return tx.OSError(rel, err)
}

// Replace publishes rel over any file already there.
func (tx *Tx) Replace(rel string, data []byte) *errs.Error {
	if e := tx.mustWrite("replace " + rel); e != nil {
		return e
	}
	if _, err := Publish(tx.root, rel, data, true); err != nil {
		return tx.OSError(rel, err)
	}
	return nil
}

// ReplaceRaw is Replace returning the OS error, for a call site that gives
// every error its own meaning (create's .md: a notes-missing warning).
func (tx *Tx) ReplaceRaw(rel string, data []byte) error {
	if e := tx.mustWrite("replace " + rel); e != nil {
		return e
	}
	_, err := Publish(tx.root, rel, data, true)
	return err
}

// NotesMissing records the notes-missing warning for task id, whose .md at
// rel could not be written because of err. An OS error with no symbolic name
// still gets the warning, without code: the task exists, so the write can't
// fail, and the notes must not be lost silently.
func (tx *Tx) NotesMissing(rel string, id model.ID, err error) {
	code, _ := tx.code(rel, err)
	tx.Warn(errs.NotesMissing(tx.Path(rel), int64(id), code))
}

// Mkdir creates the folder rel, returning the OS error: whether EEXIST is an
// error is the caller's call.
func (tx *Tx) Mkdir(rel string) error {
	if e := tx.mustWrite("mkdir " + rel); e != nil {
		return e
	}
	return tx.root.Mkdir(rel, FolderMode)
}

// SetLastID records lastID in the state file, while the root's lock is held.
// Before writing it removes any temp file an
// interrupted write left in the config directory.
func (tx *Tx) SetLastID(lastID int64) *errs.Error {
	return tx.writeState(lastID)
}

func (tx *Tx) writeState(lastID int64) *errs.Error {
	if e := tx.mustWrite("write " + StateName); e != nil {
		return e
	}
	if e := writeState(tx.env, tx.rootPath, lastID); e != nil {
		return e
	}
	tx.state = stateResult{state: StateOK, file: model.StateFile{Schema: model.StateSchema, Root: tx.rootPath, LastID: lastID}}
	return nil
}

// SetMeta replaces koan.json with m, whole: migrate's last write.
func (tx *Tx) SetMeta(m model.RootFile) *errs.Error {
	data, err := m.Encode()
	if err != nil {
		return errs.Internal("encode " + MetaName + ": " + err.Error())
	}
	if e := tx.Replace(MetaName, data); e != nil {
		return e
	}
	tx.meta = m
	tx.metaSt = metaState{state: MetaOK, meta: m}
	return nil
}

// OldMeta is the ordered tree of koan.json when it is in an older format,
// with its schema, in a migration transaction; ok is false otherwise.
func (tx *Tx) OldMeta() (obj *jsonio.Object, schema int64, ok bool) {
	if tx.metaSt.state != MetaOldFormat {
		return nil, 0, false
	}
	return tx.metaSt.old, tx.metaSt.meta.Schema, true
}

// OldLastID is the last_id a koan.json in an older format holds, in a
// migration transaction; ok is false when koan.json holds none.
func (tx *Tx) OldLastID() (last int64, ok bool) {
	if tx.metaSt.state != MetaOldFormat || tx.metaSt.oldLastID == nil {
		return 0, false
	}
	return *tx.metaSt.oldLastID, true
}
