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
		Subject: &cli.Option{Field: "folder", Required: true, Complete: foldersOnly.complete()},
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
			var making bool
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				id, err := resolveFolder(tx, ref, "folder")
				if err != nil {
					return nil, err
				}
				f, err := loadFolders(tx, s.Root)
				if err != nil {
					return nil, err
				}
				res.Folder = f.record(id)
				if id != core.RootID {
					if making, err = fsstep.Makes(tx, fsstep.FolderNotesPath(res.Folder.Path)); err != nil {
						return nil, err
					}
				}
				return recordPaths(Cut[Folder]{Record: res.Folder}), nil
			})
			if e != nil {
				return nil, nil, e
			}
			if res.Folder.ID != nil && !making {
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

// resolveFolder resolves a folder-ref, given at field, to a folder, or
// the root's RootID.
func resolveFolder(tx *store.Tx, ref core.Ref, field string) (int64, error) {
	res, err := resolve(tx, ref, core.WantFolder, field)
	if err != nil {
		return 0, err
	}
	if res.Kind == core.ResolvedRoot {
		return core.RootID, nil
	}
	return res.ID, nil
}

// resolve resolves a reference given at field. A refusal that is
// invalid-input, such as a path anchored at a task, points at the field,
// as every invalid-input names its faults.
func resolve(tx *store.Tx, ref core.Ref, want core.Want, field string) (core.Resolved, error) {
	return resolveAt(tx, ref, want, cli.Pointer(field))
}

// resolveAt is resolve for a reference at a JSON Pointer, such as an
// entry of a list.
func resolveAt(tx *store.Tx, ref core.Ref, want core.Want, at string) (core.Resolved, error) {
	res, err := core.Resolve(tx, ref, want, nil)
	if e, ok := err.(*core.Error); ok && e.Kind == core.KindInvalidInput && len(e.Problems) == 0 {
		e.Problems = []string{at}
	}
	return res, err
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
