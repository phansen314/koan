package pick

import (
	"slices"

	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/ops"
)

// doneAction is d (pick-spec.md, Actions): done or reopen, decided by
// the readiness the lines show. If any target is shown open, it marks them
// all done; if every one is shown done, it reopens them all. A task marked
// done or reopened elsewhere since the last load makes its call
// a no-op, never a reversal, and the status line says so.
var doneAction = action{key: "d", arity: anyTargets, run: doneOrReopen}

func doneOrReopen(r *actionRun, targets []shownLine) {
	op, done, already := "reopen", "reopened", "already open"
	if slices.ContainsFunc(targets, func(t shownLine) bool { return t.Readiness != model.Done }) {
		op, done, already = "done", "done", "already done"
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
