package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// task delete removes a task, its notes, its claim and every edge to and
// from it. What it was the last open blocker of is released, and every
// task that lost an edge to it gets a new updated_at. It never splices.
// Its ID is never issued again.
func TestTaskDelete(t *testing.T) {
	home := root(t)
	for range 4 {
		ok(t, home, "task", "create", "t") // 1 to 4
	}
	// 2 waits on 1; 3 on 2; 4 on 2 and 1.
	ok(t, home, "task", "block", "2", "1", "--blocks", "3,4")
	ok(t, home, "task", "block", "4", "1")
	ok(t, home, "task", "claim", "2")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	r := ok(t, home, "task", "delete", "2")
	golden(t, r, "task-delete")
	if fmt.Sprint(r.Result()) != "map[released:[3] removed:[2]]" {
		t.Errorf("delete: %s", r.Line())
	}
	checkTree(t, home, "@1.md=", "@3.md=", "@4.md=")
	for id, want := range map[string]string{"1": fixed, "3": "", "4": ""} {
		if got := field(t, home, id, "updated_at"); (want == fixed) != (got == fixed) {
			t.Errorf("@%s updated_at %v", id, got)
		}
	}
	// 3 isn't passed 2's blocker; 4 still waits on 1.
	if fmt.Sprint(field(t, home, "3", "blocked_by")) != "[]" || field(t, home, "4", "readiness") != "blocked" ||
		fmt.Sprint(field(t, home, "1", "blocks")) != "[map[id:4 ref:@4 (/) state:open]]" {
		t.Errorf("never splices")
	}
	r = fails(t, home, "not-found", "", "task", "show", "2")
	if fmt.Sprint(r.Error()["ids"]) != "[2]" {
		t.Errorf("deleted: %s", r.Line())
	}
	if r := ok(t, home, "task", "create", "t"); fmt.Sprint(r.Result()["id"]) != "5" {
		t.Errorf("reissued: %s", r.Line())
	}
	// A task with nothing waiting releases nothing; notes already gone
	// are no failure.
	os.Remove(treePath(home, "@5.md"))
	if r := ok(t, home, "task", "delete", "@5"); fmt.Sprint(r.Result()) != "map[released:[] removed:[5]]" {
		t.Errorf("alone: %s", r.Line())
	}
	golden(t, fails(t, home, "not-found", "", "task", "delete", "2"), "task-delete-not-found")
	if r := fails(t, home, "invalid-input", "", "task", "delete", "/"); problemFields(r) != "/tasks/0" {
		t.Errorf("the root: %s", r.Line())
	}
}

// A gate can be deleted like any task, its notes with it; its folder
// lacks it then, until an edge names it again.
func TestTaskDeleteGate(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")                          // 1
	ok(t, home, "task", "create", "t", "--blocked-by", "/w.start") // 2, 3
	writeTree(t, home, "@w/@start.md", "gate notes")
	r := ok(t, home, "task", "delete", "/w.start")
	if fmt.Sprint(r.Result()) != "map[released:[2] removed:[3]]" {
		t.Errorf("gate: %s", r.Line())
	}
	checkTree(t, home, "@w/", "@w/@notes.md=", "@2.md=")
	if g := ok(t, home, "folder", "show", "/w").Result()["folder"].(map[string]any)["gates"]; fmt.Sprint(g) != "map[done:<nil> start:<nil>]" {
		t.Errorf("gates: %v", g)
	}
	fails(t, home, "not-found", "", "task", "delete", "/w.start")
	fails(t, home, "conflict", "case-mismatch", "task", "delete", "/W.done")
}

// folder delete removes a folder and everything under it, gates included,
// as task delete removes a task; removed lists every task and folder
// removed, and released what outside it was left ready.
func TestFolderDelete(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w/a")                                    // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/w/a", "--notes", "notes") // 3
	ok(t, home, "task", "create", "u", "--blocked-by", "/w.done,3")            // 4, 5
	ok(t, home, "task", "create", "v", "--blocked-by", "5")                    // 6
	ok(t, home, "task", "block", "5", "3")
	ok(t, home, "task", "claim", "3")
	ok(t, home, "folder", "create", "/k") // 7
	writeTree(t, home, "@w/.DS_Store", "finder")
	writeTree(t, home, "@w/@a/@notes.md", "folder notes")
	writeTree(t, home, "@w/@done.md", "gate notes")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	golden(t, fails(t, home, "conflict", "not-empty", "folder", "delete", "/w"), "folder-delete-not-empty")
	r := ok(t, home, "folder", "delete", "/w", "--contents")
	golden(t, r, "folder-delete")
	if fmt.Sprint(r.Result()) != "map[released:[4 6] removed:[1 2 3 5]]" {
		t.Errorf("delete: %s", r.Line())
	}
	checkTree(t, home, "@4.md=", "@6.md=", "@k/", "@k/@notes.md=")
	if field(t, home, "4", "updated_at") == fixed || field(t, home, "6", "updated_at") == fixed {
		t.Errorf("what lost an edge gets a new updated_at")
	}
	if fmt.Sprint(field(t, home, "6", "blocked_by")) != "[]" || field(t, home, "6", "readiness") != "ready" {
		t.Errorf("6 waited on the gate alone")
	}
	for _, id := range []string{"1", "2", "3", "5"} {
		fails(t, home, "not-found", "", "task", "show", id)
	}
	if r := ok(t, home, "folder", "list"); fmt.Sprint(r.Result()["total"]) != "1" {
		t.Errorf("folders left: %s", r.Line())
	}
	// A folder holding only its gates is empty.
	ok(t, home, "task", "block", "4", "/k.start,/k.done") // 8, 9
	r = ok(t, home, "folder", "delete", "/k")
	if fmt.Sprint(r.Result()) != "map[released:[4] removed:[7 8 9]]" {
		t.Errorf("gates only: %s", r.Line())
	}
	checkTree(t, home, "@4.md=", "@6.md=")
	golden(t, fails(t, home, "invalid-input", "", "folder", "delete", "/"), "folder-delete-root")
	fails(t, home, "not-found", "", "folder", "delete", "/w")
	for in, fields := range map[string]string{
		`{}`:                                  "/folders",
		`{"folders":[]}`:                      "/folders",
		`{"folders":["/w.start"]}`:            "/folders/0",
		`{"folders":["/k"],"contents":"yes"}`: "/contents",
		`{"folders":["/k"],"material":1}`:     "/material",
		`{"folders":["/k"],"force":true}`:     "/force",
		`{"folders":["/"],"material":1}`:      "/folders/0 /material",
	} {
		if r := input(t, home, "folder delete", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// not-empty names its direct contents, its gates left out, and is
// checked before has-material.
func TestFolderDeleteNotEmpty(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w/b/c")                     // 1, 2, 3
	ok(t, home, "task", "create", "t", "--folder", "/w")          // 4
	ok(t, home, "task", "create", "u", "--blocked-by", "/w.done") // 5, 6
	ok(t, home, "folder", "create", "/w/a")                       // 7
	writeTree(t, home, "@w/thesis.tex", "t")
	r := fails(t, home, "conflict", "not-empty", "folder", "delete", "/w", "--material")
	if fmt.Sprint(r.Error()["ids"]) != "[2 4 7]" {
		t.Errorf("not-empty: %s", r.Line())
	}
	fails(t, home, "conflict", "has-material", "folder", "delete", "/w", "--contents")
}

// has-material names the material at any depth, each folder's own
// entries byte for byte before those of the folders under it: entries
// not named with @, an @ entry holding content that isn't the notes of
// what is being deleted, and never .DS_Store or a zero-byte @ file. A
// @start.md or @done.md is the notes of a gate the folder has, and
// otherwise a stray entry. --material takes it all.
func TestFolderDeleteMaterial(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w/b")                                   // 1, 2
	ok(t, home, "folder", "create", "/w/a")                                   // 3
	ok(t, home, "task", "create", "t", "--folder", "/w/a", "--notes", "mine") // 4
	ok(t, home, "task", "create", "u")                                        // 5
	ok(t, home, "task", "block", "5", "/w/a.done")                            // 6
	for rel, content := range map[string]string{
		"@w/docs/x.md":        "x",
		"@w/.DS_Store":        "finder",
		"@w/@todo.md":         "by hand",
		"@w/@empty.md":        "",
		"@w/@start.md":        "no gate's",
		"@w/@b/__init__.py":   "",
		"@w/@b/@5.md":         "another task's",
		"@w/@b/@done.md":      "no gate's",
		"@w/@a/@stray/@x.md":  "x",
		"@w/@a/@hollow/@y.md": "",
		"@w/@a/.git/HEAD":     "ref",
		"@w/@a/@done.md":      "its gate's",
	} {
		writeTree(t, home, rel, content)
	}
	r := fails(t, home, "conflict", "has-material", "folder", "delete", "/w", "--contents")
	golden(t, r, "folder-delete-has-material")
	if fmt.Sprint(r.Error()["paths"]) != "[/w:@start.md /w:@todo.md /w:docs /w/a:.git /w/a:@stray /w/b:@5.md /w/b:@done.md /w/b:__init__.py]" {
		t.Errorf("material: %s", r.Line())
	}
	r = ok(t, home, "folder", "delete", "/w", "--contents", "--material")
	if fmt.Sprint(r.Result()) != "map[released:[5] removed:[1 2 3 4 6]]" {
		t.Errorf("forced: %s", r.Line())
	}
	checkTree(t, home, "@5.md=")
	// With only what isn't material, --material isn't needed, and what isn't
	// material goes with it: a gate's notes, and a @start.md or @done.md
	// with no gate that is empty.
	ok(t, home, "folder", "create", "/v")         // 7
	ok(t, home, "task", "block", "5", "/v.start") // 8
	writeTree(t, home, "@v/.DS_Store", "finder")
	writeTree(t, home, "@v/@hollow/@y.md", "")
	writeTree(t, home, "@v/@start.md", "its gate's")
	writeTree(t, home, "@v/@done.md", "")
	ok(t, home, "folder", "delete", "/v")
	checkTree(t, home, "@5.md=")
}

// Once COMMIT succeeds the delete succeeds. A directory remove the
// operating system refuses stays pending; material written before the
// next write finds it makes it stuck, and the material stays, so the
// directory still on disk takes its path.
func TestFolderDeleteStuck(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	ok(t, home, "folder", "create", "/k") // 2
	dir := filepath.Join(home, "koan-root", "tree")
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	r := warned(t, home, []string{"pending-step"}, "folder", "delete", "/w")
	golden(t, r, "folder-delete-pending-step")
	if fmt.Sprint(r.Result()) != "map[released:[] removed:[1]]" {
		t.Errorf("delete: %s", r.Line())
	}
	os.Chmod(dir, 0o755)
	writeTree(t, home, "@w/thesis.tex", "written after commit")
	r = warned(t, home, []string{"stuck-step"}, "folder", "delete", "/k")
	golden(t, r, "folder-delete-stuck-step")
	checkTree(t, home, "@w/", "@w/thesis.tex=written after commit")
	golden(t, fails(t, home, "conflict", "exists", "folder", "create", "/w"), "folder-delete-taken")
	fails(t, home, "conflict", "exists", "folder", "create", "/W")
	warned(t, home, []string{"stuck-step"}, "folder", "create", "/m")
	r = fails(t, home, "conflict", "exists", "folder", "mv", "-T", "/m", "/w")
	if fmt.Sprint(r.Error()["paths"]) != "["+treePath(home, "@w")+"]" {
		t.Errorf("move: %s", r.Line())
	}
}

// task delete takes a list of tasks, comma-separated or as more words,
// and removes them as one change: if any is refused, none goes. removed
// is ascending. A task deleted in the same call is never released, and a
// task given twice, however it is written, is invalid-input at its later
// occurrence.
func TestTaskDeleteSeveral(t *testing.T) {
	home := root(t)
	for range 5 {
		ok(t, home, "task", "create", "t") // 1 to 5
	}
	// 2 waits on 1; 3 on 2; 5 on 4.
	ok(t, home, "task", "block", "2", "1", "--blocks", "3")
	ok(t, home, "task", "block", "5", "4")

	// A bad entry mid-list removes nothing.
	fails(t, home, "not-found", "", "task", "delete", "4", "99", "1")
	checkTree(t, home, "@1.md=", "@2.md=", "@3.md=", "@4.md=", "@5.md=")
	if r := fails(t, home, "invalid-input", "", "task", "delete", "1", "@1"); problemFields(r) != "/tasks/1" ||
		r.Error()["message"] != "@1 is given twice" {
		t.Errorf("duplicate: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "task", "delete", "1", "/"); problemFields(r) != "/tasks/1" {
		t.Errorf("the root: %s", r.Line())
	}
	if r := input(t, home, "task delete", `{"tasks":[]}`); r.Kind() != "invalid-input" || problemFields(r) != "/tasks" {
		t.Errorf("none: %s", r.Line())
	}
	// 2 is deleted with 1, so only 3 and 5 are released.
	r := ok(t, home, "task", "delete", "2,1", "4")
	golden(t, r, "task-delete-several")
	if fmt.Sprint(r.Result()) != "map[released:[3 5] removed:[1 2 4]]" {
		t.Errorf("several: %s", r.Line())
	}
	checkTree(t, home, "@3.md=", "@5.md=")
	for _, id := range []string{"1", "2", "4"} {
		fails(t, home, "not-found", "", "task", "show", id)
	}
	// Through --input.
	if r := input(t, home, "task delete", `{"tasks":[3,"@5"]}`); !r.OK() || fmt.Sprint(r.Result()) != "map[released:[] removed:[3 5]]" {
		t.Errorf("input: %s", r.Line())
	}
	checkTree(t, home)
}

// folder delete takes a list of folders and removes them as one change:
// if any is refused, none goes. A folder given twice is invalid-input at
// its later occurrence, and one inside another given is invalid-input at
// the one inside. not-empty names the direct contents of the first
// folder given that holds any; has-material lists the material of every
// folder given, in input order. --contents and --material apply to every
// folder; --force is gone.
func TestFolderDeleteSeveral(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a/x")                       // 1, 2
	ok(t, home, "folder", "create", "/b")                         // 3
	ok(t, home, "task", "create", "t", "--folder", "/b")          // 4
	ok(t, home, "task", "create", "u", "--blocked-by", "/a.done") // 5, 6
	ok(t, home, "folder", "create", "/c")                         // 7
	ok(t, home, "task", "create", "v", "--blocked-by", "4")       // 8
	writeTree(t, home, "@c/docs/x.md", "x")
	writeTree(t, home, "@a/@x/main.go", "package x")
	unchanged := func() {
		t.Helper()
		if r := ok(t, home, "folder", "list"); fmt.Sprint(r.Result()["total"]) != "4" {
			t.Errorf("a refused delete changed the tree: %s", r.Line())
		}
	}

	r := fails(t, home, "invalid-input", "", "folder", "delete", "/a/x", "/c", "/a")
	golden(t, r, "folder-delete-nested")
	if problemFields(r) != "/folders/0" {
		t.Errorf("nested: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "folder", "delete", "/a", "@2", "--contents", "--material"); problemFields(r) != "/folders/1" {
		t.Errorf("nested by ID: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "folder", "delete", "/c", "@7"); problemFields(r) != "/folders/1" ||
		r.Error()["message"] != "@7 is given twice" {
		t.Errorf("duplicate: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "folder", "delete", "/c", "/"); problemFields(r) != "/folders/1" {
		t.Errorf("the root: %s", r.Line())
	}
	if r := fails(t, home, "invalid-input", "", "folder", "delete", "/c", "/C"); problemFields(r) != "/folders/0 /folders/1" {
		t.Errorf("case: %s", r.Line())
	}
	fails(t, home, "not-found", "", "folder", "delete", "/c", "/nope")
	unchanged()

	// /c holds only material, so /b is the first that holds tasks.
	r = fails(t, home, "conflict", "not-empty", "folder", "delete", "/c", "/b", "/a")
	golden(t, r, "folder-delete-not-empty-first")
	if fmt.Sprint(r.Error()["ids"]) != "[4]" {
		t.Errorf("not-empty: %s", r.Line())
	}
	r = fails(t, home, "conflict", "has-material", "folder", "delete", "/c", "/b", "/a", "--contents")
	golden(t, r, "folder-delete-has-material-several")
	if fmt.Sprint(r.Error()["paths"]) != "[/c:docs /a/x:main.go]" {
		t.Errorf("has-material: %s", r.Line())
	}
	usageAt(t, home, "--force", "folder", "delete", "/c", "--force")
	golden(t, usageAt(t, home, "--force", "folder", "delete", "/c", "/b", "--contents", "--force"), "folder-delete-force")
	unchanged()

	// 6, the gate under /a, goes with it, releasing 5; 8 is released by
	// 4, deleted in /b.
	r = ok(t, home, "folder", "delete", "/c,/a", "/b", "--contents", "--material")
	golden(t, r, "folder-delete-several")
	if fmt.Sprint(r.Result()) != "map[released:[5 8] removed:[1 2 3 4 6 7]]" {
		t.Errorf("several: %s", r.Line())
	}
	checkTree(t, home, "@5.md=", "@8.md=")
	if r := ok(t, home, "folder", "list"); fmt.Sprint(r.Result()["total"]) != "0" {
		t.Errorf("folders left: %s", r.Line())
	}

	// Through --input, with material.
	ok(t, home, "folder", "create", "/d") // 9
	ok(t, home, "folder", "create", "/e") // 10
	writeTree(t, home, "@e/notes.txt", "kept by hand")
	r = input(t, home, "folder delete", `{"folders":["/d",10],"material":true}`)
	golden(t, r, "folder-delete-input")
	if !r.OK() || fmt.Sprint(r.Result()) != "map[released:[] removed:[9 10]]" {
		t.Errorf("input: %s", r.Line())
	}
	checkTree(t, home, "@5.md=", "@8.md=")
}

// Once COMMIT succeeds a list delete succeeds: a filesystem step the
// operating system refuses is a warning, the database stays committed,
// and the next write finishes the steps.
func TestDeleteSeveralPending(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	ok(t, home, "folder", "create", "/k") // 2
	ok(t, home, "task", "create", "t")    // 3
	ok(t, home, "task", "create", "u")    // 4
	dir := filepath.Join(home, "koan-root", "tree")
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	r := warned(t, home, []string{"pending-step"}, "folder", "delete", "/w", "/k")
	if fmt.Sprint(r.Result()) != "map[released:[] removed:[1 2]]" {
		t.Errorf("folder delete: %s", r.Line())
	}
	r = warned(t, home, []string{"pending-step"}, "task", "delete", "3", "4")
	if fmt.Sprint(r.Result()) != "map[released:[] removed:[3 4]]" {
		t.Errorf("task delete: %s", r.Line())
	}
	// @w's notes went before its rmdir was refused; the rest of the change
	// waits with it.
	checkTree(t, home, "@w/", "@k/", "@k/@notes.md=", "@3.md=", "@4.md=")
	os.Chmod(dir, 0o755)
	for _, id := range []string{"1", "2"} {
		fails(t, home, "not-found", "", "folder", "show", id)
	}
	for _, id := range []string{"3", "4"} {
		fails(t, home, "not-found", "", "task", "show", id)
	}
	ok(t, home, "folder", "create", "/m") // 5
	checkTree(t, home, "@m/", "@m/@notes.md=")
}
