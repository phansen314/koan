package fsys

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// FS opens roots, and makes the two calls that happen outside one: creating
// the root itself, and the directory the state file is in. Its Hook, when
// set, runs before every call, through every Root, File, and Lock it opens
// (fault.go); the zero FS is the real filesystem.
type FS struct {
	Hook Hook
}

// OpenRoot opens the directory at p, following symlinks. Every later call
// through the Root is relative to this one resolution.
//
// A path that leads to a non-directory fails with ENOTDIR. os.OpenRoot opens
// the path without O_DIRECTORY and checks its type only afterwards, so a FIFO
// would block the open, and a regular file is reported by an error with no
// errno. It is therefore handed p + "/.": resolving that requires p to be a
// directory, so the kernel refuses a FIFO or regular file with ENOTDIR before
// opening anything, with no window for a swap. Error paths are restored to p.
// An empty path, which would become "/.", fails with ENOENT as open(2) fails
// on it.
func (f FS) OpenRoot(p string) (*Root, error) {
	if err := f.before(Op{Name: OpOpenRoot, Path: p}); err != nil {
		return nil, err
	}
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
	return &Root{r: r, name: p, hook: f.Hook}, nil
}

// Mkdir creates one directory: the root. Its parent is never created.
func (f FS) Mkdir(p string, perm fs.FileMode) error {
	if err := f.before(Op{Name: OpMkdir, Path: p, Mutating: true}); err != nil {
		return err
	}
	return os.Mkdir(p, perm)
}

// MkdirAll creates the state file's directory and any missing parents.
func (f FS) MkdirAll(p string, perm fs.FileMode) error {
	if err := f.before(Op{Name: OpMkdirAll, Path: p, Mutating: true}); err != nil {
		return err
	}
	return os.MkdirAll(p, perm)
}

// EvalSymlinks is p with every symlink in it resolved: where the root really
// is, for telling whether a path is in it. It only reads, and is not hooked.
func (FS) EvalSymlinks(p string) (string, error) { return filepath.EvalSymlinks(p) }

func (f FS) before(op Op) error { return before(f.Hook, op) }

// Root is a directory opened once, through which every call is made. Names
// are slash-separated and relative to the root; "." is the root itself.
//
// No call follows a symlink in a name's last component: ReadFile and ReadDir
// fail with ELOOP on one, and Lstat reports it. Symlinks in earlier
// components that stay inside the root are followed, so callers that must
// not follow them (the path walk) Lstat each component in turn.
//
// A name that escapes the root — through "..", or a symlink in an earlier
// component that leads outside — fails with os.Root's error, which holds no
// errno, so store reports it as internal. Only a caller bug or an outside
// change mid-operation reaches it: the path walk sees such a symlink first.
type Root struct {
	r    *os.Root
	name string
	hook Hook
}

func (r *Root) before(op Op) error {
	op.Root = r.name
	return before(r.hook, op)
}

// Name is the path the root was opened with.
func (r *Root) Name() string { return r.name }

func (r *Root) Lstat(name string) (fs.FileInfo, error) {
	if err := r.before(Op{Name: OpLstat, Path: name}); err != nil {
		return nil, err
	}
	return r.r.Lstat(name)
}

// ReadFile reads a whole file. A directory fails with EISDIR. Anything that
// is neither a regular file nor a directory — a FIFO, a socket, a device —
// reads as empty, without being read: a FIFO with a writer would block on
// Linux and fail with EAGAIN on macOS, and a device may never end.
func (r *Root) ReadFile(name string) ([]byte, error) {
	if err := r.before(Op{Name: OpReadFile, Path: name}); err != nil {
		return nil, err
	}
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

// ReadDir lists a directory in the order the OS gives; callers sort.
func (r *Root) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := r.before(Op{Name: OpReadDir, Path: name}); err != nil {
		return nil, err
	}
	f, _, err := r.openNoFollow(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

func (r *Root) Mkdir(name string, perm fs.FileMode) error {
	if err := r.before(Op{Name: OpMkdir, Path: name, Mutating: true}); err != nil {
		return err
	}
	return r.r.Mkdir(name, perm)
}

// TempPrefix begins the name of every temp file koan creates.
const TempPrefix = ".koan-tmp-"

// IsTemp reports whether name is a temp file's or folder's.
func IsTemp(name string) bool { return strings.HasPrefix(name, TempPrefix) }

// TempName is a fresh temp name in dir: hidden, recognizably koan's, and
// random, so two writes never collide. For a temp that is not created by
// CreateTemp — a hard link, or a folder renamed aside.
func TempName(dir string) string {
	var b [12]byte
	rand.Read(b[:])
	return path.Join(dir, TempPrefix+hex.EncodeToString(b[:]))
}

// CreateTemp creates a new, empty temp file in dir, exclusively, with mode
// perm before the umask. Its name, .koan-tmp-<random>, is hidden from reads
// and recognizable to doctor. The returned name includes dir.
func (r *Root) CreateTemp(dir string, perm fs.FileMode) (f *File, name string, err error) {
	if err := r.before(Op{Name: OpCreateTemp, Path: dir, Mutating: true}); err != nil {
		return nil, "", err
	}
	name = TempName(dir)
	of, err := r.r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return nil, "", err
	}
	return &File{f: of, r: r, name: name}, name, nil
}

// Link publishes a new file: it fails with EEXIST rather than replace one.
func (r *Root) Link(oldname, newname string) error {
	if err := r.before(Op{Name: OpLink, Path: oldname, NewPath: newname, Mutating: true}); err != nil {
		return err
	}
	return r.r.Link(oldname, newname)
}

// Rename publishes a file over an existing one.
func (r *Root) Rename(oldname, newname string) error {
	if err := r.before(Op{Name: OpRename, Path: oldname, NewPath: newname, Mutating: true}); err != nil {
		return err
	}
	return r.r.Rename(oldname, newname)
}

// SyncDir flushes a directory's entries to disk (fsync), so a file just
// published in it survives a system crash.
func (r *Root) SyncDir(name string) error {
	if err := r.before(Op{Name: OpSyncDir, Path: name}); err != nil {
		return err
	}
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

// RenameNoReplace moves a file or folder to a name that must be free: it
// fails with EEXIST rather than replace anything there — even an empty
// folder, which rename(2) would silently replace. The check and the move are
// one call (renameat2 RENAME_NOREPLACE on Linux, renameatx_np RENAME_EXCL on
// macOS), between both names' folders opened through the root.
func (r *Root) RenameNoReplace(oldname, newname string) error {
	if err := r.before(Op{Name: OpRenameNR, Path: oldname, NewPath: newname, Mutating: true}); err != nil {
		return err
	}
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

func (r *Root) Remove(name string) error {
	if err := r.before(Op{Name: OpRemove, Path: name, Mutating: true}); err != nil {
		return err
	}
	return r.r.Remove(name)
}

// RemoveAll removes a folder and everything under it, never following a
// symlink; a name that is already gone is not an error.
func (r *Root) RemoveAll(name string) error {
	if err := r.before(Op{Name: OpRemoveAll, Path: name, Mutating: true}); err != nil {
		return err
	}
	return r.r.RemoveAll(name)
}

// Close closes the root even when a hook fails it.
func (r *Root) Close() error {
	err := r.before(Op{Name: OpCloseRoot, Path: "."})
	if cerr := r.r.Close(); err == nil {
		err = cerr
	}
	return err
}

// openDir opens the folder name through the root. Like OpenRoot, it hands
// os.Root name + "/.", which resolves only through a directory: a FIFO or
// file in its place — put there by an outside change since the caller
// looked — fails with ENOTDIR at once, where a plain open of a FIFO would
// block forever, with the write lock held.
func (r *Root) openDir(name string) (*os.File, error) {
	return r.r.Open(name + "/.")
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
func (r *Root) openNoFollow(name string) (*os.File, fs.FileInfo, error) {
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

// File is a temp file being written.
type File struct {
	f    *os.File
	r    *Root
	name string
}

func (f *File) Write(p []byte) (int, error) {
	if err := f.r.before(Op{Name: OpWrite, Path: f.name, Mutating: true}); err != nil {
		return 0, err
	}
	return f.f.Write(p)
}

// Sync flushes what was written to disk (fsync).
func (f *File) Sync() error {
	if err := f.r.before(Op{Name: OpSyncFile, Path: f.name}); err != nil {
		return err
	}
	return f.f.Sync()
}

// Close closes the file even when a hook fails it, so a test never leaks
// the descriptor.
func (f *File) Close() error {
	err := f.r.before(Op{Name: OpCloseFile, Path: f.name})
	if cerr := f.f.Close(); err == nil {
		err = cerr
	}
	return err
}
