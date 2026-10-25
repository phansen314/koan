package doctor_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/doctor"
	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/testrun"
	_ "modernc.org/sqlite"
)

// The binary is built for the writes made while backup copies, from
// outside its process.
func TestMain(m *testing.M) { testrun.Main(m) }

// koan runs the binary in home, which must succeed.
func koan(t *testing.T, home string, args ...string) map[string]any {
	t.Helper()
	r := testrun.Run{Home: home}.Koan(t, args...)
	if !r.OK() {
		t.Fatalf("koan %q: %s", args, r.Line())
	}
	return r.Result()
}

// backup runs backup in this process, so the hook is set, and checks its
// output against its schema.
func backup(t *testing.T, home, dir string) map[string]any {
	t.Helper()
	t.Setenv("HOME", home)
	var stdout, stderr bytes.Buffer
	code := cli.Main([]string{"backup", dir}, strings.NewReader(""), &stdout, &stderr, ops.Commands())
	r := &testrun.Result{Args: []string{"backup", dir}, Home: home, Stdout: stdout.String(), Stderr: stderr.String(), Exit: code}
	dec := json.NewDecoder(strings.NewReader(stdout.String()))
	dec.UseNumber()
	if err := dec.Decode(&r.Envelope); err != nil {
		t.Fatalf("backup: %v: %s", err, stdout.String())
	}
	r.Check(t, "backup")
	if code != 0 || !r.OK() {
		t.Fatalf("backup: exit %d: %s", code, r.Line())
	}
	return r.Result()
}

func hook(t *testing.T, fn func(stage string)) {
	doctor.Hook = fn
	t.Cleanup(func() { doctor.Hook = nil })
}

// The PRD's Backup and restore test: a folder moved mid-copy, after the
// database is copied and before the tree is, makes the copy's tree
// disagree with its database, so backup copies again holding the lock.
// The second copy is clean, and restores to a root doctor finds clean,
// with the folder where the move put it.
func TestBackupRecopies(t *testing.T) {
	home := t.TempDir()
	koan(t, home, "init")
	koan(t, home, "folder", "create", "/a/x")
	koan(t, home, "task", "create", "In x", "--folder", "/a/x", "--notes", "x notes")
	moves := 0
	hook(t, func(stage string) {
		if stage == "copy" && moves == 0 {
			moves++
			koan(t, home, "folder", "mv", "-T", "/a", "/b")
		}
	})
	dir := filepath.Join(t.TempDir(), "backup")
	res := backup(t, home, dir)
	if res["recopied"] != true || res["clean"] != true || len(res["findings"].([]any)) != 0 {
		t.Errorf("result: %v", res)
	}

	restored := t.TempDir()
	if err := os.Rename(dir, filepath.Join(restored, "koan-root")); err != nil {
		t.Fatal(err)
	}
	if f := koan(t, restored, "doctor")["findings"].([]any); len(f) != 0 {
		t.Errorf("doctor on the restored root: %v", f)
	}
	task := koan(t, restored, "show", "3")["task"].(map[string]any)
	if task["folder"].(map[string]any)["path"] != "/b/x" {
		t.Errorf("task: %v", task)
	}
	b, err := os.ReadFile(task["notes_path"].(string))
	if err != nil || string(b) != "x notes" {
		t.Errorf("notes %q, %v", b, err)
	}
}

// A copy that still has findings of its own after the second copy is
// reported, not clean: here a stray entry a person keeps making and
// removing, there each time the tree is copied and gone each time doctor
// looks at the live root.
func TestBackupNotClean(t *testing.T) {
	home := t.TempDir()
	koan(t, home, "init")
	koan(t, home, "folder", "create", "/a")
	stray := filepath.Join(home, "koan-root", "tree", "@a", "@scratch")
	hook(t, func(stage string) {
		var err error
		if stage == "copy" {
			err = os.WriteFile(stray, []byte("x"), 0o644)
		} else {
			err = os.Remove(stray)
		}
		if err != nil {
			t.Error(err)
		}
	})
	dir := filepath.Join(t.TempDir(), "backup")
	res := backup(t, home, dir)
	if res["recopied"] != true || res["clean"] != false {
		t.Errorf("result: %v", res)
	}
	found := res["findings"].([]any)
	if len(found) != 1 {
		t.Fatalf("findings: %v", found)
	}
	f := found[0].(map[string]any)
	if f["kind"] != "stray-entry" || f["paths"].([]any)[0] != filepath.Join(dir, "tree", "@a", "@scratch") {
		t.Errorf("finding: %v", f)
	}
}
