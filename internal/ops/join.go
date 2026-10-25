package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// joinCommand is the shape join and unjoin share: a subject, a task or a
// folder, then the folders it joins, at least one, as the words after
// it. With add set, it writes the edges of joining each folder, making
// any gate named; else it removes them, never making a gate.
func joinCommand(name string, add bool) *cli.Command {
	return &cli.Command{
		Name:    name,
		Subject: &cli.Option{Field: "ref", Required: true, Complete: tasksOnly.complete()},
		Object:  &cli.Option{Field: "join", Kind: cli.List, Arg: "folder", Required: true, Complete: foldersOnly.complete()},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			// A gate, made or not, or the root: the shape check
			// refuses material.
			ref := r.ref("ref", core.WantRef, "")
			if ref.End != core.EndNone {
				r.fault("/ref", "%q: a gate joins no folder; the subject is a task or a folder", ref.Raw)
			}
			if ref.IsRoot() {
				r.fault("/ref", "%q: the root has no gates, so it joins no folder", ref.Raw)
			}
			if v, ok := r.required("join"); ok {
				if list, isList := v.([]any); isList && len(list) == 0 {
					r.fault("/join", "at least one folder is needed")
				}
			}
			joins := r.joinRefs("join")
			refs, pointers := []core.Ref{ref}, []string{"/ref"}
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
				id, start, done, err := w.member(ref, add)
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

// Join joins a task or a folder to each folder given. A task waits on
// the folder's start gate, and the folder's done gate waits on it; a
// folder joins through its gates. Any gate named is made, the subject's
// first, then each folder's start gate first.
func Join() *cli.Command { return joinCommand("join", true) }

// Unjoin removes a join's two edges for each folder given. It never
// makes a gate, the subject's included: an edge to one a folder lacks
// isn't there.
func Unjoin() *cli.Command { return joinCommand("unjoin", false) }
