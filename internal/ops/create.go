package ops

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

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
	blockers []item   // existing tasks and folders, and a batch's folders
	refs     []string // batch: earlier tasks of the batch
	refsAt   []string // batch: where each of refs is in the input
	placed   bool     // batch: the entry names its folder
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
	if blockers == nil {
		blockers = []model.ID{}
	}
	return &model.TaskFile{
		Schema: model.TaskFormat, ID: id, Title: n.title, Priority: n.priority,
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
	n.blockers = in.items("blocked_by", false)
	blocks := in.items("blocks", false)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	var e *errs.Error
	if n.folder, e = c.resolve(n.at); e != nil {
		return nil, e
	}
	bs, e := c.blockers(n.blockers, false)
	if e != nil {
		return nil, e
	}
	ds, e := c.blockers(blocks, false)
	if e != nil {
		return nil, e
	}
	// Everything in blocks is rewritten, so each must be usable: found out
	// now, before an ID is used.
	deps, e := c.dependents(ds)
	if e != nil {
		return nil, e
	}
	missing, e := c.plan(n.folder)
	if e != nil {
		return nil, e
	}
	next, e := c.nextID()
	if e != nil {
		return nil, e
	}
	id := next + model.ID(len(missing))
	// A new task closes a cycle only through its blocked_by and blocks.
	if len(bs) > 0 || len(deps) > 0 {
		o := c.overlay()
		for i, m := range missing {
			o.addFolder(m, next+model.ID(i), nil)
		}
		o.addTask(id, n.folder, idsOf(bs), fmt.Sprintf("the new task %q", n.title))
		k := c.newCheck(o)
		k.changed = []graph.Ref{graph.TaskRef(id)}
		for _, b := range bs {
			k.wait(graph.TaskRef(id), b.id, b.at)
		}
		for _, d := range deps {
			o.block(d.id, id)
			k.wait(d.ref(), id, d.at)
		}
		if n.folder != model.Root {
			k.edge(graph.TaskRef(id), graph.WaitOf(n.folder), "/folder")
			k.edge(graph.DoneOf(n.folder), graph.TaskRef(id), "/folder")
		}
		if e := c.refuse(k); e != nil {
			return nil, e
		}
	}
	first, e := c.issue(len(missing) + 1)
	if e != nil {
		return nil, e
	}
	if first != next {
		return nil, errs.New(errs.Internal, "the IDs checked, from %d, are not the IDs issued, from %d", next, first)
	}
	folders, e := c.makeFolders(missing, first)
	if e != nil {
		return nil, e
	}
	// What is in blocks is made to wait on the new task before it exists: a
	// kill between leaves them waiting on an ID with no task, a dangling
	// reference doctor finds, as task delete leaves one. The other order
	// would leave the new task with its dependents still free, and nothing to
	// show for it.
	dependents, e := c.block(deps, id, "task")
	if e != nil {
		return nil, e
	}
	tf := n.file(id, c.now, idsOf(bs))
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
		n := newFolder{folder: f.folder("folder", true, ""), at: f.at + "/folder"}
		n.title, _ = f.optTitle("title")
		n.extra, _ = f.object("extra")
		n.notes, _ = f.text("notes")
		n.items = f.items("blocked_by", false)
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
	// Every folder path in the batch, and where it is, for the case check.
	var paths []model.Folder
	var pathsAt []string
	addPaths := func(items []item) {
		for _, it := range items {
			if it.id == 0 {
				paths, pathsAt = append(paths, it.path), append(pathsAt, it.at)
			}
		}
	}
	for _, n := range entries {
		paths, pathsAt = append(paths, n.folder), append(pathsAt, n.at)
		addPaths(n.items)
	}
	if folder.id == 0 && folder.path != "" {
		paths, pathsAt = append(paths, folder.path), append(pathsAt, "/folder")
	}
	refAt := map[string]int{}
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
		f.get("blocked_by", false, &blockers, "a list of task or folder IDs, folder paths, and refs")
		for j, b := range blockers {
			bat := fmt.Sprintf("%s/blocked_by/%d", f.at, j)
			// An ID or a folder path as every command takes one, or a
			// string that can be a ref.
			var ref string
			_, isID := idOf(b)
			isRef := !isID && model.Decode(b, &ref) == nil && model.ValidRef(ref)
			switch {
			case isRef:
				if _, ok := refAt[ref]; !ok {
					c.in.bad(bat, "must be the ref of an earlier task in the batch")
				} else if slices.Contains(n.refs, ref) {
					c.in.bad(bat, "is repeated")
				}
				n.refs, n.refsAt = append(n.refs, ref), append(n.refsAt, bat)
			default:
				if !isID && !strings.HasPrefix(ref, "/") {
					c.in.bad(bat, "must be a task or folder ID, like 42 or @42, a folder path, like /proj/research, or the ref of an earlier task")
					continue
				}
				it, ok := f.readItem(b, bat)
				if !ok {
					continue
				}
				if slices.ContainsFunc(n.blockers, func(o item) bool { return o.id == it.id && o.path == it.path }) {
					c.in.bad(bat, "names the same task or folder as an earlier one")
					continue
				}
				n.blockers = append(n.blockers, it)
			}
		}
		f.unknown("a task")
		if f.has("folder") && n.at.id == 0 && n.at.path != "" {
			paths, pathsAt = append(paths, n.at.path), append(pathsAt, f.at+"/folder")
		}
		addPaths(n.blockers)
		if n.ref != "" {
			refAt[n.ref] = i
		}
		n.placed = f.has("folder")
		tasks[i] = n
	}
	if i, q := model.CaseClash(paths); i >= 0 {
		c.in.bad(pathsAt[i], "differs only in case from %s, earlier in the batch", q)
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	// A folder entry means one to make: every one already there is named.
	var there []model.Folder
	for _, n := range entries {
		taken, e := c.t.Taken(n.folder, "")
		if e != nil {
			return nil, e
		}
		if taken {
			there = append(there, n.folder)
		}
	}
	if len(there) > 0 {
		return nil, c.destinationExists("already exists: a folder entry is a folder to make; to put tasks in one, name it in their folder, and to change its fields, use folder update", there...)
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
	next, e := c.nextID()
	if e != nil {
		return nil, e
	}
	base := next + model.ID(len(missing))
	// The blockers named by ID or by a path of the tree, and by a path of
	// a folder the batch makes, which has the ID it will take.
	resolve := func(items []item) ([]resolved, *errs.Error) {
		var mine []resolved
		var theirs []item
		for _, it := range items {
			if i := slices.Index(missing, it.path); it.id == 0 && i >= 0 {
				mine = append(mine, resolved{next + model.ID(i), it.at})
			} else {
				theirs = append(theirs, it)
			}
		}
		rs, e := c.blockers(theirs, false)
		return append(rs, mine...), e
	}
	entryBlockers := make([][]resolved, len(entries))
	for i := range entries {
		if entryBlockers[i], e = resolve(entries[i].items); e != nil {
			return nil, e
		}
		entries[i].blockedBy = idsOf(entryBlockers[i])
	}
	taskBlockers := make([][]resolved, len(tasks))
	for i, n := range tasks {
		if taskBlockers[i], e = resolve(n.blockers); e != nil {
			return nil, e
		}
		for j, ref := range n.refs {
			taskBlockers[i] = append(taskBlockers[i], resolved{base + model.ID(refAt[ref]), n.refsAt[j]})
		}
	}
	// Folder paths can close a cycle, as refs alone can't: the batch is
	// checked as a whole, with the tree.
	if slices.ContainsFunc(entryBlockers, func(rs []resolved) bool { return len(rs) > 0 }) ||
		slices.ContainsFunc(taskBlockers, func(rs []resolved) bool { return len(rs) > 0 }) {
		o := c.overlay()
		k := c.newCheck(o)
		for i, m := range missing {
			o.addFolder(m, next+model.ID(i), nil)
		}
		for i, n := range entries {
			o.folders[n.folder].blockedBy = n.blockedBy
			k.changed = append(k.changed, graph.WaitOf(n.folder))
			for _, b := range entryBlockers[i] {
				k.wait(graph.WaitOf(n.folder), b.id, b.at)
			}
			if p := n.folder.Parent(); p != model.Root {
				k.edge(graph.WaitOf(n.folder), graph.WaitOf(p), n.at)
				k.edge(graph.DoneOf(p), graph.DoneOf(n.folder), n.at)
			}
		}
		for i, n := range tasks {
			id := base + model.ID(i)
			name := fmt.Sprintf("the new task %q", n.title)
			if n.ref != "" {
				name = "the new task " + n.ref
			}
			o.addTask(id, n.folder, idsOf(taskBlockers[i]), name)
		}
		for i, n := range tasks {
			id := base + model.ID(i)
			k.changed = append(k.changed, graph.TaskRef(id))
			for _, b := range taskBlockers[i] {
				k.wait(graph.TaskRef(id), b.id, b.at)
			}
			if n.folder != model.Root {
				at := "/folder"
				if n.placed {
					at = fmt.Sprintf("/tasks/%d/folder", i)
				}
				k.edge(graph.TaskRef(id), graph.WaitOf(n.folder), at)
				k.edge(graph.DoneOf(n.folder), graph.TaskRef(id), at)
			}
		}
		if e := c.refuse(k); e != nil {
			return nil, e
		}
	}
	first, e := c.issue(len(missing) + len(tasks))
	if e != nil {
		return nil, e
	}
	if first != next {
		return nil, errs.New(errs.Internal, "the IDs checked, from %d, are not the IDs issued, from %d", next, first)
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
	for i, n := range tasks {
		id := base + model.ID(i)
		created, e := c.writeNew(n, n.file(id, c.now, idsOf(taskBlockers[i])))
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
