package store

import (
	"path"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
)

// StateName is the state file's name in the config directory.
const StateName = "state.json"

// StatePath is the state file's path; "" when the config directory is
// unknown.
func (e Env) StatePath() string {
	if e.ConfigDir == "" {
		return ""
	}
	return path.Join(e.ConfigDir, StateName)
}

// StateState is the state file's outcome, as info reports it
// (implementation-spec.md, State file).
type StateState string

const (
	StateMissing     StateState = "missing"
	StateOtherRoot   StateState = "other-root"
	StateUnreadable  StateState = "unreadable"
	StateCorrupt     StateState = "corrupt"
	StateUnsupported StateState = "unsupported-format"
	StateOK          StateState = "ok"
)

// stateResult is what reading the state file for a root found.
type stateResult struct {
	state StateState
	err   error             // StateUnreadable: the OS error
	cause errs.CorruptCause // StateCorrupt
	found int64             // StateUnsupported: its schema
	file  model.StateFile   // StateOK, StateOtherRoot
}

// readState reads the state file by path, outside the root, as a regular
// file: anything else in its place is corrupt (unexpected-file). root is the
// config's root, cleaned and with ~/ expanded: a valid file naming any other
// is other-root, which for this root is as good as missing.
func readState(env Env, root string) stateResult {
	p := env.StatePath()
	if p == "" {
		return stateResult{state: StateMissing}
	}
	fi, err := env.FS.Lstat(p)
	if err != nil {
		return stateReadError(err)
	}
	if !fi.Mode().IsRegular() {
		return stateResult{state: StateCorrupt, cause: unexpectedFile}
	}
	data, err := env.FS.ReadFile(p)
	if err != nil {
		return stateReadError(err)
	}
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		return stateResult{state: StateCorrupt, cause: notJSON(err)}
	}
	file, res := model.DecodeStateFile(obj, repeated)
	switch res.Status {
	case model.FileUnsupported:
		return stateResult{state: StateUnsupported, found: res.Found}
	case model.FileCorrupt:
		return stateResult{state: StateCorrupt, cause: invalid(res)}
	}
	if file.Root != root {
		return stateResult{state: StateOtherRoot, file: file}
	}
	return stateResult{state: StateOK, file: file}
}

func stateReadError(err error) stateResult {
	if isErrno(err, syscall.ENOENT) {
		return stateResult{state: StateMissing}
	}
	return stateResult{state: StateUnreadable, err: err}
}

// stateError is the Root states error of a state file that is not usable for
// this root, nil for one that is: not-initialized (state) when it is missing
// or names another root, else io, corrupt, or unsupported-format.
func stateError(env Env, sr stateResult) *errs.Error {
	p := env.StatePath()
	switch sr.state {
	case StateOK:
		return nil
	case StateMissing, StateOtherRoot:
		return errs.NotInitialized(errs.MissingState)
	case StateUnreadable:
		return errs.FromOS(p, sr.err)
	case StateUnsupported:
		return errs.UnsupportedFormat(p, sr.found, []int64{model.StateSchema})
	}
	return errs.CorruptBy(p, sr.cause)
}

// writeState publishes the state file naming root with lastID: a temp file in
// the config directory, flushed, then renamed over any file there, and the
// directory flushed. Any temp file an interrupted write left there is removed
// first. The caller holds the root's lock (init creating a new tree apart).
func writeState(env Env, root string, lastID int64) *errs.Error {
	data, err := model.StateFile{Schema: model.StateSchema, Root: root, LastID: lastID}.Encode()
	if err != nil {
		return errs.Internal("encode " + StateName + ": " + err.Error())
	}
	r, err := env.FS.OpenRoot(env.ConfigDir)
	if err != nil {
		return errs.FromOS(env.ConfigDir, err)
	}
	defer r.Close()
	if err := removeTemps(r); err != nil {
		return errs.FromOS(env.ConfigDir, err)
	}
	if _, err := Publish(r, StateName, data, true); err != nil {
		return errs.FromOS(env.StatePath(), err)
	}
	return nil
}
