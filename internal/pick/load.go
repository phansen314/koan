package pick

import (
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/ops"
)

// Load is one load (pick-spec.md, Session): one list over the whole tree,
// every readiness and every folder, from which everything pick shows is
// derived, so all of it agrees.
type Load struct {
	Folders  []model.FolderPath
	Tasks    []model.TaskView
	Warnings []errs.Warning
}

// load runs list over the whole tree, with the folders for a load, without
// for the final read (pick-spec.md, Output). On failure, the envelope is
// list's own, which the first load passes through (pick-spec.md, Errors).
func load(env ops.Env, folders bool) (*Load, *ops.Envelope) {
	in := &jsonio.Object{}
	in.Set("folder", string(model.RootFolder))
	in.Set("recursive", true)
	in.Set("readiness", []any{string(model.Ready), string(model.Blocked), string(model.Complete)})
	in.Set("include_folders", folders)
	out := ops.Run("list", in, nil, env)
	if !out.OK {
		return nil, &out
	}
	r, ok := out.Result.(ops.ListOutput)
	if !ok || (r.Folders == nil) == folders {
		e := ops.Failed(errs.Internal("list returned an unexpected result"))
		return nil, &e
	}
	l := &Load{Tasks: r.Tasks.Views, Warnings: out.Warnings}
	if folders {
		l.Folders = *r.Folders
	}
	return l, nil
}

// Scope is what decides the candidates: the scope folder, recursion,
// readiness scope and tag filters, with list's meanings, and a snapshot's
// IDs (pick-spec.md, Candidates). The f and s actions change it.
type Scope struct {
	Folder    model.FolderPath
	Recursive bool
	Readiness ops.PickScope
	TagsAny   []model.Tag // nil: no filter
	TagsAll   []model.Tag // nil: no filter
	IDs       []model.ID  // a snapshot's, or a live source's last run's; nil when there is neither
	// Source is a live source's command, run again on every reload; ""
	// when there is none.
	Source string
	// Folders is the folder picker's: it lists folders, not tasks, and has
	// no actions.
	Folders bool
}

// scopeOf is the scope pick's input starts with.
func scopeOf(in ops.PickInput) Scope {
	s := Scope{
		Folder:    in.Folder,
		Recursive: in.Recursive,
		Readiness: in.Scope,
		TagsAny:   in.TagsAny,
		TagsAll:   in.TagsAll,
		IDs:       in.IDs,
	}
	if in.Source != nil {
		s.Source = *in.Source
	}
	s.Folders = in.Folders
	return s
}

// checkFolder is list's not-found for a scope folder the load does not
// have: the first folder on its path that is missing.
func (l *Load) checkFolder(f model.FolderPath) *errs.Error {
	if slices.Contains(l.Folders, f) {
		return nil
	}
	segs := f.Segments()
	for i := range segs {
		p := model.FolderPath("/" + strings.Join(segs[:i+1], "/"))
		if !slices.Contains(l.Folders, p) {
			return errs.NotFound([]string{string(p)}, nil, nil)
		}
	}
	return errs.NotFound([]string{string(f)}, nil, nil)
}

// candidates are the load's tasks in s, in pick's line order (ops.PickOrder),
// and how many of a snapshot's IDs the load does not have at all, for the
// header (pick-spec.md, Session).
func (l *Load) candidates(s Scope) ([]model.TaskView, int) {
	var ids map[model.ID]bool
	missing := 0
	if s.IDs != nil {
		found := map[model.ID]bool{}
		for _, v := range l.Tasks {
			found[v.ID] = true
		}
		ids = map[model.ID]bool{}
		for _, id := range s.IDs {
			ids[id] = true
			if !found[id] {
				missing++
			}
		}
	}
	out := []model.TaskView{}
	for _, v := range l.Tasks {
		if inFolder(v.Folder, s.Folder, s.Recursive) &&
			inReadiness(v.Readiness, s.Readiness) &&
			(s.TagsAny == nil || slices.ContainsFunc(s.TagsAny, func(t model.Tag) bool { return slices.Contains(v.Tags, t) })) &&
			(s.TagsAll == nil || !slices.ContainsFunc(s.TagsAll, func(t model.Tag) bool { return !slices.Contains(v.Tags, t) })) &&
			(ids == nil || ids[v.ID]) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, ops.PickOrder)
	return out, missing
}

// inFolder reports whether a task in folder g is in scope folder f.
func inFolder(g, f model.FolderPath, recursive bool) bool {
	if g == f {
		return true
	}
	return recursive && (f == model.RootFolder || strings.HasPrefix(string(g), string(f)+"/"))
}

func inReadiness(r model.Readiness, s ops.PickScope) bool {
	switch s {
	case ops.PickReady:
		return r == model.Ready
	case ops.PickOpen:
		return r != model.Complete
	}
	return true
}
