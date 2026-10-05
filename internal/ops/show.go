package ops

import (
	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

func decodeShow(f *model.Fields, p *model.Problems) any { return decodeID(f, p) }

// ShowOutput is show's result (show-output).
type ShowOutput struct {
	Tasks []model.TaskView `json:"tasks"`
}

// runShow returns every copy of the task, in tree order, each with its own
// readiness; several copies are a duplicate-id warning.
func runShow(env Env, in IDInput, w *errs.Collector) (any, *errs.Error) {
	var out ShowOutput
	e := store.Read(env.Env, w, func(tx *store.Tx) *errs.Error {
		copies, e := findCopies(tx, in.ID)
		if e != nil {
			return e
		}
		if len(copies) > 1 {
			locs := make([]store.Location, len(copies))
			for i, ld := range copies {
				locs[i] = ld.Loc
			}
			duplicateID(tx, in.ID, locs)
		}
		for _, ld := range copies {
			v, e := view(tx, ld)
			if e != nil {
				return e
			}
			out.Tasks = append(out.Tasks, v)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
