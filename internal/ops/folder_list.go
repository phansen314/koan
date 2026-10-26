package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// FolderListResult is folder list's result, in its schema's order.
type FolderListResult struct {
	Folders   []Cut[Folder] `json:"folders"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated"`
}

// FolderList lists the folders under a folder, itself left out, in tree
// order.
func FolderList() *cli.Command {
	return &cli.Command{
		Name:    "folder list",
		Subject: &cli.Option{Field: "folder", Complete: foldersOnly.complete()},
		Options: []cli.Option{
			{Field: "file", Kind: cli.Path},
			{Field: "direct", Kind: cli.Bool},
			{Field: "limit", Kind: cli.Integer, Nullable: true},
			{Field: "fields", Kind: cli.List},
		},
		Here: &cli.Here{Field: "file", Flag: "--here"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			q := r.foldering("folder")
			direct := r.boolean("direct", false)
			limit := r.limit("limit", 100)
			fields := r.fields("fields", folderFields)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			res := FolderListResult{Folders: []Cut[Folder]{}}
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
				f, err := loadFolders(tx, s.Root)
				if err != nil {
					return nil, err
				}
				matches := f.under(id, direct)
				res.Total = len(matches)
				if limit != nil && int64(len(matches)) > *limit {
					matches, res.Truncated = matches[:*limit], true
				}
				var paths []fsstep.Path
				for _, m := range matches {
					c := Cut[Folder]{Record: f.record(m), Fields: fields}
					res.Folders = append(res.Folders, c)
					paths = append(paths, recordPaths(c)...)
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
