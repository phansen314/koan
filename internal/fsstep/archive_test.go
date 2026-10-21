package fsstep

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/store"
)

// archiveDir is the spelling ArchiveDir finds, as a change sees it.
func archiveDir(t *testing.T, s *store.Store, above string) string {
	t.Helper()
	var got string
	if e := s.Read(func(tx *store.Tx) error {
		var err error
		got, err = ArchiveDir(tx, s.Root, above)
		return err
	}); e != nil {
		t.Fatal(e.Message)
	}
	return got
}

// archiveStep archives notes to dir, as a lone task's archive does.
func archiveStep(from, dir, base string) Step {
	return Step{Op: OpArchive, From: from, To: dir + "/" + base + ".md", File: dir + "/" + base + ".json", Rows: json.RawMessage(`{}`)}
}

func TestArchiveAbove(t *testing.T) {
	for folder, want := range map[string]string{"/": "archived", "/work": "archived/work", "/work/HOME-777": "archived/work/HOME-777"} {
		if got := ArchiveAbove(folder); got != want {
			t.Errorf("%s: %s, want %s", folder, got, want)
		}
	}
}

// With nothing on disk and nothing pending, the names are as given; a
// directory on disk differing only in case is used as it is spelled
// there, at every level, and the levels below it as given.
func TestArchiveDirOnDisk(t *testing.T) {
	_, s := newRoot(t)
	if got := archiveDir(t, s, "archived/work/api"); got != "archived/work/api" {
		t.Errorf("nothing there: %s", got)
	}
	if got := archiveDir(t, s, "archived"); got != "archived" {
		t.Errorf("the root: %s", got)
	}
	files(t, s, "archived/Work/API/", "archived/Work/@1.md=")
	if got := archiveDir(t, s, "archived/work/api/v2"); got != "archived/Work/API/v2" {
		t.Errorf("on disk: %s", got)
	}
	if got := archiveDir(t, s, "archived/Work/API"); got != "archived/Work/API" {
		t.Errorf("exact: %s", got)
	}
}

// Two archive changes in a row, the first's step still pending: the
// second takes the first's spelling, and both land in one directory.
func TestArchiveDirPending(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@1.md=one", "tree/@2.md=two")
	faults(t, func(p string) error {
		if strings.HasPrefix(p, "step 1: ") {
			return errors.New("refused")
		}
		return nil
	})
	if w := write(t, s, archiveStep("tree/@1.md", "archived/Work", "@1")); !hasKind(w, "pending-step") {
		t.Fatalf("warnings %s", describe(w))
	}
	dir := archiveDir(t, s, "archived/work")
	if dir != "archived/Work" {
		t.Errorf("pending: %s", dir)
	}
	// Below a pending step's directory, its own names count too.
	if got := archiveDir(t, s, "archived/WORK/api"); got != "archived/Work/api" {
		t.Errorf("below: %s", got)
	}
	store.Fault = nil
	write(t, s, archiveStep("tree/@2.md", dir, "@2"))
	checkTree(t, s, sorted("archived/Work/", "archived/Work/@1.md=one", "archived/Work/@1.json={}",
		"archived/Work/@2.md=two", "archived/Work/@2.json={}"))
	noPending(t, s)
}

// The earliest pending step's spelling wins over a later one's, and a
// spelling on disk over both.
func TestArchiveDirEarliest(t *testing.T) {
	_, s := newRoot(t)
	faults(t, func(p string) error {
		if strings.Contains(p, ": rename tree/") {
			return errors.New("refused")
		}
		return nil
	})
	files(t, s, "tree/@1.md=one", "tree/@2.md=two")
	write(t, s, archiveStep("tree/@1.md", "archived/Proj/x", "@1"))
	write(t, s, archiveStep("tree/@2.md", "archived/proj/X", "@2"))
	// Each step made its directories before its rename was refused; take
	// them away, so only the rows say what is coming.
	if err := removeAll(s, "archived/Proj"); err != nil {
		t.Fatal(err)
	}
	if got := archiveDir(t, s, "archived/PROJ/x"); got != "archived/Proj/x" {
		t.Errorf("earliest: %s", got)
	}
	files(t, s, "archived/pROJ/")
	if got := archiveDir(t, s, "archived/proj/x"); got != "archived/pROJ/x" {
		t.Errorf("on disk first: %s", got)
	}
}

// A stuck archive step counts as pending: its directories are still to
// come once repair clears it.
func TestArchiveDirStuck(t *testing.T) {
	_, s := newRoot(t)
	w := write(t, s, Step{Op: OpArchive, From: "tree/@f", To: "archived/Lab/@4_f", File: "archived/Lab/@4_f.json", Dir: true, Rows: json.RawMessage(`{}`)})
	if !hasKind(w, "stuck-step") {
		t.Fatalf("warnings %s", describe(w))
	}
	if err := removeAll(s, "archived/Lab"); err != nil {
		t.Fatal(err)
	}
	if got := archiveDir(t, s, "archived/lab/api"); got != "archived/Lab/api" {
		t.Errorf("stuck: %s", got)
	}
}

// There finds each name on the path byte for byte, so a directory above
// spelled in another case holds nothing, on every filesystem.
func TestThere(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@Work/@2.md=")
	for rel, want := range map[string]bool{"tree/@Work/@2.md": true, "tree/@work/@2.md": false, "tree/@Work/@3.md": false, "tree/@x/@1.md": false} {
		if got, err := There(s.Root, rel); err != nil || got != want {
			t.Errorf("%s: %v %v", rel, got, err)
		}
	}
}

func hasKind(ws []cli.Warning, kind string) bool {
	_, ok := warning(ws, kind)
	return ok
}

// removeAll removes an entry under the root's directory, and all it holds.
func removeAll(s *store.Store, rel string) error {
	return os.RemoveAll(filepath.Join(s.Root.Dir, filepath.FromSlash(rel)))
}
