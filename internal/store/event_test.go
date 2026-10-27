package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
)

// row is an event as the table holds it.
type row struct {
	Seq, Change int64
	At          string
	Command     string
	Kind        string
	ID          sql.NullInt64
	FolderID    sql.NullInt64
	FolderPath  string
	Data        string
}

// events reads the log's table, oldest first.
func events(t *testing.T, home string) []row {
	t.Helper()
	rows, err := rawDB(t, home).Query("SELECT seq, change, at, command, kind, id, folder_id, folder_path, data FROM event ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Seq, &r.Change, &r.At, &r.Command, &r.Kind, &r.ID, &r.FolderID, &r.FolderPath, &r.Data); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// init records exactly one event, initialized, at seq 1, about the root.
func TestInitRecordsInitialized(t *testing.T) {
	home, _ := newRoot(t, 7)
	got := events(t, home)
	if len(got) != 1 {
		t.Fatalf("events: %+v", got)
	}
	e := got[0]
	if e.Seq != 1 || e.Change != 1 || e.Command != "init" || e.Kind != "initialized" ||
		e.ID.Valid || e.FolderID.Valid || e.FolderPath != "/" || e.Data != `{"format":2,"first_id":7}` {
		t.Errorf("initialized: %+v", e)
	}
	if _, err := core.ParseTimestamp(e.At); err != nil {
		t.Errorf("at %q: %v", e.At, err)
	}
}

// Every event of a change takes its time, its command and the seq of its
// first event; where each happened is its folder, a task's folder, or the
// root. A change that records nothing, and a refused one, insert nothing.
func TestRecorder(t *testing.T) {
	home, s := newRoot(t, 1)
	s.Command = "task create"
	var now string
	var folder, task, rootTask int64
	write(t, s, func(tx *Tx) error {
		now = tx.Now
		var err error
		if folder, err = addFolder(tx, core.RootID, "work"); err != nil {
			return err
		}
		if task, err = addTask(tx, folder, "a"); err != nil {
			return err
		}
		if rootTask, err = addTask(tx, core.RootID, "b"); err != nil {
			return err
		}
		tx.Record(EventCreated, folder, tx.PlaceOf(folder), map[string]any{"n": 1})
		tx.Record(EventCreated, task, tx.PlaceOf(task), nil)
		tx.Record(EventCreated, rootTask, tx.PlaceOf(rootTask), nil)
		return nil
	})
	got := events(t, home)
	if len(got) != 4 {
		t.Fatalf("events: %+v", got)
	}
	want := []struct {
		id     int64
		folder sql.NullInt64
		path   string
		data   string
	}{
		{folder, sql.NullInt64{Int64: folder, Valid: true}, "/work", `{"n":1}`},
		{task, sql.NullInt64{Int64: folder, Valid: true}, "/work", `{}`},
		{rootTask, sql.NullInt64{}, "/", `{}`},
	}
	for i, w := range want {
		e := got[i+1]
		if e.Seq != int64(i+2) || e.Change != 2 || e.At != now || e.Command != "task create" || e.Kind != "created" ||
			e.ID.Int64 != w.id || e.FolderID != w.folder || e.FolderPath != w.path || e.Data != w.data {
			t.Errorf("event %d: %+v", i, e)
		}
	}

	// Nothing recorded, nothing inserted.
	write(t, s, func(tx *Tx) error { return nil })
	// A refused change inserts nothing it recorded.
	_, _, e := s.Write(func(tx *Tx) (any, []cli.Warning, error) {
		tx.Record(EventCompleted, task, tx.PlaceOf(task), nil)
		return nil, nil, core.Conflict(core.RuleClaimed, []int64{task}, nil, "refused")
	})
	if e == nil {
		t.Fatal("the change was not refused")
	}
	if n := len(events(t, home)); n != 4 {
		t.Errorf("%d events", n)
	}

	// The next change's events start a change of their own.
	s.Command = "complete"
	write(t, s, func(tx *Tx) error {
		tx.Record(EventCompleted, task, tx.PlaceOf(task), nil)
		tx.Record(EventFinished, folder, tx.PlaceOf(folder), nil)
		return nil
	})
	got = events(t, home)
	if len(got) != 6 || got[4].Seq != 5 || got[4].Change != 5 || got[5].Change != 5 || got[5].Command != "complete" {
		t.Errorf("second change: %+v", got[4:])
	}
}

// An event never changes, and a seq once issued is never issued again,
// even once every row is gone.
func TestEventsFixed(t *testing.T) {
	home, s := newRoot(t, 1)
	db := rawDB(t, home)
	if _, err := db.Exec("UPDATE event SET kind = 'rotated'"); err == nil || !strings.Contains(err.Error(), "events are never changed") {
		t.Errorf("update: %v", err)
	}
	if _, err := db.Exec("DELETE FROM event"); err != nil {
		t.Fatal(err)
	}
	st, e := s.Stats()
	if e != nil || st.LastSeq != 1 || st.Events != 0 {
		t.Errorf("stats with every row gone: %+v %+v", st, e)
	}
	s.Command = "complete"
	write(t, s, func(tx *Tx) error {
		tx.Record(EventCompleted, 0, tx.PlaceOf(core.RootID), nil)
		return nil
	})
	got := events(t, home)
	if len(got) != 1 || got[0].Seq != 2 || got[0].Change != 2 {
		t.Errorf("after every row went: %+v", got)
	}
	st, _ = s.Stats()
	if st.LastSeq != 2 || st.Events != 1 {
		t.Errorf("stats: %+v", st)
	}
}

// log is koan's directory: a file by that name, or one spelled otherwise,
// isn't.
func TestInitLogNotARoot(t *testing.T) {
	for _, entry := range []struct {
		name string
		dir  bool
	}{{"log", false}, {"Log", true}} {
		home := t.TempDir()
		root := filepath.Join(home, "koan-root")
		os.Mkdir(root, 0o755)
		if entry.dir {
			os.Mkdir(filepath.Join(root, entry.name), 0o755)
		} else {
			os.WriteFile(filepath.Join(root, entry.name), nil, 0o644)
		}
		_, _, _, e := Init(home, nil)
		if e == nil || e.Rule == nil || *e.Rule != cli.RuleNotARoot || !slices.Equal(e.Paths, []string{filepath.Join(root, entry.name)}) {
			t.Errorf("%s: %+v", entry.name, e)
		}
	}
}
