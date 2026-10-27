package store

import (
	"database/sql"
	"encoding/json"

	"github.com/phansen314/koan/internal/core"
)

// TaskRow is a task's row: its fields other than its edges and tags, and
// its folder, RootID for the root.
type TaskRow struct {
	ID          int64
	Folder      int64
	Gate        core.End
	Title       string
	Priority    *int64
	CreatedAt   string
	CompletedAt *string
	UpdatedAt   string
	Extra       json.RawMessage
}

// InsertTask inserts an ordinary task's row, made now, open, with its
// tags. The change issues its ID first, and extra is a JSON object.
func (t *Tx) InsertTask(r TaskRow, tags []string) error {
	_, err := t.Exec(`INSERT INTO task (id, folder, gate, title, priority, created_at, completed_at, updated_at, extra)
		VALUES (?, ?, NULL, ?, ?, ?, NULL, ?, ?)`, r.ID, parentArg(r.Folder), r.Title, r.Priority, t.Now, t.Now, string(r.Extra))
	if err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := t.Exec("INSERT INTO tag (task, tag) VALUES (?, ?)", r.ID, tag); err != nil {
			return err
		}
	}
	return nil
}

// Tasks is every task's row, gates included.
func (t *Tx) Tasks() ([]TaskRow, error) {
	rows, err := t.Query("SELECT id, folder, gate, title, priority, created_at, completed_at, updated_at, extra FROM task")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		var r TaskRow
		var folder, priority sql.NullInt64
		var gate, completed sql.NullString
		var extra string
		if err := rows.Scan(&r.ID, &folder, &gate, &r.Title, &priority, &r.CreatedAt, &completed, &r.UpdatedAt, &extra); err != nil {
			return nil, err
		}
		r.Folder, r.Gate, r.Extra = folder.Int64, core.End(gate.String), json.RawMessage(extra)
		if priority.Valid {
			r.Priority = &priority.Int64
		}
		if completed.Valid {
			r.CompletedAt = &completed.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Tags is every task's tags, by task, each list sorted byte for byte.
func (t *Tx) Tags() (map[int64][]string, error) {
	rows, err := t.Query("SELECT task, tag FROM tag ORDER BY task, tag COLLATE BINARY")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return nil, err
		}
		out[id] = append(out[id], tag)
	}
	return out, rows.Err()
}

// Edges is every edge: From waits on To. They come by From, then To,
// lowest ID first.
func (t *Tx) Edges() ([]core.Edge, error) {
	rows, err := t.Query("SELECT task, blocker FROM edge ORDER BY task, blocker")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Edge
	for rows.Next() {
		var e core.Edge
		if err := rows.Scan(&e.From, &e.To); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClaimRow is a claim's row.
type ClaimRow struct {
	Task      int64
	ClaimedAt string
	Extra     json.RawMessage
}

// Claims is every claim, by task.
func (t *Tx) Claims() (map[int64]ClaimRow, error) {
	rows, err := t.Query("SELECT task, claimed_at, extra FROM claim")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ClaimRow{}
	for rows.Next() {
		var c ClaimRow
		var extra string
		if err := rows.Scan(&c.Task, &c.ClaimedAt, &extra); err != nil {
			return nil, err
		}
		c.Extra = json.RawMessage(extra)
		out[c.Task] = c
	}
	return out, rows.Err()
}

// HasEdge reports whether from waits on to.
func (t *Tx) HasEdge(from, to int64) (bool, error) {
	var n int
	err := t.QueryRow("SELECT count(*) FROM edge WHERE task = ? AND blocker = ?", from, to).Scan(&n)
	return n > 0, err
}

// AddEdge writes an edge, from waiting on to, and gives from a new
// updated_at, since its blocked_by gains an entry. It records blocked on
// from.
func (t *Tx) AddEdge(from, to int64) error {
	if _, err := t.Exec("INSERT INTO edge (task, blocker) VALUES (?, ?)", from, to); err != nil {
		return err
	}
	t.Record(EventBlocked, from, t.PlaceOf(from), Blocker{Blocker: to})
	_, err := t.Exec("UPDATE task SET updated_at = ? WHERE id = ?", t.Now, from)
	return err
}

// RemoveEdge removes an edge, from waiting on to, giving from a new
// updated_at and recording unblocked on it when there was one. It
// reports whether there was.
func (t *Tx) RemoveEdge(from, to int64) (bool, error) {
	res, err := t.Exec("DELETE FROM edge WHERE task = ? AND blocker = ?", from, to)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	t.Record(EventUnblocked, from, t.PlaceOf(from), Blocker{Blocker: to})
	_, err = t.Exec("UPDATE task SET updated_at = ? WHERE id = ?", t.Now, from)
	return err == nil, err
}

// Claimed reports whether a task has a claim.
func (t *Tx) Claimed(id int64) (bool, error) {
	var n int
	err := t.QueryRow("SELECT count(*) FROM claim WHERE task = ?", id).Scan(&n)
	return n > 0, err
}

// Task is one task's row and its tags, sorted byte for byte.
func (t *Tx) Task(id int64) (TaskRow, []string, error) {
	r := TaskRow{ID: id}
	var folder, priority sql.NullInt64
	var gate, completed sql.NullString
	var extra string
	err := t.QueryRow("SELECT folder, gate, title, priority, created_at, completed_at, updated_at, extra FROM task WHERE id = ?", id).
		Scan(&folder, &gate, &r.Title, &priority, &r.CreatedAt, &completed, &r.UpdatedAt, &extra)
	if err != nil {
		return TaskRow{}, nil, err
	}
	r.Folder, r.Gate, r.Extra = folder.Int64, core.End(gate.String), json.RawMessage(extra)
	if priority.Valid {
		r.Priority = &priority.Int64
	}
	if completed.Valid {
		r.CompletedAt = &completed.String
	}
	var tags []string
	rows, err := t.Query("SELECT tag FROM tag WHERE task = ? ORDER BY tag COLLATE BINARY", id)
	if err != nil {
		return TaskRow{}, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return TaskRow{}, nil, err
		}
		tags = append(tags, tag)
	}
	return r, tags, rows.Err()
}

// SetTaskFields writes a task's title, priority, extra and tags, as an
// update changing one of them does, and gives it a new updated_at.
func (t *Tx) SetTaskFields(r TaskRow, tags []string) error {
	if _, err := t.Exec("UPDATE task SET title = ?, priority = ?, extra = ?, updated_at = ? WHERE id = ?",
		r.Title, r.Priority, string(r.Extra), t.Now, r.ID); err != nil {
		return err
	}
	if _, err := t.Exec("DELETE FROM tag WHERE task = ?", r.ID); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := t.Exec("INSERT INTO tag (task, tag) VALUES (?, ?)", r.ID, tag); err != nil {
			return err
		}
	}
	return nil
}

// SetComplete marks a task complete now, or open again, and gives it a new
// updated_at. Completing ends its claim, and reports whether it had one.
func (t *Tx) SetComplete(id int64, complete bool) (unclaimed bool, err error) {
	var at any
	if complete {
		at = t.Now
	}
	if _, err := t.Exec("UPDATE task SET completed_at = ?, updated_at = ? WHERE id = ?", at, t.Now, id); err != nil {
		return false, err
	}
	if complete {
		return t.EndClaim(id)
	}
	return false, nil
}

// Blocks is the tasks that wait on a task, lowest ID first.
func (t *Tx) Blocks(id int64) []int64 {
	return t.ids("SELECT task FROM edge WHERE blocker = ? ORDER BY task", id)
}

// Claim is a task's claim; ok is false when it has none.
func (t *Tx) Claim(id int64) (c ClaimRow, ok bool, err error) {
	var extra string
	err = t.QueryRow("SELECT task, claimed_at, extra FROM claim WHERE task = ?", id).Scan(&c.Task, &c.ClaimedAt, &extra)
	if err == sql.ErrNoRows {
		return ClaimRow{}, false, nil
	}
	c.Extra = json.RawMessage(extra)
	return c, err == nil, err
}

// InsertClaim claims a task now, with the caller's extra, a JSON object.
// Claiming isn't a change to the task's fields, so its updated_at stays.
func (t *Tx) InsertClaim(id int64, extra json.RawMessage) (ClaimRow, error) {
	c := ClaimRow{Task: id, ClaimedAt: t.Now, Extra: extra}
	_, err := t.Exec("INSERT INTO claim (task, claimed_at, extra) VALUES (?, ?, ?)", id, c.ClaimedAt, string(extra))
	return c, err
}

// EndClaim ends a task's claim, as unclaim, complete and delete do, and
// reports whether it had one. A claim isn't a field of its task, so its
// updated_at stays.
func (t *Tx) EndClaim(id int64) (bool, error) {
	res, err := t.Exec("DELETE FROM claim WHERE task = ?", id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetTaskFolder puts a task in another folder, or the root, as a move
// does. Its place isn't a field, so its updated_at stays.
func (t *Tx) SetTaskFolder(id, folder int64) error {
	_, err := t.Exec("UPDATE task SET folder = ? WHERE id = ?", parentArg(folder), id)
	return err
}
