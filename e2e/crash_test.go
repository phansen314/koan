package e2e

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// crashCases are the write commands, each run on the tree that setup makes.
// For every one, and every change it makes to the disk, the process is
// killed just before that change, or the change is failed with an errno
// (spec/implementation.md, Testing).
var crashCases = []struct {
	name string
	args []string
}{
	{freshInit, []string{"init"}},
	{"create", []string{"create", "new", "--folder", "/fresh/deep", "--blocked-by", "1", "--notes", "n"}},
	{"create-batch", []string{"create-batch", "-i", "-"}},
	{"create-folder", []string{"create-folder", "/x/y/z"}},
	{"move-folder", []string{"move-folder", "/proj", "--to", "/moved/proj"}},
	{"delete-folder", []string{"delete-folder", "/proj", "--recursive"}},
	{"update", []string{"update", "2", "--title", "renamed", "--tags-add", "t"}},
	{"done", []string{"done", "3"}},
	{"reopen", []string{"reopen", "4"}},
	{"block", []string{"block", "1", "--blockers", "4"}},
	{"unblock", []string{"unblock", "2", "--blockers", "1"}},
	{"move", []string{"move", "3", "--to", "/elsewhere"}},
	{"delete", []string{"delete", "1"}},
	{"claim", []string{"claim", "1"}},
	{"claim-next", []string{"claim-next"}},
	{"release", []string{"release", "3"}},
	{"repair", []string{"repair"}},
	{"repair by name", []string{"repair", "--kinds", "dangling-reference,stale-claim"}},
}

const batchInput = `{"tasks":[{"ref":"a","title":"A","folder":"/b1","notes":"n"},{"title":"B","folder":"/b2","blocked_by":["a",1]}]}`

// freshInit marks the case that runs on a HOME where nothing exists yet.
const freshInit = "init on a new home"

// setup makes a tree with something for every command to act on: task 1 in
// the root; 2 and 3 in /proj, 2 waiting on 1 and 3 claimed with notes; and
// 4, done, in /proj/sub. For repair, 1 is claimed and its task file is gone,
// leaving a dangling reference, a claim on a task that is gone, and notes
// without their task.
func setup(t *testing.T, name string) string {
	t.Helper()
	if name == freshInit {
		home := t.TempDir()
		os.Mkdir(filepath.Join(home, ".config"), 0o755) // as every real home has
		return home
	}
	home := newHome(t)
	ok(t, home, "create", "one")
	ok(t, home, "create", "two", "--folder", "/proj", "--blocked-by", "1")
	ok(t, home, "create", "three", "--folder", "/proj", "--notes", "notes")
	ok(t, home, "create", "four", "--folder", "/proj/sub")
	ok(t, home, "done", "4")
	ok(t, home, "claim", "3")
	if strings.HasPrefix(name, "repair") {
		ok(t, home, "claim", "1")
		os.Remove(filepath.Join(home, "koan-root", "1.json"))
	}
	return home
}

// leftovers are the findings a killed or failed command may leave, as
// spec/design.md lists them: a temp file, a reference to a deleted task, a
// claim on a task that is done or gone, and notes without their task. Every
// one is something repair fixes, the reference, and a claim on a task that is
// gone, only when asked for by name.
// An ID used with no task is not a finding.
var leftovers = []string{"temp-leftover", "dangling-reference", "stale-claim", "orphan-notes"}

// checkAfter requires what the design promises after a crash or a failure:
// the next write succeeds, doctor reports nothing but the expected kinds,
// and repair leaves nothing it could have fixed.
func checkAfter(t *testing.T, home, what string) {
	t.Helper()
	// Only init itself can leave the root half made, and init finishes it.
	if what0 := koan(t, home, "", nil, "info").result()["state"]; what0 != "usable" {
		if r := koan(t, home, "", nil, "init"); r.exit != 0 {
			t.Fatalf("%s: left the root %v, and init afterwards failed: %s", what, what0, r.stdout)
		}
	}
	if r := koan(t, home, "", nil, "create", "after"); r.exit != 0 {
		t.Fatalf("%s: the next write failed: %s", what, r.stdout)
	}
	for _, f := range ok(t, home, "doctor")["findings"].([]any) {
		f := f.(map[string]any)
		if !slices.Contains(leftovers, f["kind"].(string)) {
			t.Errorf("%s: doctor found %v", what, f)
		}
	}
	for _, f := range ok(t, home, "repair")["findings"].([]any) {
		if f := f.(map[string]any); f["kind"] != "dangling-reference" && f["kind"] != "stale-claim" {
			t.Errorf("%s: repair left %v", what, f)
		}
	}
	if left := ok(t, home, "repair", "--kinds", "dangling-reference,stale-claim")["findings"].([]any); len(left) != 0 {
		t.Errorf("%s: repair left %v", what, left)
	}
	if got := ok(t, home, "doctor"); got["healthy"] != true {
		t.Errorf("%s: not healthy after repair: %v", what, got)
	}
}

func stdinFor(args []string) string {
	if args[0] == "create-batch" {
		return batchInput
	}
	return ""
}

// changes counts the changes a command makes to the disk on the setup tree.
func changes(t *testing.T, name string, args []string) int {
	t.Helper()
	home := setup(t, name)
	count := filepath.Join(t.TempDir(), "count")
	if r := koan(t, home, stdinFor(args), []string{"KOAN_E2E_COUNT_FILE=" + count}, args...); r.exit != 0 {
		t.Fatalf("koan %v on the setup tree: %s", args, r.stdout)
	}
	fi, err := os.Stat(count)
	if err != nil {
		return 0
	}
	return int(fi.Size())
}

// snapshot maps every path under home to its content; a directory to "dir".
func snapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil || p == home || strings.Contains(p, ".koan-tmp-"):
		case d.IsDir():
			snap[p] = "dir"
		default:
			data, _ := os.ReadFile(p)
			snap[p] = "file:" + string(data)
		}
		return nil
	})
	return snap
}

func TestCrashes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every write command once per disk change")
	}
	for _, c := range crashCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			n := changes(t, c.name, c.args)
			if n == 0 {
				t.Fatal("the command changed nothing on disk")
			}
			for k := 1; k <= n; k++ {
				home := setup(t, c.name)
				r := koan(t, home, stdinFor(c.args), []string{fmt.Sprintf("KOAN_E2E_CRASH_BEFORE=%d", k)}, c.args...)
				if !r.killed {
					t.Fatalf("not killed before change %d of %d: %s", k, n, r.stdout)
				}
				checkAfter(t, home, fmt.Sprintf("killed before change %d of %d", k, n))
			}
		})
	}
}

// A change that fails must fail the command with io, unless it is one koan
// may ignore, and the error must name everything that did take effect.
func TestFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every write command once per disk change")
	}
	for _, c := range crashCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			n := changes(t, c.name, c.args)
			for k := 1; k <= n; k++ {
				home := setup(t, c.name)
				before := snapshot(t, home)
				r := koan(t, home, stdinFor(c.args), []string{fmt.Sprintf("KOAN_E2E_ERRNO_BEFORE=%d", k)}, c.args...)
				what := fmt.Sprintf("change %d of %d failed", k, n)
				switch {
				case r.exit == 0:
					// A failure koan may ignore: a directory flush, or the
					// removal of a temp file or a folder renamed aside.
				case r.exit == 1 && r.err()["kind"] == "io":
					after := snapshot(t, home)
					var reported []string
					for _, p := range r.err()["changed_paths"].([]any) {
						reported = append(reported, p.(string))
					}
					for p := range allKeys(before, after) {
						if before[p] != after[p] && !slices.ContainsFunc(reported, func(c string) bool {
							return p == c || strings.HasPrefix(p, c+"/")
						}) {
							t.Errorf("%s: %s changed, but changed_paths is %v", what, p, reported)
						}
					}
				default:
					t.Fatalf("%s: exit %d: %s%s", what, r.exit, r.stdout, r.stderr)
				}
				checkAfter(t, home, what)
			}
		})
	}
}

func allKeys(a, b map[string]string) map[string]bool {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	return keys
}
