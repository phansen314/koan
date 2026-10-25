package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	r := fails(t, home, "conflict", "cycle", "block", "8", "10")
	golden(t, r, "block-cycle")
	for _, c := range []struct {
		args []string
		ids  string
	}{
		{[]string{"8", "10"}, "[8 10 13 12]"},
		{[]string{"@7.start", "@9"}, "[11 14 10 13 12 8]"},
		{[]string{"8", "@7"}, "[8 12]"},
	} {
		r := fails(t, home, "conflict", "cycle", append([]string{"block"}, c.args...)...)
		if fmt.Sprint(r.Error()["ids"]) != c.ids || problemFields(r) != "/blocked_by/0" {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	if fmt.Sprint(field(t, home, "8", "blocked_by")) != "[map[id:11 ref:@11 (/work/phase1.start) state:open]]" {
		t.Errorf("a refused block wrote an edge")
	}
	r = ok(t, home, "block", "4", "@1")
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

// block adds edges from either end, and says whether it changed
// anything: an edge already there changes nothing and leaves updated_at
// alone. A task it makes wait that was ready is reblocked.
func TestTaskBlock(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	ok(t, home, "task", "create", "a")           // 3
	ok(t, home, "task", "create", "b", "--folder", "/work")
	db(t, home, "UPDATE task SET updated_at = '2026-10-20T10:00:00Z'")
	r := ok(t, home, "block", "3", "4")
	golden(t, r, "block")
	if field(t, home, "3", "updated_at") == "2026-10-20T10:00:00Z" || field(t, home, "4", "updated_at") != "2026-10-20T10:00:00Z" {
		t.Errorf("updated_at moves on the task whose blocked_by gains the edge, and only there")
	}
	db(t, home, "UPDATE task SET updated_at = '2026-10-20T10:00:00Z'")
	r = ok(t, home, "block", "3", "@4,4")
	if fmt.Sprint(r.Result()) != "map[changed:false gates_made:[] id:3 reblocked:[]]" || field(t, home, "3", "updated_at") != "2026-10-20T10:00:00Z" {
		t.Errorf("again: %s", r.Line())
	}
	// From the other end: 4 waits on 3 would close a cycle; through
	// --input, with IDs as integers, 4 blocks a new task's wait.
	r = fails(t, home, "conflict", "cycle", "block", "3", "--blocks", "4")
	if fmt.Sprint(r.Error()["ids"]) != "[4 3]" || problemFields(r) != "/blocks/0" {
		t.Errorf("cycle from blocks: %s", r.Line())
	}
	ok(t, home, "task", "create", "c") // 5
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"task":4,"blocks":[5]}`)}.Koan(t, "block", "-i", "-")
	r.Check(t, "block")
	if fmt.Sprint(r.Result()) != "map[changed:true gates_made:[] id:4 reblocked:[5]]" {
		t.Errorf("--input: %s", r.Line())
	}
	if fmt.Sprint(field(t, home, "4", "blocks")) != "[map[id:3 ref:@3 (/) state:open] map[id:5 ref:@5 (/) state:open]]" {
		t.Errorf("blocks: %v", field(t, home, "4", "blocks"))
	}
}

// block makes each gate it names that its folder lacks, its subject
// included, whose ID comes first, then the rest in the order the edges
// are given, blocked_by before blocks; each with an empty @start.md or
// @done.md, keeping one made by hand. A gate named twice is made once.
func TestTaskBlockGates(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	ok(t, home, "task", "create", "a")           // 3
	r := ok(t, home, "block", "/work/api.start", "/work.start,@1.start", "--blocks", "3")
	golden(t, r, "block-gates")
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
	r = ok(t, home, "block", "3", "/web", "--blocks", "/web.start,/work")
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
	r = ok(t, home, "block", "/web.start", "@2.start")
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
	golden(t, fails(t, home, "invalid-input", "", "block", "1"), "block-no-edge")
	for _, c := range []struct {
		args   []string
		fields string
	}{
		{[]string{"/w", "x"}, "/task /blocked_by/0"},
		{[]string{"1", "/"}, "/blocked_by/0"},
		{[]string{"1", "--blocks", "@2:x,$a"}, "/blocks/0 /blocks/1"},
		{[]string{"/w.start", "/W"}, "/task /blocked_by/0"},
		{[]string{"1", "/a/b", "--blocks", "/A.start"}, "/blocked_by/0 /blocks/0"},
		{[]string{"/", "1"}, "/task"},
		{[]string{"/w:x"}, "/task "},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"block"}, c.args...)...)
		if problemFields(r) != c.fields {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	for _, cmd := range []string{"block", "unblock"} {
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
	fails(t, home, "not-initialized", "", "block", "1", "2")

	home = root(t)
	ok(t, home, "folder", "create", "/w") // 1
	ok(t, home, "task", "create", "a")    // 2
	ok(t, home, "task", "create", "b")    // 3
	golden(t, fails(t, home, "invalid-input", "", "block", "2", "3,@2"), "block-itself")
	// Its own gate, made and then refused: nothing is made.
	r := fails(t, home, "invalid-input", "", "block", "/w.start", "--blocks", "@1.start")
	if problemFields(r) != "/blocks/0" {
		t.Errorf("itself through a gate: %s", r.Line())
	}
	// Resolution's invalid-input points at the field.
	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"@2.start", "3"}, "/task"},
		{[]string{"2", "1,@3/x"}, "/blocked_by/1"},
		{[]string{"2", "--blocks", "@3.done"}, "/blocks/0"},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"block"}, c.args...)...)
		if problemFields(r) != c.field {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	checkTree(t, home, "@w/", "@w/@notes.md=", "@2.md=", "@3.md=")
	if r := ok(t, home, "task", "create", "c"); r.Result()["id"] != json.Number("4") {
		t.Errorf("a refused block issued an ID: %s", r.Line())
	}
}

// What a task waits on is the words after it, a list given as more
// words or comma-separated, and --blocked-by is gone from block and
// unblock; through --input it is blocked_by.
func TestTaskBlockPositional(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "a") // 1
	ok(t, home, "task", "create", "b") // 2
	ok(t, home, "task", "create", "c") // 3
	ok(t, home, "task", "create", "d") // 4
	r := ok(t, home, "block", "1", "2", "3,@4")
	if fmt.Sprint(r.Result()["changed"]) != "true" {
		t.Errorf("block: %s", r.Line())
	}
	if r := ok(t, home, "why", "1"); fmt.Sprint(r.Result()["ready"]) != "[2 3 4]" {
		t.Errorf("why: %s", r.Line())
	}
	if r := ok(t, home, "unblock", "1", "2", "3"); fmt.Sprint(r.Result()["changed"], r.Result()["released"]) != "true []" {
		t.Errorf("unblock: %s", r.Line())
	}
	if r := ok(t, home, "unblock", "1", "4", "--blocks", "2"); fmt.Sprint(r.Result()["changed"], r.Result()["released"]) != "true [1]" {
		t.Errorf("unblock with --blocks: %s", r.Line())
	}
	golden(t, usageAt(t, home, "--blocked-by", "block", "1", "--blocked-by", "2"), "block-blocked-by")
	usageAt(t, home, "--blocked-by", "unblock", "1", "--blocked-by", "2")
	usageAt(t, home, "1", "block", "1", "2", "-i", "-")
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"task":1,"blocked_by":[2,"@3"]}`)}.Koan(t, "block", "-i", "-")
	r.Check(t, "block")
	golden(t, r, "block-input")
	if r := ok(t, home, "why", "1"); fmt.Sprint(r.Result()["ready"]) != "[2 3]" {
		t.Errorf("why after --input: %s", r.Line())
	}
}

// A reference that names nothing is not-found, one matching only
// ignoring case is case-mismatch, and nothing is written.
func TestTaskBlockRefused(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/Work") // 1
	ok(t, home, "task", "create", "a")       // 2
	golden(t, fails(t, home, "not-found", "", "block", "2", "99"), "block-not-found")
	// Only the first reference that names nothing, in input order.
	r := fails(t, home, "not-found", "", "block", "2", "/nope,99", "--blocks", "98")
	if fmt.Sprint(r.Error()["ids"], r.Error()["paths"]) != "[] [/nope]" {
		t.Errorf("first only: %s", r.Line())
	}
	r = fails(t, home, "not-found", "", "block", "97", "99")
	if fmt.Sprint(r.Error()["ids"], r.Error()["paths"]) != "[97] []" {
		t.Errorf("subject first: %s", r.Line())
	}
	fails(t, home, "not-found", "", "block", "99", "2")
	fails(t, home, "not-found", "", "block", "@1", "2")
	fails(t, home, "not-found", "", "block", "2", "--blocks", "/Work/api")
	golden(t, fails(t, home, "conflict", "case-mismatch", "block", "2", "/work.start"), "block-case-mismatch")
	fails(t, home, "conflict", "case-mismatch", "block", "/WORK.done", "2")
	// A later edge's refusal undoes the gate an earlier one made.
	fails(t, home, "not-found", "", "block", "2", "/Work.start,98")
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
	golden(t, warned(t, home, []string{"complete-blocker"}, "block", "1", "6"), "block-complete-blocker")
	golden(t, warned(t, home, []string{"claimed-blocked"}, "block", "3", "5"), "block-claimed-blocked")
	if field(t, home, "3", "claim") == nil || field(t, home, "1", "state") != "complete" {
		t.Errorf("the claim stays, and nothing reopens")
	}
	// Several of each, in the plural; an edge already there adds
	// nothing.
	r := warned(t, home, []string{"complete-blocker", "claimed-blocked"}, "block", "5", "--blocks", "2,4,6,3,1")
	golden(t, r, "block-warnings")
	// A claimed task already waiting isn't made to wait again.
	ok(t, home, "task", "create", "t") // 7
	r = ok(t, home, "block", "4", "7")
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
	golden(t, warned(t, home, []string{"pending-step"}, "block", "2", "/work.start"), "block-pending-step")
	os.Chmod(dir, 0o755)
	ok(t, home, "task", "create", "u")
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@start.md=", "@2.md=", "@4.md=")
}

// unblock removes edges from either end and reports the open tasks
// it made ready; an edge that isn't there, to itself included, changes
// nothing. It never makes a gate: naming one the folder lacks is
// not-found.
func TestTaskUnblock(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	for range 3 {
		ok(t, home, "task", "create", "t") // 2, 3, 4
	}
	ok(t, home, "block", "2", "4,/w.start") // 5
	ok(t, home, "block", "2", "--blocks", "3")
	db(t, home, "UPDATE task SET updated_at = '2026-10-20T10:00:00Z'")
	r := ok(t, home, "unblock", "2", "4,@1.start", "--blocks", "3")
	if fmt.Sprint(r.Result()) != "map[changed:true id:2 released:[2 3]]" || field(t, home, "3", "updated_at") == "2026-10-20T10:00:00Z" {
		t.Errorf("unblock: %s", r.Line())
	}
	db(t, home, "UPDATE task SET updated_at = '2026-10-20T10:00:00Z'")
	r = ok(t, home, "unblock", "2", "4,2", "--blocks", "2")
	if fmt.Sprint(r.Result()) != "map[changed:false id:2 released:[]]" || field(t, home, "2", "updated_at") != "2026-10-20T10:00:00Z" {
		t.Errorf("nothing to remove: %s", r.Line())
	}
	ok(t, home, "block", "2", "3")
	r = ok(t, home, "unblock", "@2", "3")
	golden(t, r, "unblock")
	golden(t, fails(t, home, "not-found", "", "unblock", "2", "--blocks", "/w"), "unblock-no-gate")
	golden(t, fails(t, home, "not-found", "", "unblock", "/w.done", "--blocks", "2"), "unblock-no-subject-gate")
	fails(t, home, "conflict", "case-mismatch", "unblock", "2", "--blocks", "/W.start")
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

// why gives a task's open blockers, and the ready tasks it waits on
// through open blockers at any depth, in work order; a complete task
// holds nothing back, so the walk never passes one.
func TestTaskWhy(t *testing.T) {
	home := example(t)
	golden(t, ok(t, home, "why", "10"), "why")
	r := ok(t, home, "why", "/work/phase1.start")
	golden(t, r, "why-ready")

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
	ok(t, home, "block", "1", "2,3")
	ok(t, home, "block", "2", "4")
	ok(t, home, "block", "4", "5")
	ok(t, home, "block", "3", "6")
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id = 4",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (6, '2026-10-20T10:00:00Z', '{}')`)
	r = ok(t, home, "why", "1")
	if fmt.Sprint(r.Result()) != "map[blockers:[map[id:2 ref:@2 (/) state:open] map[id:3 ref:@3 (/) state:open]] id:1 readiness:blocked ready:[6 2]]" {
		t.Errorf("why 1: %s", r.Line())
	}
	r = ok(t, home, "why", "4")
	if fmt.Sprint(r.Result()) != "map[blockers:[] id:4 readiness:<nil> ready:[]]" {
		t.Errorf("complete: %s", r.Line())
	}
	fails(t, home, "not-found", "", "why", "99")
	if r := fails(t, home, "invalid-input", "", "why", "/"); problemFields(r) != "/task" {
		t.Errorf("root: %s", r.Line())
	}
}

// frontier gives the ready tasks with no claim, gates included, in
// work order, under its subject; the folder chooses where to look, never
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
	ok(t, home, "block", "7", "/c.start") // 12
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id = 11",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (9, '2026-10-20T10:00:00Z', '{}')`)
	ids := func(r *testrun.Result) string {
		var out []string
		for _, x := range r.Result()["tasks"].([]any) {
			out = append(out, fmt.Sprint(x.(map[string]any)["id"]))
		}
		return strings.Join(out, " ")
	}
	golden(t, ok(t, home, "frontier", "--fields", "title,priority,folder,readiness"), "frontier")
	golden(t, ok(t, home, "frontier", "/a", "--fields", "title,priority,folder,readiness"), "frontier-subject")
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "6 5 8 4 12"},
		{[]string{"/a"}, "6 5"},
		{[]string{"@1", "--recursive=false"}, "5"},
		{[]string{"/c"}, "12"},
		{[]string{"/", "--recursive=false"}, "8 4"},
		{[]string{"--tags-any", "x,q"}, "6 8"},
		{[]string{"--tags-all", "x,y"}, "8"},
		{[]string{"--tags-any", "x", "--tags-all", "y"}, "8"},
	} {
		r := ok(t, home, append([]string{"frontier", "--fields", "id"}, c.args...)...)
		if got := ids(r); got != c.want || r.Result()["total"] != json.Number(fmt.Sprint(len(strings.Fields(c.want)))) {
			t.Errorf("%q: got %q, want %q: %s", c.args, got, c.want, r.Line())
		}
	}
	r := ok(t, home, "frontier", "--limit", "2", "--fields", "id")
	if ids(r) != "6 5" || r.Result()["total"] != json.Number("5") || r.Result()["truncated"] != true {
		t.Errorf("--limit: %s", r.Line())
	}
	if r := ok(t, home, "frontier", "--limit", "null"); len(r.Result()["tasks"].([]any)[0].(map[string]any)) != len(taskFieldNames) {
		t.Errorf("every field: %s", r.Line())
	}
	// Completing 4 lets 10 in.
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id = 4")
	if got := ids(ok(t, home, "frontier", "--fields", "id")); got != "6 5 8 10 12" {
		t.Errorf("after complete: %s", got)
	}
	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"--limit", "0"}, "/limit"},
		{[]string{"--fields", "nope"}, "/fields/0"},
		{[]string{"/a.start"}, "/folder"},
		{[]string{"--tags-any", "A"}, "/tags_any/0"},
	} {
		if r := fails(t, home, "invalid-input", "", append([]string{"frontier"}, c.args...)...); problemFields(r) != c.field {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	fails(t, home, "not-found", "", "frontier", "/z")
	fails(t, home, "conflict", "case-mismatch", "frontier", "/A")
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
	r := ok(t, home, "unblock", "3", "2,@2")
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
	r := fails(t, home, "conflict", "case-mismatch", "block", "/work/api.start", "@1/API.done")
	if fmt.Sprint(r.Error()["paths"]) != "[@1/API /work/api]" {
		t.Errorf("paths: %s", r.Line())
	}
	batchFails(t, home, "conflict", "case-mismatch", "/tasks/0/folder",
		`{"folders": [{"path": "/work/Proj"}], "tasks": [{"title": "t", "folder": "@1/proj"}]}`)
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@api/", "@work/@api/@notes.md=")
}

// When several new edges close a cycle together, the closing one is the
// first that closes it, blocked_by before blocks whatever the order on
// the command line.
func TestCycleClosingEdge(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "a") // 1
	ok(t, home, "task", "create", "b") // 2
	r := fails(t, home, "conflict", "cycle", "block", "1", "2", "--blocks", "2")
	if fmt.Sprint(r.Error()["ids"]) != "[2 1]" || problemFields(r) != "/blocks/0 /blocked_by/0" {
		t.Errorf("closing edge: %s", r.Line())
	}
	// A refusal's problems carry its message.
	for _, p := range r.Error()["problems"].([]any) {
		if p.(map[string]any)["reason"] != r.Error()["message"] {
			t.Errorf("reason: %s", r.Line())
		}
	}
}
