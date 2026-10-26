package doctor

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// Copied is what a backup copied, and what doctor found in the copy.
type Copied struct {
	Files    int64
	Bytes    int64
	Recopied bool
	Clean    bool
	Findings []Finding
}

// Hook, when a test sets it, is called each time backup copies the tree
// and the archive: at "copy", once the database is copied, before them,
// and at "copied", after them, before doctor runs. The first time, the
// lock is not held. Nothing but tests sets it.
var Hook func(stage string)

func hook(stage string) {
	if Hook != nil {
		Hook(stage)
	}
}

// Backup copies a root into dir, an empty or new directory: the database
// as one snapshot under the write lock, then the tree and the archive as
// files, without it. It runs doctor on the copy and on the live root;
// when the copy has findings the live root doesn't, a write landed
// mid-copy, and it copies once more, holding the lock throughout. The
// copy is clean when it then has no findings the live root lacks.
func Backup(s *store.Store, dir string) (Copied, *cli.Error) {
	to := store.Root{
		Path:     dir,
		Tree:     filepath.Join(dir, store.TreeName),
		Archive:  filepath.Join(dir, store.ArchiveName),
		Database: filepath.Join(dir, store.DatabaseName),
		Dir:      dir,
	}
	var out Copied
	e := s.Hold(func(h *store.Held) *cli.Error {
		if e := prepare(dir); e != nil {
			return e
		}
		return h.VacuumInto(to.Database)
	})
	if e != nil {
		return out, e
	}
	copied, live, e := copyRest(s, to, &out)
	if e != nil {
		return out, e
	}
	if len(only(copied, to, live, s.Root)) > 0 {
		out.Recopied = true
		e = s.Hold(func(h *store.Held) *cli.Error {
			if err := empty(dir); err != nil {
				return cli.IO("could not clear "+dir+" to copy again: "+err.Error(), dir)
			}
			if e := h.VacuumInto(to.Database); e != nil {
				return e
			}
			copied, live, e = copyRest(s, to, &out)
			return e
		})
		if e != nil {
			return out, e
		}
	}
	out.Clean = len(only(copied, to, live, s.Root)) == 0
	out.Findings = copied
	return out, nil
}

// prepare makes dir, with any parents it lacks, or finds it empty. One
// holding anything, or that isn't a directory, is taken.
func prepare(dir string) *cli.Error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return cli.IO("could not make "+dir+": "+err.Error(), dir)
		}
		return nil
	case errors.Is(err, syscall.ENOTDIR) || err == nil && len(entries) > 0:
		e := cli.Conflict(cli.RuleExists, dir+" is taken: a backup goes into an empty or new directory")
		e.Paths = []string{dir}
		e.Problems = []cli.Problem{{Field: cli.Pointer("dir"), Reason: e.Message}}
		return e
	case err != nil:
		return cli.IO("could not list "+dir+": "+err.Error(), dir)
	}
	return nil
}

// copyRest copies the tree and the archive into the copy, whose database
// is there, counting every regular file in it, then runs doctor on the
// copy and on the live root.
func copyRest(s *store.Store, to store.Root, out *Copied) (copied, live []Finding, e *cli.Error) {
	fi, err := os.Lstat(filepath.Join(to.Dir, store.DatabaseName))
	if err != nil {
		return nil, nil, cli.IO("could not read the copy of the database: "+err.Error(), to.Database)
	}
	out.Files, out.Bytes = 1, fi.Size()
	hook("copy")
	for _, name := range []string{store.TreeName, store.ArchiveName} {
		src, dst := filepath.Join(s.Root.Dir, name), filepath.Join(to.Dir, name)
		err := copyDir(src, dst, out)
		if errors.Is(err, fs.ErrNotExist) {
			err = os.Mkdir(dst, 0o755)
		}
		if err != nil {
			return nil, nil, cli.IO("could not copy "+filepath.Join(s.Root.Path, name)+": "+err.Error(), dst)
		}
	}
	hook("copied")
	c, _, e := fsstep.OpenDatabase(to)
	if e != nil {
		return nil, nil, e
	}
	copied, e = Check(c)
	c.Close()
	if e != nil {
		return nil, nil, e
	}
	live, e = Check(s)
	return copied, live, e
}

// only is the findings of a root that another lacks.
func only(found []Finding, root store.Root, others []Finding, otherRoot store.Root) []Finding {
	var out []Finding
	for _, f := range found {
		if !slices.ContainsFunc(others, func(o Finding) bool { return same(f, root.Path, o, otherRoot.Path) }) {
			out = append(out, f)
		}
	}
	return out
}

// copyDir copies a directory as files, never hard-linked: directories,
// regular files and symlinks, each with its permissions, and files with
// their times. An entry that is gone by the time it is copied was moved
// or removed by a write, and is left out; doctor finds what that
// changed.
func copyDir(src, dst string, out *Copied) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	for _, e := range entries {
		from, to := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		var err error
		switch {
		case e.IsDir():
			err = copyDir(from, to, out)
		case e.Type()&fs.ModeSymlink != 0:
			var target string
			if target, err = os.Readlink(from); err == nil {
				err = os.Symlink(target, to)
			}
		case e.Type().IsRegular():
			var n int64
			if n, err = copyFile(from, to); err == nil {
				out.Files++
				out.Bytes += n
			}
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	mode := fs.FileMode(0o755)
	if fi, err := os.Lstat(src); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := sync(dst); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// copyFile copies a regular file, and gives its size.
func copyFile(from, to string) (int64, error) {
	in, err := os.Open(from)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return 0, err
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(to, fi.Mode().Perm())
	}
	if err == nil {
		err = os.Chtimes(to, fi.ModTime(), fi.ModTime())
	}
	return n, err
}

// sync makes a directory's entries durable.
func sync(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// empty removes everything in dir, which backup made, so it can copy
// again. A directory copied read-only is made writable first.
func empty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if err := os.Chmod(p, 0o700); err != nil {
				return err
			}
			if err := empty(p); err != nil {
				return err
			}
		}
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}
