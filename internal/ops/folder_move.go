package ops

import (
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// FolderMoveResult is folder move's result, in its schema's order.
type FolderMoveResult struct {
	ID      int64      `json:"id"`
	Changed bool       `json:"changed"`
	Path    string     `json:"path"`
	Dir     string     `json:"dir"`
	Created []FolderAt `json:"created"`
}

// FolderMove moves or renames a folder to its whole new path, everything
// under it included, material too, making missing parents as folder
// create makes them. A case-only rename is a move, and the one input
// allowed to name both spellings, and only of the folder's own name.
func FolderMove() *cli.Command {
	return &cli.Command{
		Name:    "folder move",
		Subject: &cli.Option{Field: "folder", Required: true},
		Options: []cli.Option{{Field: "to", Kind: cli.String}},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("folder", core.WantFolder, "")
			if ref.IsRoot() {
				r.fault("/folder", "the root can't be moved")
			}
			dest := r.path("to")
			r.sameCase([]core.Ref{ref, dest}, []string{"/folder", "/to"}, respells)
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
				n := len(dest.Names)
				parent, missing, err := walkToMove(tx, id, dest)
				if err != nil {
					return nil, nil, err
				}
				name, path := dest.Names[n-1], dest.TargetString()
				if len(missing) == 0 {
					if at, ok := tx.Child(parent, name); ok && at != id {
						return nil, nil, core.Conflict(core.RuleExists, nil, []string{path}, "%s is taken by @%d", path, at)
					}
					if at, spelled, ok := tx.ChildFold(parent, name); ok && at != id {
						is := strings.TrimSuffix(tx.Path(parent), "/") + "/" + spelled
						return nil, nil, core.Conflict(core.RuleCaseMismatch, []int64{at}, []string{path, is},
							"%s differs from %s only in case", path, is)
					}
				}
				was := tx.Path(id)
				out := FolderMoveResult{ID: id, Path: path, Dir: fsstep.Abs(s.Root, fsstep.FolderDir(path)), Created: []FolderAt{}}
				if len(missing) == 0 && parent == row.Parent && name == row.Name {
					out.Path, out.Dir = was, fsstep.Abs(s.Root, fsstep.FolderDir(was))
					return out, nil, nil
				}
				// Every missing parent is a destination, and so is the
				// folder's new path; a case-only rename's own directory is
				// not in its way.
				above := strings.TrimSuffix(tx.Path(parent), "/")
				var paths []string
				for _, m := range missing {
					above += "/" + m
					paths = append(paths, above)
				}
				own := ""
				if parent == row.Parent {
					own = fsstep.FolderDir(was)
				}
				for _, p := range append(paths, path) {
					entry, taken, err := fsstep.TakenBeside(s.Root, p, own)
					if err != nil {
						dir := fsstep.Abs(s.Root, fsstep.FolderDir(p[:strings.LastIndex(p, "/")]))
						return nil, nil, store.Err(cli.IO("could not list "+dir+": "+err.Error(), dir))
					}
					if taken {
						at := fsstep.Abs(s.Root, entry)
						return nil, nil, core.Conflict(core.RuleExists, nil, []string{at}, "%s is taken on disk by %s", p, at)
					}
				}
				for i, m := range missing {
					made, err := tx.Issue(core.NodeFolder)
					if err != nil {
						return nil, nil, err
					}
					if err := tx.InsertFolder(store.FolderRow{ID: made, Parent: parent, Name: m, Extra: []byte("{}")}); err != nil {
						return nil, nil, err
					}
					if _, err := fsstep.Add(tx, fsstep.Step{Op: fsstep.OpMkdir, To: fsstep.FolderDir(paths[i])}); err != nil {
						return nil, nil, err
					}
					out.Created = append(out.Created, FolderAt{ID: &made, Path: paths[i]})
					parent = made
				}
				if err := tx.SetFolderPlace(id, parent, name); err != nil {
					return nil, nil, err
				}
				step := fsstep.Step{Op: fsstep.OpMove, From: fsstep.FolderDir(was), To: fsstep.FolderDir(path), Dir: true}
				if _, err := fsstep.Add(tx, step); err != nil {
					return nil, nil, err
				}
				out.Changed = true
				// Every task under it, gates included, has new notes.
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, nil, err
				}
				var ids []int64
				var notes []string
				for tid := range t.claims {
					if t.in(tid, id, true) {
						ids = append(ids, tid)
					}
				}
				slices.Sort(ids)
				for _, tid := range ids {
					notes = append(notes, fsstep.Abs(s.Root, t.notesRel(tid)))
				}
				warnings, err := movedClaimed(tx, ids, notes)
				return out, warnings, err
			})
		},
	}
}

// walkToMove follows the parents of a folder's new path from the root,
// one exact name at a time, and gives the last folder there and the names
// still to make. A part that is the folder itself is into-itself; one
// that matches a folder only ignoring case is case-mismatch.
func walkToMove(tx *store.Tx, self int64, dest core.Ref) (int64, []string, error) {
	names := dest.Names[:len(dest.Names)-1]
	at, given := core.RootID, ""
	for i, name := range names {
		given += "/" + name
		if id, ok := tx.Child(at, name); ok {
			if id == self {
				return 0, nil, core.Conflict(core.RuleIntoItself, []int64{self}, []string{dest.TargetString()},
					"@%d would land inside itself at %s", self, dest.TargetString())
			}
			at = id
			continue
		}
		if id, spelled, ok := tx.ChildFold(at, name); ok {
			is := strings.TrimSuffix(tx.Path(at), "/") + "/" + spelled
			return 0, nil, core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{given, is},
				"%s differs from %s only in case", given, is)
		}
		return at, names[i:], nil
	}
	return at, nil, nil
}

// respells reports whether a destination only respells its subject's own
// name: the same path but for the case of its last name.
func respells(subject, dest core.Ref) bool {
	n := len(subject.Names)
	return subject.Kind == core.TargetPath && dest.Kind == core.TargetPath && n > 0 && len(dest.Names) == n &&
		slices.Equal(subject.Names[:n-1], dest.Names[:n-1]) && strings.EqualFold(subject.Names[n-1], dest.Names[n-1])
}
