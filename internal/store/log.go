package store

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// Event is an event as log list prints it, in its schema's order.
type Event struct {
	Seq     int64           `json:"seq"`
	Change  int64           `json:"change"`
	At      string          `json:"at"`
	Command string          `json:"command"`
	Kind    string          `json:"kind"`
	ID      *int64          `json:"id"`
	Folder  FolderAt        `json:"folder"`
	Data    json.RawMessage `json:"data"`
}

// EventFilter is what log list keeps: every event that passes each
// filter set. Each is compared with what the event stored, never
// resolved against the tree.
type EventFilter struct {
	// Path keeps the events stored at it or under it, byte for byte;
	// "" or "/" keeps every event.
	Path string
	// Folder, when not zero, keeps the events whose stored folder ID is
	// it.
	Folder int64
	// Task, when not zero, keeps the events about that task: its ID, and
	// a folder ID that isn't.
	Task int64
	// Since, when not "", keeps the events at or after it.
	Since string
	// After keeps the events after that seq.
	After int64
	// Below, when not zero, keeps the events before that seq.
	Below int64
	// Kinds, when not empty, keeps the events of those kinds.
	Kinds []string
	// Limit, when not nil, is the most events returned.
	Limit *int64
}

// LastSeq is the highest seq the log has issued, whether or not its event
// is still in the table; zero before the first.
func (t *Tx) LastSeq() (int64, error) {
	var seq int64
	err := t.QueryRow("SELECT " + lastSeq).Scan(&seq)
	return seq, err
}

// Events are the events the filter keeps, oldest first in seq order, the
// first limit of them, and how many it keeps in all.
func (t *Tx) Events(f EventFilter) (events []Event, total int, err error) {
	where, args := f.where()
	if err := t.QueryRow("SELECT count(*) FROM event"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	events = []Event{}
	err = t.EachEvent(f, func(e Event) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return events, total, nil
}

// EachEvent calls fn with each event the filter keeps, oldest first in seq
// order, the first limit of them, one row at a time, and stops at the
// first error fn gives.
func (t *Tx) EachEvent(f EventFilter, fn func(Event) error) error {
	where, args := f.where()
	limit := int64(-1)
	if f.Limit != nil {
		limit = *f.Limit
	}
	rows, err := t.Query("SELECT seq, change, at, command, kind, id, folder_id, folder_path, data FROM event"+where+
		" ORDER BY seq LIMIT ?", append(args, limit)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var id, folder sql.NullInt64
		var data string
		if err := rows.Scan(&e.Seq, &e.Change, &e.At, &e.Command, &e.Kind, &id, &folder, &e.Folder.Path, &data); err != nil {
			return err
		}
		if id.Valid {
			e.ID = &id.Int64
		}
		if folder.Valid {
			e.Folder.ID = &folder.Int64
		}
		e.Data = json.RawMessage(data)
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// where is the filter's WHERE clause, "" when it keeps every event, and
// its arguments.
func (f EventFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.Path != "" && f.Path != "/" {
		// A range over the index's BINARY order: "/" and "0" are adjacent
		// bytes, so what is under the path sorts between them.
		conds = append(conds, "(folder_path = ? OR (folder_path >= ? AND folder_path < ?))")
		args = append(args, f.Path, f.Path+"/", f.Path+"0")
	}
	if f.Folder != 0 {
		conds = append(conds, "folder_id = ?")
		args = append(args, f.Folder)
	}
	if f.Task != 0 {
		conds = append(conds, "id = ? AND folder_id IS NOT ?")
		args = append(args, f.Task, f.Task)
	}
	if f.Since != "" {
		conds = append(conds, "at >= ?")
		args = append(args, f.Since)
	}
	if f.After != 0 {
		conds = append(conds, "seq > ?")
		args = append(args, f.After)
	}
	if f.Below != 0 {
		conds = append(conds, "seq < ?")
		args = append(args, f.Below)
	}
	if len(f.Kinds) > 0 {
		conds = append(conds, "kind IN (?"+strings.Repeat(", ?", len(f.Kinds)-1)+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}
