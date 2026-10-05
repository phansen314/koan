package pick

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/phansen314/koan/internal/errs"
)

// MinFzf is the oldest fzf pick runs with (pick-spec.md, Requirements).
var MinFzf = version{0, 63, 0}

// System is what pick needs from the process besides ops.Env: finding and
// running programs. Tests replace it.
type System struct {
	LookPath func(file string) (string, error)
	// Output runs path with args and env (a list of KEY=value), and returns
	// its stdout, its stderr, and its exit status; err only when it could
	// not be started or did not exit normally.
	Output  func(path string, args, env []string) (stdout, stderr []byte, status int, err error)
	Environ func() []string
	// Executable is the absolute path of this koan binary, which fzf's
	// callbacks run.
	Executable func() (string, error)
	// OpenTTY checks that /dev/tty opens for reading and writing.
	OpenTTY func() error
	// RunFzf runs the picker: path with args and env, stdin from stdin,
	// stdout to /dev/null, and stderr to this process's, where fzf reports
	// its own problems. It returns fzf's exit status; err only when fzf
	// could not be started or did not exit normally.
	RunFzf func(path string, args, env []string, stdin []byte) (status int, err error)
	// Filter runs fzf --filter: path with args and env, stdin from stdin,
	// stderr to this process's, and returns its stdout and exit status; err
	// only when fzf could not be started or did not exit normally.
	Filter func(path string, args, env []string, stdin []byte) (stdout []byte, status int, err error)
	// RunSource runs a live source's command (see runSource): its stdout,
	// its stderr, and whether limit passed first; err only when it could
	// not be started.
	RunSource func(command string, env []string, limit time.Duration) (stdout, stderr []byte, timedOut bool, err error)
	// RunEditor runs argv with env, with this process's stdin, stdout and
	// stderr, the terminal fzf gives execute; it returns the exit status,
	// 128+n if a signal n killed it, err only when it could not be
	// started.
	RunEditor func(argv, env []string) (status int, err error)
	// CatchInterrupts catches SIGINT and SIGQUIT and discards them, until
	// the function it returns restores default handling (pick-spec.md,
	// Signals).
	CatchInterrupts func() (restore func())
}

// OSSystem is the process's own.
func OSSystem() System {
	return System{
		LookPath:   exec.LookPath,
		Output:     output,
		Environ:    os.Environ,
		Executable: os.Executable,
		OpenTTY:    openTTY,
		RunFzf:     runFzf,
		Filter:     filter,
		RunEditor:  runEditor,
		RunSource:  runSource,

		CatchInterrupts: catchInterrupts,
	}
}

func filter(path string, args, env []string, stdin []byte) ([]byte, int, error) {
	var out bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, bytes.NewReader(stdin), &out, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return out.Bytes(), exit.ExitCode(), nil
	}
	return out.Bytes(), 0, err
}

// catchInterrupts catches the signals rather than ignoring them: an ignored
// signal stays ignored in every program fzf starts (the editor, a source
// command), while a caught one is reset to default by exec.
func catchInterrupts() func() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-c:
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Reset(syscall.SIGINT, syscall.SIGQUIT)
		close(done)
	}
}

func openTTY() error {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

func runFzf(path string, args, env []string, stdin []byte) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Env, cmd.Stdin, cmd.Stderr = env, bytes.NewReader(stdin), os.Stderr
	// Stdout nil is /dev/null: the selection never comes from fzf's output.
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return exit.ExitCode(), nil
	}
	return 0, err
}

func runEditor(argv, env []string) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// Killed by a signal, e.g. ctrl-c: 128+n, as a shell reports it.
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return exit.ExitCode(), nil
	}
	return 0, err
}

func output(path string, args, env []string) ([]byte, []byte, int, error) {
	var out, errOut bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return out.Bytes(), errOut.Bytes(), exit.ExitCode(), nil
	}
	return out.Bytes(), errOut.Bytes(), 0, err
}

// UnavailableReason is why the picker cannot run.
type UnavailableReason string

const (
	NoTerminal UnavailableReason = "no-terminal"
	FzfMissing UnavailableReason = "fzf-missing"
	FzfTooOld  UnavailableReason = "fzf-too-old"
	FzfFailed  UnavailableReason = "fzf-failed"
)

// UnavailableDetails is unavailable's details (pick-error-details).
type UnavailableDetails struct {
	Reason   UnavailableReason `json:"reason"`
	Found    *string           `json:"found,omitempty"`
	Required string            `json:"required,omitempty"`
	Status   *int              `json:"status,omitempty"`
	Actions  []any             `json:"actions,omitzero"` // [] when fzf ran: present, if empty
}

func unavailable(msg string, d UnavailableDetails) *errs.Error {
	return &errs.Error{Kind: errs.KindUnavailable, Message: msg, Details: d}
}

// findFzf returns the path of an fzf on PATH whose version is MinFzf or
// later, or unavailable. The version is asked for without the person's
// FZF_DEFAULT_OPTS and FZF_DEFAULT_OPTS_FILE, since a bad option in either
// makes fzf --version fail: a bad option is reported where fzf really
// starts, as fzf-failed.
func findFzf(sys System) (string, *errs.Error) {
	need := MinFzf.String()
	path, err := sys.LookPath("fzf")
	if err != nil {
		return "", unavailable("fzf not found on PATH: pick needs fzf "+need+" or later", UnavailableDetails{Reason: FzfMissing})
	}
	out, errOut, status, err := sys.Output(path, []string{"--version"}, withoutOpts(sys.Environ()))
	switch {
	case err != nil:
		return "", unavailable(fmt.Sprintf("fzf --version failed: %v", err), UnavailableDetails{Reason: FzfFailed, Actions: []any{}})
	case status != 0:
		msg := fmt.Sprintf("fzf --version exited with status %d", status)
		if line := firstLine(errOut); line != "" {
			msg += ": " + line
		}
		return "", unavailable(msg, UnavailableDetails{Reason: FzfFailed, Status: &status, Actions: []any{}})
	}
	found, v, ok := parseVersion(out)
	d := UnavailableDetails{Reason: FzfTooOld, Found: &found, Required: need}
	switch {
	case !ok:
		return "", unavailable(fmt.Sprintf("fzf --version printed %q, not a version: pick needs fzf %s or later", found, need), d)
	case v.less(MinFzf):
		return "", unavailable(fmt.Sprintf("fzf %s is too old: pick needs fzf %s or later", found, need), d)
	}
	return path, nil
}

// withoutOpts is env without FZF_DEFAULT_OPTS and FZF_DEFAULT_OPTS_FILE.
// It is never nil, which exec would take as the whole of this process's
// environment, options included.
func withoutOpts(env []string) []string {
	out := []string{}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "FZF_DEFAULT_OPTS=") && !strings.HasPrefix(kv, "FZF_DEFAULT_OPTS_FILE=") {
			out = append(out, kv)
		}
	}
	return out
}

type version [3]int

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func (v version) less(w version) bool {
	for i := range v {
		if v[i] != w[i] {
			return v[i] < w[i]
		}
	}
	return false
}

// parseVersion reads fzf --version's output: the first whitespace-separated
// word, without any suffix from the first "-" ("0.75.0-dev" is 0.75.0), as
// three numbers. found is that word, or, when it doesn't parse, the first
// line as printed.
func parseVersion(out []byte) (found string, v version, ok bool) {
	words := strings.Fields(string(out))
	if len(words) == 0 {
		return firstLine(out), v, false
	}
	core, _, _ := strings.Cut(words[0], "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return firstLine(out), v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || p[0] == '+' {
			return firstLine(out), v, false
		}
		v[i] = n
	}
	return words[0], v, true
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSuffix(line, "\r")
}
