package graph

import "github.com/phansen314/koan/internal/model"

// BlockerState is what a blocked_by entry names, as readiness sees it (see
// design-spec.md, Dependencies).
type BlockerState int

const (
	// BlockerMissing: no task file (a dangling reference). The zero value, so a
	// state lookup that misses reads as missing and blocks.
	BlockerMissing BlockerState = iota
	// BlockerOpen: exactly one task file, usable and open.
	BlockerOpen
	// BlockerDuplicate: more than one task file.
	BlockerDuplicate
	// BlockerUnusable: exactly one task file, and it is unusable.
	BlockerUnusable
	// BlockerComplete: exactly one task file, usable and complete. The only
	// state that does not block.
	BlockerComplete
)

// Readiness derives t's readiness from its own blocked_by and completed_at,
// asking state about each blocker. t must be normalized
// (model.TaskFile.Normalize), so blocked_by is ascending. For an open task,
// state is called for every blocker, in ascending order, even once one is
// known to block, so the caller sees each one (to warn about it); for a
// complete task it is not called. Blocking lists, ascending, the blockers that block: non-empty
// exactly when the readiness is model.Blocked.
func Readiness(t *model.TaskFile, state func(model.ID) BlockerState) (r model.Readiness, blocking []model.ID) {
	if !t.Open() {
		return model.Complete, []model.ID{}
	}
	blocking = []model.ID{}
	for _, b := range t.BlockedBy {
		if state(b) != BlockerComplete {
			blocking = append(blocking, b)
		}
	}
	if len(blocking) > 0 {
		return model.Blocked, blocking
	}
	return model.Ready, blocking
}
