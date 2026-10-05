package store

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/schematest"
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
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":1,"last_id":100},"initialized":true,"usable":true,"compatible":true}`},
		{"no config directory", func(f *fixture) Env { f.env.ConfigDir = ""; return f.env },
			`{"config":{"path":null,"state":"missing","root":null},"tree":null,"initialized":false,"usable":false,"compatible":null}`},
		{"missing config", func(f *fixture) Env { must(t, os.Remove(f.path("cfg/"+ConfigName))); return f.env },
			`{"config":{"path":"CFG","state":"missing","root":null},"tree":null,"initialized":false,"usable":false,"compatible":null}`},
		{"unreadable config", func(f *fixture) Env {
			return f.withFault(fsys.ErrnoAt(fsys.OpReadFile, "", 1, syscall.EACCES))
		}, `{"config":{"path":"CFG","state":"unreadable","root":null},"tree":null,"initialized":true,"usable":false,"compatible":null}`},
		{"config is a FIFO", func(f *fixture) Env {
			must(t, os.Remove(f.path("cfg/"+ConfigName)))
			must(t, syscall.Mkfifo(f.path("cfg/"+ConfigName), 0o644))
			return f.env
		}, `{"config":{"path":"CFG","state":"corrupt","root":null},"tree":null,"initialized":true,"usable":false,"compatible":null}`},
		{"corrupt config", func(f *fixture) Env { f.write("cfg/"+ConfigName, "root = \"rel\"\n"); return f.env },
			`{"config":{"path":"CFG","state":"corrupt","root":null},"tree":null,"initialized":true,"usable":false,"compatible":null}`},
		{"~/ root without home", func(f *fixture) Env {
			f.write("cfg/"+ConfigName, "root = \"~/tasks\"\n")
			f.env.Home = ""
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":null},"tree":null,"initialized":false,"usable":false,"compatible":null}`},
		{"missing root", func(f *fixture) Env { must(t, os.RemoveAll(f.root)); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":false,"metadata":"missing","schema":null,"last_id":null},"initialized":false,"usable":false,"compatible":null}`},
		{"root is a file", func(f *fixture) Env { must(t, os.RemoveAll(f.root)); f.write("tasks", ""); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":false,"metadata":"missing","schema":null,"last_id":null},"initialized":false,"usable":false,"compatible":null}`},
		{"root is a symlink loop", func(f *fixture) Env {
			must(t, os.RemoveAll(f.root))
			must(t, os.Symlink("tasks", f.root))
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":false,"metadata":"missing","schema":null,"last_id":null},"initialized":false,"usable":false,"compatible":null}`},
		{"root is a symlink to a directory", func(f *fixture) Env {
			must(t, os.Rename(f.root, f.path("real")))
			must(t, os.Symlink("real", f.root))
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"ok","schema":1,"last_id":100},"initialized":true,"usable":true,"compatible":true}`},
		{"root is a dangling symlink", func(f *fixture) Env {
			must(t, os.RemoveAll(f.root))
			must(t, os.Symlink("gone", f.root))
			return f.env
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":false,"metadata":"missing","schema":null,"last_id":null},"initialized":false,"usable":false,"compatible":null}`},
		{"root cannot be opened", func(f *fixture) Env {
			return f.withFault(fsys.ErrnoAt(fsys.OpOpenRoot, "", 1, syscall.EACCES))
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"unreadable","schema":null,"last_id":null},"initialized":true,"usable":false,"compatible":null}`},
		{"missing ftask.json", func(f *fixture) Env { must(t, os.Remove(f.root+"/"+MetaName)); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"missing","schema":null,"last_id":null},"initialized":false,"usable":false,"compatible":null}`},
		{"unreadable ftask.json", func(f *fixture) Env {
			return f.withFault(fsys.ErrnoAt(fsys.OpReadFile, MetaName, 1, syscall.EIO))
		}, `{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"unreadable","schema":null,"last_id":null},"initialized":true,"usable":false,"compatible":null}`},
		{"ftask.json not JSON", func(f *fixture) Env { f.write("tasks/"+MetaName, "[]"); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"corrupt","schema":null,"last_id":null},"initialized":true,"usable":false,"compatible":null}`},
		{"ftask.json corrupt at step 3", func(f *fixture) Env { f.write("tasks/"+MetaName, `{"schema": 1, "last_id": 1.5}`); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"corrupt","schema":1,"last_id":null},"initialized":true,"usable":false,"compatible":true}`},
		{"ftask.json unsupported", func(f *fixture) Env { f.write("tasks/"+MetaName, `{"schema": 2}`); return f.env },
			`{"config":{"path":"CFG","state":"ok","root":"ROOT"},"tree":{"root_exists":true,"metadata":"unsupported-format","schema":2,"last_id":null},"initialized":true,"usable":false,"compatible":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			got, err := json.Marshal(Inspect(tc.setup(f)))
			must(t, err)
			want := tc.want
			for k, v := range map[string]string{"CFG": f.path("cfg/" + ConfigName), "ROOT": f.root} {
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
