package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// realDir is a temp directory given by its real path, so that a path
// through it and its real path, such as marker.json's dir, read alike on
// every platform: macOS's temp directories sit behind a symlink.
func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// realRoot is an initialized root under a HOME that is its own real path.
func realRoot(t *testing.T) string {
	t.Helper()
	home := realDir(t)
	if r := (testrun.Run{Home: home}).Koan(t, "init"); r.Exit != 0 {
		t.Fatalf("init: %s", r.Line())
	}
	return home
}

// koanIn runs a command in home with its working directory at dir.
func koanIn(t *testing.T, home, dir string, args ...string) *testrun.Result {
	t.Helper()
	r := testrun.Run{Home: home, Dir: dir}.Koan(t, args...)
	name := args[0]
	if len(args) > 1 && (name == "folder" || name == "task" || name == "marker") {
		name += " " + args[1]
	}
	r.Check(t, name)
	return r
}

// mkdir makes a directory and those above it.
func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// treeAt is a path in a root's tree.
func treeAt(home string, parts ...string) string {
	return filepath.Join(append([]string{home, "koan-root", "tree"}, parts...)...)
}

// location is a where result's fields, written short for comparing:
// kind, id, ref, folder id and path, gate, material, file, marker.
func location(r *testrun.Result) string {
	l := r.Result()
	f := l["folder"].(map[string]any)
	return fmt.Sprintf("%v %v %v %v:%v %v %v %v %v",
		l["kind"], l["id"], l["ref"], f["id"], f["path"], l["gate"], l["material"], l["file"], l["marker"])
}

// errorPaths is a failure's paths.
func errorPaths(r *testrun.Result) []any {
	p, _ := r.Error()["paths"].([]any)
	return p
}

// where with a reference says what it names both ways: a folder, a
// task, a gate, the root, and material in a folder or the root.
func TestWhereRef(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api")
	ok(t, home, "task", "create", "a", "--folder", "/work/api")
	ok(t, home, "task", "create", "b", "--blocked-by", "/work/api.start")
	ok(t, home, "task", "create", "c")
	tree := treeAt(home)
	for _, c := range []struct{ ref, want string }{
		{"/", "root <nil> / <nil>:/ <nil> <nil> " + tree + " <nil>"},
		{"@2", "folder 2 @2 (/work/api) 2:/work/api <nil> <nil> " + tree + "/@work/@api <nil>"},
		{"/work/api", "folder 2 @2 (/work/api) 2:/work/api <nil> <nil> " + tree + "/@work/@api <nil>"},
		{"1/api", "folder 2 @2 (/work/api) 2:/work/api <nil> <nil> " + tree + "/@work/@api <nil>"},
		{"3", "task 3 @3 (/work/api) 2:/work/api <nil> <nil> " + tree + "/@work/@api/@3.md <nil>"},
		{"@2.start", "task 5 @5 (/work/api.start) 2:/work/api start <nil> " + tree + "/@work/@api/@start.md <nil>"},
		{"@5", "task 5 @5 (/work/api.start) 2:/work/api start <nil> " + tree + "/@work/@api/@start.md <nil>"},
		{"@6", "task 6 @6 (/) <nil>:/ <nil> <nil> " + tree + "/@6.md <nil>"},
		{"/work/api:docs/plan.md", "material 2 @2:docs/plan.md (/work/api:docs/plan.md) 2:/work/api <nil> docs/plan.md " + tree + "/@work/@api/docs/plan.md <nil>"},
		{"@2:node_modules/@types/x", "material 2 @2:node_modules/@types/x (/work/api:node_modules/@types/x) 2:/work/api <nil> node_modules/@types/x " + tree + "/@work/@api/node_modules/@types/x <nil>"},
		{"/:x.md", "material <nil> /:x.md <nil>:/ <nil> x.md " + tree + "/x.md <nil>"},
	} {
		if got := location(ok(t, home, "where", c.ref)); got != c.want {
			t.Errorf("where %s:\n got %s\nwant %s", c.ref, got, c.want)
		}
	}
	golden(t, ok(t, home, "where", "@3"), "where-task")
	golden(t, ok(t, home, "where", "/work/api:docs/plan.md"), "where-material")

	golden(t, fails(t, home, "not-found", "", "where", "@99"), "where-not-found")
	fails(t, home, "not-found", "", "where", "/work/api.done")
	golden(t, fails(t, home, "conflict", "case-mismatch", "where", "/Work/api"), "where-case-mismatch")
	fails(t, home, "invalid-input", "", "where", "@3.start")
	fails(t, home, "invalid-input", "", "where", "@3:x")
	fails(t, home, "invalid-input", "", "where", "$a")

	// An archived ID is not-found, with where it went.
	ok(t, home, "task", "complete", "6")
	ok(t, home, "task", "archive", "6")
	r := fails(t, home, "not-found", "", "where", "@6")
	golden(t, r, "where-archived")
	if a := r.Error()["archived"].([]any); len(a) != 1 {
		t.Errorf("archived: %s", r.Line())
	}
}

// where with a path in the tree names what koan keeps there, the path
// given as file; anything else there is not-found, and a directory in
// another case than its folder's name is case-mismatch. The path need
// not exist.
func TestWhereTreePath(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api")
	ok(t, home, "task", "create", "a", "--folder", "/work") // 3
	ok(t, home, "task", "create", "b", "--folder", "/work/api", "--blocked-by", "/work.done")
	ok(t, home, "task", "create", "c") // 6
	tree := treeAt(home)
	w := tree + "/@work"
	for _, c := range []struct{ file, want string }{
		{tree, "root <nil> / <nil>:/ <nil> <nil> " + tree + " <nil>"},
		{tree + "/", "root <nil> / <nil>:/ <nil> <nil> " + tree + " <nil>"},
		{w, "folder 1 @1 (/work) 1:/work <nil> <nil> " + w + " <nil>"},
		{w + "/@api/../", "folder 1 @1 (/work) 1:/work <nil> <nil> " + w + " <nil>"},
		{w + "/@notes.md", "folder 1 @1 (/work) 1:/work <nil> <nil> " + w + "/@notes.md <nil>"},
		{w + "/@3.md", "task 3 @3 (/work) 1:/work <nil> <nil> " + w + "/@3.md <nil>"},
		{w + "/@done.md", "task 5 @5 (/work.done) 1:/work done <nil> " + w + "/@done.md <nil>"},
		{tree + "/@6.md", "task 6 @6 (/) <nil>:/ <nil> <nil> " + tree + "/@6.md <nil>"},
		{w + "/@.tmp-12", "folder 1 @1 (/work) 1:/work <nil> <nil> " + w + "/@.tmp-12 <nil>"},
		{w + "/@api/@.case-3", "folder 2 @2 (/work/api) 2:/work/api <nil> <nil> " + w + "/@api/@.case-3 <nil>"},
		{tree + "/@.tmp-1", "root <nil> / <nil>:/ <nil> <nil> " + tree + "/@.tmp-1 <nil>"},
		{w + "/docs/plan.md", "material 1 @1:docs/plan.md (/work:docs/plan.md) 1:/work <nil> docs/plan.md " + w + "/docs/plan.md <nil>"},
		{w + "/node_modules/@types/x", "material 1 @1:node_modules/@types/x (/work:node_modules/@types/x) 1:/work <nil> node_modules/@types/x " + w + "/node_modules/@types/x <nil>"},
		{w + "/.DS_Store", "material 1 @1:.DS_Store (/work:.DS_Store) 1:/work <nil> .DS_Store " + w + "/.DS_Store <nil>"},
		{tree + "/x.md", "material <nil> /:x.md <nil>:/ <nil> x.md " + tree + "/x.md <nil>"},
	} {
		if got := location(ok(t, home, "where", "--file", c.file)); got != c.want {
			t.Errorf("where --file %s:\n got %s\nwant %s", c.file, got, c.want)
		}
	}
	golden(t, ok(t, home, "where", "--file", w+"/@notes.md"), "where-tree-path")

	for _, file := range []string{
		w + "/@6.md",         // its task's notes don't belong here
		w + "/@4.md",         // a gate's ID
		w + "/@1.md",         // a folder's ID
		w + "/@99.md",        // never issued
		w + "/@start.md",     // no start gate
		w + "/@api/@done.md", // no done gate
		tree + "/@notes.md",  // the root has no notes
		w + "/@Notes.md",     // a stray @ entry
		w + "/@nope",         // an @ directory that is no folder
		w + "/@nope/docs",    // and under it
		w + "/@notes.md/x",   // below a file koan keeps
		w + "/@3.md/x",       // and a task's notes
		w + "/@.tmp-012",     // not a seq as koan writes one
		w + "/@.tmp-x",       // nor this
		w + "/@.case-3/docs", // below a temporary name
		w + "/@",             // an @ name koan doesn't have
	} {
		r := fails(t, home, "not-found", "", "where", "--file", file)
		if p := errorPaths(r); len(p) != 1 || p[0] != file {
			t.Errorf("where --file %s: %s", file, r.Line())
		}
	}
	golden(t, fails(t, home, "not-found", "", "where", "--file", w+"/@Notes.md"), "where-tree-not-found")

	r := fails(t, home, "conflict", "case-mismatch", "where", "--file", w+"/@API/docs")
	golden(t, r, "where-tree-case-mismatch")
	if p := errorPaths(r); len(p) != 2 || p[0] != w+"/@API" || p[1] != w+"/@api" {
		t.Errorf("case-mismatch paths: %s", r.Line())
	}

	// Relative to the working directory, and with a working directory in
	// the tree.
	if got := location(koanIn(t, home, treeAt(home, "@work", "@api"), "where", "--file", "../@notes.md")); !strings.HasPrefix(got, "folder 1 ") ||
		!strings.Contains(got, " "+w+"/@notes.md ") {
		t.Errorf("relative: %s", got)
	}
	if got := location(koanIn(t, home, treeAt(home, "@work"), "where", "--file", ".")); !strings.HasPrefix(got, "folder 1 ") {
		t.Errorf("dot: %s", got)
	}
}

// A path under ~/koan-root but outside the tree is not-found; one in
// archived/ comes with archived filled in for the nearest name on the
// path, from the item upward, that carries an ID.
func TestWhereRootPath(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api")
	ok(t, home, "task", "create", "a", "--folder", "/work/api") // 3
	ok(t, home, "task", "create", "b")                          // 4
	ok(t, home, "task", "complete", "3")
	ok(t, home, "task", "complete", "4")
	ok(t, home, "task", "archive", "4")
	ok(t, home, "folder", "archive", "/work/api")
	root := filepath.Join(home, "koan-root")

	for _, file := range []string{root, root + "/koan.db", root + "/elsewhere/x", root + "/archived", root + "/archived/work"} {
		r := fails(t, home, "not-found", "", "where", "--file", file)
		if p := errorPaths(r); len(p) != 1 || p[0] != file || len(r.Error()["archived"].([]any)) != 0 {
			t.Errorf("where --file %s: %s", file, r.Line())
		}
	}
	golden(t, fails(t, home, "not-found", "", "where", "--file", root+"/koan.db"), "where-in-root")

	api := root + "/archived/work/@2_api"
	for _, c := range []struct {
		file string
		id   int64
		at   string
	}{
		{root + "/archived/@4.md", 4, root + "/archived/@4.md"},
		{root + "/archived/@4.json", 4, root + "/archived/@4.md"},
		{api, 2, api},
		{api + ".json", 2, api},
		{api + "/@notes.md", 2, api},
		{api + "/docs/plan.md", 2, api},
		{api + "/@3.md", 3, api + "/@3.md"},
		{api + "/@3.md/x", 3, api + "/@3.md"},
	} {
		r := fails(t, home, "not-found", "", "where", "--file", c.file)
		a := r.Error()["archived"].([]any)
		if p := errorPaths(r); len(p) != 1 || p[0] != c.file || len(a) != 1 ||
			fmt.Sprint(a[0].(map[string]any)["id"]) != fmt.Sprint(c.id) || a[0].(map[string]any)["archive"] != c.at {
			t.Errorf("where --file %s: %s", c.file, r.Line())
		}
	}
	golden(t, fails(t, home, "not-found", "", "where", "--file", api+"/@notes.md"), "where-archived-path")
}

// A path outside the tree is in the folder of the nearest marker at or
// above it; with none, it is not-found.
func TestWhereMarker(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api")
	ok(t, home, "folder", "create", "/home")
	wt := mkdir(t, home, "code", "wt")
	inner := mkdir(t, wt, "sub", "inner")
	ok(t, home, "marker", "bind", "/work/api", "--dir", wt)
	ok(t, home, "marker", "bind", "/home", "--dir", inner)

	file := wt + "/src/main.go"
	r := ok(t, home, "where", "--file", file)
	if got, want := location(r), "folder 2 @2 (/work/api) 2:/work/api <nil> <nil> "+file+" "+wt+"/.koan/marker.json"; got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	golden(t, r, "where-marker")
	// At the marker's own directory, and through the working directory.
	if got := location(ok(t, home, "where", "--file", wt)); !strings.HasPrefix(got, "folder 2 ") {
		t.Errorf("at the marker: %s", got)
	}
	if got := location(koanIn(t, home, mkdir(t, wt, "src"), "where", "--file", "main.go")); !strings.Contains(got, " "+file+" ") {
		t.Errorf("relative: %s", got)
	}
	// The nearest marker wins.
	if got := location(ok(t, home, "where", "--file", inner+"/x")); !strings.HasPrefix(got, "folder 3 ") {
		t.Errorf("nearest: %s", got)
	}
	// A .koan/ with no marker.json is passed over.
	mkdir(t, wt, "sub", ".koan")
	if got := location(ok(t, home, "where", "--file", wt+"/sub/x")); !strings.HasPrefix(got, "folder 2 ") {
		t.Errorf("no marker.json: %s", got)
	}
	// A .koan that is a file is no marker either.
	os.WriteFile(filepath.Join(wt, "src", ".koan"), []byte("x"), 0o644)
	if got := location(ok(t, home, "where", "--file", file)); !strings.HasPrefix(got, "folder 2 ") {
		t.Errorf(".koan file: %s", got)
	}

	// The look stops at the first marker.json, whatever its state.
	os.WriteFile(markerFile(inner), []byte("not json"), 0o644)
	r = fails(t, home, "not-found", "", "where", "--file", inner+"/x")
	if p := errorPaths(r); len(p) != 1 || p[0] != markerFile(inner) || len(r.Error()["ids"].([]any)) != 0 {
		t.Errorf("stops: %s", r.Line())
	}
	golden(t, r, "where-marker-not-json")

	// With no marker above, not-found.
	out := mkdir(t, home, "other")
	r = fails(t, home, "not-found", "", "where", "--file", out+"/x")
	if p := errorPaths(r); len(p) != 1 || p[0] != out+"/x" {
		t.Errorf("no marker: %s", r.Line())
	}
	golden(t, r, "where-no-marker")
}

// Every marker that binds nothing is treated alike: not-found, saying
// why, with the marker.json in paths, and the look stops there.
func TestWhereMarkerBindsNothing(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api")
	ok(t, home, "folder", "create", "/gone")    // 3
	ok(t, home, "folder", "create", "/archive") // 4
	ok(t, home, "task", "create", "a")          // 5
	outer := mkdir(t, home, "code")
	ok(t, home, "marker", "bind", "/work", "--dir", outer)
	wt := mkdir(t, outer, "wt")
	ok(t, home, "marker", "bind", "/work/api", "--dir", wt)
	good, err := os.ReadFile(markerFile(wt))
	if err != nil {
		t.Fatal(err)
	}

	// Copied: its dir isn't where it is.
	cp := mkdir(t, outer, "copy")
	mkdir(t, cp, ".koan")
	os.WriteFile(markerFile(cp), good, 0o644)
	r := fails(t, home, "not-found", "", "where", "--file", cp+"/x")
	golden(t, r, "where-marker-copied")
	// Moved: the same.
	moved := outer + "/moved"
	if err := os.Rename(wt, moved); err != nil {
		t.Fatal(err)
	}
	fails(t, home, "not-found", "", "where", "--file", moved+"/x")
	os.Rename(moved, wt)
	ok(t, home, "where", "--file", wt+"/x")

	dir := mkdir(t, outer, "bad")
	mkdir(t, dir, ".koan")
	marker := markerFile(dir)
	set := func(s string) { os.WriteFile(marker, []byte(s), 0o644) }
	real := fmt.Sprintf("%q", dir)
	for _, c := range []struct {
		name, content string
		ids           string
	}{
		{"where-marker-no-field", `{"schema":1,"id":2,"path":"/work/api"}`, "[]"},
		{"where-marker-unknown-schema", `{"schema":2,"id":2,"path":"/work/api","dir":` + real + `}`, "[]"},
		{"", `{"id":2,"path":"/work/api","dir":` + real + `}`, "[]"},
		{"", `{"schema":"1","id":2,"path":"/work/api","dir":` + real + `}`, "[]"},
		{"", `{"schema":1,"id":"2","path":"/work/api","dir":` + real + `}`, "[]"},
		{"", `{"schema":1,"id":2,"path":"/","dir":` + real + `}`, "[]"},
		{"", `{"schema":1,"id":2,"path":"/work/api","dir":"rel"}`, "[]"},
		{"", `{"schema":1,"id":2,"path":"/work/api","dir":` + fmt.Sprintf("%q", dir+"/") + `}`, "[]"},
		{"", `[1]`, "[]"},
		{"", ``, "[]"},
		{"where-marker-task", `{"schema":1,"id":5,"path":"/work/api","dir":` + real + `}`, "[5]"},
		{"where-marker-never-issued", `{"schema":1,"id":99,"path":"/work/api","dir":` + real + `}`, "[99]"},
	} {
		set(c.content)
		r := fails(t, home, "not-found", "", "where", "--file", dir+"/x")
		if p := errorPaths(r); len(p) != 1 || p[0] != marker || fmt.Sprint(r.Error()["ids"]) != c.ids {
			t.Errorf("%s: %s", c.content, r.Line())
		}
		if c.name != "" {
			golden(t, r, c.name)
		}
	}
	// Fields it doesn't know are ignored.
	set(`{"schema":1,"id":2,"path":"/work/api","dir":` + real + `,"later":true}`)
	ok(t, home, "where", "--file", dir+"/x")

	// Unreadable.
	if os.Geteuid() != 0 {
		os.Chmod(marker, 0)
		golden(t, fails(t, home, "not-found", "", "where", "--file", dir+"/x"), "where-marker-unreadable")
		os.Chmod(marker, 0o644)
	}

	// Deleted and archived folders: where says which, and where an
	// archived one went, from the index.
	ok(t, home, "marker", "bind", "/gone", "--dir", dir, "--force")
	ok(t, home, "folder", "delete", "/gone")
	r = fails(t, home, "not-found", "", "where", "--file", dir+"/x")
	golden(t, r, "where-marker-deleted")
	if fmt.Sprint(r.Error()["ids"]) != "[3]" || len(r.Error()["archived"].([]any)) != 0 {
		t.Errorf("deleted: %s", r.Line())
	}
	ok(t, home, "marker", "bind", "/archive", "--dir", dir)
	ok(t, home, "folder", "archive", "/archive")
	r = fails(t, home, "not-found", "", "where", "--file", dir+"/x")
	golden(t, r, "where-marker-archived")
	a := r.Error()["archived"].([]any)
	if fmt.Sprint(r.Error()["ids"]) != "[4]" || len(a) != 1 || a[0].(map[string]any)["archive"] != filepath.Join(home, "koan-root", "archived", "@4_archive") {
		t.Errorf("archived: %s", r.Line())
	}
	// Reads never write a marker.
	if b, _ := os.ReadFile(marker); !strings.Contains(string(b), `"id":4`) {
		t.Errorf("marker changed: %s", b)
	}
}

// A marker's path is a hint: the ID decides. A path that no longer
// matches is stale-marker, and binding again refreshes it.
func TestStaleMarker(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api")
	wt := mkdir(t, home, "wt")
	ok(t, home, "marker", "bind", "/work/api", "--dir", wt)
	ok(t, home, "folder", "move", "/work/api", "--to", "/work/web")
	r := koan(t, home, "where", "--file", wt)
	if r.Exit != 0 || r.Stderr != "koan: 1 warning\n" || r.Result()["ref"] != "@2 (/work/web)" {
		t.Fatalf("exit %d, stderr %q: %s", r.Exit, r.Stderr, r.Line())
	}
	golden(t, r, "where-stale-marker")
	r = koan(t, home, "context", "--file", wt)
	ws := r.Envelope["warnings"].([]any)
	if r.Exit != 0 || len(ws) != 1 || ws[0].(map[string]any)["kind"] != "stale-marker" {
		t.Errorf("context: %s", r.Line())
	}
	ok(t, home, "marker", "bind", "/work/web", "--dir", wt)
	ok(t, home, "where", "--file", wt)
}

// Inside the tree markers are never looked at, and a path reaching the
// tree by a second route, such as a symlink, is judged by its real path,
// though paths printed stay under ~/koan-root as written.
func TestWhereRealPath(t *testing.T) {
	home, elsewhere := realDir(t), realDir(t)
	if err := os.Symlink(elsewhere, filepath.Join(home, "koan-root")); err != nil {
		t.Fatal(err)
	}
	if r := (testrun.Run{Home: home}).Koan(t, "init"); r.Exit != 0 {
		t.Fatalf("init: %s", r.Line())
	}
	ok(t, home, "folder", "create", "/work")
	w := treeAt(home, "@work")
	for _, file := range []string{elsewhere + "/tree/@work/@notes.md", w + "/@notes.md"} {
		if got, want := location(ok(t, home, "where", "--file", file)), "folder 1 @1 (/work) 1:/work <nil> <nil> "+w+"/@notes.md <nil>"; got != want {
			t.Errorf("where --file %s:\n got %s\nwant %s", file, got, want)
		}
	}
	// A symlink outside, into the tree, is the tree: its marker is never
	// looked at.
	link := filepath.Join(home, "link")
	os.Symlink(w, link)
	mkdir(t, home, ".koan")
	os.WriteFile(markerFile(home), []byte("not json"), 0o644)
	if got := location(ok(t, home, "where", "--file", link+"/@notes.md")); !strings.Contains(got, " "+w+"/@notes.md ") {
		t.Errorf("through a link: %s", got)
	}
	r := fails(t, home, "not-found", "", "where", "--file", elsewhere+"/koan.db")
	if p := errorPaths(r); len(p) != 1 || p[0] != filepath.Join(home, "koan-root", "koan.db") {
		t.Errorf("elsewhere in the root: %s", r.Line())
	}
	// A path through a symlinked directory outside the root, as macOS's
	// /tmp is, is printed as given, and its marker binds.
	os.Remove(markerFile(home))
	wt := mkdir(t, home, "wt")
	via := filepath.Join(home, "via")
	os.Symlink(wt, via)
	ok(t, home, "marker", "bind", "/work", "--dir", wt)
	if got, want := location(ok(t, home, "where", "--file", via+"/x")), "folder 1 @1 (/work) 1:/work <nil> <nil> "+via+"/x "+via+"/.koan/marker.json"; got != want {
		t.Errorf("via:\n got %s\nwant %s", got, want)
	}
}

// context briefs an agent on a folder: its record, the folders above it,
// its notes and its own open tasks, gates included, in tree order.
func TestContext(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api", "--notes", "API notes")
	ok(t, home, "task", "create", "a", "--folder", "/work/api")                                    // 3
	ok(t, home, "task", "create", "b", "--folder", "/work/api", "--blocked-by", "/work/api.start") // 4, gate 5
	ok(t, home, "task", "create", "c", "--folder", "/work/api")                                    // 6
	ok(t, home, "folder", "create", "/work/api/sub")                                               // 7
	ok(t, home, "task", "create", "d", "--folder", "/work/api/sub")                                // 8
	ok(t, home, "task", "create", "e")                                                             // 9
	ok(t, home, "task", "complete", "6")

	r := ok(t, home, "context", "/work/api")
	goldenNow(t, r, "context")
	b := r.Result()
	if b["notes"] != "API notes" || b["marker"] != nil {
		t.Errorf("brief: %s", r.Line())
	}
	if got := ids(b["tasks"]); got != "[3 4 5]" {
		t.Errorf("tasks %s", got)
	}
	if got := ids(b["ancestors"]); got != "[1]" {
		t.Errorf("ancestors %s", got)
	}
	if got := ids(ok(t, home, "context", "@7").Result()["ancestors"]); got != "[1 2]" {
		t.Errorf("sub ancestors %s", got)
	}

	// The root: no notes, nothing above it.
	r = ok(t, home, "context", "/")
	if b := r.Result(); b["notes"] != nil || ids(b["ancestors"]) != "[]" || ids(b["tasks"]) != "[9]" {
		t.Errorf("root: %s", r.Line())
	}

	// Paths resolve as for where: a task's notes, material and a marker
	// give the folder they are in.
	api := treeAt(home, "@work", "@api")
	for _, file := range []string{api, api + "/@3.md", api + "/@start.md", api + "/docs/x"} {
		if id := ok(t, home, "context", "--file", file).Result()["folder"].(map[string]any)["id"]; fmt.Sprint(id) != "2" {
			t.Errorf("context --file %s: %v", file, id)
		}
	}
	if id := ok(t, home, "context", "--file", treeAt(home, "x")).Result()["folder"].(map[string]any)["id"]; id != nil {
		t.Errorf("root material: %v", id)
	}
	wt := mkdir(t, home, "wt")
	ok(t, home, "marker", "bind", "/work/api", "--dir", wt)
	r = ok(t, home, "context", "--file", wt+"/src")
	if r.Result()["marker"] != markerFile(wt) {
		t.Errorf("marker: %s", r.Line())
	}
	goldenNow(t, r, "context-marker")
	// With no subject, the working directory.
	r = koanIn(t, home, mkdir(t, wt, "src"), "context")
	if r.Exit != 0 || r.Result()["marker"] != markerFile(wt) {
		t.Errorf("cwd: %s", r.Line())
	}
	if id := koanIn(t, home, treeAt(home, "@work"), "context").Result()["folder"].(map[string]any)["id"]; fmt.Sprint(id) != "1" {
		t.Errorf("cwd in the tree: %v", id)
	}

	fails(t, home, "not-found", "", "context", "@3")
	fails(t, home, "not-found", "", "context", "--file", mkdir(t, home, "none"))
	fails(t, home, "invalid-input", "", "context", "/work.start")
	fails(t, home, "conflict", "case-mismatch", "context", "/WORK")
	fails(t, home, "conflict", "case-mismatch", "context", "--file", treeAt(home, "@Work"))
}

// ids is the ids of a list of records.
func ids(v any) string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, fmt.Sprint(x.(map[string]any)["id"]))
	}
	return "[" + strings.Join(out, " ") + "]"
}

// A folder whose @notes.md is missing briefs empty notes, and warns,
// unless a pending step is still to make it.
func TestContextMissingNotes(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work")
	os.Remove(treeAt(home, "@work", "@notes.md"))
	os.WriteFile(treeAt(home, "@work", "@Notes.md"), []byte("by hand"), 0o644)
	r := koan(t, home, "context", "/work")
	if r.Exit != 0 || r.Stderr != "koan: 1 warning\n" || r.Result()["notes"] != "" {
		t.Errorf("exit %d, stderr %q: %s", r.Exit, r.Stderr, r.Line())
	}
	goldenNow(t, r, "context-missing-notes")
	ok(t, home, "context", "/")

	db(t, home,
		"INSERT INTO issued (id, kind, state) VALUES (2, 'folder', 'live')",
		"INSERT INTO folder (id, parent, name, extra) VALUES (2, 1, 'api', '{}')",
		`INSERT INTO pending (seq, change, step) VALUES (1, 1, '{"op":"mkdir","to":"tree/@work/@api"}')`)
	r = koan(t, home, "context", "/work/api")
	ws := r.Envelope["warnings"].([]any)
	if r.Exit != 0 || len(ws) != 1 || ws[0].(map[string]any)["kind"] != "pending-step" {
		t.Errorf("pending: %s", r.Line())
	}
}

// The command-line conveniences: --file relative to the working
// directory, context's working directory with no subject. Through
// --input, what they stand for is given instead, and a working directory
// koan can't resolve is io, before any fault in the input's shape.
func TestLocateConveniences(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work")
	w := treeAt(home, "@work")

	golden(t, fails(t, home, "invalid-input", "", "where"), "where-neither")
	r := fails(t, home, "invalid-input", "", "where", "/work", "--file", w)
	golden(t, r, "where-both")
	if p := r.Error()["problems"].([]any); len(p) != 1 || p[0].(map[string]any)["field"] != "" {
		t.Errorf("both: %s", r.Line())
	}
	fails(t, home, "invalid-input", "", "context", "/work", "--file", w)
	for _, args := range [][]string{
		{"where", "--file", w, "--file", w},
		{"where", "--file"},
		{"where", "--input", "-", "--file", w},
		{"context", "--file", w, "-i", "-"},
		{"context", "-i", "-", "/work"},
		{"where", "--dir", w},
	} {
		if r := koan(t, home, args...); r.Exit != 2 || r.Kind() != "usage" {
			t.Errorf("%q: exit %d: %s", args, r.Exit, r.Line())
		}
	}

	// Through --input, a path must be absolute, and context takes no
	// working directory.
	in := func(args ...string) func(s string) *testrun.Result {
		return func(s string) *testrun.Result {
			r := testrun.Run{Home: home, Dir: w, Stdin: strings.NewReader(s)}.Koan(t, append(args, "-i", "-")...)
			r.Check(t, args[0])
			return r
		}
	}
	where, context := in("where"), in("context")
	if r := where(`{"file":"@notes.md"}`); r.Kind() != "invalid-input" || r.Error()["problems"].([]any)[0].(map[string]any)["field"] != "/file" {
		t.Errorf("relative through --input: %s", r.Line())
	}
	if r := where(`{"file":"` + w + `"}`); !r.OK() {
		t.Errorf("--input file: %s", r.Line())
	}
	if r := context(`{}`); r.Kind() != "invalid-input" {
		t.Errorf("context {}: %s", r.Line())
	}
	if r := context(`{"file":"` + w + `"}`); !r.OK() {
		t.Errorf("context --input: %s", r.Line())
	}
	// An empty --file is no path at all, as through --input.
	fails(t, home, "invalid-input", "", "where", "--file", "")

	// --file made absolute: given as relative or absolute alike.
	for _, f := range []string{"@notes.md", "./@notes.md", w + "/@notes.md", w + "//@notes.md"} {
		if got := koanIn(t, home, w, "where", "--file", f).Result()["file"]; got != w+"/@notes.md" {
			t.Errorf("--file %s: %v", f, got)
		}
	}
}

// A working directory koan can't resolve is io with no path, read as the
// command line becomes an input: before the input's shape.
func TestUnresolvableWorkingDirectory(t *testing.T) {
	home := realRoot(t)
	gone := mkdir(t, home, "gone")
	run := func(args ...string) *testrun.Result {
		t.Helper()
		// The shell leaves koan in a directory removed under it.
		script := `cd "$1" && rmdir "$1" && shift && exec "$@"`
		cmd := exec.Command("sh", append([]string{"-c", script, "sh", gone, testrun.Binary(t)}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, _ := cmd.Output()
		os.MkdirAll(gone, 0o755)
		line := strings.TrimSuffix(string(out), "\n")
		r := &testrun.Result{Args: args, Home: home, Stdout: string(out)}
		if err := testrun.Load(t).Validate("envelope", "", line); err != nil {
			t.Fatalf("%q: %v: %s", args, err, line)
		}
		return r
	}
	for _, args := range [][]string{
		{"context"},
		{"where", "--file", "x"},
		{"where", "@1.start:x", "--file", "x"},
		{"marker", "bind", "/x:y"},
		{"marker", "unbind"},
		{"marker", "unbind", "--dir", "x"},
	} {
		r := run(args...)
		if !strings.Contains(r.Stdout, `"kind":"io"`) || !strings.Contains(r.Stdout, `"paths":[]`) {
			if strings.Contains(r.Stdout, `"ok":true`) {
				t.Skip("this system resolves a removed working directory")
			}
			t.Errorf("%q: %s", args, r.Stdout)
		}
	}
	// An absolute --file needs no working directory.
	if r := run("where", "--file", treeAt(home)); !strings.Contains(r.Stdout, `"ok":true`) {
		t.Errorf("absolute: %s", r.Stdout)
	}
}
