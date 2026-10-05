package pick

import (
	"encoding/json"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/ops"
)

func TestPickerArgs(t *testing.T) {
	pk := picker{
		exe:   "/opt/my koan/koan",
		scope: Scope{Folder: "/", Recursive: true, Readiness: ops.PickOpen},
		query: "renew pass",
	}
	want := []string{
		"--no-select-1", "--no-exit-0", "--no-expect", "--no-tmux",
		"--no-read0", "--no-header-lines", "--no-print0", "--no-print-query", "--accept-nth", "..",
		"--with-shell", "sh -c",
		"--multi", "--ansi",
		"--delimiter", "\t", "--with-nth", "2..", "--nth", "2,3",
		"--tiebreak", "index", "--tabstop", "1",
		"--prompt", "open> ",
		"--query", "renew pass",
		"--header", "/\nenter: pick · tab: mark · esc: commands",
		"--preview", "'/opt/my koan/koan' __pick preview {1}",
		"--bind", "enter:transform:'/opt/my koan/koan' __pick enter {q} {+1}",
		"--bind", "esc:transform:'/opt/my koan/koan' __pick esc {q}",
		"--bind", "ctrl-space:transform:'/opt/my koan/koan' __pick command {q}",
		"--bind", "ctrl-d:delete-char",
		"--bind", "ctrl-/:toggle-preview",
		"--bind", "j:down",
		"--bind", "k:up",
		"--bind", "g:first",
		"--bind", "G:last",
		"--bind", "space:toggle+down",
		"--bind", "q:transform:'/opt/my koan/koan' __pick quit",
		"--bind", "i:transform:'/opt/my koan/koan' __pick insert",
		"--bind", "/:transform:'/opt/my koan/koan' __pick insert",
		"--bind", "?:preview:'/opt/my koan/koan' __pick help",
		"--bind", "c:transform:'/opt/my koan/koan' __pick act 'c' {+1}",
		"--bind", "e:transform:'/opt/my koan/koan' __pick act 'e' {+1}",
		"--bind", "n:transform:'/opt/my koan/koan' __pick act 'n'",
		"--bind", "p:transform:'/opt/my koan/koan' __pick act 'p' {+1}",
		"--bind", "t:transform:'/opt/my koan/koan' __pick act 't' {+1}",
		"--bind", "x:transform:'/opt/my koan/koan' __pick act 'x' {+1}",
		"--bind", "b:transform:'/opt/my koan/koan' __pick act 'b' {+1}",
		"--bind", "u:transform:'/opt/my koan/koan' __pick act 'u' {+1}",
		"--bind", "m:transform:'/opt/my koan/koan' __pick act 'm' {+1}",
		"--bind", "f:transform:'/opt/my koan/koan' __pick act 'f'",
		"--bind", "s:transform:'/opt/my koan/koan' __pick act 's'",
		"--bind", "r:transform:'/opt/my koan/koan' __pick act 'r'",
		"--bind", "start:unbind(load,j,k,g,G,space,q,i,/,?,c,e,n,p,t,x,b,u,m,f,s,r)",
		"--bind", "load:transform:'/opt/my koan/koan' __pick on-load",
	}
	if got := pk.args(); !slices.Equal(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}

	// The load's warnings are in the footer; the person's options come last.
	pk.warnings, pk.userOpts = 2, []string{"--layout", "reverse", "--prompt", "mine> "}
	got := pk.args()
	if tail := got[len(got)-6:]; !slices.Equal(tail, []string{"--footer", "2 warnings", "--layout", "reverse", "--prompt", "mine> "}) {
		t.Errorf("tail %q", tail)
	}
	pk.warnings = 1
	if got := pk.args(); !slices.Contains(got, "1 warning") {
		t.Errorf("one warning: %q", got)
	}
}

func TestScopeLine(t *testing.T) {
	for _, tc := range []struct {
		pk   picker
		want string
	}{
		{picker{scope: Scope{Folder: "/work", Recursive: true}}, "/work"},
		{picker{scope: Scope{Folder: "/work", Recursive: false}}, "/work (not subfolders)"},
		{picker{scope: Scope{Folder: "/", Recursive: true, TagsAny: []model.Tag{"a", "b"}, TagsAll: []model.Tag{"c"}}}, "/ · any of #a #b · all of #c"},
		{picker{scope: Scope{Folder: "/", Recursive: true, IDs: []model.ID{1, 2, 3}}, missing: 2}, "/ · 3 given IDs · 2 given IDs not found"},
		{picker{scope: Scope{Folder: "/", Recursive: true, IDs: []model.ID{}}}, "/ · 0 given IDs"},
	} {
		if got := tc.pk.scopeLine(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestUserOpts(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want []string
	}{
		{"", nil},
		{"--height 60% --layout reverse", []string{"--height", "60%", "--layout", "reverse"}},
		{"--prompt 'a b> ' # a comment\n--exact", []string{"--prompt", "a b> ", "--exact"}},
	} {
		got, e := userOpts([]string{OptsVar + "=" + tc.env})
		if e != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %q, %v", tc.env, got, e)
		}
	}
	_, e := userOpts([]string{OptsVar + "=--prompt 'x"})
	if e == nil || e.Kind != errs.KindUnavailable {
		t.Fatalf("unbalanced: %v", e)
	}
	b, _ := json.Marshal(e.Details)
	if string(b) != `{"reason":"fzf-failed"}` || !strings.Contains(e.Message, OptsVar) {
		t.Errorf("unbalanced: %s: %s", e.Message, b)
	}
}

// KOAN_PICK_OPTS that doesn't split is found with fzf, before anything
// is read from the tree (pick-spec.md, Errors): not a scope folder's
// not-found.
func TestRunBadOptsFirst(t *testing.T) {
	tr := newTestTree(t)
	sys := System{
		LookPath: func(string) (string, error) { return "/bin/fzf", nil },
		Output:   func(string, []string, []string) ([]byte, []byte, int, error) { return []byte("0.63.0\n"), nil, 0, nil },
		Environ:  func() []string { return []string{OptsVar + "=--prompt 'x"} },
	}
	in := &jsonio.Object{}
	in.Set("folder", "/nope")
	out := Run(in, nil, Env{Ops: tr.env, Sys: sys})
	if out.OK || out.Error.Kind != errs.KindUnavailable || out.Error.Details.(UnavailableDetails).Reason != FzfFailed {
		t.Errorf("%+v", out.Error)
	}
}

// Every word reaches sh as it was.
func TestShQuote(t *testing.T) {
	words := []string{"plain", "with space", "it's", `"double"`, "$HOME", "a\nb", "", `\`, "')+execute-silent(touch X)+('"}
	var quoted []string
	for _, w := range words {
		quoted = append(quoted, shQuote(w))
	}
	out, err := exec.Command("sh", "-c", `for w in `+strings.Join(quoted, " ")+`; do printf '%s\0' "$w"; done`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"); !slices.Equal(got, words) {
		t.Errorf("got %q", got)
	}
}

func TestFzfEnv(t *testing.T) {
	s := &Session{Dir: "/run/koan-pick-x"}
	got := fzfEnv([]string{"A=1", SessionVar + "=/stale", "FZF_DEFAULT_OPTS=--exact"}, s)
	if want := []string{"A=1", "FZF_DEFAULT_OPTS=--exact", SessionVar + "=/run/koan-pick-x"}; !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}

// fakeRun is a System whose terminal and fzf are fakes.
type fakeRun struct {
	ttyErr error
	status int
	err    error
	ran    bool
	// caught is set while fzf runs; restored, once restored after it ran.
	caught, restored bool
	path             string
	args, env        []string
	stdin            []byte
}

func (f *fakeRun) system() System {
	return System{
		Environ: func() []string { return []string{"HOME=/h"} },
		OpenTTY: func() error { return f.ttyErr },
		CatchInterrupts: func() func() {
			f.caught = !f.ran
			return func() { f.restored = f.ran }
		},
		RunFzf: func(path string, args, env []string, stdin []byte) (int, error) {
			f.ran, f.path, f.args, f.env, f.stdin = true, path, args, env, stdin
			return f.status, f.err
		},
	}
}

func TestShow(t *testing.T) {
	s, e := newSession(fsys.OS{}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	pk := picker{exe: "/bin/koan", scope: Scope{Folder: "/", Recursive: true, Readiness: ops.PickOpen}}
	details := func(e *errs.Error) string {
		b, _ := json.Marshal(e.Details)
		return string(b)
	}

	f := &fakeRun{status: 130}
	status, e := show(Env{Sys: f.system()}, "/bin/fzf", pk, []string{"a\tx", "b\ty"}, s)
	if status != 130 || e != nil {
		t.Errorf("130: %d, %v", status, e)
	}
	if !f.caught || !f.restored {
		t.Errorf("interrupts caught %v before fzf, restored %v after", f.caught, f.restored)
	}
	if f.path != "/bin/fzf" || !slices.Equal(f.args, pk.args()) || string(f.stdin) != "a\tx\nb\ty\n" ||
		!slices.Equal(f.env, []string{"HOME=/h", SessionVar + "=" + s.Dir}) {
		t.Errorf("ran %q %q env %q stdin %q", f.path, f.args, f.env, f.stdin)
	}

	f = &fakeRun{}
	show(Env{Sys: f.system()}, "/bin/fzf", pk, nil, s)
	if f.stdin != nil {
		t.Errorf("no lines: stdin %q", f.stdin)
	}

	f = &fakeRun{ttyErr: errors.New("ENXIO")}
	_, e = show(Env{Sys: f.system()}, "/bin/fzf", pk, nil, s)
	if e == nil || e.Kind != errs.KindUnavailable || details(e) != `{"reason":"no-terminal"}` || f.ran {
		t.Errorf("no terminal: %v %s, ran %v", e, details(e), f.ran)
	}

	f = &fakeRun{err: errors.New("signal: killed")}
	_, e = show(Env{Sys: f.system()}, "/bin/fzf", pk, nil, s)
	if e == nil || details(e) != `{"reason":"fzf-failed","actions":[]}` {
		t.Errorf("killed: %v %s", e, details(e))
	}
}

func TestOnLoad(t *testing.T) {
	s, e := newSession(fsys.OS{}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	if out, e := onLoad(s, nil, Env{}); string(out) != "unbind(load)" || e != nil {
		t.Errorf("unarmed: %q, %v", out, e)
	}
	s.Write(cursorFile, []byte("3"))
	if out, e := onLoad(s, nil, Env{}); string(out) != "pos(3)+unbind(load)" || e != nil {
		t.Errorf("armed: %q, %v", out, e)
	}
	// Disarmed by running.
	if out, _ := onLoad(s, nil, Env{}); string(out) != "unbind(load)" {
		t.Errorf("after: %q", out)
	}
	// Armed to hide the input, it hides it only in command mode: a key
	// handled before the load may have left it.
	s.Write(hideFile, nil)
	s.Write(modeFile, []byte(modeCommand))
	if out, _ := onLoad(s, nil, Env{}); string(out) != "hide-input+unbind(load)" {
		t.Errorf("hide: %q", out)
	}
	for _, mode := range []string{"", modePrompt, modeChoose} {
		s.Write(hideFile, nil)
		if mode == "" {
			s.Delete(modeFile)
		} else {
			s.Write(modeFile, []byte(mode))
		}
		if out, _ := onLoad(s, nil, Env{}); string(out) != "unbind(load)" {
			t.Errorf("hide, left for %q mode: %q", mode, out)
		}
		if _, ok, _ := s.Read(hideFile); ok {
			t.Errorf("hide, left for %q mode: still armed", mode)
		}
	}
	s.Write(cursorFile, []byte("0"))
	if _, e := onLoad(s, nil, Env{}); e == nil || e.Kind != errs.KindInternal {
		t.Errorf("bad cursor: %v", e)
	}
	if _, e := onLoad(s, []string{"x"}, Env{}); e == nil || e.Kind != errs.KindUsage {
		t.Errorf("argument: %v", e)
	}
}

func TestText(t *testing.T) {
	s, e := newSession(fsys.OS{}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	env := Env{Sys: System{Environ: func() []string { return nil }}}
	if out, e := text(s, []string{"footer"}, env); string(out) != "" || e != nil {
		t.Errorf("never written: %q, %v", out, e)
	}
	s.Write(textPrefix+"footer", []byte("✗ x: )+execute-silent(touch X)+(\nnext\x1b[31m\u2028"))
	if out, e := text(s, []string{"footer"}, env); string(out) != "✗ x: )+execute-silent(touch X)+(\\nnext\\x1b[31m\\u2028" || e != nil {
		t.Errorf("got %q, %v", out, e)
	}
	// The header keeps its lines, each escaped.
	s.Write(textPrefix+"header", []byte("[cmd] query: a\x1bb\n/\nkeys"))
	if out, _ := text(s, []string{"header"}, env); string(out) != "[cmd] query: a\\x1bb\n/\nkeys" {
		t.Errorf("header: %q", out)
	}
	for _, args := range [][]string{nil, {"other"}, {"footer", "x"}} {
		if _, e := text(s, args, env); e == nil || e.Kind != errs.KindUsage {
			t.Errorf("%q: %v", args, e)
		}
	}
}
