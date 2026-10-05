package fsys

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// OS is the real filesystem.
type OS struct{}

var _ FS = OS{}

// OpenRoot on a path that leads to a non-directory fails with ENOTDIR.
// os.OpenRoot opens the path without O_DIRECTORY and checks its type only
// afterwards, so a FIFO would block the open, and a regular file is reported
// by an error with no errno. It is therefore handed p + "/.": resolving that
// requires p to be a directory, so the kernel refuses a FIFO or regular file
// with ENOTDIR before opening anything, with no window for a swap. Error
// paths are restored to p. An empty path, which would become "/.", fails
// with ENOENT as open(2) fails on it.
func (OS) OpenRoot(p string) (Root, error) {
	if p == "" {
		return nil, &os.PathError{Op: "open", Path: p, Err: syscall.ENOENT}
	}
	r, err := os.OpenRoot(p + "/.")
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			pe.Path = p
		}
		return nil, err
	}
	return &osRoot{r: r, name: p}, nil
}

func (OS) Mkdir(p string, perm fs.FileMode) error    { return os.Mkdir(p, perm) }
func (OS) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
func (OS) ReadFile(p string) ([]byte, error)         { return os.ReadFile(p) }
func (OS) Stat(p string) (fs.FileInfo, error)        { return os.Stat(p) }
func (OS) Lstat(p string) (fs.FileInfo, error)       { return os.Lstat(p) }
func (OS) Rename(oldpath, newpath string) error      { return os.Rename(oldpath, newpath) }
func (OS) Remove(p string) error                     { return os.Remove(p) }

type osRoot struct {
	r    *os.Root
	name string
}

func (r *osRoot) Name() string                              { return r.name }
func (r *osRoot) Lstat(name string) (fs.FileInfo, error)    { return r.r.Lstat(name) }
func (r *osRoot) Mkdir(name string, perm fs.FileMode) error { return r.r.Mkdir(name, perm) }
func (r *osRoot) Link(oldname, newname string) error        { return r.r.Link(oldname, newname) }
func (r *osRoot) Rename(oldname, newname string) error      { return r.r.Rename(oldname, newname) }
func (r *osRoot) Remove(name string) error                  { return r.r.Remove(name) }
func (r *osRoot) RemoveAll(name string) error               { return r.r.RemoveAll(name) }
func (r *osRoot) Close() error                              { return r.r.Close() }

// RenameNoReplace opens both names' folders through the root (openDir) and
// renames between them with the platform's no-replace rename
// (renameNoReplace).
func (r *osRoot) RenameNoReplace(oldname, newname string) error {
	od, err := r.openDir(path.Dir(oldname))
	if err != nil {
		return err
	}
	defer od.Close()
	nd, err := r.openDir(path.Dir(newname))
	if err != nil {
		return err
	}
	defer nd.Close()
	oc, err := od.SyscallConn()
	if err != nil {
		return err
	}
	nc, err := nd.SyscallConn()
	if err != nil {
		return err
	}
	var rerr error
	cerr := oc.Control(func(ofd uintptr) {
		if err := nc.Control(func(nfd uintptr) {
			for {
				rerr = renameNoReplace(int(ofd), path.Base(oldname), int(nfd), path.Base(newname))
				if rerr != unix.EINTR {
					return
				}
			}
		}); err != nil {
			rerr = err
		}
	})
	if cerr != nil {
		return cerr
	}
	if rerr != nil {
		return &os.LinkError{Op: "rename", Old: oldname, New: newname, Err: rerr}
	}
	return nil
}

// ReadFile reads anything that is neither a regular file nor a directory —
// a FIFO, a socket, a device — as empty, without reading it: a FIFO with a
// writer would block on Linux and fail with EAGAIN on macOS, and a device
// may never end.
func (r *osRoot) ReadFile(name string) ([]byte, error) {
	f, fi, err := r.openNoFollow(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if !fi.Mode().IsRegular() && !fi.IsDir() {
		return []byte{}, nil
	}
	return io.ReadAll(f)
}

func (r *osRoot) SyncDir(name string) error {
	d, err := r.openDir(name)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// openDir opens the folder name through the root. Like OpenRoot, it hands
// os.Root name + "/.", which resolves only through a directory: a FIFO or
// file in its place — put there by an outside change since the caller
// looked — fails with ENOTDIR at once, where a plain open of a FIFO would
// block forever, with the write lock held.
func (r *osRoot) openDir(name string) (*os.File, error) {
	return r.r.Open(name + "/.")
}

func (r *osRoot) ReadDir(name string) ([]fs.DirEntry, error) {
	f, _, err := r.openNoFollow(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// beforeOpen, when set by a test, runs just before openNoFollow's openat, so
// the test can swap the entry.
var beforeOpen func(name string)

// openNoFollow opens name for reading without following a symlink in its
// last component: a symlink there fails with ELOOP. os.Root ignores
// O_NOFOLLOW — it adds the flag itself and, on ELOOP, follows the symlink
// when its target stays inside the root — so name's folder is opened through
// the root and name itself with openat(2) and O_NOFOLLOW, from
// golang.org/x/sys/unix, which has openat on Linux and macOS alike. The one
// call decides: there is no window between a check and the open, so a file
// replaced by a concurrent write's rename is simply read in one version or
// the other.
//
// O_NONBLOCK keeps a FIFO from blocking the open, and openDir one in the
// folder's place. The returned FileInfo is the opened file's.
func (r *osRoot) openNoFollow(name string) (*os.File, fs.FileInfo, error) {
	d, err := r.openDir(path.Dir(name))
	if err != nil {
		return nil, nil, err
	}
	defer d.Close()
	c, err := d.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	if beforeOpen != nil {
		beforeOpen(name)
	}
	fd, oerr := -1, error(nil)
	if err := c.Control(func(dfd uintptr) {
		for {
			fd, oerr = unix.Openat(int(dfd), path.Base(name), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if oerr != unix.EINTR {
				return
			}
		}
	}); err != nil {
		return nil, nil, err
	}
	if oerr != nil {
		return nil, nil, &os.PathError{Op: "openat", Path: name, Err: oerr}
	}
	f := os.NewFile(uintptr(fd), name)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}

// TempPrefix begins the name of every temp file koan creates.
const TempPrefix = ".koan-tmp-"

// LegacyTempPrefix begins the temp files ftask, koan's former name, created.
// One left by an interrupted ftask write counts as koan's own.
const LegacyTempPrefix = ".ftask-tmp-"

// IsTemp reports whether name is a temp file's or folder's, koan's or
// ftask's.
func IsTemp(name string) bool {
	return strings.HasPrefix(name, TempPrefix) || strings.HasPrefix(name, LegacyTempPrefix)
}

// TempName is a fresh temp name in dir: hidden, recognizably koan's, and
// random, so two writes never collide. For a temp that is not created by
// CreateTemp — a hard link, or a folder renamed aside.
func TempName(dir string) string {
	var b [12]byte
	rand.Read(b[:])
	return path.Join(dir, TempPrefix+hex.EncodeToString(b[:]))
}

func (r *osRoot) CreateTemp(dir string) (File, string, error) {
	name := TempName(dir)
	f, err := r.r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, "", err
	}
	return f, name, nil
}
