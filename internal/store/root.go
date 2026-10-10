package store

import (
	"encoding/json"
	"strconv"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/migrations"
	"github.com/phansen314/koan/internal/model"
)

// MetaName is the root metadata file's name, at the root.
const MetaName = "koan.json"

// config is what a readable, parseable config names.
type config struct {
	path string
	raw  RootPath
}

// configState is the outcome of reading the config: its state as info
// reports it, the OS error of an unreadable one, and what is wrong with a
// corrupt one.
type configState struct {
	state ConfigState
	cfg   config
	err   error
	cause errs.CorruptCause // ConfigCorrupt: what is wrong
}

// readConfig reads the config, which must be a regular file or a symlink to
// one. Anything else is corrupt (unexpected-file), and never read: a FIFO
// would block every command. A FIFO swapped in between the Stat and the
// read can still block; that takes an outside change, and no lock is held
// yet.
func readConfig(env Env) configState {
	p := env.ConfigPath()
	if p == "" {
		return configState{state: ConfigMissing}
	}
	cs := configState{cfg: config{path: p}}
	fi, err := env.FS.Stat(p)
	var data []byte
	if err == nil && !fi.Mode().IsRegular() {
		cs.state, cs.cause = ConfigCorrupt, errs.CorruptCause{Reason: errs.CorruptUnexpectedFile}
		return cs
	}
	if err == nil {
		data, err = env.FS.ReadFile(p)
	}
	if err != nil {
		cs.state, cs.err = ConfigUnreadable, err
		if isErrno(err, syscall.ENOENT) {
			cs.state = ConfigMissing
		}
		return cs
	}
	raw, err := parseConfig(data)
	if err == nil {
		cs.cfg.raw, err = ParseRootPath(raw)
	}
	if err != nil {
		cs.state, cs.cause = ConfigCorrupt, errs.CorruptCause{Reason: errs.CorruptInvalid, Detail: err.Error()}
		return cs
	}
	cs.state = ConfigOK
	return cs
}

// locateRoot runs Root states up to the root path: the config located, read,
// and parsed, and its root expanded. It returns the root as reported.
func locateRoot(env Env) (string, *errs.Error) {
	if env.ConfigDir == "" {
		return "", errs.Environment("HOME")
	}
	cs := readConfig(env)
	switch cs.state {
	case ConfigMissing:
		return "", errs.NotInitialized(errs.MissingConfig)
	case ConfigUnreadable:
		return "", errs.FromOS(cs.cfg.path, cs.err)
	case ConfigCorrupt:
		return "", errs.CorruptBy(cs.cfg.path, cs.cause)
	}
	root, ok := cs.cfg.raw.Expand(env.Home)
	if !ok {
		return "", errs.NotInitialized(errs.MissingRoot)
	}
	return root, nil
}

func openRoot(env Env, root string) (fsys.Root, *errs.Error) {
	r, err := env.FS.OpenRoot(root)
	if err != nil {
		if rootMissing(err) {
			return nil, errs.NotInitialized(errs.MissingRoot)
		}
		return nil, errs.FromOS(root, err)
	}
	return r, nil
}

// rootMissing reports whether err, from opening the root, means the root
// path leads to no directory: nothing there, something other than a
// directory, or a symlink loop (design-spec.md, Resolving the root).
func rootMissing(err error) bool {
	return isErrno(err, syscall.ENOENT) || isErrno(err, syscall.ENOTDIR) || isErrno(err, syscall.ELOOP)
}

// metaState is the outcome of reading koan.json: its state as info reports
// it, with what each state carries.
type metaState struct {
	state MetaState
	err   error             // MetaUnreadable: the OS error
	cause errs.CorruptCause // MetaCorrupt
	file  model.FileResult  // after a parse: the File validity result
	meta  model.RootFile    // MetaOK, MetaOldFormat; also MetaUnsupported when pastLatest
	// pastLatest: koan.json passed every File validity step but records a
	// migration step past this binary's latest, which makes it MetaUnsupported.
	pastLatest bool
	// old is the ordered tree of a koan.json in an older format (MetaOldFormat),
	// for the steps to convert.
	old *jsonio.Object
	// oldLastID is the last_id an older koan.json holds, when it holds one.
	oldLastID *int64
}

// pending reports whether koan.json is valid but behind: in an older format,
// or recording a step below this binary's latest (Root states, needs
// migration).
func (ms metaState) pending() bool {
	return ms.state == MetaOldFormat || ms.state == MetaOK && ms.meta.Migration < migrations.Latest()
}

// readMeta reads koan.json through r. It must be a regular file: a symlink,
// a directory, or anything else in its place is corrupt (unexpected-file).
func readMeta(r fsys.Root) metaState {
	fi, err := r.Lstat(MetaName)
	if err != nil {
		return metaReadError(err)
	}
	if !fi.Mode().IsRegular() {
		return metaState{state: MetaCorrupt, cause: unexpectedFile}
	}
	data, err := r.ReadFile(MetaName)
	if err != nil {
		return metaReadError(err)
	}
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		return metaState{state: MetaCorrupt, cause: notJSON(err)}
	}
	meta, res := model.DecodeRootFile(obj, repeated)
	ms := metaState{file: res, meta: meta}
	switch res.Status {
	case model.FileOK:
		ms.state = MetaOK
		if meta.Migration > migrations.Latest() {
			ms.state, ms.pastLatest = MetaUnsupported, true
		}
	case model.FileUnsupported:
		ms.state = MetaUnsupported
		if migrations.Reads(migrations.Root, res.Found) {
			// An older format is checked against its own rules (File
			// validity, step 2): breaking them is corrupt.
			if ps := migrations.Check(migrations.Root, res.Found, obj, repeated); len(ps) > 0 {
				ms.file.Status, ms.file.Problems = model.FileCorrupt, ps
				ms.state, ms.cause = MetaCorrupt, invalid(ms.file)
			} else {
				ms.state, ms.meta, ms.old = MetaOldFormat, oldRoot(obj, res.Found), obj
				if n, ok := obj.Get("last_id"); ok {
					last := numberOr0(n)
					ms.oldLastID = &last
				}
			}
		}
	default:
		ms.state, ms.cause = MetaCorrupt, invalid(res)
	}
	return ms
}

// oldRoot is the content of a koan.json in an older format that passed that
// format's rules: its schema and the step it records, 0 when
// the format has none (schema 1 predates migrations).
func oldRoot(obj *jsonio.Object, schema int64) model.RootFile {
	m := model.RootFile{Schema: schema}
	if n, ok := obj.Get("migration"); ok {
		m.Migration = numberOr0(n)
	}
	return m
}

func numberOr0(v any) int64 {
	n, ok := v.(json.Number)
	if !ok {
		return 0
	}
	i, _ := strconv.ParseInt(string(n), 10, 64)
	return i
}

// unexpectedFile is the cause of a koan.json that is not a regular file.
var unexpectedFile = errs.CorruptCause{Reason: errs.CorruptUnexpectedFile}

func metaReadError(err error) metaState {
	switch {
	case isErrno(err, syscall.ENOENT):
		return metaState{state: MetaMissing}
	case isErrno(err, syscall.ELOOP), isErrno(err, syscall.EISDIR):
		return metaState{state: MetaCorrupt, cause: unexpectedFile}
	}
	return metaState{state: MetaUnreadable, err: err}
}

// metaError turns koan.json's state into the Root states error, nil when the
// root is usable: an unusable koan.json, or one that needs migration, which
// is last (operations.md, Precedence).
func metaError(ms metaState, root string) *errs.Error {
	if e := metaUnusable(ms, root); e != nil {
		return e
	}
	if ms.pending() {
		return errs.MigrationPending(ms.meta.Migration, migrations.Latest())
	}
	return nil
}

// metaUnusable is the Root states error of a koan.json that is missing or
// unusable; nil for one that is valid, whether or not it is behind.
func metaUnusable(ms metaState, root string) *errs.Error {
	p := joinPath(root, MetaName)
	switch ms.state {
	case MetaOK, MetaOldFormat:
		return nil
	case MetaMissing:
		return errs.NotInitialized(errs.MissingMetadata)
	case MetaUnreadable:
		return errs.FromOS(p, ms.err)
	case MetaUnsupported:
		if ms.pastLatest {
			return errs.UnsupportedMigration(p, ms.meta.Migration, migrations.Latest())
		}
		return errs.UnsupportedFormat(p, ms.file.Found, []int64{model.RootSchema})
	}
	return errs.CorruptBy(p, ms.cause)
}

func isErrno(err error, want syscall.Errno) bool {
	e, ok := errs.ErrnoOf(err)
	return ok && e == want
}
