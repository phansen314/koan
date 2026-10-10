package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
)

// initFixture is a home with nothing set up: the config directory would be
// home/cfg, and init's root is home/tasks unless a case says otherwise.
type initFixture struct {
	t    *testing.T
	home string
	env  Env
}

func newInitFixture(t *testing.T) *initFixture {
	home := t.TempDir()
	return &initFixture{t: t, home: home, env: Env{FS: fsys.OS{}, Home: home, ConfigDir: filepath.Join(home, "cfg")}}
}

func (f *initFixture) path(rel string) string { return filepath.Join(f.home, rel) }

func (f *initFixture) write(rel, content string) {
	f.t.Helper()
	must(f.t, os.MkdirAll(filepath.Dir(f.path(rel)), 0o755))
	must(f.t, os.WriteFile(f.path(rel), []byte(content), 0o644))
}

func (f *initFixture) mkdir(rel string) { f.t.Helper(); must(f.t, os.MkdirAll(f.path(rel), 0o755)) }

// read is the file at rel, or "<none>" when nothing is there.
func (f *initFixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(f.path(rel))
	if os.IsNotExist(err) {
		return "<none>"
	}
	must(f.t, err)
	return string(b)
}

// summary is Init's outcome in short, with the home as "~": the action,
// last_id, and what was created; or the error's kind and details, then
// what was created before it.
func (f *initFixture) summary(res InitResult, e *errs.Error) string {
	f.t.Helper()
	created := fmt.Sprintf("root_created=%v metadata_created=%v state_created=%v", res.RootCreated, res.MetaCreated, res.StateCreated)
	if e == nil {
		return fmt.Sprintf("%s %d %s", res.Action, res.LastID, created)
	}
	d, err := json.Marshal(e.Details)
	must(f.t, err)
	return strings.ReplaceAll(fmt.Sprintf("%s %s %s", e.Kind, d, created), f.home, "~")
}

const newMeta = "{\n  \"schema\": 2,\n  \"migration\": 1\n}\n"

func TestInit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(f *initFixture)
		root    string // under the home
		replace bool
		want    string
		meta    string // want tasks/koan.json afterwards; "" to skip
		config  string // want cfg/config.toml afterwards, "~" for the home; "" to skip
	}{
		// State of root (operations.md, init, Preconditions).
		{"new root", nil, "tasks", false,
			"created 0 root_created=true metadata_created=true state_created=true", newMeta, `root = "~/tasks"` + "\n"},
		{"parent missing", nil, "a/tasks", false,
			`not-found {"folders":[],"ids":[],"paths":["~/a"]} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"parent is a file", func(f *initFixture) { f.write("a", "") }, "a/tasks", false,
			`not-found {"folders":[],"ids":[],"paths":["~/a"]} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"empty directory", func(f *initFixture) { f.mkdir("tasks") }, "tasks", false,
			"created 0 root_created=false metadata_created=true state_created=true", newMeta, `root = "~/tasks"` + "\n"},
		{"only hidden entries", func(f *initFixture) { f.mkdir("tasks/.git"); f.write("tasks/.koan-tmp-x", "") }, "tasks", false,
			"created 0 root_created=false metadata_created=true state_created=true", newMeta, ""},
		{"not empty", func(f *initFixture) { f.write("tasks/notes.txt", "") }, "tasks", false,
			`conflict {"rule":"root-not-empty","ids":[]} root_created=false metadata_created=false state_created=false`, "<none>", "<none>"},
		{"existing tree", func(f *initFixture) {
			f.write("tasks/koan.json", `{"schema": 2, "migration": 1}`)
			f.write("tasks/3.json", "x")
			f.write("tasks/p/7.json", "not even JSON") // only the filename counts
			f.write("tasks/p/7.md", "")
		}, "tasks", false,
			"attached 7 root_created=false metadata_created=false state_created=true", `{"schema": 2, "migration": 1}`, `root = "~/tasks"` + "\n"},
		{"corrupt koan.json", func(f *initFixture) { f.write("tasks/koan.json", `{"schema": 2}`) }, "tasks", false,
			`corrupt {"path":"~/tasks/koan.json","reason":"invalid","problems":[{"field":"/migration","reason":"required"}]} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"koan.json in an older format", func(f *initFixture) { f.write("tasks/koan.json", `{"schema": 1, "last_id": 5}`) }, "tasks", false,
			"attached 5 root_created=false metadata_created=false state_created=true", `{"schema": 1, "last_id": 5}`, `root = "~/tasks"` + "\n"},
		{"koan.json a step behind", func(f *initFixture) { f.write("tasks/koan.json", `{"schema": 2, "migration": 0}`) }, "tasks", false,
			"attached 0 root_created=false metadata_created=false state_created=true", `{"schema": 2, "migration": 0}`, `root = "~/tasks"` + "\n"},
		{"older koan.json breaking its own format", func(f *initFixture) { f.write("tasks/koan.json", `{"schema": 1, "last_id": 5, "migration": 0}`) }, "tasks", false,
			`corrupt {"path":"~/tasks/koan.json","reason":"invalid","problems":[{"field":"/migration","reason":"unknown field"}]} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"older koan.json missing last_id", func(f *initFixture) { f.write("tasks/koan.json", `{"schema": 1}`) }, "tasks", false,
			`corrupt {"path":"~/tasks/koan.json","reason":"invalid","problems":[{"field":"/last_id","reason":"required"}]} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"koan.json a directory", func(f *initFixture) { f.mkdir("tasks/koan.json") }, "tasks", false,
			`corrupt {"path":"~/tasks/koan.json","reason":"unexpected-file"} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"koan.json a symlink", func(f *initFixture) {
			f.write("real.json", `{"schema": 2, "migration": 1}`)
			f.mkdir("tasks")
			must(f.t, os.Symlink(f.path("real.json"), f.path("tasks/koan.json")))
		}, "tasks", false,
			`corrupt {"path":"~/tasks/koan.json","reason":"unexpected-file"} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"unsupported koan.json", func(f *initFixture) { f.write("tasks/koan.json", `{"schema": 3}`) }, "tasks", false,
			`unsupported-format {"path":"~/tasks/koan.json","found":3,"supported":[2]} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"koan.json schema 0", func(f *initFixture) { f.write("tasks/koan.json", `{"schema": 0, "last_id": 1}`) }, "tasks", false,
			`unsupported-format {"path":"~/tasks/koan.json","found":0,"supported":[2]} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"koan.json past the latest step", func(f *initFixture) { f.write("tasks/koan.json", `{"schema": 2, "migration": 2}`) }, "tasks", false,
			`unsupported-format {"path":"~/tasks/koan.json","found":2,"supported":[1],"field":"migration"} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"root a symlink to a directory", func(f *initFixture) { f.mkdir("real"); must(f.t, os.Symlink(f.path("real"), f.path("tasks"))) }, "tasks", false,
			"created 0 root_created=false metadata_created=true state_created=true", newMeta, `root = "~/tasks"` + "\n"},

		// Additional validation: something there that is not a directory.
		{"root a file", func(f *initFixture) { f.write("tasks", "") }, "tasks", false,
			`invalid-input {"problems":[{"field":"/root","reason":"must lead to a directory: something else is there"}]} root_created=false metadata_created=false state_created=false`, "", "<none>"},
		{"root a dangling symlink", func(f *initFixture) { must(f.t, os.Symlink(f.path("nowhere"), f.path("tasks"))) }, "tasks", false,
			`invalid-input {"problems":[{"field":"/root","reason":"must lead to a directory: something else is there"}]} root_created=false metadata_created=false state_created=false`, "", "<none>"},

		// The config.
		{"no config directory", func(f *initFixture) { f.env.ConfigDir = "" }, "tasks", false,
			`environment {"variable":"HOME"} root_created=false metadata_created=false state_created=false`, "<none>", ""},
		{"invalid input before environment", func(f *initFixture) { f.env.ConfigDir = ""; f.write("tasks", "") }, "tasks", false,
			`invalid-input {"problems":[{"field":"/root","reason":"must lead to a directory: something else is there"}]} root_created=false metadata_created=false state_created=false`, "", ""},
		{"config exists", func(f *initFixture) { f.write("cfg/config.toml", "not toml at all") }, "tasks", false,
			`conflict {"rule":"config-exists","ids":[]} root_created=false metadata_created=false state_created=false`, "<none>", "not toml at all"},
		{"config exists before not-found", func(f *initFixture) { f.write("cfg/config.toml", "x") }, "a/tasks", false,
			`conflict {"rule":"config-exists","ids":[]} root_created=false metadata_created=false state_created=false`, "", "x"},
		{"replace config", func(f *initFixture) { f.write("cfg/config.toml", `root = "/old"`) }, "tasks", true,
			"created 0 root_created=true metadata_created=true state_created=true", newMeta, `root = "~/tasks"` + "\n"},
		{"replace with no config", nil, "tasks", true,
			"created 0 root_created=true metadata_created=true state_created=true", newMeta, `root = "~/tasks"` + "\n"},
		{"config directory is a file", func(f *initFixture) { f.write("cfg", "") }, "tasks", false,
			`io {"path":"~/cfg/config.toml","code":"ENOTDIR"} root_created=false metadata_created=false state_created=false`, "<none>", ""},
		{"config directory's parents created", func(f *initFixture) { f.env.ConfigDir = f.path("x/y/koan") }, "tasks", false,
			"created 0 root_created=true metadata_created=true state_created=true", newMeta, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newInitFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			res, e := Init(f.env, f.path(tc.root), tc.replace)
			if got := f.summary(res, e); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if tc.meta != "" {
				if got := f.read("tasks/koan.json"); got != tc.meta {
					t.Errorf("koan.json %q, want %q", got, tc.meta)
				}
			}
			if tc.config != "" {
				want := strings.ReplaceAll(tc.config, "~", f.home)
				if got := f.read("cfg/config.toml"); got != want {
					t.Errorf("config %q, want %q", got, want)
				}
			}
		})
	}
}

// Every stale temp file in the config directory is gone, and nothing else.
func TestInitRemovesStaleTemps(t *testing.T) {
	f := newInitFixture(t)
	f.write("cfg/.koan-tmp-1", "x")
	f.write("cfg/.koan-tmp-2", "x")
	f.write("cfg/keep", "y")
	_, e := Init(f.env, f.path("tasks"), false)
	wantNoErr(t, e)
	entries, err := os.ReadDir(f.path("cfg"))
	must(t, err)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	if got := strings.Join(names, " "); got != "config.toml keep state.json" {
		t.Errorf("config directory holds %s", got)
	}
}

// replace_config replaces the config file itself: a symlinked config (into
// a dotfiles checkout, say) becomes a regular file, and the file it pointed
// to is left as it was (operations.md, init, Effects).
func TestInitReplacesSymlinkedConfig(t *testing.T) {
	f := newInitFixture(t)
	f.write("dotfiles/config.toml", `root = "/old"`+"\n")
	f.mkdir("cfg")
	must(t, os.Symlink(f.path("dotfiles/config.toml"), f.path("cfg/config.toml")))
	_, e := Init(f.env, f.path("tasks"), true)
	wantNoErr(t, e)
	fi, err := os.Lstat(f.path("cfg/config.toml"))
	must(t, err)
	if !fi.Mode().IsRegular() {
		t.Errorf("config is %v, want a regular file", fi.Mode())
	}
	if got := f.read("dotfiles/config.toml"); got != `root = "/old"`+"\n" {
		t.Errorf("symlink's target %q", got)
	}
}

// A crash after the config was published leaves its temp file; the init
// that then finds the config exists still removes it.
func TestInitConfigExistsRemovesStaleTemps(t *testing.T) {
	f := newInitFixture(t)
	_, e := Init(f.env, f.path("tasks"), false)
	wantNoErr(t, e)
	f.write("cfg/.koan-tmp-1", "x")
	_, e = Init(f.env, f.path("tasks"), false)
	if e == nil || e.Kind != errs.KindConflict {
		t.Fatalf("got %v, want config-exists", e)
	}
	if _, err := os.Lstat(f.path("cfg/.koan-tmp-1")); !os.IsNotExist(err) {
		t.Errorf("temp file still there: %v", err)
	}
}

// A failure after the root or koan.json was created reports what was
// created; rerunning with the same input completes the job, attaching the
// tree the first run created (operations.md, init, Retry safety).
func TestInitPartialAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(o fsys.Op) bool
		want string
	}{
		{"config not written", func(o fsys.Op) bool { return o.Name == fsys.OpLink && o.NewPath == ConfigName },
			`io {"path":"~/cfg/config.toml","code":"EACCES"} root_created=true metadata_created=true state_created=true`},
		{"config directory not created", func(o fsys.Op) bool { return o.Name == fsys.OpMkdirAll },
			`io {"path":"~/cfg","code":"EACCES"} root_created=true metadata_created=true state_created=false`},
		{"koan.json not written", func(o fsys.Op) bool { return o.Name == fsys.OpLink && o.NewPath == MetaName },
			`io {"path":"~/tasks/koan.json","code":"EACCES"} root_created=true metadata_created=false state_created=false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newInitFixture(t)
			env := f.env
			env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(o fsys.Op) error {
				if tc.fail(o) {
					return syscall.EACCES
				}
				return nil
			}}
			res, e := Init(env, f.path("tasks"), false)
			if got := f.summary(res, e); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if got := f.read("cfg/config.toml"); got != "<none>" {
				t.Errorf("config written: %q", got)
			}
			res, e = Init(f.env, f.path("tasks"), false)
			want := "attached 0 root_created=false metadata_created=false state_created=true"
			if !strings.Contains(tc.want, "metadata_created=true") {
				want = "created 0 root_created=false metadata_created=true state_created=true"
			}
			if got := f.summary(res, e); got != want {
				t.Errorf("retry: got %s, want %s", got, want)
			}
			if got := f.read("tasks/koan.json"); got != newMeta {
				t.Errorf("retry: koan.json %q", got)
			}
		})
	}
}

// The state file init writes: its last_id is the highest of 0, the highest ID
// in any task filename and an older koan.json's last_id (attaching only), and
// the last_id of a usable state file already naming this root.
func TestInitState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *initFixture)
		want  int64
	}{
		{"new tree", nil, 0},
		{"new tree, a usable state file for the root is a floor", func(f *initFixture) {
			f.mkdir("tasks")
			f.write("cfg/state.json", stateJSON(f.path("tasks"), 80))
		}, 80},
		{"new tree, a state file for another root is replaced", func(f *initFixture) {
			f.write("cfg/state.json", stateJSON("/elsewhere", 80))
		}, 0},
		{"new tree, a corrupt state file is replaced", func(f *initFixture) { f.write("cfg/state.json", "{") }, 0},
		{"new tree, an unsupported state file is replaced", func(f *initFixture) { f.write("cfg/state.json", `{"schema": 9}`) }, 0},
		{"attached: the highest filename", func(f *initFixture) {
			f.write("tasks/koan.json", `{"schema": 2, "migration": 1}`)
			f.write("tasks/4.json", "x")
			f.write("tasks/a/b/9.json", "x")
			f.write("tasks/a/12.json", "x")
		}, 12},
		{"attached: an older koan.json's last_id above the filenames", func(f *initFixture) {
			f.write("tasks/koan.json", `{"schema": 1, "last_id": 40}`)
			f.write("tasks/4.json", "x")
		}, 40},
		{"attached: filenames above an older koan.json's last_id", func(f *initFixture) {
			f.write("tasks/koan.json", `{"schema": 1, "last_id": 3}`)
			f.write("tasks/4.json", "x")
		}, 4},
		{"attached: the state file's floor above the rest", func(f *initFixture) {
			f.write("tasks/koan.json", `{"schema": 2, "migration": 1}`)
			f.write("tasks/4.json", "x")
			f.write("cfg/state.json", stateJSON(f.path("tasks"), 90))
		}, 90},
		{"attached: a lower state file is raised to the filenames", func(f *initFixture) {
			f.write("tasks/koan.json", `{"schema": 2, "migration": 1}`)
			f.write("tasks/40.json", "x")
			f.write("cfg/state.json", stateJSON(f.path("tasks"), 5))
		}, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newInitFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			res, e := Init(f.env, f.path("tasks"), true)
			wantNoErr(t, e)
			if res.LastID != tc.want || !res.StateCreated {
				t.Errorf("result %+v, want last_id %d", res, tc.want)
			}
			if got := f.read("cfg/state.json"); got != stateJSON(f.path("tasks"), tc.want) {
				t.Errorf("state file %q", got)
			}
		})
	}
}

// A folder init can't list while attaching is io, before anything is written.
func TestInitUnlistableFolder(t *testing.T) {
	f := newInitFixture(t)
	f.write("tasks/koan.json", `{"schema": 2, "migration": 1}`)
	f.mkdir("tasks/q")
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpReadDir, "q", 1, syscall.EACCES)}
	res, e := Init(f.env, f.path("tasks"), false)
	wantErr(t, e, errs.KindIO, errs.IODetails{Path: f.path("tasks/q"), Code: "EACCES"})
	if res.StateCreated || f.read("cfg/state.json") != "<none>" || f.read("cfg/config.toml") != "<none>" {
		t.Error("init wrote before failing")
	}
}

// A root that exists is locked while init reads and writes the state file:
// busy when another write holds it; a new root has no lock to wait for.
func TestInitBusy(t *testing.T) {
	f := newInitFixture(t)
	f.write("tasks/koan.json", `{"schema": 2, "migration": 1}`)
	r, err := fsys.OS{}.OpenRoot(f.path("tasks"))
	must(t, err)
	defer r.Close()
	l, err := r.Lock()
	must(t, err)
	res, e := Init(f.env, f.path("tasks"), false)
	wantKind(t, e, errs.KindBusy)
	if res.StateCreated || f.read("cfg/state.json") != "<none>" || f.read("cfg/config.toml") != "<none>" {
		t.Error("init wrote while the root was locked")
	}
	l.Unlock()
	_, e = Init(f.env, f.path("tasks"), false)
	wantNoErr(t, e)
}

// An interrupted init that wrote the state file but not the config leaves
// the root not initialized (state), and a rerun finishes the job.
func TestInitStateWrittenConfigNot(t *testing.T) {
	f := newInitFixture(t)
	env := f.env
	env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(o fsys.Op) error {
		if o.Name == fsys.OpLink && o.NewPath == ConfigName {
			return syscall.EACCES
		}
		return nil
	}}
	res, e := Init(env, f.path("tasks"), false)
	if e == nil || !res.StateCreated || f.read("cfg/state.json") != stateJSON(f.path("tasks"), 0) {
		t.Fatalf("%v %+v", e, res)
	}
	_, e = Init(f.env, f.path("tasks"), false)
	wantNoErr(t, e)
}
