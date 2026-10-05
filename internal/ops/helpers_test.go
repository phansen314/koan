package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

// fixedClock is the tests' clock, so written files compare byte for byte.
func fixedClock() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }

// stamped is file, a task file as ftask writes it, with its updated_at set to
// fixedClock, as a write that changes the task leaves it.
func stamped(file string) string {
	return regexp.MustCompile(`"updated_at": "[^"]*"`).ReplaceAllString(file, `"updated_at": "2026-09-28T12:00:00Z"`)
}

// fixture is a usable root under a temp home: the config in cfg/, the root
// at tasks/ with ftask.json's last_id 100, and no tasks.
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
	f.env = Env{Env: store.Env{FS: fsys.OS{}, Home: home, ConfigDir: filepath.Join(home, "cfg")}, Clock: fixedClock}
	f.write("cfg/"+store.ConfigName, string(store.EncodeConfig(f.root)))
	f.write("tasks/"+store.MetaName, "{\"schema\": 1, \"last_id\": 100}\n")
	return f
}

// write writes content at rel, under the home, creating its directory.
func (f *fixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) remove(rel string) {
	f.t.Helper()
	if err := os.RemoveAll(filepath.Join(f.home, rel)); err != nil {
		f.t.Fatal(err)
	}
}

// task writes a valid task file for id in folder (a path under the root,
// "" for the root), open or completed, with blockedBy.
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
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join("tasks", folder, fmt.Sprintf("%d.json", id)), string(data))
}

// tagged writes an open task id in folder with tags.
func (f *fixture) tagged(folder string, id model.ID, tags ...model.Tag) {
	f.t.Helper()
	tf := model.TaskFile{
		Schema: model.TaskSchema, ID: id, Title: model.Title(fmt.Sprintf("task %d", id)),
		CreatedAt: "2026-09-20T18:31:51Z", UpdatedAt: "2026-09-20T18:31:51Z", Tags: tags, Extra: &jsonio.Object{},
	}
	data, err := tf.Encode()
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join("tasks", folder, fmt.Sprintf("%d.json", id)), string(data))
}

// fail wraps f's filesystem so op on the path rel (relative to the root, as
// store passes it) fails with errno.
func (f *fixture) fail(op, rel string, errno syscall.Errno) {
	f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: func(o fsys.Op) error {
		if o.Name == op && o.Path == rel {
			return errno
		}
		return nil
	}}
}

// read is the file at rel, under the home, or "<none>" when nothing is there.
func (f *fixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.home, rel))
	if os.IsNotExist(err) {
		return "<none>"
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// hook wraps f's filesystem so each call runs through h first.
func (f *fixture) hook(h fsys.Hook) { f.env.FS = fsys.Fault{FS: fsys.OS{}, Hook: h} }

// failFile fails every look at the file at rel, under the root, with errno:
// Lstat and ReadFile, as in a folder that can be listed but not searched.
func (f *fixture) failFile(rel string, errno syscall.Errno) {
	f.hook(func(o fsys.Op) error {
		if (o.Name == fsys.OpLstat || o.Name == fsys.OpReadFile) && o.Path == rel {
			return errno
		}
		return nil
	})
}

// rel replaces f's home in s with "~", so expected outputs name paths short.
func (f *fixture) rel(s string) string { return strings.ReplaceAll(s, f.home, "~") }
