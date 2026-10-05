package errs

import "syscall"

// aliasCases: each alias and the name it must get. EOPNOTSUPP has its own
// number on darwin.
var aliasCases = []struct {
	e    syscall.Errno
	want string
}{
	{syscall.EWOULDBLOCK, "EAGAIN"},
	{syscall.EOPNOTSUPP, "ENOTSUP"},
}

// sharedNames: names given to more than one errno, with how many.
var sharedNames = map[string]int{"ENOTSUP": 2}
