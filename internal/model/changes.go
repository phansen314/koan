package model

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/core"
)

func invalid(format string, args ...any) error {
	return &core.Error{Kind: core.KindInvalidInput, Message: fmt.Sprintf(format, args...)}
}

// pathRef parses a folder path to make: a path from the root, not the root.
func pathRef(s string) (core.Ref, error) {
	r, err := core.ParseRef(s)
	if err != nil {
		return r, err
	}
	if r.Kind != core.TargetPath || r.IsRoot() || r.End != core.EndNone || r.HasMaterial {
		return r, invalid("%q is not a folder path from the root", s)
	}
	return r, nil
}

func checkPriority(p *int64) error {
	if p == nil {
		return nil
	}
	return core.CheckInt(*p)
}

func checkFolderTitle(title *string) (*string, error) {
	if title == nil {
		return nil, nil
	}
	t, err := core.CheckTitle(*title)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// makeFolder makes a folder with no title, no gates and no material.
func (c *Tree) makeFolder(parent int64, name string) *Folder {
	f := &Folder{ID: c.issue(), Parent: parent, Name: name, Extra: map[string]any{}, Material: map[string]bool{}}
	c.Folders[f.ID] = f
	return f
}

// parents follows names from the root, making each folder missing. A part
// that matches only ignoring case is case-mismatch; one that is self is
// into-itself.
func (c *Tree) parents(names []string, self int64) (int64, []FolderAt, error) {
	at, given := core.RootID, ""
	var made []FolderAt
	for _, name := range names {
		given += "/" + name
		if id, ok := c.Child(at, name); ok {
			if id == self {
				return 0, nil, core.Conflict(core.RuleIntoItself, []int64{self}, nil,
					"@%d would land inside itself", self)
			}
			at = id
			continue
		}
		if id, spelled, ok := c.ChildFold(at, name); ok {
			is := strings.TrimSuffix(c.Path(at), "/") + "/" + spelled
			return 0, nil, core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{given, is},
				"%s matches %s only ignoring case", given, is)
		}
		f := c.makeFolder(at, name)
		made = append(made, FolderAt{ID: f.ID, Path: c.Path(f.ID)})
		at = f.ID
	}
	return at, made, nil
}

// createFolder makes the folder at ref's path, and any missing parents.
func (c *Tree) createFolder(ref core.Ref, pointer string, title *string, extra map[string]any) ([]FolderAt, error) {
	n := len(ref.Names)
	parent, made, err := c.parents(ref.Names[:n-1], 0)
	if err != nil {
		return nil, err
	}
	name, path := ref.Names[n-1], ref.TargetString()
	if id, ok := c.Child(parent, name); ok {
		return nil, core.ExistsAt(pointer, id, path, "%s is taken", path)
	}
	if id, spelled, ok := c.ChildFold(parent, name); ok {
		is := strings.TrimSuffix(c.Path(parent), "/") + "/" + spelled
		return nil, core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{path, is},
			"%s differs from %s only in case", path, is)
	}
	f := c.makeFolder(parent, name)
	f.Title, f.Extra = title, orEmpty(extra)
	c.given(f.ID)
	return append(made, FolderAt{ID: f.ID, Path: c.Path(f.ID)}), nil
}

// CreateFolder makes a folder at path, and any missing parents. The result
// lists every folder made, parents first, this one last.
func (t *Tree) CreateFolder(path string, title *string, extra map[string]any) (made []FolderAt, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		ref := s.path("/path", path)
		title := s.folderTitle("/title", title)
		if err := s.done(nil); err != nil {
			return err
		}
		made, err = c.createFolder(ref, "/path", title, extra)
		return err
	})
	return made, err
}

// makeGate makes a folder's gate, as an edge naming it does.
func (c *Tree) makeGate(folder int64, end core.End) GateMade {
	title := "Start"
	if end == core.EndDone {
		title = "Done"
	}
	g := &Task{
		ID: c.issue(), Gate: end, Folder: folder, Title: title,
		CreatedAt: c.now, UpdatedAt: c.now,
		BlockedBy: map[int64]bool{}, Tags: map[string]bool{}, Extra: map[string]any{},
	}
	c.Tasks[g.ID] = g
	return GateMade{ID: g.ID, Folder: folder, Path: c.Path(folder), End: end}
}

// edgeIn is one edge as given: X's blocked_by entry (blocks false), or one
// written from the other end (blocks true). Pointer is where it is in the
// input.
type edgeIn struct {
	task    int64
	ref     core.Ref
	blocks  bool
	pointer string
}

type edge struct {
	core.Edge
	pointer string
}

// resolveEdges resolves edges, making each missing gate they name when make
// is set, and leaving out its edge otherwise. In a batch, a refusal
// points at the edge.
func (c *Tree) resolveEdges(in []edgeIn, labels map[string]int64, make, batch bool) ([]edge, []GateMade, error) {
	var out []edge
	var made []GateMade
	for _, e := range in {
		r, err := core.Resolve(c, e.ref, core.WantEdge, labels)
		if err != nil {
			return nil, nil, pointAt(err, e.pointer, batch)
		}
		if r.MissingGate() {
			// An earlier edge in this change may have made it.
			if id, ok := c.Gate(r.Folder, r.End); ok {
				r.ID = id
			} else if make {
				g := c.makeGate(r.Folder, r.End)
				made = append(made, g)
				r.ID = g.ID
			} else {
				// A removal finds an edge that isn't there.
				continue
			}
		}
		x := core.Edge{From: e.task, To: r.ID}
		if e.blocks {
			x = core.Edge{From: r.ID, To: e.task}
		}
		out = append(out, edge{x, e.pointer})
	}
	return out, made, nil
}

// addEdges writes edges, refusing an edge to itself and any that closes a
// cycle. It returns the complete-blocker warning, if any.
func (c *Tree) addEdges(edges []edge) ([]Warning, bool, error) {
	var fresh []edge
	seen := map[core.Edge]bool{}
	for _, e := range edges {
		if e.From == e.To {
			err := invalid("@%d can't wait on itself", e.From)
			err.(*core.Error).Problems = []string{e.pointer}
			return nil, false, err
		}
		if c.Tasks[e.From].BlockedBy[e.To] || seen[e.Edge] {
			continue
		}
		seen[e.Edge] = true
		fresh = append(fresh, e)
	}
	plain := make([]core.Edge, len(fresh))
	for i, e := range fresh {
		plain[i] = e.Edge
	}
	if cycle, _ := core.FindCycle(c.BlockedBy, plain); cycle != nil {
		err := core.Conflict(core.RuleCycle, cycle, nil,
			"@%d would wait on itself through %s", cycle[0], core.FormatIDs(cycle[1:]))
		for i := range cycle {
			step := core.Edge{From: cycle[i], To: cycle[(i+1)%len(cycle)]}
			for _, e := range fresh {
				if e.Edge == step {
					err.Problems = append(err.Problems, e.pointer)
				}
			}
		}
		return nil, false, err
	}
	complete := map[int64]bool{}
	for _, e := range fresh {
		from := c.Tasks[e.From]
		from.BlockedBy[e.To] = true
		from.UpdatedAt = c.now
		c.edged(e.Edge)
		if !from.Open() {
			complete[e.From] = true
		}
	}
	var warnings []Warning
	if len(complete) > 0 {
		warnings = append(warnings, Warning{Kind: "complete-blocker", IDs: sortedKeys(complete)})
	}
	return warnings, len(fresh) > 0, nil
}

// TaskInput is what task create takes. Folder is a folder-ref; empty means
// the root. BlockedBy and Blocks are edge-refs.
type TaskInput struct {
	Title     string
	Folder    string
	Priority  *int64
	Tags      []string
	BlockedBy []string
	Blocks    []string
	Extra     map[string]any
}

// TaskCreated is what task create returns.
type TaskCreated struct {
	ID        int64
	GatesMade []GateMade
	Reblocked []int64
	Warnings  []Warning
}

// newTask makes a task from fields already checked, with no edges yet.
func (c *Tree) newTask(title string, priority *int64, tags map[string]bool, extra map[string]any, folder int64) *Task {
	x := &Task{
		ID: c.issue(), Folder: folder, Title: title, Priority: priority,
		CreatedAt: c.now, UpdatedAt: c.now,
		BlockedBy: map[int64]bool{}, Tags: tags, Extra: orEmpty(extra),
	}
	c.Tasks[x.ID] = x
	c.given(x.ID)
	return x
}

// edgeRefs checks the shape of a task's edges, at prefix/blocked_by/i and
// prefix/blocks/i.
func (s *shape) edgeRefs(prefix string, blockedBy, blocks []string, inBatch bool) (bb, bl []core.Ref) {
	for i, r := range blockedBy {
		bb = append(bb, s.ref(fmt.Sprintf("%s/blocked_by/%d", prefix, i), r, core.WantEdge, inBatch))
	}
	for i, r := range blocks {
		bl = append(bl, s.ref(fmt.Sprintf("%s/blocks/%d", prefix, i), r, core.WantEdge, inBatch))
	}
	return bb, bl
}

func edgeInputs(task int64, refs []core.Ref, blocks bool, prefix string) []edgeIn {
	out := make([]edgeIn, len(refs))
	name := "blocked_by"
	if blocks {
		name = "blocks"
	}
	for i, r := range refs {
		out[i] = edgeIn{task: task, ref: r, blocks: blocks, pointer: fmt.Sprintf("%s/%s/%d", prefix, name, i)}
	}
	return out
}

// resolveFolder resolves a folder-ref whose shape is checked; a refusal
// that is invalid-input points at pointer.
func (c *Tree) resolveFolder(ref core.Ref, pointer string) (int64, error) {
	r, err := core.Resolve(c, ref, core.WantFolder, nil)
	if err != nil {
		return 0, pointAt(err, pointer, false)
	}
	return r.ID, nil
}

// CreateTask makes a task in a folder or the root, with its edges. A gate
// an edge names is made.
func (t *Tree) CreateTask(in TaskInput) (out TaskCreated, err error) {
	err = t.change(func(c *Tree) error {
		folderRef := in.Folder
		if folderRef == "" {
			folderRef = "/"
		}
		var s shape
		title := s.title("/title", in.Title)
		fref := s.ref("/folder", folderRef, core.WantFolder, false)
		s.priority("/priority", in.Priority)
		tags := s.tags("/tags", in.Tags)
		bb, bl := s.edgeRefs("", in.BlockedBy, in.Blocks, false)
		if err := s.done(nil); err != nil {
			return err
		}
		folder, err := c.resolveFolder(fref, "/folder")
		if err != nil {
			return err
		}
		before := c.readySet()
		x := c.newTask(title, in.Priority, tags, in.Extra, folder)
		edges, made, err := c.resolveEdges(slices.Concat(
			edgeInputs(x.ID, bb, false, ""),
			edgeInputs(x.ID, bl, true, "")), nil, true, false)
		if err != nil {
			return err
		}
		warnings, _, err := c.addEdges(edges)
		if err != nil {
			return err
		}
		out = TaskCreated{ID: x.ID, GatesMade: made, Reblocked: c.reblocked(before)}
		out.Warnings = append(warnings, c.claimedBlocked(out.Reblocked)...)
		return nil
	})
	return out, err
}

// subjectTask checks the shape of a task subject, then resolves it.
func (c *Tree) subjectTask(task string) (*Task, error) {
	var s shape
	ref := s.ref("/task", task, core.WantTask, false)
	if err := s.done(nil); err != nil {
		return nil, err
	}
	return c.resolveTask(ref)
}

// resolveTask resolves a task-ref whose shape is checked.
func (c *Tree) resolveTask(ref core.Ref) (*Task, error) {
	r, err := core.Resolve(c, ref, core.WantTask, nil)
	if err != nil {
		return nil, pointAt(err, "/task", false)
	}
	return c.Tasks[r.ID], nil
}

// Update is what update changes. A nil field is left alone; Priority and
// Title apply only when their Set flag is.
type Update struct {
	SetTitle    bool
	Title       *string // a task's title can't be null; a folder's can
	SetPriority bool
	Priority    *int64

	TagsAdd        []string
	TagsRemove     []string
	TagsReplaceAll []string // nil: not given

	ExtraMerge      map[string]any
	ExtraRemove     []string
	ExtraReplaceAll map[string]any // nil: not given
}

// none reports whether an update gives nothing to change.
func (u Update) none() bool {
	return !u.SetTitle && !u.SetPriority && u.TagsAdd == nil && u.TagsRemove == nil && u.TagsReplaceAll == nil &&
		u.ExtraMerge == nil && u.ExtraRemove == nil && u.ExtraReplaceAll == nil
}

// extra checks an update's extra fields: extra_replace_all goes with
// neither of the others, and no key is both merged and removed.
func (s *shape) extra(u Update) {
	if u.ExtraReplaceAll != nil && (u.ExtraMerge != nil || u.ExtraRemove != nil) {
		s.faultf("", "extra_replace_all can't go with extra_merge or extra_remove")
	}
	merged := map[string]bool{}
	for k := range u.ExtraMerge {
		merged[k] = true
	}
	s.both("/extra_remove", "merged", u.ExtraRemove, merged)
}

// both refuses what a list removes that the same update adds, at its
// entry in the list; a duplicate is refused once, at its first.
func (s *shape) both(pointer, verb string, removed []string, added map[string]bool) {
	for i, item := range removed {
		if added[item] && !slices.Contains(removed[:i], item) {
			s.faultf(fmt.Sprintf("%s/%d", pointer, i), "%q is both %s and removed", item, verb)
		}
	}
}

func applyExtra(extra map[string]any, u Update) map[string]any {
	out := maps.Clone(extra)
	if u.ExtraReplaceAll != nil {
		out = maps.Clone(u.ExtraReplaceAll)
	}
	maps.Copy(out, u.ExtraMerge)
	for _, k := range u.ExtraRemove {
		delete(out, k)
	}
	return out
}

// subject is a task or a folder a change that takes both acts on.
type subject struct {
	id     int64
	folder bool
}

// subjectRefs reads refs, tasks and folders mixed, into s, at /refs/0 on;
// the root can't be one. verb says what is done to them.
func subjectRefs(s *shape, refs []string, verb string) []core.Ref {
	out := make([]core.Ref, len(refs))
	for i, raw := range refs {
		at := fmt.Sprintf("/refs/%d", i)
		out[i] = s.ref(at, raw, core.WantRef, false)
		if out[i].IsRoot() {
			s.faultf(at, "the root can't be %s", verb)
		}
	}
	if len(refs) == 0 {
		s.faultf("/refs", "at least one task or folder is needed")
	}
	return out
}

// resolveSubjects resolves the references subjectRefs read, in order: one
// named twice is invalid-input at its later occurrence. With within, one
// under a folder also named is invalid-input at the one inside.
func (c *Tree) resolveSubjects(refs []core.Ref, verb string, within bool) ([]subject, error) {
	var out []subject
	for i, ref := range refs {
		at := fmt.Sprintf("/refs/%d", i)
		r, err := core.Resolve(c, ref, core.WantRef, nil)
		if err != nil {
			return nil, pointAt(err, at, false)
		}
		if r.Kind == core.ResolvedRoot {
			return nil, problem(invalid("the root can't be %s", verb), "%s", at)
		}
		if slices.ContainsFunc(out, func(x subject) bool { return x.id == r.ID }) {
			return nil, problem(&core.Error{Kind: core.KindInvalidInput, Message: fmt.Sprintf("@%d is given twice", r.ID)}, "%s", at)
		}
		out = append(out, subject{id: r.ID, folder: r.Kind == core.ResolvedFolder})
		c.subject(r.ID)
	}
	if !within {
		return out, nil
	}
	return out, c.within(out)
}

// within refuses a subject inside a folder also given, at the inner one.
func (c *Tree) within(out []subject) error {
	var inside *core.Error
	for i, x := range out {
		for _, g := range out {
			if g.folder && c.Under(x.id, g.id) {
				reason := fmt.Sprintf("@%d is inside @%d, also given", x.id, g.id)
				if inside == nil {
					inside = &core.Error{Kind: core.KindInvalidInput, Message: reason}
				}
				problem(inside, "/refs/%d", i)
				break
			}
		}
	}
	if inside != nil {
		return inside
	}
	return nil
}

// firstOf is the first subject of a kind, or false when none is.
func firstOf(subs []subject, folder bool) (int64, bool) {
	for _, x := range subs {
		if x.folder == folder {
			return x.id, true
		}
	}
	return 0, false
}

// Update changes the fields of tasks and folders, mixed, as one change. A
// folder has no priority and no tags, and its title can be cleared; a
// task's can't. It returns the subjects whose fields changed, in input
// order; a task's updated_at moves only then.
func (t *Tree) Update(refs []string, u Update) (updated []int64, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		rs := subjectRefs(&s, refs, "updated")
		if u.none() {
			s.faultf("", "nothing to change")
		}
		var title *string
		if u.SetTitle {
			title = s.folderTitle("/title", u.Title)
		}
		if u.SetPriority {
			s.priority("/priority", u.Priority)
		}
		add := s.tags("/tags_add", u.TagsAdd)
		remove := s.tags("/tags_remove", u.TagsRemove)
		replace := s.tags("/tags_replace_all", u.TagsReplaceAll)
		if u.TagsReplaceAll != nil && (u.TagsAdd != nil || u.TagsRemove != nil) {
			s.faultf("", "tags_replace_all can't go with tags_add or tags_remove")
		}
		s.both("/tags_remove", "added", u.TagsRemove, add)
		s.extra(u)
		if err := s.done(nil); err != nil {
			return err
		}
		subs, err := c.resolveSubjects(rs, "updated", false)
		if err != nil {
			return err
		}
		var k shape
		if id, ok := firstOf(subs, false); ok && u.SetTitle && title == nil {
			k.faultIDs("/title", idsOf(subs, false), "@%d is a task, whose title can't be null", id)
		}
		if id, ok := firstOf(subs, true); ok {
			if u.SetPriority {
				k.faultIDs("/priority", idsOf(subs, true), "@%d is a folder, which has no priority", id)
			}
			for _, f := range []struct {
				name string
				tags []string
			}{{"tags_add", u.TagsAdd}, {"tags_remove", u.TagsRemove}, {"tags_replace_all", u.TagsReplaceAll}} {
				if f.tags != nil {
					k.faultIDs("/"+f.name, idsOf(subs, true), "@%d is a folder, which has no tags", id)
				}
			}
		}
		if k.err != nil {
			return k.err
		}
		updated = []int64{}
		for _, sub := range subs {
			var changed bool
			if sub.folder {
				f := c.Folders[sub.id]
				before := *f
				if u.SetTitle {
					f.Title = title
				}
				f.Extra = applyExtra(f.Extra, u)
				changed = !equalPtr(f.Title, before.Title) || !equalJSON(f.Extra, before.Extra)
			} else {
				x := c.Tasks[sub.id]
				before := *x
				if u.SetTitle {
					x.Title = *title
				}
				if u.SetPriority {
					x.Priority = u.Priority
				}
				tags := maps.Clone(x.Tags)
				if u.TagsReplaceAll != nil {
					tags = maps.Clone(replace)
				}
				maps.Copy(tags, add)
				for tag := range remove {
					delete(tags, tag)
				}
				x.Tags = tags
				x.Extra = applyExtra(x.Extra, u)
				changed = x.Title != before.Title || !equalPtr(x.Priority, before.Priority) ||
					!maps.Equal(x.Tags, before.Tags) || !equalJSON(x.Extra, before.Extra)
				if changed {
					x.UpdatedAt = c.now
				}
			}
			if changed {
				updated = append(updated, sub.id)
			}
		}
		return nil
	})
	return updated, err
}

// EdgeResult is what block and unblock return.
type EdgeResult struct {
	ID        int64
	Changed   bool
	GatesMade []GateMade // block only
	Reblocked []int64    // block only
	Released  []int64    // unblock only
	Warnings  []Warning
}

// edgeChange checks the shape of block's or unblock's input, then
// resolves its task. When make is set, a gate the subject names that its
// folder lacks is made, and returned.
func (c *Tree) edgeChange(task string, blockedBy, blocks []string, make bool) (*Task, []core.Ref, []core.Ref, []GateMade, error) {
	var s shape
	ref := s.ref("/task", task, core.WantTask, false)
	if len(blockedBy)+len(blocks) == 0 {
		s.faultf("", "at least one edge is needed")
	}
	bb, bl := s.edgeRefs("", blockedBy, blocks, false)
	if err := s.done(nil); err != nil {
		return nil, nil, nil, nil, err
	}
	if !make || ref.End == core.EndNone {
		x, err := c.resolveTask(ref)
		return x, bb, bl, nil, err
	}
	// With its end given, the subject resolves as an edge does, so a gate
	// the folder lacks is found missing rather than not-found.
	r, err := core.Resolve(c, ref, core.WantEdge, nil)
	if err != nil {
		return nil, nil, nil, nil, pointAt(err, "/task", false)
	}
	if !r.MissingGate() {
		return c.Tasks[r.ID], bb, bl, nil, nil
	}
	g := c.makeGate(r.Folder, r.End)
	return c.Tasks[g.ID], bb, bl, []GateMade{g}, nil
}

// Block adds edges: task waits on each of blockedBy, and each of blocks
// waits on task. A gate an edge names is made, the subject included,
// whose gate gets its ID first.
func (t *Tree) Block(task string, blockedBy, blocks []string) (out EdgeResult, err error) {
	err = t.change(func(c *Tree) error {
		before := c.readySet()
		x, bb, bl, subject, err := c.edgeChange(task, blockedBy, blocks, true)
		if err != nil {
			return err
		}
		edges, made, err := c.resolveEdges(slices.Concat(
			edgeInputs(x.ID, bb, false, ""),
			edgeInputs(x.ID, bl, true, "")), nil, true, false)
		if err != nil {
			return err
		}
		warnings, changed, err := c.addEdges(edges)
		if err != nil {
			return err
		}
		out = EdgeResult{ID: x.ID, Changed: changed, GatesMade: append(subject, made...), Reblocked: c.reblocked(before)}
		out.Warnings = append(warnings, c.claimedBlocked(out.Reblocked)...)
		return nil
	})
	return out, err
}

// Unblock removes edges. It never makes a gate.
func (t *Tree) Unblock(task string, blockedBy, blocks []string) (out EdgeResult, err error) {
	err = t.change(func(c *Tree) error {
		x, bb, bl, _, err := c.edgeChange(task, blockedBy, blocks, false)
		if err != nil {
			return err
		}
		before, existed := c.readySet(), c.taskIDs()
		edges, _, err := c.resolveEdges(slices.Concat(
			edgeInputs(x.ID, bb, false, ""),
			edgeInputs(x.ID, bl, true, "")), nil, false, false)
		if err != nil {
			return err
		}
		out.ID = x.ID
		for _, e := range edges {
			from := c.Tasks[e.From]
			if from.BlockedBy[e.To] {
				delete(from.BlockedBy, e.To)
				c.edged(e.Edge)
				from.UpdatedAt = c.now
				out.Changed = true
			}
		}
		out.Released = c.released(before, existed)
		return nil
	})
	return out, err
}

// CompleteResult is what complete and reopen return.
type CompleteResult struct {
	Changed   []int64 // the tasks completed or reopened, in input order
	Unclaimed []int64 // complete only: those completed whose claim it ended, in Changed's order
	Released  []int64 // complete only
	Reblocked []int64 // reopen only
	Warnings  []Warning
}

// subjectTasks resolves a list of tasks, in order: a task named twice is
// invalid-input at its later occurrence.
func (c *Tree) subjectTasks(tasks []string) ([]*Task, error) {
	var s shape
	refs := taskRefs(&s, tasks)
	if err := s.done(nil); err != nil {
		return nil, err
	}
	return c.resolveTasks(refs)
}

// taskRefs reads a list of tasks into s, at /tasks/0 on.
func taskRefs(s *shape, tasks []string) []core.Ref {
	refs := make([]core.Ref, len(tasks))
	for i, task := range tasks {
		refs[i] = s.ref(fmt.Sprintf("/tasks/%d", i), task, core.WantTask, false)
	}
	if len(tasks) == 0 {
		s.faultf("/tasks", "at least one task is needed")
	}
	return refs
}

// resolveTasks resolves a list of tasks read by taskRefs, in order.
func (c *Tree) resolveTasks(refs []core.Ref) ([]*Task, error) {
	var out []*Task
	for i, ref := range refs {
		at := fmt.Sprintf("/tasks/%d", i)
		r, err := core.Resolve(c, ref, core.WantTask, nil)
		if err != nil {
			return nil, pointAt(err, at, false)
		}
		x := c.Tasks[r.ID]
		if slices.Contains(out, x) {
			return nil, problem(&core.Error{Kind: core.KindInvalidInput, Message: fmt.Sprintf("@%d given twice", x.ID)}, "%s", at)
		}
		out = append(out, x)
		c.subject(x.ID)
	}
	return out, nil
}

// Complete marks tasks complete and ends their claims, as one change.
func (t *Tree) Complete(tasks ...string) (out CompleteResult, err error) {
	err = t.change(func(c *Tree) error {
		xs, err := c.subjectTasks(tasks)
		if err != nil {
			return err
		}
		before, existed := c.readySet(), c.taskIDs()
		now := c.now
		out.Changed, out.Unclaimed = []int64{}, []int64{}
		for _, x := range xs {
			if !x.Open() {
				continue
			}
			x.CompletedAt, x.UpdatedAt = &now, now
			if c.claimed(x.ID) {
				delete(c.Claims, x.ID)
				out.Unclaimed = append(out.Unclaimed, x.ID)
			}
			out.Changed = append(out.Changed, x.ID)
		}
		out.Released = c.released(before, existed)
		return nil
	})
	return out, err
}

// Reopen marks tasks open again, as one change; whatever waits on them
// waits again.
func (t *Tree) Reopen(tasks ...string) (out CompleteResult, err error) {
	err = t.change(func(c *Tree) error {
		xs, err := c.subjectTasks(tasks)
		if err != nil {
			return err
		}
		before := c.readySet()
		out.Changed = []int64{}
		for _, x := range xs {
			if x.Open() {
				continue
			}
			x.CompletedAt, x.UpdatedAt = nil, c.now
			out.Changed = append(out.Changed, x.ID)
		}
		out.Reblocked = c.reblocked(before)
		out.Warnings = c.claimedBlocked(out.Reblocked)
		return nil
	})
	return out, err
}

// MoveResult is what mv returns.
type MoveResult struct {
	// Moved is the tasks and folders that moved, in input order: a
	// folder at its new path, a task at the path of the folder it is now
	// in.
	Moved    []FolderAt
	Warnings []Warning
}

// Mv is how mv reads its destination.
type Mv int

const (
	// MvDest is a folder that exists, to move into; otherwise the one
	// folder's new path.
	MvDest Mv = iota
	// MvInto is the folder to move into: -t.
	MvInto
	// MvRename is the one folder's new path, whatever exists: -T.
	MvRename
)

// Move moves tasks and folders, mixed, into a folder that exists, or
// renames one folder to a new path whose parent exists, everything under
// each included, as one change. Nothing is replaced and no parent is
// made; a gate stays in its folder, and what is already where it would go
// doesn't move. No edge changes. A case-only rename is the one input
// allowed to name both spellings, and only of the folder's own name.
func (t *Tree) Move(mv Mv, dest string, refs ...string) (out MoveResult, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		rs := subjectRefs(&s, refs, "moved")
		pointer := "/dest"
		if mv == MvInto {
			pointer = "/into"
		}
		if mv == MvRename && len(refs) > 1 {
			s.faultf("/refs/1", "a rename takes one folder")
		}
		to := s.ref(pointer, dest, core.WantFolder, false)
		if mv == MvRename && to.IsRoot() {
			s.faultf(pointer, "the root can't be a new path")
		}
		one := len(refs) == 1 && mv != MvInto
		if err := s.done(func(a, b core.Ref) bool { return one && respells(a, b) }); err != nil {
			return err
		}
		subs, err := c.resolveSubjects(rs, "moved", false)
		if err != nil {
			return err
		}
		// A gate is refused with the subjects, before the destination
		// resolves.
		for i, sub := range subs {
			if x := c.Tasks[sub.id]; !sub.folder && x.Gate != core.EndNone {
				return &core.Error{Kind: core.KindInvalidInput, IDs: []int64{x.ID}, Problems: []string{fmt.Sprintf("/refs/%d", i)},
					Message: fmt.Sprintf("@%d is a gate, and stays in its folder", x.ID)}
			}
		}
		if err := c.within(subs); err != nil {
			return err
		}
		task, hasTask := firstOf(subs, false)
		if mv == MvRename && hasTask {
			e := invalid("@%d is a task, which can't be renamed", task).(*core.Error)
			e.IDs = idsOf(subs, false)
			return problem(e, "/rename")
		}
		into, rename := core.RootID, mv == MvRename
		switch mv {
		case MvInto:
			if into, err = c.resolveFolder(to, pointer); err != nil {
				return err
			}
		case MvDest:
			r, err := core.Resolve(c, to, core.WantFolder, nil)
			switch {
			case err == nil:
				into = r.ID
			case len(subs) > 1 || hasTask || to.Kind == core.TargetID:
				return pointAt(err, pointer, false)
			default:
				rename = true
			}
		}
		var fs []*Folder
		for _, sub := range subs {
			if sub.folder {
				fs = append(fs, c.Folders[sub.id])
			}
		}
		moved := map[int64]bool{}
		if rename {
			f := fs[0]
			parent, name, err := c.newPath(to, f.ID, pointer)
			if err != nil {
				return pointAt(err, pointer, false)
			}
			path := strings.TrimSuffix(c.Path(parent), "/") + "/" + name
			if id, ok := c.Child(parent, name); ok {
				if id != f.ID {
					return core.ExistsAt(pointer, id, path, "%s is taken", path)
				}
			} else if id, spelled, ok := c.ChildFold(parent, name); ok && id != f.ID {
				is := strings.TrimSuffix(c.Path(parent), "/") + "/" + spelled
				return core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{path, is},
					"%s differs from %s only in case", path, is)
			}
			if f.Parent != parent || f.Name != name {
				f.Parent, f.Name = parent, name
				moved[f.ID] = true
			}
		} else {
			at := strings.TrimSuffix(c.Path(into), "/") + "/"
			for _, f := range fs {
				if into == f.ID || c.Under(into, f.ID) {
					return core.Conflict(core.RuleIntoItself, []int64{f.ID}, []string{at + f.Name},
						"@%d would land inside itself at %s", f.ID, at+f.Name)
				}
			}
			// What the folder holds once the move is made: what is there,
			// and each folder that lands, in input order.
			var there []*Folder
			for _, g := range c.Folders {
				if g.Parent == into {
					there = append(there, g)
				}
			}
			slices.SortFunc(there, func(a, b *Folder) int { return cmp.Compare(a.Name, b.Name) })
			var landing []*Folder
			for _, f := range fs {
				if f.Parent != into {
					landing = append(landing, f)
				}
			}
			for _, exact := range []bool{false, true} {
				held := slices.Clone(there)
				for _, f := range landing {
					for _, g := range held {
						switch {
						case !exact && g.Name != f.Name && strings.EqualFold(g.Name, f.Name):
							return core.Conflict(core.RuleCaseMismatch, []int64{g.ID}, []string{at + f.Name, at + g.Name},
								"%s differs from %s only in case", at+f.Name, at+g.Name)
						case exact && g.Name == f.Name:
							return core.ExistsAt(pointer, g.ID, at+f.Name, "%s is taken", at+f.Name)
						}
					}
					held = append(held, f)
				}
			}
			for _, f := range landing {
				f.Parent = into
				moved[f.ID] = true
			}
			for _, sub := range subs {
				if x := c.Tasks[sub.id]; !sub.folder && x.Folder != into {
					x.Folder = into
					moved[x.ID] = true
				}
			}
		}
		out.Moved = []FolderAt{}
		var claimed []int64
		for _, sub := range subs {
			if !moved[sub.id] {
				continue
			}
			if !sub.folder {
				out.Moved = append(out.Moved, FolderAt{ID: sub.id, Path: c.Path(into)})
				if c.claimed(sub.id) {
					claimed = append(claimed, sub.id)
				}
				continue
			}
			out.Moved = append(out.Moved, FolderAt{ID: sub.id, Path: c.Path(sub.id)})
			for id := range c.Claims {
				if c.Under(id, sub.id) {
					claimed = append(claimed, id)
				}
			}
		}
		if claimed != nil {
			slices.Sort(claimed)
			out.Warnings = []Warning{{Kind: "moved-claimed", IDs: claimed}}
		}
		return nil
	})
	return out, err
}

// newPath finds where a rename puts a folder: the folder its new path's
// parent names, which must exist, and the new name. A bare ID names a
// folder that exists, so its path is the new one. Walking the parent
// through the folder itself, or anchoring inside it, is into-itself, at
// the new path from the root.
func (c *Tree) newPath(ref core.Ref, self int64, pointer string) (int64, string, error) {
	dest := ""
	inside := func(at int64) error {
		if at == self || c.Under(at, self) {
			return core.Conflict(core.RuleIntoItself, []int64{self}, []string{dest},
				"@%d would land inside itself at %s", self, dest)
		}
		return nil
	}
	if ref.Kind == core.TargetID {
		r, err := core.Resolve(c, ref, core.WantFolder, nil)
		if err != nil {
			return 0, "", pointAt(err, pointer, false)
		}
		g := c.Folders[r.ID]
		if g.ID == self {
			return g.Parent, g.Name, nil
		}
		dest = c.Path(g.ID)
		return g.Parent, g.Name, inside(g.Parent)
	}
	at, given := core.RootID, ""
	if ref.Kind == core.TargetAnchored {
		anchor := ref
		anchor.Names = nil
		r, err := core.Resolve(c, anchor, core.WantFolder, nil)
		if err != nil {
			return 0, "", pointAt(err, pointer, false)
		}
		at, given = r.ID, strings.SplitN(ref.Raw, "/", 2)[0]
	}
	dest = strings.TrimSuffix(c.Path(at), "/") + "/" + strings.Join(ref.Names, "/")
	n := len(ref.Names)
	for _, name := range ref.Names[:n-1] {
		if err := inside(at); err != nil {
			return 0, "", err
		}
		given += "/" + name
		if id, ok := c.Child(at, name); ok {
			at = id
			continue
		}
		if id, spelled, ok := c.ChildFold(at, name); ok {
			is := strings.TrimSuffix(c.Path(at), "/") + "/" + spelled
			return 0, "", core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{given, is},
				"%s matches %s only ignoring case", given, is)
		}
		return 0, "", core.NotFound(nil, []string{given}, "no folder at %s", given)
	}
	return at, ref.Names[n-1], inside(at)
}

// respells reports whether a destination only respells its subject's own
// name: the same path from the same start but for the case of its last
// name.
func respells(subject, dest core.Ref) bool {
	n := len(subject.Names)
	return subject.Kind == dest.Kind && subject.ID == dest.ID && n > 0 && len(dest.Names) == n &&
		(subject.Kind == core.TargetPath || subject.Kind == core.TargetAnchored) &&
		slices.Equal(subject.Names[:n-1], dest.Names[:n-1]) && strings.EqualFold(subject.Names[n-1], dest.Names[n-1])
}

// remove takes tasks and folders out of the tree with every edge to and
// from them (I5), and their claims. It returns the edges removed.
func (c *Tree) remove(tasks, folders []int64) []core.Edge {
	edges := c.touching(tasks)
	gone := map[int64]bool{}
	for _, id := range tasks {
		gone[id] = true
	}
	for _, x := range c.Tasks {
		for to := range x.BlockedBy {
			if !gone[x.ID] && gone[to] {
				delete(x.BlockedBy, to)
				x.UpdatedAt = c.now
			}
		}
	}
	for _, id := range tasks {
		delete(c.Tasks, id)
		delete(c.Claims, id)
	}
	for _, id := range folders {
		delete(c.Folders, id)
	}
	return edges
}

// touching is every edge with an end in tasks, sorted by the task that
// waits, then the one it waits on.
func (c *Tree) touching(tasks []int64) []core.Edge {
	gone := map[int64]bool{}
	for _, id := range tasks {
		gone[id] = true
	}
	var edges []core.Edge
	for _, x := range c.Tasks {
		for to := range x.BlockedBy {
			if gone[x.ID] || gone[to] {
				edges = append(edges, core.Edge{From: x.ID, To: to})
			}
		}
	}
	slices.SortFunc(edges, func(a, b core.Edge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return edges
}

// under lists the tasks and folders under a folder at any depth, itself
// included.
func (c *Tree) under(folder int64) (tasks, folders []int64) {
	for id := range c.Tasks {
		if c.Under(id, folder) {
			tasks = append(tasks, id)
		}
	}
	for id := range c.Folders {
		if id == folder || c.Under(id, folder) {
			folders = append(folders, id)
		}
	}
	slices.Sort(tasks)
	slices.Sort(folders)
	return tasks, folders
}

// Removed is what a delete returns.
type Removed struct {
	Deleted   []int64 // the subjects, in input order
	Removed   []int64 // every task and folder removed, ascending
	Unclaimed []int64 // the tasks removed that had a claim, ascending
	Released  []int64
}

// Delete removes tasks and folders, mixed, as one change: a task with
// its claim and every edge to and from it, a folder with everything under
// it, gates included. It never splices: what waited on them isn't passed
// their blockers. contents and material are nil when not given; either
// given with no folder among the subjects is invalid-input.
func (t *Tree) Delete(refs []string, contents, material *bool) (out Removed, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		rs := subjectRefs(&s, refs, "deleted")
		if err := s.done(nil); err != nil {
			return err
		}
		subs, err := c.resolveSubjects(rs, "deleted", true)
		if err != nil {
			return err
		}
		if _, ok := firstOf(subs, true); !ok {
			var k shape
			if contents != nil {
				k.faultIDs("/contents", idsOf(subs, false), "contents is for folders, and no folder is given")
			}
			if material != nil {
				k.faultIDs("/material", idsOf(subs, false), "material is for folders, and no folder is given")
			}
			if k.err != nil {
				return k.err
			}
		}
		var tasks, fs []int64
		for _, sub := range subs {
			if sub.folder {
				fs = append(fs, sub.id)
			} else {
				tasks = append(tasks, sub.id)
			}
		}
		if contents == nil || !*contents {
			var direct []int64
			for _, f := range fs {
				for _, x := range c.Tasks {
					if x.Folder == f && x.Gate == core.EndNone {
						direct = append(direct, x.ID)
					}
				}
				for _, g := range c.Folders {
					if g.Parent == f {
						direct = append(direct, g.ID)
					}
				}
			}
			if direct != nil {
				slices.Sort(direct)
				return core.Conflict(core.RuleNotEmpty, direct, nil, "%s are in folders given", core.FormatIDs(direct))
			}
		}
		var gone []int64
		var found []string
		for _, f := range fs {
			ts, under := c.under(f)
			tasks, gone = append(tasks, ts...), append(gone, under...)
			if material != nil && *material {
				continue
			}
			// In tree order: each folder's own entries, byte for byte,
			// before those of the folders under it. .DS_Store is never
			// material to the check.
			in := map[int64]bool{}
			for _, id := range under {
				in[id] = true
			}
			for _, id := range c.TreeOrder() {
				if in[id] {
					for _, name := range sortedKeys(c.Folders[id].Material) {
						if name != ".DS_Store" {
							found = append(found, c.Path(id)+":"+name)
						}
					}
				}
			}
		}
		if found != nil {
			return core.Conflict(core.RuleHasMaterial, nil, found, "the folders hold material")
		}
		out = Removed{Deleted: []int64{}, Unclaimed: []int64{}}
		for _, sub := range subs {
			out.Deleted = append(out.Deleted, sub.id)
		}
		for _, id := range tasks {
			if c.claimed(id) {
				out.Unclaimed = append(out.Unclaimed, id)
			}
		}
		slices.Sort(out.Unclaimed)
		before, existed := c.readySet(), c.taskIDs()
		c.remove(tasks, gone)
		out.Removed, out.Released = slices.Sorted(slices.Values(slices.Concat(tasks, gone))), c.released(before, existed)
		return nil
	})
	return out, err
}

// archived is what archiving an item keeps: its tasks and folders, and
// every edge they had, to or from anything. It changes nothing.
func (c *Tree) archived(id int64, tasks, folders []int64) Archived {
	a := Archived{ID: id, Edges: c.touching(tasks)}
	for _, t := range tasks {
		x := *c.Tasks[t]
		x.BlockedBy, x.Tags = maps.Clone(x.BlockedBy), maps.Clone(x.Tags)
		a.Tasks = append(a.Tasks, x)
	}
	for _, f := range folders {
		y := *c.Folders[f]
		y.Material = maps.Clone(y.Material)
		a.Folders = append(a.Folders, y)
	}
	return a
}

// Archive archives complete tasks, gates included, and finished folders
// with everything under them, mixed, as one change: if any is refused,
// none goes. Each archive keeps every edge its item had, to or from
// anything, those to the others archived with it included. It returns
// the subjects archived, in input order, and every ID that left.
func (t *Tree) Archive(refs ...string) (archived, left []int64, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		rs := subjectRefs(&s, refs, "archived")
		if err := s.done(nil); err != nil {
			return err
		}
		subs, err := c.resolveSubjects(rs, "archived", true)
		if err != nil {
			return err
		}
		var as []Archived
		var open []int64
		for _, sub := range subs {
			tasks, folders := []int64{sub.id}, []int64(nil)
			if sub.folder {
				tasks, folders = c.under(sub.id)
			}
			for _, id := range tasks {
				if c.Tasks[id].Open() {
					open = append(open, id)
				}
			}
			as = append(as, c.archived(sub.id, tasks, folders))
		}
		if open != nil {
			slices.Sort(open)
			return core.Conflict(core.RuleNotFinished, open, nil, "%s is open", core.FormatIDs(open))
		}
		archived = []int64{}
		for _, a := range as {
			var tasks, folders []int64
			for _, x := range a.Tasks {
				tasks = append(tasks, x.ID)
			}
			for _, y := range a.Folders {
				folders = append(folders, y.ID)
			}
			c.remove(tasks, folders)
			c.Archives = append(c.Archives, a)
			for _, l := range slices.Concat(tasks, folders) {
				c.Index[l] = len(c.Archives) - 1
			}
			archived = append(archived, a.ID)
			left = append(left, slices.Concat(tasks, folders)...)
		}
		slices.Sort(left)
		return nil
	})
	return archived, left, err
}

// ClaimTask claims open tasks with no claim, blocked or not, as one
// change: if any is refused, none is claimed. extra is kept with every
// claim. It returns the tasks claimed, in input order.
func (t *Tree) ClaimTask(tasks []string, extra map[string]any) (claimed []int64, err error) {
	err = t.change(func(c *Tree) error {
		xs, err := c.subjectTasks(tasks)
		if err != nil {
			return err
		}
		claimed = []int64{}
		for _, x := range xs {
			if c.claimed(x.ID) {
				return core.Conflict(core.RuleClaimed, []int64{x.ID}, nil, "@%d is claimed", x.ID)
			}
			if !x.Open() {
				return core.Conflict(core.RuleAlreadyComplete, []int64{x.ID}, nil, "@%d is complete", x.ID)
			}
			c.claim(x.ID, extra)
			claimed = append(claimed, x.ID)
		}
		return nil
	})
	return claimed, err
}

func (c *Tree) claim(id int64, extra map[string]any) Claim {
	cl := &Claim{Task: id, ClaimedAt: c.now, Extra: orEmpty(extra)}
	c.Claims[id] = cl
	return *cl
}

// Scope is where claim-next looks, as task-list's filter says.
type Scope struct {
	Folder  string // a folder-ref; empty means the root
	Direct  bool
	TagsAny []string
	TagsAll []string
}

// ClaimNext claims the first task of the frontier within scope. It returns
// nil when there is none.
func (t *Tree) ClaimNext(s Scope, extra map[string]any) (claim *Claim, err error) {
	err = t.change(func(c *Tree) error {
		ids, err := c.ScopedFrontier(s)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			c.subject(ids[0])
			cl := c.claim(ids[0], extra)
			claim = &cl
		}
		return nil
	})
	return claim, err
}

// ScopedFrontier is the frontier, cut to a scope. Scope chooses where to
// look, never what is ready.
func (t *Tree) ScopedFrontier(s Scope) ([]int64, error) {
	ref := s.Folder
	if ref == "" {
		ref = "/"
	}
	var sh shape
	fref := sh.ref("/folder", ref, core.WantFolder, false)
	anyTags := sh.tags("/tags_any", s.TagsAny)
	allTags := sh.tags("/tags_all", s.TagsAll)
	if err := sh.done(nil); err != nil {
		return nil, err
	}
	folder, err := t.resolveFolder(fref, "/folder")
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, id := range t.Frontier() {
		x := t.Tasks[id]
		if !s.Direct && !t.Under(id, folder) || s.Direct && x.Folder != folder {
			continue
		}
		if len(anyTags) > 0 && !hasAny(x.Tags, anyTags) || !hasAll(x.Tags, allTags) {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

func hasAny(have, want map[string]bool) bool {
	for t := range want {
		if have[t] {
			return true
		}
	}
	return false
}

func hasAll(have, want map[string]bool) bool {
	for t := range want {
		if !have[t] {
			return false
		}
	}
	return true
}

// Unclaim ends the claims of tasks, whoever made them, as one change.
// It returns the tasks that had one, in input order.
func (t *Tree) Unclaim(tasks ...string) (unclaimed []int64, err error) {
	err = t.change(func(c *Tree) error {
		xs, err := c.subjectTasks(tasks)
		if err != nil {
			return err
		}
		unclaimed = []int64{}
		for _, x := range xs {
			if _, ok := c.Claims[x.ID]; ok {
				delete(c.Claims, x.ID)
				unclaimed = append(unclaimed, x.ID)
			}
		}
		return nil
	})
	return unclaimed, err
}

func equalPtr[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// equalJSON reports whether two extras would print the same. koan never
// interprets extra, so a number spelled differently, such as 1.0 for 1,
// given as a json.Number, is a change.
func equalJSON(a, b map[string]any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// idsOf is the IDs of the subjects of a kind, in input order.
func idsOf(subs []subject, folder bool) []int64 {
	var out []int64
	for _, s := range subs {
		if s.folder == folder {
			out = append(out, s.id)
		}
	}
	return out
}
