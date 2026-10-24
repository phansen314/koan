package ops

import (
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// DeleteResult is task delete's result, and folder delete's, in its
// schema's order.
type DeleteResult struct {
	Removed  []int64 `json:"removed"`
	Released []int64 `json:"released"`
}

// TaskDelete removes tasks, their notes, their claims and every edge to
// and from them, as one change. It never splices: what waited on them
// isn't passed their blockers, and a task deleted with them is never
// released.
func TaskDelete() *cli.Command {
	return tasksCommand("task delete", tasksAndGates, func(tx *store.Tx, ids []int64) (any, []cli.Warning, error) {
		var steps []fsstep.Step
		for _, id := range ids {
			row, _, err := tx.Task(id)
			if err != nil {
				return nil, nil, err
			}
			steps = append(steps, fsstep.Step{Op: fsstep.OpRemove, From: fsstep.TaskNotesPath(tx.Path(row.Folder), id, row.Gate)})
		}
		released, err := tx.RemoveTasks(ids, nil)
		if err != nil {
			return nil, nil, err
		}
		for _, step := range steps {
			if _, err := fsstep.Add(tx, step); err != nil {
				return nil, nil, err
			}
		}
		return DeleteResult{Removed: slices.Sorted(slices.Values(ids)), Released: nonNil(released)}, nil, nil
	})
}
