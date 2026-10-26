package ops

import (
	"os"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// Brief is what context returns, in its schema's order.
type Brief struct {
	Folder    Folder   `json:"folder"`
	Ancestors []Folder `json:"ancestors"`
	Notes     *string  `json:"notes"`
	Tasks     []Task   `json:"tasks"`
	Total     int      `json:"total"`
	Truncated bool     `json:"truncated"`
	Marker    *string  `json:"marker"`
}

// Context briefs an agent on a folder, found by reference or by a path
// on disk as where finds one: its record, the folders above it, its notes
// and its own open tasks, the first limit of them.
func Context() *cli.Command {
	return &cli.Command{
		Name:    "context",
		Subject: &cli.Option{Field: "folder", Complete: foldersOnly.complete()},
		Options: []cli.Option{
			{Field: "file", Kind: cli.Path},
			{Field: "limit", Kind: cli.Integer, Nullable: true},
		},
		Here: &cli.Here{Field: "file", Flag: "--here"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			q := r.foldering("folder")
			limit := r.limit("limit", 100)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			var res Brief
			var stale []cli.Warning
			var making bool
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, err
				}
				var id int64
				if id, res.Marker, stale, err = subjectFolder(tx, t, q); err != nil {
					return nil, err
				}
				res.Folder = t.folders.record(id)
				paths := recordPaths(Cut[Folder]{Record: res.Folder})
				res.Ancestors = []Folder{}
				for p := t.folders.rows[id].Parent; id != core.RootID && p != core.RootID; p = t.folders.rows[p].Parent {
					res.Ancestors = append([]Folder{t.folders.record(p)}, res.Ancestors...)
				}
				for _, a := range res.Ancestors {
					paths = append(paths, recordPaths(Cut[Folder]{Record: a})...)
				}
				var open []int64
				for _, tid := range t.order {
					if t.rows[tid].Folder == id && t.Open(tid) {
						open = append(open, tid)
					}
				}
				res.Total = len(open)
				if limit != nil && int64(len(open)) > *limit {
					open, res.Truncated = open[:*limit], true
				}
				res.Tasks = []Task{}
				for _, tid := range open {
					rec := t.record(tid)
					res.Tasks = append(res.Tasks, rec)
					paths = append(paths, taskPaths(Cut[Task]{Record: rec})...)
				}
				if id != core.RootID {
					if making, err = fsstep.Makes(tx, fsstep.FolderNotesPath(res.Folder.Path)); err != nil {
						return nil, err
					}
				}
				return paths, nil
			})
			if e != nil {
				return nil, nil, e
			}
			warnings = append(warnings, stale...)
			if res.Folder.ID == nil {
				return res, warnings, nil
			}
			rel := fsstep.FolderNotesPath(res.Folder.Path)
			missing, err := fsstep.Missing(s.Root, rel)
			if err == nil && !missing {
				var b []byte
				b, err = os.ReadFile(fsstep.Abs(s.Root, rel))
				notes := string(b)
				res.Notes = &notes
			}
			if err != nil {
				at := fsstep.Abs(s.Root, rel)
				return nil, nil, cli.IO("could not read "+at+": "+err.Error(), at)
			}
			if missing {
				empty := ""
				res.Notes = &empty
				if !making {
					w, e := missingNotes(s.Root, *res.Folder.ID, rel)
					if e != nil {
						return nil, nil, e
					}
					warnings = append(warnings, w...)
				}
			}
			return res, warnings, nil
		},
	}
}

// subjectFolder is the folder a command such as context acts on, the root
// included: by reference, or by a path on disk as where finds one. A path
// found through a marker gives the marker.json, and stale-marker when its
// path hint is stale.
func subjectFolder(tx *store.Tx, t *tasks, q *query) (int64, *string, []cli.Warning, error) {
	if q.hasRef {
		id, err := resolveFolder(tx, q.ref, "folder")
		return id, nil, nil, err
	}
	loc, _, stale, err := q.locate(tx, t, "folder")
	if err != nil {
		return 0, nil, nil, err
	}
	id := int64(core.RootID)
	if loc.Folder.ID != nil {
		id = *loc.Folder.ID
	}
	return id, loc.Marker, stale, nil
}
