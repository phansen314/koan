package ops

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// MvResult is mv's result, in its schema's order.
type MvResult struct {
	Moved []Moved `json:"moved"`
}

// Moved is one task or folder that moved: its kept text with its new
// path, its new notes path, and a folder's directory.
type Moved struct {
	ID        int64   `json:"id"`
	Ref       string  `json:"ref"`
	NotesPath string  `json:"notes_path"`
	Dir       *string `json:"dir"`
}

// Mv moves tasks and folders, mixed, into a folder that exists, or
// renames one folder to a new path whose parent exists, as one change, as
// Linux mv moves files and directories. A task's notes move with it, and
// everything under a folder, material too. Nothing is replaced and no
// parent is made; a gate stays in its folder, and what is already where
// it would go doesn't move. A case-only rename is the one input allowed
// to name both spellings, and only of the folder's own name.
func Mv() *cli.Command {
	return &cli.Command{
		Name:          "mv",
		Uses:          []string{"<ref>... <dest>", "-t <folder> <ref>...", "-T <folder> <path>"},
		Subject:       &cli.Option{Field: "refs", Kind: cli.List, Arg: "ref", Required: true, Complete: tasksAndFolders.complete()},
		Object:        &cli.Option{Field: "dest", Required: true, Complete: tasksAndFolders.complete()},
		Instead:       "into",
		Single:        "rename",
		SingleSubject: foldersOnly.complete(),
		SingleObject:  newPaths.complete(),
		Options: []cli.Option{
			{Field: "into", Short: "t", Arg: "folder", Complete: foldersOnly.complete()},
			{Field: "rename", Short: "T", Kind: cli.Bool},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			refs, at := r.subjectRefs("moved")
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
				subs, err := resolveSubjects(tx, refs, at, "moved")
				if err != nil {
					return nil, nil, err
				}
				// A gate is refused with the subjects, before the destination
				// resolves.
				// Each task's row, and each folder given.
				rows := map[int64]store.TaskRow{}
				var ids []int64
				for i, sub := range subs {
					if sub.folder {
						ids = append(ids, sub.id)
						continue
					}
					row, _, err := tx.Task(sub.id)
					if err != nil {
						return nil, nil, err
					}
					if row.Gate != core.EndNone {
						return nil, nil, &core.Error{Kind: core.KindInvalidInput, IDs: []int64{sub.id}, Problems: []string{at[i]},
							Message: fmt.Sprintf("@%d is a gate, and stays in its folder", sub.id)}
					}
					rows[sub.id] = row
				}
				if err := within(tx, subs, at); err != nil {
					return nil, nil, err
				}
				task, hasTask := firstOf(subs, false)
				if rename && hasTask {
					return nil, nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{"/rename"},
						Message: fmt.Sprintf("@%d is a task, which can't be renamed: rename renames a folder", task)}
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
					case len(subs) > 1 || hasTask || dest.Kind == core.TargetID:
						return nil, nil, err
					default:
						rename = true
					}
				}
				// Tasks alone need no walk of the tree.
				f := &folders{}
				if len(ids) > 0 {
					if f, err = loadFolders(tx, s.Root); err != nil {
						return nil, nil, err
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
				// The moves, made in input order; the claimed tasks whose
				// notes they move, with their new paths.
				out := MvResult{Moved: []Moved{}}
				claimed := map[int64]string{}
				for _, sub := range subs {
					if !sub.folder {
						row := rows[sub.id]
						if row.Folder == into {
							continue
						}
						from := fsstep.TaskNotesPath(tx.Path(row.Folder), sub.id, core.EndNone)
						notes := fsstep.TaskNotesPath(tx.Path(into), sub.id, core.EndNone)
						if err := tx.SetTaskFolder(sub.id, into); err != nil {
							return nil, nil, err
						}
						if _, err := fsstep.Add(tx, fsstep.Step{Op: fsstep.OpMove, From: from, To: notes}); err != nil {
							return nil, nil, err
						}
						abs := fsstep.Abs(s.Root, notes)
						out.Moved = append(out.Moved, Moved{ID: sub.id, Ref: core.Kept(sub.id, tx.Path(into), core.EndNone), NotesPath: abs})
						c, err := tx.Claimed(sub.id)
						if err != nil {
							return nil, nil, err
						}
						if c {
							claimed[sub.id] = abs
						}
						continue
					}
					i := slices.IndexFunc(moves, func(m place) bool { return m.id == sub.id })
					if i < 0 {
						continue
					}
					m := moves[i]
					was := f.paths[m.id]
					if err := tx.SetFolderPlace(m.id, m.parent, m.name); err != nil {
						return nil, nil, err
					}
					path := tx.Path(m.id)
					step := fsstep.Step{Op: fsstep.OpMove, From: fsstep.FolderDir(was), To: fsstep.FolderDir(path), Dir: true}
					if _, err := fsstep.Add(tx, step); err != nil {
						return nil, nil, err
					}
					dir := fsstep.Abs(s.Root, fsstep.FolderDir(path))
					out.Moved = append(out.Moved, Moved{ID: m.id, Ref: core.Kept(m.id, path, core.EndNone),
						NotesPath: fsstep.Abs(s.Root, fsstep.FolderNotesPath(path)), Dir: &dir})
				}
				// Every task under a folder that moved, gates included, has
				// new notes.
				if len(moves) > 0 {
					t, err := loadTasks(tx, s.Root)
					if err != nil {
						return nil, nil, err
					}
					for tid := range t.claims {
						for _, m := range moves {
							if t.in(tid, m.id, false) {
								claimed[tid] = fsstep.Abs(s.Root, t.notesRel(tid))
								break
							}
						}
					}
				}
				var claimedIDs []int64
				var notes []string
				for _, id := range slices.Sorted(maps.Keys(claimed)) {
					claimedIDs, notes = append(claimedIDs, id), append(notes, claimed[id])
				}
				warnings, err := movedClaimed(claimedIDs, notes)
				return out, warnings, err
			})
		},
	}
}

// movedClaimed is the moved-claimed warning for the claimed tasks a move
// gave new notes paths, each beside its new path.
func movedClaimed(claimed []int64, at []string) ([]cli.Warning, error) {
	if len(claimed) == 0 {
		return nil, nil
	}
	message := core.FormatIDs(claimed) + " is claimed, and its notes moved; whoever holds it should read them at the new path."
	if len(claimed) > 1 {
		message = core.FormatIDs(claimed) + " are claimed, and their notes moved; whoever holds them should read them at the new paths."
	}
	return []cli.Warning{{Kind: "moved-claimed", Message: message, IDs: claimed, Paths: at}}, nil
}

// newPath finds where a rename puts a folder: the folder its new path's
// parent names, which must exist, and the new name. A bare ID names a
// folder that exists, so its path is the new one. A parent that is the
// folder itself, or inside it, is into-itself, checked at each step of
// the walk, before what is further along it; a part that matches a
// folder only ignoring case is case-mismatch, and one that matches none
// not-found. Into-itself gives the new path from the root.
func newPath(tx *store.Tx, f *folders, ref core.Ref, self int64) (int64, string, error) {
	dest := ""
	inside := func(at int64) error {
		if at == self || f.inside(at, self) {
			return core.Conflict(core.RuleIntoItself, []int64{self}, []string{dest},
				"@%d would land inside itself at %s", self, dest)
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
		dest = tx.Path(res.ID)
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
	dest = strings.TrimSuffix(tx.Path(at), "/") + "/" + strings.Join(ref.Names, "/")
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
