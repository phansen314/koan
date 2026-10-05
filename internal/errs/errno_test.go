package errs

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

// Every errno from 1 to 255 with a real message on this platform must have a
// name, so a missing one fails CI rather than shipping.
func TestErrnoTableComplete(t *testing.T) {
	for n := 1; n <= 255; n++ {
		e := syscall.Errno(n)
		if e.Error() == fmt.Sprintf("errno %d", n) {
			continue
		}
		if _, ok := ErrnoName(e); !ok {
			t.Errorf("errno %d (%q) has no name", n, e.Error())
		}
	}
}

// Each name belongs to one errno, except an alias with its own number, which
// shares its canonical name (sharedNames, per platform).
func TestErrnoNamesUnique(t *testing.T) {
	count := map[string]int{}
	for _, name := range errnoNames {
		count[name]++
	}
	for name, n := range count {
		want := sharedNames[name]
		if want == 0 {
			want = 1
		}
		if n != want {
			t.Errorf("name %s used for %d errnos, want %d", name, n, want)
		}
	}
}

// Aliases get one fixed name on both platforms (aliasCases, per platform).
func TestErrnoAliases(t *testing.T) {
	cases := append(aliasCases, []struct {
		e    syscall.Errno
		want string
	}{
		{syscall.EAGAIN, "EAGAIN"},
		{syscall.ENOTSUP, "ENOTSUP"},
		{syscall.EDEADLK, "EDEADLK"},
		{syscall.ENOSPC, "ENOSPC"},
		{syscall.ENOENT, "ENOENT"},
	}...)
	for _, tc := range cases {
		if got, _ := ErrnoName(tc.e); got != tc.want {
			t.Errorf("ErrnoName(%d) = %q, want %q", int(tc.e), got, tc.want)
		}
	}
}

func TestFromOS(t *testing.T) {
	pathErr := &os.PathError{Op: "write", Path: "/r/1.json", Err: syscall.ENOSPC}
	got := FromOS("/r/1.json", pathErr)
	if got.Kind != KindIO || got.Details != (IODetails{Path: "/r/1.json", Code: "ENOSPC"}) {
		t.Errorf("PathError: got %+v", got)
	}

	linkErr := &os.LinkError{Op: "link", Old: "a", New: "b", Err: syscall.EEXIST}
	if got := FromOS("b", linkErr); got.Details != (IODetails{Path: "b", Code: "EEXIST"}) {
		t.Errorf("LinkError: got %+v", got)
	}

	sysErr := os.NewSyscallError("flock", syscall.EBADF)
	if got := FromOS("/r", sysErr); got.Details != (IODetails{Path: "/r", Code: "EBADF"}) {
		t.Errorf("SyscallError: got %+v", got)
	}

	if got := FromOS("/r", fmt.Errorf("plain")); got.Kind != KindInternal {
		t.Errorf("no errno: kind %s, want internal", got.Kind)
	}

	if got := FromOS("/r", syscall.Errno(4000)); got.Kind != KindInternal {
		t.Errorf("unnamed errno: kind %s, want internal", got.Kind)
	}
}
