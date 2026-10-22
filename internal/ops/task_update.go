package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// TaskUpdateResult is the result of task update, folder update and task
// release, in its schema's order.
type TaskUpdateResult struct {
	ID      int64 `json:"id"`
	Changed bool  `json:"changed"`
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

// TaskUpdate changes a task's title, priority, tags or extra. A change
// that changes nothing leaves updated_at alone.
func TaskUpdate() *cli.Command {
	return &cli.Command{
		Name:    "task update",
		Subject: &cli.Option{Field: "task", Required: true, Complete: tasksAndGates.complete()},
		Options: []cli.Option{
			{Field: "title", Kind: cli.String},
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
			ref := r.ref("task", core.WantTask, "")
			r.changes("task")
			_, setTitle := r.value("title")
			var title string
			if setTitle {
				title = r.taskTitle("title")
			}
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
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				res, err := resolve(tx, ref, core.WantTask, "task")
				if err != nil {
					return nil, nil, err
				}
				row, tags, err := tx.Task(res.ID)
				if err != nil {
					return nil, nil, err
				}
				was := row
				if setTitle {
					row.Title = title
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
				changed := row.Title != was.Title || !same(row.Priority, was.Priority) ||
					!slices.Equal(now, tags) || !bytes.Equal(row.Extra, was.Extra)
				if changed {
					if err := tx.SetTaskFields(row, now); err != nil {
						return nil, nil, err
					}
				}
				return TaskUpdateResult{ID: res.ID, Changed: changed}, nil, nil
			})
		},
	}
}

// same reports whether two optional values are equal: both null, or both
// set to the same value.
func same[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
