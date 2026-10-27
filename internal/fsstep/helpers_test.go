package fsstep

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
	"github.com/phansen314/koan/internal/testrun"
)

// TestMain lets the test binary run as a helper process: one that crashes
// at a fault point, or one inside a case-insensitive mount.
func TestMain(m *testing.M) {
	switch os.Getenv("KOAN_FSSTEP_HELPER") {
	case "":
		os.Exit(m.Run())
	case "crash":
		at, _ := strconv.Atoi(os.Getenv("KOAN_FSSTEP_AT"))
		os.Exit(crashHelper(os.Getenv("HOME"), at))
	case "casefold":
		os.Exit(casefoldHelper(os.Getenv("HOME")))
	}
	os.Exit(2)
}

// newRoot makes a root under a fresh HOME and opens it with its steps
// carried out here.
func newRoot(t *testing.T) (string, *store.Store) {
	t.Helper()
	home := t.TempDir()
	return home, openRoot(t, home)
}

func openRoot(t *testing.T, home string) *store.Store {
	t.Helper()
	first := int64(1)
	if _, _, _, e := store.Init(home, &first); e != nil {
		t.Fatalf("init: %s", e.Message)
	}
	s, e := Open(home)
	if e != nil {
		t.Fatalf("open: %s", e.Message)
	}
	t.Cleanup(s.Close)
	return s
}

// change is a change that adds steps, and makes one task so its commit
// shows in the database.
func change(steps ...Step) store.Change {
	return func(tx *store.Tx) (any, []cli.Warning, error) {
		id, err := tx.Issue(core.NodeTask, true)
		if err != nil {
			return nil, nil, err
		}
		if err := tx.InsertTask(store.TaskRow{ID: id, Folder: core.RootID, Title: "t", Extra: []byte("{}")}, nil); err != nil {
			return nil, nil, err
		}
		for _, s := range steps {
			if _, err := Add(tx, s); err != nil {
				return nil, nil, err
			}
		}
		return nil, nil, nil
	}
}

// write is the test-only write: it commits a change adding steps, and
// fails the test on an error.
func write(t *testing.T, s *store.Store, steps ...Step) []cli.Warning {
	t.Helper()
	_, w, e := s.Write(change(steps...))
	if e != nil {
		t.Fatalf("write: %s: %s", e.Kind, e.Message)
	}
	checkWarnings(t, w)
	return w
}

var (
	schemasOnce sync.Once
	schemas     *testrun.Schemas
	schemasErr  error
)

// checkWarnings checks each warning against its schema.
func checkWarnings(t *testing.T, ws []cli.Warning) {
	t.Helper()
	schemasOnce.Do(func() { schemas, schemasErr = testrun.LoadDir(filepath.Join(testrun.Repo(), "schemas")) })
	if schemasErr != nil {
		t.Fatal(schemasErr)
	}
	for _, w := range ws {
		b, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		if err := schemas.Validate("warning", "", string(b)); err != nil {
			t.Errorf("warning %s: %v", b, err)
		}
		if err := schemas.Order("warning", "", string(b)); err != nil {
			t.Errorf("warning %s: %v", b, err)
		}
	}
}

// kinds is the warnings' kinds, in order.
func kinds(ws []cli.Warning) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Kind)
	}
	return out
}

func warning(ws []cli.Warning, kind string) (cli.Warning, bool) {
	for _, w := range ws {
		if w.Kind == kind {
			return w, true
		}
	}
	return cli.Warning{}, false
}

// files makes files under the root's directory: a name ending / is a
// directory, anything else a file with that content.
func files(t *testing.T, s *store.Store, entries ...string) {
	t.Helper()
	if err := makeFiles(s.Root.Dir, entries...); err != nil {
		t.Fatal(err)
	}
}

func makeFiles(dir string, entries ...string) error {
	for _, e := range entries {
		name, content, _ := strings.Cut(e, "=")
		p := filepath.Join(dir, filepath.FromSlash(name))
		var err error
		if strings.HasSuffix(name, "/") {
			err = os.MkdirAll(p, 0o755)
		} else if err = os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
			err = os.WriteFile(p, []byte(content), 0o644)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// snapshot is the tree and the archive as sorted entries: a directory as
// its path ending /, a file as path=content.
func snapshot(t *testing.T, s *store.Store) []string {
	t.Helper()
	out, err := snap(s.Root.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func snap(dir string) ([]string, error) {
	var out []string
	for _, top := range []string{store.TreeName, store.ArchiveName} {
		base := filepath.Join(dir, top)
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == base {
				return err
			}
			rel := filepath.ToSlash(p[len(dir)+1:])
			if d.IsDir() {
				out = append(out, rel+"/")
				return nil
			}
			b, err := os.ReadFile(p)
			out = append(out, rel+"="+string(b))
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return out, nil
}

func sorted(entries ...string) []string {
	slices.Sort(entries)
	return entries
}

func checkTree(t *testing.T, s *store.Store, want []string) {
	t.Helper()
	if got := snapshot(t, s); !slices.Equal(got, want) {
		t.Errorf("tree:\ngot  %q\nwant %q", got, want)
	}
}

// rows is the pending rows: live ones and stuck ones, by seq.
func rows(t *testing.T, s *store.Store) (live, stuck []store.Pending) {
	t.Helper()
	if e := s.Read(func(tx *store.Tx) error {
		var err error
		live, stuck, err = tx.Pending()
		return err
	}); e != nil {
		t.Fatal(e.Message)
	}
	return live, stuck
}

func seqs(ps []store.Pending) []int64 {
	var out []int64
	for _, p := range ps {
		out = append(out, p.Seq)
	}
	return out
}

func noPending(t *testing.T, s *store.Store) {
	t.Helper()
	if live, stuck := rows(t, s); len(live)+len(stuck) != 0 {
		t.Errorf("pending: live %v, stuck %v", seqs(live), seqs(stuck))
	}
}

// issued counts the IDs issued, to tell whether a change committed.
func issued(t *testing.T, s *store.Store) int64 {
	t.Helper()
	st, e := s.Stats()
	if e != nil {
		t.Fatal(e.Message)
	}
	return st.NextID - 1
}

// faults sets the fault hook for the test.
func faults(t *testing.T, fn func(point string) error) {
	t.Helper()
	store.Fault = fn
	t.Cleanup(func() { store.Fault = nil })
}

// ignoresCase reports whether the root's filesystem ignores case.
func ignoresCase(t *testing.T, s *store.Store) bool {
	t.Helper()
	p := filepath.Join(s.Root.Dir, "probe")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	_, err := os.Lstat(filepath.Join(s.Root.Dir, "PROBE"))
	return err == nil
}

// abs is a path under the root as written.
func abs(s *store.Store, rel string) string {
	return filepath.Join(s.Root.Path, filepath.FromSlash(rel))
}

func absAll(s *store.Store, rels ...string) []string {
	var out []string
	for _, r := range rels {
		out = append(out, abs(s, r))
	}
	return out
}

func describe(ws []cli.Warning) string {
	b, _ := json.Marshal(ws)
	return fmt.Sprint(string(b))
}
