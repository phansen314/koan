package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realDir is a new directory outside any tree, at its real path.
func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// okIn runs koan in dir and requires success.
func okIn(t *testing.T, dir, home string, args ...string) map[string]any {
	t.Helper()
	r := koanIn(t, dir, home, "", nil, args...)
	if r.exit != 0 || r.env["ok"] != true {
		t.Fatalf("koan %v in %s: exit %d: %s%s", args, dir, r.exit, r.stdout, r.stderr)
	}
	return r.result()
}

// The marker commands and context take the working directory when given no
// directory, and git leaves a bound worktree clean.
func TestMarkersInAWorktree(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	home := newHome(t)
	ok(t, home, "folder", "create", "/work/story", "--notes", "story")
	ok(t, home, "folder", "create", "/work/story/piece", "--notes", "piece") // 3
	ok(t, home, "task", "create", "Do it", "--folder", "/work/story/piece")

	repo, wt := realDir(t), filepath.Join(realDir(t), "wt")
	gitEnv := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	sh := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir, cmd.Env = dir, gitEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	sh(repo, "init", "-q", ".")
	sh(repo, "commit", "-q", "--allow-empty", "-m", "init")
	sh(repo, "worktree", "add", "-q", wt)

	if r := koanIn(t, repo, home, "", nil, "marker", "bind", "/work/story/piece"); r.err()["rule"] != "checkout" {
		t.Errorf("bind the checkout: %s", r.stdout)
	}
	if got := okIn(t, wt, home, "marker", "bind", "/work/story/piece"); got["dir"] != wt {
		t.Errorf("bind: %v", got)
	}
	if out := sh(wt, "status", "--porcelain", "--ignored=no"); out != "" {
		t.Errorf("git status: %q", out)
	}
	sub := filepath.Join(wt, "src", "deep")
	os.MkdirAll(sub, 0o755)
	if got := okIn(t, sub, home, "where", "--file", "."); got["ref"] != "@3" {
		t.Errorf("where: %v", got)
	}
	got := okIn(t, sub, home, "context")
	if got["folder"].(map[string]any)["path"] != "/work/story/piece" || got["notes"] != "piece" || got["parent_notes"] != "story" ||
		len(got["tasks"].([]any)) != 1 {
		t.Errorf("context: %v", got)
	}
	if got := okIn(t, sub, home, "context", "/work/story"); got["marker"] != nil || got["notes"] != "story" {
		t.Errorf("context of a folder: %v", got)
	}
	sh(repo, "worktree", "remove", wt)

	// unbind, in the working directory.
	d := realDir(t)
	okIn(t, d, home, "marker", "bind", "@3")
	if got := okIn(t, d, home, "marker", "unbind"); got["changed"] != true {
		t.Errorf("unbind: %v", got)
	}
	if _, err := os.Lstat(filepath.Join(d, ".koan")); !os.IsNotExist(err) {
		t.Errorf(".koan left: %v", err)
	}
}

// A marker bind killed before any of its steps leaves the old marker, the
// new one, or no marker at all, never part of one; the next bind completes
// it, and unbind removes everything.
func TestMarkerBindCrashes(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		home := newHome(t)
		ok(t, home, "folder", "create", "/a") // 1
		ok(t, home, "folder", "create", "/b") // 2
		bind := []string{"marker", "bind", "/b", "--force", "--dir"}
		count := filepath.Join(t.TempDir(), "count")
		probe := realDir(t)
		if !fresh {
			ok(t, home, "marker", "bind", "/a", "--dir", probe)
		}
		if r := koan(t, home, "", []string{"KOAN_E2E_COUNT_FILE=" + count}, append(bind, probe)...); r.exit != 0 {
			t.Fatalf("count: %s", r.stdout)
		}
		data, _ := os.ReadFile(count)
		steps := len(data)
		if steps < 3 {
			t.Fatalf("marker bind made %d changes", steps)
		}
		for k := 1; k <= steps; k++ {
			dir := realDir(t)
			if !fresh {
				ok(t, home, "marker", "bind", "/a", "--dir", dir)
			}
			r := koan(t, home, "", []string{fmt.Sprintf("KOAN_E2E_CRASH_BEFORE=%d", k)}, append(bind, dir)...)
			if !r.killed {
				t.Fatalf("fresh=%v k=%d: not killed: %s", fresh, k, r.stdout)
			}
			what := fmt.Sprintf("fresh=%v, killed before change %d", fresh, k)
			w := koan(t, home, "", nil, "where", "--file", dir)
			switch {
			case w.exit == 0 && (w.result()["ref"] == "@2" || (!fresh && w.result()["ref"] == "@1")):
			case w.exit != 0 && fresh && w.err()["kind"] == "not-found":
			default:
				t.Errorf("%s: where: %s", what, w.stdout)
			}
			if got := ok(t, home, append(bind, dir)...); got["id"] != 2.0 {
				t.Errorf("%s: bind again: %v", what, got)
			}
			if got := ok(t, home, "marker", "unbind", "--dir", dir); !strings.HasSuffix(fmt.Sprint(got["removed"]), ".koan]") {
				t.Errorf("%s: unbind: %v", what, got)
			}
			if _, err := os.Lstat(filepath.Join(dir, ".koan")); !os.IsNotExist(err) {
				t.Errorf("%s: .koan left: %v", what, err)
			}
		}
	}
}
