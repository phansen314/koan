package fsys

import (
	"io"
	"io/fs"
)

// FS opens roots, and makes the few calls that happen outside one: init's
// creation of the root and the config directory, reading the config, and
// checking what the root path leads to; and pick's reads of notes files.
type FS interface {
	// OpenRoot opens the directory at path, following symlinks. Every later
	// call through the Root is relative to this one resolution.
	OpenRoot(path string) (Root, error)
	// Mkdir creates one directory; init never creates the root's parent.
	Mkdir(path string, perm fs.FileMode) error
	// MkdirAll creates the config directory and any missing parents.
	MkdirAll(path string, perm fs.FileMode) error
	// ReadFile reads a file outside any root, following symlinks: the config
	// (often a symlink into a dotfiles checkout), and pick's notes files.
	ReadFile(path string) ([]byte, error)
	// Stat follows symlinks: whether the root path leads to a directory
	// (init's check of an existing root), and whether the config is a
	// regular file, which must be known before reading it.
	Stat(path string) (fs.FileInfo, error)
	// Lstat does not follow a final symlink: whether anything is at the root
	// path at all, so init can tell a dangling symlink from nothing, and
	// whether a config exists.
	Lstat(path string) (fs.FileInfo, error)
	// Rename moves the config ftask left into koan's config directory
	// (store.Migrate), which runs only while koan's config is missing.
	Rename(oldpath, newpath string) error
	// Remove removes ftask's config directory once emptied; a non-empty one
	// fails, and is left.
	Remove(path string) error
}

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
type Root interface {
	// Name is the path the root was opened with.
	Name() string
	Lstat(name string) (fs.FileInfo, error)
	// ReadFile reads a whole file. A directory fails with EISDIR; a FIFO,
	// socket, or device reads as empty.
	ReadFile(name string) ([]byte, error)
	// ReadDir lists a directory in the order the OS gives; callers sort.
	ReadDir(name string) ([]fs.DirEntry, error)
	Mkdir(name string, perm fs.FileMode) error
	// CreateTemp creates a new, empty temp file in dir, exclusively, with
	// mode 0644 before the umask. Its name, .koan-tmp-<random>, is hidden
	// from reads and recognizable to doctor. The returned name includes dir.
	CreateTemp(dir string) (f File, name string, err error)
	// Link publishes a new file: it fails with EEXIST rather than replace one.
	Link(oldname, newname string) error
	// Rename publishes a file over an existing one.
	Rename(oldname, newname string) error
	// SyncDir flushes a directory's entries to disk (fsync), so a file just
	// published in it survives a system crash.
	SyncDir(name string) error
	// RenameNoReplace moves a file or folder to a name that must be free: it
	// fails with EEXIST rather than replace anything there — even an empty
	// folder, which rename(2) would silently replace. The check and the move
	// are one call (renameat2 RENAME_NOREPLACE on Linux, renameatx_np
	// RENAME_EXCL on macOS).
	RenameNoReplace(oldname, newname string) error
	Remove(name string) error
	// RemoveAll removes a folder and everything under it, never following a
	// symlink; a name that is already gone is not an error.
	RemoveAll(name string) error
	// Lock takes the write lock: flock(LOCK_EX|LOCK_NB) on the root
	// directory itself. A held lock fails with EAGAIN; EINTR is retried.
	Lock() (Lock, error)
	Close() error
}

// File is a temp file being written.
type File interface {
	io.Writer
	// Sync flushes what was written to disk (fsync).
	Sync() error
	Close() error
}

// Lock is a held write lock. It keeps the locked descriptor referenced until
// Unlock, which closes it and so releases the lock.
type Lock interface {
	Unlock() error
}
