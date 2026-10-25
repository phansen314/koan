package ops

import (
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// TaskCompleteResult is task complete's result, in its schema's order.
type TaskCompleteResult struct {
	Completed []int64 `json:"completed"`
	Released  []int64 `json:"released"`
}

// TaskReopenResult is task reopen's result, in its schema's order.
type TaskReopenResult struct {
	Reopened  []int64 `json:"reopened"`
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

// tasksCommand is the shape of a write over a list of tasks and nothing
// else, made as one change; subject is what each task completes to.
// change is given the tasks resolved, in input order, none twice.
func tasksCommand(name string, subject offers, change func(tx *store.Tx, ids []int64) (any, []cli.Warning, error)) *cli.Command {
	return &cli.Command{
		Name:    name,
		Subject: &cli.Option{Field: "tasks", Kind: cli.List, Arg: "task", Required: true, Complete: subject.complete()},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			if v, ok := r.required("tasks"); ok {
				if list, isList := v.([]any); isList && len(list) == 0 {
					r.fault("/tasks", "at least one task is needed")
				}
			}
			refs := r.refs("tasks", core.WantTask)
			at := make([]string, len(refs))
			for i := range refs {
				at[i] = fmt.Sprintf("/tasks/%d", i)
			}
			r.sameCase(refs, at)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				var ids []int64
				for i, ref := range refs {
					res, err := resolveAt(tx, ref, core.WantTask, at[i])
					if err != nil {
						return nil, nil, err
					}
					if slices.Contains(ids, res.ID) {
						return nil, nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{at[i]},
							Message: fmt.Sprintf("@%d is given twice", res.ID)}
					}
					ids = append(ids, res.ID)
				}
				return change(tx, ids)
			})
		},
	}
}

// waiting is the tasks that wait on any of ids.
func waiting(tx *store.Tx, ids []int64) []int64 {
	var out []int64
	for _, id := range ids {
		out = append(out, tx.Blocks(id)...)
	}
	return out
}

// TaskComplete marks tasks complete and ends their claims, as one change.
// What waited on them alone is released; a task completed with them never
// is.
func TaskComplete() *cli.Command {
	return tasksCommand("task complete", openOnes, func(tx *store.Tx, ids []int64) (any, []cli.Warning, error) {
		watched := watch(tx, waiting(tx, ids))
		completed := []int64{}
		for _, id := range ids {
			if !tx.Open(id) {
				continue
			}
			if err := tx.SetComplete(id, true); err != nil {
				return nil, nil, err
			}
			completed = append(completed, id)
		}
		return TaskCompleteResult{Completed: completed, Released: watched.released()}, nil, nil
	})
}

// TaskReopen marks tasks open again, as one change, so whatever waits on
// them waits again, a claimed task with its claim kept. A task reopened
// with them wasn't ready, so it never waits again.
func TaskReopen() *cli.Command {
	return tasksCommand("task reopen", completeOnes, func(tx *store.Tx, ids []int64) (any, []cli.Warning, error) {
		watched := watch(tx, waiting(tx, ids))
		reopened := []int64{}
		for _, id := range ids {
			if tx.Open(id) {
				continue
			}
			if err := tx.SetComplete(id, false); err != nil {
				return nil, nil, err
			}
			reopened = append(reopened, id)
		}
		reblocked, warnings, err := watched.reblocked()
		if err != nil {
			return nil, nil, err
		}
		return TaskReopenResult{Reopened: reopened, Reblocked: reblocked}, warnings, nil
	})
}
