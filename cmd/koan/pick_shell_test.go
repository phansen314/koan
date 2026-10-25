package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/testrun"
)

// pickShell starts a shell with the stand-in fzf first on PATH, picking
// @9, the root task in completeTree.
func pickShell(t *testing.T, argv func(rc string) []string, rc, rcName string) *shell {
	t.Helper()
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "fzf"), []byte(standIn), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, rcName), []byte(rc), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"ZDOTDIR=" + dir, "FAKE_FZF_DIR=" + t.TempDir(), "FAKE_FZF_PICK=@9",
		"PATH=" + fake + ":" + filepath.Dir(testrun.Binary(t)) + ":" + os.Getenv("PATH")}
	return startShell(t, completeTree(t), env, argv(filepath.Join(dir, rcName))...)
}

// edit moves to the end of the line, clears it, types keys, then asks for
// the line as edited.
func (s *shell) edit(keys, want string) {
	s.t.Helper()
	s.send("\x05\x15" + keys)
	time.Sleep(300 * time.Millisecond)
	s.send("\x14")
	s.expect("<" + want + ">")
}

// altK presses Alt-K in s, bound to pick's widget: a pick is typed in
// at the cursor, at the end of the line or in the middle of it, and a
// cancel, nothing matched, or a koan that prints and then fails leaves
// the line as it was (spec pick, --bash and --zsh).
func altK(s *shell) {
	s.t.Helper()
	s.edit("koan show \x1bk", "koan show @9")
	s.edit("echo ab\x02\x1bkX", "echo a@9Xb")
	for _, exit := range []string{"130", "1"} {
		s.send("\x05\x15export FAKE_FZF_PICK= FAKE_FZF_EXIT=" + exit + "; echo SE''T\r")
		s.expect("SET")
		s.edit("echo ab\x02\x1bk", "echo ab")
	}
	s.send("\x05\x15koan() { echo @1; return 2; }; echo DE''FINED\r")
	s.expect("DEFINED")
	s.edit("echo ab\x02\x1bk", "echo ab")
}

// Alt-K in a real bash, 4.0 or later, bound to pick --bash's widget.
func TestPickAltKBash(t *testing.T) {
	if testing.Short() {
		t.Skip("drives a real bash")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	out, err := exec.Command(bash, "-c", `echo ${BASH_VERSINFO[0]}`).Output()
	if v, _ := strconv.Atoi(strings.TrimSpace(string(out))); err != nil || v < 4 {
		t.Skipf("bash on PATH is older than 4.0: %q", out)
	}
	s := pickShell(t, func(rc string) []string { return []string{bash, "--noprofile", "--rcfile", rc, "-i"} }, `PS1='$ '
eval "$(koan pick --bash)"
bind -x '"\ek": __koan_pick_widget'
bind -x '"\C-t": printf "<%s>\n" "$READLINE_LINE"'
echo READY
`, "bashrc")
	altK(s)
}

// Alt-K in a real zsh, bound to pick --zsh's widget.
func TestPickAltKZsh(t *testing.T) {
	if testing.Short() {
		t.Skip("drives a real zsh")
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("no zsh on PATH")
	}
	s := pickShell(t, func(string) []string { return []string{zsh, "-d", "-i"} }, `PS1='$ '
eval "$(koan pick --zsh)"
bindkey '\ek' __koan_pick_widget
show-line() { zle -I; print -r -- "<$BUFFER>" }
zle -N show-line
bindkey '^T' show-line
bindkey '^U' backward-kill-line
echo READY
`, ".zshrc")
	altK(s)
}
