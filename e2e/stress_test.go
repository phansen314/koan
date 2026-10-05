package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// busyRetries counts the busy results untilNotBusy retried, to show the
// tests contended.
var busyRetries atomic.Int64

// untilNotBusy runs the command mk makes until it ends other than busy, and
// returns that result. It is safe off the test goroutine: it reports
// failure as an error, not through t.
func untilNotBusy(mk func() *exec.Cmd) (result, error) {
	for {
		cmd := mk()
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			return result{}, err
		}
		r := result{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
		if r.code != 1 || !strings.Contains(r.stdout, `"kind":"busy"`) {
			return r, nil
		}
		busyRetries.Add(1)
		time.Sleep(time.Duration(rand.IntN(2000)) * time.Microsecond)
	}
}

// Concurrent writers, each retrying on busy, lose no update: every create
// gets its own ID, and last_id counts them all (implementation-spec.md,
// Lock 5).
func TestLockStress(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns 800 processes")
	}
	const procs, each = 16, 50
	tr := newTree(t)
	defer func(n int64) { t.Logf("%d busy retries", busyRetries.Load()-n) }(busyRetries.Load())
	var wg sync.WaitGroup
	errc := make(chan error, procs)
	for p := range procs {
		wg.Go(func() {
			for i := range each {
				r, err := untilNotBusy(func() *exec.Cmd { return tr.cmd("create", fmt.Sprintf("p%d-%d", p, i)) })
				if err == nil && r.code != 0 {
					err = fmt.Errorf("exit %d: %s%s", r.code, r.stdout, r.stderr)
				}
				if err != nil {
					errc <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}

	r := run(t, tr.cmd("list", "--readiness", "ready,blocked,complete"))
	envelope(t, r)
	var list struct {
		Result struct {
			Tasks []struct {
				ID int `json:"id"`
			} `json:"tasks"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &list); err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, task := range list.Result.Tasks {
		if task.ID < 1 || task.ID > procs*each || seen[task.ID] {
			t.Errorf("id %d repeated or out of range", task.ID)
		}
		seen[task.ID] = true
	}
	if len(seen) != procs*each {
		t.Errorf("%d distinct tasks, want %d", len(seen), procs*each)
	}
	steps(t, []step{{tr.cmd("info"), 0, fmt.Sprintf(`"last_id":%d`, procs*each)}})
}

// Two blocks that would close a cycle between them, racing: in every round
// exactly one succeeds and the other is refused as a cycle (Lock 6).
func TestLockRacingBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns hundreds of processes")
	}
	tr := newTree(t)
	defer func(n int64) { t.Logf("%d busy retries", busyRetries.Load()-n) }(busyRetries.Load())
	steps(t, []step{
		{tr.cmd("create", "a"), 0, `"id":1,`},
		{tr.cmd("create", "b"), 0, `"id":2,`},
	})
	for round := range 50 {
		var rs [2]result
		var errs [2]error
		var wg sync.WaitGroup
		for i, args := range [][]string{{"block", "1", "--blockers", "2"}, {"block", "2", "--blockers", "1"}} {
			wg.Go(func() { rs[i], errs[i] = untilNotBusy(func() *exec.Cmd { return tr.cmd(args...) }) })
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		ok, refused := 0, 0
		for _, r := range rs {
			envelope(t, r)
			switch {
			case r.code == 0 && strings.Contains(r.stdout, `"added":[`):
				ok++
			case r.code == 1 && strings.Contains(r.stdout, `"rule":"acyclic"`):
				refused++
			}
		}
		if ok != 1 || refused != 1 {
			t.Fatalf("round %d: want one success and one acyclic: %s%s", round, rs[0].stdout, rs[1].stdout)
		}
		steps(t, []step{
			{tr.cmd("unblock", "1", "--blockers", "2"), 0, `"removed":`},
			{tr.cmd("unblock", "2", "--blockers", "1"), 0, `"removed":`},
		})
	}
}
