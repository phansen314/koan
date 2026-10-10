package store

import (
	"path"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/migrations"
	"github.com/phansen314/koan/internal/model"
)

// InitAction is what init did with the tree.
type InitAction string

const (
	InitCreated  InitAction = "created"
	InitAttached InitAction = "attached"
)

// InitResult is what init did. RootCreated and MetaCreated are set as each
// piece is created, so they hold on an error too: init's partial result.
type InitResult struct {
	Action       InitAction
	LastID       int64
	RootCreated  bool
	MetaCreated  bool
	StateCreated bool
}

// Init creates a new tree at root, or attaches the one there, and writes the
// config naming it (implementation-spec.md, init), in init's own precedence
// order. root is absolute and cleaned, with no ".." segment. The config is
// written last, so until it exists nothing else uses the root.
func Init(env Env, root string, replace bool) (InitResult, *errs.Error) {
	var res InitResult
	exists, e := rootExists(env, root)
	if e != nil {
		return res, e
	}
	if env.ConfigDir == "" {
		return res, errs.Environment("HOME")
	}
	cfgExists, e := configExists(env)
	if e != nil {
		return res, e
	}
	if cfgExists && !replace {
		// A crash after the config was published can leave its temp file:
		// remove it here too, or nothing ever would.
		if r, err := env.FS.OpenRoot(env.ConfigDir); err == nil {
			removeTemps(r)
			r.Close()
		}
		return res, errs.Conflict(errs.RuleConfigExists, nil)
	}
	if !exists {
		if err := env.FS.Mkdir(root, FolderMode); err != nil {
			if isErrno(err, syscall.ENOENT) || isErrno(err, syscall.ENOTDIR) {
				return res, errs.NotFound(nil, nil, []string{path.Dir(root)})
			}
			return res, errs.FromOS(root, err)
		}
		res.RootCreated = true
	}
	if e := initTree(env, root, exists, cfgExists, &res); e != nil {
		return res, e
	}
	return res, nil
}

// rootExists reports whether anything is at root; if so it must lead,
// through symlinks, to a directory, or root is invalid input. A missing
// ancestor, or one that is not a directory, counts as nothing there: Mkdir
// then reports the parent missing.
func rootExists(env Env, root string) (bool, *errs.Error) {
	fi, err := env.FS.Lstat(root)
	switch {
	case isErrno(err, syscall.ENOENT), isErrno(err, syscall.ENOTDIR):
		return false, nil
	case err != nil:
		return false, errs.FromOS(root, err)
	case fi.IsDir():
		return true, nil
	}
	fi, err = env.FS.Stat(root)
	switch {
	case err == nil && fi.IsDir():
		return true, nil
	case err != nil && !isErrno(err, syscall.ENOENT) && !isErrno(err, syscall.ENOTDIR) && !isErrno(err, syscall.ELOOP):
		return false, errs.FromOS(root, err)
	}
	return false, errs.InvalidInput([]errs.Problem{{Field: "/root", Reason: "must lead to a directory: something else is there"}})
}

// configExists reports whether anything is at the config's path; init never
// reads it. Only ENOENT means no config, as for every other reader of it: a
// file where the config directory should be (ENOTDIR) is io, before init
// creates anything.
func configExists(env Env) (bool, *errs.Error) {
	p := env.ConfigPath()
	_, err := env.FS.Lstat(p)
	switch {
	case err == nil:
		return true, nil
	case isErrno(err, syscall.ENOENT):
		return false, nil
	}
	return false, errs.FromOS(p, err)
}

// initTree attaches the tree at root if it has koan.json, checked as File
// validity says; otherwise, if root is empty but for hidden entries, it
// creates koan.json for a new tree. Then it writes the state file, and last
// the config (implementation-spec.md, init). existed: the root was there
// before this init, so the state file is read and written under its lock.
func initTree(env Env, root string, existed, replace bool, res *InitResult) *errs.Error {
	r, err := env.FS.OpenRoot(root)
	if err != nil {
		return errs.FromOS(root, err)
	}
	defer r.Close()
	ms := readMeta(r)
	res.Action = InitAttached
	var last int64 // the highest of the sources of the state file's last_id
	switch ms.state {
	case MetaOK, MetaOldFormat: // attached whatever migrations are pending
		if ms.oldLastID != nil {
			last = *ms.oldLastID
		}
	case MetaMissing:
		if e := createMeta(r, root, res); e != nil {
			return e
		}
	default:
		return metaUnusable(ms, root)
	}
	if res.Action == InitAttached {
		// The highest ID in any task filename: names only, no task file read.
		tx := &Tx{root: r, rootPath: root, cache: map[Location]*Loaded{}}
		x := tx.Index()
		if e := tx.RequireWholeTree(x); e != nil {
			return e
		}
		for _, l := range x.Tasks {
			last = max(last, int64(l.ID))
		}
	}
	if existed {
		lock, e := takeLock(r, root, env.LockWait)
		if e != nil {
			return e
		}
		defer lock.Unlock()
	}
	if err := env.FS.MkdirAll(env.ConfigDir, FolderMode); err != nil {
		return errs.FromOS(env.ConfigDir, err)
	}
	// The counter for a root never goes down: a usable state file already
	// naming it is a floor; any other is replaced.
	if sr := readState(env, root); sr.state == StateOK {
		last = max(last, sr.file.LastID)
	}
	if e := writeState(env, root, last); e != nil {
		return e
	}
	res.StateCreated, res.LastID = true, last
	return writeConfig(env, root, replace)
}

// createMeta creates koan.json for a new tree: root is empty but for hidden
// entries, or was just made.
func createMeta(r fsys.Root, root string, res *InitResult) *errs.Error {
	if !res.RootCreated {
		entries, err := r.ReadDir(".")
		if err != nil {
			return errs.FromOS(root, err)
		}
		for _, en := range entries {
			if !strings.HasPrefix(en.Name(), ".") {
				return errs.Conflict(errs.RuleRootNotEmpty, nil)
			}
		}
	}
	data, err := model.RootFile{Schema: model.RootSchema, Migration: migrations.Latest()}.Encode()
	if err != nil {
		return errs.Internal("encoding koan.json: " + err.Error())
	}
	p := joinPath(root, MetaName)
	if atLink, err := Publish(r, MetaName, data, false); err != nil {
		if atLink && isErrno(err, syscall.EEXIST) {
			return errs.Corrupt(p, errs.CorruptUnexpectedFile)
		}
		return errs.FromOS(p, err)
	}
	res.Action, res.MetaCreated = InitCreated, true
	return nil
}

// writeConfig writes the config naming root: the config directory created
// as needed, temp files an interrupted init left there removed, then the
// config published by link, or by rename over the existing one.
func writeConfig(env Env, root string, replace bool) *errs.Error {
	if err := env.FS.MkdirAll(env.ConfigDir, FolderMode); err != nil {
		return errs.FromOS(env.ConfigDir, err)
	}
	r, err := env.FS.OpenRoot(env.ConfigDir)
	if err != nil {
		return errs.FromOS(env.ConfigDir, err)
	}
	defer r.Close()
	if err := removeTemps(r); err != nil {
		return errs.FromOS(env.ConfigDir, err)
	}
	if atLink, err := Publish(r, ConfigName, EncodeConfig(root), replace); err != nil {
		if atLink && isErrno(err, syscall.EEXIST) {
			return errs.Conflict(errs.RuleConfigExists, nil) // written since the check
		}
		return errs.FromOS(env.ConfigPath(), err)
	}
	return nil
}

// removeTemps removes the temp files an interrupted init left in the config
// directory r. Removing is best effort: the next init tries again.
func removeTemps(r fsys.Root) error {
	entries, err := r.ReadDir(".")
	if err != nil {
		return err
	}
	for _, en := range entries {
		if fsys.IsTemp(en.Name()) {
			r.Remove(en.Name())
		}
	}
	return nil
}
