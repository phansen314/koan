package ops

import (
	"bytes"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// Narrowing is the input frontier and list share for narrowing the tasks
// they return (operations.md, Narrowing tasks), defaults applied.
type Narrowing struct {
	TagsAny []model.Tag // nil: no filter
	TagsAll []model.Tag // nil: no filter
	Limit   *int64      // nil: no limit
	Fields  []string    // nil: whole views
}

// limitMax is limit's schema maximum: the integers every JSON reader holds
// exactly.
const limitMax = 1<<53 - 1

// readinessValues are the names readiness may take (task-view's readiness).
var readinessValues = []string{string(model.Ready), string(model.Blocked), string(model.Complete)}

func decodeNarrowing(f *model.Fields, p *model.Problems) Narrowing {
	var n Narrowing
	n.TagsAny = optionalTagSet(f, p, "tags_any")
	n.TagsAll = optionalTagSet(f, p, "tags_all")
	if v, ok := f.Optional("limit"); ok {
		if l, ok := p.Int(v, f.Ptr("limit"), 0, limitMax); ok {
			n.Limit = &l
		}
	}
	if v, ok := f.Optional("fields"); ok {
		n.Fields, _ = nonEmptySet(p, v, f.Ptr("fields"), "field", nameSet(model.ViewFields))
	}
	return n
}

// optionalTagSet returns the field key as a non-empty set of tags, or nil
// when it is absent.
func optionalTagSet(f *model.Fields, p *model.Problems, key string) []model.Tag {
	v, ok := f.Optional(key)
	if !ok {
		return nil
	}
	tags, _ := nonEmptySet(p, v, f.Ptr(key), "tag", (*model.Problems).Tags)
	return tags
}

// nameSet checks a value as a set of names, each one of allowed: an array
// of distinct strings, as an enum's items with uniqueItems.
func nameSet(allowed []string) func(*model.Problems, any, string) ([]string, bool) {
	reason := "must be one of " + strings.Join(allowed, ", ")
	return func(p *model.Problems, v any, ptr string) ([]string, bool) {
		a, ok := p.Array(v, ptr)
		if !ok {
			return nil, false
		}
		names := make([]string, 0, len(a))
		valid := make([]bool, 0, len(a))
		for i, item := range a {
			at := jsonio.Pointer(ptr, strconv.Itoa(i))
			s, ok1 := p.String(item, at)
			if ok1 && !slices.Contains(allowed, s) {
				p.Add(at, reason)
				ok1 = false
			}
			ok = ok && ok1
			names = append(names, s)
			valid = append(valid, ok1)
		}
		return names, model.Unique(p, names, valid, ptr) && ok
	}
}

// Tasks is the tasks frontier and list return: whole task views, or with
// Fields, each projected to them (task-projection).
type Tasks struct {
	Views  []model.TaskView
	Fields []string // nil: whole views
}

// MarshalJSON encodes the tasks as ftask's output does (jsonio): views
// whole, or projected.
func (t Tasks) MarshalJSON() ([]byte, error) {
	var v any = t.Views
	if t.Fields != nil {
		projected := make([]any, len(t.Views))
		for i, view := range t.Views {
			projected[i] = view.Project(t.Fields)
		}
		v = projected
	}
	b, err := jsonio.MarshalLine(v)
	return bytes.TrimSuffix(b, []byte("\n")), err
}

// narrow applies n to views, already in scope and in the operation's order
// (operations.md, Narrowing tasks): the tag filters, then the count, the
// limit, and the fields. It returns the tasks, how many passed the filters,
// and whether the limit cut any. Warnings are the caller's, all recorded
// before.
func narrow(views []model.TaskView, n Narrowing) (Tasks, int, bool) {
	kept := slices.DeleteFunc(append([]model.TaskView{}, views...), func(v model.TaskView) bool {
		return (n.TagsAny != nil && !slices.ContainsFunc(n.TagsAny, func(t model.Tag) bool { return slices.Contains(v.Tags, t) })) ||
			(n.TagsAll != nil && slices.ContainsFunc(n.TagsAll, func(t model.Tag) bool { return !slices.Contains(v.Tags, t) }))
	})
	total := len(kept)
	if n.Limit != nil && int64(total) > *n.Limit {
		kept = kept[:*n.Limit]
	}
	return Tasks{Views: kept, Fields: n.Fields}, total, len(kept) < total
}
