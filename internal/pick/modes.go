package pick

import (
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
)

// The picker's modes (pick-spec.md, Modes). It starts in insert mode, where
// typing edits the query. Command mode hides the input line, the only way
// fzf drops typed keys, so that letters run actions and keys with no
// binding do nothing. Keys bound to edit the query, such as backspace,
// do nothing either while it is hidden; the query is kept, and still
// filters the list.
//
// fzf can't bind a key differently by mode, only enable and disable a
// binding. So the command keys are bound from the start, to what they do
// in command mode, and unbound in insert mode, where they type. Esc, whose
// meaning depends on the mode, asks the helper, which keeps the mode in
// the session.

const (
	// modeFile holds the mode while it is command; there is none in
	// insert mode.
	modeFile    = "mode"
	modeCommand = "command"
	// queryFile holds the query while in command mode, which hides it, for
	// the header.
	queryFile = "query"
)

// moveKeys are the keys bound only in command mode, other than actions'.
var moveKeys = []string{"j", "k", "g", "G", "space", "q", "i", "/", "?"}

// commandKeys are the keys bound only in command mode: moveKeys, then each
// action's.
func commandKeys() []string {
	keys := slices.Clone(moveKeys)
	for _, a := range actions {
		keys = append(keys, a.key)
	}
	return keys
}

// Key hints, the header's last line in each mode: short enough for the
// list beside the preview in a terminal 100 wide. ? shows every key.
const (
	insertHint  = "enter: pick · tab: mark · esc: commands"
	commandHint = "c: complete · i: search · ?: keys · q: quit"
	// folderHint is command mode's in the folder picker, which has no
	// actions.
	folderHint  = "i: search · ?: keys · q: quit"
	promptHint  = "enter: apply · esc: cancel"
	chooseHint  = "enter: choose · tab: mark · esc: cancel"
	pickOneHint = "enter: choose · esc: cancel"
)

// help is command mode's keys, which ? shows in the preview until the
// cursor moves.
var help = [][2]string{
	{"enter", "pick the marked tasks, or the one under the cursor"},
	{"tab space", "mark or unmark"},
	{"c", "complete; or reopen, when every one is complete"},
	{"e", "edit notes, in $VISUAL, else $EDITOR, else vi"},
	{"n", "new task, titled in a prompt that starts with the query"},
	{"p", "priority: a number, or null for none"},
	{"t", "tags: a b to set, +a -b to change, - for none"},
	{"x", "edit title, priority, tags and extra as JSON"},
	{"b", "block: choose blockers (tab marks several)"},
	{"u", "unblock: choose blockers to remove"},
	{"m", "move to a folder"},
	{"f", "scope folder: show another folder's tasks"},
	{"s", "scope: ready, open, all"},
	{"r", "reload"},
	{"j k", "down, up"},
	{"g G", "first, last line"},
	{"i /", "search: back to insert mode, with the query"},
	{"?", "this help, until the cursor moves"},
	{"ctrl-/", "show or hide the preview"},
	{"esc q", "quit: pick nothing"},
	{"ctrl-c", "cancel"},
}

// header is the header in a mode: in command mode, the mode and the
// query, which is hidden; in prompt mode, what the prompt asks for; then
// the scope line; then the mode's keys.
func header(mode, query, label string, single, folders bool, scopeLine string) string {
	switch mode {
	case modeCommand:
		line := "[cmd]"
		if query != "" {
			line += " query: " + errs.OneLine(query)
		}
		hint := commandHint
		if folders {
			hint = folderHint
		}
		return line + "\n" + scopeLine + "\n" + hint
	case modePrompt:
		return "[" + strings.TrimSuffix(label, "> ") + "]\n" + scopeLine + "\n" + promptHint
	case modeChoose:
		hint := chooseHint
		if single {
			hint = pickOneHint
		}
		return "[" + strings.TrimSuffix(label, "> ") + "]\n" + scopeLine + "\n" + hint
	}
	return scopeLine + "\n" + insertHint
}

// escVerb is Esc: command mode from insert mode, and quit from command
// mode. Its argument is the query, for the header.
func escVerb(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if e := oneArg(args); e != nil {
		return nil, e
	}
	b, _, e := s.Read(modeFile)
	if e != nil {
		return nil, e
	}
	switch string(b) {
	case modeCommand:
		return quit(s, nil, env)
	case modePrompt:
		return cancelPrompt(s, env)
	case modeChoose:
		return cancelChoose(s, env)
	}
	return commandVerb(s, args, env)
}

// commandVerb enters command mode. Its argument is the query, for the
// header.
func commandVerb(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if e := oneArg(args); e != nil {
		return nil, e
	}
	// In a prompt or choose list, ctrl-space cancels it, as Esc does.
	if b, _, e := s.Read(modeFile); e != nil {
		return nil, e
	} else if string(b) == modePrompt {
		return cancelPrompt(s, env)
	} else if string(b) == modeChoose {
		return cancelChoose(s, env)
	}
	if e := s.Write(modeFile, []byte(modeCommand)); e != nil {
		return nil, e
	}
	if e := s.Write(queryFile, []byte(args[0])); e != nil {
		return nil, e
	}
	return switchMode(s, true, env)
}

// insertVerb enters insert mode.
func insertVerb(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	if e := s.Delete(modeFile); e != nil {
		return nil, e
	}
	return switchMode(s, false, env)
}

// switchMode returns the actions that switch fzf to the mode the session
// is now in.
func switchMode(s *Session, command bool, env Env) ([]byte, *errs.Error) {
	header, e := writeHeader(s, env)
	if e != nil {
		return nil, e
	}
	keys := "+unbind("
	input := "show-input"
	if command {
		keys, input = "+rebind(", "hide-input"
	}
	return []byte(input + keys + strings.Join(commandKeys(), ",") + ")+" + header), nil
}

// writeHeader writes the header for the session's mode, scope and last
// load, and returns the action that shows it.
func writeHeader(s *Session, env Env) (string, *errs.Error) {
	mode, _, e := s.Read(modeFile)
	if e != nil {
		return "", e
	}
	return writeHeaderAs(s, env, string(mode))
}

// writeHeaderAs is writeHeader for mode, which the session may not be in
// yet.
func writeHeaderAs(s *Session, env Env, mode string) (string, *errs.Error) {
	var scope Scope
	var sh shown
	if e := readJSON(s, scopeFile, &scope); e != nil {
		return "", e
	}
	if e := readJSON(s, shownFile, &sh); e != nil {
		return "", e
	}
	query, _, e := s.Read(queryFile)
	if e != nil {
		return "", e
	}
	var label string
	var single bool
	switch mode {
	case modePrompt:
		if label, e = promptLabel(s); e != nil {
			return "", e
		}
	case modeChoose:
		if label, single, e = chooseLabel(s); e != nil {
			return "", e
		}
	}
	h := header(mode, string(query), label, single, scope.Folders, scopeLine(scope, sh.Missing))
	if e := s.Write(textPrefix+"header", []byte(h)); e != nil {
		return "", e
	}
	exe, err := env.Sys.Executable()
	if err != nil {
		return "", errs.Internal("locating the koan binary: " + err.Error())
	}
	return "transform-header(" + helperLine(exe, "text", "header") + ")", nil
}

// helpVerb prints command mode's keys, for the preview.
func helpVerb(s *Session, args []string, _ Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	// The folder picker has no actions.
	var scope Scope
	if s != nil {
		if _, ok, _ := s.Read(scopeFile); ok {
			readJSON(s, scopeFile, &scope)
		}
	}
	var b strings.Builder
	b.WriteString("command mode\n\n")
	for _, row := range help {
		if _, isAction := lookupAction(row[0]); isAction && scope.Folders {
			continue
		}
		b.WriteString(row[0] + strings.Repeat(" ", 11-len(row[0])) + row[1] + "\n")
	}
	return []byte(b.String()), nil
}

func oneArg(args []string) *errs.Error {
	switch {
	case len(args) == 0:
		return errs.Usage([]errs.UsageProblem{{Reason: "missing query"}})
	case len(args) > 1:
		return errs.Usage([]errs.UsageProblem{{Argument: &args[1], Reason: "unexpected argument"}})
	}
	return nil
}
