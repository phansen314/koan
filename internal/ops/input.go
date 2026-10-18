package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
)

// input is a command's input object, read one field at a time so that every
// problem in it is found, not just the first.
type input struct {
	m        map[string]json.RawMessage
	used     map[string]bool
	problems []errs.Problem
}

func parseInput(raw []byte) (*input, *errs.Error) {
	in := &input{m: map[string]json.RawMessage{}, used: map[string]bool{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return in, nil
	}
	m, err := model.Keys(raw)
	if err != nil {
		return nil, errs.Invalid([]errs.Problem{{Field: "", Reason: "is not one JSON object: " + err.Error()}})
	}
	in.m = m
	return in, nil
}

func (in *input) bad(field, format string, args ...any) {
	in.problems = append(in.problems, errs.Problem{Field: field, Reason: fmt.Sprintf(format, args...)})
}

// done reports every problem found, and every field no one asked for.
func (in *input) done() *errs.Error {
	in.top().unknown("this command")
	if len(in.problems) == 0 {
		return nil
	}
	return errs.Invalid(in.problems)
}

func (in *input) has(k string) bool { _, ok := in.m[k]; return ok }

// fields reads one object's fields: the input itself (at == ""), or one
// entry of a batch. used holds the fields asked for.
type fields struct {
	in   *input
	m    map[string]json.RawMessage
	at   string
	used map[string]bool
}

func (in *input) top() fields { return fields{in: in, m: in.m, used: in.used} }

// entry reads one entry of a batch, at the JSON pointer `at`. It returns
// false when the entry is not an object, which it records.
func (in *input) entry(raw json.RawMessage, at string) (fields, bool) {
	m, err := model.Keys(raw)
	if err != nil {
		in.bad(at, "must be a JSON object")
		return fields{}, false
	}
	return fields{in: in, m: m, at: at, used: map[string]bool{}}, true
}

// get decodes field k into v. It returns false when the field is absent, or
// is wrong, which it records.
func (f fields) get(k string, required bool, v any, what string) bool {
	f.used[k] = true
	raw, ok := f.m[k]
	if !ok {
		if required {
			f.in.bad(f.at+"/"+k, "is required")
		}
		return false
	}
	if err := model.Decode(raw, v); err != nil || string(raw) == "null" {
		f.in.bad(f.at+"/"+k, "must be %s", what)
		return false
	}
	return true
}

// unknown records every field of the object no one asked for, as not a
// field of what.
func (f fields) unknown(what string) {
	for _, k := range slices.Sorted(maps.Keys(f.m)) {
		if !f.used[k] {
			f.in.bad(f.at+"/"+k, "is not a field of %s", what)
		}
	}
}

func (f fields) has(k string) bool { _, ok := f.m[k]; return ok }

// idOf reads an ID as an input may give it: an integer, or a string with or
// without its `@`.
func idOf(raw json.RawMessage) (model.ID, bool) {
	var n model.ID
	if model.Decode(raw, &n) == nil {
		return n, model.ValidID(n)
	}
	var s string
	if model.Decode(raw, &s) == nil {
		return model.ParseIDRef(s)
	}
	return 0, false
}

func (f fields) id(k string, required bool) model.ID {
	var raw json.RawMessage
	if !f.get(k, required, &raw, "a task ID") {
		return 0
	}
	n, ok := idOf(raw)
	if !ok {
		f.in.bad(f.at+"/"+k, "must be a task ID, like 42 or @42")
	}
	return n
}

// ids reads a list of distinct task IDs.
func (f fields) ids(k string, required bool) []model.ID {
	var raws []json.RawMessage
	if !f.get(k, required, &raws, "a list of task IDs") {
		return nil
	}
	ns := []model.ID{}
	for i, raw := range raws {
		n, ok := idOf(raw)
		switch {
		case !ok:
			f.in.bad(fmt.Sprintf("%s/%s/%d", f.at, k, i), "must be a task ID, like 42 or @42")
		case slices.Contains(ns, n):
			f.in.bad(fmt.Sprintf("%s/%s/%d", f.at, k, i), "names the same task as an earlier one")
		default:
			ns = append(ns, n)
		}
	}
	return ns
}

// folderRef is a folder as an input names it: by path, or by ID.
type folderRef struct {
	path model.Folder
	id   model.ID // when not 0, the folder is the one with this ID
}

// folderRef reads a folder given by path, or by ID with or without its `@`.
func (f fields) folderRef(k string, required bool, def folderRef) folderRef {
	var raw json.RawMessage
	if !f.get(k, required, &raw, "a folder path or ID") {
		return def
	}
	var s string
	if model.Decode(raw, &s) == nil && strings.HasPrefix(s, "/") {
		if folder, ok := model.ParseFolder(s); ok {
			return folderRef{path: folder}
		}
	} else if id, ok := idOf(raw); ok {
		return folderRef{id: id}
	}
	f.in.bad(f.at+"/"+k, "must be a folder path like /proj/travel, or a folder's ID like 41 or @41")
	return def
}

// folder reads a folder path: for a folder that may not exist yet, which
// has no ID to give.
func (f fields) folder(k string, required bool, def model.Folder) model.Folder {
	var s string
	if !f.get(k, required, &s, "a folder path") {
		return def
	}
	folder, ok := model.ParseFolder(s)
	if !ok {
		f.in.bad(f.at+"/"+k, "must be a folder path like /proj/travel")
		return def
	}
	return folder
}

func (f fields) boolean(k string, def bool) bool {
	v := def
	f.get(k, false, &v, "true or false")
	return v
}

func (f fields) text(k string) (string, bool) {
	var s string
	ok := f.get(k, false, &s, "a string")
	return s, ok
}

// title reads and trims a title.
func (f fields) title(k string, required bool) (string, bool) {
	var s string
	if !f.get(k, required, &s, "a string") {
		return "", false
	}
	title, reason := model.CleanTitle(s)
	if reason != "" {
		f.in.bad(f.at+"/"+k, "%s", reason)
		return "", false
	}
	return title, true
}

// optTitle reads a title, trimmed, or null. set is false when the field is
// absent, or is wrong, which it records.
func (f fields) optTitle(k string) (title *string, set bool) {
	raw, ok := f.m[k]
	f.used[k] = true
	if !ok {
		return nil, false
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return nil, true
	}
	t, ok := f.title(k, false)
	if !ok {
		return nil, false
	}
	return &t, true
}

// priority reads an integer or null. set is false when the field is absent.
func (f fields) priority(k string) (p *int64, set bool) {
	raw, ok := f.m[k]
	f.used[k] = true
	if !ok {
		return nil, false
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return nil, true
	}
	var n int64
	if err := model.Decode(raw, &n); err != nil || n > model.MaxInt || n < -model.MaxInt {
		f.in.bad(f.at+"/"+k, "must be an integer or null")
		return nil, false
	}
	return &n, true
}

// tags reads a list of distinct tags.
func (f fields) tags(k string) ([]string, bool) {
	var ts []string
	if !f.get(k, false, &ts, "a list of tags") {
		return nil, false
	}
	seen := map[string]bool{}
	for i, t := range ts {
		switch {
		case !model.ValidTag(t):
			f.in.bad(fmt.Sprintf("%s/%s/%d", f.at, k, i), "must be a tag: lowercase letters, digits, and hyphens")
		case seen[t]:
			f.in.bad(fmt.Sprintf("%s/%s/%d", f.at, k, i), "is repeated")
		}
		seen[t] = true
	}
	return ts, true
}

// object reads a JSON object.
func (f fields) object(k string) (map[string]any, bool) {
	var o map[string]any
	if !f.get(k, false, &o, "a JSON object") {
		return nil, false
	}
	return o, true
}

// strs reads a list of distinct strings.
func (f fields) strs(k string) ([]string, bool) {
	var ss []string
	if !f.get(k, false, &ss, "a list of strings") {
		return nil, false
	}
	seen := map[string]bool{}
	for i, s := range ss {
		if seen[s] {
			f.in.bad(fmt.Sprintf("%s/%s/%d", f.at, k, i), "is repeated")
		}
		seen[s] = true
	}
	return ss, true
}

// oneOf reads a list of distinct values, each one of allowed.
func (f fields) oneOf(k string, allowed []string) ([]string, bool) {
	ss, ok := f.strs(k)
	for i, s := range ss {
		if !slices.Contains(allowed, s) {
			f.in.bad(fmt.Sprintf("%s/%s/%d", f.at, k, i), "must be one of %s", strings.Join(allowed, ", "))
		}
	}
	return ss, ok
}
