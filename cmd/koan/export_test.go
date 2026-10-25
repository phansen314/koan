package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/store"
	"github.com/phansen314/koan/internal/testrun"
)

// exportTree is /work/api with a folder under it, both gates, and open,
// complete and claimed tasks; outside it, /other's done gate, complete,
// which /work/api's start gate waits on, and a task in /work waiting on
// /work/api, which also waits on /other's done gate; and a task in the
// root waiting on one under /work/api: first in tree order, last by ID.
func exportTree(t *testing.T) string {
	t.Helper()
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api/sub")                                                                        // 1, 2, 3
	ok(t, home, "folder", "create", "/other")                                                                               // 4
	ok(t, home, "task", "create", "a", "--folder", "/work/api", "--blocked-by", "/work/api.start", "--blocks", "/work/api") // 5, gates 6 and 7
	ok(t, home, "task", "create", "b", "--folder", "/work/api/sub")                                                         // 8
	ok(t, home, "block", "/work/api.start", "/other")                                                                       // gate 9
	ok(t, home, "complete", "9")
	ok(t, home, "task", "create", "waiter", "--folder", "/work", "--blocked-by", "/work/api,/other") // 10
	ok(t, home, "task", "create", "loose", "--blocked-by", "8")                                      // 11
	ok(t, home, "complete", "8")
	ok(t, home, "claim", "5", "--extra", `{"by":"me"}`)
	return home
}

// edgeList is export's edges as task>blocker pairs.
func edgeList(res map[string]any) []string {
	var out []string
	for _, e := range res["edges"].([]any) {
		m := e.(map[string]any)
		out = append(out, fmt.Sprintf("%v>%v", m["task"], m["blocker"]))
	}
	return out
}

// export gives the subtree's folders and every task under it, the tasks
// outside it at the other end of an edge, and every edge with an end
// under it, once, both its ends listed: what folder list and task list
// give with no limit, in one snapshot, the same bytes each time.
func TestExport(t *testing.T) {
	home := exportTree(t)
	r := koan(t, home, "export", "/work/api")
	if r.Exit != 0 || !r.OK() || len(r.Envelope["warnings"].([]any)) != 0 {
		t.Fatalf("exit %d: %s", r.Exit, r.Line())
	}
	goldenNow(t, r, "export")
	res := r.Result()
	if got := fmt.Sprint(res["folder"].(map[string]any)["id"]); got != "2" {
		t.Errorf("folder %s", got)
	}
	if got := ids(res["folders"]); got != "[3]" {
		t.Errorf("folders %s", got)
	}
	if got := ids(res["tasks"]); got != "[5 6 7 8]" {
		t.Errorf("tasks %s", got)
	}
	// The gate of /other and the tasks in /work and the root, by ID, not
	// in tree order; but not the edge between two of them.
	if got := ids(res["external"]); got != "[9 10 11]" {
		t.Errorf("external %s", got)
	}
	if got := strings.Join(edgeList(res), " "); got != "5>6 6>9 7>5 10>7 11>8" {
		t.Errorf("edges %s", got)
	}

	// The edges are exactly those the records under it list.
	var fromRecords []string
	inside := map[string]bool{}
	for _, x := range res["tasks"].([]any) {
		task := x.(map[string]any)
		id := fmt.Sprint(task["id"])
		inside[id] = true
		for _, b := range task["blocked_by"].([]any) {
			fromRecords = append(fromRecords, id+">"+fmt.Sprint(b.(map[string]any)["id"]))
		}
		for _, w := range task["blocks"].([]any) {
			fromRecords = append(fromRecords, fmt.Sprint(w.(map[string]any)["id"])+">"+id)
		}
	}
	listed := map[string]bool{}
	for _, x := range append(res["tasks"].([]any), res["external"].([]any)...) {
		listed[fmt.Sprint(x.(map[string]any)["id"])] = true
	}
	slices.Sort(fromRecords)
	fromRecords = slices.Compact(fromRecords)
	edges := edgeList(res)
	sorted := slices.Clone(edges)
	slices.Sort(sorted)
	if !slices.Equal(sorted, fromRecords) {
		t.Errorf("edges %v, the records list %v", edges, fromRecords)
	}
	for _, e := range edges {
		task, blocker, _ := strings.Cut(e, ">")
		if !listed[task] || !listed[blocker] {
			t.Errorf("edge %s has an end not listed", e)
		}
	}

	// The records are those of folder list and task list with no limit.
	folders := ok(t, home, "folder", "list", "/work/api", "--limit", "null").Result()["folders"]
	if !reflect.DeepEqual(res["folders"], folders) {
		t.Errorf("folders differ from folder list")
	}
	tasks := ok(t, home, "task", "list", "/work/api", "--state", "open,complete", "--limit", "null").Result()["tasks"]
	if !reflect.DeepEqual(res["tasks"], tasks) {
		t.Errorf("tasks differ from task list")
	}
	if again := ok(t, home, "export", "/work/api"); again.Stdout != r.Stdout {
		t.Errorf("a second export differs:\n%s\n%s", r.Stdout, again.Stdout)
	}
	// By ID, the same.
	if byID := ok(t, home, "export", "@2"); byID.Stdout != r.Stdout {
		t.Errorf("by ID: %s", byID.Line())
	}
}

// export / is the whole tree: the root's record, counting every task,
// tasks in the root included, and nothing outside it.
func TestExportRoot(t *testing.T) {
	home := exportTree(t)
	r := ok(t, home, "export", "/")
	goldenNow(t, r, "export-root")
	res := r.Result()
	f := res["folder"].(map[string]any)
	if f["id"] != nil || f["ref"] != "/" || fmt.Sprintf("%v %v", f["open"], f["complete"]) != "5 2" {
		t.Errorf("root: %v", f)
	}
	if got := ids(res["folders"]); got != "[4 1 2 3]" {
		t.Errorf("folders %s", got)
	}
	if got := ids(res["tasks"]); got != "[11 9 10 5 6 7 8]" {
		t.Errorf("tasks %s", got)
	}
	if got := ids(res["external"]); got != "[]" {
		t.Errorf("external %s", got)
	}
	tasks := ok(t, home, "task", "list", "--state", "open,complete", "--limit", "null").Result()["tasks"]
	if !reflect.DeepEqual(res["tasks"], tasks) {
		t.Errorf("tasks differ from task list")
	}
	if got := strings.Join(edgeList(res), " "); got != "5>6 6>9 7>5 10>7 10>9 11>8" {
		t.Errorf("edges %s", got)
	}
}

// An empty folder exports its own record and nothing else.
func TestExportEmpty(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/empty")
	r := ok(t, home, "export", "/empty")
	goldenNow(t, r, "export-empty")
	res := r.Result()
	for _, k := range []string{"folders", "tasks", "external", "edges"} {
		if len(res[k].([]any)) != 0 {
			t.Errorf("%s: %v", k, res[k])
		}
	}
}

// export reads only the database: material in the folder's directory
// never appears, and a missing notes file gives no missing-notes.
func TestExportReadsOnlyTheDatabase(t *testing.T) {
	home := exportTree(t)
	before := ok(t, home, "export", "/work/api").Stdout
	api := treeAt(home, "@work", "@api")
	mkdir(t, api, "node_modules", "@types", "x")
	for _, f := range []string{filepath.Join(api, "docs.md"), filepath.Join(api, "node_modules", "@types", "x", "index.d.ts")} {
		if err := os.WriteFile(f, []byte("material"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"@notes.md", "@5.md", "@start.md"} {
		if err := os.Remove(filepath.Join(api, f)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if after := ok(t, home, "export", "/work/api").Stdout; after != before {
		t.Errorf("material or missing notes changed the export:\n%s\n%s", before, after)
	}
}

// The folder is found as context finds it: through the nearest marker by
// --file or the working directory, warning stale-marker for a stale hint;
// a marker that binds nothing is not-found.
func TestExportMarkers(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	ok(t, home, "task", "create", "a", "--folder", "/work/api")
	byRef := ok(t, home, "export", "/work/api").Stdout
	wt := mkdir(t, home, "wt")
	ok(t, home, "marker", "bind", "/work/api", wt)
	if r := ok(t, home, "export", "--file", wt+"/src/x.go"); r.Stdout != byRef {
		t.Errorf("--file: %s", r.Line())
	}
	if r := koanIn(t, home, mkdir(t, wt, "src"), "export"); r.Exit != 0 || r.Stdout != byRef {
		t.Errorf("working directory: %s", r.Line())
	}
	if id := koanIn(t, home, treeAt(home, "@work"), "export").Result()["folder"].(map[string]any)["id"]; fmt.Sprint(id) != "1" {
		t.Errorf("working directory in the tree: %v", id)
	}

	ok(t, home, "folder", "mv", "-T", "/work/api", "/work/web")
	r := koan(t, home, "export", "--file", wt)
	ws := r.Envelope["warnings"].([]any)
	if r.Exit != 0 || len(ws) != 1 || ws[0].(map[string]any)["kind"] != "stale-marker" || r.Stderr != "koan: 1 warning\n" {
		t.Fatalf("stale: exit %d, stderr %q: %s", r.Exit, r.Stderr, r.Line())
	}
	goldenNow(t, r, "export-stale-marker")

	// A copied marker binds nothing.
	copied := mkdir(t, home, "copy", ".koan")
	for _, f := range []string{"marker.json", ".gitignore"} {
		b, err := os.ReadFile(filepath.Join(wt, ".koan", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(copied, f), b, 0o644)
	}
	r = fails(t, home, "not-found", "", "export", "--file", filepath.Dir(copied))
	if p := errorPaths(r); len(p) != 1 || p[0] != filepath.Join(copied, "marker.json") {
		t.Errorf("copied: %s", r.Line())
	}

	// Its folder deleted, or archived.
	dir := mkdir(t, home, "gone")
	ok(t, home, "folder", "create", "/gone") // 4
	ok(t, home, "marker", "bind", "/gone", dir)
	ok(t, home, "folder", "delete", "/gone")
	r = fails(t, home, "not-found", "", "export", "--file", dir)
	if fmt.Sprint(r.Error()["ids"]) != "[4]" || len(r.Error()["archived"].([]any)) != 0 {
		t.Errorf("deleted: %s", r.Line())
	}
	ok(t, home, "folder", "create", "/done") // 5
	ok(t, home, "marker", "bind", "/done", dir)
	ok(t, home, "archive", "/done")
	r = fails(t, home, "not-found", "", "export", "--file", dir)
	if fmt.Sprint(r.Error()["ids"]) != "[5]" || len(r.Error()["archived"].([]any)) != 1 {
		t.Errorf("archived: %s", r.Line())
	}
}

// What export refuses: what any folder subject refuses.
func TestExportRefusals(t *testing.T) {
	home := exportTree(t)
	ok(t, home, "folder", "create", "/old") // 12
	ok(t, home, "archive", "/old")
	r := fails(t, home, "not-found", "", "export", "@12")
	golden(t, r, "export-archived")
	if a := r.Error()["archived"].([]any); len(a) != 1 {
		t.Errorf("archived: %s", r.Line())
	}
	fails(t, home, "not-found", "", "export", "5")
	fails(t, home, "not-found", "", "export", "/nope")
	fails(t, home, "invalid-input", "", "export", "/work/api.done")
	fails(t, home, "invalid-input", "", "export", "/work/api:docs")
	fails(t, home, "invalid-input", "", "export", "/work", "--file", treeAt(home, "@work"))
	fails(t, home, "conflict", "case-mismatch", "export", "/WORK")
	fails(t, home, "conflict", "case-mismatch", "export", "--file", treeAt(home, "@Work"))

	in := func(s string) *testrun.Result {
		r := testrun.Run{Home: home, Stdin: strings.NewReader(s)}.Koan(t, "export", "-i", "-")
		r.Check(t, "export")
		return r
	}
	if r := in(`{}`); r.Kind() != "invalid-input" {
		t.Errorf("{}: %s", r.Line())
	}
	if r := in(`{"folder":"/work","file":"/x"}`); r.Kind() != "invalid-input" {
		t.Errorf("both: %s", r.Line())
	}
	if r := in(`{"folder":"/work/api"}`); !r.OK() || r.Stdout != ok(t, home, "export", "/work/api").Stdout {
		t.Errorf("--input: %s", r.Line())
	}
}

// With a write holding the lock, a notes path a pending step is making is
// pending-filesystem, an external task's as well as one under the folder.
func TestExportPending(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/w")                   // 1
	ok(t, home, "task", "create", "a", "--folder", "/w")    // 2
	ok(t, home, "task", "create", "b", "--blocked-by", "2") // 3, in the root
	db(t, home, `INSERT INTO pending (seq, change, step) VALUES (1, 1, '{"op":"write","to":"tree/@3.md"}')`)
	lock, e := store.TakeLock(filepath.Join(home, "koan-root"), filepath.Join(home, "koan-root"))
	if e != nil {
		t.Fatal(e.Message)
	}
	defer lock.Release()
	r := koan(t, home, "export", "/w")
	ws := r.Envelope["warnings"].([]any)
	if r.Exit != 0 || len(ws) != 1 || ws[0].(map[string]any)["kind"] != "pending-filesystem" || fmt.Sprint(ws[0].(map[string]any)["ids"]) != "[3]" {
		t.Errorf("pending: %s", r.Line())
	}
}
