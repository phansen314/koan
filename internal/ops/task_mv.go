package ops

import (
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// TaskMvResult is task mv's result, in its schema's order.
type TaskMvResult struct {
	Moved []TaskMoved `json:"moved"`
}

// TaskMoved is one task that moved, with its new notes path.
type TaskMoved struct {
	ID        int64  `json:"id"`
	NotesPath string `json:"notes_path"`
}

// TaskMv puts tasks in a folder or the root, as one change; their notes
// move with them. No edge changes, so no readiness does. A gate stays in
// its folder, and a task already in the folder doesn't move.
func TaskMv() *cli.Command {
	return &cli.Command{
		Name:    "task mv",
		Subject: &cli.Option{Field: "tasks", Kind: cli.List, Arg: "task", Required: true, Complete: tasksOnly.complete()},
		Object:  &cli.Option{Field: "into", Arg: "folder", Required: true, Complete: tasksAndFolders.complete()},
		Instead: "into",
		Options: []cli.Option{{Field: "into", Short: "t", Complete: foldersOnly.complete()}},
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
			into := r.ref("into", core.WantFolder, "")
			r.sameCase(append(slices.Clone(refs), into), append(at, "/into"))
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
				folder, err := resolveFolder(tx, into, "into")
				if err != nil {
					return nil, nil, err
				}
				rows := make([]store.TaskRow, len(ids))
				for i, id := range ids {
					if rows[i], _, err = tx.Task(id); err != nil {
						return nil, nil, err
					}
					if rows[i].Gate != core.EndNone {
						return nil, nil, core.Conflict(core.RuleGate, []int64{id}, nil, "@%d is a gate, and stays in its folder", id)
					}
				}
				out := TaskMvResult{Moved: []TaskMoved{}}
				var moved []int64
				var paths []string
				for i, id := range ids {
					if rows[i].Folder == folder {
						continue
					}
					from := fsstep.TaskNotesPath(tx.Path(rows[i].Folder), id, core.EndNone)
					notes := fsstep.TaskNotesPath(tx.Path(folder), id, core.EndNone)
					if err := tx.SetTaskFolder(id, folder); err != nil {
						return nil, nil, err
					}
					if _, err := fsstep.Add(tx, fsstep.Step{Op: fsstep.OpMove, From: from, To: notes}); err != nil {
						return nil, nil, err
					}
					out.Moved = append(out.Moved, TaskMoved{ID: id, NotesPath: fsstep.Abs(s.Root, notes)})
					moved, paths = append(moved, id), append(paths, fsstep.Abs(s.Root, notes))
				}
				warnings, err := movedClaimed(tx, moved, paths)
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
