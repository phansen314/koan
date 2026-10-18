package ops

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// newTask is the input for one task to create.
type newTask struct {
	title    string
	folder   model.Folder
	priority *int64
	tags     []string
	blockers []model.ID // existing tasks
	refs     []string   // create-batch: earlier tasks of the batch
	extra    map[string]any
	notes    string
	ref      string
}

// readNewTask reads the fields create and create-batch share.
func readNewTask(f fields, defFolder model.Folder) newTask {
	var n newTask
	n.title, _ = f.title("title", true)
	n.folder = f.folder("folder", false, defFolder)
	n.priority, _ = f.priority("priority")
	n.tags, _ = f.tags("tags")
	n.extra, _ = f.object("extra")
	n.notes, _ = f.text("notes")
	return n
}

func (n newTask) file(id model.ID, now string, blockers []model.ID) *model.TaskFile {
	return &model.TaskFile{
		Schema: model.FileFormat, ID: id, Title: n.title, Priority: n.priority,
		CreatedAt: now, UpdatedAt: now, BlockedBy: blockers, Tags: n.tags, Extra: n.extra,
	}
}

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
		return notFoundTask(gone...)
	}
	return nil
}

// issue takes n new IDs from the counter and saves it: the first is returned.
func (c *call) issue(n int) (model.ID, *errs.Error) {
	first := c.t.State.LastID + 1
	if first+model.ID(n)-1 > model.MaxID {
		return 0, errs.New(errs.Internal, "the ID counter is exhausted")
	}
	c.t.State.LastID += model.ID(n)
	return first, c.t.SaveState()
}

// writeNew writes one new task, whose folder is there: its task file, then
// its notes. created is true once the task file exists.
func (c *call) writeNew(n newTask, tf *model.TaskFile) (created bool, e *errs.Error) {
	if e = c.t.WriteTask(n.folder, tf, true); e != nil {
		return false, e
	}
	return true, c.t.WriteNotes(n.folder, tf.ID, n.notes)
}

type createResult struct {
	Task           model.Task     `json:"task"`
	FoldersCreated []model.Folder `json:"folders_created"`
}

func cmdCreate(c *call) (any, *errs.Error) {
	in := c.in.top()
	n := readNewTask(in, model.Root)
	n.blockers = in.ids("blocked_by", false)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	if e := c.mustExist(n.blockers); e != nil {
		return nil, e
	}
	missing, e := c.plan(n.folder)
	if e != nil {
		return nil, e
	}
	first, e := c.issue(len(missing) + 1)
	if e != nil {
		return nil, e
	}
	folders, e := c.makeFolders(missing, first)
	if e != nil {
		return nil, e
	}
	id := first + model.ID(len(missing))
	tf := n.file(id, c.now, n.blockers)
	created, e := c.writeNew(n, tf)
	if e != nil {
		if created {
			e.WithIDs(id)
		}
		return nil, e
	}
	// A task with no blockers needs no walk to describe: it is ready.
	if c.x == nil {
		c.x = &store.Index{Tasks: map[model.ID]model.Folder{}}
	}
	c.x.Tasks[id] = n.folder
	return createResult{Task: c.task(n.folder, tf), FoldersCreated: folders}, nil
}

type batchResult struct {
	IDs            []model.ID          `json:"ids"`
	Refs           map[string]model.ID `json:"refs"`
	FoldersCreated []model.Folder      `json:"folders_created"`
}

// MaxBatch is the most tasks one create-batch takes.
const MaxBatch = 1000

func cmdCreateBatch(c *call) (any, *errs.Error) {
	in := c.in.top()
	folder := in.folder("folder", false, model.Root)
	var raws []json.RawMessage
	in.get("tasks", true, &raws, "a list of tasks")
	if c.in.has("tasks") && (len(raws) < 1 || len(raws) > MaxBatch) {
		c.in.bad("/tasks", "must hold 1 to %d tasks", MaxBatch)
		raws = nil
	}
	tasks := make([]newTask, len(raws))
	refAt := map[string]int{}
	var existing []model.ID
	for i, raw := range raws {
		at := fmt.Sprintf("/tasks/%d", i)
		m, err := model.Keys(raw)
		if err != nil {
			c.in.bad(at, "must be a JSON object")
			continue
		}
		f := fields{in: c.in, m: m, at: at}
		n := readNewTask(f, folder)
		if f.has("ref") {
			if f.get("ref", false, &n.ref, "a name") && !model.ValidTag(n.ref) {
				c.in.bad(at+"/ref", "must be a name: lowercase letters, digits, and hyphens")
			} else if _, dup := refAt[n.ref]; dup {
				c.in.bad(at+"/ref", "is used by an earlier task")
			}
		}
		var blockers []json.RawMessage
		f.get("blocked_by", false, &blockers, "a list of task IDs and refs")
		for j, b := range blockers {
			bat := fmt.Sprintf("%s/blocked_by/%d", at, j)
			var id model.ID
			var ref string
			switch {
			case model.Decode(b, &id) == nil && model.ValidID(id):
				if slices.Contains(n.blockers, id) {
					c.in.bad(bat, "is repeated")
				}
				n.blockers = append(n.blockers, id)
			case model.Decode(b, &ref) == nil && string(b) != "null":
				if _, ok := refAt[ref]; !ok {
					c.in.bad(bat, "must be the ref of an earlier task in the batch")
				} else if slices.Contains(n.refs, ref) {
					c.in.bad(bat, "is repeated")
				}
				n.refs = append(n.refs, ref)
			default:
				c.in.bad(bat, "must be a task ID or a ref")
			}
		}
		for k := range m {
			if !slices.Contains([]string{"ref", "title", "folder", "priority", "tags", "blocked_by", "extra", "notes"}, k) {
				c.in.bad(at+"/"+k, "is not a field of a task")
			}
		}
		if n.ref != "" {
			refAt[n.ref] = i
		}
		existing = append(existing, n.blockers...)
		tasks[i] = n
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	if e := c.mustExist(existing); e != nil {
		return nil, e
	}
	targets := make([]model.Folder, len(tasks))
	for i, n := range tasks {
		targets[i] = n.folder
	}
	missing, e := c.plan(targets...)
	if e != nil {
		return nil, e
	}
	first, e := c.issue(len(missing) + len(tasks))
	if e != nil {
		return nil, e
	}
	res := batchResult{IDs: []model.ID{}, Refs: map[string]model.ID{}}
	if res.FoldersCreated, e = c.makeFolders(missing, first); e != nil {
		return nil, e
	}
	base := first + model.ID(len(missing))
	for i, n := range tasks {
		id := base + model.ID(i)
		blockers := slices.Clone(n.blockers)
		for _, ref := range n.refs {
			blockers = append(blockers, base+model.ID(refAt[ref]))
		}
		created, e := c.writeNew(n, n.file(id, c.now, blockers))
		if created {
			res.IDs = append(res.IDs, id)
		}
		if e != nil {
			return nil, e.WithIDs(res.IDs...)
		}
		if n.ref != "" {
			res.Refs[n.ref] = id
		}
	}
	return res, nil
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
	merge, _ := in.object("extra_merge")
	drop, _ := in.strs("extra_remove")
	all, hasAll := in.object("extra_replace_all")

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
	if hasAll && (c.in.has("extra_merge") || c.in.has("extra_remove")) {
		c.in.bad("/extra_replace_all", "may not be given with extra_merge or extra_remove")
	}
	for i, k := range drop {
		if _, both := merge[k]; both {
			c.in.bad(fmt.Sprintf("/extra_remove/%d", i), "is also in extra_merge")
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
		if hasAll {
			tf.Extra = all
		}
		for k, v := range merge {
			tf.Extra[k] = v
		}
		for _, k := range drop {
			delete(tf.Extra, k)
		}
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
		look := func(n model.ID) (graph.Node, bool) {
			if f, ok := c.x.Tasks[n]; ok && unreadable == nil {
				if l := c.t.Load(f, n); l.Err != nil && !l.Gone {
					unreadable = l.Err
				}
			}
			return c.look(n)
		}
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
	to := in.folder("to", true, "")
	if e := c.begin(true); e != nil {
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
		notes := store.NotesRel(to, id)
		if fi, err := c.t.Stat(notes); err == nil {
			if fi.Size() > 0 || !fi.Mode().IsRegular() {
				return nil, exists.WithPaths(c.t.Abs(notes))
			}
			if e := c.t.Remove(notes); e != nil {
				return nil, e
			}
		}
		taken, e := c.t.Move(store.TaskRel(from, id), store.TaskRel(to, id))
		if e != nil {
			return nil, e
		}
		if taken {
			return nil, exists.WithPaths(c.t.Abs(store.TaskRel(to, id)))
		}
		c.x.Tasks[id] = to
		if _, err := c.t.Stat(store.NotesRel(from, id)); err == nil {
			if _, e := c.t.Move(store.NotesRel(from, id), notes); e != nil {
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
		return nil, notFoundTask(id)
	}
	// The task goes before its dependents are rewritten: a kill after that
	// leaves references to a task that is gone, which doctor finds. The other
	// order would leave the task with its dependents already freed, and
	// nothing to show for it. Its notes go before the task file, so that a
	// kill between the two leaves a task, which delete can be run on again,
	// and not notes without one. Only a regular file is its notes: the walk
	// ignores anything else of that name, and so does delete.
	notes := store.NotesRel(f, id)
	if fi, err := c.t.Stat(notes); err == nil && fi.Mode().IsRegular() {
		if e := c.t.Remove(notes); e != nil {
			return nil, e
		}
	}
	if e := c.t.Remove(store.TaskRel(f, id)); e != nil {
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
