package ops

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
)

// mustExist checks that every ID names a task, reporting all that don't.
func (c *call) mustExist(ids []model.ID) *errs.Error {
	if len(ids) == 0 {
		return nil
	}
	x, e := c.index()
	if e != nil {
		return e
	}
	var gone []model.ID
	for _, id := range ids {
		if _, ok := x.Tasks[id]; !ok && !slices.Contains(gone, id) {
			gone = append(gone, id)
		}
	}
	if len(gone) > 0 {
		return c.notFoundTasks(gone...)
	}
	return nil
}

// cycleLook is c.look for a search for a cycle, which follows blocked_by
// and can't read it from an unusable file: the first one met has its error
// kept in *unreadable.
func (c *call) cycleLook(unreadable **errs.Error) func(model.ID) (graph.Node, bool) {
	return func(n model.ID) (graph.Node, bool) {
		if f, ok := c.x.Tasks[n]; ok && *unreadable == nil {
			if l := c.t.Load(f, n); l.Err != nil && !l.Gone {
				*unreadable = l.Err
			}
		}
		return c.look(n)
	}
}

type taskResult struct {
	Task    model.Task `json:"task"`
	Changed *bool      `json:"changed,omitempty"`
}

type moveResult struct {
	Task           model.Task     `json:"task"`
	FoldersCreated []model.Folder `json:"folders_created"`
	Changed        bool           `json:"changed"`
}

func cmdShow(c *call) (any, *errs.Error) {
	id := c.in.top().id("id", true)
	if e := c.begin(false); e != nil {
		return nil, e
	}
	f, tf, e := c.find(id)
	if e != nil {
		return nil, e
	}
	return taskResult{Task: c.task(f, tf)}, nil
}

// edit is the shape of the commands that change one task file: find the
// task, let change alter it, and write it if it differs.
func (c *call) edit(id model.ID, change func(tf *model.TaskFile) *errs.Error, after func(tf *model.TaskFile) *errs.Error) (any, *errs.Error) {
	f, tf, e := c.find(id)
	if e != nil {
		return nil, e
	}
	before := *tf
	before.BlockedBy, before.Tags = slices.Clone(tf.BlockedBy), slices.Clone(tf.Tags)
	before.Extra = cloneExtra(tf.Extra)
	if e := change(tf); e != nil {
		return nil, e
	}
	tf.Normalize()
	changed := !reflect.DeepEqual(&before, tf)
	if changed {
		tf.UpdatedAt = c.now
		if e := c.t.WriteTask(f, tf, false); e != nil {
			return nil, e
		}
	}
	if after != nil {
		if e := after(tf); e != nil {
			return nil, e
		}
	}
	return taskResult{Task: c.task(f, tf), Changed: &changed}, nil
}

func cloneExtra(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cmdUpdate(c *call) (any, *errs.Error) {
	in := c.in.top()
	id := in.id("id", true)
	title, hasTitle := in.title("title", false)
	priority, hasPriority := in.priority("priority")
	add, _ := in.tags("tags_add")
	remove, _ := in.tags("tags_remove")
	replace, hasReplace := in.tags("tags_replace_all")
	edit := readExtraEdit(c, in)

	if len(c.in.m) < 2 && c.in.has("id") {
		c.in.bad("", "gives nothing to change")
	}
	if hasReplace && (c.in.has("tags_add") || c.in.has("tags_remove")) {
		c.in.bad("/tags_replace_all", "may not be given with tags_add or tags_remove")
	}
	for i, t := range add {
		if slices.Contains(remove, t) {
			c.in.bad(fmt.Sprintf("/tags_add/%d", i), "is also in tags_remove")
		}
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	return c.edit(id, func(tf *model.TaskFile) *errs.Error {
		if hasTitle {
			tf.Title = title
		}
		if hasPriority {
			tf.Priority = priority
		}
		if hasReplace {
			tf.Tags = replace
		}
		tf.Tags = slices.DeleteFunc(tf.Tags, func(t string) bool { return slices.Contains(remove, t) })
		for _, t := range add {
			if !slices.Contains(tf.Tags, t) {
				tf.Tags = append(tf.Tags, t)
			}
		}
		tf.Extra = edit.apply(tf.Extra)
		return nil
	}, nil)
}

func cmdDone(c *call) (any, *errs.Error) {
	id := c.in.top().id("id", true)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	return c.edit(id, func(tf *model.TaskFile) *errs.Error {
		if tf.Open() {
			tf.CompletedAt = &c.now
		}
		return nil
	}, func(*model.TaskFile) *errs.Error { return c.dropClaims(id) })
}

func cmdReopen(c *call) (any, *errs.Error) {
	id := c.in.top().id("id", true)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	// A claim left on a done task by a crash must not come back with it.
	var wasDone bool
	return c.edit(id, func(tf *model.TaskFile) *errs.Error {
		wasDone = !tf.Open()
		tf.CompletedAt = nil
		return nil
	}, func(*model.TaskFile) *errs.Error {
		if wasDone {
			return c.dropClaims(id)
		}
		return nil
	})
}

// blockers reads the input block and unblock share.
func readBlockers(c *call) (model.ID, []model.ID) {
	in := c.in.top()
	id := in.id("id", true)
	bs := in.ids("blockers", true)
	if bs != nil && len(bs) == 0 {
		c.in.bad("/blockers", "must hold at least one task ID")
	}
	return id, bs
}

func cmdBlock(c *call) (any, *errs.Error) {
	id, bs := readBlockers(c)
	for i, b := range bs {
		if b == id {
			c.in.bad(fmt.Sprintf("/blockers/%d", i), "is the task itself")
		}
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	if e := c.mustExist(append([]model.ID{id}, bs...)); e != nil {
		return nil, e
	}
	return c.edit(id, func(tf *model.TaskFile) *errs.Error {
		// The search for a cycle follows blocked_by, which it can't read
		// from an unusable file: meeting one, the block is refused, since a
		// cycle through that file can't be ruled out.
		var unreadable *errs.Error
		look := c.cycleLook(&unreadable)
		for _, b := range bs {
			if slices.Contains(tf.BlockedBy, b) {
				continue
			}
			if path := graph.Path(b, id, look); path != nil {
				cycle := append([]model.ID{id}, path[:len(path)-1]...)
				return errs.NewConflict(errs.RuleCycle, "task %d already waits on task %d, so blocking %d by %d would make a cycle", b, id, id, b).
					WithIDs(cycle...)
			}
		}
		if unreadable != nil {
			return unreadable
		}
		for _, b := range bs {
			if !slices.Contains(tf.BlockedBy, b) {
				tf.BlockedBy = append(tf.BlockedBy, b)
			}
		}
		return nil
	}, nil)
}

func cmdUnblock(c *call) (any, *errs.Error) {
	id, bs := readBlockers(c)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	return c.edit(id, func(tf *model.TaskFile) *errs.Error {
		tf.BlockedBy = slices.DeleteFunc(tf.BlockedBy, func(b model.ID) bool { return slices.Contains(bs, b) })
		return nil
	}, nil)
}

func cmdMove(c *call) (any, *errs.Error) {
	in := c.in.top()
	id := in.id("id", true)
	ref := in.folderRef("to", true, folderRef{})
	if e := c.begin(true); e != nil {
		return nil, e
	}
	to, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	from, tf, e := c.find(id)
	if e != nil {
		return nil, e
	}
	res := moveResult{FoldersCreated: []model.Folder{}, Changed: from != to}
	if res.Changed {
		if res.FoldersCreated, e = c.ensure(to); e != nil {
			return nil, e
		}
		exists := errs.NewConflict(errs.RuleDestinationExists, "%s already holds files of task %d", to, id).
			WithIDs(id).WithFolders(string(to))
		// An empty .md at the destination is what a move cut short leaves:
		// it is replaced. One with content is someone's notes.
		notes := model.NotesRel(to, id)
		if fi, err := c.t.Stat(notes); err == nil {
			if fi.Size() > 0 || !fi.Mode().IsRegular() {
				return nil, exists.WithPaths(c.t.Abs(notes))
			}
			if e := c.t.Remove(notes); e != nil {
				return nil, e
			}
		}
		taken, e := c.t.Move(model.TaskRel(from, id), model.TaskRel(to, id))
		if e != nil {
			return nil, e
		}
		if taken {
			return nil, exists.WithPaths(c.t.Abs(model.TaskRel(to, id)))
		}
		c.x.Tasks[id] = to
		if _, err := c.t.Stat(model.NotesRel(from, id)); err == nil {
			if _, e := c.t.Move(model.NotesRel(from, id), notes); e != nil {
				return nil, e
			}
		}
	}
	res.Task = c.task(to, tf)
	return res, nil
}

type deleteResult struct {
	ID         model.ID   `json:"id"`
	Dependents []model.ID `json:"dependents"`
}

func cmdDelete(c *call) (any, *errs.Error) {
	id := c.in.top().id("id", true)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	f, ok := x.Tasks[id]
	if !ok {
		return nil, c.notFoundTasks(id)
	}
	// The task goes before its dependents are rewritten: a kill after that
	// leaves references to a task that is gone, which doctor finds. The other
	// order would leave the task with its dependents already freed, and
	// nothing to show for it. Its notes go before the task file, so that a
	// kill between the two leaves a task, which delete can be run on again,
	// and not notes without one. Only a regular file is its notes: the walk
	// ignores anything else of that name, and so does delete.
	notes := model.NotesRel(f, id)
	if fi, err := c.t.Stat(notes); err == nil && fi.Mode().IsRegular() {
		if e := c.t.Remove(notes); e != nil {
			return nil, e
		}
	}
	if e := c.t.Remove(model.TaskRel(f, id)); e != nil {
		return nil, e
	}
	dependents, e := c.unreference([]model.ID{id})
	if e != nil {
		return nil, e
	}
	if e := c.dropClaims(id); e != nil {
		return nil, e
	}
	return deleteResult{ID: id, Dependents: dependents}, nil
}
