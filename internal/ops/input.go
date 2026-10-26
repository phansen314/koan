package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
)

// input reads a command's input field by field, collecting every fault in
// its shape at its JSON Pointer, so that invalid-input names them all. The
// error's message is the first fault's.
type input struct {
	in       cli.Input
	problems []cli.Problem
	message  string
	// ids are the IDs the faults name: what a reference of the wrong
	// kind names, or the subjects a field's kind doesn't take.
	ids []int64
	// prefix is the pointer to the object read, "" for the whole input;
	// a part's faults go to up, the input holding it.
	prefix string
	up     *input
	// batch is whether labels are taken, where an edge names a task.
	batch bool
}

// at is the JSON Pointer to a field of the object read.
func (r *input) at(field string) string { return r.prefix + cli.Pointer(field) }

// fault records a fault at a pointer.
func (r *input) fault(pointer, format string, args ...any) {
	r.faultIDs(pointer, nil, format, args...)
}

// faultIDs records a fault at a pointer that names IDs.
func (r *input) faultIDs(pointer string, ids []int64, format string, args ...any) {
	if r.up != nil {
		r.up.faultIDs(pointer, ids, format, args...)
		return
	}
	for _, id := range ids {
		if !slices.Contains(r.ids, id) {
			r.ids = append(r.ids, id)
		}
	}
	reason := fmt.Sprintf(format, args...)
	if r.message == "" {
		r.message = reason
	}
	r.problems = append(r.problems, cli.Problem{Field: pointer, Reason: reason})
}

// err is invalid-input with every fault found, or nil.
func (r *input) err() *cli.Error {
	if len(r.problems) == 0 {
		return nil
	}
	e := cli.InvalidInput(r.message, r.problems...)
	e.IDs = r.ids
	return e
}

// value is a field's value; ok is false when it isn't given.
func (r *input) value(field string) (any, bool) {
	v, ok := r.in[field]
	return v, ok
}

// part is an object in the input, at pointer, read as an input of its
// own whose faults are this one's. A field it doesn't take, one of
// fields, is a fault at that field. ok is false when it isn't an object.
func (r *input) part(pointer string, v any, fields ...string) (*input, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		r.fault(pointer, "%s must be an object", pointer)
		return nil, false
	}
	p := &input{in: m, prefix: pointer, up: r, batch: r.batch}
	var unknown []string
	for k := range m {
		if !slices.Contains(fields, k) {
			unknown = append(unknown, k)
		}
	}
	slices.Sort(unknown)
	for _, k := range unknown {
		p.fault(p.at(k), "unknown field %s", k)
	}
	return p, true
}

// label is a batch label field, or "" when it isn't given.
func (r *input) label(field string) string {
	v, ok := r.value(field)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok || !core.ValidLabel(s) {
		r.fault(r.at(field), "%v is not a label: $ then 1 to 64 letters, digits and hyphens, not starting or ending with a hyphen", printed(v))
		return ""
	}
	return s
}

// required is a field's value, or a fault when it isn't given.
func (r *input) required(field string) (any, bool) {
	v, ok := r.in[field]
	if !ok {
		r.fault(r.at(field), "%s is required", field)
	}
	return v, ok
}

// text is a string field, UTF-8, or def when it isn't given.
func (r *input) text(field, def string) string {
	v, ok := r.value(field)
	if !ok {
		return def
	}
	s, ok := v.(string)
	switch {
	case !ok:
		r.fault(r.at(field), "%s must be a string", field)
	case !utf8.ValidString(s):
		r.fault(r.at(field), "%s is not UTF-8", field)
	}
	return s
}

// title is a folder's title: a title by the task rule, as stored, or null.
func (r *input) title(field string) *string {
	v, ok := r.value(field)
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		r.fault(r.at(field), "%s must be a string or null", field)
		return nil
	}
	t, err := core.CheckTitle(s)
	if err != nil {
		r.fault(r.at(field), "%s", reason(err))
		return nil
	}
	return &t
}

// object is a JSON object field, as stored, or {} when it isn't given.
func (r *input) object(field string) json.RawMessage {
	v, ok := r.value(field)
	if !ok {
		return json.RawMessage("{}")
	}
	m, ok := v.(map[string]any)
	if !ok {
		r.fault(r.at(field), "%s must be a JSON object", field)
		return nil
	}
	b, err := marshal(m)
	if err != nil {
		r.fault(r.at(field), "%s: %v", field, err)
	}
	return b
}

// boolean is a boolean field, or def when it isn't given.
func (r *input) boolean(field string, def bool) bool {
	v, ok := r.value(field)
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		r.fault(r.at(field), "%s must be true or false", field)
	}
	return b
}

// limit is a limit field: an integer of 1 or more, or null for none; def
// when it isn't given.
func (r *input) limit(field string, def int64) *int64 {
	v, ok := r.value(field)
	if !ok {
		return &def
	}
	if v == nil {
		return nil
	}
	n, isNum := v.(json.Number)
	var i int64
	var err error
	if isNum {
		i, err = core.ParseInt(string(n))
	}
	if !isNum || err != nil || i < 1 {
		r.fault(r.at(field), "%s must be an integer of 1 or more, or null", field)
		return nil
	}
	return &i
}

// ref is a reference field, parsed and checked where want is taken: a
// string, or an ID as a JSON integer. def is used when it isn't given, or
// "" when it is required.
func (r *input) ref(field string, want core.Want, def string) core.Ref {
	var v any = def
	if def == "" {
		var ok bool
		if v, ok = r.required(field); !ok {
			return core.Ref{}
		}
	} else if got, ok := r.value(field); ok {
		v = got
	}
	return r.refAt(r.at(field), field, v, want)
}

// refs is a list of references, each checked as ref checks one, or none
// when it isn't given.
func (r *input) refs(field string, want core.Want) []core.Ref {
	list, ok := r.list(field)
	if !ok {
		return nil
	}
	out := []core.Ref{}
	for i, v := range list {
		out = append(out, r.refAt(fmt.Sprintf("%s/%d", r.at(field), i), field, v, want))
	}
	return out
}

// refAt checks one reference value at a pointer.
func (r *input) refAt(at, field string, v any, want core.Want) core.Ref {
	var s string
	switch v := v.(type) {
	case string:
		s = v
	case json.Number:
		if _, ok := core.ParseID(string(v)); !ok {
			r.fault(at, "%s is not an ID", v)
			return core.Ref{}
		}
		s = string(v)
	default:
		r.fault(at, "%s must be a reference: a string, or an ID", field)
		return core.Ref{}
	}
	ref, err := core.ParseRef(s)
	if err == nil {
		err = core.CheckShape(ref, want, r.batch)
	}
	if err != nil {
		r.fault(at, "%s", reason(err))
		return core.Ref{}
	}
	return ref
}

// sameCase refuses references that name paths differing only in case,
// pointing at both, in input order; at is each one's pointer. exempt,
// when given, lets a pair through.
func (r *input) sameCase(refs []core.Ref, at []string, exempt ...func(a, b core.Ref) bool) {
	for _, p := range core.CasePairs(refs) {
		a, b := refs[p[0]], refs[p[1]]
		if len(exempt) > 0 && exempt[0](a, b) {
			continue
		}
		reason := fmt.Sprintf("%q and %q differ only in case", a.Raw, b.Raw)
		r.fault(at[p[0]], "%s", reason)
		r.fault(at[p[1]], "%s", reason)
	}
}

// absPath is a path on disk, absolute, cleaned; "" when it isn't given,
// or a fault too when it is required.
func (r *input) absPath(field string, required bool) string {
	v, ok := r.value(field)
	if !ok {
		if required {
			r.fault(r.at(field), "%s is required", field)
		}
		return ""
	}
	s, ok := v.(string)
	switch {
	case !ok:
		r.fault(r.at(field), "%s must be a string", field)
	case !utf8.ValidString(s):
		r.fault(r.at(field), "%s is not UTF-8", field)
	case !filepath.IsAbs(s):
		r.fault(r.at(field), "%s must be an absolute path: %q isn't", field, s)
	default:
		return filepath.Clean(s)
	}
	return ""
}

// taskTitle is a task's title, required: trimmed, then checked.
func (r *input) taskTitle(field string) string {
	v, ok := r.required(field)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		r.fault(r.at(field), "%s must be a string", field)
		return ""
	}
	t, err := core.CheckTitle(s)
	if err != nil {
		r.fault(r.at(field), "%s", reason(err))
	}
	return t
}

// priority is a priority field: an integer within ±(2^53 − 1), or null;
// null when it isn't given.
func (r *input) priority(field string) *int64 {
	v, ok := r.value(field)
	if !ok || v == nil {
		return nil
	}
	n, ok := v.(json.Number)
	if !ok {
		r.fault(r.at(field), "%s must be an integer or null", field)
		return nil
	}
	i, err := core.ParseInt(string(n))
	if err != nil {
		r.fault(r.at(field), "%s", reason(err))
		return nil
	}
	return &i
}

// list is a list field's items; ok is false when it isn't given, or
// isn't a list.
func (r *input) list(field string) ([]any, bool) {
	v, ok := r.value(field)
	if !ok {
		return nil, false
	}
	list, ok := v.([]any)
	if !ok {
		r.fault(r.at(field), "%s must be a list", field)
	}
	return list, ok
}

// tags is a list of tags, each matching the tag pattern, one for each given; empty
// when it isn't given.
func (r *input) tags(field string) []string {
	return r.strings(field, func(s string) bool { return core.ValidTag(s) }, "is not a tag: 1 to 64 lowercase letters, digits and hyphens, not starting or ending with a hyphen")
}

// choices is a list of at least one of allowed, a value given twice once; def when it
// isn't given.
func (r *input) choices(field string, allowed, def []string) []string {
	if _, ok := r.value(field); !ok {
		return def
	}
	out := r.strings(field, func(s string) bool { return slices.Contains(allowed, s) }, fmt.Sprintf("is not one of %v", allowed))
	if list, ok := r.in[field].([]any); ok && len(list) == 0 {
		r.fault(r.at(field), "%s must name at least one of %v", field, allowed)
	}
	return out
}

// strings is a list of strings that pass valid, a value given twice once,
// since a set takes duplicates; bad ends the fault for one that doesn't
// pass.
func (r *input) strings(field string, valid func(string) bool, bad string) []string {
	list, ok := r.list(field)
	out := []string{}
	if !ok {
		return out
	}
	for i, item := range list {
		at := fmt.Sprintf("%s/%d", r.at(field), i)
		s, ok := item.(string)
		switch {
		case !ok || !valid(s):
			r.fault(at, "%v %s", printed(item), bad)
		case !slices.Contains(out, s):
			out = append(out, s)
		}
	}
	return out
}

// printed is a value as a fault names it: a string quoted, else as JSON.
func printed(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	b, _ := marshal(v)
	return string(b)
}

// nullBool is a field that is true, false or null; null when it isn't
// given.
func (r *input) nullBool(field string) *bool {
	v, ok := r.value(field)
	if !ok || v == nil {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		r.fault(r.at(field), "%s must be true, false or null", field)
		return nil
	}
	return &b
}

// path is a folder path to make: a path from the root, not the root.
func (r *input) path(field string) core.Ref {
	v, ok := r.required(field)
	if !ok {
		return core.Ref{}
	}
	s, ok := v.(string)
	if !ok {
		r.fault(r.at(field), "%s must be a folder path", field)
		return core.Ref{}
	}
	ref, err := core.ParseRef(s)
	if err == nil && (ref.Kind != core.TargetPath || ref.IsRoot() || ref.End != core.EndNone || ref.HasMaterial) {
		err = fmt.Errorf("%q is not a folder path from the root, such as /work/api", s)
	}
	if err != nil {
		r.fault(r.at(field), "%s", reason(err))
	}
	return ref
}

// fields is a --fields list: names from the record's fields, a name given
// twice once.
// nil means it isn't given: every field.
func (r *input) fields(field string, names []string) []string {
	v, ok := r.value(field)
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		r.fault(r.at(field), "%s must be a list of field names", field)
		return nil
	}
	out := []string{}
	for i, item := range list {
		at := fmt.Sprintf("%s/%d", r.at(field), i)
		s, ok := item.(string)
		switch {
		case !ok || !slices.Contains(names, s):
			r.fault(at, "%v is not a field: the fields are %v", item, names)
		case !slices.Contains(out, s):
			out = append(out, s)
		}
	}
	return out
}

// reason is an error's message without its kind.
func reason(err error) string {
	if e, ok := err.(*core.Error); ok {
		return e.Message
	}
	return err.Error()
}

// marshal writes a value as JSON as the envelope does: without escaping
// <, > and &.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
