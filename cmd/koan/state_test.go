package main_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

const fixed = "2026-10-20T10:00:00Z"

// input runs a command with its input on standard input, checked against
// its schemas.
func input(t *testing.T, home, command, in string) *testrun.Result {
	t.Helper()
	r := testrun.Run{Home: home, Stdin: strings.NewReader(in)}.Koan(t, append(strings.Fields(command), "-i", "-")...)
	r.Check(t, command)
	return r
}

// complete marks a task complete, blocked or not, and ends its
// claim, listing it in unclaimed; it reports the open tasks it made
// ready, and moves updated_at on it alone. Completing a complete task
// changes nothing.
func TestTaskComplete(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	for range 4 {
		ok(t, home, "task", "create", "t") // 2 to 5
	}
	// 3 waits on 2; 4 on 2 and 5; 5 on /w.start, made 6.
	ok(t, home, "block", "2", "--blocks", "3,4")
	ok(t, home, "block", "5", "--blocked-by", "/w.start", "--blocks", "4")
	ok(t, home, "claim", "2")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "complete", "2")
	golden(t, r, "complete")
	if fmt.Sprint(r.Result()) != "map[completed:[2] released:[3] unclaimed:[2]]" {
		t.Errorf("complete: %s", r.Line())
	}
	rec := ok(t, home, "show", "2").Result()["task"].(map[string]any)
	if rec["state"] != "complete" || rec["readiness"] != nil || rec["claim"] != nil || rec["completed_at"] == nil ||
		rec["completed_at"] != rec["updated_at"] || rec["updated_at"] == fixed {
		t.Errorf("completed: %v", rec)
	}
	if field(t, home, "3", "updated_at") != fixed || field(t, home, "3", "readiness") != "ready" {
		t.Errorf("a released task's fields don't change")
	}
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r = ok(t, home, "complete", "@2")
	if fmt.Sprint(r.Result()) != "map[completed:[] released:[] unclaimed:[]]" || field(t, home, "2", "updated_at") != fixed {
		t.Errorf("again: %s", r.Line())
	}
	// A blocked task, and a gate by its path, can be completed; the
	// gate's completing releases 5, which then releases 4.
	if r := ok(t, home, "complete", "/w.start"); fmt.Sprint(r.Result()) != "map[completed:[6] released:[5] unclaimed:[]]" {
		t.Errorf("gate: %s", r.Line())
	}
	if r := ok(t, home, "complete", "5"); fmt.Sprint(r.Result()) != "map[completed:[5] released:[4] unclaimed:[]]" {
		t.Errorf("last blocker: %s", r.Line())
	}
	ok(t, home, "block", "3", "--blocked-by", "4")
	if r := ok(t, home, "complete", "3"); fmt.Sprint(r.Result()) != "map[completed:[3] released:[] unclaimed:[]]" {
		t.Errorf("blocked: %s", r.Line())
	}
	golden(t, fails(t, home, "not-found", "", "complete", "99"), "complete-not-found")
	fails(t, home, "not-found", "", "complete", "/w.done")
	fails(t, home, "conflict", "case-mismatch", "complete", "/W.start")
	if r := fails(t, home, "invalid-input", "", "complete", "/w"); problemFields(r) != "/tasks/0" {
		t.Errorf("a folder path: %s", r.Line())
	}
}

// reopen marks a task open again, so a ready task that waits on it
// waits again; a claimed one keeps its claim, with claimed-blocked. A
// reopened task starts unclaimed. Reopening an open task changes nothing.
func TestTaskReopen(t *testing.T) {
	home := root(t)
	for range 5 {
		ok(t, home, "task", "create", "t") // 1 to 5
	}
	// 2, 3 and 4 wait on 1; 4 on 5 too.
	ok(t, home, "block", "1", "--blocks", "2,3,4")
	ok(t, home, "block", "4", "--blocked-by", "5")
	ok(t, home, "claim", "1")
	ok(t, home, "complete", "1")
	ok(t, home, "claim", "2")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := warned(t, home, []string{"claimed-blocked"}, "reopen", "1")
	golden(t, r, "reopen-claimed-blocked")
	if fmt.Sprint(r.Result()) != "map[reblocked:[2 3] reopened:[1]]" {
		t.Errorf("reopen: %s", r.Line())
	}
	rec := ok(t, home, "show", "1").Result()["task"].(map[string]any)
	if rec["state"] != "open" || rec["completed_at"] != nil || rec["claim"] != nil || rec["updated_at"] == fixed {
		t.Errorf("reopened: %v", rec)
	}
	if field(t, home, "2", "claim") == nil || field(t, home, "2", "updated_at") != fixed {
		t.Errorf("the claim stays, and the waiting task's fields don't change")
	}
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r = ok(t, home, "reopen", "1")
	if fmt.Sprint(r.Result()) != "map[reblocked:[] reopened:[]]" || field(t, home, "1", "updated_at") != fixed {
		t.Errorf("again: %s", r.Line())
	}
	ok(t, home, "complete", "1")
	if r := warned(t, home, []string{"claimed-blocked"}, "reopen", "1"); fmt.Sprint(r.Result()["reblocked"]) != "[2 3]" {
		t.Errorf("reopened again: %s", r.Line())
	}
	// Unclaimed, and two claimed in the plural.
	ok(t, home, "complete", "1")
	ok(t, home, "unclaim", "2")
	golden(t, ok(t, home, "reopen", "1"), "reopen")
	ok(t, home, "complete", "1")
	ok(t, home, "claim", "2")
	ok(t, home, "claim", "3")
	r = warned(t, home, []string{"claimed-blocked"}, "reopen", "1")
	w := r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[2 3]" || w["message"] != "@2, @3 are claimed, and wait again; their claims stay." {
		t.Errorf("plural: %s", r.Line())
	}
	fails(t, home, "not-found", "", "reopen", "99")
}

// complete and reopen take a list of tasks, comma-separated or
// as more words, and make one change of them all: if any is refused,
// none changes. completed and reopened list only the tasks whose state
// changed, in input order. A task completed in the same call is never
// released, and one reopened in the same call never reblocked. A task
// given twice is invalid-input at its later occurrence.
func TestCompleteSeveral(t *testing.T) {
	home := root(t)
	for range 5 {
		ok(t, home, "task", "create", "t") // 1 to 5
	}
	// 2 and 3 wait on 1; 5 on 4.
	ok(t, home, "block", "1", "--blocks", "2,3")
	ok(t, home, "block", "5", "--blocked-by", "4")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")

	// A bad entry mid-list changes nothing.
	golden(t, fails(t, home, "not-found", "", "complete", "4", "99", "1"), "complete-bad-mid-list")
	if field(t, home, "4", "state") != "open" || field(t, home, "4", "updated_at") != fixed {
		t.Errorf("a refused list changed @4")
	}
	// A duplicate, however it is written, at its later occurrence.
	r := fails(t, home, "invalid-input", "", "complete", "4", "@4")
	golden(t, r, "complete-duplicate")
	if problemFields(r) != "/tasks/1" {
		t.Errorf("duplicate: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "reopen", "1,2", "1"); problemFields(r) != "/tasks/2" {
		t.Errorf("reopen duplicate: %s", r.Line())
	}
	// The blocker and one of its waiters together: the waiter is
	// completed in the same call, so only 3 is released.
	golden(t, ok(t, home, "complete", "2", "1"), "complete-blocker-and-waiter")
	// Commas and words mix.
	golden(t, ok(t, home, "complete", "3,4"), "complete-several")
	// Only the open ones are listed, and only they change.
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r = ok(t, home, "complete", "1", "5", "2")
	golden(t, r, "complete-some-complete")
	if fmt.Sprint(r.Result()) != "map[completed:[5] released:[] unclaimed:[]]" {
		t.Errorf("some complete: %s", r.Line())
	}
	if field(t, home, "1", "updated_at") != fixed || field(t, home, "2", "updated_at") != fixed {
		t.Errorf("an already complete task changed")
	}
	// Reopening the blocker with one waiter: 2 wasn't ready, so only 3,
	// ready before, waits again; 5 is complete, and stays so.
	ok(t, home, "reopen", "3")
	r = ok(t, home, "reopen", "1", "2", "4")
	golden(t, r, "reopen-several")
	if fmt.Sprint(r.Result()) != "map[reblocked:[3] reopened:[1 2 4]]" {
		t.Errorf("reopen several: %s", r.Line())
	}

	// unclaimed lists the claims ended, in completed's order.
	ok(t, home, "claim", "2,4")
	r = ok(t, home, "complete", "4", "1", "2")
	if fmt.Sprint(r.Result()) != "map[completed:[4 1 2] released:[3] unclaimed:[4 2]]" {
		t.Errorf("unclaimed: %s", r.Line())
	}
	ok(t, home, "reopen", "1,2")

	// Through --input, a list of references.
	r = input(t, home, "complete", `{"tasks":[1,"@2","3"]}`)
	golden(t, r, "complete-input")
	// At least one task, as a word or in the input.
	golden(t, invalidAt(t, home, "/tasks", "complete"), "complete-no-subject")
	invalidAt(t, home, "/tasks", "reopen")
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"tasks":[]}`)}.Koan(t, "complete", "-i", "-")
	if r.Kind() != "invalid-input" || problemFields(r) != "/tasks" {
		t.Errorf("an empty list: %s", r.Line())
	}
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"task":1}`)}.Koan(t, "reopen", "-i", "-")
	if r.Kind() != "invalid-input" {
		t.Errorf("the old field: %s", r.Line())
	}
}

// update changes a task's title, priority, tags and extra: tags added,
// removed or replaced, extra merged at the top level, keys removed, or
// replaced. A change that changes nothing says so and leaves updated_at
// alone.
func TestTaskUpdate(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "t", "--tags", "a,b", "--extra", `{"x":1,"y":{"z":2}}`) // 1
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "update", "1", "--title", "  New ", "--priority", "3", "--tags-add", "c,0", "--tags-remove", "a",
		"--extra-merge", `{"w":true,"x":{"q":1}}`, "--extra-remove", "y")
	golden(t, r, "task-update")
	rec := ok(t, home, "show", "1").Result()["task"].(map[string]any)
	extra, _ := json.Marshal(rec["extra"])
	if rec["title"] != "New" || rec["priority"] != json.Number("3") || fmt.Sprint(rec["tags"]) != "[0 b c]" ||
		string(extra) != `{"w":true,"x":{"q":1}}` || rec["updated_at"] == fixed {
		t.Errorf("updated: %v", rec)
	}
	// The same values again, a tag already there added and one not
	// there removed, a key not there removed: nothing changes.
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	for _, args := range [][]string{
		{"--title", "New", "--priority", "3"},
		{"--tags-add", "b", "--tags-remove", "z"},
		{"--extra-merge", `{"w":true}`, "--extra-remove", "nope"},
		{"--tags-replace-all", "c,b,0", "--extra-replace-all", `{"x":{"q":1},"w":true}`},
	} {
		r := ok(t, home, append([]string{"update", "1"}, args...)...)
		if fmt.Sprint(r.Result()) != "map[updated:[]]" || field(t, home, "1", "updated_at") != fixed {
			t.Errorf("%q: %s", args, r.Line())
		}
	}
	// --priority null clears; --title null is null, which a task's title
	// can't be; replacing all with nothing empties.
	r = ok(t, home, "update", "1", "--priority", "null", "--extra-replace-all", "{}")
	if fmt.Sprint(r.Result()["updated"]) != "[1]" || field(t, home, "1", "priority") != nil ||
		fmt.Sprint(field(t, home, "1", "extra")) != "map[]" {
		t.Errorf("null: %s", r.Line())
	}
	sameFault(t, home, "invalid-input", "/title", []string{"update", "1", "--title", "null"}, "update", `{"refs":[1],"title":null}`)
	if r := input(t, home, "update", `{"refs":[1],"tags_replace_all":[]}`); fmt.Sprint(r.Result()["updated"]) != "[1]" || fmt.Sprint(field(t, home, "1", "tags")) != "[]" {
		t.Errorf("no tags: %s", r.Line())
	}
	if r := input(t, home, "update", `{"refs":[1],"tags_add":[]}`); fmt.Sprint(r.Result()) != "map[updated:[]]" {
		t.Errorf("an empty list: %s", r.Line())
	}
	// extra changes when it would print differently, so a number spelled
	// another way is a change, and is kept as spelled.
	for _, c := range []struct{ merge, want string }{
		{`{"n":1.0}`, `{"updated":[1]}`},
		{`{"n":1.0}`, `{"updated":[]}`},
		{`{"n":1}`, `{"updated":[1]}`},
	} {
		r := ok(t, home, "update", "1", "--extra-merge", c.merge)
		if got, _ := json.Marshal(r.Result()); string(got) != c.want {
			t.Errorf("extra %s: %s", c.merge, r.Line())
		}
	}
	ok(t, home, "update", "1", "--extra-merge", `{"n":1.0}`)
	if r := ok(t, home, "show", "1"); !strings.Contains(r.Line(), `"extra":{"n":1.0}`) {
		t.Errorf("extra as spelled: %s", r.Line())
	}
	// A gate's fields change like any task's.
	ok(t, home, "folder", "create", "/w")                 // 2
	ok(t, home, "block", "1", "--blocked-by", "/w.start") // 3
	if r := ok(t, home, "update", "/w.start", "--title", "Begin"); fmt.Sprint(r.Result()) != "map[updated:[3]]" || field(t, home, "3", "title") != "Begin" {
		t.Errorf("gate: %s", r.Line())
	}
	// Another task's fields stay as they were.
	if field(t, home, "1", "title") != "New" {
		t.Errorf("only the subject changes")
	}
	golden(t, fails(t, home, "not-found", "", "update", "99", "--title", "x"), "task-update-not-found")
	fails(t, home, "conflict", "case-mismatch", "update", "/W.start", "--title", "x")
}

// What an update needs: something to change, values by their rules, and
// fields that go together; every fault named, before the root is looked
// at.
func TestTaskUpdateInvalid(t *testing.T) {
	home := t.TempDir()
	golden(t, fails(t, home, "invalid-input", "", "update", "1"), "task-update-nothing")
	golden(t, fails(t, home, "invalid-input", "", "update", "1", "--tags-add", "x,y", "--tags-remove", "z,x",
		"--extra-merge", `{"k":1}`, "--extra-remove", "k"), "task-update-added-and-removed")
	for _, c := range []struct {
		args   []string
		fields string
	}{
		{[]string{"/", "--title", "x"}, "/refs/0"},
		{[]string{"1", "--title", " "}, "/title"},
		{[]string{"1", "--priority", "9007199254740992"}, "/priority"},
		{[]string{"1", "--tags-add", "A,b,b"}, "/tags_add/0"},
		{[]string{"1", "--tags-replace-all", "a", "--tags-add", "b"}, ""},
		{[]string{"1", "--tags-replace-all", "a", "--tags-remove", "b"}, ""},
		{[]string{"1", "--extra-replace-all", "{}", "--extra-merge", "{}"}, ""},
		{[]string{"1", "--extra-replace-all", "{}", "--extra-remove", "k"}, ""},
		{[]string{"1", "--extra-merge", "[1]", "--extra-replace-all", "x"}, "/extra_merge /extra_replace_all"},
		{[]string{"1", "--tags-add", "x", "--tags-remove", "x,x"}, "/tags_remove/0"},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"update"}, c.args...)...)
		if problemFields(r) != c.fields {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	for in, fields := range map[string]string{
		`{}`:                               "/refs ",
		`{"title":"x"}`:                    "/refs",
		`{"refs":1,"title":"x"}`:           "/refs",
		`{"refs":[],"title":"x"}`:          "/refs",
		`{"refs":[1],"priority":1.5}`:      "/priority",
		`{"refs":[1],"extra_remove":[1]}`:  "/extra_remove/0",
		`{"refs":[1],"tags_add":"a"}`:      "/tags_add",
		`{"refs":[1],"tags_add":[],"x":1}`: "/x",
	} {
		if r := input(t, home, "update", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	fails(t, home, "not-initialized", "", "update", "1", "--title", "x")
}

// update changes a folder's title and extra as it does a task's, and
// --title null clears the title; the root can't be updated.
func TestFolderUpdate(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w", "--title", "T", "--extra", `{"a":1,"b":[1]}`) // 1
	r := ok(t, home, "update", "/w", "--title", "null", "--extra-merge", `{"c":"x"}`, "--extra-remove", "a")
	golden(t, r, "folder-update")
	rec := ok(t, home, "show", "1").Result()["folder"].(map[string]any)
	extra, _ := json.Marshal(rec["extra"])
	if rec["title"] != nil || string(extra) != `{"b":[1],"c":"x"}` {
		t.Errorf("updated: %v", rec)
	}
	for _, args := range [][]string{{"--title", "null"}, {"--extra-merge", `{"c":"x"}`}, {"--extra-replace-all", `{"c":"x","b":[1]}`}} {
		if r := ok(t, home, append([]string{"update", "@1"}, args...)...); fmt.Sprint(r.Result()) != "map[updated:[]]" {
			t.Errorf("%q: %s", args, r.Line())
		}
	}
	if r := ok(t, home, "update", "1", "--title", " New "); fmt.Sprint(r.Result()["updated"]) != "[1]" ||
		ok(t, home, "show", "1").Result()["folder"].(map[string]any)["title"] != "New" {
		t.Errorf("title: %s", r.Line())
	}
	golden(t, fails(t, home, "invalid-input", "", "update", "/", "--title", "x"), "folder-update-root")
	golden(t, fails(t, home, "not-found", "", "update", "/z", "--title", "x"), "folder-update-not-found")
	fails(t, home, "conflict", "case-mismatch", "update", "/W", "--title", "x")
	for in, fields := range map[string]string{
		`{"refs":["/w"]}`:                                            "",
		`{"refs":["/w"],"priority":1}`:                               "/priority",
		`{"refs":["/w"],"tags_add":["a"]}`:                           "/tags_add",
		`{"refs":["/w:x"],"title":"x"}`:                              "/refs/0",
		`{"refs":["/w"],"title":1}`:                                  "/title",
		`{"refs":["/w"],"extra_merge":{"k":1},"extra_remove":["k"]}`: "/extra_remove/0",
		`{"refs":["/w"],"extra_replace_all":{},"extra_merge":{}}`:    "",
	} {
		if r := input(t, home, "update", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// update takes tasks and folders mixed, as one change: if any is
// refused, none changes. An option a subject's kind doesn't take is
// invalid-input at its field, after the references resolve: priority and
// tags with a folder among the subjects, the priority's reason pointing
// at the folder's gates; --title null with a task among them. updated
// lists those that changed, in input order.
func TestUpdateMixed(t *testing.T) {
	home := root(t)
	setup(t, home, "folder", "create", "/w", "--title", "W") // 1
	setup(t, home, "task", "create", "t")                    // 2
	setup(t, home, "task", "create", "u", "--folder", "/w")  // 3
	r := ok(t, home, "update", "3", "/w", "2", "--extra-merge", `{"k":1}`)
	golden(t, r, "update-mixed")
	if fmt.Sprint(r.Result()["updated"]) != "[3 1 2]" {
		t.Errorf("mixed: %s", r.Line())
	}
	// Only what changed is listed.
	if r := ok(t, home, "update", "2,/w", "--title", "W"); fmt.Sprint(r.Result()["updated"]) != "[2]" {
		t.Errorf("only changed: %s", r.Line())
	}

	r = fails(t, home, "invalid-input", "", "update", "2", "/w", "--priority", "1")
	golden(t, r, "update-folder-priority")
	if problemFields(r) != "/priority" || !strings.Contains(r.Error()["message"].(string), "/w.start") {
		t.Errorf("priority: %s", r.Line())
	}
	sameFault(t, home, "invalid-input", "/priority", []string{"update", "/w", "--priority", "null"}, "update", `{"refs":["/w"],"priority":null}`)
	for _, field := range []string{"tags-add", "tags-remove", "tags-replace-all"} {
		pointer := "/" + strings.ReplaceAll(field, "-", "_")
		sameFault(t, home, "invalid-input", pointer, []string{"update", "/w", "2", "--" + field, "a"}, "update",
			fmt.Sprintf(`{"refs":["/w",2],%q:["a"]}`, pointer[1:]))
	}
	r = fails(t, home, "invalid-input", "", "update", "/w", "3", "--title", "null")
	golden(t, r, "update-task-title-null")
	if problemFields(r) != "/title" {
		t.Errorf("title null: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "update", "2", "/w", "--title", "null", "--priority", "1", "--tags-add", "a"); problemFields(r) != "/title /priority /tags_add" {
		t.Errorf("every field: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "update", "2", "/w", "@2", "--title", "x"); problemFields(r) != "/refs/2" {
		t.Errorf("twice: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "update", "2", "/", "--title", "x"); problemFields(r) != "/refs/1" {
		t.Errorf("the root: %s", r.Line())
	}
	fails(t, home, "not-found", "", "update", "2", "/z", "--title", "x")
	// Nothing refused changed anything: the folder keeps its title.
	if title := ok(t, home, "show", "/w").Result()["folder"].(map[string]any)["title"]; title != "W" {
		t.Errorf("a refused update changed the folder: %v", title)
	}
	if title := field(t, home, "2", "title"); title != "W" {
		t.Errorf("a refused update changed the task: %v", title)
	}
	// On a folder alone, --title null clears it.
	if r := ok(t, home, "update", "/w", "--title", "null"); fmt.Sprint(r.Result()["updated"]) != "[1]" {
		t.Errorf("clear: %s", r.Line())
	}
}

// claim claims open tasks, blocked or not, with the caller's extra on
// each; a second claim is claimed, a complete task complete. unclaim ends
// claims whoever made them, and a task with none changes nothing.
// Neither moves updated_at.
func TestTaskClaim(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "a")                      // 1
	ok(t, home, "task", "create", "b", "--blocked-by", "1") // 2
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "claim", "2", "--extra", `{"who":"w1"}`)
	golden(t, r, "claim")
	claim := field(t, home, "2", "claim").(map[string]any)
	if fmt.Sprint(r.Result()) != "map[claimed:[2]]" || fmt.Sprint(claim["extra"]) != "map[who:w1]" ||
		field(t, home, "2", "updated_at") != fixed {
		t.Errorf("claim: %s, record %v", r.Line(), claim)
	}
	golden(t, fails(t, home, "conflict", "claimed", "claim", "@2"), "claim-claimed")
	ok(t, home, "complete", "1")
	golden(t, fails(t, home, "conflict", "already-complete", "claim", "1"), "claim-complete")
	fails(t, home, "not-found", "", "claim", "99")
	if r := input(t, home, "claim", `{"tasks":[1],"extra":[]}`); r.Kind() != "invalid-input" || problemFields(r) != "/extra" {
		t.Errorf("extra: %s", r.Line())
	}
	r = ok(t, home, "unclaim", "2")
	golden(t, r, "unclaim")
	if fmt.Sprint(r.Result()) != "map[unclaimed:[2]]" || field(t, home, "2", "claim") != nil || field(t, home, "2", "updated_at") != fixed {
		t.Errorf("unclaimed: %s", r.Line())
	}
	if r := ok(t, home, "unclaim", "2"); fmt.Sprint(r.Result()) != "map[unclaimed:[]]" {
		t.Errorf("again: %s", r.Line())
	}
	ok(t, home, "claim", "2")
	if fmt.Sprint(field(t, home, "2", "claim").(map[string]any)["extra"]) != "map[]" {
		t.Errorf("default extra")
	}
	fails(t, home, "not-found", "", "unclaim", "99")
}

// claim and unclaim take several tasks, comma-separated or as more
// words, as one change: one refused among them claims none, and a task
// given twice is invalid-input at its later occurrence. --extra is kept
// with every claim. Each result lists what changed, in input order.
func TestClaimSeveral(t *testing.T) {
	home := root(t)
	for range 5 {
		ok(t, home, "task", "create", "t") // 1 to 5
	}
	ok(t, home, "block", "4", "--blocked-by", "5")
	ok(t, home, "claim", "3")
	ok(t, home, "complete", "5")

	before := dump(t, home)
	for _, c := range []struct {
		args      []string
		rule, ids string
	}{
		{[]string{"4", "1", "3"}, "claimed", "[3]"},
		{[]string{"4,5", "3"}, "already-complete", "[5]"},
	} {
		r := fails(t, home, "conflict", c.rule, append([]string{"claim"}, c.args...)...)
		if fmt.Sprint(r.Error()["ids"]) != c.ids {
			t.Errorf("%v: %s", c.args, r.Line())
		}
	}
	for _, args := range [][]string{{"claim", "1", "@1"}, {"unclaim", "3", "@3"}} {
		if r := fails(t, home, "invalid-input", "", args...); !strings.HasPrefix(problemFields(r), "/tasks/") ||
			r.Error()["message"] == "" {
			t.Errorf("%v: %s", args, r.Line())
		}
	}
	if r := fails(t, home, "invalid-input", "", "claim", "2,1,2"); problemFields(r) != "/tasks/2" {
		t.Errorf("duplicate: %s", r.Line())
	}
	fails(t, home, "not-found", "", "unclaim", "3", "99")
	if dump(t, home) != before {
		t.Error("a refused claim or unclaim changed the tree")
	}

	r := ok(t, home, "claim", "4,1", "2", "--extra", `{"by":"w"}`)
	golden(t, r, "claim-several")
	if fmt.Sprint(r.Result()) != "map[claimed:[4 1 2]]" {
		t.Errorf("several: %s", r.Line())
	}
	for _, id := range []string{"4", "1", "2"} {
		if c, _ := field(t, home, id, "claim").(map[string]any); c == nil || fmt.Sprint(c["extra"]) != "map[by:w]" {
			t.Errorf("claim of %s: %v", id, c)
		}
	}
	// Only the tasks that had a claim are listed.
	ok(t, home, "unclaim", "1")
	r = ok(t, home, "unclaim", "2", "1,3", "5")
	golden(t, r, "unclaim-several")
	if fmt.Sprint(r.Result()) != "map[unclaimed:[2 3]]" {
		t.Errorf("unclaim: %s", r.Line())
	}
	if c := field(t, home, "4", "claim"); c == nil {
		t.Error("an unclaim of others ended @4's claim")
	}
	if r := input(t, home, "claim", `{"tasks":[1,"@2"]}`); !r.OK() || fmt.Sprint(r.Result()) != "map[claimed:[1 2]]" {
		t.Errorf("input: %s", r.Line())
	}
}

// claim-next takes the folder it looks under as its subject.
func TestTaskClaimNextSubject(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a/b")                 // 1, 2
	ok(t, home, "task", "create", "r1", "--priority", "9")  // 3
	ok(t, home, "task", "create", "b1", "--folder", "/a/b") // 4
	goldenNow(t, ok(t, home, "claim-next", "/a"), "claim-next-subject")
	usageAt(t, home, "/a/b", "claim-next", "/a", "/a/b")
}

// claim-next claims the first ready task with no claim within task
// frontier's filters, in work order, and returns its record with the new
// claim; nulls when there is none.
func TestTaskClaimNext(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a/b") // 1, 2
	for _, c := range [][]string{
		{"r1"}, // 3
		{"a1", "--folder", "/a", "--priority", "2"},                  // 4
		{"b1", "--folder", "/a/b", "--priority", "5", "--tags", "x"}, // 5
		{"r2", "--priority", "-1", "--tags", "x,y"},                  // 6
		{"a2", "--folder", "/a", "--blocked-by", "3"},                // 7
	} {
		ok(t, home, append([]string{"task", "create"}, c...)...)
	}
	r := ok(t, home, "claim-next", "--extra", `{"who":"w"}`)
	goldenNow(t, r, "claim-next")
	res := r.Result()
	if res["task"].(map[string]any)["id"] != json.Number("5") || res["task"].(map[string]any)["claim"] == nil || res["claim"] != nil {
		t.Errorf("first: %s", r.Line())
	}
	next := func(args ...string) string {
		r := ok(t, home, append([]string{"claim-next"}, args...)...)
		if r.Result()["task"] == nil {
			return "none"
		}
		return fmt.Sprint(r.Result()["task"].(map[string]any)["id"])
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"/a", "--direct", "--tags-any", "x"}, "none"},
		{[]string{"--tags-all", "x,y"}, "6"},
		{[]string{"@1", "--direct"}, "4"},
		{[]string{"/a"}, "none"},
		{nil, "3"},
		{nil, "none"},
	} {
		if got := next(c.args...); got != c.want {
			t.Errorf("%q: got %s, want %s", c.args, got, c.want)
		}
	}
	golden(t, ok(t, home, "claim-next"), "claim-next-none")
	// Completing 3 lets 7 in.
	ok(t, home, "complete", "3")
	if got := next("/a"); got != "7" {
		t.Errorf("after complete: %s", got)
	}
	golden(t, fails(t, home, "not-found", "", "claim-next", "/z"), "claim-next-not-found")
	fails(t, home, "conflict", "case-mismatch", "claim-next", "/A")
	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"/a.start"}, "/folder"},
		{[]string{"--tags-any", "A"}, "/tags_any/0"},
		{[]string{"--extra", "1"}, "/extra"},
	} {
		if r := fails(t, home, "invalid-input", "", append([]string{"claim-next"}, c.args...)...); problemFields(r) != c.field {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
}

// Processes claiming at once never get the same task: each ready task is
// handed out once, and the rest get nulls.
func TestTaskClaimNextRace(t *testing.T) {
	if testing.Short() {
		t.Skip("starts many processes")
	}
	home := root(t)
	for range 6 {
		ok(t, home, "task", "create", "t")
	}
	results := make([]*testrun.Result, 16)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = testrun.Run{Home: home}.Koan(t, "claim-next")
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	nulls := 0
	for _, r := range results {
		r.Check(t, "claim-next")
		if !r.OK() {
			t.Fatalf("failed: %s", r.Line())
		}
		c, _ := r.Result()["task"].(map[string]any)
		if c == nil {
			nulls++
			continue
		}
		id := fmt.Sprint(c["id"])
		if seen[id] {
			t.Errorf("@%s handed out twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 6 || nulls != 10 {
		t.Errorf("%d claimed, %d nulls", len(seen), nulls)
	}
}
