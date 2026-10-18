package ops

import (
	"encoding/json"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
)

type claimResult struct {
	Task *model.Task `json:"task"`
}

// claim records a claim on a task and saves the state file.
func (c *call) claim(id model.ID, extra map[string]any) *errs.Error {
	if extra == nil {
		extra = map[string]any{}
	}
	c.t.State.Claims[model.ClaimKey(id)] = model.Claim{ClaimedAt: c.now, Extra: extra}
	return c.t.SaveState()
}

func cmdClaim(c *call) (any, *errs.Error) {
	in := c.in.top()
	id := in.id("id", true)
	extra, _ := in.object("extra")
	if e := c.begin(true); e != nil {
		return nil, e
	}
	_, tf, e := c.find(id)
	if e != nil {
		return nil, e
	}
	if !tf.Open() {
		return nil, errs.NewConflict(errs.RuleTaskDone, "task %d is done", id).WithIDs(id)
	}
	if held := c.t.Claim(id); held != nil {
		by, _ := json.Marshal(held.Extra)
		return nil, errs.NewConflict(errs.RuleClaimed, "task %d is already claimed (since %s, extra %s)", id, held.ClaimedAt, by).WithIDs(id)
	}
	if e := c.claim(id, extra); e != nil {
		return nil, e
	}
	return written{ID: id, Changed: true}, nil
}

// cmdClaimNext is task frontier and task claim under one lock, so two callers asking
// at once never get the same task.
func cmdClaimNext(c *call) (any, *errs.Error) {
	in := c.in.top()
	s := readScope(in)
	extra, _ := in.object("extra")
	if e := c.begin(true); e != nil {
		return nil, e
	}
	tasks, e := c.tasks(&s, available)
	if e != nil {
		return nil, e
	}
	if len(tasks) == 0 {
		return claimResult{}, nil
	}
	first := slices.MinFunc(tasks, workOrder)
	if e := c.claim(first.ID, extra); e != nil {
		return nil, e
	}
	first.Claim = c.t.Claim(first.ID)
	return claimResult{Task: &first}, nil
}

type releaseResult struct {
	ID      model.ID `json:"id"`
	Changed bool     `json:"changed"`
}

// cmdRelease ends a claim. The task need not exist: releasing goes by the
// entry in the state file.
func cmdRelease(c *call) (any, *errs.Error) {
	id := c.in.top().id("id", true)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	_, held := c.t.State.Claims[model.ClaimKey(id)]
	if e := c.dropClaims(id); e != nil {
		return nil, e
	}
	return releaseResult{ID: id, Changed: held}, nil
}
