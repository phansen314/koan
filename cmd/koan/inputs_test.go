package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// fault is the error's first problem's field, and its ids and paths as
// strings.
func fault(r *testrun.Result) (field, ids, paths string) {
	e := r.Error()
	if e == nil {
		return "", "", ""
	}
	if ps, _ := e["problems"].([]any); len(ps) > 0 {
		field, _ = ps[0].(map[string]any)["field"].(string)
	}
	return field, fmt.Sprint(e["ids"]), fmt.Sprint(e["paths"])
}

// A reference that names nothing is not-found with the field in problems,
// the pointer --input gives it, on every command, not only batch.
func TestNotFoundPointsAtTheField(t *testing.T) {
	home := root(t)
	setup(t, home, "folder", "create", "/w") // 1
	setup(t, home, "task", "create", "a")    // 2
	setup(t, home, "task", "create", "b")    // 3
	for _, c := range []struct {
		args    []string
		command string
		in      string
		field   string
	}{
		{[]string{"show", "99"}, "show", `{"ref":99}`, "/ref"},
		{[]string{"why", "99"}, "why", `{"task":99}`, "/task"},
		{[]string{"complete", "2", "99"}, "complete", `{"tasks":[2,99]}`, "/tasks/1"},
		{[]string{"reopen", "99"}, "reopen", `{"tasks":[99]}`, "/tasks/0"},
		{[]string{"claim", "2", "99"}, "claim", `{"tasks":[2,99]}`, "/tasks/1"},
		{[]string{"unclaim", "99"}, "unclaim", `{"tasks":[99]}`, "/tasks/0"},
		{[]string{"claim-next", "/nope"}, "claim-next", `{"folder":"/nope"}`, "/folder"},
		{[]string{"block", "2", "--blocked-by", "3,99"}, "block", `{"task":2,"blocked_by":[3,99]}`, "/blocked_by/1"},
		{[]string{"block", "99", "--blocked-by", "3"}, "block", `{"task":99,"blocked_by":[3]}`, "/task"},
		{[]string{"unblock", "2", "--blocks", "99"}, "unblock", `{"task":2,"blocks":[99]}`, "/blocks/0"},
		{[]string{"task", "create", "t", "--folder", "/nope"}, "task create", `{"title":"t","folder":"/nope"}`, "/folder"},
		{[]string{"task", "create", "t", "--blocked-by", "99"}, "task create", `{"title":"t","blocked_by":[99]}`, "/blocked_by/0"},
		{[]string{"update", "2", "99", "--title", "x"}, "update", `{"refs":[2,99],"title":"x"}`, "/refs/1"},
		{[]string{"mv", "99", "/w"}, "mv", `{"refs":[99],"dest":"/w"}`, "/refs/0"},
		{[]string{"mv", "2", "/nope"}, "mv", `{"refs":[2],"dest":"/nope"}`, "/dest"},
		{[]string{"mv", "-t", "/nope", "2"}, "mv", `{"refs":[2],"into":"/nope"}`, "/into"},
		{[]string{"mv", "-T", "/w", "/nope/x"}, "mv", `{"refs":["/w"],"dest":"/nope/x","rename":true}`, "/dest"},
		{[]string{"delete", "2", "99"}, "delete", `{"refs":[2,99]}`, "/refs/1"},
		{[]string{"archive", "99"}, "archive", `{"refs":[99]}`, "/refs/0"},
		{[]string{"join", "2", "/nope"}, "join", `{"ref":2,"join":["/nope"]}`, "/join/0"},
		{[]string{"unjoin", "2", "99"}, "unjoin", `{"ref":2,"join":[99]}`, "/join/0"},
		{[]string{"where", "99"}, "where", `{"ref":99}`, "/ref"},
		{[]string{"context", "/nope"}, "context", `{"folder":"/nope"}`, "/folder"},
		{[]string{"export", "/nope"}, "export", `{"folder":"/nope"}`, "/folder"},
		{[]string{"task", "list", "/nope"}, "task list", `{"folder":"/nope"}`, "/folder"},
		{[]string{"frontier", "/nope"}, "frontier", `{"folder":"/nope"}`, "/folder"},
		{[]string{"folder", "list", "/nope"}, "folder list", `{"folder":"/nope"}`, "/folder"},
		{[]string{"bind", "/nope", home}, "bind", `{"folder":"/nope","dir":"` + home + `"}`, "/folder"},
	} {
		line := koan(t, home, c.args...)
		via := testrun.Run{Home: home, Stdin: strings.NewReader(c.in)}.Koan(t, append(strings.Fields(c.command), "-i", "-")...)
		for how, r := range map[string]*testrun.Result{"koan " + strings.Join(c.args, " "): line, "--input " + c.in: via} {
			field, _, _ := fault(r)
			if r.Exit != 1 || r.Kind() != "not-found" || field != c.field {
				t.Errorf("%s: exit %d, want not-found at %q: %s", how, r.Exit, c.field, r.Line())
			}
		}
	}
	// A gate its folder lacks, in an edge-less place, points at its field too.
	r := koan(t, home, "why", "/w.start")
	if f, _, p := fault(r); r.Kind() != "not-found" || f != "/task" || p != "[/w.start]" {
		t.Errorf("why /w.start: %s", r.Line())
	}
	// A deleted ID and an archived one point at theirs, with ids.
	r = koan(t, home, "show", "99")
	if f, ids, _ := fault(r); f != "/ref" || ids != "[99]" {
		t.Errorf("show 99: %s", r.Line())
	}
}

// not-found for a file points at /file, as --here does.
func TestNotFoundFile(t *testing.T) {
	home := realRoot(t)
	dir := realDir(t)
	for _, args := range [][]string{{"where", "--file", "x"}, {"where", "--here"}, {"context", "--here"}, {"task", "list", "--file", "x"}} {
		r := koanIn(t, home, dir, args...)
		if f, _, _ := fault(r); r.Exit != 1 || r.Kind() != "not-found" || f != "/file" {
			t.Errorf("%v: exit %d, want not-found at /file: %s", args, r.Exit, r.Line())
		}
	}
}

// exists carries the field giving the path in problems, and the folder
// that has the path in ids, when one does.
func TestExistsPointsAtThePath(t *testing.T) {
	home := root(t)
	setup(t, home, "folder", "create", "/a")   // 1
	setup(t, home, "folder", "create", "/b")   // 2
	setup(t, home, "folder", "create", "/a/c") // 3
	setup(t, home, "task", "create", "t")      // 4
	dir := mkdir(t, home, "bk")
	os.WriteFile(filepath.Join(dir, "x"), nil, 0o644)
	for _, c := range []struct {
		args    []string
		command string
		in      string
		field   string
		ids     string
		paths   string
	}{
		{[]string{"folder", "create", "/a"}, "folder create", `{"path":"/a"}`, "/path", "[1]", "[/a]"},
		{[]string{"mv", "-T", "/b", "/a"}, "mv", `{"refs":["/b"],"dest":"/a","rename":true}`, "/dest", "[1]", "[/a]"},
		{[]string{"mv", "-t", "/a", "/b", "/a/c"}, "mv", `{"refs":["/b","/a/c"],"into":"/a"}`, "", "", ""},
		{[]string{"backup", dir}, "backup", `{"dir":"` + dir + `"}`, "/dir", "[]", "[" + dir + "]"},
	} {
		if c.field == "" {
			continue
		}
		line := koan(t, home, c.args...)
		via := testrun.Run{Home: home, Stdin: strings.NewReader(c.in)}.Koan(t, append(strings.Fields(c.command), "-i", "-")...)
		for how, r := range map[string]*testrun.Result{"koan " + strings.Join(c.args, " "): line, "--input " + c.in: via} {
			field, ids, paths := fault(r)
			if r.Exit != 1 || r.Kind() != "conflict" || r.Error()["rule"] != "exists" || field != c.field || ids != c.ids || paths != c.paths {
				t.Errorf("%s: exit %d, want exists at %q ids %s paths %s: %s", how, r.Exit, c.field, c.ids, c.paths, r.Line())
			}
		}
	}
	// A move into a folder that already holds the name: the folder with it.
	setup(t, home, "folder", "create", "/b/c") // 5
	r := koan(t, home, "mv", "-t", "/b", "/a/c")
	if f, ids, paths := fault(r); r.Error()["rule"] != "exists" || f != "/into" || ids != "[5]" || paths != "[/b/c]" {
		t.Errorf("mv -t: %s", r.Line())
	}
	// An entry on disk holds the path: no folder, so no ids.
	if err := os.MkdirAll(filepath.Join(home, "koan-root", "tree", "@a", "@d"), 0o755); err == nil {
		r = koan(t, home, "folder", "create", "/a/d")
		if f, ids, _ := fault(r); r.Error()["rule"] != "exists" || f != "/path" || ids != "[]" {
			t.Errorf("folder create on disk: %s", r.Line())
		}
	}
	// In a batch.
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"folders":[{"path":"/x"},{"path":"/a"}]}`)}.Koan(t, "batch", "-i", "-")
	if f, ids, paths := fault(r); r.Error()["rule"] != "exists" || f != "/folders/1/path" || ids != "[1]" || paths != "[/a]" {
		t.Errorf("batch: %s", r.Line())
	}
}

// A wrong-kind reference puts what it names in ids; a field the subject's
// kind doesn't take puts the subjects of that kind there.
func TestWrongKindNamesIDs(t *testing.T) {
	home := root(t)
	setup(t, home, "folder", "create", "/w") // 1
	setup(t, home, "task", "create", "a")    // 2
	setup(t, home, "folder", "create", "/v") // 3
	for _, c := range []struct {
		args  []string
		field string
		ids   string
	}{
		{[]string{"block", "2", "--blocked-by", "@2.start"}, "/blocked_by/0", "[2]"},
		{[]string{"task", "list", "2"}, "/folder", "[2]"},
		{[]string{"mv", "-t", "2", "2"}, "/into", "[2]"},
		{[]string{"update", "2", "1", "3", "--priority", "1"}, "/priority", "[1 3]"},
		{[]string{"update", "1", "2", "3", "--tags-add", "x"}, "/tags_add", "[1 3]"},
		{[]string{"update", "1", "2", "--tags-add", "x", "--priority", "1"}, "/priority", "[1]"},
		{[]string{"update", "2", "--title", "null"}, "/title", "[2]"},
		{[]string{"delete", "2", "--contents"}, "/contents", "[2]"},
		{[]string{"mv", "-T", "2", "/x"}, "/rename", "[2]"},
	} {
		r := koan(t, home, c.args...)
		if f, ids, _ := fault(r); r.Exit != 1 || r.Kind() != "invalid-input" || f != c.field || ids != c.ids {
			t.Errorf("koan %q: exit %d, want invalid-input at %s with ids %s: %s", c.args, r.Exit, c.field, c.ids, r.Line())
		}
	}
	// A shape fault, which resolves nothing, has no ids.
	r := koan(t, home, "update", "1", "--tags-add", "X")
	if _, ids, _ := fault(r); r.Kind() != "invalid-input" || ids != "[]" {
		t.Errorf("shape fault: %s", r.Line())
	}
}

// A set takes duplicates, counting once; a tag both added and removed, a
// key both merged and removed, and a subject given twice stay refused.
func TestSetsTakeDuplicates(t *testing.T) {
	home := root(t)
	setup(t, home, "task", "create", "a", "--tags", "t1") // 1
	setup(t, home, "task", "create", "b")                 // 2
	ok(t, home, "complete", "2")

	r := ok(t, home, "task", "list", "--fields", "id,ref,id,ref")
	if t0 := r.Result()["tasks"].([]any)[0].(map[string]any); fmt.Sprint(keysOf(t0)) != "[id ref]" {
		t.Errorf("--fields id,ref,id,ref: %s", r.Line())
	}
	ok(t, home, "task", "list", "--state", "open,open,complete,open")
	ok(t, home, "task", "list", "--readiness", "ready,ready")
	ok(t, home, "task", "list", "--tags-any", "t1,t1", "--tags-all", "t1,t1")
	ok(t, home, "frontier", "--fields", "id,id")
	ok(t, home, "folder", "list", "--fields", "path,path")
	ok(t, home, "repair", "--kinds", "case-differs,case-differs")

	r = ok(t, home, "task", "create", "c", "--tags", "x,y,x")
	id := fmt.Sprint(r.Result()["id"])
	r = ok(t, home, "show", id)
	if fmt.Sprint(r.Result()["task"].(map[string]any)["tags"]) != "[x y]" {
		t.Errorf("tags: %s", r.Line())
	}
	ok(t, home, "update", id, "--tags-add", "z,z", "--tags-remove", "x,x")
	ok(t, home, "update", id, "--tags-replace-all", "q,q")
	r = ok(t, home, "show", id)
	if fmt.Sprint(r.Result()["task"].(map[string]any)["tags"]) != "[q]" {
		t.Errorf("tags: %s", r.Line())
	}
	ok(t, home, "update", id, "--extra-merge", `{"k":1,"j":2}`)
	ok(t, home, "update", id, "--extra-remove", "k", "--extra-remove", "k")
	r = ok(t, home, "show", id)
	if fmt.Sprint(r.Result()["task"].(map[string]any)["extra"]) != "map[j:2]" {
		t.Errorf("extra: %s", r.Line())
	}
	// The same through --input.
	in := testrun.Run{Home: home, Stdin: strings.NewReader(`{"refs":[` + id + `],"tags_add":["w","w"],"extra_remove":["j","j"]}`)}.Koan(t, "update", "-i", "-")
	if in.Exit != 0 {
		t.Errorf("update -i: %s", in.Line())
	}

	// Still refused.
	invalidAt(t, home, "/tags_remove/0", "update", id, "--tags-add", "a,a", "--tags-remove", "a,a")
	invalidAt(t, home, "/extra_remove/0", "update", id, "--extra-merge", `{"k":1}`, "--extra-remove", "k", "--extra-remove", "k")
	invalidAt(t, home, "/tasks/1", "complete", "1", "@1")
	invalidAt(t, home, "/refs/1", "update", "1", "@1", "--title", "x")
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// pick takes --file, relative to the working directory, as the other
// commands that look under a folder do, and not with a folder or --here.
func TestPickFile(t *testing.T) {
	home := pickTree(t)
	work := t.TempDir()
	ok(t, home, "bind", "@2", work)
	if err := os.Mkdir(filepath.Join(work, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := newPicker(t, home).with("FAKE_FZF_EXIT=1")
	want := []string{"@2", "@3", "@4"}
	for _, file := range []string{filepath.Join(work, "src", "x.go"), "src/x.go", "../" + filepath.Base(work), "."} {
		p.dir = work
		p.run("--file", file, "--kinds", "folder,task")
		if got := p.keys(); !slices.Equal(got, want) {
			t.Errorf("--file %s: keys %v, want %v", file, got, want)
		}
	}
	p.dir = work
	p.run("--file", "x", "/work").failed(t, 1, "invalid-input", "the folder, --file and --here")
	p.run("--file", "x", "--here").failed(t, 1, "invalid-input", "the folder, --file and --here")
	p.dir = t.TempDir()
	p.run("--file", "x").failed(t, 1, "not-found", "no marker")
}
