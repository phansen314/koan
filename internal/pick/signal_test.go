package pick

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// While fzf runs, SIGINT and SIGQUIT to pick are discarded, and the
// programs fzf starts get them at their defaults, not ignored. Afterwards,
// SIGINT is a crash again (pick-spec.md, Signals). The test runs in a child
// process, which the last SIGINT kills.
func TestCatchInterrupts(t *testing.T) {
	if dir := os.Getenv("PICK_SIGNAL_CHILD"); dir != "" {
		signalChild(dir)
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCatchInterrupts$")
	cmd.Env = append(os.Environ(), "PICK_SIGNAL_CHILD="+dir)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("child ended %v, want killed by SIGINT\n%s", exit, out)
	}
	if !strings.Contains(string(out), "survived fzf\n") || strings.Contains(string(out), "survived SIGINT") {
		t.Errorf("child output:\n%s", out)
	}
	if runtime.GOOS == "linux" {
		// Whatever this process inherited ignored (a shell may ignore
		// SIGQUIT), pick adds nothing: the mask under fzf is the one
		// before.
		before, during := sigIgn(t, dir, "before"), sigIgn(t, dir, "during")
		if bits := uint64(1)<<(syscall.SIGINT-1) | 1<<(syscall.SIGQUIT-1); before&bits != during&bits {
			t.Errorf("SigIgn %x under fzf, %x before", during, before)
		}
	}
}

func sigIgn(t *testing.T, dir, name string) uint64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	mask, err := strconv.ParseUint(strings.TrimSpace(string(b)), 16, 64)
	if err != nil {
		t.Fatalf("%s: SigIgn %q", name, b)
	}
	return mask
}

func signalChild(dir string) {
	// The "fzf" signals pick, its parent, as ctrl-c in an editor would, and
	// records which signals it ignores itself.
	record := func(name string) string {
		return fmt.Sprintf(`grep SigIgn /proc/$$/status 2>/dev/null | cut -f2 > %s/%s`, dir, name)
	}
	runFzf("/bin/sh", []string{"-c", record("before")}, os.Environ(), nil)
	script := record("during") + "\nkill -INT $PPID; kill -QUIT $PPID; sleep 0.3"
	restore := catchInterrupts()
	status, err := runFzf("/bin/sh", []string{"-c", script}, os.Environ(), nil)
	restore()
	fmt.Printf("survived fzf\n")
	if status != 0 || err != nil {
		fmt.Printf("sh: %d, %v\n", status, err)
	}
	syscall.Kill(os.Getpid(), syscall.SIGINT)
	time.Sleep(2 * time.Second)
	fmt.Printf("survived SIGINT\n")
}
