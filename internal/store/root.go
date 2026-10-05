package store

import (
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
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
	meta  model.RootFile    // MetaOK
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
	case model.FileUnsupported:
		ms.state = MetaUnsupported
	default:
		ms.state, ms.cause = MetaCorrupt, invalid(res)
	}
	return ms
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

// metaError turns an unusable koan.json into the Root states error.
func metaError(ms metaState, root string) *errs.Error {
	p := joinPath(root, MetaName)
	switch ms.state {
	case MetaOK:
		return nil
	case MetaMissing:
		return errs.NotInitialized(errs.MissingMetadata)
	case MetaUnreadable:
		return errs.FromOS(p, ms.err)
	case MetaUnsupported:
		return errs.UnsupportedFormat(p, ms.file.Found, []int64{model.RootSchema})
	}
	return errs.CorruptBy(p, ms.cause)
}

func isErrno(err error, want syscall.Errno) bool {
	e, ok := errs.ErrnoOf(err)
	return ok && e == want
}
