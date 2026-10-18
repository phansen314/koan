package ops

import (
	"os"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/schematest"
	"github.com/phansen314/koan/internal/store"
)

func TestInit(t *testing.T) {
	w := newWorld(t)
	w.fail("show", `{"id":1}`, errs.NotInitialized, "")
	if got := w.ok("info", `{}`); got["state"] != "not-initialized" {
		t.Errorf("info before init: %v", got)
	}
	got := w.ok("init", `{}`)
	if c := got["created"].([]any); len(c) != 3 {
		t.Errorf("created %v, want all three", c)
	}
	schematest.Must(t, "root-file", readJSON(t, w.root("koan.json")))
	if c := w.ok("init", `{}`)["created"].([]any); len(c) != 0 {
		t.Errorf("second init created %v", c)
	}
	if got := w.ok("info", `{}`); got["state"] != "usable" || got["last_id"] != float64(0) {
		t.Errorf("info after init: %v", got)
	}
	w.fail("init", `{"path":"/x"}`, errs.InvalidInput, "")
}

// A state file made beside an existing tree starts at the highest ID in it.
func TestInitAdoptsTree(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"title":"a"},{"title":"b","folder":"/proj"}]}`)
	if err := os.Remove(w.env.StatePath()); err != nil {
		t.Fatal(err)
	}
	w.fail("show", `{"id":1}`, errs.NotInitialized, "")
	if c := w.ok("init", `{}`)["created"].([]any); len(c) != 1 || c[0] != "state" {
		t.Errorf("created %v, want state", c)
	}
	// /proj took 1, its tasks 2 and 3: the folder's ID counts too.
	if id := task(w.ok("create", `{"title":"c"}`))["id"]; id != float64(4) {
		t.Errorf("next ID %v, want 4", id)
	}
}

func TestInitRefuses(t *testing.T) {
	w := newWorld(t)
	if err := os.WriteFile(w.env.RootPath(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w.fail("init", `{}`, errs.Conflict, errs.RuleNotAFolder)
	os.Remove(w.env.RootPath())
	w.ok("init", `{}`)
	os.WriteFile(w.root("koan.json"), []byte("{"), 0o644)
	w.fail("init", `{}`, errs.Corrupt, "")
	if got := w.ok("info", `{}`); got["state"] != "unusable" || got["error"] == nil {
		t.Errorf("info on a corrupt root: %v", got)
	}
}

func TestInitReportsWhatItCreated(t *testing.T) {
	for k, want := range map[int]int{1: 1, 5: 2} { // the 1st and 5th changes after the root is made
		w := newWorld(t)
		seen := 0
		w.env.FS = fsys.FS{Hook: func(op fsys.Op) error {
			if op.Mutating && op.Name != fsys.OpMkdir {
				if seen++; seen == k {
					return syscall.ENOSPC
				}
			}
			return nil
		}}
		e := w.fail("init", `{}`, errs.IO, "")
		if len(e.ChangedPaths) != want || e.ChangedPaths[0] != w.env.RootPath() {
			t.Errorf("failing change %d: changed_paths %v, want %d starting with the root", k, e.ChangedPaths, want)
		}
	}
}

// init can't know the highest ID in a tree it can't list.
func TestInitNeedsTheWholeTree(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can list anything")
	}
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/locked"}`)
	os.Remove(w.env.StatePath())
	os.Chmod(w.root("locked"), 0)
	defer os.Chmod(w.root("locked"), 0o755)
	w.fail("init", `{}`, errs.IO, "")
	if _, err := os.Stat(w.env.StatePath()); !os.IsNotExist(err) {
		t.Error("init made a state file without having seen the whole tree")
	}
}

// init takes the counter from folder IDs too, with no task to give one.
func TestInitCountsFolderIDs(t *testing.T) {
	w := ready(t)
	w.ok("create-folder", `{"folder":"/a/b/c"}`)
	os.Remove(w.env.StatePath())
	w.ok("init", `{}`)
	if n := w.ok("info", `{}`)["last_id"]; n != float64(3) {
		t.Errorf("last_id %v, want 3", n)
	}
}

// A tree from before the @ names has no @koan.json: koan finds it not set
// up, and init adopts it with everything in it as material.
func TestATreeInFormatOneIsMaterial(t *testing.T) {
	w := newWorld(t)
	os.MkdirAll(w.disk("proj"), 0o755)
	os.WriteFile(w.disk("koan.json"), []byte(`{"schema": 1}`), 0o644)
	os.WriteFile(w.disk("proj", "1.json"), []byte("{}"), 0o644)
	if got := w.ok("info", `{}`); got["state"] != "not-initialized" {
		t.Errorf("info: %v", got)
	}
	w.ok("init", `{}`)
	if fs := w.findings(); len(fs) != 0 {
		t.Errorf("findings %v", fs)
	}
	if got := w.ok("list", `{}`); got["total"] != float64(0) {
		t.Errorf("list: %v", got)
	}
}

func TestFormats(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	if got := w.ok("migrate", `{}`); got["converted"] != float64(0) || got["from"] != float64(2) {
		t.Errorf("migrate on a current tree: %v", got)
	}

	// Format 1 has no conversion: migrate and doctor refuse it.
	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 1}`), 0o644)
	w.fail("show", `{"id":1}`, errs.MigrationPending, "")
	w.fail("migrate", `{}`, errs.Conflict, errs.RuleNoConversion)
	w.fail("doctor", `{}`, errs.Conflict, errs.RuleNoConversion)

	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 3}`), 0o644)
	w.fail("show", `{"id":1}`, errs.UnsupportedFormat, "")
	w.fail("migrate", `{}`, errs.UnsupportedFormat, "")
	w.fail("doctor", `{}`, errs.UnsupportedFormat, "")
	if got := w.ok("info", `{}`); got["state"] != "too-new" {
		t.Errorf("info: %v", got)
	}
	w.ok("init", `{}`)
	if got := w.ok("version", `{}`); got["format"] != float64(model.Format) {
		t.Errorf("version: %v", got)
	}
}

func TestPrecedence(t *testing.T) {
	w := ready(t)
	// Input comes before everything, HOME included.
	home := w.env.Home
	w.env.Home = "relative"
	w.fail("show", `{"id":"abc"}`, errs.InvalidInput, "")
	w.fail("show", `{"id":1}`, errs.IO, "")
	w.fail("init", `{}`, errs.IO, "")
	w.ok("version", `{}`)
	w.env.Home = home

	// The root's state comes before busy, for init too.
	os.WriteFile(w.root("koan.json"), []byte("{"), 0o644)
	held, e := store.OpenLocked(w.env)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	w.fail("init", `{}`, errs.Corrupt, "")
	w.fail("list", `{}`, errs.Corrupt, "")
}

func TestBusy(t *testing.T) {
	w := ready(t)
	held, e := store.Open(w.env, store.NeedUsable, true)
	if e != nil {
		t.Fatal(e)
	}
	w.fail("create", `{"title":"x"}`, errs.Busy, "")
	w.fail("create", `{"title":""}`, errs.InvalidInput, "") // input is checked before the lock
	w.fail("list", `{}`, errs.Busy, "")                     // a read waits for a write to finish
	w.ok("info", `{}`)                                      // info and version take no lock
	held.Close()
	w.ok("create", `{"title":"x"}`)

	// Readers share the lock with each other, and hold off writers.
	reader, e := store.Open(w.env, store.NeedUsable, false)
	if e != nil {
		t.Fatal(e)
	}
	w.ok("list", `{}`)
	w.fail("create", `{"title":"y"}`, errs.Busy, "")
	reader.Close()
	w.ok("create", `{"title":"y"}`)
}
