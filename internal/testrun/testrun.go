// Package testrun runs the koan binary for tests: it builds it once, runs
// it with HOME set to a temp directory, reads its one line of output, and
// checks that line against schemas/ by command name.
//
// Only tests import it.
package testrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Repo is the repository's root directory.
func Repo() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

var (
	buildDir string
	binary   string
)

// Main builds the binary, runs the package's tests, and removes the
// build. Call it from TestMain.
// KOAN_TEST_BINARY, when set, names a binary already built, for a test
// that runs the tests again somewhere else.
func Main(m *testing.M) {
	if b := os.Getenv("KOAN_TEST_BINARY"); b != "" {
		binary = b
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "koan-test-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	buildDir = dir
	binary = filepath.Join(dir, "koan")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/koan")
	cmd.Dir = Repo()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building koan: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// Binary is the built koan.
func Binary(t testing.TB) string {
	t.Helper()
	if binary == "" {
		t.Fatal("testrun.Main did not run: call it from TestMain")
	}
	return binary
}

// Run is one run of koan.
type Run struct {
	Home  string    // HOME for the run; a new temp directory when empty
	Stdin io.Reader // standard input; empty when nil
	Dir   string    // working directory; Home when empty
}

// Result is what one run printed and how it exited.
type Result struct {
	Args     []string
	Home     string
	Stdout   string
	Stderr   string
	Exit     int
	Envelope map[string]any // the one line, decoded; numbers as json.Number
}

// Koan runs koan with a fresh HOME and checks its output is one line of
// JSON, unless the line is --help alone.
func Koan(t testing.TB, args ...string) *Result {
	t.Helper()
	return Run{}.Koan(t, args...)
}

// Koan runs koan as r says.
func (r Run) Koan(t testing.TB, args ...string) *Result {
	t.Helper()
	if r.Home == "" {
		r.Home = t.TempDir()
	}
	if r.Dir == "" {
		r.Dir = r.Home
	}
	cmd := exec.Command(Binary(t), args...)
	cmd.Dir = r.Dir
	// A shell sets PWD to the directory as it was given, so koan sees the
	// working directory as written, not its real path.
	cmd.Env = append(environ(r.Home), "PWD="+r.Dir)
	cmd.Stdin = r.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := &Result{Args: args, Home: r.Home, Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		res.Exit = exit.ExitCode()
	case err != nil:
		t.Fatalf("koan %q: %v", args, err)
	}
	if helpLine(args) {
		return res
	}
	res.Envelope = res.decode(t)
	return res
}

// commands are the spec's commands, of one word or two.
var commands = []string{
	"task create", "task show", "task list", "task frontier", "task why", "task update",
	"task complete", "task reopen", "task block", "task unblock", "task move", "task delete",
	"task archive", "task claim", "task claim-next", "task release",
	"folder create", "folder show", "folder list", "folder update", "folder move",
	"folder delete", "folder archive",
	"batch", "where", "context", "marker bind", "marker unbind",
	"init", "info", "version", "doctor", "repair", "backup",
}

// helpLine reports whether a line prints usage text instead of an
// envelope: --help alone, or right after a command. --help with anything
// else is an unknown option, and prints an envelope.
func helpLine(args []string) bool {
	n := len(args)
	if n == 0 || args[n-1] != "--help" {
		return false
	}
	return n == 1 || slices.Contains(commands, strings.Join(args[:n-1], " "))
}

// environ is the environment with HOME replaced, so no run can reach the
// real ~/koan-root, and PWD left out.
func environ(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "PWD=") {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+home)
}

func (r *Result) decode(t testing.TB) map[string]any {
	t.Helper()
	line, rest, ok := strings.Cut(r.Stdout, "\n")
	if !ok || rest != "" {
		t.Fatalf("koan %q: stdout is not exactly one line:\n%s", r.Args, r.Stdout)
	}
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("koan %q: stdout is not a JSON object: %v\n%s", r.Args, err, line)
	}
	if strings.Count(r.Stderr, "\n") > 1 {
		t.Errorf("koan %q: more than one line on stderr:\n%s", r.Args, r.Stderr)
	}
	return env
}

// OK is whether the run succeeded.
func (r *Result) OK() bool { return r.Envelope["ok"] == true }

// Result is the success's result.
func (r *Result) Result() map[string]any {
	m, _ := r.Envelope["result"].(map[string]any)
	return m
}

// Error is the failure's error.
func (r *Result) Error() map[string]any {
	m, _ := r.Envelope["error"].(map[string]any)
	return m
}

// Kind is the failure's error kind, or "" on success.
func (r *Result) Kind() string {
	s, _ := r.Error()["kind"].(string)
	return s
}

// Line is the one line of output, without its newline.
func (r *Result) Line() string { return strings.TrimSuffix(r.Stdout, "\n") }

// Check validates the run's output against the envelope schema and, on
// success, the result schema of the command named, such as "task create",
// and checks every object's fields are in the schema's order.
func (r *Result) Check(t testing.TB, command string) {
	t.Helper()
	s := Load(t)
	id := strings.ReplaceAll(command, " ", "-")
	if err := s.Validate("envelope", "", r.Line()); err != nil {
		t.Errorf("koan %q: envelope fails its schema: %v\n%s", r.Args, err, r.Line())
	}
	if err := s.Order("envelope", "", r.Line()); err != nil {
		t.Errorf("koan %q: envelope: %v\n%s", r.Args, err, r.Line())
	}
	if !r.OK() {
		return
	}
	raw, _ := json.Marshal(r.Result())
	if err := s.Validate(id, "/$defs/result", string(raw)); err != nil {
		t.Errorf("koan %q: result fails schema %s: %v\n%s", r.Args, id, err, r.Line())
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	json.Unmarshal([]byte(r.Line()), &env)
	if err := s.Order(id, "/$defs/result", string(env.Result)); err != nil {
		t.Errorf("koan %q: result: %v\n%s", r.Args, err, r.Line())
	}
}

// Golden compares the run's line, with its HOME written $HOME, to
// testdata/golden/<name>.json, rewriting the file when update is set.
// Keys in replace are swapped for their values first, for fields that
// change from build to build.
func (r *Result) Golden(t testing.TB, name string, update bool, replace map[string]string) {
	t.Helper()
	got := strings.ReplaceAll(r.Line(), r.Home, "$HOME")
	for old, new := range replace {
		got = strings.ReplaceAll(got, old, new)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(got), "", "  "); err != nil {
		t.Fatalf("golden %s: %v", name, err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join(Repo(), "testdata", "golden", name+".json")
	if update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v (run with -update to write it)", name, err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Errorf("golden %s differs:\ngot:\n%s\nwant:\n%s", name, pretty.Bytes(), want)
	}
}

var (
	loadOnce sync.Once
	loaded   *Schemas
	loadErr  error
)

// Load reads schemas/ once.
func Load(t testing.TB) *Schemas {
	t.Helper()
	loadOnce.Do(func() { loaded, loadErr = LoadDir(filepath.Join(Repo(), "schemas")) })
	if loadErr != nil {
		t.Fatalf("loading schemas: %v", loadErr)
	}
	return loaded
}
