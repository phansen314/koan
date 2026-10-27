package store

import (
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

// event is one event a change records, before it has a seq.
type event struct {
	kind  string
	id    int64
	place Place
	data  any
}

// recorder holds a change's events until step 5 inserts them, in the
// order they were recorded. Every event of the change takes its command
// and time.
type recorder struct {
	command string
	at      string
	events  []event
}

// Record adds an event to the change: its kind, the task or folder it is
// about, or zero for the root or the log, where it happened, and what its
// kind carries, as JSON makes an object of it.
func (r *recorder) Record(kind string, id int64, at Place, data any) {
	r.events = append(r.events, event{kind: kind, id: id, place: at, data: data})
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
			if data, err = json.Marshal(e.data); err != nil {
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
