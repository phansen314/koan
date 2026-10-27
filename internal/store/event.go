package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

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
// created events come first.
type recorder struct {
	command string
	at      string
	events  []event
	made    []made
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

// OpenUnder reports whether any open task, a gate included, is under a
// folder, at any depth, stopping at the first it finds.
func (t *Tx) OpenUnder(folder int64) bool {
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
			var f sql.NullInt64
			t.fail(t.QueryRow("SELECT folder FROM task WHERE id = ?", id).Scan(&f))
			folder = f.Int64
		}
	}
	return Place{Folder: folder, Path: t.Path(folder)}
}
