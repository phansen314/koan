package pick

import (
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
)

// The command keys: moving and marking, then each action's.
func TestCommandKeys(t *testing.T) {
	if got := strings.Join(commandKeys(), ","); !strings.HasPrefix(got, "j,k,g,G,space,q,i,/,?,c,e") {
		t.Errorf("got %s", got)
	}
}

func TestHeaderByMode(t *testing.T) {
	if got := header("", "renew", "", false, false, "/trips"); got != "/trips\n"+insertHint {
		t.Errorf("insert: %q", got)
	}
	if got := header(modeCommand, "renew", "", false, false, "/trips"); got != "[cmd] query: renew\n/trips\n"+commandHint {
		t.Errorf("command: %q", got)
	}
	if got := header(modeCommand, "", "", false, false, "/trips"); got != "[cmd]\n/trips\n"+commandHint {
		t.Errorf("command, no query: %q", got)
	}
	// The query is data: a control character in it can't split the header.
	if got := header(modeCommand, "a\nb", "", false, false, "/"); !strings.HasPrefix(got, "[cmd] query: a\\nb\n/\n") {
		t.Errorf("control character: %q", got)
	}
}

// Esc enters command mode, then quits; i and / come back to insert mode;
// each switch rewrites the header and binds or unbinds the command keys.
func TestModeVerbs(t *testing.T) {
	s, e := newSession(fsys.OS{}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	writeJSON(s, scopeFile, Scope{Folder: "/trips", Recursive: true})
	writeJSON(s, shownFile, shown{})
	env := Env{Sys: System{Executable: func() (string, error) { return "/bin/ft ask", nil }}}
	toHeader := "+transform-header('/bin/ft ask' __pick text 'header')"
	keys := strings.Join(commandKeys(), ",")
	toCommand := "hide-input+rebind(" + keys + ")" + toHeader
	toInsert := "show-input+unbind(" + keys + ")" + toHeader
	headerNow := func() string {
		b, _, _ := s.Read(textPrefix + "header")
		return string(b)
	}

	if out, e := escVerb(s, []string{"renew"}, env); string(out) != toCommand || e != nil {
		t.Fatalf("esc in insert mode: %q, %v", out, e)
	}
	if h := headerNow(); h != header(modeCommand, "renew", "", false, false, "/trips") {
		t.Errorf("command header: %q", h)
	}
	if out, e := insertVerb(s, nil, env); string(out) != toInsert || e != nil {
		t.Fatalf("insert: %q, %v", out, e)
	}
	if h := headerNow(); h != header("", "", "", false, false, "/trips") {
		t.Errorf("insert header: %q", h)
	}
	if out, e := commandVerb(s, []string{""}, env); string(out) != toCommand || e != nil {
		t.Fatalf("ctrl-space: %q, %v", out, e)
	}
	// Esc in command mode quits: an empty selection, then accept.
	if out, e := escVerb(s, []string{""}, env); string(out) != "accept" || e != nil {
		t.Fatalf("esc in command mode: %q, %v", out, e)
	}
	if b, ok, _ := s.Read(selectionFile); !ok || len(b) != 0 {
		t.Errorf("selection %q, %v", b, ok)
	}

	for name, run := range map[string]func() *errs.Error{
		"esc, no query":       func() *errs.Error { _, e := escVerb(s, nil, env); return e },
		"esc, two":            func() *errs.Error { _, e := escVerb(s, []string{"a", "b"}, env); return e },
		"command, no query":   func() *errs.Error { _, e := commandVerb(s, nil, env); return e },
		"insert, an argument": func() *errs.Error { _, e := insertVerb(s, []string{"x"}, env); return e },
		"help, an argument":   func() *errs.Error { _, e := helpVerb(s, []string{"x"}, env); return e },
	} {
		if e := run(); e == nil || e.Kind != errs.KindUsage {
			t.Errorf("%s: %v", name, e)
		}
	}
}

func TestHelpVerb(t *testing.T) {
	out, e := helpVerb(nil, nil, Env{})
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range help {
		if !strings.Contains(string(out), row[1]) {
			t.Errorf("no %q in\n%s", row[1], out)
		}
	}
}
