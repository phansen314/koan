package ops

import (
	"slices"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

func decodeList(f *model.Fields, p *model.Problems) any {
	return ScopeInput{
		Folder:         optionalFolder(f, p, "folder"),
		Recursive:      optionalBool(f, p, "recursive", true),
		Readiness:      optionalReadiness(f, p, "readiness"),
		IncludeFolders: optionalBool(f, p, "include_folders", false),
		Narrowing:      decodeNarrowing(f, p),
	}
}

// optionalReadiness returns the field key as a non-empty set of readiness
// values, or ready and blocked when it is absent.
func optionalReadiness(f *model.Fields, p *model.Problems, key string) []model.Readiness {
	v, ok := f.Optional(key)
	if !ok {
		return []model.Readiness{model.Ready, model.Blocked}
	}
	names, _ := nonEmptySet(p, v, f.Ptr(key), "readiness value", nameSet(readinessValues))
	rs := make([]model.Readiness, len(names))
	for i, n := range names {
		rs[i] = model.Readiness(n)
	}
	return rs
}

// ListOutput is list's result (list-output). Folders is present only when
// include_folders is set.
type ListOutput struct {
	Folders   *[]model.FolderPath `json:"folders,omitempty"`
	Tasks     Tasks               `json:"tasks"`
	Total     int                 `json:"total"`
	Truncated bool                `json:"truncated"`
}

// runList returns every task in scope with one of the readiness values
// asked for, narrowed, and, on request, the folders in scope.
func runList(env Env, in ScopeInput, w *errs.Collector) (any, *errs.Error) {
	var out ListOutput
	e := store.Read(env.Env, w, func(tx *store.Tx) *errs.Error {
		folders, views, e := inScope(tx, in)
		if e != nil {
			return e
		}
		if in.IncludeFolders {
			out.Folders = &folders
		}
		views = slices.DeleteFunc(views, func(v model.TaskView) bool { return !slices.Contains(in.Readiness, v.Readiness) })
		out.Tasks, out.Total, out.Truncated = narrow(views, in.Narrowing)
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
