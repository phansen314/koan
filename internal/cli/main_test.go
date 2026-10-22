package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type run struct {
	stdout, stderr string
	code           int
}

func runMain(t *testing.T, stdin string, cmds []*Command, args ...string) run {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var out, errw bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &out, &errw, cmds)
	return run{out.String(), errw.String(), code}
}

func TestMainSuccess(t *testing.T) {
	r := runMain(t, "", []*Command{fake}, "thing", "make", "a<b>&c", "--tags", "x")
	want := `{"ok":true,"result":{"tags":["x"],"title":"a<b>&c"},"warnings":[]}` + "\n"
	if r.stdout != want || r.stderr != "" || r.code != 0 {
		t.Errorf("got %q %q %d", r.stdout, r.stderr, r.code)
	}
}

func TestMainUsage(t *testing.T) {
	r := runMain(t, "", []*Command{bare}, "version", "x")
	want := `{"ok":false,"error":{"kind":"usage","rule":null,"message":"version takes no subject","ids":[],"paths":[],"problems":[{"field":"x","reason":"version takes no subject"}],"archived":[]},"warnings":[]}` + "\n"
	if r.stdout != want || r.stderr != "koan: usage: version takes no subject\n" || r.code != ExitUsage {
		t.Errorf("got %q %q %d", r.stdout, r.stderr, r.code)
	}
}

func TestMainWarnings(t *testing.T) {
	warn := &Command{Name: "warn", Run: func(*Env, Input) (any, []Warning, *Error) {
		return struct{}{}, []Warning{{Kind: "pending-step", Message: "m"}, {Kind: "stuck-step", Message: "n", Paths: []string{"/a"}}}, nil
	}}
	r := runMain(t, "", []*Command{warn}, "warn")
	want := `{"ok":true,"result":{},"warnings":[{"kind":"pending-step","message":"m","ids":[],"paths":[]},{"kind":"stuck-step","message":"n","ids":[],"paths":["/a"]}]}` + "\n"
	if r.stdout != want || r.stderr != "koan: 2 warnings\n" || r.code != 0 {
		t.Errorf("got %q %q %d", r.stdout, r.stderr, r.code)
	}
	if got := stderrLine(nil, []Warning{{Kind: "pending-step", Message: "m"}}); got != "koan: 1 warning\n" {
		t.Errorf("one warning: got %q", got)
	}
}

func TestMainFailureKinds(t *testing.T) {
	for _, kind := range Kinds {
		e := NewError(kind, "first line\nsecond line")
		if kind == KindConflict {
			e = Conflict(RuleCycle, "first line\nsecond line")
			e.IDs = []int64{8, 10, 57}
		}
		c := &Command{Name: "fail", Run: func(*Env, Input) (any, []Warning, *Error) { return nil, nil, e }}
		r := runMain(t, "", []*Command{c}, "fail")
		wantCode := ExitFailure
		if kind == KindUsage {
			wantCode = ExitUsage
		}
		if r.code != wantCode {
			t.Errorf("%s: exit %d, want %d", kind, r.code, wantCode)
		}
		if want := "koan: " + kind + ": first line second line\n"; r.stderr != want {
			t.Errorf("%s: stderr %q, want %q", kind, r.stderr, want)
		}
		if !strings.Contains(r.stdout, `"kind":"`+kind+`"`) || strings.Count(r.stdout, "\n") != 1 {
			t.Errorf("%s: stdout %q", kind, r.stdout)
		}
	}
	r := runMain(t, "", []*Command{{Name: "fail", Run: func(*Env, Input) (any, []Warning, *Error) {
		return nil, nil, Conflict(RuleCycle, "m")
	}}}, "fail")
	if !strings.Contains(r.stdout, `"rule":"cycle"`) {
		t.Errorf("conflict without its rule: %s", r.stdout)
	}
}

func TestMainInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "in.json")
	os.WriteFile(file, []byte(`{"title":"from file","priority":3}`), 0o644)

	r := runMain(t, "", []*Command{fake}, "thing", "make", "-i", file)
	if want := `{"ok":true,"result":{"priority":3,"title":"from file"},"warnings":[]}` + "\n"; r.stdout != want {
		t.Errorf("file: got %q", r.stdout)
	}
	r = runMain(t, `{"title":"from stdin"}`, []*Command{fake}, "thing", "make", "--input", "-")
	if want := `{"ok":true,"result":{"title":"from stdin"},"warnings":[]}` + "\n"; r.stdout != want {
		t.Errorf("stdin: got %q", r.stdout)
	}
	// Standard input is read only when - names it.
	r = runMain(t, `{"title":"never read"}`, []*Command{fake}, "thing", "make", "t")
	if strings.Contains(r.stdout, "never read") {
		t.Errorf("read stdin unasked: %q", r.stdout)
	}
}

func TestMainInputRefused(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	cases := []struct {
		name  string
		args  []string
		stdin string
		kind  string
		field string
		path  string
	}{
		{"missing file", []string{"-i", filepath.Join(dir, "nope.json")}, "", KindIO, "", filepath.Join(dir, "nope.json")},
		{"not JSON", []string{"-i", write("bad.json", `{"title":`)}, "", KindInvalidInput, "", ""},
		{"two values", []string{"-i", "-"}, `{} {}`, KindInvalidInput, "", ""},
		{"not an object", []string{"-i", "-"}, `["t"]`, KindInvalidInput, "", ""},
		{"empty", []string{"-i", "-"}, ``, KindInvalidInput, "", ""},
		{"not UTF-8", []string{"-i", "-"}, "{\"title\":\"t\xff\"}", KindInvalidInput, "", ""},
		{"unknown field", []string{"-i", "-"}, `{"title":"t","frob":1}`, KindInvalidInput, "/frob", ""},
	}
	for _, c := range cases {
		r := runMain(t, c.stdin, []*Command{fake}, append([]string{"thing", "make"}, c.args...)...)
		if r.code != ExitFailure || !strings.Contains(r.stdout, `"kind":"`+c.kind+`"`) {
			t.Errorf("%s: got %d %s", c.name, r.code, r.stdout)
		}
		if c.kind == KindInvalidInput && !strings.Contains(r.stdout, `"field":"`+c.field+`"`) {
			t.Errorf("%s: want problem at %q: %s", c.name, c.field, r.stdout)
		}
		if c.path != "" && !strings.Contains(r.stdout, `"paths":["`+c.path+`"]`) {
			t.Errorf("%s: want path %s: %s", c.name, c.path, r.stdout)
		}
	}
}

func TestMainPanic(t *testing.T) {
	c := &Command{Name: "boom", Run: func(*Env, Input) (any, []Warning, *Error) { panic("bad") }}
	r := runMain(t, "", []*Command{c}, "boom")
	if r.code != ExitFailure || !strings.HasPrefix(r.stdout, `{"ok":false,"error":{"kind":"internal"`) || r.stderr != "koan: internal: koan failed: bad\n" {
		t.Errorf("got %q %q %d", r.stdout, r.stderr, r.code)
	}
}

func TestMainUnencodable(t *testing.T) {
	c := &Command{Name: "nan", Run: func(*Env, Input) (any, []Warning, *Error) { return map[string]any{"x": func() {}}, nil, nil }}
	r := runMain(t, "", []*Command{c}, "nan")
	if r.code != ExitFailure || !strings.Contains(r.stdout, `"kind":"internal"`) {
		t.Errorf("got %q %d", r.stdout, r.code)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("no space") }

func TestMainOutputFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var errw bytes.Buffer
	if code := Main([]string{"version"}, strings.NewReader(""), failWriter{}, &errw, []*Command{bare}); code != ExitOutput {
		t.Errorf("exit %d, want %d", code, ExitOutput)
	}
}

// Help is cobra's: --help or -h anywhere before -- prints the help of the
// command named, to standard output with exit 0, and so does koan help.
func TestMainHelp(t *testing.T) {
	cmds := []*Command{fake, bare}
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"--help"}, []string{"thing", "version", "completion"}},
		{[]string{"-h"}, []string{"thing", "version"}},
		{[]string{"help"}, []string{"thing", "version"}},
		{[]string{"thing", "--help"}, []string{"make"}},
		{[]string{"thing", "make", "--help"}, []string{"koan thing make <title>", "--priority", "--contents", "--note-file", "--input"}},
		{[]string{"thing", "make", "t", "--help"}, []string{"koan thing make <title>"}},
		{[]string{"thing", "make", "t", "--name", "n", "-h"}, []string{"koan thing make <title>"}},
		{[]string{"help", "thing", "make"}, []string{"koan thing make <title>"}},
		{[]string{"version", "x", "--help"}, []string{"koan version"}},
	}
	for _, c := range cases {
		r := runMain(t, "", cmds, c.args...)
		if r.code != 0 || r.stderr != "" || strings.HasPrefix(r.stdout, "{") {
			t.Errorf("%q: %d %q %q", c.args, r.code, r.stdout, r.stderr)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(r.stdout, w) {
				t.Errorf("%q: help lacks %q: %q", c.args, w, r.stdout)
			}
		}
	}
	// After --, --help is a subject like any other.
	r := runMain(t, "", cmds, "thing", "make", "--", "--help")
	if want := `{"ok":true,"result":{"title":"--help"},"warnings":[]}` + "\n"; r.stdout != want {
		t.Errorf("-- --help: %q", r.stdout)
	}
}

// Completion is cobra's: a script for each shell, and the hidden
// __complete command the script calls back, which completes commands and
// options.
func TestMainCompletion(t *testing.T) {
	cmds := []*Command{fake, bare}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		r := runMain(t, "", cmds, "completion", shell)
		if r.code != 0 || !strings.Contains(r.stdout, "koan") || strings.HasPrefix(r.stdout, "{") {
			t.Errorf("completion %s: %d %.80q", shell, r.code, r.stdout)
		}
	}
	r := runMain(t, "", cmds, "completion")
	if r.code != 0 || !strings.Contains(r.stdout, "bash") || strings.HasPrefix(r.stdout, "{") {
		t.Errorf("completion alone: %d %q", r.code, r.stdout)
	}
	// A shell cobra doesn't know is usage, not its help.
	r = runMain(t, "", cmds, "completion", "zhs")
	if r.code != ExitUsage || !strings.Contains(r.stdout, `"field":"zhs"`) {
		t.Errorf("completion zhs: %d %q", r.code, r.stdout)
	}
	r = runMain(t, "", cmds, "__complete", "thing", "")
	if r.code != 0 || !strings.Contains(r.stdout, "make") {
		t.Errorf("__complete thing: %d %q", r.code, r.stdout)
	}
	r = runMain(t, "", cmds, "__complete", "thing", "make", "t", "--pri")
	if r.code != 0 || !strings.Contains(r.stdout, "--priority") {
		t.Errorf("__complete --pri: %d %q", r.code, r.stdout)
	}
}

// HOME is found as part of the root's state, after the input's shape,
// and only by a command that uses the root: unset, empty or relative, it
// is io with no path.
func TestMainHome(t *testing.T) {
	rooted := &Command{Name: "rooted", Run: func(env *Env, in Input) (any, []Warning, *Error) {
		home, e := env.Home()
		if e != nil {
			return nil, nil, e
		}
		return map[string]string{"home": home}, nil, nil
	}}
	for _, home := range []string{"", "relative/dir"} {
		t.Setenv("HOME", home)
		var out, errw bytes.Buffer
		code := Main([]string{"rooted"}, strings.NewReader(""), &out, &errw, []*Command{rooted})
		if want := `{"ok":false,"error":{"kind":"io","rule":null,`; code != ExitFailure || !strings.HasPrefix(out.String(), want) || !strings.Contains(out.String(), `"paths":[]`) {
			t.Errorf("HOME %q: %d %s", home, code, out.String())
		}
		// A command that doesn't use the root never reads it.
		out.Reset()
		if code := Main([]string{"version"}, strings.NewReader(""), &out, &errw, []*Command{bare}); code != ExitOK {
			t.Errorf("HOME %q: version: %d %s", home, code, out.String())
		}
		// The input's shape comes first.
		out.Reset()
		if code := Main([]string{"rooted", "-i", "-"}, strings.NewReader("[1]"), &out, &errw, []*Command{rooted}); code != ExitFailure || !strings.Contains(out.String(), `"kind":"invalid-input"`) {
			t.Errorf("HOME %q: shape: %d %s", home, code, out.String())
		}
	}
	t.Setenv("HOME", "/some/home/")
	var out, errw bytes.Buffer
	Main([]string{"rooted"}, strings.NewReader(""), &out, &errw, []*Command{rooted})
	if !strings.Contains(out.String(), `"home":"/some/home"`) {
		t.Errorf("HOME set: %s", out.String())
	}
}

// --note-file reads its field whole from a file, or from standard input
// for -. A file it can't read is io with its absolute path.
func TestMainFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "n.md"), []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	r := runMain(t, "", []*Command{fake}, "thing", "make", "t", "--note-file", "n.md")
	want := `{"ok":true,"result":{"note":"line one\nline two\n","title":"t"},"warnings":[]}` + "\n"
	if r.stdout != want || r.code != 0 {
		t.Errorf("file: got %q %d", r.stdout, r.code)
	}
	r = runMain(t, "from stdin", []*Command{fake}, "thing", "make", "t", "--note-file", "-")
	want = `{"ok":true,"result":{"note":"from stdin","title":"t"},"warnings":[]}` + "\n"
	if r.stdout != want || r.code != 0 {
		t.Errorf("stdin: got %q %d", r.stdout, r.code)
	}
	r = runMain(t, "", []*Command{fake}, "thing", "make", "t", "--note-file", "missing.md")
	want = `"paths":["` + filepath.Join(dir, "missing.md") + `"]`
	if !strings.Contains(r.stdout, `"kind":"io"`) || !strings.Contains(r.stdout, want) || r.code != ExitFailure {
		t.Errorf("missing: got %q %d", r.stdout, r.code)
	}
}

// A Path option is made absolute from the working directory, as the
// command line becomes an input; Here gives a field the working
// directory when the command line gives neither it nor what stands in
// for it. Through --input, neither applies.
func TestMainPaths(t *testing.T) {
	cmd := &Command{
		Name:    "where",
		Subject: &Option{Field: "ref"},
		Options: []Option{{Field: "file", Kind: Path}},
		Here:    &Here{Field: "file", Unless: []string{"ref"}},
		Run:     func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
	}
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	q := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
	for _, c := range []struct {
		stdin string
		args  []string
		want  string
	}{
		{"", []string{"where", "--file", "a/../b"}, `{"file":"` + q(filepath.Join(wd, "b")) + `"}`},
		{"", []string{"where", "--file=/x//y/"}, `{"file":"/x/y"}`},
		{"", []string{"where", "--file", ""}, `{"file":""}`},
		{"", []string{"where"}, `{"file":"` + q(wd) + `"}`},
		{"", []string{"where", "@1"}, `{"ref":"@1"}`},
		{"", []string{"where", "@1", "--file", "x"}, `{"file":"` + q(filepath.Join(wd, "x")) + `","ref":"@1"}`},
		{`{"file":"rel"}`, []string{"where", "-i", "-"}, `{"file":"rel"}`},
		{`{}`, []string{"where", "-i", "-"}, `{}`},
	} {
		r := runMain(t, c.stdin, []*Command{cmd}, c.args...)
		want := `{"ok":true,"result":` + c.want + `,"warnings":[]}` + "\n"
		if r.stdout != want || r.code != 0 {
			t.Errorf("%q: got %s want %s", c.args, r.stdout, want)
		}
	}
}
