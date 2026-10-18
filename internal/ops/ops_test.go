package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/schematest"
	"github.com/phansen314/koan/internal/store"
)

// world is a HOME of its own with a fixed clock.
type world struct {
	t   *testing.T
	env store.Env
	now time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	w.env = store.Env{Home: t.TempDir(), Now: func() time.Time { return w.now }, FS: fsys.OS{}, LockWait: 200 * time.Millisecond}
	return w
}

// ready is a world with init run.
func ready(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.ok("init", `{}`)
	return w
}

func (w *world) root(rel ...string) string {
	return filepath.Join(append([]string{w.env.RootPath()}, rel...)...)
}

// run runs a command and checks its envelope against the schemas.
func (w *world) run(name, input string) Envelope {
	w.t.Helper()
	env := Run(w.env, name, []byte(input))
	data, err := json.Marshal(env)
	if err != nil {
		w.t.Fatalf("%s: encode: %v", name, err)
	}
	if err := schematest.Check(w.t, "envelope", data); err != nil {
		w.t.Errorf("%s %s: envelope: %v\n%s", name, input, err, data)
	}
	if env.OK {
		schematest.Must(w.t, name+"-output", env.Result)
	}
	// The input schema and the command must agree on what is valid.
	if json.Valid([]byte(input)) {
		schemaErr := schematest.Check(w.t, name+"-input", []byte(input))
		invalid := !env.OK && env.Error.Kind == errs.InvalidInput
		if (schemaErr != nil) != invalid && !crossField(env) {
			w.t.Errorf("%s %s: schema says %v, command says invalid=%v", name, input, schemaErr, invalid)
		}
	}
	return env
}

// crossField reports whether an invalid-input error is about a rule between
// fields, which the schemas do not express.
func crossField(env Envelope) bool {
	if env.OK || env.Error.Kind != errs.InvalidInput {
		return false
	}
	for _, p := range env.Error.Problems {
		for _, s := range []string{"may not be", "is also in", "is the task itself", "earlier task", "is used by",
			"is empty", "is longer than", "control character", "gives nothing"} {
			if strings.Contains(p.Reason, s) {
				return true
			}
		}
	}
	return false
}

// ok runs a command that must succeed and returns its result as JSON-decoded
// data.
func (w *world) ok(name, input string) map[string]any {
	w.t.Helper()
	env := w.run(name, input)
	if !env.OK {
		w.t.Fatalf("%s %s: %s", name, input, env.Error)
	}
	data, _ := json.Marshal(env.Result)
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

// fail runs a command that must fail with the given kind and, for a
// conflict, rule.
func (w *world) fail(name, input string, kind errs.Kind, rule string) *errs.Error {
	w.t.Helper()
	env := w.run(name, input)
	if env.OK {
		w.t.Fatalf("%s %s: succeeded, want %s %s", name, input, kind, rule)
	}
	if env.Error.Kind != kind || (rule != "" && !env.Error.HasRule(rule)) {
		w.t.Fatalf("%s %s: got %s, want %s %s", name, input, env.Error, kind, rule)
	}
	return env.Error
}

// task returns the `task` of a result.
func task(m map[string]any) map[string]any { return m["task"].(map[string]any) }

func ids(v any) []int64 {
	out := []int64{}
	for _, x := range v.([]any) {
		switch x := x.(type) {
		case float64:
			out = append(out, int64(x))
		case map[string]any:
			out = append(out, int64(x["id"].(float64)))
		}
	}
	return out
}

func wantIDs(t *testing.T, what string, got any, want ...int64) {
	t.Helper()
	if g := ids(got); !slices.Equal(g, want) {
		t.Errorf("%s: got %v, want %v", what, g, want)
	}
}

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
	if id := task(w.ok("create", `{"title":"c"}`))["id"]; id != float64(3) {
		t.Errorf("next ID %v, want 3", id)
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

func TestCreateAndShow(t *testing.T) {
	w := ready(t)
	got := w.ok("create", `{"title":"  Book flights ","folder":"/proj/travel","priority":2,"tags":["urgent","travel"],"extra":{"n":1.50,"big":12345678901234567890},"notes":"hello"}`)
	tk := task(got)
	if tk["title"] != "Book flights" || tk["id"] != float64(1) || tk["folder"] != "/proj/travel" || tk["readiness"] != "ready" {
		t.Errorf("task: %v", tk)
	}
	if fc := got["folders_created"].([]any); len(fc) != 2 || fc[0] != "/proj" {
		t.Errorf("folders_created %v", fc)
	}
	if notes, _ := os.ReadFile(tk["notes_path"].(string)); string(notes) != "hello" {
		t.Errorf("notes %q", notes)
	}
	// Numbers in extra keep their exact text, and sets are sorted.
	file, _ := os.ReadFile(w.root("proj/travel/1.json"))
	for _, want := range []string{`"n": 1.50`, `"big": 12345678901234567890`, "\"travel\",\n    \"urgent\""} {
		if !strings.Contains(string(file), want) {
			t.Errorf("task file lacks %s:\n%s", want, file)
		}
	}
	schematest.Must(t, "task-file", json.RawMessage(file))
	if got := task(w.ok("show", `{"id":1}`)); got["title"] != "Book flights" {
		t.Errorf("show: %v", got)
	}
	w.fail("show", `{"id":9}`, errs.NotFound, "")
	w.fail("show", `{"id":"x"}`, errs.InvalidInput, "")
	e := w.fail("create", `{"title":" ","tags":["Bad"],"nope":1}`, errs.InvalidInput, "")
	if len(e.Problems) != 3 {
		t.Errorf("want every problem reported, got %v", e.Problems)
	}
	w.fail("create", `{"title":"x","blocked_by":[7,8]}`, errs.NotFound, "")
	if n := task(w.ok("create", `{"title":"next"}`))["id"]; n != float64(2) {
		t.Errorf("a refused create used an ID: next is %v", n)
	}
}

func TestBlockingAndReadiness(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"A"},{"ref":"b","title":"B","blocked_by":["a"]},{"title":"C","blocked_by":["b","a"]}]}`)
	if tk := task(w.ok("show", `{"id":3}`)); tk["readiness"] != "blocked" {
		t.Errorf("task 3: %v", tk)
	} else {
		wantIDs(t, "blocking", tk["blocking"], 1, 2)
	}
	e := w.fail("block", `{"id":1,"blockers":[3]}`, errs.Conflict, errs.RuleCycle)
	if !slices.Equal(e.IDs, []int64{1, 3, 2}) && !slices.Equal(e.IDs, []int64{1, 3}) {
		t.Errorf("cycle %v", e.IDs)
	}
	w.fail("block", `{"id":1,"blockers":[1]}`, errs.InvalidInput, "")
	w.fail("block", `{"id":1,"blockers":[99]}`, errs.NotFound, "")
	if got := w.ok("block", `{"id":3,"blockers":[1]}`); got["changed"] != false {
		t.Errorf("blocking by a blocker already there changed something")
	}

	why := w.ok("why", `{"id":3}`)
	wantIDs(t, "why ready", why["ready"], 1)
	wantIDs(t, "why stuck", why["stuck"])

	w.ok("done", `{"id":1}`)
	wantIDs(t, "frontier", w.ok("frontier", `{}`)["tasks"], 2)
	if got := w.ok("done", `{"id":1}`); got["changed"] != false {
		t.Error("done twice changed something")
	}
	w.ok("reopen", `{"id":1}`)
	wantIDs(t, "frontier after reopen", w.ok("frontier", `{}`)["tasks"], 1)

	w.ok("unblock", `{"id":3,"blockers":[2,77]}`)
	wantIDs(t, "blocked_by", task(w.ok("show", `{"id":3}`))["blocked_by"], 1)

	// Deleting a blocker frees what waited only on it.
	got := w.ok("delete", `{"id":1}`)
	wantIDs(t, "dependents", got["dependents"], 2, 3)
	wantIDs(t, "frontier after delete", w.ok("frontier", `{}`)["tasks"], 2, 3)
	if _, err := os.Stat(w.root("1.md")); !os.IsNotExist(err) {
		t.Error("delete left the notes")
	}
}

func TestUpdate(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"t","tags":["a","b"],"extra":{"k":1,"o":{"x":1}}}`)
	w.now = w.now.Add(time.Hour)
	got := w.ok("update", `{"id":1,"title":"new","priority":5,"tags_add":["c"],"tags_remove":["a"],"extra_merge":{"o":{"y":2},"n":null},"extra_remove":["k"]}`)
	tk := task(got)
	if got["changed"] != true || tk["title"] != "new" || tk["priority"] != float64(5) || tk["updated_at"] == tk["created_at"] {
		t.Errorf("update: %v", got)
	}
	extra, _ := json.Marshal(tk["extra"])
	if string(extra) != `{"n":null,"o":{"y":2}}` {
		t.Errorf("extra %s", extra)
	}
	if tags, _ := json.Marshal(tk["tags"]); string(tags) != `["b","c"]` {
		t.Errorf("tags %s", tags)
	}
	if got := w.ok("update", `{"id":1,"title":"new","priority":5}`); got["changed"] != false {
		t.Error("an update to the same values changed something")
	}
	if tk := task(w.ok("update", `{"id":1,"priority":null,"tags_replace_all":[],"extra_replace_all":{}}`)); tk["priority"] != nil {
		t.Errorf("priority not cleared: %v", tk)
	}
	w.fail("update", `{"id":1}`, errs.InvalidInput, "")
	w.fail("update", `{"id":1,"tags_add":["x"],"tags_remove":["x"]}`, errs.InvalidInput, "")
	w.fail("update", `{"id":1,"tags_add":["x"],"tags_replace_all":["y"]}`, errs.InvalidInput, "")
	w.fail("update", `{"id":1,"extra_merge":{"a":1},"extra_remove":["a"]}`, errs.InvalidInput, "")
}

func TestFoldersAndMove(t *testing.T) {
	w := ready(t)
	if got := w.ok("create-folder", `{"folder":"/a/b"}`); len(got["folders_created"].([]any)) != 2 {
		t.Errorf("create-folder: %v", got)
	}
	if got := w.ok("create-folder", `{"folder":"/a/b"}`); len(got["folders_created"].([]any)) != 0 {
		t.Errorf("create-folder again: %v", got)
	}
	w.ok("create", `{"title":"t","folder":"/a/b","notes":"n"}`)

	got := w.ok("move", `{"id":1,"to":"/c/d"}`)
	if task(got)["folder"] != "/c/d" || got["changed"] != true || len(got["folders_created"].([]any)) != 2 {
		t.Errorf("move: %v", got)
	}
	if n, _ := os.ReadFile(w.root("c/d/1.md")); string(n) != "n" {
		t.Errorf("notes did not move: %q", n)
	}
	if got := w.ok("move", `{"id":1,"to":"/c/d"}`); got["changed"] != false {
		t.Error("move to the same folder changed something")
	}
	os.WriteFile(w.root("a/1.md"), []byte("someone's"), 0o644)
	w.fail("move", `{"id":1,"to":"/a"}`, errs.Conflict, errs.RuleDestinationExists)

	w.fail("move-folder", `{"folder":"/c","to":"/c/x"}`, errs.InvalidInput, "")
	w.fail("move-folder", `{"folder":"/nope","to":"/x"}`, errs.NotFound, "")
	w.fail("move-folder", `{"folder":"/c","to":"/a"}`, errs.Conflict, errs.RuleDestinationExists)
	if got := w.ok("move-folder", `{"folder":"/c","to":"/z/y"}`); got["folder"] != "/z/y" || got["changed"] != true {
		t.Errorf("move-folder: %v", got)
	}
	if tk := task(w.ok("show", `{"id":1}`)); tk["folder"] != "/z/y/d" {
		t.Errorf("task after move-folder: %v", tk["folder"])
	}

	w.fail("delete-folder", `{"folder":"/z"}`, errs.Conflict, errs.RuleNotEmpty)
	w.fail("delete-folder", `{"folder":"/"}`, errs.InvalidInput, "")
	w.ok("create", `{"title":"outside","blocked_by":[1]}`)
	w.ok("claim", `{"id":1}`)
	got = w.ok("delete-folder", `{"folder":"/z","recursive":true}`)
	wantIDs(t, "deleted", got["deleted"], 1)
	wantIDs(t, "dependents", got["dependents"], 2)
	if n := w.ok("info", `{}`)["claims"]; n != float64(0) {
		t.Errorf("claims after delete-folder: %v", n)
	}
	os.WriteFile(w.root("afile"), nil, 0o644)
	w.fail("create-folder", `{"folder":"/afile/x"}`, errs.Conflict, errs.RuleNotAFolder)
}

func TestListAndFrontier(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[
		{"ref":"low","title":"low","folder":"/proj","priority":1,"tags":["x"]},
		{"title":"high","folder":"/proj/sub","priority":9,"tags":["x","y"]},
		{"title":"none","folder":"/Zed"},
		{"title":"blocked","blocked_by":["low"]},
		{"title":"done","folder":"/proj"}]}`)
	w.ok("done", `{"id":5}`)

	wantIDs(t, "frontier is in work order", w.ok("frontier", `{}`)["tasks"], 2, 1, 3)
	wantIDs(t, "list is in tree order", w.ok("list", `{}`)["tasks"], 4, 3, 1, 2)
	wantIDs(t, "list done", w.ok("list", `{"readiness":["done"]}`)["tasks"], 5)
	wantIDs(t, "scope", w.ok("list", `{"folder":"/proj","recursive":false}`)["tasks"], 1)
	wantIDs(t, "tags_all", w.ok("frontier", `{"tags_all":["x","y"]}`)["tasks"], 2)
	wantIDs(t, "tags_any", w.ok("frontier", `{"tags_any":["y","q"]}`)["tasks"], 2)

	got := w.ok("frontier", `{"limit":1,"fields":["title","readiness"]}`)
	if got["total"] != float64(3) || got["truncated"] != true {
		t.Errorf("limit: %v", got)
	}
	if one, _ := json.Marshal(got["tasks"]); string(one) != `[{"id":2,"readiness":"ready","title":"high"}]` {
		t.Errorf("fields: %s", one)
	}
	folders, _ := json.Marshal(w.ok("list", `{"include_folders":true}`)["folders"])
	if string(folders) != `["/","/Zed","/proj","/proj/sub"]` {
		t.Errorf("folders %s", folders)
	}
	w.fail("list", `{"folder":"/nope"}`, errs.NotFound, "")
	w.fail("frontier", `{"claimed":false}`, errs.InvalidInput, "")
	w.fail("frontier", `{"fields":["nope"],"limit":0}`, errs.InvalidInput, "")
}

func TestClaims(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a","priority":1},{"title":"b","priority":2},{"title":"c","blocked_by":["a"]}]}`)
	first := w.ok("claim-next", `{"extra":{"worker":"w1"}}`)
	if tk := task(first); tk["id"] != float64(2) || tk["claim"] == nil {
		t.Errorf("claim-next: %v", first)
	}
	wantIDs(t, "frontier hides the claimed task", w.ok("frontier", `{}`)["tasks"], 1)
	wantIDs(t, "list --claimed", w.ok("list", `{"claimed":true}`)["tasks"], 2)
	e := w.fail("claim", `{"id":2}`, errs.Conflict, errs.RuleClaimed)
	if !strings.Contains(e.Message, "w1") {
		t.Errorf("the refusal does not say who holds the claim: %s", e.Message)
	}
	w.ok("claim", `{"id":3}`) // a blocked task can be reserved
	w.ok("claim-next", `{}`)
	if got := w.ok("claim-next", `{}`); got["task"] != nil {
		t.Errorf("claim-next with nothing left: %v", got)
	}
	if got := w.ok("release", `{"id":2}`); got["changed"] != true {
		t.Errorf("release: %v", got)
	}
	if got := w.ok("release", `{"id":2}`); got["changed"] != false {
		t.Errorf("release twice: %v", got)
	}
	w.ok("release", `{"id":999}`)
	w.ok("done", `{"id":1}`)
	if tk := task(w.ok("show", `{"id":1}`)); tk["claim"] != nil {
		t.Errorf("a done task still shows a claim: %v", tk)
	}
	w.fail("claim", `{"id":1}`, errs.Conflict, errs.RuleTaskDone)
	if n := w.ok("info", `{}`)["claims"]; n != float64(1) {
		t.Errorf("claims left: %v, want only task 3's", n)
	}
	schematest.Must(t, "state-file", readJSON(t, w.env.StatePath()))
}

func readJSON(t *testing.T, p string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDuplicateIDIsAConflict(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	w.ok("create-folder", `{"folder":"/x"}`)
	data, _ := os.ReadFile(w.root("1.json"))
	os.WriteFile(w.root("x/1.json"), data, 0o644)
	e := w.fail("show", `{"id":1}`, errs.Conflict, errs.RuleDuplicateID)
	if len(e.Paths) != 2 {
		t.Errorf("want both copies, got %v", e.Paths)
	}
	w.fail("list", `{}`, errs.Conflict, errs.RuleDuplicateID)
}

func TestWarnings(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]},{"title":"c"}]}`)
	os.Remove(w.root("1.json"))
	os.WriteFile(w.root("3.json"), []byte("not json"), 0o644)
	env := w.run("list", `{}`)
	kinds := []string{}
	for _, wn := range env.Warnings {
		kinds = append(kinds, wn.Kind)
		schematest.Must(t, "warning", wn)
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{"dangling-reference", "unusable-file"}) {
		t.Errorf("warnings %v", kinds)
	}
	w.fail("show", `{"id":3}`, errs.Corrupt, "")
	wantIDs(t, "stuck", w.ok("why", `{"id":2}`)["stuck"], 1)
	w.ok("delete", `{"id":3}`) // delete goes by filename
}

func TestDoctorAndRepair(t *testing.T) {
	w := ready(t)
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Fatalf("a new tree is not healthy: %v", got)
	}
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]},{"title":"c","folder":"/p","notes":"keep"},{"title":"d"}]}`)
	w.ok("claim", `{"id":1}`)

	os.Remove(w.root("1.json"))                       // dangling reference in 2, stale claim, orphan notes (empty)
	os.WriteFile(w.root(".koan-tmp-abc"), nil, 0o644) // temp leftover
	os.Rename(w.root("p/3.json"), w.root("3.json"))   // notes left in /p: orphan, movable
	data, _ := os.ReadFile(w.root("4.json"))          // id above counter
	os.WriteFile(w.root("9.json"), []byte(strings.Replace(string(data), `"id": 4`, `"id": 9`, 1)), 0o644)
	os.WriteFile(w.root("p/4.json"), data, 0o644)          // duplicate
	os.WriteFile(w.root("p/5.json"), []byte("{}"), 0o644)  // unusable (and above the counter)
	os.Symlink("nowhere", w.root("link"))                  // skipped entry
	os.WriteFile(w.root("p/7.md"), []byte("stray"), 0o644) // orphan with content and no task

	found := map[string]int{}
	for _, f := range w.ok("doctor", `{}`)["findings"].([]any) {
		found[f.(map[string]any)["kind"].(string)]++
	}
	want := map[string]int{"temp-leftover": 1, "id-above-counter": 1, "dangling-reference": 1, "stale-claim": 1,
		"orphan-notes": 3, "duplicate-id": 1, "unusable-file": 1, "skipped-entry": 1}
	for k, n := range want {
		if found[k] != n {
			t.Errorf("doctor: %d %s findings, want %d (all: %v)", found[k], k, n, found)
		}
	}

	// A plain repair leaves the dangling reference, which only it names.
	left := func(got map[string]any) map[string]int {
		n := map[string]int{}
		for _, f := range got["findings"].([]any) {
			f := f.(map[string]any)
			n[f["kind"].(string)]++
			if f["repairable"] == true && f["kind"] != "dangling-reference" {
				t.Errorf("repair left a repairable finding: %v", f)
			}
		}
		return n
	}
	got := left(w.ok("repair", `{}`))
	if got["duplicate-id"] != 1 || got["unusable-file"] != 1 || got["skipped-entry"] != 1 || got["orphan-notes"] != 1 || got["dangling-reference"] != 1 || len(got) != 5 {
		t.Errorf("after repair: %v", got)
	}
	got = left(w.ok("repair", `{"kinds":["dangling-reference"]}`))
	if got["dangling-reference"] != 0 || len(got) != 4 {
		t.Errorf("after repair of dangling-reference: %v", got)
	}
	if n, _ := os.ReadFile(w.root("3.md")); string(n) != "keep" {
		t.Errorf("repair did not move the notes to their task: %q", n)
	}
	if n := w.ok("info", `{}`); n["last_id"] != float64(9) || n["claims"] != float64(0) {
		t.Errorf("after repair: %v", n)
	}
	if again := w.ok("repair", `{}`); len(again["repaired"].([]any)) != 0 {
		t.Errorf("a second repair repaired %v", again["repaired"])
	}
}

func TestDoctorFindsCycles(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]}]}`)
	data, _ := os.ReadFile(w.root("1.json"))
	os.WriteFile(w.root("1.json"), []byte(strings.Replace(string(data), `"blocked_by": []`, `"blocked_by": [2]`, 1)), 0o644)
	fs := w.ok("doctor", `{}`)["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["kind"] != "cycle" {
		t.Fatalf("findings %v", fs)
	}
	wantIDs(t, "cycle", fs[0].(map[string]any)["ids"], 1, 2)
	wantIDs(t, "why stuck", w.ok("why", `{"id":2}`)["stuck"], 1, 2)
}

func TestFormats(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	if got := w.ok("migrate", `{}`); got["converted"] != float64(0) || got["from"] != float64(1) {
		t.Errorf("migrate on a current tree: %v", got)
	}
	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 2}`), 0o644)
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

// A failure partway through a batch leaves the first tasks created, and the
// error says which.
func TestBatchFailsPartway(t *testing.T) {
	w := ready(t)
	w.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpLink, "", 5, 28 /* ENOSPC */)}
	env := w.run("create-batch", `{"tasks":[{"title":"a"},{"title":"b"},{"title":"c"}]}`)
	if env.OK || env.Error.Kind != errs.IO {
		t.Fatalf("got %+v", env)
	}
	if !slices.Equal(env.Error.IDs, []int64{1, 2}) || len(env.Error.ChangedPaths) == 0 {
		t.Errorf("ids %v, changed %v", env.Error.IDs, env.Error.ChangedPaths)
	}
	w.env.FS = fsys.OS{}
	if id := task(w.ok("create", `{"title":"d"}`))["id"]; id != float64(4) {
		t.Errorf("next ID %v: the batch's IDs must not be reused", id)
	}
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Errorf("doctor after a failed batch: %v", got)
	}
}
