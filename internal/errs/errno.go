package errs

import (
	"errors"
	"fmt"
	"syscall"
)

// ErrnoName returns e's symbolic name, the same on Linux and macOS (e.g.
// "ENOSPC"). ok is false for an errno not in the table, which callers must
// report as internal, never with an invented name.
func ErrnoName(e syscall.Errno) (name string, ok bool) {
	name, ok = errnoNames[e]
	return name, ok
}

// ErrnoOf finds the errno inside err, through *os.PathError, *os.LinkError,
// and *os.SyscallError.
func ErrnoOf(err error) (syscall.Errno, bool) {
	var e syscall.Errno
	if errors.As(err, &e) {
		return e, true
	}
	return 0, false
}

// FromOS turns an OS error at path into io with the errno's symbolic name. An
// error with no errno inside it, or one not in the table, is internal. Call
// sites that give an errno another meaning (see implementation-spec.md, OS
// errors) must classify it before falling back to FromOS.
func FromOS(path string, err error) *Error {
	e, ok := ErrnoOf(err)
	if !ok {
		return Internal(fmt.Sprintf("%s: error with no OS error code: %v", path, err))
	}
	name, ok := ErrnoName(e)
	if !ok {
		return Internal(fmt.Sprintf("%s: OS error %d has no symbolic name: %v", path, int(e), err))
	}
	return IO(path, name)
}
