package pick

import (
	"fmt"
	"strings"

	"github.com/junegunn/go-shellwords"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
)

// OptsVar holds the person's options for pick's fzf, which come after
// pick's own and so win (pick-spec.md, fzf options).
const OptsVar = "FTASK_PICK_OPTS"

// undone are the options pick passes after FZF_DEFAULT_OPTS to undo any
// there that would end fzf without a callback, move it into a popup, or
// change which lines it reads or how --filter writes them.
var undone = []string{
	"--no-select-1", "--no-exit-0", "--no-expect", "--no-tmux",
	"--no-read0", "--no-header-lines", "--no-print0", "--no-print-query", "--accept-nth", "..",
}

// picker is what one fzf session starts with.
type picker struct {
	exe      string // this binary, for callbacks
	scope    Scope
	query    string
	missing  int // snapshot IDs the load lacks
	warnings int // the load's
	userOpts []string
}

// userOpts splits FTASK_PICK_OPTS as fzf splits FZF_DEFAULT_OPTS, with its
// own parser, comments included. One that doesn't split is reported as fzf
// reports a bad FZF_DEFAULT_OPTS: fzf-failed, here without a status, since
// fzf never ran.
func userOpts(environ []string) ([]string, *errs.Error) {
	p := shellwords.NewParser()
	p.ParseComment = true
	opts, err := p.Parse(lookupEnv(environ, OptsVar))
	if err != nil {
		return nil, unavailable(fmt.Sprintf("$%s: %v", OptsVar, err), UnavailableDetails{Reason: FzfFailed})
	}
	return opts, nil
}

// args are fzf's arguments, in order: pick's own (pick-spec.md, fzf
// contract), then FTASK_PICK_OPTS. FZF_DEFAULT_OPTS, which fzf reads
// itself, comes before them all.
func (pk picker) args() []string {
	a := append([]string{}, undone...)
	a = append(a,
		"--with-shell", "sh -c",
		"--multi",
		"--ansi",
		"--delimiter", lineDelimiter,
		"--with-nth", lineWithNth,
		"--nth", lineNth,
		"--tiebreak", "index",
		"--tabstop", "1",
		"--prompt", promptOf(pk.scope),
		"--query", pk.query,
		"--header", header("", "", "", false, false, pk.scopeLine()),
		"--preview", pk.helper("preview")+" {1}",
		"--bind", "enter:transform:"+pk.helper("enter")+" {q} {+1}",
		"--bind", "esc:transform:"+pk.helper("esc")+" {q}",
		"--bind", "ctrl-space:transform:"+pk.helper("command")+" {q}",
		"--bind", "ctrl-d:delete-char",
		"--bind", "ctrl-/:toggle-preview",
		// Command mode's keys, unbound at start: they type in insert mode.
		"--bind", "j:down",
		"--bind", "k:up",
		"--bind", "g:first",
		"--bind", "G:last",
		"--bind", "space:toggle+down",
		"--bind", "q:transform:"+pk.helper("quit"),
		"--bind", "i:transform:"+pk.helper("insert"),
		"--bind", "/:transform:"+pk.helper("insert"),
		"--bind", "?:preview:"+pk.helper("help"),
	)
	for _, ac := range actions {
		if pk.scope.Folders {
			break // the folder picker has no actions
		}
		bind := ac.key + ":transform:" + pk.helper("act", ac.key)
		if ac.arity != noTargets {
			bind += " {+1}"
		}
		a = append(a, "--bind", bind)
	}
	a = append(a,
		// load is bound from the start, so that a callback can rebind it
		// to move the cursor once a reload is in (pick-spec.md, Actions).
		"--bind", "start:unbind(load,"+strings.Join(commandKeys(), ",")+")",
		"--bind", "load:transform:"+pk.helper("on-load"),
	)
	if pk.warnings > 0 {
		a = append(a, "--footer", plural(pk.warnings, "warning"))
	}
	return append(a, pk.userOpts...)
}

// helper is the sh command line of a helper verb, every word quoted.
func (pk picker) helper(verb string, args ...string) string {
	return helperLine(pk.exe, verb, args...)
}

// helperLine is the sh command line that runs exe's helper verb with args,
// every word quoted.
func helperLine(exe, verb string, args ...string) string {
	words := []string{shQuote(exe), HelperCommand, verb}
	for _, a := range args {
		words = append(words, shQuote(a))
	}
	return strings.Join(words, " ")
}

// scopeLine is the header's line for the scope.
func (pk picker) scopeLine() string { return scopeLine(pk.scope, pk.missing) }

// scopeLine is the header's line for a scope: its folder and the filters in
// effect, and how many of a snapshot's IDs the load lacks.
func scopeLine(s Scope, missing int) string {
	parts := []string{string(s.Folder)}
	if !s.Recursive {
		parts[0] += " (not subfolders)"
	}
	if s.TagsAny != nil {
		parts = append(parts, "any of "+tags(s.TagsAny))
	}
	if s.TagsAll != nil {
		parts = append(parts, "all of "+tags(s.TagsAll))
	}
	switch {
	case s.Source != "":
		parts = append(parts, "live source")
	case s.IDs != nil:
		parts = append(parts, plural(len(s.IDs), "given ID"))
	}
	if missing > 0 {
		parts = append(parts, plural(missing, "given ID")+" not found")
	}
	return strings.Join(parts, " · ")
}

func tags(ts []model.Tag) string {
	s := make([]string, len(ts))
	for i, t := range ts {
		s[i] = "#" + string(t)
	}
	return strings.Join(s, " ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// shQuote quotes s as one sh word.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fzfEnv is fzf's environment: this process's, with the session for the
// helper.
func fzfEnv(environ []string, s *Session) []string {
	env := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, SessionVar+"=") {
			env = append(env, kv)
		}
	}
	return append(env, SessionVar+"="+s.Dir)
}

// show runs the picker on lines and returns fzf's exit status. The terminal
// is checked first, as the picker is about to be shown (pick-spec.md,
// Errors).
func show(env Env, fzf string, pk picker, lines []string, s *Session) (int, *errs.Error) {
	if err := env.Sys.OpenTTY(); err != nil {
		return 0, unavailable("no terminal: /dev/tty does not open; pick is for a person at a terminal", UnavailableDetails{Reason: NoTerminal})
	}
	var stdin []byte
	if len(lines) > 0 {
		stdin = []byte(strings.Join(lines, "\n") + "\n")
	}
	// In the picker, ctrl-c is a key. Outside it, while fzf is suspended
	// for an editor, it is SIGINT to the whole foreground process group,
	// pick included, which carries on.
	restore := env.Sys.CatchInterrupts()
	status, err := env.Sys.RunFzf(fzf, pk.args(), fzfEnv(env.Sys.Environ(), s), stdin)
	restore()
	if err != nil {
		return 0, unavailable(fmt.Sprintf("fzf failed: %v", err), UnavailableDetails{Reason: FzfFailed, Actions: []any{}})
	}
	return status, nil
}
