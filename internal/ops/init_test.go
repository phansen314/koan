package ops

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/schematest"
	"github.com/phansen314/ftask/internal/store"
)

// init through Run: its output, then a failure after the tree was created,
// which carries init-partial. store covers every precondition.
func TestInit(t *testing.T) {
	home := t.TempDir()
	env := Env{Env: store.Env{FS: fsys.OS{}, Home: home, ConfigDir: filepath.Join(home, "cfg")}}
	run := func(env Env, schema string) string {
		t.Helper()
		e := Run("init", parse(t, fmt.Sprintf(`{"root": %q}`, home+"/./tasks/")), nil, env)
		got := line(t, e)
		var part any = e.Result
		if !e.OK {
			part = e.Error.Partial
		}
		b, err := jsonio.MarshalLine(part)
		if err != nil {
			t.Fatal(err)
		}
		if ok, f := schematest.Check(t, schema, b); !ok {
			t.Errorf("%s rejects at %s: %s", schema, f, b)
		}
		return strings.ReplaceAll(got, home, "~")
	}

	failing := env
	failing.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(o fsys.Op) error {
		if o.Name == fsys.OpLink && o.NewPath == store.ConfigName {
			return syscall.EACCES
		}
		return nil
	}}
	got := run(failing, "init-partial")
	want := `{"ok":false,"error":{"kind":"io","message":"~/cfg/config.toml: EACCES","details":{"path":"~/cfg/config.toml","code":"EACCES"},"partial":{"root_created":true,"metadata_created":true}},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	// The cleaned root is what the config records and the output reports.
	got = run(env, "init-output")
	want = `{"ok":true,"result":{"root":"~/tasks","action":"attached","last_id":0},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
