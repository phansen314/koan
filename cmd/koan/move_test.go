package main_test

import (
	"fmt"
	"os"
	"path/filepath"
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

// task mv puts tasks in another folder or the root, their notes with
// them; no edge changes, so no readiness does, and updated_at stays. A
// task already in the folder doesn't move.
func TestTaskMove(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")                                     // 1
	ok(t, home, "folder", "create", "/b")                                     // 2
	ok(t, home, "task", "create", "t", "--folder", "/a", "--notes", "mine")   // 3
	ok(t, home, "task", "create", "u", "--folder", "/a", "--blocked-by", "3") // 4
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "task", "mv", "3", "/b")
	golden(t, r, "task-mv")
	if fmt.Sprint(r.Result()) != fmt.Sprintf("map[moved:[map[id:3 notes_path:%s]]]", treePath(home, "@b/@3.md")) {
		t.Errorf("mv: %s", r.Line())
	}
	checkTree(t, home, "@a/", "@a/@notes.md=", "@a/@4.md=", "@b/", "@b/@notes.md=", "@b/@3.md=mine")
	rec := ok(t, home, "task", "show", "3").Result()["task"].(map[string]any)
	if rec["updated_at"] != fixed || fmt.Sprint(rec["folder"]) != "map[id:2 path:/b]" || rec["notes_path"] != treePath(home, "@b/@3.md") {
		t.Errorf("moved: %v", rec)
	}
	if field(t, home, "4", "readiness") != "blocked" || field(t, home, "4", "updated_at") != fixed {
		t.Errorf("what waits on it doesn't change")
	}
	// Where it already is, by any reference: nothing moves.
	for i, into := range []string{"/b", "@2", "2"} {
		r := ok(t, home, "task", "mv", "@3", into)
		if i == 0 {
			golden(t, r, "task-mv-already-there")
		}
		if fmt.Sprint(r.Result()["moved"]) != "[]" {
			t.Errorf("into %s: %s", into, r.Line())
		}
	}
	// To the root; notes that are missing move as nothing.
	ok(t, home, "task", "mv", "3", "/")
	checkTree(t, home, "@a/", "@a/@notes.md=", "@a/@4.md=", "@b/", "@b/@notes.md=", "@3.md=mine")
	os.Remove(treePath(home, "@a/@4.md"))
	ok(t, home, "task", "mv", "4", "/b")
	checkTree(t, home, "@a/", "@a/@notes.md=", "@b/", "@b/@notes.md=", "@3.md=mine")
	if koan(t, home, "task", "show", "4").Result()["task"].(map[string]any)["folder"].(map[string]any)["path"] != "/b" {
		t.Errorf("moved without notes")
	}

	golden(t, fails(t, home, "not-found", "", "task", "mv", "3", "/z"), "task-mv-not-found")
	fails(t, home, "not-found", "", "task", "mv", "99", "/b")
	fails(t, home, "conflict", "case-mismatch", "task", "mv", "3", "/B")
	for in, fields := range map[string]string{
		`{"tasks":[3]}`:                       "/into",
		`{"into":"/b"}`:                       "/tasks",
		`{"tasks":[],"into":"/b"}`:            "/tasks",
		`{"tasks":3,"into":"/b"}`:             "/tasks",
		`{"tasks":[3],"into":"/b.start"}`:     "/into",
		`{"tasks":["/b"],"into":"/a"}`:        "/tasks/0",
		`{"tasks":["/a.start"],"into":"/A"}`:  "/tasks/0 /into",
		`{"tasks":[3],"into":"/b","x":1}`:     "/x",
		`{"tasks":[3],"into":"/b:docs/x.md"}`: "/into",
		`{"tasks":[3,"@3"],"into":"/b"}`:      "/tasks/1",
	} {
		if r := input(t, home, "task mv", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// task mv takes several tasks, as words, joined by commas, or after -t;
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

	r := ok(t, home, "task", "mv", "5", "4", "3", "/b")
	golden(t, r, "task-mv-several")
	if ids(r.Result()["moved"]) != "[5 3]" {
		t.Errorf("several: %s", r.Line())
	}
	checkTree(t, home, "@a/", "@a/@done.md=", "@a/@notes.md=", "@b/", "@b/@notes.md=", "@b/@3.md=", "@b/@4.md=", "@b/@5.md=", "@6.md=")

	r = ok(t, home, "task", "mv", "-t", "/a", "3", "4")
	golden(t, r, "task-mv-into")
	if ids(r.Result()["moved"]) != "[3 4]" {
		t.Errorf("-t: %s", r.Line())
	}
	if r := ok(t, home, "task", "mv", "--into", "/", "3,5", "4"); ids(r.Result()["moved"]) != "[3 5 4]" {
		t.Errorf("--into, with commas: %s", r.Line())
	}
	r = input(t, home, "task mv", `{"tasks":[3,5],"into":"/b"}`)
	golden(t, r, "task-mv-input")

	// If any is refused, none moves.
	before := tree(t, home)
	if r := fails(t, home, "conflict", "gate", "task", "mv", "4", "/a.done", "/b"); fmt.Sprint(r.Error()["ids"]) != "[7]" {
		t.Errorf("gate: %s", r.Line())
	}
	fails(t, home, "not-found", "", "task", "mv", "4", "99", "/b")
	fails(t, home, "invalid-input", "", "task", "mv", "4", "@4", "/b")
	if fmt.Sprint(tree(t, home)) != fmt.Sprint(before) || field(t, home, "4", "folder").(map[string]any)["path"] != "/" {
		t.Errorf("a refused move moved something")
	}
}

// The command line of task mv: the last word is the folder, or -t takes
// its place and every word is a task; task move and --to are gone.
func TestTaskMoveUsage(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	ok(t, home, "task", "create", "t")    // 2
	golden(t, usageAt(t, home, "", "task", "mv", "2"), "task-mv-no-object")
	golden(t, usageAt(t, home, "", "task", "mv", "-t", "/w"), "task-mv-no-subject")
	usageAt(t, home, "", "task", "mv")
	golden(t, usageAt(t, home, "--to", "task", "mv", "2", "--to", "/w"), "task-mv-to")
	golden(t, usageAt(t, home, "move", "task", "move", "2", "/w"), "task-move-old")
	usageAt(t, home, "--to", "task", "move", "2", "--to", "/w")
	golden(t, usageAt(t, home, "--into", "task", "mv", "-t", "/w", "--into", "/", "2"), "task-mv-into-twice")
	usageAt(t, home, "2", "task", "mv", "2", "/w", "-i", "-")
	// With -t, every word is a task: a folder there is no task.
	r := koan(t, home, "task", "mv", "-t", "/w", "2", "/w")
	if r.Kind() != "invalid-input" || problemFields(r) != "/tasks/1" {
		t.Errorf("a folder after -t: %s", r.Line())
	}
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
	golden(t, fails(t, home, "conflict", "gate", "task", "mv", "/a.done", "/"), "task-mv-gate")
	if r := fails(t, home, "conflict", "gate", "task", "mv", "3", "/a"); fmt.Sprint(r.Error()["ids"]) != "[3]" {
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
	ok(t, home, "task", "claim", "2")
	r := warned(t, home, []string{"moved-claimed"}, "task", "mv", "2", "/a")
	golden(t, r, "task-mv-moved-claimed")
	w := r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[2]" || fmt.Sprint(w["paths"]) != "["+treePath(home, "@a/@2.md")+"]" {
		t.Errorf("warning: %v", w)
	}
	if field(t, home, "2", "claim") == nil {
		t.Errorf("the claim stays")
	}
	ok(t, home, "task", "mv", "2", "/a")

	ok(t, home, "task", "create", "u") // 3
	ok(t, home, "task", "create", "v") // 4
	ok(t, home, "task", "claim", "4")
	r = warned(t, home, []string{"moved-claimed"}, "task", "mv", "4", "3", "2", "/")
	w = r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[2]" || fmt.Sprint(w["paths"]) != "["+treePath(home, "@2.md")+"]" {
		t.Errorf("of several: %v", w)
	}
}

// folder move moves or renames a folder, everything under it included,
// material too, making missing parents as folder create makes them. A
// move to its own path changes nothing.
func TestFolderMove(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w/api", "--notes", "api notes")              // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/w/api", "--notes", "t notes") // 3
	ok(t, home, "task", "create", "u", "--blocked-by", "/w.start")                 // 4, 5
	writeTree(t, home, "@w/docs/plan.md", "plan")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "folder", "move", "/w", "--to", "/x/y/w2")
	golden(t, r, "folder-move")
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
	// Its own path, by any reference, changes nothing and makes nothing.
	for _, subject := range []string{"/x/y/w2", "@1", "@6/y/w2"} {
		r := ok(t, home, "folder", "move", subject, "--to", "/x/y/w2")
		if fmt.Sprint(r.Result()) != fmt.Sprintf("map[changed:false created:[] dir:%s id:1 path:/x/y/w2]", treePath(home, "@x/@y/@w2")) {
			t.Errorf("%s: %s", subject, r.Line())
		}
	}
	// A rename in place, and up a level; no parent is made.
	r = ok(t, home, "folder", "move", "/x/y/w2", "--to", "/x/y/w3")
	if fmt.Sprint(r.Result()["created"]) != "[]" || r.Result()["path"] != "/x/y/w3" {
		t.Errorf("rename: %s", r.Line())
	}
	ok(t, home, "folder", "move", "@2", "--to", "/api")
	checkTree(t, home, "@x/", "@x/@notes.md=", "@x/@y/", "@x/@y/@notes.md=", "@x/@y/@w3/", "@x/@y/@w3/@notes.md=",
		"@x/@y/@w3/@start.md=", "@x/@y/@w3/docs/", "@x/@y/@w3/docs/plan.md=plan",
		"@api/", "@api/@notes.md=api notes", "@api/@3.md=t notes", "@4.md=")
}

// A case-only rename is a move, through a temporary name, on every
// filesystem: the one input allowed to name both spellings, and only of
// the folder's own name. Its own directory is not in its way.
func TestFolderMoveCase(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/a")                // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/work/a") // 3
	r := ok(t, home, "folder", "move", "/work", "--to", "/Work")
	golden(t, r, "folder-move-case-only")
	checkTree(t, home, "@Work/", "@Work/@notes.md=", "@Work/@a/", "@Work/@a/@notes.md=", "@Work/@a/@3.md=")
	ok(t, home, "folder", "move", "/Work/a", "--to", "/Work/A")
	checkTree(t, home, "@Work/", "@Work/@notes.md=", "@Work/@A/", "@Work/@A/@notes.md=", "@Work/@A/@3.md=")
	if field(t, home, "3", "notes_path") != treePath(home, "@Work/@A/@3.md") {
		t.Errorf("notes path")
	}
	// Two spellings of anything else in one input are invalid-input.
	for in, fields := range map[string]string{
		`{"folder":"/Work/A","to":"/work/b"}`: "/folder /to",
		`{"folder":"/Work/A","to":"/WORK/A"}`: "/folder /to",
		`{"folder":"/Work","to":"/work/x"}`:   "/folder /to",
	} {
		if r := input(t, home, "folder move", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// The refusals, each checked in the order the docs give: into-itself,
// then case-mismatch, then exists, by a folder or by an entry on disk.
func TestFolderMoveRefused(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a/b") // 1, 2
	ok(t, home, "folder", "create", "/c")   // 3
	golden(t, fails(t, home, "conflict", "into-itself", "folder", "move", "/a", "--to", "/a/b/x"), "folder-move-into-itself")
	golden(t, fails(t, home, "conflict", "case-mismatch", "folder", "move", "/a", "--to", "/C"), "folder-move-case-mismatch")
	golden(t, fails(t, home, "conflict", "exists", "folder", "move", "/a", "--to", "/c"), "folder-move-exists")
	writeTree(t, home, "@Stray/x", "x")
	golden(t, fails(t, home, "conflict", "exists", "folder", "move", "/c", "--to", "/stray"), "folder-move-exists-on-disk")
	for _, c := range []struct {
		args       []string
		rule, path string
	}{
		// Into itself before the destination is taken, by a folder or on
		// disk.
		{[]string{"@1", "--to", "/a/b"}, "into-itself", "/a/b"},
		{[]string{"@1", "--to", "/a/b/c/d"}, "into-itself", "/a/b/c/d"},
		{[]string{"@2", "--to", "/a/b/x"}, "into-itself", "/a/b/x"},
		// A parent that matches only ignoring case, before what is at
		// the end.
		{[]string{"@3", "--to", "/A/b"}, "case-mismatch", "/A"},
		{[]string{"/c", "--to", "/a/B"}, "case-mismatch", "/a/B"},
		// A missing parent made is a destination too.
		{[]string{"/c", "--to", "/stray/c"}, "exists", treePath(home, "@Stray")},
		{[]string{"/c", "--to", "/STRAY/c"}, "exists", treePath(home, "@Stray")},
	} {
		r := fails(t, home, "conflict", c.rule, append([]string{"folder", "move"}, c.args...)...)
		if paths := r.Error()["paths"].([]any); len(paths) == 0 || paths[0] != c.path {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	// Material inside is not in the way.
	writeTree(t, home, "@c/c", "x")
	ok(t, home, "folder", "move", "/c", "--to", "/a/c")
	checkTree(t, home, "@a/", "@a/@notes.md=", "@a/@b/", "@a/@b/@notes.md=", "@a/@c/", "@a/@c/@notes.md=", "@a/@c/c=x", "@Stray/", "@Stray/x=x")
	golden(t, fails(t, home, "invalid-input", "", "folder", "move", "/", "--to", "/x"), "folder-move-root")
	for in, fields := range map[string]string{
		`{"folder":"/a"}`:                  "/to",
		`{"folder":"/a","to":"/"}`:         "/to",
		`{"folder":"/a","to":"@1/x"}`:      "/to",
		`{"folder":"/a","to":"/x.start"}`:  "/to",
		`{"folder":"/a.start","to":"/x"}`:  "/folder",
		`{"folder":"/","to":"/x","y":1}`:   "/y",
		`{"folder":"/a","to":"/bad name"}`: "/to",
	} {
		if r := input(t, home, "folder move", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	fails(t, home, "not-found", "", "folder", "move", "/z", "--to", "/y")
	fails(t, home, "conflict", "case-mismatch", "folder", "move", "/A", "--to", "/y")
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
	r := fails(t, home, "conflict", "exists", "folder", "move", "/work", "--to", "/Work")
	if fmt.Sprint(r.Error()["paths"]) != "["+treePath(home, "@WORK")+"]" {
		t.Errorf("taken: %s", r.Line())
	}
}

// A move that changes the notes path of claimed tasks under the folder,
// at any depth and gates included, warns moved-claimed, lowest ID first,
// each with its new notes path.
func TestFolderMoveClaimed(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w/x")                         // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/w/x")          // 3
	ok(t, home, "task", "create", "u", "--blocked-by", "/w/x.done") // 4, 5
	ok(t, home, "task", "create", "v", "--folder", "/w")            // 6
	ok(t, home, "task", "claim", "5")
	ok(t, home, "task", "claim", "3")
	ok(t, home, "task", "claim", "4")
	r := warned(t, home, []string{"moved-claimed"}, "folder", "move", "/w", "--to", "/v/w")
	golden(t, r, "folder-move-moved-claimed")
	w := r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[3 5]" || fmt.Sprint(w["paths"]) != fmt.Sprintf("[%s %s]", treePath(home, "@v/@w/@x/@3.md"), treePath(home, "@v/@w/@x/@done.md")) {
		t.Errorf("warning: %v", w)
	}
	ok(t, home, "folder", "move", "/v/w", "--to", "/v/w")
}
