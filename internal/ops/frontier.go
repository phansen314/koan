package ops

import (
	"cmp"
	"slices"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

// ScopeInput is the input of frontier and list, defaults applied.
// Readiness and IncludeFolders are list's only: frontier's tasks are the
// ready ones, which inScope leaves to it.
type ScopeInput struct {
	Folder         model.FolderPath
	Recursive      bool
	Readiness      []model.Readiness
	IncludeFolders bool
	Narrowing      Narrowing
}

func decodeFrontier(f *model.Fields, p *model.Problems) any {
	return ScopeInput{
		Folder:    optionalFolder(f, p, "folder"),
		Recursive: optionalBool(f, p, "recursive", true),
		Narrowing: decodeNarrowing(f, p),
	}
}

// FrontierOutput is frontier's result (frontier-output).
type FrontierOutput struct {
	Tasks     Tasks `json:"tasks"`
	Total     int   `json:"total"`
	Truncated bool  `json:"truncated"`
}

// runFrontier returns the ready tasks in scope in the order to work on them
// (frontierOrder), narrowed. Blockers are looked up anywhere in the
// tree, and every task-file problem is a warning, so one bad file never
// fails the frontier.
func runFrontier(env Env, in ScopeInput, w *errs.Collector) (any, *errs.Error) {
	var out FrontierOutput
	e := store.Read(env.Env, w, func(tx *store.Tx) *errs.Error {
		_, views, e := inScope(tx, ScopeInput{Folder: in.Folder, Recursive: in.Recursive})
		if e != nil {
			return e
		}
		ready := []model.TaskView{}
		for _, v := range views {
			if v.Readiness == model.Ready {
				ready = append(ready, v)
			}
		}
		slices.SortFunc(ready, frontierOrder)
		out.Tasks, out.Total, out.Truncated = narrow(ready, in.Narrowing)
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// frontierOrder is frontier order (operations.md, frontier, Order):
// priority, highest first, no priority after every prioritized task; then
// ID, lowest first; then, for copies of a duplicated ID — each in its own
// folder — tree order. It is a total order, so the sort needs no stability.
func frontierOrder(a, b model.TaskView) int {
	switch {
	case a.Priority == nil && b.Priority != nil:
		return 1
	case a.Priority != nil && b.Priority == nil:
		return -1
	case a.Priority != nil && *a.Priority != *b.Priority:
		return cmp.Compare(*b.Priority, *a.Priority)
	}
	return cmp.Or(cmp.Compare(a.ID, b.ID), store.CompareFolders(a.Folder, b.Folder))
}
