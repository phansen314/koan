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
// claim; it reports the open tasks it made ready, and moves updated_at on
// it alone. Completing a complete task changes nothing.
func TestTaskComplete(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	for range 4 {
		ok(t, home, "task", "create", "t") // 2 to 5
	}
	// 3 waits on 2; 4 on 2 and 5; 5 on /w.start, made 6.
	ok(t, home, "block", "2", "--blocks", "3,4")
	ok(t, home, "block", "5", "/w.start", "--blocks", "4")
	ok(t, home, "claim", "2")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "complete", "2")
	golden(t, r, "complete")
	if fmt.Sprint(r.Result()) != "map[completed:[2] released:[3]]" {
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
	if fmt.Sprint(r.Result()) != "map[completed:[] released:[]]" || field(t, home, "2", "updated_at") != fixed {
		t.Errorf("again: %s", r.Line())
	}
	// A blocked task, and a gate by its path, can be completed; the
	// gate's completing releases 5, which then releases 4.
	if r := ok(t, home, "complete", "/w.start"); fmt.Sprint(r.Result()) != "map[completed:[6] released:[5]]" {
		t.Errorf("gate: %s", r.Line())
	}
	if r := ok(t, home, "complete", "5"); fmt.Sprint(r.Result()) != "map[completed:[5] released:[4]]" {
		t.Errorf("last blocker: %s", r.Line())
	}
	ok(t, home, "block", "3", "4")
	if r := ok(t, home, "complete", "3"); fmt.Sprint(r.Result()) != "map[completed:[3] released:[]]" {
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
	ok(t, home, "block", "4", "5")
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
	ok(t, home, "release", "2")
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
	ok(t, home, "block", "5", "4")
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
	if fmt.Sprint(r.Result()) != "map[completed:[5] released:[]]" {
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

	// Through --input, a list of references.
	r = input(t, home, "complete", `{"tasks":[1,"@2","3"]}`)
	golden(t, r, "complete-input")
	// At least one task, as a word or in the input.
	golden(t, usageAt(t, home, "", "complete"), "complete-no-subject")
	usageAt(t, home, "", "reopen")
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"tasks":[]}`)}.Koan(t, "complete", "-i", "-")
	if r.Kind() != "invalid-input" || problemFields(r) != "/tasks" {
		t.Errorf("an empty list: %s", r.Line())
	}
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"task":1}`)}.Koan(t, "reopen", "-i", "-")
	if r.Kind() != "invalid-input" {
		t.Errorf("the old field: %s", r.Line())
	}
}

// task update changes title, priority, tags and extra: tags added,
// removed or replaced, extra merged at the top level, keys removed, or
// replaced. A change that changes nothing says so and leaves updated_at
// alone.
func TestTaskUpdate(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "t", "--tags", "a,b", "--extra", `{"x":1,"y":{"z":2}}`) // 1
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "task", "update", "1", "--title", "  New ", "--priority", "3", "--tags-add", "c,0", "--tags-remove", "a",
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
		r := ok(t, home, append([]string{"task", "update", "1"}, args...)...)
		if fmt.Sprint(r.Result()) != "map[changed:false id:1]" || field(t, home, "1", "updated_at") != fixed {
			t.Errorf("%q: %s", args, r.Line())
		}
	}
	// --priority null clears; --title null is the word; replacing all
	// with nothing empties.
	r = ok(t, home, "task", "update", "1", "--priority", "null", "--title", "null", "--extra-replace-all", "{}")
	if r.Result()["changed"] != true || field(t, home, "1", "priority") != nil || field(t, home, "1", "title") != "null" ||
		fmt.Sprint(field(t, home, "1", "extra")) != "map[]" {
		t.Errorf("null: %s", r.Line())
	}
	if r := input(t, home, "task update", `{"task":1,"tags_replace_all":[]}`); r.Result()["changed"] != true || fmt.Sprint(field(t, home, "1", "tags")) != "[]" {
		t.Errorf("no tags: %s", r.Line())
	}
	if r := input(t, home, "task update", `{"task":1,"tags_add":[]}`); fmt.Sprint(r.Result()) != "map[changed:false id:1]" {
		t.Errorf("an empty list: %s", r.Line())
	}
	// extra changes when it would print differently, so a number spelled
	// another way is a change, and is kept as spelled.
	for _, c := range []struct{ merge, want string }{
		{`{"n":1.0}`, `{"changed":true,"id":1}`},
		{`{"n":1.0}`, `{"changed":false,"id":1}`},
		{`{"n":1}`, `{"changed":true,"id":1}`},
	} {
		r := ok(t, home, "task", "update", "1", "--extra-merge", c.merge)
		if got, _ := json.Marshal(r.Result()); string(got) != c.want {
			t.Errorf("extra %s: %s", c.merge, r.Line())
		}
	}
	ok(t, home, "task", "update", "1", "--extra-merge", `{"n":1.0}`)
	if r := ok(t, home, "show", "1"); !strings.Contains(r.Line(), `"extra":{"n":1.0}`) {
		t.Errorf("extra as spelled: %s", r.Line())
	}
	// A gate's fields change like any task's.
	ok(t, home, "folder", "create", "/w") // 2
	ok(t, home, "block", "1", "/w.start") // 3
	if r := ok(t, home, "task", "update", "/w.start", "--title", "Begin"); fmt.Sprint(r.Result()) != "map[changed:true id:3]" || field(t, home, "3", "title") != "Begin" {
		t.Errorf("gate: %s", r.Line())
	}
	// Another task's fields stay as they were.
	if field(t, home, "1", "title") != "null" {
		t.Errorf("only the subject changes")
	}
	golden(t, fails(t, home, "not-found", "", "task", "update", "99", "--title", "x"), "task-update-not-found")
	fails(t, home, "conflict", "case-mismatch", "task", "update", "/W.start", "--title", "x")
}

// What an update needs: something to change, values by their rules, and
// fields that go together; every fault named, before the root is looked
// at.
func TestTaskUpdateInvalid(t *testing.T) {
	home := t.TempDir()
	golden(t, fails(t, home, "invalid-input", "", "task", "update", "1"), "task-update-nothing")
	golden(t, fails(t, home, "invalid-input", "", "task", "update", "1", "--tags-add", "x,y", "--tags-remove", "z,x",
		"--extra-merge", `{"k":1}`, "--extra-remove", "k"), "task-update-added-and-removed")
	for _, c := range []struct {
		args   []string
		fields string
	}{
		{[]string{"/", "--title", "x"}, "/task"},
		{[]string{"1", "--title", " "}, "/title"},
		{[]string{"1", "--priority", "9007199254740992"}, "/priority"},
		{[]string{"1", "--tags-add", "A,b,b"}, "/tags_add/0 /tags_add/2"},
		{[]string{"1", "--tags-replace-all", "a", "--tags-add", "b"}, ""},
		{[]string{"1", "--tags-replace-all", "a", "--tags-remove", "b"}, ""},
		{[]string{"1", "--extra-replace-all", "{}", "--extra-merge", "{}"}, ""},
		{[]string{"1", "--extra-replace-all", "{}", "--extra-remove", "k"}, ""},
		{[]string{"1", "--extra-merge", "[1]", "--extra-replace-all", "x"}, "/extra_merge /extra_replace_all"},
		{[]string{"1", "--extra-remove", "k", "--extra-remove", "k"}, "/extra_remove/1"},
		{[]string{"1", "--tags-add", "x", "--tags-remove", "x,x"}, "/tags_remove/1 /tags_remove/0"},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"task", "update"}, c.args...)...)
		if problemFields(r) != c.fields {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	for in, fields := range map[string]string{
		`{}`:                             "/task ",
		`{"title":"x"}`:                  "/task",
		`{"task":1,"title":null}`:        "/title",
		`{"task":1,"priority":1.5}`:      "/priority",
		`{"task":1,"extra_remove":[1]}`:  "/extra_remove/0",
		`{"task":1,"tags_add":"a"}`:      "/tags_add",
		`{"task":1,"tags_add":[],"x":1}`: "/x",
	} {
		if r := input(t, home, "task update", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	fails(t, home, "not-initialized", "", "task", "update", "1", "--title", "x")
}

// folder update changes title and extra as task update does; the root
// can't be updated.
func TestFolderUpdate(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w", "--title", "T", "--extra", `{"a":1,"b":[1]}`) // 1
	r := ok(t, home, "folder", "update", "/w", "--title", "null", "--extra-merge", `{"c":"x"}`, "--extra-remove", "a")
	golden(t, r, "folder-update")
	rec := ok(t, home, "show", "1").Result()["folder"].(map[string]any)
	extra, _ := json.Marshal(rec["extra"])
	if rec["title"] != nil || string(extra) != `{"b":[1],"c":"x"}` {
		t.Errorf("updated: %v", rec)
	}
	for _, args := range [][]string{{"--title", "null"}, {"--extra-merge", `{"c":"x"}`}, {"--extra-replace-all", `{"c":"x","b":[1]}`}} {
		if r := ok(t, home, append([]string{"folder", "update", "@1"}, args...)...); fmt.Sprint(r.Result()) != "map[changed:false id:1]" {
			t.Errorf("%q: %s", args, r.Line())
		}
	}
	if r := ok(t, home, "folder", "update", "1", "--title", " New "); r.Result()["changed"] != true ||
		ok(t, home, "show", "1").Result()["folder"].(map[string]any)["title"] != "New" {
		t.Errorf("title: %s", r.Line())
	}
	golden(t, fails(t, home, "invalid-input", "", "folder", "update", "/", "--title", "x"), "folder-update-root")
	golden(t, fails(t, home, "not-found", "", "folder", "update", "/z", "--title", "x"), "folder-update-not-found")
	fails(t, home, "conflict", "case-mismatch", "folder", "update", "/W", "--title", "x")
	for in, fields := range map[string]string{
		`{"folder":"/w"}`:                                            "",
		`{"folder":"/w","priority":1}`:                               "/priority",
		`{"folder":"/w","tags_add":["a"]}`:                           "/tags_add",
		`{"folder":"/w.start","title":"x"}`:                          "/folder",
		`{"folder":"/w","title":1}`:                                  "/title",
		`{"folder":"/w","extra_merge":{"k":1},"extra_remove":["k"]}`: "/extra_remove/0",
		`{"folder":"/w","extra_replace_all":{},"extra_merge":{}}`:    "",
	} {
		if r := input(t, home, "folder update", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// claim claims an open task, blocked or not, with the caller's
// extra; a second claim is claimed, a complete task complete. release
// ends a claim whoever made it, and a task with none changes nothing.
// Neither moves updated_at.
func TestTaskClaim(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "a")                      // 1
	ok(t, home, "task", "create", "b", "--blocked-by", "1") // 2
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "claim", "2", "--extra", `{"who":"w1"}`)
	goldenNow(t, r, "claim")
	claim := r.Result()["claim"]
	if got := field(t, home, "2", "claim"); fmt.Sprint(got) != fmt.Sprint(claim) || field(t, home, "2", "updated_at") != fixed {
		t.Errorf("claim: %v, record %v", claim, got)
	}
	golden(t, fails(t, home, "conflict", "claimed", "claim", "@2"), "claim-claimed")
	ok(t, home, "complete", "1")
	golden(t, fails(t, home, "conflict", "complete", "claim", "1"), "claim-complete")
	fails(t, home, "not-found", "", "claim", "99")
	if r := input(t, home, "claim", `{"task":1,"extra":[]}`); r.Kind() != "invalid-input" || problemFields(r) != "/extra" {
		t.Errorf("extra: %s", r.Line())
	}
	r = ok(t, home, "release", "2")
	golden(t, r, "release")
	if field(t, home, "2", "claim") != nil || field(t, home, "2", "updated_at") != fixed {
		t.Errorf("released")
	}
	if r := ok(t, home, "release", "2"); fmt.Sprint(r.Result()) != "map[changed:false id:2]" {
		t.Errorf("again: %s", r.Line())
	}
	r = ok(t, home, "claim", "2")
	if fmt.Sprint(r.Result()["claim"].(map[string]any)["extra"]) != "map[]" {
		t.Errorf("default extra: %s", r.Line())
	}
	fails(t, home, "not-found", "", "release", "99")
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
	if res["task"].(map[string]any)["id"] != json.Number("5") || fmt.Sprint(res["task"].(map[string]any)["claim"]) != fmt.Sprint(res["claim"]) {
		t.Errorf("first: %s", r.Line())
	}
	next := func(args ...string) string {
		r := ok(t, home, append([]string{"claim-next"}, args...)...)
		if r.Result()["task"] == nil {
			if r.Result()["claim"] != nil {
				t.Errorf("a claim with no task: %s", r.Line())
			}
			return "none"
		}
		return fmt.Sprint(r.Result()["claim"].(map[string]any)["task"])
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"/a", "--recursive=false", "--tags-any", "x"}, "none"},
		{[]string{"--tags-all", "x,y"}, "6"},
		{[]string{"@1", "--recursive=false"}, "4"},
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
		c, _ := r.Result()["claim"].(map[string]any)
		if c == nil {
			nulls++
			continue
		}
		id := fmt.Sprint(c["task"])
		if seen[id] {
			t.Errorf("@%s handed out twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 6 || nulls != 10 {
		t.Errorf("%d claimed, %d nulls", len(seen), nulls)
	}
}
