package main_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// hereTree is /work (1) holding /work/api (2), a task in /work/api (3),
// a task in /work (4) and one in the root (5), with a worktree bound to
// /work/api and a directory under it.
func hereTree(t *testing.T) (home, wt, sub string) {
	home = realRoot(t)
	ok(t, home, "folder", "create", "/work/api")
	ok(t, home, "task", "create", "in api", "--folder", "/work/api")
	ok(t, home, "task", "create", "in work", "--folder", "/work")
	ok(t, home, "task", "create", "in root")
	wt = mkdir(t, home, "wt")
	ok(t, home, "bind", "/work/api", wt)
	sub = mkdir(t, wt, "src")
	return
}

// subjectsOf names what a command's result lists or briefs, to compare
// two runs by.
func subjectsOf(r *testrun.Result) string {
	res := r.Result()
	var ids []string
	if f, isMap := res["folder"].(map[string]any); isMap {
		ids = append(ids, fmt.Sprint("folder ", f["id"]))
	}
	for _, k := range []string{"tasks", "folders"} {
		list, _ := res[k].([]any)
		for _, x := range list {
			ids = append(ids, fmt.Sprint(x.(map[string]any)["id"]))
		}
	}
	return strings.Join(ids, ",")
}

// The commands that look under a folder, with the options that make
// them read-only.
var lookers = [][]string{
	{"task", "list", "--state", "open,complete"},
	{"frontier"},
	{"folder", "list"},
	{"context"},
	{"export"},
}

// A folder left out is the root, on every command, the working
// directory notwithstanding.
func TestFolderLeftOutIsTheRoot(t *testing.T) {
	home, wt, _ := hereTree(t)
	for _, c := range lookers {
		root := subjectsOf(ok(t, home, append(append([]string{}, c...), "/")...))
		for _, dir := range []string{wt, treeAt(home, "@work")} {
			if got := subjectsOf(koanIn(t, home, dir, c...)); got != root {
				t.Errorf("%v in %s: %s, want the root's %s", c, dir, got, root)
			}
		}
	}
	// claim-next claims from the root, not from the worktree's folder.
	r := koanIn(t, home, wt, "claim-next")
	if id := r.Result()["task"].(map[string]any)["id"]; fmt.Sprint(id) != "3" {
		t.Errorf("claim-next: %s", r.Line())
	}
}

// --here finds the folder as where --file finds it: through a marker, or
// by where the working directory is in the tree; from anywhere else it
// is not-found.
func TestHereEverywhere(t *testing.T) {
	home, wt, sub := hereTree(t)
	for _, c := range lookers {
		byRef := subjectsOf(ok(t, home, append(append([]string{}, c...), "/work/api")...))
		for _, dir := range []string{wt, sub, treeAt(home, "@work", "@api")} {
			args := append(append([]string{}, c...), "--here")
			if got := subjectsOf(koanIn(t, home, dir, args...)); got != byRef {
				t.Errorf("%v --here in %s: %s, want %s", c, dir, got, byRef)
			}
		}
		args := append(append([]string{}, c...), "--here")
		r := koanIn(t, home, realDir(t), args...)
		if r.Exit != 1 || r.Kind() != "not-found" {
			t.Errorf("%v --here outside: exit %d: %s", c, r.Exit, r.Line())
		}
		// In the tree, the folder is the directory's own.
		if got := subjectsOf(koanIn(t, home, treeAt(home, "@work"), args...)); got == byRef {
			t.Errorf("%v --here in /work gave /work/api's", c)
		}
	}
	r := koanIn(t, home, sub, "claim-next", "--here")
	if id := r.Result()["task"].(map[string]any)["id"]; fmt.Sprint(id) != "3" {
		t.Errorf("claim-next --here: %s", r.Line())
	}
	r = koanIn(t, home, realDir(t), "where", "--here")
	if r.Exit != 1 || r.Kind() != "not-found" {
		t.Errorf("where --here outside: %s", r.Line())
	}
	if got := location(koanIn(t, home, sub, "where", "--here")); !strings.HasPrefix(got, "folder 2 ") {
		t.Errorf("where --here: %s", got)
	}
}

// --file takes a path relative to the working directory, and a stale
// marker warns as where does.
func TestFileEverywhere(t *testing.T) {
	home, wt, sub := hereTree(t)
	for _, c := range lookers {
		byRef := subjectsOf(ok(t, home, append(append([]string{}, c...), "/work/api")...))
		for _, file := range []string{"x.go", "../src", ".."} {
			args := append(append([]string{}, c...), "--file", file)
			if got := subjectsOf(koanIn(t, home, sub, args...)); got != byRef {
				t.Errorf("%v --file %s: %s, want %s", c, file, got, byRef)
			}
		}
	}
	r := koanIn(t, home, sub, "claim-next", "--file", "../x")
	if id := r.Result()["task"].(map[string]any)["id"]; fmt.Sprint(id) != "3" {
		t.Errorf("claim-next --file: %s", r.Line())
	}

	koan(t, home, "mv", "-T", "/work/api", "/work/web") // warns: the worktree's marker is stale
	for _, c := range append(lookers, []string{"where"}) {
		args := append(append([]string{}, c...), "--file", wt)
		r := koan(t, home, args...)
		ws := r.Envelope["warnings"].([]any)
		if r.Exit != 0 || len(ws) != 1 || ws[0].(map[string]any)["kind"] != "stale-marker" {
			t.Errorf("%v stale: exit %d: %s", c, r.Exit, r.Line())
		}
	}
	r = koanIn(t, home, realDir(t), "claim-next", "--here")
	if r.Exit != 1 {
		t.Errorf("claim-next --here from the test's directory: %s", r.Line())
	}
	r = koanIn(t, home, wt, "claim-next", "--here")
	if ws := r.Envelope["warnings"].([]any); r.Exit != 0 || len(ws) != 1 || ws[0].(map[string]any)["kind"] != "stale-marker" {
		t.Errorf("claim-next stale: %s", r.Line())
	}
}

// At most one of the subject, --file and --here: more is invalid-input
// at "", and where needs exactly one.
func TestOneOfFolderFileHere(t *testing.T) {
	home, wt, _ := hereTree(t)
	for _, c := range append(lookers, []string{"claim-next"}, []string{"where"}) {
		for _, extra := range [][]string{
			{"/work", "--file", wt},
			{"/work", "--here"},
			{"--file", wt, "--here"},
			{"/work", "--file", wt, "--here"},
		} {
			args := append(append([]string{}, c...), extra...)
			r := koanIn(t, home, wt, args...)
			if r.Exit != 1 || r.Kind() != "invalid-input" {
				t.Errorf("%v: exit %d: %s", args, r.Exit, r.Line())
				continue
			}
			p := r.Error()["problems"].([]any)
			if len(p) != 1 || p[0].(map[string]any)["field"] != "" {
				t.Errorf("%v: problems %v", args, p)
			}
		}
	}
	fails(t, home, "invalid-input", "", "where")
	// Subject and either alone are fine for the commands that take them.
	for _, c := range lookers {
		ok(t, home, append(append([]string{}, c...), "/work")...)
		ok(t, home, append(append([]string{}, c...), "--file", wt)...)
	}
}

// Through -i only file exists; --here has no field and can't go with -i.
func TestHereThroughInput(t *testing.T) {
	home, wt, _ := hereTree(t)
	byRef := subjectsOf(ok(t, home, "frontier", "/work/api"))
	in := func(s string, args ...string) *testrun.Result {
		r := testrun.Run{Home: home, Dir: wt, Stdin: strings.NewReader(s)}.Koan(t, append(args, "-i", "-")...)
		r.Check(t, args[0])
		return r
	}
	if got := subjectsOf(in(`{"file": "`+wt+`"}`, "frontier")); got != byRef {
		t.Errorf("file: %s, want %s", got, byRef)
	}
	if r := in(`{}`, "context"); r.Result()["folder"].(map[string]any)["id"] != nil {
		t.Errorf("context {}: %s", r.Line())
	}
	for _, field := range []string{"here", "dir"} {
		r := in(`{"`+field+`": true}`, "frontier")
		if r.Exit != 1 || r.Kind() != "invalid-input" {
			t.Errorf("%s: %s", field, r.Line())
		}
	}
	if r := in(`{"folder": "/work", "file": "`+wt+`"}`, "folder", "list"); r.Kind() != "invalid-input" {
		t.Errorf("both: %s", r.Line())
	}
	for _, args := range [][]string{
		{"frontier", "--here", "-i", "-"},
		{"where", "--here", "-i", "-"},
	} {
		if r := koanIn(t, home, wt, args...); r.Exit != 2 || r.Kind() != "usage" {
			t.Errorf("%q: exit %d: %s", args, r.Exit, r.Line())
		}
	}
}
