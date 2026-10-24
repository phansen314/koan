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

// FolderDelete removes folders and everything under them, gates
// included, as task delete removes tasks, as one change. One holding
// tasks or folders other than its gates needs contents, and one holding
// material, at any depth, needs material.
func FolderDelete() *cli.Command {
	return &cli.Command{
		Name:    "folder delete",
		Subject: &cli.Option{Field: "folders", Kind: cli.List, Arg: "folder", Required: true, Complete: foldersOnly.complete()},
		Options: []cli.Option{
			{Field: "contents", Kind: cli.Bool},
			{Field: "material", Kind: cli.Bool},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			if v, ok := r.required("folders"); ok {
				if list, isList := v.([]any); isList && len(list) == 0 {
					r.fault("/folders", "at least one folder is needed")
				}
			}
			refs := r.refs("folders", core.WantFolder)
			at := make([]string, len(refs))
			for i, ref := range refs {
				at[i] = fmt.Sprintf("/folders/%d", i)
				if ref.IsRoot() {
					r.fault(at[i], "the root can't be deleted")
				}
			}
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
				var ids []int64
				for i, ref := range refs {
					res, err := resolveAt(tx, ref, core.WantFolder, at[i])
					if err != nil {
						return nil, nil, err
					}
					if res.Kind == core.ResolvedRoot {
						return nil, nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{at[i]},
							Message: "the root can't be deleted"}
					}
					if slices.Contains(ids, res.ID) {
						return nil, nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{at[i]},
							Message: fmt.Sprintf("@%d is given twice", res.ID)}
					}
					ids = append(ids, res.ID)
				}
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, nil, err
				}
				// under[i] is every folder under the one given at i, itself
				// first; a folder given inside another given is refused.
				under := make([][]int64, len(ids))
				var inside *core.Error
				for i, id := range ids {
					under[i] = append([]int64{id}, t.under(id, true)...)
				}
				for i, id := range ids {
					for j, other := range ids {
						if i != j && slices.Contains(under[j], id) {
							if inside == nil {
								inside = &core.Error{Kind: core.KindInvalidInput,
									Message: fmt.Sprintf("@%d is inside @%d, also given", id, other)}
							}
							inside.Problems = append(inside.Problems, at[i])
							break
						}
					}
				}
				if inside != nil {
					return nil, nil, inside
				}
				if !contents {
					for _, id := range ids {
						var direct []int64
						for tid, row := range t.rows {
							if row.Folder == id && row.Gate == core.EndNone {
								direct = append(direct, tid)
							}
						}
						direct = append(direct, t.under(id, false)...)
						if len(direct) > 0 {
							slices.Sort(direct)
							return nil, nil, core.Conflict(core.RuleNotEmpty, direct, nil,
								"@%d holds %s; give contents to delete them with it", id, core.FormatIDs(direct))
						}
					}
				}
				var tasks, folders []int64
				var steps []fsstep.Step
				var found []string
				var holders []int64
				for i, id := range ids {
					var mine []int64
					for _, tid := range t.order {
						if t.in(tid, id, true) {
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
					tasks, folders = append(tasks, mine...), append(folders, under[i]...)
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
				released, err := tx.RemoveTasks(tasks, nil)
				if err != nil {
					return nil, nil, err
				}
				// Each folder goes before its parent.
				gone := slices.Clone(folders)
				slices.Reverse(gone)
				if err := tx.RemoveFolders(gone, nil); err != nil {
					return nil, nil, err
				}
				for _, step := range steps {
					if _, err := fsstep.Add(tx, step); err != nil {
						return nil, nil, err
					}
				}
				removed := slices.Concat(tasks, folders)
				slices.Sort(removed)
				return DeleteResult{Removed: removed, Released: nonNil(released)}, nil, nil
			})
		},
	}
}
