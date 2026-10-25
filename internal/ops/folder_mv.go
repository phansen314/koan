package ops

import (
	"fmt"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// FolderMvResult is folder mv's result, in its schema's order.
type FolderMvResult struct {
	Moved []FolderMoved `json:"moved"`
}

// FolderMoved is one folder that moved, at its new path.
type FolderMoved struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Dir  string `json:"dir"`
}

// FolderMv moves folders into a folder that exists, or renames one to a
// new path whose parent exists, everything under each included, material
// too, as one change. Nothing is replaced and no parent is made. A
// case-only rename is the one input allowed to name both spellings, and
// only of the folder's own name.
func FolderMv() *cli.Command {
	return &cli.Command{
		Name:    "folder mv",
		Subject: &cli.Option{Field: "folders", Kind: cli.List, Arg: "folder", Required: true, Complete: foldersOnly.complete()},
		Object:  &cli.Option{Field: "dest", Required: true, Complete: foldersOnly.complete()},
		Instead: "into",
		Single:  "rename",
		Apart:   []string{"into", "rename"},
		Options: []cli.Option{
			{Field: "into", Short: "t", Complete: foldersOnly.complete()},
			{Field: "rename", Short: "T", Kind: cli.Bool},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			if v, ok := r.required("folders"); ok {
				if list, isList := v.([]any); isList && len(list) == 0 {
					r.fault("/folders", "at least one folder is needed")
				}
			}
			refs := r.refs("folders", core.WantFolder)
			at := make([]string, len(refs))
			for i, ref := range refs {
				at[i] = fmt.Sprintf("/folders/%d", i)
				if ref.IsRoot() {
					r.fault(at[i], "the root can't be moved")
				}
			}
			rename := r.boolean("rename", false)
			_, hasDest := r.value("dest")
			_, hasInto := r.value("into")
			field := "dest"
			switch {
			case hasDest && hasInto:
				r.fault("", "dest and into can't both be given")
			case !hasDest && !hasInto:
				r.fault("", "dest or into is needed")
			case hasInto:
				field = "into"
			}
			if rename && hasInto {
				r.fault("", "rename takes dest, not into")
			}
			if rename && len(refs) > 1 {
				r.fault(at[1], "a rename takes one folder")
			}
			var dest core.Ref
			if hasDest || hasInto {
				dest = r.ref(field, core.WantFolder, "")
				if rename && field == "dest" && dest.IsRoot() {
					r.fault("/dest", "the root can't be a new path")
				}
			}
			one := len(refs) == 1 && !hasInto
			r.sameCase(append(slices.Clone(refs), dest), append(at, cli.Pointer(field)), func(a, b core.Ref) bool {
				return one && respells(a, b)
			})
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				var ids []int64
				for i, ref := range refs {
					res, err := resolveAt(tx, ref, core.WantFolder, at[i])
					if err != nil {
						return nil, nil, err
					}
					if res.Kind == core.ResolvedRoot {
						return nil, nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{at[i]},
							Message: "the root can't be moved"}
					}
					if slices.Contains(ids, res.ID) {
						return nil, nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{at[i]},
							Message: fmt.Sprintf("@%d is given twice", res.ID)}
					}
					ids = append(ids, res.ID)
				}
				f, err := loadFolders(tx, s.Root)
				if err != nil {
					return nil, nil, err
				}
				var inside *core.Error
				for i, id := range ids {
					for _, other := range ids {
						if other != id && f.inside(id, other) {
							if inside == nil {
								inside = &core.Error{Kind: core.KindInvalidInput,
									Message: fmt.Sprintf("@%d is inside @%d, also given", id, other)}
							}
							inside.Problems = append(inside.Problems, at[i])
							break
						}
					}
				}
				if inside != nil {
					return nil, nil, inside
				}
				into := core.RootID
				switch {
				case hasInto:
					if into, err = resolveFolder(tx, dest, "into"); err != nil {
						return nil, nil, err
					}
				case !rename:
					res, err := resolve(tx, dest, core.WantFolder, "dest")
					switch {
					case err == nil:
						into = res.ID
					case len(ids) > 1 || dest.Kind == core.TargetID:
						return nil, nil, err
					default:
						rename = true
					}
				}
				// Each folder that moves, and where it goes.
				type place struct {
					id, parent int64
					name       string
				}
				var moves []place
				if rename {
					id, row := ids[0], f.rows[ids[0]]
					parent, name, err := newPath(tx, f, dest, id)
					if err != nil {
						return nil, nil, err
					}
					path := strings.TrimSuffix(tx.Path(parent), "/") + "/" + name
					if at, ok := tx.Child(parent, name); ok && at != id {
						return nil, nil, core.Conflict(core.RuleExists, nil, []string{path}, "%s is taken by @%d", path, at)
					}
					if at, spelled, ok := tx.ChildFold(parent, name); ok && at != id {
						is := strings.TrimSuffix(tx.Path(parent), "/") + "/" + spelled
						return nil, nil, core.Conflict(core.RuleCaseMismatch, []int64{at}, []string{path, is},
							"%s differs from %s only in case", path, is)
					}
					if parent != row.Parent || name != row.Name {
						moves = append(moves, place{id, parent, name})
					}
				} else {
					above := strings.TrimSuffix(tx.Path(into), "/") + "/"
					for _, id := range ids {
						if into == id || f.inside(into, id) {
							p := above + f.rows[id].Name
							return nil, nil, core.Conflict(core.RuleIntoItself, []int64{id}, []string{p},
								"@%d would land inside itself at %s", id, p)
						}
					}
					// What the folder holds once the move is made: what is
					// there, then each folder that lands, in input order.
					var there []place
					for _, row := range f.rows {
						if row.Parent == into {
							there = append(there, place{row.ID, into, row.Name})
						}
					}
					slices.SortFunc(there, func(a, b place) int { return strings.Compare(a.name, b.name) })
					for _, id := range ids {
						if row := f.rows[id]; row.Parent != into {
							moves = append(moves, place{id, into, row.Name})
						}
					}
					for _, exact := range []bool{false, true} {
						held := slices.Clone(there)
						for _, m := range moves {
							for _, g := range held {
								switch {
								case !exact && g.name != m.name && strings.EqualFold(g.name, m.name):
									return nil, nil, core.Conflict(core.RuleCaseMismatch, []int64{g.id},
										[]string{above + m.name, above + g.name}, "%s differs from %s only in case", above+m.name, above+g.name)
								case exact && g.name == m.name:
									return nil, nil, core.Conflict(core.RuleExists, nil, []string{above + m.name},
										"%s is taken by @%d", above+m.name, g.id)
								}
							}
							held = append(held, m)
						}
					}
				}
				// Each new path is a destination on disk as well; a
				// case-only rename's own directory is not in its way.
				for _, m := range moves {
					p := strings.TrimSuffix(tx.Path(m.parent), "/") + "/" + m.name
					own := ""
					if m.parent == f.rows[m.id].Parent {
						own = fsstep.FolderDir(f.paths[m.id])
					}
					entry, taken, err := fsstep.TakenBeside(s.Root, p, own)
					if err != nil {
						dir := fsstep.Abs(s.Root, fsstep.FolderDir(tx.Path(m.parent)))
						return nil, nil, store.Err(cli.IO("could not list "+dir+": "+err.Error(), dir))
					}
					if taken {
						at := fsstep.Abs(s.Root, entry)
						return nil, nil, core.Conflict(core.RuleExists, nil, []string{at}, "%s is taken on disk by %s", p, at)
					}
				}
				out := FolderMvResult{Moved: []FolderMoved{}}
				for _, m := range moves {
					was := f.paths[m.id]
					if err := tx.SetFolderPlace(m.id, m.parent, m.name); err != nil {
						return nil, nil, err
					}
					path := tx.Path(m.id)
					step := fsstep.Step{Op: fsstep.OpMove, From: fsstep.FolderDir(was), To: fsstep.FolderDir(path), Dir: true}
					if _, err := fsstep.Add(tx, step); err != nil {
						return nil, nil, err
					}
					out.Moved = append(out.Moved, FolderMoved{ID: m.id, Path: path, Dir: fsstep.Abs(s.Root, fsstep.FolderDir(path))})
				}
				if len(moves) == 0 {
					return out, nil, nil
				}
				// Every task under them, gates included, has new notes.
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, nil, err
				}
				var claimed []int64
				var notes []string
				for tid := range t.claims {
					for _, m := range moves {
						if t.in(tid, m.id, true) {
							claimed = append(claimed, tid)
							break
						}
					}
				}
				slices.Sort(claimed)
				for _, tid := range claimed {
					notes = append(notes, fsstep.Abs(s.Root, t.notesRel(tid)))
				}
				warnings, err := movedClaimed(tx, claimed, notes)
				return out, warnings, err
			})
		},
	}
}

// newPath finds where a rename puts a folder: the folder its new path's
// parent names, which must exist, and the new name. A bare ID names a
// folder that exists, so its path is the new one. A parent that is the
// folder itself, or inside it, is into-itself, checked at each step of
// the walk, before what is further along it; a part that matches a
// folder only ignoring case is case-mismatch, and one that matches none
// not-found.
func newPath(tx *store.Tx, f *folders, ref core.Ref, self int64) (int64, string, error) {
	inside := func(at int64) error {
		if at == self || f.inside(at, self) {
			return core.Conflict(core.RuleIntoItself, []int64{self}, []string{ref.TargetString()},
				"@%d would land inside itself at %s", self, ref.TargetString())
		}
		return nil
	}
	if ref.Kind == core.TargetID {
		res, err := resolve(tx, ref, core.WantFolder, "dest")
		if err != nil {
			return 0, "", err
		}
		row := f.rows[res.ID]
		if res.ID == self {
			return row.Parent, row.Name, nil
		}
		return row.Parent, row.Name, inside(row.Parent)
	}
	at, given := core.RootID, ""
	if ref.Kind == core.TargetAnchored {
		anchor := ref
		anchor.Names = nil
		res, err := resolve(tx, anchor, core.WantFolder, "dest")
		if err != nil {
			return 0, "", err
		}
		at, given = res.ID, strings.SplitN(ref.Raw, "/", 2)[0]
	}
	n := len(ref.Names)
	for _, name := range ref.Names[:n-1] {
		if err := inside(at); err != nil {
			return 0, "", err
		}
		given += "/" + name
		if id, ok := tx.Child(at, name); ok {
			at = id
			continue
		}
		if id, spelled, ok := tx.ChildFold(at, name); ok {
			is := strings.TrimSuffix(tx.Path(at), "/") + "/" + spelled
			return 0, "", core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{given, is},
				"%s differs from %s only in case", given, is)
		}
		return 0, "", core.NotFound(nil, []string{given}, "no folder at %s", given)
	}
	return at, ref.Names[n-1], inside(at)
}

// inside reports whether a folder sits in another at any depth.
func (f *folders) inside(id, folder int64) bool {
	for at := f.rows[id].Parent; at != core.RootID; at = f.rows[at].Parent {
		if at == folder {
			return true
		}
	}
	return false
}

// respells reports whether a destination only respells its subject's own
// name: the same path from the same start but for the case of its last
// name.
func respells(subject, dest core.Ref) bool {
	n := len(subject.Names)
	return subject.Kind == dest.Kind && subject.ID == dest.ID && n > 0 && len(dest.Names) == n &&
		(subject.Kind == core.TargetPath || subject.Kind == core.TargetAnchored) &&
		slices.Equal(subject.Names[:n-1], dest.Names[:n-1]) && strings.EqualFold(subject.Names[n-1], dest.Names[n-1])
}
