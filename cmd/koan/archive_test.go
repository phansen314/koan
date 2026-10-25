package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// archivePath is a path under a root's archive directory.
func archivePath(home, rel string) string {
	return filepath.Join(home, "koan-root", "archived", filepath.FromSlash(rel))
}

// archived is every entry under the archive's directory, as tree gives
// the tree's: the JSON files as their kind and ID only, since checkJSON
// reads them whole.
func archived(t *testing.T, home string) []string {
	t.Helper()
	dir := filepath.Join(home, "koan-root", "archived")
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel := filepath.ToSlash(p[len(dir)+1:])
		if d.IsDir() {
			out = append(out, rel+"/")
			return nil
		}
		b, err := os.ReadFile(p)
		if strings.HasSuffix(rel, ".json") {
			var f struct{ Kind string }
			json.Unmarshal(b, &f)
			b = []byte(f.Kind)
		}
		out = append(out, rel+"="+string(b))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func checkArchive(t *testing.T, home string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := archived(t, home); !slices.Equal(got, want) {
		t.Errorf("archive:\n got %q\nwant %q", got, want)
	}
}

// checkJSON checks an archived item's JSON against the spec's
// archive-file schema, its fields in the schema's order, and against its
// golden, archived_at written $NOW.
func checkJSON(t *testing.T, home, rel, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(archivePath(home, rel))
	if err != nil {
		t.Fatal(err)
	}
	s := testrun.Load(t)
	if err := s.Validate("archive-file", "", string(b)); err != nil {
		t.Errorf("%s fails archive-file: %v\n%s", rel, err, b)
	}
	if err := s.Order("archive-file", "", string(b)); err != nil {
		t.Errorf("%s: %v\n%s", rel, err, b)
	}
	if bytes.Contains(b, []byte("\n")) {
		t.Errorf("%s isn't one line", rel)
	}
	var f map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	r := &testrun.Result{Home: home, Stdout: string(b)}
	r.Golden(t, name, *update, map[string]string{`"archived_at":"` + f["archived_at"].(string) + `"`: `"archived_at":"$NOW"`})
	return f
}

// archivedTo is where the index says an archived ID went, from the
// not-found a show of it gives.
func archivedTo(t *testing.T, home string, args ...string) string {
	t.Helper()
	r := fails(t, home, "not-found", "", args...)
	a := r.Error()["archived"].([]any)
	if len(a) != 1 {
		t.Fatalf("archived: %s", r.Line())
	}
	return a[0].(map[string]any)["archive"].(string)
}

// archivedItem is the one item an archive of one subject lists.
func archivedItem(t *testing.T, r *testrun.Result) map[string]any {
	t.Helper()
	a, _ := r.Result()["archived"].([]any)
	if len(a) != 1 {
		t.Fatalf("archived: %s", r.Line())
	}
	return a[0].(map[string]any)
}

// archive moves a complete task's notes to the archive, under
// directories named after the folders it was in, with its JSON beside
// them; its ID is retired, and every edge to and from it is removed, the
// archive keeping them. What waited on it gets a new updated_at; what it
// waited on doesn't.
func TestTaskArchive(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/HOME-777") // 1, 2
	ok(t, home, "task", "create", "Book flights", "--folder", "/work/HOME-777", "--notes", "plan",
		"--priority", "2", "--tags", "trip,a", "--extra", `{"z":1,"a":"x"}`) // 3
	ok(t, home, "task", "create", "u", "--blocked-by", "3") // 4
	ok(t, home, "task", "create", "v", "--blocks", "3")     // 5
	ok(t, home, "complete", "5")
	ok(t, home, "complete", "3")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"', created_at = '"+fixed+"'",
		"UPDATE task SET completed_at = '"+fixed+"' WHERE completed_at IS NOT NULL")

	r := fails(t, home, "conflict", "not-finished", "archive", "4")
	golden(t, r, "archive-not-finished")
	if fmt.Sprint(r.Error()["ids"]) != "[4]" {
		t.Errorf("not-finished: %s", r.Line())
	}
	r = ok(t, home, "archive", "3")
	golden(t, r, "archive")
	if fmt.Sprint(r.Result()) != fmt.Sprintf("map[archived:[map[archive:%s id:3 json:%s]] removed:[3]]",
		archivePath(home, "work/HOME-777/@3.md"), archivePath(home, "work/HOME-777/@3.json")) {
		t.Errorf("archive: %s", r.Line())
	}
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@HOME-777/", "@work/@HOME-777/@notes.md=", "@4.md=", "@5.md=")
	checkArchive(t, home, "work/", "work/HOME-777/", "work/HOME-777/@3.md=plan", "work/HOME-777/@3.json=task")
	f := checkJSON(t, home, "work/HOME-777/@3.json", "archive-file-task")
	if fmt.Sprint(f["edges"]) != "[map[blocker:5 task:3] map[blocker:3 task:4]]" || f["path"] != "/work/HOME-777" {
		t.Errorf("json: %v", f)
	}

	// 4 lost an entry from blocked_by; 5 only from blocks.
	if field(t, home, "4", "updated_at") == fixed || field(t, home, "5", "updated_at") != fixed {
		t.Errorf("updated_at follows blocked_by")
	}
	if fmt.Sprint(field(t, home, "4", "blocked_by")) != "[]" || field(t, home, "4", "readiness") != "ready" ||
		fmt.Sprint(field(t, home, "5", "blocks")) != "[]" {
		t.Errorf("edges left")
	}

	// Named later, it is not-found, saying where it went.
	r = fails(t, home, "not-found", "", "show", "3")
	golden(t, r, "archive-not-found")
	if !strings.Contains(r.Error()["message"].(string), archivePath(home, "work/HOME-777/@3.md")) {
		t.Errorf("message: %s", r.Line())
	}
	if got := archivedTo(t, home, "archive", "@3"); got != archivePath(home, "work/HOME-777/@3.md") {
		t.Errorf("archived to %s", got)
	}
	r = fails(t, home, "not-found", "", "block", "4", "3")
	if fmt.Sprint(r.Error()["archived"]) != fmt.Sprintf("[map[archive:%s id:3]]", archivePath(home, "work/HOME-777/@3.md")) {
		t.Errorf("an edge to it: %s", r.Line())
	}
	// Its ID is never issued again.
	if r := ok(t, home, "task", "create", "w"); fmt.Sprint(r.Result()["id"]) != "6" {
		t.Errorf("reissued: %s", r.Line())
	}

	if r := fails(t, home, "invalid-input", "", "archive", "/"); problemFields(r) != "/refs/0" {
		t.Errorf("the root: %s", r.Line())
	}
	// A gate not made yet names nothing.
	r = fails(t, home, "not-found", "", "archive", "/work.done")
	golden(t, r, "archive-gate-not-made")
	if fmt.Sprint(r.Error()["paths"]) != "[/work.done]" {
		t.Errorf("a gate not made: %s", r.Line())
	}
	if r := fails(t, home, "not-found", "", "archive", "99"); r.Error()["message"] != "@99 was never issued" {
		t.Errorf("never issued: %s", r.Line())
	}
	fails(t, home, "conflict", "case-mismatch", "archive", "/Work.done")
	for in, fields := range map[string]string{`{}`: "/refs", `{"refs":[]}`: "/refs", `{"refs":["3"],"x":1}`: "/x",
		`{"refs":["@1:x"]}`: "/refs/0", `{"task":"4"}`: "/refs /task"} {
		if r := input(t, home, "archive", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// A task whose notes file is missing is archived as its JSON alone, and
// the index names the JSON, so it names a file that exists. Notes are
// looked for by listing each directory, byte for byte, so notes in a
// directory respelled by hand aren't there on any filesystem. A task in
// the root goes to the archive's top.
func TestTaskArchiveNoNotes(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "t") // 1
	ok(t, home, "complete", "1")
	os.Remove(treePath(home, "@1.md"))
	r := ok(t, home, "archive", "1")
	golden(t, r, "archive-no-notes")
	if archivedItem(t, r)["archive"] != nil || archivedItem(t, r)["json"] != archivePath(home, "@1.json") {
		t.Errorf("no notes: %s", r.Line())
	}
	checkArchive(t, home, "@1.json=task")
	if got := archivedTo(t, home, "show", "1"); got != archivePath(home, "@1.json") {
		t.Errorf("index: %s", got)
	}

	// With its notes there, a task in the root goes to the archive's top.
	ok(t, home, "task", "create", "u") // 2
	ok(t, home, "complete", "2")
	r = ok(t, home, "archive", "2")
	if archivedItem(t, r)["archive"] != archivePath(home, "@2.md") {
		t.Errorf("root: %s", r.Line())
	}
	checkArchive(t, home, "@1.json=task", "@2.md=", "@2.json=task")
}

// A gate archived on its own gets its ID in its name, @<id>_start.md, so
// its folder's next start gate can follow it there. What waited on it
// loses the edge.
func TestTaskArchiveGate(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api")                          // 1, 2
	ok(t, home, "task", "create", "x", "--blocked-by", "/work/api.start") // 3, 4
	writeTree(t, home, "@work/@api/@start.md", "kickoff")
	ok(t, home, "complete", "/work/api.start")
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"', created_at = '"+fixed+"'",
		"UPDATE task SET completed_at = '"+fixed+"' WHERE completed_at IS NOT NULL")
	r := ok(t, home, "archive", "/work/api.start")
	golden(t, r, "archive-gate")
	if fmt.Sprint(r.Result()["removed"]) != "[4]" || archivedItem(t, r)["archive"] != archivePath(home, "work/api/@4_start.md") ||
		archivedItem(t, r)["json"] != archivePath(home, "work/api/@4_start.json") {
		t.Errorf("gate: %s", r.Line())
	}
	checkArchive(t, home, "work/", "work/api/", "work/api/@4_start.md=kickoff", "work/api/@4_start.json=task")
	f := checkJSON(t, home, "work/api/@4_start.json", "archive-file-gate")
	task := f["tasks"].([]any)[0].(map[string]any)
	if task["gate"] != "start" || fmt.Sprint(task["folder"]) != "map[id:2 path:/work/api]" {
		t.Errorf("json: %v", f)
	}
	if field(t, home, "3", "updated_at") == fixed || fmt.Sprint(field(t, home, "3", "blocked_by")) != "[]" {
		t.Errorf("what waited on it")
	}
	if got := archivedTo(t, home, "show", "4"); got != archivePath(home, "work/api/@4_start.md") {
		t.Errorf("index: %s", got)
	}
	// The folder lacks it now; the next start gate is made afresh, with
	// notes of its own in the tree.
	fails(t, home, "not-found", "", "show", "/work/api.start")
	ok(t, home, "block", "3", "/work/api.start") // 5
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@api/", "@work/@api/@notes.md=", "@work/@api/@start.md=", "@3.md=")
	ok(t, home, "complete", "5")
	ok(t, home, "archive", "5")
	checkArchive(t, home, "work/", "work/api/", "work/api/@4_start.md=kickoff", "work/api/@4_start.json=task",
		"work/api/@5_start.md=", "work/api/@5_start.json=task")
}

// archive moves a finished folder's directory to the archive,
// renamed @<id>_<name>, with everything in it as it was: notes, the
// folders under it, and material, hidden files included. Every ID that
// left is retired, each indexed where it itself sits.
func TestFolderArchive(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/HOME-12345/api", "--title", "API", "--extra", `{"k":"v"}`)                  // 1, 2, 3
	ok(t, home, "task", "create", "t", "--folder", "/work/HOME-12345", "--notes", "mine")                              // 4
	ok(t, home, "task", "create", "u", "--folder", "/work/HOME-12345/api", "--blocked-by", "/work/HOME-12345.start,4") // 5, 6
	ok(t, home, "task", "create", "outside", "--blocked-by", "/work/HOME-12345.done")                                  // 7, 8
	ok(t, home, "task", "create", "before")                                                                            // 9
	ok(t, home, "block", "4", "9")
	for rel, content := range map[string]string{
		"@work/@HOME-12345/docs/x.md":      "x",
		"@work/@HOME-12345/.hidden":        "h",
		"@work/@HOME-12345/.git/HEAD":      "ref",
		"@work/@HOME-12345/@start.md":      "go",
		"@work/@HOME-12345/@api/@notes.md": "api notes",
	} {
		writeTree(t, home, rel, content)
	}
	r := fails(t, home, "conflict", "not-finished", "archive", "/work/HOME-12345")
	golden(t, r, "archive-folder-not-finished")
	if fmt.Sprint(r.Error()["ids"]) != "[4 5 6 8]" {
		t.Errorf("not-finished: %s", r.Line())
	}
	for _, id := range []string{"9", "6", "4", "5", "8"} {
		ok(t, home, "complete", id)
	}
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"', created_at = '"+fixed+"'",
		"UPDATE task SET completed_at = '"+fixed+"' WHERE completed_at IS NOT NULL")
	r = ok(t, home, "archive", "/work/HOME-12345")
	golden(t, r, "archive-folder")
	dir := archivePath(home, "work/@2_HOME-12345")
	if fmt.Sprint(r.Result()) != fmt.Sprintf("map[archived:[map[archive:%s id:2 json:%s.json]] removed:[2 3 4 5 6 8]]", dir, dir) {
		t.Errorf("archive: %s", r.Line())
	}
	checkTree(t, home, "@work/", "@work/@notes.md=", "@7.md=", "@9.md=")
	checkArchive(t, home, "work/", "work/@2_HOME-12345/", "work/@2_HOME-12345.json=folder",
		"work/@2_HOME-12345/@notes.md=", "work/@2_HOME-12345/@4.md=mine", "work/@2_HOME-12345/@start.md=go",
		"work/@2_HOME-12345/@done.md=", "work/@2_HOME-12345/docs/", "work/@2_HOME-12345/docs/x.md=x",
		"work/@2_HOME-12345/.hidden=h", "work/@2_HOME-12345/.git/", "work/@2_HOME-12345/.git/HEAD=ref",
		"work/@2_HOME-12345/@api/", "work/@2_HOME-12345/@api/@notes.md=api notes", "work/@2_HOME-12345/@api/@5.md=")
	f := checkJSON(t, home, "work/@2_HOME-12345.json", "archive-file-folder")
	var folders, tasks []string
	for _, x := range f["folders"].([]any) {
		x := x.(map[string]any)
		folders = append(folders, fmt.Sprintf("%v %v %v", x["id"], x["parent"], x["path"]))
	}
	for _, x := range f["tasks"].([]any) {
		tasks = append(tasks, fmt.Sprint(x.(map[string]any)["id"]))
	}
	if fmt.Sprint(folders) != "[2 1 /work/HOME-12345 3 2 /work/HOME-12345/api]" || fmt.Sprint(tasks) != "[4 6 8 5]" ||
		fmt.Sprint(f["edges"]) != "[map[blocker:9 task:4] map[blocker:4 task:5] map[blocker:6 task:5] map[blocker:8 task:7]]" {
		t.Errorf("json: folders %v, tasks %v, edges %v", folders, tasks, f["edges"])
	}

	// Each ID is indexed where it itself sits.
	for args, want := range map[string]string{
		"show 2":    dir,
		"show 3":    dir + "/@api",
		"show 4":    dir + "/@4.md",
		"show 5":    dir + "/@api/@5.md",
		"show 6":    dir + "/@start.md",
		"show 8":    dir + "/@done.md",
		"show @3/x": dir + "/@api",
		"archive 6": dir + "/@start.md",
		"archive 3": dir + "/@api",
	} {
		if got := archivedTo(t, home, strings.Fields(args)...); got != want {
			t.Errorf("%s: %s, want %s", args, got, want)
		}
	}
	golden(t, fails(t, home, "not-found", "", "show", "2"), "archive-folder-not-found")
	// What waited on it lost the edge and is ready; what it waited on
	// keeps its updated_at.
	if field(t, home, "7", "updated_at") == fixed || field(t, home, "7", "readiness") != "ready" ||
		field(t, home, "9", "updated_at") != fixed || fmt.Sprint(field(t, home, "9", "blocks")) != "[]" {
		t.Errorf("what stays")
	}
	// The path is free again, and a folder made there later archives
	// beside the first: the ID tells them apart.
	ok(t, home, "folder", "create", "/work/HOME-12345") // 10
	golden(t, ok(t, home, "archive", "10"), "archive-folder-by-id")
	checkArchive(t, home, "work/", "work/@2_HOME-12345/", "work/@2_HOME-12345.json=folder",
		"work/@2_HOME-12345/@notes.md=", "work/@2_HOME-12345/@4.md=mine", "work/@2_HOME-12345/@start.md=go",
		"work/@2_HOME-12345/@done.md=", "work/@2_HOME-12345/docs/", "work/@2_HOME-12345/docs/x.md=x",
		"work/@2_HOME-12345/.hidden=h", "work/@2_HOME-12345/.git/", "work/@2_HOME-12345/.git/HEAD=ref",
		"work/@2_HOME-12345/@api/", "work/@2_HOME-12345/@api/@notes.md=api notes", "work/@2_HOME-12345/@api/@5.md=",
		"work/@10_HOME-12345/", "work/@10_HOME-12345/@notes.md=", "work/@10_HOME-12345.json=folder")

	golden(t, fails(t, home, "invalid-input", "", "archive", "/"), "archive-root")
	fails(t, home, "conflict", "case-mismatch", "archive", "/Work")
	for in, fields := range map[string]string{`{}`: "/refs", `{"refs":["/work:x"]}`: "/refs/0", `{"refs":["/work"],"force":true}`: "/force",
		`{"folder":"/work"}`: "/folder /refs"} {
		if r := input(t, home, "archive", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
}

// An empty folder is finished, and a folder whose tasks' notes are
// missing archives with them indexed where they would sit. A folder in
// the root goes to the archive's top.
func TestFolderArchiveEmpty(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/f/g")                // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/f/g") // 3
	ok(t, home, "complete", "3")
	os.Remove(treePath(home, "@f/@g/@3.md"))
	r := ok(t, home, "archive", "/f")
	if fmt.Sprint(r.Result()["removed"]) != "[1 2 3]" || archivedItem(t, r)["archive"] != archivePath(home, "@1_f") {
		t.Errorf("archive: %s", r.Line())
	}
	checkArchive(t, home, "@1_f/", "@1_f.json=folder", "@1_f/@notes.md=", "@1_f/@g/", "@1_f/@g/@notes.md=")
	if got := archivedTo(t, home, "show", "3"); got != archivePath(home, "@1_f/@g/@3.md") {
		t.Errorf("index: %s", got)
	}
	checkTree(t, home)
}

// Where a directory above an item already exists differing only in case,
// it is used, and the index records it as spelled on disk. Archiving
// never refuses for case.
func TestArchiveCase(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/Work") // 1
	for range 2 {
		ok(t, home, "task", "create", "t", "--folder", "/Work") // 2, 3
	}
	ok(t, home, "complete", "2")
	ok(t, home, "complete", "3")
	ok(t, home, "archive", "2")
	ok(t, home, "mv", "-T", "/Work", "/work")
	r := ok(t, home, "archive", "3")
	if archivedItem(t, r)["archive"] != archivePath(home, "Work/@3.md") {
		t.Errorf("on disk: %s", r.Line())
	}
	if got := archivedTo(t, home, "show", "3"); got != archivePath(home, "Work/@3.md") {
		t.Errorf("index: %s", got)
	}
	checkArchive(t, home, "Work/", "Work/@2.md=", "Work/@2.json=task", "Work/@3.md=", "Work/@3.json=task")
}

// So does a directory a pending archive step will make: with the
// archive's directory refused, the first step stays pending, and a later
// change takes its spelling, so both land in one directory once the
// steps run. Whether notes are there is judged by listing when the change
// is made, whatever pending steps will do.
func TestArchiveCasePending(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := root(t)
	ok(t, home, "folder", "create", "/Work")                // 1
	ok(t, home, "task", "create", "t", "--folder", "/Work") // 2
	ok(t, home, "complete", "2")
	dir := filepath.Join(home, "koan-root", "archived")
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	pending := []string{"pending-step"}
	r := warned(t, home, pending, "archive", "2")
	golden(t, r, "archive-pending-step")
	if archivedItem(t, r)["archive"] != archivePath(home, "Work/@2.md") {
		t.Errorf("pending: %s", r.Line())
	}
	// The rename waits behind the archive step, its paths overlapping.
	warned(t, home, pending, "mv", "-T", "/Work", "/work")
	warned(t, home, pending, "task", "create", "u", "--folder", "/work") // 3
	warned(t, home, pending, "complete", "3")
	// Its notes are still to be written, so they aren't there when the
	// change is made: the index names its JSON. The write runs before the
	// archive step, so the notes still go into the archive, beside it.
	r = warned(t, home, pending, "archive", "3")
	if archivedItem(t, r)["archive"] != nil || archivedItem(t, r)["json"] != archivePath(home, "Work/@3.json") {
		t.Errorf("after pending: %s", r.Line())
	}
	os.Chmod(dir, 0o755)
	ok(t, home, "task", "create", "v") // 4
	if got := archivedTo(t, home, "show", "3"); got != archivePath(home, "Work/@3.json") {
		t.Errorf("index: %s", got)
	}
	checkArchive(t, home, "Work/", "Work/@2.md=", "Work/@2.json=task", "Work/@3.md=", "Work/@3.json=task")
	checkTree(t, home, "@work/", "@work/@notes.md=", "@4.md=")
}

// A folder whose directory is missing archives in the database, and its
// step, finding neither side, is stuck: once COMMIT succeeds the change
// succeeds.
func TestFolderArchiveStuck(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/f") // 1
	os.RemoveAll(treePath(home, "@f"))
	r := warned(t, home, []string{"stuck-step"}, "archive", "/f")
	golden(t, r, "archive-folder-stuck-step")
	if fmt.Sprint(r.Result()["removed"]) != "[1]" {
		t.Errorf("archive: %s", r.Line())
	}
	fails(t, home, "not-found", "", "show", "1")
}

// archive takes its subjects through -i as refs, tasks and folders.
func TestArchiveInput(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")                // 1
	ok(t, home, "task", "create", "t", "--folder", "/work") // 2
	ok(t, home, "complete", "2")
	golden(t, input(t, home, "archive", `{"refs":["@2"]}`), "archive-input")
	golden(t, input(t, home, "archive", `{"refs":["/work"]}`), "archive-input-folder")
	checkArchive(t, home, "work/", "work/@2.md=", "work/@2.json=task", "@1_work/", "@1_work/@notes.md=", "@1_work.json=folder")
}

// archiveFileOf is an archived item's JSON, read whole.
func archiveFileOf(t *testing.T, home, rel string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(archivePath(home, rel))
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// archive takes tasks and folders, mixed, comma-separated or as more
// words, as one change: one refused among them archives none, and a
// subject given twice, or inside a folder also given, is invalid-input at
// the later or inner one. archived lists each subject in input order with
// where it went; removed is every ID that left. Each archive keeps every
// edge its item had, those to the others archived with it included.
func TestArchiveSeveral(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/a")                                  // 1, 2
	ok(t, home, "task", "create", "t", "--folder", "/work", "--notes", "three") // 3
	ok(t, home, "task", "create", "u", "--folder", "/work/a")                   // 4
	ok(t, home, "task", "create", "v", "--blocked-by", "3")                     // 5
	ok(t, home, "task", "create", "w", "--blocked-by", "3")                     // 6
	ok(t, home, "complete", "3", "6")

	before, files := dump(t, home), tree(t, home)
	unchanged := func(what string) {
		t.Helper()
		if dump(t, home) != before || !slices.Equal(tree(t, home), files) {
			t.Errorf("%s changed the tree", what)
		}
		checkArchive(t, home)
	}
	r := fails(t, home, "conflict", "not-finished", "archive", "3", "/work/a", "6")
	if fmt.Sprint(r.Error()["ids"]) != "[4]" {
		t.Errorf("not-finished: %s", r.Line())
	}
	unchanged("a refused archive")
	// Every open task of every subject is listed, in ascending order.
	r = fails(t, home, "conflict", "not-finished", "archive", "5", "/work/a", "3")
	if fmt.Sprint(r.Error()["ids"]) != "[4 5]" {
		t.Errorf("not-finished lists every subject: %s", r.Line())
	}
	unchanged("a refused archive of several")
	for _, c := range []struct{ args, field string }{
		{"3 @3", "/refs/1"},
		{"6,3,@6", "/refs/2"},
		{"/work/a 4", "/refs/1"},
		{"4,/work/a", "/refs/0"},
		{"/work 3", "/refs/1"},
		{"3 /", "/refs/1"},
	} {
		if r := fails(t, home, "invalid-input", "", append([]string{"archive"}, strings.Fields(c.args)...)...); problemFields(r) != c.field {
			t.Errorf("%s: %s", c.args, r.Line())
		}
	}
	unchanged("an invalid archive")
	fails(t, home, "not-found", "", "archive", "3", "99")
	unchanged("an archive naming nothing")

	ok(t, home, "complete", "4")
	r = ok(t, home, "archive", "6,/work/a", "3")
	golden(t, r, "archive-several")
	want := fmt.Sprintf("map[archived:[map[archive:%s id:6 json:%s] map[archive:%s id:2 json:%s] map[archive:%s id:3 json:%s]] removed:[2 3 4 6]]",
		archivePath(home, "@6.md"), archivePath(home, "@6.json"),
		archivePath(home, "work/@2_a"), archivePath(home, "work/@2_a.json"),
		archivePath(home, "work/@3.md"), archivePath(home, "work/@3.json"))
	if fmt.Sprint(r.Result()) != want {
		t.Errorf("several:\n got %v\nwant %s", r.Result(), want)
	}
	checkTree(t, home, "@work/", "@work/@notes.md=", "@5.md=")
	checkArchive(t, home, "@6.md=", "@6.json=task", "work/", "work/@2_a/", "work/@2_a/@notes.md=", "work/@2_a/@4.md=",
		"work/@2_a.json=folder", "work/@3.md=three", "work/@3.json=task")
	for rel, edges := range map[string]string{
		"@6.json":        "[map[blocker:3 task:6]]",
		"work/@3.json":   "[map[blocker:3 task:5] map[blocker:3 task:6]]",
		"work/@2_a.json": "[]",
	} {
		if got := fmt.Sprint(archiveFileOf(t, home, rel)["edges"]); got != edges {
			t.Errorf("%s edges %s, want %s", rel, got, edges)
		}
	}
	for id, at := range map[string]string{"2": "work/@2_a", "3": "work/@3.md", "4": "work/@2_a/@4.md", "6": "@6.md"} {
		if got := archivedTo(t, home, "show", id); got != archivePath(home, at) {
			t.Errorf("index of %s: %s", id, got)
		}
	}
	if fmt.Sprint(field(t, home, "5", "blocked_by")) != "[]" {
		t.Errorf("an edge to what left stayed")
	}
	// Through -i.
	ok(t, home, "complete", "5")
	if r := input(t, home, "archive", `{"refs":[5,"/work"]}`); !r.OK() || len(r.Result()["archived"].([]any)) != 2 ||
		fmt.Sprint(r.Result()["removed"]) != "[1 5]" {
		t.Errorf("input: %s", r.Line())
	}
}
