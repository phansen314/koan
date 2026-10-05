package store

import (
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
)

// LoadState is the outcome of loading a task file.
type LoadState int

const (
	// Usable: the file passed File validity; Loaded.Task holds it.
	Usable LoadState = iota
	// Vanished: the file was gone when read (ENOENT). It is treated as never
	// found: skipped silently in a read, not-found if it was the ID's only
	// file (implementation-spec.md, Concurrent writes during a read).
	Vanished
	// Unreadable: the file could not be read; Loaded.Err holds the OS error.
	Unreadable
	// Corrupt: the file failed File validity step 1 or 3; Loaded.Cause says
	// how, and what is wrong.
	Corrupt
	// Unsupported: the file's schema is not the supported version;
	// Loaded.Found holds it.
	Unsupported
)

// Loaded is a loaded task file: usable, or unusable with its reason.
type Loaded struct {
	Loc   Location
	State LoadState
	Task  model.TaskFile
	Err   error
	Cause errs.CorruptCause
	Found int64
}

// Load reads and checks the task file at l, once per operation: later calls
// return the cached result until NextStep.
func (tx *Tx) Load(l Location) *Loaded {
	if ld, ok := tx.cache[l]; ok {
		return ld
	}
	ld := tx.load(l)
	tx.cache[l] = ld
	return ld
}

func (tx *Tx) load(l Location) *Loaded {
	ld := &Loaded{Loc: l}
	data, err := tx.root.ReadFile(l.Rel())
	if err != nil {
		ld.State, ld.Err = Unreadable, err
		if isErrno(err, syscall.ENOENT) {
			ld.State = Vanished
		}
		return ld
	}
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		ld.State, ld.Cause = Corrupt, notJSON(err)
		return ld
	}
	t, res := model.DecodeTaskFile(obj, repeated, l.ID)
	switch res.Status {
	case model.FileOK:
		ld.Task = t
	case model.FileUnsupported:
		ld.State, ld.Found = Unsupported, res.Found
	default:
		ld.State, ld.Cause = Corrupt, invalid(res)
	}
	return ld
}

// notJSON is the cause of a file jsonio could not read, err its ReadError.
func notJSON(err error) errs.CorruptCause {
	return errs.CorruptCause{Reason: errs.CorruptNotJSON, Detail: err.Error()}
}

// invalid is the cause of a file that failed File validity (res).
func invalid(res model.FileResult) errs.CorruptCause {
	return errs.CorruptCause{Reason: errs.CorruptInvalid, Problems: res.Problems}
}

// Needed is the error a needed file's problem is (operations.md,
// Precedence): io when unreadable, corrupt, or unsupported-format. It is nil
// for a usable file and for one that vanished, which the caller treats as
// never found.
func (tx *Tx) Needed(ld *Loaded) *errs.Error {
	p := tx.Path(ld.Loc.Rel())
	switch ld.State {
	case Unreadable:
		return tx.OSError(ld.Loc.Rel(), ld.Err)
	case Corrupt:
		return errs.CorruptBy(p, ld.Cause)
	case Unsupported:
		return errs.UnsupportedFormat(p, ld.Found, []int64{model.TaskSchema})
	}
	return nil
}

// Relevant records the unusable-file warning a relevant file's problem is,
// if any. A vanished file is skipped silently. The error is internal only:
// an OS error with no symbolic name.
func (tx *Tx) Relevant(ld *Loaded) *errs.Error {
	rel := ld.Loc.Rel()
	p, id := tx.Path(rel), int64(ld.Loc.ID)
	switch ld.State {
	case Unreadable:
		code, e := tx.code(rel, ld.Err)
		if e != nil {
			return e
		}
		tx.Warn(errs.UnreadableFile(p, id, code))
	case Corrupt:
		tx.Warn(errs.CorruptFile(p, id))
	case Unsupported:
		tx.Warn(errs.UnsupportedFile(p, id))
	}
	return nil
}

// Task is a usable task file with where it lives, as operations return it.
func (tx *Tx) Task(ld *Loaded) model.Task {
	t := model.Task{TaskFile: ld.Task, Folder: ld.Loc.Folder, NotesPath: tx.Path(ld.Loc.NotesRel())}
	t.Normalize()
	return t
}
