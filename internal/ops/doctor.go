package ops

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"

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
	abs := func(l loc) string { return t.Abs(store.TaskRel(l.f, l.id)) }

	// Folder files, by folder; a folder whose file can't be used has none
	// here, and its error in folderErrs.
	folderFiles := map[model.Folder]*model.FolderFile{}
	folderErrs := map[model.Folder]*errs.Error{}
	for _, f := range x.Folders {
		if !x.Filed[f] {
			continue
		}
		if ff, _, e := t.LoadFolder(f); e != nil {
			folderErrs[f] = e
		} else if ff != nil {
			folderFiles[f] = ff
		}
	}
	// highest is the highest ID in the tree, or the counter if higher: what
	// a new ID must be above.
	highest := t.State.LastID
	for _, l := range files {
		highest = max(highest, l.id)
	}
	for _, ff := range folderFiles {
		highest = max(highest, ff.ID)
	}
	for id := range x.Notes { // notes whose task is gone still hold its ID
		highest = max(highest, id)
	}

	for _, rel := range x.Temps {
		d.add("temp-leftover", "a temp file or folder koan left behind: `koan repair` removes it", nil,
			[]string{t.Abs(rel)}, func() (bool, *errs.Error) { return done(t.RemoveAll(rel)) })
	}

	var above []model.ID
	var abovePaths []string
	for _, l := range files {
		if l.id > t.State.LastID {
			above, abovePaths = append(above, l.id), append(abovePaths, abs(l))
		}
	}
	for _, f := range x.Folders {
		if ff := folderFiles[f]; ff != nil && ff.ID > t.State.LastID {
			above, abovePaths = append(above, ff.ID), append(abovePaths, t.Abs(store.FolderFileRel(f)))
		}
	}
	if len(above) > 0 {
		top := slices.Max(above)
		d.add("id-above-counter", fmt.Sprintf("IDs above the counter (%d), which would be issued again: `koan repair` raises it to %d", t.State.LastID, top),
			above, abovePaths, func() (bool, *errs.Error) {
				t.State.LastID = top
				return done(t.SaveState())
			})
	}

	for i, l := range files {
		tf := loaded[i].File
		if tf == nil {
			continue
		}
		var gone []model.ID
		for _, b := range tf.BlockedBy {
			if _, ok := x.Tasks[b]; !ok {
				gone = append(gone, b)
			}
		}
		if len(gone) == 0 {
			continue
		}
		d.add("dangling-reference", fmt.Sprintf("task %d is blocked by %v, which do not exist: if a folder was moved by hand, move it back first; otherwise `koan repair --kinds dangling-reference` removes them from its blocked_by", l.id, gone),
			append([]model.ID{l.id}, gone...), []string{abs(l)}, func() (bool, *errs.Error) {
				tf.BlockedBy = slices.DeleteFunc(tf.BlockedBy, func(b model.ID) bool { return slices.Contains(gone, b) })
				tf.UpdatedAt = c.now
				return done(t.WriteTask(l.f, tf, false))
			})
		d.onlyByName()
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
		if !exists {
			msg = fmt.Sprintf("a claim on task %d, which does not exist: if a folder was moved by hand, move it back first; otherwise `koan repair --kinds stale-claim` removes it", id)
		} else if tf := usable[id]; tf == nil || tf.Open() {
			continue
		}
		d.add("stale-claim", msg, []model.ID{id}, []string{t.Env().StatePath()}, func() (bool, *errs.Error) {
			delete(t.State.Claims, model.ClaimKey(id))
			return done(t.SaveState())
		})
		if !exists {
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
		fix, msg := orphanFix(t, x, l.f, l.id)
		d.add("orphan-notes", fmt.Sprintf("notes with no task file beside them: %s", msg),
			[]model.ID{l.id}, []string{t.Abs(store.NotesRel(l.f, l.id))}, fix)
	}

	for _, f := range x.Unfiled() {
		d.add("missing-folder-file", fmt.Sprintf("folder %s has no %s: `koan repair` writes one, with a new ID", f, store.FolderFileName),
			nil, []string{t.Abs(f.Rel())}, func() (bool, *errs.Error) {
				id := max(t.State.LastID, highest) + 1
				t.State.LastID, highest = id, id
				if e := t.SaveState(); e != nil {
					return false, e
				}
				return done(t.WriteFolderFile(f, model.NewFolderFile(id)))
			})
	}

	// Every file holding each ID: task files, every copy, and folder files.
	holders := map[model.ID][]string{}
	for _, l := range files {
		holders[l.id] = append(holders[l.id], abs(l))
	}
	for _, f := range x.Folders {
		if ff := folderFiles[f]; ff != nil {
			holders[ff.ID] = append(holders[ff.ID], t.Abs(store.FolderFileRel(f)))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(holders)) {
		if paths := holders[id]; len(paths) > 1 {
			d.add("duplicate-id", fmt.Sprintf("ID %d is in %d files: keep one, and delete or renumber the others by hand", id, len(paths)),
				[]model.ID{id}, paths, nil)
		}
	}

	edges := map[model.ID][]model.ID{}
	for id, tf := range usable {
		edges[id] = tf.BlockedBy
	}
	for _, cycle := range graph.Cycles(edges) {
		d.add("cycle", fmt.Sprintf("tasks %v block each other, each waiting on the next: `koan unblock` one of them", cycle), cycle, nil, nil)
	}

	// Unusable files come in tree order: a folder's own file before its
	// tasks'.
	byFolder := map[model.Folder][]int{}
	for i, l := range files {
		byFolder[l.f] = append(byFolder[l.f], i)
	}
	for _, f := range x.Folders {
		if e := folderErrs[f]; e != nil {
			d.add("unusable-file", e.Message+": fix the file by hand, or remove it and `koan repair` writes a new one",
				nil, []string{t.Abs(store.FolderFileRel(f))}, nil)
		}
		for _, i := range byFolder[f] {
			if l := files[i]; loaded[i].File == nil && loaded[i].OldFormat == 0 && !loaded[i].Gone {
				d.add("unusable-file", loaded[i].Err.Message+": fix the file by hand, or `koan delete` the task",
					[]model.ID{l.id}, []string{abs(l)}, nil)
			}
		}
	}
	for i, l := range files {
		if loaded[i].OldFormat != 0 {
			d.add("old-format", fmt.Sprintf("a task file in format %d: `koan migrate` converts it", loaded[i].OldFormat),
				[]model.ID{l.id}, []string{abs(l)}, nil)
		}
	}
	for _, rel := range x.Skipped {
		d.add("skipped-entry", "an entry named with @ that is not one of koan's, a symlink, or of the wrong type: koan ignores it; rename it without the @ to keep it as material", nil, []string{t.Abs(rel)}, nil)
	}
	for _, rel := range x.Unlistable {
		d.add("skipped-entry", "a folder that can't be listed: every command but doctor fails on it", nil, []string{t.Abs(rel)}, nil)
	}
	if d.findings == nil {
		d.findings = []errs.Finding{}
	}
	return d, nil
}

// orphanFix decides what repair does with a .md that has no task file beside
// it: remove it when it is empty, move it to its task when the task is
// elsewhere without notes, and otherwise leave it.
func orphanFix(t *store.Tree, x *store.Index, f model.Folder, id model.ID) (fix, string) {
	rel := store.NotesRel(f, id)
	if fi, err := t.Stat(rel); err == nil && fi.Size() == 0 {
		return func() (bool, *errs.Error) { return done(t.Remove(rel)) }, "they are empty, and `koan repair` removes them"
	}
	home, exists := x.Tasks[id]
	if exists && len(x.Copies[id]) == 0 && !slices.Contains(x.Notes[id], home) {
		return func() (bool, *errs.Error) {
			// taken: another orphan of this task got there first.
			taken, e := t.Move(rel, store.NotesRel(home, id))
			return e == nil && !taken, e
		}, fmt.Sprintf("`koan repair` moves them to their task in %s", home)
	}
	return nil, "move or delete them by hand"
}
