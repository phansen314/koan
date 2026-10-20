package store

import (
	"database/sql"
	"encoding/json"

	"github.com/phansen314/koan/internal/core"
)

// FolderRow is a folder's row: its four fields and its parent, RootID for
// the root.
type FolderRow struct {
	ID     int64
	Parent int64
	Name   string
	Title  *string
	Extra  json.RawMessage
}

// InsertFolder inserts a folder's row. The change issues its ID first,
// and extra is a JSON object.
func (t *Tx) InsertFolder(f FolderRow) error {
	_, err := t.Exec("INSERT INTO folder (id, parent, name, title, extra) VALUES (?, ?, ?, ?, ?)",
		f.ID, parentArg(f.Parent), f.Name, f.Title, string(f.Extra))
	return err
}

// Folders is every folder's row.
func (t *Tx) Folders() ([]FolderRow, error) {
	rows, err := t.Query("SELECT id, parent, name, title, extra FROM folder")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderRow
	for rows.Next() {
		var f FolderRow
		var parent sql.NullInt64
		var title sql.NullString
		var extra string
		if err := rows.Scan(&f.ID, &parent, &f.Name, &title, &extra); err != nil {
			return nil, err
		}
		f.Parent = parent.Int64
		if title.Valid {
			f.Title = &title.String
		}
		f.Extra = json.RawMessage(extra)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Counts is how many tasks are open and complete.
type Counts struct{ Open, Complete int64 }

// TaskCounts counts the tasks directly in each folder, gates included, by
// folder; RootID for the root.
func (t *Tx) TaskCounts() (map[int64]Counts, error) {
	rows, err := t.Query(`SELECT folder, count(*) FILTER (WHERE completed_at IS NULL), count(*) FILTER (WHERE completed_at IS NOT NULL)
		FROM task GROUP BY folder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Counts{}
	for rows.Next() {
		var folder sql.NullInt64
		var c Counts
		if err := rows.Scan(&folder, &c.Open, &c.Complete); err != nil {
			return nil, err
		}
		out[folder.Int64] = c
	}
	return out, rows.Err()
}

// GateRow is a folder's gate: its ID, and whether it is complete.
type GateRow struct {
	ID       int64
	Complete bool
}

// Gates is every folder's gates, by folder and end.
func (t *Tx) Gates() (map[int64]map[core.End]GateRow, error) {
	rows, err := t.Query("SELECT id, folder, gate, completed_at IS NOT NULL FROM task WHERE gate IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[core.End]GateRow{}
	for rows.Next() {
		var g GateRow
		var folder int64
		var end string
		if err := rows.Scan(&g.ID, &folder, &end, &g.Complete); err != nil {
			return nil, err
		}
		if out[folder] == nil {
			out[folder] = map[core.End]GateRow{}
		}
		out[folder][core.End(end)] = g
	}
	return out, rows.Err()
}

// Folder is one folder's row.
func (t *Tx) Folder(id int64) (FolderRow, error) {
	f := FolderRow{ID: id}
	var parent sql.NullInt64
	var title sql.NullString
	var extra string
	err := t.QueryRow("SELECT parent, name, title, extra FROM folder WHERE id = ?", id).Scan(&parent, &f.Name, &title, &extra)
	if err != nil {
		return FolderRow{}, err
	}
	f.Parent = parent.Int64
	if title.Valid {
		f.Title = &title.String
	}
	f.Extra = json.RawMessage(extra)
	return f, nil
}

// SetFolderFields writes a folder's title and extra.
func (t *Tx) SetFolderFields(f FolderRow) error {
	_, err := t.Exec("UPDATE folder SET title = ?, extra = ? WHERE id = ?", f.Title, string(f.Extra), f.ID)
	return err
}

// SetFolderPlace gives a folder a new parent and name, as a move does.
func (t *Tx) SetFolderPlace(id, parent int64, name string) error {
	_, err := t.Exec("UPDATE folder SET parent = ?, name = ? WHERE id = ?", parentArg(parent), name, id)
	return err
}

// RemoveFolders deletes folder rows, as delete does, in the order given,
// so each folder must come before its parent, and marks each ID deleted.
// The tasks in them go first, through RemoveTasks.
func (t *Tx) RemoveFolders(ids []int64) error {
	for _, id := range ids {
		if _, err := t.Exec("DELETE FROM folder WHERE id = ?", id); err != nil {
			return err
		}
		if _, err := t.Exec("UPDATE issued SET state = 'deleted' WHERE id = ?", id); err != nil {
			return err
		}
	}
	return nil
}
