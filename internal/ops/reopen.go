package ops

import (
	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
)

func decodeReopen(f *model.Fields, p *model.Problems) any { return decodeID(f, p) }

// runReopen clears the task's completed_at, unless it is already open.
func runReopen(env Env, in IDInput, w *errs.Collector) (any, *errs.Error) {
	return setCompleted(env, in.ID, false, w)
}
