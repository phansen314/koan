package main_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// tagsNoneTree makes a tree where parked work would come first: five
// ready tasks in /w, highest priority first, and in /v a blocked task and
// the start gate it waits on.
func tagsNoneTree(t *testing.T) string {
	t.Helper()
	home := root(t)
	batchOK(t, home, `{"folders": [{"path": "/w"}, {"path": "/v"}],
	  "tasks": [
	    {"title": "parked", "folder": "/w", "priority": 5, "tags": ["waiting"]},
	    {"title": "later", "folder": "/w", "priority": 4, "tags": ["deferred", "db"]},
	    {"title": "db work", "folder": "/w", "priority": 3, "tags": ["db"]},
	    {"title": "plain", "folder": "/w", "priority": 2},
	    {"title": "db and api", "folder": "/w", "priority": 1, "tags": ["db", "api"]},
	    {"title": "gated", "folder": "/v", "tags": ["waiting"], "blocked_by": ["/v.start"]}]}`)
	return home
}

// idsOf is the IDs a list's result gives, in order.
func idsOf(r *testrun.Result) string {
	var out []string
	for _, x := range r.Result()["tasks"].([]any) {
		out = append(out, fmt.Sprint(x.(map[string]any)["id"]))
	}
	return strings.Join(out, " ")
}

// Only tasks holding none of the tags pass, on task list and frontier
// alike: alone, with the other tag filters, empty, and given twice.
func TestTagsNoneFilter(t *testing.T) {
	home := tagsNoneTree(t)
	for _, c := range []struct {
		args           []string
		frontier, list string
	}{
		{nil, "3 4 5 6 7 9", "3 4 5 6 7 8 9"},
		{[]string{"--tags-none", "waiting"}, "4 5 6 7 9", "4 5 6 7 9"},
		{[]string{"--tags-none", "waiting,deferred"}, "5 6 7 9", "5 6 7 9"},
		{[]string{"--tags-none", "waiting", "--tags-none", "deferred"}, "5 6 7 9", "5 6 7 9"},
		{[]string{"--tags-none", "waiting,waiting"}, "4 5 6 7 9", "4 5 6 7 9"},
		{[]string{"--tags-none", "nothing-holds-this"}, "3 4 5 6 7 9", "3 4 5 6 7 8 9"},
		{[]string{"--tags-any", "db", "--tags-none", "deferred"}, "5 7", "5 7"},
		{[]string{"--tags-all", "db,api", "--tags-none", "waiting"}, "7", "7"},
		{[]string{"--tags-any", "api", "--tags-all", "db", "--tags-none", "deferred"}, "7", "7"},
		{[]string{"--tags-any", "db", "--tags-none", "api"}, "4 5", "4 5"},
		{[]string{"/w", "--tags-none", "waiting"}, "4 5 6 7", "4 5 6 7"},
		{[]string{"/v", "--tags-none", "waiting"}, "9", "9"},
		{[]string{"/v", "--direct", "--tags-none", "deferred"}, "9", "8 9"},
	} {
		r := ok(t, home, append([]string{"frontier", "--fields", "id"}, c.args...)...)
		if got := idsOf(r); got != c.frontier {
			t.Errorf("frontier %q: got %q, want %q", c.args, got, c.frontier)
		}
		if r.Result()["total"] != json.Number(fmt.Sprint(len(strings.Fields(c.frontier)))) {
			t.Errorf("frontier %q: %s", c.args, r.Line())
		}
		r = ok(t, home, append([]string{"task", "list", "--fields", "id"}, c.args...)...)
		if got := sorted(idsOf(r)); got != c.list {
			t.Errorf("task list %q: got %q, want %q", c.args, got, c.list)
		}
		if r.Result()["total"] != json.Number(fmt.Sprint(len(strings.Fields(c.list)))) {
			t.Errorf("task list %q: %s", c.args, r.Line())
		}
	}
	// Through -i it is an array, and an empty one filters nothing.
	if got := idsOf(input(t, home, "frontier", `{"tags_none":[],"fields":["id"]}`)); got != "3 4 5 6 7 9" {
		t.Errorf("frontier, empty: %s", got)
	}
	if got := idsOf(input(t, home, "frontier", `{"tags_none":["waiting","waiting"],"fields":["id"]}`)); got != "4 5 6 7 9" {
		t.Errorf("frontier, given twice: %s", got)
	}
	if got := sorted(idsOf(input(t, home, "task list", `{"tags_none":[],"fields":["id"]}`))); got != "3 4 5 6 7 8 9" {
		t.Errorf("task list, empty: %s", got)
	}
}

// sorted is a list of IDs in ascending order: task list's tree order
// isn't what these cases are about.
func sorted(ids string) string {
	f := strings.Fields(ids)
	slices.SortFunc(f, func(a, b string) int { return len(a) - len(b) + strings.Compare(a, b) })
	return strings.Join(f, " ")
}

// The limit counts only the tasks that pass: what is parked takes no
// place in the first few, and total and truncated describe what passed.
func TestTagsNoneLimit(t *testing.T) {
	home := tagsNoneTree(t)
	for _, command := range [][]string{{"frontier"}, {"task", "list"}} {
		r := ok(t, home, append(command, "--tags-none", "waiting,deferred", "--limit", "2", "--fields", "id")...)
		if len(r.Result()["tasks"].([]any)) != 2 || r.Result()["total"] != json.Number("4") || r.Result()["truncated"] != true {
			t.Errorf("%q, cut: %s", command, r.Line())
		}
		r = ok(t, home, append(command, "--tags-none", "waiting,deferred", "--limit", "4", "--fields", "id")...)
		if len(r.Result()["tasks"].([]any)) != 4 || r.Result()["total"] != json.Number("4") || r.Result()["truncated"] != false {
			t.Errorf("%q, exact: %s", command, r.Line())
		}
	}
	if got := idsOf(ok(t, home, "frontier", "--tags-none", "waiting", "--limit", "2", "--fields", "id")); got != "4 5" {
		t.Errorf("frontier's first two: %s", got)
	}
}

// claim-next never claims a task holding a listed tag, even the first in
// work order, and with nothing else to give it claims nothing.
func TestTagsNoneClaimNext(t *testing.T) {
	home := tagsNoneTree(t)
	next := func(args ...string) string {
		r := ok(t, home, append([]string{"claim-next"}, args...)...)
		if r.Result()["task"] == nil {
			return "none"
		}
		return fmt.Sprint(r.Result()["task"].(map[string]any)["id"])
	}
	if got := next("/w", "--tags-none", "waiting"); got != "4" {
		t.Errorf("first passing: %s", got)
	}
	if got := next("/w", "--tags-none", "waiting,deferred"); got != "5" {
		t.Errorf("two tags: %s", got)
	}
	if got := next("/w", "--tags-any", "db", "--tags-none", "deferred"); got != "7" {
		t.Errorf("with tags_any: %s", got)
	}
	if got := next("/w", "--tags-all", "db", "--tags-none", "deferred"); got != "none" {
		t.Errorf("with tags_all, nothing left: %s", got)
	}
	if got := next("/w", "--tags-none", "waiting,waiting"); got != "6" {
		t.Errorf("given twice: %s", got)
	}
	if got := next("/w", "--tags-none", "waiting"); got != "none" {
		t.Errorf("only parked left: %s", got)
	}
	if c := field(t, home, "3", "claim"); c != nil {
		t.Errorf("the parked task was claimed: %v", c)
	}
	// Empty, it filters nothing: the parked task is next.
	if r := input(t, home, "claim-next", `{"folder":"/w","tags_none":[]}`); fmt.Sprint(r.Result()["task"].(map[string]any)["id"]) != "3" {
		t.Errorf("empty: %s", r.Line())
	}
}

// A tag in tags_none and in tags_any or tags_all is refused, at its entry
// in tags_none, once however many times it is given; a refused claim-next
// claims nothing.
func TestTagsNoneBoth(t *testing.T) {
	home := tagsNoneTree(t)
	golden(t, fails(t, home, "invalid-input", "", "task", "list", "--tags-any", "waiting", "--tags-none", "waiting"), "task-list-tags-none-both")
	golden(t, fails(t, home, "invalid-input", "", "frontier", "--tags-any", "waiting", "--tags-none", "waiting"), "frontier-tags-none-both")
	golden(t, fails(t, home, "invalid-input", "", "claim-next", "--tags-any", "waiting", "--tags-none", "waiting"), "claim-next-tags-none-both")
	for _, command := range [][]string{{"task", "list"}, {"frontier"}, {"claim-next"}} {
		for _, c := range []struct {
			args  []string
			field string
		}{
			{[]string{"--tags-any", "a", "--tags-none", "a"}, "/tags_none/0"},
			{[]string{"--tags-all", "a", "--tags-none", "a"}, "/tags_none/0"},
			{[]string{"--tags-all", "a", "--tags-none", "b,a"}, "/tags_none/1"},
			{[]string{"--tags-any", "a", "--tags-all", "b", "--tags-none", "a,b"}, "/tags_none/0 /tags_none/1"},
			{[]string{"--tags-any", "a", "--tags-none", "a,a"}, "/tags_none/0"},
			{[]string{"--tags-none", "A"}, "/tags_none/0"},
			{[]string{"--tags-any", "A", "--tags-none", "A"}, "/tags_any/0 /tags_none/0"},
		} {
			r := fails(t, home, "invalid-input", "", append(slices.Clone(command), c.args...)...)
			if problemFields(r) != c.field {
				t.Errorf("%q %q: %s", command, c.args, r.Line())
			}
		}
		// A tag in both tags_any and tags_all still contradicts nothing.
		if command[0] != "claim-next" {
			ok(t, home, append(slices.Clone(command), "--tags-any", "a", "--tags-all", "a", "--tags-none", "b")...)
		}
	}
	r := input(t, home, "frontier", `{"tags_any":["a"],"tags_none":["a"]}`)
	if r.OK() || problemFields(r) != "/tags_none/0" {
		t.Errorf("through -i: %s", r.Line())
	}
	if got := ok(t, home, "task", "list", "--claimed", "--fields", "id"); got.Result()["total"] != json.Number("0") {
		t.Errorf("a refused claim-next claimed: %s", got.Line())
	}
}

// A gate is made with no tags, so it passes tags_none. Given a tag by
// update, it is filtered as any task is, by tags_none as by tags_any.
func TestTagsNoneGates(t *testing.T) {
	home := tagsNoneTree(t)
	if got := idsOf(ok(t, home, "frontier", "/v", "--tags-none", "waiting,deferred", "--fields", "id")); got != "9" {
		t.Errorf("a gate with no tags: %q", got)
	}
	r := ok(t, home, "claim-next", "/v", "--tags-none", "waiting")
	if fmt.Sprint(r.Result()["task"].(map[string]any)["id"]) != "9" {
		t.Errorf("claim-next of the gate: %s", r.Line())
	}
	ok(t, home, "unclaim", "9")
	ok(t, home, "update", "/v.start", "--tags-add", "waiting")
	if got := idsOf(ok(t, home, "frontier", "/v", "--tags-any", "waiting", "--fields", "id")); got != "9" {
		t.Errorf("tags_any, a tagged gate: %q", got)
	}
	if got := idsOf(ok(t, home, "frontier", "/v", "--tags-none", "waiting", "--fields", "id")); got != "" {
		t.Errorf("tags_none, a tagged gate: %q", got)
	}
}

// In pick, tags_none not empty is a task filter: a folder is a candidate
// only with a passing task under it, and a gate not made yet never is;
// left out, the empty folder and the parked one are candidates again.
func TestTagsNonePick(t *testing.T) {
	home := root(t)
	batchOK(t, home, `{"folders": [{"path": "/parked"}, {"path": "/open"}, {"path": "/gated"}, {"path": "/empty"}],
	  "tasks": [
	    {"title": "held", "folder": "/parked", "tags": ["waiting"]},
	    {"title": "free", "folder": "/open"},
	    {"title": "behind", "folder": "/gated", "tags": ["waiting"], "blocked_by": ["/gated.start"]},
	    {"title": "root, held", "tags": ["waiting", "db"]},
	    {"title": "root, db", "tags": ["db"]}]}`)
	p := newPicker(t, home).with("FAKE_FZF_EXIT=1")
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"--kinds", "task", "--tags-none", "waiting"}, []string{"@9", "@6"}},
		{[]string{"--kinds", "task", "--tags-none", "waiting,waiting"}, []string{"@9", "@6"}},
		{[]string{"--kinds", "task", "--tags-any", "db", "--tags-none", "waiting"}, []string{"@9"}},
		{[]string{"--kinds", "task", "--tags-all", "db", "--tags-none", "waiting"}, []string{"@9"}},
		{[]string{"--kinds", "folder", "--tags-none", "waiting"}, []string{"@2", "@3"}},
		{[]string{"--kinds", "gate", "--tags-none", "waiting"}, []string{"@2.start"}},
		{[]string{"--tags-none", "waiting"}, []string{"@9", "@2", "@2.start", "@3", "@6"}},
		{[]string{"--frontier", "--kinds", "task,gate", "--tags-none", "waiting"}, []string{"@6", "@9", "@2.start"}},
		{[]string{"--kinds", "folder"}, []string{"@1", "@2", "@3", "@4"}},
	} {
		r := p.run(c.args...)
		if r.exit != 1 || r.out != "" || r.err != "" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", c.args, r.exit, r.out, r.err)
		}
		if got := p.keys(); !slices.Equal(got, c.want) {
			t.Errorf("%q: keys %v, want %v", c.args, got, c.want)
		}
	}
	p.run("--tags-any", "a", "--tags-none", "a").failed(t, 1, "invalid-input", `"a"`)
	p.run("--tags-all", "a", "--tags-none", "a").failed(t, 1, "invalid-input", `"a"`)
	p.run("--tags-none", "A").failed(t, 1, "invalid-input", `"A"`)
}
