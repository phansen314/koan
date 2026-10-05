package fsys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// newRoot creates a temp directory, runs setup in it, and opens it as a Root.
func newRoot(t *testing.T, setup func(dir string)) (Root, string) {
	t.Helper()
	dir := t.TempDir()
	if setup != nil {
		setup(dir)
	}
	r, err := OS{}.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErrno(t *testing.T, err error, want syscall.Errno) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// symlinkTree has a regular file, a directory, and a symlink to each, all
// inside the root: the symlinks os.Root itself would follow.
func symlinkTree(t *testing.T) func(string) {
	return func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("{}"), 0o644))
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
		must(t, os.Symlink("1.json", filepath.Join(dir, "2.json")))
		must(t, os.Symlink("sub", filepath.Join(dir, "link")))
	}
}

func TestReadFileRejectsSymlink(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	got, err := r.ReadFile("1.json")
	must(t, err)
	if string(got) != "{}" {
		t.Fatalf("got %q", got)
	}
	_, err = r.ReadFile("2.json")
	wantErrno(t, err, syscall.ELOOP)
}

func TestReadDirRejectsSymlink(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	if _, err := r.ReadDir("sub"); err != nil {
		t.Fatal(err)
	}
	_, err := r.ReadDir("link")
	wantErrno(t, err, syscall.ELOOP)
}

func TestLstatReportsSymlink(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	fi, err := r.Lstat("link")
	must(t, err)
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("mode %v, want a symlink", fi.Mode())
	}
}

// An entry swapped for a symlink just before the open fails with ELOOP: the
// open itself refuses it.
func TestReadFileRejectsSwap(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("a"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "2.json"), []byte("b"), 0o644))
	})
	beforeOpen = func(name string) {
		p := filepath.Join(dir, name)
		must(t, os.Remove(p))
		must(t, os.Symlink("2.json", p))
	}
	t.Cleanup(func() { beforeOpen = nil })
	_, err := r.ReadFile("1.json")
	wantErrno(t, err, syscall.ELOOP)
}

// A file renamed away and replaced by a symlink to itself is refused too, not
// followed back to the same file.
func TestReadFileRejectsSwapToSameFile(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("a"), 0o644))
	})
	beforeOpen = func(name string) {
		p := filepath.Join(dir, name)
		must(t, os.Rename(p, filepath.Join(dir, "2.json")))
		must(t, os.Symlink("2.json", p))
	}
	t.Cleanup(func() { beforeOpen = nil })
	_, err := r.ReadFile("1.json")
	wantErrno(t, err, syscall.ELOOP)
}

// A file replaced by a concurrent write just before the open is read in its
// new version, not reported as a symlink.
func TestReadFileFollowsReplacement(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("old"), 0o644))
	})
	calls := 0
	beforeOpen = func(name string) {
		calls++
		if calls == 1 {
			tmp := filepath.Join(dir, "tmp")
			must(t, os.WriteFile(tmp, []byte("new"), 0o644))
			must(t, os.Rename(tmp, filepath.Join(dir, name)))
		}
	}
	t.Cleanup(func() { beforeOpen = nil })
	got, err := r.ReadFile("1.json")
	must(t, err)
	if string(got) != "new" {
		t.Errorf("got %q, want the new version", got)
	}
	if calls != 1 {
		t.Errorf("opened %d times, want 1", calls)
	}
}

// A file replaced over and over by concurrent renames, as koan.json is
// under a burst of writes, is always read whole, in one version or another.
func TestReadFileUnderConcurrentReplacement(t *testing.T) {
	const writers = 4
	r, dir := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("version"), 0o644))
	})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				tmp := filepath.Join(dir, fmt.Sprintf("tmp%d-%d", w, i))
				if err := os.WriteFile(tmp, []byte("version"), 0o644); err != nil {
					t.Error(err)
					return
				}
				if err := os.Rename(tmp, filepath.Join(dir, "1.json")); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for range 5000 {
		got, err := r.ReadFile("1.json")
		if err != nil || string(got) != "version" {
			t.Errorf("read %q, %v", got, err)
			break
		}
	}
	close(stop)
	wg.Wait()
}

// A folder swapped for a symlink just before the open is refused as a file
// is.
func TestReadDirRejectsSwap(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
		must(t, os.Mkdir(filepath.Join(dir, "other"), 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "other", "x"), nil, 0o644))
	})
	calls := 0
	beforeOpen = func(name string) {
		calls++
		if calls == 1 {
			p := filepath.Join(dir, name)
			must(t, os.Remove(p))
			must(t, os.Symlink("other", p))
		}
	}
	t.Cleanup(func() { beforeOpen = nil })
	_, err := r.ReadDir("sub")
	wantErrno(t, err, syscall.ELOOP)
}

// A folder renamed away and replaced by a symlink to itself is caught as a
// file is.
func TestReadDirRejectsSwapToSameDir(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	})
	beforeOpen = func(name string) {
		p := filepath.Join(dir, name)
		must(t, os.Rename(p, filepath.Join(dir, "other")))
		must(t, os.Symlink("other", p))
	}
	t.Cleanup(func() { beforeOpen = nil })
	_, err := r.ReadDir("sub")
	wantErrno(t, err, syscall.ELOOP)
}

// A folder replaced just before the open is listed in its new version.
func TestReadDirFollowsReplacement(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	})
	calls := 0
	beforeOpen = func(name string) {
		calls++
		if calls == 1 {
			tmp := filepath.Join(dir, "new")
			must(t, os.Mkdir(tmp, 0o755))
			must(t, os.WriteFile(filepath.Join(tmp, "y"), nil, 0o644))
			// os.Rename refuses a directory over a directory; rename(2)
			// replaces an empty one on Linux and macOS.
			must(t, syscall.Rename(tmp, filepath.Join(dir, name)))
		}
	}
	t.Cleanup(func() { beforeOpen = nil })
	got, err := r.ReadDir("sub")
	must(t, err)
	if len(got) != 1 || got[0].Name() != "y" {
		t.Errorf("got %v, want [y]", got)
	}
	if calls != 1 {
		t.Errorf("opened %d times, want 1", calls)
	}
}

func TestReadFileDirectory(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	_, err := r.ReadFile("sub")
	wantErrno(t, err, syscall.EISDIR)
}

// A FIFO reads as empty without blocking, with or without a writer holding it
// open.
func TestReadFileFIFODoesNotBlock(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, syscall.Mkfifo(filepath.Join(dir, "1.json"), 0o644))
	})
	readEmpty := func() {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			got, err := r.ReadFile("1.json")
			if err == nil && len(got) != 0 {
				err = fmt.Errorf("got %q", got)
			}
			done <- err
		}()
		select {
		case err := <-done:
			must(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("ReadFile blocked on a FIFO")
		}
	}
	readEmpty()

	// O_RDWR opens a FIFO without waiting for a reader: a writer that never
	// writes.
	w, err := os.OpenFile(filepath.Join(dir, "1.json"), os.O_RDWR, 0)
	must(t, err)
	defer w.Close()
	readEmpty()
}

func TestReadFileMissing(t *testing.T) {
	r, _ := newRoot(t, nil)
	_, err := r.ReadFile("1.json")
	wantErrno(t, err, syscall.ENOENT)
}

func TestCreateTemp(t *testing.T) {
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })

	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	})
	f, name, err := r.CreateTemp("sub")
	must(t, err)
	if !strings.HasPrefix(name, "sub/"+TempPrefix) {
		t.Fatalf("name %q", name)
	}
	_, err = f.Write([]byte("hello"))
	must(t, err)
	must(t, f.Close())

	fi, err := os.Lstat(filepath.Join(dir, name))
	must(t, err)
	if got := fi.Mode(); got != 0o640 {
		t.Errorf("mode %v, want 0640 (0644 under umask 027)", got)
	}
	got, err := r.ReadFile(name)
	must(t, err)
	if string(got) != "hello" {
		t.Errorf("got %q", got)
	}

	_, name2, err := r.CreateTemp("sub")
	must(t, err)
	if name2 == name {
		t.Errorf("two temp files named %q", name)
	}
	_, root, err := r.CreateTemp(".")
	must(t, err)
	if !strings.HasPrefix(root, TempPrefix) {
		t.Errorf("name %q", root)
	}
}

func TestMkdirMode(t *testing.T) {
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })

	r, dir := newRoot(t, nil)
	must(t, r.Mkdir("proj", 0o755))
	fi, err := os.Lstat(filepath.Join(dir, "proj"))
	must(t, err)
	if got := fi.Mode().Perm(); got != 0o750 {
		t.Errorf("mode %v, want 0750", got)
	}
	wantErrno(t, r.Mkdir("proj", 0o755), syscall.EEXIST)
}

func TestLinkNeverClobbers(t *testing.T) {
	r, _ := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "tmp"), []byte("new"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("old"), 0o644))
	})
	err := r.Link("tmp", "1.json")
	wantErrno(t, err, syscall.EEXIST)
	var le *os.LinkError
	if !errors.As(err, &le) {
		t.Errorf("got %T, want *os.LinkError", err)
	}
	got, _ := r.ReadFile("1.json")
	if string(got) != "old" {
		t.Errorf("1.json is %q", got)
	}
	must(t, r.Link("tmp", "2.json"))
	got, _ = r.ReadFile("2.json")
	if string(got) != "new" {
		t.Errorf("2.json is %q", got)
	}
}

func TestRenameReplaces(t *testing.T) {
	r, _ := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "tmp"), []byte("new"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("old"), 0o644))
	})
	must(t, r.Rename("tmp", "1.json"))
	got, _ := r.ReadFile("1.json")
	if string(got) != "new" {
		t.Errorf("1.json is %q", got)
	}
	_, err := r.Lstat("tmp")
	wantErrno(t, err, syscall.ENOENT)
}

func TestOpenRootErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := OS{}.OpenRoot(filepath.Join(dir, "missing"))
	wantErrno(t, err, syscall.ENOENT)
	file := filepath.Join(dir, "file")
	must(t, os.WriteFile(file, nil, 0o644))
	_, err = OS{}.OpenRoot(file)
	wantErrno(t, err, syscall.ENOTDIR)
	var pe *os.PathError
	if !errors.As(err, &pe) || pe.Path != file {
		t.Errorf("got %v, want a *os.PathError on %q", err, file)
	}
	_, err = OS{}.OpenRoot("")
	wantErrno(t, err, syscall.ENOENT)
}

// Name is the path the root was opened with, not the path handed to
// os.OpenRoot.
func TestOpenRootName(t *testing.T) {
	r, dir := newRoot(t, nil)
	if got := r.Name(); got != dir {
		t.Errorf("Name() = %q, want %q", got, dir)
	}
}

// A root reached through a symlink opens the symlink's target.
func TestOpenRootFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "real"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "real", "x"), []byte("x"), 0o644))
	link := filepath.Join(dir, "link")
	must(t, os.Symlink("real", link))
	r, err := OS{}.OpenRoot(link)
	must(t, err)
	defer r.Close()
	got, err := r.ReadFile("x")
	must(t, err)
	if string(got) != "x" {
		t.Errorf("got %q", got)
	}
}

// A FIFO at the root path is refused without being opened, which would block.
func TestOpenRootFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "root")
	must(t, syscall.Mkfifo(fifo, 0o644))
	done := make(chan error, 1)
	go func() {
		_, err := OS{}.OpenRoot(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		wantErrno(t, err, syscall.ENOTDIR)
	case <-time.After(5 * time.Second):
		t.Fatal("OpenRoot blocked on a FIFO")
	}
}

// A FIFO where a folder should be — put there by an outside change after
// the caller looked — is refused with ENOTDIR, never opened, which would
// block with the write lock held.
func TestFolderFIFODoesNotBlock(t *testing.T) {
	r, _ := newRoot(t, func(dir string) {
		must(t, syscall.Mkfifo(filepath.Join(dir, "a"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "x"), nil, 0o644))
	})
	for name, call := range map[string]func() error{
		"ReadFile":                func() error { _, err := r.ReadFile("a/1.json"); return err },
		"ReadDir":                 func() error { _, err := r.ReadDir("a/b"); return err },
		"SyncDir":                 func() error { return r.SyncDir("a") },
		"RenameNoReplace from it": func() error { return r.RenameNoReplace("a/x", "y") },
		"RenameNoReplace into it": func() error { return r.RenameNoReplace("x", "a/y") },
	} {
		done := make(chan error, 1)
		go func() { done <- call() }()
		select {
		case err := <-done:
			if !errors.Is(err, syscall.ENOTDIR) {
				t.Errorf("%s: got %v, want ENOTDIR", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s blocked on a FIFO", name)
		}
	}
}

func TestFSReadFileFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "real.toml"), []byte("x"), 0o644))
	must(t, os.Symlink("real.toml", filepath.Join(dir, "config.toml")))
	got, err := OS{}.ReadFile(filepath.Join(dir, "config.toml"))
	must(t, err)
	if string(got) != "x" {
		t.Errorf("got %q", got)
	}
}

func TestFSStatFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "real"), 0o755))
	must(t, os.Symlink("real", filepath.Join(dir, "link")))
	fi, err := OS{}.Stat(filepath.Join(dir, "link"))
	must(t, err)
	if !fi.IsDir() {
		t.Errorf("mode %v, want a directory", fi.Mode())
	}
	_, err = OS{}.Stat(filepath.Join(dir, "missing"))
	wantErrno(t, err, syscall.ENOENT)
}

func TestFSLstatSeesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Symlink("nowhere", filepath.Join(dir, "link")))
	fi, err := OS{}.Lstat(filepath.Join(dir, "link"))
	must(t, err)
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("mode %v, want a symlink", fi.Mode())
	}
	_, err = OS{}.Stat(filepath.Join(dir, "link"))
	wantErrno(t, err, syscall.ENOENT)
}

// RenameNoReplace moves a file or folder, across folders, but never onto
// anything — not even an empty folder, which rename(2) would replace.
func TestRenameNoReplace(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.MkdirAll(filepath.Join(dir, "a/b"), 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "a/b/1.json"), []byte("x"), 0o644))
		must(t, os.Mkdir(filepath.Join(dir, "empty"), 0o755))
		must(t, os.Mkdir(filepath.Join(dir, "q"), 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "f"), nil, 0o644))
	})
	wantErrno(t, r.RenameNoReplace("a", "empty"), syscall.EEXIST)
	wantErrno(t, r.RenameNoReplace("a", "f"), syscall.EEXIST)
	wantErrno(t, r.RenameNoReplace("a/b/1.json", "f"), syscall.EEXIST)
	wantErrno(t, r.RenameNoReplace("gone", "g"), syscall.ENOENT)
	must(t, r.RenameNoReplace("a/b/1.json", "q/1.json"))
	must(t, r.RenameNoReplace("a", "q/a"))
	if _, err := os.Stat(filepath.Join(dir, "q/a/b")); err != nil {
		t.Error(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "q/1.json")); err != nil || string(b) != "x" {
		t.Errorf("moved file: %q, %v", b, err)
	}
}

// RemoveAll removes a folder with everything under it, and never follows a
// symlink out of it.
func TestRemoveAll(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.MkdirAll(filepath.Join(dir, "a/b"), 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "a/b/1.json"), nil, 0o644))
		must(t, os.Mkdir(filepath.Join(dir, "keep"), 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "keep/x"), nil, 0o644))
		must(t, os.Symlink("../keep", filepath.Join(dir, "a/link")))
	})
	must(t, r.RemoveAll("a"))
	must(t, r.RemoveAll("a"))
	if _, err := os.Lstat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Errorf("a: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep/x")); err != nil {
		t.Errorf("followed a symlink: %v", err)
	}
}
