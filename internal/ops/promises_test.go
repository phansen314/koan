package ops

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/store"
)

// These tests each pin one sentence of spec/operations.md or spec/design.md
// that the broader tests would not notice breaking.

func TestTreeOrderPutsChildrenBeforeLaterSiblings(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"title":"a","folder":"/proj-b"},{"title":"b","folder":"/proj/travel"},{"title":"c","folder":"/proj"},{"title":"d","folder":"/Zed"}]}`)
	wantIDs(t, "list", w.ok("list", `{}`)["tasks"], 4, 3, 2, 1)
	folders, _ := json.Marshal(w.ok("list", `{"include_folders":true}`)["folders"])
	if string(folders) != `["/","/Zed","/proj","/proj/travel","/proj-b"]` {
		t.Errorf("folders %s", folders)
	}
}

func (w *world) file(rel string) string {
	w.t.Helper()
	data, err := os.ReadFile(w.root(rel))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(data)
}

func TestWhatChangesUpdatedAt(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]}]}`)
	created := task(w.ok("show", `{"id":2}`))["updated_at"]
	w.now = w.now.Add(time.Hour)

	// Moving a task changes nothing in its file.
	before := w.file("2.json")
	w.ok("move", `{"id":2,"to":"/x"}`)
	if w.file("x/2.json") != before {
		t.Error("move changed the task file")
	}
	// Asking for what already holds writes nothing: the file is not even
	// replaced by an identical one.
	w.ok("done", `{"id":1}`)
	fi, _ := os.Stat(w.root("1.json"))
	if got := w.ok("done", `{"id":1}`); got["changed"] != false {
		t.Error("done twice reports a change")
	}
	if again, _ := os.Stat(w.root("1.json")); !os.SameFile(fi, again) {
		t.Error("a done that changed nothing still rewrote the file")
	}
	// A task whose blocked_by a delete changes is updated.
	w.ok("delete", `{"id":1}`)
	if got := task(w.ok("show", `{"id":2}`))["updated_at"]; got == created {
		t.Error("a dependent rewritten by delete kept its old updated_at")
	}
}

func TestClaimsEndWithTheirTasks(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"title":"a"},{"title":"b"}]}`)
	w.ok("claim", `{"id":1}`)
	w.ok("claim", `{"id":2}`)
	w.ok("delete", `{"id":1}`)
	if n := w.ok("info", `{}`)["claims"]; n != float64(1) {
		t.Errorf("delete left the claim: %v claims", n)
	}
	// A claim left on a done task, as a crash between done's two writes
	// leaves it, must not come back when the task is reopened.
	done := "2026-10-11T12:00:00Z"
	os.WriteFile(w.root("2.json"), []byte(strings.Replace(w.file("2.json"), `"completed_at": null`, `"completed_at": "`+done+`"`, 1)), 0o644)
	if tk := task(w.ok("reopen", `{"id":2}`)); tk["claim"] != nil {
		t.Errorf("a reopened task is claimed: %v", tk["claim"])
	}
	wantIDs(t, "frontier", w.ok("frontier", `{}`)["tasks"], 2)
}

func TestClaimedFilter(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"title":"a"},{"title":"b"}]}`)
	w.ok("claim", `{"id":1}`)
	wantIDs(t, "claimed", w.ok("list", `{"claimed":true}`)["tasks"], 1)
	wantIDs(t, "unclaimed", w.ok("list", `{"claimed":false}`)["tasks"], 2)
	wantIDs(t, "both", w.ok("list", `{}`)["tasks"], 1, 2)
}

func TestBatchRefsPointBackwards(t *testing.T) {
	w := ready(t)
	w.fail("create-batch", `{"tasks":[{"title":"a","blocked_by":["b"]},{"ref":"b","title":"b"}]}`, errs.InvalidInput, "")
	w.fail("create-batch", `{"tasks":[{"ref":"a","title":"a","blocked_by":["a"]}]}`, errs.InvalidInput, "")
	w.fail("create-batch", `{"tasks":[{"title":"a","blocked_by":["nope"]}]}`, errs.InvalidInput, "")
	w.fail("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"ref":"a","title":"b"}]}`, errs.InvalidInput, "")
	if id := task(w.ok("create", `{"title":"x"}`))["id"]; id != float64(1) {
		t.Errorf("a refused batch used IDs: next is %v", id)
	}
}

func TestMoveReplacesAnEmptyNotesFile(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","notes":"mine"}`)
	w.ok("create-folder", `{"folder":"/x"}`)
	os.WriteFile(w.root("x/1.md"), nil, 0o644)
	w.ok("move", `{"id":1,"to":"/x"}`)
	if w.file("x/1.md") != "mine" {
		t.Errorf("notes after move: %q", w.file("x/1.md"))
	}
}

func TestDoctorTakesTheLock(t *testing.T) {
	w := ready(t)
	held, e := store.Open(w.env, store.NeedUsable, true)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	w.fail("doctor", `{}`, errs.Busy, "")
	w.fail("init", `{}`, errs.Busy, "")
}

// A new task's notes never replace a file already there, and a state file
// rebuilt by init counts notes that lost their task.
func TestCreateNeverOverwritesNotes(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"title":"a"},{"title":"b"}]}`)
	os.WriteFile(w.root("3.md"), []byte("someone's"), 0o644)
	os.Remove(w.env.StatePath())
	w.ok("init", `{}`)
	if id := task(w.ok("create", `{"title":"c"}`))["id"]; id != float64(4) {
		t.Errorf("new ID %v, want 4: 3 is taken by the orphan notes", id)
	}
	if w.file("3.md") != "someone's" {
		t.Errorf("the orphan notes were overwritten: %q", w.file("3.md"))
	}
	// And when the counter is behind anyway, create fails rather than
	// overwrite.
	os.WriteFile(w.root("5.md"), []byte("also someone's"), 0o644)
	e := w.fail("create", `{"title":"d"}`, errs.IO, "")
	if w.file("5.md") != "also someone's" || len(e.IDs) != 1 || e.IDs[0] != 5 {
		t.Errorf("ids %v, notes %q", e.IDs, w.file("5.md"))
	}
}

// A kill between delete's steps must leave something doctor can see: here
// the same state is made by hand, as a failure after the task's removal.
func TestDeleteRemovesTheTaskFirst(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]}]}`)
	// delete's changes to the disk, in order: remove 1.json, remove 1.md,
	// then the rewrite of 2.json, which starts with a temp file.
	w.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpCreateTemp, "", 1, syscall.ENOSPC)}
	e := w.fail("delete", `{"id":1}`, errs.IO, "")
	w.env.FS = fsys.OS{}
	if len(e.ChangedPaths) != 2 {
		t.Errorf("changed_paths %v, want the task's two files", e.ChangedPaths)
	}
	if tk := task(w.ok("show", `{"id":2}`)); tk["readiness"] != "blocked" {
		t.Errorf("the dependent was freed though delete failed: %v", tk)
	}
	fs := w.ok("doctor", `{}`)["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["kind"] != "dangling-reference" {
		t.Errorf("doctor: %v", fs)
	}
}

func TestInitReportsWhatItCreated(t *testing.T) {
	for k, want := range map[int]int{1: 1, 5: 2} { // the 1st and 5th changes after the root is made
		w := newWorld(t)
		seen := 0
		w.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
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

func TestWarningsOncePerFileWhateverTheNarrowing(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"ref":"b","title":"b"},{"title":"c","blocked_by":["a","b"],"tags":["x"]}]}`)
	os.Remove(w.root("1.json"))
	os.Remove(w.root("2.json"))
	for _, input := range []string{`{}`, `{"tags_any":["zzz"]}`, `{"limit":1}`, `{"readiness":["done"]}`} {
		env := w.run("list", input)
		if len(env.Warnings) != 1 || len(env.Warnings[0].IDs) != 3 {
			t.Errorf("list %s: warnings %+v, want one naming the task and both blockers", input, env.Warnings)
		}
	}
}

func TestFilesInOtherShapes(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	good := w.file("1.json")
	// A key that differs only in case is not the key.
	os.WriteFile(w.root("1.json"), []byte(strings.Replace(good, `"extra": {}`, `"extra": {}, "TITLE": "x"`, 1)), 0o644)
	w.fail("show", `{"id":1}`, errs.Corrupt, "")
	os.WriteFile(w.root("1.json"), []byte(good), 0o644)

	w.fail("show", `{"id":1}}`, errs.InvalidInput, "")
	w.fail("show", `{"id":1}]`, errs.InvalidInput, "")
	w.fail("show", `{"id":1} {"id":1}`, errs.InvalidInput, "")

	// A koan.json from a newer koan is that, whatever else it holds.
	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 2, "name": "x"}`), 0o644)
	w.fail("show", `{"id":1}`, errs.UnsupportedFormat, "")
	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 1, "name": "x"}`), 0o644)
	w.fail("show", `{"id":1}`, errs.Corrupt, "")
}

func TestRepairReportsOnlyWhatItRepaired(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/home"}`)
	os.Remove(w.root("home/1.md"))
	for _, dir := range []string{"x", "y"} {
		os.Mkdir(w.root(dir), 0o755)
		os.WriteFile(w.root(dir, "1.md"), []byte(dir), 0o644)
	}
	got := w.ok("repair", `{}`)
	if len(got["repaired"].([]any)) != 1 || len(got["findings"].([]any)) != 1 {
		t.Errorf("repaired %v, left %v: want one of each", got["repaired"], got["findings"])
	}
}

func TestStaleClaimsInIDOrder(t *testing.T) {
	w := ready(t)
	tasks := make([]string, 10)
	for i := range tasks {
		tasks[i] = `{"title":"t"}`
	}
	w.ok("create-batch", `{"tasks":[`+strings.Join(tasks, ",")+`]}`)
	w.ok("claim", `{"id":9}`)
	w.ok("claim", `{"id":10}`)
	os.Remove(w.root("9.json"))
	os.Remove(w.root("10.json"))
	var got []int64
	for _, f := range w.ok("doctor", `{}`)["findings"].([]any) {
		if f := f.(map[string]any); f["kind"] == "stale-claim" {
			got = append(got, ids(f["ids"])...)
		}
	}
	if len(got) != 2 || got[0] != 9 {
		t.Errorf("stale claims on %v, want 9 then 10", got)
	}
}

func TestBlockReportsEveryProblem(t *testing.T) {
	w := ready(t)
	if e := w.fail("block", `{"id":"x","blockers":[]}`, errs.InvalidInput, ""); len(e.Problems) != 2 {
		t.Errorf("problems %v, want both", e.Problems)
	}
}
