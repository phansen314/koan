package pick

import (
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/errs"
)

// HelperCommand is the hidden command fzf's callbacks run: koan __pick
// <verb> …. It is internal: not in help, not part of the contract, and it
// may change in any release (pick-spec.md, Session).
const HelperCommand = "__pick"

// verb is one of the helper's verbs. It runs in s with args, the words
// after the verb, and returns what to print: fzf actions, or text that fzf
// shows as it is.
type verb func(s *Session, args []string, env Env) ([]byte, *errs.Error)

// verbs are the helper's verbs, by name.
var verbs = map[string]verb{
	"enter":      enter,
	"act":        act,
	"edit":       editVerb,
	"after-edit": afterEdit,
	"after-x":    afterX,
	"lines":      linesVerb,
	"choices":    choicesVerb,
	"esc":        escVerb,
	"command":    commandVerb,
	"insert":     insertVerb,
	"help":       helpVerb,
	"on-load":    onLoad,
	"quit":       quit,
	"preview":    previewVerb,
	"text":       text,
}

// Helper runs koan __pick with args, the words after it, and returns what
// to print. With no valid session, or no verb it knows, it fails as any
// command does, with an envelope: usage. A verb's failure is reported
// instead, and out is what to print for it (see failed): fzf reads a
// verb's output as actions or as a list, never as an envelope. e is
// returned then too, for the exit status, with reported true.
func Helper(args []string, env Env) (out []byte, reported bool, e *errs.Error) {
	s, e := openSession(env.Ops.FS, env.Sys.Environ())
	if e != nil {
		return nil, false, e
	}
	defer s.Close()
	if len(args) == 0 {
		return nil, false, errs.Usage([]errs.UsageProblem{{Reason: "missing verb"}})
	}
	v, ok := verbs[args[0]]
	if !ok {
		return nil, false, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unknown verb"}})
	}
	if out, e = v(s, args[1:], env); e != nil {
		return failed(s, args, e, env), true, e
	}
	return out, false, nil
}

// actionVerbs are the verbs whose output fzf runs as actions: transform's,
// and execute's chained transform.
var actionVerbs = []string{"enter", "act", "esc", "command", "insert", "quit", "after-edit", "after-x", "on-load"}

// listVerbs are the verbs whose output is a reload-sync's list.
var listVerbs = []string{"lines", "choices"}

// failed is what a verb that failed with e prints, by where fzf sends its
// output, so that no envelope, which may hold data, is ever parsed as
// actions or listed: for an action verb, the status line, showing e; for
// a list verb, nothing, which leaves the list empty; for the rest, whose
// output fzf shows as it is (the preview, a text, the editor's terminal),
// e as one line, but for the query, which would become the value a prompt
// applies: that prints nothing, which leaves the query as it is.
func failed(s *Session, args []string, e *errs.Error, env Env) []byte {
	verb := args[0]
	msg := "✗ " + errText(e)
	var sh shown
	readJSON(s, shownFile, &sh) // no load to count warnings from: none
	switch {
	case slices.Contains(actionVerbs, verb):
		if out, e := setStatus(s, env, sh.Warnings, msg); e == nil {
			return out
		}
		return []byte{} // nothing to show it with: do nothing
	case slices.Contains(listVerbs, verb):
		// The empty list says nothing: the status line, which the reload's
		// chain shows after the list, says why.
		status, _, _ := s.Read(statusFile)
		setStatus(s, env, sh.Warnings, joinStatus(string(status), msg))
		return []byte{}
	case verb == "text" && len(args) > 1 && args[1] == "query":
		return []byte{}
	}
	return []byte(errs.OneLine(msg) + "\n")
}

// Session files the verbs share.
const (
	// cursorFile holds the line a callback wants the cursor on once its
	// reload is in: on-load's arming (pick-spec.md, Actions).
	cursorFile = "cursor"
	// hideFile, when present, has on-load hide the input: leaving a
	// prompt or choose list for command mode (see leaveMode).
	hideFile = "hide-input"
	// textPrefix begins the files that hold text fzf shows, by name:
	// text-footer, text-header, text-prompt, text-query.
	textPrefix = "text-"
)

// textNames are the texts the text verb shows.
var textNames = []string{"footer", "header", "prompt", "query"}

// onLoad runs on fzf's load event, when it is bound: it hides the input,
// if armed and still in command mode, and moves the cursor to the line
// recorded, as armed, and unbinds load again. fzf may run it once, unarmed, for the first list, before start's
// unbind takes effect.
func onLoad(s *Session, args []string, _ Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	var out string
	if _, ok, e := s.Read(hideFile); e != nil {
		return nil, e
	} else if ok {
		if e := s.Delete(hideFile); e != nil {
			return nil, e
		}
		// A key handled while the reload was in flight may have left
		// command mode already: i, or an action's prompt or choose list,
		// which show the input.
		mode, _, e := s.Read(modeFile)
		if e != nil {
			return nil, e
		}
		if string(mode) == modeCommand {
			out = "hide-input+"
		}
	}
	b, ok, e := s.Read(cursorFile)
	switch {
	case e != nil:
		return nil, e
	case !ok:
		return []byte(out + "unbind(load)"), nil
	}
	if e := s.Delete(cursorFile); e != nil {
		return nil, e
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 1 {
		return nil, errs.Internal("session cursor file holds " + strconv.Quote(string(b)))
	}
	return []byte(out + "pos(" + strconv.Itoa(n) + ")+unbind(load)"), nil
}

// text prints one of the session's texts, which fzf shows as it is
// (pick-spec.md, No data in action text): one line, its control characters
// escaped as on the CLI's stderr line, except the header, whose lines the
// helper composes, each escaped so. A text never written is empty.
func text(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	switch {
	case len(args) == 0:
		return nil, errs.Usage([]errs.UsageProblem{{Reason: "missing text name"}})
	case !slices.Contains(textNames, args[0]):
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unknown text"}})
	case len(args) > 1:
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[1], Reason: "unexpected argument"}})
	}
	b, _, e := s.Read(textPrefix + args[0])
	if e != nil {
		return nil, e
	}
	if args[0] == "footer" {
		// The status line, cut to the terminal less the list's margin.
		cols, _ := strconv.Atoi(lookupEnv(env.Sys.Environ(), "FZF_COLUMNS"))
		return []byte(fitWidth(errs.OneLine(string(b)), cols-2)), nil
	}
	if args[0] == "header" {
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			lines[i] = errs.OneLine(l)
		}
		return []byte(strings.Join(lines, "\n")), nil
	}
	return []byte(errs.OneLine(string(b))), nil
}
