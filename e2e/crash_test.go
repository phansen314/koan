package e2e

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
)

// clock fixes the time every crash-test command sees, so the files of two
// runs compare byte for byte.
const clock = "KOAN_E2E_CLOCK=2026-09-28T12:00:00Z"

// snapshot is a home directory's contents: each path relative to the home
// maps to the file's bytes, with the home itself written "~", or to "/" for
// a directory. Temp files are left out and listed in temps.
type snapshot struct {
	files map[string]string
	temps []string
}

func snap(t *testing.T, home string) snapshot {
	t.Helper()
	s := snapshot{files: map[string]string{}}
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == home {
			return err
		}
		rel, _ := filepath.Rel(home, p)
		switch {
		case strings.HasPrefix(d.Name(), fsys.TempPrefix):
			s.temps = append(s.temps, rel)
			if d.IsDir() {
				return filepath.SkipDir // delete-folder's folder, renamed aside
			}
		case d.IsDir():
			s.files[rel] = "/"
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			s.files[rel] = strings.ReplaceAll(string(b), home, "~")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// crashCase is one write, and what its Crash behavior and Retry safety
// promise (operations.md).
type crashCase struct {
	name string
	// init: run from a fresh home with no tree; else from a new tree after
	// setup.
	init  bool
	setup [][]string
	// seed, if set, then changes the tree as no koan command would: the
	// outside change repair repairs.
	seed func(t *testing.T, root string)
	args []string
	// stdin, if set, is the write's standard input, for -i -.
	stdin string
	// order is what the write changes, relative to the home, in the order
	// its Crash behavior says the changes land. Paths in one group land in
	// one call.
	order [][]string
	// rerun is what running the write again gives after a crash that left
	// the tree at stage (0: nothing changed; len(order): everything): the
	// exit code, a fragment of the envelope, and whether the tree must then
	// be as after an uninterrupted run.
	rerun func(stage int) (code int, want string, same bool)
	// found is the finding kinds besides temp-leftover that doctor reports
	// for a crash leaving the tree at a stage, and kept those repair leaves
	// to a person; nil for none.
	found, kept map[int][]string
}

func safe(want string) func(int) (int, string, bool) {
	return func(int) (int, string, bool) { return 0, want, true }
}

var crashCases = []crashCase{
	{
		name: "init", init: true,
		args:  []string{"init", "~/tasks"},
		order: [][]string{{"tasks"}, {"tasks/koan.json"}, configDirs, {configDir + "/config.toml"}},
		// Once the config is written, init has succeeded, and a rerun is a
		// rerun after success.
		rerun: func(stage int) (int, string, bool) {
			if stage == 4 {
				return 1, `"rule":"config-exists"`, true
			}
			return 0, `"ok":true`, true
		},
	},
	{
		name:  "create",
		args:  []string{"create", "Fix", "--notes", "call first"},
		order: [][]string{{"tasks/koan.json"}, {"tasks/1.json"}, {"tasks/1.md"}},
		// Not safe after a crash: once the ID is consumed, a rerun creates
		// a second task.
		rerun: func(stage int) (int, string, bool) {
			if stage == 0 {
				return 0, `"id":1,`, true
			}
			return 0, `"id":2,`, false
		},
	},
	{
		name:  "create-batch",
		args:  []string{"create-batch", "-i", "-"},
		stdin: `{"tasks": [{"ref": "a", "title": "x", "folder": "/p", "notes": "n"}, {"title": "y", "blocked_by": ["a"], "notes": "m"}]}`,
		order: [][]string{{"tasks/p"}, {"tasks/koan.json"}, {"tasks/p/1.json"}, {"tasks/p/1.md"}, {"tasks/2.json"}, {"tasks/2.md"}},
		// Not safe after a crash once IDs are consumed: a rerun creates the
		// batch again, with new IDs.
		rerun: func(stage int) (int, string, bool) {
			if stage <= 1 {
				return 0, `"ids":[1,2]`, true
			}
			return 0, `"ids":[3,4]`, false
		},
	},
	{
		name:  "create-folder",
		args:  []string{"create-folder", "-p", "/proj/travel"},
		order: [][]string{{"tasks/proj"}, {"tasks/proj/travel"}},
		rerun: safe(`"folder":"/proj/travel"`),
	},
	{
		name:  "complete",
		setup: [][]string{{"create", "a"}},
		args:  []string{"complete", "1"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"completed_at":"2026-09-28T12:00:00Z"`),
	},
	{
		name:  "reopen",
		setup: [][]string{{"create", "a"}, {"complete", "1"}},
		args:  []string{"reopen", "1"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"completed_at":null`),
	},
	{
		name:  "update",
		setup: [][]string{{"create", "a"}},
		args:  []string{"update", "1", "--title", "b", "--tags-add", "x"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"title":"b"`),
	},
	{
		name:  "block",
		setup: [][]string{{"create", "a"}, {"create", "b"}},
		args:  []string{"block", "1", "--blockers", "2"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"blocked_by":[2]`),
	},
	{
		name:  "unblock",
		setup: [][]string{{"create", "a"}, {"create", "b"}, {"block", "1", "--blockers", "2"}},
		args:  []string{"unblock", "1", "--blockers", "2"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"blocked_by":[]`),
	},
	{
		name:  "delete",
		setup: [][]string{{"create", "a", "--notes", "n"}, {"create", "b", "--blocked-by", "1"}},
		args:  []string{"delete", "1"},
		order: [][]string{{"tasks/2.json"}, {"tasks/1.json"}, {"tasks/1.md"}},
		// The task is gone, its notes left: no-task, which only a person
		// may remove.
		found: map[int][]string{2: {"orphan-notes"}},
		kept:  map[int][]string{2: {"orphan-notes"}},
		rerun: func(stage int) (int, string, bool) {
			if stage >= 2 {
				// The task is gone; an orphaned .md may be left for doctor.
				return 1, `"kind":"not-found"`, false
			}
			return 0, `"id":1,"folder":"/"`, true
		},
	},
	{
		name:  "delete-folder",
		setup: [][]string{{"create-folder", "-p", "/p/a"}, {"create", "a", "--folder", "/p/a"}, {"create", "b", "--blocked-by", "1"}},
		args:  []string{"delete-folder", "-r", "/p"},
		order: [][]string{{"tasks/2.json"}, {"tasks/p", "tasks/p/a", "tasks/p/a/1.json", "tasks/p/a/1.md"}},
		rerun: func(stage int) (int, string, bool) {
			if stage == 2 {
				// Renamed aside: gone from the tree, a hidden leftover for doctor.
				return 1, `"kind":"not-found"`, true
			}
			return 0, `"ids":[1]`, true
		},
	},
	{
		name:  "move",
		setup: [][]string{{"create", "a", "--notes", "n"}},
		args:  []string{"move", "1", "--to", "/p", "-p"},
		order: [][]string{{"tasks/p"}, {"tasks/p/1.md"}, {"tasks/1.json", "tasks/p/1.json"}, {"tasks/1.md"}},
		// The notes have two names, one beside no task file: linked, which
		// repair removes.
		found: map[int][]string{2: {"orphan-notes"}, 3: {"orphan-notes"}},
		rerun: func(stage int) (int, string, bool) {
			if stage == 3 {
				// Moved; the old .md is left for doctor.
				return 0, `"changed":false`, false
			}
			return 0, `"changed":true`, true
		},
	},
	{
		// One item of each auto kind: a temp file; tasks 1 and 2 above
		// last_id; 1 blocked by 2, whose task file is gone, leaving its
		// empty notes.
		name:  "repair",
		setup: [][]string{{"create", "a"}, {"create", "b"}, {"block", "1", "--blockers", "2"}},
		seed: func(t *testing.T, root string) {
			for name, data := range map[string]string{
				".koan-tmp-seed": "x",
				"koan.json":      "{\n  \"schema\": 1,\n  \"last_id\": 0\n}\n",
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(filepath.Join(root, "2.json")); err != nil {
				t.Fatal(err)
			}
		},
		args:  []string{"repair"},
		order: [][]string{{"tasks/koan.json"}, {"tasks/1.json"}, {"tasks/2.md"}},
		found: map[int][]string{
			0: {"dangling-reference", "id-above-last-id", "orphan-notes"},
			1: {"dangling-reference", "orphan-notes"},
			2: {"orphan-notes"},
		},
		rerun: safe(`"ok":true`),
	},
	{
		name:  "move-folder",
		setup: [][]string{{"create-folder", "/a"}, {"create", "a", "--folder", "/a"}},
		args:  []string{"move-folder", "/a", "--to", "/x/y", "-p"},
		order: [][]string{{"tasks/x"}, {"tasks/a", "tasks/a/1.json", "tasks/a/1.md", "tasks/x/y", "tasks/x/y/1.json", "tasks/x/y/1.md"}},
		rerun: safe(`"folder":"/x/y"`),
	},
}

// diagnose checks doctor and repair on a tree a crash left at stage: doctor
// reports exactly the findings Crash behavior predicts, and after repair only
// those it leaves to a person (implementation-spec.md, Crash injection).
func (c crashCase) diagnose(t *testing.T, tr *tree, stage int, crashed snapshot) {
	t.Helper()
	want := slices.Clone(c.found[stage])
	if slices.ContainsFunc(crashed.temps, func(p string) bool { return strings.HasPrefix(p, "tasks/") }) {
		want = append(want, "temp-leftover")
	}
	slices.Sort(want)
	if got := findingKinds(t, tr.cmd("doctor")); !slices.Equal(got, want) {
		t.Errorf("stage %d: doctor found %v, want %v", stage, got, want)
	}
	if got := findingKinds(t, tr.cmd("repair")); !slices.Equal(got, c.kept[stage]) {
		t.Errorf("stage %d: repair left %v, want %v", stage, got, c.kept[stage])
	}
	if got := findingKinds(t, tr.cmd("doctor")); !slices.Equal(got, c.kept[stage]) {
		t.Errorf("stage %d: doctor after repair found %v, want %v", stage, got, c.kept[stage])
	}
}

// findingKinds runs doctor or repair, and returns the kinds of the findings
// it reports, in order; nil for none.
func findingKinds(t *testing.T, cmd *exec.Cmd) []string {
	t.Helper()
	r := run(t, cmd)
	envelope(t, r)
	var out struct {
		Result struct {
			Healthy  bool `json:"healthy"`
			Findings []struct {
				Kind string `json:"kind"`
			} `json:"findings"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || r.code != 0 {
		t.Fatalf("%q: exit %d: %s", cmd.Args[1:], r.code, r.stdout)
	}
	var kinds []string
	for _, f := range out.Result.Findings {
		kinds = append(kinds, f.Kind)
	}
	if out.Result.Healthy != (kinds == nil) {
		t.Errorf("%q: healthy %v with findings %v", cmd.Args[1:], out.Result.Healthy, kinds)
	}
	return kinds
}

// cmd prepares c's write in tr, with its stdin.
func (c crashCase) cmd(tr *tree) *exec.Cmd {
	cmd := tr.cmd(c.args...)
	if c.stdin != "" {
		stdin(cmd, c.stdin)
	}
	return cmd
}

// fixture is the tree c runs against, in a home of its own.
func (c crashCase) fixture(t *testing.T) *tree {
	t.Helper()
	var tr *tree
	if c.init {
		cmd := koan(t)
		tr = &tree{t: t, env: cmd.Env, home: envHome(cmd)}
	} else {
		tr = newTree(t)
	}
	tr.env = append(tr.env, clock)
	for _, args := range c.setup {
		r := run(t, tr.cmd(args...))
		if r.code != 0 {
			t.Fatalf("setup %q: exit %d: %s", args, r.code, r.stdout)
		}
	}
	if c.seed != nil {
		c.seed(t, tr.root())
	}
	return tr
}

// stages are the trees a crash may leave: before, then each group of order
// in turn changed as in after.
func (c crashCase) stages(before, after snapshot) []map[string]string {
	cur := maps.Clone(before.files)
	out := []map[string]string{maps.Clone(cur)}
	for _, group := range c.order {
		for _, p := range group {
			if v, ok := after.files[p]; ok {
				cur[p] = v
			} else {
				delete(cur, p)
			}
		}
		out = append(out, maps.Clone(cur))
	}
	return out
}

// Every write, killed with SIGKILL before each call that changes the disk in
// turn, leaves what its Crash behavior says: a tree at one of its stages,
// never going backwards, with only temp files besides; the tree still reads
// cleanly, nothing is wedged, and a rerun gives what its Retry safety
// promises (implementation-spec.md, Crash injection).
func TestCrashInjection(t *testing.T) {
	for _, c := range crashCases {
		t.Run(c.name, func(t *testing.T) {
			ref := c.fixture(t)
			before := snap(t, ref.home)
			steps(t, []step{{c.cmd(ref), 0, `"ok":true`}})
			after := snap(t, ref.home)
			stages := c.stages(before, after)
			if !maps.Equal(stages[len(stages)-1], after.files) {
				t.Fatalf("order does not cover every change:\nbefore %v\nafter  %v", before.files, after.files)
			}

			seen := make([]bool, len(stages))
			last := 0
			for k := 1; ; k++ {
				if k > 50 {
					t.Fatal("still crashing at k=50")
				}
				tr := c.fixture(t)
				cmd := c.cmd(tr)
				cmd.Env = append(cmd.Env, "KOAN_E2E_CRASH_BEFORE="+strconv.Itoa(k))
				r := run(t, cmd)
				ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if !ws.Signaled() {
					// Fewer than k calls change the disk: the uninterrupted run.
					envelope(t, r)
					if s := snap(t, tr.home); r.code != 0 || !maps.Equal(s.files, after.files) || len(s.temps) != 0 {
						t.Fatalf("k=%d, no crash: exit %d, tree %v temps %v: %s", k, r.code, s.files, s.temps, r.stdout)
					}
					// A write whose last call publishes by rename reaches its
					// last stage only here: no call follows to crash before.
					seen[len(stages)-1] = true
					break
				}
				if ws.Signal() != syscall.SIGKILL || r.stdout != "" {
					t.Fatalf("k=%d: %v, stdout %q; want SIGKILL and nothing", k, cmd.ProcessState, r.stdout)
				}
				crashed := snap(t, tr.home)
				stage := slices.IndexFunc(stages, func(m map[string]string) bool { return maps.Equal(m, crashed.files) })
				if stage < 0 {
					t.Fatalf("k=%d: tree at no stage of %q: %v", k, c.order, crashed.files)
				}
				if stage < last {
					t.Fatalf("k=%d: stage %d after stage %d", k, stage, last)
				}
				last, seen[stage] = stage, true
				t.Logf("k=%d: stage %d, temps %v", k, stage, crashed.temps)

				// A seeded tree starts out breaking invariants; repair's
				// promise is checked by diagnose instead.
				if _, err := os.Stat(filepath.Join(tr.home, configDir, "config.toml")); err == nil && c.seed == nil {
					steps(t, []step{{tr.cmd("list", "--readiness", "ready,blocked,complete", "--include-folders"), 0, `"warnings":[]`}})
				}
				if _, err := os.Stat(filepath.Join(tr.home, configDir, "config.toml")); err == nil {
					c.diagnose(t, tr, stage, crashed)
				}
				code, want, same := c.rerun(stage)
				steps(t, []step{{c.cmd(tr), code, want}})
				s := snap(t, tr.home)
				if same && !maps.Equal(s.files, after.files) {
					t.Errorf("k=%d: rerun left %v, want %v", k, s.files, after.files)
				}
				// doctor never sees the config directory: init clears it.
				for _, tmp := range s.temps {
					if strings.HasPrefix(tmp, configDirs[0]+"/") {
						t.Errorf("k=%d: rerun left %s", k, tmp)
					}
				}
			}
			for i, ok := range seen {
				if !ok {
					t.Errorf("no crash left stage %d (%s)", i, fmt.Sprint(stages[i]))
				}
			}
		})
	}
}
