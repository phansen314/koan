package ops

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/cli"
)

// minFzf is the oldest fzf pick runs (spec pick, Requirements).
var minFzf = [3]int{0, 63, 0}

func unavailable(format string, args ...any) *cli.Error {
	return cli.NewError(cli.KindUnavailable, fmt.Sprintf(format, args...))
}

// openTTY checks that /dev/tty opens for reading and writing: pick is for
// a person at a terminal, and fails at once without one.
func openTTY() *cli.Error {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return unavailable("no terminal: /dev/tty doesn't open (%v); pick is for a person at a terminal", err)
	}
	f.Close()
	return nil
}

// findFzf is the path of an fzf on PATH whose version is minFzf or later.
// The version is asked for without FZF_DEFAULT_OPTS and
// FZF_DEFAULT_OPTS_FILE, since a bad option in either makes it fail; a bad
// option is fzf's to report when it runs.
func findFzf() (string, *cli.Error) {
	need := fmt.Sprintf("%d.%d.%d", minFzf[0], minFzf[1], minFzf[2])
	path, err := exec.LookPath("fzf")
	if err != nil {
		return "", unavailable("fzf not found on PATH: pick needs fzf %s or later", need)
	}
	cmd := exec.Command(path, "--version")
	cmd.Env = withoutFzfOpts(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", unavailable("fzf --version failed: %v", err)
	}
	found, v, ok := fzfVersion(out)
	if !ok || slices.Compare(v[:], minFzf[:]) < 0 {
		return "", unavailable("fzf %s is too old: pick needs fzf %s or later", found, need)
	}
	return path, nil
}

// withoutFzfOpts is env without FZF_DEFAULT_OPTS and
// FZF_DEFAULT_OPTS_FILE, never nil, which exec would take as this
// process's whole environment.
func withoutFzfOpts(env []string) []string {
	out := []string{}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "FZF_DEFAULT_OPTS=") && !strings.HasPrefix(kv, "FZF_DEFAULT_OPTS_FILE=") {
			out = append(out, kv)
		}
	}
	return out
}

// fzfVersion reads fzf --version's output: its first word, without any
// suffix from the first "-", as three numbers. found is that word, or the
// first line when there is none.
func fzfVersion(out []byte) (found string, v [3]int, ok bool) {
	words := strings.Fields(string(out))
	if len(words) == 0 {
		return strings.TrimSpace(string(out)), v, false
	}
	core, _, _ := strings.Cut(words[0], "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return words[0], v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || p == "" || p[0] < '0' || p[0] > '9' {
			return words[0], v, false
		}
		v[i] = n
	}
	return words[0], v, true
}

// fzfArgs are fzf's arguments (spec pick, fzf's options): the options
// undone, then pick's own. dir holds the preview headers.
func fzfArgs(dir, query string) []string {
	return []string{
		"--no-select-1", "--no-exit-0", "--no-expect", "--no-tmux", "--no-read0", "--no-header-lines",
		"--no-print0", "--no-print-query", "--no-tac", "--accept-nth", "..",
		"--multi",
		"--delimiter", "\t",
		"--with-nth", "3..",
		"--tiebreak", "index",
		"--with-shell", "sh -c",
		"--preview", "cat -- " + shQuote(dir) + "/{1} {2} 2>/dev/null",
		"--preview-window", "down,50%",
		"--query", query,
	}
}

// shQuote quotes s as one sh word.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runFzf runs fzf on the candidates and gives the keys picked, in the
// candidates' order, and pick's exit: 0 for a pick, 1 when nothing
// matched, 130 for a cancel. Any other outcome is unavailable.
func runFzf(env *cli.Env, fzf string, cands []candidate, query string) ([]string, int, *cli.Error) {
	dir, e := writePreviews(cands)
	if e != nil {
		return nil, 0, e
	}
	defer os.RemoveAll(dir)

	var in bytes.Buffer
	for _, l := range lines(cands) {
		in.WriteString(l + "\n")
	}
	var out bytes.Buffer
	cmd := exec.Command(fzf, fzfArgs(dir, query)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &in, &out, env.Stderr

	// In fzf, ctrl-c is a key: fzf cancels. A SIGINT or SIGQUIT that
	// reaches pick meanwhile is caught and dropped, not ignored, since an
	// ignored signal stays ignored in the preview fzf runs.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGQUIT)
	err := cmd.Run()
	signal.Stop(sig)

	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.Exited():
		switch code := exit.ExitCode(); code {
		case 1, 130:
			return nil, code, nil
		default:
			return nil, 0, unavailable("fzf exited with status %d", code)
		}
	case err != nil:
		return nil, 0, unavailable("fzf failed: %v", err)
	}
	picked := map[string]bool{}
	for _, l := range strings.Split(out.String(), "\n") {
		key, _, _ := strings.Cut(l, "\t")
		picked[key] = true
	}
	var keys []string
	for _, c := range cands {
		if picked[c.key] {
			keys = append(keys, c.key)
		}
	}
	if len(keys) == 0 {
		return nil, 1, nil
	}
	return keys, 0, nil
}

// writePreviews writes each candidate's preview header to a file named by
// its key, in a private directory koan-pick-<random> under
// $XDG_RUNTIME_DIR, or else the system's temporary directory.
func writePreviews(cands []candidate) (string, *cli.Error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return "", cli.IO("could not find a temporary directory: " + err.Error())
	}
	dir, err := os.MkdirTemp(base, "koan-pick-")
	if err != nil {
		return "", cli.IO("could not make a directory for the previews: "+err.Error(), base)
	}
	for _, c := range cands {
		at := filepath.Join(dir, c.key)
		if err := os.WriteFile(at, []byte(c.header), 0o600); err != nil {
			os.RemoveAll(dir)
			return "", cli.IO("could not write a preview: "+err.Error(), at)
		}
	}
	return dir, nil
}
