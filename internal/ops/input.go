package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
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
}

// fault records a fault at a pointer.
func (r *input) fault(pointer, format string, args ...any) {
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
	return cli.InvalidInput(r.message, r.problems...)
}

// value is a field's value; ok is false when it isn't given.
func (r *input) value(field string) (any, bool) {
	v, ok := r.in[field]
	return v, ok
}

// required is a field's value, or a fault when it isn't given.
func (r *input) required(field string) (any, bool) {
	v, ok := r.in[field]
	if !ok {
		r.fault(cli.Pointer(field), "%s is required", field)
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
		r.fault(cli.Pointer(field), "%s must be a string", field)
	case !utf8.ValidString(s):
		r.fault(cli.Pointer(field), "%s is not UTF-8", field)
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
		r.fault(cli.Pointer(field), "%s must be a string or null", field)
		return nil
	}
	t, err := core.CheckTitle(s)
	if err != nil {
		r.fault(cli.Pointer(field), "%s", reason(err))
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
		r.fault(cli.Pointer(field), "%s must be a JSON object", field)
		return nil
	}
	b, err := marshal(m)
	if err != nil {
		r.fault(cli.Pointer(field), "%s: %v", field, err)
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
		r.fault(cli.Pointer(field), "%s must be true or false", field)
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
		r.fault(cli.Pointer(field), "%s must be an integer of 1 or more, or null", field)
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
	var s string
	switch v := v.(type) {
	case string:
		s = v
	case json.Number:
		if _, ok := core.ParseID(string(v)); !ok {
			r.fault(cli.Pointer(field), "%s is not an ID", v)
			return core.Ref{}
		}
		s = string(v)
	default:
		r.fault(cli.Pointer(field), "%s must be a reference: a string, or an ID", field)
		return core.Ref{}
	}
	ref, err := core.ParseRef(s)
	if err == nil {
		err = core.CheckShape(ref, want, false)
	}
	if err != nil {
		r.fault(cli.Pointer(field), "%s", reason(err))
	}
	return ref
}

// path is a folder path to make: a path from the root, not the root.
func (r *input) path(field string) core.Ref {
	v, ok := r.required(field)
	if !ok {
		return core.Ref{}
	}
	s, ok := v.(string)
	if !ok {
		r.fault(cli.Pointer(field), "%s must be a folder path", field)
		return core.Ref{}
	}
	ref, err := core.ParseRef(s)
	if err == nil && (ref.Kind != core.TargetPath || ref.IsRoot() || ref.End != core.EndNone || ref.HasMaterial) {
		err = fmt.Errorf("%q is not a folder path from the root, such as /work/api", s)
	}
	if err != nil {
		r.fault(cli.Pointer(field), "%s", reason(err))
	}
	return ref
}

// fields is a --fields list: names from the record's fields, none twice.
// nil means it isn't given: every field.
func (r *input) fields(field string, names []string) []string {
	v, ok := r.value(field)
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		r.fault(cli.Pointer(field), "%s must be a list of field names", field)
		return nil
	}
	out := []string{}
	for i, item := range list {
		at := fmt.Sprintf("%s/%d", cli.Pointer(field), i)
		s, ok := item.(string)
		switch {
		case !ok || !slices.Contains(names, s):
			r.fault(at, "%v is not a field: the fields are %v", item, names)
		case slices.Contains(out, s):
			r.fault(at, "%q given twice", s)
		default:
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
