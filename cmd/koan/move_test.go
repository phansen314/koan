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

// task move puts a task in another folder or the root, its notes with
// it; no edge changes, so no readiness does, and updated_at stays. A move
// to where it already is changes nothing.
func TestTaskMove(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")                                     // 1
	ok(t, home, "folder", "create", "/b")                                     // 2
	ok(t, home, "task", "create", "t", "--folder", "/a", "--notes", "mine")   // 3
	ok(t, home, "task", "create", "u", "--folder", "/a", "--blocked-by", "3") // 4
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "task", "move", "3", "--to", "/b")
	golden(t, r, "task-move")
	if fmt.Sprint(r.Result()) != fmt.Sprintf("map[changed:true id:3 notes_path:%s]", treePath(home, "@b/@3.md")) {
		t.Errorf("move: %s", r.Line())
	}
	checkTree(t, home, "@a/", "@a/@notes.md=", "@a/@4.md=", "@b/", "@b/@notes.md=", "@b/@3.md=mine")
	rec := ok(t, home, "task", "show", "3").Result()["task"].(map[string]any)
	if rec["updated_at"] != fixed || fmt.Sprint(rec["folder"]) != "map[id:2 path:/b]" || rec["notes_path"] != treePath(home, "@b/@3.md") {
		t.Errorf("moved: %v", rec)
	}
	if field(t, home, "4", "readiness") != "blocked" || field(t, home, "4", "updated_at") != fixed {
		t.Errorf("what waits on it doesn't change")
	}
	// Where it already is, by any reference: nothing changes.
	for _, to := range []string{"/b", "@2", "2"} {
		if r := ok(t, home, "task", "move", "@3", "--to", to); fmt.Sprint(r.Result()["changed"]) != "false" ||
			r.Result()["notes_path"] != treePath(home, "@b/@3.md") {
			t.Errorf("--to %s: %s", to, r.Line())
		}
	}
	// To the root; notes that are missing move as nothing.
	ok(t, home, "task", "move", "3", "--to", "/")
	checkTree(t, home, "@a/", "@a/@notes.md=", "@a/@4.md=", "@b/", "@b/@notes.md=", "@3.md=mine")
	os.Remove(treePath(home, "@a/@4.md"))
	ok(t, home, "task", "move", "4", "--to", "/b")
	checkTree(t, home, "@a/", "@a/@notes.md=", "@b/", "@b/@notes.md=", "@3.md=mine")
	if koan(t, home, "task", "show", "4").Result()["task"].(map[string]any)["folder"].(map[string]any)["path"] != "/b" {
		t.Errorf("moved without notes")
	}

	golden(t, fails(t, home, "not-found", "", "task", "move", "3", "--to", "/z"), "task-move-not-found")
	fails(t, home, "not-found", "", "task", "move", "99", "--to", "/b")
	fails(t, home, "conflict", "case-mismatch", "task", "move", "3", "--to", "/B")
	for in, fields := range map[string]string{
		`{"task":3}`:                     "/to",
		`{"to":"/b"}`:                    "/task",
		`{"task":3,"to":"/b.start"}`:     "/to",
		`{"task":"/b","to":"/a"}`:        "/task",
		`{"task":"/a.start","to":"/A"}`:  "/task /to",
		`{"task":3,"to":"/b","x":1}`:     "/x",
		`{"task":3,"to":"/b:docs/x.md"}`: "/to",
	} {
		if r := input(t, home, "task move", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// A gate stays in its folder: moving one is refused, even to its own
// folder.
func TestTaskMoveGate(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")                         // 1
	ok(t, home, "task", "create", "t", "--blocked-by", "/a.done") // 2, 3
	golden(t, fails(t, home, "conflict", "gate", "task", "move", "/a.done", "--to", "/"), "task-move-gate")
	if r := fails(t, home, "conflict", "gate", "task", "move", "3", "--to", "/a"); fmt.Sprint(r.Error()["ids"]) != "[3]" {
		t.Errorf("own folder: %s", r.Line())
	}
}

// A move that changes a claimed task's notes path warns moved-claimed,
// with its new notes path; one that changes nothing doesn't.
func TestTaskMoveClaimed(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a") // 1
	ok(t, home, "task", "create", "t")    // 2
	ok(t, home, "task", "claim", "2")
	r := warned(t, home, []string{"moved-claimed"}, "task", "move", "2", "--to", "/a")
	golden(t, r, "task-move-moved-claimed")
	w := r.Envelope["warnings"].([]any)[0].(map[string]any)
	if fmt.Sprint(w["ids"]) != "[2]" || fmt.Sprint(w["paths"]) != "["+treePath(home, "@a/@2.md")+"]" {
		t.Errorf("warning: %v", w)
	}
	if field(t, home, "2", "claim") == nil {
		t.Errorf("the claim stays")
	}
	ok(t, home, "task", "move", "2", "--to", "/a")
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
