package store

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
)

// migrateFixture is a home where ftask kept its config in old/ and koan
// keeps it in cfg/, with the tree at tasks/.
func newMigrateFixture(t *testing.T) *initFixture {
	f := newInitFixture(t)
	f.env.LegacyConfigDir = f.path("old")
	return f
}

func (f *initFixture) migrate() []errs.Warning {
	f.t.Helper()
	var w errs.Collector
	if e := Migrate(f.env, &w); e != nil {
		f.t.Fatalf("Migrate: %v", e)
	}
	return w.Warnings()
}

func (f *initFixture) exists(rel string) bool {
	_, err := os.Lstat(f.path(rel))
	return err == nil
}

// migrated is the paths of each migrated warning, relative to the home.
func (f *initFixture) migrated(ws []errs.Warning) [][]string {
	f.t.Helper()
	var out [][]string
	for _, w := range ws {
		if w.Kind != errs.WarnMigrated {
			f.t.Fatalf("warning of kind %s", w.Kind)
		}
		var rel []string
		for _, p := range w.Paths {
			r, err := filepath.Rel(f.home, p)
			must(f.t, err)
			rel = append(rel, r)
		}
		out = append(out, rel)
	}
	return out
}

func wantMigrated(t *testing.T, got [][]string, want ...[]string) {
	t.Helper()
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("migrated %q, want %q", got, want)
	}
}

func TestMigrateFtaskSetup(t *testing.T) {
	f := newMigrateFixture(t)
	f.write("old/config.toml", `root = "~/tasks"`+"\n")
	f.write("tasks/ftask.json", `{"schema":1,"last_id":0}`)
	ws := f.migrate()
	wantMigrated(t, f.migrated(ws),
		[]string{"old/config.toml", "cfg/config.toml"},
		[]string{"tasks/ftask.json", "tasks/koan.json"})
	if got := f.read("cfg/config.toml"); got != `root = "~/tasks"`+"\n" {
		t.Errorf("config %q", got)
	}
	if got := f.read("tasks/koan.json"); got != `{"schema":1,"last_id":0}` {
		t.Errorf("koan.json %q", got)
	}
	for _, rel := range []string{"old", "tasks/ftask.json"} {
		if f.exists(rel) {
			t.Errorf("%s still there", rel)
		}
	}
	if ws := f.migrate(); len(ws) != 0 {
		t.Errorf("second Migrate warned %v", ws)
	}
}

func TestMigrateNothingToDo(t *testing.T) {
	f := newMigrateFixture(t)
	if ws := f.migrate(); len(ws) != 0 {
		t.Errorf("warned %v", ws)
	}
	if f.exists("cfg") {
		t.Error("created koan's config directory")
	}
}

func TestMigrateKeepsKoanFiles(t *testing.T) {
	f := newMigrateFixture(t)
	f.write("old/config.toml", `root = "~/old-tasks"`+"\n")
	f.write("cfg/config.toml", `root = "~/tasks"`+"\n")
	f.write("tasks/ftask.json", "ftask's")
	f.write("tasks/koan.json", "koan's")
	if ws := f.migrate(); len(ws) != 0 {
		t.Errorf("warned %v", ws)
	}
	for rel, want := range map[string]string{
		"old/config.toml":  `root = "~/old-tasks"` + "\n",
		"cfg/config.toml":  `root = "~/tasks"` + "\n",
		"tasks/ftask.json": "ftask's",
		"tasks/koan.json":  "koan's",
	} {
		if got := f.read(rel); got != want {
			t.Errorf("%s: %q, want %q", rel, got, want)
		}
	}
}

// A koan config naming an ftask tree: only the tree is migrated.
func TestMigrateTreeOnly(t *testing.T) {
	f := newMigrateFixture(t)
	f.write("cfg/config.toml", `root = "~/tasks"`+"\n")
	f.write("tasks/ftask.json", "{}")
	wantMigrated(t, f.migrated(f.migrate()), []string{"tasks/ftask.json", "tasks/koan.json"})
}

// ftask's config directory keeps anything else in it, and stays.
func TestMigrateLeavesNonEmptyLegacyDir(t *testing.T) {
	f := newMigrateFixture(t)
	f.write("old/config.toml", `root = "~/tasks"`+"\n")
	f.write("old/notes.txt", "mine")
	f.mkdir("tasks")
	wantMigrated(t, f.migrated(f.migrate()), []string{"old/config.toml", "cfg/config.toml"})
	if f.read("old/notes.txt") != "mine" {
		t.Error("notes.txt gone")
	}
}

// A config symlinked from a dotfiles checkout moves as the symlink.
func TestMigrateConfigSymlink(t *testing.T) {
	f := newMigrateFixture(t)
	f.write("dotfiles/config.toml", `root = "~/tasks"`+"\n")
	f.mkdir("old")
	must(t, os.Symlink(f.path("dotfiles/config.toml"), f.path("old/config.toml")))
	f.mkdir("tasks")
	f.migrate()
	if fi, err := os.Lstat(f.path("cfg/config.toml")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("cfg/config.toml: %v, %v; want a symlink", fi, err)
	}
}

// A corrupt config, or a root that is not there, is left for the operation
// to report.
func TestMigrateLeavesUnusableSetup(t *testing.T) {
	f := newMigrateFixture(t)
	f.write("cfg/config.toml", "nonsense\n")
	if ws := f.migrate(); len(ws) != 0 {
		t.Errorf("corrupt config: warned %v", ws)
	}
	f.write("cfg/config.toml", `root = "~/nowhere"`+"\n")
	if ws := f.migrate(); len(ws) != 0 {
		t.Errorf("missing root: warned %v", ws)
	}
}

func TestMigrateFailures(t *testing.T) {
	f := newMigrateFixture(t)
	f.write("old/config.toml", `root = "~/tasks"`+"\n")
	f.write("tasks/ftask.json", "{}")
	for _, tc := range []struct {
		op   string
		want string
	}{
		{fsys.OpRename, "old/config.toml"},
		{fsys.OpRenameNR, "tasks/ftask.json"},
	} {
		env := f.env
		env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
			if op.Name == tc.op {
				return syscall.EACCES
			}
			return nil
		}}
		var w errs.Collector
		e := Migrate(env, &w)
		if e == nil || e.Kind != errs.KindIO {
			t.Fatalf("%s failing: %v, want io", tc.op, e)
		}
		if !f.exists(tc.want) {
			t.Errorf("%s failing: %s gone", tc.op, tc.want)
		}
	}
}
