package main_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/testrun"
)

// mkFolder inserts a folder's row, with its directory and empty notes on
// disk, for trees no command in this slice can make with these IDs.
func mkFolder(t *testing.T, home string, id, parent int64, path string) {
	t.Helper()
	p := "NULL"
	if parent != 0 {
		p = fmt.Sprint(parent)
	}
	db(t, home,
		fmt.Sprintf("INSERT INTO issued (id, kind, state) VALUES (%d, 'folder', 'live')", id),
		fmt.Sprintf("INSERT INTO folder (id, parent, name, extra) VALUES (%d, %s, '%s', '{}')", id, p, filepath.Base(path)))
	dir := filepath.Join(home, "koan-root", "tree", strings.ReplaceAll(path, "/", "/@"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "@notes.md"), nil, 0o644)
}

// example is the rules doc's Readiness example, with its IDs and edges,
// every task open. The doc leaves the gates' IDs to the test: they are
// 11 and 12 for the first phase's start and done, 13 and 14 for the
// second's, and 15 for the top folder's done gate.
func example(t *testing.T) string {
	t.Helper()
	home := root(t)
	mkFolder(t, home, 1, 0, "/work")
	mkFolder(t, home, 7, 1, "/work/phase1")
	mkFolder(t, home, 9, 1, "/work/phase2")
	var stmts []string
	for _, x := range []struct {
		id, folder int64
		gate       string
	}{{4, 1, ""}, {8, 7, ""}, {10, 9, ""}, {11, 7, "start"}, {12, 7, "done"}, {13, 9, "start"}, {14, 9, "done"}, {15, 1, "done"}} {
		stmts = append(stmts, task(x.id, x.folder, x.gate, false)...)
		dir := map[int64]string{1: "@work", 7: "@work/@phase1", 9: "@work/@phase2"}[x.folder]
		name := map[string]string{"": fmt.Sprintf("@%d.md", x.id), "start": "@start.md", "done": "@done.md"}[x.gate]
		os.WriteFile(filepath.Join(home, "koan-root", "tree", dir, name), nil, 0o644)
	}
	// Each edge is the task that waits, then what it waits on.
	stmts = append(stmts, "INSERT INTO edge (task, blocker) VALUES (8, 11), (12, 8), (10, 13), (14, 10), (13, 12), (15, 12), (4, 12)")
	db(t, home, stmts...)
	return home
}

// problemFields is an error's problems' fields, space-separated.
func problemFields(r *testrun.Result) string {
	var out []string
	for _, p := range r.Error()["problems"].([]any) {
		out = append(out, p.(map[string]any)["field"].(string))
	}
	return strings.Join(out, " ")
}

// warned runs a command that must succeed with warnings of these kinds,
// in order, and checks standard error counts them.
func warned(t *testing.T, home string, kinds []string, args ...string) *testrun.Result {
	t.Helper()
	r := koan(t, home, args...)
	var got []string
	for _, w := range r.Envelope["warnings"].([]any) {
		got = append(got, w.(map[string]any)["kind"].(string))
	}
	want := fmt.Sprintf("koan: %d warnings\n", len(kinds))
	if len(kinds) == 1 {
		want = "koan: 1 warning\n"
	}
	if r.Exit != 0 || !r.OK() || !slices.Equal(got, kinds) || r.Stderr != want {
		t.Fatalf("koan %q: exit %d, stderr %q, want %v: %s", args, r.Exit, r.Stderr, kinds, r.Line())
	}
	return r
}

// The rules doc's changes in its example: three refused as cycles, each
// cycle in order from the task whose blocked_by gains the closing edge,
// and one allowed.
func TestTaskBlockExamples(t *testing.T) {
	home := example(t)
	r := fails(t, home, "conflict", "cycle", "task", "block", "8", "--blocked-by", "10")
	golden(t, r, "task-block-cycle")
	for _, c := range []struct {
		args []string
		ids  string
	}{
		{[]string{"8", "--blocked-by", "10"}, "[8 10 13 12]"},
		{[]string{"@7.start", "--blocked-by", "@9"}, "[11 14 10 13 12 8]"},
		{[]string{"8", "--blocked-by", "@7"}, "[8 12]"},
	} {
		r := fails(t, home, "conflict", "cycle", append([]string{"task", "block"}, c.args...)...)
		if fmt.Sprint(r.Error()["ids"]) != c.ids || problemFields(r) != "/blocked_by/0" {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	if fmt.Sprint(field(t, home, "8", "blocked_by")) != "[map[id:11 ref:@11 (/work/phase1.start) state:open]]" {
		t.Errorf("a refused block wrote an edge")
	}
	r = ok(t, home, "task", "block", "4", "--blocked-by", "@1")
	if fmt.Sprint(r.Result()) != "map[changed:true gates_made:[] id:4 reblocked:[]]" {
		t.Errorf("allowed: %s", r.Line())
	}
	// Only the first phase's start gate waits on nothing; the rest wait
	// on it, at some depth.
	r = ok(t, home, "task", "list", "--readiness", "ready", "--fields", "id")
	if fmt.Sprint(r.Result()["tasks"]) != "[map[id:11]]" {
		t.Errorf("ready: %s", r.Line())
	}
}

// task block adds edges from either end, and says whether it changed
// anything: an edge already there changes nothing and leaves updated_at
// alone. A task it makes wait that was ready is reblocked.
func TestTaskBlock(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	ok(t, home, "task", "create", "a")           // 3
	ok(t, home, "task", "create", "b", "--folder", "/work")
	db(t, home, "UPDATE task SET updated_at = '2026-10-20T10:00:00Z'")
	r := ok(t, home, "task", "block", "3", "--blocked-by", "4")
	golden(t, r, "task-block")
	if field(t, home, "3", "updated_at") == "2026-10-20T10:00:00Z" || field(t, home, "4", "updated_at") != "2026-10-20T10:00:00Z" {
		t.Errorf("updated_at moves on the task whose blocked_by gains the edge, and only there")
	}
	db(t, home, "UPDATE task SET updated_at = '2026-10-20T10:00:00Z'")
	r = ok(t, home, "task", "block", "3", "--blocked-by", "@4,4")
	if fmt.Sprint(r.Result()) != "map[changed:false gates_made:[] id:3 reblocked:[]]" || field(t, home, "3", "updated_at") != "2026-10-20T10:00:00Z" {
		t.Errorf("again: %s", r.Line())
	}
	// From the other end: 4 waits on 3 would close a cycle; through
	// --input, with IDs as integers, 4 blocks a new task's wait.
	r = fails(t, home, "conflict", "cycle", "task", "block", "3", "--blocks", "4")
	if fmt.Sprint(r.Error()["ids"]) != "[4 3]" || problemFields(r) != "/blocks/0" {
		t.Errorf("cycle from blocks: %s", r.Line())
	}
	ok(t, home, "task", "create", "c") // 5
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"task":4,"blocks":[5]}`)}.Koan(t, "task", "block", "-i", "-")
	r.Check(t, "task block")
	if fmt.Sprint(r.Result()) != "map[changed:true gates_made:[] id:4 reblocked:[5]]" {
		t.Errorf("--input: %s", r.Line())
	}
	if fmt.Sprint(field(t, home, "4", "blocks")) != "[map[id:3 ref:@3 (/) state:open] map[id:5 ref:@5 (/) state:open]]" {
		t.Errorf("blocks: %v", field(t, home, "4", "blocks"))
	}
}

// task block makes each gate it names that its folder lacks, its subject
// included, whose ID comes first, then the rest in the order the edges
// are given, blocked_by before blocks; each with an empty @start.md or
// @done.md, keeping one made by hand. A gate named twice is made once.
func TestTaskBlockGates(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	ok(t, home, "task", "create", "a")           // 3
	r := ok(t, home, "task", "block", "/work/api.start", "--blocks", "3", "--blocked-by", "/work.start,@1.start")
	golden(t, r, "task-block-gates")
	rec := ok(t, home, "task", "show", "4").Result()["task"].(map[string]any)
	if rec["gate"] != "start" || rec["title"] != "Start" || rec["priority"] != nil || fmt.Sprint(rec["tags"]) != "[]" ||
		fmt.Sprint(rec["extra"]) != "map[]" || rec["readiness"] != "blocked" || rec["ref"] != "@4 (/work/api.start)" {
		t.Errorf("made gate: %v", rec)
	}
	if field(t, home, "5", "readiness") != "ready" || field(t, home, "5", "title") != "Start" {
		t.Errorf("a made gate that waits on nothing is ready")
	}

	// The done gate by the folder alone, its notes made by hand first.
	os.WriteFile(filepath.Join(home, "koan-root", "tree", "@work", "@done.md"), []byte("by hand"), 0o644)
	ok(t, home, "folder", "create", "/web") // 6
	r = ok(t, home, "task", "block", "3", "--blocks", "/web.start,/work", "--blocked-by", "/web")
	ids := func(r *testrun.Result) string {
		var out []string
		for _, g := range r.Result()["gates_made"].([]any) {
			g := g.(map[string]any)
			out = append(out, fmt.Sprintf("%v %v.%v", g["id"], g["folder"].(map[string]any)["path"], g["end"]))
		}
		return strings.Join(out, ", ")
	}
	if got := ids(r); got != "7 /web.done, 8 /web.start, 9 /work.done" {
		t.Errorf("order: %s", got)
	}
	checkTree(t, home, "@3.md=", "@work/", "@work/@notes.md=", "@work/@start.md=", "@work/@done.md=by hand",
		"@work/@api/", "@work/@api/@notes.md=", "@work/@api/@start.md=", "@web/", "@web/@notes.md=", "@web/@start.md=", "@web/@done.md=")
	if field(t, home, "9", "notes_path") != filepath.Join(home, "koan-root", "tree", "@work", "@done.md") {
		t.Errorf("hand-made notes are the gate's")
	}
	// Naming a gate that is there makes nothing.
	r = ok(t, home, "task", "block", "/web.start", "--blocked-by", "@2.start")
	if ids(r) != "" || r.Result()["changed"] != true {
		t.Errorf("no gate to make: %s", r.Line())
	}
}

// What a block needs: a task, at least one edge, edges of the right
// shape, and paths that don't differ only in case; every fault named,
// before the root is looked at. An edge to itself is invalid-input at
// that edge, once the references resolve.
func TestTaskBlockInvalid(t *testing.T) {
	home := t.TempDir()
	golden(t, fails(t, home, "invalid-input", "", "task", "block", "1"), "task-block-no-edge")
	for _, c := range []struct {
		args   []string
		fields string
	}{
		{[]string{"/w", "--blocked-by", "x"}, "/task /blocked_by/0"},
		{[]string{"1", "--blocked-by", "/"}, "/blocked_by/0"},
		{[]string{"1", "--blocks", "@2:x,$a"}, "/blocks/0 /blocks/1"},
		{[]string{"/w.start", "--blocked-by", "/W"}, "/task /blocked_by/0"},
		{[]string{"1", "--blocked-by", "/a/b", "--blocks", "/A.start"}, "/blocked_by/0 /blocks/0"},
		{[]string{"/", "--blocked-by", "1"}, "/task"},
		{[]string{"/w:x"}, "/task "},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"task", "block"}, c.args...)...)
		if problemFields(r) != c.fields {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	for _, cmd := range []string{"task block", "task unblock"} {
		for in, fields := range map[string]string{
			`{"task":1}`:                               "",
			`{"task":1,"blocked_by":[],"blocks":[]}`:   "",
			`{"task":1,"blocked_by":"2"}`:              "/blocked_by",
			`{"blocks":[2]}`:                           "/task",
			`{"task":1,"blocks":[2],"blocked_by":[0]}`: "/blocked_by/0",
		} {
			r := testrun.Run{Home: home, Stdin: strings.NewReader(in)}.Koan(t, append(strings.Fields(cmd), "-i", "-")...)
			r.Check(t, cmd)
			if r.Kind() != "invalid-input" || !strings.HasSuffix(problemFields(r), fields) {
				t.Errorf("%s %s: %s", cmd, in, r.Line())
			}
		}
	}
	fails(t, home, "not-initialized", "", "task", "block", "1", "--blocked-by", "2")

	home = root(t)
	ok(t, home, "folder", "create", "/w") // 1
	ok(t, home, "task", "create", "a")    // 2
	ok(t, home, "task", "create", "b")    // 3
	golden(t, fails(t, home, "invalid-input", "", "task", "block", "2", "--blocked-by", "3,@2"), "task-block-itself")
	// Its own gate, made and then refused: nothing is made.
	r := fails(t, home, "invalid-input", "", "task", "block", "/w.start", "--blocks", "@1.start")
	if problemFields(r) != "/blocks/0" {
		t.Errorf("itself through a gate: %s", r.Line())
	}
	// Resolution's invalid-input points at the field.
	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"@2.start", "--blocked-by", "3"}, "/task"},
		{[]string{"2", "--blocked-by", "1,@3/x"}, "/blocked_by/1"},
		{[]string{"2", "--blocks", "@3.done"}, "/blocks/0"},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"task", "block"}, c.args...)...)
		if problemFields(r) != c.field {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	checkTree(t, home, "@w/", "@w/@notes.md=", "@2.md=", "@3.md=")
	if r := ok(t, home, "task", "create", "c"); r.Result()["id"] != json.Number("4") {
		t.Errorf("a refused block issued an ID: %s", r.Line())
	}
}

// A reference that names nothing is not-found, one matching only
// ignoring case is case-mismatch, and nothing is written.
func TestTaskBlockRefused(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/Work") // 1
	ok(t, home, "task", "create", "a")       // 2
	golden(t, fails(t, home, "not-found", "", "task", "block", "2", "--blocked-by", "99"), "task-block-not-found")
	fails(t, home, "not-found", "", "task", "block", "99", "--blocked-by", "2")
	fails(t, home, "not-found", "", "task", "block", "@1", "--blocked-by", "2")
	fails(t, home, "not-found", "", "task", "block", "2", "--blocks", "/Work/api")
	golden(t, fails(t, home, "conflict", "case-mismatch", "task", "block", "2", "--blocked-by", "/work.start"), "task-block-case-mismatch")
	fails(t, home, "conflict", "case-mismatch", "task", "block", "/WORK.done", "--blocked-by", "2")
	// A later edge's refusal undoes the gate an earlier one made.
	fails(t, home, "not-found", "", "task", "block", "2", "--blocked-by", "/Work.start,98")
	checkTree(t, home, "@Work/", "@Work/@notes.md=", "@2.md=")
	if r := ok(t, home, "task", "create", "b"); r.Result()["id"] != json.Number("3") {
		t.Errorf("a refused block issued an ID: %s", r.Line())
	}
}

// An edge added to a complete task's blocked_by warns complete-blocker;
// a claimed task that was ready and waits now warns claimed-blocked, and
// is reblocked with its claim kept.
func TestTaskBlockWarnings(t *testing.T) {
	home := root(t)
	for range 6 {
		ok(t, home, "task", "create", "t") // 1 to 6
	}
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id IN (1, 2)",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (3, '2026-10-20T10:00:00Z', '{}'), (4, '2026-10-20T10:00:00Z', '{}'), (6, '2026-10-20T10:00:00Z', '{}')`)
	golden(t, warned(t, home, []string{"complete-blocker"}, "task", "block", "1", "--blocked-by", "6"), "task-block-complete-blocker")
	golden(t, warned(t, home, []string{"claimed-blocked"}, "task", "block", "3", "--blocked-by", "5"), "task-block-claimed-blocked")
	if field(t, home, "3", "claim") == nil || field(t, home, "1", "state") != "complete" {
		t.Errorf("the claim stays, and nothing reopens")
	}
	// Several of each, in the plural; an edge already there adds
	// nothing.
	r := warned(t, home, []string{"complete-blocker", "claimed-blocked"}, "task", "block", "5", "--blocks", "2,4,6,3,1")
	golden(t, r, "task-block-warnings")
	// A claimed task already waiting isn't made to wait again.
	ok(t, home, "task", "create", "t") // 7
	r = ok(t, home, "task", "block", "4", "--blocked-by", "7")
	if fmt.Sprint(r.Result()["reblocked"]) != "[]" {
		t.Errorf("already blocked: %s", r.Line())
	}
}

// Once COMMIT succeeds the block succeeds: a gate's notes the operating
// system refuses leave the gate made, with pending-step, and the next
// write writes them.
func TestTaskBlockStepRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := root(t)
	ok(t, home, "folder", "create", "/work") // 1
	ok(t, home, "task", "create", "t")       // 2
	dir := filepath.Join(home, "koan-root", "tree", "@work")
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	golden(t, warned(t, home, []string{"pending-step"}, "task", "block", "2", "--blocked-by", "/work.start"), "task-block-pending-step")
	os.Chmod(dir, 0o755)
	ok(t, home, "task", "create", "u")
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@start.md=", "@2.md=", "@4.md=")
}

// task unblock removes edges from either end and reports the open tasks
// it made ready; an edge that isn't there, to itself included, changes
// nothing. It never makes a gate: naming one the folder lacks is
// not-found.
func TestTaskUnblock(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	for range 3 {
		ok(t, home, "task", "create", "t") // 2, 3, 4
	}
	ok(t, home, "task", "block", "2", "--blocked-by", "4,/w.start") // 5
	ok(t, home, "task", "block", "2", "--blocks", "3")
	db(t, home, "UPDATE task SET updated_at = '2026-10-20T10:00:00Z'")
	r := ok(t, home, "task", "unblock", "2", "--blocked-by", "4,@1.start", "--blocks", "3")
	if fmt.Sprint(r.Result()) != "map[changed:true id:2 released:[2 3]]" || field(t, home, "3", "updated_at") == "2026-10-20T10:00:00Z" {
		t.Errorf("unblock: %s", r.Line())
	}
	db(t, home, "UPDATE task SET updated_at = '2026-10-20T10:00:00Z'")
	r = ok(t, home, "task", "unblock", "2", "--blocked-by", "4,2", "--blocks", "2")
	if fmt.Sprint(r.Result()) != "map[changed:false id:2 released:[]]" || field(t, home, "2", "updated_at") != "2026-10-20T10:00:00Z" {
		t.Errorf("nothing to remove: %s", r.Line())
	}
	ok(t, home, "task", "block", "2", "--blocked-by", "3")
	r = ok(t, home, "task", "unblock", "@2", "--blocked-by", "3")
	golden(t, r, "task-unblock")
	golden(t, fails(t, home, "not-found", "", "task", "unblock", "2", "--blocks", "/w"), "task-unblock-no-gate")
	golden(t, fails(t, home, "not-found", "", "task", "unblock", "/w.done", "--blocks", "2"), "task-unblock-no-subject-gate")
	fails(t, home, "conflict", "case-mismatch", "task", "unblock", "2", "--blocks", "/W.start")
	checkTree(t, home, "@w/", "@w/@notes.md=", "@w/@start.md=", "@2.md=", "@3.md=", "@4.md=")
}

// task create writes its edges in the same change, making any gate they
// name after the task's own ID, blocked_by before blocks; a ready task it
// makes wait through --blocks is reblocked.
func TestTaskCreateEdges(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work") // 1
	ok(t, home, "task", "create", "a")       // 2
	r := ok(t, home, "task", "create", "b", "--folder", "/work", "--blocks", "@1", "--blocked-by", "/work.start")
	golden(t, r, "task-create-edges")
	r = ok(t, home, "task", "create", "c", "--folder", "/work", "--blocked-by", "/work.start", "--blocks", "/work,2")
	if fmt.Sprint(r.Result()) != "map[gates_made:[] id:6 notes_path:"+filepath.Join(home, "koan-root", "tree", "@work", "@6.md")+" reblocked:[2] ref:@6 (/work)]" {
		t.Errorf("reblocked: %s", r.Line())
	}
	if fmt.Sprint(field(t, home, "6", "blocked_by")) != "[map[id:4 ref:@4 (/work.start) state:open]]" ||
		fmt.Sprint(field(t, home, "5", "blocked_by")) != "[map[id:3 ref:@3 (/work) state:open] map[id:6 ref:@6 (/work) state:open]]" {
		t.Errorf("edges")
	}
	checkTree(t, home, "@2.md=", "@work/", "@work/@notes.md=", "@work/@3.md=", "@work/@start.md=", "@work/@done.md=", "@work/@6.md=")
	r = fails(t, home, "conflict", "cycle", "task", "create", "d", "--blocked-by", "2", "--blocks", "6")
	golden(t, r, "task-create-cycle")
	// The next ID would be the task's own.
	if r := fails(t, home, "invalid-input", "", "task", "create", "d", "--blocked-by", "7"); problemFields(r) != "/blocked_by/0" {
		t.Errorf("its own ID: %s", r.Line())
	}
	fails(t, home, "not-found", "", "task", "create", "d", "--blocked-by", "/web.start")
	if r := ok(t, home, "task", "create", "d"); r.Result()["id"] != json.Number("7") {
		t.Errorf("a refused create issued an ID: %s", r.Line())
	}
}

// task create warns as block does, through --blocks.
func TestTaskCreateWarnings(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "t") // 1
	ok(t, home, "task", "create", "t") // 2
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id = 1",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (2, '2026-10-20T10:00:00Z', '{}')`)
	golden(t, warned(t, home, []string{"complete-blocker"}, "task", "create", "x", "--blocks", "1"), "task-create-complete-blocker")
	golden(t, warned(t, home, []string{"claimed-blocked"}, "task", "create", "x", "--blocks", "2"), "task-create-claimed-blocked")
}

// task why gives a task's open blockers, and the ready tasks it waits on
// through open blockers at any depth, in work order; a complete task
// holds nothing back, so the walk never passes one.
func TestTaskWhy(t *testing.T) {
	home := example(t)
	golden(t, ok(t, home, "task", "why", "10"), "task-why")
	r := ok(t, home, "task", "why", "/work/phase1.start")
	golden(t, r, "task-why-ready")

	home = root(t)
	for _, p := range []string{"", "", "", "", "3", "7"} {
		args := []string{"task", "create", "t"}
		if p != "" {
			args = append(args, "--priority", p)
		}
		ok(t, home, args...) // 1 to 6
	}
	// 1 waits on 2 and 3; 2 on 4, complete, which waits on 5; 3 on 6,
	// claimed.
	ok(t, home, "task", "block", "1", "--blocked-by", "2,3")
	ok(t, home, "task", "block", "2", "--blocked-by", "4")
	ok(t, home, "task", "block", "4", "--blocked-by", "5")
	ok(t, home, "task", "block", "3", "--blocked-by", "6")
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id = 4",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (6, '2026-10-20T10:00:00Z', '{}')`)
	r = ok(t, home, "task", "why", "1")
	if fmt.Sprint(r.Result()) != "map[blockers:[map[id:2 ref:@2 (/) state:open] map[id:3 ref:@3 (/) state:open]] id:1 readiness:blocked ready:[6 2]]" {
		t.Errorf("why 1: %s", r.Line())
	}
	r = ok(t, home, "task", "why", "4")
	if fmt.Sprint(r.Result()) != "map[blockers:[] id:4 readiness:<nil> ready:[]]" {
		t.Errorf("complete: %s", r.Line())
	}
	fails(t, home, "not-found", "", "task", "why", "99")
	if r := fails(t, home, "invalid-input", "", "task", "why", "/"); problemFields(r) != "/task" {
		t.Errorf("root: %s", r.Line())
	}
}

// task frontier gives the ready tasks with no claim, gates included, in
// work order, under --folder; the folder chooses where to look, never
// what is ready.
func TestTaskFrontier(t *testing.T) {
	home := root(t)
	for _, p := range []string{"/a/b", "/c"} {
		ok(t, home, "folder", "create", p) // 1, 2, 3
	}
	for _, c := range [][]string{
		{"r1"}, // 4
		{"a1", "--folder", "/a", "--priority", "2"},                  // 5
		{"b1", "--folder", "/a/b", "--priority", "5", "--tags", "x"}, // 6
		{"c1", "--folder", "/c", "--priority", "2"},                  // 7
		{"r2", "--priority", "-1", "--tags", "x,y"},                  // 8
		{"a2", "--folder", "/a"},                                     // 9
		{"a3", "--folder", "/a", "--blocked-by", "4"},                // 10
		{"r3"}, // 11
	} {
		ok(t, home, append([]string{"task", "create"}, c...)...)
	}
	ok(t, home, "task", "block", "7", "--blocked-by", "/c.start") // 12
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id = 11",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (9, '2026-10-20T10:00:00Z', '{}')`)
	ids := func(r *testrun.Result) string {
		var out []string
		for _, x := range r.Result()["tasks"].([]any) {
			out = append(out, fmt.Sprint(x.(map[string]any)["id"]))
		}
		return strings.Join(out, " ")
	}
	golden(t, ok(t, home, "task", "frontier", "--fields", "title,priority,folder,readiness"), "task-frontier")
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "6 5 8 4 12"},
		{[]string{"--folder", "/a"}, "6 5"},
		{[]string{"--folder", "@1", "--recursive=false"}, "5"},
		{[]string{"--folder", "/c"}, "12"},
		{[]string{"--folder", "/", "--recursive=false"}, "8 4"},
		{[]string{"--tags-any", "x,q"}, "6 8"},
		{[]string{"--tags-all", "x,y"}, "8"},
		{[]string{"--tags-any", "x", "--tags-all", "y"}, "8"},
	} {
		r := ok(t, home, append([]string{"task", "frontier", "--fields", "id"}, c.args...)...)
		if got := ids(r); got != c.want || r.Result()["total"] != json.Number(fmt.Sprint(len(strings.Fields(c.want)))) {
			t.Errorf("%q: got %q, want %q: %s", c.args, got, c.want, r.Line())
		}
	}
	r := ok(t, home, "task", "frontier", "--limit", "2", "--fields", "id")
	if ids(r) != "6 5" || r.Result()["total"] != json.Number("5") || r.Result()["truncated"] != true {
		t.Errorf("--limit: %s", r.Line())
	}
	if r := ok(t, home, "task", "frontier", "--limit", "null"); len(r.Result()["tasks"].([]any)[0].(map[string]any)) != len(taskFieldNames) {
		t.Errorf("every field: %s", r.Line())
	}
	// Completing 4 lets 10 in.
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id = 4")
	if got := ids(ok(t, home, "task", "frontier", "--fields", "id")); got != "6 5 8 10 12" {
		t.Errorf("after complete: %s", got)
	}
	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"--limit", "0"}, "/limit"},
		{[]string{"--fields", "nope"}, "/fields/0"},
		{[]string{"--folder", "/a.start"}, "/folder"},
		{[]string{"--tags-any", "A"}, "/tags_any/0"},
	} {
		if r := fails(t, home, "invalid-input", "", append([]string{"task", "frontier"}, c.args...)...); problemFields(r) != c.field {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	fails(t, home, "not-found", "", "task", "frontier", "--folder", "/z")
	fails(t, home, "conflict", "case-mismatch", "task", "frontier", "--folder", "/A")
}

// The model's create, block, unblock, complete, reopen, claim, claim-next,
// release, move, delete and archive, of tasks and of folders, and batch, run beside koan on
// the same inputs, agree on every result and refusal, and on readiness,
// edges, claims, the frontier and the folders after each.
func TestEdgesModel(t *testing.T) {
	// The first seed alone makes every command succeed, for -short.
	seeds := []int64{11, 1, 2}
	if testing.Short() {
		seeds = seeds[:1]
	}
	// Every command must succeed somewhere in the sequences run.
	succeeded := map[string]int{}
	for _, seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { edgesModel(t, seed, succeeded) })
	}
	for _, cmd := range []string{"task create", "task block", "task unblock", "task complete", "task reopen", "task claim",
		"task claim-next", "task release", "task move", "task delete", "task archive", "folder create", "folder move",
		"folder delete", "folder archive", "batch"} {
		if succeeded[cmd] == 0 {
			t.Errorf("no %s succeeded", cmd)
		}
	}
}

func edgesModel(t *testing.T, seed int64, succeeded map[string]int) {
	rng := rand.New(rand.NewSource(seed))
	// Batches draw from a source of their own, so that the other changes
	// draw as they would without them.
	brng := rand.New(rand.NewSource(seed))
	home := root(t)
	m := model.New(1, func() string { return "2026-10-20T10:00:00Z" })
	for _, p := range []string{"/a/b", "/c"} {
		if _, err := m.CreateFolder(p, nil, nil); err != nil {
			t.Fatal(err)
		}
		ok(t, home, "folder", "create", p)
	}
	taskIDs := func() []int64 {
		var out []int64
		for id := range m.Tasks {
			out = append(out, id)
		}
		slices.Sort(out)
		return out
	}
	pick := func(pool []string) string { return pool[rng.Intn(len(pool))] }
	edgeRef := func() string {
		if ids := taskIDs(); len(ids) > 0 && rng.Intn(3) > 0 {
			return fmt.Sprint(ids[rng.Intn(len(ids))])
		}
		if rng.Intn(5) == 0 {
			return pick([]string{"/A", "@99", "/a/x.start"})
		}
		return pick([]string{"/a", "/a.start", "/a/b.done", "@2.start", "@3", "/c.start", "@1/b"})
	}
	subject := func() string {
		if ids := taskIDs(); len(ids) > 0 && rng.Intn(3) > 0 {
			return fmt.Sprint(ids[rng.Intn(len(ids))])
		}
		if rng.Intn(4) == 0 {
			return pick([]string{"@1", "99", "/a/B.done"})
		}
		return pick([]string{"/a.start", "/c.done", "@2.start"})
	}
	edgeList := func() []string {
		var out []string
		for range rng.Intn(3) {
			out = append(out, edgeRef())
		}
		return out
	}
	edgeArgs := func(args []string, bb, bl []string) []string {
		if len(bb) > 0 {
			args = append(args, "--blocked-by", strings.Join(bb, ","))
		}
		if len(bl) > 0 {
			args = append(args, "--blocks", strings.Join(bl, ","))
		}
		return args
	}
	// sameErr reports whether koan refused as the model did.
	sameErr := func(r *testrun.Result, err error) bool {
		e := err.(*core.Error)
		got := r.Error()
		if got == nil {
			return false
		}
		rule, _ := got["rule"].(string)
		var problems []string
		for _, p := range got["problems"].([]any) {
			problems = append(problems, p.(map[string]any)["field"].(string))
		}
		var archived []any
		for _, a := range got["archived"].([]any) {
			archived = append(archived, a.(map[string]any)["id"])
		}
		return r.Kind() == e.Kind && rule == e.Rule && fmt.Sprint(got["ids"]) == fmt.Sprint(nonNil(e.IDs)) &&
			fmt.Sprint(archived) == fmt.Sprint(e.Archived) &&
			fmt.Sprint(got["paths"]) == fmt.Sprint(nonNilS(e.Paths)) && fmt.Sprint(problems) == fmt.Sprint(e.Problems)
	}
	gates := func(made []model.GateMade) string {
		var out []string
		for _, g := range made {
			out = append(out, fmt.Sprintf("%d %d %s %s", g.ID, g.Folder, g.Path, g.End))
		}
		return fmt.Sprint(out)
	}
	koanGates := func(r *testrun.Result) string {
		var out []string
		for _, g := range r.Result()["gates_made"].([]any) {
			g := g.(map[string]any)
			f := g["folder"].(map[string]any)
			out = append(out, fmt.Sprintf("%v %v %v %v", g["id"], f["id"], f["path"], g["end"]))
		}
		return fmt.Sprint(out)
	}
	madeAt := func(made []model.FolderAt) []string {
		out := []string{}
		for _, f := range made {
			out = append(out, fmt.Sprintf("%d %s", f.ID, f.Path))
		}
		return out
	}
	koanMade := func(v any) []string {
		out := []string{}
		for _, f := range v.([]any) {
			f := f.(map[string]any)
			out = append(out, fmt.Sprintf("%v %v", f["id"], f["path"]))
		}
		return out
	}
	// folderRef is mostly a folder there is, by path or ID; else one from
	// a pool that may name nothing, or the root.
	folderRef := func() string {
		var live []int64
		for _, id := range m.TreeOrder() {
			if _, ok := m.Folders[id]; ok {
				live = append(live, id)
			}
		}
		if len(live) > 0 && rng.Intn(3) > 0 {
			id := live[rng.Intn(len(live))]
			if rng.Intn(2) == 0 {
				return fmt.Sprint(id)
			}
			return m.Path(id)
		}
		return pick([]string{"/a", "/a/b", "/c", "/d", "@1", "@2", "/c/e", "/A", "/"})
	}
	folderPaths := []string{"/d", "/a/b/x", "/c/a", "/x/y", "/C", "/a/B", "/c", "/a", "/c/e", "/a/b"}
	warnings := func(ws []model.Warning) string {
		var out []string
		for _, w := range ws {
			out = append(out, fmt.Sprintf("%s %v", w.Kind, w.IDs))
		}
		return fmt.Sprint(out)
	}
	koanWarnings := func(r *testrun.Result) string {
		var out []string
		for _, w := range r.Envelope["warnings"].([]any) {
			w := w.(map[string]any)
			out = append(out, fmt.Sprintf("%s %v", w["kind"], w["ids"]))
		}
		return fmt.Sprint(out)
	}

	// batchRef is an edge-ref, or often a label of one of a batch's n
	// tasks, now and then one no part defines.
	batchRef := func(n int) string {
		switch rng.Intn(8) {
		case 0, 1, 2:
			return "$" + string(rune('a'+rng.Intn(n)))
		case 3:
			return "$z"
		}
		return edgeRef()
	}
	// batchIn is a batch file, as the model and as JSON.
	batchIn := func() (model.BatchInput, string) {
		var in model.BatchInput
		file := map[string]any{}
		var folders, tasks []any
		for range []int{0, 0, 1, 2}[rng.Intn(4)] {
			p := pick(folderPaths)
			in.Folders = append(in.Folders, model.BatchFolder{Path: p})
			folders = append(folders, map[string]any{"path": p})
		}
		if rng.Intn(3) == 0 {
			in.Folder = folderRef()
			file["folder"] = in.Folder
		}
		n := rng.Intn(4)
		if rng.Intn(5) > 0 {
			n = max(n, 1)
		}
		for range n {
			x := model.BatchTask{TaskInput: model.TaskInput{Title: "t"}}
			part := map[string]any{"title": "t"}
			// Each label once, now and then twice.
			if l := "$" + string(rune('a'+len(in.Tasks))); rng.Intn(4) > 0 {
				if rng.Intn(10) == 0 {
					l = "$a"
				}
				x.Label = l
				part["label"] = x.Label
			}
			if rng.Intn(3) == 0 {
				x.Folder = folderRef()
				if len(in.Folders) > 0 && rng.Intn(2) == 0 {
					x.Folder = in.Folders[0].Path
				}
				part["folder"] = x.Folder
			}
			for range rng.Intn(3) {
				x.BlockedBy = append(x.BlockedBy, batchRef(n))
			}
			for range rng.Intn(2) {
				x.Blocks = append(x.Blocks, batchRef(n))
			}
			if x.BlockedBy != nil {
				part["blocked_by"] = x.BlockedBy
			}
			if x.Blocks != nil {
				part["blocks"] = x.Blocks
			}
			in.Tasks = append(in.Tasks, x)
			tasks = append(tasks, part)
		}
		if folders != nil {
			file["folders"] = folders
		}
		if tasks != nil {
			file["tasks"] = tasks
		}
		b, _ := json.Marshal(file)
		return in, string(b)
	}

	var made, refused int
	for step := range 160 {
		var args []string
		var stdin string
		var err error
		var want string
		op := -1
		if brng.Intn(8) > 0 {
			op = rng.Intn(20)
		}
		switch {
		case op == -1:
			was := rng
			rng = brng
			in, file := batchIn()
			rng = was
			var out model.BatchResult
			out, err = m.Batch(in)
			args, stdin = []string{"batch", "-i", "-"}, file
			want = fmt.Sprintf("%v %v %v %s %v %s", madeAt(out.Folders), nonNil(out.Tasks), out.Labels,
				gates(out.GatesMade), nonNil(out.Reblocked), warnings(out.Warnings))
		case op < 3:
			folder := pick([]string{"", "/a", "/a/b", "/c"})
			bb, bl := edgeList(), edgeList()
			var out model.TaskCreated
			out, err = m.CreateTask(model.TaskInput{Title: "t", Folder: folder, BlockedBy: bb, Blocks: bl})
			args = []string{"task", "create", "t"}
			if folder != "" {
				args = append(args, "--folder", folder)
			}
			args = edgeArgs(args, bb, bl)
			want = fmt.Sprintf("%d %s %v %s", out.ID, gates(out.GatesMade), nonNil(out.Reblocked), warnings(out.Warnings))
		case op < 6:
			x, bb, bl := subject(), edgeList(), edgeList()
			var out model.EdgeResult
			out, err = m.Block(x, bb, bl)
			args = edgeArgs([]string{"task", "block", x}, bb, bl)
			want = fmt.Sprintf("%d %v %s %v %s", out.ID, out.Changed, gates(out.GatesMade), nonNil(out.Reblocked), warnings(out.Warnings))
		case op < 8:
			x, bb, bl := subject(), edgeList(), edgeList()
			var out model.EdgeResult
			out, err = m.Unblock(x, bb, bl)
			args = edgeArgs([]string{"task", "unblock", x}, bb, bl)
			want = fmt.Sprintf("%d %v %v", out.ID, out.Changed, nonNil(out.Released))
		case op == 13:
			x, to := subject(), folderRef()
			var changed bool
			var ws []model.Warning
			changed, ws, err = m.MoveTask(x, to)
			args = []string{"task", "move", x, "--to", to}
			want = fmt.Sprintf("%v %s", changed, warnings(ws))
		case op == 14:
			x := subject()
			var out model.Removed
			out, err = m.DeleteTask(x)
			args = []string{"task", "delete", x}
			want = fmt.Sprintf("%v %v", nonNil(out.Removed), nonNil(out.Released))
		case op >= 18:
			var left []int64
			if op == 18 {
				x := subject()
				left, err = m.ArchiveTask(x)
				args = []string{"task", "archive", x}
			} else {
				x := folderRef()
				left, err = m.ArchiveFolder(x)
				args = []string{"folder", "archive", x}
			}
			want = fmt.Sprint(nonNil(left))
		case op >= 15:
			// Creates come most often and deletes least, so the folders
			// the other parts name come back.
			switch x, to := folderRef(), pick(folderPaths); rng.Intn(8) {
			case 0, 1, 2, 3:
				var made []model.FolderAt
				made, err = m.CreateFolder(to, nil, nil)
				args = []string{"folder", "create", to}
				want = fmt.Sprint(madeAt(made))
			case 4, 5, 6:
				var out model.FolderMoved
				out, err = m.MoveFolder(x, to)
				args = []string{"folder", "move", x, "--to", to}
				want = fmt.Sprintf("%d %v %s %v %s", out.ID, out.Changed, out.Path, madeAt(out.Created), warnings(out.Warnings))
			default:
				contents := rng.Intn(3) == 0
				var out model.Removed
				out, err = m.DeleteFolder(x, contents, false)
				args = []string{"folder", "delete", x, fmt.Sprintf("--contents=%v", contents)}
				want = fmt.Sprintf("%v %v", nonNil(out.Removed), nonNil(out.Released))
			}
		default:
			x := subject()
			switch rng.Intn(5) {
			case 0:
				var out model.CompleteResult
				out, err = m.Complete(x)
				args = []string{"task", "complete", x}
				want = fmt.Sprintf("%d %v %v", out.ID, out.Changed, nonNil(out.Released))
			case 1:
				var out model.CompleteResult
				out, err = m.Reopen(x)
				args = []string{"task", "reopen", x}
				want = fmt.Sprintf("%d %v %v %s", out.ID, out.Changed, nonNil(out.Reblocked), warnings(out.Warnings))
			case 2:
				var c model.Claim
				c, err = m.ClaimTask(x, nil)
				args = []string{"task", "claim", x}
				want = fmt.Sprint(c.Task)
			case 3:
				var changed bool
				changed, err = m.Release(x)
				args = []string{"task", "release", x}
				want = fmt.Sprint(changed)
			default:
				sc := model.Scope{Folder: pick([]string{"/", "/a", "/a/b", "/c"}), Recursive: rng.Intn(2) == 0}
				var c *model.Claim
				c, err = m.ClaimNext(sc, nil)
				args = []string{"task", "claim-next", "--folder", sc.Folder, fmt.Sprintf("--recursive=%v", sc.Recursive)}
				want = "<nil>"
				if c != nil {
					want = fmt.Sprint(c.Task)
				}
			}
		}
		if args != nil {
			name := args[0] + " " + args[1]
			var r *testrun.Result
			if stdin != "" {
				name = args[0]
				r = input(t, home, name, stdin)
			} else {
				r = koan(t, home, args...)
			}
			if err != nil {
				refused++
			} else {
				made++
				succeeded[name]++
			}
			switch {
			case err != nil:
				if !sameErr(r, err) {
					t.Fatalf("step %d %q: model %v %v, koan %s", step, args, err, err.(*core.Error).Problems, r.Line())
				}
			case !r.OK():
				t.Fatalf("step %d %q: model %s, koan %s", step, args, want, r.Line())
			default:
				res := r.Result()
				var got string
				switch args[1] {
				case "-i":
					got = fmt.Sprintf("%v %v %v %s %v %s", koanMade(res["folders"]), res["tasks"], res["labels"],
						koanGates(r), res["reblocked"], koanWarnings(r))
				case "create":
					if args[0] == "folder" {
						got = fmt.Sprint(koanMade(res["created"]))
						break
					}
					got = fmt.Sprintf("%v %s %v %s", res["id"], koanGates(r), res["reblocked"], koanWarnings(r))
				case "block":
					got = fmt.Sprintf("%v %v %s %v %s", res["id"], res["changed"], koanGates(r), res["reblocked"], koanWarnings(r))
				case "unblock", "complete":
					got = fmt.Sprintf("%v %v %v", res["id"], res["changed"], res["released"])
				case "reopen":
					got = fmt.Sprintf("%v %v %v %s", res["id"], res["changed"], res["reblocked"], koanWarnings(r))
				case "claim":
					got = fmt.Sprint(res["claim"].(map[string]any)["task"])
				case "release":
					got = fmt.Sprint(res["changed"])
				case "claim-next":
					got = "<nil>"
					if c, _ := res["claim"].(map[string]any); c != nil {
						got = fmt.Sprint(c["task"])
					}
				case "move":
					if args[0] == "task" {
						got = fmt.Sprintf("%v %s", res["changed"], koanWarnings(r))
					} else {
						got = fmt.Sprintf("%v %v %v %v %s", res["id"], res["changed"], res["path"], koanMade(res["created"]), koanWarnings(r))
					}
				case "delete":
					got = fmt.Sprintf("%v %v", res["removed"], res["released"])
				case "archive":
					got = fmt.Sprint(res["left"])
				}
				if got != want {
					t.Fatalf("step %d %q: model %s, koan %s", step, args, want, got)
				}
			}
		}

		// Readiness and edges, task by task, then the frontier.
		var wantTasks, gotTasks []string
		for _, id := range m.TreeOrder() {
			x, isTask := m.Tasks[id]
			if !isTask {
				continue
			}
			readiness := "<nil>"
			if x.Open() {
				readiness = "blocked"
				if m.Ready(id) {
					readiness = "ready"
				}
			}
			var bb []int64
			for b := range x.BlockedBy {
				bb = append(bb, b)
			}
			slices.Sort(bb)
			_, claimed := m.Claims[id]
			wantTasks = append(wantTasks, fmt.Sprintf("%d %s %v %v", id, readiness, nonNil(bb), claimed))
		}
		for _, x := range ok(t, home, "task", "list", "--state", "open,complete", "--limit", "null", "--fields", "readiness,blocked_by,claim").Result()["tasks"].([]any) {
			x := x.(map[string]any)
			var bb []any
			for _, e := range x["blocked_by"].([]any) {
				bb = append(bb, e.(map[string]any)["id"])
			}
			if bb == nil {
				bb = []any{}
			}
			gotTasks = append(gotTasks, fmt.Sprintf("%v %v %v %v", x["id"], x["readiness"], bb, x["claim"] != nil))
		}
		if !slices.Equal(gotTasks, wantTasks) {
			t.Fatalf("step %d: tasks: koan %v, model %v", step, gotTasks, wantTasks)
		}
		var wantFolders, gotFolders []string
		for _, id := range m.TreeOrder() {
			if _, isFolder := m.Folders[id]; isFolder {
				wantFolders = append(wantFolders, fmt.Sprintf("%d %s", id, m.Path(id)))
			}
		}
		for _, f := range ok(t, home, "folder", "list", "--limit", "null", "--fields", "path").Result()["folders"].([]any) {
			f := f.(map[string]any)
			gotFolders = append(gotFolders, fmt.Sprintf("%v %v", f["id"], f["path"]))
		}
		if !slices.Equal(gotFolders, wantFolders) {
			t.Fatalf("step %d: folders: koan %v, model %v", step, gotFolders, wantFolders)
		}
		var front []string
		for _, x := range ok(t, home, "task", "frontier", "--limit", "null", "--fields", "id").Result()["tasks"].([]any) {
			front = append(front, fmt.Sprint(x.(map[string]any)["id"]))
		}
		if fmt.Sprint(front) != fmt.Sprint(m.Frontier()) {
			t.Fatalf("step %d: frontier: koan %v, model %v", step, front, m.Frontier())
		}
	}
	// The sequence must try both sides.
	if made < 5 || refused < 5 {
		t.Errorf("%d changes made, %d refused", made, refused)
	}
	t.Logf("%d changes made, %d refused: %v", made, refused, succeeded)
}

// An edge given more than once, however it is written and from whichever
// end, is written once; in a batch, across parts too.
func TestEdgeGivenTwice(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	ok(t, home, "task", "create", "a")    // 2
	ok(t, home, "task", "create", "b", "--blocked-by", "2,@2,2", "--blocks", "/w.done,@1,1.done")
	if fmt.Sprint(field(t, home, "3", "blocked_by")) != "[map[id:2 ref:@2 (/) state:open]]" ||
		fmt.Sprint(field(t, home, "4", "blocked_by")) != "[map[id:3 ref:@3 (/) state:open]]" {
		t.Errorf("edges: %v %v", field(t, home, "3", "blocked_by"), field(t, home, "4", "blocked_by"))
	}
	batchOK(t, home, `{"tasks": [{"label": "$a", "title": "a", "blocks": ["$b"]}, {"label": "$b", "title": "b", "blocked_by": ["$a", "$a"]}]}`)
	if fmt.Sprint(field(t, home, "6", "blocked_by")) != "[map[id:5 ref:@5 (/) state:open]]" {
		t.Errorf("batch: %v", field(t, home, "6", "blocked_by"))
	}
	r := ok(t, home, "task", "unblock", "3", "--blocked-by", "2,@2")
	if fmt.Sprint(r.Result()) != "map[changed:true id:3 released:[3]]" {
		t.Errorf("unblock: %s", r.Line())
	}
}

// Two spellings of a path written from different starts aren't compared
// as the input's shape: the one the tree doesn't hold is case-mismatch
// when it resolves.
func TestCaseFromDifferentStarts(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	r := fails(t, home, "conflict", "case-mismatch", "task", "block", "/work/api.start", "--blocked-by", "@1/API.done")
	if fmt.Sprint(r.Error()["paths"]) != "[@1/API /work/api]" {
		t.Errorf("paths: %s", r.Line())
	}
	batchFails(t, home, "conflict", "case-mismatch", "/tasks/0/folder",
		`{"folders": [{"path": "/work/Proj"}], "tasks": [{"title": "t", "folder": "@1/proj"}]}`)
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@api/", "@work/@api/@notes.md=")
}
