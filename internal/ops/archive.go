package ops

import (
	"cmp"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// ArchiveResult is archive's result, in its schema's order.
type ArchiveResult struct {
	Archived []ArchivedItem `json:"archived"`
	Removed  []int64        `json:"removed"`
}

// ArchivedItem is where one subject of archive went, as mv gives where
// one went, with its JSON beside it.
type ArchivedItem struct {
	ID        int64   `json:"id"`
	Ref       string  `json:"ref"`
	NotesPath *string `json:"notes_path"`
	Dir       *string `json:"dir"`
	JSON      string  `json:"json"`
}

// archiveFile is the JSON beside an archived item, in the archive-file
// schema's order.
type archiveFile struct {
	Schema     int             `json:"schema"`
	Kind       string          `json:"kind"`
	ArchivedAt string          `json:"archived_at"`
	Path       string          `json:"path"`
	Folders    []archiveFolder `json:"folders"`
	Tasks      []archiveTask   `json:"tasks"`
	Edges      []Edge          `json:"edges"`
}

type archiveFolder struct {
	ID     int64           `json:"id"`
	Parent *int64          `json:"parent"`
	Name   string          `json:"name"`
	Path   string          `json:"path"`
	Title  *string         `json:"title"`
	Extra  json.RawMessage `json:"extra"`
}

type archiveTask struct {
	ID          int64           `json:"id"`
	Gate        *string         `json:"gate"`
	Folder      FolderAt        `json:"folder"`
	Title       string          `json:"title"`
	Priority    *int64          `json:"priority"`
	CreatedAt   string          `json:"created_at"`
	CompletedAt string          `json:"completed_at"`
	UpdatedAt   string          `json:"updated_at"`
	Tags        []string        `json:"tags"`
	Extra       json.RawMessage `json:"extra"`
}

// Edge is an edge as Archive JSON and export list it: the task that
// waits, then the task it waits on.
type Edge struct {
	Task    int64 `json:"task"`
	Blocker int64 `json:"blocker"`
}

// edgesTouching is every edge with an end in a set of tasks, sorted by
// task, then blocker.
func edgesTouching(t *tasks, in map[int64]bool) []Edge {
	out := []Edge{}
	for from, blockers := range t.blockedBy {
		for _, to := range blockers {
			if in[from] || in[to] {
				out = append(out, Edge{Task: from, Blocker: to})
			}
		}
	}
	slices.SortFunc(out, func(a, b Edge) int {
		return cmp.Or(cmp.Compare(a.Task, b.Task), cmp.Compare(a.Blocker, b.Blocker))
	})
	return out
}

// leaving is what an archive takes out of the tree: the item, its
// folders and tasks in tree order, and where it goes, as a step's paths.
type leaving struct {
	id      int64
	kind    string // task or folder
	path    string // the folder's path, or the task's folder's
	folders []int64
	tasks   []int64
	// from is the item in the tree, to where it goes in the archive, and
	// file its JSON beside it.
	from, to, file string
	dir            bool
	// index is where each ID that leaves itself sits in the archive.
	index map[int64]string
	// missing is a lone task's notes not there when the change is made:
	// the index names its JSON, and the result no notes.
	missing bool
}

// Archive moves complete tasks, gates included, and finished folders
// with everything under them, mixed, to the archive, each with its JSON
// beside it, as one change: if any is refused, none goes.
func Archive() *cli.Command {
	return &cli.Command{
		Name:    "archive",
		Subject: &cli.Option{Field: "refs", Kind: cli.List, Arg: "ref", Holds: "the tasks and folders to archive", Required: true, Complete: archivable.complete()},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			refs, at := r.subjectRefs("archived")
			r.sameCase(refs, at)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				subs, err := resolveSubjects(tx, refs, at, "archived")
				if err != nil {
					return nil, nil, err
				}
				if err := within(tx, subs, at); err != nil {
					return nil, nil, err
				}
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, nil, err
				}
				// Each archive is made from the tree as it was, so it keeps
				// the edges to the others archived with it.
				var open []int64
				for _, sub := range subs {
					if !sub.folder {
						if t.Open(sub.id) {
							open = append(open, sub.id)
						}
						continue
					}
					for _, tid := range t.order {
						if t.in(tid, sub.id, false) && t.Open(tid) {
							open = append(open, tid)
						}
					}
				}
				// One subject keeps its own message, from its own check.
				if len(open) > 0 && len(subs) > 1 {
					slices.Sort(open)
					open = slices.Compact(open)
					return nil, nil, core.Conflict(core.RuleNotFinished, open, nil,
						"%s %s still open; only finished work is archived", core.FormatIDs(open), isAre(len(open)))
				}
				res := ArchiveResult{Archived: []ArchivedItem{}, Removed: []int64{}}
				for _, sub := range subs {
					var l leaving
					if sub.folder {
						l, err = t.archiveFolder(tx, s.Root, sub.id)
					} else {
						l, err = t.archiveTask(tx, s.Root, sub.id)
					}
					if err != nil {
						return nil, nil, err
					}
					item, err := t.archive(tx, l)
					if err != nil {
						return nil, nil, err
					}
					res.Archived = append(res.Archived, item)
					res.Removed = append(res.Removed, slices.Concat(l.tasks, l.folders)...)
				}
				slices.Sort(res.Removed)
				return res, nil, nil
			})
		},
	}
}

// archiveTask moves a complete task to the archive, its notes as
// @<id>.md, or a gate's as @<id>_start.md or @<id>_done.md, with its JSON
// beside them, under directories named after the folders it was in.
func (t *tasks) archiveTask(tx *store.Tx, root store.Root, id int64) (leaving, error) {
	if t.Open(id) {
		return leaving{}, core.Conflict(core.RuleNotFinished, []int64{id}, nil, "@%d is still open; only finished work is archived", id)
	}
	row := t.rows[id]
	above, err := archiveAbove(tx, root, t.paths[row.Folder])
	if err != nil {
		return leaving{}, err
	}
	name := "@" + strconv.FormatInt(id, 10)
	if row.Gate != core.EndNone {
		name += "_" + string(row.Gate)
	}
	l := leaving{id: id, kind: "task", path: t.paths[row.Folder], tasks: []int64{id},
		from: t.notesRel(id), to: path.Join(above, name+".md"), file: path.Join(above, name+".json")}
	there, err := fsstep.There(root, l.from)
	if err != nil {
		return leaving{}, lookErr(root, path.Dir(l.from), err)
	}
	l.missing = !there
	l.index = map[int64]string{id: l.to}
	if l.missing {
		l.index[id] = l.file
	}
	return l, nil
}

// archiveFolder moves a finished folder to the archive with everything
// under it, gates, notes and material included: its directory, renamed
// @<id>_<name>, with its JSON beside it, under directories named after
// the folders it was in.
func (t *tasks) archiveFolder(tx *store.Tx, root store.Root, id int64) (leaving, error) {
	l := leaving{id: id, kind: "folder", path: t.paths[id], folders: append([]int64{id}, t.under(id, false)...), dir: true}
	var open []int64
	for _, tid := range t.order {
		if t.in(tid, id, false) {
			l.tasks = append(l.tasks, tid)
			if t.Open(tid) {
				open = append(open, tid)
			}
		}
	}
	if len(open) > 0 {
		slices.Sort(open)
		return leaving{}, core.Conflict(core.RuleNotFinished, open, nil,
			"%s under @%d %s still open; only finished work is archived", core.FormatIDs(open), id, isAre(len(open)))
	}
	row := t.folders.rows[id]
	above, err := archiveAbove(tx, root, t.paths[row.Parent])
	if err != nil {
		return leaving{}, err
	}
	name := "@" + strconv.FormatInt(id, 10) + "_" + row.Name
	l.from = fsstep.FolderDir(l.path)
	l.to, l.file = path.Join(above, name), path.Join(above, name+".json")
	// Everything under it keeps its place in the directory, so each is
	// indexed there, whether or not it is on disk.
	l.index = map[int64]string{}
	for _, f := range l.folders {
		l.index[f] = l.to + strings.TrimPrefix(fsstep.FolderDir(t.paths[f]), l.from)
	}
	for _, tid := range l.tasks {
		l.index[tid] = l.to + strings.TrimPrefix(t.notesRel(tid), l.from)
	}
	return l, nil
}

func themOrIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// archiveAbove is the directories above an item archived from a folder,
// or the root, spelled as the index records them.
func archiveAbove(tx *store.Tx, root store.Root, folder string) (string, error) {
	dir, err := fsstep.ArchiveDir(tx, root, fsstep.ArchiveAbove(folder))
	if err != nil {
		return "", lookErr(root, store.ArchiveName, err)
	}
	return dir, nil
}

// lookErr is the operating system refusing a look on disk as io; any
// other error, the database's, is as it is.
func lookErr(root store.Root, rel string, err error) error {
	if pe := (*fs.PathError)(nil); !errors.As(err, &pe) {
		return err
	}
	at := fsstep.Abs(root, rel)
	return store.Err(cli.IO("could not look in "+at+": "+err.Error(), at))
}

// archive removes the rows of what leaves, every edge between it and what
// stays, and adds the step that moves it to the archive with its JSON,
// which carries the rows. Removing them gives what waited on it a new
// updated_at; none is released, since a complete task never blocks. The
// JSON is made from t, which removing leaves as it was.
func (t *tasks) archive(tx *store.Tx, l leaving) (ArchivedItem, error) {
	rows, err := json.Marshal(t.archiveFile(tx.Now, l))
	if err != nil {
		return ArchivedItem{}, err
	}
	if _, err := tx.RemoveTasks(l.tasks, l.index); err != nil {
		return ArchivedItem{}, err
	}
	if len(l.folders) > 0 {
		// Each folder goes before its parent.
		gone := slices.Clone(l.folders)
		slices.Reverse(gone)
		if err := tx.RemoveFolders(gone, l.index); err != nil {
			return ArchivedItem{}, err
		}
	}
	step := fsstep.Step{Op: fsstep.OpArchive, From: l.from, To: l.to, File: l.file, Dir: l.dir, Rows: rows}
	if _, err := fsstep.Add(tx, step); err != nil {
		return ArchivedItem{}, err
	}
	item := ArchivedItem{ID: l.id, JSON: fsstep.Abs(t.root, l.file)}
	at := fsstep.Abs(t.root, l.to)
	switch {
	case l.dir:
		notes := fsstep.Abs(t.root, path.Join(l.to, fsstep.FolderNotes))
		item.Ref, item.NotesPath, item.Dir = core.Kept(l.id, l.path, core.EndNone), &notes, &at
	case l.missing:
		item.Ref = t.ref(l.id)
	default:
		item.Ref, item.NotesPath = t.ref(l.id), &at
	}
	return item, nil
}

// archiveFile is the JSON of what leaves: its folders and tasks, and
// every edge that touched them, to or from anything.
func (t *tasks) archiveFile(now string, l leaving) archiveFile {
	f := archiveFile{Schema: 1, Kind: l.kind, ArchivedAt: now, Path: l.path,
		Folders: []archiveFolder{}, Tasks: []archiveTask{}, Edges: []Edge{}}
	gone := map[int64]bool{}
	for _, id := range l.folders {
		r := t.folders.rows[id]
		folder := archiveFolder{ID: id, Name: r.Name, Path: t.paths[id], Title: r.Title, Extra: r.Extra}
		if r.Parent != core.RootID {
			parent := r.Parent
			folder.Parent = &parent
		}
		f.Folders = append(f.Folders, folder)
	}
	for _, id := range l.tasks {
		gone[id] = true
		r := t.rows[id]
		x := archiveTask{ID: id, Folder: FolderAt{Path: t.paths[r.Folder]}, Title: r.Title, Priority: r.Priority,
			CreatedAt: r.CreatedAt, CompletedAt: *r.CompletedAt, UpdatedAt: r.UpdatedAt, Tags: t.tags[id], Extra: r.Extra}
		if r.Gate != core.EndNone {
			gate := string(r.Gate)
			x.Gate = &gate
		}
		if r.Folder != core.RootID {
			folder := r.Folder
			x.Folder.ID = &folder
		}
		if x.Tags == nil {
			x.Tags = []string{}
		}
		f.Tasks = append(f.Tasks, x)
	}
	f.Edges = edgesTouching(t, gone)
	return f
}
