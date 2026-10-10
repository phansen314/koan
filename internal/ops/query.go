package ops

import (
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// findCopies finds id's task files for an operation that targets one task
// (implementation-spec.md, Queries: find exactly one), in precedence order:
// io if a folder could not be listed, not-found if id has no task file, then
// each copy loaded in tree order, the first unusable one its error. A copy
// that vanished since the walk is treated as never found. It warns about
// nothing: whether several copies are a warning or a conflict is the
// caller's.
func findCopies(tx *store.Tx, id model.ID) ([]*store.Loaded, *errs.Error) {
	found, e := loadCopies(tx, id)
	if e != nil {
		return nil, e
	}
	if len(found) == 0 {
		return nil, errs.NotFound(nil, []int64{int64(id)}, nil)
	}
	return found, nil
}

// loadCopies is findCopies without the not-found, for a caller that reports
// id's absence together with other missing IDs: no copies and no error when
// id has no task file.
func loadCopies(tx *store.Tx, id model.ID) ([]*store.Loaded, *errs.Error) {
	if e := tx.RequireWholeTree(tx.Index()); e != nil {
		return nil, e
	}
	locs := tx.Copies(id)
	var found []*store.Loaded
	for _, l := range locs {
		ld := tx.Load(l)
		if ld.State == store.Vanished {
			continue
		}
		if e := tx.Needed(ld); e != nil {
			return nil, e
		}
		found = append(found, ld)
	}
	return found, nil
}

// duplicateID records the duplicate-id warning for id's task files at locs.
func duplicateID(tx *store.Tx, id model.ID, locs []store.Location) {
	ps := make([]string, len(locs))
	for i, l := range locs {
		ps[i] = tx.Path(l.Rel())
	}
	tx.Warn(errs.DuplicateID(int64(id), ps))
}

// view is ld's task, usable, with its readiness derived from its own
// blocked_by (implementation-spec.md, Queries: derive readiness). For an
// open task every blocker is looked up, each problem with one a warning; a
// done task's blockers are not read.
func view(tx *store.Tx, ld *store.Loaded) (model.TaskView, *errs.Error) {
	v, _, e := viewStates(tx, ld)
	return v, e
}

// viewStates is view, also returning the state of each blocker it looked
// up: none for a done task.
func viewStates(tx *store.Tx, ld *store.Loaded) (model.TaskView, map[model.ID]graph.BlockerState, *errs.Error) {
	v := model.TaskView{Task: tx.Task(ld)}
	states := map[model.ID]graph.BlockerState{}
	if v.Open() {
		for _, b := range v.BlockedBy {
			s, e := blockerState(tx, ld, b)
			if e != nil {
				return model.TaskView{}, nil, e
			}
			states[b] = s
		}
	}
	v.Readiness, v.Blocking = graph.Readiness(&v.TaskFile, func(id model.ID) graph.BlockerState { return states[id] })
	v.Normalize()
	return v, states, nil
}

// blockerState looks up blocker id of the open task in ref, recording the
// warning its state calls for: dangling-reference when it has no task file
// (unless a folder could not be listed, so it may be there), duplicate-id
// when it has several, unusable-file when its one file is unusable. A file
// that vanished since the walk counts as no task file.
func blockerState(tx *store.Tx, ref *store.Loaded, id model.ID) (graph.BlockerState, *errs.Error) {
	locs := tx.Copies(id)
	switch {
	case len(locs) == 0:
		return missingBlocker(tx, ref, id), nil
	case len(locs) > 1:
		duplicateID(tx, id, locs)
		return graph.BlockerDuplicate, nil
	}
	ld := tx.Load(locs[0])
	switch {
	case ld.State == store.Vanished:
		return missingBlocker(tx, ref, id), nil
	case ld.State != store.Usable:
		if e := tx.Relevant(ld); e != nil {
			return 0, e
		}
		return graph.BlockerUnusable, nil
	case ld.Task.Open():
		return graph.BlockerOpen, nil
	}
	return graph.BlockerDone, nil
}

func missingBlocker(tx *store.Tx, ref *store.Loaded, id model.ID) graph.BlockerState {
	if tx.Index().Complete() {
		tx.Warn(errs.DanglingReference(tx.Path(ref.Loc.Rel()), int64(ref.Loc.ID), int64(id)))
	}
	return graph.BlockerMissing
}

// lookupIDs finds each of ids for an operation that requires them to exist
// (implementation-spec.md, Queries: check existence): io if a folder could
// not be listed, then each ID's copies loaded, a copy that vanished since the
// walk treated as never found. It returns the usable-or-not copies of each
// ID found, and the IDs with none, ascending. Whether the copies are
// usable is requireIDs', which runs after not-found is reported.
func lookupIDs(tx *store.Tx, ids []model.ID) (map[model.ID][]*store.Loaded, []int64, *errs.Error) {
	if e := tx.RequireWholeTree(tx.Index()); e != nil {
		return nil, nil, e
	}
	found := map[model.ID][]*store.Loaded{}
	var missing []int64
	for _, id := range ids {
		for _, l := range tx.Copies(id) {
			if ld := tx.Load(l); ld.State != store.Vanished {
				found[id] = append(found[id], ld)
			}
		}
		if len(found[id]) == 0 {
			missing = append(missing, int64(id))
		}
	}
	slices.Sort(missing)
	return found, missing, nil
}

// requireIDs checks every copy lookupIDs found, all of them needed files:
// the first unusable one in tree order is the error. Then each ID with
// several copies is a duplicate-id warning.
func requireIDs(tx *store.Tx, found map[model.ID][]*store.Loaded) *errs.Error {
	var all []*store.Loaded
	for _, copies := range found {
		all = append(all, copies...)
	}
	slices.SortFunc(all, func(a, b *store.Loaded) int { return store.CompareLocations(a.Loc, b.Loc) })
	for _, ld := range all {
		if e := tx.Needed(ld); e != nil {
			return e
		}
	}
	warnDuplicates(tx, found)
	return nil
}

// aboveLastID is the id-above-last-id conflict for the blockers in ids above
// last_id, ascending, or nil if there are none. Only a crash or an outside
// change leaves one, and a new task could be given its ID and block itself.
func aboveLastID(tx *store.Tx, ids []model.ID) *errs.Error {
	var above []int64
	for _, id := range ids {
		if int64(id) > tx.LastID() {
			above = append(above, int64(id))
		}
	}
	if above == nil {
		return nil
	}
	slices.Sort(above)
	return errs.Conflict(errs.RuleIDAboveLastID, slices.Compact(above))
}

// warnDuplicates records a duplicate-id warning for each ID found with
// several copies.
func warnDuplicates(tx *store.Tx, found map[model.ID][]*store.Loaded) {
	for id, copies := range found {
		if len(copies) > 1 {
			locs := make([]store.Location, len(copies))
			for i, ld := range copies {
				locs[i] = ld.Loc
			}
			duplicateID(tx, id, locs)
		}
	}
}

// findOne finds id's one task file for a write that changes it
// (implementation-spec.md, Queries: find exactly one): findCopies' errors,
// then conflict (duplicate-id) if several copies remain, since a write must
// know which task it changes.
func findOne(tx *store.Tx, id model.ID) (*store.Loaded, *errs.Error) {
	copies, e := findCopies(tx, id)
	if e != nil {
		return nil, e
	}
	if len(copies) > 1 {
		return nil, errs.Conflict(errs.RuleDuplicateID, []int64{int64(id)})
	}
	return copies[0], nil
}

// replaceTask writes t, changed by the caller, over the task file at rel,
// stamping its updated_at with now.
func replaceTask(tx *store.Tx, rel string, t *model.TaskFile, now model.Timestamp) *errs.Error {
	t.UpdatedAt = now
	data, err := t.Encode()
	if err != nil {
		return errs.Internal("encoding the task file: " + err.Error())
	}
	return tx.Replace(rel, data)
}

// inScope is the query behind list and frontier (implementation-spec.md,
// Queries: filter by scope): the path walk of in.Folder, then every folder
// that could not be listed as a warning, then the folders in scope and the
// usable tasks in scope as views, both in tree order — done tasks only
// if in.Readiness has done. Every open task in scope is a view whatever
// in.Readiness, so the warnings its blockers call for are recorded whatever
// the caller keeps. An unusable task file in scope is a warning and is
// left out; an ID with several copies in scope is a duplicate-id warning,
// whether or not every copy is returned.
func inScope(tx *store.Tx, in ScopeInput) ([]model.FolderPath, []model.TaskView, *errs.Error) {
	n, e := tx.WalkFolder(in.Folder)
	if e != nil {
		return nil, nil, e
	}
	if n < len(in.Folder.Segments()) {
		return nil, nil, errs.NotFound([]string{string(folderPrefix(in.Folder, n+1))}, nil, nil)
	}
	x := tx.Index()
	if e := tx.WarnUnreadable(x); e != nil {
		return nil, nil, e
	}
	folders, locs := x.InScope(in.Folder, in.Recursive)

	inScope := map[store.Location]bool{}
	counts := map[model.ID]int{}
	for _, l := range locs {
		inScope[l] = true
		counts[l.ID]++
	}
	checked := map[model.ID]bool{}
	for _, l := range locs {
		if counts[l.ID] < 2 || checked[l.ID] {
			continue
		}
		checked[l.ID] = true
		copies := slices.DeleteFunc(tx.Copies(l.ID), func(m store.Location) bool { return !inScope[m] })
		if len(copies) > 1 {
			duplicateID(tx, l.ID, copies)
		}
	}

	views := []model.TaskView{}
	for _, l := range locs {
		ld := tx.Load(l)
		switch {
		case ld.State == store.Vanished:
			continue
		case ld.State != store.Usable:
			if e := tx.Relevant(ld); e != nil {
				return nil, nil, e
			}
			continue
		case !ld.Task.Open() && !slices.Contains(in.Readiness, model.Done):
			continue
		}
		v, e := view(tx, ld)
		if e != nil {
			return nil, nil, e
		}
		views = append(views, v)
	}
	return folders, views, nil
}
