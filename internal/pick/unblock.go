package pick

import (
	"fmt"
	"slices"
	"strings"

	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// unblockAction is u (pick-spec.md, Actions): a choose list, unblock 42> ,
// of the one target's blocked_by, read fresh, IDs with no task included;
// the chosen are removed in one unblock call.
var unblockAction = action{key: "u", arity: oneTarget, run: openUnblock, choose: removeBlockers}

func openUnblock(r *actionRun, targets []shownLine) {
	t := targets[0]
	l, failed := load(r.env.Ops, true)
	if failed != nil {
		r.status = "✗ u: " + errText(failed.Error)
		return
	}
	lines, why := unblockChoices(l, t, !noColor(r.env.Sys.Environ()))
	switch {
	case why != "":
		r.status = "✗ u: " + string(idNumber(t.ID)) + " " + why
		return
	case len(lines) == 0:
		r.status = string(idNumber(t.ID)) + " has no blockers"
		return
	}
	if r.err = writePreviews(r.s, l); r.err != nil {
		return
	}
	r.openChoose("u", targetLabel("unblock", targets), lines, false, targets)
}

// unblockChoices are the lines of t's blockers in the load: each blocker's
// task, in pick's line order, then each ID with no task, ascending, keyed
// by the ID alone. t is found as findLine finds it, e.g. moved by another
// process meanwhile; failed says why it isn't.
func unblockChoices(l *Load, t shownLine, color bool) (lines []string, failed string) {
	i, failed := findLine(l.Tasks, t)
	if failed != "" {
		return nil, failed
	}
	blockedBy := l.Tasks[i].BlockedBy
	var missing []model.ID
	for _, b := range blockedBy {
		if !slices.ContainsFunc(l.Tasks, func(v model.TaskView) bool { return v.ID == b }) {
			missing = append(missing, b)
		}
	}
	all, _ := l.candidates(Scope{Folder: model.RootFolder, Recursive: true})
	views := slices.DeleteFunc(all, func(v model.TaskView) bool { return !slices.Contains(blockedBy, v.ID) })
	lines = renderLines(views, color)
	slices.Sort(missing)
	for _, id := range missing {
		lines = append(lines, fmt.Sprintf("%d\t?  %d\t\t(no such task)", id, id))
	}
	return lines, ""
}

// removeBlockers removes the chosen blockers, by ID, from the target's
// blocked_by.
func removeBlockers(r *actionRun, chosen []string, targets []shownLine) {
	t := targets[0]
	var ids []string
	var blockers []any
	for _, k := range chosen {
		id, _, _ := strings.Cut(k, "@")
		if slices.Contains(ids, id) {
			continue // a duplicated ID's other copy
		}
		ids = append(ids, id)
		blockers = append(blockers, jsonNumberOf(id))
	}
	in := &jsonio.Object{}
	in.Set("id", idNumber(t.ID))
	in.Set("blockers", blockers)
	r.call(t.ID, "unblock", in, "unblocked", "unblock "+string(idNumber(t.ID))+" ← "+strings.Join(ids, ", "))
}
