package ops

import (
	"path"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// FolderDelete removes a folder and everything under it, gates included,
// as a task delete removes a task. One holding tasks or folders other
// than its gates needs contents, and one holding material, at any depth,
// needs force.
func FolderDelete() *cli.Command {
	return &cli.Command{
		Name:    "folder delete",
		Subject: &cli.Option{Field: "folder", Required: true},
		Options: []cli.Option{
			{Field: "contents", Kind: cli.Bool},
			{Field: "force", Kind: cli.Bool},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("folder", core.WantFolder, "")
			if ref.IsRoot() {
				r.fault("/folder", "the root can't be deleted")
			}
			contents := r.boolean("contents", false)
			force := r.boolean("force", false)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				id, err := resolveFolder(tx, ref, "folder")
				if err != nil {
					return nil, nil, err
				}
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, nil, err
				}
				if !contents {
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
				folders := append([]int64{id}, t.under(id, true)...)
				var tasks []int64
				for _, tid := range t.order {
					if t.in(tid, id, true) {
						tasks = append(tasks, tid)
					}
				}
				// What the step may remove: the directories of the folders
				// being deleted, and the notes of what is being deleted.
				var owns []string
				for _, f := range folders {
					dir := fsstep.FolderDir(t.paths[f])
					owns = append(owns, dir, path.Join(dir, fsstep.FolderNotes),
						path.Join(dir, fsstep.StartNotes), path.Join(dir, fsstep.DoneNotes))
				}
				for _, tid := range tasks {
					if n := t.notesRel(tid); !slices.Contains(owns, n) {
						owns = append(owns, n)
					}
				}
				if !force {
					var material []string
					for _, f := range folders {
						dir := fsstep.FolderDir(t.paths[f])
						names, err := fsstep.MaterialIn(s.Root, dir, owns)
						if err != nil {
							at := fsstep.Abs(s.Root, dir)
							return nil, nil, store.Err(cli.IO("could not look in "+at+": "+err.Error(), at))
						}
						for _, n := range names {
							material = append(material, t.paths[f]+":"+n)
						}
					}
					if len(material) > 0 {
						return nil, nil, core.Conflict(core.RuleHasMaterial, nil, material,
							"@%d holds material; give force to delete it with the folder", id)
					}
				}
				released, err := tx.RemoveTasks(tasks, nil)
				if err != nil {
					return nil, nil, err
				}
				// Each folder goes before its parent.
				gone := slices.Clone(folders)
				slices.Reverse(gone)
				if err := tx.RemoveFolders(gone); err != nil {
					return nil, nil, err
				}
				step := fsstep.Step{Op: fsstep.OpRemove, From: fsstep.FolderDir(t.paths[id]), Dir: true, Force: force, Owns: owns}
				if _, err := fsstep.Add(tx, step); err != nil {
					return nil, nil, err
				}
				removed := slices.Concat(tasks, folders)
				slices.Sort(removed)
				return DeleteResult{Removed: removed, Released: nonNil(released)}, nil, nil
			})
		},
	}
}
