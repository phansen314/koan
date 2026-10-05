package store

import (
	"cmp"
	"os"
	"reflect"
	"slices"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

func TestLocationPaths(t *testing.T) {
	for _, tc := range []struct {
		l         Location
		rel, note string
	}{
		{Location{"/", 42}, "42.json", "42.md"},
		{Location{"/proj/travel", 7}, "proj/travel/7.json", "proj/travel/7.md"},
	} {
		if got := tc.l.Rel(); got != tc.rel {
			t.Errorf("Rel %q, want %q", got, tc.rel)
		}
		if got := tc.l.NotesRel(); got != tc.note {
			t.Errorf("NotesRel %q, want %q", got, tc.note)
		}
	}
}

func TestWalkFolder(t *testing.T) {
	f := newFixture(t)
	f.mkdir("tasks/proj/travel")
	f.write("tasks/file", "")
	must(t, os.Symlink("proj", f.root+"/link"))
	must(t, os.Symlink("travel", f.root+"/proj/tlink"))
	readTx(t, f.env, nil, func(tx *Tx) {
		for folder, want := range map[model.FolderPath]int{
			"/": 0, "/proj": 1, "/proj/travel": 2, "/missing": 0, "/proj/missing/x": 1, "/proj/travel/x/y": 2,
		} {
			n, e := tx.WalkFolder(folder)
			wantNoErr(t, e)
			if n != want {
				t.Errorf("WalkFolder(%s) = %d, want %d", folder, n, want)
			}
		}
		for folder, entry := range map[model.FolderPath]string{
			"/file": "file", "/file/x": "file", "/link": "link", "/link/travel": "link", "/proj/tlink/x": "proj/tlink",
		} {
			_, e := tx.WalkFolder(folder)
			wantErr(t, e, errs.KindCorrupt, errs.CorruptDetails{Path: tx.Path(entry), Reason: errs.CorruptUnexpectedFile})
		}
	})
	env := f.withFault(fsys.ErrnoAt(fsys.OpLstat, "proj", 1, syscall.EACCES))
	readTx(t, env, nil, func(tx *Tx) {
		_, e := tx.WalkFolder("/proj/travel")
		wantErr(t, e, errs.KindIO, errs.IODetails{Path: tx.Path("proj"), Code: "EACCES"})
	})
}

// The index lists folders and tasks in tree order, skipping hidden entries,
// names that match neither rule, names whose type does not match, and
// symlinks.
func TestIndex(t *testing.T) {
	f := newFixture(t)
	f.task("proj-b", 3, false)
	f.task("proj/travel", 5, false)
	f.task("proj", 10, false)
	f.task("proj", 9, false)
	f.task("", 1, false)
	f.task("infra", 2, false)
	f.task("infra", 9, false) // a duplicate
	f.mkdir("tasks/empty")
	for _, skip := range []string{".hidden.json", "11.json~", "11.md", "011.json", "0.json", "1234567890123456.json", "x.json", "UPPER"} {
		f.write("tasks/proj/"+skip, "")
	}
	f.mkdir("tasks/.git/objects")
	f.write("tasks/.git/12.json", "")
	f.mkdir("tasks/Misc") // not "Proj": on a case-insensitive disk that is proj
	f.write("tasks/Misc/13.json", "")
	f.mkdir("tasks/-x")
	f.mkdir("tasks/14.json") // a task filename that is a directory
	f.write("tasks/notafolder", "")
	must(t, os.Symlink("proj", f.root+"/linked"))
	must(t, os.Symlink("proj/9.json", f.root+"/15.json"))
	must(t, syscall.Mkfifo(f.root+"/16.json", 0o644))

	readTx(t, f.env, nil, func(tx *Tx) {
		x := tx.Index()
		wantFolders := []model.FolderPath{"/", "/empty", "/infra", "/proj", "/proj/travel", "/proj-b"}
		if !reflect.DeepEqual(x.Folders, wantFolders) {
			t.Errorf("folders %v, want %v", x.Folders, wantFolders)
		}
		wantTasks := []Location{{"/", 1}, {"/infra", 2}, {"/infra", 9}, {"/proj", 9}, {"/proj", 10}, {"/proj/travel", 5}, {"/proj-b", 3}}
		if !reflect.DeepEqual(x.Tasks, wantTasks) {
			t.Errorf("tasks %v, want %v", x.Tasks, wantTasks)
		}
		if got := x.Locations(9); !reflect.DeepEqual(got, []Location{{"/infra", 9}, {"/proj", 9}}) {
			t.Errorf("Locations(9) = %v", got)
		}
		if got := x.Locations(99); got != nil {
			t.Errorf("Locations(99) = %v", got)
		}
		if !x.Complete() || x.Unreadable != nil {
			t.Errorf("unreadable %v", x.Unreadable)
		}
		if tx.Index() != x {
			t.Error("the index is walked again")
		}
		if !slices.IsSortedFunc(x.Tasks, CompareLocations) || !slices.IsSortedFunc(x.Folders, CompareFolders) {
			t.Error("tree order disagrees with CompareLocations or CompareFolders")
		}
	})
}

func TestIndexWalksOnce(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 1, false)
	var ops []fsys.Op
	readTx(t, f.withFault(fsys.Record(&ops)), nil, func(tx *Tx) {
		tx.Index()
		tx.Index()
	})
	n := 0
	for _, op := range ops {
		if op.Name == fsys.OpReadDir {
			n++
		}
		if op.Name == fsys.OpReadFile && op.Root != "" && op.Path != MetaName {
			t.Errorf("the walk read %s", op.Path)
		}
	}
	if n != 2 {
		t.Errorf("listed %d folders, want 2", n)
	}
}

func TestIndexUnreadableFolder(t *testing.T) {
	f := newFixture(t)
	f.task("a", 1, false)
	f.task("a/sub", 2, false)
	f.task("b", 3, false)
	f.task("c", 4, false)
	env := f.withFault(fsys.Hooks(
		fsys.ErrnoAt(fsys.OpReadDir, "a", 1, syscall.EACCES),
		fsys.ErrnoAt(fsys.OpReadDir, "c", 1, syscall.EIO),
	))
	var w errs.Collector
	readTx(t, env, &w, func(tx *Tx) {
		x := tx.Index()
		if x.Complete() || len(x.Unreadable) != 2 || x.Unreadable[0].Folder != "/a" || x.Unreadable[1].Folder != "/c" {
			t.Fatalf("unreadable %v", x.Unreadable)
		}
		if want := []Location{{"/b", 3}}; !reflect.DeepEqual(x.Tasks, want) {
			t.Errorf("tasks %v, want %v", x.Tasks, want)
		}
		// Unreadable folders exist, so they are listed; /a/sub is unknown.
		if want := []model.FolderPath{"/", "/a", "/b", "/c"}; !reflect.DeepEqual(x.Folders, want) {
			t.Errorf("folders %v, want %v", x.Folders, want)
		}
		wantErr(t, tx.RequireWholeTree(x), errs.KindIO, errs.IODetails{Path: tx.Path("a"), Code: "EACCES"})
		wantNoErr(t, tx.WarnUnreadable(x))
	})
	ws := w.Warnings()
	if len(ws) != 2 || ws[0].Paths[0] != f.root+"/a" || ws[0].Code != "EACCES" || ws[1].Paths[0] != f.root+"/c" || ws[1].Code != "EIO" {
		t.Errorf("warnings %+v", ws)
	}
}

func TestIndexUnreadableRoot(t *testing.T) {
	f := newFixture(t)
	env := f.withFault(fsys.ErrnoAt(fsys.OpReadDir, ".", 1, syscall.ENOENT))
	readTx(t, env, nil, func(tx *Tx) {
		x := tx.Index()
		wantErr(t, tx.RequireWholeTree(x), errs.KindIO, errs.IODetails{Path: f.root, Code: "ENOENT"})
	})
}

// A folder gone by the time it is listed was moved or removed mid-read: it
// is skipped silently.
func TestIndexVanishedFolder(t *testing.T) {
	f := newFixture(t)
	f.task("a", 1, false)
	f.task("b", 2, false)
	env := f.withFault(func(op fsys.Op) error {
		if op.Name == fsys.OpReadDir && op.Path == "a" {
			must(t, os.RemoveAll(f.root+"/a"))
		}
		return nil
	})
	readTx(t, env, nil, func(tx *Tx) {
		x := tx.Index()
		if !x.Complete() {
			t.Errorf("unreadable %v", x.Unreadable)
		}
		if want := []Location{{"/b", 2}}; !reflect.DeepEqual(x.Tasks, want) {
			t.Errorf("tasks %v, want %v", x.Tasks, want)
		}
		if want := []model.FolderPath{"/", "/b"}; !reflect.DeepEqual(x.Folders, want) {
			t.Errorf("folders %v, want %v", x.Folders, want)
		}
	})
}

func TestInScope(t *testing.T) {
	x := &Index{
		Folders: []model.FolderPath{"/", "/proj", "/proj/travel", "/proj/travel/x", "/proj-b"},
		Tasks:   []Location{{"/", 1}, {"/proj", 2}, {"/proj/travel", 3}, {"/proj/travel/x", 5}, {"/proj-b", 4}},
	}
	for _, tc := range []struct {
		f         model.FolderPath
		recursive bool
		folders   []model.FolderPath
		ids       []model.ID
	}{
		// Without recursive: tasks in f only, folders f and its immediate
		// subfolders.
		{"/", false, []model.FolderPath{"/", "/proj", "/proj-b"}, []model.ID{1}},
		{"/", true, x.Folders, []model.ID{1, 2, 3, 5, 4}},
		{"/proj", false, []model.FolderPath{"/proj", "/proj/travel"}, []model.ID{2}},
		{"/proj", true, []model.FolderPath{"/proj", "/proj/travel", "/proj/travel/x"}, []model.ID{2, 3, 5}},
		{"/proj/travel", false, []model.FolderPath{"/proj/travel", "/proj/travel/x"}, []model.ID{3}},
		{"/proj/travel/x", true, []model.FolderPath{"/proj/travel/x"}, []model.ID{5}},
		{"/proj-b", true, []model.FolderPath{"/proj-b"}, []model.ID{4}},
	} {
		folders, tasks := x.InScope(tc.f, tc.recursive)
		var ids []model.ID
		for _, l := range tasks {
			ids = append(ids, l.ID)
		}
		if !reflect.DeepEqual(folders, tc.folders) || !reflect.DeepEqual(ids, tc.ids) {
			t.Errorf("InScope(%s, %v) = %v, %v; want %v, %v", tc.f, tc.recursive, folders, ids, tc.folders, tc.ids)
		}
	}
}

// A copy gone by the time duplicates are counted is dropped: a task moved
// mid-read is counted once, at the location seen last. One that can't be
// looked at is kept, for its load to report.
func TestCopies(t *testing.T) {
	f := newFixture(t)
	f.task("a", 5, false)
	f.task("b", 5, false)
	f.task("c", 5, false)
	f.task("a", 6, false)
	readTx(t, f.env, nil, func(tx *Tx) {
		tx.Index()
		must(t, os.Remove(f.root+"/a/5.json"))
		must(t, os.Remove(f.root+"/a/6.json"))
		got := tx.Copies(5)
		if want := []Location{{"/b", 5}, {"/c", 5}}; !reflect.DeepEqual(got, want) {
			t.Errorf("Copies(5) = %v, want %v", got, want)
		}
		// A single copy is not checked again; loading it finds it gone.
		got = tx.Copies(6)
		if want := []Location{{"/a", 6}}; !reflect.DeepEqual(got, want) {
			t.Errorf("Copies(6) = %v, want %v", got, want)
		}
		if got := tx.Copies(7); len(got) != 0 {
			t.Errorf("Copies(7) = %v", got)
		}
	})
	env := f.withFault(fsys.ErrnoAt(fsys.OpLstat, "b/5.json", 1, syscall.EIO))
	readTx(t, env, nil, func(tx *Tx) {
		if got, want := tx.Copies(5), []Location{{"/b", 5}, {"/c", 5}}; !reflect.DeepEqual(got, want) {
			t.Errorf("Copies(5) = %v, want %v", got, want)
		}
	})
}

func TestCompareFolders(t *testing.T) {
	order := []model.FolderPath{"/", "/infra", "/proj", "/proj/travel", "/proj/travel/x", "/proj-b", "/proj0"}
	for i := range order {
		for j := range order {
			if got, want := CompareFolders(order[i], order[j]), cmp.Compare(i, j); got != want {
				t.Errorf("CompareFolders(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	if CompareLocations(Location{"/proj", 10}, Location{"/proj", 9}) <= 0 {
		t.Error("tasks compare by ID, not by name")
	}
}
