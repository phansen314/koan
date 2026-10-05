package pick

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// xAction is x (pick-spec.md, Actions: Editing as JSON): the target's
// title, priority, tags and extra, as a JSON object in a file the editor
// opens with fzf suspended. Once it exits, one update applies what changed.
// A file that isn't valid, or that update refuses, is kept: the next x on
// the same task reopens it, with the edits. Any other action or a reload
// discards it.
var xAction = action{key: "x", arity: oneTarget, run: editJSON}

// xFile holds the open x: its target, the file, and what was written to it.
const xFile = "x.json"

// xState is an x: the target's key and ID, the file the editor opens, and
// the file's content as written, which an edit is compared with.
type xState struct {
	Key   string   `json:"key"`
	ID    model.ID `json:"id"`
	Path  string   `json:"path"`
	Wrote string   `json:"wrote"`
}

// xFields are the fields x edits, in the file's order.
var xFields = []string{"title", "priority", "tags", "extra"}

func editJSON(r *actionRun, targets []shownLine) {
	t := targets[0]
	var st xState
	kept, e := readX(r.s)
	if e != nil {
		r.err = e
		return
	}
	if kept != nil && kept.Key == t.Key {
		st = *kept // reopened, with the edits
	} else {
		if r.err = discardX(r.s); r.err != nil {
			return
		}
		b, failed := xContent(r.env, t)
		if failed != "" {
			r.status = "✗ x " + string(idNumber(t.ID)) + ": " + failed
			return
		}
		name := xFileName(t.ID)
		st = xState{Key: t.Key, ID: t.ID, Path: filepath.Join(r.s.Dir, name), Wrote: string(b)}
		if r.err = r.s.Write(name, b); r.err != nil {
			return
		}
		if r.err = writeJSON(r.s, xFile, st); r.err != nil {
			return
		}
	}
	// The editor is e's, on the one file.
	if r.err = writeJSON(r.s, editFile, []edited{{ID: t.ID, Path: st.Path}}); r.err != nil {
		return
	}
	if r.err = r.s.Delete(editFailedFile); r.err != nil {
		return
	}
	exe, err := r.env.Sys.Executable()
	if err != nil {
		r.err = errInternalExe(err)
		return
	}
	r.next = "execute(" + helperLine(exe, "edit") + ")+transform(" + helperLine(exe, "after-x") + ")"
}

// xContent is the file x writes for t: its editable fields as they are
// now, read fresh, pretty-printed. failed says why there is none.
func xContent(env Env, t shownLine) ([]byte, string) {
	v, failed := current(env, t)
	if failed != "" {
		return nil, failed
	}
	obj := &jsonio.Object{}
	obj.Set("title", string(v.Title))
	if v.Priority == nil {
		obj.Set("priority", nil)
	} else {
		obj.Set("priority", json.Number(strconv.FormatInt(*v.Priority, 10)))
	}
	tags := make([]any, len(v.Tags))
	for i, tag := range v.Tags {
		tags[i] = string(tag)
	}
	obj.Set("tags", tags)
	extra := v.Extra
	if extra == nil {
		extra = &jsonio.Object{}
	}
	obj.Set("extra", extra)
	b, err := jsonio.MarshalFile(obj)
	if err != nil {
		return nil, "encoding: " + err.Error()
	}
	return b, ""
}

func readX(s *Session) (*xState, *errs.Error) {
	if _, ok, e := s.Read(xFile); e != nil || !ok {
		return nil, e
	}
	var st xState
	if e := readJSON(s, xFile, &st); e != nil {
		return nil, e
	}
	return &st, nil
}

// discardX drops a kept x, if any, and its file.
func discardX(s *Session) *errs.Error {
	st, e := readX(s)
	if e != nil || st == nil {
		return e
	}
	if e := s.Delete(xFileName(st.ID)); e != nil {
		return e
	}
	return s.Delete(xFile)
}

// xFileName is the session file x writes for id.
func xFileName(id model.ID) string { return "x-" + string(idNumber(id)) + ".json" }

// afterX runs once x's editor exits: it applies what changed in one
// update, and reloads. An invalid file, or one update refuses, is kept for
// the next x on the task.
func afterX(s *Session, args []string, env Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	st, e := readX(s)
	if e != nil {
		return nil, e
	}
	if st == nil {
		return nil, errs.Internal("after-x with no x open")
	}
	r := &actionRun{s: s, env: env}
	status, keep := applyX(r, *st)
	if r.err != nil {
		return nil, r.err
	}
	if !keep {
		if e := discardX(s); e != nil {
			return nil, e
		}
	}
	failed, _, e := s.Read(editFailedFile)
	if e != nil {
		return nil, e
	}
	if len(failed) > 0 {
		status = joinStatus(status, "✗ "+string(failed))
	}
	if len(r.outcomes) == 0 {
		var sh shown
		if e := readJSON(s, shownFile, &sh); e != nil {
			return nil, e
		}
		return clearMarks(setStatus(s, env, sh.Warnings, status))
	}
	return reloadWithStatus(s, env, status, "", nil)
}

// applyX compares the edited file with what x wrote, and runs one update
// with what changed. It returns the status line, and whether the file is
// kept: when it isn't valid, update refused it, or the tree was busy.
func applyX(r *actionRun, st xState) (string, bool) {
	what := "x " + string(idNumber(st.ID))
	b, err := r.env.Ops.FS.ReadFile(st.Path)
	if err != nil {
		return "✗ " + what + ": " + err.Error(), false
	}
	if bytes.Equal(b, []byte(st.Wrote)) {
		return what + ": no change", false
	}
	edited, repeated, err := jsonio.ParseObject(b)
	switch {
	case err != nil:
		return "✗ " + what + ": not a JSON object: " + err.Error(), true
	case len(repeated) > 0:
		return "✗ " + what + ": " + repeated[0] + " given twice", true
	}
	for _, m := range edited.Members {
		if !slices.Contains(xFields, m.Key) {
			return "✗ " + what + ": " + errs.OneLine(m.Key) + " can't be edited here; only " + strings.Join(xFields, ", "), true
		}
	}
	wrote, _, err := jsonio.ParseObject([]byte(st.Wrote))
	if err != nil {
		r.err = errs.Internal("x wrote what it can't read: " + err.Error())
		return "", false
	}
	in := xChanges(wrote, edited)
	if in.Len() == 0 {
		return what + ": no change", false
	}
	in.Members = append([]jsonio.Member{{Key: "id", Value: idNumber(st.ID)}}, in.Members...)
	out := r.call(st.ID, "update", in, "updated", "update "+string(idNumber(st.ID)))
	return statusLine(r.outcomes), out.Error != nil && (out.Error.Kind == errs.KindInvalidInput || out.Error.Kind == errs.KindBusy)
}

// xChanges is update's input for what changed from wrote to edited, but
// for the ID: only fields edited, so that a change made elsewhere to
// another survives. A field left out of the file is not edited.
func xChanges(wrote, edited *jsonio.Object) *jsonio.Object {
	in := &jsonio.Object{}
	for _, f := range xFields {
		now, ok := edited.Get(f)
		was, _ := wrote.Get(f)
		if !ok || jsonio.Equal(was, now) {
			continue
		}
		switch f {
		case "tags":
			if sameSet(was, now) {
				continue
			}
			change := &jsonio.Object{}
			change.Set("replace_all", now)
			in.Set("tags", change)
		case "extra":
			wasObj, okWas := was.(*jsonio.Object)
			nowObj, okNow := now.(*jsonio.Object)
			if !okWas || !okNow {
				// Not an object: update says what's wrong.
				change := &jsonio.Object{}
				change.Set("merge", now)
				in.Set("extra", change)
				continue
			}
			merge := &jsonio.Object{}
			var remove []any
			for _, m := range nowObj.Members {
				if v, ok := wasObj.Get(m.Key); !ok || !jsonio.Equal(v, m.Value) {
					merge.Set(m.Key, m.Value)
				}
			}
			for _, m := range wasObj.Members {
				if _, ok := nowObj.Get(m.Key); !ok {
					remove = append(remove, m.Key)
				}
			}
			change := &jsonio.Object{}
			if merge.Len() > 0 {
				change.Set("merge", merge)
			}
			if remove != nil {
				change.Set("remove", remove)
			}
			in.Set("extra", change)
		default:
			in.Set(f, now)
		}
	}
	return in
}

// sameSet reports whether a and b are arrays of the same values, in any
// order, any repeated.
func sameSet(a, b any) bool {
	as, okA := a.([]any)
	bs, okB := b.([]any)
	if !okA || !okB {
		return false
	}
	in := func(v any, list []any) bool {
		return slices.ContainsFunc(list, func(w any) bool { return jsonio.Equal(v, w) })
	}
	for _, v := range as {
		if !in(v, bs) {
			return false
		}
	}
	for _, v := range bs {
		if !in(v, as) {
			return false
		}
	}
	return true
}
