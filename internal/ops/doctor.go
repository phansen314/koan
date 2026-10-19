package ops

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// loc is one task file: an ID in a folder.
type loc struct {
	f  model.Folder
	id model.ID
}

// inTreeOrder lists every task file, copies of a duplicated ID included, by
// folder in tree order and then by ID.
func inTreeOrder(x *store.Index) []loc {
	var ls []loc
	for id := range x.Tasks {
		for _, f := range copiesOf(x, id) {
			ls = append(ls, loc{f, id})
		}
	}
	sortLocs(x, ls)
	return ls
}

func sortLocs(x *store.Index, ls []loc) {
	pos := map[model.Folder]int{}
	for i, f := range x.Folders {
		pos[f] = i
	}
	slices.SortFunc(ls, func(a, b loc) int {
		if a.f != b.f {
			return pos[a.f] - pos[b.f]
		}
		return cmp.Compare(a.id, b.id)
	})
}

// fix makes one repair. fixed is false when it found it could not after
// all: the finding stays, and is not reported as repaired.
type fix func() (fixed bool, e *errs.Error)

// done is the result of a fix that did its work unless it failed.
func done(e *errs.Error) (bool, *errs.Error) { return e == nil, e }

// diagnosis is doctor's findings, each with the fix repair would make: nil
// for one a person has to resolve. byName marks a fix repair makes only when
// its kind is asked for by name.
type diagnosis struct {
	findings []errs.Finding
	fixes    []fix
	byName   []bool
}

func (d *diagnosis) add(kind, msg string, ids []model.ID, paths []string, fix fix) {
	if ids == nil {
		ids = []model.ID{}
	}
	if paths == nil {
		paths = []string{}
	}
	d.findings = append(d.findings, errs.Finding{Kind: kind, Message: msg, IDs: ids, Paths: paths, Repairable: fix != nil})
	d.fixes = append(d.fixes, fix)
	d.byName = append(d.byName, false)
}

// onlyByName marks the last finding's fix as one repair makes only when its
// kind is asked for by name: one that removes a task's ID, since a task that
// seems gone may be in a folder moved by hand.
func (d *diagnosis) onlyByName() { d.byName[len(d.byName)-1] = true }

// diagnose finds everything wrong with the tree (spec/design.md, Doctor and
// repair). Findings come out by kind, in the order the design lists them,
// and in tree order within a kind.
func diagnose(c *call) (*diagnosis, *errs.Error) {
	t := c.t
	x, e := t.Walk(true)
	if e != nil {
		return nil, e
	}
	d := &diagnosis{}
	files := inTreeOrder(x)
	usable := map[model.ID]*model.TaskFile{} // by ID, for tasks with one copy
	loaded := make([]*store.Loaded, len(files))
	for i, l := range files {
		loaded[i] = t.LoadAt(l.f, l.id)
		if loaded[i].File != nil && len(x.Copies[l.id]) == 0 {
			usable[l.id] = loaded[i].File
		}
	}
	abs := func(l loc) string { return t.Abs(model.TaskRel(l.f, l.id)) }

	// Folder files, by folder; a folder whose file can't be used has none
	// here, and its error in folderErrs.
	folderFiles, folderErrs := loadFolderFiles(t, x)
	// The archive, walked once: its IDs count against the counter and for
	// duplicates, and a reference to one it holds is gone for good.
	ar := t.WalkArchive()
	inArchive := func(id model.ID) bool { return len(ar.Holders[id]) > 0 }
	// highest is the highest ID in the tree and the archive, or the counter
	// if higher: what a new ID must be above.
	highest := max(t.Meta.LastID, highestID(x, folderFiles))
	for id := range ar.Named {
		highest = max(highest, id)
	}

	for _, rel := range slices.Concat(t.RootTemps(), x.Temps, ar.Temps) {
		d.add(errs.FindingTempLeftover, "a temp file koan left behind: `koan repair` removes it", nil,
			[]string{t.Abs(rel)}, func() (bool, *errs.Error) { return done(t.Remove(rel)) })
	}

	var above []model.ID
	var abovePaths []string
	for _, l := range files {
		if l.id > t.Meta.LastID {
			above, abovePaths = append(above, l.id), append(abovePaths, abs(l))
		}
	}
	for _, f := range x.Folders {
		if ff := folderFiles[f]; ff != nil && ff.ID > t.Meta.LastID {
			above, abovePaths = append(above, ff.ID), append(abovePaths, t.Abs(model.FolderFileRel(f)))
		}
	}
	// Notes hold their ID when their task is gone: the next task given it
	// would find them in its place.
	for _, id := range slices.Sorted(maps.Keys(x.Notes)) {
		if _, isTask := x.Tasks[id]; id > t.Meta.LastID && !isTask {
			for _, f := range x.Notes[id] {
				above, abovePaths = append(above, id), append(abovePaths, t.Abs(model.NotesRel(f, id)))
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ar.Named)) {
		if id > t.Meta.LastID {
			for _, p := range ar.Named[id] {
				above, abovePaths = append(above, id), append(abovePaths, p)
			}
		}
	}
	if len(above) > 0 {
		top := slices.Max(above)
		d.add(errs.FindingIDAboveCounter, fmt.Sprintf("IDs above the counter (%d), which would be issued again: `koan repair` raises it to %d", t.Meta.LastID, top),
			above, abovePaths, func() (bool, *errs.Error) {
				m := *t.Meta
				m.LastID = top
				return done(t.WriteMeta(m, false))
			})
	}

	// The graph's view of the tree, for references and cycles.
	dt := &doctorTree{x: x, usable: usable, files: folderFiles, bad: folderErrs,
		byID: map[model.ID][]model.Folder{}, tasks: map[model.Folder][]model.ID{}, subs: map[model.Folder][]model.Folder{}}
	for _, f := range x.Folders {
		if ff := folderFiles[f]; ff != nil {
			dt.byID[ff.ID] = append(dt.byID[ff.ID], f)
		}
		if f != model.Root {
			dt.subs[f.Parent()] = append(dt.subs[f.Parent()], f)
		}
	}
	for _, l := range files {
		dt.tasks[l.f] = append(dt.tasks[l.f], l.id)
	}
	var brokenFiles []string
	for _, f := range x.Folders {
		if folderErrs[f] != nil {
			brokenFiles = append(brokenFiles, t.Abs(model.FolderFileRel(f)))
		}
	}
	// A dangling ID may be the ID of a folder whose file can't be used:
	// removing it would lose the link once the file is fixed.
	dangling := func(who string, id model.ID, gone []model.ID, p string, remove func(gone []model.ID) *errs.Error) {
		// What the archive holds is gone for good: removed by default,
		// whatever else is wrong.
		if archived := slices.DeleteFunc(slices.Clone(gone), func(b model.ID) bool { return !inArchive(b) }); len(archived) > 0 {
			var where []string
			for _, b := range archived {
				where = append(where, ar.Holders[b][0])
			}
			d.add(errs.FindingDanglingReference, fmt.Sprintf("%s is blocked by %v, which were archived (%s): `koan repair` removes them from its blocked_by", who, archived, strings.Join(where, ", ")),
				append([]model.ID{id}, archived...), []string{p}, func() (bool, *errs.Error) { return done(remove(archived)) })
			gone = slices.DeleteFunc(gone, inArchive)
			if len(gone) == 0 {
				return
			}
		}
		if len(brokenFiles) > 0 {
			d.add(errs.FindingDanglingReference, fmt.Sprintf("%s is blocked by %v, which do not exist, unless one is the ID of a folder whose file can't be used (%s): fix that file first, and then run doctor again", who, gone, strings.Join(brokenFiles, ", ")),
				append([]model.ID{id}, gone...), []string{p}, nil)
			return
		}
		d.add(errs.FindingDanglingReference, fmt.Sprintf("%s is blocked by %v, which do not exist: if a folder was moved by hand, move it back first; otherwise `koan repair --kinds dangling-reference` removes them from its blocked_by", who, gone),
			append([]model.ID{id}, gone...), []string{p}, func() (bool, *errs.Error) { return done(remove(gone)) })
		d.onlyByName()
	}
	byFolder := map[model.Folder][]int{}
	for i, l := range files {
		byFolder[l.f] = append(byFolder[l.f], i)
	}
	for _, f := range x.Folders {
		if ff := folderFiles[f]; ff != nil {
			if gone := dt.gone(ff.BlockedBy); len(gone) > 0 {
				dangling("folder "+string(f), ff.ID, gone, t.Abs(model.FolderFileRel(f)), func(gone []model.ID) *errs.Error {
					ff.BlockedBy = slices.DeleteFunc(ff.BlockedBy, func(b model.ID) bool { return slices.Contains(gone, b) })
					return t.WriteFolderFile(f, ff, false)
				})
			}
		}
		for _, i := range byFolder[f] {
			l, tf := files[i], loaded[i].File
			if tf == nil {
				continue
			}
			if gone := dt.gone(tf.BlockedBy); len(gone) > 0 {
				dangling(fmt.Sprintf("task %d", l.id), l.id, gone, abs(l), func(gone []model.ID) *errs.Error {
					tf.BlockedBy = slices.DeleteFunc(tf.BlockedBy, func(b model.ID) bool { return slices.Contains(gone, b) })
					tf.UpdatedAt = c.now
					return t.WriteTask(l.f, tf, false)
				})
			}
		}
	}

	var claimed []model.ID
	for key := range t.State.Claims {
		id, _ := strconv.ParseInt(key, 10, 64)
		claimed = append(claimed, id)
	}
	slices.Sort(claimed)
	for _, id := range claimed {
		_, exists := x.Tasks[id]
		msg := fmt.Sprintf("a claim on task %d, which is done: `koan repair` removes it", id)
		if !exists && inArchive(id) {
			msg = fmt.Sprintf("a claim on task %d, which was archived: `koan repair` removes it", id)
		} else if !exists {
			msg = fmt.Sprintf("a claim on task %d, which does not exist: if a folder was moved by hand, move it back first; otherwise `koan repair --kinds stale-claim` removes it", id)
		} else if tf := usable[id]; tf == nil || tf.Open() {
			continue
		}
		d.add(errs.FindingStaleClaim, msg, []model.ID{id}, []string{t.Env().StatePath()}, func() (bool, *errs.Error) {
			delete(t.State.Claims, model.ClaimKey(id))
			return done(t.SaveState())
		})
		if !exists && !inArchive(id) {
			d.onlyByName()
		}
	}

	var orphans []loc
	for id, folders := range x.Notes {
		for _, f := range folders {
			if _, exists := x.Tasks[id]; !exists || !slices.Contains(copiesOf(x, id), f) {
				orphans = append(orphans, loc{f, id})
			}
		}
	}
	sortLocs(x, orphans)
	for _, l := range orphans {
		fix, msg := orphanFix(t, x, ar, l.f, l.id)
		d.add(errs.FindingOrphanNotes, fmt.Sprintf("notes with no task file beside them: %s", msg),
			[]model.ID{l.id}, []string{t.Abs(model.NotesRel(l.f, l.id))}, fix)
	}

	for _, f := range x.Unfiled() {
		d.add(errs.FindingMissingFolderFile, fmt.Sprintf("folder %s has no %s, so its blockers are unknown and everything in it is held back: `koan repair` writes one, with a new ID and no blockers; a blocked_by that named its old ID then dangles, so re-point such references with `koan task block` or `koan folder block` before repairing dangling-reference", f, model.FolderFileName),
			nil, []string{t.Abs(f.Rel())}, func() (bool, *errs.Error) {
				id := max(t.Meta.LastID, highest) + 1
				highest = id
				m := *t.Meta
				m.LastID = id
				if e := t.WriteMeta(m, false); e != nil {
					return false, e
				}
				return done(t.WriteFolderFile(f, model.NewFolderFile(id), true))
			})
	}

	diagnoseIndex(d, t, ar)

	// Every file holding each ID: task files, every copy, and folder files,
	// in the tree and the archive.
	holders := map[model.ID][]string{}
	for _, l := range files {
		holders[l.id] = append(holders[l.id], abs(l))
	}
	for _, f := range x.Folders {
		if ff := folderFiles[f]; ff != nil {
			holders[ff.ID] = append(holders[ff.ID], t.Abs(model.FolderFileRel(f)))
		}
	}
	for id, ps := range ar.Holders {
		holders[id] = append(holders[id], ps...)
	}
	for _, id := range slices.Sorted(maps.Keys(holders)) {
		if paths := holders[id]; len(paths) > 1 {
			d.add(errs.FindingDuplicateID, fmt.Sprintf("ID %d is in %d files: keep one, and delete or renumber the others by hand", id, len(paths)),
				[]model.ID{id}, paths, nil)
		}
	}

	// Every node of the graph: each usable task, and each folder's two.
	g := graph.New(dt)
	var nodes []graph.Ref
	for _, l := range files {
		if usable[l.id] != nil {
			nodes = append(nodes, graph.TaskRef(l.id))
		}
	}
	for _, f := range x.Folders {
		if f != model.Root {
			nodes = append(nodes, graph.WaitOf(f), graph.DoneOf(f))
		}
	}
	key := func(r graph.Ref) model.ID {
		if r.Kind == graph.TaskNode {
			return r.ID
		}
		return dt.Folder(r.Folder).ID
	}
	for _, cycle := range g.Cycles(nodes, key) {
		var ids []model.ID
		var dirs []string
		for _, r := range graph.Collapse(cycle) {
			if id := key(r); id != 0 {
				ids = append(ids, id)
			}
			if r.IsFolder() {
				dirs = append(dirs, t.Abs(r.Folder.Rel()))
			}
		}
		var steps []string
		now := "they are stuck now"
		empty := func(f model.Folder) bool { return len(g.Under(f)) == 0 }
		for i, a := range cycle {
			b := cycle[(i+1)%len(cycle)]
			if a.Kind == graph.DoneNode && b.Kind == graph.WaitNode && a.Folder == b.Folder && empty(a.Folder) {
				now = "nothing is stuck yet, but a task added to " + string(a.Folder) + " would be"
			}
			steps = append(steps, stepWords(a, b, func(r graph.Ref) string {
				if r.Kind == graph.TaskNode {
					return fmt.Sprintf("task %d", r.ID)
				}
				return string(r.Folder)
			}, empty))
		}
		d.add(errs.FindingCycle, fmt.Sprintf("tasks and folders wait on each other (%s), and %s: `koan task unblock` or `koan folder unblock` one of them", strings.Join(steps, "; "), now),
			ids, dirs, nil)
	}

	// Unusable files come in tree order: a folder's own file before its
	// tasks'.
	for _, f := range x.Folders {
		if e := folderErrs[f]; e != nil {
			d.add(errs.FindingUnusableFile, e.Message+": fix the file by hand, or remove it and `koan repair` writes a new one",
				nil, []string{t.Abs(model.FolderFileRel(f))}, nil)
		}
		for _, i := range byFolder[f] {
			if l := files[i]; loaded[i].File == nil && loaded[i].OldFormat == 0 && !loaded[i].Gone {
				d.add(errs.FindingUnusableFile, loaded[i].Err.Message+": fix the file by hand, or `koan task delete` the task",
					[]model.ID{l.id}, []string{abs(l)}, nil)
			}
		}
	}
	for _, p := range ar.Mismatched {
		d.add(errs.FindingUnusableFile, p+" holds another ID than its archived folder's name: fix the file by hand",
			nil, []string{p}, nil)
	}
	for i, l := range files {
		if loaded[i].OldFormat != 0 {
			d.add(errs.FindingOldFormat, fmt.Sprintf("a task file in format %d: `koan migrate` converts it", loaded[i].OldFormat),
				[]model.ID{l.id}, []string{abs(l)}, nil)
		}
	}
	for _, rel := range x.Skipped {
		d.add(errs.FindingSkippedEntry, "an entry named with @ that is not one of koan's, a symlink, or of the wrong type: koan ignores it; rename it without the @ to keep it as material", nil, []string{t.Abs(rel)}, nil)
	}
	for _, rel := range x.Unlistable {
		d.add(errs.FindingSkippedEntry, "a folder that can't be listed: every command but doctor fails on it", nil, []string{t.Abs(rel)}, nil)
	}
	if d.findings == nil {
		d.findings = []errs.Finding{}
	}
	return d, nil
}

// orphanFix decides what repair does with a .md that has no task file beside
// it: remove it when it is empty, move it to its task when the task is
// elsewhere without notes, and otherwise leave it.
func orphanFix(t *store.Tree, x *store.Index, ar *store.Archive, f model.Folder, id model.ID) (fix, string) {
	rel := model.NotesRel(f, id)
	if fi, err := t.Stat(rel); err == nil && fi.Size() == 0 {
		return func() (bool, *errs.Error) { return done(t.Remove(rel)) }, "they are empty, and `koan repair` removes them"
	}
	// A task archive cut short leaves its notes behind.
	if _, exists := x.Tasks[id]; !exists {
		for _, a := range ar.Items {
			if a.ID != id || a.Kind != model.ArchivedTask {
				continue
			}
			to := model.ArchiveRel(strings.TrimSuffix(a.Archived, model.TaskSuffix) + model.NotesSuffix)
			if _, err := t.Stat(to); err == nil {
				break
			}
			return func() (bool, *errs.Error) {
				taken, e := t.Move(rel, to)
				return e == nil && !taken, e
			}, fmt.Sprintf("their task was archived, and `koan repair` moves them to it, at %s", t.Abs(to))
		}
	}
	home, exists := x.Tasks[id]
	if exists && len(x.Copies[id]) == 0 && !slices.Contains(x.Notes[id], home) {
		return func() (bool, *errs.Error) {
			// taken: another orphan of this task got there first.
			taken, e := t.Move(rel, model.NotesRel(home, id))
			return e == nil && !taken, e
		}, fmt.Sprintf("`koan repair` moves them to their task in %s", home)
	}
	return nil, "move or delete them by hand"
}

// doctorTree is the graph's view of the tree as doctor reads it: a task
// with more than one copy is not usable, and a duplicated ID is not an
// error.
type doctorTree struct {
	x      *store.Index
	usable map[model.ID]*model.TaskFile
	files  map[model.Folder]*model.FolderFile
	bad    map[model.Folder]*errs.Error
	byID   map[model.ID][]model.Folder
	tasks  map[model.Folder][]model.ID
	subs   map[model.Folder][]model.Folder
}

// gone lists the IDs in bs that name no task and no folder.
func (d *doctorTree) gone(bs []model.ID) []model.ID {
	var gone []model.ID
	for _, b := range bs {
		if _, isTask := d.x.Tasks[b]; !isTask && len(d.byID[b]) == 0 {
			gone = append(gone, b)
		}
	}
	return gone
}

func (d *doctorTree) Resolve(id model.ID) graph.Item {
	if f, ok := d.x.Tasks[id]; ok {
		return graph.Item{Kind: graph.TaskKind, Folder: f}
	}
	if fs := d.byID[id]; len(fs) > 0 {
		return graph.Item{Kind: graph.FolderKind, Folder: fs[0]}
	}
	return graph.Item{}
}

func (d *doctorTree) TaskFolder(id model.ID) (model.Folder, bool) {
	f, ok := d.x.Tasks[id]
	return f, ok
}

func (d *doctorTree) Task(id model.ID) (graph.Node, bool) {
	tf := d.usable[id]
	if tf == nil {
		return graph.Node{}, false
	}
	return graph.Node{Open: tf.Open(), Priority: tf.Priority, BlockedBy: tf.BlockedBy}, true
}

func (d *doctorTree) Folder(f model.Folder) graph.Folder {
	var id model.ID
	if ff := d.files[f]; ff != nil {
		id = ff.ID
	}
	return graph.Folder{ID: id, Tasks: d.tasks[f], Folders: d.subs[f]}
}

func (d *doctorTree) Blockers(f model.Folder) ([]model.ID, bool, bool) {
	if f == model.Root {
		return nil, true, false
	}
	if ff := d.files[f]; ff != nil {
		return ff.BlockedBy, true, false
	}
	return nil, false, d.bad[f] != nil
}
