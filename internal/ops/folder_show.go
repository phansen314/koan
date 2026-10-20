package ops

import (
	"path/filepath"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// FolderShowResult is folder show's result, in its schema's order.
type FolderShowResult struct {
	Folder Folder `json:"folder"`
}

// FolderShow reads one folder, or the root.
func FolderShow() *cli.Command {
	return &cli.Command{
		Name:    "folder show",
		Subject: &cli.Option{Field: "folder", Required: true},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("folder", core.WantFolder, "")
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			var res FolderShowResult
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				id, err := resolveFolder(tx, ref)
				if err != nil {
					return nil, err
				}
				f, err := loadFolders(tx, s.Root)
				if err != nil {
					return nil, err
				}
				res.Folder = f.record(id)
				return recordPaths(Cut[Folder]{Record: res.Folder}), nil
			})
			if e != nil {
				return nil, nil, e
			}
			if res.Folder.ID != nil {
				w, e := missingNotes(s.Root, *res.Folder.ID, fsstep.FolderNotesPath(res.Folder.Path))
				if e != nil {
					return nil, nil, e
				}
				warnings = append(warnings, w...)
			}
			return res, warnings, nil
		},
	}
}

// openStore opens the store of the root under HOME, checking the root's
// state.
func openStore(env *cli.Env) (*store.Store, *cli.Error) {
	home, e := env.Home()
	if e != nil {
		return nil, e
	}
	return fsstep.Open(home)
}

// resolveFolder resolves a folder-ref to a folder, or the root's RootID.
func resolveFolder(tx *store.Tx, ref core.Ref) (int64, error) {
	res, err := core.Resolve(tx, ref, core.WantFolder, nil)
	if err != nil {
		return 0, err
	}
	if res.Kind == core.ResolvedRoot {
		return core.RootID, nil
	}
	return res.ID, nil
}

// missingNotes is the missing-notes warning for notes with no file at
// their path, judged by listing the directory they go in.
func missingNotes(root store.Root, id int64, rel string) ([]cli.Warning, *cli.Error) {
	missing, err := fsstep.Missing(root, rel)
	at := fsstep.Abs(root, rel)
	if err != nil {
		dir := filepath.Dir(at)
		return nil, cli.IO("could not list "+dir+": "+err.Error(), dir)
	}
	if !missing {
		return nil, nil
	}
	return []cli.Warning{{
		Kind:    "missing-notes",
		Message: "There is no file at " + at + ". It reads as empty notes.",
		IDs:     []int64{id},
		Paths:   []string{at},
	}}, nil
}
