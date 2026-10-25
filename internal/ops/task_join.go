package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// joinEnds resolves a join's subject to its ID and the ends a join's
// edges name: a task is both its own ends, and a folder's are its start
// and done gates. With make set, a gate it lacks is made; else it is 0.
type joinEnds func(w *edges, ref core.Ref, make bool) (id, start, done int64, err error)

// joinCommand is the shape the join commands share: a subject, then the
// folders it joins, at least one, as the words after it. With add set,
// it writes the edges of joining each folder, making any gate named;
// else it removes them, never making a gate.
func joinCommand(name string, subject cli.Option, want core.Want, ends joinEnds, add bool) *cli.Command {
	subject.Required = true
	return &cli.Command{
		Name:    name,
		Subject: &subject,
		Object:  &cli.Option{Field: "join", Kind: cli.List, Arg: "folder", Required: true, Complete: foldersOnly.complete()},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			at := cli.Pointer(subject.Field)
			// A task subject's end, or a folder subject's root: the
			// shape check refuses the others.
			ref := r.ref(subject.Field, want, "")
			if ref.End != core.EndNone {
				r.fault(at, "%q: a gate joins no folder; the subject is a task", ref.Raw)
			}
			if ref.IsRoot() {
				r.fault(at, "%q: the root has no gates, so it joins no folder", ref.Raw)
			}
			if v, ok := r.required("join"); ok {
				if list, isList := v.([]any); isList && len(list) == 0 {
					r.fault("/join", "at least one folder is needed")
				}
			}
			joins := r.joinRefs("join")
			refs, pointers := []core.Ref{ref}, []string{at}
			for _, j := range joins {
				refs, pointers = append(refs, j.ref), append(pointers, j.at)
			}
			r.sameCase(refs, pointers)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				w := newEdges(tx)
				id, start, done, err := ends(w, ref, add)
				if err != nil {
					return nil, nil, err
				}
				es, err := w.join(start, done, joins, add)
				if err != nil {
					return nil, nil, err
				}
				if !add {
					changed, released, err := w.remove(es)
					if err != nil {
						return nil, nil, err
					}
					return TaskUnblockResult{ID: id, Changed: changed, Released: released}, nil, nil
				}
				changed, reblocked, warnings, err := w.add(es)
				if err != nil {
					return nil, nil, err
				}
				return TaskBlockResult{ID: id, Changed: changed, GatesMade: w.made, Reblocked: reblocked}, warnings, nil
			})
		},
	}
}

// taskEnds resolves a task join's subject: a task, never a gate.
func taskEnds(w *edges, ref core.Ref, make bool) (id, start, done int64, err error) {
	id, err = w.member(ref)
	return id, id, id, err
}

// TaskJoin joins a task to each folder given: it waits on the folder's
// start gate, and the folder's done gate waits on it. Any gate named is
// made, each folder's start gate first.
func TaskJoin() *cli.Command {
	return joinCommand("task join", cli.Option{Field: "task", Complete: tasksOnly.complete()}, core.WantTask, taskEnds, true)
}

// TaskUnjoin removes a join's two edges for each folder given. It never
// makes a gate: an edge to one a folder lacks isn't there.
func TaskUnjoin() *cli.Command {
	return joinCommand("task unjoin", cli.Option{Field: "task", Complete: tasksOnly.complete()}, core.WantTask, taskEnds, false)
}
