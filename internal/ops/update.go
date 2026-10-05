package ops

import (
	"slices"
	"strconv"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// UpdateInput is update's input. A nil field is left unchanged.
type UpdateInput struct {
	ID    model.ID
	Title *model.Title
	// Priority is set when the input names priority; Priority.Value nil
	// clears it.
	Priority *PriorityChange
	Tags     *TagsChange
	Extra    *ExtraChange
}

// PriorityChange sets the priority; a nil Value clears it.
type PriorityChange struct {
	Value *int64
}

// TagsChange is one of two forms: ReplaceAll set (the whole new tag set), or
// Add and Remove (either may be empty, not both).
type TagsChange struct {
	ReplaceAll []model.Tag
	Replace    bool
	Add        []model.Tag
	Remove     []model.Tag
}

// ExtraChange is one of two forms: ReplaceAll set, or Merge and Remove
// (either may be empty, not both).
type ExtraChange struct {
	ReplaceAll *jsonio.Object
	Merge      *jsonio.Object
	Remove     []string
}

// The fields update can change, in the order changed reports them.
var updateFields = []string{"title", "priority", "tags", "extra"}

func decodeUpdate(f *model.Fields, p *model.Problems) any {
	in := UpdateInput{ID: requiredID(f, p, "id")}
	named := false
	for _, key := range updateFields {
		named = named || f.Has(key)
	}
	if !named {
		p.Add("", "give at least one field to change: title, priority, tags, or extra")
	}
	if v, ok := f.Optional("title"); ok {
		if s, ok := p.String(v, "/title"); ok {
			if t, ok := p.TitleInput(s, "/title"); ok {
				in.Title = &t
			}
		}
	}
	if v, ok := f.Optional("priority"); ok {
		if pr, ok := p.Priority(v, "/priority"); ok {
			in.Priority = &PriorityChange{Value: pr}
		}
	}
	if v, ok := f.Optional("tags"); ok {
		in.Tags = decodeTagsChange(v, p)
	}
	if v, ok := f.Optional("extra"); ok {
		in.Extra = decodeExtraChange(v, p)
	}
	return in
}

// decodeTagsChange checks update's tags: {replace_all} or {add, remove}. The
// input evidently means the replace_all form when it has that key, and the
// add/remove form otherwise; only that form's problems are reported.
func decodeTagsChange(v any, p *model.Problems) *TagsChange {
	f, ok := p.Object(v, "/tags")
	if !ok {
		return nil
	}
	var c TagsChange
	if f.Has("replace_all") {
		rv, _ := f.Required("replace_all")
		c.ReplaceAll, _ = p.Tags(rv, "/tags/replace_all")
		c.Replace = true
		conflicts(f, p, "replace_all", "add", "remove")
		f.Done()
		return &c
	}
	if !f.Has("add") && !f.Has("remove") {
		p.Add("/tags", "give replace_all, or add and/or remove")
		f.Done()
		return nil
	}
	var addOK, removeOK bool
	c.Add, addOK = tagList(f, p, "add")
	c.Remove, removeOK = tagList(f, p, "remove")
	f.Done()
	if addOK && removeOK {
		for i, t := range c.Remove {
			for _, a := range c.Add {
				if t == a {
					p.AddAdditional(jsonio.Pointer("/tags/remove", strconv.Itoa(i)), "is also in add")
				}
			}
		}
	}
	return &c
}

// conflicts reports each of keys present alongside form, which excludes them.
func conflicts(f *model.Fields, p *model.Problems, form string, keys ...string) {
	for _, k := range keys {
		if _, ok := f.Optional(k); ok {
			p.Add(f.Ptr(k), "cannot be combined with "+form)
		}
	}
}

// tagList returns the optional field key of a tags change, a non-empty set
// of tags, and whether it is valid.
func tagList(f *model.Fields, p *model.Problems, key string) ([]model.Tag, bool) {
	v, ok := f.Optional(key)
	if !ok {
		return []model.Tag{}, true
	}
	return nonEmptySet(p, v, f.Ptr(key), "tag", (*model.Problems).Tags)
}

// decodeExtraChange checks update's extra: {replace_all} or {merge, remove},
// choosing the form as decodeTagsChange does.
func decodeExtraChange(v any, p *model.Problems) *ExtraChange {
	f, ok := p.Object(v, "/extra")
	if !ok {
		return nil
	}
	var c ExtraChange
	if f.Has("replace_all") {
		rv, _ := f.Required("replace_all")
		if _, ok := p.Object(rv, "/extra/replace_all"); ok {
			c.ReplaceAll = rv.(*jsonio.Object)
		}
		conflicts(f, p, "replace_all", "merge", "remove")
		f.Done()
		return &c
	}
	if !f.Has("merge") && !f.Has("remove") {
		p.Add("/extra", "give replace_all, or merge and/or remove")
		f.Done()
		return nil
	}
	c.Merge, c.Remove = &jsonio.Object{}, []string{}
	mergeOK, removeOK := true, true
	if mv, ok := f.Optional("merge"); ok {
		if _, mergeOK = p.Object(mv, "/extra/merge"); mergeOK {
			c.Merge = mv.(*jsonio.Object)
			if c.Merge.Len() == 0 {
				p.Add("/extra/merge", "must set at least one key")
				mergeOK = false
			}
		}
	}
	if rv, ok := f.Optional("remove"); ok {
		c.Remove, removeOK = extraKeys(rv, p)
	}
	f.Done()
	if mergeOK && removeOK {
		for i, k := range c.Remove {
			if _, dup := c.Merge.Get(k); dup {
				p.AddAdditional(jsonio.Pointer("/extra/remove", strconv.Itoa(i)), "is also in merge")
			}
		}
	}
	return &c
}

// extraKeys checks extra.remove, a non-empty set of keys, and returns it and
// whether it is valid.
func extraKeys(v any, p *model.Problems) ([]string, bool) {
	return nonEmptySet(p, v, "/extra/remove", "key", keySet)
}

// keySet checks v, at ptr, as a set of strings.
func keySet(p *model.Problems, v any, ptr string) ([]string, bool) {
	a, ok := p.Array(v, ptr)
	if !ok {
		return nil, false
	}
	keys := make([]string, len(a))
	valid := make([]bool, len(a))
	allValid := true
	for i, item := range a {
		keys[i], valid[i] = p.String(item, jsonio.Pointer(ptr, strconv.Itoa(i)))
		allValid = allValid && valid[i]
	}
	return keys, model.Unique(p, keys, valid, ptr) && allValid
}

// UpdateOutput is update's result (update-output): the task after the
// operation, plus the fields whose value changed, in updateFields' order.
type UpdateOutput struct {
	model.Task
	Changed []string `json:"changed"`
}

// runUpdate finds the one task with id, under the write lock, and applies
// each named field. A field whose new value equals its old one as a JSON
// value keeps its old form, so the file changes only where a value did; if
// none did, the file is not rewritten. Otherwise it is replaced in one
// rename, every other field and the .md as they were.
func runUpdate(env Env, in UpdateInput, w *errs.Collector) (any, *errs.Error) {
	out := UpdateOutput{Changed: []string{}}
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		ld, e := findOne(tx, in.ID)
		if e != nil {
			return e
		}
		t := &ld.Task
		if in.Title != nil && *in.Title != t.Title {
			t.Title = *in.Title
			out.Changed = append(out.Changed, "title")
		}
		if in.Priority != nil && !equalPriority(in.Priority.Value, t.Priority) {
			t.Priority = in.Priority.Value
			out.Changed = append(out.Changed, "priority")
		}
		if in.Tags != nil {
			if tags := in.Tags.apply(t.Tags); !equalTags(tags, t.Tags) {
				t.Tags = tags
				out.Changed = append(out.Changed, "tags")
			}
		}
		if in.Extra != nil {
			if extra := in.Extra.apply(t.Extra); !jsonio.Equal(extra, t.Extra) {
				t.Extra = extra
				out.Changed = append(out.Changed, "extra")
			}
		}
		if len(out.Changed) > 0 {
			if e := replaceTask(tx, ld.Loc.Rel(), t, model.TimestampOf(env.Clock())); e != nil {
				return e
			}
		}
		out.Task = tx.Task(ld)
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

func equalPriority(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// equalTags compares two tag sets.
func equalTags(a, b []model.Tag) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// apply returns the tag set old becomes: ReplaceAll, or old plus Add minus
// Remove. Order is left to TaskFile.Normalize.
func (c *TagsChange) apply(old []model.Tag) []model.Tag {
	if c.Replace {
		return slices.Clone(c.ReplaceAll)
	}
	var tags []model.Tag
	for _, t := range slices.Concat(old, c.Add) {
		if !slices.Contains(c.Remove, t) && !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	return tags
}

// apply returns the map old becomes: ReplaceAll, or old with each Merge key
// set — an existing key keeps its position, a new one is appended in the
// order given — and each Remove key deleted. old is not modified.
func (c *ExtraChange) apply(old *jsonio.Object) *jsonio.Object {
	if c.ReplaceAll != nil {
		return c.ReplaceAll
	}
	extra := &jsonio.Object{Members: old.Members} // Set and Delete never write to Members
	for _, m := range c.Merge.Members {
		extra.Set(m.Key, m.Value)
	}
	for _, k := range c.Remove {
		extra.Delete(k)
	}
	return extra
}
