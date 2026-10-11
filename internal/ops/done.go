package ops

import (
	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

func decodeDone(f *model.Fields, p *model.Problems) any { return decodeTokenInput(f, p) }

// ChangedOutput is done's and reopen's result (done-output,
// reopen-output): the task after the operation, plus whether it changed.
type ChangedOutput struct {
	model.Task
	Changed bool `json:"changed"`
}

// runDone sets the task's completed_at to now, unless it is already
// done.
func runDone(env Env, in TokenInput, w *errs.Collector) (any, *errs.Error) {
	return setCompleted(env, in.ID, true, in.Token, w)
}

// setCompleted finds the one task with id, under the write lock, and
// marks it done (completed_at now) or reopens it (completed_at null). A task
// already as asked is not written, and keeps its completed_at. Otherwise its
// task file is replaced in one rename, every other field as it was; the .md
// is left alone.
//
// Marking done is a targeting write: token is checked against the task's
// claim, and the task's claim entry is dropped last, once the task is done —
// also when it was done already, which clears what an interrupted done left.
// Reopening checks no token, since a done task's claim is inert; it drops the
// entry first, and fails with nothing done if it can't, so a reopened task
// never comes back with an old claim. An open task's claim is left alone.
func setCompleted(env Env, id model.ID, complete bool, token string, w *errs.Collector) (any, *errs.Error) {
	var out ChangedOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		ld, e := findOneClaimed(tx, id)
		if e != nil {
			return e
		}
		if complete {
			if e := checkTokens(tx, []model.ID{id}, token); e != nil {
				return e
			}
		} else if !ld.Task.Open() {
			if _, e := tx.DropClaims([]model.ID{id}); e != nil {
				return e
			}
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
		if complete {
			return dropClaims(tx, []model.ID{id})
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
