package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// batch runs a batch with its input on standard input.
func batch(t *testing.T, home, in string) *testrun.Result {
	t.Helper()
	return input(t, home, "batch", in)
}

// batchOK runs a batch that must succeed with no warnings.
func batchOK(t *testing.T, home, in string) *testrun.Result {
	t.Helper()
	r := batch(t, home, in)
	if r.Exit != 0 || !r.OK() || len(r.Envelope["warnings"].([]any)) != 0 || r.Stderr != "" {
		t.Fatalf("batch %s: exit %d, stderr %q: %s", in, r.Exit, r.Stderr, r.Line())
	}
	return r
}

// batchFails runs a batch that must fail with kind, and rule for a
// conflict, with problems at fields, space-separated.
func batchFails(t *testing.T, home, kind, rule, fields, in string) *testrun.Result {
	t.Helper()
	r := batch(t, home, in)
	got, _ := r.Error()["rule"].(string)
	if r.Exit != 1 || r.Kind() != kind || got != rule {
		t.Fatalf("batch %s: exit %d, want %s %s: %s", in, r.Exit, kind, rule, r.Line())
	}
	if problemFields(r) != fields {
		t.Errorf("batch %s: problems %q, want %q: %s", in, problemFields(r), fields, r.Line())
	}
	return r
}

// nextID checks a refused batch issued nothing: the next task made gets
// the ID given.
func nextID(t *testing.T, home string, want int) {
	t.Helper()
	r := ok(t, home, "task", "create", "next")
	if r.Result()["id"] != json.Number(fmt.Sprint(want)) {
		t.Errorf("a refused batch issued an ID: %s", r.Line())
	}
	ok(t, home, "delete", fmt.Sprint(want))
}

// The spec's batch file: a missing parent is made, folders first, then
// tasks in input order, then the gates their edges name; two parts
// naming the same done gate make one.
func TestBatch(t *testing.T) {
	home := root(t)
	r := batchOK(t, home, `{
  "folders": [ { "path": "/work/api", "title": "API rework" } ],
  "folder": "/work/api",
  "tasks": [
    { "label": "$design", "title": "Design the endpoints", "blocked_by": ["/work/api.start"] },
    { "label": "$build",  "title": "Build them", "blocked_by": ["$design"], "blocks": ["/work/api"] },
    { "title": "Review", "folder": "/work", "blocked_by": ["/work/api"] }
  ]
}`)
	golden(t, r, "batch")
	if fmt.Sprint(field(t, home, "3", "blocked_by")) != "[map[id:6 ref:@6 (/work/api.start) state:open]]" ||
		fmt.Sprint(field(t, home, "4", "blocked_by")) != "[map[id:3 ref:@3 (/work/api) state:open]]" ||
		fmt.Sprint(field(t, home, "7", "blocked_by")) != "[map[id:4 ref:@4 (/work/api) state:open]]" ||
		fmt.Sprint(field(t, home, "5", "blocked_by")) != "[map[id:7 ref:@7 (/work/api.done) state:open]]" {
		t.Errorf("edges")
	}
	f := ok(t, home, "show", "/work/api").Result()["folder"].(map[string]any)
	if f["title"] != "API rework" || ok(t, home, "show", "/work").Result()["folder"].(map[string]any)["title"] != nil {
		t.Errorf("titles: %v", f)
	}
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@5.md=", "@work/@api/", "@work/@api/@notes.md=",
		"@work/@api/@3.md=", "@work/@api/@4.md=", "@work/@api/@start.md=", "@work/@api/@done.md=")
	// Only the start gate waits on nothing.
	r = ok(t, home, "task", "list", "--readiness", "ready", "--fields", "id")
	if fmt.Sprint(r.Result()["tasks"]) != "[map[id:6]]" {
		t.Errorf("ready: %s", r.Line())
	}
}

// The batch is read as the tree it would leave: a part can name a folder
// another part makes, as a missing parent or by the ID it gets, and a
// label another part defines later; an edge naming the ID a task of the
// batch gets names that task. Folders are made parents first, in tree
// order, whatever the input's order, each with its own fields; a missing
// parent with none.
func TestBatchReadAsTree(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "old") // 1
	r := batchOK(t, home, `{
  "tasks": [
    { "label": "$a", "title": "a", "folder": "/x", "blocked_by": ["$b"], "notes": "a's notes" },
    { "label": "$b", "title": "b", "folder": "@3/y", "blocks": ["/x/y.start"], "priority": 3, "tags": ["q", "p"], "extra": {"z": 1, "a": [true]} },
    { "title": "c", "folder": "/x/y", "blocked_by": [6, "@1"] }
  ],
  "folders": [ { "path": "/x/y", "title": " Y ", "notes": "y's notes", "extra": {"k": "v"} }, { "path": "/m" } ]
}`)
	golden(t, r, "batch-read-as-tree")
	if fmt.Sprint(field(t, home, "5", "blocked_by")) != "[map[id:6 ref:@6 (/x/y) state:open]]" ||
		fmt.Sprint(field(t, home, "8", "blocked_by")) != "[map[id:6 ref:@6 (/x/y) state:open]]" ||
		fmt.Sprint(field(t, home, "7", "blocked_by")) != "[map[id:1 ref:@1 (/) state:open] map[id:6 ref:@6 (/x/y) state:open]]" {
		t.Errorf("edges")
	}
	b := ok(t, home, "show", "6").Result()["task"].(map[string]any)
	if b["priority"] != json.Number("3") || fmt.Sprint(b["tags"]) != "[p q]" || fmt.Sprint(b["extra"]) != "map[a:[true] z:1]" {
		t.Errorf("fields: %v", b)
	}
	f := ok(t, home, "show", "/x/y").Result()["folder"].(map[string]any)
	if f["title"] != "Y" || fmt.Sprint(f["extra"]) != "map[k:v]" {
		t.Errorf("folder: %v", f)
	}
	checkTree(t, home, "@1.md=", "@m/", "@m/@notes.md=", "@x/", "@x/@notes.md=", "@x/@5.md=a's notes",
		"@x/@y/", "@x/@y/@notes.md=y's notes", "@x/@y/@6.md=", "@x/@y/@7.md=", "@x/@y/@start.md=")

	// A folder given as well as a missing parent of another gets its own
	// fields, whichever comes first; tasks alone, or folders alone, are a
	// batch.
	r = batchOK(t, home, `{"folders": [{"path": "/p/q"}, {"path": "/p", "title": "P", "notes": "p"}]}`)
	if fmt.Sprint(r.Result()) != "map[folders_made:[map[id:9 path:/p] map[id:10 path:/p/q]] gates_made:[] labels:map[] reblocked:[] tasks_made:[]]" {
		t.Errorf("folders alone: %s", r.Line())
	}
	if ok(t, home, "show", "/p").Result()["folder"].(map[string]any)["title"] != "P" {
		t.Errorf("/p's title")
	}
	r = batchOK(t, home, `{"tasks": [{"title": "t"}], "folders": []}`)
	if fmt.Sprint(r.Result()) != "map[folders_made:[] gates_made:[] labels:map[] reblocked:[] tasks_made:[11]]" {
		t.Errorf("tasks alone: %s", r.Line())
	}
}

// Several parts naming the same missing gate, in any form, make one.
func TestBatchOneGate(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	r := batchOK(t, home, `{"tasks": [
  { "title": "a", "blocked_by": ["/w.start"] },
  { "title": "b", "blocked_by": ["@1.start"], "blocks": ["/w"] },
  { "title": "c", "blocks": ["@1", "/w.done"], "blocked_by": ["/w.start"] }
]}`)
	if fmt.Sprint(r.Result()["gates_made"]) != "[map[folder:map[id:1 path:/w] gate:start id:5] map[folder:map[id:1 path:/w] gate:done id:6]]" {
		t.Errorf("gates: %s", r.Line())
	}
	if fmt.Sprint(field(t, home, "6", "blocked_by")) != "[map[id:3 ref:@3 (/) state:open] map[id:4 ref:@4 (/) state:open]]" {
		t.Errorf("done gate: %v", field(t, home, "6", "blocked_by"))
	}
	checkTree(t, home, "@w/", "@w/@notes.md=", "@w/@start.md=", "@w/@done.md=", "@2.md=", "@3.md=", "@4.md=")
}

// The cycle check runs over the whole batch: a cycle formed only across
// parts is refused, pointing at every edge on it in cycle order, and so
// is one closed through edges already there.
func TestBatchCycle(t *testing.T) {
	home := root(t)
	r := batchFails(t, home, "conflict", "cycle", "/tasks/2/blocks/0 /tasks/2/blocked_by/0 /tasks/0/blocked_by/0", `{"tasks": [
  { "label": "$a", "title": "a", "blocked_by": ["$b"] },
  { "label": "$b", "title": "b" },
  { "label": "$c", "title": "c", "blocked_by": ["$a"], "blocks": ["$b"] }
]}`)
	golden(t, r, "batch-cycle")
	ok(t, home, "task", "create", "x")                      // 1
	ok(t, home, "task", "create", "y", "--blocked-by", "1") // 2
	ok(t, home, "folder", "create", "/w")                   // 3
	ok(t, home, "block", "/w.start", "2")                   // 4
	r = batchFails(t, home, "conflict", "cycle", "/tasks/1/blocks/0 /tasks/1/blocked_by/0 /tasks/0/blocks/0",
		`{"tasks": [{"label": "$a", "title": "a", "blocks": [1]}, {"title": "b", "blocked_by": ["/w.start"], "blocks": ["$a"]}]}`)
	if fmt.Sprint(r.Error()["ids"]) != "[5 6 4 2 1]" {
		t.Errorf("cycle: %s", r.Line())
	}
	checkTree(t, home, "@1.md=", "@2.md=", "@w/", "@w/@notes.md=", "@w/@start.md=")
	nextID(t, home, 5)
}

// A path already in the tree, or given twice, or taken on disk where a
// folder the batch makes would go, a missing parent included, is exists,
// pointing at the part.
func TestBatchExists(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work") // 1
	golden(t, batchFails(t, home, "conflict", "exists", "/folders/1/path",
		`{"folders": [{"path": "/new"}, {"path": "/work"}], "tasks": [{"title": "t"}]}`), "batch-exists")
	r := batchFails(t, home, "conflict", "exists", "/folders/2/path", `{"folders": [{"path": "/b"}, {"path": "/a"}, {"path": "/b"}]}`)
	if fmt.Sprint(r.Error()["paths"]) != "[/b]" {
		t.Errorf("twice: %s", r.Line())
	}
	treeDir := filepath.Join(home, "koan-root", "tree")
	os.Mkdir(filepath.Join(treeDir, "@Stray"), 0o755)
	os.WriteFile(filepath.Join(treeDir, "@work", "@api"), []byte("x"), 0o644)
	golden(t, batchFails(t, home, "conflict", "exists", "/folders/1/path",
		`{"folders": [{"path": "/fine"}, {"path": "/stray/a/b"}]}`), "batch-exists-on-disk")
	for _, c := range []struct{ in, fields, entry string }{
		{`{"folders": [{"path": "/work/api/x"}]}`, "/folders/0/path", "tree/@work/@api"},
		{`{"folders": [{"path": "/work/API"}]}`, "/folders/0/path", "tree/@work/@api"},
		{`{"folders": [{"path": "/a"}, {"path": "/work/b"}, {"path": "/work/api/c/d"}]}`, "/folders/2/path", "tree/@work/@api"},
	} {
		r := batchFails(t, home, "conflict", "exists", c.fields, c.in)
		if paths := r.Error()["paths"].([]any); len(paths) != 1 || paths[0] != filepath.Join(home, "koan-root", c.entry) {
			t.Errorf("%s: %s", c.in, r.Line())
		}
	}
	checkTree(t, home, "@Stray/", "@work/", "@work/@notes.md=", "@work/@api=x")
	nextID(t, home, 2)
}

// A path that matches a folder only ignoring case is case-mismatch,
// pointing at the part that gave it: a folder's path, a task's folder,
// the batch's folder or an edge.
func TestBatchCaseMismatch(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/Work") // 1
	golden(t, batchFails(t, home, "conflict", "case-mismatch", "/folders/0/path", `{"folders": [{"path": "/work/api"}]}`), "batch-case-mismatch")
	for in, fields := range map[string]string{
		`{"folders": [{"path": "/WORK"}]}`:                                             "/folders/0/path",
		`{"tasks": [{"title": "t", "folder": "/work"}]}`:                               "/tasks/0/folder",
		`{"folder": "/work", "tasks": [{"title": "t"}]}`:                               "/folder",
		`{"tasks": [{"title": "t"}, {"title": "u", "blocks": ["/work.start"]}]}`:       "/tasks/1/blocks/0",
		`{"folders": [{"path": "/a/b"}], "tasks": [{"title": "t", "folder": "@2/B"}]}`: "/tasks/0/folder",
	} {
		batchFails(t, home, "conflict", "case-mismatch", fields, in)
	}
	checkTree(t, home, "@Work/", "@Work/@notes.md=")
	nextID(t, home, 2)
}

// A reference that names nothing is not-found, pointing at the part; the
// batch's folder is resolved though no task takes it.
func TestBatchNotFound(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	golden(t, batchFails(t, home, "not-found", "", "/tasks/1/blocks/0",
		`{"tasks": [{"title": "a"}, {"title": "b", "blocks": ["@99"]}]}`), "batch-not-found")
	for in, fields := range map[string]string{
		`{"tasks": [{"title": "a", "folder": "/nope"}]}`:                          "/tasks/0/folder",
		`{"folder": "/nope", "tasks": [{"title": "a"}]}`:                          "/folder",
		`{"folder": "/nope", "folders": [{"path": "/x"}]}`:                        "/folder",
		`{"folder": "/nope", "tasks": [{"title": "a", "folder": "/w"}]}`:          "/folder",
		`{"folder": "@1/x", "folders": [{"path": "/w/y"}]}`:                       "/folder",
		`{"tasks": [{"title": "a", "folder": "@2"}]}`:                             "/tasks/0/folder",
		`{"tasks": [{"title": "a", "blocked_by": ["/w/x.start"]}]}`:               "/tasks/0/blocked_by/0",
		`{"tasks": [{"title": "a", "blocked_by": ["@1/x"]}, {"title": "b"}]}`:     "/tasks/0/blocked_by/0",
		`{"tasks": [{"label": "$a", "title": "a"}, {"title": "b", "folder": 3}]}`: "/tasks/1/folder",
		// Only the first named, in the order the batch resolves: every
		// task's folder before any edge.
		`{"tasks": [{"title": "a", "blocked_by": [98]}, {"title": "b", "folder": "/nope"}]}`: "/tasks/1/folder",
	} {
		batchFails(t, home, "not-found", "", fields, in)
	}
	checkTree(t, home, "@w/", "@w/@notes.md=")
	nextID(t, home, 2)
}

// Every fault in the input's shape is named, by its pointer into the
// batch, before the root is looked at: no parts, too many, a label
// misused or defined twice, a bad path, title or value, a field a part
// doesn't take, and two paths that differ only in case anywhere in the
// batch.
func TestBatchInvalid(t *testing.T) {
	home := t.TempDir()
	golden(t, batchFails(t, home, "invalid-input", "", "/folders/0/path /folders/1/title /folder /tasks/0/title /tasks/1/label /tasks/1/blocks/0 /tasks/1/blocks/1", `{
  "folders": [{"path": "/"}, {"path": "/a", "title": ""}],
  "folder": "@1.start",
  "tasks": [{"label": "$a", "title": " "}, {"label": "$a", "title": "b", "blocks": ["$a.done", "/"]}]
}`), "batch-invalid")
	for in, fields := range map[string]string{
		`{}`:                                        "",
		`{"folders": [], "tasks": []}`:              "",
		`{"tasks": "x"}`:                            " /tasks",
		`{"tasks": ["x"]}`:                          "/tasks/0",
		`{"tasks": [{}]}`:                           "/tasks/0/title",
		`{"tasks": [{"title": "a", "x": 1}]}`:       "/tasks/0/x",
		`{"frob": 1, "tasks": [{"title": "a"}]}`:    "/frob",
		`{"tasks": [{"title": "a", "label": "a"}]}`: "/tasks/0/label",
		`{"tasks": [{"title": "a", "label": "$a"}, {"title": "b", "label": "$a"}]}`:        "/tasks/1/label",
		`{"tasks": [{"title": "a", "blocked_by": ["$a.start"]}]}`:                          "/tasks/0/blocked_by/0",
		`{"tasks": [{"title": "a", "blocked_by": ["$a:x"]}]}`:                              "/tasks/0/blocked_by/0",
		`{"tasks": [{"title": "a", "folder": "$a", "label": "$a"}]}`:                       "/tasks/0/folder",
		`{"folder": "$a", "tasks": [{"title": "a", "label": "$a"}]}`:                       "/folder",
		`{"tasks": [{"title": "a", "blocked_by": ["@2:x", "/"]}]}`:                         "/tasks/0/blocked_by/0 /tasks/0/blocked_by/1",
		`{"tasks": [{"title": "a", "folder": "/w.start"}]}`:                                "/tasks/0/folder",
		`{"folders": [{"path": "/a.start"}, {"path": "$a"}, {"path": 1}, {"title": "x"}]}`: "/folders/0/path /folders/1/path /folders/2/path /folders/3/path",
		`{"folders": [{"path": "/a", "frob": 1, "notes": 1, "extra": []}]}`:                "/folders/0/frob /folders/0/extra /folders/0/notes",
		`{"folders": ["/a"]}`: "/folders/0",
		`{"folders": [{"path": "/new/x"}, {"path": "/New/y"}]}`:                                          "/folders/0/path /folders/1/path",
		`{"folder": "/w", "tasks": [{"title": "a", "blocked_by": ["/W.start"]}]}`:                        "/folder /tasks/0/blocked_by/0",
		`{"folders": [{"path": "/w"}], "tasks": [{"title": "a"}, {"title": "b", "folder": "/W/x"}]}`:     "/folders/0/path /tasks/1/folder",
		`{"tasks": [{"title": "a", "priority": 1.5, "tags": ["A", "b", "b"], "extra": [], "notes": 1}]}`: "/tasks/0/priority /tasks/0/tags/0 /tasks/0/tags/2 /tasks/0/extra /tasks/0/notes",
		`{"tasks": [{"title": "a", "blocked_by": "x", "blocks": [0]}]}`:                                  "/tasks/0/blocked_by /tasks/0/blocks/0",
	} {
		batchFails(t, home, "invalid-input", "", fields, in)
	}
	if _, err := os.Lstat(filepath.Join(home, "koan-root")); !os.IsNotExist(err) {
		t.Errorf("a refused batch made the root")
	}
	batchFails(t, home, "not-initialized", "", "", `{"tasks": [{"title": "a"}]}`)
	// The command line gives no input but {}, which holds no parts.
	golden(t, fails(t, home, "invalid-input", "", "batch"), "batch-no-input")
	for _, args := range [][]string{{"batch", "x"}, {"batch", "--folder", "/"}, {"batch", "--tasks", "[]"}} {
		if r := koan(t, home, args...); r.Exit != 2 || r.Kind() != "usage" {
			t.Errorf("%q: %s", args, r.Line())
		}
	}

	// Found once the references resolve: an edge to itself, and a label
	// no part defines, at the edge.
	home = root(t)
	ok(t, home, "task", "create", "x") // 1
	golden(t, batchFails(t, home, "invalid-input", "", "/tasks/1/blocks/1",
		`{"tasks": [{"title": "a"}, {"label": "$b", "title": "b", "blocks": [1, "$b"]}]}`), "batch-itself")
	batchFails(t, home, "invalid-input", "", "/tasks/0/blocked_by/0", `{"tasks": [{"title": "a", "blocked_by": [2]}]}`)
	golden(t, batchFails(t, home, "invalid-input", "", "/tasks/0/blocked_by/1",
		`{"tasks": [{"label": "$a", "title": "a", "blocked_by": ["$a2", "$nope"]}, {"label": "$a2", "title": "b"}]}`), "batch-no-label")
	batchFails(t, home, "invalid-input", "", "/tasks/0/folder", `{"tasks": [{"title": "a", "folder": "@1/x"}]}`)
	checkTree(t, home, "@1.md=")
	nextID(t, home, 2)
}

// A batch holds 1 to 1000 parts, folders and tasks together.
func TestBatchSize(t *testing.T) {
	parts := func(folders, tasks int) string {
		var fs, ts []string
		for i := range folders {
			fs = append(fs, fmt.Sprintf(`{"path": "/f/p%d"}`, i))
		}
		for i := range tasks {
			edge := ""
			if i > 0 {
				edge = fmt.Sprintf(`, "blocked_by": ["$t%d"]`, i-1)
			}
			ts = append(ts, fmt.Sprintf(`{"label": "$t%d", "title": "t%d", "folder": "/f/p%d"%s}`, i, i, i%folders, edge))
		}
		return `{"folders": [` + strings.Join(fs, ",") + `], "tasks": [` + strings.Join(ts, ",") + `]}`
	}
	home := root(t)
	golden(t, batchFails(t, home, "invalid-input", "", "", parts(500, 501)), "batch-too-many")
	r := batchOK(t, home, parts(500, 500))
	res := r.Result()
	if len(res["folders_made"].([]any)) != 501 || len(res["tasks_made"].([]any)) != 500 || len(res["labels"].(map[string]any)) != 500 {
		t.Errorf("1000 parts: %d folders, %d tasks", len(res["folders_made"].([]any)), len(res["tasks_made"].([]any)))
	}
	// /f, then its folders in byte order: p0, p1, p10, p100, ...
	if f := res["folders_made"].([]any); fmt.Sprint(f[0], f[1], f[2], f[3]) != "map[id:1 path:/f] map[id:2 path:/f/p0] map[id:3 path:/f/p1] map[id:4 path:/f/p10]" {
		t.Errorf("tree order: %v", f[:4])
	}
	if res["labels"].(map[string]any)["$t499"] != json.Number("1001") {
		t.Errorf("labels: %v", res["labels"].(map[string]any)["$t499"])
	}
	if r := ok(t, home, "task", "list", "--readiness", "ready", "--fields", "id"); fmt.Sprint(r.Result()["tasks"]) != "[map[id:502]]" {
		t.Errorf("ready: %s", r.Line())
	}
}

// An edge added to a complete task's blocked_by warns complete-blocker;
// a claimed task that was ready and waits now warns claimed-blocked, and
// is reblocked with its claim kept.
func TestBatchWarnings(t *testing.T) {
	home := root(t)
	for range 3 {
		ok(t, home, "task", "create", "t") // 1, 2, 3
	}
	db(t, home, "UPDATE task SET completed_at = '2026-10-20T10:00:00Z' WHERE id = 1",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (2, '2026-10-20T10:00:00Z', '{}')`)
	golden(t, warned(t, home, []string{"complete-blocker"}, "batch", "-i", writeInput(t, `{"tasks": [{"title": "x", "blocks": [1]}]}`)), "batch-complete-blocker")
	golden(t, warned(t, home, []string{"claimed-blocked"}, "batch", "-i", writeInput(t, `{"tasks": [{"title": "x", "blocks": [2, 3]}]}`)), "batch-claimed-blocked")
	if field(t, home, "2", "claim") == nil || field(t, home, "1", "state") != "complete" {
		t.Errorf("the claim stays, and nothing reopens")
	}
}

// writeInput writes a batch file, for a command line that names it.
func writeInput(t *testing.T, in string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(file, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// Once COMMIT succeeds the batch succeeds: a step the operating system
// refuses leaves what the batch made, with pending-step, and the next
// write carries it out.
func TestBatchStepRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := root(t)
	ok(t, home, "folder", "create", "/work") // 1
	dir := filepath.Join(home, "koan-root", "tree", "@work")
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	r := batch(t, home, `{"folders": [{"path": "/work/api"}], "tasks": [{"title": "t", "folder": "/work/api"}]}`)
	if r.Exit != 0 || !r.OK() || len(r.Envelope["warnings"].([]any)) != 1 {
		t.Fatalf("exit %d: %s", r.Exit, r.Line())
	}
	golden(t, r, "batch-pending-step")
	os.Chmod(dir, 0o755)
	ok(t, home, "task", "create", "u")
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@api/", "@work/@api/@notes.md=", "@work/@api/@3.md=", "@4.md=")
}
