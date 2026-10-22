package ops

import (
	"bytes"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// FolderUpdate changes a folder's title or extra, as task update does. The
// root can't be updated.
func FolderUpdate() *cli.Command {
	return &cli.Command{
		Name:    "folder update",
		Subject: &cli.Option{Field: "folder", Required: true, Complete: foldersOnly.complete()},
		Options: []cli.Option{
			{Field: "title", Kind: cli.String, Nullable: true},
			{Field: "extra_merge", Kind: cli.JSON},
			{Field: "extra_remove", Kind: cli.Each},
			{Field: "extra_replace_all", Kind: cli.JSON},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("folder", core.WantFolder, "")
			if ref.IsRoot() {
				r.fault("/folder", "the root can't be updated")
			}
			r.changes("folder")
			_, setTitle := r.value("title")
			title := r.title("title")
			extra := r.extraChange()
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
				row, err := tx.Folder(id)
				if err != nil {
					return nil, nil, err
				}
				was := row
				if setTitle {
					row.Title = title
				}
				if row.Extra, err = extra.apply(row.Extra); err != nil {
					return nil, nil, err
				}
				changed := !same(row.Title, was.Title) || !bytes.Equal(row.Extra, was.Extra)
				if changed {
					if err := tx.SetFolderFields(row); err != nil {
						return nil, nil, err
					}
				}
				return TaskUpdateResult{ID: id, Changed: changed}, nil, nil
			})
		},
	}
}
