package ops

import (
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// ExportResult is export's result, in its schema's order.
type ExportResult struct {
	Folder   Folder   `json:"folder"`
	Folders  []Folder `json:"folders"`
	Tasks    []Task   `json:"tasks"`
	External []Task   `json:"external"`
	Edges    []Edge   `json:"edges"`
}

// Export gives a folder's whole subtree in one read transaction, for a
// tool to turn into a graph: its folders and every task under it, the
// tasks outside it at the other end of an edge, and the edges. It reads
// only the database, so material never appears and no notes are checked.
func Export() *cli.Command {
	return &cli.Command{
		Name:    "export",
		Subject: &cli.Option{Field: "folder", Complete: foldersOnly.complete()},
		Options: []cli.Option{{Field: "file", Kind: cli.Path}},
		Here:    &cli.Here{Field: "file", Flag: "--here"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			q := r.foldering("folder")
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			res := ExportResult{Folders: []Folder{}, Tasks: []Task{}, External: []Task{}}
			var stale []cli.Warning
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, err
				}
				var id int64
				if id, _, stale, err = subjectFolder(tx, t, q); err != nil {
					return nil, err
				}
				res.Folder = t.folders.record(id)
				paths := recordPaths(Cut[Folder]{Record: res.Folder})
				for _, f := range t.folders.under(id, false) {
					rec := t.folders.record(f)
					res.Folders = append(res.Folders, rec)
					paths = append(paths, recordPaths(Cut[Folder]{Record: rec})...)
				}
				under := map[int64]bool{}
				for _, tid := range t.order {
					if t.in(tid, id, false) {
						under[tid] = true
						rec := t.record(tid)
						res.Tasks = append(res.Tasks, rec)
						paths = append(paths, taskPaths(Cut[Task]{Record: rec})...)
					}
				}
				res.Edges = edgesTouching(t, under)
				var outside []int64
				for _, e := range res.Edges {
					for _, end := range []int64{e.Task, e.Blocker} {
						if !under[end] {
							outside = append(outside, end)
						}
					}
				}
				slices.Sort(outside)
				for _, tid := range slices.Compact(outside) {
					rec := t.record(tid)
					res.External = append(res.External, rec)
					paths = append(paths, taskPaths(Cut[Task]{Record: rec})...)
				}
				return paths, nil
			})
			if e != nil {
				return nil, nil, e
			}
			return res, append(warnings, stale...), nil
		},
	}
}
