package pick

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/ops"
)

// The action framework (pick-spec.md, Actions): command mode's keys that
// run operations. Each key's callback, act, finds the targets, runs the
// action's operations one call per target, logs every call, then reloads,
// clears the marks, and shows the outcome in the status line.

// arity is how many targets an action takes.
type arity int

const (
	anyTargets arity = iota // the marks, or the line under the cursor
	oneTarget               // as anyTargets, but refused with several marked
	noTargets               // none: the action ignores marks and cursor
)

// action is one of command mode's keys that runs operations.
type action struct {
	key   string
	arity arity
	// run runs the action on its targets, in line order, through r.
	run func(r *actionRun, targets []shownLine)
	// apply, for an action that asks for a value in a prompt, applies the
	// value once entered; it returns whether the prompt stays open.
	apply func(r *actionRun, value string, targets []shownLine) (keep bool)
	// choose, for an action that swaps in a choose list, applies the
	// action to the chosen lines' keys.
	choose func(r *actionRun, chosen []string, targets []shownLine)
}

// actions are the actions, in the order their keys are bound.
var actions []action

// init fills actions, which some actions' own code reads (the command
// keys, for a prompt).
func init() {
	actions = []action{completeAction, editAction, newAction, priorityAction, tagsAction, xAction, blockAction, unblockAction, moveAction, folderAction, scopeAction, reloadAction}
}

func lookupAction(key string) (action, bool) {
	i := slices.IndexFunc(actions, func(a action) bool { return a.key == key })
	if i < 0 {
		return action{}, false
	}
	return actions[i], true
}

// Session files for actions and loads.
const (
	// scopeFile holds the Scope, as JSON.
	scopeFile = "scope.json"
	// shownFile holds what the last load shows (shown), as JSON.
	shownFile = "shown.json"
	// linesFile holds the last load's lines, which reload-sync reads
	// through the lines verb.
	linesFile = "lines"
	// logFile holds the action log: every operation the actions ran, in
	// order, each with its input and its envelope, or null while it runs.
	logFile = "actions.json"
	// statusFile holds the last action's status line, without the
	// load's warnings.
	statusFile = "status"
)

// shown is what the last load shows: its lines, in line order, and what the
// header and status line count.
type shown struct {
	Lines []shownLine `json:"lines"`
	// Before holds the lines of the load before that this one dropped.
	// fzf shows a reload's list only once it is complete, while the
	// session already has it: a key pressed meanwhile passes a line
	// fzf still shows, which the person sees and means.
	Before   []shownLine `json:"before"`
	Missing  int         `json:"missing"`  // snapshot IDs the load lacks
	Warnings int         `json:"warnings"` // the load's
}

// shownLine is one line as the last load showed it.
type shownLine struct {
	Key       string          `json:"key"`
	ID        model.ID        `json:"id"`
	Readiness model.Readiness `json:"readiness"`
	Notes     string          `json:"notes"` // notes_path
	Priority  *int64          `json:"priority"`
	Tags      []model.Tag     `json:"tags"`
}

// writeLoaded records a load in the session: its lines, what they show, and
// the previews.
func writeLoaded(s *Session, l *Load, views []model.TaskView, lines []string, missing int) *errs.Error {
	sh := shown{Lines: make([]shownLine, len(views)), Before: []shownLine{}, Missing: missing, Warnings: len(l.Warnings)}
	for i, v := range views {
		sh.Lines[i] = shownLine{Key: key(v), ID: v.ID, Readiness: v.Readiness, Notes: v.NotesPath, Priority: v.Priority, Tags: v.Tags}
	}
	var prev shown
	if _, ok, e := s.Read(shownFile); e != nil {
		return e
	} else if ok {
		if e := readJSON(s, shownFile, &prev); e != nil {
			return e
		}
	}
	for _, l := range prev.Lines {
		if !slices.ContainsFunc(sh.Lines, func(n shownLine) bool { return n.Key == l.Key }) {
			sh.Before = append(sh.Before, l)
		}
	}
	if e := writeJSON(s, shownFile, sh); e != nil {
		return e
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line + "\n")
	}
	if e := s.Write(linesFile, []byte(b.String())); e != nil {
		return e
	}
	return writePreviews(s, l)
}

func writeJSON(s *Session, name string, v any) *errs.Error {
	b, err := json.Marshal(v)
	if err != nil {
		return errs.Internal("encoding " + name + ": " + err.Error())
	}
	return s.Write(name, b)
}

func readJSON(s *Session, name string, v any) *errs.Error {
	b, ok, e := s.Read(name)
	switch {
	case e != nil:
		return e
	case !ok:
		return errs.Internal("session has no " + name)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return errs.Internal("session " + name + ": " + err.Error())
	}
	return nil
}

// linesVerb prints the last load's lines, for reload-sync.
func linesVerb(s *Session, args []string, _ Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	b, _, e := s.Read(linesFile)
	return b, e
}

// act runs an action: its key, then the keys fzf passes ({+1}: the marked
// lines', or the one under the cursor's; none with an empty list).
func act(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if len(args) == 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Reason: "missing action"}})
	}
	a, ok := lookupAction(args[0])
	if !ok {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unknown action"}})
	}
	// An x kept for its edits lasts until any other action.
	if a.key != xAction.key {
		if e := discardX(s); e != nil {
			return nil, e
		}
	}
	// fzf still showed a choose list being left: its keys aren't tasks.
	if staleKeys(args[1:], false) {
		return loadingStatus(s, env)
	}
	var sh shown
	if e := readJSON(s, shownFile, &sh); e != nil {
		return nil, e
	}
	targets := inLineOrder(sh, args[1:])
	switch {
	case a.arity == noTargets:
		targets = nil
	case len(targets) == 0:
		return nil, nil // nothing to act on
	case a.arity == oneTarget && len(targets) > 1:
		return setStatus(s, env, sh.Warnings, fmt.Sprintf("✗ %s takes one task: %d marked", a.key, len(targets)))
	}
	r := &actionRun{s: s, env: env}
	a.run(r, targets)
	if r.err != nil {
		// fzf stays in command mode, the only one act runs in: a prompt or
		// choose list half opened must not take the next Enter.
		backToCommand(s)
		return nil, r.err
	}
	if r.next != "" {
		return []byte(r.next), nil
	}
	status := r.status
	if status == "" {
		status = statusLine(r.outcomes)
	}
	var out []byte
	var e *errs.Error
	if len(r.outcomes) == 0 && !r.reload {
		// No reload to clear the marks, but the action is over.
		out, e = clearMarks(setStatus(s, env, sh.Warnings, status))
	} else {
		// Reload after every action that ran an operation (pick-spec.md,
		// Actions).
		out, e = reloadWithStatus(s, env, status, r.ifReloaded, r.nextScope)
	}
	return out, e
}

// reloadWithStatus reloads, in next if not nil, then shows status in the
// status line, and ifReloaded after it if the reload went through. A load
// that fails leaves the list and the scope as they were, and the status
// line says why.
func reloadWithStatus(s *Session, env Env, status, ifReloaded string, next *Scope) ([]byte, *errs.Error) {
	out, warnings, failed := reload(s, env, next)
	if failed != nil {
		var sh shown
		if e := readJSON(s, shownFile, &sh); e != nil {
			return nil, e
		}
		// The list stays, but the action is over.
		return clearMarks(setStatus(s, env, sh.Warnings, joinStatus(status, reloadFailed(failed))))
	}
	footer, e := setStatus(s, env, warnings, joinStatus(status, ifReloaded))
	if e != nil {
		return nil, e
	}
	return []byte(out + "+" + string(footer)), nil
}

// inLineOrder is the shown lines with the given keys, in line order, once
// each, then any the last load dropped, as the load before showed them. A
// key in neither, which fzf never passes, is left out.
func inLineOrder(sh shown, keys []string) []shownLine {
	var out []shownLine
	for _, l := range append(slices.Clone(sh.Lines), sh.Before...) {
		if slices.Contains(keys, l.Key) {
			out = append(out, l)
		}
	}
	return out
}

// actionRun is one run of an action: the calls it made, and their
// outcomes.
type actionRun struct {
	s        *Session
	env      Env
	outcomes []outcome
	// status replaces the status line the outcomes make, when set.
	status string
	// next, when set, is what fzf does next instead of the reload: e.g.
	// run an editor.
	next string
	// reload asks for a reload though no operation ran, e.g. to show a
	// new scope.
	reload bool
	// ifReloaded is said in the status line only if the reload goes
	// through: r's ✓ reloaded.
	ifReloaded string
	// nextScope, when set, is the scope to reload in: it becomes the
	// session's only if the reload goes through.
	nextScope *Scope
	// clearQuery, after a prompt, clears the search query rather than
	// restore it.
	clearQuery bool
	// cursorTo is the key of the line the cursor goes to once the reload
	// is in, if the line is in it; the status line says if it isn't.
	cursorTo string
	// returnTo is as cursorTo, but a line not in the list goes unsaid.
	returnTo string
	// err is a session failure, which ends the action.
	err *errs.Error
}

// outcome is one call's result, for the status line.
type outcome struct {
	id   model.ID
	done string      // the success, as its group says it: "completed"
	what string      // the call, as a failure says it: "complete 42"
	err  *errs.Error // nil on success
}

// logEntry is one call in the action log (pick-output's actions).
type logEntry struct {
	Operation string          `json:"operation"`
	Input     *jsonio.Object  `json:"input"`
	Output    json.RawMessage `json:"output"`
}

// call runs operation op with in, for target id, as one call: logged with a
// null output before it runs, then completed with its envelope, so that a
// helper that dies between the two leaves the entry null (pick-spec.md,
// Session). done and what name the call in the status line. After a
// session failure, it runs nothing.
func (r *actionRun) call(id model.ID, op string, in *jsonio.Object, done, what string) ops.Envelope {
	if r.err != nil {
		return ops.Failed(r.err)
	}
	// The log's entries are kept as written: an input holds numbers as
	// they were given.
	var log []json.RawMessage
	if _, ok, e := r.s.Read(logFile); e != nil {
		r.err = e
	} else if ok {
		r.err = readJSON(r.s, logFile, &log)
	}
	if r.err != nil {
		return ops.Failed(r.err)
	}
	entry := logEntry{Operation: op, Input: in, Output: json.RawMessage("null")}
	b, err := jsonio.MarshalLine(entry)
	if err != nil {
		r.err = errs.Internal("encoding " + op + "'s input: " + err.Error())
		return ops.Failed(r.err)
	}
	log = append(log, json.RawMessage(strings.TrimSuffix(string(b), "\n")))
	if r.err = writeJSON(r.s, logFile, log); r.err != nil {
		return ops.Failed(r.err)
	}
	out := ops.Run(op, in, nil, r.env.Ops)
	if b, err = jsonio.MarshalLine(out); err != nil {
		r.err = errs.Internal("encoding " + op + "'s envelope: " + err.Error())
		return ops.Failed(r.err)
	}
	entry.Output = json.RawMessage(strings.TrimSuffix(string(b), "\n"))
	if b, err = jsonio.MarshalLine(entry); err != nil {
		r.err = errs.Internal("encoding " + op + "'s log entry: " + err.Error())
		return ops.Failed(r.err)
	}
	log[len(log)-1] = json.RawMessage(strings.TrimSuffix(string(b), "\n"))
	if r.err = writeJSON(r.s, logFile, log); r.err != nil {
		return ops.Failed(r.err)
	}
	r.outcomes = append(r.outcomes, outcome{id: id, done: done, what: what, err: out.Error})
	return out
}

// current is t's task as it now is, read fresh by ID, found as findLine
// finds it. failed says why there is none.
func current(env Env, t shownLine) (v model.TaskView, failed string) {
	in := &jsonio.Object{}
	in.Set("id", idNumber(t.ID))
	out := ops.Run("show", in, nil, env.Ops)
	if !out.OK {
		return model.TaskView{}, errText(out.Error)
	}
	views := out.Result.(ops.ShowOutput).Tasks
	i, failed := findLine(views, t)
	if failed != "" {
		return model.TaskView{}, failed
	}
	return views[i], ""
}

// findLine is the index in tasks of t's task, as the final read finds a
// selected line (pick-spec.md, Output): by its key, else the one task with
// its ID, wherever it now is. failed says why there is none: no task with
// its ID, or several copies, none in the line's folder.
func findLine(tasks []model.TaskView, t shownLine) (i int, failed string) {
	if i = slices.IndexFunc(tasks, func(v model.TaskView) bool { return key(v) == t.Key }); i >= 0 {
		return i, ""
	}
	copies := 0
	for j, v := range tasks {
		if v.ID == t.ID {
			i, copies = j, copies+1
		}
	}
	switch {
	case copies == 0:
		return -1, "is gone"
	case copies > 1:
		_, folder, _ := strings.Cut(t.Key, "@")
		return -1, fmt.Sprintf("has %d copies, none in %s", copies, folder)
	}
	return i, ""
}

// idNumber is id as an operation's input holds it.
func idNumber(id model.ID) json.Number {
	return json.Number(strconv.FormatInt(int64(id), 10))
}

// jsonNumberOf is an integer's text as an operation's input holds it.
func jsonNumberOf(text string) json.Number { return json.Number(text) }

// loggedActions is the action log, for the output: every entry, a null
// output included.
func loggedActions(s *Session) ([]any, *errs.Error) {
	var log []json.RawMessage
	if _, ok, e := s.Read(logFile); e != nil {
		return nil, e
	} else if ok {
		if e := readJSON(s, logFile, &log); e != nil {
			return nil, e
		}
	}
	out := make([]any, len(log))
	for i, entry := range log {
		out[i] = entry
	}
	return out, nil
}

// reload runs a load in the session's scope, or in next if not nil, and
// records it. It returns the actions that show it, with the marks cleared,
// and the load's warnings; or the load's error, when the list should stay
// as it was (pick-spec.md, Errors). next becomes the session's scope only
// once its load is in, so that a failed one leaves the header, the prompt
// and later actions in the scope the list shows.
func reload(s *Session, env Env, next *Scope) (string, int, *errs.Error) {
	var scope Scope
	if next != nil {
		scope = *next
	} else if e := readJSON(s, scopeFile, &scope); e != nil {
		return "", 0, e
	}
	if scope.Source != "" {
		ids, reason := sourceIDs(env, scope.Source, sourceLimit)
		if reason != "" {
			return "", 0, &errs.Error{Kind: sourceFailed, Message: reason}
		}
		scope.IDs = ids
	}
	l, failed := load(env.Ops, true)
	if failed != nil {
		return "", 0, failed.Error
	}
	if e := l.checkFolder(scope.Folder); e != nil {
		return "", 0, e
	}
	views, missing := l.candidates(scope)
	lines := renderLines(views, !noColor(env.Sys.Environ()))
	if e := writeLoaded(s, l, views, lines, missing); e != nil {
		return "", 0, e
	}
	exe, err := env.Sys.Executable()
	if err != nil {
		return "", 0, errs.Internal("locating the ftask binary: " + err.Error())
	}
	var prompt string
	if next != nil {
		// Reloads run in command mode, whose prompt names the scope.
		if e := writeJSON(s, scopeFile, *next); e != nil {
			return "", 0, e
		}
		if e := s.Write(textPrefix+"prompt", []byte(promptOf(*next))); e != nil {
			return "", 0, e
		}
		prompt = "+transform-prompt(" + helperLine(exe, "text", "prompt") + ")"
	}
	header, e := writeHeader(s, env)
	if e != nil {
		return "", 0, e
	}
	// Marks are cleared explicitly, on every reload: fzf keeps them across
	// one with --track --id-nth in the person's options (pick-spec.md,
	// Modes).
	return "clear-selection+reload-sync(" + helperLine(exe, "lines") + ")+" + header + prompt, len(l.Warnings), nil
}

// setStatus records the status line and returns the action that shows it,
// with the load's warnings after it.
func setStatus(s *Session, env Env, warnings int, status string) ([]byte, *errs.Error) {
	if e := s.Write(statusFile, []byte(status)); e != nil {
		return nil, e
	}
	if warnings > 0 {
		status = joinStatus(status, plural(warnings, "warning"))
	}
	if e := s.Write(textPrefix+"footer", []byte(status)); e != nil {
		return nil, e
	}
	exe, err := env.Sys.Executable()
	if err != nil {
		return nil, errs.Internal("locating the ftask binary: " + err.Error())
	}
	return []byte("transform-footer(" + helperLine(exe, "text", "footer") + ")"), nil
}

// backToCommand puts the session back in command mode after a failure,
// as best it can: it is already failing.
func backToCommand(s *Session) {
	s.Delete(promptFile)
	s.Delete(chooseFile)
	s.Write(modeFile, []byte(modeCommand))
}

// clearMarks is out, an action's ending without a reload, after clearing
// the marks: they are cleared after every action (pick-spec.md, Modes).
func clearMarks(out []byte, e *errs.Error) ([]byte, *errs.Error) {
	if e != nil {
		return nil, e
	}
	return []byte("clear-selection+" + string(out)), nil
}

// reloadFailed is a failed reload as the status line shows it: a live
// source's ✗ source: …, or the load's error.
func reloadFailed(e *errs.Error) string {
	if e.Kind == sourceFailed {
		return "✗ source: " + e.Message
	}
	return "✗ reload: " + errText(e)
}

func joinStatus(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " · " + b
}

// maxIDs is the most IDs a group in the status line lists.
const maxIDs = 5

// statusLine is the outcomes as the status line shows them (pick-spec.md,
// Status line): one call's whole, or several grouped, successes first.
func statusLine(outs []outcome) string {
	switch len(outs) {
	case 0:
		return ""
	case 1:
		o := outs[0]
		if o.err == nil {
			return fmt.Sprintf("✓ %s %d", o.done, o.id)
		}
		return fmt.Sprintf("✗ %s: %s", o.what, errText(o.err))
	}
	var groups []string
	var dones []string
	byDone := map[string][]string{}
	var failed []string
	for _, o := range outs {
		if o.err != nil {
			failed = append(failed, fmt.Sprintf("%d %s", o.id, kindText(o.err)))
			continue
		}
		if _, ok := byDone[o.done]; !ok {
			dones = append(dones, o.done)
		}
		byDone[o.done] = append(byDone[o.done], strconv.FormatInt(int64(o.id), 10))
	}
	for _, d := range dones {
		groups = append(groups, fmt.Sprintf("✓ %s %d: %s", d, len(byDone[d]), firstFew(byDone[d])))
	}
	if len(failed) > 0 {
		groups = append(groups, fmt.Sprintf("✗ %d failed: %s", len(failed), firstFew(failed)))
	}
	return strings.Join(groups, " · ")
}

// firstFew is items, at most maxIDs of them, then +N for the rest.
func firstFew(items []string) string {
	if len(items) <= maxIDs {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:maxIDs], ", ") + fmt.Sprintf(" +%d", len(items)-maxIDs)
}

// errText is an error as the status line shows it: its kind, its rule if
// any, and its message.
func errText(e *errs.Error) string {
	msg := e.Message
	if e.Kind == errs.KindUsage {
		msg = strings.TrimPrefix(msg, "usage: ") // the kind says it already
	}
	return kindText(e) + ": " + msg
}

// kindText is an error's kind, with its rule if it has one: "conflict
// (acyclic)".
func kindText(e *errs.Error) string {
	var d struct {
		Rule string `json:"rule"`
	}
	if b, err := json.Marshal(e.Details); err == nil {
		json.Unmarshal(b, &d)
	}
	if d.Rule != "" {
		return fmt.Sprintf("%s (%s)", e.Kind, d.Rule)
	}
	return string(e.Kind)
}

func errInternalExe(err error) *errs.Error {
	return errs.Internal("locating the ftask binary: " + err.Error())
}

// fitWidth cuts a one-line text to width terminal cells, with …; width 0
// or less is unknown, and leaves it whole.
func fitWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	return runewidth.Truncate(s, width, "…")
}
