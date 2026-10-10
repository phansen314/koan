package store

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/schematest"
)

// Inspect's output for each root state, as info reports it (JSON, so null
// and absent pointers are visible), and that it matches info-output.
func TestInspect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture) Env
		want  string // with CFG for the config path and ROOT for the root
	}{
		{"usable", func(f *fixture) Env { return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":true,"compatible":true,"migration_pending":false}`},
		{"no config directory", func(f *fixture) Env { f.env.ConfigDir = ""; return f.env },
			`{"config":{"path":null,"state":"missing","root":null},"tree":null,"state":null,"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
		{"missing config", func(f *fixture) Env { must(t, os.Remove(f.path("cfg/"+ConfigName))); return f.env },
			`{"config":{"path":"CFG","state":"missing","root":null},"tree":null,"state":null,"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
		{"unreadable config", func(f *fixture) Env {
			return f.withFault(fsys.ErrnoAt(fsys.OpReadFile, "", 1, syscall.EACCES))
		}, `{"config":{"path":"CFG","state":"unreadable","root":null},"tree":null,"state":null,"initialized":true,"usable":false,"compatible":null,"migration_pending":null}`},
		{"config is a FIFO", func(f *fixture) Env {
			must(t, os.Remove(f.path("cfg/"+ConfigName)))
			must(t, syscall.Mkfifo(f.path("cfg/"+ConfigName), 0o644))
			return f.env
		}, `{"config":{"path":"CFG","state":"corrupt","root":null},"tree":null,"state":null,"initialized":true,"usable":false,"compatible":null,"migration_pending":null}`},
		{"corrupt config", func(f *fixture) Env { f.write("cfg/"+ConfigName, "root = \"rel\"\n"); return f.env },
			`{"config":{"path":"CFG","state":"corrupt","root":null},"tree":null,"state":null,"initialized":true,"usable":false,"compatible":null,"migration_pending":null}`},
		{"~/ root without home", func(f *fixture) Env {
			f.write("cfg/"+ConfigName, "root = \"~/tasks\"\n")
			f.env.Home = ""
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":null},"tree":null,"state":null,"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
		{"missing root", func(f *fixture) Env { must(t, os.RemoveAll(f.root)); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":false,"metadata":"missing","schema":null,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
		{"root is a file", func(f *fixture) Env { must(t, os.RemoveAll(f.root)); f.write("tasks", ""); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":false,"metadata":"missing","schema":null,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
		{"root is a symlink loop", func(f *fixture) Env {
			must(t, os.RemoveAll(f.root))
			must(t, os.Symlink("tasks", f.root))
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":false,"metadata":"missing","schema":null,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
		{"root is a symlink to a directory", func(f *fixture) Env {
			must(t, os.Rename(f.root, f.path("real")))
			must(t, os.Symlink("real", f.root))
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":true,"compatible":true,"migration_pending":false}`},
		{"root is a dangling symlink", func(f *fixture) Env {
			must(t, os.RemoveAll(f.root))
			must(t, os.Symlink("gone", f.root))
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":false,"metadata":"missing","schema":null,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
		{"root cannot be opened", func(f *fixture) Env {
			return f.withFault(fsys.ErrnoAt(fsys.OpOpenRoot, "", 1, syscall.EACCES))
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"unreadable","schema":null,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":null,"migration_pending":null}`},
		{"missing koan.json", func(f *fixture) Env { must(t, os.Remove(f.root+"/"+MetaName)); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"missing","schema":null,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
		{"unreadable koan.json", func(f *fixture) Env {
			return f.withFault(fsys.ErrnoAt(fsys.OpReadFile, MetaName, 1, syscall.EIO))
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"unreadable","schema":null,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":null,"migration_pending":null}`},
		{"koan.json not JSON", func(f *fixture) Env { f.write("tasks/"+MetaName, "[]"); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"corrupt","schema":null,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":null,"migration_pending":null}`},
		{"koan.json corrupt at step 3", func(f *fixture) Env {
			f.write("tasks/"+MetaName, `{"schema": 2, "migration": 1.5}`)
			return f.env
		},
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"corrupt","schema":2,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":true,"migration_pending":null}`},
		{"koan.json in an older format", func(f *fixture) Env { f.write("tasks/"+MetaName, `{"schema": 1, "last_id": 9}`); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"old-format","schema":1,"migration":0},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":false,"migration_pending":true}`},
		{"older koan.json breaking its own format", func(f *fixture) Env {
			f.write("tasks/"+MetaName, `{"schema": 1, "last_id": 9, "migration": 1}`)
			return f.env
		},
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"corrupt","schema":1,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":false,"migration_pending":null}`},
		{"koan.json a step behind", func(f *fixture) Env {
			f.write("tasks/"+MetaName, `{"schema": 2, "migration": 0}`)
			return f.env
		},
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":0},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":true,"migration_pending":true}`},
		{"koan.json past the latest step", func(f *fixture) Env {
			f.write("tasks/"+MetaName, `{"schema": 2, "migration": 2}`)
			return f.env
		},
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"unsupported-format","schema":2,"migration":2},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":true,"migration_pending":false}`},
		{"koan.json schema 0", func(f *fixture) Env { f.write("tasks/"+MetaName, `{"schema": 0, "last_id": 9}`); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"unsupported-format","schema":0,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":false,"migration_pending":null}`},
		{"koan.json missing migration", func(f *fixture) Env { f.write("tasks/"+MetaName, `{"schema": 2}`); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"corrupt","schema":2,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":true,"migration_pending":null}`},
		{"koan.json unsupported", func(f *fixture) Env { f.write("tasks/"+MetaName, `{"schema": 3}`); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"unsupported-format","schema":3,"migration":null},"state":{"path":"STATE","state":"ok","last_id":100},"initialized":true,"usable":false,"compatible":false,"migration_pending":null}`},
		{"state file missing", func(f *fixture) Env { must(t, os.Remove(f.path("cfg/"+StateName))); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":"STATE","state":"missing","last_id":null},"initialized":false,"usable":false,"compatible":true,"migration_pending":false}`},
		{"state file for another root", func(f *fixture) Env { f.write("cfg/"+StateName, stateJSON("/elsewhere", 9)); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":"STATE","state":"other-root","last_id":null},"initialized":false,"usable":false,"compatible":true,"migration_pending":false}`},
		{"state file corrupt", func(f *fixture) Env { f.write("cfg/"+StateName, `{"schema": 1, "root": "x"}`); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":"STATE","state":"corrupt","last_id":null},"initialized":true,"usable":false,"compatible":true,"migration_pending":false}`},
		{"state file unsupported", func(f *fixture) Env { f.write("cfg/"+StateName, `{"schema": 2}`); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":"STATE","state":"unsupported-format","last_id":null},"initialized":true,"usable":false,"compatible":true,"migration_pending":false}`},
		{"state file unreadable", func(f *fixture) Env {
			return f.withFault(fsys.ErrnoAt(fsys.OpReadFile, f.path("cfg/"+StateName), 1, syscall.EACCES))
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":"STATE","state":"unreadable","last_id":null},"initialized":true,"usable":false,"compatible":true,"migration_pending":false}`},
		{"state file a directory", func(f *fixture) Env {
			must(t, os.Remove(f.path("cfg/"+StateName)))
			f.mkdir("cfg/" + StateName)
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":"STATE","state":"corrupt","last_id":null},"initialized":true,"usable":false,"compatible":true,"migration_pending":false}`},
		// A missing state file does not make a root that needs migration
		// not initialized: the state file counts once koan.json is current.
		{"state file missing, step pending", func(f *fixture) Env {
			must(t, os.Remove(f.path("cfg/"+StateName)))
			f.write("tasks/"+MetaName, `{"schema": 1, "last_id": 9}`)
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"old-format","schema":1,"migration":0},"state":{"path":"STATE","state":"missing","last_id":null},"initialized":true,"usable":false,"compatible":false,"migration_pending":true}`},
		{"state file missing, koan.json missing", func(f *fixture) Env {
			must(t, os.Remove(f.path("cfg/"+StateName)))
			must(t, os.Remove(f.root+"/"+MetaName))
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"missing","schema":null,"migration":null},"state":{"path":"STATE","state":"missing","last_id":null},"initialized":false,"usable":false,"compatible":null,"migration_pending":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			got, err := json.Marshal(Inspect(tc.setup(f)))
			must(t, err)
			want := tc.want
			for k, v := range map[string]string{"CFG": f.path("cfg/" + ConfigName), "ROOT": f.root, "STATE": f.path("cfg/" + StateName)} {
				b, _ := json.Marshal(v)
				want = strings.ReplaceAll(want, `"`+k+`"`, string(b))
			}
			if string(got) != want {
				t.Errorf("got  %s\nwant %s", got, want)
			}
			if ok, f := schematest.Check(t, "info-output", got); !ok {
				t.Errorf("info-output rejects at %s: %s", f, got)
			}
		})
	}
}
