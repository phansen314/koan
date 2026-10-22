package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// TaskMoveResult is task move's result, in its schema's order.
type TaskMoveResult struct {
	ID        int64  `json:"id"`
	Changed   bool   `json:"changed"`
	NotesPath string `json:"notes_path"`
}

// TaskMove puts a task in another folder or the root; its notes move with
// it. No edge changes, so no readiness does. A gate stays in its folder.
func TaskMove() *cli.Command {
	return &cli.Command{
		Name:    "task move",
		Subject: &cli.Option{Field: "task", Required: true, Complete: tasksOnly.complete()},
		Options: []cli.Option{{Field: "to", Kind: cli.String, Complete: foldersOnly.complete()}},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("task", core.WantTask, "")
			to := r.ref("to", core.WantFolder, "")
			r.sameCase([]core.Ref{ref, to}, []string{"/task", "/to"})
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
				folder, err := resolveFolder(tx, to, "to")
				if err != nil {
					return nil, nil, err
				}
				id := res.ID
				row, _, err := tx.Task(id)
				if err != nil {
					return nil, nil, err
				}
				if row.Gate != core.EndNone {
					return nil, nil, core.Conflict(core.RuleGate, []int64{id}, nil, "@%d is a gate, and stays in its folder", id)
				}
				from := fsstep.TaskNotesPath(tx.Path(row.Folder), id, core.EndNone)
				notes := fsstep.TaskNotesPath(tx.Path(folder), id, core.EndNone)
				out := TaskMoveResult{ID: id, NotesPath: fsstep.Abs(s.Root, notes)}
				if folder == row.Folder {
					return out, nil, nil
				}
				if err := tx.SetTaskFolder(id, folder); err != nil {
					return nil, nil, err
				}
				if _, err := fsstep.Add(tx, fsstep.Step{Op: fsstep.OpMove, From: from, To: notes}); err != nil {
					return nil, nil, err
				}
				out.Changed = true
				warnings, err := movedClaimed(tx, []int64{id}, []string{out.NotesPath})
				return out, warnings, err
			})
		},
	}
}

// movedClaimed is the moved-claimed warning for the claimed tasks among
// those a move gave new notes paths, each beside its new path.
func movedClaimed(tx *store.Tx, ids []int64, paths []string) ([]cli.Warning, error) {
	var claimed []int64
	var at []string
	for i, id := range ids {
		c, err := tx.Claimed(id)
		if err != nil {
			return nil, err
		}
		if c {
			claimed, at = append(claimed, id), append(at, paths[i])
		}
	}
	if len(claimed) == 0 {
		return nil, nil
	}
	message := core.FormatIDs(claimed) + " is claimed, and its notes moved; whoever holds it should read them at the new path."
	if len(claimed) > 1 {
		message = core.FormatIDs(claimed) + " are claimed, and their notes moved; whoever holds them should read them at the new paths."
	}
	return []cli.Warning{{Kind: "moved-claimed", Message: message, IDs: claimed, Paths: at}}, nil
}
