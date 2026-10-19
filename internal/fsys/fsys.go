package fsys

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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

// Lstat reports on the entry at p, outside any root, without following a
// symlink at its end: the look for a marker, which may cross any directory.
func (f FS) Lstat(p string) (fs.FileInfo, error) {
	if err := f.before(Op{Name: OpLstat, Path: p}); err != nil {
		return nil, err
	}
	return os.Lstat(p)
}

// EvalSymlinks is p with every symlink in it resolved: where the root really
// is, for telling whether a path is in it. It only reads, and is not hooked.
func (FS) EvalSymlinks(p string) (string, error) { return filepath.EvalSymlinks(p) }

func (f FS) before(op Op) error { return before(f.Hook, op) }

// Root is a directory opened once, through which every call is made. Names
// are slash-separated and relative to the root; "." is the root itself.
//
// ReadFile fails with ELOOP on a symlink, and Lstat reports one; otherwise
// os.Root's own calls follow symlinks that stay inside the root. Callers
// that must not follow them check an entry's type first: the walk by the
// type ReadDir gives, and the path walk by Lstat on each component.
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

// ReadFile reads a whole file. Its type is checked first, by Lstat: a
// symlink fails with ELOOP, and anything else that is neither a regular file
// nor a directory — a FIFO, a socket, a device — reads as empty, without
// being opened, since a FIFO would block. A directory fails with EISDIR. An
// entry replaced between the check and the read, which only an outside
// change can do, is read as it then is.
func (r *Root) ReadFile(name string) ([]byte, error) {
	if err := r.before(Op{Name: OpReadFile, Path: name}); err != nil {
		return nil, err
	}
	fi, err := r.r.Lstat(name)
	switch {
	case err != nil:
		return nil, err
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, &os.PathError{Op: "open", Path: name, Err: syscall.ELOOP}
	case !fi.Mode().IsRegular() && !fi.IsDir():
		return []byte{}, nil
	}
	return r.r.ReadFile(name)
}

// ReadDir lists a directory in the order the OS gives; callers sort.
func (r *Root) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := r.before(Op{Name: OpReadDir, Path: name}); err != nil {
		return nil, err
	}
	f, err := r.r.Open(name)
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

// IsTemp reports whether name is a temp file's.
func IsTemp(name string) bool { return strings.HasPrefix(name, TempPrefix) }

// CreateTemp creates a new, empty temp file in dir, exclusively, with mode
// perm before the umask. Its name, .koan-tmp-<random>, is hidden from reads
// and recognizable to doctor, and random, so two writes never collide. The
// returned name includes dir.
func (r *Root) CreateTemp(dir string, perm fs.FileMode) (f *File, name string, err error) {
	if err := r.before(Op{Name: OpCreateTemp, Path: dir, Mutating: true}); err != nil {
		return nil, "", err
	}
	var b [12]byte
	rand.Read(b[:])
	name = path.Join(dir, TempPrefix+hex.EncodeToString(b[:]))
	of, err := r.r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return nil, "", err
	}
	return &File{f: of, r: r, name: name}, name, nil
}

// Create creates a new, empty file at name, exclusively: it fails with
// EEXIST rather than open one that is there. It is for a file written in
// place, which a reader may see before it is whole: a marker's .gitignore,
// which has nothing to publish over.
func (r *Root) Create(name string, perm fs.FileMode) (*File, error) {
	if err := r.before(Op{Name: OpCreate, Path: name, Mutating: true}); err != nil {
		return nil, err
	}
	of, err := r.r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return nil, err
	}
	return &File{f: of, r: r, name: name}, nil
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
	d, err := r.r.Open(name)
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
	od, err := r.r.Open(path.Dir(oldname))
	if err != nil {
		return err
	}
	defer od.Close()
	nd, err := r.r.Open(path.Dir(newname))
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

// Close closes the root even when a hook fails it.
func (r *Root) Close() error {
	err := r.before(Op{Name: OpCloseRoot, Path: "."})
	if cerr := r.r.Close(); err == nil {
		err = cerr
	}
	return err
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
