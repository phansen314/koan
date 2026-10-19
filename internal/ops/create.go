package ops

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
)

// newTask is the input for one task to create.
type newTask struct {
	title    string
	at       folderRef    // the folder, as the input names it
	folder   model.Folder // the folder, once resolved
	priority *int64
	tags     []string
	blockers []model.ID // existing tasks
	refs     []string   // batch: earlier tasks of the batch
	extra    map[string]any
	notes    string
	ref      string
}

// readNewTask reads the fields task create and batch share.
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

// issue takes n new IDs and saves the counter: the first is returned. They
// start above the tree's highest ID as well as the counter's, since an
// outside change can bring in IDs the counter never issued.
func (c *call) issue(n int) (model.ID, *errs.Error) {
	top, rel, e := c.highest()
	if e != nil {
		return 0, e
	}
	if top > c.t.State.LastID {
		c.warn(errs.WarnIDAboveCounter, fmt.Sprintf("the tree holds ID %d, above the counter (%d), as an outside change can leave it: the counter was raised past it", top, c.t.State.LastID),
			[]model.ID{top}, c.t.Abs(rel))
		c.t.State.LastID = top
	}
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
	ID             model.ID       `json:"id"`
	Changed        bool           `json:"changed"`
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
	// reference doctor finds, as task delete leaves one. The other order would
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
	return createResult{ID: id, Changed: true, FoldersCreated: folders, Dependents: dependents}, nil
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

type batchResult struct {
	IDs            []model.ID                `json:"ids"`
	Refs           map[string]model.ID       `json:"refs"`
	Folders        map[model.Folder]model.ID `json:"folders"`
	FoldersCreated []model.Folder            `json:"folders_created"`
}

// MaxBatch is the most folders and tasks one batch takes.
const MaxBatch = 1000

// readFolderEntries reads batch's folder entries, and says how many
// were given.
func readFolderEntries(c *call) ([]newFolder, int) {
	var raws []json.RawMessage
	c.in.top().get("folders", false, &raws, "a list of folders")
	var entries []newFolder
	for i, raw := range raws {
		f, ok := c.in.entry(raw, fmt.Sprintf("/folders/%d", i))
		if !ok {
			continue
		}
		n := newFolder{folder: f.folder("folder", true, "")}
		n.title, _ = f.optTitle("title")
		n.extra, _ = f.object("extra")
		n.notes, _ = f.text("notes")
		f.unknown("a folder")
		switch {
		case n.folder == model.Root:
			c.in.bad(f.at+"/folder", "may not be the root")
		case n.folder == "":
		case slices.ContainsFunc(entries, func(e newFolder) bool { return e.folder == n.folder }):
			c.in.bad(f.at+"/folder", "is used by an earlier folder")
		default:
			entries = append(entries, n)
		}
	}
	return entries, len(raws)
}

func cmdBatch(c *call) (any, *errs.Error) {
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
		f, ok := c.in.entry(raw, fmt.Sprintf("/tasks/%d", i))
		if !ok {
			continue
		}
		n := readNewTask(f, folder)
		if f.has("ref") {
			if f.get("ref", false, &n.ref, "a name") && !model.ValidRef(n.ref) {
				c.in.bad(f.at+"/ref", "must be a name: lowercase letters, digits, and hyphens, with a letter, so it can't be read as a task ID")
			} else if _, dup := refAt[n.ref]; dup {
				c.in.bad(f.at+"/ref", "is used by an earlier task")
			}
		}
		var blockers []json.RawMessage
		f.get("blocked_by", false, &blockers, "a list of task IDs and refs")
		for j, b := range blockers {
			bat := fmt.Sprintf("%s/blocked_by/%d", f.at, j)
			// An ID as every command takes one; any other string, a ref.
			id, isID := idOf(b)
			var ref string
			isRef := !isID && model.Decode(b, &ref) == nil && string(b) != "null"
			switch {
			case isID:
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
		f.unknown("a task")
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
		return nil, c.destinationExists("already exists: a folder entry is a folder to make; to put tasks in one, name it in their folder, and to change its fields, use folder update", there...)
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
