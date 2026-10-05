package pick

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/phansen314/ftask/internal/jsonio"
)

// priorityAction is p (pick-spec.md, Actions): the targets' priority, in a
// prompt that starts with the one target's priority, or empty for several.
// An integer sets it; null clears it, as an empty value does for one
// target. Anything else goes to update as it is, to be refused there, so
// the prompt stays open with it.
var priorityAction = action{key: "p", arity: anyTargets, run: openPriority, apply: setPriority}

func openPriority(r *actionRun, targets []shownLine) {
	start := promptStart(targets, func(t shownLine) string {
		if t.Priority == nil {
			return ""
		}
		return strconv.FormatInt(*t.Priority, 10)
	})
	r.openPrompt("p", targetLabel("priority", targets), start, targets)
}

func setPriority(r *actionRun, value string, targets []shownLine) bool {
	value = strings.TrimSpace(value)
	var priority any = value
	switch n, err := strconv.ParseInt(value, 10, 64); {
	case value == "" || value == "null":
		priority = nil
	case err == nil:
		// As JSON has it: +5 and 05 parse, but are no JSON number.
		priority = json.Number(strconv.FormatInt(n, 10))
	}
	return r.applyEach(value, targets, func(t shownLine) {
		in := &jsonio.Object{}
		in.Set("id", idNumber(t.ID))
		in.Set("priority", priority)
		r.call(t.ID, "update", in, "set priority", "priority "+string(idNumber(t.ID)))
	})
}
