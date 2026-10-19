package ops

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
)

type taskResult struct {
	Task model.Task `json:"task"`
}

// written is what a write that changes one task returns: the task's
// identity.
type written struct {
	ID      model.ID `json:"id"`
	Changed bool     `json:"changed"`
}

type moveResult struct {
	ID             model.ID       `json:"id"`
	Changed        bool           `json:"changed"`
	FoldersCreated []model.Folder `json:"folders_created"`
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
	return written{ID: id, Changed: changed}, nil
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
	c.light = true

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
	c.light = true
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
	c.light = true
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

// readBlockedBy reads the input task block and task unblock share.
func readBlockedBy(c *call) (model.ID, []item) {
	in := c.in.top()
	id := in.id("id", true)
	bs := in.items("blocked_by", true)
	if bs != nil && len(bs) == 0 && c.in.has("blocked_by") {
		c.in.bad("/blocked_by", "must hold at least one task or folder")
	}
	return id, bs
}

func cmdBlock(c *call) (any, *errs.Error) {
	id, items := readBlockedBy(c)
	for _, b := range items {
		if b.id == id {
			c.in.bad(b.at, "is the task itself")
		}
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	bs, e := c.blockers(items, false)
	// Every ID that names nothing is reported at once.
	if _, ok := x.Tasks[id]; !ok {
		gone := c.notFoundTasks(id)
		if e != nil && e.Kind == errs.NotFound {
			gone.Message += "; " + e.Message
			gone.WithIDs(e.IDs...)
		}
		return nil, gone
	}
	if e != nil {
		return nil, e
	}
	return c.edit(id, func(tf *model.TaskFile) *errs.Error {
		var added []resolved
		for _, b := range bs {
			if !slices.Contains(tf.BlockedBy, b.id) {
				added = append(added, b)
			}
		}
		if len(added) == 0 {
			return nil
		}
		o := c.overlay()
		o.blockTask(id, idsOf(added)...)
		k := c.newCheck(o)
		k.changed = []graph.Ref{graph.TaskRef(id)}
		for _, b := range added {
			k.wait(graph.TaskRef(id), b.id, b.at)
		}
		if e := c.refuse(k); e != nil {
			return e
		}
		tf.BlockedBy = union(tf.BlockedBy, idsOf(added))
		return nil
	}, nil)
}

func cmdUnblock(c *call) (any, *errs.Error) {
	id, items := readBlockedBy(c)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	if _, _, e := c.find(id); e != nil {
		return nil, e
	}
	bs, e := c.blockers(items, true)
	if e != nil {
		return nil, e
	}
	drop := idsOf(bs)
	return c.edit(id, func(tf *model.TaskFile) *errs.Error {
		tf.BlockedBy = slices.DeleteFunc(tf.BlockedBy, func(b model.ID) bool { return slices.Contains(drop, b) })
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
	from, _, e := c.find(id)
	if e != nil {
		return nil, e
	}
	res := moveResult{ID: id, Changed: from != to, FoldersCreated: []model.Folder{}}
	if res.Changed {
		if e := c.moveCheck(graph.TaskRef(id), from, to); e != nil {
			return nil, e
		}
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
		if _, err := c.t.Stat(model.NotesRel(from, id)); err == nil {
			if _, e := c.t.Move(model.NotesRel(from, id), notes); e != nil {
				return nil, e
			}
		}
	}
	return res, nil
}

type deleteResult struct {
	ID         model.ID    `json:"id"`
	Task       *model.Task `json:"task"`
	Dependents []model.ID  `json:"dependents"`
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
	// Its record, as it was; a file it can't use has none, which does not
	// stop the delete.
	var record *model.Task
	if tf := c.pass(id); tf != nil {
		t := c.task(f, tf)
		record = &t
	}
	if e := c.met(); e != nil {
		return nil, e
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
	dependents, e := c.unreference([]model.ID{id}, "")
	if e != nil {
		return nil, e
	}
	if e := c.dropClaims(id); e != nil {
		return nil, e
	}
	return deleteResult{ID: id, Task: record, Dependents: dependents}, nil
}
