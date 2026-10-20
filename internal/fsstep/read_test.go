package fsstep

import (
	"slices"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/store"
)

// read is the test-only read: it returns the paths given, as a read
// command's result would, and fails the test on an error.
func read(t *testing.T, s *store.Store, paths []Path, during func()) []cli.Warning {
	t.Helper()
	w, e := Read(s, func(tx *store.Tx) ([]Path, error) {
		if during != nil {
			during()
		}
		return paths, nil
	})
	if e != nil {
		t.Fatalf("read: %s: %s", e.Kind, e.Message)
	}
	checkWarnings(t, w)
	return w
}

// leave commits a change whose steps stay pending, as a write that
// crashed after COMMIT leaves them.
func leave(t *testing.T, home string, steps ...Step) {
	t.Helper()
	raw, e := store.Open(home)
	if e != nil {
		t.Fatal(e.Message)
	}
	defer raw.Close()
	if _, _, e := raw.Write(change(steps...)); e != nil {
		t.Fatal(e.Message)
	}
}

// Reads never repair. With steps pending and a write holding the lock,
// the paths returned that the steps bring about are pending-filesystem;
// with no one holding it, the steps have no write carrying them out, and
// give pending-step instead; a write that finished them in between leaves
// nothing to say.
func TestRead(t *testing.T) {
	home, s := newRoot(t)
	paths := []Path{
		{ID: 0, Path: abs(s, "tree")},
		{ID: 7, Path: abs(s, "tree/@a")},
		{ID: 5, Path: abs(s, "tree/@a/@5.md")},
		{ID: 7, Path: abs(s, "tree/@a/@notes.md")},
		{ID: 1, Path: abs(s, "tree/@b/@1.md")},
		{ID: 3, Path: abs(s, "tree/@ab/@3.md")},
	}
	if w := read(t, s, paths, nil); len(w) != 0 {
		t.Errorf("nothing pending: %s", describe(w))
	}

	leave(t, home, Step{Op: OpMkdir, To: "tree/@a"}, Step{Op: OpWrite, To: "tree/@a/@5.md", Content: "five"})
	lock, e := store.TakeLock(s.Root.Dir, s.Root.Path)
	if e != nil {
		t.Fatal(e.Message)
	}
	w := read(t, s, paths, nil)
	lock.Release()
	got, ok := warning(w, "pending-filesystem")
	if !ok || len(w) != 1 || !slices.Equal(got.IDs, []int64{7, 5}) ||
		!slices.Equal(got.Paths, absAll(s, "tree/@a", "tree/@a/@5.md", "tree/@a/@notes.md")) {
		t.Errorf("a write holding the lock: %s", describe(w))
	}

	w = read(t, s, paths, nil)
	got, ok = warning(w, "pending-step")
	if !ok || len(w) != 1 || !slices.Equal(got.Paths, absAll(s, "tree/@a", "tree/@a/@5.md")) {
		t.Errorf("no write: %s", describe(w))
	}
	checkTree(t, s, nil)

	w = read(t, s, paths, func() {
		other, e := Open(home)
		if e != nil {
			t.Fatal(e.Message)
		}
		defer other.Close()
		if _, _, e := other.Write(change()); e != nil {
			t.Fatal(e.Message)
		}
	})
	if len(w) != 0 {
		t.Errorf("finished in between: %s", describe(w))
	}
	checkTree(t, s, sorted("tree/@a/", "tree/@a/@notes.md=", "tree/@a/@5.md=five"))
}

// Stuck steps give stuck-step on every read, beside the rest.
func TestReadStuck(t *testing.T) {
	home, s := newRoot(t)
	files(t, s, "tree/@1.md=a", "tree/@a/@1.md=b")
	write(t, s, Step{Op: OpMove, From: "tree/@1.md", To: "tree/@a/@1.md"})
	if w := read(t, s, nil, nil); !slices.Equal(kinds(w), []string{"stuck-step"}) {
		t.Errorf("stuck: %s", describe(w))
	}
	leave(t, home, Step{Op: OpMkdir, To: "tree/@c"})
	if w := read(t, s, nil, nil); !slices.Equal(kinds(w), []string{"pending-step", "stuck-step"}) {
		t.Errorf("stuck and pending: %s", describe(w))
	}
}
