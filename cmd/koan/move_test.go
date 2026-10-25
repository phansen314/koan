package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// treePath is a path under a root's tree directory, as results give it.
func treePath(home, rel string) string {
	return filepath.Join(home, "koan-root", "tree", filepath.FromSlash(rel))
}

// writeTree writes a file under a root's tree directory, making the
// directories above it.
func writeTree(t *testing.T, home, rel, content string) {
	t.Helper()
	p := treePath(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mv puts tasks in another folder or the root, their notes with
// them; no edge changes, so no readiness does, and updated_at stays. A
// task already in the folder doesn't move.
func TestTaskMove(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")                                     // 1
	ok(t, home, "folder", "create", "/b")                                     // 2
	ok(t, home, "task", "create", "t", "--folder", "/a", "--notes", "mine")   // 3
	ok(t, home, "task", "create", "u", "--folder", "/a", "--blocked-by", "3") // 4
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "mv", "3", "/b")
	golden(t, r, "task-mv")
	if fmt.Sprint(r.Result()) != fmt.Sprintf("map[moved:[map[dir:<nil> id:3 notes_path:%s ref:@3 (/b)]]]", treePath(home, "@b/@3.md")) {
		t.Errorf("mv: %s", r.Line())
	}
	checkTree(t, home, "@a/", "@a/@notes.md=", "@a/@4.md=", "@b/", "@b/@notes.md=", "@b/@3.md=mine")
	rec := ok(t, home, "show", "3").Result()["task"].(map[string]any)
	if rec["updated_at"] != fixed || fmt.Sprint(rec["folder"]) != "map[id:2 path:/b]" || rec["notes_path"] != treePath(home, "@b/@3.md") {
		t.Errorf("moved: %v", rec)
	}
	if field(t, home, "4", "readiness") != "blocked" || field(t, home, "4", "updated_at") != fixed {
		t.Errorf("what waits on it doesn't change")
	}
	// Where it already is, by any reference: nothing moves.
	for i, into := range []string{"/b", "@2", "2"} {
		r := ok(t, home, "mv", "@3", into)
		if i == 0 {
			golden(t, r, "task-mv-already-there")
		}
		if fmt.Sprint(r.Result()["moved"]) != "[]" {
			t.Errorf("into %s: %s", into, r.Line())
		}
	}
	// To the root; notes that are missing move as nothing.
	ok(t, home, "mv", "3", "/")
	checkTree(t, home, "@a/", "@a/@notes.md=", "@a/@4.md=", "@b/", "@b/@notes.md=", "@3.md=mine")
	os.Remove(treePath(home, "@a/@4.md"))
	ok(t, home, "mv", "4", "/b")
	checkTree(t, home, "@a/", "@a/@notes.md=", "@b/", "@b/@notes.md=", "@3.md=mine")
	if koan(t, home, "show", "4").Result()["task"].(map[string]any)["folder"].(map[string]any)["path"] != "/b" {
		t.Errorf("moved without notes")
	}

	golden(t, fails(t, home, "not-found", "", "mv", "3", "/z"), "task-mv-not-found")
	fails(t, home, "not-found", "", "mv", "99", "/b")
	fails(t, home, "conflict", "case-mismatch", "mv", "3", "/B")
	for in, fields := range map[string]string{
		`{"refs":[3]}`:                       "",
		`{"into":"/b"}`:                      "/refs",
		`{"refs":[],"into":"/b"}`:            "/refs",
		`{"refs":3,"into":"/b"}`:             "/refs",
		`{"refs":[3],"into":"/b.start"}`:     "/into",
		`{"refs":["/b:x"],"into":"/a"}`:      "/refs/0",
		`{"refs":["/a.start"],"into":"/A"}`:  "/refs/0 /into",
		`{"refs":[3],"into":"/b","x":1}`:     "/x",
		`{"refs":[3],"into":"/b:docs/x.md"}`: "/into",
		`{"refs":[3,"@3"],"into":"/b"}`:      "/refs/1",
	} {
		if r := input(t, home, "mv", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// mv takes several tasks, as words, joined by commas, or after -t;
// they move as one change, and moved lists those that moved in input
// order.
func TestTaskMoveSeveral(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")                // 1
	ok(t, home, "folder", "create", "/b")                // 2
	ok(t, home, "task", "create", "t", "--folder", "/a") // 3
	ok(t, home, "task", "create", "u", "--folder", "/b") // 4
	ok(t, home, "task", "create", "v", "--folder", "/a") // 5
	ok(t, home, "task", "create", "w", "--blocked-by", "/a.done")

	r := ok(t, home, "mv", "5", "4", "3", "/b")
	golden(t, r, "task-mv-several")
	if ids(r.Result()["moved"]) != "[5 3]" {
		t.Errorf("several: %s", r.Line())
	}
	checkTree(t, home, "@a/", "@a/@done.md=", "@a/@notes.md=", "@b/", "@b/@notes.md=", "@b/@3.md=", "@b/@4.md=", "@b/@5.md=", "@6.md=")

	r = ok(t, home, "mv", "-t", "/a", "3", "4")
	golden(t, r, "task-mv-into")
	if ids(r.Result()["moved"]) != "[3 4]" {
		t.Errorf("-t: %s", r.Line())
	}
	if r := ok(t, home, "mv", "--into", "/", "3,5", "4"); ids(r.Result()["moved"]) != "[3 5 4]" {
		t.Errorf("--into, with commas: %s", r.Line())
	}
	r = input(t, home, "mv", `{"refs":[3,5],"into":"/b"}`)
	golden(t, r, "task-mv-input")

	// If any is refused, none moves.
	before := tree(t, home)
	if r := fails(t, home, "conflict", "gate", "mv", "4", "/a.done", "/b"); fmt.Sprint(r.Error()["ids"]) != "[7]" {
		t.Errorf("gate: %s", r.Line())
	}
	fails(t, home, "not-found", "", "mv", "4", "99", "/b")
	fails(t, home, "invalid-input", "", "mv", "4", "@4", "/b")
	if fmt.Sprint(tree(t, home)) != fmt.Sprint(before) || field(t, home, "4", "folder").(map[string]any)["path"] != "/" {
		t.Errorf("a refused move moved something")
	}
}

// The command line of mv: the last word is the folder, or -t takes
// its place and every word is a task; task move and --to are gone.
func TestTaskMoveUsage(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	ok(t, home, "task", "create", "t")    // 2
	golden(t, invalidAt(t, home, "", "mv", "2"), "task-mv-no-object")
	golden(t, invalidAt(t, home, "/refs", "mv", "-t", "/w"), "task-mv-no-subject")
	invalidAt(t, home, "/refs", "mv")
	golden(t, usageAt(t, home, "--to", "mv", "2", "--to", "/w"), "task-mv-to")
	golden(t, usageAt(t, home, "move", "task", "move", "2", "/w"), "task-move-old")
	usageAt(t, home, "--to", "task", "move", "2", "--to", "/w")
	golden(t, usageAt(t, home, "--into", "mv", "-t", "/w", "--into", "/", "2"), "task-mv-into-twice")
	usageAt(t, home, "2", "mv", "2", "/w", "-i", "-")
	// With -t, every word is a subject: a folder there moves too, here
	// into itself.
	r := fails(t, home, "conflict", "into-itself", "mv", "-t", "/w", "2", "/w")
	golden(t, r, "task-mv-into-and-object")
	if field(t, home, "2", "folder").(map[string]any)["path"] != "/" {
		t.Errorf("a refused line moved the task")
	}
}

// A gate stays in its folder: moving one is refused, even to its own
// folder.
func TestTaskMoveGate(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")                         // 1
	ok(t, home, "task", "create", "t", "--blocked-by", "/a.done") // 2, 3
	golden(t, fails(t, home, "conflict", "gate", "mv", "/a.done", "/"), "task-mv-gate")
	if r := fails(t, home, "conflict", "gate", "mv", "3", "/a"); fmt.Sprint(r.Error()["ids"]) != "[3]" {
		t.Errorf("own folder: %s", r.Line())
	}
}

// A move that changes a claimed task's notes path warns moved-claimed,
// with its new notes path; one that changes nothing doesn't. Of several,
// it names the claimed ones that moved.
func TestTaskMoveClaimed(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a") // 1
	ok(t, home, "task", "create", "t")    // 2
	ok(t, home, "claim", "2")
	r := warned(t, home, []string{"moved-claimed"}, "mv", "2", "/a")
	golden(t, r, "task-mv-moved-claimed")
	w := r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[2]" || fmt.Sprint(w["paths"]) != "["+treePath(home, "@a/@2.md")+"]" {
		t.Errorf("warning: %v", w)
	}
	if field(t, home, "2", "claim") == nil {
		t.Errorf("the claim stays")
	}
	ok(t, home, "mv", "2", "/a")

	ok(t, home, "task", "create", "u") // 3
	ok(t, home, "task", "create", "v") // 4
	ok(t, home, "claim", "4")
	r = warned(t, home, []string{"moved-claimed"}, "mv", "4", "3", "2", "/")
	w = r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[2]" || fmt.Sprint(w["paths"]) != "["+treePath(home, "@2.md")+"]" {
		t.Errorf("of several: %v", w)
	}
}

// moved is each task and folder in a result's moved, as id and the path
// its kept text gives.
func moved(v any) string {
	var out []string
	for _, x := range v.([]any) {
		x := x.(map[string]any)
		ref := x["ref"].(string)
		out = append(out, fmt.Sprintf("%v %s", x["id"], ref[strings.Index(ref, "(")+1:len(ref)-1]))
	}
	return "[" + strings.Join(out, ", ") + "]"
}

// mv moves a folder into one that exists, or renames it, as mv
// moves and renames a directory: everything under it included, material
// too. No parent is made, and a move that changes nothing moves nothing.
func TestFolderMove(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w/api", "--notes", "api notes")              // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/w/api", "--notes", "t notes") // 3
	ok(t, home, "task", "create", "u", "--blocked-by", "/w.start")                 // 4, 5
	ok(t, home, "folder", "create", "/x/y")                                        // 6, 7
	writeTree(t, home, "@w/docs/plan.md", "plan")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "mv", "-T", "/w", "/x/y/w2")
	golden(t, r, "folder-mv")
	if fmt.Sprint(r.Result()) != fmt.Sprintf("map[moved:[map[dir:%s id:1 notes_path:%s ref:@1 (/x/y/w2)]]]",
		treePath(home, "@x/@y/@w2"), treePath(home, "@x/@y/@w2/@notes.md")) {
		t.Errorf("mv: %s", r.Line())
	}
	checkTree(t, home, "@x/", "@x/@notes.md=", "@x/@y/", "@x/@y/@notes.md=", "@x/@y/@w2/", "@x/@y/@w2/@notes.md=",
		"@x/@y/@w2/@start.md=", "@x/@y/@w2/docs/", "@x/@y/@w2/docs/plan.md=plan",
		"@x/@y/@w2/@api/", "@x/@y/@w2/@api/@notes.md=api notes", "@x/@y/@w2/@api/@3.md=t notes", "@4.md=")
	got := ok(t, home, "folder", "list", "--fields", "path,title").Result()["folders"]
	if fmt.Sprint(got) != "[map[id:6 path:/x title:<nil>] map[id:7 path:/x/y title:<nil>] map[id:1 path:/x/y/w2 title:<nil>] map[id:2 path:/x/y/w2/api title:<nil>]]" {
		t.Errorf("folders: %v", got)
	}
	if field(t, home, "3", "updated_at") != fixed || field(t, home, "4", "readiness") != "blocked" ||
		field(t, home, "5", "notes_path") != treePath(home, "@x/@y/@w2/@start.md") {
		t.Errorf("no field and no readiness changes")
	}
	// Renamed to its own path, or into the folder it is in, by any
	// reference: nothing moves.
	for i, subject := range []string{"/x/y/w2", "@1", "@6/y/w2"} {
		r := ok(t, home, "mv", "-T", subject, "/x/y/w2")
		if i == 0 {
			golden(t, r, "folder-mv-rename-itself")
		}
		if fmt.Sprint(r.Result()["moved"]) != "[]" {
			t.Errorf("-T %s: %s", subject, r.Line())
		}
		r = ok(t, home, "mv", subject, "/x/y")
		if i == 0 {
			golden(t, r, "folder-mv-own-parent")
		}
		if fmt.Sprint(r.Result()["moved"]) != "[]" {
			t.Errorf("%s into its own parent: %s", subject, r.Line())
		}
	}
	ok(t, home, "mv", "-T", "/x/y/w2", "@1")
	// With nothing at the destination, it is the new path; with a folder
	// there, the folder to move into.
	for _, c := range []struct {
		args         []string
		name, result string
	}{
		{[]string{"/x/y/w2", "/x/y/w3"}, "folder-mv-rename", "[1 /x/y/w3]"},
		{[]string{"@2", "/x"}, "folder-mv-into", "[2 /x/api]"},
		{[]string{"-t", "/", "@2"}, "folder-mv-into-flag", "[2 /api]"},
		{[]string{"/x/y/w3", "/"}, "folder-mv-into-root", "[1 /w3]"},
		{[]string{"-T", "@2", "@1/api2"}, "folder-mv-anchored", "[2 /w3/api2]"},
	} {
		r := ok(t, home, append([]string{"mv"}, c.args...)...)
		golden(t, r, c.name)
		if moved(r.Result()["moved"]) != c.result {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	checkTree(t, home, "@x/", "@x/@notes.md=", "@x/@y/", "@x/@y/@notes.md=",
		"@w3/", "@w3/@notes.md=", "@w3/@start.md=", "@w3/docs/", "@w3/docs/plan.md=plan",
		"@w3/@api2/", "@w3/@api2/@notes.md=api notes", "@w3/@api2/@3.md=t notes", "@4.md=")
}

// mv takes several folders, as words, joined by commas, or after
// -t; they move as one change, judged against the tree as the whole move
// would leave it, and moved lists those that moved in input order.
func TestFolderMoveSeveral(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a/p")                // 1, 2
	ok(t, home, "folder", "create", "/b/q")                // 3, 4
	ok(t, home, "folder", "create", "/c")                  // 5
	ok(t, home, "folder", "create", "/d")                  // 6
	ok(t, home, "task", "create", "t", "--folder", "/a/p") // 7
	r := ok(t, home, "mv", "/a/p", "/b/q", "/c")
	golden(t, r, "folder-mv-several")
	if moved(r.Result()["moved"]) != "[2 /c/p, 4 /c/q]" || field(t, home, "7", "notes_path") != treePath(home, "@c/@p/@7.md") {
		t.Errorf("several: %s", r.Line())
	}
	if r := ok(t, home, "mv", "/c/q,/c/p", "/d"); moved(r.Result()["moved"]) != "[4 /d/q, 2 /d/p]" {
		t.Errorf("with commas: %s", r.Line())
	}
	if r := ok(t, home, "mv", "--into", "/c", "/d/p", "/d/q"); moved(r.Result()["moved"]) != "[2 /c/p, 4 /c/q]" {
		t.Errorf("--into: %s", r.Line())
	}
	for _, c := range []struct{ name, in, result string }{
		{"folder-mv-input", `{"refs":["/c/p"],"dest":"/d"}`, "[2 /d/p]"},
		{"folder-mv-input-into", `{"refs":["/d/p",4],"into":"/c"}`, "[2 /c/p]"},
		{"folder-mv-input-rename", `{"refs":["/c/p"],"dest":"/c/p2","rename":true}`, "[2 /c/p2]"},
	} {
		r := input(t, home, "mv", c.in)
		golden(t, r, c.name)
		if moved(r.Result()["moved"]) != c.result {
			t.Errorf("%s: %s", c.in, r.Line())
		}
	}
	checkTree(t, home, "@a/", "@a/@notes.md=", "@b/", "@b/@notes.md=", "@c/", "@c/@notes.md=",
		"@c/@p2/", "@c/@p2/@notes.md=", "@c/@p2/@7.md=", "@c/@q/", "@c/@q/@notes.md=", "@d/", "@d/@notes.md=")

	// If any is refused, none moves.
	ok(t, home, "folder", "create", "/a/q") // 8
	ok(t, home, "folder", "create", "/b/Q") // 9
	before := tree(t, home)
	golden(t, fails(t, home, "not-found", "", "mv", "/c/p2", "/c/q", "/nothing"), "folder-mv-several-dest-missing")
	r = fails(t, home, "conflict", "exists", "mv", "/a/q", "/c/q", "/d")
	golden(t, r, "folder-mv-same-name")
	if fmt.Sprint(r.Error()["paths"]) != "[/d/q]" {
		t.Errorf("same name: %s", r.Line())
	}
	// Names that differ only in case, before a name taken.
	for _, args := range [][]string{{"/a/q", "/b/Q", "/d"}, {"/a/q", "/c/q", "/b/Q", "/d"}} {
		r := fails(t, home, "conflict", "case-mismatch", append([]string{"mv"}, args...)...)
		if fmt.Sprint(r.Error()["paths"]) != "[/d/Q /d/q]" || fmt.Sprint(r.Error()["ids"]) != "[8]" {
			t.Errorf("%q: %s", args, r.Line())
		}
	}
	// A folder that exists is never replaced, even by one moving out.
	fails(t, home, "conflict", "exists", "mv", "-t", "/c", "/a/q", "/c/q")
	r = fails(t, home, "invalid-input", "", "mv", "/a", "/a/q", "/d")
	golden(t, r, "folder-mv-nested")
	if problemFields(r) != "/refs/1" {
		t.Errorf("nested: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "mv", "/a/q", "@8", "/d"); problemFields(r) != "/refs/1" {
		t.Errorf("twice: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "mv", "-T", "/a/q,/b/Q", "/d/x"); problemFields(r) != "/refs/1" {
		t.Errorf("a rename of two: %s", r.Line())
	}
	fails(t, home, "not-found", "", "mv", "/a/q", "/zz", "/d")
	if fmt.Sprint(tree(t, home)) != fmt.Sprint(before) {
		t.Errorf("a refused move moved something")
	}
}

// A case-only rename is a move, through a temporary name, on every
// filesystem: the one input allowed to name both spellings, and only of
// the folder's own name. Its own directory is not in its way.
func TestFolderMoveCase(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/a")                // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/work/a") // 3
	r := ok(t, home, "mv", "/work", "/Work")
	golden(t, r, "folder-mv-case-only")
	checkTree(t, home, "@Work/", "@Work/@notes.md=", "@Work/@a/", "@Work/@a/@notes.md=", "@Work/@a/@3.md=")
	ok(t, home, "mv", "-T", "/Work/a", "/Work/A")
	checkTree(t, home, "@Work/", "@Work/@notes.md=", "@Work/@A/", "@Work/@A/@notes.md=", "@Work/@A/@3.md=")
	ok(t, home, "mv", "-T", "@1/A", "@1/a")
	ok(t, home, "mv", "/Work/a", "/Work/A")
	if field(t, home, "3", "notes_path") != treePath(home, "@Work/@A/@3.md") {
		t.Errorf("notes path")
	}
	// Two spellings of anything else in one input are invalid-input.
	for in, fields := range map[string]string{
		`{"refs":["/Work/A"],"dest":"/work/b"}`:             "/refs/0 /dest",
		`{"refs":["/Work/A"],"dest":"/WORK/A"}`:             "/refs/0 /dest",
		`{"refs":["/Work"],"dest":"/work/x"}`:               "/refs/0 /dest",
		`{"refs":["/Work/A"],"into":"/Work/a"}`:             "/refs/0 /into",
		`{"refs":["/Work/A","/Work/b"],"dest":"/Work/a"}`:   "/refs/0 /dest",
		`{"refs":["/Work/A"],"dest":"/work","rename":true}`: "/refs/0 /dest",
	} {
		if r := input(t, home, "mv", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// The refusals, each checked in the order the docs give: into-itself,
// then case-mismatch, then exists, by a folder or by an entry on disk.
// No parent is made, and nothing is ever replaced.
func TestFolderMoveRefused(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a/b") // 1, 2
	ok(t, home, "folder", "create", "/c")   // 3
	golden(t, fails(t, home, "conflict", "into-itself", "mv", "-T", "/a", "/a/b/x"), "folder-mv-into-itself")
	r := fails(t, home, "conflict", "into-itself", "mv", "/a", "/a")
	golden(t, r, "folder-mv-onto-itself")
	if fmt.Sprint(r.Error()["paths"]) != "[/a/a]" {
		t.Errorf("onto itself: %s", r.Line())
	}
	golden(t, fails(t, home, "conflict", "case-mismatch", "mv", "/a", "/C"), "folder-mv-case-mismatch")
	ok(t, home, "folder", "create", "/c/b") // 4
	golden(t, fails(t, home, "conflict", "exists", "mv", "/a/b", "/c"), "folder-mv-exists")
	golden(t, fails(t, home, "conflict", "exists", "mv", "-T", "/a", "/c"), "folder-mv-rename-flag")
	writeTree(t, home, "@Stray/x", "x")
	golden(t, fails(t, home, "conflict", "exists", "mv", "-T", "/c", "/stray"), "folder-mv-exists-on-disk")
	r = fails(t, home, "not-found", "", "mv", "-T", "/c", "/stray/c")
	golden(t, r, "folder-mv-no-parent")
	if fmt.Sprint(r.Error()["paths"]) != "[/stray]" {
		t.Errorf("no parent: %s", r.Line())
	}
	fails(t, home, "not-found", "", "mv", "/c", "/x/y")
	golden(t, fails(t, home, "not-found", "", "mv", "/c", "99"), "folder-mv-id-rename")
	fails(t, home, "not-found", "", "mv", "-T", "/c", "@99")
	fails(t, home, "not-found", "", "mv", "-t", "99", "/c")
	fails(t, home, "not-found", "", "mv", "-T", "/c", "@99/x")
	fails(t, home, "not-found", "", "mv", "/z", "/y")
	fails(t, home, "conflict", "case-mismatch", "mv", "/A", "/y")
	for _, c := range []struct {
		args       []string
		rule, path string
	}{
		// Into itself before the destination is taken, by a folder or on
		// disk, or its parent is missing.
		{[]string{"-T", "@1", "/a/b"}, "into-itself", "/a/b"},
		{[]string{"-T", "@1", "/a/b/c/d"}, "into-itself", "/a/b/c/d"},
		{[]string{"-T", "@2", "/a/b/x"}, "into-itself", "/a/b/x"},
		{[]string{"-T", "@1", "@2/x"}, "into-itself", "/a/b/x"},
		{[]string{"-T", "@1", "@1/x"}, "into-itself", "/a/x"},
		{[]string{"-T", "@1", "2"}, "into-itself", "/a/b"},
		{[]string{"@1", "/a/B/x"}, "into-itself", "/a/B/x"},
		{[]string{"@1", "/a/b"}, "into-itself", "/a/b/a"},
		{[]string{"-t", "@2", "@1"}, "into-itself", "/a/b/a"},
		// A parent that matches only ignoring case, before what is at
		// the end.
		{[]string{"-T", "@3", "/A/b"}, "case-mismatch", "/A"},
		{[]string{"-T", "/c", "/a/B"}, "case-mismatch", "/a/B"},
		{[]string{"-T", "/c", "/a/B/x"}, "case-mismatch", "/a/B"},
		{[]string{"/c", "/a/B"}, "case-mismatch", "/a/B"},
		// An entry on disk is in the way, by any spelling.
		{[]string{"/c", "/stray"}, "exists", treePath(home, "@Stray")},
		{[]string{"-T", "/c", "/STRAY"}, "exists", treePath(home, "@Stray")},
	} {
		r := fails(t, home, "conflict", c.rule, append([]string{"mv"}, c.args...)...)
		if paths := r.Error()["paths"].([]any); len(paths) == 0 || paths[0] != c.path {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	// Material inside is not in the way.
	writeTree(t, home, "@c/c", "x")
	ok(t, home, "mv", "/c", "/a")
	checkTree(t, home, "@a/", "@a/@notes.md=", "@a/@b/", "@a/@b/@notes.md=", "@a/@c/", "@a/@c/@notes.md=", "@a/@c/c=x",
		"@a/@c/@b/", "@a/@c/@b/@notes.md=", "@Stray/", "@Stray/x=x")
	golden(t, fails(t, home, "invalid-input", "", "mv", "/", "/x"), "folder-mv-root")
	r = fails(t, home, "invalid-input", "", "mv", "/a", "/a/c.start")
	golden(t, r, "folder-mv-end")
	if problemFields(r) != "/dest" {
		t.Errorf("end: %s", r.Line())
	}
	for in, fields := range map[string]string{
		`{"refs":["/a"]}`:                                    "",
		`{"dest":"/a"}`:                                      "/refs",
		`{"refs":[],"dest":"/a"}`:                            "/refs",
		`{"refs":"/a","dest":"/c"}`:                          "/refs",
		`{"refs":["/a/c"],"dest":"/"}`:                       "",
		`{"refs":["/a/c"],"dest":"/","rename":true}`:         "/dest",
		`{"refs":["/a/c"],"into":"/x.start"}`:                "/into",
		`{"refs":["/a/c"],"dest":"/a:docs"}`:                 "/dest",
		`{"refs":["/a:x"],"dest":"/x"}`:                      "/refs/0",
		`{"refs":["/"],"dest":"/x","y":1}`:                   "/y",
		`{"refs":["/a/c"],"dest":"/bad name"}`:               "/dest",
		`{"refs":["/a/c"],"dest":"/x","rename":"yes"}`:       "/rename",
		`{"refs":["/a/c"],"into":"/","rename":true}`:         "",
		`{"refs":["/a/c"],"dest":"/","into":"/a"}`:           "",
		`{"refs":["/a/c","/a/b"],"dest":"/x","rename":true}`: "/refs/1",
	} {
		r := input(t, home, "mv", in)
		if fields == "" && r.OK() {
			continue
		}
		if r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	golden(t, input(t, home, "mv", `{"refs":["/a/c"],"dest":"/","into":"/"}`), "folder-mv-input-both")
	golden(t, input(t, home, "mv", `{"refs":["/a/c","/a/b"],"dest":"/x","rename":true}`), "folder-mv-input-rename-several")
}

// On a filesystem that tells case apart, an entry beside a case-only
// rename's own directory, differing from its new name only in case, is
// in its way.
func TestFolderMoveCaseTaken(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")
	os.Mkdir(treePath(home, "@WORK"), 0o755)
	if _, err := os.Stat(treePath(home, "@WORK/@notes.md")); err == nil {
		t.Skip("the filesystem ignores case")
	}
	r := fails(t, home, "conflict", "exists", "mv", "/work", "/Work")
	if fmt.Sprint(r.Error()["paths"]) != "["+treePath(home, "@WORK")+"]" {
		t.Errorf("taken: %s", r.Line())
	}
}

// A move that changes the notes path of claimed tasks under the folders,
// at any depth and gates included, warns moved-claimed, lowest ID first,
// each with its new notes path.
func TestFolderMoveClaimed(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w/x")                         // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/w/x")          // 3
	ok(t, home, "task", "create", "u", "--blocked-by", "/w/x.done") // 4, 5
	ok(t, home, "task", "create", "v", "--folder", "/w")            // 6
	ok(t, home, "folder", "create", "/v")                           // 7
	ok(t, home, "claim", "5")
	ok(t, home, "claim", "3")
	ok(t, home, "claim", "4")
	r := warned(t, home, []string{"moved-claimed"}, "mv", "/w", "/v")
	golden(t, r, "folder-mv-moved-claimed")
	w := r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[3 5]" || fmt.Sprint(w["paths"]) != fmt.Sprintf("[%s %s]", treePath(home, "@v/@w/@x/@3.md"), treePath(home, "@v/@w/@x/@done.md")) {
		t.Errorf("warning: %v", w)
	}
	ok(t, home, "mv", "/v/w", "/v")

	ok(t, home, "folder", "create", "/s")                // 8
	ok(t, home, "task", "create", "s", "--folder", "/s") // 9
	ok(t, home, "claim", "9")
	r = warned(t, home, []string{"moved-claimed"}, "mv", "-t", "/v", "/s", "/v/w/x")
	w = r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[3 5 9]" || fmt.Sprint(w["paths"]) != fmt.Sprintf("[%s %s %s]",
		treePath(home, "@v/@x/@3.md"), treePath(home, "@v/@x/@done.md"), treePath(home, "@v/@s/@9.md")) {
		t.Errorf("of several: %v", w)
	}
}

// The command line of mv: the last word is the destination, or
// -t takes its place and every word is a folder; -T makes it the one
// folder's new path. folder move and --to are gone.
func TestFolderMoveUsage(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a") // 1
	ok(t, home, "folder", "create", "/c") // 2
	golden(t, invalidAt(t, home, "", "mv", "/a"), "folder-mv-no-object")
	invalidAt(t, home, "", "mv", "-T", "/a")
	invalidAt(t, home, "/refs", "mv", "-t", "/c")
	invalidAt(t, home, "/refs", "mv")
	golden(t, invalidAt(t, home, "/refs/1", "mv", "-T", "/a", "/b", "/c"), "folder-mv-rename-extra")
	golden(t, invalidAt(t, home, "", "mv", "-T", "-t", "/c", "/a"), "folder-mv-both-flags")
	invalidAt(t, home, "", "mv", "-t", "/c", "/a", "--rename")
	golden(t, usageAt(t, home, "move", "folder", "move", "/a", "/c"), "folder-move-old")
	usageAt(t, home, "--to", "mv", "/a", "--to", "/c")
	usageAt(t, home, "--into", "mv", "-t", "/c", "--into", "/", "/a")
	usageAt(t, home, "/a", "mv", "/a", "/c", "-i", "-")
	// With -t, every word is a folder to move.
	if r := koan(t, home, "mv", "-t", "/c", "/a", "/c"); r.Kind() != "conflict" || r.Error()["rule"] != "into-itself" {
		t.Errorf("the folder after -t: %s", r.Line())
	}
	if fmt.Sprint(tree(t, home)) != "[@a/ @a/@notes.md= @c/ @c/@notes.md=]" {
		t.Errorf("a refused line moved a folder: %q", tree(t, home))
	}
	// --rename=false is no rename, so it goes with -t, as through -i.
	if r := ok(t, home, "mv", "--rename=false", "-t", "/c", "/a"); fmt.Sprint(r.Result()["moved"]) == "[]" {
		t.Errorf("--rename=false with -t: %s", r.Line())
	}
	if fmt.Sprint(tree(t, home)) != "[@c/ @c/@a/ @c/@a/@notes.md= @c/@notes.md=]" {
		t.Errorf("--rename=false with -t: %q", tree(t, home))
	}
}

// mv takes tasks and folders mixed, as one change: each moves into the
// folder, a folder with everything under it, and moved lists each in
// input order with its kept text, its notes path, and a folder's
// directory, a task's null. moved-claimed names every claimed task whose
// notes moved, lowest ID first.
func TestMvMixed(t *testing.T) {
	home := root(t)
	setup(t, home, "folder", "create", "/a/p")                // 1, 2
	setup(t, home, "folder", "create", "/b")                  // 3
	setup(t, home, "task", "create", "t", "--folder", "/a")   // 4
	setup(t, home, "task", "create", "u", "--folder", "/a/p") // 5
	setup(t, home, "task", "create", "v", "--folder", "/b")   // 6
	setup(t, home, "claim", "5")
	setup(t, home, "claim", "4")
	r := warned(t, home, []string{"moved-claimed"}, "mv", "4", "/a/p", "6", "/b")
	golden(t, r, "mv-mixed")
	if moved(r.Result()["moved"]) != "[4 /b, 2 /b/p]" {
		t.Errorf("mixed: %s", r.Line())
	}
	m := r.Result()["moved"].([]any)
	if task, folder := m[0].(map[string]any), m[1].(map[string]any); task["dir"] != nil || task["notes_path"] != treePath(home, "@b/@4.md") ||
		folder["dir"] != treePath(home, "@b/@p") || folder["notes_path"] != treePath(home, "@b/@p/@notes.md") {
		t.Errorf("records: %s", r.Line())
	}
	w := r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[4 5]" || fmt.Sprint(w["paths"]) != fmt.Sprintf("[%s %s]", treePath(home, "@b/@4.md"), treePath(home, "@b/@p/@5.md")) {
		t.Errorf("warning: %v", w)
	}
	checkTree(t, home, "@a/", "@a/@notes.md=", "@b/", "@b/@notes.md=", "@b/@4.md=", "@b/@6.md=",
		"@b/@p/", "@b/@p/@notes.md=", "@b/@p/@5.md=")
	if r := warned(t, home, []string{"moved-claimed"}, "mv", "-t", "/", "/b/p", "6"); moved(r.Result()["moved"]) != "[2 /p, 6 /]" {
		t.Errorf("-t: %s", r.Line())
	}

	// A task inside a folder given goes with it: invalid-input at the
	// task, wherever it is in the list.
	r = fails(t, home, "invalid-input", "", "mv", "/p", "5", "/a")
	golden(t, r, "mv-inside")
	if problemFields(r) != "/refs/1" || r.Error()["message"] != "@5 is inside @2, also given" {
		t.Errorf("inside: %s", r.Line())
	}
	// With a task among the subjects, the destination must be a folder
	// that exists, so one that names nothing is not-found, never a
	// rename.
	r = fails(t, home, "not-found", "", "mv", "6", "/c")
	if fmt.Sprint(r.Error()["paths"]) != "[/c]" {
		t.Errorf("task to a new path: %s", r.Line())
	}
	fails(t, home, "not-found", "", "mv", "/p", "6", "/c")
	fails(t, home, "conflict", "case-mismatch", "mv", "6", "/A")
	// A task is a file that can't be renamed: -T with a task is
	// invalid-input at /rename, as through --input.
	sameFault(t, home, "invalid-input", "/rename", []string{"mv", "-T", "6", "/c"}, "mv", `{"refs":[6],"dest":"/c","rename":true}`)
	// A task as the destination is invalid-input there.
	sameFault(t, home, "invalid-input", "/dest", []string{"mv", "/p", "6"}, "mv", `{"refs":["/p"],"dest":6}`)
	sameFault(t, home, "invalid-input", "/into", []string{"mv", "-t", "6", "/p"}, "mv", `{"refs":["/p"],"into":6}`)
	// A gate is refused before the folders' own refusals.
	setup(t, home, "block", "6", "/p.start") // 7
	r = fails(t, home, "conflict", "gate", "mv", "/a", "/p.start", "/a")
	if fmt.Sprint(r.Error()["ids"]) != "[7]" {
		t.Errorf("gate first: %s", r.Line())
	}
	// Nothing refused moved anything.
	checkTree(t, home, "@a/", "@a/@notes.md=", "@b/", "@b/@notes.md=", "@b/@4.md=", "@6.md=", "@p/", "@p/@notes.md=", "@p/@5.md=", "@p/@start.md=")
}
