package store

import (
	"fmt"
	"syscall"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// Tx is one operation's — or one composed command's — access to a usable
// root: every call goes through the one fsys.Root opened for it
// (implementation-spec.md, Filesystem access). It is not safe for concurrent
// use.
//
// Its index and task-file cache are built on first use and never see its own
// writes: Create, Replace, and Mkdir leave them as they were. A composed
// command therefore calls NextStep before each operation after the first, so
// that operation starts fresh and sees what the earlier ones wrote.
type Tx struct {
	root     fsys.Root
	rootPath string // the root as reported: as stored, cleaned, "~/" expanded
	env      Env
	meta     model.RootFile
	state    stateResult // the state file as last read or written
	write    bool
	survey   bool      // a diagnostic transaction: the walk records a Survey
	metaSt   metaState // as read under the lock, in a diagnostic transaction
	warn     *errs.Collector
	index    *Index
	cache    map[Location]*Loaded
	lock     fsys.Lock
	keepOld  bool // a migration transaction: the cache keeps the trees of older files
}

// Read runs fn over the root without the lock, after the Root states checks
// (operations.md, Precedence step 2). Warnings go to w.
func Read(env Env, w *errs.Collector, fn func(*Tx) *errs.Error) *errs.Error {
	tx, e := begin(env, w)
	if e != nil {
		return e
	}
	defer tx.root.Close()
	return fn(tx)
}

// Write runs fn holding the write lock: after the Root states checks, it
// takes the lock (see takeLock) and re-reads koan.json, since a write
// decides on current state. The lock is released when fn returns.
func Write(env Env, w *errs.Collector, fn func(*Tx) *errs.Error) *errs.Error {
	tx, e := begin(env, w)
	if e != nil {
		return e
	}
	defer tx.root.Close()
	lock, e := takeLock(tx.root, tx.rootPath, env.LockWait)
	if e != nil {
		return e
	}
	defer lock.Unlock()
	ms := readMeta(tx.root)
	if e := metaError(ms, tx.rootPath); e != nil {
		return e
	}
	tx.meta, tx.write = ms.meta, true
	if e := tx.readUsableState(); e != nil {
		return e
	}
	return fn(tx)
}

// Diagnose runs fn holding the write lock, for doctor and repair
// (implementation-spec.md, doctor and repair): after locating the config and
// opening the root, it takes the lock (see takeLock) and reads koan.json
// without failing on it — MetaState says what it found. The
// transaction allows writes, and its walk records a Survey.
func Diagnose(env Env, w *errs.Collector, fn func(*Tx) *errs.Error) *errs.Error {
	tx, e := locked(env, w)
	if e != nil {
		return e
	}
	defer tx.release()
	tx.survey = true
	return fn(tx)
}

// MigrateFormats runs fn holding the write lock, for migrate
// (implementation-spec.md, migrate): as Diagnose does, except that koan.json
// must be valid, in this binary's format or an older one, whatever step it
// records; a missing, unreadable, corrupt, or unknown one is migrate's own
// error, after busy. The cache keeps the ordered tree of a task file in an
// older format, and Tx.OldMeta that of an older koan.json, for the steps.
func MigrateFormats(env Env, w *errs.Collector, fn func(*Tx) *errs.Error) *errs.Error {
	tx, e := locked(env, w)
	if e != nil {
		return e
	}
	defer tx.release()
	if e := metaUnusable(tx.metaSt, tx.rootPath); e != nil {
		return e
	}
	tx.keepOld = true
	return fn(tx)
}

// locked is the start of a diagnostic or migration transaction: the config
// located and the root opened, the lock taken, and koan.json read (its state
// in metaSt) but not judged. The caller releases it.
func locked(env Env, w *errs.Collector) (*Tx, *errs.Error) {
	tx, e := open(env, w)
	if e != nil {
		return nil, e
	}
	lock, e := takeLock(tx.root, tx.rootPath, env.LockWait)
	if e != nil {
		tx.root.Close()
		return nil, e
	}
	tx.lock = lock
	tx.metaSt = readMeta(tx.root)
	tx.meta, tx.write = tx.metaSt.meta, true
	tx.state = readState(env, tx.rootPath)
	return tx, nil
}

// release unlocks and closes a transaction made by locked.
func (tx *Tx) release() {
	tx.lock.Unlock()
	tx.root.Close()
}

// lockPoll is how often a write retries a held write lock
// (implementation-spec.md, Mechanism).
const lockPoll = 10 * time.Millisecond

// takeLock takes r's write lock, retrying while it is held until wait has
// passed: busy if it is still held then. A wait of 0 tries once.
func takeLock(r fsys.Root, rootPath string, wait time.Duration) (fsys.Lock, *errs.Error) {
	deadline := time.Now().Add(wait)
	for {
		l, err := r.Lock()
		if err == nil {
			return l, nil
		}
		if !isErrno(err, syscall.EAGAIN) {
			return nil, errs.FromOS(rootPath, err)
		}
		if !time.Now().Before(deadline) {
			return nil, errs.Busy()
		}
		time.Sleep(min(lockPoll, time.Until(deadline)))
	}
}

// MetaState is koan.json's state as a diagnostic transaction read it, and
// the error an operation requiring a usable root would fail with, nil when
// it is ok. The error of a missing one is not-initialized (metadata).
func (tx *Tx) MetaState() (MetaState, *errs.Error) {
	return tx.metaSt.state, metaError(tx.metaSt, tx.rootPath)
}

// StateState is the state file's outcome as a diagnostic or migration
// transaction read it, whatever koan.json's state, and the error an
// operation requiring a usable root would fail with, nil when it is ok.
func (tx *Tx) StateState() (StateState, *errs.Error) {
	return tx.state.state, stateError(tx.env, tx.state)
}

// CreateMeta creates koan.json, which must not exist, with this binary's
// schema and the migration step its caller says to record: repair's rebuild
// of a lost one.
func (tx *Tx) CreateMeta(migration int64) *errs.Error {
	m := model.RootFile{Schema: model.RootSchema, Migration: migration}
	data, err := m.Encode()
	if err != nil {
		return errs.Internal("encode " + MetaName + ": " + err.Error())
	}
	if e := tx.Create(MetaName, data); e != nil {
		return e
	}
	tx.meta, tx.metaSt = m, metaState{state: MetaOK, meta: m}
	return nil
}

// CreateState writes the state file naming this root with lastID, replacing
// any there: repair's rebuild of a lost one, and migrate's move of a
// koan.json's last_id.
func (tx *Tx) CreateState(lastID int64) *errs.Error {
	return tx.writeState(lastID)
}

// LastID is the state file's last_id, the highest task ID issued. It is
// meaningful only when the state file is ok, as it is in every transaction
// that requires a usable root.
func (tx *Tx) LastID() int64 { return tx.state.file.LastID }

// RemoveAll removes the file or folder at rel and everything under it,
// returning the OS error.
func (tx *Tx) RemoveAll(rel string) error {
	if e := tx.mustWrite("remove " + rel); e != nil {
		return e
	}
	return tx.root.RemoveAll(rel)
}

// open locates the config and opens the root: the start of every
// transaction.
func open(env Env, w *errs.Collector) (*Tx, *errs.Error) {
	if w == nil {
		w = &errs.Collector{}
	}
	rootPath, e := locateRoot(env)
	if e != nil {
		return nil, e
	}
	r, e := openRoot(env, rootPath)
	if e != nil {
		return nil, e
	}
	return &Tx{root: r, rootPath: rootPath, env: env, warn: w, cache: map[Location]*Loaded{}}, nil
}

// readUsableState reads the state file, the last of the Root states checks,
// and returns its error: it is looked at only once koan.json is usable and
// current.
func (tx *Tx) readUsableState() *errs.Error {
	tx.state = readState(tx.env, tx.rootPath)
	return stateError(tx.env, tx.state)
}

func begin(env Env, w *errs.Collector) (*Tx, *errs.Error) {
	tx, e := open(env, w)
	if e != nil {
		return nil, e
	}
	ms := readMeta(tx.root)
	if e := metaError(ms, tx.rootPath); e != nil {
		tx.root.Close()
		return nil, e
	}
	tx.meta = ms.meta
	if e := tx.readUsableState(); e != nil {
		tx.root.Close()
		return nil, e
	}
	return tx, nil
}

// NextStep starts the next operation of a composed command: it drops the
// index and the task-file cache, which the previous operation's writes have
// made stale. The lock and the root stay as they are, and Meta stays current.
func (tx *Tx) NextStep() {
	tx.index = nil
	tx.cache = map[Location]*Loaded{}
}

// Meta is koan.json's content as last read or written.
func (tx *Tx) Meta() model.RootFile { return tx.meta }

// Path is the filesystem path koan reports for rel, a slash-separated path
// relative to the root ("." for the root itself): the root as stored, with
// "~/" expanded, joined with rel (design-spec.md, Root path).
func (tx *Tx) Path(rel string) string { return joinPath(tx.rootPath, rel) }

// Warn records a warning in the invocation's collector.
func (tx *Tx) Warn(w errs.Warning) { tx.warn.Add(w) }

// OSError is the io error for err, an OS error on rel, from a call site
// that gives its errno no other meaning.
func (tx *Tx) OSError(rel string, err error) *errs.Error {
	return errs.FromOS(tx.Path(rel), err)
}

// Code is err's symbolic OS error name, an OS error on rel, for a warning or
// a finding; an error with no name is internal.
func (tx *Tx) Code(rel string, err error) (string, *errs.Error) { return tx.code(rel, err) }

// code is err's symbolic OS error name, for a warning; an error with no name
// is internal.
func (tx *Tx) code(rel string, err error) (string, *errs.Error) {
	e := tx.OSError(rel, err)
	if d, ok := e.Details.(errs.IODetails); ok {
		return d.Code, nil
	}
	return "", e
}

func (tx *Tx) mustWrite(what string) *errs.Error {
	if !tx.write {
		return errs.Internal(fmt.Sprintf("%s outside a write", what))
	}
	return nil
}

func joinPath(dir, rel string) string {
	switch {
	case rel == ".":
		return dir
	case dir == "/":
		return "/" + rel
	case dir == ".":
		return rel
	}
	return dir + "/" + rel
}

// StatePath is the state file's path, which is outside the root.
func (tx *Tx) StatePath() string { return tx.env.StatePath() }
