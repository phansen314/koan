package main_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
	"github.com/phansen314/koan/internal/testrun"
)

// root is an initialized root under a fresh HOME.
func root(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if r := (testrun.Run{Home: home}).Koan(t, "init"); r.Exit != 0 {
		t.Fatalf("init: %s", r.Line())
	}
	return home
}

// koan runs a command in home and checks its output against its schemas.
func koan(t *testing.T, home string, args ...string) *testrun.Result {
	t.Helper()
	r := testrun.Run{Home: home}.Koan(t, args...)
	r.Check(t, commandName(args))
	return r
}

// commandName is the command a line names: a group and its command, or
// one word.
func commandName(args []string) string {
	name := args[0]
	if len(args) > 1 && (name == "folder" || name == "task") {
		name += " " + args[1]
	}
	return name
}

// ok runs a command that must succeed with no warnings.
func ok(t *testing.T, home string, args ...string) *testrun.Result {
	t.Helper()
	r := koan(t, home, args...)
	if r.Exit != 0 || !r.OK() || len(r.Envelope["warnings"].([]any)) != 0 || r.Stderr != "" {
		t.Fatalf("koan %q: exit %d, stderr %q: %s", args, r.Exit, r.Stderr, r.Line())
	}
	return r
}

// fails runs a command that must fail with kind, and rule for a conflict.
func fails(t *testing.T, home, kind, rule string, args ...string) *testrun.Result {
	t.Helper()
	r := koan(t, home, args...)
	got, _ := r.Error()["rule"].(string)
	if r.Exit != 1 || r.Kind() != kind || got != rule {
		t.Fatalf("koan %q: exit %d, want %s %s: %s", args, r.Exit, kind, rule, r.Line())
	}
	return r
}

// tree is every entry under the tree's directory, directories ending in
// /, files with their content after =.
func tree(t *testing.T, home string) []string {
	t.Helper()
	dir := filepath.Join(home, "koan-root", "tree")
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out = append(out, rel+"/")
			return nil
		}
		b, err := os.ReadFile(p)
		out = append(out, rel+"="+string(b))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func checkTree(t *testing.T, home string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := tree(t, home); !slices.Equal(got, want) {
		t.Errorf("tree:\n got %q\nwant %q", got, want)
	}
}

// db runs statements against a root's database, for state no command in
// this slice can make: tasks, gates, edges and pending rows.
func db(t *testing.T, home string, stmts ...string) {
	t.Helper()
	d, err := sql.Open("sqlite", filepath.Join(home, "koan-root", "koan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// task inserts a task row, with gate "" for an ordinary task and folder 0
// for the root.
func task(id, folder int64, gate string, complete bool) []string {
	f, g, c := "NULL", "NULL", "NULL"
	if folder != 0 {
		f = fmt.Sprint(folder)
	}
	if gate != "" {
		g = "'" + gate + "'"
	}
	if complete {
		c = "'2026-10-20T10:00:00Z'"
	}
	return []string{
		fmt.Sprintf("INSERT INTO issued (id, kind, state) VALUES (%d, 'task', 'live')", id),
		fmt.Sprintf(`INSERT INTO task (id, folder, gate, title, priority, created_at, completed_at, updated_at, extra)
			VALUES (%d, %s, %s, 'T', NULL, '2026-10-20T10:00:00Z', %s, '2026-10-20T10:00:00Z', '{}')`, id, f, g, c),
	}
}

func golden(t *testing.T, r *testrun.Result, name string) {
	t.Helper()
	r.Golden(t, name, *update, nil)
}

// folder create makes the folder, its directory and its notes, and each
// missing parent the same way with no title, parents first; IDs are
// issued in that order.
func TestFolderCreate(t *testing.T) {
	home := root(t)
	r := ok(t, home, "folder", "create", "/work/api", "--title", "  API  ", "--extra", `{"team":"<core>","n":1.50}`, "--notes", "# API\n")
	golden(t, r, "folder-create")
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@api/", "@work/@api/@notes.md=# API\n")

	r = ok(t, home, "show", "/work/api")
	golden(t, r, "show-folder")
	r = ok(t, home, "show", "/work")
	f := r.Result()["folder"].(map[string]any)
	if f["title"] != nil || fmt.Sprint(f["extra"]) != "map[]" {
		t.Errorf("a parent has no title and an empty extra: %s", r.Line())
	}

	// A third level under parents that are there makes only itself.
	r = ok(t, home, "folder", "create", "/work/api/v2")
	if created := r.Result()["folders_made"].([]any); len(created) != 1 || r.Result()["id"] != json.Number("3") {
		t.Errorf("created: %s", r.Line())
	}
	// Through --input, as the options would give it.
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"path":"/home","title":null,"extra":{},"notes":"n"}`)}.Koan(t, "folder", "create", "-i", "-")
	r.Check(t, "folder create")
	if !r.OK() {
		t.Errorf("--input: %s", r.Line())
	}
	if got := tree(t, home); !slices.Contains(got, "@home/@notes.md=n") || !slices.Contains(got, "@work/@api/@v2/@notes.md=") {
		t.Errorf("tree %q", got)
	}
}

// --notes-file gives notes from a file whole, or from standard input; a
// file koan can't read is io with its absolute path; both --notes and
// --notes-file is usage; notes that aren't UTF-8 are invalid-input.
func TestFolderCreateNotesFile(t *testing.T) {
	home := root(t)
	os.WriteFile(filepath.Join(home, "n.md"), []byte("from a file\n"), 0o644)
	ok(t, home, "folder", "create", "/a", "--notes-file", "n.md")
	r := testrun.Run{Home: home, Stdin: strings.NewReader("from stdin")}.Koan(t, "folder", "create", "/b", "--notes-file", "-")
	r.Check(t, "folder create")
	checkTree(t, home, "@a/", "@a/@notes.md=from a file\n", "@b/", "@b/@notes.md=from stdin")

	r = fails(t, home, "io", "", "folder", "create", "/c", "--notes-file", "missing.md")
	if paths := r.Error()["paths"].([]any); len(paths) != 1 || paths[0] != filepath.Join(home, "missing.md") {
		t.Errorf("io paths: %s", r.Line())
	}
	r = koan(t, home, "folder", "create", "/c", "--notes", "x", "--notes-file", "n.md")
	if r.Exit != 2 || r.Kind() != "usage" || r.Error()["problems"].([]any)[0].(map[string]any)["field"] != "" {
		t.Errorf("both: %s", r.Line())
	}

	os.WriteFile(filepath.Join(home, "bad.md"), []byte("caf\xe9"), 0o644)
	r = fails(t, home, "invalid-input", "", "folder", "create", "/c", "--notes-file", "bad.md")
	golden(t, r, "folder-create-not-utf8")
	checkTree(t, home, "@a/", "@a/@notes.md=from a file\n", "@b/", "@b/@notes.md=from stdin")
}

// A bad input is invalid-input, naming every fault, before the root's
// state is looked at.
func TestFolderCreateInvalid(t *testing.T) {
	home := t.TempDir() // no root: the shape is checked first
	r := fails(t, home, "invalid-input", "", "folder", "create", "/work/-bad", "--title", " ", "--extra", "[1]")
	golden(t, r, "folder-create-invalid")
	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"/"}, "/path"},
		{[]string{"work"}, "/path"},
		{[]string{"/work.done"}, "/path"},
		{[]string{"/work:notes"}, "/path"},
		{[]string{"@4/x"}, "/path"},
		{[]string{"/a/" + strings.Repeat("x", 65)}, "/path"},
		{[]string{"/a", "--title", strings.Repeat("t", 201)}, "/title"},
		{[]string{"/a", "--title", "a\nb"}, "/title"},
		{[]string{"/a", "--extra", "{"}, "/extra"},
		{[]string{"/a", "--notes", "caf\xe9"}, "/notes"},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"folder", "create"}, c.args...)...)
		problems := r.Error()["problems"].([]any)
		if len(problems) != 1 || problems[0].(map[string]any)["field"] != c.field {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	for _, in := range []string{`{}`, `{"path":4}`, `{"path":"/a","title":4}`, `{"path":"/a","notes":null}`} {
		r := testrun.Run{Home: home, Stdin: strings.NewReader(in)}.Koan(t, "folder", "create", "-i", "-")
		r.Check(t, "folder create")
		if r.Kind() != "invalid-input" {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	r = fails(t, home, "not-initialized", "", "folder", "create", "/a")
	if _, err := os.Lstat(filepath.Join(home, "koan-root")); !os.IsNotExist(err) {
		t.Errorf("made the root: %v", err)
	}
}

// The path is taken when a folder has it, or when the tree's directory
// holds an entry where the folder's directory would go, spelled the same
// or differing only in case, file or directory; a missing parent's path
// counts too. Nothing is made.
func TestFolderCreateExists(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")
	golden(t, fails(t, home, "conflict", "exists", "folder", "create", "/work"), "folder-create-exists")

	treeDir := filepath.Join(home, "koan-root", "tree")
	os.Mkdir(filepath.Join(treeDir, "@Stray"), 0o755)
	os.WriteFile(filepath.Join(treeDir, "@work", "@api"), []byte("x"), 0o644)
	golden(t, fails(t, home, "conflict", "exists", "folder", "create", "/stray"), "folder-create-exists-on-disk")
	for _, c := range []struct{ path, entry string }{
		{"/stray/a/b", "tree/@Stray"},
		{"/work/api", "tree/@work/@api"},
		{"/work/API", "tree/@work/@api"},
	} {
		r := fails(t, home, "conflict", "exists", "folder", "create", c.path)
		if paths := r.Error()["paths"].([]any); len(paths) != 1 || paths[0] != filepath.Join(home, "koan-root", c.entry) {
			t.Errorf("%s: %s", c.path, r.Line())
		}
	}
	// Material is not in the way: its names never start with @.
	os.Mkdir(filepath.Join(treeDir, "docs"), 0o755)
	ok(t, home, "folder", "create", "/docs")
	r := ok(t, home, "folder", "list", "--fields", "path")
	if fmt.Sprint(r.Result()["folders"]) != "[map[id:2 path:/docs] map[id:1 path:/work]]" {
		t.Errorf("folders: %s", r.Line())
	}
}

// A part that matches a folder only ignoring case is case-mismatch, on
// every platform, and so is a new name beside a sibling's that differs
// only in case. The error names the folder, then the path as given up to
// that part and the path as it is.
func TestFolderCreateCaseMismatch(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/Work/api")
	golden(t, fails(t, home, "conflict", "case-mismatch", "folder", "create", "/work/api/v2"), "folder-create-case-mismatch-parent")
	golden(t, fails(t, home, "conflict", "case-mismatch", "folder", "create", "/Work/API"), "folder-create-case-mismatch")
	checkTree(t, home, "@Work/", "@Work/@notes.md=", "@Work/@api/", "@Work/@api/@notes.md=")
}

// show reads a folder by path, ID or anchored path, or the root: its
// gates, and its tasks counted at any depth, gates included.
func TestFolderShow(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	ok(t, home, "folder", "create", "/work/web") // 3
	var stmts []string
	stmts = append(stmts, task(4, 1, "start", true)...)
	stmts = append(stmts, task(5, 1, "done", false)...)
	stmts = append(stmts, task(6, 2, "", false)...)
	stmts = append(stmts, task(7, 2, "", true)...)
	stmts = append(stmts, task(8, 0, "", false)...)
	stmts = append(stmts, task(9, 3, "start", false)...)
	stmts = append(stmts, "INSERT INTO edge (task, blocker) VALUES (5, 6)")
	db(t, home, stmts...)

	r := ok(t, home, "show", "/work")
	golden(t, r, "show-folder-gates")
	for _, ref := range []string{"1", "@1", "@2/..", "@1/api"} {
		r := koan(t, home, "show", ref)
		if ref == "@2/.." {
			if r.Kind() != "invalid-input" {
				t.Errorf("%s: %s", ref, r.Line())
			}
			continue
		}
		if !r.OK() {
			t.Errorf("%s: %s", ref, r.Line())
		}
	}
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"ref":2}`)}.Koan(t, "show", "-i", "-")
	r.Check(t, "show")
	if r.Result()["folder"].(map[string]any)["path"] != "/work/api" {
		t.Errorf("-i with an integer: %s", r.Line())
	}
	r = ok(t, home, "show", "/")
	golden(t, r, "show-root")
	r = ok(t, home, "show", "/work/web")
	if g := r.Result()["folder"].(map[string]any)["gates"].(map[string]any); fmt.Sprint(g) != "map[done:<nil> start:map[id:9 readiness:ready state:open]]" {
		t.Errorf("gates: %v", g)
	}

	fails(t, home, "not-found", "", "show", "@99")
	fails(t, home, "not-found", "", "show", "/work/nope")
	golden(t, fails(t, home, "conflict", "case-mismatch", "show", "/WORK/api"), "show-folder-case-mismatch")
	r = fails(t, home, "invalid-input", "", "show", "/work:docs")
	if problemFields(r) != "/ref" {
		t.Errorf("material: %s", r.Line())
	}
	// A task's ID, and a folder's end, name tasks.
	for ref, id := range map[string]string{"@6": "6", "/work.start": "4"} {
		r := koan(t, home, "show", ref)
		if task, _ := r.Result()["task"].(map[string]any); task == nil || fmt.Sprint(task["id"]) != id {
			t.Errorf("%s: %s", ref, r.Line())
		}
	}
}

// A folder whose @notes.md is missing reads as empty notes, and warns;
// a hand-made @Notes.md is not its notes. The root has none, and never
// warns.
func TestFolderShowMissingNotes(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")
	dir := filepath.Join(home, "koan-root", "tree", "@work")
	os.Remove(filepath.Join(dir, "@notes.md"))
	os.WriteFile(filepath.Join(dir, "@Notes.md"), []byte("by hand"), 0o644)
	r := koan(t, home, "show", "/work")
	if r.Exit != 0 || r.Stderr != "koan: 1 warning\n" {
		t.Errorf("exit %d, stderr %q", r.Exit, r.Stderr)
	}
	golden(t, r, "show-folder-missing-notes")
	ok(t, home, "show", "/")
	// folder list never looks at notes.
	ok(t, home, "folder", "list")
}

// folder list gives the folders under its subject, itself left out, in tree
// order: a folder before everything under it, siblings by name compared
// byte for byte, so Zeta before alpha.
func TestFolderList(t *testing.T) {
	home := root(t)
	for _, p := range []string{"/alpha/b", "/Zeta", "/alpha/a/x", "/beta", "/alpha/C"} {
		ok(t, home, "folder", "create", p)
	}
	paths := func(r *testrun.Result) string {
		var out []string
		for _, f := range r.Result()["folders"].([]any) {
			out = append(out, f.(map[string]any)["path"].(string))
		}
		return strings.Join(out, " ")
	}
	r := ok(t, home, "folder", "list")
	golden(t, r, "folder-list")
	golden(t, ok(t, home, "folder", "list", "/alpha", "--direct"), "folder-list-subject")
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "/Zeta /alpha /alpha/C /alpha/a /alpha/a/x /alpha/b /beta"},
		{[]string{"--direct"}, "/Zeta /alpha /beta"},
		{[]string{"/alpha"}, "/alpha/C /alpha/a /alpha/a/x /alpha/b"},
		{[]string{"@1", "--direct"}, "/alpha/C /alpha/a /alpha/b"},
		{[]string{"/alpha/a/x"}, ""},
	} {
		r := ok(t, home, append([]string{"folder", "list"}, c.args...)...)
		if got := paths(r); got != c.want {
			t.Errorf("%q: got %q, want %q", c.args, got, c.want)
		}
	}

	r = ok(t, home, "folder", "list", "--limit", "2")
	golden(t, r, "folder-list-truncated")
	r = ok(t, home, "folder", "list", "--limit", "null")
	if r.Result()["total"] != json.Number("7") || r.Result()["truncated"] != false || len(r.Result()["folders"].([]any)) != 7 {
		t.Errorf("--limit null: %s", r.Line())
	}
	r = ok(t, home, "folder", "list", "--limit", "7")
	if r.Result()["truncated"] != false {
		t.Errorf("--limit 7: %s", r.Line())
	}

	// --fields gives id first, then the fields named, in the record's
	// order.
	r = ok(t, home, "folder", "list", "/alpha/a", "--fields", "dir,name", "--fields", "finished")
	golden(t, r, "folder-list-fields")
	r = ok(t, home, "folder", "list", "/alpha/a", "--fields", "id")
	if fmt.Sprint(r.Result()["folders"]) != "[map[id:5]]" {
		t.Errorf("--fields id: %s", r.Line())
	}

	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"--fields", "name,nope"}, "/fields/1"},
		{[]string{"--fields", "name,path,name"}, "/fields/2"},
		{[]string{"--limit", "0"}, "/limit"},
		{[]string{"@3.done"}, "/folder"},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"folder", "list"}, c.args...)...)
		problems := r.Error()["problems"].([]any)
		if len(problems) != 1 || problems[0].(map[string]any)["field"] != c.field {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	fails(t, home, "conflict", "case-mismatch", "folder", "list", "/ALPHA")
	fails(t, home, "not-found", "", "folder", "list", "/gamma")
}

// A change left pending, by a write that crashed after COMMIT, gives
// pending-step on reads; with a write holding the lock, the paths a read
// returns that the steps bring about are pending-filesystem instead; a
// stuck step gives stuck-step on every command.
func TestFolderReadsPending(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")
	db(t, home,
		"INSERT INTO issued (id, kind, state) VALUES (2, 'folder', 'live')",
		"INSERT INTO folder (id, parent, name, extra) VALUES (2, 1, 'api', '{}')",
		`INSERT INTO pending (seq, change, step) VALUES (1, 1, '{"op":"mkdir","to":"tree/@work/@api"}')`)
	golden(t, koan(t, home, "show", "/work/api"), "show-folder-pending-step")

	lock, e := store.TakeLock(filepath.Join(home, "koan-root"), filepath.Join(home, "koan-root"))
	if e != nil {
		t.Fatal(e.Message)
	}
	r := koan(t, home, "show", "/work/api")
	golden(t, r, "show-folder-pending-filesystem")
	r = koan(t, home, "folder", "list", "--fields", "path,dir")
	ws := r.Envelope["warnings"].([]any)
	if len(ws) != 1 || fmt.Sprint(ws[0].(map[string]any)["ids"]) != "[2]" {
		t.Errorf("list: %s", r.Line())
	}
	// A list that returns no path the steps bring about says nothing of
	// them.
	r = koan(t, home, "folder", "list", "--fields", "name")
	lock.Release()
	if len(r.Envelope["warnings"].([]any)) != 0 {
		t.Errorf("no paths: %s", r.Line())
	}

	// The next write finishes the steps.
	ok(t, home, "folder", "create", "/home")
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@api/", "@work/@api/@notes.md=", "@home/", "@home/@notes.md=")
	ok(t, home, "show", "/work/api")

	db(t, home, `INSERT INTO pending (seq, change, step, stuck) VALUES (9, 9, '{"op":"move","from":"tree/@1.md","to":"tree/@work/@1.md","stuck":"both"}', 1)`)
	golden(t, koan(t, home, "show", "/"), "show-folder-stuck-step")
	r = koan(t, home, "folder", "create", "/next")
	if !r.OK() || len(r.Envelope["warnings"].([]any)) != 1 {
		t.Errorf("create beside a stuck step: %s", r.Line())
	}
}

// Once COMMIT succeeds the command succeeds: a step the operating system
// refuses leaves the folder made, with pending-step, and the next write
// finishes it.
func TestFolderCreateStepRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := root(t)
	ok(t, home, "folder", "create", "/work")
	dir := filepath.Join(home, "koan-root", "tree", "@work")
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	r := koan(t, home, "folder", "create", "/work/api")
	if r.Exit != 0 || !r.OK() {
		t.Fatalf("exit %d: %s", r.Exit, r.Line())
	}
	golden(t, r, "folder-create-pending-step")
	golden(t, koan(t, home, "show", "/work/api"), "show-folder-pending-step-refused")
	os.Chmod(dir, 0o755)
	ok(t, home, "folder", "create", "/b")
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@api/", "@work/@api/@notes.md=", "@b/", "@b/@notes.md=")
}

// The model's folder create, run beside koan on the same inputs, agrees
// on what each makes and on each refusal, and folder list's tree order is
// the model's.
func TestFolderCreateModel(t *testing.T) {
	home := root(t)
	m := model.New(1, func() string { return "2026-10-20T10:00:00Z" })
	for _, p := range []string{
		"/a", "/a/b/c", "/A", "/a/B", "/a/b", "/a/b/c", "/x/y", "/X/z", "/x/Y/z", "/a/b/C", "/Zeta", "/zeta2", "/a/a",
	} {
		made, err := m.CreateFolder(p, nil, nil)
		r := koan(t, home, "folder", "create", p)
		if err != nil {
			e := err.(*core.Error)
			got := r.Error()
			rule, _ := got["rule"].(string)
			if r.Kind() != e.Kind || rule != e.Rule || fmt.Sprint(got["ids"]) != fmt.Sprint(nonNil(e.IDs)) ||
				fmt.Sprint(got["paths"]) != fmt.Sprint(nonNilS(e.Paths)) {
				t.Errorf("%s: model %v, koan %s", p, e, r.Line())
			}
			continue
		}
		var want []string
		for _, f := range made {
			want = append(want, fmt.Sprintf("map[id:%d path:%s]", f.ID, f.Path))
		}
		if got := fmt.Sprint(r.Result()["folders_made"]); got != "["+strings.Join(want, " ")+"]" {
			t.Errorf("%s: model %v, koan %s", p, want, r.Line())
		}
	}
	var want []string
	for _, id := range m.TreeOrder() {
		want = append(want, fmt.Sprint(id))
	}
	var got []string
	for _, f := range ok(t, home, "folder", "list", "--fields", "id").Result()["folders"].([]any) {
		got = append(got, fmt.Sprint(f.(map[string]any)["id"]))
	}
	if !slices.Equal(got, want) {
		t.Errorf("tree order: koan %v, model %v", got, want)
	}
}

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func nonNilS(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
