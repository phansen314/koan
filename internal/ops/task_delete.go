package ops

import (
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

// TaskDelete removes a task, its notes, its claim and every edge to and
// from it. It never splices: what waited on it isn't passed its blockers.
func TaskDelete() *cli.Command {
	return taskCommand("task delete", tasksAndGates, func(tx *store.Tx, id int64) (any, []cli.Warning, error) {
		row, _, err := tx.Task(id)
		if err != nil {
			return nil, nil, err
		}
		notes := fsstep.TaskNotesPath(tx.Path(row.Folder), id, row.Gate)
		released, err := tx.RemoveTasks([]int64{id}, nil)
		if err != nil {
			return nil, nil, err
		}
		if _, err := fsstep.Add(tx, fsstep.Step{Op: fsstep.OpRemove, From: notes}); err != nil {
			return nil, nil, err
		}
		return DeleteResult{Removed: []int64{id}, Released: nonNil(released)}, nil, nil
	})
}
