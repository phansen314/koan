// Package store is koan's access to the tree and the state file: the root's
// state, the write lock, the walk, and reading and writing files
// (spec/design.md, Reading a tree and Concurrency).
package store

import (
	"path"
	"slices"
	"syscall"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// Env is everything a command takes from its surroundings.
type Env struct {
	Home     string           // the HOME directory, absolute
	Now      func() time.Time // the clock
	FS       fsys.FS
	LockWait time.Duration // how long a command waits for the lock
}

// DefaultLockWait is how long a command waits for the lock before busy.
const DefaultLockWait = 5 * time.Second

// Names and modes of what koan creates.
const (
	MetaName        = "@koan.json"
	FolderFileName  = "@folder.json"
	FolderNotesName = "@folder.md"
	StateName       = "state.json"

	FolderMode = 0o755
	FileMode   = 0o644
	StateMode  = 0o600
)

// RootPath is the tree's path.
func (e Env) RootPath() string { return path.Join(e.Home, "koan-root") }

// StateDir is the directory the state file is in.
func (e Env) StateDir() string { return path.Join(e.Home, ".config", "koan") }

// StatePath is the state file's path.
func (e Env) StatePath() string { return path.Join(e.StateDir(), StateName) }

// Status is the root's state.
type Status string

const (
	NotInitialized Status = "not-initialized"
	Unusable       Status = "unusable"
	NeedsMigration Status = "needs-migration"
	TooNew         Status = "too-new"
	Usable         Status = "usable"
)

// Parts of a root that can be missing.
const (
	PartRoot  = "root"
	PartMeta  = "metadata"
	PartState = "state"
)

// Condition is what Inspect finds: the root's state, and the error commands
// that need a usable root fail with.
type Condition struct {
	Status  Status
	Missing []string     // when not initialized
	Meta    *model.Meta  // nil when it can't be read
	State   *model.State // nil when it can't be read
	Err     *errs.Error  // nil when usable
}

// Inspect reports the root's state without failing on it. root may be nil
// when the root directory could not be opened, with err the reason.
func Inspect(env Env, root fsys.Root, openErr error) Condition {
	c := Condition{Missing: []string{}}
	var bad *errs.Error
	switch {
	case openErr == nil:
		c.Meta, bad = readMeta(env, root)
		if c.Meta == nil && bad == nil {
			c.Missing = append(c.Missing, PartMeta)
		}
	case errs.IsErrno(openErr, syscall.ENOENT):
		c.Missing = append(c.Missing, PartRoot, PartMeta)
	case errs.IsErrno(openErr, syscall.ENOTDIR):
		bad = errs.NewConflict(errs.RuleNotAFolder, "%s is not a folder", env.RootPath()).WithPaths(env.RootPath())
	default:
		bad = errs.FromOS(env.RootPath(), openErr)
	}
	st, stErr := readState(env)
	if st == nil && stErr == nil {
		c.Missing = append(c.Missing, PartState)
	}
	c.State = st
	if bad == nil {
		bad = stErr
	}
	switch {
	case bad != nil:
		c.Status, c.Err = Unusable, bad
	case len(c.Missing) > 0:
		c.Status = NotInitialized
		c.Err = errs.New(errs.NotInitialized, "koan is not set up here (missing: %v): run `koan init`", c.Missing)
	case c.Meta.Schema < model.Format:
		c.Status = NeedsMigration
		c.Err = errs.New(errs.MigrationPending, "the tree is in format %d and this koan uses %d: run `koan migrate`", c.Meta.Schema, model.Format)
	case c.Meta.Schema > model.Format:
		c.Status = TooNew
		c.Err = errs.New(errs.UnsupportedFormat, "the tree is in format %d, newer than this koan's %d", c.Meta.Schema, model.Format)
	default:
		c.Status = Usable
	}
	return c
}

// readMeta reads @koan.json. Both results are nil when it is missing.
func readMeta(env Env, root fsys.Root) (*model.Meta, *errs.Error) {
	p := path.Join(env.RootPath(), MetaName)
	data, err := root.ReadFile(MetaName)
	if errs.IsErrno(err, syscall.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, unreadable(p, err)
	}
	m, problems, err := model.DecodeMeta(data)
	if e := corrupt(p, problems, err); e != nil {
		return nil, e
	}
	return m, nil
}

// readState reads the state file. Both results are nil when it is missing.
func readState(env Env) (*model.State, *errs.Error) {
	p := env.StatePath()
	dir, err := env.FS.OpenRoot(env.StateDir())
	if errs.IsErrno(err, syscall.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.FromOS(env.StateDir(), err)
	}
	defer dir.Close()
	data, err := dir.ReadFile(StateName)
	if errs.IsErrno(err, syscall.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, unreadable(p, err)
	}
	s, problems, err := model.DecodeState(data)
	if e := corrupt(p, problems, err); e != nil {
		return nil, e
	}
	return s, nil
}

// unreadable is the error for a file that is there but can't be read: a
// symlink or a directory in its place is corrupt, anything else io.
func unreadable(p string, err error) *errs.Error {
	if errs.IsErrno(err, syscall.ELOOP, syscall.EISDIR) {
		return errs.New(errs.Corrupt, "%s is not a regular file", p).WithPaths(p)
	}
	return errs.FromOS(p, err)
}

// corrupt is the error for a file that was read but can't be used, nil when
// it can.
func corrupt(p string, problems []model.Problem, err error) *errs.Error {
	switch {
	case err != nil:
		return errs.New(errs.Corrupt, "%s is not valid JSON: %v", p, err).WithPaths(p)
	case len(problems) > 0:
		return errs.New(errs.Corrupt, "%s is invalid: %s %s", p, problems[0].Field, problems[0].Reason).
			WithPaths(p).WithProblems(problems...)
	}
	return nil
}

// Need says what a command needs of the root.
type Need int

const (
	NeedUsable      Need = iota // a usable root
	NeedInitialized             // usable, or needing migration: doctor and migrate
)

// Tree is an open tree: the handle every file call goes through, the state
// file as read, and, for a write, the held lock.
type Tree struct {
	env     Env
	root    fsys.Root
	lock    fsys.Lock
	Meta    *model.Meta
	State   *model.State
	changed []string
	files   map[model.ID]*Loaded
}

// Open opens the tree for a command and takes its lock: exclusive for a
// write, shared for a read, so that a read sees the tree between writes and
// never part of one. The root's state is checked before the lock, so that a
// root that is not usable is reported as that and not as busy, and read
// again under it, so that the command decides on current state. The caller
// closes the tree.
func Open(env Env, need Need, write bool) (*Tree, *errs.Error) {
	root, err := env.FS.OpenRoot(env.RootPath())
	c := Inspect(env, root, err)
	if e := need.check(c); e != nil {
		if root != nil {
			root.Close()
		}
		return nil, e
	}
	t := &Tree{env: env, root: root, files: map[model.ID]*Loaded{}}
	if e := t.takeLock(!write); e != nil {
		root.Close()
		return nil, e
	}
	c = Inspect(env, root, nil)
	if e := need.check(c); e != nil {
		t.Close()
		return nil, e
	}
	t.Meta, t.State = c.Meta, c.State
	return t, nil
}

func (n Need) check(c Condition) *errs.Error {
	if c.Status == Usable || (n == NeedInitialized && c.Status == NeedsMigration) {
		return nil
	}
	return c.Err
}

// lockPoll is how often a command retries a held lock.
const lockPoll = 10 * time.Millisecond

// takeLock takes the lock, retrying while it is held until the wait has
// passed: busy if it is still held then.
func (t *Tree) takeLock(shared bool) *errs.Error {
	deadline := time.Now().Add(t.env.LockWait)
	for {
		l, err := t.root.Lock(shared)
		if err == nil {
			t.lock = l
			return nil
		}
		if !errs.IsErrno(err, syscall.EAGAIN) {
			return errs.FromOS(t.env.RootPath(), err)
		}
		if !time.Now().Before(deadline) {
			return errs.New(errs.Busy, "another koan command held the lock for %v", t.env.LockWait)
		}
		time.Sleep(min(lockPoll, time.Until(deadline)))
	}
}

// OpenLocked opens the root directory and takes the lock without asking
// anything of the root's state: init's way in.
func OpenLocked(env Env) (*Tree, *errs.Error) {
	root, err := env.FS.OpenRoot(env.RootPath())
	if err != nil {
		if errs.IsErrno(err, syscall.ENOTDIR, syscall.ENOENT) {
			return nil, errs.NewConflict(errs.RuleNotAFolder, "%s is not a folder", env.RootPath()).WithPaths(env.RootPath())
		}
		return nil, errs.FromOS(env.RootPath(), err)
	}
	t := &Tree{env: env, root: root, files: map[model.ID]*Loaded{}}
	if e := t.takeLock(false); e != nil {
		root.Close()
		return nil, e
	}
	return t, nil
}

// Close releases the lock, if held, and the handle.
func (t *Tree) Close() {
	if t.lock != nil {
		t.lock.Unlock()
	}
	t.root.Close()
}

// Env is the environment the tree was opened with.
func (t *Tree) Env() Env { return t.env }

// Inspect reports the open root's state.
func (t *Tree) Inspect() Condition { return Inspect(t.env, t.root, nil) }

// Touched records a change made outside the tree's own methods: the root
// directory itself, which init creates before there is a tree to open.
func (t *Tree) Touched(rel string) { t.note(rel) }

// Changed lists what the command has created, rewritten, or removed so far.
func (t *Tree) Changed() []string { return t.changed }

func (t *Tree) note(rel string) { t.noteAbs(t.Abs(rel)) }

// noteAbs records a change to path p, which may be outside the tree.
func (t *Tree) noteAbs(p string) {
	if !slices.Contains(t.changed, p) {
		t.changed = append(t.changed, p)
	}
}

// Abs is the filesystem path of a path relative to the root.
func (t *Tree) Abs(rel string) string { return path.Join(t.env.RootPath(), rel) }

// TaskRel and NotesRel are the root-relative paths of a task's two files.
func TaskRel(f model.Folder, id model.ID) string {
	return path.Join(f.Rel(), model.Mark+model.ClaimKey(id)+model.TaskSuffix)
}

func NotesRel(f model.Folder, id model.ID) string {
	return path.Join(f.Rel(), model.Mark+model.ClaimKey(id)+model.NotesSuffix)
}

// FolderFileRel and FolderNotesRel are the root-relative paths of a
// folder's two files.
func FolderFileRel(f model.Folder) string { return path.Join(f.Rel(), FolderFileName) }

func FolderNotesRel(f model.Folder) string { return path.Join(f.Rel(), FolderNotesName) }

// Claim is the task's claim, nil when it has none.
func (t *Tree) Claim(id model.ID) *model.Claim {
	if c, ok := t.State.Claims[model.ClaimKey(id)]; ok {
		return &c
	}
	return nil
}
