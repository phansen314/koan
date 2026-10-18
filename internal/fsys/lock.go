package fsys

import (
	"os"
	"syscall"
)

// flock is syscall.Flock; a test replaces it to inject EINTR.
var flock = syscall.Flock

// Lock takes the tree's lock, a flock on the root directory itself, without
// waiting: exclusive (LOCK_EX) for a write, or shared (LOCK_SH) for a read,
// which other readers may hold with it. A lock that is held the other way
// fails with EAGAIN; EINTR is retried.
func (r *Root) Lock(shared bool) (*Lock, error) {
	if err := r.before(Op{Name: OpLock, Path: "."}); err != nil {
		return nil, err
	}
	how := syscall.LOCK_EX
	if shared {
		how = syscall.LOCK_SH
	}
	f, err := r.r.Open(".")
	if err != nil {
		return nil, err
	}
	c, err := f.SyscallConn()
	if err != nil {
		f.Close()
		return nil, err
	}
	var ferr error
	if err := c.Control(func(fd uintptr) {
		for {
			ferr = flock(int(fd), how|syscall.LOCK_NB)
			if ferr != syscall.EINTR {
				return
			}
		}
	}); err != nil {
		f.Close()
		return nil, err
	}
	if ferr != nil {
		f.Close()
		return nil, &os.PathError{Op: "flock", Path: ".", Err: ferr}
	}
	return &Lock{f: f, r: r}, nil
}

// Lock is a held lock. It keeps the locked directory's *os.File referenced
// until Unlock: while it is, no finalizer can close the descriptor and
// silently release the lock.
type Lock struct {
	f *os.File
	r *Root
}

// Unlock closes the locked descriptor, which releases the lock, even when a
// hook fails it.
func (l *Lock) Unlock() error {
	err := l.r.before(Op{Name: OpUnlock, Path: "."})
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	return err
}
