package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// joinCommand is the shape the join commands share: a subject, then the
// folders it joins, at least one, as the words after it.
func joinCommand(name string, subject cli.Option, run func(env *cli.Env, subject core.Ref, in []joinIn) (any, []cli.Warning, *cli.Error)) *cli.Command {
	subject.Required = true
	return &cli.Command{
		Name:    name,
		Subject: &subject,
		Rest:    &cli.Option{Field: "join", Kind: cli.List, Arg: "folder"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			var ref core.Ref
			if subject.Field == "task" {
				ref = r.ref("task", core.WantTask, "")
				if ref.End != core.EndNone {
					r.fault("/task", "%q: a gate joins no folder; the subject is a task", ref.Raw)
				}
			} else {
				ref = r.ref("folder", core.WantFolder, "")
				if ref.IsRoot() {
					r.fault("/folder", "%q: the root has no gates, so it joins no folder", ref.Raw)
				}
			}
			if v, ok := r.required("join"); ok {
				if list, isList := v.([]any); isList && len(list) == 0 {
					r.fault("/join", "at least one folder is needed")
				}
			}
			joins := r.joinRefs("join")
			refs, at := []core.Ref{ref}, []string{cli.Pointer(subject.Field)}
			for _, j := range joins {
				refs, at = append(refs, j.ref), append(at, j.at)
			}
			r.sameCase(refs, at)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			return run(env, ref, joins)
		},
	}
}

// TaskJoin joins a task to each folder given: it waits on the folder's
// start gate, and the folder's done gate waits on it. Any gate named is
// made, each folder's start gate first.
func TaskJoin() *cli.Command {
	return joinCommand("task join", cli.Option{Field: "task"}, func(env *cli.Env, task core.Ref, in []joinIn) (any, []cli.Warning, *cli.Error) {
		s, e := openStore(env)
		if e != nil {
			return nil, nil, e
		}
		defer s.Close()
		return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
			w := newEdges(tx)
			id, err := w.member(task)
			if err != nil {
				return nil, nil, err
			}
			es, err := w.join(id, id, in, true)
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

// TaskUnjoin removes a join's two edges for each folder given. It never
// makes a gate: an edge to one a folder lacks isn't there.
func TaskUnjoin() *cli.Command {
	return joinCommand("task unjoin", cli.Option{Field: "task"}, func(env *cli.Env, task core.Ref, in []joinIn) (any, []cli.Warning, *cli.Error) {
		s, e := openStore(env)
		if e != nil {
			return nil, nil, e
		}
		defer s.Close()
		return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
			w := newEdges(tx)
			id, err := w.member(task)
			if err != nil {
				return nil, nil, err
			}
			es, err := w.join(id, id, in, false)
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
