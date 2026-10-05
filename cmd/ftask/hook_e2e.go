//go:build e2e_hooks

package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/phansen314/ftask/internal/cli"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/pick"
)

// Test builds only (implementation-spec.md, Test hooks). Each variable is
// read once the process is set up; the release build has none of them.
//
//   - FTASK_E2E_PANIC: panic, so e2e can check that a crash exits 134, not 2.
//   - FTASK_E2E_CLOCK=<RFC 3339>: a fixed clock, so written files compare
//     byte for byte.
//   - FTASK_E2E_CRASH_BEFORE=<k>: SIGKILL just before the k-th call that
//     changes the disk.
//   - FTASK_E2E_HOLD: once the write lock is taken, write "held\n" to stderr
//     and wait for EOF on fd 3 before going on. With FTASK_E2E_HOLD_GC, force
//     garbage collections first, to show the lock survives them.
//   - FTASK_E2E_LOCK_WAIT=<duration>: a write waits this long, not 5s, for
//     a held write lock, so e2e sees busy quickly.
//   - FTASK_E2E_SOURCE_LIMIT=<duration>: pick's live source is killed after
//     this, not 10s, on a reload, so e2e can time it out quickly. The
//     status line still names the real limit.
//   - FTASK_E2E_HELPER_LOG=<file>: each run of pick's helper appends its
//     verb and a newline to file as it starts, so e2e can tell which
//     callbacks fzf ran.
func init() {
	envHook = func(env *cli.Env) {
		if v := os.Getenv("FTASK_E2E_HELPER_LOG"); v != "" && len(os.Args) > 2 && os.Args[1] == pick.HelperCommand {
			f, err := os.OpenFile(v, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
			if err != nil {
				panic(fmt.Sprintf("FTASK_E2E_HELPER_LOG: %v", err))
			}
			f.WriteString(os.Args[2] + "\n")
			f.Close()
		}
		if os.Getenv("FTASK_E2E_PANIC") != "" {
			panic("forced by FTASK_E2E_PANIC")
		}
		if v := os.Getenv("FTASK_E2E_CLOCK"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				panic(fmt.Sprintf("FTASK_E2E_CLOCK: %v", err))
			}
			env.Ops.Clock = func() time.Time { return t }
		}
		var hooks []fsys.Hook
		if v := os.Getenv("FTASK_E2E_CRASH_BEFORE"); v != "" {
			k, err := strconv.Atoi(v)
			if err != nil || k < 1 {
				panic(fmt.Sprintf("FTASK_E2E_CRASH_BEFORE: %q", v))
			}
			hooks = append(hooks, fsys.CrashBefore(k))
		}
		if os.Getenv("FTASK_E2E_HOLD") != "" {
			hooks = append(hooks, holdAfterLock(os.Getenv("FTASK_E2E_HOLD_GC") != ""))
		}
		if hooks != nil {
			env.Ops.FS = fsys.Fault{FS: env.Ops.FS, Hook: fsys.Hooks(hooks...)}
		}
		if v, ok := os.LookupEnv("FTASK_E2E_LOCK_WAIT"); ok {
			d, err := time.ParseDuration(v)
			if err != nil || d < 0 {
				panic(fmt.Sprintf("FTASK_E2E_LOCK_WAIT: %q", v))
			}
			env.Ops.LockWait = d
		}
		if v := os.Getenv("FTASK_E2E_SOURCE_LIMIT"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				panic(fmt.Sprintf("FTASK_E2E_SOURCE_LIMIT: %q", v))
			}
			run := env.Pick.RunSource
			env.Pick.RunSource = func(command string, environ []string, limit time.Duration) ([]byte, []byte, bool, error) {
				if limit > 0 {
					limit = d
				}
				return run(command, environ, limit)
			}
		}
	}
}

// holdAfterLock pauses at the first call after the lock is taken: the lock
// is held and nothing has been written, whatever the write. The hook runs
// before Lock, so it cannot see Lock fail; a failed Lock (busy) is followed
// only by closing the root, which does not pause.
func holdAfterLock(gc bool) fsys.Hook {
	locked := false
	return func(op fsys.Op) error {
		if op.Name == fsys.OpLock {
			locked = true
			return nil
		}
		if !locked {
			return nil
		}
		locked = false
		if op.Name == fsys.OpCloseRoot {
			return nil
		}
		if gc {
			for range 3 {
				runtime.GC()
			}
		}
		os.Stderr.WriteString("held\n")
		io.Copy(io.Discard, os.NewFile(3, "hold"))
		return nil
	}
}
