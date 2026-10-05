package ops

import (
	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

func decodeComplete(f *model.Fields, p *model.Problems) any { return decodeID(f, p) }

// ChangedOutput is complete's and reopen's result (complete-output,
// reopen-output): the task after the operation, plus whether it changed.
type ChangedOutput struct {
	model.Task
	Changed bool `json:"changed"`
}

// runComplete sets the task's completed_at to now, unless it is already
// complete.
func runComplete(env Env, in IDInput, w *errs.Collector) (any, *errs.Error) {
	return setCompleted(env, in.ID, true, w)
}

// setCompleted finds the one task with id, under the write lock, and
// completes it (completed_at now) or reopens it (completed_at null). A task
// already as asked is not written, and keeps its completed_at. Otherwise its
// task file is replaced in one rename, every other field as it was; the .md
// is left alone.
func setCompleted(env Env, id model.ID, complete bool, w *errs.Collector) (any, *errs.Error) {
	var out ChangedOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		ld, e := findOne(tx, id)
		if e != nil {
			return e
		}
		if ld.Task.Open() == complete {
			now := model.TimestampOf(env.Clock())
			ld.Task.CompletedAt = nil
			if complete {
				ld.Task.CompletedAt = &now
			}
			if e := replaceTask(tx, ld.Loc.Rel(), &ld.Task, now); e != nil {
				return e
			}
			out.Changed = true
		}
		out.Task = tx.Task(ld)
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
