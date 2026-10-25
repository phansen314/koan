package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// markerFile is the marker.json of a directory.
func markerFile(dir string) string { return filepath.Join(dir, ".koan", "marker.json") }

// koanDir is what a directory's .koan/ holds, by name, files with their
// content after =; nil when there is no .koan/.
func koanDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".koan"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, ".koan", e.Name()))
		out = append(out, e.Name()+"="+string(b))
	}
	return out
}

// checkMarker checks a marker.json against its own schema, in its
// schema's field order, and returns it.
func checkMarker(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(markerFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	s := testrun.Load(t)
	if err := s.Validate("marker-file", "", string(b)); err != nil {
		t.Errorf("marker.json fails its schema: %v\n%s", err, b)
	}
	if err := s.Order("marker-file", "", string(b)); err != nil {
		t.Errorf("marker.json: %v\n%s", err, b)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

// bind writes .koan/ with .gitignore and marker.json, binding the
// directory to the folder by its ID, its path as a hint and its own real
// path.
func TestMarkerBind(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work/api")
	ok(t, home, "folder", "create", "/home")
	wt := mkdir(t, home, "wt")
	r := ok(t, home, "bind", "/work/api", wt)
	golden(t, r, "bind")
	if r.Result()["replaced"] != false || r.Result()["marker"] != markerFile(wt) {
		t.Errorf("bind: %s", r.Line())
	}
	m := checkMarker(t, wt)
	if m["dir"] != wt || m["path"] != "/work/api" {
		t.Errorf("marker: %v", m)
	}
	want := []string{".gitignore=*\n", "marker.json=" + `{"schema":1,"id":2,"path":"/work/api","dir":"` + wt + `"}` + "\n"}
	if got := koanDir(t, wt); !slices.Equal(got, want) {
		t.Errorf(".koan/:\n got %q\nwant %q", got, want)
	}

	// The same folder again replaces it, refreshing its path; another
	// live folder needs --force.
	ok(t, home, "mv", "-T", "/work/api", "/work/web")
	r = ok(t, home, "bind", "2", wt)
	if r.Result()["replaced"] != true || checkMarker(t, wt)["path"] != "/work/web" {
		t.Errorf("rebind: %s", r.Line())
	}
	r = fails(t, home, "conflict", "bound", "bind", "/home", wt)
	golden(t, r, "bind-bound")
	if checkMarker(t, wt)["id"] != 2.0 {
		t.Errorf("refused bind changed the marker")
	}
	r = ok(t, home, "bind", "/home", wt, "--force")
	if r.Result()["replaced"] != true || checkMarker(t, wt)["id"] != 3.0 {
		t.Errorf("force: %s", r.Line())
	}

	// A marker that binds nothing is replaced without --force: copied,
	// unreadable, or of a folder that is gone.
	for _, content := range []string{
		`{"schema":1,"id":2,"path":"/work/web","dir":"/elsewhere"}`,
		`not json`,
		`{"schema":2,"id":2,"path":"/work/web","dir":"` + wt + `"}`,
		`{"schema":1,"id":99,"path":"/work/web","dir":"` + wt + `"}`,
	} {
		os.WriteFile(markerFile(wt), []byte(content), 0o644)
		if r := ok(t, home, "bind", "/work/web", wt); r.Result()["replaced"] != true {
			t.Errorf("over %s: %s", content, r.Line())
		}
	}
	ok(t, home, "folder", "create", "/gone")
	ok(t, home, "bind", "/gone", wt, "--force")
	ok(t, home, "delete", "/gone")
	ok(t, home, "bind", "/home", wt)

	// A temp file left by a crash is koan's own, and written over.
	os.WriteFile(filepath.Join(wt, ".koan", "marker.json.tmp"), []byte("partial"), 0o644)
	ok(t, home, "bind", "/home", wt)
	if got := koanDir(t, wt); len(got) != 2 {
		t.Errorf("temp file left: %q", got)
	}

	// The folder: by reference, never the root, never a task.
	golden(t, fails(t, home, "invalid-input", "", "bind", "/", wt), "bind-root")
	ok(t, home, "task", "create", "a")
	fails(t, home, "invalid-input", "", "bind", "@5", wt)
	fails(t, home, "not-found", "", "bind", "/nope", wt)
	fails(t, home, "conflict", "case-mismatch", "bind", "/HOME", wt)
	fails(t, home, "invalid-input", "", "bind", "/home.done", wt)
	if r := koan(t, home, "bind", "/home", wt, "--force=x"); r.Exit != 2 || r.Kind() != "usage" {
		t.Errorf("--force=x: %s", r.Line())
	}
	ok(t, home, "folder", "create", "/done")
	ok(t, home, "archive", "/done")
	fails(t, home, "not-found", "", "bind", "/done", wt)
	r = fails(t, home, "not-found", "", "bind", "@6", wt)
	if len(r.Error()["archived"].([]any)) != 1 {
		t.Errorf("archived: %s", r.Line())
	}
	if checkMarker(t, wt)["id"] != 3.0 {
		t.Errorf("refused binds changed the marker")
	}
}

// bind refuses a .koan/ holding anything not koan's (foreign-files), a
// checkout (checkout, unless forced), a directory under ~/koan-root
// (invalid-input) and one that doesn't exist (io), in the spec's order.
func TestMarkerBindRefusals(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work")
	ok(t, home, "folder", "create", "/home")

	// foreign-files, with those files.
	wt := mkdir(t, home, "wt")
	mkdir(t, wt, ".koan", "sub")
	os.WriteFile(filepath.Join(wt, ".koan", "notes"), nil, 0o644)
	os.WriteFile(filepath.Join(wt, ".koan", ".gitignore"), []byte("*\n"), 0o644)
	r := fails(t, home, "conflict", "foreign-files", "bind", "/work", wt, "--force")
	golden(t, r, "bind-foreign-files")
	if p := errorPaths(r); len(p) != 2 || p[0] != wt+"/.koan/notes" || p[1] != wt+"/.koan/sub" {
		t.Errorf("foreign files: %s", r.Line())
	}

	// checkout: .git is a directory. A worktree's .git is a file.
	co := mkdir(t, home, "co")
	mkdir(t, co, ".git")
	r = fails(t, home, "conflict", "checkout", "bind", "/work", co)
	golden(t, r, "bind-checkout")
	if p := errorPaths(r); len(p) != 1 || p[0] != co {
		t.Errorf("checkout: %s", r.Line())
	}
	ok(t, home, "bind", "/work", co, "--force")
	worktree := mkdir(t, home, "worktree")
	os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /x\n"), 0o644)
	ok(t, home, "bind", "/work", worktree)

	// The order: foreign-files before checkout before bound.
	mkdir(t, co, ".koan", "x")
	fails(t, home, "conflict", "foreign-files", "bind", "/home", co)
	os.Remove(filepath.Join(co, ".koan", "x"))
	fails(t, home, "conflict", "checkout", "bind", "/home", co)

	// Under ~/koan-root, anywhere, is invalid-input at /dir.
	root := filepath.Join(home, "koan-root")
	for _, dir := range []string{root, root + "/tree", root + "/tree/@work", root + "/archived", root + "/tree/@work/missing"} {
		r := fails(t, home, "invalid-input", "", "bind", "/work", dir)
		if p := r.Error()["problems"].([]any); len(p) != 1 || p[0].(map[string]any)["field"] != "/dir" {
			t.Errorf("<dir> %s: %s", dir, r.Line())
		}
	}
	golden(t, fails(t, home, "invalid-input", "", "bind", "/work", root+"/tree/@work"), "bind-under-root")
	// A directory beside the root is not under it.
	ok(t, home, "bind", "/work", mkdir(t, home, "koan-root-2"))

	// A <dir> that doesn't exist, or isn't a directory, is io.
	r = fails(t, home, "io", "", "bind", "/work", home+"/missing")
	golden(t, r, "bind-missing")
	if p := errorPaths(r); len(p) != 1 || p[0] != home+"/missing" {
		t.Errorf("missing: %s", r.Line())
	}
	os.WriteFile(home+"/file", nil, 0o644)
	fails(t, home, "io", "", "bind", "/work", home+"/file")

	// The order: under the root, then missing, then the folder, then the
	// conflicts.
	fails(t, home, "invalid-input", "", "bind", "/nope", root+"/missing")
	fails(t, home, "io", "", "bind", "/nope", home+"/missing")
	fails(t, home, "not-found", "", "bind", "/nope", co)
	// The input's shape before the root's state, and the root's state
	// before the directory.
	fails(t, home, "invalid-input", "", "bind", "/", home+"/missing")
	bare := realDir(t)
	fails(t, bare, "not-initialized", "", "bind", "/work", bare+"/missing")
	fails(t, bare, "not-initialized", "", "unbind", bare)
}

// A second route to the root, such as a symlink, never gets past the
// check of a directory under it, since it compares real paths; paths
// printed stay as given.
func TestMarkerBindRealPath(t *testing.T) {
	home, elsewhere := realDir(t), realDir(t)
	if err := os.Symlink(elsewhere, filepath.Join(home, "koan-root")); err != nil {
		t.Fatal(err)
	}
	if r := (testrun.Run{Home: home}).Koan(t, "init"); r.Exit != 0 {
		t.Fatalf("init: %s", r.Line())
	}
	ok(t, home, "folder", "create", "/work")
	link := filepath.Join(home, "link")
	os.Symlink(filepath.Join(elsewhere, "tree"), link)
	for _, dir := range []string{elsewhere, elsewhere + "/tree/@work", link, link + "/@work", home + "/koan-root/tree"} {
		fails(t, home, "invalid-input", "", "bind", "/work", dir)
		fails(t, home, "invalid-input", "", "unbind", dir)
	}

	// Through a symlinked directory outside the root, as macOS's /tmp is:
	// marker.json's dir is the real path, every path printed is as given.
	wt := mkdir(t, home, "wt")
	via := filepath.Join(home, "via")
	os.Symlink(wt, via)
	r := ok(t, home, "bind", "/work", via)
	if r.Result()["marker"] != via+"/.koan/marker.json" || r.Result()["binding"].(map[string]any)["dir"] != wt {
		t.Errorf("via: %s", r.Line())
	}
	if got := koanDir(t, wt); len(got) != 2 {
		t.Errorf("not written through the link: %q", got)
	}
	r = ok(t, home, "unbind", via)
	if r.Result()["dir"] != via || r.Result()["changed"] != true {
		t.Errorf("unbind via: %s", r.Line())
	}
}

// unbind removes the temp file, marker.json, .gitignore, then
// .koan/; with no marker it succeeds and says so.
func TestMarkerUnbind(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work")
	wt := mkdir(t, home, "wt")
	ok(t, home, "bind", "/work", wt)
	r := ok(t, home, "unbind", wt)
	golden(t, r, "unbind")
	if r.Result()["changed"] != true || koanDir(t, wt) != nil {
		t.Errorf("unbind: %s %q", r.Line(), koanDir(t, wt))
	}
	r = ok(t, home, "unbind", wt)
	golden(t, r, "unbind-none")
	if r.Result()["changed"] != false {
		t.Errorf("again: %s", r.Line())
	}

	// What a crash leaves, whatever its state, is koan's own to remove.
	mkdir(t, wt, ".koan")
	os.WriteFile(filepath.Join(wt, ".koan", ".gitignore"), []byte("*\n"), 0o644)
	os.WriteFile(filepath.Join(wt, ".koan", "marker.json.tmp"), []byte("x"), 0o644)
	if r := ok(t, home, "unbind", wt); r.Result()["changed"] != false || koanDir(t, wt) != nil {
		t.Errorf("leftovers: %s %q", r.Line(), koanDir(t, wt))
	}
	mkdir(t, wt, ".koan")
	os.WriteFile(markerFile(wt), []byte("not json"), 0o644)
	if r := ok(t, home, "unbind", wt); r.Result()["changed"] != true || koanDir(t, wt) != nil {
		t.Errorf("unreadable: %s", r.Line())
	}

	// A .koan/ holding anything else is never touched.
	ok(t, home, "bind", "/work", wt)
	os.WriteFile(filepath.Join(wt, ".koan", "mine"), nil, 0o644)
	r = fails(t, home, "conflict", "foreign-files", "unbind", wt)
	golden(t, r, "unbind-foreign-files")
	if got := koanDir(t, wt); len(got) != 3 {
		t.Errorf("touched: %q", got)
	}

	// <dir>: absolute through --input, made absolute or the working
	// directory on the command line; missing or under the root.
	os.Remove(filepath.Join(wt, ".koan", "mine"))
	if r := koanIn(t, home, filepath.Join(wt, ".koan"), "unbind", ".."); r.Result()["dir"] != wt || r.Result()["changed"] != true {
		t.Errorf("relative: %s", r.Line())
	}
	ok(t, home, "bind", "/work", wt)
	if r := koanIn(t, home, wt, "unbind"); r.Result()["dir"] != wt || r.Result()["changed"] != true {
		t.Errorf("cwd: %s", r.Line())
	}
	golden(t, fails(t, home, "io", "", "unbind", home+"/missing"), "unbind-missing")
	fails(t, home, "invalid-input", "", "unbind", filepath.Join(home, "koan-root", "tree"))
	r = testrun.Run{Home: home, Dir: wt, Stdin: strings.NewReader(`{}`)}.Koan(t, "unbind", "-i", "-")
	r.Check(t, "unbind")
	if r.Kind() != "invalid-input" || r.Error()["problems"].([]any)[0].(map[string]any)["field"] != "/dir" {
		t.Errorf("--input {}: %s", r.Line())
	}
}

// <dir> for bind: relative, or the working directory when left out;
// through --input, required and absolute.
func TestMarkerBindDir(t *testing.T) {
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work")
	wt := mkdir(t, home, "wt")
	sub := mkdir(t, wt, "sub")
	if r := koanIn(t, home, sub, "bind", "/work"); r.Result()["marker"] != markerFile(sub) {
		t.Errorf("cwd: %s", r.Line())
	}
	if r := koanIn(t, home, sub, "bind", "/work", ".."); r.Result()["marker"] != markerFile(wt) {
		t.Errorf("relative: %s", r.Line())
	}
	for _, c := range []struct{ in, field string }{
		{`{"folder":"/work"}`, "/dir"},
		{`{"folder":"/work","dir":"sub"}`, "/dir"},
		{`{"dir":"` + wt + `"}`, "/folder"},
	} {
		r := testrun.Run{Home: home, Dir: wt, Stdin: strings.NewReader(c.in)}.Koan(t, "bind", "-i", "-")
		r.Check(t, "bind")
		if r.Kind() != "invalid-input" || r.Error()["problems"].([]any)[0].(map[string]any)["field"] != c.field {
			t.Errorf("%s: %s", c.in, r.Line())
		}
	}
	// The folder, then the directory: one word each, and --dir is gone.
	golden(t, invalidAt(t, home, "/folder", "bind"), "bind-no-subject")
	golden(t, usageAt(t, home, sub, "bind", "/work", wt, sub), "bind-extra")
	golden(t, usageAt(t, home, "--dir", "bind", "/work", "--dir", wt), "bind-dir")
	usageAt(t, home, sub, "unbind", wt, sub)
	usageAt(t, home, "--dir", "unbind", "--dir", wt)
	usageAt(t, home, "/work", "bind", "/work", wt, "-i", "-")
	r := testrun.Run{Home: home, Stdin: strings.NewReader(`{"folder":"/work","dir":"` + wt + `"}`)}.Koan(t, "bind", "-i", "-")
	r.Check(t, "bind")
	if r.Exit != 0 || r.Result()["marker"] != markerFile(wt) {
		t.Errorf("--input: %s", r.Line())
	}
	golden(t, r, "bind-input")
}

// git ignores .koan/, so git status never shows it and git worktree
// remove needs no --force; bind refuses the checkout, and binds the
// worktree.
func TestMarkerGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	home := realRoot(t)
	ok(t, home, "folder", "create", "/work")
	repo := mkdir(t, home, "repo")
	wt := filepath.Join(home, "wt")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+home, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git(repo, "init", "-q")
	os.WriteFile(filepath.Join(repo, "README"), []byte("x"), 0o644)
	git(repo, "add", "README")
	git(repo, "commit", "-q", "-m", "first")
	git(repo, "worktree", "add", "-q", wt)

	fails(t, home, "conflict", "checkout", "bind", "/work", repo)
	ok(t, home, "bind", "/work", wt)
	if s := git(wt, "status", "--porcelain", "--ignored=no"); s != "" {
		t.Errorf("git status shows: %q", s)
	}
	if s := git(wt, "status", "--porcelain", "--untracked-files=all"); s != "" {
		t.Errorf("git status shows: %q", s)
	}
	if got := location(ok(t, home, "where", "--file", wt+"/README")); !strings.HasPrefix(got, "folder 1 ") {
		t.Errorf("where: %s", got)
	}
	git(repo, "worktree", "remove", wt)
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree left: %v", err)
	}
}
