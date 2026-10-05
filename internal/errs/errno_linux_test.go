//go:build linux && (amd64 || arm64)

package errs

import "syscall"

// aliasCases: each alias and the name it must get. On linux every alias
// shares its canonical name's number.
var aliasCases = []struct {
	e    syscall.Errno
	want string
}{
	{syscall.EWOULDBLOCK, "EAGAIN"},
	{syscall.EOPNOTSUPP, "ENOTSUP"},
	{syscall.EDEADLOCK, "EDEADLK"},
}

// sharedNames: names given to more than one errno, with how many.
var sharedNames = map[string]int{}
