package ops

import (
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// FolderArchive moves a finished folder to the archive with everything
// under it, gates, notes and material included: its directory, renamed
// @<id>_<name>, with its JSON beside it, under directories named after
// the folders it was in.
func FolderArchive() *cli.Command {
	return &cli.Command{
		Name:    "folder archive",
		Subject: &cli.Option{Field: "folder", Required: true, Complete: finishedOnes.complete()},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("folder", core.WantFolder, "")
			if ref.IsRoot() {
				r.fault("/folder", "the root can't be archived")
			}
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
				l := leaving{id: id, kind: "folder", path: t.paths[id], folders: append([]int64{id}, t.under(id, true)...), dir: true}
				var open []int64
				for _, tid := range t.order {
					if t.in(tid, id, true) {
						l.tasks = append(l.tasks, tid)
						if t.Open(tid) {
							open = append(open, tid)
						}
					}
				}
				if len(open) > 0 {
					slices.Sort(open)
					return nil, nil, core.Conflict(core.RuleNotFinished, open, nil,
						"%s under @%d %s open; complete them before archiving it", core.FormatIDs(open), id, isAre(len(open)))
				}
				row := t.folders.rows[id]
				above, err := archiveAbove(tx, s.Root, t.paths[row.Parent])
				if err != nil {
					return nil, nil, err
				}
				name := "@" + strconv.FormatInt(id, 10) + "_" + row.Name
				l.from = fsstep.FolderDir(l.path)
				l.to, l.file = path.Join(above, name), path.Join(above, name+".json")
				// Everything under it keeps its place in the directory, so
				// each is indexed there, whether or not it is on disk.
				l.index = map[int64]string{}
				for _, f := range l.folders {
					l.index[f] = l.to + strings.TrimPrefix(fsstep.FolderDir(t.paths[f]), l.from)
				}
				for _, tid := range l.tasks {
					l.index[tid] = l.to + strings.TrimPrefix(t.notesRel(tid), l.from)
				}
				return t.archive(tx, l)
			})
		},
	}
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
