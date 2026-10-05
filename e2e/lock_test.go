package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A write held at the lock makes other writes busy once their wait is over,
// while reads go on; once released, it completes (implementation-spec.md,
// Lock 1).
func TestLockContention(t *testing.T) {
	tr := newTree(t)
	h := hold(t, tr.cmd("create", "a"))
	steps(t, []step{
		{tr.cmd("create", "b"), 1, `"kind":"busy"`},
		{tr.cmd("done", "1"), 1, `"kind":"busy"`},
		{tr.cmd("list"), 0, `"tasks":[]`},
		{tr.cmd("info"), 0, `"last_id":0`},
	})
	h.release()
	if r := h.wait(); r.code != 0 || !strings.Contains(r.stdout, `"id":1,`) {
		t.Fatalf("holder: exit %d: %s%s", r.code, r.stdout, r.stderr)
	}
	steps(t, []step{{tr.cmd("info"), 0, `"last_id":1`}})
}

// One lock however the root is reached: through a symlink to it, and from
// another config naming it (Lock 2).
func TestLockOneRoot(t *testing.T) {
	tr := newTree(t)
	link := filepath.Join(tr.home, "link")
	if err := os.Symlink(tr.root(), link); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{"symlink": link, "other config": tr.root()} {
		other := koan(t, "init", root)
		steps(t, []step{{other, 0, `"action":"attached"`}})
		h := hold(t, tr.cmd("create", "a"))
		second := koan(t, "create", "b")
		second.Env = other.Env
		r := run(t, second)
		envelope(t, r)
		if r.code != 1 || !strings.Contains(r.stdout, `"kind":"busy"`) {
			t.Errorf("%s: exit %d, want busy: %s", name, r.code, r.stdout)
		}
		h.release()
		if r := h.wait(); r.code != 0 {
			t.Fatalf("holder: exit %d: %s%s", r.code, r.stdout, r.stderr)
		}
	}
}

// A holder killed with SIGKILL releases the lock, and leaves nothing to
// clean up (Lock 3).
func TestLockReleaseOnCrash(t *testing.T) {
	tr := newTree(t)
	h := hold(t, tr.cmd("create", "a"))
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	h.wait()
	steps(t, []step{
		{tr.cmd("create", "b"), 0, `"id":1,`},
	})
	noTemps(t, tr.root())
}

// Garbage collection while the lock is held does not release it: nothing
// lets the locked descriptor be finalized (Lock 4).
func TestLockKeepAlive(t *testing.T) {
	tr := newTree(t)
	h := hold(t, tr.cmd("create", "a"), "KOAN_E2E_HOLD_GC=1")
	steps(t, []step{{tr.cmd("create", "b"), 1, `"kind":"busy"`}})
	h.release()
	if r := h.wait(); r.code != 0 {
		t.Fatalf("holder: exit %d: %s%s", r.code, r.stdout, r.stderr)
	}
}

// doctor and repair take the lock: a doctor held makes a write busy, and a
// write held makes both busy (implementation-spec.md, Lock 7).
func TestLockDiagnostics(t *testing.T) {
	tr := newTree(t)
	h := hold(t, tr.cmd("doctor"))
	steps(t, []step{
		{tr.cmd("create", "a"), 1, `"kind":"busy"`},
		{tr.cmd("list"), 0, `"tasks":[]`},
	})
	h.release()
	if r := h.wait(); r.code != 0 || !strings.Contains(r.stdout, `"healthy":true`) {
		t.Fatalf("doctor: exit %d: %s%s", r.code, r.stdout, r.stderr)
	}
	h = hold(t, tr.cmd("create", "a"))
	steps(t, []step{
		{tr.cmd("doctor"), 1, `"kind":"busy"`},
		{tr.cmd("repair"), 1, `"kind":"busy"`},
	})
	h.release()
	if r := h.wait(); r.code != 0 {
		t.Fatalf("holder: exit %d: %s%s", r.code, r.stdout, r.stderr)
	}
}

// A write that finds the lock held, with the default wait, waits for it and
// completes once the holder releases, taking the next ID: it never ran while
// the holder held the lock (implementation-spec.md, Lock 8).
func TestLockWaits(t *testing.T) {
	tr := newTree(t)
	h := hold(t, tr.cmd("create", "a"))
	waiter := make(chan result)
	go func() { waiter <- run(t, withoutLockWait(tr.cmd("create", "b"))) }()
	time.Sleep(300 * time.Millisecond)
	h.release()
	if r := h.wait(); r.code != 0 || !strings.Contains(r.stdout, `"id":1,`) {
		t.Fatalf("holder: exit %d: %s%s", r.code, r.stdout, r.stderr)
	}
	r := <-waiter
	envelope(t, r)
	if r.code != 0 || !strings.Contains(r.stdout, `"id":2,`) {
		t.Fatalf("waiter: exit %d, want id 2: %s", r.code, r.stdout)
	}
}

// SIGTERM mid-write is a crash: death by the signal (143 to a shell), no
// envelope, and the lock released (implementation-spec.md, Exit and
// signals).
func TestSIGTERM(t *testing.T) {
	tr := newTree(t)
	h := hold(t, tr.cmd("create", "a"))
	if err := h.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	r := h.wait()
	ws, _ := h.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Errorf("exit %d (%v), want death by SIGTERM", r.code, h.cmd.ProcessState)
	}
	if r.stdout != "" {
		t.Errorf("stdout %q, want nothing", r.stdout)
	}
	steps(t, []step{{tr.cmd("create", "b"), 0, `"id":1,`}})
}

// noTemps checks that no .koan-tmp-* file is left under root.
func noTemps(t *testing.T, root string) {
	t.Helper()
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Error(err)
		} else if strings.HasPrefix(d.Name(), ".koan-tmp-") {
			t.Errorf("leftover temp file %s", p)
		}
		return nil
	})
}
