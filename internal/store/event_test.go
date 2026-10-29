package store

import (
	"database/sql"
	"fmt"
	"maps"
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
// root. What the change made comes first, a created event each in the
// order of their IDs, then the events it recorded, in order. A change
// that records nothing, and a refused one, insert nothing.
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
		tx.Record(EventUpdated, folder, tx.PlaceOf(folder), map[string]any{"n": 1})
		if rootTask, err = addTask(tx, core.RootID, "b"); err != nil {
			return err
		}
		tx.Record(EventUpdated, task, tx.PlaceOf(task), nil)
		tx.Record(EventUpdated, rootTask, tx.PlaceOf(rootTask), nil)
		return nil
	})
	got := events(t, home)
	if len(got) != 7 {
		t.Fatalf("events: %+v", got)
	}
	inFolder := sql.NullInt64{Int64: folder, Valid: true}
	want := []struct {
		kind   string
		id     int64
		folder sql.NullInt64
		path   string
		data   string
	}{
		{EventCreated, folder, inFolder, "/work", `{"given":true,"name":"work","title":null,"extra":{},"finished":false}`},
		{EventCreated, task, inFolder, "/work", `{"given":true,"gate":null,"title":"a","priority":null,"tags":[],"extra":{},"readiness":"ready"}`},
		{EventCreated, rootTask, sql.NullInt64{}, "/", `{"given":true,"gate":null,"title":"b","priority":null,"tags":[],"extra":{},"readiness":"ready"}`},
		{EventUpdated, folder, inFolder, "/work", `{"n":1}`},
		{EventUpdated, task, inFolder, "/work", `{}`},
		{EventUpdated, rootTask, sql.NullInt64{}, "/", `{}`},
	}
	for i, w := range want {
		e := got[i+1]
		if e.Seq != int64(i+2) || e.Change != 2 || e.At != now || e.Command != "task create" || e.Kind != w.kind ||
			e.ID.Int64 != w.id || e.FolderID != w.folder || e.FolderPath != w.path || e.Data != w.data {
			t.Errorf("event %d: %+v", i, e)
		}
	}

	// Nothing recorded, nothing inserted.
	write(t, s, func(tx *Tx) error { return nil })
	// A refused change inserts nothing it recorded or made.
	_, _, e := s.Write(func(tx *Tx) (any, []cli.Warning, error) {
		if _, err := addTask(tx, folder, "c"); err != nil {
			return nil, nil, err
		}
		tx.Record(EventCompleted, task, tx.PlaceOf(task), nil)
		return nil, nil, core.Conflict(core.RuleClaimed, []int64{task}, nil, "refused")
	})
	if e == nil {
		t.Fatal("the change was not refused")
	}
	if n := len(events(t, home)); n != 7 {
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
	if len(got) != 9 || got[7].Seq != 8 || got[7].Change != 8 || got[8].Change != 8 || got[8].Command != "complete" {
		t.Errorf("second change: %+v", got[7:])
	}
}

// A made task's readiness and a made folder's finished are as the change
// leaves them, and a gate an edge named, or a missing parent, is not
// given.
func TestRecordMade(t *testing.T) {
	home, s := newRoot(t, 1)
	s.Command = "batch"
	var parent, folder, task, gate int64
	write(t, s, func(tx *Tx) error {
		var err error
		if parent, err = tx.Issue(core.NodeFolder, false); err != nil {
			return err
		}
		if err := tx.InsertFolder(FolderRow{ID: parent, Name: "p", Extra: []byte("{}")}); err != nil {
			return err
		}
		if folder, err = addFolder(tx, parent, "f"); err != nil {
			return err
		}
		if task, err = addTask(tx, folder, "a"); err != nil {
			return err
		}
		if gate, err = tx.MakeGate(parent, core.EndDone); err != nil {
			return err
		}
		// Made ready, the task waits on the gate by the end of the change.
		return tx.AddEdge(task, gate)
	})
	got := events(t, home)[1:]
	want := []string{
		`{"given":false,"name":"p","title":null,"extra":{},"finished":false}`,
		`{"given":true,"name":"f","title":null,"extra":{},"finished":false}`,
		`{"given":true,"gate":null,"title":"a","priority":null,"tags":[],"extra":{},"readiness":"blocked"}`,
		`{"given":false,"gate":"done","title":"Done","priority":null,"tags":[],"extra":{},"readiness":"ready"}`,
	}
	// The edge's own event comes after everything made.
	if len(got) != len(want)+1 {
		t.Fatalf("events: %+v", got)
	}
	if e := got[len(want)]; e.Kind != EventBlocked || e.ID.Int64 != task || e.Data != fmt.Sprintf(`{"blocker":%d}`, gate) {
		t.Errorf("the edge: %+v", e)
	}
	for i, w := range want {
		if got[i].Kind != EventCreated || got[i].ID.Int64 != []int64{parent, folder, task, gate}[i] || got[i].Data != w {
			t.Errorf("event %d: %+v", i, got[i])
		}
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

// A change asks only the folders above what it touches, each before and
// after, and never the rest of the tree; an open task under a folder
// answers for each folder above it before the change. A task completed
// and reopened in one change leaves every folder as it found it, so
// nothing derived is recorded.
func TestDerivedAsksAbove(t *testing.T) {
	home, s := newRoot(t, 1)
	s.Command = "task create"
	var a, b, c, z, y, task int64
	write(t, s, func(tx *Tx) error {
		var err error
		for _, f := range []struct {
			id     *int64
			parent *int64
			name   string
		}{{&a, ptr(core.RootID), "a"}, {&b, &a, "b"}, {&c, &b, "c"}, {&z, ptr(core.RootID), "z"}, {&y, &z, "y"}} {
			if *f.id, err = addFolder(tx, *f.parent, f.name); err != nil {
				return err
			}
		}
		if task, err = addTask(tx, c, "x"); err != nil {
			return err
		}
		_, err = addTask(tx, y, "o")
		return err
	})
	times := map[int64]int{}
	asked = func(folder int64) { times[folder]++ }
	t.Cleanup(func() { asked = nil })
	before := len(events(t, home))
	s.Command = "complete"
	write(t, s, func(tx *Tx) error {
		if _, err := tx.SetComplete(task, true); err != nil {
			return err
		}
		_, err := tx.SetComplete(task, false)
		return err
	})
	if want := map[int64]int{c: 2, b: 1, a: 1}; !maps.Equal(times, want) {
		t.Errorf("asked %v, want %v", times, want)
	}
	if got := events(t, home)[before:]; len(got) != 0 {
		t.Errorf("a change that changed back recorded %+v", got)
	}
	// Completing it asks the same folders, and records each finished,
	// deepest first, where it is.
	clear(times)
	write(t, s, func(tx *Tx) error {
		_, err := tx.SetComplete(task, true)
		return err
	})
	if want := map[int64]int{c: 2, b: 1, a: 1}; !maps.Equal(times, want) {
		t.Errorf("asked %v, want %v", times, want)
	}
	var got []string
	for _, e := range events(t, home)[before:] {
		got = append(got, fmt.Sprintf("%s %d %s %s", e.Kind, e.ID.Int64, e.FolderPath, e.Data))
	}
	want := []string{fmt.Sprintf("finished %d /a/b/c {}", c), fmt.Sprintf("finished %d /a/b {}", b), fmt.Sprintf("finished %d /a {}", a)}
	if !slices.Equal(got, want) {
		t.Errorf("events %q, want %q", got, want)
	}
}

// OpenUnder streams the folders under the one asked about, so it can stop
// at the first open task, rather than materialising them all first.
func TestOpenUnderStreams(t *testing.T) {
	home, _ := newRoot(t, 1)
	rows, err := rawDB(t, home).Query("EXPLAIN QUERY PLAN "+openUnder, core.RootID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan, "CO-ROUTINE under") {
		t.Errorf("plan %q runs under other than as a co-routine", plan)
	}
}
