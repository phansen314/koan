package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
)

// Problems collects every problem an adapter finds, each at the JSON Pointer
// of the field it concerns. An adapter checks each field once, so no field is
// reported twice for one rule.
type Problems struct {
	list       []errs.Problem
	additional []bool // per problem: from a rule no schema expresses
	bad        map[string]bool
}

// Add records a problem at field.
func (p *Problems) Add(field, reason string) {
	p.add(field, reason, false)
}

// AddAdditional records a problem found by a rule the published schema cannot
// express (an operation's Additional validation, or a file-level rule beyond
// its schema). Such problems are reported like any other; the schema-agreement
// tests leave them out (see SchemaList).
func (p *Problems) AddAdditional(field, reason string) {
	p.add(field, reason, true)
}

func (p *Problems) add(field, reason string, additional bool) {
	p.list = append(p.list, errs.Problem{Field: field, Reason: reason})
	p.additional = append(p.additional, additional)
	if p.bad == nil {
		p.bad = map[string]bool{}
	}
	p.bad[field] = true
}

// OK reports whether no problem was found.
func (p *Problems) OK() bool { return len(p.list) == 0 }

// Failed reports whether a problem was recorded at field, so later checks
// that depend on it are skipped.
func (p *Problems) Failed(field string) bool { return p.bad[field] }

// List returns the problems in the order found; errs.InvalidInput sorts them.
func (p *Problems) List() []errs.Problem { return p.list }

// SchemaList returns the problems the published schema also finds: every
// problem not added with AddAdditional.
func (p *Problems) SchemaList() []errs.Problem {
	var out []errs.Problem
	for i, pr := range p.list {
		if !p.additional[i] {
			out = append(out, pr)
		}
	}
	return out
}

// Reasons shared by every adapter, so one mistake reads the same everywhere.
const (
	reasonObject   = "expected a JSON object"
	reasonString   = "expected a string"
	reasonBool     = "expected a boolean"
	reasonArray    = "expected an array"
	reasonInteger  = "expected an integer"
	reasonRequired = "required"
	reasonUnknown  = "unknown field"
	reasonLiteral  = "must be an integer written without a fraction or exponent (2, not 2.0 or 2e0)"
)

// Fields walks one JSON object's members for an adapter. Missing and unknown
// fields are reported at the field's own pointer.
type Fields struct {
	obj  *jsonio.Object
	ptr  string
	p    *Problems
	used map[string]bool
}

// Object checks that v, at ptr, is an object, and returns a walker over it.
func (p *Problems) Object(v any, ptr string) (*Fields, bool) {
	o, ok := v.(*jsonio.Object)
	if !ok {
		p.Add(ptr, reasonObject)
		return nil, false
	}
	return &Fields{obj: o, ptr: ptr, p: p, used: map[string]bool{}}, true
}

// Ptr returns the pointer of the member key.
func (f *Fields) Ptr(key string) string { return jsonio.Pointer(f.ptr, key) }

// Required returns the member key, reporting it when missing.
func (f *Fields) Required(key string) (any, bool) {
	v, ok := f.Optional(key)
	if !ok {
		f.p.Add(f.Ptr(key), reasonRequired)
	}
	return v, ok
}

// Optional returns the member key, if present.
func (f *Fields) Optional(key string) (any, bool) {
	f.used[key] = true
	return f.obj.Get(key)
}

// Has reports whether the member key is present, without using it.
func (f *Fields) Has(key string) bool {
	_, ok := f.obj.Get(key)
	return ok
}

// Done reports every member not asked for as unknown. Call it after asking
// for every allowed field.
func (f *Fields) Done() {
	for _, m := range f.obj.Members {
		if !f.used[m.Key] {
			f.used[m.Key] = true // a repeated unknown key is reported once
			f.p.Add(f.Ptr(m.Key), reasonUnknown)
		}
	}
}

// String checks that v, at ptr, is a string.
func (p *Problems) String(v any, ptr string) (string, bool) {
	s, ok := v.(string)
	if !ok {
		p.Add(ptr, reasonString)
	}
	return s, ok
}

// Bool checks that v, at ptr, is a boolean.
func (p *Problems) Bool(v any, ptr string) (bool, bool) {
	b, ok := v.(bool)
	if !ok {
		p.Add(ptr, reasonBool)
	}
	return b, ok
}

// Array checks that v, at ptr, is an array.
func (p *Problems) Array(v any, ptr string) ([]any, bool) {
	a, ok := v.([]any)
	if !ok {
		p.Add(ptr, reasonArray)
	}
	return a, ok
}

var integerLiteral = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// Int checks that v, at ptr, is an integer written as an integer literal,
// within [lo, hi]. The literal rule is the spec's check of integer literals
// outside extra: every number outside extra, in every schema, is an integer,
// so a number anywhere else is rejected by its field's type.
func (p *Problems) Int(v any, ptr string, lo, hi int64) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		p.Add(ptr, reasonInteger)
		return 0, false
	}
	if !integerLiteral.MatchString(string(n)) {
		p.Add(ptr, reasonLiteral)
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || i < lo || i > hi {
		p.Add(ptr, fmt.Sprintf("must be between %d and %d", lo, hi))
		return 0, false
	}
	return i, true
}

// Unique reports each item of items equal to an earlier one, at the later
// item's pointer. Items whose own check failed (valid[i] false) take no part,
// so uniqueness is checked among the rest whatever else is wrong with the
// set. It returns whether all compared items were distinct.
func Unique[T comparable](p *Problems, items []T, valid []bool, ptr string) bool {
	first := map[T]int{}
	ok := true
	for i, it := range items {
		if !valid[i] {
			continue
		}
		if j, dup := first[it]; dup {
			p.Add(jsonio.Pointer(ptr, strconv.Itoa(i)), fmt.Sprintf("duplicate of item %d", j))
			ok = false
			continue
		}
		first[it] = i
	}
	return ok
}
