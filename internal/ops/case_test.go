package ops

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/phansen314/koan/internal/errs"
)

// ignoresCase reports whether the test directories' filesystem ignores
// case, as macOS's usually does.
func ignoresCase(t *testing.T) bool {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "probe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := os.Lstat(filepath.Join(d, "PROBE"))
	return err == nil
}

// A path names a folder only in its exact case, on every platform: on a
// filesystem that ignores case /Proj would otherwise reach /proj, and
// elsewhere a second folder would be made beside it.
func TestCaseMismatch(t *testing.T) {
	t.Logf("filesystem ignores case: %v", ignoresCase(t))
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/proj","notes":"mine"}`) // /proj is 1, the task 2
	for _, c := range []struct{ name, input string }{
		{"folder show", `{"folder":"/Proj"}`},
		{"folder show", `{"folder":"/Proj/sub"}`},
		{"folder create", `{"folder":"/Proj"}`},
		{"folder create", `{"folder":"/PROJ/sub"}`},
		{"folder update", `{"folder":"/Proj","title":"t"}`},
		{"folder list", `{"folder":"/Proj"}`},
		{"folder delete", `{"folder":"/Proj","contents":true}`},
		{"task create", `{"title":"b","folder":"/Proj"}`},
		{"task list", `{"folder":"/Proj"}`},
		{"task move", `{"id":2,"to":"/Proj"}`},
		{"task move", `{"id":2,"to":"/Proj/sub"}`},
		{"folder move", `{"folder":"/Proj","to":"/x"}`},
		{"batch", `{"folders":[{"folder":"/Proj"}]}`},
		{"batch", `{"tasks":[{"title":"b","folder":"/Proj"}]}`},
		{"where", `{"ref":"/Proj"}`},
	} {
		env := w.run(c.name, c.input)
		if env.OK || !env.Error.HasRule(errs.RuleCaseMismatch) {
			t.Errorf("%s %s: %+v, want case-mismatch", c.name, c.input, env.Error)
			continue
		}
		if !slices.Equal(env.Error.Folders, []string{"/proj"}) {
			t.Errorf("%s %s: folders %v, want the path on disk", c.name, c.input, env.Error.Folders)
		}
	}
	// Nothing moved, nothing made, the notes kept.
	if w.file("proj/2.md") != "mine" {
		t.Errorf("notes %q", w.file("proj/2.md"))
	}
	if ents, _ := os.ReadDir(w.disk()); len(ents) != 1 { // @proj
		t.Errorf("the tree holds %d entries", len(ents))
	}
	if f := w.findings(); len(f) != 0 {
		t.Errorf("findings %v", f)
	}
}

// A case-only rename is the way to fix a folder's case, and keeps its ID.
func TestFolderMoveChangesCase(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/work/api"}`) // /work 1, /work/api 2, the task 3
	got := w.ok("folder move", `{"folder":"/work","to":"/Work"}`)
	if got["id"] != float64(1) || got["path"] != "/Work" || got["changed"] != true {
		t.Errorf("folder move: %v", got)
	}
	ents, _ := os.ReadDir(w.disk())
	if !slices.ContainsFunc(ents, func(e os.DirEntry) bool { return e.Name() == "@Work" }) ||
		slices.ContainsFunc(ents, func(e os.DirEntry) bool { return e.Name() == "@work" }) {
		t.Errorf("the tree holds %v, want @Work and no @work", ents)
	}
	if id, e := w.holderOf("/Work/api"); e != nil || id != 2 {
		t.Errorf("/Work/api: %v %v", id, e)
	}
	if f := task(w.ok("task show", `{"id":3}`))["folder"]; f != "/Work/api" {
		t.Errorf("task 3 in %v", f)
	}
	w.fail("folder show", `{"folder":"/work"}`, errs.Conflict, errs.RuleCaseMismatch)
	// And a deeper one, by ID.
	if got := w.ok("folder move", `{"folder":2,"to":"/Work/API"}`); got["path"] != "/Work/API" {
		t.Errorf("folder move by ID: %v", got)
	}
}

// A tree that already holds names differing only in case, made on Linux by
// hand, keeps working where the filesystem allows it: an exact match wins.
func TestExactCaseWins(t *testing.T) {
	if ignoresCase(t) {
		t.Skip("the filesystem can't hold both names")
	}
	w := ready(t)
	w.ok("folder create", `{"folder":"/proj"}`)
	os.Mkdir(w.root("Proj"), 0o755)
	w.ok("folder show", `{"folder":"/proj"}`)
	w.ok("folder show", `{"folder":"/Proj"}`)
	w.ok("task create", `{"title":"a","folder":"/Proj"}`)
}

// One input never names two paths that differ only in case: refused before
// the tree is read, on every platform.
func TestPathsDifferingOnlyInCase(t *testing.T) {
	w := ready(t)
	for _, c := range []struct{ name, input, field string }{
		{"batch", `{"folders":[{"folder":"/Work"},{"folder":"/work"}]}`, "/folders/1/folder"},
		{"batch", `{"folders":[{"folder":"/a/Work"},{"folder":"/a/work/b"}]}`, "/folders/1/folder"},
		{"batch", `{"tasks":[{"title":"x","folder":"/a/X"},{"title":"y","folder":"/a/x"}]}`, "/tasks/1/folder"},
		{"batch", `{"folders":[{"folder":"/Work"}],"tasks":[{"title":"x","folder":"/work/b"}]}`, "/tasks/0/folder"},
		{"batch", `{"folder":"/Work","tasks":[{"title":"x","folder":"/work"}]}`, "/tasks/0/folder"},
		{"folder move", `{"folder":"/proj/a","to":"/Proj/b"}`, "/to"},
	} {
		e := w.fail(c.name, c.input, errs.InvalidInput, "")
		if len(e.Problems) != 1 || e.Problems[0].Field != c.field {
			t.Errorf("%s %s: problems %v, want one at %s", c.name, c.input, e.Problems, c.field)
		}
	}
	// Paths that only share a prefix, or differ in more than case, are fine.
	w.ok("batch", `{"folders":[{"folder":"/a/b"},{"folder":"/a/c"},{"folder":"/A-1"}],"tasks":[{"title":"x","folder":"/a/b/d"}]}`)
}
