package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// FolderJoin joins a folder to each folder given, through its gates: its
// start gate waits on that folder's start gate, and that folder's done
// gate waits on its done gate. Any gate named is made, its own first.
func FolderJoin() *cli.Command {
	return joinCommand("folder join", cli.Option{Field: "folder"}, func(env *cli.Env, folder core.Ref, in []joinIn) (any, []cli.Warning, *cli.Error) {
		s, e := openStore(env)
		if e != nil {
			return nil, nil, e
		}
		defer s.Close()
		return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
			id, err := resolveFolder(tx, folder, "folder")
			if err != nil {
				return nil, nil, err
			}
			w := newEdges(tx)
			start, done, err := w.gates(id, true)
			if err != nil {
				return nil, nil, err
			}
			es, err := w.join(start, done, in, true)
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

// FolderUnjoin removes a folder join's two edges for each folder given.
// It never makes a gate, the subject's included.
func FolderUnjoin() *cli.Command {
	return joinCommand("folder unjoin", cli.Option{Field: "folder"}, func(env *cli.Env, folder core.Ref, in []joinIn) (any, []cli.Warning, *cli.Error) {
		s, e := openStore(env)
		if e != nil {
			return nil, nil, e
		}
		defer s.Close()
		return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
			id, err := resolveFolder(tx, folder, "folder")
			if err != nil {
				return nil, nil, err
			}
			w := newEdges(tx)
			start, done, err := w.gates(id, false)
			if err != nil {
				return nil, nil, err
			}
			es, err := w.join(start, done, in, false)
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
