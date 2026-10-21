package ops

import (
	"cmp"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strconv"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// ArchiveResult is task archive's result, and folder archive's, in its
// schema's order.
type ArchiveResult struct {
	ID      int64   `json:"id"`
	Archive *string `json:"archive"`
	JSON    string  `json:"json"`
	Left    []int64 `json:"left"`
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
	Edges      []archiveEdge   `json:"edges"`
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

type archiveEdge struct {
	Task    int64 `json:"task"`
	Blocker int64 `json:"blocker"`
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
	// the index names its JSON, and the result no archive.
	missing bool
}

// TaskArchive moves a complete task to the archive, its notes as
// @<id>.md, or a gate's as @<id>_start.md or @<id>_done.md, with its JSON
// beside them, under directories named after the folders it was in.
func TaskArchive() *cli.Command {
	return rootedTaskCommand("task archive", func(tx *store.Tx, root store.Root, id int64) (any, []cli.Warning, error) {
		t, err := loadTasks(tx, root)
		if err != nil {
			return nil, nil, err
		}
		if t.Open(id) {
			return nil, nil, core.Conflict(core.RuleNotFinished, []int64{id}, nil, "@%d is open; complete it before archiving it", id)
		}
		row := t.rows[id]
		above, err := archiveAbove(tx, root, t.paths[row.Folder])
		if err != nil {
			return nil, nil, err
		}
		name := "@" + strconv.FormatInt(id, 10)
		if row.Gate != core.EndNone {
			name += "_" + string(row.Gate)
		}
		l := leaving{id: id, kind: "task", path: t.paths[row.Folder], tasks: []int64{id},
			from: t.notesRel(id), to: path.Join(above, name+".md"), file: path.Join(above, name+".json")}
		there, err := fsstep.There(root, l.from)
		if err != nil {
			return nil, nil, lookErr(root, path.Dir(l.from), err)
		}
		l.missing = !there
		l.index = map[int64]string{id: l.to}
		if l.missing {
			l.index[id] = l.file
		}
		return t.archive(tx, l)
	})
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
// updated_at; none is released, since a complete task never blocks.
func (t *tasks) archive(tx *store.Tx, l leaving) (any, []cli.Warning, error) {
	rows, err := json.Marshal(t.archiveFile(tx.Now, l))
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.RemoveTasks(l.tasks, l.index); err != nil {
		return nil, nil, err
	}
	if len(l.folders) > 0 {
		// Each folder goes before its parent.
		gone := slices.Clone(l.folders)
		slices.Reverse(gone)
		if err := tx.RemoveFolders(gone, l.index); err != nil {
			return nil, nil, err
		}
	}
	step := fsstep.Step{Op: fsstep.OpArchive, From: l.from, To: l.to, File: l.file, Dir: l.dir, Rows: rows}
	if _, err := fsstep.Add(tx, step); err != nil {
		return nil, nil, err
	}
	res := ArchiveResult{ID: l.id, JSON: fsstep.Abs(t.root, l.file), Left: slices.Sorted(slices.Values(slices.Concat(l.tasks, l.folders)))}
	if !l.missing {
		at := fsstep.Abs(t.root, l.to)
		res.Archive = &at
	}
	return res, nil, nil
}

// archiveFile is the JSON of what leaves: its folders and tasks, and
// every edge that touched them, to or from anything.
func (t *tasks) archiveFile(now string, l leaving) archiveFile {
	f := archiveFile{Schema: 1, Kind: l.kind, ArchivedAt: now, Path: l.path,
		Folders: []archiveFolder{}, Tasks: []archiveTask{}, Edges: []archiveEdge{}}
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
	for from, blockers := range t.blockedBy {
		for _, to := range blockers {
			if gone[from] || gone[to] {
				f.Edges = append(f.Edges, archiveEdge{Task: from, Blocker: to})
			}
		}
	}
	slices.SortFunc(f.Edges, func(a, b archiveEdge) int {
		return cmp.Or(cmp.Compare(a.Task, b.Task), cmp.Compare(a.Blocker, b.Blocker))
	})
	return f
}
