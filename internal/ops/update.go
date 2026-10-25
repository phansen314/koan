package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/store"
)

// UpdateResult is update's result, in its schema's order.
type UpdateResult struct {
	Updated []int64 `json:"updated"`
}

// extraChange is what an update does to extra: replace all of it, or
// merge keys in and remove others. A nil map isn't given.
type extraChange struct {
	merge, replace map[string]any
	remove         []string
}

// extraChange reads extra_merge, extra_remove and extra_replace_all. The
// last can't go with the others, and a key can't be both merged and
// removed.
func (r *input) extraChange() extraChange {
	var x extraChange
	x.merge = r.objectMap("extra_merge")
	x.remove = r.strings("extra_remove", func(string) bool { return true }, "is not a string")
	x.replace = r.objectMap("extra_replace_all")
	_, removing := r.value("extra_remove")
	if x.replace != nil && (x.merge != nil || removing) {
		r.fault("", "extra_replace_all can't go with extra_merge or extra_remove")
	}
	r.both("extra_remove", "merged", func(k string) bool { _, ok := x.merge[k]; return ok })
	return x
}

// apply is extra after the change, as stored: its keys in byte order.
func (x extraChange) apply(extra json.RawMessage) (json.RawMessage, error) {
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(extra))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	if x.replace != nil {
		m = maps.Clone(x.replace)
	}
	maps.Copy(m, x.merge)
	for _, k := range x.remove {
		delete(m, k)
	}
	return marshal(m)
}

// objectMap is a JSON object field, or nil when it isn't given.
func (r *input) objectMap(field string) map[string]any {
	v, ok := r.value(field)
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		r.fault(r.at(field), "%s must be a JSON object", field)
		return nil
	}
	return m
}

// both refuses each entry of a list that removes what the same update
// adds, at that entry; one given twice is refused at its first.
func (r *input) both(field, verb string, added func(string) bool) {
	list, _ := r.in[field].([]any)
	for i, item := range list {
		if s, ok := item.(string); ok && added(s) && slices.Index(list, item) == i {
			r.fault(fmt.Sprintf("%s/%d", r.at(field), i), "%q is both %s and removed", s, verb)
		}
	}
}

// changes is whether an update gives anything to change: a field other
// than its subject.
func (r *input) changes(subject string) {
	for field := range r.in {
		if field != subject {
			return
		}
	}
	r.fault("", "nothing to change: give at least one field to change")
}

// tagFields are update's fields that only a task has, besides priority.
var tagFields = []string{"tags_add", "tags_remove", "tags_replace_all"}

// Update changes the fields of tasks and folders, mixed, as one change. A
// folder has a title and extra, and no priority or tags; its title can be
// cleared, a task's can't. A change that changes nothing leaves
// updated_at alone.
func Update() *cli.Command {
	return &cli.Command{
		Name:    "update",
		Subject: &cli.Option{Field: "refs", Kind: cli.List, Arg: "ref", Required: true, Complete: anyRef.complete()},
		Options: []cli.Option{
			{Field: "title", Kind: cli.String, Nullable: true},
			{Field: "priority", Kind: cli.Integer, Nullable: true},
			{Field: "tags_add", Kind: cli.List},
			{Field: "tags_remove", Kind: cli.List},
			{Field: "tags_replace_all", Kind: cli.List},
			{Field: "extra_merge", Kind: cli.JSON},
			{Field: "extra_remove", Kind: cli.Each},
			{Field: "extra_replace_all", Kind: cli.JSON},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			refs, at := r.subjectRefs("updated")
			r.changes("refs")
			_, setTitle := r.value("title")
			title := r.title("title")
			_, setPriority := r.value("priority")
			priority := r.priority("priority")
			add := r.tags("tags_add")
			remove := r.tags("tags_remove")
			replace := r.tags("tags_replace_all")
			_, replacing := r.value("tags_replace_all")
			_, adding := r.value("tags_add")
			_, removing := r.value("tags_remove")
			if replacing && (adding || removing) {
				r.fault("", "tags_replace_all can't go with tags_add or tags_remove")
			}
			r.both("tags_remove", "added", func(tag string) bool { return slices.Contains(add, tag) })
			extra := r.extraChange()
			r.sameCase(refs, at)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				subs, err := resolveSubjects(tx, refs, at, "updated")
				if err != nil {
					return nil, nil, err
				}
				// A field one of the subjects doesn't have.
				k := input{}
				if id, ok := firstOf(subs, false); ok && setTitle && title == nil {
					k.fault("/title", "@%d is a task, whose title can't be null", id)
				}
				if id, ok := firstOf(subs, true); ok {
					if setPriority {
						k.fault("/priority", "@%d is a folder, which has no priority: a folder's work is ordered through its gates, which are tasks, such as %s.start",
							id, tx.Path(id))
					}
					for _, field := range tagFields {
						if _, given := r.value(field); given {
							k.fault(cli.Pointer(field), "@%d is a folder, which has no tags", id)
						}
					}
				}
				if e := k.err(); e != nil {
					return nil, nil, store.Err(e)
				}
				updated := []int64{}
				for _, sub := range subs {
					var changed bool
					if sub.folder {
						row, err := tx.Folder(sub.id)
						if err != nil {
							return nil, nil, err
						}
						was := row
						if setTitle {
							row.Title = title
						}
						if row.Extra, err = extra.apply(row.Extra); err != nil {
							return nil, nil, err
						}
						changed = !same(row.Title, was.Title) || !bytes.Equal(row.Extra, was.Extra)
						if changed {
							if err := tx.SetFolderFields(row); err != nil {
								return nil, nil, err
							}
						}
					} else {
						row, tags, err := tx.Task(sub.id)
						if err != nil {
							return nil, nil, err
						}
						was := row
						if setTitle {
							row.Title = *title
						}
						if setPriority {
							row.Priority = priority
						}
						now := slices.Clone(tags)
						if replacing {
							now = slices.Clone(replace)
						}
						for _, tag := range add {
							if !slices.Contains(now, tag) {
								now = append(now, tag)
							}
						}
						now = slices.DeleteFunc(now, func(tag string) bool { return slices.Contains(remove, tag) })
						slices.Sort(now)
						if row.Extra, err = extra.apply(row.Extra); err != nil {
							return nil, nil, err
						}
						changed = row.Title != was.Title || !same(row.Priority, was.Priority) ||
							!slices.Equal(now, tags) || !bytes.Equal(row.Extra, was.Extra)
						if changed {
							if err := tx.SetTaskFields(row, now); err != nil {
								return nil, nil, err
							}
						}
					}
					if changed {
						updated = append(updated, sub.id)
					}
				}
				return UpdateResult{Updated: updated}, nil, nil
			})
		},
	}
}

// same reports whether two optional values are equal: both null, or both
// set to the same value.
func same[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
