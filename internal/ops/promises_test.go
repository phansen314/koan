package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
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
	// The four folders take 1 to 4; the tasks, in input order, 5 to 8.
	wantIDs(t, "list", w.ok("list", `{}`)["tasks"], 8, 7, 6, 5)
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

	// A @koan.json from a newer koan is that, whatever else it holds.
	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 3, "name": "x"}`), 0o644)
	w.fail("show", `{"id":1}`, errs.UnsupportedFormat, "")
	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 2, "name": "x"}`), 0o644)
	w.fail("show", `{"id":1}`, errs.Corrupt, "")
}

func TestRepairReportsOnlyWhatItRepaired(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/home"}`) // task 2, in /home, 1
	os.Remove(w.root("home/2.md"))
	for _, dir := range []string{"x", "y"} {
		w.ok("create-folder", `{"folder":"/`+dir+`"}`)
		os.WriteFile(w.root(dir, "2.md"), []byte(dir), 0o644)
	}
	got := w.ok("repair", `{}`)
	if len(got["repaired"].([]any)) != 1 || len(got["findings"].([]any)) != 1 {
		t.Errorf("repaired %v, left %v: want one of each", got["repaired"], got["findings"])
	}
}

// repair fixes only the kinds it is given. Without them, it fixes every kind
// but leaves what removes the ID of a task that is gone. It refuses a kind
// it can't fix.
func TestRepairFixesOnlyTheKindsAsked(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a","notes":"keep"},{"title":"b","blocked_by":["a"]},{"title":"c"}]}`)
	w.ok("claim", `{"id":1}`)
	w.ok("claim", `{"id":3}`)
	state, _ := os.ReadFile(w.env.StatePath())
	w.ok("done", `{"id":3}`)
	os.WriteFile(w.env.StatePath(), state, 0o644) // a claim on 3, which is done
	os.Remove(w.root("1.json"))                   // dangling reference in 2, claim on 1, which is gone, and orphan notes
	os.WriteFile(w.root(".koan-tmp-abc"), nil, 0o644)

	kinds := func(fs any) (ks []string) {
		for _, f := range fs.([]any) {
			f := f.(map[string]any)
			ks = append(ks, fmt.Sprintf("%s %v", f["kind"], f["ids"]))
		}
		return ks
	}
	check := func(input string, repaired, left []string) {
		t.Helper()
		got := w.ok("repair", input)
		if r := kinds(got["repaired"]); !slices.Equal(r, repaired) {
			t.Errorf("repair %s: repaired %v, want %v", input, r, repaired)
		}
		if f := kinds(got["findings"]); !slices.Equal(f, left) {
			t.Errorf("repair %s: left %v, want %v", input, f, left)
		}
	}
	check(`{}`, []string{"temp-leftover []", "stale-claim [3]"},
		[]string{"dangling-reference [2 1]", "stale-claim [1]", "orphan-notes [1]"})
	check(`{"kinds":["stale-claim"]}`, []string{"stale-claim [1]"},
		[]string{"dangling-reference [2 1]", "orphan-notes [1]"})
	check(`{"kinds":["temp-leftover","dangling-reference"]}`, []string{"dangling-reference [2 1]"},
		[]string{"orphan-notes [1]"})

	w.fail("repair", `{"kinds":["cycle"]}`, errs.InvalidInput, "")
	w.fail("repair", `{"kinds":["old-format"]}`, errs.InvalidInput, "")
	w.fail("repair", `{"kinds":[]}`, errs.InvalidInput, "")
	w.fail("repair", `{"kinds":["stale-claim","stale-claim"]}`, errs.InvalidInput, "")
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

func TestDeleteFolderRemovesTheFolderFirst(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a","folder":"/gone"},{"title":"b","blocked_by":["a"]}]}`)
	// /gone is 1, a 2, b 3. The rewrite of task 3 starts with a temp
	// file; fail it.
	w.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpCreateTemp, "", 1, syscall.ENOSPC)}
	w.fail("delete-folder", `{"folder":"/gone","recursive":true}`, errs.IO, "")
	w.env.FS = fsys.OS{}
	if tk := task(w.ok("show", `{"id":3}`)); tk["readiness"] != "blocked" {
		t.Errorf("the dependent was freed though delete-folder failed: %v", tk)
	}
	fs := w.ok("doctor", `{}`)["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["kind"] != "dangling-reference" {
		t.Errorf("doctor: %v", fs)
	}
}

// A command that succeeds leaves nothing for doctor to find.
func TestSuccessLeavesTheTreeHealthy(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a","folder":"/p/q","notes":"n"},{"title":"b","folder":"/p","blocked_by":["a"]},{"title":"c"}]}`)
	for _, step := range [][2]string{
		{"claim", `{"id":3}`}, {"update", `{"id":4,"tags_add":["x"]}`}, {"move", `{"id":3,"to":"/r"}`},
		{"move-folder", `{"folder":"/p","to":"/s/p"}`}, {"done", `{"id":3}`}, {"reopen", `{"id":3}`},
		{"claim-next", `{}`}, {"delete", `{"id":5}`}, {"delete-folder", `{"folder":"/s","recursive":true}`},
		{"delete-folder", `{"folder":"/r","recursive":true}`},
	} {
		w.ok(step[0], step[1])
		if got := w.ok("doctor", `{}`); got["healthy"] != true {
			t.Fatalf("after %s %s: %v", step[0], step[1], got["findings"])
		}
	}
}

// Everything in a batch is checked before anything is written, a folder
// path that runs through a file included.
func TestBatchChecksFoldersFirst(t *testing.T) {
	w := ready(t)
	os.WriteFile(w.root("afile"), nil, 0o644)
	e := w.fail("create-batch", `{"tasks":[{"title":"a"},{"title":"b","folder":"/afile/x"}]}`, errs.Conflict, errs.RuleNotAFolder)
	if len(e.ChangedPaths) != 0 || len(e.IDs) != 0 {
		t.Errorf("changed %v, ids %v: want nothing written", e.ChangedPaths, e.IDs)
	}
	w.fail("create", `{"title":"c","folder":"/afile"}`, errs.Conflict, errs.RuleNotAFolder)
	if id := task(w.ok("create", `{"title":"d"}`))["id"]; id != float64(1) {
		t.Errorf("the refused creates used IDs: next is %v", id)
	}
}

// block can't rule out a cycle through a file it can't read, so it refuses.
func TestBlockRefusesThroughAnUnusableFile(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"ref":"b","title":"b","blocked_by":["a"]},{"title":"c","blocked_by":["b"]},{"title":"d"}]}`)
	good := w.file("2.json")
	os.WriteFile(w.root("2.json"), []byte("{"), 0o644)
	w.fail("block", `{"id":1,"blockers":[3]}`, errs.Corrupt, "") // 3 waits on 2, which can't be read
	w.fail("block", `{"id":1,"blockers":[2]}`, errs.Corrupt, "") // the new blocker itself
	w.ok("block", `{"id":4,"blockers":[1]}`)                     // nothing unreadable on the way
	os.WriteFile(w.root("2.json"), []byte(good), 0o644)
	w.fail("block", `{"id":1,"blockers":[3]}`, errs.Conflict, errs.RuleCycle)
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Errorf("doctor: %v", got)
	}
}

func TestBlockNamesEverythingMissing(t *testing.T) {
	w := ready(t)
	if e := w.fail("block", `{"id":99,"blockers":[98,97]}`, errs.NotFound, ""); len(e.IDs) != 3 {
		t.Errorf("ids %v, want all three", e.IDs)
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

func TestStateFileKeysAreExact(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	w.ok("claim", `{"id":1}`)
	state, _ := os.ReadFile(w.env.StatePath())
	os.WriteFile(w.env.StatePath(), []byte(strings.Replace(string(state), `"claimed_at"`, `"Claimed_At"`, 1)), 0o600)
	w.fail("show", `{"id":1}`, errs.Corrupt, "")
}

// delete removes a task's notes, and nothing else that has their name.
func TestDeleteLeavesWhatIsNotNotes(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	os.Remove(w.root("1.md"))
	os.Mkdir(w.root("1.md"), 0o755)
	w.ok("delete", `{"id":1}`)
	if fi, err := os.Stat(w.root("1.md")); err != nil || !fi.IsDir() {
		t.Errorf("delete removed a directory named like the notes: %v", err)
	}
}

// findings lists doctor's findings as "kind paths", the paths relative to
// the root.
func (w *world) findings() []string {
	var fs []string
	for _, f := range w.ok("doctor", `{}`)["findings"].([]any) {
		f := f.(map[string]any)
		var paths []string
		for _, p := range f["paths"].([]any) {
			paths = append(paths, strings.TrimPrefix(p.(string), w.disk()+"/"))
		}
		fs = append(fs, f["kind"].(string)+" "+strings.Join(paths, ","))
	}
	return fs
}

// Only what is named with @ is koan's: everything else is material, never
// walked into, whatever its name, @ folders inside it included.
func TestMaterialIsNeverRead(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/p"}`) // /p is 1, the task 2
	for _, dir := range []string{"@p/docs/@inner", "docs", "@p/node_modules/@types"} {
		os.MkdirAll(w.disk(dir), 0o755)
	}
	for _, f := range []string{"@p/docs/@3.json", "@p/docs/@inner/@folder.json", "@p/2026.md", "@p/7.json", "notes.md", ".hidden"} {
		os.WriteFile(w.disk(f), []byte("material"), 0o644)
	}
	if fs := w.findings(); len(fs) != 0 {
		t.Errorf("doctor found %v in material", fs)
	}
	got := w.ok("list", `{"include_folders":true}`)
	if folders, _ := json.Marshal(got["folders"]); string(folders) != `["/","/p"]` {
		t.Errorf("folders %s", folders)
	}
	wantIDs(t, "tasks", got["tasks"], 2)
	// A non-recursive delete-folder refuses visible material, and notes.
	w.fail("delete-folder", `{"folder":"/p"}`, errs.Conflict, errs.RuleNotEmpty)
	w.ok("create-folder", `{"folder":"/q"}`)
	os.WriteFile(w.root("q/folder.md"), []byte("notes"), 0o644)
	w.fail("delete-folder", `{"folder":"/q"}`, errs.Conflict, errs.RuleNotEmpty)
	os.WriteFile(w.root("q/folder.md"), nil, 0o644)
	os.WriteFile(w.disk("@q/.gitignore"), nil, 0o644)
	w.ok("delete-folder", `{"folder":"/q"}`)
}

// An entry named with @ that is none of koan's is reported, and skipped.
func TestStrayAtNamesAreSkipped(t *testing.T) {
	w := ready(t)
	w.ok("create-folder", `{"folder":"/p"}`)
	os.WriteFile(w.disk("@p/@notes.txt"), nil, 0o644)
	os.WriteFile(w.disk("@folder.json"), nil, 0o644) // the root has no folder file
	os.Mkdir(w.disk("@p/@5.json"), 0o755)
	os.Symlink("nowhere", w.disk("@p/@link"))
	want := []string{"skipped-entry @folder.json", "skipped-entry @p/@5.json", "skipped-entry @p/@link", "skipped-entry @p/@notes.txt"}
	if fs := w.findings(); !slices.Equal(fs, want) {
		t.Errorf("findings %v, want %v", fs, want)
	}
}

// A folder made by hand, without a folder file, is still a folder; repair
// gives it a new ID, above every ID in the tree.
func TestRepairGivesAFolderItsFile(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	os.Mkdir(w.root("hand"), 0o755)
	data, _ := os.ReadFile(w.root("1.json"))
	os.WriteFile(w.root("hand/9.json"), []byte(strings.Replace(string(data), `"id": 1`, `"id": 9`, 1)), 0o644)
	if fs := w.findings(); !slices.Equal(fs, []string{"id-above-counter @hand/@9.json", "missing-folder-file @hand"}) {
		t.Errorf("findings %v", fs)
	}
	wantIDs(t, "tasks", w.ok("list", `{}`)["tasks"], 1, 9)
	got := w.ok("repair", `{"kinds":["missing-folder-file"]}`)
	if len(got["repaired"].([]any)) != 1 {
		t.Errorf("repaired %v", got["repaired"])
	}
	if data, _ := os.ReadFile(w.root("hand/folder.json")); !strings.Contains(string(data), `"id": 10`) {
		t.Errorf("folder file %s, want ID 10", data)
	}
	if n := w.ok("info", `{}`)["last_id"]; n != float64(10) {
		t.Errorf("last_id %v, want 10", n)
	}
}

// Folders and tasks share one counter, so an ID in two of their files is a
// duplicate; a folder file koan can't use is reported, and init passes it.
func TestFolderFilesAndIDs(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/p"}`) // /p is 1, the task 2
	w.ok("create-folder", `{"folder":"/q"}`)      // 3
	w.ok("create-folder", `{"folder":"/r"}`)      // 4
	os.WriteFile(w.root("q/folder.json"), []byte(`{"schema":1,"id":2,"title":null,"start":null,"done":null,"extra":{}}`), 0o644)
	os.WriteFile(w.root("r/folder.json"), []byte(`{"schema":1,"id":4}`), 0o644)
	want := []string{"duplicate-id @p/@2.json,@q/@folder.json", "unusable-file @r/@folder.json"}
	if fs := w.findings(); !slices.Equal(fs, want) {
		t.Errorf("findings %v, want %v", fs, want)
	}
	os.Remove(w.env.StatePath())
	w.ok("init", `{}`)
	if n := w.ok("info", `{}`)["last_id"]; n != float64(2) {
		t.Errorf("last_id %v, want 2: /r's file can't be read", n)
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

// A failed command's changed_paths name only what it changed: never the
// temp folder a new folder is built in.
func TestChangedPathsLeaveOutTempFolders(t *testing.T) {
	w := ready(t)
	// The state file, the folder file, the folder's notes, then the task
	// file: fail the fourth.
	w.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpCreateTemp, "", 4, syscall.ENOSPC)}
	e := w.fail("create", `{"title":"a","folder":"/new"}`, errs.IO, "")
	w.env.FS = fsys.OS{}
	if !slices.Contains(e.ChangedPaths, w.root("new")) {
		t.Errorf("changed_paths %v lack the folder made", e.ChangedPaths)
	}
	for _, p := range e.ChangedPaths {
		if strings.Contains(p, fsys.TempPrefix) {
			t.Errorf("changed_paths %v name a temp folder", e.ChangedPaths)
		}
	}
	if fs := w.findings(); len(fs) != 0 {
		t.Errorf("findings %v", fs)
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

// A folder file koan can't use is reported as that, never as missing: a
// symlink, or one in another format. repair leaves it, and the counter.
func TestUnusableFolderFiles(t *testing.T) {
	w := ready(t)
	w.ok("create-folder", `{"folder":"/p"}`)
	w.ok("create-folder", `{"folder":"/q"}`)
	os.Remove(w.root("p/folder.json"))
	os.Symlink("nowhere", w.root("p/folder.json"))
	os.WriteFile(w.root("q/folder.json"), []byte(`{"schema":2,"id":2}`), 0o644)
	want := []string{"unusable-file @p/@folder.json", "unusable-file @q/@folder.json"}
	if fs := w.findings(); !slices.Equal(fs, want) {
		t.Errorf("findings %v, want %v", fs, want)
	}
	if got := w.ok("repair", `{}`); len(got["repaired"].([]any)) != 0 {
		t.Errorf("repaired %v", got["repaired"])
	}
	if n := w.ok("info", `{}`)["last_id"]; n != float64(2) {
		t.Errorf("last_id %v, want 2", n)
	}
	// Nor is a directory in the folder file's place the folder's own.
	w.ok("create-folder", `{"folder":"/r"}`)
	os.Remove(w.root("r/folder.json"))
	os.Mkdir(w.root("r/folder.json"), 0o755)
	w.fail("delete-folder", `{"folder":"/r"}`, errs.Conflict, errs.RuleNotEmpty)
}

// repair's new folder ID is above an ID that notes alone still hold.
func TestRepairSkipsIDsHeldByNotes(t *testing.T) {
	w := ready(t)
	os.Mkdir(w.root("hand"), 0o755)
	os.WriteFile(w.root("hand/6.md"), []byte("kept"), 0o644)
	w.ok("repair", `{}`)
	if data, _ := os.ReadFile(w.root("hand/folder.json")); !strings.Contains(string(data), `"id": 7`) {
		t.Errorf("folder file %s, want ID 7", data)
	}
}

// A recursive delete-folder removes material only when forced: it lists
// what it found, at any depth but never inside it. Hidden entries don't
// count.
func TestDeleteFolderRefusesMaterial(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/p/q","notes":"n"}`)
	os.WriteFile(w.disk("@p/.gitignore"), nil, 0o644)
	os.MkdirAll(w.disk("@p/.obsidian"), 0o755)
	w.ok("delete-folder", `{"folder":"/p","recursive":true}`) // only koan's and hidden entries

	w.ok("create", `{"title":"b","folder":"/p/q"}`)
	os.MkdirAll(w.disk("@p/@q/docs/deep"), 0o755)
	os.WriteFile(w.disk("@p/@q/docs/deep/plan.md"), nil, 0o644)
	os.WriteFile(w.disk("@p/@stray.txt"), nil, 0o644)
	e := w.fail("delete-folder", `{"folder":"/p","recursive":true}`, errs.Conflict, errs.RuleHasMaterial)
	want := []string{w.disk("@p/@stray.txt"), w.disk("@p/@q/docs")}
	if !slices.Equal(e.Paths, want) {
		t.Errorf("paths %v, want %v", e.Paths, want)
	}
	if len(e.ChangedPaths) != 0 {
		t.Errorf("changed %v", e.ChangedPaths)
	}
	got := w.ok("delete-folder", `{"folder":"/p","recursive":true,"force":true}`)
	wantIDs(t, "deleted", got["deleted"], 6)
	if _, err := os.Stat(w.disk("@p")); !os.IsNotExist(err) {
		t.Errorf("the folder is still there: %v", err)
	}
}
