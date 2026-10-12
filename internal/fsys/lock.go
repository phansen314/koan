package fsys

import (
	"os"
	"syscall"
)

// flock is syscall.Flock; a test replaces it to inject EINTR.
var flock = syscall.Flock

func (r *osRoot) Lock(shared bool) (Lock, error) {
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
	return &osLock{f: f}, nil
}

// osLock holds the locked directory's *os.File. While it is referenced, no
// finalizer can close the descriptor and silently release the lock.
type osLock struct {
	f *os.File
}

func (l *osLock) Unlock() error { return l.f.Close() }
