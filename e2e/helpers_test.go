package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/schematest"
)

// binary is ftask built for the tests, with the e2e_hooks test hooks;
// release is ftask built as shipped, without them.
var binary, release string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ftask-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "ftask")
	release = filepath.Join(dir, "ftask-release")
	for out, tags := range map[string][]string{binary: {"-tags", "e2e_hooks"}, release: nil} {
		build := exec.Command("go", append(append([]string{"build"}, tags...), "-o", out, "../cmd/ftask")...)
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "building ftask:", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// ftask prepares the binary with args in its own home and config directory.
// A write waits only briefly for a held lock (FTASK_E2E_LOCK_WAIT), so a busy
// comes quickly; withoutLockWait restores the default.
func ftask(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PATH=" + os.Getenv("PATH"), lockWait}
	return cmd
}

// lockWait is the brief wait ftask gives every command.
const lockWait = "FTASK_E2E_LOCK_WAIT=100ms"

// withoutLockWait drops lockWait from cmd's environment, so it waits for a
// held lock as a real ftask does.
func withoutLockWait(cmd *exec.Cmd) *exec.Cmd {
	cmd.Env = slices.DeleteFunc(cmd.Env, func(v string) bool { return v == lockWait })
	return cmd
}

// configDirs is the config directory, relative to the home ftask gives a
// command, after each parent init creates for it (design-spec.md, Config
// file); configDir is the last of them.
var configDirs = func() []string {
	if runtime.GOOS == "darwin" {
		return []string{"Library", "Library/Application Support", "Library/Application Support/ftask"}
	}
	return []string{".config", ".config/ftask"}
}()

var configDir = configDirs[len(configDirs)-1]

// envHome is the home directory ftask gives cmd.
func envHome(cmd *exec.Cmd) string {
	for _, kv := range cmd.Env {
		if h, ok := strings.CutPrefix(kv, "HOME="); ok {
			return h
		}
	}
	return ""
}

type result struct {
	code           int
	stdout, stderr string
}

func run(t *testing.T, cmd *exec.Cmd) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if cmd.Stdout == nil {
		cmd.Stdout = &stdout
	}
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return result{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

// envelope checks that r delivered exactly one complete envelope line, as
// every exit 0, 1, or 2 must, and stderr's one line to match (cli-spec.md,
// Output).
func envelope(t *testing.T, r result) {
	t.Helper()
	if strings.Count(r.stdout, "\n") != 1 || !strings.HasSuffix(r.stdout, "\n") {
		t.Fatalf("exit %d: not one line: %q", r.code, r.stdout)
	}
	if ok, f := schematest.Check(t, "envelope", []byte(r.stdout)); !ok {
		t.Errorf("envelope schema rejects at %s: %s", f, r.stdout)
	}
	if want := note(t, r.stdout); r.stderr != want {
		t.Errorf("stderr %q, want %q", r.stderr, want)
	}
}

// note is the stderr line the envelope in stdout calls for: the error's kind
// and message, or a count of warnings; "" for a clean success. A message
// with a control character in it is left to TestStderr.
func note(t *testing.T, stdout string) string {
	t.Helper()
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
		Warnings []any `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatal(err)
	}
	switch n := len(env.Warnings); {
	case env.Error.Kind == "usage":
		return "ftask: usage: " + strings.TrimPrefix(env.Error.Message, "usage: ") + "\n"
	case !env.OK:
		return "ftask: " + env.Error.Kind + ": " + env.Error.Message + "\n"
	case n == 1:
		return "ftask: 1 warning (see .warnings in the output)\n"
	case n > 1:
		return fmt.Sprintf("ftask: %d warnings (see .warnings in the output)\n", n)
	}
	return ""
}

// tree is a root that init created at ~/tasks in a home of its own.
type tree struct {
	t    *testing.T
	env  []string
	home string
}

func newTree(t *testing.T) *tree {
	t.Helper()
	cmd := ftask(t, "init", "~/tasks")
	r := run(t, cmd)
	envelope(t, r)
	if r.code != 0 {
		t.Fatalf("init: exit %d: %s", r.code, r.stdout)
	}
	return &tree{t: t, env: cmd.Env, home: envHome(cmd)}
}

// root is the tree's root directory.
func (tr *tree) root() string { return filepath.Join(tr.home, "tasks") }

// cmd prepares the binary with args, in the tree's environment plus extra.
func (tr *tree) cmd(args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Env = append([]string(nil), tr.env...)
	return cmd
}

// step is one command in a sequence, and the exit code and output fragment
// it must give.
type step struct {
	cmd  *exec.Cmd
	code int
	want string
}

// steps runs each step in turn, stopping at the first that fails.
func steps(t *testing.T, ss []step) {
	t.Helper()
	for _, s := range ss {
		r := run(t, s.cmd)
		envelope(t, r)
		if r.code != s.code || !strings.Contains(r.stdout, s.want) {
			t.Fatalf("%q: exit %d, want %d and %s: %s", s.cmd.Args[1:], r.code, s.code, s.want, r.stdout)
		}
	}
}

// stdin gives cmd s as its standard input.
func stdin(cmd *exec.Cmd, s string) *exec.Cmd {
	cmd.Stdin = strings.NewReader(s)
	return cmd
}

// holder is a write paused with the lock held (FTASK_E2E_HOLD).
type holder struct {
	t      *testing.T
	cmd    *exec.Cmd
	pw     *os.File
	stdout bytes.Buffer
	stderr bytes.Buffer
	done   chan struct{}
}

// hold starts cmd with FTASK_E2E_HOLD and returns once it holds the lock.
// extra is added to its environment.
func hold(t *testing.T, cmd *exec.Cmd, extra ...string) *holder {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	h := &holder{t: t, cmd: cmd, pw: pw, done: make(chan struct{})}
	cmd.Env = append(append(cmd.Env, "FTASK_E2E_HOLD=1"), extra...)
	cmd.ExtraFiles = []*os.File{pr}
	cmd.Stdout = &h.stdout
	se, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pr.Close()
	t.Cleanup(func() {
		h.release()
		cmd.Process.Kill()
		<-h.done
	})
	br := bufio.NewReader(se)
	line, err := br.ReadString('\n')
	go func() {
		io.Copy(&h.stderr, br)
		cmd.Wait()
		close(h.done)
	}()
	if line != "held\n" {
		t.Fatalf("holder did not hold: %q %v", line, err)
	}
	return h
}

// release lets the holder go on.
func (h *holder) release() { h.pw.Close() }

// wait waits for the holder to exit, returning its result; stderr is what
// followed "held".
func (h *holder) wait() result {
	h.t.Helper()
	<-h.done
	return result{code: h.cmd.ProcessState.ExitCode(), stdout: h.stdout.String(), stderr: h.stderr.String()}
}
