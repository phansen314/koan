package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// BlockResult is block's result, in its schema's order.
type BlockResult struct {
	ID        int64      `json:"id"`
	Changed   bool       `json:"changed"`
	GatesMade []GateMade `json:"gates_made"`
	Reblocked []int64    `json:"reblocked"`
}

// Derived is what the change made ready and made wait, as the log records
// them.
func (r BlockResult) Derived() (released, reblocked []int64) { return nil, r.Reblocked }

// UnblockResult is unblock's result, in its schema's order.
type UnblockResult struct {
	ID       int64   `json:"id"`
	Changed  bool    `json:"changed"`
	Released []int64 `json:"released"`
}

// Derived is what the change made ready and made wait, as the log records
// them.
func (r UnblockResult) Derived() (released, reblocked []int64) { return r.Released, nil }

// edgeCommand is the shape block and unblock share: a task,
// then at least one edge, by --blocked-by or --blocks. ends is what the edges'
// other ends complete to.
func edgeCommand(name string, subject, ends offers, run func(env *cli.Env, task core.Ref, in []edgeIn) (any, []cli.Warning, *cli.Error)) *cli.Command {
	return &cli.Command{
		Name:    name,
		Subject: &cli.Option{Field: "task", Required: true, Complete: subject.complete()},
		Options: []cli.Option{
			{Field: "blocked_by", Kind: cli.List, Complete: ends.complete()},
			{Field: "blocks", Kind: cli.List, Complete: ends.complete()},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			task := r.ref("task", core.WantTask, "")
			n := 0
			for _, field := range []string{"blocked_by", "blocks"} {
				if list, ok := in[field].([]any); ok {
					n += len(list)
				}
			}
			if n == 0 {
				r.fault("", "at least one edge is needed: %s or %s", r.name("blocked_by"), r.name("blocks"))
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
	return edgeCommand("block", blockable, edgeEnds, func(env *cli.Env, task core.Ref, in []edgeIn) (any, []cli.Warning, *cli.Error) {
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
			return BlockResult{ID: id, Changed: changed, GatesMade: w.made, Reblocked: reblocked}, warnings, nil
		})
	})
}

// TaskUnblock removes edges from a task. It never makes a gate.
func TaskUnblock() *cli.Command {
	return edgeCommand("unblock", tasksAndGates, madeEnds, func(env *cli.Env, task core.Ref, in []edgeIn) (any, []cli.Warning, *cli.Error) {
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
			return UnblockResult{ID: id, Changed: changed, Released: released}, nil, nil
		})
	})
}
