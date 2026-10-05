package pick

import (
	"slices"

	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/ops"
)

// completeAction is c (pick-spec.md, Actions): complete or reopen, decided
// by the readiness the lines show. If any target is shown open, it
// completes them all; if every one is shown complete, it reopens them all.
// A task completed or reopened elsewhere since the last load makes its call
// a no-op, never a reversal, and the status line says so.
var completeAction = action{key: "c", arity: anyTargets, run: completeOrReopen}

func completeOrReopen(r *actionRun, targets []shownLine) {
	op, done, already := "reopen", "reopened", "already open"
	if slices.ContainsFunc(targets, func(t shownLine) bool { return t.Readiness != model.Complete }) {
		op, done, already = "complete", "completed", "already complete"
	}
	for _, t := range targets {
		in := &jsonio.Object{}
		in.Set("id", idNumber(t.ID))
		out := r.call(t.ID, op, in, done, op+" "+string(idNumber(t.ID)))
		if res, ok := out.Result.(ops.ChangedOutput); out.OK && ok && !res.Changed {
			r.outcomes[len(r.outcomes)-1].done = already
		}
	}
}
