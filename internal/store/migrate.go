package store

import (
	"path"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
)

// LegacyMetaName is the root metadata file's name under ftask, koan's
// former name.
const LegacyMetaName = "ftask.json"

// Migrate moves what ftask, koan's former name, left to koan's names, once:
// its config into koan's config directory, then its root's ftask.json to
// koan.json. Each move happens only while koan's file is missing and
// ftask's is there, so a setup already koan's is never touched; and each
// is reported in w as a migrated warning. Anything Migrate cannot use — no
// config, a corrupt one, no root — it leaves for the operation to report.
// It fails only when a move it started fails.
func Migrate(env Env, w *errs.Collector) *errs.Error {
	if e := migrateConfig(env, w); e != nil {
		return e
	}
	return migrateMeta(env, w)
}

// migrateConfig moves ftask's config file to koan's config path, then
// removes ftask's config directory if that left it empty.
func migrateConfig(env Env, w *errs.Collector) *errs.Error {
	to := env.ConfigPath()
	if to == "" || env.LegacyConfigDir == "" {
		return nil
	}
	from := path.Join(env.LegacyConfigDir, ConfigName)
	if !missing(env.FS.Lstat(to)) || !present(env.FS.Lstat(from)) {
		return nil
	}
	if err := env.FS.MkdirAll(env.ConfigDir, FolderMode); err != nil {
		return errs.FromOS(env.ConfigDir, err)
	}
	if err := env.FS.Rename(from, to); err != nil {
		if isErrno(err, syscall.ENOENT) {
			return nil // another koan moved it first
		}
		return errs.FromOS(from, err)
	}
	env.FS.Remove(env.LegacyConfigDir) // best effort: a non-empty one stays
	w.Add(errs.Migrated(from, to))
	return nil
}

// migrateMeta renames the root's ftask.json to koan.json, holding the write
// lock so no write sees the root half migrated.
func migrateMeta(env Env, w *errs.Collector) *errs.Error {
	cs := readConfig(env)
	if cs.state != ConfigOK {
		return nil
	}
	root, ok := cs.cfg.raw.Expand(env.Home)
	if !ok {
		return nil
	}
	r, err := env.FS.OpenRoot(root)
	if err != nil {
		return nil
	}
	defer r.Close()
	if !needsMetaMove(r) {
		return nil
	}
	lock, e := takeLock(r, root, env.LockWait)
	if e != nil {
		return e
	}
	defer lock.Unlock()
	if !needsMetaMove(r) {
		return nil // another koan moved it while this one waited
	}
	if err := r.RenameNoReplace(LegacyMetaName, MetaName); err != nil {
		return errs.FromOS(joinPath(root, LegacyMetaName), err)
	}
	if err := r.SyncDir("."); err != nil {
		return errs.FromOS(root, err)
	}
	w.Add(errs.Migrated(joinPath(root, LegacyMetaName), joinPath(root, MetaName)))
	return nil
}

// needsMetaMove reports whether the root has ftask.json and no koan.json.
func needsMetaMove(r fsys.Root) bool {
	return missing(r.Lstat(MetaName)) && present(r.Lstat(LegacyMetaName))
}

func missing[T any](_ T, err error) bool { return isErrno(err, syscall.ENOENT) }

func present[T any](_ T, err error) bool { return err == nil }
