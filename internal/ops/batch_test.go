package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
)

func TestBatchRefsPointBackwards(t *testing.T) {
	w := ready(t)
	w.fail("batch", `{"tasks":[{"title":"a","blocked_by":["b"]},{"ref":"b","title":"b"}]}`, errs.InvalidInput, "")
	w.fail("batch", `{"tasks":[{"ref":"a","title":"a","blocked_by":["a"]}]}`, errs.InvalidInput, "")
	w.fail("batch", `{"tasks":[{"title":"a","blocked_by":["nope"]}]}`, errs.InvalidInput, "")
	w.fail("batch", `{"tasks":[{"ref":"a","title":"a"},{"ref":"a","title":"b"}]}`, errs.InvalidInput, "")
	if id := w.ok("task create", `{"title":"x"}`)["id"]; id != float64(1) {
		t.Errorf("a refused batch used IDs: next is %v", id)
	}
}

// A field no entry has is reported at its own pointer, as the top level's
// are.
func TestBatchUnknownFields(t *testing.T) {
	w := ready(t)
	e := w.fail("batch", `{"folders":[{"folder":"/f","zz":1,"aa":2}],"tasks":[{"title":"a","bogus":1}],"top":1}`, errs.InvalidInput, "")
	want := []errs.Problem{
		{Field: "/folders/0/aa", Reason: "is not a field of a folder"},
		{Field: "/folders/0/zz", Reason: "is not a field of a folder"},
		{Field: "/tasks/0/bogus", Reason: "is not a field of a task"},
		{Field: "/top", Reason: "is not a field of this command"},
	}
	if !slices.Equal(e.Problems, want) {
		t.Errorf("problems %v, want %v", e.Problems, want)
	}
}

// Everything in a batch is checked before anything is written, a folder
// path that runs through a file included.
func TestBatchChecksFoldersFirst(t *testing.T) {
	w := ready(t)
	os.WriteFile(w.root("afile"), nil, 0o644)
	e := w.fail("batch", `{"tasks":[{"title":"a"},{"title":"b","folder":"/afile/x"}]}`, errs.Conflict, errs.RuleNotAFolder)
	if len(e.ChangedPaths) != 0 || len(e.IDs) != 0 {
		t.Errorf("changed %v, ids %v: want nothing written", e.ChangedPaths, e.IDs)
	}
	w.fail("task create", `{"title":"c","folder":"/afile"}`, errs.Conflict, errs.RuleNotAFolder)
	if id := w.ok("task create", `{"title":"d"}`)["id"]; id != float64(1) {
		t.Errorf("the refused creates used IDs: next is %v", id)
	}
}

// batch makes folders with their fields, and tasks in them, in one
// write: folders take IDs first, in tree order, then the tasks.
func TestBatchFolders(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/work"}`) // 1
	got := w.ok("batch", `{"folders":[
		{"folder":"/work/s/research","title":"Research"},
		{"folder":"/work/s","title":"Story","extra":{"jira":"H-1"},"notes":"context"}],
	 "tasks":[
		{"ref":"a","title":"Read","folder":"/work/s/research"},
		{"title":"Write","folder":"/work/s/research","blocked_by":["a"]},
		{"title":"Elsewhere","folder":"/other"}]}`)
	// /other (2) sorts before /work/s (3) and /work/s/research (4).
	if data, _ := json.Marshal(got["folders"]); string(data) != `{"/other":2,"/work/s":3,"/work/s/research":4}` {
		t.Errorf("folders %s", data)
	}
	if data, _ := json.Marshal(got["folders_created"]); string(data) != `["/other","/work/s","/work/s/research"]` {
		t.Errorf("folders_created %s", data)
	}
	wantIDs(t, "ids", got["ids"], 5, 6, 7)
	if r := w.record(w.ok("folder show", `{"folder":"/work/s"}`)); r != `{"dir":"~/@work/@s","extra":{"jira":"H-1"},"id":3,"notes_path":"~/@work/@s/@folder.md","path":"/work/s","title":"Story"}` {
		t.Errorf("folder show: %s", r)
	}
	if n, _ := os.ReadFile(w.root("work/s/folder.md")); string(n) != "context" {
		t.Errorf("notes %q", n)
	}
	if r := w.record(w.ok("folder show", `{"folder":4}`)); !strings.Contains(r, `"title":"Research"`) {
		t.Errorf("research: %s", r)
	}
	// Folders only.
	got = w.ok("batch", `{"folders":[{"folder":"/x"}]}`)
	if data, _ := json.Marshal(got["folders"]); string(data) != `{"/x":8}` || len(got["ids"].([]any)) != 0 {
		t.Errorf("folders only: %v", got)
	}
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Errorf("doctor: %v", got)
	}
}

// A folder entry means a folder to make: every one already there is
// refused, before anything is written; bad entries are invalid-input.
func TestBatchFolderEntriesRefused(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q"}`) // 1, 2
	e := w.fail("batch", `{"folders":[{"folder":"/p"},{"folder":"/n"},{"folder":"/p/q"}],"tasks":[{"title":"a"}]}`, errs.Conflict, errs.RuleDestinationExists)
	if !slices.Equal(e.Folders, []string{"/p", "/p/q"}) || len(e.Paths) != 2 || len(e.ChangedPaths) != 0 {
		t.Errorf("folders %v, paths %v, changed %v", e.Folders, e.Paths, e.ChangedPaths)
	}
	// A task may still go into a folder already there.
	w.ok("batch", `{"folders":[{"folder":"/n"}],"tasks":[{"title":"a","folder":"/p"}]}`)
	for _, in := range []string{
		`{}`, `{"folders":[],"tasks":[]}`,
		`{"folders":[{"folder":"/"}]}`,
		`{"folders":[{"folder":"/m"},{"folder":"/m"}]}`,
		`{"folders":[{"folder":"@1"}]}`,
		`{"folders":[{"folder":"/m","gates":true}]}`,
		`{"folders":[{"title":"x"}]}`,
		`{"folders":[{"folder":"/m","title":"  "}]}`,
		`{"folders":[{"folder":"/m","extra":[]}]}`,
		`{"folders":"/m"}`,
	} {
		w.fail("batch", in, errs.InvalidInput, "")
	}
	var many []string
	for i := range 600 {
		many = append(many, fmt.Sprintf(`{"folder":"/f%d"}`, i))
	}
	tasks := strings.Repeat(`{"title":"t"},`, 400) + `{"title":"t"}`
	w.fail("batch", `{"folders":[`+strings.Join(many, ",")+`],"tasks":[`+tasks+`]}`, errs.InvalidInput, "")
	if id := w.ok("task create", `{"title":"z"}`)["id"]; id != float64(5) {
		t.Errorf("a refused batch used an ID: next is %v", id)
	}
}

// A failure partway through a batch leaves the first tasks created, and the
// error says which.
func TestBatchFailsPartway(t *testing.T) {
	w := ready(t)
	w.env.FS = fsys.FS{Hook: fsys.ErrnoAt(fsys.OpLink, "", 5, 28 /* ENOSPC */)}
	env := w.run("batch", `{"tasks":[{"title":"a"},{"title":"b"},{"title":"c"}]}`)
	if env.OK || env.Error.Kind != errs.IO {
		t.Fatalf("got %+v", env)
	}
	if !slices.Equal(env.Error.IDs, []int64{1, 2}) || len(env.Error.ChangedPaths) == 0 {
		t.Errorf("ids %v, changed %v", env.Error.IDs, env.Error.ChangedPaths)
	}
	w.env.FS = fsys.FS{}
	if id := w.ok("task create", `{"title":"d"}`)["id"]; id != float64(4) {
		t.Errorf("next ID %v: the batch's IDs must not be reused", id)
	}
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Errorf("doctor after a failed batch: %v", got)
	}
}

// A batch cut short leaves the folders made so far, and says what it made:
// a folder cut short has no folder file, which repair gives it.
func TestBatchFoldersFailPartway(t *testing.T) {
	w := ready(t)
	// Each folder's two files are linked into place: the third link is /b's
	// first, so /a is made whole and /b without its files.
	w.env.FS = fsys.FS{Hook: fsys.ErrnoAt(fsys.OpLink, "", 3, 28 /* ENOSPC */)}
	env := w.run("batch", `{"folders":[{"folder":"/a","title":"A"},{"folder":"/b"}],"tasks":[{"title":"t","folder":"/b"}]}`)
	w.env.FS = fsys.FS{}
	if env.OK || env.Error.Kind != errs.IO || len(env.Error.IDs) != 0 || !slices.Equal(env.Error.Folders, []string{"/a", "/b"}) {
		t.Fatalf("got %+v", env)
	}
	if r := w.record(w.ok("folder show", `{"folder":"/a"}`)); !strings.Contains(r, `"title":"A"`) || !strings.Contains(r, `"id":1`) {
		t.Errorf("/a: %s", r)
	}
	if id := w.ok("task create", `{"title":"d"}`)["id"]; id != float64(4) {
		t.Errorf("next ID %v: the batch's IDs must not be reused", id)
	}
	if fs := w.findings(); !slices.Equal(fs, []string{"missing-folder-file @b"}) {
		t.Errorf("doctor after a failed batch: %v", fs)
	}
}

// raceAt makes the folder dir, as an outside change, just before koan
// makes a new folder there.
func raceAt(dir string) fsys.Hook {
	return func(op fsys.Op) error {
		if op.Name == fsys.OpMkdir && strings.HasSuffix(dir, "/"+op.Path) {
			os.Mkdir(dir, 0o755)
		}
		return nil
	}
}

// A folder entry that appears while the batch runs stops it there: the
// folders before it stay made, and the error names them.
func TestBatchFolderEntryRaced(t *testing.T) {
	w := ready(t)
	w.env.FS = fsys.FS{Hook: raceAt(w.root("b"))}
	e := w.fail("batch", `{"folders":[{"folder":"/a"},{"folder":"/b","title":"B"}],"tasks":[{"title":"t","folder":"/b"}]}`, errs.Conflict, errs.RuleDestinationExists)
	if !slices.Equal(e.Folders, []string{"/a"}) || len(e.IDs) != 0 || !slices.Equal(e.Paths, []string{w.root("b")}) || !strings.Contains(e.Message, "/b") {
		t.Errorf("folders %v, ids %v, paths %v, message %q", e.Folders, e.IDs, e.Paths, e.Message)
	}
	// folder create, given fields, is stopped the same way; without them, a
	// folder that appeared is fine.
	w.env.FS = fsys.FS{Hook: raceAt(w.root("c"))}
	w.fail("folder create", `{"folder":"/c","title":"C"}`, errs.Conflict, errs.RuleDestinationExists)
	w.env.FS = fsys.FS{Hook: raceAt(w.root("d"))}
	if got := w.ok("folder create", `{"folder":"/d"}`); len(got["folders_created"].([]any)) != 0 {
		t.Errorf("folder create /d: %v", got)
	}
}

// A failure in writing the tasks names the folders made, the tasks
// created, and every file written.
func TestBatchTasksFailAfterFolders(t *testing.T) {
	w := ready(t)
	// Two links make each folder, and two each task: the seventh is task
	// 4's file, after folders 1 and 2 and task 3 are made whole.
	w.env.FS = fsys.FS{Hook: fsys.ErrnoAt(fsys.OpLink, "", 7, 28 /* ENOSPC */)}
	env := w.run("batch", `{"folders":[{"folder":"/a/b","title":"B"}],"tasks":[{"title":"t","folder":"/a/b"},{"title":"u","folder":"/a/b"},{"title":"v"}]}`)
	w.env.FS = fsys.FS{}
	if env.OK {
		t.Fatal("succeeded")
	}
	e := env.Error
	if !slices.Equal(e.Folders, []string{"/a", "/a/b"}) || !slices.Equal(e.IDs, []int64{3}) {
		t.Errorf("folders %v, ids %v", e.Folders, e.IDs)
	}
	for _, p := range []string{w.root("a"), w.root("a/b"), w.root("a/b/3.json")} {
		if !slices.Contains(e.ChangedPaths, p) {
			t.Errorf("changed_paths %v lacks %s", e.ChangedPaths, p)
		}
	}
	// The batch's folder is checked even when no task uses it.
	w.fail("batch", `{"folder":"@99","folders":[{"folder":"/x"}]}`, errs.NotFound, "")
	os.WriteFile(w.root("file"), nil, 0o644)
	w.fail("batch", `{"folder":"/file/y","folders":[{"folder":"/x"}]}`, errs.Conflict, errs.RuleNotAFolder)
	if got := w.ok("batch", `{"folder":"/unused","folders":[{"folder":"/x"}]}`); len(got["folders_created"].([]any)) != 1 {
		t.Errorf("an unused batch folder was made: %v", got)
	}
}
