package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
	"github.com/phansen314/koan/internal/store"
)

// info runs through Run to store.Inspect: a usable root, and a machine with
// nothing set up, which is state (ok) rather than an error. store tests
// cover every other state.
func TestInfo(t *testing.T) {
	home := t.TempDir()
	cfgDir, root := filepath.Join(home, "cfg"), filepath.Join(home, "tasks")
	env := Env{Env: store.Env{FS: fsys.OS{}, Home: home, ConfigDir: cfgDir}}
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	cfgPath := q(filepath.Join(cfgDir, store.ConfigName))

	check := func(want string) {
		t.Helper()
		e := Run("info", parse(t, `{}`), nil, env)
		if got := line(t, e); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
		b, err := jsonio.MarshalLine(e.Result)
		if err != nil {
			t.Fatal(err)
		}
		if ok, f := schematest.Check(t, "info-output", b); !ok {
			t.Errorf("info-output rejects at %s: %s", f, b)
		}
	}

	check(`{"ok":true,"result":{"config":{"path":` + cfgPath + `,"state":"missing","root":null},"tree":null,"state":null,"initialized":false,"usable":false,"compatible":null,"migration_pending":null},"warnings":[]}` + "\n")

	for _, err := range []error{
		os.Mkdir(cfgDir, 0o755),
		os.WriteFile(filepath.Join(cfgDir, store.ConfigName), store.EncodeConfig(root), 0o644),
		os.Mkdir(root, 0o755),
		os.WriteFile(filepath.Join(root, store.MetaName), []byte(`{"schema": 2, "migration": 1}`), 0o644),
		os.WriteFile(filepath.Join(cfgDir, store.StateName), []byte(fmt.Sprintf(`{"schema": 1, "root": %q, "last_id": 7}`, root)), 0o644),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	check(`{"ok":true,"result":{"config":{"path":` + cfgPath + `,"state":"ok","root":` + q(root) + `},"tree":{"root_exists":true,"metadata":"ok","schema":2,"migration":1},"state":{"path":` + q(filepath.Join(cfgDir, store.StateName)) + `,"state":"ok","last_id":7},"initialized":true,"usable":true,"compatible":true,"migration_pending":false},"warnings":[]}` + "\n")
}
