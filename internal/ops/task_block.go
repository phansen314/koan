package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// TaskBlockResult is block's result, in its schema's order.
type TaskBlockResult struct {
	ID        int64      `json:"id"`
	Changed   bool       `json:"changed"`
	GatesMade []GateMade `json:"gates_made"`
	Reblocked []int64    `json:"reblocked"`
}

// TaskUnblockResult is unblock's result, in its schema's order.
type TaskUnblockResult struct {
	ID       int64   `json:"id"`
	Changed  bool    `json:"changed"`
	Released []int64 `json:"released"`
}

// edgeCommand is the shape block and unblock share: a task,
// then what it waits on, and at least one edge.
func edgeCommand(name string, subject offers, run func(env *cli.Env, task core.Ref, in []edgeIn) (any, []cli.Warning, *cli.Error)) *cli.Command {
	return &cli.Command{
		Name:    name,
		Subject: &cli.Option{Field: "task", Required: true, Complete: subject.complete()},
		Object:  &cli.Option{Field: "blocked_by", Kind: cli.List, Arg: "blocker", Complete: edgeEnds.complete()},
		Options: []cli.Option{
			{Field: "blocks", Kind: cli.List, Complete: edgeEnds.complete()},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			task := r.ref("task", core.WantTask, "")
			n := 0
			for _, field := range []string{"blocked_by", "blocks"} {
				if list, ok := in[field].([]any); ok {
					n += len(list)
				}
			}
			if n == 0 {
				r.fault("", "at least one edge is needed: what it waits on, after the task, or --blocks")
			}
			edgesIn := r.edgeRefs()
			refs, at := []core.Ref{task}, []string{"/task"}
			for _, e := range edgesIn {
				refs, at = append(refs, e.ref), append(at, e.at)
			}
			r.sameCase(refs, at)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			return run(env, task, edgesIn)
		},
	}
}

// TaskBlock adds edges to a task, making any gate they name, its own
// included, whose ID is issued first.
func TaskBlock() *cli.Command {
	return edgeCommand("block", blockable, func(env *cli.Env, task core.Ref, in []edgeIn) (any, []cli.Warning, *cli.Error) {
		s, e := openStore(env)
		if e != nil {
			return nil, nil, e
		}
		defer s.Close()
		return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
			w := newEdges(tx)
			id, err := w.subject(task, true)
			if err != nil {
				return nil, nil, err
			}
			es, err := w.resolve(id, in, true)
			if err != nil {
				return nil, nil, err
			}
			changed, reblocked, warnings, err := w.add(es)
			if err != nil {
				return nil, nil, err
			}
			return TaskBlockResult{ID: id, Changed: changed, GatesMade: w.made, Reblocked: reblocked}, warnings, nil
		})
	})
}

// TaskUnblock removes edges from a task. It never makes a gate.
func TaskUnblock() *cli.Command {
	return edgeCommand("unblock", tasksAndGates, func(env *cli.Env, task core.Ref, in []edgeIn) (any, []cli.Warning, *cli.Error) {
		s, e := openStore(env)
		if e != nil {
			return nil, nil, e
		}
		defer s.Close()
		return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
			w := newEdges(tx)
			id, err := w.subject(task, false)
			if err != nil {
				return nil, nil, err
			}
			es, err := w.resolve(id, in, false)
			if err != nil {
				return nil, nil, err
			}
			changed, released, err := w.remove(es)
			if err != nil {
				return nil, nil, err
			}
			return TaskUnblockResult{ID: id, Changed: changed, Released: released}, nil, nil
		})
	})
}
