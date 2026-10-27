package store

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/core"
)

// The kinds of event, as the spec's Event record names them.
const (
	EventCreated     = "created"
	EventUpdated     = "updated"
	EventCompleted   = "completed"
	EventReopened    = "reopened"
	EventBlocked     = "blocked"
	EventUnblocked   = "unblocked"
	EventClaimed     = "claimed"
	EventUnclaimed   = "unclaimed"
	EventMoved       = "moved"
	EventDeleted     = "deleted"
	EventArchived    = "archived"
	EventReleased    = "released"
	EventReblocked   = "reblocked"
	EventFinished    = "finished"
	EventUnfinished  = "unfinished"
	EventInitialized = "initialized"
	EventRotated     = "rotated"
)

// lastSeq is the highest seq the log has issued, whether or not its event
// is still in the table: AUTOINCREMENT keeps it in sqlite_sequence once
// the rows are gone. Zero before the first event.
const lastSeq = `max(coalesce((SELECT seq FROM sqlite_sequence WHERE name = 'event'), 0),
	coalesce((SELECT max(seq) FROM event), 0))`

// Place is where an event happened, as the change found it: a folder, or
// RootID for the root, and its path then.
type Place struct {
	Folder int64
	Path   string
}

// initialized is the data of init's event.
type initialized struct {
	Format  int   `json:"format"`
	FirstID int64 `json:"first_id"`
}

// TaskMade is the data of a task's or a gate's created: its fields as
// made, and its readiness as of the end of the change.
type TaskMade struct {
	Given     bool            `json:"given"`
	Gate      *string         `json:"gate"`
	Title     string          `json:"title"`
	Priority  *int64          `json:"priority"`
	Tags      []string        `json:"tags"`
	Extra     json.RawMessage `json:"extra"`
	Readiness string          `json:"readiness"`
}

// FolderMade is the data of a folder's created: its fields as made, and
// whether it is finished as of the end of the change.
type FolderMade struct {
	Given    bool            `json:"given"`
	Name     string          `json:"name"`
	Title    *string         `json:"title"`
	Extra    json.RawMessage `json:"extra"`
	Finished bool            `json:"finished"`
}

// Updated is the data of updated: each field the update changed, in the
// record's order.
type Updated struct {
	Changes []FieldChange `json:"changes"`
}

// FieldChange is one field an update changed, its value before and after,
// tags and extra whole.
type FieldChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// Reopened is the data of reopened: the task's readiness as of the end of
// the change.
type Reopened struct {
	Readiness string `json:"readiness"`
}

// Blocker is the data of blocked and unblocked, on the task that waits:
// the task it waits on, or waited on.
type Blocker struct {
	Blocker int64 `json:"blocker"`
}

// Claimed is the data of claimed: the claim's extra.
type Claimed struct {
	Extra json.RawMessage `json:"extra"`
}

// Unclaimed is the data of unclaimed: the ended claim's time and extra.
type Unclaimed struct {
	ClaimedAt string          `json:"claimed_at"`
	Extra     json.RawMessage `json:"extra"`
}

// FolderAt is a task's folder as records give it: its ID, null for the
// root, and its path.
type FolderAt struct {
	ID   *int64 `json:"id"`
	Path string `json:"path"`
}

// At is a folder as a task's records give it.
func At(folder int64, path string) FolderAt {
	f := FolderAt{Path: path}
	if folder != core.RootID {
		f.ID = &folder
	}
	return f
}

// FolderMoved is the data of a folder's moved: its path before and after.
type FolderMoved struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// TaskMoved is the data of a task's moved: its folder before and after.
type TaskMoved struct {
	Before FolderAt `json:"before"`
	After  FolderAt `json:"after"`
}

// Deleted is the data of deleted: whether it was a subject, its kept text
// with the path it had, and its title, null for a folder with none.
type Deleted struct {
	Given bool    `json:"given"`
	Ref   string  `json:"ref"`
	Title *string `json:"title"`
}

// Archived is the data of archived: as deleted's, and where its notes and
// a folder's directory went.
type Archived struct {
	Given     bool    `json:"given"`
	Ref       string  `json:"ref"`
	Title     *string `json:"title"`
	NotesPath *string `json:"notes_path"`
	Dir       *string `json:"dir"`
}

// Readiness is an open task's readiness, as events give it.
func Readiness(g core.Graph, id int64) string {
	if core.Ready(g, id) {
		return "ready"
	}
	return "blocked"
}

// made is something a change made, and whether it was given, rather than
// made because something given needed it.
type made struct {
	id    int64
	given bool
}

// event is one event a change records, before it has a seq.
type event struct {
	kind  string
	id    int64
	place Place
	data  any
}

// recorder holds a change's events until step 5 inserts them, in the
// order they were recorded. Every event of the change takes its command
// and time. Made is what the change issued IDs to, in that order, whose
// created events come first. Held is each folder above what the change
// touches, as the change found it.
type recorder struct {
	command string
	at      string
	events  []event
	made    []made
	held    map[int64]heldOpen
}

// Record adds an event to the change: its kind, the task or folder it is
// about, or zero for the root or the log, where it happened, and what its
// kind carries, as JSON makes an object of it.
func (r *recorder) Record(kind string, id int64, at Place, data any) {
	r.events = append(r.events, event{kind: kind, id: id, place: at, data: data})
}

// marshal encodes an event's data as every result is printed: without
// escaping <, > and &.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// insert inserts the change's events in the order recorded. The first
// takes the seq SQLite issues it as its change, and the rest repeat it.
// A change that recorded nothing inserts nothing.
func (r *recorder) insert(ctx context.Context, conn *sql.Conn) error {
	var change int64
	for _, e := range r.events {
		data := []byte("{}")
		if e.data != nil {
			var err error
			if data, err = marshal(e.data); err != nil {
				return err
			}
		}
		var id, folder any
		if e.id != 0 {
			id = e.id
		}
		if e.place.Folder != core.RootID {
			folder = e.place.Folder
		}
		if change == 0 {
			// The seq this row will be issued, which no other writer can
			// take first: the change holds the write transaction.
			var seq int64
			err := conn.QueryRowContext(ctx, `INSERT INTO event (change, at, command, kind, id, folder_id, folder_path, data)
				VALUES (`+lastSeq+` + 1, ?, ?, ?, ?, ?, ?, ?) RETURNING seq, change`,
				r.at, r.command, e.kind, id, folder, e.place.Path, string(data)).Scan(&seq, &change)
			if err != nil {
				return err
			}
			if seq != change {
				return fmt.Errorf("the event was issued seq %d, not the %d its change was given", seq, change)
			}
			continue
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO event (change, at, command, kind, id, folder_id, folder_path, data)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			change, r.at, r.command, e.kind, id, folder, e.place.Path, string(data)); err != nil {
			return err
		}
	}
	return nil
}

// recordMade puts a created event for everything the change made before
// the rest of its events, in the order it issued their IDs, each with its
// state as of the end of the change.
func (t *Tx) recordMade() error {
	var created []event
	for _, m := range t.made {
		n, ok := t.Node(m.id)
		if !ok {
			if t.err != nil {
				return t.err
			}
			return fmt.Errorf("@%d was made, and is gone by the end of its change", m.id)
		}
		var data any
		if n.Kind == core.NodeFolder {
			f, err := t.Folder(m.id)
			if err != nil {
				return err
			}
			data = FolderMade{Given: m.given, Name: f.Name, Title: f.Title, Extra: f.Extra, Finished: !t.OpenUnder(m.id)}
		} else {
			r, tags, err := t.Task(m.id)
			if err != nil {
				return err
			}
			var gate *string
			if r.Gate != core.EndNone {
				g := string(r.Gate)
				gate = &g
			}
			if tags == nil {
				tags = []string{}
			}
			data = TaskMade{Given: m.given, Gate: gate, Title: r.Title, Priority: r.Priority, Tags: tags, Extra: r.Extra,
				Readiness: Readiness(t, m.id)}
		}
		created = append(created, event{kind: EventCreated, id: m.id, place: t.PlaceOf(m.id), data: data})
	}
	if t.err != nil {
		return t.err
	}
	t.events = append(created, t.events...)
	return nil
}

// asked, when a test sets it, is called with each folder OpenUnder asks
// about.
var asked func(folder int64)

// OpenUnder reports whether any open task, a gate included, is under a
// folder, at any depth, stopping at the first it finds.
func (t *Tx) OpenUnder(folder int64) bool {
	if asked != nil {
		asked(folder)
	}
	var open bool
	t.fail(t.QueryRow(`WITH RECURSIVE under(id) AS (SELECT ? UNION ALL SELECT folder.id FROM folder JOIN under ON folder.parent = under.id)
		SELECT EXISTS (SELECT 1 FROM task WHERE folder IN under AND completed_at IS NULL)`, folder).Scan(&open))
	return open
}

// PlaceOf is where an event about id happens, as the transaction now
// stands: a folder itself, a task's folder, and the root for RootID. A
// change takes it for what it moves or removes before it does, and for
// what it makes once it has made it.
func (t *Tx) PlaceOf(id int64) Place {
	folder := id
	if id != core.RootID {
		if n, ok := t.Node(id); ok && n.Kind == core.NodeTask {
			folder = t.taskFolder(id)
		}
	}
	return Place{Folder: folder, Path: t.Path(folder)}
}

// RecordCut records unblocked for each edge between the tasks given,
// which the change removes, and the tasks that stay, by task, then
// blocker, each where its waiting task is as the change found it. An
// edge between two that go records nothing. A change calls it before it
// removes anything.
func (t *Tx) RecordCut(ids []int64) error {
	gone := map[int64]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	var cut []core.Edge
	for _, id := range ids {
		for _, to := range t.BlockedBy(id) {
			if !gone[to] {
				cut = append(cut, core.Edge{From: id, To: to})
			}
		}
		for _, from := range t.Blocks(id) {
			if !gone[from] {
				cut = append(cut, core.Edge{From: from, To: id})
			}
		}
	}
	slices.SortFunc(cut, func(a, b core.Edge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	for _, e := range cut {
		t.Record(EventUnblocked, e.From, t.PlaceOf(e.From), Blocker{Blocker: e.To})
	}
	return t.err
}

// Derived is a result that lists the open tasks its change made ready
// and made wait.
type Derived interface {
	Derived() (released, reblocked []int64)
}

// heldOpen is a folder above what a change touches, as the change found
// it: where it was, how deep, the root's folders being 1, and whether an
// open task was under it.
type heldOpen struct {
	place Place
	depth int
	open  bool
}

// touch asks each folder from folder up, the root aside, whether an open
// task is under it, and keeps where it is and how deep, unless the change
// has asked already. A change touches a task's folder before it adds,
// removes, completes, reopens or moves an open task, and a folder's
// parent before and after it moves the folder, so each is asked before
// the change alters anything under it. An open task under a folder is
// under each folder above it, which needs no asking.
func (t *Tx) touch(folder int64) {
	type link struct {
		id   int64
		name string
	}
	var chain []link
	base := heldOpen{place: Place{Folder: core.RootID, Path: "/"}}
	for id := folder; id != core.RootID; {
		if h, ok := t.held[id]; ok {
			base = h
			break
		}
		var parent sql.NullInt64
		var name string
		if t.fail(t.QueryRow("SELECT parent, name FROM folder WHERE id = ?", id).Scan(&parent, &name)) {
			return
		}
		chain = append(chain, link{id: id, name: name})
		id = parent.Int64
	}
	if t.held == nil {
		t.held = map[int64]heldOpen{}
	}
	open := false
	held := make([]heldOpen, len(chain))
	for i, l := range chain {
		open = open || t.OpenUnder(l.id)
		held[i].open = open
	}
	path, depth := strings.TrimSuffix(base.place.Path, "/"), base.depth
	for i := len(chain) - 1; i >= 0; i-- {
		path += "/" + chain[i].name
		depth++
		held[i].place, held[i].depth = Place{Folder: chain[i].id, Path: path}, depth
		t.held[chain[i].id] = held[i]
	}
}

// recordDerived records what the change did to what the rules derive,
// after the rest of its events: released and reblocked, in the order its
// result lists them, then finished and unfinished for each folder it
// asked before that the change neither made nor removed, asked again,
// deepest first, then by ID, each where the change found it.
func (t *Tx) recordDerived(result any) error {
	if d, ok := result.(Derived); ok {
		released, reblocked := d.Derived()
		for _, id := range released {
			t.Record(EventReleased, id, t.PlaceOf(id), nil)
		}
		for _, id := range reblocked {
			t.Record(EventReblocked, id, t.PlaceOf(id), nil)
		}
	}
	made := map[int64]bool{}
	for _, m := range t.made {
		made[m.id] = true
	}
	var ids []int64
	for id := range t.held {
		if n, ok := t.Node(id); ok && n.Kind == core.NodeFolder && !made[id] {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b int64) int {
		return cmp.Or(cmp.Compare(t.held[b].depth, t.held[a].depth), cmp.Compare(a, b))
	})
	for _, id := range ids {
		h := t.held[id]
		switch open := t.OpenUnder(id); {
		case h.open && !open:
			t.Record(EventFinished, id, h.place, nil)
		case !h.open && open:
			t.Record(EventUnfinished, id, h.place, nil)
		}
	}
	return t.err
}
