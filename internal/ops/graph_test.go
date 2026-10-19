package ops

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
)

// readinessOf is a task's readiness, open_blockers, and inherited, as JSON.
func (w *world) readinessOf(id string) (string, string, string) {
	w.t.Helper()
	tk := task(w.ok("task show", `{"id":`+id+`}`))
	ob, _ := json.Marshal(tk["open_blockers"])
	in, _ := json.Marshal(tk["inherited"])
	return tk["readiness"].(string), string(ob), string(in)
}

// folderState is a folder's readiness and empty.
func (w *world) folderState(f string) (string, bool) {
	w.t.Helper()
	r := w.ok("folder show", `{"folder":"`+f+`"}`)["folder"].(map[string]any)
	return r["readiness"].(string), r["empty"].(bool)
}

// Everything in a folder, at any depth, waits on the folder's blockers, and
// on those of the folders above it.
func TestFoldersBlockWhatIsInThem(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"gate"}`)                   // 1
	w.ok("folder create", `{"folder":"/p","blocked_by":[1]}`) // 2
	w.ok("task create", `{"title":"deep","folder":"/p/q"}`)   // /p/q is 3, the task 4
	if r, ob, in := w.readinessOf("4"); r != "blocked" || ob != "[1]" || in != `[{"folder":"/p","ids":[1]}]` {
		t.Errorf("task under a blocked folder: %s %s %s", r, ob, in)
	}
	if r, _ := w.folderState("/p/q"); r != "blocked" {
		t.Errorf("/p/q: %s", r)
	}
	if got := w.ok("task frontier", `{}`); got["total"] != float64(1) {
		t.Errorf("frontier: %v", got)
	}
	w.ok("task done", `{"id":1}`)
	if r, ob, in := w.readinessOf("4"); r != "ready" || ob != "[]" || in != "[]" {
		t.Errorf("after the gate is done: %s %s %s", r, ob, in)
	}
	// unblock by path, block by path: the ID is what is stored.
	w.ok("task create", `{"title":"later"}`) // 5
	if got := w.ok("folder block", `{"folder":"/p/q","blocked_by":[5]}`); got["changed"] != true || got["id"] != float64(3) {
		t.Errorf("folder block: %v", got)
	}
	if got := w.ok("folder block", `{"folder":"/p/q","blocked_by":["@5"]}`); got["changed"] != false {
		t.Errorf("folder block again: %v", got)
	}
	if !strings.Contains(w.file("p/q/folder.json"), `"blocked_by": [
    5
  ]`) {
		t.Errorf("folder file: %s", w.file("p/q/folder.json"))
	}
	if got := w.ok("folder unblock", `{"folder":"/p/q","blocked_by":[5,77]}`); got["changed"] != true {
		t.Errorf("folder unblock: %v", got)
	}
	w.fail("folder unblock", `{"folder":"/p/q","blocked_by":["/nope"]}`, errs.NotFound, "")
	w.fail("folder block", `{"folder":"/","blocked_by":[5]}`, errs.InvalidInput, "")
	w.fail("folder block", `{"folder":"/p","blocked_by":["/"]}`, errs.InvalidInput, "")
	w.fail("folder block", `{"folder":"/p","blocked_by":[]}`, errs.InvalidInput, "")
	w.fail("folder block", `{"folder":"/p","blocked_by":[99]}`, errs.NotFound, "")
}

// A folder is done when every task under it is; an empty one is done. What
// waits on a folder is blocked again when a task is added to it.
func TestBlockedByAFolder(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/one"}`)                     // 1
	w.ok("task create", `{"title":"after","blocked_by":["/one"]}`) // 2
	if r, ob, _ := w.readinessOf("2"); r != "ready" || ob != "[]" {
		t.Errorf("blocked by an empty folder: %s %s", r, ob)
	}
	if r, empty := w.folderState("/one"); r != "done" || !empty {
		t.Errorf("/one: %s %v", r, empty)
	}
	w.ok("task create", `{"title":"work","folder":"/one/sub"}`) // /one/sub 3, task 4
	if r, ob, _ := w.readinessOf("2"); r != "blocked" || ob != "[1]" {
		t.Errorf("blocked by a folder with open work: %s %s", r, ob)
	}
	why := w.ok("task why", `{"id":2}`)
	wantIDs(t, "why ready", why["ready"], 4)
	w.ok("task done", `{"id":4}`)
	if r, _, _ := w.readinessOf("2"); r != "ready" {
		t.Errorf("after the folder's work is done: %s", r)
	}
	// Done-ness ignores the folder's blockers.
	w.ok("task create", `{"title":"x"}`) // 5
	w.ok("folder block", `{"folder":"/one","blocked_by":[5]}`)
	if r, _ := w.folderState("/one"); r != "done" {
		t.Errorf("a done folder with an open blocker: %s", r)
	}
	why = w.ok("folder why", `{"folder":"/one"}`)
	if why["readiness"] != "done" || len(why["ready"].([]any)) != 0 {
		t.Errorf("folder why of a done folder: %v", why)
	}
	w.ok("task reopen", `{"id":4}`)
	why = w.ok("folder why", `{"folder":"/one"}`)
	wantIDs(t, "folder why ready", why["ready"], 5)
	if why["readiness"] != "blocked" {
		t.Errorf("folder why: %v", why)
	}
}

// The cycle checks: containment by paths, then the graph with a wait and a
// done node per folder.
func TestCyclesThroughFolders(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"in p","folder":"/p"}`) // /p 1, task 2
	w.ok("folder create", `{"folder":"/p/sub"}`)          // 3
	for _, c := range [][2]string{
		{"task block", `{"id":2,"blocked_by":["/p"]}`},
		{"folder block", `{"folder":"/p","blocked_by":["/p"]}`},
		{"folder block", `{"folder":"/p","blocked_by":[1]}`},
		{"folder block", `{"folder":"/p/sub","blocked_by":["/p"]}`},
		{"folder block", `{"folder":"/p","blocked_by":["/p/sub"]}`},
		{"folder block", `{"folder":"/p","blocked_by":[2]}`},
		{"task create", `{"title":"x","folder":"/p/sub","blocked_by":["/p"]}`},
		{"task create", `{"title":"x","folder":"/p/sub","blocks":["/p"]}`},
		{"folder create", `{"folder":"/p/sub/n","blocked_by":["/p"]}`},
		{"folder create", `{"folder":"/p/sub/n","blocks":["/p"]}`},
	} {
		e := w.fail(c[0], c[1], errs.Conflict, errs.RuleCycle)
		if !strings.Contains(e.Message, "contains") || len(e.Problems) != 1 {
			t.Errorf("%s %s: %s %v", c[0], c[1], e.Message, e.Problems)
		}
	}
	if e := w.fail("folder block", `{"folder":"/p","blocked_by":["/p"]}`, errs.Conflict, errs.RuleCycle); e.Message != "/p contains /p: a folder can't wait on itself" || !slices.Equal(e.IDs, []int64{1}) {
		t.Errorf("a folder blocked by itself: %q %v", e.Message, e.IDs)
	}

	// /q waits on task 4; a new task in /q that blocks 4 inherits 4.
	w.ok("task create", `{"title":"gate"}`)                   // 4
	w.ok("folder create", `{"folder":"/q","blocked_by":[4]}`) // 5
	e := w.fail("task create", `{"title":"new","folder":"/q","blocks":[4]}`, errs.Conflict, errs.RuleCycle)
	if !slices.Equal(e.IDs, []int64{5, 4}) || !slices.Equal(e.Folders, []string{"/q"}) {
		t.Errorf("a new task inheriting what it blocks: ids %v folders %v", e.IDs, e.Folders)
	}
	if !slices.ContainsFunc(e.Problems, func(p errs.Problem) bool { return p.Field == "/blocks/0" }) {
		t.Errorf("problems %v", e.Problems)
	}
	// A new folder under /q blocking 4: a task added to it would wait on
	// itself.
	e = w.fail("folder create", `{"folder":"/q/n","blocks":[4]}`, errs.Conflict, errs.RuleCycle)
	if !strings.Contains(e.Message, "a task added to /q/n") {
		t.Errorf("message %q", e.Message)
	}
	// 6 waits on /r being done; a new /r/n blocked by 6.
	w.ok("folder create", `{"folder":"/r"}`)                       // 6
	w.ok("task create", `{"title":"after r","blocked_by":["/r"]}`) // 7
	w.fail("folder create", `{"folder":"/r/n","blocked_by":[7]}`, errs.Conflict, errs.RuleCycle)
	// Two folders that wait on each other, even empty.
	w.ok("folder create", `{"folder":"/s"}`) // 8
	w.ok("folder block", `{"folder":"/s","blocked_by":["/r"]}`)
	w.fail("folder block", `{"folder":"/r","blocked_by":["/s"]}`, errs.Conflict, errs.RuleCycle)
	// None of that used an ID.
	if id := w.ok("task create", `{"title":"next"}`)["id"]; id != float64(9) {
		t.Errorf("next ID %v", id)
	}

	// Valid: a task waiting on a folder under its own, and a folder waiting
	// on a task in its parent.
	w.ok("folder create", `{"folder":"/v/x"}`)                               // /v 10, /v/x 11
	w.ok("task create", `{"title":"t","folder":"/v","blocked_by":["/v/x"]}`) // 12
	w.ok("task create", `{"title":"u","folder":"/v"}`)                       // 13
	w.ok("folder block", `{"folder":"/v/x","blocked_by":[13]}`)
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Errorf("doctor: %v", got)
	}
}

// A cycle the graph finds reports its steps and the input that closes it.
func TestCycleErrors(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/a"}`)           // 1
	w.ok("folder create", `{"folder":"/b"}`)           // 2
	w.ok("task create", `{"title":"x","folder":"/a"}`) // 3
	w.ok("task block", `{"id":3,"blocked_by":["/b"]}`)
	e := w.fail("folder block", `{"folder":"/b","blocked_by":["/a"]}`, errs.Conflict, errs.RuleCycle)
	if !slices.Equal(e.IDs, []int64{2, 1, 3}) || !slices.Equal(e.Folders, []string{"/b", "/a"}) {
		t.Errorf("ids %v folders %v", e.IDs, e.Folders)
	}
	if want := []errs.Problem{{Field: "/blocked_by/0", Reason: "closes a cycle"}}; !slices.Equal(e.Problems, want) {
		t.Errorf("problems %v", e.Problems)
	}
	if !strings.HasPrefix(e.Message, "this would make a cycle: everything in /b waits on /a being done") {
		t.Errorf("message %q", e.Message)
	}
	// In a batch, a new task is named by its ref, and the entry pointed at.
	e = w.fail("batch", `{"folders":[{"folder":"/c","blocked_by":["/d"]},{"folder":"/d"}],"tasks":[{"ref":"t","title":"t","folder":"/d","blocked_by":["/c"]}]}`, errs.Conflict, errs.RuleCycle)
	if !strings.HasPrefix(e.Message, "this would make a cycle: everything in /c waits on /d being done") ||
		!strings.Contains(e.Message, "the new task t") || len(e.IDs) != 0 || !slices.Equal(e.Folders, []string{"/c", "/d"}) {
		t.Errorf("batch cycle: %q %v %v", e.Message, e.IDs, e.Folders)
	}
	if want := []errs.Problem{{Field: "/folders/0/blocked_by/0", Reason: "closes a cycle"}, {Field: "/tasks/0/folder", Reason: "closes a cycle"}, {Field: "/tasks/0/blocked_by/0", Reason: "closes a cycle"}}; !slices.Equal(e.Problems, want) {
		t.Errorf("batch cycle problems %v", e.Problems)
	}
	// A cycle that enters a folder entry only at its done node starts at
	// the entry all the same, folder entries before task entries.
	e = w.fail("batch", `{"folders":[{"folder":"/e"}],"tasks":[{"ref":"u","title":"u","blocked_by":["/e"]},{"ref":"v","title":"v","folder":"/e","blocked_by":["u"]}]}`, errs.Conflict, errs.RuleCycle)
	if !strings.HasPrefix(e.Message, "this would make a cycle: /e holds the new task v") || !slices.Equal(e.Folders, []string{"/e"}) {
		t.Errorf("batch cycle through a done node: %q %v", e.Message, e.Folders)
	}
	if id := w.ok("task create", `{"title":"next"}`)["id"]; id != float64(4) {
		t.Errorf("a refused batch used IDs: next is %v", id)
	}
}

// batch folder entries take blocked_by: IDs and folder paths, of the tree
// or of the batch.
func TestBatchFolderBlockers(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"existing"}`) // 1
	got := w.ok("batch", `{"folders":[{"folder":"/proj/one"},{"folder":"/proj/two","blocked_by":["/proj/one",1]}],
		"tasks":[{"title":"a","folder":"/proj/one"},{"title":"b","folder":"/proj/two"},{"title":"c","blocked_by":["/proj"]}]}`)
	// /proj 2, /proj/one 3, /proj/two 4; tasks 5, 6, 7.
	if data, _ := json.Marshal(got["folders"]); string(data) != `{"/proj":2,"/proj/one":3,"/proj/two":4}` {
		t.Errorf("folders %s", data)
	}
	if r, ob, in := w.readinessOf("6"); r != "blocked" || ob != "[1,3]" || in != `[{"folder":"/proj/two","ids":[1,3]}]` {
		t.Errorf("b: %s %s %s", r, ob, in)
	}
	if r, ob, _ := w.readinessOf("7"); r != "blocked" || ob != "[2]" {
		t.Errorf("c: %s %s", r, ob)
	}
	w.fail("batch", `{"folders":[{"folder":"/x","blocked_by":["/nope"]}]}`, errs.NotFound, "")
	w.fail("batch", `{"folders":[{"folder":"/x","blocked_by":["a"]}],"tasks":[{"ref":"a","title":"a"}]}`, errs.InvalidInput, "")
	w.fail("batch", `{"tasks":[{"title":"a","folder":"/x","blocked_by":["/x"]}]}`, errs.Conflict, errs.RuleCycle)
}

// A move is checked for a cycle: under its new parents what is moved
// inherits their blockers, and they hold it.
func TestMovesAndCycles(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"t"}`)                      // 1
	w.ok("folder create", `{"folder":"/g","blocked_by":[1]}`) // 2
	e := w.fail("task move", `{"id":1,"to":"/g"}`, errs.Conflict, errs.RuleCycle)
	if !strings.Contains(e.Message, "/g contains task 1") {
		t.Errorf("message %q", e.Message)
	}
	w.fail("task move", `{"id":1,"to":"/g/deep"}`, errs.Conflict, errs.RuleCycle)
	// x waits on t; /h waits on x: t in /h would wait on itself.
	w.ok("task create", `{"title":"x","blocked_by":[1]}`)     // 3
	w.ok("folder create", `{"folder":"/h","blocked_by":[3]}`) // 4
	w.fail("task move", `{"id":1,"to":"/h"}`, errs.Conflict, errs.RuleCycle)
	// A folder holding x, moved under /h, which waits on x.
	w.ok("task move", `{"id":3,"to":"/m"}`) // /m 5
	w.fail("folder move", `{"folder":"/m","to":"/h/m"}`, errs.Conflict, errs.RuleCycle)
	// What waited on a task's old folder no longer holds it.
	w.ok("folder create", `{"folder":"/k"}`)                     // 6
	w.ok("task create", `{"title":"y","folder":"/k"}`)           // 7
	w.ok("folder create", `{"folder":"/l","blocked_by":["/k"]}`) // 8
	w.ok("task move", `{"id":7,"to":"/l"}`)
	// /l waits on /k being done, so /k can't go under it; elsewhere it can.
	w.fail("folder move", `{"folder":"/k","to":"/l/k"}`, errs.Conflict, errs.RuleCycle)
	if got := w.ok("folder move", `{"folder":"/k","to":"/z/k"}`); got["changed"] != true {
		t.Errorf("folder move: %v", got)
	}
	if r, ob, _ := w.readinessOf("7"); r != "ready" || ob != "[]" {
		t.Errorf("y in /l, which waits on the empty /k: %s %s", r, ob)
	}
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Errorf("doctor: %v", got)
	}
}

// Deleting a task or a folder takes its ID out of every folder file too.
func TestDeletesUnreferenceFolders(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"t"}`)                           // 1
	w.ok("folder create", `{"folder":"/a"}`)                       // 2
	w.ok("folder create", `{"folder":"/b","blocked_by":[1,"/a"]}`) // 3
	w.ok("task create", `{"title":"u","blocked_by":["/a"]}`)       // 4
	got := w.ok("task delete", `{"id":1}`)
	wantIDs(t, "dependents", got["dependents"], 3)
	got = w.ok("folder delete", `{"folder":"/a"}`)
	wantIDs(t, "dependents", got["dependents"], 3, 4)
	if !strings.Contains(w.file("b/folder.json"), `"blocked_by": []`) {
		t.Errorf("/b: %s", w.file("b/folder.json"))
	}
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Errorf("doctor: %v", got)
	}
}

// Unknown blockers hold back: a folder whose file is missing or unusable
// holds back everything in it.
func TestUnknownBlockersHoldBack(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"t","folder":"/a/b"}`) // /a 1, /a/b 2, task 3
	os.Remove(w.root("a/b/folder.json"))
	env := w.run("task show", `{"id":3}`)
	tk := env.Result.(taskResult).Task
	if tk.Readiness != "blocked" || len(tk.Inherited) != 1 || tk.Inherited[0].Folder != "/a/b" || tk.Inherited[0].IDs != nil {
		t.Errorf("task in a folder with no folder file: %+v", tk)
	}
	if len(env.Warnings) != 1 || env.Warnings[0].Kind != errs.WarnUnusableFile {
		t.Errorf("warnings %+v", env.Warnings)
	}
	why := w.ok("task why", `{"id":3}`)
	if data, _ := json.Marshal(why["stuck_folders"]); string(data) != `["/a/b"]` {
		t.Errorf("why: %v", why)
	}
	why = w.ok("folder why", `{"folder":"/a/b"}`)
	if data, _ := json.Marshal(why["stuck_folders"]); string(data) != `["/a/b"]` {
		t.Errorf("folder why of a folder with no folder file: %v", why)
	}
	w.fail("folder block", `{"folder":"/a/b","blocked_by":[1]}`, errs.NotFound, "")
	w.fail("task block", `{"id":3,"blocked_by":["/a/b"]}`, errs.NotFound, "")
	// An unusable one: folder why fails on it; a search that meets it does.
	w.ok("repair", `{}`) // /a/b gets ID 4
	os.WriteFile(w.root("a/folder.json"), []byte("{"), 0o644)
	w.fail("folder why", `{"folder":"/a"}`, errs.Corrupt, "")
	w.ok("task create", `{"title":"u"}`) // 5
	w.fail("task block", `{"id":5,"blocked_by":[3]}`, errs.Corrupt, "")
	// A dangling ID may be the unusable folder's.
	w.ok("task create", `{"title":"v","blocked_by":[5]}`) // 6
	w.ok("task delete", `{"id":5}`)
	os.WriteFile(w.root("6.json"), []byte(strings.Replace(w.file("6.json"), `"blocked_by": []`, `"blocked_by": [1]`, 1)), 0o644)
	env = w.run("task show", `{"id":6}`)
	if !slices.ContainsFunc(env.Warnings, func(w errs.Warning) bool {
		return w.Kind == errs.WarnDanglingReference && strings.Contains(w.Message, "can't be used")
	}) {
		t.Errorf("warnings %+v", env.Warnings)
	}
	for _, f := range w.ok("doctor", `{}`)["findings"].([]any) {
		if f := f.(map[string]any); f["kind"] == errs.FindingDanglingReference && f["repairable"] != false {
			t.Errorf("a dangling reference while a folder file can't be used: %v", f)
		}
	}
}

// doctor finds a cycle through folders, made by hand, and names the
// folders by directory.
func TestDoctorFindsFolderCycles(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/a"}`)                     // 1
	w.ok("folder create", `{"folder":"/b","blocked_by":["/a"]}`) // 2
	os.WriteFile(w.root("a/folder.json"), []byte(`{"schema":2,"id":1,"title":null,"blocked_by":[2],"extra":{}}`), 0o644)
	var cycle map[string]any
	for _, f := range w.ok("doctor", `{}`)["findings"].([]any) {
		if f := f.(map[string]any); f["kind"] == errs.FindingCycle {
			cycle = f
		}
	}
	if cycle == nil {
		t.Fatal("no cycle found")
	}
	wantIDs(t, "cycle ids", cycle["ids"], 1, 2)
	if len(cycle["paths"].([]any)) != 2 || !strings.Contains(cycle["message"].(string), "a task added to") {
		t.Errorf("cycle: %v", cycle)
	}
	// A folder file holding its own ID can't be used.
	os.WriteFile(w.root("a/folder.json"), []byte(`{"schema":2,"id":1,"title":null,"blocked_by":[1],"extra":{}}`), 0o644)
	w.fail("folder show", `{"folder":"/a"}`, errs.Corrupt, "")
}

// An ID held twice fails a command that meets it as a blocker, and no
// other.
func TestDuplicateBlocker(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`)     // 1
	w.ok("folder create", `{"folder":"/f"}`) // 2
	w.ok("task create", `{"title":"b"}`)     // 3
	os.WriteFile(w.root("f/folder.json"), []byte(`{"schema":2,"id":3,"title":null,"blocked_by":[],"extra":{}}`), 0o644)
	w.ok("task show", `{"id":3}`)
	w.fail("task block", `{"id":1,"blocked_by":[3]}`, errs.Conflict, errs.RuleDuplicateID)
	os.WriteFile(w.root("1.json"), []byte(strings.Replace(w.file("1.json"), `"blocked_by": []`, `"blocked_by": [3]`, 1)), 0o644)
	w.fail("task show", `{"id":1}`, errs.Conflict, errs.RuleDuplicateID)
}

// A write that evaluates readiness checks for a duplicated blocker before
// it writes anything: it never fails with duplicate-id having written.
func TestDuplicateBlockerBeforeWrites(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`)                      // 1
	w.ok("task create", `{"title":"b","blocked_by":[1]}`)     // 2
	w.ok("folder create", `{"folder":"/f","blocked_by":[1]}`) // 3
	w.ok("task create", `{"title":"c","folder":"/f"}`)        // 4
	w.ok("folder create", `{"folder":"/g"}`)                  // 5
	os.WriteFile(w.root("g/folder.json"), []byte(`{"schema":2,"id":1,"title":null,"blocked_by":[],"extra":{}}`), 0o644)
	for _, c := range [][2]string{{"task delete", `{"id":2}`}, {"folder delete", `{"folder":"/f","contents":true}`}} {
		if e := w.fail(c[0], c[1], errs.Conflict, errs.RuleDuplicateID); len(e.ChangedPaths) != 0 {
			t.Errorf("%s: changed %v", c[0], e.ChangedPaths)
		}
	}
	if e := w.fail("task claim-next", `{}`, errs.Conflict, errs.RuleDuplicateID); len(e.ChangedPaths) != 0 {
		t.Errorf("task claim-next: changed %v", e.ChangedPaths)
	}
}

// The task or folder a block changes is not looked up as a blocker: an ID
// a folder file holds too fails only what meets it as one.
func TestBlockingADuplicatedID(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`)     // 1
	w.ok("folder create", `{"folder":"/f"}`) // 2
	w.ok("task create", `{"title":"b"}`)     // 3
	os.WriteFile(w.root("f/folder.json"), []byte(`{"schema":2,"id":3,"title":null,"blocked_by":[],"extra":{}}`), 0o644)
	if got := w.ok("task block", `{"id":3,"blocked_by":[1]}`); got["changed"] != true {
		t.Errorf("task block: %v", got)
	}
	w.fail("task block", `{"id":1,"blocked_by":[3]}`, errs.Conflict, errs.RuleDuplicateID)
}

// folder why of a folder with no folder file names it in its own
// stuck_folders, though only an unusable task file keeps it from done.
func TestFolderWhyWithNoFolderFile(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/f"}`) // /f 1, task 2
	os.Remove(w.root("f/folder.json"))
	os.WriteFile(w.root("f/2.json"), []byte("{"), 0o644)
	why := w.ok("folder why", `{"folder":"/f"}`)
	if data, _ := json.Marshal(why["stuck_folders"]); string(data) != `["/f"]` {
		t.Errorf("folder why: %v", why)
	}
	wantIDs(t, "stuck", why["stuck"], 2)
}

// A cycle check searches only from its new edges, so a file it can't use
// that only the old ones lead to never refuses it (spec/implementation.md,
// The graph).
func TestCycleCheckSearchesFromTheNewEdges(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"one"}`)   // 1
	w.ok("task create", `{"title":"two"}`)   // 2
	w.ok("task create", `{"title":"three"}`) // 3
	w.ok("task block", `{"id":1,"blocked_by":[3]}`)
	os.WriteFile(w.root("3.json"), []byte("{"), 0o644)
	w.ok("task block", `{"id":1,"blocked_by":[2]}`)
	// A folder file that can't be used above what is blocked.
	w.ok("task create", `{"title":"in b","folder":"/a/b"}`) // /a 4, /a/b 5, task 6
	os.WriteFile(w.root("a/folder.json"), []byte("{"), 0o644)
	w.ok("folder block", `{"folder":"/a/b","blocked_by":[2]}`)
	w.ok("task block", `{"id":6,"blocked_by":[2]}`)
	// A new blocker that leads to it is still refused.
	w.ok("task create", `{"title":"under a","folder":"/a"}`) // 7
	w.fail("task block", `{"id":2,"blocked_by":[7]}`, errs.Corrupt, "")
}

// A rename keeps the parent, and a move to the top level adds no edge: they
// make no search. A move into another folder can't rule out a cycle through
// the folder's unknown blockers (spec/operations.md, Folder and Cycles).
func TestMovesThatMakeNoSearch(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"t","folder":"/p/m"}`) // /p 1, /p/m 2, task 3
	os.WriteFile(w.root("p/m/3.json"), []byte("{"), 0o644)
	w.ok("folder move", `{"folder":"/p/m","to":"/p/n"}`)
	w.ok("folder move", `{"folder":"/p/n","to":"/n"}`)
	os.WriteFile(w.root("n/folder.json"), []byte("{"), 0o644)
	env := w.run("folder move", `{"folder":"/n","to":"/o"}`)
	if !env.OK || result(env)["id"] != nil || !slices.ContainsFunc(env.Warnings, func(wn errs.Warning) bool { return wn.Kind == errs.WarnUnusableFile }) {
		t.Errorf("a top-level move of a folder whose file can't be used: %+v", env)
	}
	w.ok("folder move", `{"folder":"/o","to":"/O"}`)
	w.fail("folder move", `{"folder":"/O","to":"/p/o"}`, errs.Corrupt, "")
}

// Containment errors give the one that would wait first, whatever the
// command changed, and the folders among the two.
func TestContainmentErrors(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"t"}`)                      // 1
	w.ok("folder create", `{"folder":"/x","blocked_by":[1]}`) // 2
	w.ok("task create", `{"title":"in m","folder":"/m"}`)     // /m 3, task 4
	w.ok("task create", `{"title":"in x","folder":"/x"}`)     // 5
	w.ok("folder create", `{"folder":"/x/sub"}`)              // 6
	for _, c := range []struct {
		name, input string
		ids         []int64
		folders     []string
		message     string
	}{
		{"task block", `{"id":5,"blocked_by":["/x"]}`, []int64{5, 2}, []string{"/x"}, "/x contains task 5"},
		{"folder block", `{"folder":"/x","blocked_by":[5]}`, []int64{2, 5}, []string{"/x"}, "/x contains task 5"},
		{"folder block", `{"folder":"/x/sub","blocked_by":["/x"]}`, []int64{6, 2}, []string{"/x/sub", "/x"}, "/x contains /x/sub"},
		{"folder block", `{"folder":"/x","blocked_by":["/x/sub"]}`, []int64{2, 6}, []string{"/x", "/x/sub"}, "/x contains /x/sub"},
		// In a move, the one that waits is a folder above the destination.
		{"task move", `{"id":1,"to":"/x/y"}`, []int64{2, 1}, []string{"/x"}, "/x contains task 1"},
	} {
		e := w.fail(c.name, c.input, errs.Conflict, errs.RuleCycle)
		if !slices.Equal(e.IDs, c.ids) || !slices.Equal(e.Folders, c.folders) || !strings.HasPrefix(e.Message, c.message) {
			t.Errorf("%s %s: ids %v folders %v message %q", c.name, c.input, e.IDs, e.Folders, e.Message)
		}
	}
	w.ok("folder block", `{"folder":"/x","blocked_by":[4]}`)
	e := w.fail("folder move", `{"folder":"/m","to":"/x/m"}`, errs.Conflict, errs.RuleCycle)
	if !slices.Equal(e.IDs, []int64{2, 4}) || !slices.Equal(e.Folders, []string{"/x"}) {
		t.Errorf("folder move under what waits on its task: ids %v folders %v", e.IDs, e.Folders)
	}
}

// A dangling ID in a folder file is reported only by a command that
// evaluates that folder's blockers.
func TestFolderDanglingReportedWhenEvaluated(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"t","folder":"/p"}`) // /p 1, task 2
	os.WriteFile(w.root("p/folder.json"), []byte(strings.Replace(w.file("p/folder.json"), `"blocked_by": []`, `"blocked_by": [99]`, 1)), 0o644)
	dangling := func(env Envelope) bool {
		return slices.ContainsFunc(env.Warnings, func(wn errs.Warning) bool { return wn.Kind == errs.WarnDanglingReference })
	}
	for _, c := range [][2]string{
		{"folder update", `{"folder":"/p","title":"x"}`},
		{"folder unblock", `{"folder":"/p","blocked_by":[98]}`},
		{"task update", `{"id":2,"priority":1}`},
	} {
		if env := w.run(c[0], c[1]); !env.OK || dangling(env) {
			t.Errorf("%s: %+v", c[0], env)
		}
	}
	for _, c := range [][2]string{{"task show", `{"id":2}`}, {"folder show", `{"folder":"/p"}`}} {
		if env := w.run(c[0], c[1]); !env.OK || !dangling(env) {
			t.Errorf("%s: %+v", c[0], env)
		}
	}
}
