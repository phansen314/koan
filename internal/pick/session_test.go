package pick

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/ops"
)

func TestSessionBase(t *testing.T) {
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{[]string{"XDG_RUNTIME_DIR=/run/user/1", "TMPDIR=/t"}, "/run/user/1"},
		{[]string{"XDG_RUNTIME_DIR=", "TMPDIR=/t"}, "/t"},
		{[]string{"TMPDIR=/t", "TMPDIR=/u"}, "/u"},
		{[]string{"HOME=/h"}, "/tmp"},
		{nil, "/tmp"},
	} {
		if got := sessionBase(tc.env); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.env, got, tc.want)
		}
	}
}

// entries is what dir holds, by name.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names
}

func TestSession(t *testing.T) {
	base := t.TempDir()
	s, e := newSession(fsys.OS{}, base)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Dir(s.Dir) != base || !strings.HasPrefix(filepath.Base(s.Dir), sessionPrefix) {
		t.Errorf("session at %s, want %s/%s…", s.Dir, base, sessionPrefix)
	}
	fi, err := os.Stat(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %v, want 0700", fi.Mode().Perm())
	}

	if _, ok, e := s.Read("mode"); ok || e != nil {
		t.Errorf("a file never written: ok %v, %v", ok, e)
	}
	for _, v := range []string{"insert", "command"} {
		if e := s.Write("mode", []byte(v)); e != nil {
			t.Fatal(e)
		}
		if b, ok, e := s.Read("mode"); string(b) != v || !ok || e != nil {
			t.Errorf("read %q, %v, %v; want %q", b, ok, e, v)
		}
	}
	// Writes leave no temp file behind.
	if got := entries(t, s.Dir); !slices.Equal(got, []string{"mode", markerName}) {
		t.Errorf("session holds %q", got)
	}

	// The helper finds it through the environment.
	h, e := openSession(fsys.OS{}, []string{SessionVar + "=" + s.Dir})
	if e != nil {
		t.Fatal(e)
	}
	if b, _, _ := h.Read("mode"); string(b) != "command" {
		t.Errorf("helper read %q", b)
	}
	h.Close()

	s.Remove()
	if got := entries(t, base); len(got) != 0 {
		t.Errorf("after Remove, base holds %q", got)
	}
}

// Two sessions in one base never share a directory.
// A relative base, e.g. from TMPDIR=tmp, is made absolute: the helper
// accepts only an absolute session directory.
func TestSessionRelativeBase(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	s, e := newSession(fsys.OS{}, "tmp")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	if !filepath.IsAbs(s.Dir) {
		t.Fatalf("session at %s", s.Dir)
	}
	o, e := openSession(fsys.OS{}, []string{SessionVar + "=" + s.Dir})
	if e != nil {
		t.Fatal(e)
	}
	o.root.Close()
}

func TestSessionsApart(t *testing.T) {
	base := t.TempDir()
	a, e := newSession(fsys.OS{}, base)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Remove()
	b, e := newSession(fsys.OS{}, base)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Remove()
	if a.Dir == b.Dir {
		t.Errorf("both at %s", a.Dir)
	}
}

func TestNewSessionFails(t *testing.T) {
	base := t.TempDir()
	if _, e := newSession(fsys.OS{}, filepath.Join(base, "missing")); e == nil || e.Kind != errs.KindIO {
		t.Errorf("missing base: %v, want io", e)
	}
	// The marker can't be written: nothing is left behind.
	f := fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpRename, "", 1, syscall.ENOSPC)}
	if _, e := newSession(f, base); e == nil || e.Kind != errs.KindIO {
		t.Errorf("failed marker: %v, want io", e)
	}
	if got := entries(t, base); len(got) != 0 {
		t.Errorf("base holds %q", got)
	}
}

func TestSessionWriteFails(t *testing.T) {
	base := t.TempDir()
	var hook fsys.Hook
	f := fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		if hook != nil {
			return hook(op)
		}
		return nil
	}}
	s, e := newSession(f, base)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	if e := s.Write("mode", []byte("insert")); e != nil {
		t.Fatal(e)
	}
	hook = fsys.ErrnoAt(fsys.OpRename, "", 1, syscall.ENOSPC)
	if e := s.Write("mode", []byte("command")); e == nil || e.Kind != errs.KindIO {
		t.Fatalf("got %v, want io", e)
	}
	// The old contents stay, and the temp file goes.
	if b, _, _ := s.Read("mode"); string(b) != "insert" {
		t.Errorf("read %q, want the old contents", b)
	}
	if got := entries(t, s.Dir); !slices.Equal(got, []string{"mode", markerName}) {
		t.Errorf("session holds %q", got)
	}
}

func TestOpenSessionRefuses(t *testing.T) {
	dir := t.TempDir()
	notSession := filepath.Join(dir, "plain")
	wrongMarker := filepath.Join(dir, "wrong")
	file := filepath.Join(dir, "file")
	for _, d := range []string{notSession, wrongMarker} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wrongMarker, markerName), []byte("something else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(markerText), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, env := range [][]string{
		nil,
		{SessionVar + "="},
		{SessionVar + "=relative/dir"},
		{SessionVar + "=" + filepath.Join(dir, "missing")},
		{SessionVar + "=" + file},
		{SessionVar + "=" + notSession},
		{SessionVar + "=" + wrongMarker},
	} {
		if _, e := openSession(fsys.OS{}, env); e == nil || e.Kind != errs.KindUsage {
			t.Errorf("%q: %v, want usage", env, e)
		}
	}
}

func TestHelper(t *testing.T) {
	s, e := newSession(fsys.OS{}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	var got []string
	verbs["echo"] = func(hs *Session, args []string, _ Env) ([]byte, *errs.Error) {
		if hs.Dir != s.Dir {
			t.Errorf("verb ran in %s, want %s", hs.Dir, s.Dir)
		}
		got = args
		return []byte("accept"), nil
	}
	defer delete(verbs, "echo")
	env := func(environ ...string) Env {
		return Env{Ops: ops.Env{}, Sys: System{Environ: func() []string { return environ }}}
	}
	env0 := env(SessionVar + "=" + s.Dir)
	env0.Ops.FS = fsys.OS{}

	out, _, e := Helper([]string{"echo", "a", "b c"}, env0)
	if e != nil || string(out) != "accept" || !slices.Equal(got, []string{"a", "b c"}) {
		t.Errorf("got %q, %v, args %q", out, e, got)
	}

	for _, tc := range []struct {
		name string
		args []string
		env  Env
		arg  *string
	}{
		{"no session", []string{"echo"}, func() Env { e := env(); e.Ops.FS = fsys.OS{}; return e }(), nil},
		{"missing verb", nil, env0, nil},
		{"unknown verb", []string{"nope", "x"}, env0, ptr("nope")},
	} {
		_, reported, e := Helper(tc.args, tc.env)
		if reported {
			t.Errorf("%s: reported, want an envelope", tc.name)
		}
		if e == nil || e.Kind != errs.KindUsage {
			t.Errorf("%s: %v, want usage", tc.name, e)
			continue
		}
		p := e.Details.(errs.UsageDetails).Problems[0]
		if (p.Argument == nil) != (tc.arg == nil) || (p.Argument != nil && *p.Argument != *tc.arg) {
			t.Errorf("%s: problem %+v", tc.name, p)
		}
	}
}

// A verb's failure is reported where fzf sends its output, never as an
// envelope: an action verb's in the status line, a list verb's as no
// lines, a shown text's as one line.
func TestHelperFailed(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one"})
	tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		env := Env{Ops: tr.env, Sys: System{
			Environ:    func() []string { return []string{SessionVar + "=" + tr.session} },
			Executable: func() (string, error) { return "/bin/ftask", nil },
		}}
		run := func(args ...string) (string, bool, *errs.Error) {
			out, reported, e := Helper(args, env)
			return string(out), reported, e
		}
		// Broken: what every verb below reads first.
		for _, name := range []string{shownFile, linesFile} {
			if err := os.Remove(filepath.Join(tr.session, name)); err != nil {
				t.Fatal(err)
			}
		}
		out, reported, e := run("act", "c", "1@/")
		if e == nil || !reported || out != "transform-footer('/bin/ftask' __pick text 'footer')" {
			t.Errorf("act: %q, %v, %v", out, reported, e)
		}
		if f, _, _ := run("text", "footer"); !strings.HasPrefix(f, "✗ internal: session has no "+shownFile) {
			t.Errorf("footer %q", f)
		}
		if os.Geteuid() != 0 { // root writes to a read-only directory
			os.Remove(filepath.Join(tr.session, statusFile))
			os.Chmod(tr.session, 0o500) // nowhere to write the status line
			out, reported, e = run("act", "c", "1@/")
			os.Chmod(tr.session, 0o700)
			if e == nil || !reported || out != "" {
				t.Errorf("act, unwritable: %q, %v, %v", out, reported, e)
			}
		}
		// After an action's status, as a reload's chain has it.
		os.WriteFile(filepath.Join(tr.session, statusFile), []byte("✓ completed 1"), 0o600)
		if out, reported, e = run("lines", "x"); e == nil || !reported || out != "" {
			t.Errorf("lines: %q, %v, %v", out, reported, e)
		}
		if out, reported, e = run("text", "nope"); e == nil || !reported || out != "✗ usage: unknown text\n" {
			t.Errorf("text: %q, %v, %v", out, reported, e)
		}
		// The lines' failure is said in the status line, which the
		// reload's chain shows after the list.
		if f, _, _ := run("text", "footer"); f != "✓ completed 1 · ✗ usage: unexpected argument" {
			t.Errorf("after lines: footer %q", f)
		}
		// The query's failure prints nothing: it would become the value a
		// prompt applies.
		query := filepath.Join(tr.session, textPrefix+"query")
		os.Remove(query)
		if err := os.Mkdir(query, 0o700); err != nil {
			t.Fatal(err)
		}
		if out, reported, e = run("text", "query"); e == nil || !reported || out != "" {
			t.Errorf("text query: %q, %v, %v", out, reported, e)
		}
		os.Remove(query)
		helper("quit")
	}})
}

func ptr[T any](v T) *T { return &v }

// An action that fails while opening a prompt leaves the session in command
// mode, where fzf still is: the next Enter emits, never applies the prompt.
func TestActFailedBackToCommand(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one"})
	tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		env := Env{Ops: tr.env, Sys: System{
			Environ:    func() []string { return []string{SessionVar + "=" + tr.session} },
			Executable: func() (string, error) { return "", errors.New("gone") },
		}}
		if _, reported, e := Helper([]string{"act", "t", "1@/"}, env); e == nil || !reported {
			t.Fatalf("act: %v, %v", reported, e)
		}
		if b, _ := os.ReadFile(filepath.Join(tr.session, modeFile)); string(b) != modeCommand {
			t.Errorf("mode %q", b)
		}
		if _, err := os.Stat(filepath.Join(tr.session, promptFile)); err == nil {
			t.Error("prompt left open")
		}
		if got := helper("enter", "renew", "1@/"); got != "accept" {
			t.Errorf("enter printed %q", got)
		}
	}})
}
