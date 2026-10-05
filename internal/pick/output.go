package pick

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/ops"
)

// selectionFile holds the selection Enter's or quit's callback recorded:
// one line key per line, none for an empty selection. Its presence, not
// fzf's exit status, says the session ended by Enter or quit
// (pick-spec.md, fzf contract).
const selectionFile = "selection"

// enter is Enter: its arguments are the query, then the keys fzf passes
// ({+1}: the marked lines', or the one under the cursor; none with an
// empty list). In a prompt, it applies the query as the value. Otherwise
// it records the selection and accepts, unless fzf still showed a choose
// list being left: then it does nothing, and says so.
func enter(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if len(args) == 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Reason: "missing query"}})
	}
	if b, _, e := s.Read(modeFile); e != nil {
		return nil, e
	} else if string(b) == modePrompt {
		return applyPrompt(s, args[0], env)
	} else if string(b) == modeChoose {
		return applyChoose(s, args[1:], env)
	}
	if staleKeys(args[1:], false) {
		return loadingStatus(s, env)
	}
	return record(s, args[1:])
}

// record records the selection, keys, and accepts.
func record(s *Session, keys []string) ([]byte, *errs.Error) {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "\n")
	}
	if e := s.Write(selectionFile, []byte(b.String())); e != nil {
		return nil, e
	}
	return []byte("accept"), nil
}

// quit records an empty selection and accepts.
func quit(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	return record(s, nil)
}

// Output is pick's result for tasks (pick-output).
type Output struct {
	Tasks       ops.Tasks  `json:"tasks"`
	Missing     []model.ID `json:"missing"`
	Actions     []any      `json:"actions"`
	NotesEdited []model.ID `json:"notes_edited"`
}

// CancelledDetails is cancelled's details (pick-error-details).
type CancelledDetails struct {
	Actions []any `json:"actions"`
}

// IncompleteDetails is incomplete's details (pick-error-details): the
// final read's error, whole.
type IncompleteDetails struct {
	Actions []any       `json:"actions"`
	Error   *errs.Error `json:"error"`
}

// finish decides how the session ended, from the session first, then from
// fzf's exit status (pick-spec.md, fzf contract), and writes the envelope.
// A recorded selection is Enter or quit, whatever the status; without one,
// 130 is cancel, and any other status is fzf-failed.
func finish(env Env, s *Session, status int, fields []string, folders bool) ops.Envelope {
	actions, e := loggedActions(s)
	if e != nil {
		return ops.Failed(e)
	}
	b, recorded, e := s.Read(selectionFile)
	switch {
	case e != nil:
		return ops.Failed(e)
	case !recorded && status == 130:
		return ops.Failed(&errs.Error{Kind: errs.KindCancelled, Message: "cancelled", Details: CancelledDetails{Actions: actions}})
	case !recorded:
		return ops.Failed(unavailable(fmt.Sprintf("fzf exited with status %d without a selection; any message from fzf is above, on the terminal", status),
			UnavailableDetails{Reason: FzfFailed, Status: &status, Actions: actions}))
	}
	if folders {
		return emitFolders(env, strings.Fields(string(b)), actions)
	}
	notesEdited, e := readNotesEdited(s)
	if e != nil {
		return ops.Failed(e)
	}
	return emit(env, strings.Fields(string(b)), fields, actions, notesEdited)
}

// FoldersOutput is the folder picker's result (pick-output).
type FoldersOutput struct {
	Folders []model.FolderPath `json:"folders"`
	Missing []model.FolderPath `json:"missing"`
	Actions []any              `json:"actions"`
}

// emitFolders runs the final read and writes the selected folders, keys,
// as Enter does in the folder picker (pick-spec.md, Folder picker): those
// the read finds, in tree order, and the rest, moved or deleted meanwhile,
// in missing.
func emitFolders(env Env, keys []string, actions []any) ops.Envelope {
	l, failed := load(env.Ops, true)
	if failed != nil {
		e := &errs.Error{
			Kind:    errs.KindIncomplete,
			Message: "the session ended, but its result could not be read: " + failed.Error.Message,
			Details: IncompleteDetails{Actions: actions, Error: failed.Error},
		}
		return ops.Envelope{Error: e, Warnings: failed.Warnings}
	}
	folders := []model.FolderPath{}
	for _, f := range l.Folders {
		if slices.Contains(keys, string(f)) {
			folders = append(folders, f)
		}
	}
	missing := []model.FolderPath{}
	for _, k := range keys {
		f := model.FolderPath(k)
		if !slices.Contains(l.Folders, f) && !slices.Contains(missing, f) {
			missing = append(missing, f)
		}
	}
	slices.Sort(missing)
	return ops.Envelope{OK: true, Result: FoldersOutput{Folders: folders, Missing: missing, Actions: actions}, Warnings: l.Warnings}
}

// emit runs the final read and writes the selection of keys, as Enter does
// (pick-spec.md, Output).
func emit(env Env, keys, fields []string, actions []any, notesEdited []model.ID) ops.Envelope {
	l, failed := load(env.Ops, false)
	if failed != nil {
		e := &errs.Error{
			Kind:    errs.KindIncomplete,
			Message: "the session ended, but its result could not be read: " + failed.Error.Message,
			Details: IncompleteDetails{Actions: actions, Error: failed.Error},
		}
		return ops.Envelope{Error: e, Warnings: failed.Warnings}
	}
	tasks, missing := selected(l.Tasks, keys)
	return ops.Envelope{
		OK:       true,
		Result:   Output{Tasks: ops.Tasks{Views: tasks, Fields: fields}, Missing: missing, Actions: actions, NotesEdited: notesEdited},
		Warnings: l.Warnings,
	}
}

// selected finds each selected key's task in views, the final read: the one
// task with its ID, wherever it now is; with several copies, the copy in
// the key's folder; otherwise its ID is missing. Tasks are in pick's line
// order, once each; missing IDs ascend, once each.
func selected(views []model.TaskView, keys []string) ([]model.TaskView, []model.ID) {
	tasks := []model.TaskView{}
	missing := []model.ID{}
	seen := map[string]bool{}
	for _, k := range keys {
		idText, folder, ok := strings.Cut(k, "@")
		n, err := strconv.ParseInt(idText, 10, 64)
		if !ok || err != nil {
			continue // not a key pick wrote
		}
		id := model.ID(n)
		var copies []model.TaskView
		for _, v := range views {
			if v.ID == id {
				copies = append(copies, v)
			}
		}
		if len(copies) > 1 {
			copies = slices.DeleteFunc(copies, func(v model.TaskView) bool { return string(v.Folder) != folder })
		}
		if len(copies) != 1 {
			if !slices.Contains(missing, id) {
				missing = append(missing, id)
			}
			continue
		}
		if v := copies[0]; !seen[key(v)] {
			seen[key(v)] = true
			tasks = append(tasks, v)
		}
	}
	slices.SortFunc(tasks, ops.PickOrder)
	slices.Sort(missing)
	return tasks, missing
}
