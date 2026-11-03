package main_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/model"
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
	slices.SortFunc(f, func(a, b string) int {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return x - y
	})
	return strings.Join(f, " ")
}

// task list filters complete tasks as it does open ones.
func TestTagsNoneComplete(t *testing.T) {
	home := tagsNoneTree(t)
	ok(t, home, "complete", "3,5")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--state", "complete"}, "3 5"},
		{[]string{"--state", "complete", "--tags-none", "waiting"}, "5"},
		{[]string{"--state", "open,complete", "--tags-none", "waiting"}, "4 5 6 7 9"},
	} {
		r := ok(t, home, append([]string{"task", "list", "--fields", "id"}, c.args...)...)
		if got := sorted(idsOf(r)); got != c.want {
			t.Errorf("%q: got %q, want %q", c.args, got, c.want)
		}
	}
}

// The binary and the reference model agree on the tag filters, on a tree
// where most tasks are tagged: what frontier gives, what claim-next
// claims, and what is refused, including bad tags and tags given twice.
func TestTagsNoneAgainstModel(t *testing.T) {
	home := root(t)
	m := model.New(1, func() string { return "2026-10-20T10:00:00Z" })
	rng := rand.New(rand.NewSource(1))
	tags := []string{"a", "b", "c"}
	subset := func(extra ...string) []string {
		out := []string{}
		for _, tag := range append(slices.Clone(tags), extra...) {
			if rng.Intn(3) == 0 {
				out = append(out, tag)
			}
		}
		if len(out) > 1 && rng.Intn(4) == 0 {
			out = append(out, out[0])
		}
		return out
	}
	for i := range 40 {
		x := subset()
		if _, err := m.CreateTask(model.TaskInput{Title: "t", Tags: x}); err != nil {
			t.Fatal(err)
		}
		args := []string{"task", "create", fmt.Sprint("t", i)}
		if len(x) > 0 {
			args = append(args, "--tags", strings.Join(x, ","))
		}
		ok(t, home, args...)
	}
	problems := func(err error) string {
		e := err.(*core.Error)
		p := slices.Clone(e.Problems)
		slices.Sort(p)
		return strings.Join(p, " ")
	}
	var refused, left int
	for n := range 150 {
		sc := model.Scope{TagsAny: subset("Bad"), TagsAll: subset("Bad"), TagsNone: subset("Bad")}
		var args []string
		for _, f := range []struct {
			flag string
			list []string
		}{{"--tags-any", sc.TagsAny}, {"--tags-all", sc.TagsAll}, {"--tags-none", sc.TagsNone}} {
			if len(f.list) > 0 {
				args = append(args, f.flag, strings.Join(f.list, ","))
			}
		}
		// Every third, claim the choice, so that the frontier moves.
		claiming := n%3 == 0
		var want string
		var err error
		r := koan(t, home, append([]string{"frontier", "--fields", "id", "--limit", "null"}, args...)...)
		if claiming {
			var c *model.Claim
			if c, err = m.ClaimNext(sc, nil); c != nil {
				want = fmt.Sprint(c.Task)
			}
			r = koan(t, home, append([]string{"claim-next"}, args...)...)
		} else {
			var ids []int64
			ids, err = m.ScopedFrontier(sc)
			want = strings.Trim(fmt.Sprint(ids), "[]")
		}
		if err != nil {
			refused++
			if r.OK() {
				t.Fatalf("%q: the model refused at %s, koan gave %s", args, problems(err), r.Line())
			}
			got := strings.Fields(problemFields(r))
			slices.Sort(got)
			if strings.Join(got, " ") != problems(err) {
				t.Errorf("%q: the model refused at %s, koan %v", args, problems(err), got)
			}
			continue
		}
		if !r.OK() {
			t.Fatalf("%q: the model gave %q, koan %s", args, want, r.Line())
		}
		var got string
		if claiming {
			if task := r.Result()["task"]; task != nil {
				got = fmt.Sprint(task.(map[string]any)["id"])
			}
		} else {
			got = idsOf(r)
		}
		if got != want {
			t.Errorf("%q: the model gave %q, koan %q", args, want, got)
		}
		left++
	}
	if refused == 0 || left == 0 {
		t.Errorf("%d refused, %d answered: the filters drawn don't test both", refused, left)
	}
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
			{[]string{"--tags-any", "a", "--tags-none", "A,a"}, "/tags_none/0 /tags_none/1"},
			// Refused even though a task holding only b would pass.
			{[]string{"--tags-any", "a,b", "--tags-none", "a"}, "/tags_none/0"},
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
	// Through -i, an empty list filters nothing: no task filter at all.
	in := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(in, []byte(`{"kinds":["folder"],"tags_none":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := p.run("-i", in); r.exit != 1 || r.out != "" || r.err != "" {
		t.Errorf("empty: exit %d, stdout %q, stderr %q", r.exit, r.out, r.err)
	}
	if got, want := p.keys(), []string{"@1", "@2", "@3", "@4"}; !slices.Equal(got, want) {
		t.Errorf("empty: keys %v, want %v", got, want)
	}
	// A gate given a tag is filtered as any task is.
	ok(t, home, "update", "/gated.start", "--tags-add", "waiting")
	if r := p.run("--kinds", "gate", "--tags-none", "waiting"); r.exit != 1 {
		t.Errorf("a tagged gate: exit %d, stderr %q", r.exit, r.err)
	}
	if got := p.keys(); len(got) != 0 {
		t.Errorf("a tagged gate: keys %v", got)
	}
	// Complete tasks are filtered too.
	ok(t, home, "complete", "9")
	ok(t, home, "complete", "6")
	ok(t, home, "update", "6", "--tags-add", "waiting")
	if r := p.run("--kinds", "task", "--state", "complete", "--tags-none", "waiting"); r.exit != 1 {
		t.Errorf("complete: exit %d, stderr %q", r.exit, r.err)
	}
	if got, want := p.keys(), []string{"@9"}; !slices.Equal(got, want) {
		t.Errorf("complete: keys %v, want %v", got, want)
	}
	p.run("--tags-any", "a", "--tags-none", "a").failed(t, 1, "invalid-input", `"a" is both wanted and left out`)
	p.run("--tags-all", "a", "--tags-none", "a").failed(t, 1, "invalid-input", `"a" is both wanted and left out`)
	p.run("--tags-none", "A").failed(t, 1, "invalid-input", `"A"`)
}

// The skill's jq recipes, as it writes them, on data that makes them
// choose: an old claim and a new one, and extra.jira as an object, as a
// string and left out. A complete task's title isn't found, as it says.
func TestSkillRecipes(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the recipes need jq")
	}
	text, err := os.ReadFile(filepath.Join(testrun.Repo(), "claude", "skills", "koan", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	all, _ := exampleCommands(string(text))
	recipe := func(has string) string {
		t.Helper()
		for _, cmd := range all {
			if strings.Contains(cmd, "| jq") && strings.Contains(cmd, has) {
				return cmd
			}
		}
		t.Fatalf("the skill has no recipe with %s", has)
		return ""
	}
	home := root(t)
	batchOK(t, home, `{"tasks": [
	  {"title": "Write the MIGRATION", "extra": {"jira": {"project": "WEB"}}},
	  {"title": "other", "extra": {"jira": {"project": "API"}}},
	  {"title": "a string", "extra": {"jira": "WEB-12"}},
	  {"title": "old migration, done"}]}`)
	ok(t, home, "complete", "4")
	ok(t, home, "claim", "1,2")
	db(t, home, `UPDATE claim SET claimed_at = '2026-10-20T10:00:00Z' WHERE task = 1`,
		`UPDATE claim SET claimed_at = '2026-11-02T10:00:00Z' WHERE task = 2`)
	path := filepath.Dir(testrun.Binary(t)) + string(os.PathListSeparator) + os.Getenv("PATH")
	for _, c := range []struct{ has, want string }{
		{"test(", "1"},
		{"claimed_at", "1"},
		{"jira", "1"},
	} {
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+recipe(c.has))
		cmd.Env = append(exampleEnv(home), "PATH="+path)
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("%s: %v", c.has, err)
			continue
		}
		r := &testrun.Result{Stdout: string(out)}
		if err := decodeNumbers(string(out), &r.Envelope); err != nil {
			t.Errorf("%s: %v: %s", c.has, err, out)
			continue
		}
		if !r.OK() || idsOf(r) != c.want {
			t.Errorf("%s: want %s, got %s", c.has, c.want, out)
		}
	}
}
