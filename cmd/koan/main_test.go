package main_test

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
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
	r.Golden(t, "version", *update, map[string]string{
		`"go":"` + res["go"].(string) + `"`: `"go":"$GO"`,
		commit:                              `"commit":"$COMMIT"`,
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
