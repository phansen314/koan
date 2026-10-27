package ops

import (
	"path/filepath"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// ShowResult is show's result, in its schema's order: the task's
// record or the folder's, by what the reference names.
type ShowResult struct {
	Task   *Task   `json:"task,omitempty"`
	Folder *Folder `json:"folder,omitempty"`
}

// Show reads one task, a gate included, or one folder, or the root.
func Show() *cli.Command {
	return &cli.Command{
		Name:    "show",
		Subject: &cli.Option{Field: "ref", Holds: "the reference", Required: true, Complete: anyRef.complete()},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			ref := r.ref("ref", core.WantRef, "")
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			var res ShowResult
			var id *int64
			var notes string
			var making bool
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				got, err := resolve(tx, ref, core.WantRef, "ref")
				if err != nil {
					return nil, err
				}
				if got.Kind == core.ResolvedTask {
					t, err := loadTasks(tx, s.Root)
					if err != nil {
						return nil, err
					}
					rec := t.record(got.ID)
					res.Task, id, notes = &rec, &rec.ID, t.notesRel(got.ID)
					if making, err = fsstep.Makes(tx, notes); err != nil {
						return nil, err
					}
					return taskPaths(Cut[Task]{Record: rec}), nil
				}
				folder := got.ID
				if got.Kind == core.ResolvedRoot {
					folder = core.RootID
				}
				f, err := loadFolders(tx, s.Root)
				if err != nil {
					return nil, err
				}
				rec := f.record(folder)
				res.Folder, id = &rec, rec.ID
				if id != nil {
					notes = fsstep.FolderNotesPath(rec.Path)
					if making, err = fsstep.Makes(tx, notes); err != nil {
						return nil, err
					}
				}
				return recordPaths(Cut[Folder]{Record: rec}), nil
			})
			if e != nil {
				return nil, nil, e
			}
			if id != nil && !making {
				w, e := missingNotes(s.Root, *id, notes)
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
// state. Its writes are logged as env's command.
func openStore(env *cli.Env) (*store.Store, *cli.Error) {
	home, e := env.Home()
	if e != nil {
		return nil, e
	}
	s, e := fsstep.Open(home)
	if e != nil {
		return nil, e
	}
	s.Command = env.Command
	return s, nil
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
// invalid-input, such as a path anchored at a task, or not-found points at
// the field, as every invalid-input names its faults.
func resolve(tx *store.Tx, ref core.Ref, want core.Want, field string) (core.Resolved, error) {
	return resolveAt(tx, ref, want, cli.Pointer(field))
}

// resolveAt is resolve for a reference at a JSON Pointer, such as an
// entry of a list.
func resolveAt(tx *store.Tx, ref core.Ref, want core.Want, at string) (core.Resolved, error) {
	res, err := core.Resolve(tx, ref, want, nil)
	if e, ok := err.(*core.Error); ok && (e.Kind == core.KindInvalidInput || e.Kind == core.KindNotFound) && len(e.Problems) == 0 {
		e.Problems = []string{at}
	}
	return res, err
}

// pointNotFound points a not-found at the field it came from, unless it
// already points at one.
func pointNotFound(err error, at string) error {
	if e, ok := err.(*core.Error); ok && e.Kind == core.KindNotFound && len(e.Problems) == 0 {
		e.Problems = []string{at}
	}
	return err
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
		Message: "there is no file at " + at + "; it reads as empty notes",
		IDs:     []int64{id},
		Paths:   []string{at},
	}}, nil
}
