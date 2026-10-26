package ops

import (
	"fmt"
	"path"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// DeleteResult is delete's result, in its schema's order.
type DeleteResult struct {
	Deleted   []int64 `json:"deleted"`
	Removed   []int64 `json:"removed"`
	Unclaimed []int64 `json:"unclaimed"`
	Released  []int64 `json:"released"`
}

// Delete removes tasks and folders, mixed, as one change: a task with its
// notes, its claim and every edge to and from it, and a folder with
// everything under it, gates included. It never splices: what waited on
// them isn't passed their blockers, and a task deleted with them is never
// released. A folder holding tasks or folders other than its gates needs
// contents, and one holding material, at any depth, needs material.
func Delete() *cli.Command {
	return &cli.Command{
		Name:    "delete",
		Subject: &cli.Option{Field: "refs", Kind: cli.List, Arg: "ref", Required: true, Complete: anyRef.complete()},
		Options: []cli.Option{
			{Field: "contents", Kind: cli.Bool},
			{Field: "material", Kind: cli.Bool},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			refs, at := r.subjectRefs("deleted")
			r.sameCase(refs, at)
			contents := r.boolean("contents", false)
			material := r.boolean("material", false)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				subs, err := resolveSubjects(tx, refs, at, "deleted")
				if err != nil {
					return nil, nil, err
				}
				if err := within(tx, subs, at); err != nil {
					return nil, nil, err
				}
				// Options only a folder takes.
				if _, ok := firstOf(subs, true); !ok {
					k := input{}
					// Given only when it does something: false is
					// what leaving it out does.
					for _, o := range []struct {
						field string
						set   bool
					}{{"contents", contents}, {"material", material}} {
						if o.set {
							k.faultIDs(cli.Pointer(o.field), idsOf(subs, false), "%s is for folders, and no folder is given", o.field)
						}
					}
					if e := k.err(); e != nil {
						return nil, nil, store.Err(e)
					}
				}
				var given, ids []int64
				for _, sub := range subs {
					if sub.folder {
						ids = append(ids, sub.id)
					} else {
						given = append(given, sub.id)
					}
				}
				// Tasks alone need no walk of the tree.
				t := &tasks{folders: &folders{}}
				if len(ids) > 0 {
					if t, err = loadTasks(tx, s.Root); err != nil {
						return nil, nil, err
					}
				}
				// under[i] is every folder under the one at ids[i], itself
				// first.
				under := make([][]int64, len(ids))
				for i, id := range ids {
					under[i] = append([]int64{id}, t.under(id, false)...)
				}
				if !contents {
					var all []int64
					for _, id := range ids {
						var direct []int64
						for tid, row := range t.rows {
							if row.Folder == id && row.Gate == core.EndNone {
								direct = append(direct, tid)
							}
						}
						direct = append(direct, t.under(id, true)...)
						all = append(all, direct...)
					}
					if len(all) > 0 {
						slices.Sort(all)
						all = slices.Compact(all)
						msg := fmt.Sprintf("%s held by the folders given; give contents to delete them with it", core.FormatIDs(all))
						if len(ids) == 1 {
							msg = fmt.Sprintf("@%d holds %s; give contents to delete them with it", ids[0], core.FormatIDs(all))
						}
						return nil, nil, core.Conflict(core.RuleNotEmpty, all, nil, "%s", msg)
					}
				}
				tasks := slices.Clone(given)
				var fs []int64
				var steps []fsstep.Step
				for _, id := range given {
					row, _, err := tx.Task(id)
					if err != nil {
						return nil, nil, err
					}
					steps = append(steps, fsstep.Step{Op: fsstep.OpRemove, From: fsstep.TaskNotesPath(tx.Path(row.Folder), id, row.Gate)})
				}
				var found []string
				var holders []int64
				for i, id := range ids {
					var mine []int64
					for _, tid := range t.order {
						if t.in(tid, id, false) {
							mine = append(mine, tid)
						}
					}
					// What the step may remove: the directories of the
					// folders being deleted, and the notes of what is being
					// deleted. A gate's notes come with its gate, among the
					// tasks; a @start.md or @done.md with no gate is a stray
					// entry.
					var owns []string
					for _, f := range under[i] {
						dir := fsstep.FolderDir(t.paths[f])
						owns = append(owns, dir, path.Join(dir, fsstep.FolderNotes))
					}
					for _, tid := range mine {
						owns = append(owns, t.notesRel(tid))
					}
					if !material {
						had := len(found)
						for _, f := range under[i] {
							dir := fsstep.FolderDir(t.paths[f])
							names, err := fsstep.MaterialIn(s.Root, dir, owns)
							if err != nil {
								at := fsstep.Abs(s.Root, dir)
								return nil, nil, store.Err(cli.IO("could not look in "+at+": "+err.Error(), at))
							}
							for _, n := range names {
								found = append(found, t.paths[f]+":"+n)
							}
						}
						if len(found) > had {
							holders = append(holders, id)
						}
					}
					tasks, fs = append(tasks, mine...), append(fs, under[i]...)
					steps = append(steps, fsstep.Step{Op: fsstep.OpRemove, From: fsstep.FolderDir(t.paths[id]), Dir: true, Force: material, Owns: owns})
				}
				if len(found) > 0 {
					hold, with := "holds", "the folder"
					if len(holders) > 1 {
						hold, with = "hold", "them"
					}
					return nil, nil, core.Conflict(core.RuleHasMaterial, nil, found,
						"%s %s material; give material to delete it with %s", core.FormatIDs(holders), hold, with)
				}
				unclaimed := []int64{}
				for _, id := range tasks {
					claimed, err := tx.Claimed(id)
					if err != nil {
						return nil, nil, err
					}
					if claimed {
						unclaimed = append(unclaimed, id)
					}
				}
				slices.Sort(unclaimed)
				released, err := tx.RemoveTasks(tasks, nil)
				if err != nil {
					return nil, nil, err
				}
				// Each folder goes before its parent.
				gone := slices.Clone(fs)
				slices.Reverse(gone)
				if err := tx.RemoveFolders(gone, nil); err != nil {
					return nil, nil, err
				}
				for _, step := range steps {
					if _, err := fsstep.Add(tx, step); err != nil {
						return nil, nil, err
					}
				}
				removed := slices.Concat(tasks, fs)
				slices.Sort(removed)
				deleted := []int64{}
				for _, sub := range subs {
					deleted = append(deleted, sub.id)
				}
				return DeleteResult{Deleted: deleted, Removed: removed, Unclaimed: unclaimed, Released: nonNil(released)}, nil, nil
			})
		},
	}
}
