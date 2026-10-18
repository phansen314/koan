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
func newRoot(t *testing.T, setup func(dir string)) (*Root, string) {
	t.Helper()
	dir := t.TempDir()
	if setup != nil {
		setup(dir)
	}
	r, err := FS{}.OpenRoot(dir)
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

func TestLstatReportsSymlink(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	fi, err := r.Lstat("link")
	must(t, err)
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("mode %v, want a symlink", fi.Mode())
	}
}

// A file replaced over and over by concurrent renames, as @koan.json is
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
	f, name, err := r.CreateTemp("sub", 0o644)
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

	_, name2, err := r.CreateTemp("sub", 0o644)
	must(t, err)
	if name2 == name {
		t.Errorf("two temp files named %q", name)
	}
	_, root, err := r.CreateTemp(".", 0o644)
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
	_, err := FS{}.OpenRoot(filepath.Join(dir, "missing"))
	wantErrno(t, err, syscall.ENOENT)
	file := filepath.Join(dir, "file")
	must(t, os.WriteFile(file, nil, 0o644))
	_, err = FS{}.OpenRoot(file)
	wantErrno(t, err, syscall.ENOTDIR)
	var pe *os.PathError
	if !errors.As(err, &pe) || pe.Path != file {
		t.Errorf("got %v, want a *os.PathError on %q", err, file)
	}
	_, err = FS{}.OpenRoot("")
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
	r, err := FS{}.OpenRoot(link)
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
		_, err := FS{}.OpenRoot(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		wantErrno(t, err, syscall.ENOTDIR)
	case <-time.After(5 * time.Second):
		t.Fatal("OpenRoot blocked on a FIFO")
	}
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
