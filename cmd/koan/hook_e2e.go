//go:build e2e_hooks

package main

import (
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/fsys"
)

// A binary built with the e2e_hooks tag takes faults from its environment,
// for the end-to-end lock and crash tests. A released binary has none of
// this.
//
//	KOAN_E2E_CRASH_BEFORE=k   kill the process just before its k-th change to the disk
//	KOAN_E2E_ERRNO_BEFORE=k   fail the k-th change to the disk with ENOSPC
//	KOAN_E2E_PAUSE_BEFORE=k   stop for good just before the k-th change, holding the lock
//	KOAN_E2E_LOCK_WAIT_MS=n   wait n milliseconds for the lock instead of 5 seconds
//	KOAN_E2E_COUNT_FILE=path  append one byte to path for every change to the disk
func init() {
	envHook = func(env *cli.Env) {
		var hooks []fsys.Hook
		if k := number("KOAN_E2E_CRASH_BEFORE"); k > 0 {
			hooks = append(hooks, fsys.CrashBefore(k))
		}
		if k := number("KOAN_E2E_ERRNO_BEFORE"); k > 0 {
			hooks = append(hooks, fsys.ErrnoAtMutating(k, syscall.ENOSPC))
		}
		if k := number("KOAN_E2E_PAUSE_BEFORE"); k > 0 {
			seen := 0
			hooks = append(hooks, func(op fsys.Op) error {
				if op.Mutating {
					if seen++; seen == k {
						select {}
					}
				}
				return nil
			})
		}
		if p := os.Getenv("KOAN_E2E_COUNT_FILE"); p != "" {
			hooks = append(hooks, func(op fsys.Op) error {
				if op.Mutating {
					if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
						f.Write([]byte{'x'})
						f.Close()
					}
				}
				return nil
			})
		}
		if len(hooks) > 0 {
			env.Ops.FS = fsys.Fault{FS: env.Ops.FS, Hook: fsys.Hooks(hooks...)}
		}
		if n := number("KOAN_E2E_LOCK_WAIT_MS"); n > 0 {
			env.Ops.LockWait = time.Duration(n) * time.Millisecond
		}
	}
}

func number(name string) int {
	n, _ := strconv.Atoi(os.Getenv(name))
	return n
}
