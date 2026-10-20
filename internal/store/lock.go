package store

import (
	"errors"
	"os"
	"time"

	"github.com/phansen314/koan/internal/cli"
	"golang.org/x/sys/unix"
)

// LockWait is how long a writer waits for the lock before it fails with
// busy.
var LockWait = 5 * time.Second

// lockPoll is how often a waiting writer tries the lock again.
const lockPoll = 5 * time.Millisecond

// Lock is the write lock: a flock on the root directory, held from before
// the transaction until after the change's last filesystem step.
type Lock struct{ f *os.File }

// TakeLock takes the write lock on the root directory dir, waiting up to
// LockWait; then it fails with busy. root is the root as written, for
// messages.
func TakeLock(dir, root string) (*Lock, *cli.Error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, cli.IO("could not open the root to lock it: "+err.Error(), root)
	}
	deadline := time.Now().Add(LockWait)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &Lock{f}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			f.Close()
			return nil, cli.IO("could not lock the root: "+err.Error(), root)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, busy("another write held the lock on " + root + " for 5 seconds")
		}
		time.Sleep(lockPoll)
	}
}

// TryLock takes the write lock only if no one holds it, without waiting.
// ok is false when someone does.
func TryLock(dir, root string) (l *Lock, ok bool, e *cli.Error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, cli.IO("could not open the root to lock it: "+err.Error(), root)
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return &Lock{f}, true, nil
	case errors.Is(err, unix.EWOULDBLOCK):
		f.Close()
		return nil, false, nil
	}
	f.Close()
	return nil, false, cli.IO("could not lock the root: "+err.Error(), root)
}

// Release releases the lock. Closing the descriptor releases the flock.
func (l *Lock) Release() {
	if l != nil && l.f != nil {
		l.f.Close()
		l.f = nil
	}
}
