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

	"github.com/phansen314/koan/internal/testrun"
	_ "modernc.org/sqlite"
)

var update = flag.Bool("update", false, "rewrite the golden results in testdata/")

func TestMain(m *testing.M) { testrun.Main(m) }

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
}

func TestInfoRootWithoutDatabase(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "koan-root"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A name that differs only in case is not the database.
	os.WriteFile(filepath.Join(home, "koan-root", "KOAN.DB"), nil, 0o644)
	r := testrun.Run{Home: home}.Koan(t, "info")
	r.Check(t, "info")
	r.Golden(t, "info-no-database", *update, nil)
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
		{[]string{"version", "--frob=1"}, "--frob=1"},
		{[]string{"info", "-x"}, "-x"},
		{[]string{"info", "-i"}, "-i"},
		{[]string{"info", "--input"}, "--input"},
		{[]string{"info", "-i=in.json"}, "-i=in.json"},
		{[]string{"info", "-iin.json"}, "-iin.json"},
		{[]string{"info", "--Input", "in.json"}, "--Input"},
		{[]string{"info", "--inp", "in.json"}, "--inp"},
		{[]string{"info", "-i", "in.json", "-i", "in.json"}, "-i"},
		{[]string{"info", "-i", "in.json", "x"}, "x"},
		{[]string{"info", "x", "-i", "in.json"}, "x"},
		{[]string{"info", "-i", "in.json", "--", "x"}, "x"},
		{[]string{"version", "--help", "x"}, "--help"},
		{[]string{"version", "-h"}, "-h"},
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

func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"version", "--help"}, {"info", "--help"}} {
		r := testrun.Koan(t, args...)
		if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "usage: koan ") || r.Stderr != "" {
			t.Errorf("koan %q: exit %d: %q", args, r.Exit, r.Stdout)
		}
	}
	// --help with anything else is an unknown option, and the line prints
	// an envelope, which the runner decodes.
	for _, args := range [][]string{{"version", "x", "--help"}, {"info", "--contents", "--help"}} {
		r := testrun.Koan(t, args...)
		if r.Exit != 2 || r.Envelope == nil || r.OK() {
			t.Errorf("koan %q: exit %d: %q", args, r.Exit, r.Stdout)
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

	for _, v := range []string{"0", "-3", "abc", "1.5", "007", "1000000000000000"} {
		r := testrun.Koan(t, "init", "--ids-from", v)
		r.Check(t, "init")
		problems, _ := r.Error()["problems"].([]any)
		if r.Exit != 1 || r.Kind() != "invalid-input" || len(problems) != 1 || problems[0].(map[string]any)["field"] != "/ids_from" {
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
	r := testrun.Run{Home: home}.Koan(t, "init")
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
	created, _ := r.Result()["created"].([]any)
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
		for _, c := range r.Result()["created"].([]any) {
			made[c.(string)]++
		}
	}
	for _, c := range []string{"root", "tree", "archive", "database"} {
		if made[c] != 1 {
			t.Errorf("%s made %d times", c, made[c])
		}
	}
}
