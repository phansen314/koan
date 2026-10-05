package pick

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/ops"
)

// A live source (pick-spec.md, Live source): --source's command runs for
// the first load and again on every reload, and its envelope's IDs are the
// candidates. Task data still comes from pick's own load, so a reload reads
// the tree twice: once in the command, once in the load.

// sourceLimit is how long a reload's run of the source may take; the
// first run, before fzf opens, has none.
const sourceLimit = 10 * time.Second

// sourceFailed is the kind a reload's failed source run is reported under,
// in the status line only, as ✗ source: …; it never leaves pick.
const sourceFailed errs.Kind = "source"

// sourceIDs runs the source command with limit (none if 0) and returns the
// IDs its envelope names; reason says why there are none: the upstream
// error's kind and message, else the first line of the command's stderr,
// else why its output is no envelope.
func sourceIDs(env Env, command string, limit time.Duration) (ids []model.ID, reason string) {
	environ := []string{}
	for _, kv := range env.Sys.Environ() {
		if !strings.HasPrefix(kv, SessionVar+"=") {
			environ = append(environ, kv)
		}
	}
	stdout, stderr, timedOut, err := env.Sys.RunSource(command, environ, limit)
	switch {
	case timedOut:
		return nil, fmt.Sprintf("timed out after %s", limit)
	case err != nil:
		return nil, "could not run: " + err.Error()
	}
	raw, reason := ops.EnvelopeIDs(stdout)
	if reason != "" {
		line, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n")
		if !strings.HasPrefix(reason, "upstream failed with ") && line != "" {
			return nil, line
		}
		return nil, reason
	}
	ids = []model.ID{}
	for _, v := range raw {
		n, ok := v.(json.Number)
		id, err := strconv.ParseInt(string(n), 10, 64)
		if !ok || err != nil || id < 1 {
			b, _ := json.Marshal(v)
			return nil, "not a task ID: " + string(b)
		}
		ids = append(ids, model.ID(id))
	}
	return ids, ""
}

// runSource runs command with sh -c, with env, stdin from /dev/null, and
// its stdout and stderr captured. With a limit, it runs in a process group
// of its own, killed whole when the limit passes; without one, it stays in
// pick's, so that ctrl-c at the terminal interrupts it. Its exit status is
// not looked at: its output says how it went.
func runSource(command string, env []string, limit time.Duration) ([]byte, []byte, bool, error) {
	var out, errOut bytes.Buffer
	cmd := exec.Command("sh", "-c", command)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, &out, &errOut
	if limit > 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		// Output held open by something the kill missed doesn't hold
		// pick up.
		cmd.WaitDelay = time.Second
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, false, err
	}
	var timedOut atomic.Bool
	if limit > 0 {
		t := time.AfterFunc(limit, func() {
			timedOut.Store(true)
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		})
		defer t.Stop()
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if timedOut.Load() || errors.As(err, &exit) || errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	return out.Bytes(), errOut.Bytes(), timedOut.Load(), err
}
