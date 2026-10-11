package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A claim through the CLI: made, shown, refused a second time, locked with a
// token that only the claim's own result shows, and ended by done, release,
// or force-release.
func TestClaims(t *testing.T) {
	const token = "3f9c0a7e5b1d48c2a6e4f0b9d7c3a185"
	tr := newTree(t)
	tr.env = append(tr.env, clock, "KOAN_E2E_TOKEN="+token)
	steps(t, []step{
		{tr.cmd("create", "a", "--priority", "1"), 0, `"id":1,`},
		{tr.cmd("create", "b", "--priority", "2"), 0, `"id":2,`},
		{tr.cmd("create", "c"), 0, `"id":3,`},
		{tr.cmd("claim"), 2, `"kind":"usage"`}, // never the next task: that is claim-next
		{tr.cmd("claim", "1", "--extra", `{"agent":"a"}`), 0, `"claim":{"claimed_at":"2026-09-28T12:00:00Z","extra":{"agent":"a"},"locked":false}}}`},
		{tr.cmd("claim", "1"), 1, `"rule":"claimed","ids":[1],"claims":[{"claimed_at":"2026-09-28T12:00:00Z","extra":{"agent":"a"},"locked":false}]`},
		{tr.cmd("claim-next", "--use-token"), 0, `"locked":true}},"token":"` + token + `"}`},
		{tr.cmd("frontier", "--claimed=false", "--fields", "id"), 0, `"tasks":[{"id":3}],"total":1`},
		{tr.cmd("list", "--claimed", "--fields", "id,claim"), 0, `"total":2`},
		{tr.cmd("update", "2", "--priority", "5"), 1, `"rule":"token-mismatch","ids":[2],"claims":[{"claimed_at":"2026-09-28T12:00:00Z","extra":{},"locked":true}]`},
		{tr.cmd("update", "2", "--priority", "5", "--token", "nope"), 1, `"kind":"invalid-input"`},
		{tr.cmd("update", "2", "--priority", "5", "--token", token), 0, `"changed":["priority"]`},
		{tr.cmd("update", "1", "--priority", "5", "--token", token), 1, `"rule":"token-mismatch"`},
		{tr.cmd("done", "1"), 0, `"changed":true`},
		{tr.cmd("release", "2"), 1, `"rule":"token-mismatch"`},
	})
	// While the locked claim stands, no read shows its token.
	for _, cmd := range []*exec.Cmd{tr.cmd("show", "2"), tr.cmd("list", "--readiness", "ready,blocked,done"), tr.cmd("list", "--claimed", "--fields", "claim"), tr.cmd("doctor"), tr.cmd("info")} {
		if r := run(t, cmd); r.code != 0 || strings.Contains(r.stdout, token) {
			t.Errorf("%q: exit %d, or it shows the token: %s", cmd.Args[1:], r.code, r.stdout)
		}
	}
	if got, err := os.ReadFile(filepath.Join(tr.home, claimsPath)); err != nil || !strings.Contains(string(got), `"token": "`+token+`"`) {
		t.Fatalf("the claims file holds no locked claim: %q (%v)", got, err)
	}
	steps(t, []step{
		{tr.cmd("force-release", "2"), 0, `"changed":true`},
		{tr.cmd("claim-next"), 0, `"task":{"schema":1,"id":2,`},
		{tr.cmd("release", "2"), 0, `"changed":true`},
		{tr.cmd("list", "--claimed", "--limit", "0"), 0, `"total":0`},
		{tr.cmd("info"), 0, `"claims":{"path":"` + filepath.Join(tr.home, claimsPath) + `","state":"ok"}`},
		{tr.cmd("version"), 0, `"claims":1}`},
	})
	want := "{\n  \"schema\": 1,\n  \"root\": \"" + tr.root() + "\",\n  \"claims\": {}\n}\n"
	if got, err := os.ReadFile(filepath.Join(tr.home, claimsPath)); err != nil || string(got) != want {
		t.Errorf("claims file %q (%v), want %q", got, err, want)
	}
}

// N processes asking for the next task at once, each retrying on busy, are
// never given the same one: every task is claimed at most once, and exactly
// min(N, M) in all (implementation-spec.md, Claims tests).
func TestClaimNextStress(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns hundreds of processes")
	}
	defer func(n int64) { t.Logf("%d busy retries", busyRetries.Load()-n) }(busyRetries.Load())
	for _, c := range []struct{ procs, tasks int }{{16, 6}, {6, 16}, {12, 12}} {
		for round := range 4 {
			tr := newTree(t)
			var batch struct {
				Tasks []map[string]string `json:"tasks"`
			}
			for i := range c.tasks {
				batch.Tasks = append(batch.Tasks, map[string]string{"title": "t" + strconv.Itoa(i)})
			}
			doc, _ := json.Marshal(batch)
			steps(t, []step{{stdin(tr.cmd("create-batch", "-i", "-"), string(doc)), 0, `"ok":true`}})

			got := make([]int, c.procs) // the ID each process claimed; 0 for none
			errs := make([]error, c.procs)
			var wg sync.WaitGroup
			for p := range c.procs {
				wg.Go(func() {
					r, err := untilNotBusy(func() *exec.Cmd { return tr.cmd("claim-next", "--extra", fmt.Sprintf(`{"p":%d}`, p)) })
					if err != nil {
						errs[p] = err
						return
					}
					var out struct {
						OK     bool `json:"ok"`
						Result struct {
							Task *struct {
								ID int `json:"id"`
							} `json:"task"`
						} `json:"result"`
					}
					if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || !out.OK {
						errs[p] = fmt.Errorf("exit %d: %s%s", r.code, r.stdout, r.stderr)
						return
					}
					if out.Result.Task != nil {
						got[p] = out.Result.Task.ID
					}
				})
			}
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			seen := map[int]bool{}
			for _, id := range got {
				if id == 0 {
					continue
				}
				if seen[id] {
					t.Fatalf("%d procs, %d tasks, round %d: task %d claimed twice: %v", c.procs, c.tasks, round, id, got)
				}
				seen[id] = true
			}
			if want := min(c.procs, c.tasks); len(seen) != want {
				t.Fatalf("%d procs, %d tasks, round %d: %d tasks claimed, want %d: %v", c.procs, c.tasks, round, len(seen), want, got)
			}
			steps(t, []step{{tr.cmd("list", "--claimed", "--limit", "0"), 0, fmt.Sprintf(`"total":%d,`, min(c.procs, c.tasks))}})
		}
	}
}

// fenced is one round of the fencing check on a new task: a holder claims it
// with a token, then its done with that token meets a force-release followed
// by a new locked claim, in the order arrange gives them. Exactly one of the
// done and the new claim succeeds, and when the claim does, the task is open
// and locked with the new token, which the old one can no longer touch. It
// returns whether the done won.
func fenced(t *testing.T, tr *tree, id string, arrange func(done, release func() *exec.Cmd) (doneResult result)) bool {
	t.Helper()
	token := func(stdout string) string {
		var out struct {
			Result struct {
				Token string `json:"token"`
			} `json:"result"`
		}
		json.Unmarshal([]byte(stdout), &out)
		return out.Result.Token
	}
	steps(t, []step{{tr.cmd("create", "t"+id), 0, `"id":` + id + `,`}})
	r := run(t, tr.cmd("claim", id, "--use-token"))
	old := token(r.stdout)
	if r.code != 0 || old == "" {
		t.Fatalf("task %s: claim: %s", id, r.stdout)
	}
	done := arrange(
		func() *exec.Cmd { return tr.cmd("done", id, "--token", old) },
		func() *exec.Cmd { return tr.cmd("force-release", id) },
	)
	claim, err := untilNotBusy(func() *exec.Cmd { return tr.cmd("claim", id, "--use-token") })
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case done.code == 0 && claim.code == 1 && strings.Contains(claim.stdout, `"rule":"task-done"`):
		return true
	case claim.code == 0 && done.code == 1 && strings.Contains(done.stdout, `"rule":"token-mismatch"`):
		fresh := token(claim.stdout)
		if fresh == "" || fresh == old {
			t.Fatalf("task %s: the new claim's token %q, the old %q", id, fresh, old)
		}
		steps(t, []step{
			{tr.cmd("show", id), 0, `"completed_at":null`},
			{tr.cmd("show", id), 0, `"locked":true`},
			{tr.cmd("done", id, "--token", old), 1, `"rule":"token-mismatch"`},
			{tr.cmd("done", id, "--token", fresh), 0, `"changed":true`},
		})
		return false
	}
	t.Fatalf("task %s: done exit %d: %sclaim exit %d: %s", id, done.code, done.stdout, claim.code, claim.stdout)
	return false
}

// A holder's done with its token against a force-release and a new locked
// claim: the old holder's write never lands on the new claim. Each order is
// forced in turn, by holding one write at the lock while the other waits for
// it, and then the two are left to race. That one write excludes the other
// is the lock's own tests' to show (lock_test.go); this is about what each
// decides once it has the lock.
func TestClaimFencing(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns hundreds of processes")
	}
	tr := newTree(t)
	defer func(n int64) { t.Logf("%d busy retries", busyRetries.Load()-n) }(busyRetries.Load())
	// waits runs cmd behind h, which holds the lock, and lets h go once cmd
	// has been turned away at least once, so the two did overlap.
	waits := func(h *holder, mk func() *exec.Cmd) result {
		t.Helper()
		before := busyRetries.Load()
		var r result
		var err error
		var wg sync.WaitGroup
		wg.Go(func() { r, err = untilNotBusy(mk) })
		for busyRetries.Load() == before {
			time.Sleep(time.Millisecond)
		}
		h.release()
		wg.Wait()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	id := 0
	next := func() string { id++; return strconv.Itoa(id) }
	for range 5 {
		// The done holds the lock; the force-release waits behind it.
		if !fenced(t, tr, next(), func(done, release func() *exec.Cmd) result {
			h := hold(t, done())
			waits(h, release)
			return h.wait()
		}) {
			t.Fatal("the done held the lock first, and lost")
		}
		// The force-release holds the lock; the done waits behind it.
		if fenced(t, tr, next(), func(done, release func() *exec.Cmd) result {
			h := hold(t, release())
			r := waits(h, done)
			h.wait()
			return r
		}) {
			t.Fatal("the force-release held the lock first, and the done won")
		}
	}
	doneWon, claimWon := 0, 0
	for range 40 {
		won := fenced(t, tr, next(), func(done, release func() *exec.Cmd) result {
			var r result
			var errs [2]error
			var wg sync.WaitGroup
			wg.Go(func() { r, errs[0] = untilNotBusy(done) })
			wg.Go(func() { _, errs[1] = untilNotBusy(release) })
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			return r
		})
		if won {
			doneWon++
		} else {
			claimWon++
		}
	}
	t.Logf("racing: the done won %d rounds, the new claim %d", doneWon, claimWon)
}
