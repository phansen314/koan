// Package pick is ftask pick, the interactive picker built on fzf
// (pick-spec.md). It runs no operation of its own: it validates its input
// through ops, like any command, and composes list and the write operations
// its keys run, each as its own call.
package pick

import (
	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/ops"
)

// Env is pick's environment: the operations' and the process's.
type Env struct {
	Ops ops.Env
	Sys System
}

// Run runs pick on in, the input the CLI built, with problems the CLI found
// while building it, and returns the one envelope pick writes. fzf is
// checked after the input and before anything is read from the tree
// (pick-spec.md, Errors).
func Run(in *jsonio.Object, problems []errs.Problem, env Env) ops.Envelope {
	v, e := ops.Validate("pick", in, problems)
	if e != nil {
		return ops.Failed(e)
	}
	pin := v.(ops.PickInput)
	// A live source's first run: its failures are input's, so before fzf
	// is checked (pick-spec.md, Errors), with no time limit.
	if pin.Source != nil {
		ids, reason := sourceIDs(env, *pin.Source, 0)
		if reason != "" {
			return ops.Failed(errs.InvalidInput([]errs.Problem{{Field: "/source", Reason: reason}}))
		}
		pin.IDs = ids
	}
	fzf, e := findFzf(env.Sys)
	if e != nil {
		return ops.Failed(e)
	}
	// FTASK_PICK_OPTS is fzf's too: checked with it, before the tree is
	// read.
	environ := env.Sys.Environ()
	opts, e := userOpts(environ)
	if e != nil {
		return ops.Failed(e)
	}
	l, failed := load(env.Ops, true)
	if failed != nil {
		return *failed
	}
	scope := scopeOf(pin)
	if e := l.checkFolder(scope.Folder); e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	var views []model.TaskView
	var missing int
	var lines []string
	if scope.Folders {
		lines = folderLinesOf(l.foldersIn(scope))
	} else {
		views, missing = l.candidates(scope)
		lines = renderLines(views, !noColor(environ))
	}
	exe, err := env.Sys.Executable()
	if err != nil {
		return ops.Envelope{Error: errs.Internal("locating the ftask binary: " + err.Error()), Warnings: l.Warnings}
	}
	pk := picker{exe: exe, scope: scope, query: pin.Query, missing: missing, warnings: len(l.Warnings), userOpts: opts}
	if pin.SelectOne || pin.ExitZero {
		matched, e := matchAtOnce(env, fzf, pk, lines)
		if e != nil {
			return ops.Envelope{Error: e, Warnings: l.Warnings}
		}
		if keys, done := decideAtOnce(matched, pin.SelectOne, pin.ExitZero); done {
			if scope.Folders {
				return emitFolders(env, keys, []any{})
			}
			return emit(env, keys, pin.Fields, []any{}, []model.ID{})
		}
	}
	s, e := newSession(env.Ops.FS, sessionBase(environ))
	if e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	defer s.Remove()
	if e := writeJSON(s, scopeFile, scope); e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	if e := writeLoaded(s, l, views, lines, missing); e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	status, e := show(env, fzf, pk, lines, s)
	if e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	return finish(env, s, status, pin.Fields, scope.Folders)
}

// noColor reports whether lines carry no color: NO_COLOR set, and not
// empty (pick-spec.md, Lines).
func noColor(environ []string) bool {
	return lookupEnv(environ, "NO_COLOR") != ""
}
