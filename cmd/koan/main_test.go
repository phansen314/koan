package main_test

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/testrun"
	_ "modernc.org/sqlite"
)

var update = flag.Bool("update", false, "rewrite the golden results in testdata/")

// TestMain builds the binary and runs the tests, or runs the test binary
// as a helper that crashes at a fault point.
func TestMain(m *testing.M) {
	if os.Getenv("KOAN_CRASH_HELPER") != "" {
		os.Exit(crashHelper())
	}
	testrun.Main(m)
}

func TestVersion(t *testing.T) {
	r := testrun.Koan(t, "version")
	if r.Exit != 0 || !r.OK() || r.Stderr != "" {
		t.Fatalf("exit %d, stderr %q: %s", r.Exit, r.Stderr, r.Line())
	}
	r.Check(t, "version")
	res := r.Result()
	commit := `"commit":null`
	if c, ok := res["commit"].(string); ok {
		commit = `"commit":"` + c + `"`
	}
	// The version depends on the build as the commit does: (devel), or a
	// pseudo-version Go stamps from the checkout.
	r.Golden(t, "version", *update, map[string]string{
		`"version":"` + res["version"].(string) + `"`: `"version":"$VERSION"`,
		`"go":"` + res["go"].(string) + `"`:           `"go":"$GO"`,
		commit:                                        `"commit":"$COMMIT"`,
	})
}

func TestInfoMissingRoot(t *testing.T) {
	r := testrun.Koan(t, "info")
	if r.Exit != 0 || !r.OK() || r.Stderr != "" {
		t.Fatalf("exit %d, stderr %q: %s", r.Exit, r.Stderr, r.Line())
	}
	r.Check(t, "info")
	r.Golden(t, "info-missing-root", *update, nil)
	if _, err := os.Lstat(filepath.Join(r.Home, "koan-root")); !os.IsNotExist(err) {
		t.Errorf("info made the root: %v", err)
	}
	r = testrun.Run{Home: r.Home}.Koan(t, "task", "list")
	r.Check(t, "task list")
	if r.Exit != 1 || r.Kind() != "not-initialized" {
		t.Errorf("task list: exit %d: %s", r.Exit, r.Line())
	}
	golden(t, r, "task-list-not-initialized")
}

func TestInfoRootWithoutDatabase(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "koan-root"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A name that differs only in case is not the database, but is
	// something that isn't koan's, which init would refuse.
	os.WriteFile(filepath.Join(home, "koan-root", "KOAN.DB"), nil, 0o644)
	r := testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	r.Golden(t, "info-no-database-foreign", *update, nil)
}

func TestInfoSymlinkRoot(t *testing.T) {
	home, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, "koan-root")); err != nil {
		t.Fatal(err)
	}
	r := testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	// Paths are through the symlink, as written.
	r.Golden(t, "info-no-database", *update, nil)

	dangling := t.TempDir()
	if err := os.Symlink(filepath.Join(dangling, "gone"), filepath.Join(dangling, "koan-root")); err != nil {
		t.Fatal(err)
	}
	r = testrun.Run{Home: dangling}.Koan(t, "info")
	r.Check(t, "info")
	r.Golden(t, "info-missing-root", *update, nil)
}

// A root that isn't a directory is the operating system refusing, not a
// state: info fails with io.
func TestInfoRootNotADirectory(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "koan-root")
	if err := os.WriteFile(root, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	paths, _ := r.Error()["paths"].([]any)
	if r.Exit != 1 || r.Kind() != "io" || len(paths) != 1 || paths[0] != root {
		t.Errorf("exit %d: %s", r.Exit, r.Line())
	}
}

func TestInput(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "in.json")
	os.WriteFile(file, []byte(`{}`), 0o644)
	for _, args := range [][]string{{"version", "-i", file}, {"info", "--input", file}, {"info", "--input=" + file}} {
		r := testrun.Run{Home: home}.Koan(t, args...)
		if r.Exit != 0 {
			t.Errorf("%q: exit %d: %s", args, r.Exit, r.Line())
		}
		r.Check(t, args[0])
	}
	r := testrun.Run{Stdin: strings.NewReader(`{}`)}.Koan(t, "version", "-i", "-")
	if r.Exit != 0 {
		t.Errorf("stdin: exit %d: %s", r.Exit, r.Line())
	}
	r.Check(t, "version")

	// Every input is closed: a field the command doesn't take is
	// invalid-input, exactly as an unknown option is usage.
	for _, c := range []struct{ stdin, kind, field string }{
		{`{"x":1}`, "invalid-input", "/x"},
		{`[]`, "invalid-input", ""},
		{`{`, "invalid-input", ""},
	} {
		r := testrun.Run{Stdin: strings.NewReader(c.stdin)}.Koan(t, "info", "-i", "-")
		r.Check(t, "info")
		problems, _ := r.Error()["problems"].([]any)
		if r.Exit != 1 || r.Kind() != c.kind || len(problems) == 0 || problems[0].(map[string]any)["field"] != c.field {
			t.Errorf("stdin %s: exit %d: %s", c.stdin, r.Exit, r.Line())
		}
		if want := "koan: " + c.kind + ": "; !strings.HasPrefix(r.Stderr, want) {
			t.Errorf("stdin %s: stderr %q", c.stdin, r.Stderr)
		}
	}
	r = testrun.Koan(t, "version", "-i", filepath.Join(home, "missing.json"))
	r.Check(t, "version")
	if r.Exit != 1 || r.Kind() != "io" {
		t.Errorf("missing file: exit %d: %s", r.Exit, r.Line())
	}
}

// TestMalformed is the table of command lines that can't be turned into
// an input: each gives usage, exit 2, naming the argument at fault.
func TestMalformed(t *testing.T) {
	cases := []struct {
		args  []string
		field string
	}{
		{nil, ""},
		{[]string{"frob"}, "frob"},
		{[]string{"Version"}, "Version"},
		{[]string{"vers"}, "vers"},
		{[]string{"--version"}, "--version"},
		{[]string{"-i", "-"}, "-i"},
		{[]string{"version", "extra"}, "extra"},
		{[]string{"info", "@42"}, "@42"},
		{[]string{"info", "--", "x"}, "x"},
		{[]string{"version", "--frob"}, "--frob"},
		{[]string{"version", "--frob=1"}, "--frob"},
		{[]string{"info", "-x"}, "-x"},
		{[]string{"info", "-i"}, "--input"},
		{[]string{"info", "--input"}, "--input"},
		{[]string{"info", "--Input", "in.json"}, "--Input"},
		{[]string{"info", "--inp", "in.json"}, "--inp"},
		{[]string{"info", "-i", "in.json", "-i", "in.json"}, "--input"},
		{[]string{"info", "-i", "in.json", "x"}, "x"},
		{[]string{"info", "x", "-i", "in.json"}, "x"},
		{[]string{"info", "-i", "in.json", "--", "x"}, "x"},
		{[]string{"task"}, ""},
		{[]string{"task", "frob"}, "frob"},
		{[]string{"folder"}, ""},
		{[]string{"Bind"}, "Bind"},
		{[]string{"task", "list", "--limit", "ten"}, "--limit"},
		{[]string{"task", "list", "--direct=maybe"}, "--direct"},
		{[]string{"task", "list", "--claimed", "--claimed=false"}, "--claimed"},
		{[]string{"task", "create", "t", "--notes", "n", "--notes-file", "f"}, ""},
		{[]string{"task", "create", "t", "-i", "in.json"}, "t"},
		{[]string{"update", "1", "--title", "a", "--title", "b"}, "--title"},
		{[]string{"completion", "zhs"}, "zhs"},
		{[]string{"completion", "bash", "extra"}, ""},
		// An option pflag refuses comes before help.
		{[]string{"info", "--contents", "-h"}, "--contents"},
	}
	for _, c := range cases {
		r := testrun.Koan(t, c.args...)
		r.Check(t, "")
		problems, _ := r.Error()["problems"].([]any)
		if r.Exit != 2 || r.Kind() != "usage" || len(problems) != 1 || problems[0].(map[string]any)["field"] != c.field {
			t.Errorf("koan %q: exit %d, want usage at %q: %s", c.args, r.Exit, c.field, r.Line())
		}
		if !strings.HasPrefix(r.Stderr, "koan: usage: ") || strings.Count(r.Stderr, "\n") != 1 {
			t.Errorf("koan %q: stderr %q", c.args, r.Stderr)
		}
	}
}

// Help and completion are for people: plain text on standard output,
// exit 0, nothing on standard error. --help and -h work anywhere.
func TestHelp(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"version", "--help"}, {"info", "--help"},
		{"version", "x", "--help"}, {"info", "x", "y", "-h"}, {"task", "--help"},
		{"help", "task", "create"}, {"completion", "bash"}, {"completion", "zsh"},
	} {
		r := testrun.Koan(t, args...)
		if r.Exit != 0 || r.Envelope != nil || !strings.Contains(r.Stdout, "koan") || r.Stderr != "" {
			t.Errorf("koan %q: exit %d: %.200q %q", args, r.Exit, r.Stdout, r.Stderr)
		}
	}
	r := testrun.Koan(t, "task", "create", "--help")
	for _, opt := range []string{"--blocked-by", "--notes-file", "--input"} {
		if !strings.Contains(r.Stdout, opt) {
			t.Errorf("task create --help lacks %s: %q", opt, r.Stdout)
		}
	}
}

// Help's usage lines are the spec's, its summaries say what a command
// does, and an option that takes a reference says which kind.
func TestHelpText(t *testing.T) {
	for _, c := range []struct {
		args  []string
		has   []string
		lacks []string
	}{
		{[]string{"where", "-h"}, []string{"koan where <ref> [flags]\n", "koan where --file <path> [flags]\n"}, []string{"[<ref>]"}},
		{[]string{"mv", "-h"}, []string{
			"koan mv <ref>... <dest> [flags]\n", "koan mv -t <folder> <ref>... [flags]\n", "koan mv -T <folder> <path> [flags]\n",
			"-t, --into folder "}, []string{"string"}},
		{[]string{"unblock", "-h"}, []string{"Remove edges: what a task waits on, or what waits on it"}, nil},
		{[]string{"mv", "-h"}, []string{"-t, --into folder "}, nil},
		{[]string{"task", "create", "-h"}, []string{"--folder folder "}, nil},
	} {
		r := testrun.Koan(t, c.args...)
		for _, s := range c.has {
			if !strings.Contains(r.Stdout, s) {
				t.Errorf("koan %q lacks %q: %s", c.args, s, r.Stdout)
			}
		}
		for _, s := range c.lacks {
			if strings.Contains(r.Stdout, s) {
				t.Errorf("koan %q has %q: %s", c.args, s, r.Stdout)
			}
		}
	}
}

// --notes with --notes-file is usage at "", worded as -t with -T is.
func TestNotesClash(t *testing.T) {
	home := root(t)
	for _, args := range [][]string{
		{"task", "create", "a", "--notes", "x", "--notes-file", "-"},
		{"folder", "create", "/a", "--notes-file", "-", "--notes", "x"},
	} {
		r := usageAt(t, home, "", args...)
		if msg := r.Error()["message"]; msg != "--notes and --notes-file can't go together" {
			t.Errorf("koan %q: %s", args, r.Line())
		}
	}
}

// Exit 3: koan couldn't write the envelope.
func TestOutputFails(t *testing.T) {
	if runtime.GOOS == "linux" {
		full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer full.Close()
		if code := run(t, full, "version"); code != 3 {
			t.Errorf("/dev/full: exit %d, want 3", code)
		}
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rd.Close()
	defer wr.Close()
	if code := run(t, wr, "info"); code != 3 {
		t.Errorf("closed pipe: exit %d, want 3", code)
	}
}

func run(t *testing.T, stdout *os.File, args ...string) int {
	cmd := exec.Command(testrun.Binary(t), args...)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	cmd.Stdout = stdout
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0
}

func TestInit(t *testing.T) {
	home := t.TempDir()
	r := testrun.Run{Home: home}.Koan(t, "init")
	if r.Exit != 0 || r.Stderr != "" {
		t.Fatalf("exit %d, stderr %q: %s", r.Exit, r.Stderr, r.Line())
	}
	r.Check(t, "init")
	r.Golden(t, "init-new", *update, nil)

	r = testrun.Run{Home: home}.Koan(t, "init")
	r.Check(t, "init")
	r.Golden(t, "init-again", *update, nil)

	r = testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	r.Golden(t, "info-usable", *update, nil)

	// --ids-from for a root that has a database is refused.
	r = testrun.Run{Home: home}.Koan(t, "init", "--ids-from", "7")
	r.Check(t, "init")
	if r.Exit != 1 {
		t.Errorf("exit %d", r.Exit)
	}
	r.Golden(t, "init-exists", *update, nil)
}

func TestInitIDsFrom(t *testing.T) {
	home := t.TempDir()
	r := testrun.Run{Home: home}.Koan(t, "init", "--ids-from", "500")
	r.Check(t, "init")
	r.Golden(t, "init-ids-from", *update, nil)
	r = testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	if r.Result()["next_id"] != json.Number("500") {
		t.Errorf("next_id: %s", r.Line())
	}

	// An integer out of range is the command's to refuse; text that is no
	// integer, pflag's.
	for _, v := range []string{"0", "-3", "abc", "1.5", "007", "1000000000000000"} {
		r := testrun.Koan(t, "init", "--ids-from", v)
		r.Check(t, "")
		problems, _ := r.Error()["problems"].([]any)
		exit, kind, field := 1, "invalid-input", "/ids_from"
		if v == "abc" || v == "1.5" || v == "007" {
			exit, kind, field = 2, "usage", "--ids-from"
		}
		if r.Exit != exit || r.Kind() != kind || len(problems) != 1 || problems[0].(map[string]any)["field"] != field {
			t.Errorf("--ids-from %s: exit %d: %s", v, r.Exit, r.Line())
		}
		if _, err := os.Lstat(filepath.Join(r.Home, "koan-root")); !os.IsNotExist(err) {
			t.Errorf("--ids-from %s made the root", v)
		}
	}
	r = testrun.Koan(t, "init", "--ids-from", "null")
	if r.Exit != 0 || r.Result()["first_id"] != json.Number("1") {
		t.Errorf("--ids-from null: %s", r.Line())
	}
	r = testrun.Run{Stdin: strings.NewReader(`{"ids_from":42}`)}.Koan(t, "init", "-i", "-")
	if r.Exit != 0 || r.Result()["first_id"] != json.Number("42") {
		t.Errorf("--input: %s", r.Line())
	}
}

func TestInitNotARoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "koan-root")
	os.Mkdir(root, 0o755)
	os.WriteFile(filepath.Join(root, "todo.md"), nil, 0o644)
	os.Mkdir(filepath.Join(root, "Tree"), 0o755)
	// Until then, not-initialized says to move it aside, not just to run
	// init, which would refuse it.
	r := testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	if e, _ := r.Result()["error"].(map[string]any); e["kind"] != "not-initialized" || !strings.Contains(e["message"].(string), "Move it aside") {
		t.Errorf("info: %s", r.Line())
	}
	r = testrun.Run{Home: home}.Koan(t, "task", "list")
	if msg, _ := r.Error()["message"].(string); r.Kind() != "not-initialized" || !strings.Contains(msg, "Move it aside") {
		t.Errorf("task list: %s", r.Line())
	}
	r = testrun.Run{Home: home}.Koan(t, "init")
	r.Check(t, "init")
	if r.Exit != 1 {
		t.Errorf("exit %d", r.Exit)
	}
	r.Golden(t, "init-not-a-root", *update, nil)
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Errorf("init changed a root that isn't one: %v", entries)
	}
}

// A symlinked root is used through its link, and its paths are given as
// written.
func TestInitSymlinkRoot(t *testing.T) {
	home, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, "koan-root")); err != nil {
		t.Fatal(err)
	}
	r := testrun.Run{Home: home}.Koan(t, "init")
	r.Check(t, "init")
	created, _ := r.Result()["made"].([]any)
	if r.Exit != 0 || len(created) != 3 {
		t.Errorf("exit %d: %s", r.Exit, r.Line())
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "koan.db")); err != nil {
		t.Error(err)
	}
	r = testrun.Run{Home: home}.Koan(t, "info")
	if r.Result()["state"] != "usable" {
		t.Errorf("info: %s", r.Line())
	}
}

func setFormat(t *testing.T, home string, format int) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, "koan-root", "koan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", format)); err != nil {
		t.Fatal(err)
	}
}

func TestInfoUnsupportedFormat(t *testing.T) {
	home := t.TempDir()
	testrun.Run{Home: home}.Koan(t, "init")
	setFormat(t, home, 2)
	r := testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	r.Golden(t, "info-unsupported-format", *update, nil)
	// init fails as every command does.
	r = testrun.Run{Home: home}.Koan(t, "init")
	r.Check(t, "init")
	if r.Exit != 1 || r.Kind() != "unsupported-format" {
		t.Errorf("init: exit %d: %s", r.Exit, r.Line())
	}
	golden(t, r, "init-unsupported-format")
}

func TestInfoCorrupt(t *testing.T) {
	home := t.TempDir()
	os.Mkdir(filepath.Join(home, "koan-root"), 0o755)
	os.WriteFile(filepath.Join(home, "koan-root", "koan.db"), []byte(strings.Repeat("not a database\n", 200)), 0o644)
	r := testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	r.Golden(t, "info-corrupt", *update, nil)
	r = testrun.Run{Home: home}.Koan(t, "init")
	r.Check(t, "init")
	if r.Exit != 1 || r.Kind() != "corrupt" {
		t.Errorf("init: exit %d: %s", r.Exit, r.Line())
	}
	golden(t, r, "init-corrupt")
}

// An unfinished init, a database with format 0 and no tables, is
// unsupported-format until init finishes it.
func TestUnfinishedInit(t *testing.T) {
	home := t.TempDir()
	os.Mkdir(filepath.Join(home, "koan-root"), 0o755)
	os.WriteFile(filepath.Join(home, "koan-root", "koan.db"), nil, 0o644)
	r := testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	if r.Result()["state"] != "unsupported-format" || r.Result()["format"] != json.Number("0") {
		t.Errorf("info: %s", r.Line())
	}
	r = testrun.Run{Home: home}.Koan(t, "init")
	r.Check(t, "init")
	r.Golden(t, "init-unfinished", *update, nil)
}

// Inits run at once on a fresh HOME all succeed, and exactly one makes
// each thing.
func TestInitRace(t *testing.T) {
	home := t.TempDir()
	const n = 6
	results := make([]*testrun.Result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = testrun.Run{Home: home}.Koan(t, "init")
		}()
	}
	wg.Wait()
	made := map[string]int{}
	for _, r := range results {
		if r.Exit != 0 {
			t.Fatalf("exit %d: %s", r.Exit, r.Line())
		}
		for _, c := range r.Result()["made"].([]any) {
			made[c.(string)]++
		}
	}
	for _, c := range []string{"root", "tree", "archive", "database"} {
		if made[c] != 1 {
			t.Errorf("%s made %d times", c, made[c])
		}
	}
}

// A root that has issued its last ID refuses every change that would
// issue another, as no-ids, writing nothing; changes that make nothing
// still run, and info gives no next_id.
func TestIDsRunOut(t *testing.T) {
	home := t.TempDir()
	testrun.Run{Home: home}.Koan(t, "init", "--ids-from", "999999999999998")
	ok(t, home, "folder", "create", "/work") // 999999999999998
	ok(t, home, "task", "create", "last")    // 999999999999999
	r := fails(t, home, "conflict", "no-ids", "task", "create", "one more")
	golden(t, r, "no-ids")
	fails(t, home, "conflict", "no-ids", "folder", "create", "/web")
	fails(t, home, "conflict", "no-ids", "block", "999999999999999", "--blocked-by", "/work.start")
	if r := batch(t, home, `{"tasks":[{"title":"t"}]}`); r.Kind() != "conflict" || r.Error()["rule"] != "no-ids" {
		t.Errorf("batch: %s", r.Line())
	}
	ok(t, home, "update", "999999999999999", "--title", "still")
	ok(t, home, "mv", "-T", "/work", "/Work")
	ok(t, home, "complete", "999999999999999")
	r = ok(t, home, "info")
	if c, _ := r.Result()["counts"].(map[string]any); r.Result()["next_id"] != nil || c["tasks"] != json.Number("1") || c["folders"] != json.Number("1") {
		t.Errorf("info: %s", r.Line())
	}
	if r := ok(t, home, "doctor"); len(r.Result()["findings"].([]any)) != 0 {
		t.Errorf("doctor: %s", r.Line())
	}
	checkTree(t, home, "@999999999999999.md=", "@Work/", "@Work/@notes.md=")
}

// An unknown option is usage, exit 2.
func TestUsageGolden(t *testing.T) {
	r := koan(t, root(t), "task", "create", "t", "--colour", "red")
	if r.Exit != 2 || r.Kind() != "usage" {
		t.Fatalf("exit %d: %s", r.Exit, r.Line())
	}
	golden(t, r, "usage")
}

// PRD Testing's Output row: every kind of failure has a golden result,
// and so does every conflict rule. internal is a bug in koan, and has
// none.
func TestGoldenEveryRefusal(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(testrun.Repo(), "testdata", "golden", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	kinds, rules := map[string]bool{}, map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Error *struct {
				Kind string  `json:"kind"`
				Rule *string `json:"rule"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &env) != nil || env.Error == nil {
			continue
		}
		kinds[env.Error.Kind] = true
		if env.Error.Rule != nil {
			rules[*env.Error.Rule] = true
		}
	}
	for _, k := range cli.Kinds {
		if !kinds[k] && k != cli.KindInternal {
			t.Errorf("no golden result for %s", k)
		}
	}
	for _, r := range cli.Rules {
		if !rules[r] {
			t.Errorf("no golden result for the rule %s", r)
		}
	}
}
