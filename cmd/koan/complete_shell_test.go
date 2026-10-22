package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/phansen314/koan/internal/testrun"
)

// shell is an interactive shell in a pseudo-terminal, with koan's
// completion script loaded and Ctrl-T bound to print the line being
// edited between < and >.
type shell struct {
	t      *testing.T
	cmd    *exec.Cmd
	master *os.File
	mu     sync.Mutex
	screen bytes.Buffer
	seen   int // how much of the screen expect has passed
}

// startShell runs argv in a pseudo-terminal, with HOME set to home and
// koan first on PATH, and waits for its first prompt.
func startShell(t *testing.T, home string, env []string, argv ...string) *shell {
	t.Helper()
	master, tty, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 50, Col: 200})
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = home
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + filepath.Dir(testrun.Binary(t)) + ":" + os.Getenv("PATH"),
		"TERM=xterm-256color", "LANG=C.UTF-8"}, env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tty.Close()
	s := &shell{t: t, cmd: cmd, master: master}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			s.mu.Lock()
			s.screen.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		master.Close()
	})
	s.expect("READY")
	return s
}

// escapes are a terminal's control sequences, which come between what a
// shell draws.
var escapes = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][0-9A-Za-z]|\x1b[=>]|\r`)

// expect waits until the screen, since what the last expect passed,
// shows each of want, in order.
func (s *shell) expect(want ...string) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		raw := s.screen.Bytes()[s.seen:]
		shown := escapes.ReplaceAllString(string(raw), "")
		s.mu.Unlock()
		at, found := 0, true
		for _, w := range want {
			i := strings.Index(shown[at:], w)
			if i < 0 {
				found = false
				break
			}
			at += i + len(w)
		}
		if found {
			s.mu.Lock()
			s.seen = s.screen.Len()
			s.mu.Unlock()
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("the shell never showed %q:\n%s", want, shown)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *shell) send(keys string) {
	s.t.Helper()
	if _, err := s.master.Write([]byte(keys)); err != nil {
		s.t.Fatal(err)
	}
}

// line clears the line, types keys, then asks for the line as edited.
func (s *shell) line(keys, want string) {
	s.t.Helper()
	s.send("\x15" + keys)
	time.Sleep(300 * time.Millisecond)
	s.send("\x14")
	s.expect("<" + want + ">")
}

// list clears the line, types keys, and waits for each of want on the
// screen.
func (s *shell) list(keys string, want ...string) {
	s.t.Helper()
	s.send("\x15" + keys)
	s.expect(want...)
}

// bashCompletion is the bash-completion package's script, where Linux
// and Homebrew put it, or "" when it isn't installed.
func bashCompletion() string {
	for _, p := range []string{
		"/usr/share/bash-completion/bash_completion",
		"/opt/homebrew/share/bash-completion/bash_completion",
		"/usr/local/share/bash-completion/bash_completion",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// A real bash, with bash-completion, completes a path folder by folder,
// a gate after a path, an ID with its title, and shows the hint (PRD
// Testing, Completion).
func TestCompleteBash(t *testing.T) {
	if testing.Short() {
		t.Skip("drives a real bash")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	// KeepOrder needs bash 4.4 or later.
	out, err := exec.Command(bash, "-c", `echo $(( BASH_VERSINFO[0] * 100 + BASH_VERSINFO[1] ))`).Output()
	if v, _ := strconv.Atoi(strings.TrimSpace(string(out))); err != nil || v < 404 {
		t.Skipf("bash on PATH is older than 4.4: %q", out)
	}
	bc := bashCompletion()
	if bc == "" {
		t.Skip("no bash-completion package")
	}
	start := func(home string) *shell {
		rc := filepath.Join(t.TempDir(), "bashrc")
		os.WriteFile(rc, []byte(`PS1='$ '
source `+bc+`
source <(koan completion bash)
bind -x '"\C-t": printf "<%s>\n" "$READLINE_LINE"'
echo READY
`), 0o644)
		return startShell(t, home, nil, bash, "--noprofile", "--rcfile", rc, "-i")
	}

	s := start(completeTree(t))
	s.line("koan task show /wo\t", "koan task show /work")
	s.line("koan task show /work/api.d\t", "koan task show /work/api.done")
	s.list("koan task show /work/api.\t\t", "/work/api.start", "/work/api.done")
	s.line("koan task show @3.s\t", "koan task show @3.start ")
	s.line("koan task show 1\t", "koan task show 12 ")
	s.list("koan task complete @\t\t", "@9", "Root task  /", "@3.done", "Done of /work/api")
	s.line("koan task block 5 --blocked-by /work.\t", "koan task block 5 --blocked-by /work.")
	s.list("koan task block 5 --blocked-by /work.\t\t", "/work.start", "/work.done")

	s = start(manyTree(t))
	s.list("koan task show \t\t", "101 match: type more, or Alt-K to pick")
}

// A real zsh does the same.
func TestCompleteZsh(t *testing.T) {
	if testing.Short() {
		t.Skip("drives a real zsh")
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("no zsh on PATH")
	}
	start := func(home string) *shell {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(`PS1='$ '
autoload -U compinit
compinit -u -D
source <(koan completion zsh)
show-line() { zle -I; print -r -- "<$BUFFER>" }
zle -N show-line
bindkey '^T' show-line
bindkey '^U' backward-kill-line
echo READY
`), 0o644)
		return startShell(t, home, []string{"ZDOTDIR=" + dir}, zsh, "-d", "-i")
	}

	s := start(completeTree(t))
	s.line("koan task show /wo\t", "koan task show /work")
	s.line("koan task show /work/api.d\t", "koan task show /work/api.done")
	s.list("koan task show /work/api.\t", "/work/api.start", "/work/api.done")
	s.line("koan task show @3.s\t", "koan task show @3.start ")
	s.line("koan task show 1\t", "koan task show 12 ")
	s.list("koan task complete @\t", "@9", "Root task  /", "@3.done", "Done of /work/api")
	s.list("koan task block 5 --blocked-by /work.\t", "/work.start", "/work.done")

	s = start(manyTree(t))
	s.list("koan task show \t", "101 match: type more, or Alt-K to pick")
}
