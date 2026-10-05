package pick

import (
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
)

// Choose mode (pick-spec.md, Modes): an action swaps in a second list,
// such as candidate blockers or folders, which typing filters. Enter
// applies the action to the chosen lines, the marked ones or the one under
// the cursor; Esc cancels. Either way, back to command mode with the task
// list, as a prompt goes back. In a single-choice list Tab doesn't mark:
// Enter takes the line under the cursor.

// modeChoose is the mode file's content in choose mode.
const modeChoose = "choose"

// Session files for choose mode.
const (
	// chooseFile holds the open choose list.
	chooseFile = "choose.json"
	// choicesFile holds its lines, which reload-sync reads through the
	// choices verb.
	choicesFile = "choices"
)

// choiceMark begins every choose list line's key, which task keys (an ID)
// and folder keys (a path) never do. fzf shows a reload's list only once
// it is complete, while the session already has the mode it is for, so a
// key pressed meanwhile passes the other list's keys; the mark tells them
// apart, even where a choose list's lines are tasks.
const choiceMark = "~"

// stillLoading is the status line for a key that passed the other list's
// keys: it did nothing, and works once fzf shows the list.
const stillLoading = "✗ list still loading: nothing done"

// staleKeys reports whether any of keys is from the list before the one
// the session has: a task list's while choosing, or a choose list's.
func staleKeys(keys []string, choosing bool) bool {
	return slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, choiceMark) != choosing })
}

// loadingStatus shows stillLoading in the status line.
func loadingStatus(s *Session, env Env) ([]byte, *errs.Error) {
	var sh shown
	if e := readJSON(s, shownFile, &sh); e != nil {
		return nil, e
	}
	return setStatus(s, env, sh.Warnings, stillLoading)
}

// chooseState is an open choose list: the action choosing, its targets,
// the search query it borrowed the line from, and whether one line only is
// chosen.
type chooseState struct {
	Action  string      `json:"action"`
	Label   string      `json:"label"`
	Targets []shownLine `json:"targets"`
	Saved   string      `json:"saved"`
	Single  bool        `json:"single"`
}

// openChoose swaps in a choose list of lines for the running action,
// labelled e.g. "blockers of 42> ". Each line starts with its key, then a
// tab, as task lines do; fzf gets the key after choiceMark. The search query is saved, to come back
// afterwards, and the choose list's query starts empty.
func (r *actionRun) openChoose(key, label string, lines []string, single bool, targets []shownLine) {
	saved, _, e := r.s.Read(queryFile)
	if e != nil {
		r.err = e
		return
	}
	st := chooseState{Action: key, Label: label, Targets: targets, Saved: string(saved), Single: single}
	if targets == nil {
		st.Targets = []shownLine{}
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(choiceMark + l + "\n")
	}
	for _, step := range []func() *errs.Error{
		func() *errs.Error { return writeJSON(r.s, chooseFile, st) },
		func() *errs.Error { return r.s.Write(choicesFile, []byte(b.String())) },
		func() *errs.Error { return r.s.Write(modeFile, []byte(modeChoose)) },
		func() *errs.Error { return r.s.Write(textPrefix+"prompt", []byte(label)) },
		// The cursor starts on the first line, not where it was in the
		// task list.
		func() *errs.Error { return r.s.Write(cursorFile, []byte("1")) },
	} {
		if r.err = step(); r.err != nil {
			return
		}
	}
	header, e := writeHeader(r.s, r.env)
	if e != nil {
		r.err = e
		return
	}
	exe, err := r.env.Sys.Executable()
	if err != nil {
		r.err = errInternalExe(err)
		return
	}
	unbind := strings.Join(commandKeys(), ",")
	if single {
		unbind += ",tab"
	}
	// The input is shown before the query changes: fzf ignores query edits
	// while it is hidden. Marks are cleared on every switch of lists.
	r.next = "show-input+unbind(" + unbind + ")" +
		"+transform-prompt(" + helperLine(exe, "text", "prompt") + ")" +
		"+change-query()+clear-selection+rebind(load)+reload-sync(" + helperLine(exe, "choices") + ")+" + header
}

// choicesVerb prints the open choose list's lines, for reload-sync.
func choicesVerb(s *Session, args []string, _ Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	b, _, e := s.Read(choicesFile)
	return b, e
}

// applyChoose is Enter in choose mode: the action applies to keys, the
// chosen lines' keys; none chosen, from an empty list, is no change. Then
// back to the task list. Keys from the task list, which fzf still showed,
// do nothing, and the choose list stays.
func applyChoose(s *Session, keys []string, env Env) ([]byte, *errs.Error) {
	if staleKeys(keys, true) {
		return loadingStatus(s, env)
	}
	var st chooseState
	if e := readJSON(s, chooseFile, &st); e != nil {
		return nil, e
	}
	a, ok := lookupAction(st.Action)
	if !ok || a.choose == nil {
		return nil, errs.Internal("choose list for an action with no choice: " + st.Action)
	}
	keys, e := inChoiceOrder(s, keys)
	if e != nil {
		return nil, e
	}
	if st.Single && len(keys) > 1 {
		keys = keys[:1]
	}
	r := &actionRun{s: s, env: env}
	if len(keys) == 0 {
		r.status = "no change"
	} else {
		a.choose(r, keys, st.Targets)
	}
	if r.err != nil {
		return nil, r.err
	}
	status := r.status
	if status == "" {
		status = statusLine(r.outcomes)
	}
	// Back on the target, if it's still in the list.
	if len(st.Targets) > 0 {
		r.returnTo = st.Targets[0].Key
	}
	return backToTasks(s, env, r, st.Saved, status)
}

// inChoiceOrder is keys in the choose list's order, not the order fzf
// passes marks in, which is the order marked; once each; without
// choiceMark.
func inChoiceOrder(s *Session, keys []string) ([]string, *errs.Error) {
	b, _, e := s.Read(choicesFile)
	if e != nil {
		return nil, e
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		k, _, _ := strings.Cut(l, "\t")
		if k != "" && slices.Contains(keys, k) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for i, k := range out {
		out[i] = strings.TrimPrefix(k, choiceMark)
	}
	return out, nil
}

// cancelChoose is Esc in choose mode: back to command mode with the task
// list, and the search query as it was.
func cancelChoose(s *Session, env Env) ([]byte, *errs.Error) {
	var st chooseState
	if e := readJSON(s, chooseFile, &st); e != nil {
		return nil, e
	}
	// Back on the target.
	if len(st.Targets) > 0 {
		if _, e := armCursor(s, st.Targets[0].Key); e != nil {
			return nil, e
		}
	}
	return cancelToTasks(s, env, st.Saved)
}

// chooseLabel is the open choose list's label and whether it chooses one
// line, for the header.
func chooseLabel(s *Session) (string, bool, *errs.Error) {
	var st chooseState
	if e := readJSON(s, chooseFile, &st); e != nil {
		return "", false, e
	}
	return st.Label, st.Single, nil
}
