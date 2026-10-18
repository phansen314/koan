package ops

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// newTask is the input for one task to create.
type newTask struct {
	title    string
	at       folderRef    // the folder, as the input names it
	folder   model.Folder // the folder, once resolved
	priority *int64
	tags     []string
	blockers []model.ID // existing tasks
	refs     []string   // create-batch: earlier tasks of the batch
	extra    map[string]any
	notes    string
	ref      string
}

// readNewTask reads the fields create and create-batch share.
func readNewTask(f fields, defFolder folderRef) newTask {
	var n newTask
	n.title, _ = f.title("title", true)
	n.at = f.folderRef("folder", false, defFolder)
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
		return c.notFoundTasks(gone...)
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
	Dependents     []model.ID     `json:"dependents"`
}

func cmdCreate(c *call) (any, *errs.Error) {
	in := c.in.top()
	n := readNewTask(in, folderRef{path: model.Root})
	n.blockers = in.ids("blocked_by", false)
	blocks := slices.Sorted(slices.Values(in.ids("blocks", false)))
	if e := c.begin(true); e != nil {
		return nil, e
	}
	var e *errs.Error
	if n.folder, e = c.resolve(n.at); e != nil {
		return nil, e
	}
	if e := c.mustExist(append(slices.Clone(n.blockers), blocks...)); e != nil {
		return nil, e
	}
	if e := c.noCycleThroughNew(n.blockers, blocks); e != nil {
		return nil, e
	}
	// Every task in blocks is rewritten, so each must be usable: found out
	// now, before an ID is used.
	type dependent struct {
		f  model.Folder
		tf *model.TaskFile
	}
	var deps []dependent
	for _, b := range blocks {
		f, btf, e := c.find(b)
		if e != nil {
			return nil, e
		}
		deps = append(deps, dependent{f, btf})
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
	// The tasks in blocks are made to wait on the new one before it exists:
	// a kill between leaves them waiting on an ID with no task, a dangling
	// reference doctor finds, as delete leaves one. The other order would
	// leave the new task with its dependents still free, and nothing to
	// show for it.
	dependents := []model.ID{}
	for _, d := range deps {
		d.tf.BlockedBy = append(d.tf.BlockedBy, id)
		d.tf.UpdatedAt = c.now
		if e := c.t.WriteTask(d.f, d.tf, false); e != nil {
			if len(dependents) > 0 {
				e.Message = fmt.Sprintf("task %d was not created, but %v were made to wait on it before task %d could not be: %s", id, dependents, d.tf.ID, e.Message)
			}
			return nil, e.WithIDs(dependents...)
		}
		dependents = append(dependents, d.tf.ID)
	}
	tf := n.file(id, c.now, n.blockers)
	created, e := c.writeNew(n, tf)
	switch {
	case e == nil:
	case created:
		// Its dependents wait on a task that exists: only the notes are
		// missing.
		return nil, e.WithIDs(id)
	case len(dependents) > 0:
		e.Message = fmt.Sprintf("task %d was not created, but %v were made to wait on it: %s", id, dependents, e.Message)
		return nil, e.WithIDs(dependents...)
	default:
		return nil, e
	}
	// A task with no blockers needs no walk to describe: it is ready.
	if c.x == nil {
		c.x = &store.Index{Tasks: map[model.ID]model.Folder{}}
	}
	c.x.Tasks[id] = n.folder
	return createResult{Task: c.task(n.folder, tf), FoldersCreated: folders, Dependents: dependents}, nil
}

// noCycleThroughNew refuses a new task that would close a cycle: one
// blocked by the tasks in blockers and blocking those in blocks closes one
// when a task in blocks is reachable from one in blockers along blocked_by.
// A cycle through a task file that can't be read can't be ruled out, so
// meeting one refuses the task with that file's error.
func (c *call) noCycleThroughNew(blockers, blocks []model.ID) *errs.Error {
	if len(blockers) == 0 || len(blocks) == 0 {
		return nil
	}
	var unreadable *errs.Error
	look := c.cycleLook(&unreadable)
	for _, b := range blocks {
		for _, x := range blockers {
			if x == b {
				return errs.NewConflict(errs.RuleCycle, "task %d is in both blocked_by and blocks, so the new task would wait on itself", b).WithIDs(b)
			}
			if path := graph.Path(x, b, look); path != nil {
				return errs.NewConflict(errs.RuleCycle, "task %d waits on task %d, so a new task blocked by %d and blocking %d would make a cycle", x, b, x, b).
					WithIDs(path...)
			}
		}
	}
	return unreadable
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

type batchResult struct {
	IDs            []model.ID                `json:"ids"`
	Refs           map[string]model.ID       `json:"refs"`
	Folders        map[model.Folder]model.ID `json:"folders"`
	FoldersCreated []model.Folder            `json:"folders_created"`
}

// MaxBatch is the most folders and tasks one create-batch takes.
const MaxBatch = 1000

// readFolderEntries reads create-batch's folder entries, and says how many
// were given.
func readFolderEntries(c *call) ([]newFolder, int) {
	var raws []json.RawMessage
	c.in.top().get("folders", false, &raws, "a list of folders")
	var entries []newFolder
	for i, raw := range raws {
		at := fmt.Sprintf("/folders/%d", i)
		m, err := model.Keys(raw)
		if err != nil {
			c.in.bad(at, "must be a JSON object")
			continue
		}
		f := fields{in: c.in, m: m, at: at}
		n := newFolder{folder: f.folder("path", true, "")}
		n.title, _ = f.optTitle("title")
		n.extra, _ = f.object("extra")
		n.notes, _ = f.text("notes")
		for k := range m {
			if !slices.Contains([]string{"path", "title", "extra", "notes"}, k) {
				c.in.bad(at+"/"+k, "is not a field of a folder")
			}
		}
		switch {
		case n.folder == model.Root:
			c.in.bad(at+"/path", "may not be the root")
		case n.folder == "":
		case slices.ContainsFunc(entries, func(e newFolder) bool { return e.folder == n.folder }):
			c.in.bad(at+"/path", "is used by an earlier folder")
		default:
			entries = append(entries, n)
		}
	}
	return entries, len(raws)
}

func cmdCreateBatch(c *call) (any, *errs.Error) {
	in := c.in.top()
	entries, nFolders := readFolderEntries(c)
	folder := in.folderRef("folder", false, folderRef{path: model.Root})
	var raws []json.RawMessage
	in.get("tasks", false, &raws, "a list of tasks")
	if n := len(raws) + nFolders; n < 1 || n > MaxBatch {
		if c.in.has("tasks") {
			c.in.bad("/tasks", "must hold 1 to %d entries between it and folders", MaxBatch)
		} else {
			c.in.bad("/folders", "must hold 1 to %d entries between it and tasks", MaxBatch)
		}
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
			var ref string
			isRef := model.Decode(b, &ref) == nil && string(b) != "null" && !strings.HasPrefix(ref, model.Mark)
			// A string without @ is a ref; anything else, an ID.
			switch id, isID := idOf(b); {
			case !isRef && isID:
				if slices.Contains(n.blockers, id) {
					c.in.bad(bat, "names the same task as an earlier one")
				}
				n.blockers = append(n.blockers, id)
			case isRef:
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
	// A folder entry means one to make: every one already there is named.
	var there []model.Folder
	for _, n := range entries {
		if _, err := c.t.Stat(n.folder.Rel()); err == nil {
			there = append(there, n.folder)
		}
	}
	if len(there) > 0 {
		return nil, c.destinationExists("already exists: a folder entry is a folder to make; to put tasks in one, name it in their folder, and to change its fields, use update-folder", there...)
	}
	if e := c.mustExist(existing); e != nil {
		return nil, e
	}
	// The batch's folder is checked even when no task falls back to it,
	// and made only when one does.
	if f, e := c.resolve(folder); e != nil {
		return nil, e
	} else if _, e := c.t.Missing(f); e != nil {
		return nil, e
	}
	var targets []model.Folder
	for _, n := range entries {
		targets = append(targets, n.folder)
	}
	for i := range tasks {
		f, e := c.resolve(tasks[i].at)
		if e != nil {
			return nil, e
		}
		tasks[i].folder = f
		targets = append(targets, f)
	}
	missing, e := c.plan(targets...)
	if e != nil {
		return nil, e
	}
	first, e := c.issue(len(missing) + len(tasks))
	if e != nil {
		return nil, e
	}
	res := batchResult{IDs: []model.ID{}, Refs: map[string]model.ID{}, Folders: map[model.Folder]model.ID{}}
	res.FoldersCreated, e = c.makeFolders(missing, first, entries...)
	for i, f := range missing {
		if slices.Contains(res.FoldersCreated, f) {
			res.Folders[f] = first + model.ID(i)
		}
	}
	// A failure from here on names what was made: the error's folders are
	// the folders made, and its ids the tasks.
	made := func(e *errs.Error) *errs.Error {
		e.Folders = []string{}
		for _, f := range res.FoldersCreated {
			e.Folders = append(e.Folders, string(f))
		}
		return e.WithIDs(res.IDs...)
	}
	if e != nil {
		return nil, made(e)
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
			return nil, made(e)
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
		return nil, c.notFoundTasks(id)
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
