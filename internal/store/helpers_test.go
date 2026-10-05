package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// fixture is a usable root, <home>/tasks, named by a config in <home>/cfg.
type fixture struct {
	t    *testing.T
	home string
	root string
	env  Env
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	f := &fixture{t: t, home: home, root: filepath.Join(home, "tasks")}
	f.env = Env{FS: fsys.OS{}, Home: home, ConfigDir: filepath.Join(home, "cfg")}
	f.mkdir("cfg")
	f.write("cfg/"+ConfigName, string(EncodeConfig(f.root)))
	f.mkdir("tasks")
	f.write("tasks/"+MetaName, "{\"schema\": 1, \"last_id\": 100}\n")
	return f
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// path is the absolute path of rel, relative to home.
func (f *fixture) path(rel string) string { return filepath.Join(f.home, rel) }

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	must(f.t, os.WriteFile(f.path(rel), []byte(content), 0o644))
}

func (f *fixture) mkdir(rel string) {
	f.t.Helper()
	must(f.t, os.MkdirAll(f.path(rel), 0o755))
}

func (f *fixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(f.path(rel))
	must(f.t, err)
	return string(b)
}

// task writes a valid task file for id in folder (a path under the root,
// "" for the root), creating the folder, with blockedBy and completed.
func (f *fixture) task(folder string, id model.ID, completed bool, blockedBy ...model.ID) {
	f.t.Helper()
	tf := model.TaskFile{
		Schema: model.TaskSchema, ID: id, Title: model.Title(fmt.Sprintf("task %d", id)),
		CreatedAt: "2026-09-20T18:31:51Z", UpdatedAt: "2026-09-20T18:31:51Z", BlockedBy: blockedBy, Extra: &jsonio.Object{},
	}
	if completed {
		ts := model.Timestamp("2026-09-21T10:00:00Z")
		tf.CompletedAt = &ts
	}
	data, err := tf.Encode()
	must(f.t, err)
	dir := filepath.Join("tasks", folder)
	f.mkdir(dir)
	f.write(filepath.Join(dir, fmt.Sprintf("%d.json", id)), string(data))
}

// withFault returns f's env with the filesystem wrapped by hook.
func (f *fixture) withFault(hook fsys.Hook) Env {
	env := f.env
	env.FS = fsys.Fault{FS: fsys.OS{}, Hook: hook}
	return env
}

// read runs fn in a Read over env, failing the test on an error.
func readTx(t *testing.T, env Env, w *errs.Collector, fn func(*Tx)) {
	t.Helper()
	if e := Read(env, w, func(tx *Tx) *errs.Error { fn(tx); return nil }); e != nil {
		t.Fatalf("Read: %v", e)
	}
}

func writeTx(t *testing.T, env Env, w *errs.Collector, fn func(*Tx)) {
	t.Helper()
	if e := Write(env, w, func(tx *Tx) *errs.Error { fn(tx); return nil }); e != nil {
		t.Fatalf("Write: %v", e)
	}
}

// wantErr checks that e is an error of kind with details want.
func wantErr(t *testing.T, e *errs.Error, kind errs.Kind, want any) {
	t.Helper()
	if e == nil {
		t.Fatalf("got no error, want %s %+v", kind, want)
	}
	if e.Kind != kind || !reflect.DeepEqual(e.Details, want) {
		t.Fatalf("got %s %+v (%s), want %s %+v", e.Kind, e.Details, e.Message, kind, want)
	}
}

func wantKind(t *testing.T, e *errs.Error, kind errs.Kind) {
	t.Helper()
	if e == nil || e.Kind != kind {
		t.Fatalf("got %v, want %s", e, kind)
	}
}

func wantNoErr(t *testing.T, e *errs.Error) {
	t.Helper()
	if e != nil {
		t.Fatalf("got %v", e)
	}
}
