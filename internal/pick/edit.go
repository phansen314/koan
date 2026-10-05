package pick

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// editAction is e (pick-spec.md, Actions): the targets' notes, all as
// arguments to one editor, with fzf suspended. koan never sees the edits;
// pick reports the notes that changed in notes_edited. The editor runs
// through execute, never inside a callback (pick-spec.md, fzf contract):
// e's callback records the notes' hashes and returns
// execute(edit)+transform(after-edit), so fzf gives the editor the
// terminal, then compares once it exits.
var editAction = action{key: "e", arity: anyTargets, run: editNotes}

// Session files for e.
const (
	// editFile holds the notes being edited, with their hashes from
	// before the editor ran.
	editFile = "edit.json"
	// editFailedFile holds why the editor failed, if it did.
	editFailedFile = "edit-failed"
	// notesEditedFile holds notes_edited so far.
	notesEditedFile = "notes-edited.json"
)

// edited is one note being edited.
type edited struct {
	ID   model.ID `json:"id"`
	Path string   `json:"path"`
	Hash string   `json:"hash"` // of the content before; a missing file's is the empty content's
}

func editNotes(r *actionRun, targets []shownLine) {
	es := make([]edited, len(targets))
	for i, t := range targets {
		// The notes path as it is now: the task may have moved since the
		// load, and its notes with it.
		v, failed := current(r.env, t)
		if failed != "" {
			r.status = "✗ e: " + string(idNumber(t.ID)) + ": " + failed
			return
		}
		h, err := notesHash(r.env.Ops.FS, v.NotesPath)
		if err != nil {
			r.status = "✗ e: " + err.Error()
			return
		}
		es[i] = edited{ID: t.ID, Path: v.NotesPath, Hash: h}
	}
	if r.err = writeJSON(r.s, editFile, es); r.err != nil {
		return
	}
	if r.err = r.s.Delete(editFailedFile); r.err != nil {
		return
	}
	exe, err := r.env.Sys.Executable()
	if err != nil {
		r.err = errs.Internal("locating the koan binary: " + err.Error())
		return
	}
	r.next = "execute(" + helperLine(exe, "edit") + ")+transform(" + helperLine(exe, "after-edit") + ")"
}

// notesHash is the hash of a notes file's content; a missing one reads as
// empty, as its notes do.
func notesHash(fsy fsys.FS, path string) (string, error) {
	b, err := fsy.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// editVerb runs the editor on the notes e recorded: $VISUAL, else $EDITOR,
// else vi, through sh, so that the variable may hold options. It runs in
// fzf's execute, with the terminal. Its failure is for after-edit to show.
func editVerb(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	var es []edited
	if e := readJSON(s, editFile, &es); e != nil {
		return nil, e
	}
	environ := env.Sys.Environ()
	editor := lookupEnv(environ, "VISUAL")
	if editor == "" {
		editor = lookupEnv(environ, "EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	argv := []string{"sh", "-c", editor + ` "$@"`, editor}
	for _, ed := range es {
		argv = append(argv, ed.Path)
	}
	// ctrl-c in the editor is SIGINT to the whole foreground process
	// group, this helper included, which carries on to report.
	restore := env.Sys.CatchInterrupts()
	status, err := env.Sys.RunEditor(argv, environ)
	restore()
	var failed string
	switch {
	case err != nil:
		failed = fmt.Sprintf("e: editor failed: %v", err)
	case status != 0:
		failed = fmt.Sprintf("e: editor exited with status %d", status)
	}
	if failed != "" {
		if e := s.Write(editFailedFile, []byte(failed)); e != nil {
			return nil, e
		}
	}
	return nil, nil
}

// afterEdit runs once the editor exits: it records the notes whose content
// changed in notes_edited, then reloads and shows the status line.
func afterEdit(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	var es []edited
	if e := readJSON(s, editFile, &es); e != nil {
		return nil, e
	}
	notesEdited, e := readNotesEdited(s)
	if e != nil {
		return nil, e
	}
	var changed []outcome
	var problems []string
	for _, ed := range es {
		h, err := notesHash(env.Ops.FS, ed.Path)
		switch {
		case err != nil:
			problems = append(problems, "✗ e: "+err.Error())
		case h != ed.Hash:
			changed = append(changed, outcome{id: ed.ID, done: "edited notes"})
			if !slices.Contains(notesEdited, ed.ID) {
				notesEdited = append(notesEdited, ed.ID)
			}
		}
	}
	if e := writeJSON(s, notesEditedFile, notesEdited); e != nil {
		return nil, e
	}
	status := statusLine(changed)
	if status == "" {
		status = "notes unchanged"
	}
	for _, p := range problems {
		status = joinStatus(status, p)
	}
	failed, _, e := s.Read(editFailedFile)
	if e != nil {
		return nil, e
	}
	if len(failed) > 0 {
		status = joinStatus(status, "✗ "+string(failed))
	}
	return reloadWithStatus(s, env, status, "", nil)
}

// readNotesEdited is notes_edited so far: IDs in the order first edited.
func readNotesEdited(s *Session) ([]model.ID, *errs.Error) {
	ids := []model.ID{}
	if _, ok, e := s.Read(notesEditedFile); e != nil {
		return nil, e
	} else if ok {
		if e := readJSON(s, notesEditedFile, &ids); e != nil {
			return nil, e
		}
	}
	return ids, nil
}
