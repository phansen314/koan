package ops

import (
	"slices"
	"strconv"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// BlockersInput is the input of block and unblock: a task and the blockers
// to add or remove.
type BlockersInput struct {
	ID       model.ID
	Blockers []model.ID
}

func decodeBlock(f *model.Fields, p *model.Problems) any {
	in := BlockersInput{ID: requiredID(f, p, "id")}
	var ok bool
	in.Blockers, ok = blockerList(f, p, "blockers")
	if ok && !p.Failed("/id") { // Additional validation runs on valid fields only
		for i, b := range in.Blockers {
			if b == in.ID {
				p.AddAdditional(jsonio.Pointer("/blockers", strconv.Itoa(i)), "a task cannot block itself")
			}
		}
	}
	return in
}

// BlockOutput is block's result (block-output): the task after the
// operation, plus the blockers it added, ascending.
type BlockOutput struct {
	model.Task
	Added []model.ID `json:"added"`
}

// runBlock adds the new blockers — those in no copy's blocked_by — to the
// task's blocked_by, all or nothing, under the write lock, in block's
// precedence order (implementation-spec.md, Error precedence): id's own
// copies loaded first, since which blockers are new depends on them; then
// one not-found for id and every missing new blocker; then the subgraph
// reachable from the new blockers loaded, its first unusable file the error;
// then the conflicts, duplicate-id before acyclic. Blockers already present
// are not checked at all. If none is new, nothing is written.
func runBlock(env Env, in BlockersInput, w *errs.Collector) (any, *errs.Error) {
	var out BlockOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		copies, e := loadCopies(tx, in.ID)
		if e != nil {
			return e
		}
		present := map[model.ID]bool{}
		for _, c := range copies {
			for _, b := range c.Task.BlockedBy {
				present[b] = true
			}
		}
		added := []model.ID{}
		for _, b := range in.Blockers {
			if !present[b] {
				added = append(added, b)
			}
		}
		slices.Sort(added)

		var missing []int64
		if len(copies) == 0 {
			missing = append(missing, int64(in.ID))
		}
		var found map[model.ID][]*store.Loaded
		if len(added) > 0 {
			var m []int64
			if found, m, e = lookupIDs(tx, added); e != nil {
				return e
			}
			missing = append(missing, m...)
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			return errs.NotFound(nil, missing, nil)
		}
		var edges map[model.ID][]model.ID
		if len(added) > 0 {
			if edges, e = reachable(tx, in.ID, added); e != nil {
				return e
			}
			warnDuplicates(tx, found)
		}
		if len(copies) > 1 {
			return errs.Conflict(errs.RuleDuplicateID, []int64{int64(in.ID)})
		}
		if ids, cycles := graph.Cycles(in.ID, added, edges); len(ids) > 0 {
			return errs.Acyclic(int64s(ids), mapSlice(cycles, int64s))
		}

		ld := copies[0]
		if len(added) > 0 {
			ld.Task.BlockedBy = slices.Concat(ld.Task.BlockedBy, added)
			if e := replaceTask(tx, ld.Loc.Rel(), &ld.Task, model.TimestampOf(env.Clock())); e != nil {
				return e
			}
		}
		out.Task, out.Added = tx.Task(ld), added
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// reachable is the cycle check's phase 1 (implementation-spec.md, Cycle
// check): the subgraph reachable from the new blockers by following
// blocked_by, never expanding id. Every copy of each reached ID is loaded,
// open or done, and a node's edges are the union of its usable copies'
// blocked_by; an ID with no task file has none. Only once the whole subgraph
// is loaded are its files checked, in tree order, the first unusable one the
// error: which files are read depends on the graph alone.
func reachable(tx *store.Tx, id model.ID, from []model.ID) (map[model.ID][]model.ID, *errs.Error) {
	edges := map[model.ID][]model.ID{}
	seen := map[model.ID]bool{id: true}
	for _, b := range from {
		seen[b] = true
	}
	var loaded []*store.Loaded
	for queue := slices.Clone(from); len(queue) > 0; queue = queue[1:] {
		n := queue[0]
		var lists [][]model.ID
		for _, l := range tx.Copies(n) {
			ld := tx.Load(l)
			switch ld.State {
			case store.Vanished:
				continue
			case store.Usable:
				lists = append(lists, ld.Task.BlockedBy)
			}
			loaded = append(loaded, ld)
		}
		edges[n] = graph.Edges(lists...)
		for _, m := range edges[n] {
			if !seen[m] {
				seen[m] = true
				queue = append(queue, m)
			}
		}
	}
	slices.SortFunc(loaded, func(a, b *store.Loaded) int { return store.CompareLocations(a.Loc, b.Loc) })
	for _, ld := range loaded {
		if e := tx.Needed(ld); e != nil {
			return nil, e
		}
	}
	return edges, nil
}

func int64s(ids []model.ID) []int64 {
	return mapSlice(ids, func(id model.ID) int64 { return int64(id) })
}

func mapSlice[T, U any](s []T, f func(T) U) []U {
	out := make([]U, len(s))
	for i, v := range s {
		out[i] = f(v)
	}
	return out
}
