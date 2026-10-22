package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// TaskCompleteResult is task complete's result, in its schema's order.
type TaskCompleteResult struct {
	ID       int64   `json:"id"`
	Changed  bool    `json:"changed"`
	Released []int64 `json:"released"`
}

// TaskReopenResult is task reopen's result, in its schema's order.
type TaskReopenResult struct {
	ID        int64   `json:"id"`
	Changed   bool    `json:"changed"`
	Reblocked []int64 `json:"reblocked"`
}

// taskCommand is the shape of a write that takes a task and nothing
// else; subject is what the task completes to.
func taskCommand(name string, subject offers, change func(tx *store.Tx, id int64) (any, []cli.Warning, error)) *cli.Command {
	return rootedTaskCommand(name, subject, func(tx *store.Tx, _ store.Root, id int64) (any, []cli.Warning, error) {
		return change(tx, id)
	})
}

// rootedTaskCommand is taskCommand for a change that also needs the
// root, to look on disk.
func rootedTaskCommand(name string, subject offers, change func(tx *store.Tx, root store.Root, id int64) (any, []cli.Warning, error)) *cli.Command {
	return &cli.Command{
		Name:    name,
		Subject: &cli.Option{Field: "task", Required: true, Complete: subject.complete()},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("task", core.WantTask, "")
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				res, err := resolve(tx, ref, core.WantTask, "task")
				if err != nil {
					return nil, nil, err
				}
				return change(tx, s.Root, res.ID)
			})
		},
	}
}

// TaskComplete marks a task complete and ends its claim. What waited on
// it alone is released.
func TaskComplete() *cli.Command {
	return taskCommand("task complete", openOnes, func(tx *store.Tx, id int64) (any, []cli.Warning, error) {
		if !tx.Open(id) {
			return TaskCompleteResult{ID: id, Released: []int64{}}, nil, nil
		}
		watched := watch(tx, tx.Blocks(id))
		if err := tx.SetComplete(id, true); err != nil {
			return nil, nil, err
		}
		return TaskCompleteResult{ID: id, Changed: true, Released: watched.released()}, nil, nil
	})
}

// TaskReopen marks a task open again, so whatever waits on it waits
// again, a claimed task with its claim kept.
func TaskReopen() *cli.Command {
	return taskCommand("task reopen", completeOnes, func(tx *store.Tx, id int64) (any, []cli.Warning, error) {
		if tx.Open(id) {
			return TaskReopenResult{ID: id, Reblocked: []int64{}}, nil, nil
		}
		watched := watch(tx, tx.Blocks(id))
		if err := tx.SetComplete(id, false); err != nil {
			return nil, nil, err
		}
		reblocked, warnings, err := watched.reblocked()
		if err != nil {
			return nil, nil, err
		}
		return TaskReopenResult{ID: id, Changed: true, Reblocked: reblocked}, warnings, nil
	})
}
