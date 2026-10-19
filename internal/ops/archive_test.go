package ops

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
)

// indexLines reads the archive's index, one decoded line each.
func (w *world) indexLines() []*model.ArchiveEntry {
	w.t.Helper()
	var out []*model.ArchiveEntry
	for _, line := range strings.Split(strings.TrimSuffix(w.fileAt(w.top("archived", "@index.jsonl")), "\n"), "\n") {
		a, problems, err := model.DecodeArchiveEntry([]byte(line))
		if err != nil || len(problems) > 0 {
			w.t.Fatalf("index line %q: %v %v", line, err, problems)
		}
		out = append(out, a)
	}
	return out
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// A done folder leaves the tree whole, material included, for its place in
// the archive; what named it lets go, and the index says where it went.
func TestFolderArchive(t *testing.T) {
	w := ready(t)
	// /work 1, /work/HOME-1 2, /work/HOME-1/sub 3, /x 4; tasks 5 and 6
	// under /work/HOME-1, and 7 outside, waiting on 5 and on /work/HOME-1.
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a","folder":"/work/HOME-1"},{"title":"b","folder":"/work/HOME-1/sub","notes":"n"},
		{"title":"c","folder":"/x","blocked_by":["a","/work/HOME-1"]}]}`)
	w.ok("folder update", `{"folder":"/work/HOME-1","title":"Ship it"}`)
	os.WriteFile(w.disk("@work/@HOME-1/plan.md"), []byte("material"), 0o644)
	w.fail("folder archive", `{"folder":"/work/HOME-1"}`, errs.Conflict, errs.RuleNotDone)
	w.ok("task done", `{"id":5}`)
	e := w.fail("folder archive", `{"folder":"/work/HOME-1"}`, errs.Conflict, errs.RuleNotDone)
	if !slices.Equal(e.IDs, []int64{6}) || !slices.Equal(e.Folders, []string{"/work/HOME-1/sub"}) {
		t.Errorf("not-done: %+v", e)
	}
	w.ok("task done", `{"id":6}`)
	got := w.ok("folder archive", `{"folder":"/work/HOME-1"}`)
	dest := w.top("archived", "work", "@2_HOME-1")
	if got["id"] != float64(2) || got["path"] != "/work/HOME-1" || got["archived"] != dest || got["changed"] != true {
		t.Errorf("result %v", got)
	}
	wantIDs(t, "ids", got["ids"], 2, 3, 5, 6)
	wantIDs(t, "dependents", got["dependents"], 7)
	if exists(w.root("work/HOME-1")) || !exists(dest+"/plan.md") || !exists(dest+"/@sub/@6.md") || !exists(dest+"/@folder.json") {
		t.Error("the folder did not move whole")
	}
	if tk := task(w.ok("task show", `{"id":7}`)); len(tk["blocked_by"].([]any)) != 0 || tk["readiness"] != "ready" {
		t.Errorf("task 7: %v", tk)
	}
	lines := w.indexLines()
	if len(lines) != 1 || lines[0].ID != 2 || lines[0].Kind != "folder" || *lines[0].Title != "Ship it" ||
		lines[0].Path != "/work/HOME-1" || lines[0].Archived != "work/@2_HOME-1" || *lines[0].ArchivedAt != "2026-10-11T12:00:00Z" ||
		!slices.Equal(lines[0].IDs, []int64{2, 3, 5, 6}) {
		t.Errorf("index %+v", lines[0])
	}
	if f := w.findings(); len(f) != 0 {
		t.Errorf("findings %v", f)
	}

	// The same path, made again and archived again, sits beside the first.
	w.ok("folder create", `{"folder":"/work/HOME-1"}`) // 8
	if got := w.ok("folder archive", `{"folder":"/work/HOME-1"}`); got["archived"] != w.top("archived", "work", "@8_HOME-1") {
		t.Errorf("second archive: %v", got)
	}
	if !exists(dest) || len(w.indexLines()) != 2 {
		t.Error("the first archive was disturbed")
	}

	w.fail("folder archive", `{"folder":"/"}`, errs.InvalidInput, "")
	w.fail("folder archive", `{"folder":"/nope"}`, errs.NotFound, "")
}

// A folder under the one archived with no folder file holds back the
// archive: the index could not record its ID.
func TestFolderArchiveNeedsEveryFolderFile(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q"}`)
	os.Remove(w.root("p/q/folder.json"))
	w.fail("folder archive", `{"folder":"/p"}`, errs.NotFound, "")
	os.WriteFile(w.root("p/q/folder.json"), []byte("{"), 0o644)
	w.fail("folder archive", `{"folder":"/p"}`, errs.Corrupt, "")
	if !exists(w.root("p/q")) {
		t.Error("a refused archive moved something")
	}
}

// A done task leaves with its notes, to the directories of its folder's
// path, and its claim ends; one in / goes to the top of the archive.
func TestTaskArchive(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/p","notes":"n"}`) // /p 1, 2
	w.ok("task create", `{"title":"b","blocked_by":[2]}`)          // 3
	w.ok("task create", `{"title":"c"}`)                           // 4
	w.fail("task archive", `{"id":2}`, errs.Conflict, errs.RuleNotDone)
	w.ok("task done", `{"id":2}`)
	got := w.ok("task archive", `{"id":2}`)
	if got["path"] != "/p" || got["archived"] != w.top("archived", "p", "@2.json") {
		t.Errorf("result %v", got)
	}
	wantIDs(t, "dependents", got["dependents"], 3)
	if exists(w.root("p/2.json")) || exists(w.root("p/2.md")) || w.fileAt(w.top("archived", "p", "@2.md")) != "n" {
		t.Error("the task and its notes did not both move")
	}
	if !exists(w.root("p")) {
		t.Error("its folder left the tree")
	}
	w.ok("task done", `{"id":4}`)
	if got := w.ok("task archive", `{"id":4}`); got["path"] != "/" || got["archived"] != w.top("archived", "@4.json") {
		t.Errorf("a task in /: %v", got)
	}
	if lines := w.indexLines(); len(lines) != 2 || lines[0].Path != "/p" || *lines[0].Title != "a" || lines[1].Archived != "@4.json" {
		t.Errorf("index %+v %+v", lines[0], lines[1])
	}
	if f := w.findings(); len(f) != 0 {
		t.Errorf("findings %v", f)
	}
}

// The archive's directories are found ignoring case: archiving never
// refuses for case, and the index records the name on disk.
func TestArchiveDirsIgnoreCase(t *testing.T) {
	w := ready(t)
	os.MkdirAll(w.top("archived", "Work"), 0o755)
	w.ok("task create", `{"title":"a","folder":"/work"}`) // /work 1, 2
	w.ok("task done", `{"id":2}`)
	if got := w.ok("task archive", `{"id":2}`); got["archived"] != w.top("archived", "Work", "@2.json") {
		t.Errorf("result %v", got)
	}
	if l := w.indexLines()[0]; l.Path != "/work" || l.Archived != "Work/@2.json" {
		t.Errorf("index %+v", l)
	}
	if f := w.findings(); len(f) != 0 {
		t.Errorf("findings %v", f)
	}
}

// What was archived is not-found, saying where it went: by ID, by the
// folder's path or a path under it, and by a file in the archive.
func TestArchivedIsNotFound(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/p/q"}`) // /p 1, /p/q 2, 3
	w.ok("task done", `{"id":3}`)
	w.ok("folder archive", `{"folder":"/p"}`)
	dest := w.top("archived", "@1_p")
	for _, c := range []struct{ name, input, path string }{
		{"task show", `{"id":3}`, dest},
		{"folder show", `{"folder":1}`, dest},
		{"folder show", `{"folder":"/p"}`, dest},
		{"folder show", `{"folder":"/p/q"}`, dest + "/@q"},
		{"task block", `{"id":3,"blocked_by":[1]}`, dest},
		{"where", `{"ref":"@2"}`, dest},
	} {
		e := w.fail(c.name, c.input, errs.NotFound, "")
		if !slices.Contains(e.Paths, c.path) || !strings.Contains(e.Message, "archived") {
			t.Errorf("%s %s: %+v, want %s", c.name, c.input, e, c.path)
		}
	}
	// A path under an archived folder with nothing there is plainly missing,
	// and a path matches whole parts only.
	for _, in := range []string{`{"folder":"/p/nope"}`, `{"folder":"/pq"}`} {
		if e := w.fail("folder show", in, errs.NotFound, ""); len(e.Paths) != 0 || strings.Contains(e.Message, "archived") {
			t.Errorf("folder show %s: %+v", in, e)
		}
	}
	if e := w.fail("where", `{"file":"`+dest+`/@q/@3.json"}`, errs.NotFound, ""); !strings.Contains(e.Message, "archive") {
		t.Errorf("where in the archive: %+v", e)
	}
	// A folder made again at the path is the folder there.
	w.ok("folder create", `{"folder":"/p"}`)
	w.ok("folder show", `{"folder":"/p"}`)
	// An index koan can't read changes nothing but the message.
	os.WriteFile(w.top("archived", "@index.jsonl"), []byte("{"), 0o644)
	if e := w.fail("task show", `{"id":3}`, errs.NotFound, ""); len(e.Paths) != 0 {
		t.Errorf("with a broken index: %+v", e)
	}
}

// doctor checks the index against the archive, and repair rewrites it,
// keeping what matches; what an archive cut short leaves, repair finishes
// by default.
func TestDoctorAndRepairTheArchive(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a","folder":"/p"},{"title":"b","folder":"/q","notes":"n"},{"title":"c","blocked_by":["a"]}]}`)
	// /p 1, /q 2; 3 in /p, 4 in /q, 5 waiting on 3.
	w.ok("task done", `{"id":3}`)
	w.ok("task done", `{"id":4}`)
	w.ok("folder archive", `{"folder":"/p"}`)
	w.ok("task archive", `{"id":4}`)
	first := w.indexLines()

	// An archive cut short after the rename: the reference, the notes left
	// behind, and no line in the index.
	os.Rename(w.top("archived", "q", "@4.md"), w.root("q/4.md"))
	index := w.fileAt(w.top("archived", "@index.jsonl"))
	os.WriteFile(w.top("archived", "@index.jsonl"), []byte(index[:strings.Index(index, "\n")+1]), 0o644)
	os.WriteFile(w.root("5.json"), []byte(strings.Replace(w.file("5.json"), `"blocked_by": []`, `"blocked_by": [3]`, 1)), 0o644)
	got := w.findings()
	for _, want := range []string{"dangling-reference @5.json", "orphan-notes @q/@4.md", "archive-index"} {
		if !slices.ContainsFunc(got, func(f string) bool { return strings.HasPrefix(f, want) }) {
			t.Errorf("findings %v, want %s", got, want)
		}
	}
	w.ok("repair", `{}`)
	if f := w.findings(); len(f) != 0 {
		t.Errorf("after repair: %v", f)
	}
	lines := w.indexLines()
	if len(lines) != 2 || !lines[0].Matches(first[0]) || lines[0].ArchivedAt == nil || lines[1].ID != 4 || lines[1].ArchivedAt != nil {
		t.Errorf("rebuilt index %+v %+v", lines[0], lines[1])
	}
	if w.fileAt(w.top("archived", "q", "@4.md")) != "n" {
		t.Error("the notes did not reach their task")
	}

	// IDs in the archive count against the counter and for duplicates.
	w.setCounter(2)
	os.WriteFile(w.root("3.json"), []byte(strings.Replace(w.file("5.json"), `"id": 5`, `"id": 3`, 1)), 0o644)
	got = w.findings()
	for _, want := range []string{"id-above-counter", "duplicate-id"} {
		if !slices.ContainsFunc(got, func(f string) bool { return strings.HasPrefix(f, want) }) {
			t.Errorf("findings %v, want %s", got, want)
		}
	}
	os.Remove(w.root("3.json"))
	w.ok("repair", `{}`)

	// An archived folder file holding another ID is unusable.
	ff := w.top("archived", "@1_p", "@folder.json")
	os.WriteFile(ff, []byte(strings.Replace(w.fileAt(ff), `"id": 1`, `"id": 9`, 1)), 0o644)
	if got := w.findings(); !slices.Contains(got, "unusable-file "+ff) {
		t.Errorf("findings %v", got)
	}
}

// A new @koan.json counts the archive's IDs with the tree's.
func TestInitCountsTheArchive(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/p/q"}`) // /p 1, /p/q 2, 3
	w.ok("task done", `{"id":3}`)
	w.ok("folder archive", `{"folder":"/p"}`)
	os.Remove(w.top("@koan.json"))
	w.ok("init", `{}`)
	if n := w.counter(); n != 3 {
		t.Errorf("last_id %v, want 3", n)
	}
}
