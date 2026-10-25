package main_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/store"
	"github.com/phansen314/koan/internal/testrun"
)

// crashExit is how a helper that crashes at a fault point exits.
const crashExit = 7

// crashHelper runs one command line in this process, as the binary
// does, with the store's fault hook set: it logs every point a write
// passes to KOAN_CRASH_LOG, and exits at point KOAN_CRASH_AT, counted
// from 0, as a crash would. The hook is the tests' alone; the binary has
// none.
func crashHelper() int {
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("KOAN_CRASH_ARGS")), &args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	at, _ := strconv.Atoi(os.Getenv("KOAN_CRASH_AT"))
	log, err := os.OpenFile(os.Getenv("KOAN_CRASH_LOG"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	n := 0
	store.Fault = func(point string) error {
		fmt.Fprintln(log, point)
		if n == at {
			log.Close()
			os.Exit(crashExit)
		}
		n++
		return nil
	}
	defer log.Close()
	return cli.Main(args, os.Stdin, os.Stdout, os.Stderr, ops.Commands())
}

// crashRun runs a command line in a helper that crashes at point at, or
// runs to its end when at is -1, and gives the points it passed and its
// exit code.
func crashRun(t *testing.T, home string, c crashCase, at int) ([]string, int) {
	t.Helper()
	args, _ := json.Marshal(c.args)
	log := filepath.Join(t.TempDir(), "points")
	cmd := exec.Command(os.Args[0])
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "PWD="+home, "KOAN_CRASH_HELPER=1", "KOAN_CRASH_ARGS="+string(args),
		"KOAN_CRASH_AT="+strconv.Itoa(at), "KOAN_CRASH_LOG="+log)
	cmd.Stdin = strings.NewReader(c.stdin)
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		t.Fatalf("helper: %v: %s", err, out)
	}
	b, _ := os.ReadFile(log)
	points := strings.Fields(strings.ReplaceAll(string(b), " ", "_"))
	for i := range points {
		points[i] = strings.ReplaceAll(points[i], "_", " ")
	}
	if at < 0 && code != 0 {
		t.Fatalf("%q: exit %d: %s", c.args, code, out)
	}
	return points, code
}

// clone copies a home, its root and all, to a new one.
func clone(t *testing.T, home string) string {
	t.Helper()
	to := t.TempDir()
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == home {
			return err
		}
		rel, _ := filepath.Rel(home, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.Mkdir(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return to
}

var anyStamp = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z`)

// dump is every row of a root's database but its pending steps, in
// order, with timestamps masked, since a change made again is made at
// another time.
func dump(t *testing.T, home string) string {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+filepath.Join(home, "koan-root", "koan.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var out []string
	for _, q := range []string{
		"SELECT id, kind, state, archive FROM issued ORDER BY id",
		"SELECT id, parent, name, title, extra FROM folder ORDER BY id",
		"SELECT id, folder, gate, title, priority, created_at, completed_at, updated_at, extra FROM task ORDER BY id",
		"SELECT task, blocker FROM edge ORDER BY task, blocker",
		"SELECT task, tag FROM tag ORDER BY task, tag",
		"SELECT task, claimed_at, extra FROM claim ORDER BY task",
		"SELECT first_id FROM root",
	} {
		rs, err := d.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		for rs.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rs.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%s %v", cols[0], fmt.Sprint(vals...)))
		}
		rs.Close()
	}
	return anyStamp.ReplaceAllString(strings.Join(out, "\n"), "T")
}

// pendingRows is how many filesystem steps a root has pending.
func pendingRows(t *testing.T, home string) int {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+filepath.Join(home, "koan-root", "koan.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow("SELECT count(*) FROM pending").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// disk is every entry under a root's tree and archive, directories
// ending in /, files with their content.
func disk(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	for _, top := range []string{"tree", "archived"} {
		dir := filepath.Join(home, "koan-root", top)
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(filepath.Join(home, "koan-root"), p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				out = append(out, rel+"/")
				return nil
			}
			b, err := os.ReadFile(p)
			out = append(out, rel+"="+anyStamp.ReplaceAllString(string(b), "T"))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(out)
	return out
}

// findings is what doctor finds in a root, its paths relative to it.
func findings(t *testing.T, home string) []string {
	t.Helper()
	r := ok(t, home, "doctor")
	out := []string{}
	for _, f := range r.Result()["findings"].([]any) {
		f := f.(map[string]any)
		out = append(out, strings.ReplaceAll(fmt.Sprintf("%v %v %v %v", f["kind"], f["detail"], f["ids"], f["paths"]), home, "$HOME"))
	}
	return out
}

// rootState is a root as the crash matrix compares it.
type rootState struct {
	rows     string
	disk     []string
	findings []string
}

func stateOf(t *testing.T, home string) rootState {
	t.Helper()
	return rootState{dump(t, home), disk(t, home), findings(t, home)}
}

// crashCase is one kind of change: a root made ready for it, and the
// command that makes it.
type crashCase struct {
	name  string
	prep  func(t *testing.T, home string)
	args  []string
	stdin string
	// repair is a repair, of findings prep made by hand. It makes
	// several changes, and only a repair finishes what it began.
	repair bool
}

// crashCases are every kind of change with filesystem steps, and some
// without, each on a root whose first task, @1, is the anchor the next
// write updates.
var crashCases = []crashCase{
	{name: "folder create", args: []string{"folder", "create", "/a/b/c", "--notes", "c notes"}},
	{name: "task create", prep: func(t *testing.T, home string) { ok(t, home, "folder", "create", "/a") },
		args: []string{"task", "create", "t", "--folder", "/a", "--notes", "t notes", "--blocked-by", "/a.start", "--blocks", "/a.done"}},
	{name: "task update", args: []string{"task", "update", "1", "--title", "u", "--tags-add", "x"}},
	{name: "complete", prep: func(t *testing.T, home string) { ok(t, home, "claim", "1") },
		args: []string{"complete", "1"}},
	{name: "block", prep: func(t *testing.T, home string) { ok(t, home, "folder", "create", "/a") },
		args: []string{"block", "1", "/a.done", "--blocks", "/a.start"}},
	{name: "task mv", prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/a/b")
		ok(t, home, "task", "create", "m", "--notes", "m notes")
	}, args: []string{"task", "mv", "4", "/a/b"}},
	{name: "task delete", prep: func(t *testing.T, home string) {
		ok(t, home, "task", "create", "d", "--notes", "d notes", "--blocks", "1")
	}, args: []string{"task", "delete", "2"}},
	{name: "task archive", prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/a/b")
		ok(t, home, "task", "create", "x", "--folder", "/a/b", "--notes", "x notes", "--blocks", "1")
		ok(t, home, "complete", "4")
	}, args: []string{"task", "archive", "4"}},
	{name: "folder mv", prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/a/b", "--notes", "b notes")
		ok(t, home, "task", "create", "x", "--folder", "/a/b", "--notes", "x notes")
		ok(t, home, "folder", "create", "/x/y")
		writeTree(t, home, "@a/@b/src/main.go", "package main")
	}, args: []string{"folder", "mv", "-T", "/a", "/x/y/z"}},
	{name: "folder mv case-only", prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/work/api", "--notes", "api notes")
		ok(t, home, "task", "create", "x", "--folder", "/work/api", "--notes", "x notes", "--blocked-by", "/work/api.start")
		writeTree(t, home, "@work/@api/docs/readme", "material")
	}, args: []string{"folder", "mv", "-T", "/work/api", "/work/API"}},
	{name: "folder delete", prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/a/b")
		ok(t, home, "task", "create", "x", "--folder", "/a/b", "--notes", "x notes", "--blocks", "1")
		ok(t, home, "block", "1", "/a.done")
	}, args: []string{"folder", "delete", "/a", "--contents"}},
	{name: "folder delete force", prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/a/b")
		ok(t, home, "task", "create", "x", "--folder", "/a/b", "--notes", "x notes")
		writeTree(t, home, "@a/@b/src/main.go", "package main")
		writeTree(t, home, "@a/.DS_Store", "")
	}, args: []string{"folder", "delete", "/a", "--contents", "--material"}},
	{name: "folder archive", prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/a/b")
		ok(t, home, "task", "create", "x", "--folder", "/a/b", "--notes", "x notes", "--blocks", "1")
		ok(t, home, "complete", "4")
		writeTree(t, home, "@a/@b/src/main.go", "package main")
	}, args: []string{"folder", "archive", "/a"}},
	{name: "batch", args: []string{"batch", "-i", "-"}, stdin: `{"folders":[{"path":"/p/q"}],"tasks":[
		{"label":"$a","title":"a","folder":"/p/q","blocks":["$b","/p.done"]},
		{"label":"$b","title":"b","folder":"/p","blocked_by":["/p/q.start"],"blocks":["1"]}]}`},
	{name: "repair case-differs", repair: true, prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/work/api", "--notes", "api notes")
		ok(t, home, "task", "create", "x", "--folder", "/work/api", "--notes", "x notes")
		rename(t, home, "@work/@api", "@work/@Api")
	}, args: []string{"repair"}},
	{name: "repair", repair: true, prep: func(t *testing.T, home string) {
		ok(t, home, "folder", "create", "/a/b")
		ok(t, home, "folder", "create", "/c")
		ok(t, home, "task", "create", "x", "--folder", "/a", "--notes", "x notes")
		ok(t, home, "task", "create", "y", "--folder", "/c")
		rename(t, home, "@a/@b", "@a/@renamed")
		hand(t, os.RemoveAll(filepath.Join(treeDir(home), "@c")))
		rename(t, home, "@a/@5.md", "@5.md")
		write(t, home, "@99.md", "")
	}, args: []string{"repair"}},
}

// The crash-point matrix: a crash injected before and after the commit,
// at each filesystem step and each flush, and before the steps' rows are
// deleted, of every kind of change. Readers never see part of a change:
// the database is as it was before or as it is after, never between, and
// a read reports steps left pending. The next write, or repair, then
// brings the tree back to T1 to T6, as it would be had nothing crashed.
// -short runs the case-only folder mv and the case-differs repair, at
// every third point.
func TestCrashPoints(t *testing.T) {
	for _, c := range crashCases {
		if testing.Short() && c.name != "folder mv case-only" && c.name != "repair case-differs" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			crashMatrix(t, c)
		})
	}
}

func crashMatrix(t *testing.T, c crashCase) {
	tmpl := root(t)
	ok(t, tmpl, "task", "create", "anchor")
	if c.prep != nil {
		c.prep(t, tmpl)
	}
	before := stateOf(t, tmpl)
	if !c.repair && len(before.findings) != 0 {
		t.Fatalf("doctor finds %q before", before.findings)
	}
	done := clone(t, tmpl)
	points, _ := crashRun(t, done, c, -1)
	after := stateOf(t, done)
	if len(after.findings) != 0 {
		t.Fatalf("doctor finds %q after", after.findings)
	}
	commit := slices.Index(points, "commit")
	if commit < 0 {
		t.Fatalf("no commit in %q", points)
	}
	steps := 0
	for _, p := range points {
		if strings.HasPrefix(p, "step ") {
			steps++
		}
	}
	if steps == 0 && !slices.Contains([]string{"task update", "complete"}, c.name) {
		t.Fatalf("no filesystem step in %q", points)
	}
	for i, p := range points {
		if testing.Short() && i%3 != 0 {
			continue
		}
		t.Run(fmt.Sprintf("%02d %s", i, p), func(t *testing.T) {
			home := clone(t, tmpl)
			if _, code := crashRun(t, home, c, i); code != crashExit {
				t.Fatalf("exit %d, not a crash", code)
			}
			// Readers see the change whole or not at all.
			rows := dump(t, home)
			committed := rows == after.rows
			switch {
			case c.repair:
				committed = true
			case !committed && rows != before.rows:
				t.Fatalf("part of a change:\n%s", rows)
			case committed != (i > commit):
				t.Errorf("committed %v at %s", committed, p)
			}
			r := koan(t, home, "folder", "list")
			if !r.OK() {
				t.Fatalf("a read after the crash: %s", r.Line())
			}
			if n := pendingRows(t, home); n > 0 && !strings.Contains(r.Line(), `"kind":"pending-step"`) {
				t.Errorf("%d steps pending, and the read says nothing: %s", n, r.Line())
			}
			// The rows hold their invariants; a repair's tree is wrong by
			// hand until it is done.
			if _, bad := testrun.CheckRoot(home, nil, testrun.Checks{Pending: true}); len(bad) > 0 && !c.repair {
				t.Errorf("after the crash:\n%s", strings.Join(bad, "\n"))
			}

			// Then the next write, or repair, puts the tree right.
			hows := []string{"repair", "write"}
			if c.repair {
				hows = hows[:1]
			}
			for _, how := range hows {
				h := clone(t, home)
				var r *testrun.Result
				if how == "repair" {
					r = koan(t, h, "repair")
				} else {
					r = koan(t, h, "task", "update", "1", "--title", "next")
				}
				if !r.OK() || len(r.Envelope["warnings"].([]any)) != 0 {
					t.Errorf("%s: %s", how, r.Line())
				}
				want := before
				if committed {
					want = after
				}
				if got := disk(t, h); !slices.Equal(got, want.disk) {
					t.Errorf("%s: disk:\n got %q\nwant %q", how, got, want.disk)
				}
				if how == "repair" && dump(t, h) != want.rows {
					t.Errorf("repair: rows:\n%s\nwant\n%s", dump(t, h), want.rows)
				}
				if got := findings(t, h); !slices.Equal(got, want.findings) {
					t.Errorf("%s: doctor finds %q, want %q", how, got, want.findings)
				}
				// Notes repair can't bring back are gone.
				if _, bad := testrun.CheckRoot(h, nil, testrun.Checks{Notes: !c.repair}); len(bad) > 0 && len(want.findings) == 0 {
					t.Errorf("%s: invariants:\n%s", how, strings.Join(bad, "\n"))
				}
			}
		})
	}
}
