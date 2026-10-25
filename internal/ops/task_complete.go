package ops

import (
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// TaskCompleteResult is complete's result, in its schema's order.
type TaskCompleteResult struct {
	Completed []int64 `json:"completed"`
	Released  []int64 `json:"released"`
}

// TaskReopenResult is reopen's result, in its schema's order.
type TaskReopenResult struct {
	Reopened  []int64 `json:"reopened"`
	Reblocked []int64 `json:"reblocked"`
}

// tasksCommand is the shape of a write over a list of tasks and nothing
// else, made as one change; subject is what each task completes to.
// change is given the tasks resolved, in input order, none twice.
func tasksCommand(name string, subject offers, change func(tx *store.Tx, ids []int64) (any, []cli.Warning, error)) *cli.Command {
	return &cli.Command{
		Name:    name,
		Subject: tasksSubject(subject),
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			refs, at := r.taskRefs()
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				ids, err := resolveTasks(tx, refs, at)
				if err != nil {
					return nil, nil, err
				}
				return change(tx, ids)
			})
		},
	}
}

// tasksSubject is a list of tasks as a command's subject.
func tasksSubject(subject offers) *cli.Option {
	return &cli.Option{Field: "tasks", Kind: cli.List, Arg: "task", Required: true, Complete: subject.complete()}
}

// taskRefs reads tasks, the list of tasks a command acts on, with each
// one's pointer.
func (r *input) taskRefs() ([]core.Ref, []string) {
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
	return refs, at
}

// resolveTasks resolves the references taskRefs read, in order. One given
// twice is invalid-input at its later occurrence.
func resolveTasks(tx *store.Tx, refs []core.Ref, at []string) ([]int64, error) {
	var ids []int64
	for i, ref := range refs {
		res, err := resolveAt(tx, ref, core.WantTask, at[i])
		if err != nil {
			return nil, err
		}
		if slices.Contains(ids, res.ID) {
			return nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{at[i]},
				Message: fmt.Sprintf("@%d is given twice", res.ID)}
		}
		ids = append(ids, res.ID)
	}
	return ids, nil
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
	return tasksCommand("complete", openOnes, func(tx *store.Tx, ids []int64) (any, []cli.Warning, error) {
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
	return tasksCommand("reopen", completeOnes, func(tx *store.Tx, ids []int64) (any, []cli.Warning, error) {
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
