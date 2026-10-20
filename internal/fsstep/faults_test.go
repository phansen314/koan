package fsstep

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/store"
)

// crashExit is how a helper that crashes at a fault point exits.
const crashExit = 7

// crashHelper runs the scenario's write in the root under home, and
// crashes at fault point at.
func crashHelper(home string, at int) int {
	s, e := Open(home)
	if e != nil {
		fmt.Fprintln(os.Stderr, e.Message)
		return 1
	}
	n := 0
	store.Fault = func(string) error {
		if n == at {
			os.Exit(crashExit)
		}
		n++
		return nil
	}
	if _, _, e := s.Write(change(scenarioSteps...)); e != nil {
		fmt.Fprintln(os.Stderr, e.Message)
		return 1
	}
	return 0
}

// points is every fault point the scenario's write passes, in order.
func points(t *testing.T) []string {
	t.Helper()
	_, s := scenario(t)
	var out []string
	faults(t, func(p string) error { out = append(out, p); return nil })
	write(t, s, scenarioSteps...)
	store.Fault = nil
	for _, want := range []string{"commit", "committed", "delete"} {
		if !slices.Contains(out, want) {
			t.Fatalf("no %s point in %q", want, out)
		}
	}
	return out
}

// sampled reports whether a short run tries point i: the store's points
// and every third of the rest.
func sampled(p string, i int) bool {
	return !testing.Short() || !strings.HasPrefix(p, "step ") || i%3 == 0
}

// A crash at any point, before COMMIT, after it, at each filesystem step
// or before the rows are deleted, leaves either nothing done or steps the
// next write finishes.
func TestCrashMatrix(t *testing.T) {
	ps := points(t)
	commit := slices.Index(ps, "commit")
	for i, p := range ps {
		if !sampled(p, i) {
			continue
		}
		t.Run(fmt.Sprintf("%02d %s", i, p), func(t *testing.T) {
			home, s := scenario(t)
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), "HOME="+home, "KOAN_FSSTEP_HELPER=crash", "KOAN_FSSTEP_AT="+strconv.Itoa(i))
			out, err := cmd.CombinedOutput()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != crashExit {
				t.Fatalf("helper: %v: %s", err, out)
			}
			committed := issued(t, s) == 1
			if committed != (i > commit) {
				t.Errorf("committed %v at %s", committed, p)
			}
			if w := write(t, s); len(w) != 0 {
				t.Errorf("warnings %s", describe(w))
			}
			want := sorted(scenarioBefore...)
			if committed {
				want = scenarioAfter
			}
			checkTree(t, s, want)
			noPending(t, s)
		})
	}
}

// The operating system refusing at any point leaves the write ok; a
// refused step stays pending with pending-step, and the next write
// finishes the change.
func TestRefusalMatrix(t *testing.T) {
	for i, p := range points(t) {
		if !sampled(p, i) {
			continue
		}
		t.Run(fmt.Sprintf("%02d %s", i, p), func(t *testing.T) {
			_, s := scenario(t)
			n := 0
			faults(t, func(string) error {
				defer func() { n++ }()
				if n == i {
					return errors.New("refused")
				}
				return nil
			})
			w := write(t, s, scenarioSteps...)
			_, pending := warning(w, "pending-step")
			if want := strings.HasPrefix(p, "step ") || strings.HasPrefix(p, "flush "); pending != want {
				t.Errorf("warnings %s", describe(w))
			}
			store.Fault = nil
			if w := write(t, s); len(w) != 0 {
				t.Errorf("warnings %s", describe(w))
			}
			checkTree(t, s, scenarioAfter)
			noPending(t, s)
		})
	}
}

// casefoldResult is what the casefold helper found.
type casefoldResult struct {
	OneRename []string `json:"one_rename"`
	Listing   []string `json:"listing"`
	Warnings  []string `json:"warnings"`
	Pending   int64    `json:"pending"`
}

// casefoldHelper runs inside a case-insensitive mount: it checks the
// filesystem ignores case, that one rename leaves the old spelling, then
// makes a case-only rename as a write does.
func casefoldHelper(home string) int {
	first := int64(1)
	if _, _, _, e := store.Init(home, &first); e != nil {
		fmt.Fprintln(os.Stderr, e.Message)
		return 1
	}
	s, e := Open(home)
	if e != nil {
		fmt.Fprintln(os.Stderr, e.Message)
		return 1
	}
	defer s.Close()
	tree := s.Root.Tree
	list := func() []string {
		f, _ := os.Open(tree)
		defer f.Close()
		names, _ := f.Readdirnames(-1)
		slices.Sort(names)
		return names
	}
	var r casefoldResult
	for _, d := range []string{"@try", "@work"} {
		if err := os.Mkdir(filepath.Join(tree, d), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	os.WriteFile(filepath.Join(tree, "@work", FolderNotes), []byte("w"), 0o644)
	if _, err := os.Stat(filepath.Join(tree, "@WORK")); err != nil {
		fmt.Println("not casefold")
		return 3
	}
	os.Rename(filepath.Join(tree, "@try"), filepath.Join(tree, "@Try"))
	r.OneRename = list()
	os.Remove(filepath.Join(tree, "@try"))

	_, w, e := s.Write(change(Step{Op: OpMove, From: "tree/@work", To: "tree/@Work", Dir: true}))
	if e != nil {
		fmt.Fprintln(os.Stderr, e.Message)
		return 1
	}
	r.Listing = list()
	r.Warnings = kinds(w)
	st, _ := s.Stats()
	r.Pending = st.Pending
	json.NewEncoder(os.Stdout).Encode(r)
	return 0
}

// On a filesystem that ignores case, one rename of @work to @Work does
// nothing; the step goes through @.case-<seq> and completes. Linux's
// casefold, on a tmpfs mounted in a user namespace, stands in for macOS.
func TestCaseOnlyCasefold(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("mounts a Linux casefold tmpfs")
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("no unshare")
	}
	dir := t.TempDir()
	script := `mount -t tmpfs -o casefold=utf8-12.1.0 none "$1" && mkdir "$1/home" && chattr +F "$1/home" || exit 99
HOME="$1/home" KOAN_FSSTEP_HELPER=casefold exec "$2" -test.run='^$'`
	cmd := exec.Command("unshare", "-rm", "sh", "-c", script, "sh", dir, os.Args[0])
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && (ee.ExitCode() == 99 || ee.ExitCode() == 3) {
		t.Skipf("no casefold mount here: %s", ee.Stderr)
	}
	if err != nil {
		t.Fatalf("helper: %v: %s", err, out)
	}
	var r casefoldResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !slices.Equal(r.OneRename, []string{"@try", "@work"}) {
		t.Errorf("one rename on casefold: %q", r.OneRename)
	}
	if !slices.Equal(r.Listing, []string{"@Work"}) || len(r.Warnings) != 0 || r.Pending != 0 {
		t.Errorf("case-only rename: %+v", r)
	}
}
