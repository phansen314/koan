package ops

import (
	"encoding/json"
	"slices"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// Task is a task as every read returns it, in its schema's order.
type Task struct {
	ID          int64           `json:"id"`
	Ref         string          `json:"ref"`
	Gate        *string         `json:"gate"`
	Title       string          `json:"title"`
	Priority    *int64          `json:"priority"`
	Folder      FolderAt        `json:"folder"`
	CreatedAt   string          `json:"created_at"`
	CompletedAt *string         `json:"completed_at"`
	UpdatedAt   string          `json:"updated_at"`
	State       string          `json:"state"`
	Readiness   *string         `json:"readiness"`
	BlockedBy   []EdgeEnd       `json:"blocked_by"`
	Blocks      []EdgeEnd       `json:"blocks"`
	Tags        []string        `json:"tags"`
	Extra       json.RawMessage `json:"extra"`
	Claim       *Claim          `json:"claim"`
	NotesPath   string          `json:"notes_path"`
}

// EdgeEnd is the other end of an edge, as a task's record lists it.
type EdgeEnd struct {
	ID    int64  `json:"id"`
	Ref   string `json:"ref"`
	State string `json:"state"`
}

// Claim is a claim, in its schema's order.
type Claim struct {
	Task      int64           `json:"task"`
	ClaimedAt string          `json:"claimed_at"`
	Extra     json.RawMessage `json:"extra"`
}

// taskFields is the task record's field list, as --fields takes it.
var taskFields = FieldNames(Task{})

// tasks is every task as one read transaction sees it, with the folders
// they sit in and what their records need.
type tasks struct {
	*folders
	rows      map[int64]store.TaskRow
	tags      map[int64][]string
	blockedBy map[int64][]int64
	blocks    map[int64][]int64
	claims    map[int64]store.ClaimRow
	// order is every task in tree order.
	order []int64
}

func loadTasks(tx *store.Tx, root store.Root) (*tasks, error) {
	f, err := loadFolders(tx, root)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Tasks()
	if err != nil {
		return nil, err
	}
	tags, err := tx.Tags()
	if err != nil {
		return nil, err
	}
	edges, err := tx.Edges()
	if err != nil {
		return nil, err
	}
	claims, err := tx.Claims()
	if err != nil {
		return nil, err
	}
	t := &tasks{folders: f, rows: map[int64]store.TaskRow{}, tags: tags,
		blockedBy: map[int64][]int64{}, blocks: map[int64][]int64{}, claims: claims}
	var placed []core.Placed
	for _, r := range f.rows {
		placed = append(placed, core.Placed{ID: r.ID, Folder: true, Parent: r.Parent, Name: r.Name})
	}
	for _, r := range rows {
		t.rows[r.ID] = r
		placed = append(placed, core.Placed{ID: r.ID, Parent: r.Folder})
	}
	for _, id := range core.TreeOrder(placed) {
		if _, ok := t.rows[id]; ok {
			t.order = append(t.order, id)
		}
	}
	// Edges come by the task that waits, then its blocker, so each list
	// is ascending by ID; blocks is sorted below.
	for _, e := range edges {
		t.blockedBy[e.From] = append(t.blockedBy[e.From], e.To)
		t.blocks[e.To] = append(t.blocks[e.To], e.From)
	}
	for _, b := range t.blocks {
		slices.Sort(b)
	}
	return t, nil
}

// Open reports whether a task exists and is open, for core.Ready.
func (t *tasks) Open(id int64) bool {
	r, ok := t.rows[id]
	return ok && r.CompletedAt == nil
}

// BlockedBy is the tasks a task waits on, for core.Ready.
func (t *tasks) BlockedBy(id int64) []int64 { return t.blockedBy[id] }

// readiness is an open task's readiness, or null once complete.
func (t *tasks) readiness(id int64) *string {
	if !t.Open(id) {
		return nil
	}
	r := "blocked"
	if core.Ready(t, id) {
		r = "ready"
	}
	return &r
}

// in reports whether a task sits in a folder, or the root: directly, or
// at any depth when recursive.
func (t *tasks) in(id, folder int64, recursive bool) bool {
	at := t.rows[id].Folder
	for {
		if at == folder {
			return true
		}
		if at == core.RootID || !recursive {
			return false
		}
		at = t.folders.rows[at].Parent
	}
}

// notesRel is a task's notes path, relative to the root's directory.
func (t *tasks) notesRel(id int64) string {
	r := t.rows[id]
	return fsstep.TaskNotesPath(t.paths[r.Folder], id, r.Gate)
}

// ref is a task's kept text: a gate's hint is its folder's path and end.
func (t *tasks) ref(id int64) string {
	r := t.rows[id]
	return core.Kept(id, t.paths[r.Folder], r.Gate)
}

func (t *tasks) state(id int64) string {
	if t.Open(id) {
		return "open"
	}
	return "complete"
}

// record is a task's record.
func (t *tasks) record(id int64) Task {
	r := t.rows[id]
	rec := Task{
		ID: id, Ref: t.ref(id), Title: r.Title, Priority: r.Priority,
		Folder:    FolderAt{Path: t.paths[r.Folder]},
		CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt, UpdatedAt: r.UpdatedAt,
		State: t.state(id), Readiness: t.readiness(id),
		BlockedBy: t.ends(t.blockedBy[id]), Blocks: t.ends(t.blocks[id]),
		Tags: t.tags[id], Extra: r.Extra,
		NotesPath: fsstep.Abs(t.root, t.notesRel(id)),
	}
	if r.Gate != core.EndNone {
		gate := string(r.Gate)
		rec.Gate = &gate
	}
	if r.Folder != core.RootID {
		folder := r.Folder
		rec.Folder.ID = &folder
	}
	if rec.Tags == nil {
		rec.Tags = []string{}
	}
	if c, ok := t.claims[id]; ok {
		rec.Claim = &Claim{Task: c.Task, ClaimedAt: c.ClaimedAt, Extra: c.Extra}
	}
	return rec
}

func (t *tasks) ends(ids []int64) []EdgeEnd {
	out := []EdgeEnd{}
	for _, id := range ids {
		out = append(out, EdgeEnd{ID: id, Ref: t.ref(id), State: t.state(id)})
	}
	return out
}

// taskPaths is the paths on disk a cut of a task's record gives, for
// pending-filesystem.
func taskPaths(c Cut[Task]) []fsstep.Path {
	if !c.Has("notes_path") {
		return nil
	}
	return []fsstep.Path{{ID: c.Record.ID, Path: c.Record.NotesPath}}
}
