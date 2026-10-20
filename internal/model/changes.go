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
func (c *Tree) createFolder(ref core.Ref, title *string, extra map[string]any) ([]FolderAt, error) {
	n := len(ref.Names)
	parent, made, err := c.parents(ref.Names[:n-1], 0)
	if err != nil {
		return nil, err
	}
	name, path := ref.Names[n-1], ref.TargetString()
	if _, ok := c.Child(parent, name); ok {
		return nil, core.Conflict(core.RuleExists, nil, []string{path}, "%s is taken", path)
	}
	if id, spelled, ok := c.ChildFold(parent, name); ok {
		is := strings.TrimSuffix(c.Path(parent), "/") + "/" + spelled
		return nil, core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{path, is},
			"%s differs from %s only in case", path, is)
	}
	f := c.makeFolder(parent, name)
	f.Title, f.Extra = title, orEmpty(extra)
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
		made, err = c.createFolder(ref, title, extra)
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
// is set, and refusing it as not-found otherwise. In a batch, a refusal
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
				p := c.Path(r.Folder) + "." + string(r.End)
				return nil, nil, pointAt(core.NotFound(nil, []string{p}, "@%d (%s) has no %s gate", r.Folder, c.Path(r.Folder), r.End), e.pointer, batch)
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

// Update is what task update and folder update change. A nil field is left
// alone; Priority and Title apply only when their Set flag is.
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

// extra checks an update's extra fields: extra_remove holds no key twice,
// and extra_replace_all goes with neither of the others.
func (s *shape) extra(u Update) {
	s.unique("/extra_remove", u.ExtraRemove)
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

// UpdateTask changes a task's title, priority, tags or extra. It reports
// whether anything changed; updated_at moves only then.
func (t *Tree) UpdateTask(task string, u Update) (changed bool, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		ref := s.ref("/task", task, core.WantTask, false)
		if u.none() {
			s.faultf("", "nothing to change")
		}
		var title string
		if u.SetTitle {
			if u.Title == nil {
				s.faultf("/title", "a task's title can't be null")
			} else {
				title = s.title("/title", *u.Title)
			}
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
		x, err := c.resolveTask(ref)
		if err != nil {
			return err
		}
		before := *x
		if u.SetTitle {
			x.Title = title
		}
		if u.SetPriority {
			x.Priority = u.Priority
		}
		tags := maps.Clone(x.Tags)
		if u.TagsReplaceAll != nil {
			tags = replace
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
		return nil
	})
	return changed, err
}

// subjectFolder checks the shape of a folder subject, then resolves it.
func (c *Tree) subjectFolder(folder string) (*Folder, error) {
	var s shape
	ref := s.ref("/folder", folder, core.WantFolder, false)
	if err := s.done(nil); err != nil {
		return nil, err
	}
	return c.resolveSubjectFolder(ref)
}

// resolveSubjectFolder resolves a folder subject whose shape is checked;
// the root can be read but not changed.
func (c *Tree) resolveSubjectFolder(ref core.Ref) (*Folder, error) {
	r, err := core.Resolve(c, ref, core.WantFolder, nil)
	if err != nil {
		return nil, pointAt(err, "/folder", false)
	}
	if r.Kind == core.ResolvedRoot {
		return nil, problem(invalid("the root can't be changed"), "/folder")
	}
	return c.Folders[r.ID], nil
}

// UpdateFolder changes a folder's title or extra.
func (t *Tree) UpdateFolder(folder string, u Update) (changed bool, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		ref := s.ref("/folder", folder, core.WantFolder, false)
		if u.none() {
			s.faultf("", "nothing to change")
		}
		var title *string
		if u.SetTitle {
			title = s.folderTitle("/title", u.Title)
		}
		if u.SetPriority {
			s.faultf("/priority", "a folder has no priority")
		}
		for _, f := range []struct {
			name string
			tags []string
		}{{"tags_add", u.TagsAdd}, {"tags_remove", u.TagsRemove}, {"tags_replace_all", u.TagsReplaceAll}} {
			if f.tags != nil {
				s.faultf("/"+f.name, "a folder has no tags")
			}
		}
		s.extra(u)
		if err := s.done(nil); err != nil {
			return err
		}
		f, err := c.resolveSubjectFolder(ref)
		if err != nil {
			return err
		}
		before := *f
		if u.SetTitle {
			f.Title = title
		}
		f.Extra = applyExtra(f.Extra, u)
		changed = !equalPtr(f.Title, before.Title) || !equalJSON(f.Extra, before.Extra)
		return nil
	})
	return changed, err
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
	ID        int64
	Changed   bool
	Released  []int64 // complete only
	Reblocked []int64 // reopen only
	Warnings  []Warning
}

// Complete marks a task complete and ends its claim.
func (t *Tree) Complete(task string) (out CompleteResult, err error) {
	err = t.change(func(c *Tree) error {
		x, err := c.subjectTask(task)
		if err != nil {
			return err
		}
		out.ID = x.ID
		if !x.Open() {
			return nil
		}
		before, existed := c.readySet(), c.taskIDs()
		now := c.now
		x.CompletedAt, x.UpdatedAt = &now, now
		delete(c.Claims, x.ID)
		out.Changed, out.Released = true, c.released(before, existed)
		return nil
	})
	return out, err
}

// Reopen marks a task open again; whatever waits on it waits again.
func (t *Tree) Reopen(task string) (out CompleteResult, err error) {
	err = t.change(func(c *Tree) error {
		x, err := c.subjectTask(task)
		if err != nil {
			return err
		}
		out.ID = x.ID
		if x.Open() {
			return nil
		}
		before := c.readySet()
		x.CompletedAt, x.UpdatedAt = nil, c.now
		out.Changed, out.Reblocked = true, c.reblocked(before)
		out.Warnings = c.claimedBlocked(out.Reblocked)
		return nil
	})
	return out, err
}

// MoveTask puts a task in another folder or the root. No edge changes.
func (t *Tree) MoveTask(task, to string) (changed bool, warnings []Warning, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		tref := s.ref("/task", task, core.WantTask, false)
		dest := s.ref("/to", to, core.WantFolder, false)
		if err := s.done(nil); err != nil {
			return err
		}
		x, err := c.resolveTask(tref)
		if err != nil {
			return err
		}
		folder, err := c.resolveFolder(dest, "/to")
		if err != nil {
			return err
		}
		if x.Gate != core.EndNone {
			return core.Conflict(core.RuleGate, []int64{x.ID}, nil, "@%d is a gate, and stays in its folder", x.ID)
		}
		if folder == x.Folder {
			return nil
		}
		x.Folder, changed = folder, true
		if c.claimed(x.ID) {
			warnings = []Warning{{Kind: "moved-claimed", IDs: []int64{x.ID}}}
		}
		return nil
	})
	return changed, warnings, err
}

// FolderMoved is what folder move returns.
type FolderMoved struct {
	ID       int64
	Changed  bool
	Path     string
	Created  []FolderAt
	Warnings []Warning
}

// MoveFolder moves or renames a folder to its whole new path, everything
// under it included. Missing parents are made. A case-only rename is a
// move, and the one input allowed to name both spellings, and only of the
// folder's own name.
func (t *Tree) MoveFolder(folder, to string) (out FolderMoved, err error) {
	err = t.change(func(c *Tree) error {
		var s shape
		subject := s.ref("/folder", folder, core.WantFolder, false)
		dest := s.path("/to", to)
		if err := s.done(respells); err != nil {
			return err
		}
		f, err := c.resolveSubjectFolder(subject)
		if err != nil {
			return err
		}
		n := len(dest.Names)
		parent, made, err := c.parents(dest.Names[:n-1], f.ID)
		if err != nil {
			return err
		}
		name, path := dest.Names[n-1], dest.TargetString()
		out.ID, out.Created = f.ID, made
		if id, ok := c.Child(parent, name); ok {
			if id != f.ID {
				return core.Conflict(core.RuleExists, nil, []string{path}, "%s is taken", path)
			}
		} else if id, spelled, ok := c.ChildFold(parent, name); ok && id != f.ID {
			is := strings.TrimSuffix(c.Path(parent), "/") + "/" + spelled
			return core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{path, is},
				"%s differs from %s only in case", path, is)
		}
		if f.Parent != parent || f.Name != name {
			f.Parent, f.Name, out.Changed = parent, name, true
		}
		out.Path = c.Path(f.ID)
		if out.Changed {
			var ids []int64
			for id := range c.Claims {
				if c.Under(id, f.ID) {
					ids = append(ids, id)
				}
			}
			if ids != nil {
				slices.Sort(ids)
				out.Warnings = []Warning{{Kind: "moved-claimed", IDs: ids}}
			}
		}
		return nil
	})
	return out, err
}

// respells reports whether a destination only respells its subject's own
// name: the same path but for the case of its last name.
func respells(subject, dest core.Ref) bool {
	n := len(subject.Names)
	return subject.Kind == core.TargetPath && dest.Kind == core.TargetPath && n > 0 && len(dest.Names) == n &&
		slices.Equal(subject.Names[:n-1], dest.Names[:n-1]) && strings.EqualFold(subject.Names[n-1], dest.Names[n-1])
}

// remove takes tasks and folders out of the tree with every edge to and
// from them (I5), and their claims. It returns the edges removed.
func (c *Tree) remove(tasks, folders []int64) []core.Edge {
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
	Removed  []int64
	Released []int64
}

// DeleteTask removes a task, its claim and every edge to and from it. It
// never splices: what waited on it isn't passed its blockers.
func (t *Tree) DeleteTask(task string) (out Removed, err error) {
	err = t.change(func(c *Tree) error {
		x, err := c.subjectTask(task)
		if err != nil {
			return err
		}
		before, existed := c.readySet(), c.taskIDs()
		c.remove([]int64{x.ID}, nil)
		out = Removed{Removed: []int64{x.ID}, Released: c.released(before, existed)}
		return nil
	})
	return out, err
}

// DeleteFolder removes a folder and everything under it, gates included.
func (t *Tree) DeleteFolder(folder string, contents, force bool) (out Removed, err error) {
	err = t.change(func(c *Tree) error {
		f, err := c.subjectFolder(folder)
		if err != nil {
			return err
		}
		if !contents {
			var direct []int64
			for _, x := range c.Tasks {
				if x.Folder == f.ID && x.Gate == core.EndNone {
					direct = append(direct, x.ID)
				}
			}
			for _, g := range c.Folders {
				if g.Parent == f.ID {
					direct = append(direct, g.ID)
				}
			}
			if direct != nil {
				slices.Sort(direct)
				return core.Conflict(core.RuleNotEmpty, direct, nil, "@%d holds %s", f.ID, core.FormatIDs(direct))
			}
		}
		tasks, folders := c.under(f.ID)
		if !force {
			// In tree order: each folder's own entries, byte for byte,
			// before those of the folders under it. .DS_Store is never
			// material to the check.
			under := map[int64]bool{}
			for _, id := range folders {
				under[id] = true
			}
			var material []string
			for _, id := range c.TreeOrder() {
				if under[id] {
					for _, name := range sortedKeys(c.Folders[id].Material) {
						if name != ".DS_Store" {
							material = append(material, c.Path(id)+":"+name)
						}
					}
				}
			}
			if material != nil {
				return core.Conflict(core.RuleHasMaterial, nil, material, "@%d holds material", f.ID)
			}
		}
		before, existed := c.readySet(), c.taskIDs()
		c.remove(tasks, folders)
		out = Removed{Removed: slices.Sorted(slices.Values(slices.Concat(tasks, folders))), Released: c.released(before, existed)}
		return nil
	})
	return out, err
}

// archive takes tasks and folders out of the tree into the archive, and
// retires their IDs.
func (c *Tree) archive(id int64, tasks, folders []int64) []int64 {
	a := Archived{ID: id}
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
	a.Edges = c.remove(tasks, folders)
	c.Archives = append(c.Archives, a)
	left := slices.Sorted(slices.Values(slices.Concat(tasks, folders)))
	for _, l := range left {
		c.Index[l] = len(c.Archives) - 1
	}
	return left
}

// ArchiveTask archives a complete task. It returns every ID that left.
func (t *Tree) ArchiveTask(task string) (left []int64, err error) {
	err = t.change(func(c *Tree) error {
		x, err := c.subjectTask(task)
		if err != nil {
			return err
		}
		if x.Open() {
			return core.Conflict(core.RuleNotFinished, []int64{x.ID}, nil, "@%d is open", x.ID)
		}
		left = c.archive(x.ID, []int64{x.ID}, nil)
		return nil
	})
	return left, err
}

// ArchiveFolder archives a finished folder with everything under it.
func (t *Tree) ArchiveFolder(folder string) (left []int64, err error) {
	err = t.change(func(c *Tree) error {
		f, err := c.subjectFolder(folder)
		if err != nil {
			return err
		}
		tasks, folders := c.under(f.ID)
		var open []int64
		for _, id := range tasks {
			if c.Tasks[id].Open() {
				open = append(open, id)
			}
		}
		if open != nil {
			return core.Conflict(core.RuleNotFinished, open, nil, "%s is open", core.FormatIDs(open))
		}
		left = c.archive(f.ID, tasks, folders)
		return nil
	})
	return left, err
}

// ClaimTask claims an open task with no claim.
func (t *Tree) ClaimTask(task string, extra map[string]any) (claim Claim, err error) {
	err = t.change(func(c *Tree) error {
		x, err := c.subjectTask(task)
		if err != nil {
			return err
		}
		if c.claimed(x.ID) {
			return core.Conflict(core.RuleClaimed, []int64{x.ID}, nil, "@%d is claimed", x.ID)
		}
		if !x.Open() {
			return core.Conflict(core.RuleComplete, []int64{x.ID}, nil, "@%d is complete", x.ID)
		}
		claim = c.claim(x.ID, extra)
		return nil
	})
	return claim, err
}

func (c *Tree) claim(id int64, extra map[string]any) Claim {
	cl := &Claim{Task: id, ClaimedAt: c.now, Extra: orEmpty(extra)}
	c.Claims[id] = cl
	return *cl
}

// Scope is where claim-next looks, as task-list's filter says. The spec's
// default for Recursive is true; Go's zero value is false, so set it.
type Scope struct {
	Folder    string // a folder-ref; empty means the root
	Recursive bool
	TagsAny   []string
	TagsAll   []string
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
		if s.Recursive && !t.Under(id, folder) || !s.Recursive && x.Folder != folder {
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

// Release ends a task's claim, whoever made it.
func (t *Tree) Release(task string) (changed bool, err error) {
	err = t.change(func(c *Tree) error {
		x, err := c.subjectTask(task)
		if err != nil {
			return err
		}
		_, changed = c.Claims[x.ID]
		delete(c.Claims, x.ID)
		return nil
	})
	return changed, err
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
