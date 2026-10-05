package fsys

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestLockContention(t *testing.T) {
	r1, dir := newRoot(t, nil)
	r2, err := OS{}.OpenRoot(dir)
	must(t, err)
	defer r2.Close()

	l, err := r1.Lock()
	must(t, err)
	_, err = r2.Lock()
	wantErrno(t, err, syscall.EAGAIN)
	// A second lock through the same root is a second open file
	// description, so it contends too.
	_, err = r1.Lock()
	wantErrno(t, err, syscall.EAGAIN)

	must(t, l.Unlock())
	l2, err := r2.Lock()
	must(t, err)
	must(t, l2.Unlock())
}

// The lock is on the directory, not a path: a root reached through a
// symlink contends on the same lock.
func TestLockThroughSymlinkedRoot(t *testing.T) {
	r1, dir := newRoot(t, nil)
	link := filepath.Join(t.TempDir(), "root")
	must(t, os.Symlink(dir, link))
	r2, err := OS{}.OpenRoot(link)
	must(t, err)
	defer r2.Close()

	l, err := r1.Lock()
	must(t, err)
	defer l.Unlock()
	_, err = r2.Lock()
	wantErrno(t, err, syscall.EAGAIN)
}

// A held lock survives garbage collection: the Lock keeps its descriptor
// referenced, so no finalizer closes it. Finalizers run on their own
// goroutine after a collection, so each collection is followed by a pause
// that lets them run.
func TestLockSurvivesGC(t *testing.T) {
	r1, dir := newRoot(t, nil)
	r2, err := OS{}.OpenRoot(dir)
	must(t, err)
	defer r2.Close()

	l, err := r1.Lock()
	must(t, err)
	for range 10 {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	_, err = r2.Lock()
	wantErrno(t, err, syscall.EAGAIN)
	runtime.KeepAlive(l)
	must(t, l.Unlock())
}

func TestLockRetriesEINTR(t *testing.T) {
	r, _ := newRoot(t, nil)
	calls := 0
	flock = func(fd, how int) error {
		calls++
		if calls <= 2 {
			return syscall.EINTR
		}
		return syscall.Flock(fd, how)
	}
	t.Cleanup(func() { flock = syscall.Flock })

	l, err := r.Lock()
	must(t, err)
	must(t, l.Unlock())
	if calls != 3 {
		t.Errorf("flock called %d times, want 3", calls)
	}
}
