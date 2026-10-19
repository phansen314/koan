package ops

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/schematest"
)

func TestCreateAndShow(t *testing.T) {
	w := ready(t)
	got := w.ok("task create", `{"title":"  Book flights ","folder":"/proj/travel","priority":2,"tags":["urgent","travel"],"extra":{"n":1.50,"big":12345678901234567890},"notes":"hello"}`)
	if got["id"] != float64(3) || got["changed"] != true || len(got["dependents"].([]any)) != 0 {
		t.Errorf("create: %v", got)
	}
	tk := task(w.ok("task show", `{"id":3}`))
	if tk["title"] != "Book flights" || tk["folder"] != "/proj/travel" || tk["readiness"] != "ready" {
		t.Errorf("task: %v", tk)
	}
	if fc := got["folders_created"].([]any); len(fc) != 2 || fc[0] != "/proj" {
		t.Errorf("folders_created %v", fc)
	}
	if notes, _ := os.ReadFile(tk["notes_path"].(string)); string(notes) != "hello" {
		t.Errorf("notes %q", notes)
	}
	// The folders made took IDs first, parents first, and were made whole.
	for f, id := range map[string]int{"proj": 1, "proj/travel": 2} {
		data, err := os.ReadFile(w.root(f, "folder.json"))
		if err != nil {
			t.Fatal(err)
		}
		schematest.Must(t, "folder-file", json.RawMessage(data))
		var ff struct{ ID int }
		if json.Unmarshal(data, &ff); ff.ID != id {
			t.Errorf("%s has ID %d, want %d", f, ff.ID, id)
		}
		if n, err := os.ReadFile(w.root(f, "folder.md")); err != nil || len(n) != 0 {
			t.Errorf("%s notes %q %v", f, n, err)
		}
	}
	// Numbers in extra keep their exact text, and sets are sorted.
	file, _ := os.ReadFile(w.root("proj/travel/3.json"))
	for _, want := range []string{`"n": 1.50`, `"big": 12345678901234567890`, "\"travel\",\n    \"urgent\""} {
		if !strings.Contains(string(file), want) {
			t.Errorf("task file lacks %s:\n%s", want, file)
		}
	}
	schematest.Must(t, "task-file", json.RawMessage(file))
	w.fail("task show", `{"id":9}`, errs.NotFound, "")
	w.fail("task show", `{"id":"x"}`, errs.InvalidInput, "")
	e := w.fail("task create", `{"title":" ","tags":["Bad"],"nope":1}`, errs.InvalidInput, "")
	if len(e.Problems) != 3 {
		t.Errorf("want every problem reported, got %v", e.Problems)
	}
	w.fail("task create", `{"title":"x","blocked_by":[7,8]}`, errs.NotFound, "")
	if n := w.ok("task create", `{"title":"next"}`)["id"]; n != float64(4) {
		t.Errorf("a refused create used an ID: next is %v", n)
	}
}

func TestOpenBlockersAndReadiness(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"A"},{"ref":"b","title":"B","blocked_by":["a"]},{"title":"C","blocked_by":["b","a"]}]}`)
	if tk := task(w.ok("task show", `{"id":3}`)); tk["readiness"] != "blocked" {
		t.Errorf("task 3: %v", tk)
	} else {
		wantIDs(t, "open_blockers", tk["open_blockers"], 1, 2)
	}
	e := w.fail("task block", `{"id":1,"blocked_by":[3]}`, errs.Conflict, errs.RuleCycle)
	if !slices.Equal(e.IDs, []int64{1, 3, 2}) && !slices.Equal(e.IDs, []int64{1, 3}) {
		t.Errorf("cycle %v", e.IDs)
	}
	w.fail("task block", `{"id":1,"blocked_by":[1]}`, errs.InvalidInput, "")
	w.fail("task block", `{"id":1,"blocked_by":[99]}`, errs.NotFound, "")
	if got := w.ok("task block", `{"id":3,"blocked_by":[1]}`); got["changed"] != false {
		t.Errorf("blocking by a blocker already there changed something")
	}

	why := w.ok("task why", `{"id":3}`)
	wantIDs(t, "why ready", why["ready"], 1)
	wantIDs(t, "why stuck", why["stuck"])

	w.ok("task done", `{"id":1}`)
	wantIDs(t, "frontier", w.ok("task frontier", `{}`)["tasks"], 2)
	if got := w.ok("task done", `{"id":1}`); got["changed"] != false {
		t.Error("done twice changed something")
	}
	w.ok("task reopen", `{"id":1}`)
	wantIDs(t, "frontier after reopen", w.ok("task frontier", `{}`)["tasks"], 1)

	w.ok("task unblock", `{"id":3,"blocked_by":[2,77]}`)
	wantIDs(t, "blocked_by", task(w.ok("task show", `{"id":3}`))["blocked_by"], 1)

	// Deleting a blocker frees what waited only on it.
	got := w.ok("task delete", `{"id":1}`)
	if got["id"] != float64(1) || task(got)["title"] != "A" {
		t.Errorf("delete: %v", got)
	}
	wantIDs(t, "dependents", got["dependents"], 2, 3)
	wantIDs(t, "frontier after delete", w.ok("task frontier", `{}`)["tasks"], 2, 3)
	if _, err := os.Stat(w.root("1.md")); !os.IsNotExist(err) {
		t.Error("delete left the notes")
	}
}

func TestUpdate(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"t","tags":["a","b"],"extra":{"k":1,"o":{"x":1}}}`)
	w.now = w.now.Add(time.Hour)
	got := w.ok("task update", `{"id":1,"title":"new","priority":5,"tags_add":["c"],"tags_remove":["a"],"extra_merge":{"o":{"y":2},"n":null},"extra_remove":["k"]}`)
	if got["id"] != float64(1) || got["changed"] != true {
		t.Errorf("update: %v", got)
	}
	tk := task(w.ok("task show", `{"id":1}`))
	if tk["title"] != "new" || tk["priority"] != float64(5) || tk["updated_at"] == tk["created_at"] {
		t.Errorf("update: %v", tk)
	}
	extra, _ := json.Marshal(tk["extra"])
	if string(extra) != `{"n":null,"o":{"y":2}}` {
		t.Errorf("extra %s", extra)
	}
	if tags, _ := json.Marshal(tk["tags"]); string(tags) != `["b","c"]` {
		t.Errorf("tags %s", tags)
	}
	if got := w.ok("task update", `{"id":1,"title":"new","priority":5}`); got["changed"] != false {
		t.Error("an update to the same values changed something")
	}
	w.ok("task update", `{"id":1,"priority":null,"tags_replace_all":[],"extra_replace_all":{}}`)
	if tk := task(w.ok("task show", `{"id":1}`)); tk["priority"] != nil {
		t.Errorf("priority not cleared: %v", tk)
	}
	w.fail("task update", `{"id":1}`, errs.InvalidInput, "")
	w.fail("task update", `{"id":1,"tags_add":["x"],"tags_remove":["x"]}`, errs.InvalidInput, "")
	w.fail("task update", `{"id":1,"tags_add":["x"],"tags_replace_all":["y"]}`, errs.InvalidInput, "")
	w.fail("task update", `{"id":1,"extra_merge":{"a":1},"extra_remove":["a"]}`, errs.InvalidInput, "")
}

func TestListAndFrontier(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[
		{"ref":"low","title":"low","folder":"/proj","priority":1,"tags":["x"]},
		{"title":"high","folder":"/proj/sub","priority":9,"tags":["x","y"]},
		{"title":"none","folder":"/Zed"},
		{"title":"blocked","blocked_by":["low"]},
		{"title":"done","folder":"/proj"}]}`)
	// /Zed, /proj, and /proj/sub take 1 to 3, in tree order; the tasks 4 to 8.
	w.ok("task done", `{"id":8}`)

	wantIDs(t, "frontier is in work order", w.ok("task frontier", `{}`)["tasks"], 5, 4, 6)
	wantIDs(t, "list is in tree order", w.ok("task list", `{}`)["tasks"], 7, 6, 4, 5)
	wantIDs(t, "list done", w.ok("task list", `{"readiness":["done"]}`)["tasks"], 8)
	wantIDs(t, "scope", w.ok("task list", `{"folder":"/proj","recursive":false}`)["tasks"], 4)
	wantIDs(t, "tags_all", w.ok("task frontier", `{"tags_all":["x","y"]}`)["tasks"], 5)
	wantIDs(t, "tags_any", w.ok("task frontier", `{"tags_any":["y","q"]}`)["tasks"], 5)

	got := w.ok("task frontier", `{"limit":1,"fields":["title","readiness"]}`)
	if got["total"] != float64(3) || got["truncated"] != true {
		t.Errorf("limit: %v", got)
	}
	if one, _ := json.Marshal(got["tasks"]); string(one) != `[{"id":5,"readiness":"ready","title":"high"}]` {
		t.Errorf("fields: %s", one)
	}
	// folder list: the folders under one, not itself, in tree order.
	if folders := paths(w.ok("folder list", `{}`)["folders"]); folders != `["/Zed","/proj","/proj/sub"]` {
		t.Errorf("folders %s", folders)
	}
	if folders := paths(w.ok("folder list", `{"recursive":false}`)["folders"]); folders != `["/Zed","/proj"]` {
		t.Errorf("folders directly in /: %s", folders)
	}
	if folders := paths(w.ok("folder list", `{"folder":"/proj"}`)["folders"]); folders != `["/proj/sub"]` {
		t.Errorf("folders under /proj: %s", folders)
	}
	got = w.ok("folder list", `{"limit":1,"fields":["title"]}`)
	if one, _ := json.Marshal(got["folders"]); string(one) != `[{"id":1,"path":"/Zed","title":null}]` || got["total"] != float64(3) || got["truncated"] != true {
		t.Errorf("folder list limit and fields: %v", got)
	}
	w.fail("folder list", `{"folder":"/nope"}`, errs.NotFound, "")
	w.fail("folder list", `{"fields":["claim"]}`, errs.InvalidInput, "")
	w.fail("task list", `{"folder":"/nope"}`, errs.NotFound, "")
	w.fail("task frontier", `{"claimed":false}`, errs.InvalidInput, "")
	w.fail("task frontier", `{"fields":["nope"],"limit":0}`, errs.InvalidInput, "")
}

func TestWhatChangesUpdatedAt(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]}]}`)
	created := task(w.ok("task show", `{"id":2}`))["updated_at"]
	w.now = w.now.Add(time.Hour)

	// Moving a task changes nothing in its file.
	before := w.file("2.json")
	w.ok("task move", `{"id":2,"to":"/x"}`)
	if w.file("x/2.json") != before {
		t.Error("move changed the task file")
	}
	// Asking for what already holds writes nothing: the file is not even
	// replaced by an identical one.
	w.ok("task done", `{"id":1}`)
	fi, _ := os.Stat(w.root("1.json"))
	if got := w.ok("task done", `{"id":1}`); got["changed"] != false {
		t.Error("done twice reports a change")
	}
	if again, _ := os.Stat(w.root("1.json")); !os.SameFile(fi, again) {
		t.Error("a done that changed nothing still rewrote the file")
	}
	// A task whose blocked_by a delete changes is updated.
	w.ok("task delete", `{"id":1}`)
	if got := task(w.ok("task show", `{"id":2}`))["updated_at"]; got == created {
		t.Error("a dependent rewritten by delete kept its old updated_at")
	}
}

// A new task's notes never replace a file already there, and a state file
// rebuilt by init counts notes that lost their task.
func TestCreateNeverOverwritesNotes(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"title":"a"},{"title":"b"}]}`)
	os.WriteFile(w.root("3.md"), []byte("someone's"), 0o644)
	os.Remove(w.env.StatePath())
	w.ok("init", `{}`)
	if id := w.ok("task create", `{"title":"c"}`)["id"]; id != float64(4) {
		t.Errorf("new ID %v, want 4: 3 is taken by the orphan notes", id)
	}
	if w.file("3.md") != "someone's" {
		t.Errorf("the orphan notes were overwritten: %q", w.file("3.md"))
	}
	// And when the counter is behind anyway, create starts above the notes.
	os.WriteFile(w.root("5.md"), []byte("also someone's"), 0o644)
	env := w.run("task create", `{"title":"d"}`)
	if !env.OK || result(env)["id"] != float64(6) || !aboveCounter(env, 5) || w.file("5.md") != "also someone's" {
		t.Errorf("create beside notes 5: %+v, notes %q", env, w.file("5.md"))
	}
}

func TestMoveReplacesAnEmptyNotesFile(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","notes":"mine"}`)
	w.ok("folder create", `{"folder":"/x"}`)
	os.WriteFile(w.root("x/1.md"), nil, 0o644)
	w.ok("task move", `{"id":1,"to":"/x"}`)
	if w.file("x/1.md") != "mine" {
		t.Errorf("notes after move: %q", w.file("x/1.md"))
	}
}

// A kill between delete's steps must leave something doctor can see: here
// the same state is made by hand, as a failure after the task's removal.
func TestDeleteRemovesTheTaskFirst(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]}]}`)
	// delete's changes to the disk, in order: remove 1.json, remove 1.md,
	// then the rewrite of 2.json, which starts with a temp file.
	w.env.FS = fsys.FS{Hook: fsys.ErrnoAt(fsys.OpCreateTemp, "", 1, syscall.ENOSPC)}
	e := w.fail("task delete", `{"id":1}`, errs.IO, "")
	w.env.FS = fsys.FS{}
	if len(e.ChangedPaths) != 2 {
		t.Errorf("changed_paths %v, want the task's two files", e.ChangedPaths)
	}
	if tk := task(w.ok("task show", `{"id":2}`)); tk["readiness"] != "blocked" {
		t.Errorf("the dependent was freed though delete failed: %v", tk)
	}
	fs := w.ok("doctor", `{}`)["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["kind"] != "dangling-reference" {
		t.Errorf("doctor: %v", fs)
	}
}

// delete removes a task's notes, and nothing else that has their name.
func TestDeleteLeavesWhatIsNotNotes(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`)
	os.Remove(w.root("1.md"))
	os.Mkdir(w.root("1.md"), 0o755)
	w.ok("task delete", `{"id":1}`)
	if fi, err := os.Stat(w.root("1.md")); err != nil || !fi.IsDir() {
		t.Errorf("delete removed a directory named like the notes: %v", err)
	}
}

func TestBlockReportsEveryProblem(t *testing.T) {
	w := ready(t)
	if e := w.fail("task block", `{"id":"x","blocked_by":[]}`, errs.InvalidInput, ""); len(e.Problems) != 2 {
		t.Errorf("problems %v, want both", e.Problems)
	}
}

// block can't rule out a cycle through a file it can't read, so it refuses.
func TestBlockRefusesThroughAnUnusableFile(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a"},{"ref":"b","title":"b","blocked_by":["a"]},{"title":"c","blocked_by":["b"]},{"title":"d"}]}`)
	good := w.file("2.json")
	os.WriteFile(w.root("2.json"), []byte("{"), 0o644)
	w.fail("task block", `{"id":1,"blocked_by":[3]}`, errs.Corrupt, "") // 3 waits on 2, which can't be read
	w.fail("task block", `{"id":1,"blocked_by":[2]}`, errs.Corrupt, "") // the new blocker itself
	w.ok("task block", `{"id":4,"blocked_by":[1]}`)                     // nothing unreadable on the way
	os.WriteFile(w.root("2.json"), []byte(good), 0o644)
	w.fail("task block", `{"id":1,"blocked_by":[3]}`, errs.Conflict, errs.RuleCycle)
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Errorf("doctor: %v", got)
	}
}

func TestBlockNamesEverythingMissing(t *testing.T) {
	w := ready(t)
	if e := w.fail("task block", `{"id":99,"blocked_by":[98,97]}`, errs.NotFound, ""); len(e.IDs) != 3 {
		t.Errorf("ids %v, want all three", e.IDs)
	}
}

// create's blocks: the tasks named wait on the new one. A cycle through it
// is refused before an ID is used.
func TestCreateBlocks(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]}]}`)
	got := w.ok("task create", `{"title":"c","blocks":[2,1]}`)
	wantIDs(t, "dependents", got["dependents"], 1, 2)
	if tk := task(w.ok("task show", `{"id":1}`)); tk["readiness"] != "blocked" {
		t.Errorf("task 1 does not wait on 3: %v", tk)
	}
	// 2 waits on 1: a task blocked by 2 that blocks 1 closes a cycle.
	e := w.fail("task create", `{"title":"d","blocked_by":[2],"blocks":[1]}`, errs.Conflict, errs.RuleCycle)
	if !slices.Equal(e.IDs, []int64{2, 1}) {
		t.Errorf("cycle %v, want [2 1]", e.IDs)
	}
	w.fail("task create", `{"title":"d","blocked_by":[3],"blocks":[3]}`, errs.Conflict, errs.RuleCycle)
	w.fail("task create", `{"title":"d","blocks":[9]}`, errs.NotFound, "")
	w.fail("task create", `{"title":"d","blocks":[1,1]}`, errs.InvalidInput, "")
	if id := w.ok("task create", `{"title":"e"}`)["id"]; id != float64(4) {
		t.Errorf("a refused create used an ID: next is %v", id)
	}
	// blocks takes folders as well, and names every ID that is neither.
	w.ok("folder create", `{"folder":"/f"}`) // 5
	if e := w.fail("task create", `{"title":"g","blocks":[5,99,98]}`, errs.NotFound, ""); !slices.Equal(e.IDs, []int64{99, 98}) {
		t.Errorf("ids %v", e.IDs)
	}
	if e := w.fail("task show", `{"id":5}`, errs.NotFound, ""); !strings.Contains(e.Message, "folder /f") {
		t.Errorf("show: message %q", e.Message)
	}
	if e := w.fail("task create", `{"title":"d","blocked_by":[3],"blocks":[3]}`, errs.Conflict, errs.RuleCycle); !slices.Equal(e.IDs, []int64{3}) || !strings.Contains(e.Message, `the new task "d" waits on task 3`) {
		t.Errorf("cycle %v: message %q", e.IDs, e.Message)
	}

	// A done task can be blocked, as block allows.
	w.ok("task done", `{"id":4}`)
	wantIDs(t, "dependents", w.ok("task create", `{"title":"h","blocks":[4]}`)["dependents"], 4)
}

// Every task in blocks is read before anything is written: one that can't
// be used refuses the command, and changes nothing. So does one the cycle
// search meets.
func TestCreateBlocksReadsFirst(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]},{"title":"c"}]}`)
	good := w.file("3.json")
	os.WriteFile(w.root("3.json"), []byte("{"), 0o644)
	e := w.fail("task create", `{"title":"d","blocks":[2,3]}`, errs.Corrupt, "")
	if len(e.ChangedPaths) != 0 {
		t.Errorf("changed %v", e.ChangedPaths)
	}
	if tk := task(w.ok("task show", `{"id":2}`)); tk["readiness"] != "blocked" || len(tk["blocked_by"].([]any)) != 1 {
		t.Errorf("task 2 was rewritten: %v", tk)
	}
	os.WriteFile(w.root("3.json"), []byte(good), 0o644)
	os.WriteFile(w.root("1.json"), []byte("{"), 0o644)
	// From 2 the search reaches 1, which it can't read: a cycle through it
	// can't be ruled out.
	w.fail("task create", `{"title":"d","blocked_by":[2],"blocks":[3]}`, errs.Corrupt, "")
	os.Remove(w.root("1.json"))
	if id := w.ok("task create", `{"title":"e"}`)["id"]; id != float64(4) {
		t.Errorf("a refused create used an ID: next is %v", id)
	}
}

// create's blocks are rewritten before the new task is written: a failure
// between leaves them waiting on an ID with no task, which doctor reports,
// never the new task with its dependents still free.
func TestCreateBlocksWritesDependentsFirst(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`) // 1
	w.ok("task create", `{"title":"b"}`) // 2
	// Rewriting task 2 fails, after task 1 is rewritten.
	w.env.FS = fsys.FS{Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpRename && op.NewPath == "@2.json" {
			return syscall.ENOSPC
		}
		return nil
	}}
	env := w.run("task create", `{"title":"c","blocks":[1,2]}`)
	w.env.FS = fsys.FS{}
	if env.OK || !slices.Equal(env.Error.IDs, []int64{1}) || !strings.Contains(env.Error.Message, "task 3 was not created") {
		t.Fatalf("got %+v", env.Error)
	}
	w.fail("task show", `{"id":3}`, errs.NotFound, "")
	if tk := task(w.ok("task show", `{"id":1}`)); tk["readiness"] != "blocked" {
		t.Errorf("task 1 is %v", tk["readiness"])
	}
	if f := w.findings(); !slices.Equal(f, []string{"dangling-reference @1.json"}) {
		t.Errorf("findings %v", f)
	}
}

// A failure writing the new task's notes, after its dependents and its task
// file, names only the new task: its dependents wait on a task that exists.
func TestCreateBlocksNotesFail(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`) // 1
	w.env.FS = fsys.FS{Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpLink && op.NewPath == "@2.md" {
			return syscall.ENOSPC
		}
		return nil
	}}
	env := w.run("task create", `{"title":"b","blocks":[1]}`)
	w.env.FS = fsys.FS{}
	if env.OK || !slices.Equal(env.Error.IDs, []int64{2}) {
		t.Fatalf("got %+v", env.Error)
	}
	if tk := task(w.ok("task show", `{"id":1}`)); tk["readiness"] != "blocked" {
		t.Errorf("task 1 is %v", tk["readiness"])
	}
	if f := w.findings(); len(f) != 0 {
		t.Errorf("findings %v", f)
	}
}

// not-found names everything missing at once, across every field of the
// input: IDs in ids, folder paths in folders.
func TestNotFoundAcrossFields(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`) // 1
	for _, c := range []struct {
		name, input string
		ids         []int64
		folders     []string
	}{
		{"task create", `{"title":"x","blocked_by":[98],"blocks":[99]}`, []int64{98, 99}, []string{}},
		{"task create", `{"title":"x","blocked_by":[98,"/nope"]}`, []int64{98}, []string{"/nope"}},
		{"task block", `{"id":1,"blocked_by":[98,"/nope"]}`, []int64{98}, []string{"/nope"}},
		{"task block", `{"id":97,"blocked_by":[98,"/nope"]}`, []int64{97, 98}, []string{"/nope"}},
		{"task unblock", `{"id":97,"blocked_by":["/nope"]}`, []int64{97}, []string{"/nope"}},
		{"folder block", `{"folder":"/gone","blocked_by":[98,"/nope"]}`, []int64{98}, []string{"/gone", "/nope"}},
		{"folder unblock", `{"folder":"@96","blocked_by":["/nope"]}`, []int64{96}, []string{"/nope"}},
		{"folder create", `{"folder":"/f","blocked_by":["/nope"],"blocks":[99]}`, []int64{99}, []string{"/nope"}},
		{"batch", `{"folders":[{"folder":"/f","blocked_by":["/nope"]}],"tasks":[{"title":"a","blocked_by":[98]},{"title":"b","blocked_by":[99]}]}`, []int64{98, 99}, []string{"/nope"}},
	} {
		e := w.fail(c.name, c.input, errs.NotFound, "")
		if !slices.Equal(e.IDs, c.ids) || !slices.Equal(e.Folders, c.folders) {
			t.Errorf("%s %s: ids %v folders %v", c.name, c.input, e.IDs, e.Folders)
		}
	}
	// A folder with no folder file has no ID to wait on: it is missing
	// beside the rest, with the folder file's path.
	w.ok("folder create", `{"folder":"/nf"}`) // 2
	os.Remove(w.root("nf/folder.json"))
	for _, c := range [][2]string{
		{"task create", `{"title":"x","blocked_by":[98,"/nf"],"blocks":["/nope"]}`},
		{"task unblock", `{"id":1,"blocked_by":[98,"/nf","/nope"]}`},
	} {
		e := w.fail(c[0], c[1], errs.NotFound, "")
		wantIDs := []int64{98}
		if c[0] == "task unblock" {
			wantIDs = []int64{} // an ID need not exist there
		}
		if !slices.Equal(e.IDs, wantIDs) || !slices.Equal(e.Folders, []string{"/nf", "/nope"}) ||
			!slices.Equal(e.Paths, []string{w.root("nf/folder.json")}) || !strings.Contains(e.Message, "koan repair") {
			t.Errorf("%s %s: ids %v folders %v paths %v message %q", c[0], c[1], e.IDs, e.Folders, e.Paths, e.Message)
		}
	}
}
