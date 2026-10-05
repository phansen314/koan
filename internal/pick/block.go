package pick

import (
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/ops"
)

// blockAction is b (pick-spec.md, Actions): a choose list of candidate
// blockers, blockers of 42> ; the chosen are added to each target's
// blocked_by, one block call per target.
var blockAction = action{key: "b", arity: anyTargets, run: openBlockers, choose: addBlockers}

func openBlockers(r *actionRun, targets []shownLine) {
	l, failed := load(r.env.Ops, true)
	if failed != nil {
		r.status = "✗ b: " + errText(failed.Error)
		return
	}
	views := blockerCandidates(l, targets)
	if r.err = writePreviews(r.s, l); r.err != nil {
		return
	}
	r.openChoose("b", targetLabel("blockers of", targets), renderLines(views, !noColor(r.env.Sys.Environ())), false, targets)
}

// blockerCandidates are the tasks b offers as blockers of targets, in
// pick's line order: every open task in the tree, whatever the scope, but
// the targets and every task from which a target can be reached by
// following blocked_by, which would close a cycle. The graph is the
// load's, keyed by ID, with every copy's edges, done tasks included,
// as block's cycle check follows them. It is read fresh for the choose
// list; block still checks, for a cycle made meanwhile.
func blockerCandidates(l *Load, targets []shownLine) []model.TaskView {
	// dependents[b] are the IDs that name b in blocked_by.
	dependents := map[model.ID][]model.ID{}
	for _, v := range l.Tasks {
		for _, b := range v.BlockedBy {
			dependents[b] = append(dependents[b], v.ID)
		}
	}
	excluded := map[model.ID]bool{}
	var queue []model.ID
	for _, t := range targets {
		if !excluded[t.ID] {
			excluded[t.ID] = true
			queue = append(queue, t.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, d := range dependents[id] {
			if !excluded[d] {
				excluded[d] = true
				queue = append(queue, d)
			}
		}
	}
	open, _ := l.candidates(Scope{Folder: model.RootFolder, Recursive: true, Readiness: ops.PickOpen})
	return slices.DeleteFunc(open, func(v model.TaskView) bool { return excluded[v.ID] })
}

// addBlockers adds the chosen tasks, by ID, to each target's blocked_by.
func addBlockers(r *actionRun, chosen []string, targets []shownLine) {
	var ids []string
	var blockers []any
	for _, k := range chosen {
		id, _, _ := strings.Cut(k, "@")
		if _, err := strconv.ParseInt(id, 10, 64); err != nil || slices.Contains(ids, id) {
			continue // a duplicated ID's other copy
		}
		ids = append(ids, id)
		blockers = append(blockers, jsonNumberOf(id))
	}
	for _, t := range targets {
		in := &jsonio.Object{}
		in.Set("id", idNumber(t.ID))
		in.Set("blockers", blockers)
		r.call(t.ID, "block", in, "blocked", "block "+string(idNumber(t.ID))+" ← "+strings.Join(ids, ", "))
	}
}
