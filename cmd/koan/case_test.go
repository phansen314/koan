package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// caseRow is one command of the Case table's tests: what it gives, a
// kind and rule when it is refused, and anything else to check.
type caseRow struct {
	args        []string
	stdin       string
	kind, rule  string
	paths, prob string
	check       func(t *testing.T, home string, r *testrun.Result)
}

// The rules doc's Case section, a test per row, through the commands and
// on the disk. Each holds on a filesystem that tells case apart and on one
// that ignores it: TestCasefold runs them again on one.
func TestCaseTable(t *testing.T) {
	notes := func(rel string) func(t *testing.T, home string, r *testrun.Result) {
		return func(t *testing.T, home string, r *testrun.Result) {
			if got := r.Result()["folder"].(map[string]any)["notes_path"]; got != treePath(home, rel) {
				t.Errorf("notes_path %v", got)
			}
		}
	}
	listed := func(dir string, want ...string) func(t *testing.T, home string, r *testrun.Result) {
		return func(t *testing.T, home string, r *testrun.Result) {
			es, err := os.ReadDir(treePath(home, dir))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range es {
				if strings.HasPrefix(e.Name(), "@") {
					got = append(got, e.Name())
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s lists %q, want %q", dir, got, want)
			}
		}
	}
	for _, c := range []struct {
		rule string
		rows []caseRow
	}{
		{"Names are kept as given and compared byte for byte", []caseRow{
			{args: []string{"folder", "create", "/web/Work"}, check: listed("@web", "@Work", "@notes.md")},
			{args: []string{"folder", "show", "/web/Work"}},
			{args: []string{"task", "create", "t", "--folder", "/web/Work"}},
			{args: []string{"folder", "create", "/web/a"}},
			// Byte order: upper case before lower.
			{args: []string{"folder", "list", "--fields", "path"}, check: func(t *testing.T, home string, r *testrun.Result) {
				if got := fmt.Sprint(r.Result()["folders"]); got != "[map[id:3 path:/web] map[id:4 path:/web/Work] map[id:6 path:/web/a] map[id:1 path:/work] map[id:2 path:/work/api]]" {
					t.Errorf("folders %s", got)
				}
			}},
		}},
		{"Siblings never differ only in case", []caseRow{
			{args: []string{"folder", "create", "/Work"}, kind: "conflict", rule: "case-mismatch", paths: "[/Work /work]"},
			{args: []string{"folder", "create", "/work/API"}, kind: "conflict", rule: "case-mismatch", paths: "[/work/API /work/api]"},
			{args: []string{"folder", "create", "/web"}},
			{args: []string{"folder", "move", "/web", "--to", "/WORK"}, kind: "conflict", rule: "case-mismatch"},
			{args: []string{"batch", "-i", "-"}, stdin: `{"folders":[{"path":"/web/Api"},{"path":"/work/Api"}]}`, kind: "conflict", rule: "case-mismatch"},
			{args: []string{"folder", "list", "--fields", "path"}, check: listed("", "@web", "@work")},
		}},
		{"A path names a folder only in its exact case, on every platform", []caseRow{
			{args: []string{"task", "create", "t", "--folder", "/Work"}, kind: "conflict", rule: "case-mismatch", paths: "[/Work /work]"},
			{args: []string{"task", "create", "t", "--folder", "/work/API"}, kind: "conflict", rule: "case-mismatch", paths: "[/work/API /work/api]"},
			{args: []string{"folder", "show", "/WORK"}, kind: "conflict", rule: "case-mismatch"},
			{args: []string{"task", "show", "/Work.start"}, kind: "conflict", rule: "case-mismatch"},
			{args: []string{"where", "/Work/api"}, kind: "conflict", rule: "case-mismatch"},
			{args: []string{"folder", "delete", "/work/Api"}, kind: "conflict", rule: "case-mismatch"},
			{args: []string{"folder", "show", "/work/api"}},
		}},
		{"One input never names two paths that differ only in case", []caseRow{
			{args: []string{"folder", "create", "/proj/a"}},
			{args: []string{"task", "create", "t", "--folder", "/proj/a", "--blocks", "/Proj/a.done"}, kind: "invalid-input", prob: "/folder /blocks/0"},
			{args: []string{"folder", "move", "/proj/a", "--to", "/Proj/b"}, kind: "invalid-input"},
			{args: []string{"batch", "-i", "-"}, stdin: `{"folders":[{"path":"/Proj/a"},{"path":"/proj/b"}]}`, kind: "invalid-input"},
			// From different starts, they are compared as they resolve.
			{args: []string{"task", "create", "t", "--folder", "/proj/a", "--blocks", "@3/A.done"}, kind: "conflict", rule: "case-mismatch"},
		}},
		{"Changing a folder's case is a move", []caseRow{
			{args: []string{"task", "create", "t", "--folder", "/work/api", "--notes", "kept"}},
			{args: []string{"folder", "move", "/work/api", "--to", "/work/API"}, check: listed("@work", "@API", "@notes.md")},
			{args: []string{"folder", "show", "/work/API"}},
			{args: []string{"task", "show", "3"}, check: func(t *testing.T, home string, r *testrun.Result) {
				b, err := os.ReadFile(treePath(home, "@work/@API/@3.md"))
				if err != nil || string(b) != "kept" {
					t.Errorf("notes after the respell: %q, %v", b, err)
				}
			}},
			{args: []string{"folder", "move", "/work", "--to", "/Work"}, check: listed("", "@Work")},
			{args: []string{"folder", "move", "/Work/API", "--to", "/work/b"}, kind: "invalid-input"},
			{args: []string{"folder", "move", "/Work/API", "--to", "/WORK/API"}, kind: "invalid-input"},
			{args: []string{"doctor"}, check: func(t *testing.T, home string, r *testrun.Result) {
				if len(r.Result()["findings"].([]any)) != 0 {
					t.Errorf("doctor: %s", r.Line())
				}
			}},
		}},
		{"A folder's directory is spelled exactly as its name", []caseRow{
			{args: []string{"folder", "create", "/a/Bc/dE"}, check: listed("@a/@Bc", "@dE", "@notes.md")},
			{args: []string{"doctor"}, check: func(t *testing.T, home string, r *testrun.Result) {
				rename(t, home, "@a/@Bc", "@a/@bc")
				if got := findings(t, home); fmt.Sprint(got) != "[case-differs <nil> [4] [$HOME/koan-root/tree/@a/@bc]]" {
					t.Errorf("respelled by hand: %q", got)
				}
			}},
			{args: []string{"repair", "--kinds", "case-differs"}, check: listed("@a", "@Bc", "@notes.md")},
		}},
		{"koan's own names are matched exactly", []caseRow{
			{args: []string{"task", "block", "/work.start", "--blocks", "/work.done"}, check: func(t *testing.T, home string, r *testrun.Result) {
				for _, name := range []string{"@Start.md", "@DONE.md", "@Notes.md", "@5.MD", "@.Tmp-1", "@.CASE-1"} {
					if ignoresCase(t, treePath(home, "@work")) && slices.Contains([]string{"@Start.md", "@DONE.md", "@Notes.md"}, name) {
						// Here the file would be the gates' or the
						// folder's own notes, under another spelling.
						continue
					}
					if name == "@.CASE-1" {
						hand(t, os.Mkdir(treePath(home, "@work/"+name), 0o755))
					} else {
						write(t, home, "@work/"+name, "x")
					}
				}
			}},
			{args: []string{"doctor"}, check: func(t *testing.T, home string, r *testrun.Result) {
				got := strings.Join(findings(t, home), "\n")
				for _, want := range []string{"stray-entry <nil> [] [$HOME/koan-root/tree/@work/@.Tmp-1]",
					"stray-entry <nil> [] [$HOME/koan-root/tree/@work/@5.MD]", "stray-directory <nil> [] [$HOME/koan-root/tree/@work/@.CASE-1]"} {
					if !strings.Contains(got, want) {
						t.Errorf("doctor misses %s:\n%s", want, got)
					}
				}
				if !ignoresCase(t, treePath(home, "@work")) {
					for _, name := range []string{"@Start.md", "@DONE.md", "@Notes.md"} {
						if !strings.Contains(got, "stray-entry <nil> [] [$HOME/koan-root/tree/@work/"+name+"]") {
							t.Errorf("doctor misses %s:\n%s", name, got)
						}
					}
				}
			}},
			{args: []string{"folder", "show", "/work"}, check: notes("@work/@notes.md")},
		}},
		{"Tags are lowercase only, and IDs are digits", []caseRow{
			{args: []string{"task", "create", "t", "--tags", "Later"}, kind: "invalid-input", prob: "/tags/0"},
			{args: []string{"task", "create", "t", "--tags", "later"}},
			{args: []string{"task", "list", "--tags-any", "LATER"}, kind: "invalid-input"},
			{args: []string{"task", "show", "@3"}},
			{args: []string{"task", "show", "@0x3"}, kind: "invalid-input"},
		}},
		{"Material names are never compared", []caseRow{
			{args: []string{"where", "/work/api:DOCS/Plan.MD"}, check: func(t *testing.T, home string, r *testrun.Result) {
				if got := fmt.Sprint(r.Result()["file"]); got != treePath(home, "@work/@api/DOCS/Plan.MD") || r.Result()["material"] != "DOCS/Plan.MD" {
					t.Errorf("path %s", got)
				}
			}},
			{args: []string{"folder", "show", "/work/api"}, check: func(t *testing.T, home string, r *testrun.Result) {
				writeTree(t, home, "@work/@api/Docs/Plan.MD", "material")
			}},
			{args: []string{"folder", "delete", "/work/api"}, kind: "conflict", rule: "has-material", paths: "[/work/api:Docs]"},
		}},
	} {
		t.Run(c.rule, func(t *testing.T) {
			home := root(t)
			ok(t, home, "folder", "create", "/work/api") // 1, 2
			for i, row := range c.rows {
				var r *testrun.Result
				if row.stdin != "" {
					r = testrun.Run{Home: home, Stdin: strings.NewReader(row.stdin)}.Koan(t, row.args...)
				} else {
					r = koan(t, home, row.args...)
				}
				got, _ := r.Error()["rule"].(string)
				switch {
				case row.kind == "" && !r.OK():
					t.Fatalf("%d %q: %s", i, row.args, r.Line())
				case row.kind != "" && (r.Kind() != row.kind || got != row.rule):
					t.Fatalf("%d %q: want %s %s: %s", i, row.args, row.kind, row.rule, r.Line())
				case row.paths != "" && fmt.Sprint(r.Error()["paths"]) != row.paths:
					t.Errorf("%d %q: paths %v", i, row.args, r.Error()["paths"])
				case row.prob != "" && problemFields(r) != row.prob:
					t.Errorf("%d %q: problems %s", i, row.args, problemFields(r))
				}
				if row.check != nil {
					row.check(t, home, r)
				}
			}
		})
	}
}

// casefoldTests are the tests TestCasefold runs again on a filesystem that
// ignores case: under -short the Case table, the case-only moves of the
// property run and the crash matrix's case rows; in full, every test.
const casefoldTests = `^(TestCaseTable|TestCaseFromDifferentStarts|TestCaseOutsideTheTree|TestProperties|TestCrashPoints|TestRepair|TestFolderMove.*|TestOnCasefold)$`

// The Case row of PRD Testing, on Linux: the tests run again with their
// temp directories on a tmpfs that ignores case, mounted in a user
// namespace, reached through a symlink as macOS's temp directory is.
// They hold there as they do here.
func TestCasefold(t *testing.T) {
	if os.Getenv("KOAN_CASEFOLD") != "" {
		t.Skip("already on the casefold mount")
	}
	if runtime.GOOS != "linux" {
		t.Skip("mounts a Linux casefold tmpfs; on macOS the tests run on its own filesystem")
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("no unshare")
	}
	t.Parallel()
	dir := t.TempDir()
	script := `mount -t tmpfs -o casefold=utf8-12.1.0 none "$1" && mkdir "$1/tmp" && chattr +F "$1/tmp" && ln -s "$1/tmp" "$1/link" || exit 99
d="$1" && cd "$d/tmp" && shift && TMPDIR="$d/link" exec "$@"`
	args := []string{"-rm", "sh", "-c", script, "sh", dir,
		os.Args[0], "-test.count=1", "-test.run=" + casefoldTests}
	if testing.Short() {
		args = append(args, "-test.short")
	} else {
		args[len(args)-1] = "-test.run=."
	}
	if testing.Verbose() {
		args = append(args, "-test.v")
	}
	cmd := exec.Command("unshare", args...)
	cmd.Env = append(os.Environ(), "KOAN_CASEFOLD=1", "KOAN_TEST_BINARY="+testrun.Binary(t))
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 99 {
		t.Skipf("no casefold mount here: %s", out)
	}
	if err != nil {
		t.Fatalf("on the casefold mount: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS") {
		t.Fatalf("on the casefold mount:\n%s", out)
	}
	t.Logf("on the casefold mount:\n%s", out)
}

// TestOnCasefold checks the casefold run is where it says it is.
func TestOnCasefold(t *testing.T) {
	if os.Getenv("KOAN_CASEFOLD") == "" {
		t.Skip("not on the casefold mount")
	}
	tmp := t.TempDir()
	if !ignoresCase(t, tmp) {
		t.Fatalf("%s tells case apart", tmp)
	}
	if real, _ := filepath.EvalSymlinks(tmp); real == tmp {
		t.Fatalf("%s is reached through no symlink", tmp)
	}
}
