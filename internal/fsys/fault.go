package fsys

import (
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"time"
)

// Op names, one per call a Fault can intercept.
const (
	OpOpenRoot   = "openroot"
	OpMkdir      = "mkdir"
	OpMkdirAll   = "mkdirall"
	OpReadFile   = "readfile"
	OpStat       = "stat"
	OpLstat      = "lstat"
	OpReadDir    = "readdir"
	OpCreateTemp = "createtemp"
	OpWrite      = "write"
	OpSyncFile   = "syncfile"
	OpCloseFile  = "closefile"
	OpLink       = "link"
	OpRename     = "rename"
	OpRenameNR   = "rename-noreplace"
	OpSyncDir    = "syncdir"
	OpRemove     = "remove"
	OpRemoveAll  = "removeall"
	OpLock       = "lock"
	OpUnlock     = "unlock"
	OpCloseRoot  = "closeroot"
)

// Op describes one intercepted call.
type Op struct {
	// Root is the Name of the root the call is made through; "" for calls on
	// the FS itself.
	Root string
	Name string
	// Path is the call's path; for Link and Rename, the old name.
	Path string
	// NewPath is Link's and Rename's new name.
	NewPath string
	// Mutating is true for calls that change the disk — the steps crash
	// injection kills the process before.
	Mutating bool
}

// Hook runs before each call. A non-nil error fails the call without making
// it: a syscall.Errno comes back wrapped as the real call's error would be.
type Hook func(Op) error

// Fault wraps an FS and runs Hook before every call, through every Root and
// File it opens. It is the seam for OS-error, precedence, and crash tests. A
// nil Hook passes every call through.
type Fault struct {
	FS   FS
	Hook Hook
}

var _ FS = Fault{}

func (f Fault) before(op Op) error {
	if f.Hook == nil {
		return nil
	}
	err := f.Hook(op)
	if err == nil {
		return nil
	}
	if op.Name == OpLink || op.Name == OpRename || op.Name == OpRenameNR {
		return &os.LinkError{Op: op.Name, Old: op.Path, New: op.NewPath, Err: err}
	}
	return &os.PathError{Op: op.Name, Path: op.Path, Err: err}
}

func (f Fault) OpenRoot(p string) (Root, error) {
	if err := f.before(Op{Name: OpOpenRoot, Path: p}); err != nil {
		return nil, err
	}
	r, err := f.FS.OpenRoot(p)
	if err != nil {
		return nil, err
	}
	return &faultRoot{f: f, r: r}, nil
}

func (f Fault) Mkdir(p string, perm fs.FileMode) error {
	if err := f.before(Op{Name: OpMkdir, Path: p, Mutating: true}); err != nil {
		return err
	}
	return f.FS.Mkdir(p, perm)
}

func (f Fault) MkdirAll(p string, perm fs.FileMode) error {
	if err := f.before(Op{Name: OpMkdirAll, Path: p, Mutating: true}); err != nil {
		return err
	}
	return f.FS.MkdirAll(p, perm)
}

func (f Fault) ReadFile(p string) ([]byte, error) {
	if err := f.before(Op{Name: OpReadFile, Path: p}); err != nil {
		return nil, err
	}
	return f.FS.ReadFile(p)
}

func (f Fault) Stat(p string) (fs.FileInfo, error) {
	if err := f.before(Op{Name: OpStat, Path: p}); err != nil {
		return nil, err
	}
	return f.FS.Stat(p)
}

func (f Fault) Lstat(p string) (fs.FileInfo, error) {
	if err := f.before(Op{Name: OpLstat, Path: p}); err != nil {
		return nil, err
	}
	return f.FS.Lstat(p)
}

func (f Fault) Rename(oldpath, newpath string) error {
	if err := f.before(Op{Name: OpRename, Path: oldpath, NewPath: newpath, Mutating: true}); err != nil {
		return err
	}
	return f.FS.Rename(oldpath, newpath)
}

func (f Fault) Remove(p string) error {
	if err := f.before(Op{Name: OpRemove, Path: p, Mutating: true}); err != nil {
		return err
	}
	return f.FS.Remove(p)
}

type faultRoot struct {
	f Fault
	r Root
}

func (r *faultRoot) before(op Op) error {
	op.Root = r.r.Name()
	return r.f.before(op)
}

func (r *faultRoot) Name() string { return r.r.Name() }

func (r *faultRoot) Lstat(name string) (fs.FileInfo, error) {
	if err := r.before(Op{Name: OpLstat, Path: name}); err != nil {
		return nil, err
	}
	return r.r.Lstat(name)
}

func (r *faultRoot) ReadFile(name string) ([]byte, error) {
	if err := r.before(Op{Name: OpReadFile, Path: name}); err != nil {
		return nil, err
	}
	return r.r.ReadFile(name)
}

func (r *faultRoot) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := r.before(Op{Name: OpReadDir, Path: name}); err != nil {
		return nil, err
	}
	return r.r.ReadDir(name)
}

func (r *faultRoot) Mkdir(name string, perm fs.FileMode) error {
	if err := r.before(Op{Name: OpMkdir, Path: name, Mutating: true}); err != nil {
		return err
	}
	return r.r.Mkdir(name, perm)
}

func (r *faultRoot) CreateTemp(dir string) (File, string, error) {
	if err := r.before(Op{Name: OpCreateTemp, Path: dir, Mutating: true}); err != nil {
		return nil, "", err
	}
	f, name, err := r.r.CreateTemp(dir)
	if err != nil {
		return nil, "", err
	}
	return &faultFile{r: r, f: f, name: name}, name, nil
}

func (r *faultRoot) Link(oldname, newname string) error {
	if err := r.before(Op{Name: OpLink, Path: oldname, NewPath: newname, Mutating: true}); err != nil {
		return err
	}
	return r.r.Link(oldname, newname)
}

func (r *faultRoot) Rename(oldname, newname string) error {
	if err := r.before(Op{Name: OpRename, Path: oldname, NewPath: newname, Mutating: true}); err != nil {
		return err
	}
	return r.r.Rename(oldname, newname)
}

func (r *faultRoot) SyncDir(name string) error {
	if err := r.before(Op{Name: OpSyncDir, Path: name}); err != nil {
		return err
	}
	return r.r.SyncDir(name)
}

func (r *faultRoot) RenameNoReplace(oldname, newname string) error {
	if err := r.before(Op{Name: OpRenameNR, Path: oldname, NewPath: newname, Mutating: true}); err != nil {
		return err
	}
	return r.r.RenameNoReplace(oldname, newname)
}

func (r *faultRoot) RemoveAll(name string) error {
	if err := r.before(Op{Name: OpRemoveAll, Path: name, Mutating: true}); err != nil {
		return err
	}
	return r.r.RemoveAll(name)
}

func (r *faultRoot) Remove(name string) error {
	if err := r.before(Op{Name: OpRemove, Path: name, Mutating: true}); err != nil {
		return err
	}
	return r.r.Remove(name)
}

func (r *faultRoot) Lock() (Lock, error) {
	if err := r.before(Op{Name: OpLock, Path: "."}); err != nil {
		return nil, err
	}
	l, err := r.r.Lock()
	if err != nil {
		return nil, err
	}
	return &faultLock{r: r, l: l}, nil
}

// Close closes the real root even when a fault is injected.
func (r *faultRoot) Close() error {
	err := r.before(Op{Name: OpCloseRoot, Path: "."})
	cerr := r.r.Close()
	if err != nil {
		return err
	}
	return cerr
}

type faultFile struct {
	r    *faultRoot
	f    File
	name string
}

func (f *faultFile) Write(p []byte) (int, error) {
	if err := f.r.before(Op{Name: OpWrite, Path: f.name, Mutating: true}); err != nil {
		return 0, err
	}
	return f.f.Write(p)
}

func (f *faultFile) Sync() error {
	if err := f.r.before(Op{Name: OpSyncFile, Path: f.name}); err != nil {
		return err
	}
	return f.f.Sync()
}

// Close closes the real file even when a fault is injected, so a test never
// leaks the descriptor.
func (f *faultFile) Close() error {
	err := f.r.before(Op{Name: OpCloseFile, Path: f.name})
	cerr := f.f.Close()
	if err != nil {
		return err
	}
	return cerr
}

type faultLock struct {
	r *faultRoot
	l Lock
}

// Unlock releases the real lock even when a fault is injected.
func (l *faultLock) Unlock() error {
	err := l.r.before(Op{Name: OpUnlock, Path: "."})
	uerr := l.l.Unlock()
	if err != nil {
		return err
	}
	return uerr
}

// Hooks run in order; the first to return an error fails the call.
func Hooks(hs ...Hook) Hook {
	return func(op Op) error {
		for _, h := range hs {
			if err := h(op); err != nil {
				return err
			}
		}
		return nil
	}
}

// ErrnoAt fails the n-th call (counting from 1) named name — and, when path
// is not "", made on path — with errno. Other calls pass through.
func ErrnoAt(name, path string, n int, errno syscall.Errno) Hook {
	var mu sync.Mutex
	seen := 0
	return func(op Op) error {
		if op.Name != name || (path != "" && op.Path != path) {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		seen++
		if seen == n {
			return errno
		}
		return nil
	}
}

// ErrnoAtMutating fails the k-th mutating call (counting from 1) with errno:
// the calls CrashBefore counts, so an error midway can be injected at each
// step a crash can.
func ErrnoAtMutating(k int, errno syscall.Errno) Hook {
	var mu sync.Mutex
	seen := 0
	return func(op Op) error {
		if !op.Mutating {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		seen++
		if seen == k {
			return errno
		}
		return nil
	}
}

// CrashBefore kills the process with SIGKILL just before the k-th mutating
// call (counting from 1): what a crash leaves behind, with no deferred
// cleanup run.
func CrashBefore(k int) Hook {
	var mu sync.Mutex
	seen := 0
	return func(op Op) error {
		if !op.Mutating {
			return nil
		}
		mu.Lock()
		seen++
		crash := seen == k
		mu.Unlock()
		if crash {
			if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
				panic(fmt.Sprintf("fsys: CrashBefore: kill: %v", err))
			}
			for {
				time.Sleep(time.Hour) // the kill lands; never return
			}
		}
		return nil
	}
}

// Record appends every call to *ops, for tests that count steps.
func Record(ops *[]Op) Hook {
	var mu sync.Mutex
	return func(op Op) error {
		mu.Lock()
		*ops = append(*ops, op)
		mu.Unlock()
		return nil
	}
}
