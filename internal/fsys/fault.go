package fsys

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// Op names, one per call a Hook sees.
const (
	OpOpenRoot   = "openroot"
	OpMkdir      = "mkdir"
	OpMkdirAll   = "mkdirall"
	OpReadFile   = "readfile"
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
	OpLock       = "lock"
	OpUnlock     = "unlock"
	OpCloseRoot  = "closeroot"
)

// Op describes one call.
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

// Hook runs before each call. It is the seam for OS-error, precedence, and
// crash tests. A non-nil error fails the call without making it: a
// syscall.Errno comes back wrapped as the real call's error would be.
type Hook func(Op) error

func before(h Hook, op Op) error {
	if h == nil {
		return nil
	}
	err := h(op)
	if err == nil {
		return nil
	}
	if op.Name == OpLink || op.Name == OpRename || op.Name == OpRenameNR {
		return &os.LinkError{Op: op.Name, Old: op.Path, New: op.NewPath, Err: err}
	}
	return &os.PathError{Op: op.Name, Path: op.Path, Err: err}
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

// Nth runs action at the n-th call (counting from 1) that match accepts;
// every other call passes through.
func Nth(n int, match func(Op) bool, action Hook) Hook {
	var mu sync.Mutex
	seen := 0
	return func(op Op) error {
		if !match(op) {
			return nil
		}
		mu.Lock()
		seen++
		hit := seen == n
		mu.Unlock()
		if hit {
			return action(op)
		}
		return nil
	}
}

// Mutating matches the calls that change the disk.
func Mutating(op Op) bool { return op.Mutating }

// Fail fails a call with errno.
func Fail(errno syscall.Errno) Hook { return func(Op) error { return errno } }

// ErrnoAt fails the n-th call (counting from 1) named name — and, when path
// is not "", made on path — with errno.
func ErrnoAt(name, path string, n int, errno syscall.Errno) Hook {
	return Nth(n, func(op Op) bool { return op.Name == name && (path == "" || op.Path == path) }, Fail(errno))
}

// Crash kills the process with SIGKILL: what a crash leaves behind, with no
// deferred cleanup run.
func Crash(Op) error {
	if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
		panic(fmt.Sprintf("fsys: Crash: kill: %v", err))
	}
	for {
		time.Sleep(time.Hour) // the kill lands; never return
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
