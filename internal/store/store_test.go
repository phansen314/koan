package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/testrun"
)

// newRoot makes a root under a fresh HOME, issuing from first, and opens
// it.
func newRoot(t *testing.T, first int64) (string, *Store) {
	t.Helper()
	home := t.TempDir()
	if _, _, _, e := Init(home, &first); e != nil {
		t.Fatalf("init: %s", e.Message)
	}
	s, e := Open(home)
	if e != nil {
		t.Fatalf("open: %s", e.Message)
	}
	t.Cleanup(s.Close)
	return home, s
}

// write is the test-only write: it runs fn as a change through the steps
// of a write, and fails the test on an error.
func write(t *testing.T, s *Store, fn func(tx *Tx) error) []cli.Warning {
	t.Helper()
	_, w, e := s.Write(func(tx *Tx) (any, []cli.Warning, error) { return nil, nil, fn(tx) })
	if e != nil {
		t.Fatalf("write: %s: %s", e.Kind, e.Message)
	}
	return w
}

// addTask inserts an ordinary task, issuing its ID.
func addTask(tx *Tx, folder int64, title string) (int64, error) {
	id, err := tx.Issue(core.NodeTask)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT INTO task (id, folder, title, created_at, updated_at, extra) VALUES (?, ?, ?, ?, ?, '{}')`,
		id, parentArg(folder), title, tx.Now, tx.Now)
	return id, err
}

// addFolder inserts a folder, issuing its ID.
func addFolder(tx *Tx, parent int64, name string) (int64, error) {
	id, err := tx.Issue(core.NodeFolder)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT INTO folder (id, parent, name, extra) VALUES (?, ?, ?, '{}')`, id, parentArg(parent), name)
	return id, err
}

func rawDB(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, "koan-root", DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// The schema is PRD Storage's, statement for statement.
func TestSchemaIsThePRDs(t *testing.T) {
	prd, err := os.ReadFile(filepath.Join(testrun.Repo(), "docs", "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(prd)
	start := strings.Index(text, "## Storage")
	block := text[start:]
	block = block[strings.Index(block, "```sql")+len("```sql"):]
	block = block[:strings.Index(block, "```")]
	norm := func(s string) string {
		s = regexp.MustCompile(`--[^\n]*`).ReplaceAllString(s, "")
		return strings.Join(strings.Fields(s), " ")
	}
	if got, want := norm(schema), norm(block); got != want {
		t.Errorf("schema differs from the PRD's:\ngot:  %s\nwant: %s", got, want)
	}
}

// Every open sets the pragmas, and init stores WAL mode and the format.
func TestPragmas(t *testing.T) {
	_, s := newRoot(t, 1)
	for _, p := range []struct {
		name string
		want string
	}{
		{"journal_mode", "wal"},
		{"synchronous", "2"},
		{"fullfsync", "1"},
		{"checkpoint_fullfsync", "1"},
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
		{"user_version", "1"},
	} {
		var got string
		if err := s.db.conn.QueryRowContext(t.Context(), "PRAGMA "+p.name).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != p.want {
			t.Errorf("%s = %s, want %s", p.name, got, p.want)
		}
	}
}

func TestOpenStates(t *testing.T) {
	home := t.TempDir()
	if _, e := Open(home); e == nil || e.Kind != cli.KindNotInitialized || e.Paths[0] != filepath.Join(home, "koan-root") {
		t.Errorf("no root: %+v", e)
	}
	os.Mkdir(filepath.Join(home, "koan-root"), 0o755)
	if _, e := Open(home); e == nil || e.Kind != cli.KindNotInitialized || e.Paths[0] != filepath.Join(home, "koan-root", "koan.db") {
		t.Errorf("no database: %+v", e)
	}

	home, s := newRoot(t, 1)
	s.Close()
	if _, err := rawDB(t, home).Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	_, e := Open(home)
	if e == nil || e.Kind != cli.KindUnsupportedFormat || !strings.Contains(e.Message, "format 2") || !strings.Contains(e.Message, "format 1") {
		t.Errorf("format 2: %+v", e)
	}

	home = t.TempDir()
	os.Mkdir(filepath.Join(home, "koan-root"), 0o755)
	os.WriteFile(filepath.Join(home, "koan-root", "koan.db"), []byte(strings.Repeat("not a database ", 100)), 0o644)
	if _, e := Open(home); e == nil || e.Kind != cli.KindCorrupt || e.Paths[0] != filepath.Join(home, "koan-root", "koan.db") {
		t.Errorf("not a database: %+v", e)
	}
}

func TestInit(t *testing.T) {
	home := t.TempDir()
	r, created, first, e := Init(home, nil)
	if e != nil || first != 1 || !slices.Equal(created, []string{"root", "tree", "archive", "database"}) {
		t.Fatalf("first init: %v %d %+v", created, first, e)
	}
	for _, d := range []string{r.Tree, r.Archive} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("%s: %v", d, err)
		}
	}
	// Run again, it changes nothing, and finds what is missing.
	os.Remove(r.Archive)
	_, created, first, e = Init(home, nil)
	if e != nil || first != 1 || !slices.Equal(created, []string{"archive"}) {
		t.Errorf("second init: %v %d %+v", created, first, e)
	}
	_, _, _, e = Init(home, ptr(int64(7)))
	if e == nil || e.Kind != cli.KindConflict || *e.Rule != cli.RuleExists || !slices.Equal(e.Paths, []string{r.Database}) {
		t.Errorf("--ids-from on a database: %+v", e)
	}
}

func TestInitNotARoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "koan-root")
	os.Mkdir(root, 0o755)
	os.WriteFile(filepath.Join(root, "notes.md"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "KOAN.DB"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "tree"), nil, 0o644) // a file, not koan's directory
	os.Mkdir(filepath.Join(root, "archived"), 0o755)
	_, _, _, e := Init(home, nil)
	want := []string{filepath.Join(root, "KOAN.DB"), filepath.Join(root, "notes.md"), filepath.Join(root, "tree")}
	if e == nil || e.Kind != cli.KindConflict || *e.Rule != cli.RuleNotARoot || !slices.Equal(e.Paths, want) {
		t.Errorf("foreign entries: %+v", e)
	}
	// Judged by listing, since KOAN.DB stats as koan.db where case is
	// ignored.
	if entries, _ := os.ReadDir(root); len(entries) != 4 {
		t.Errorf("init changed a root that isn't one: %v", entries)
	}

	// A root that isn't a directory.
	home = t.TempDir()
	root = filepath.Join(home, "koan-root")
	os.WriteFile(root, nil, 0o644)
	_, _, _, e = Init(home, nil)
	if e == nil || e.Kind != cli.KindConflict || *e.Rule != cli.RuleNotARoot || !slices.Equal(e.Paths, []string{root}) {
		t.Errorf("file root: %+v", e)
	}

	// not-a-root comes before exists.
	home, s := newRoot(t, 1)
	s.Close()
	os.WriteFile(filepath.Join(home, "koan-root", "x"), nil, 0o644)
	_, _, _, e = Init(home, ptr(int64(5)))
	if e == nil || e.Rule == nil || *e.Rule != cli.RuleNotARoot {
		t.Errorf("not-a-root and exists: %+v", e)
	}
}

// A database with format 0 and no tables is an unfinished init: init
// finishes it, and until then it is unsupported-format.
func TestUnfinishedInit(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "koan-root")
	os.Mkdir(root, 0o755)
	os.WriteFile(filepath.Join(root, "koan.db"), nil, 0o644)
	if _, e := Open(home); e == nil || e.Kind != cli.KindUnsupportedFormat || !strings.Contains(e.Message, "format 0") {
		t.Errorf("before: %+v", e)
	}
	_, created, first, e := Init(home, ptr(int64(40)))
	if e != nil || first != 40 || !slices.Equal(created, []string{"tree", "archive", "database"}) {
		t.Fatalf("init: %v %d %+v", created, first, e)
	}
	s, e := Open(home)
	if e != nil {
		t.Fatal(e.Message)
	}
	s.Close()

	// Format 0 with a table in it is not koan's to finish.
	home = t.TempDir()
	root = filepath.Join(home, "koan-root")
	os.Mkdir(root, 0o755)
	if _, err := rawDB(t, home).Exec("CREATE TABLE x (y)"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, e := Init(home, nil); e == nil || e.Kind != cli.KindUnsupportedFormat {
		t.Errorf("format 0 with a table: %+v", e)
	}
}

// IDs come from the root's first ID, then from the highest ever issued,
// and are never issued twice.
func TestIssue(t *testing.T) {
	_, s := newRoot(t, 100)
	var got []int64
	write(t, s, func(tx *Tx) error {
		f, err := addFolder(tx, core.RootID, "work")
		if err != nil {
			return err
		}
		a, err := addTask(tx, f, "a")
		if err != nil {
			return err
		}
		g, err := tx.MakeGate(f, core.EndDone)
		got = append(got, f, a, g)
		return err
	})
	write(t, s, func(tx *Tx) error {
		_, err := tx.RemoveTasks([]int64{101}, "")
		return err
	})
	write(t, s, func(tx *Tx) error {
		id, err := addTask(tx, core.RootID, "b")
		got = append(got, id)
		return err
	})
	if !slices.Equal(got, []int64{100, 101, 102, 103}) {
		t.Errorf("issued %v", got)
	}
	st, e := s.Stats()
	if e != nil || st.NextID != 104 || st.Tasks != 2 || st.Folders != 1 {
		t.Errorf("stats %+v %+v", st, e)
	}

	// A root issuing from far past what an earlier one issued.
	home, s2 := newRoot(t, 1)
	if _, err := rawDB(t, home).Exec("UPDATE root SET first_id = 5000"); err != nil {
		t.Fatal(err)
	}
	write(t, s2, func(tx *Tx) error {
		id, err := addTask(tx, core.RootID, "x")
		if id != 5000 {
			t.Errorf("issued %d, want 5000", id)
		}
		return err
	})
}

func TestMakeGate(t *testing.T) {
	home, s := newRoot(t, 1)
	write(t, s, func(tx *Tx) error {
		f, err := addFolder(tx, core.RootID, "work")
		if err != nil {
			return err
		}
		if _, err := tx.MakeGate(f, core.EndStart); err != nil {
			return err
		}
		_, err = tx.MakeGate(f, core.EndDone)
		return err
	})
	rows, err := rawDB(t, home).Query(`SELECT t.id, t.folder, t.gate, t.title, t.priority IS NULL, t.created_at = t.updated_at,
		t.completed_at IS NULL, t.extra, (SELECT count(*) FROM tag WHERE task = t.id), i.kind, i.state
		FROM task t JOIN issued i USING (id) ORDER BY t.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, folder, tags int64
		var gate, title, extra, kind, state string
		var nullPriority, same, open bool
		rows.Scan(&id, &folder, &gate, &title, &nullPriority, &same, &open, &extra, &tags, &kind, &state)
		if !nullPriority || !same || !open || extra != "{}" || tags != 0 || kind != "task" || state != "live" {
			t.Errorf("gate @%d: priority null %v, times same %v, open %v, extra %s, tags %d, %s %s",
				id, nullPriority, same, open, extra, tags, kind, state)
		}
		got = append(got, strings.Join([]string{gate, title}, " "))
	}
	if !slices.Equal(got, []string{"start Start", "done Done"}) {
		t.Errorf("gates %v", got)
	}
}

func ptr[T any](v T) *T { return &v }

// steps is a fake Steps: it records what it is given and answers as told.
type steps struct {
	carried [][]int64
	stuck   map[int64]bool // changes to report stuck
	fail    map[int64]bool // seqs that fail and stay pending
}

func (f *steps) Carry(rows []Pending) Outcome {
	var out Outcome
	var seqs []int64
	for _, r := range rows {
		seqs = append(seqs, r.Seq)
		switch {
		case f.stuck[r.Change]:
			if !slices.Contains(out.Stuck, r.Change) {
				out.Stuck = append(out.Stuck, r.Change)
			}
		case f.fail[r.Seq]:
		default:
			out.Done = append(out.Done, r.Seq)
		}
	}
	f.carried = append(f.carried, seqs)
	return out
}

func pendingRows(t *testing.T, s *Store) (live, stuck []int64) {
	t.Helper()
	rows, err := s.db.conn.QueryContext(t.Context(), "SELECT seq, stuck FROM pending ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var st bool
		rows.Scan(&seq, &st)
		if st {
			stuck = append(stuck, seq)
		} else {
			live = append(live, seq)
		}
	}
	return live, stuck
}

func step(t *testing.T, tx *Tx, s string) int64 {
	t.Helper()
	seq, err := tx.AddStep(json.RawMessage(s))
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// The steps of a write: a change's steps are carried out after COMMIT and
// their rows deleted; steps left pending are finished first by the next
// write; stuck changes are set aside with every step, and skipped.
func TestWriteSteps(t *testing.T) {
	_, s := newRoot(t, 1)

	// With no Steps, everything stays pending.
	write(t, s, func(tx *Tx) error { step(t, tx, `{"a":1}`); return nil })
	if live, _ := pendingRows(t, s); !slices.Equal(live, []int64{1}) {
		t.Fatalf("pending %v", live)
	}

	f := &steps{stuck: map[int64]bool{}, fail: map[int64]bool{}}
	s.Steps = f
	// Step 2 finishes seq 1; step 7 carries out this change's two steps,
	// sharing one change, of which the second fails.
	var first, second, change int64
	write(t, s, func(tx *Tx) error {
		if len(f.carried) != 1 || !slices.Equal(f.carried[0], []int64{1}) {
			t.Errorf("step 2 carried %v", f.carried)
		}
		first = step(t, tx, `{"b":1}`)
		second = step(t, tx, `{"b":2}`)
		f.fail[second] = true
		change = tx.change
		return nil
	})
	if change != first || second != first+1 {
		t.Errorf("seqs %d, %d in change %d", first, second, change)
	}
	if live, _ := pendingRows(t, s); !slices.Equal(live, []int64{second}) {
		t.Errorf("pending after a failed step: %v", live)
	}

	// The next write finishes it first.
	f.fail = map[int64]bool{}
	f.carried = nil
	write(t, s, func(tx *Tx) error { return nil })
	if len(f.carried) != 1 || !slices.Equal(f.carried[0], []int64{second}) {
		t.Errorf("carried %v", f.carried)
	}

	// A stuck change is marked with every step, and later writes skip it.
	var stuck []int64
	write(t, s, func(tx *Tx) error {
		stuck = append(stuck, step(t, tx, `{"c":1}`), step(t, tx, `{"c":2}`))
		f.stuck[tx.change] = true
		return nil
	})
	if live, got := pendingRows(t, s); len(live) != 0 || !slices.Equal(got, stuck) {
		t.Errorf("stuck: live %v, stuck %v", live, got)
	}
	f.carried = nil
	write(t, s, func(tx *Tx) error { step(t, tx, `{"d":1}`); return nil })
	for _, c := range f.carried {
		for _, seq := range c {
			if slices.Contains(stuck, seq) {
				t.Errorf("carried stuck seq %d", seq)
			}
		}
	}
	if live, got := pendingRows(t, s); len(live) != 0 || !slices.Equal(got, stuck) {
		t.Errorf("after: live %v, stuck %v", live, got)
	}
	st, _ := s.Stats()
	if st.Pending != 2 || st.Stuck != 2 {
		t.Errorf("stats %+v", st)
	}
}

// A refused change writes nothing, and its error is the one reported.
func TestWriteRefused(t *testing.T) {
	_, s := newRoot(t, 1)
	_, _, e := s.Write(func(tx *Tx) (any, []cli.Warning, error) {
		addTask(tx, core.RootID, "a")
		step(t, tx, `{}`)
		return nil, nil, core.Conflict(core.RuleClaimed, []int64{1}, nil, "@1 is claimed")
	})
	if e == nil || e.Kind != cli.KindConflict || *e.Rule != cli.RuleClaimed || !slices.Equal(e.IDs, []int64{1}) {
		t.Fatalf("error %+v", e)
	}
	st, _ := s.Stats()
	if st.Tasks != 0 || st.Pending != 0 || st.NextID != 1 {
		t.Errorf("a refused change left %+v", st)
	}
	// An error SQLite reports in the change fails it too.
	_, _, e = s.Write(func(tx *Tx) (any, []cli.Warning, error) {
		_, err := tx.Exec("INSERT INTO task (id, title, created_at, updated_at, extra) VALUES (1, 'x', 'a', 'a', '{}')")
		return nil, nil, err
	})
	if e == nil || e.Kind != cli.KindInternal {
		t.Errorf("foreign key: %+v", e)
	}
	// An error passed as an envelope error is reported as it is.
	_, _, e = s.Write(func(tx *Tx) (any, []cli.Warning, error) {
		return nil, nil, Err(cli.NewError(cli.KindNotFound, "nothing"))
	})
	if e == nil || e.Kind != cli.KindNotFound {
		t.Errorf("envelope error: %+v", e)
	}
}

// Removing rows: the tasks waiting on a removed one are found through
// edge_blocker and get a new updated_at; those left ready are released.
func TestRemoveTasks(t *testing.T) {
	home, s := newRoot(t, 1)
	db := rawDB(t, home)
	var a, b, c, d, e, x int64
	write(t, s, func(tx *Tx) error {
		for _, p := range []*int64{&a, &b, &c, &d, &e, &x} {
			id, err := addTask(tx, core.RootID, "t")
			if err != nil {
				return err
			}
			*p = id
		}
		// a waits on b; c waits on b and d; e waits on b and is complete.
		for _, edge := range [][2]int64{{a, b}, {c, b}, {c, d}, {e, b}} {
			if _, err := tx.Exec("INSERT INTO edge (task, blocker) VALUES (?, ?)", edge[0], edge[1]); err != nil {
				return err
			}
		}
		_, err := tx.Exec("UPDATE task SET completed_at = ? WHERE id = ?", tx.Now, e)
		tx.Exec("INSERT INTO claim (task, claimed_at, extra) VALUES (?, ?, '{}')", b, tx.Now)
		tx.Exec("INSERT INTO tag (task, tag) VALUES (?, 'x')", b)
		return err
	})
	db.Exec("UPDATE task SET updated_at = '2000-01-01T00:00:00Z'")

	var released []int64
	write(t, s, func(tx *Tx) error {
		var err error
		released, err = tx.RemoveTasks([]int64{b}, "")
		return err
	})
	if !slices.Equal(released, []int64{a}) {
		t.Errorf("released %v, want [%d]", released, a)
	}
	moved := map[int64]bool{}
	rows, _ := db.Query("SELECT id FROM task WHERE updated_at <> '2000-01-01T00:00:00Z'")
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		moved[id] = true
	}
	rows.Close()
	if !moved[a] || !moved[c] || !moved[e] || moved[d] || moved[x] || len(moved) != 3 {
		t.Errorf("updated_at moved on %v", moved)
	}
	var edges, claims, tags int
	db.QueryRow("SELECT (SELECT count(*) FROM edge WHERE task = ?1 OR blocker = ?1), (SELECT count(*) FROM claim), (SELECT count(*) FROM tag)", b).Scan(&edges, &claims, &tags)
	if edges != 0 || claims != 0 || tags != 0 {
		t.Errorf("left edges %d, claims %d, tags %d", edges, claims, tags)
	}
	var state string
	db.QueryRow("SELECT state FROM issued WHERE id = ?", b).Scan(&state)
	if state != "deleted" {
		t.Errorf("issued state %s", state)
	}

	// Archived, an ID is not-found with where it went.
	write(t, s, func(tx *Tx) error {
		_, err := tx.RemoveTasks([]int64{d}, "/archive/d")
		return err
	})
	_, _, er := s.Write(func(tx *Tx) (any, []cli.Warning, error) {
		_, err := core.ResolveString(tx, "@"+itoa(d), core.WantTask, nil)
		return nil, nil, err
	})
	if er == nil || er.Kind != cli.KindNotFound || len(er.Archived) != 1 || er.Archived[0].Archive != "/archive/d" {
		t.Errorf("archived: %+v", er)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
