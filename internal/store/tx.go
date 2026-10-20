package store

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/core"
)

var (
	_ core.Tree    = (*Tx)(nil)
	_ core.Archive = (*Tx)(nil)
	_ core.Graph   = (*Tx)(nil)
)

// parentArg is a parent as the folder and task tables hold it: NULL for
// the root.
func parentArg(id int64) any {
	if id == core.RootID {
		return nil
	}
	return id
}

// Node looks up an ID among live tasks and folders.
func (t *Tx) Node(id int64) (core.Node, bool) {
	var gate sql.NullString
	err := t.QueryRow("SELECT gate FROM task WHERE id = ?", id).Scan(&gate)
	if err == nil {
		return core.Node{ID: id, Kind: core.NodeTask, Gate: core.End(gate.String)}, true
	}
	if t.fail(err) {
		return core.Node{}, false
	}
	err = t.QueryRow("SELECT id FROM folder WHERE id = ?", id).Scan(&id)
	if err == nil {
		return core.Node{ID: id, Kind: core.NodeFolder}, true
	}
	t.fail(err)
	return core.Node{}, false
}

// Child finds the folder in parent named exactly name, byte for byte.
func (t *Tx) Child(parent int64, name string) (int64, bool) {
	var id int64
	err := t.QueryRow("SELECT id FROM folder WHERE parent IS ? AND name = ? COLLATE BINARY", parentArg(parent), name).Scan(&id)
	if err == nil {
		return id, true
	}
	t.fail(err)
	return 0, false
}

// ChildFold finds the folder in parent whose name equals name ignoring
// case, with its name as spelled.
func (t *Tx) ChildFold(parent int64, name string) (int64, string, bool) {
	var id int64
	var spelled string
	err := t.QueryRow("SELECT id, name FROM folder WHERE parent IS ? AND name = ? COLLATE NOCASE", parentArg(parent), name).Scan(&id, &spelled)
	if err == nil {
		return id, spelled, true
	}
	t.fail(err)
	return 0, "", false
}

// Gate finds a folder's start or done gate.
func (t *Tx) Gate(folder int64, end core.End) (int64, bool) {
	var id int64
	err := t.QueryRow("SELECT id FROM task WHERE folder = ? AND gate = ?", folder, string(end)).Scan(&id)
	if err == nil {
		return id, true
	}
	t.fail(err)
	return 0, false
}

// Path is a folder's path from the root.
func (t *Tx) Path(folder int64) string {
	if folder == core.RootID {
		return "/"
	}
	var names []string
	for id := folder; id != core.RootID; {
		var parent sql.NullInt64
		var name string
		if err := t.QueryRow("SELECT parent, name FROM folder WHERE id = ?", id).Scan(&parent, &name); err != nil {
			t.fail(err)
			return ""
		}
		names = append(names, name)
		id = parent.Int64
	}
	slices.Reverse(names)
	return "/" + strings.Join(names, "/")
}

// Archived reports whether an ID was archived.
func (t *Tx) Archived(id int64) bool {
	var n int
	err := t.QueryRow("SELECT 1 FROM issued WHERE id = ? AND state = 'archived'", id).Scan(&n)
	t.fail(err)
	return err == nil
}

// archivePath is where an archived ID went, from the index.
func (t *Tx) archivePath(id int64) string {
	var path string
	t.QueryRow("SELECT archive FROM issued WHERE id = ?", id).Scan(&path)
	return path
}

// Open reports whether a task exists and is open.
func (t *Tx) Open(id int64) bool {
	var open bool
	err := t.QueryRow("SELECT completed_at IS NULL FROM task WHERE id = ?", id).Scan(&open)
	t.fail(err)
	return err == nil && open
}

// BlockedBy is the tasks a task waits on, lowest ID first.
func (t *Tx) BlockedBy(id int64) []int64 {
	return t.ids("SELECT blocker FROM edge WHERE task = ? ORDER BY blocker", id)
}

// ids runs a query for a column of IDs, keeping the first error.
func (t *Tx) ids(query string, args ...any) []int64 {
	if t.err != nil {
		return nil
	}
	rows, err := t.Query(query, args...)
	if t.fail(err) {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if t.fail(rows.Scan(&id)) {
			return nil
		}
		out = append(out, id)
	}
	t.fail(rows.Err())
	return out
}

// Issue issues the next ID, to a task or a folder: the greater of the
// highest ID ever issued, plus one, and the root's first ID. A change
// calls it in the order it makes things: its folders, parents first, in
// tree order; then its tasks, in input order; then the gates its edges
// name, in the order those edges are given, each task's blocked_by before
// its blocks.
func (t *Tx) Issue(kind core.NodeKind) (int64, error) {
	var id int64
	err := t.QueryRow(`SELECT max(coalesce((SELECT max(id) FROM issued), 0) + 1, (SELECT first_id FROM root))`).Scan(&id)
	if err != nil {
		return 0, err
	}
	k := "task"
	if kind == core.NodeFolder {
		k = "folder"
	}
	if _, err := t.Exec("INSERT INTO issued (id, kind, state) VALUES (?, ?, 'live')", id, k); err != nil {
		return 0, err
	}
	return id, nil
}

// MakeGate makes a folder's start or done gate, as an edge naming one the
// folder lacks does: it issues the gate an ID and inserts its row with a
// made gate's fields, Start or Done as its title, null priority, no tags
// and an empty extra. The change adds the step writing its notes.
func (t *Tx) MakeGate(folder int64, end core.End) (int64, error) {
	id, err := t.Issue(core.NodeTask)
	if err != nil {
		return 0, err
	}
	title := "Start"
	if end == core.EndDone {
		title = "Done"
	}
	_, err = t.Exec(`INSERT INTO task (id, folder, gate, title, priority, created_at, completed_at, updated_at, extra)
		VALUES (?, ?, ?, ?, NULL, ?, NULL, ?, '{}')`, id, folder, string(end), title, t.Now, t.Now)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// RemoveTasks deletes task rows, as delete and archive do. First it finds
// the tasks that wait on them through edge_blocker and gives each a new
// updated_at, since its blocked_by loses an entry; then it deletes the
// edges, claims, tags and rows, and marks each ID deleted, or, when
// archive is given, archived to where archive says that ID itself went.
// It returns the open tasks it left ready that were blocked before,
// lowest ID first.
func (t *Tx) RemoveTasks(ids []int64, archive map[int64]string) (released []int64, err error) {
	if len(ids) == 0 {
		return nil, nil
	}
	for _, id := range ids {
		if _, ok := archive[id]; archive != nil && !ok {
			return nil, fmt.Errorf("no archive path for task %d", id)
		}
	}
	gone := map[int64]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	var waiting []int64
	for _, id := range ids {
		for _, w := range t.ids("SELECT task FROM edge WHERE blocker = ?", id) {
			if !gone[w] && !slices.Contains(waiting, w) {
				waiting = append(waiting, w)
			}
		}
	}
	slices.Sort(waiting)
	before := map[int64]bool{}
	for _, w := range waiting {
		before[w] = core.Ready(t, w)
		t.exec("UPDATE task SET updated_at = ? WHERE id = ?", t.Now, w)
	}
	for _, id := range ids {
		state, path := "deleted", any(nil)
		if archive != nil {
			state, path = "archived", archive[id]
		}
		// The cascade is a backstop: the edges go first, by name.
		t.exec("DELETE FROM edge WHERE task = ? OR blocker = ?", id, id)
		t.exec("DELETE FROM task WHERE id = ?", id)
		t.exec("UPDATE issued SET state = ?, archive = ? WHERE id = ?", state, path, id)
	}
	for _, w := range waiting {
		if !before[w] && core.Ready(t, w) {
			released = append(released, w)
		}
	}
	if t.err != nil {
		return nil, t.err
	}
	return released, nil
}
