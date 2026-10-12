// Package e2e tests the built koan binary: the command line, the write lock
// between processes, and what a crash leaves behind.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/schematest"
)

var koanBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "koan-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	koanBin = filepath.Join(dir, "koan")
	build := exec.Command("go", "build", "-tags", "e2e_hooks", "-o", koanBin, "../cmd/koan")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build koan: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// run is one koan process.
type run struct {
	exit   int
	killed bool
	stdout string
	stderr string
	env    map[string]any // the envelope, nil when stdout is not one
}

func (r run) result() map[string]any { m, _ := r.env["result"].(map[string]any); return m }
func (r run) err() map[string]any    { m, _ := r.env["error"].(map[string]any); return m }

// koan runs the binary with HOME set to home. extra are NAME=value pairs
// added to its environment; stdin is what it reads.
func koan(t testing.TB, home, stdin string, extra []string, args ...string) run {
	t.Helper()
	cmd := exec.Command(koanBin, args...)
	cmd.Env = append([]string{"HOME=" + home}, extra...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := run{stdout: out.String(), stderr: errb.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		r.exit = ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			r.killed = true
		}
	} else if err != nil {
		t.Fatalf("run koan %v: %v", args, err)
	}
	if json.Unmarshal(out.Bytes(), &r.env) == nil && r.env != nil {
		if strings.Count(r.stdout, "\n") != 1 {
			t.Errorf("koan %v: stdout is not one line: %q", args, r.stdout)
		}
		if err := schematest.Check(t, "envelope", out.Bytes()); err != nil {
			t.Errorf("koan %v: envelope: %v\n%s", args, err, r.stdout)
		}
	}
	return r
}

// ok runs koan and requires success.
func ok(t testing.TB, home string, args ...string) map[string]any {
	t.Helper()
	r := koan(t, home, "", nil, args...)
	if r.exit != 0 || r.env["ok"] != true {
		t.Fatalf("koan %v: exit %d: %s%s", args, r.exit, r.stdout, r.stderr)
	}
	return r.result()
}

func newHome(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	ok(t, home, "init")
	return home
}

func TestCommandLine(t *testing.T) {
	home := newHome(t)
	res := ok(t, home, "create", "Book flights", "--folder", "/proj/travel", "--priority", "2",
		"--tags", "travel,urgent", "--extra", `{"status":"waiting"}`, "--notes", "hi")
	task := res["task"].(map[string]any)
	if task["id"] != float64(1) || task["folder"] != "/proj/travel" || task["priority"] != float64(2) {
		t.Errorf("create: %v", task)
	}
	ok(t, home, "create", "--blocked-by", "1", "--", "-starts with a dash")
	ok(t, home, "update", "2", "--priority", "null", "--tags-add", "a", "--tags-add", "b,c", "--extra-merge", `{"k":"v"}`, "--extra-remove", "x,y")
	ok(t, home, "block", "2", "--blockers", "1")
	ok(t, home, "move", "2", "--to", "/archive")
	if got := ok(t, home, "list", "--fields", "id,title", "--readiness", "ready,blocked,done"); got["total"] != float64(2) {
		t.Errorf("list: %v", got)
	}
	if got := ok(t, home, "frontier", "--recursive=false"); got["total"] != float64(0) {
		t.Errorf("frontier of / alone: %v", got)
	}
	ok(t, home, "claim-next", "--extra", `{"worker":"w1"}`)
	if got := ok(t, home, "list", "--claimed"); got["total"] != float64(1) {
		t.Errorf("list --claimed: %v", got)
	}
	ok(t, home, "release", "1")
	ok(t, home, "done", "1")
	ok(t, home, "delete-folder", "/proj", "--recursive")

	// --input and --notes-file read stdin only when - names it.
	r := koan(t, home, `{"tasks":[{"ref":"a","title":"First"},{"title":"Second","blocked_by":["a"]}]}`, nil, "create-batch", "-i", "-")
	if r.exit != 0 {
		t.Fatalf("create-batch: %s", r.stdout)
	}
	r = koan(t, home, "from stdin\n", nil, "create", "With notes", "--notes-file", "-")
	notes, _ := os.ReadFile(r.result()["task"].(map[string]any)["notes_path"].(string))
	if string(notes) != "from stdin\n" {
		t.Errorf("notes %q", notes)
	}
}

func TestExitCodesAndStderr(t *testing.T) {
	home := newHome(t)
	cases := []struct {
		args   []string
		exit   int
		kind   string
		stderr string
	}{
		{[]string{"show", "99"}, 1, "not-found", "koan: not-found: task 99 does not exist\n"},
		{[]string{"show", "abc"}, 1, "invalid-input", ""},
		{[]string{"show"}, 2, "usage", ""},
		{[]string{"show", "1", "2"}, 2, "usage", ""},
		{[]string{"show", "1", "--nope"}, 2, "usage", ""},
		{[]string{"sho", "1"}, 2, "usage", ""},
		{[]string{}, 2, "usage", ""},
		{[]string{"help"}, 2, "usage", ""},
		{[]string{"create", "x", "--notes", "a", "--notes-file", "b"}, 2, "usage", ""},
		{[]string{"create", "x", "-i", "f"}, 2, "usage", ""},
		{[]string{"create", "x", "--extra", "{"}, 2, "usage", ""},
		{[]string{"create", "x", "--priority", "high"}, 1, "invalid-input", ""},
	}
	for _, c := range cases {
		r := koan(t, home, "", nil, c.args...)
		if r.exit != c.exit || r.err()["kind"] != c.kind {
			t.Errorf("koan %v: exit %d, %s; want %d, %s", c.args, r.exit, r.stdout, c.exit, c.kind)
		}
		if c.stderr != "" && r.stderr != c.stderr {
			t.Errorf("koan %v: stderr %q, want %q", c.args, r.stderr, c.stderr)
		}
		if strings.Count(r.stderr, "\n") > 1 {
			t.Errorf("koan %v: more than one line on stderr: %q", c.args, r.stderr)
		}
	}
	for _, args := range [][]string{{"--help"}, {"create", "-h"}} {
		r := koan(t, home, "", nil, args...)
		if r.exit != 0 || r.env != nil || !strings.Contains(r.stdout, "koan") || r.stderr != "" {
			t.Errorf("koan %v: exit %d, stdout %q", args, r.exit, r.stdout)
		}
	}
	// A success with warnings says so on stderr.
	ok(t, home, "create", "a")
	os.WriteFile(filepath.Join(home, "koan-root", "7.json"), []byte("x"), 0o644)
	if r := koan(t, home, "", nil, "list"); r.exit != 0 || r.stderr != "koan: 1 warning\n" {
		t.Errorf("warnings: exit %d, stderr %q", r.exit, r.stderr)
	}
}

// A result that can't be written is neither success nor an ordinary failure.
func TestStdoutClosed(t *testing.T) {
	home := newHome(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	pr.Close()
	cmd := exec.Command(koanBin, "info")
	cmd.Env = []string{"HOME=" + home}
	cmd.Stdout = pw
	var errb bytes.Buffer
	cmd.Stderr = &errb
	err = cmd.Run()
	pw.Close()
	ee, isExit := err.(*exec.ExitError)
	if !isExit || ee.ExitCode() <= 2 {
		t.Errorf("exit %v, want above 2; stderr %q", err, errb.String())
	}
}

// holder starts a write that stops for good just before its first change to
// the disk, holding the lock, and returns once the lock is seen to be held.
func holder(t *testing.T, home string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(koanBin, "create", "held")
	cmd.Env = []string{"HOME=" + home, "KOAN_E2E_PAUSE_BEFORE=1"}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r := koan(t, home, "", []string{"KOAN_E2E_LOCK_WAIT_MS=20"}, "create-folder", "/probe"); r.err()["kind"] == "busy" {
			return cmd
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the holder never took the lock")
	return nil
}

func TestLockBusyAndKilledHolder(t *testing.T) {
	home := newHome(t)
	h := holder(t, home)
	r := koan(t, home, "", []string{"KOAN_E2E_LOCK_WAIT_MS=100"}, "create", "second")
	if r.exit != 1 || r.err()["kind"] != "busy" {
		t.Fatalf("a write under a held lock: exit %d, %s", r.exit, r.stdout)
	}
	if r := koan(t, home, "", nil, "list"); r.exit != 0 {
		t.Errorf("a read under a held lock: %s", r.stdout)
	}
	h.Process.Signal(syscall.SIGKILL)
	h.Wait()
	ok(t, home, "create", "after the holder died")
	if got := ok(t, home, "doctor"); got["healthy"] != true {
		t.Errorf("doctor after a killed holder: %v", got)
	}
}

func TestConcurrentCreates(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns 800 processes")
	}
	home := newHome(t)
	const procs, each = 16, 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[float64]bool{}
	for p := 0; p < procs; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				r := koan(t, home, "", nil, "create", fmt.Sprintf("task %d-%d", p, i), "--folder", fmt.Sprintf("/p%d", p%4))
				if r.exit != 0 {
					t.Errorf("create: %s", r.stdout)
					return
				}
				id := r.result()["task"].(map[string]any)["id"].(float64)
				mu.Lock()
				if seen[id] {
					t.Errorf("ID %v issued twice", id)
				}
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != procs*each {
		t.Errorf("%d distinct IDs, want %d", len(seen), procs*each)
	}
	if got := ok(t, home, "doctor"); got["healthy"] != true {
		t.Errorf("doctor: %v", got)
	}
}

// Two blocks that would close a cycle between them never both succeed.
func TestRacingBlocks(t *testing.T) {
	home := newHome(t)
	ok(t, home, "create", "a")
	ok(t, home, "create", "b")
	for i := 0; i < 20; i++ {
		var wg sync.WaitGroup
		exits := make([]int, 2)
		for j, args := range [][]string{{"block", "1", "--blockers", "2"}, {"block", "2", "--blockers", "1"}} {
			wg.Add(1)
			go func() { defer wg.Done(); exits[j] = koan(t, home, "", nil, args...).exit }()
		}
		wg.Wait()
		if exits[0] == 0 && exits[1] == 0 {
			t.Fatalf("round %d: both blocks succeeded", i)
		}
		ok(t, home, "unblock", "1", "--blockers", "2")
		ok(t, home, "unblock", "2", "--blockers", "1")
	}
}

// Two claim-nexts asking at once never get the same task.
func TestRacingClaims(t *testing.T) {
	home := newHome(t)
	for i := 0; i < 8; i++ {
		ok(t, home, "create", fmt.Sprintf("t%d", i))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := []float64{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := koan(t, home, "", nil, "claim-next")
			if task, _ := r.result()["task"].(map[string]any); task != nil {
				mu.Lock()
				got = append(got, task["id"].(float64))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	slices.Sort(got)
	if len(slices.Compact(slices.Clone(got))) != 8 || len(got) != 8 {
		t.Errorf("claimed %v, want eight distinct tasks", got)
	}
}
