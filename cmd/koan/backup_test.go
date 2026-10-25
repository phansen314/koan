package main_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// regular counts the regular files under a directory, and their bytes.
func regular(t *testing.T, dir string) (files, bytes int64) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		files++
		bytes += info.Size()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files, bytes
}

// everything is every entry under a directory: directories ending in /,
// symlinks with their target after ->, files with their mode and
// content.
func everything(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch {
		case d.IsDir():
			out = append(out, rel+"/")
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			out = append(out, rel+" -> "+target)
			return err
		default:
			info, err := d.Info()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			out = append(out, fmt.Sprintf("%s %v=%s", rel, info.Mode().Perm(), b))
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// restore puts a backup in place of a fresh home's ~/koan-root.
func restore(t *testing.T, backup string) string {
	t.Helper()
	home := t.TempDir()
	hand(t, os.Rename(backup, filepath.Join(home, "koan-root")))
	return home
}

var bytesField = regexp.MustCompile(`"bytes":[0-9]+`)

// goldenBackup compares a backup's result to its golden, its byte count
// left out, since the database's size depends on SQLite's version.
func goldenBackup(t *testing.T, r *testrun.Result, name string) {
	t.Helper()
	r.Golden(t, name, *update, map[string]string{bytesField.FindString(r.Line()): `"bytes":"$BYTES"`})
}

// The PRD's Backup and restore test, without the concurrency run: a
// backup copies the database, the tree and the archive into a new
// directory, its missing parents made, files copied with their modes and
// symlinks as symlinks; it restores to a root doctor finds clean, which
// reads as the root did.
func TestBackup(t *testing.T) {
	home := base(t)
	ok(t, home, "folder", "create", "/done/old") // 7, 8
	ok(t, home, "archive", "/done/old")
	hand(t, os.MkdirAll(filepath.Join(treeDir(home), "@work", "src", "@types"), 0o755))
	write(t, home, "@work/src/main.go", "package main")
	hand(t, os.Chmod(filepath.Join(treeDir(home), "@work", "src", "main.go"), 0o600))
	hand(t, os.Symlink("../src/main.go", filepath.Join(treeDir(home), "@work", "src", "@types", "link")))
	write(t, home, "@work/@3.md", "work notes, edited")

	dir := filepath.Join(home, "backups", "one")
	r := ok(t, home, "backup", dir)
	goldenBackup(t, r, "backup")
	res := r.Result()
	files, bytes := regular(t, dir)
	if res["files"] != json.Number(fmt.Sprint(files)) || res["bytes"] != json.Number(fmt.Sprint(bytes)) {
		t.Errorf("counted %d files, %d bytes: %s", files, bytes, r.Line())
	}
	root := filepath.Join(home, "koan-root")
	for _, name := range []string{"tree", "archived"} {
		got, want := everything(t, filepath.Join(dir, name)), everything(t, filepath.Join(root, name))
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"archived", "koan.db", "tree"}) {
		t.Errorf("backup holds %q", names)
	}

	want := ok(t, home, "task", "list", "--state", "open,complete", "--fields", "title,notes_path").Line()
	restored := restore(t, dir)
	r = ok(t, restored, "doctor")
	if len(r.Result()["findings"].([]any)) != 0 {
		t.Errorf("doctor on the restored root: %s", r.Line())
	}
	got := ok(t, restored, "task", "list", "--state", "open,complete", "--fields", "title,notes_path").Line()
	if strings.ReplaceAll(got, restored, home) != want {
		t.Errorf("restored root reads\n%s\nnot\n%s", got, want)
	}
	if r := ok(t, restored, "info"); r.Result()["state"] != "usable" {
		t.Errorf("info: %s", r.Line())
	}
	// It takes writes, as the root did.
	ok(t, restored, "task", "create", "After", "--folder", "/work")

	// An empty directory is taken as a new one is.
	hand(t, os.Mkdir(filepath.Join(home, "two"), 0o755))
	r = ok(t, home, "backup", filepath.Join(home, "two"))
	if r.Result()["dir"] != filepath.Join(home, "two") {
		t.Errorf("dir: %s", r.Line())
	}
}

// What the live root finds too, such as a stuck step or a stray entry,
// is copied faithfully and reported with the copy's paths; the copy is
// clean, and a restore finds the same.
func TestBackupCopiesFindings(t *testing.T) {
	home := base(t)
	write(t, home, "@Notes.md", "by hand")
	write(t, home, "@work/@5.md", "work copy")
	write(t, home, "@5.md", "root copy")
	db(t, home, `INSERT INTO pending (seq, change, step, stuck) VALUES (1, 1, '{"op":"move","from":"tree/@work/@5.md","to":"tree/@5.md","stuck":"both"}', 1)`)
	live := summary(ok(t, home, "doctor").Result()["findings"])
	dir := filepath.Join(home, "copy")
	r := ok(t, home, "backup", dir)
	goldenBackup(t, r, "backup-findings")
	if r.Result()["clean"] != true || r.Result()["recopied"] != false {
		t.Errorf("clean, recopied: %s", r.Line())
	}
	sameList(t, "findings", summary(r.Result()["findings"]), live)
	for _, f := range r.Result()["findings"].([]any) {
		for _, p := range f.(map[string]any)["paths"].([]any) {
			if !strings.HasPrefix(p.(string), dir+"/") {
				t.Errorf("a path not in the copy: %s", p)
			}
		}
	}
	sameList(t, "restored", summary(ok(t, restore(t, dir), "doctor").Result()["findings"]), live)
}

// backup goes only into an empty or new directory, outside the root,
// judged by real paths, so a symlink is no way in.
func TestBackupRefused(t *testing.T) {
	home := base(t)
	hand(t, os.MkdirAll(filepath.Join(home, "full", "x"), 0o755))
	golden(t, fails(t, home, "conflict", "exists", "backup", filepath.Join(home, "full")), "backup-exists")
	hand(t, os.WriteFile(filepath.Join(home, "file"), nil, 0o644))
	fails(t, home, "conflict", "exists", "backup", filepath.Join(home, "file"))

	golden(t, fails(t, home, "invalid-input", "", "backup", filepath.Join(home, "koan-root", "b")), "backup-under-root")
	fails(t, home, "invalid-input", "", "backup", filepath.Join(home, "koan-root"))
	hand(t, os.Symlink(filepath.Join(home, "koan-root", "tree"), filepath.Join(home, "link")))
	fails(t, home, "invalid-input", "", "backup", filepath.Join(home, "link", "new"))
	for _, p := range []string{filepath.Join(home, "koan-root", "b"), filepath.Join(home, "link", "new")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("made %s", p)
		}
	}

	// On the command line a relative dir is made absolute from the
	// working directory, as --dir is for bind; through --input it
	// must be absolute.
	fails(t, home, "invalid-input", "", "backup", filepath.Join("koan-root", "rel"))
	r := ok(t, home, "backup", "rel")
	if got, want := r.Result()["dir"], filepath.Join(home, "rel"); got != want {
		t.Errorf("a relative dir: got %v, want %s", got, want)
	}
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"dir":"rel2"}`)}.Koan(t, "backup", "-i", "-")
	r.Check(t, "backup")
	if r.Kind() != "invalid-input" {
		t.Errorf("a relative dir: %s", r.Line())
	}
	r = koan(t, home, "backup")
	if r.Kind() != "invalid-input" {
		t.Errorf("no dir: %s", r.Line())
	}
	fails(t, t.TempDir(), "not-initialized", "", "backup", filepath.Join(home, "new"))
}
