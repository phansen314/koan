package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
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
		Name: "folder list",
		Options: []cli.Option{
			{Field: "folder", Kind: cli.String},
			{Field: "recursive", Kind: cli.Bool},
			{Field: "limit", Kind: cli.Integer, Nullable: true},
			{Field: "fields", Kind: cli.List},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("folder", core.WantFolder, "/")
			recursive := r.boolean("recursive", true)
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
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				id, err := resolveFolder(tx, ref)
				if err != nil {
					return nil, err
				}
				f, err := loadFolders(tx, s.Root)
				if err != nil {
					return nil, err
				}
				matches := f.under(id, recursive)
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
			return res, warnings, nil
		},
	}
}
