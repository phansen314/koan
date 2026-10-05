package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/pick"
	"github.com/phansen314/koan/internal/schematest"
	"github.com/phansen314/koan/internal/store"
)

func testEnv(stdin string) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return Env{
		Ops:    ops.Env{Env: store.Env{FS: fsys.OS{}}, Clock: ops.RealClock},
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errOut,
		Getwd:  func() (string, error) { return "/work", nil },
	}, &out, &errOut
}

// result is one invocation's output, decoded when it is an envelope.
type result struct {
	code     int
	raw      string
	envelope map[string]any
}

func (r result) kind() string {
	e, _ := r.envelope["error"].(map[string]any)
	k, _ := e["kind"].(string)
	return k
}

// usageProblem is the one usage problem's argument ("" when absent) and reason.
func (r result) usageProblem(t *testing.T) (string, string) {
	t.Helper()
	ps := r.envelope["error"].(map[string]any)["details"].(map[string]any)["problems"].([]any)
	if len(ps) != 1 {
		t.Fatalf("got %d problems, want 1: %s", len(ps), r.raw)
	}
	p := ps[0].(map[string]any)
	arg, _ := p["argument"].(string)
	return arg, p["reason"].(string)
}

func run(t *testing.T, cmds []Command, stdin string, args ...string) result {
	t.Helper()
	env, out, errOut := testEnv(stdin)
	b, code, note := execute(cmds, args, env)
	code = deliver(env, b, code, note)
	r := result{code: code, raw: out.String()}
	if !strings.HasPrefix(r.raw, "{") {
		if errOut.Len() != 0 {
			t.Errorf("%q: help with stderr %q", args, errOut)
		}
		return r // help text
	}
	if strings.Count(r.raw, "\n") != 1 || !strings.HasSuffix(r.raw, "\n") {
		t.Errorf("%q: not one line: %q", args, r.raw)
	}
	if ok, f := schematest.Check(t, "envelope", out.Bytes()); !ok {
		t.Errorf("%q: envelope schema rejects at %s: %s", args, f, r.raw)
	}
	if err := json.Unmarshal(out.Bytes(), &r.envelope); err != nil {
		t.Fatal(err)
	}
	wantNote(t, r, errOut.String())
	if r.kind() == "usage" {
		d, _ := json.Marshal(r.envelope["error"].(map[string]any)["details"])
		if ok, f := schematest.Check(t, "usage-details", d); !ok {
			t.Errorf("%q: usage-details rejects at %s: %s", args, f, d)
		}
	}
	return r
}

// wantNote checks stderr against the delivered envelope (cli-spec.md,
// Output): one line naming a failure's kind and message, or counting
// warnings; nothing for a clean success.
func wantNote(t *testing.T, r result, stderr string) {
	t.Helper()
	var want string
	switch n := len(r.envelope["warnings"].([]any)); {
	case r.envelope["ok"] != true:
		e := r.envelope["error"].(map[string]any)
		msg := e["message"].(string)
		if e["kind"] == "usage" {
			msg = strings.TrimPrefix(msg, "usage: ")
		}
		want = oneLine(fmt.Sprintf("koan: %s: %s", e["kind"], msg)) + "\n"
	case n == 1:
		want = "koan: 1 warning (see .warnings in the output)\n"
	case n > 1:
		want = fmt.Sprintf("koan: %d warnings (see .warnings in the output)\n", n)
	}
	if stderr != want {
		t.Errorf("stderr %q, want %q", stderr, want)
	}
}

// A failure's note is its error line alone, even with warnings; no command
// yields that today, but an operation may.
func TestFailureNoteIgnoresWarnings(t *testing.T) {
	env := ops.Envelope{Error: errs.Busy(), Warnings: []errs.Warning{errs.CorruptFile("/r/1.json", 1), errs.CorruptFile("/r/2.json", 2)}}
	_, code, note := envelopeLine(env)
	if want := "koan: busy: another write held the write lock throughout the wait"; code != ExitError || note != want {
		t.Errorf("exit %d, note %q; want 1, %q", code, note, want)
	}
}

func TestOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                       "plain",
		`at "/a": x \ y`:              `at "/a": x \ y`,
		"/home/a\nb/tasks: é 😀":       `/home/a\nb/tasks: é 😀`,
		"a\r\tb\x1b[31mc\x7fd\u0085e": `a\r\tb\x1b[31mc\x7fd\u0085e`,
		"a\u2028b\u2029c":             `a\u2028b\u2029c`,
	} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersion(t *testing.T) {
	r := run(t, commands, "", "version")
	if r.code != ExitOK || r.envelope["ok"] != true {
		t.Fatalf("exit %d: %s", r.code, r.raw)
	}
	res, _ := json.Marshal(r.envelope["result"])
	if ok, f := schematest.Check(t, "version-output", res); !ok {
		t.Errorf("version-output rejects at %s: %s", f, res)
	}
}

// info reports an unlocatable config as state: ok, exit 0.
func TestInfo(t *testing.T) {
	r := run(t, commands, "", "info")
	if r.code != ExitOK || r.envelope["ok"] != true {
		t.Fatalf("exit %d: %s", r.code, r.raw)
	}
	res, _ := json.Marshal(r.envelope["result"])
	if ok, f := schematest.Check(t, "info-output", res); !ok {
		t.Errorf("info-output rejects at %s: %s", f, res)
	}
	config := r.envelope["result"].(map[string]any)["config"].(map[string]any)
	if config["path"] != nil || config["state"] != "missing" {
		t.Errorf("config %v, want path null and state missing", config)
	}
}

// show's argument reaches /id as an integer: a non-integer fails there, a
// valid ID gets as far as locating the config (none in testEnv).
// Commands whose one argument is a task ID.
func TestIDCommands(t *testing.T) {
	for _, name := range []string{"show", "done", "reopen"} {
		for _, tc := range []struct {
			args []string
			code int
			kind string
		}{
			{[]string{name}, ExitUsage, "usage"},
			{[]string{name, "abc"}, ExitError, "invalid-input"},
			{[]string{name, "042"}, ExitError, "invalid-input"},
			{[]string{name, "42"}, ExitError, "environment"},
			{[]string{name, "-i", "-"}, ExitError, "environment"},
		} {
			r := run(t, commands, `{"id": 42}`, tc.args...)
			if r.code != tc.code || r.kind() != tc.kind {
				t.Errorf("%q: exit %d kind %q, want %d %s: %s", tc.args, r.code, r.kind(), tc.code, tc.kind, r.raw)
			}
		}
		if arg, reason := run(t, commands, "", name).usageProblem(t); arg != "" || reason != "missing argument <id>" {
			t.Errorf("bare %s: %q %q", name, arg, reason)
		}
		if r := run(t, commands, "", name, "abc"); !strings.Contains(r.raw, `"field":"/id"`) {
			t.Errorf("%s abc: want a problem at /id: %s", name, r.raw)
		}
	}
}

func TestInputFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		code  int
		kind  string
		field string // the first invalid-input problem's field
	}{
		{"file", []string{"version", "-i", write("ok.json", `{}`)}, "", ExitOK, "", ""},
		{"stdin", []string{"version", "--input", "-"}, " {} ", ExitOK, "", ""},
		{"attached", []string{"version", "--input=-"}, "{}", ExitOK, "", ""},
		{"field", []string{"version", "-i", write("x.json", `{"x": 1}`)}, "", ExitError, "invalid-input", "/x"},
		{"not json", []string{"version", "-i", "-"}, "{", ExitError, "invalid-input", ""},
		{"two values", []string{"version", "-i", "-"}, "{} {}", ExitError, "invalid-input", ""},
		{"repeated key", []string{"version", "-i", "-"}, `{"a": 1, "a": 2}`, ExitError, "invalid-input", "/a"},
		{"missing file", []string{"version", "-i", filepath.Join(dir, "nope.json")}, "", ExitError, "io", ""},
		{"directory", []string{"version", "-i", dir}, "", ExitError, "io", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, commands, tc.stdin, tc.args...)
			if r.code != tc.code || r.kind() != tc.kind {
				t.Fatalf("exit %d kind %q, want %d %q: %s", r.code, r.kind(), tc.code, tc.kind, r.raw)
			}
			if tc.kind == "invalid-input" {
				ps := r.envelope["error"].(map[string]any)["details"].(map[string]any)["problems"].([]any)
				if f := ps[0].(map[string]any)["field"]; f != tc.field {
					t.Errorf("field %q, want %q", f, tc.field)
				}
			}
		})
	}
}

func TestUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		arg    string // "" when the problem names no token
		reason string // a substring
	}{
		{"bare", nil, "", "missing command"},
		{"unknown command", []string{"nosuch"}, "nosuch", "unknown command"},
		{"suggestion", []string{"verison"}, "verison", "did you mean version"},
		{"case", []string{"Version"}, "Version", "unknown command"},
		{"extra argument", []string{"version", "extra"}, "extra", "unexpected argument"},
		{"unknown option", []string{"version", "--bogus"}, "--bogus", "unknown flag"},
		{"unknown short", []string{"version", "-x"}, "-x", "unknown shorthand"},
		{"unknown before command", []string{"--bogus", "version"}, "--bogus", "unknown flag"},
		{"missing value", []string{"version", "--input"}, "--input", "needs an argument"},
		{"missing short value", []string{"version", "-i"}, "-i", "needs an argument"},
		{"input with argument", []string{"version", "-i", "-", "x"}, "x", "--input cannot be combined"},
		{"help command", []string{"help"}, "help", "use --help"},
		{"help topic", []string{"help", "shwo"}, "help", "use --help"},
		{"help command help", []string{"help", "--help"}, "help", "use --help"},
		{"completion", []string{"completion", "bash"}, "completion", "unknown command"},
		{"command after --", []string{"--", "version"}, "version", "the command must come before --"},
		{"unknown command after --", []string{"--", "verison"}, "verison", "did you mean version"},
		{"exclusive options", []string{"create", "t", "--notes", "x", "--notes-file", "-"}, "--notes-file", "--notes-file cannot be combined with --notes"},
		{"exclusive options, other order", []string{"create", "t", "--notes-file", "-", "--notes", "x"}, "--notes", "--notes cannot be combined with --notes-file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, commands, "{}", tc.args...)
			if r.code != ExitUsage || r.kind() != "usage" {
				t.Fatalf("exit %d kind %q, want usage: %s", r.code, r.kind(), r.raw)
			}
			arg, reason := r.usageProblem(t)
			if arg != tc.arg || !strings.Contains(reason, tc.reason) {
				t.Errorf("problem (%q, %q), want (%q, ...%q...)", arg, reason, tc.arg, tc.reason)
			}
		})
	}
	// The hidden completion command is never suggested.
	if _, reason := run(t, commands, "{}", "completio").usageProblem(t); reason != "unknown command" {
		t.Errorf("completio: %q, want no suggestion", reason)
	}
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"version", "--help"}, {"version", "-h"},
	} {
		r := run(t, commands, "", args...)
		if r.code != ExitOK || r.envelope != nil || !strings.Contains(r.raw, "Usage:") {
			t.Errorf("%q: exit %d: %s", args, r.code, r.raw)
		}
		// Help lists no command outside cli-spec.md.
		if strings.Contains(r.raw, "completion") || strings.Contains(r.raw, "help [command]") {
			t.Errorf("%q: lists help or completion: %s", args, r.raw)
		}
	}
}

// synthetic exercises every value type and the shape checks.
var synthetic = []Command{{
	Name: "t",
	Op:   "t",
	Args: []Arg{{Name: "id", Field: "/id", Type: Int}, {Name: "title", Field: "/title", Type: String}},
	Options: []Option{
		{Name: "folder", Field: "/folder", Type: String},
		{Name: "priority", Field: "/priority", Type: NullableInt},
		{Name: "count", Field: "/count", Type: Int},
		{Name: "recursive", Field: "/recursive", Type: Bool},
		{Name: "parents", Short: "p", Field: "/parents", Type: Bool},
		{Name: "tags", Field: "/tags", Type: StringList},
		{Name: "tags-add", Field: "/tags/add", Type: StringList},
		{Name: "blocked-by", Field: "/blocked_by", Type: IDList},
		{Name: "extra", Field: "/extra", Type: JSON},
		{Name: "extra-merge", Field: "/extra/merge", Type: JSON},
		{Name: "extra-remove", Field: "/extra/remove", Type: Repeated},
		{Name: "mode", Field: "/mode", Type: String, Required: true},
	},
}}

// captured runs args against synthetic, capturing what reaches ops.Run.
func captured(t *testing.T, args ...string) (string, []errs.Problem) {
	t.Helper()
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got *jsonio.Object
	var problems []errs.Problem
	runOp = func(_ string, in *jsonio.Object, ps []errs.Problem, _ ops.Env) ops.Envelope {
		got, problems = in, ps
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	r := run(t, synthetic, "{}", append([]string{"t"}, args...)...)
	if r.code != ExitOK {
		t.Fatalf("%q: exit %d: %s", args, r.code, r.raw)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), problems
}

func TestBuildInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"minimal", []string{"42", "x", "--mode", "m"}, `{"id":42,"title":"x","mode":"m"}`},
		{"options anywhere", []string{"--mode=m", "42", "--folder", "/a", "x"}, `{"id":42,"title":"x","folder":"/a","mode":"m"}`},
		{"not an integer", []string{"abc", "x", "--mode", "m", "--count", "2.0"}, `{"id":"abc","title":"x","count":"2.0","mode":"m"}`},
		{"null", []string{"1", "x", "--mode", "m", "--priority", "null", "--count", "null"}, `{"id":1,"title":"x","priority":null,"count":"null","mode":"m"}`},
		{"negative value", []string{"1", "x", "--mode", "m", "--priority", "-3"}, `{"id":1,"title":"x","priority":-3,"mode":"m"}`},
		{"booleans", []string{"1", "x", "--mode", "m", "--recursive=false", "-p"}, `{"id":1,"title":"x","recursive":false,"parents":true,"mode":"m"}`},
		{"empty list", []string{"1", "x", "--mode", "m", "--tags", ""}, `{"id":1,"title":"x","tags":[],"mode":"m"}`},
		{"lists join", []string{"1", "x", "--mode", "m", "--tags", "a, b", "--tags", "a,,c", "--blocked-by", "3,x,-0"}, `{"id":1,"title":"x","tags":["a"," b","a","","c"],"blocked_by":[3,"x",-0],"mode":"m"}`},
		{"nested", []string{"1", "x", "--mode", "m", "--tags-add", "a", "--extra-merge", `{"k":2.0}`, "--extra-remove", "a,b", "--extra-remove", "c"}, `{"id":1,"title":"x","tags":{"add":["a"]},"extra":{"merge":{"k":2.0},"remove":["a,b","c"]},"mode":"m"}`},
		{"last wins", []string{"1", "x", "--mode", "a", "--mode", "b"}, `{"id":1,"title":"x","mode":"b"}`},
		{"after --", []string{"--mode", "m", "--", "1", "-urgent"}, `{"id":1,"title":"-urgent","mode":"m"}`},
		{"lone dash", []string{"--mode", "m", "1", "-"}, `{"id":1,"title":"-","mode":"m"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ps := captured(t, tc.args...)
			if got != tc.want || len(ps) != 0 {
				t.Errorf("got  %s %v\nwant %s", got, ps, tc.want)
			}
		})
	}
}

func TestBuildInputProblems(t *testing.T) {
	deep := strings.Repeat("[", 9990) + strings.Repeat("]", 9990)
	for _, tc := range []struct {
		name   string
		args   []string
		want   string
		fields []string
	}{
		{"bad json", []string{"1", "x", "--mode", "m", "--extra", "{"}, `{"id":1,"title":"x","mode":"m"}`, []string{"/extra"}},
		{"repeated key", []string{"1", "x", "--mode", "m", "--extra-merge", `{"a":1,"a":2}`}, `{"id":1,"title":"x","mode":"m"}`, []string{"/extra/merge/a"}},
		// 9,990 levels fit the input, but not at /extra.
		{"too deep", []string{"1", "x", "--mode", "m", "--extra", deep}, `{"id":1,"title":"x","mode":"m"}`, []string{"/extra"}},
		{"not utf-8", []string{"1", "x\xff", "--mode", "m", "--tags", "a\xff"}, `{"id":1,"mode":"m"}`, []string{"/title", "/tags"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ps := captured(t, tc.args...)
			var fields []string
			for _, p := range ps {
				fields = append(fields, p.Field)
			}
			if got != tc.want || !slices.Equal(fields, tc.fields) {
				t.Errorf("got  %s %q\nwant %s %q", got, fields, tc.want, tc.fields)
			}
		})
	}
}

func TestShape(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		arg    string
		reason string
	}{
		{"missing argument", []string{"1", "--mode", "m"}, "", "missing argument <title>"},
		{"missing required option", []string{"1", "x"}, "", "missing required option --mode"},
		{"input with option", []string{"-i", "-", "--folder", "/a"}, "--folder", "--input cannot be combined"},
		{"bool with value token", []string{"1", "x", "--mode", "m", "--recursive", "false"}, "false", "unexpected argument"},
		{"bool with bad value", []string{"1", "x", "--mode", "m", "--recursive=maybe"}, "--recursive", "invalid argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, synthetic, "{}", append([]string{"t"}, tc.args...)...)
			if r.code != ExitUsage {
				t.Fatalf("exit %d: %s", r.code, r.raw)
			}
			arg, reason := r.usageProblem(t)
			if arg != tc.arg || !strings.Contains(reason, tc.reason) {
				t.Errorf("problem (%q, %q), want (%q, ...%q...)", arg, reason, tc.arg, tc.reason)
			}
		})
	}
	// --input alone satisfies required arguments and options.
	if got, _ := captured(t, "-i", "-"); got != `{}` {
		t.Errorf("--input: got %s", got)
	}
}

type failWriter struct{ writeErr, closeErr error }

// stderrWriter records what is written, and fails every write when err is
// set.
type stderrWriter struct {
	buf bytes.Buffer
	err error
}

func (w *stderrWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	return w.buf.Write(p)
}

func (w failWriter) Write(p []byte) (int, error) { return len(p), w.writeErr }
func (w failWriter) Close() error                { return w.closeErr }

func TestDeliver(t *testing.T) {
	const note = "koan: busy: another write held the write lock throughout the wait"
	for _, tc := range []struct {
		name      string
		w         failWriter
		stderrErr error
		want      int
		stderr    string // "notice" for the not-delivered notice
	}{
		{"ok", failWriter{}, nil, ExitUsage, note + "\n"},
		{"write fails", failWriter{writeErr: errors.New("EPIPE")}, nil, ExitNotDelivered, "notice"},
		{"close fails", failWriter{closeErr: errors.New("EIO")}, nil, ExitNotDelivered, "notice"},
		{"stderr fails", failWriter{}, errors.New("EPIPE"), ExitUsage, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr := &stderrWriter{err: tc.stderrErr}
			got := deliver(Env{Stdout: tc.w, Stderr: stderr}, []byte("x\n"), ExitUsage, note)
			s := stderr.buf.String()
			if tc.stderr == "notice" {
				if !strings.HasPrefix(s, "koan: result not delivered: ") || strings.Count(s, "\n") != 1 {
					t.Errorf("stderr %q, want only the notice", s)
				}
			} else if s != tc.stderr {
				t.Errorf("stderr %q, want %q", s, tc.stderr)
			}
			if got != tc.want {
				t.Errorf("exit %d, want %d", got, tc.want)
			}
		})
	}
}

// Every command runs an operation, or, with Run, has an input adapter.
func TestCommandsRunOperations(t *testing.T) {
	for _, c := range commands {
		if c.Run != nil {
			if _, _, e := ops.Decode(c.Op, &jsonio.Object{}); e != nil {
				t.Errorf("command %s has no input adapter %s: %v", c.Name, c.Op, e)
			}
			continue
		}
		if !slices.Contains(ops.Operations(), c.Op) {
			t.Errorf("command %s runs unknown operation %s", c.Name, c.Op)
		}
	}
}

// init's root, resolved as cli-spec.md, init, Input says.
func TestResolveRoot(t *testing.T) {
	cwd := func() (string, error) { return "/work/dir", nil }
	for _, tc := range []struct {
		name  string
		in    string
		home  string
		getwd func() (string, error)
		want  string // the input after, or the problem or error
	}{
		{"absolute", `{"root":"/a/../b"}`, "/h", cwd, `{"root":"/a/../b"}`},
		{"home", `{"root":"~/t"}`, "/h", cwd, `{"root":"/h/t"}`},
		{"bare ~", `{"root":"~"}`, "/h", cwd, `{"root":"/h"}`},
		{"home at /", `{"root":"~/t"}`, "/", cwd, `{"root":"//t"}`},
		{"no home", `{"root":"~/t"}`, "", cwd, `environment`},
		{"~user", `{"root":"~bob/t"}`, "/h", cwd, `problem /root: ~user/ is not supported: use ~/ or an absolute path`},
		{"relative", `{"root":"t"}`, "/h", cwd, `{"root":"/work/dir/t"}`},
		{"dot-dot kept", `{"root":"../t"}`, "/h", cwd, `{"root":"/work/dir/../t"}`},
		{"cwd is /", `{"root":"t"}`, "/h", func() (string, error) { return "/", nil }, `{"root":"/t"}`},
		{"no working directory", `{"root":"t"}`, "/h", func() (string, error) { return "", &os.PathError{Op: "getwd", Path: ".", Err: syscall.ENOENT} }, `io {"path":".","code":"ENOENT"}`},
		{"empty", `{"root":""}`, "/h", cwd, `{"root":""}`},
		{"not a string", `{"root":5}`, "/h", cwd, `{"root":5}`},
		{"absent", `{}`, "/h", cwd, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _, err := jsonio.ParseObject([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			env, _, _ := testEnv("")
			env.Ops.Home, env.Getwd = tc.home, tc.getwd
			ps, e := resolveRoot(in, env)
			var got string
			switch {
			case e != nil && e.Kind == errs.KindEnvironment:
				got = "environment"
			case e != nil:
				d, _ := json.Marshal(e.Details)
				got = string(e.Kind) + " " + string(d)
			case len(ps) > 0:
				got = "problem " + ps[0].Field + ": " + ps[0].Reason
			default:
				b, _ := json.Marshal(in)
				got = string(b)
			}
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// Both ways of giving init's root are resolved; the option maps too.
func TestInitInput(t *testing.T) {
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got string
	runOp = func(_ string, in *jsonio.Object, _ []errs.Problem, _ ops.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got = string(b)
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	for _, tc := range []struct {
		args        []string
		stdin, want string
	}{
		{[]string{"init", "t"}, "", `{"root":"/work/t"}`},
		{[]string{"init", "t", "--replace-config"}, "", `{"root":"/work/t","replace_config":true}`},
		{[]string{"init", "-i", "-"}, `{"root": "t", "replace_config": false}`, `{"root":"/work/t","replace_config":false}`},
	} {
		if r := run(t, commands, tc.stdin, tc.args...); r.code != ExitOK || got != tc.want {
			t.Errorf("%q: exit %d, input %s, want %s", tc.args, r.code, got, tc.want)
		}
	}
}

// create's input, including --notes-file (cli-spec.md, create, Input).
func TestCreateInput(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.md")
	bad := filepath.Join(dir, "bad.md")
	if err := os.WriteFile(notes, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("x\xff"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got string
	var fields []string
	runOp = func(_ string, in *jsonio.Object, ps []errs.Problem, _ ops.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got, fields = string(b), nil
		for _, p := range ps {
			fields = append(fields, p.Field)
		}
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	for _, tc := range []struct {
		name   string
		args   []string
		stdin  string
		want   string
		fields []string
	}{
		{"every option", []string{"create", "Book flights", "--folder", "/proj/travel", "--tags", "travel,urgent", "--priority", "2",
			"--blocked-by", "41,42", "--extra", `{"status":"waiting"}`, "--notes", "n"},
			"", `{"title":"Book flights","folder":"/proj/travel","priority":2,"tags":["travel","urgent"],"blocked_by":[41,42],"extra":{"status":"waiting"},"notes":"n"}`, nil},
		{"notes file, exactly as it is", []string{"create", "t", "--notes-file", notes}, "", `{"title":"t","notes":"line one\nline two\n"}`, nil},
		{"notes from stdin", []string{"create", "t", "--notes-file", "-"}, "from stdin\n", `{"title":"t","notes":"from stdin\n"}`, nil},
		{"empty stdin", []string{"create", "t", "--notes-file", "-"}, "", `{"title":"t","notes":""}`, nil},
		{"notes file not UTF-8", []string{"create", "t", "--notes-file", bad}, "", `{"title":"t"}`, []string{"/notes"}},
		{"input from stdin", []string{"create", "-i", "-"}, `{"title": "t", "notes": "n"}`, `{"title":"t","notes":"n"}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, fields = "", nil
			if r := run(t, commands, tc.stdin, tc.args...); r.code != ExitOK || got != tc.want || !slices.Equal(fields, tc.fields) {
				t.Errorf("exit %d, input %s %q\nwant %s %q", r.code, got, fields, tc.want, tc.fields)
			}
		})
	}

	// A notes file that can't be read stops the command with io.
	for _, tc := range []struct{ name, path, code string }{
		{"missing", filepath.Join(dir, "nope"), "ENOENT"},
		{"a directory", dir, "EISDIR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = ""
			r := run(t, commands, "", "create", "t", "--notes-file", tc.path)
			e, _ := r.envelope["error"].(map[string]any)
			d, _ := e["details"].(map[string]any)
			if r.code != ExitError || r.kind() != "io" || d["code"] != tc.code || d["path"] != tc.path || got != "" {
				t.Errorf("exit %d: %s (operation ran: %v)", r.code, r.raw, got != "")
			}
		})
	}

	// Usage errors: both notes options, or --notes-file with --input.
	for _, args := range [][]string{
		{"create", "t", "--notes", "a", "--notes-file", notes},
		{"create", "-i", "-", "--notes-file", "-"},
	} {
		if r := run(t, commands, "{}", args...); r.code != ExitUsage {
			t.Errorf("%q: exit %d: %s", args, r.code, r.raw)
		}
	}
}

// create-folder's input: the folder argument and -p.
func TestCreateFolderInput(t *testing.T) {
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got string
	runOp = func(_ string, in *jsonio.Object, _ []errs.Problem, _ ops.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got = string(b)
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	for _, tc := range []struct {
		args        []string
		stdin, want string
	}{
		{[]string{"create-folder", "/proj"}, "", `{"folder":"/proj"}`},
		{[]string{"create-folder", "-p", "/proj/travel/2026"}, "", `{"folder":"/proj/travel/2026","parents":true}`},
		{[]string{"create-folder", "/proj", "--parents=false"}, "", `{"folder":"/proj","parents":false}`},
		{[]string{"create-folder", "-i", "-"}, `{"folder": "/a", "parents": true}`, `{"folder":"/a","parents":true}`},
	} {
		got = ""
		if r := run(t, commands, tc.stdin, tc.args...); r.code != ExitOK || got != tc.want {
			t.Errorf("%q: exit %d, input %s, want %s", tc.args, r.code, got, tc.want)
		}
	}
}

// create-batch's input is only --input, which is required.
func TestCreateBatchInput(t *testing.T) {
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got string
	runOp = func(_ string, in *jsonio.Object, _ []errs.Problem, _ ops.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got = string(b)
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	if r := run(t, commands, `{"tasks": [{"title": "x"}]}`, "create-batch", "-i", "-"); r.code != ExitOK || got != `{"tasks":[{"title":"x"}]}` {
		t.Errorf("-i -: exit %d, input %s", r.code, got)
	}
	for _, tc := range []struct {
		args        []string
		arg, reason string
	}{
		{[]string{"create-batch"}, "", "missing required option --input"},
		{[]string{"create-batch", "plan.json"}, "plan.json", "unexpected argument"},
		{[]string{"create-batch", "-i", "-", "x"}, "x", "--input cannot be combined with arguments"},
		{[]string{"create-batch", "--folder", "/a"}, "--folder", "unknown flag"},
	} {
		got = ""
		r := run(t, commands, "", tc.args...)
		if r.code != ExitUsage || got != "" {
			t.Errorf("%q: exit %d, input %s: %s", tc.args, r.code, got, r.raw)
			continue
		}
		if arg, reason := r.usageProblem(t); arg != tc.arg || !strings.Contains(reason, tc.reason) {
			t.Errorf("%q: problem (%q, %q), want (%q, ...%q...)", tc.args, arg, reason, tc.arg, tc.reason)
		}
	}
}

// update's input: each option at its field, nested ones included.
func TestUpdateInput(t *testing.T) {
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got string
	runOp = func(_ string, in *jsonio.Object, _ []errs.Problem, _ ops.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got = string(b)
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"update", "42", "--priority", "3", "--tags-add", "urgent"}, `{"id":42,"priority":3,"tags":{"add":["urgent"]}}`},
		{[]string{"update", "42", "--extra-merge", `{"status":"waiting"}`}, `{"id":42,"extra":{"merge":{"status":"waiting"}}}`},
		{[]string{"update", "42", "--priority", "null", "--tags-remove", "urgent", "--extra-remove", "status", "--extra-remove", "a,b"},
			`{"id":42,"priority":null,"tags":{"remove":["urgent"]},"extra":{"remove":["status","a,b"]}}`},
		{[]string{"update", "42", "--tags-replace-all", ""}, `{"id":42,"tags":{"replace_all":[]}}`},
		{[]string{"update", "42", "--title", "New", "--extra-replace-all", "{}"}, `{"id":42,"title":"New","extra":{"replace_all":{}}}`},
		{[]string{"update", "42"}, `{"id":42}`}, // the operation reports that nothing is to change
	} {
		got = ""
		if r := run(t, commands, "", tc.args...); r.code != ExitOK || got != tc.want {
			t.Errorf("%q: exit %d, input %s\nwant %s", tc.args, r.code, got, tc.want)
		}
	}
}

// block's and unblock's input, and --blockers required unless --input is
// given.
func TestBlockersInput(t *testing.T) {
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got string
	runOp = func(_ string, in *jsonio.Object, _ []errs.Problem, _ ops.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got = string(b)
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	for _, name := range []string{"block", "unblock"} {
		for _, tc := range []struct {
			args        []string
			stdin, want string
		}{
			{[]string{name, "42", "--blockers", "41,43"}, "", `{"id":42,"blockers":[41,43]}`},
			{[]string{name, "42", "--blockers", "41", "--blockers", "43"}, "", `{"id":42,"blockers":[41,43]}`},
			{[]string{name, "-i", "-"}, `{"id": 42, "blockers": [7]}`, `{"id":42,"blockers":[7]}`},
		} {
			got = ""
			if r := run(t, commands, tc.stdin, tc.args...); r.code != ExitOK || got != tc.want {
				t.Errorf("%q: exit %d, input %s, want %s", tc.args, r.code, got, tc.want)
			}
		}
		r := run(t, commands, "", name, "42")
		if _, reason := r.usageProblem(t); r.code != ExitUsage || reason != "missing required option --blockers" {
			t.Errorf("%s without --blockers: exit %d, %q", name, r.code, reason)
		}
	}
}

// list's input: options only, --recursive a flag that --recursive=false
// turns off. frontier shares --folder and --recursive.
func TestListInput(t *testing.T) {
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got string
	runOp = func(_ string, in *jsonio.Object, _ []errs.Problem, _ ops.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got = string(b)
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list"}, `{}`},
		{[]string{"list", "--folder", "/proj", "--recursive=false"}, `{"folder":"/proj","recursive":false}`},
		{[]string{"list", "--readiness", "done", "--include-folders"}, `{"readiness":["done"],"include_folders":true}`},
		{[]string{"list", "--readiness", "ready,blocked", "--readiness", "done"}, `{"readiness":["ready","blocked","done"]}`},
		{[]string{"list", "--tags-any", "a,b", "--tags-all", "c", "--limit", "5", "--fields", "title,folder"},
			`{"tags_any":["a","b"],"tags_all":["c"],"limit":5,"fields":["title","folder"]}`},
		{[]string{"list", "--limit", "x", "--fields", ""}, `{"limit":"x","fields":[]}`},
	} {
		got = ""
		if r := run(t, commands, "", tc.args...); r.code != ExitOK || got != tc.want {
			t.Errorf("%q: exit %d, input %s, want %s", tc.args, r.code, got, tc.want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"frontier"}, `{}`},
		{[]string{"frontier", "--folder", "/proj", "--recursive=false"}, `{"folder":"/proj","recursive":false}`},
		{[]string{"frontier", "--limit", "10", "--fields", "id,title,priority", "--tags-any", "urgent"},
			`{"tags_any":["urgent"],"limit":10,"fields":["id","title","priority"]}`},
		{[]string{"frontier", "--limit=0", "--tags-all", "a", "--tags-all", "b"}, `{"tags_all":["a","b"],"limit":0}`},
	} {
		got = ""
		if r := run(t, commands, "", tc.args...); r.code != ExitOK || got != tc.want {
			t.Errorf("%q: exit %d, input %s, want %s", tc.args, r.code, got, tc.want)
		}
	}
	for _, args := range [][]string{{"list", "/proj"}, {"frontier", "/proj"}, {"frontier", "--readiness", "ready"}, {"list", "--include-complete"}} {
		if r := run(t, commands, "", args...); r.code != ExitUsage {
			t.Errorf("%q: exit %d: %s", args, r.code, r.raw)
		}
	}
}

// delete's, delete-folder's, move's, and move-folder's input, and --to
// required unless --input is given.
func TestDeleteMoveInput(t *testing.T) {
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got string
	runOp = func(_ string, in *jsonio.Object, _ []errs.Problem, _ ops.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got = string(b)
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	for _, tc := range []struct {
		args        []string
		stdin, want string
	}{
		{[]string{"delete", "42"}, "", `{"id":42}`},
		{[]string{"delete-folder", "/proj"}, "", `{"folder":"/proj"}`},
		{[]string{"delete-folder", "-r", "/proj"}, "", `{"folder":"/proj","recursive":true}`},
		{[]string{"delete-folder", "/proj", "--recursive"}, "", `{"folder":"/proj","recursive":true}`},
		{[]string{"move", "42", "--to", "/proj"}, "", `{"id":42,"to":"/proj"}`},
		{[]string{"move", "-p", "42", "--to", "/proj/a"}, "", `{"id":42,"to":"/proj/a","parents":true}`},
		{[]string{"move-folder", "/proj", "--to", "/archive"}, "", `{"folder":"/proj","to":"/archive"}`},
		{[]string{"move-folder", "/proj", "--to", "/a/b", "-p"}, "", `{"folder":"/proj","to":"/a/b","parents":true}`},
		{[]string{"move", "-i", "-"}, `{"id": 42, "to": "/a"}`, `{"id":42,"to":"/a"}`},
	} {
		got = ""
		if r := run(t, commands, tc.stdin, tc.args...); r.code != ExitOK || got != tc.want {
			t.Errorf("%q: exit %d, input %s, want %s", tc.args, r.code, got, tc.want)
		}
	}
	for _, args := range [][]string{{"move", "42"}, {"move-folder", "/proj"}} {
		r := run(t, commands, "", args...)
		if _, reason := r.usageProblem(t); r.code != ExitUsage || reason != "missing required option --to" {
			t.Errorf("%q without --to: exit %d, %q", args, r.code, reason)
		}
	}
}

// pick's input: each option at its field, --from's envelopes resolved to
// ids, and the clashes (pick-spec.md, Command).
func TestPickInput(t *testing.T) {
	dir := t.TempDir()
	file := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	listed := file("list.json", `{"ok":true,"result":{"tasks":[{"id":42,"title":"a"},{"id":7},{"id":42}],"total":3,"truncated":false},"warnings":[]}`+"\n")
	saved := runPick
	t.Cleanup(func() { runPick = saved })
	var got string
	var problems []errs.Problem
	runPick = func(in *jsonio.Object, ps []errs.Problem, _ pick.Env) ops.Envelope {
		b, _ := json.Marshal(in)
		got, problems = string(b), ps
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		want  string
		field string // the one problem's field, if any
	}{
		{"none", nil, "", `{}`, ""},
		{"every task option", []string{"--folder", "/work", "--recursive=false", "--scope", "ready", "--tags-any", "a,b", "--tags-all", "c",
			"--ids", "41,42", "--query", "renew pass", "--select-one", "--exit-zero", "--fields", "id,title"},
			"", `{"folder":"/work","recursive":false,"scope":"ready","tags_any":["a","b"],"tags_all":["c"],"ids":[41,42],"query":"renew pass","select_one":true,"exit_zero":true,"fields":["id","title"]}`, ""},
		{"source", []string{"--source", "koan frontier --tags-any today"}, "", `{"source":"koan frontier --tags-any today"}`, ""},
		{"folders", []string{"--folders", "--folder", "/a"}, "", `{"folder":"/a","folders":true}`, ""},
		{"empty ids", []string{"--ids", ""}, "", `{"ids":[]}`, ""},
		// --from: the IDs in order, without duplicates.
		{"from list", []string{"--from", listed}, "", `{"ids":[42,7]}`, ""},
		{"from stdin", []string{"--from", "-"}, `{"ok":true,"result":{"tasks":[{"id":3}]},"warnings":[]}`, `{"ids":[3]}`, ""},
		{"from show", []string{"--from", "-"}, `{"ok":true,"result":{"tasks":[{"id":4,"title":"x"}]},"warnings":[]}`, `{"ids":[4]}`, ""},
		{"from create", []string{"--from", "-"}, `{"ok":true,"result":{"schema":1,"id":51,"title":"x"},"warnings":[]}`, `{"ids":[51]}`, ""},
		{"from pick", []string{"--from", "-"}, `{"ok":true,"result":{"tasks":[{"id":5},{"id":6}],"missing":[],"actions":[],"notes_edited":[]},"warnings":[]}`, `{"ids":[5,6]}`, ""},
		{"from empty tasks", []string{"--from", "-"}, `{"ok":true,"result":{"tasks":[],"total":0,"truncated":false},"warnings":[]}`, `{"ids":[]}`, ""},
		{"from, ids judged by the adapter", []string{"--from", "-"}, `{"ok":true,"result":{"tasks":[{"id":"x"},{"id":2.0}]},"warnings":[]}`, `{"ids":["x",2.0]}`, ""},
		{"from, with the other options", []string{"--from", "-", "--scope", "open"}, `{"ok":true,"result":{"id":1},"warnings":[]}`, `{"scope":"open","ids":[1]}`, ""},
		// Content that is no accepted envelope is a problem at /ids.
		{"from not JSON", []string{"--from", "-"}, `{"ok":`, `{}`, "/ids"},
		{"from empty", []string{"--from", "-"}, ``, `{}`, "/ids"},
		{"from two values", []string{"--from", "-"}, `{"ok":true,"result":{"id":1},"warnings":[]}{}`, `{}`, "/ids"},
		{"from not an object", []string{"--from", "-"}, `[1]`, `{}`, "/ids"},
		{"from no ok", []string{"--from", "-"}, `{"result":{"id":1}}`, `{}`, "/ids"},
		{"from repeated key", []string{"--from", "-"}, `{"ok":true,"ok":true,"result":{"id":1}}`, `{}`, "/ids"},
		{"from no result", []string{"--from", "-"}, `{"ok":true,"warnings":[]}`, `{}`, "/ids"},
		{"from tasks not an array", []string{"--from", "-"}, `{"ok":true,"result":{"tasks":{}}}`, `{}`, "/ids"},
		{"from a task with no id", []string{"--from", "-"}, `{"ok":true,"result":{"tasks":[{"id":1},{"title":"x"}]}}`, `{}`, "/ids"},
		{"from neither tasks nor id", []string{"--from", "-"}, `{"ok":true,"result":{"folders":["/a"],"missing":[],"actions":[]},"warnings":[]}`, `{}`, "/ids"},
		{"from not UTF-8", []string{"--from", "-"}, "{\"ok\":true,\"result\":{\"id\":1},\"x\":\"\xff\"}", `{}`, "/ids"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, problems = "", nil
			r := run(t, commands, tc.stdin, append([]string{"pick"}, tc.args...)...)
			var fields []string
			for _, p := range problems {
				fields = append(fields, p.Field)
			}
			var want []string
			if tc.field != "" {
				want = []string{tc.field}
			}
			if r.code != ExitOK || got != tc.want || !slices.Equal(fields, want) {
				t.Errorf("exit %d, input %s %v\nwant %s %q", r.code, got, problems, tc.want, want)
			}
		})
	}

	// An upstream failure names its kind.
	got, problems = "", nil
	run(t, commands, `{"ok":false,"error":{"kind":"not-found","message":"no task 9"},"warnings":[]}`, "pick", "--from", "-")
	if len(problems) != 1 || problems[0].Reason != "upstream failed with not-found: no task 9" {
		t.Errorf("upstream failure: %v", problems)
	}

	// A file that can't be read stops the command with io.
	got = ""
	r := run(t, commands, "", "pick", "--from", filepath.Join(dir, "nope"))
	if r.code != ExitError || r.kind() != "io" || got != "" {
		t.Errorf("unreadable --from: exit %d: %s (pick ran: %v)", r.code, r.raw, got != "")
	}

	// --ids and --from both set /ids: a usage error, as is --from with --input.
	for _, args := range [][]string{
		{"pick", "--ids", "1", "--from", listed},
		{"pick", "-i", "-", "--from", listed},
		{"pick", "-i", "-", "--folders"},
		{"pick", "extra"},
	} {
		if r := run(t, commands, "{}", args...); r.code != ExitUsage {
			t.Errorf("%q: exit %d: %s", args, r.code, r.raw)
		}
	}
}

// What pick's input schema and adapter refuse reaches the envelope as
// invalid-input, through the command line or --input alike.
func TestPickInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		stdin  string
		fields []string
	}{
		{"ids and source", []string{"--ids", "1", "--source", "koan list"}, "", []string{""}},
		{"from and source", []string{"--from", "-", "--source", "koan list"}, `{"ok":true,"result":{"id":1},"warnings":[]}`, []string{""}},
		{"folders and task options", []string{"--folders", "--scope", "all", "--fields", "id", "--tags-any", "a"}, "", []string{"/fields", "/scope", "/tags_any"}},
		{"bad values", []string{"--scope", "done", "--ids", "0,x", "--folder", "work", "--source", ""}, "", []string{"", "/folder", "/ids/0", "/ids/1", "/scope", "/source"}},
		{"bad --from and a bad option", []string{"--from", "-", "--scope", "done"}, `{"ok":false,"error":{"kind":"busy","message":"m"},"warnings":[]}`, []string{"/ids", "/scope"}},
		{"input file", []string{"-i", "-"}, `{"folders": true, "ids": [1]}`, []string{"/ids"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, commands, tc.stdin, append([]string{"pick"}, tc.args...)...)
			if r.code != ExitError || r.kind() != "invalid-input" {
				t.Fatalf("exit %d: %s", r.code, r.raw)
			}
			var fields []string
			for _, p := range r.envelope["error"].(map[string]any)["details"].(map[string]any)["problems"].([]any) {
				fields = append(fields, p.(map[string]any)["field"].(string))
			}
			if !slices.Equal(fields, tc.fields) {
				t.Errorf("fields %q, want %q: %s", fields, tc.fields, r.raw)
			}
		})
	}
}
