package ops

import (
	"os"
	"slices"
	"strconv"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

func decodeRepair(f *model.Fields, p *model.Problems) any {
	kinds := optionalKinds(f, p)
	for i, k := range kinds {
		if c := findingClass[k]; c == classManual || c == classInformational {
			reason := k + " is " + c + ": repair never changes it; see koan doctor --kinds " + k
			if k == kindMigrationPending || k == kindOldFormat {
				reason = k + " is not repaired: converting formats is koan migrate's job alone"
			}
			p.AddAdditional(jsonio.Pointer(f.Ptr("kinds"), strconv.Itoa(i)), reason)
		}
	}
	return KindsInput{Kinds: kinds}
}

// RepairOutput is repair's result (repair-output).
type RepairOutput struct {
	Repaired []Finding `json:"repaired"`
	Healthy  bool      `json:"healthy"`
	Findings []Finding `json:"findings"`
}

// RepairPartial is what repair changed before an error (repair-partial).
type RepairPartial struct {
	Repaired []Finding `json:"repaired"`
}

// runRepair applies the safe repairs, under the write lock, in the order
// Crash behavior gives (operations.md, repair), then reports what is left.
func runRepair(env Env, in KindsInput, w *errs.Collector) (any, *errs.Error) {
	kinds := in.Kinds
	if kinds == nil {
		for _, k := range findingKinds {
			if findingClass[k] == classAuto {
				kinds = append(kinds, k)
			}
		}
	}
	var out RepairOutput
	e := store.Diagnose(env.Env, w, func(tx *store.Tx) *errs.Error {
		// Both files are judged once, under the lock, before any repair is
		// made: koan.json's, then the state file's.
		switch state, metaErr := tx.MetaState(); {
		case state == store.MetaMissing && slices.Contains(kinds, kindMetadataMissing):
		case metaErr != nil:
			return metaErr
		}
		switch state, stateErr := tx.StateState(); {
		case (state == store.StateMissing || state == store.StateOtherRoot) && slices.Contains(kinds, kindStateMissing):
		case stateErr != nil:
			return stateErr
		}
		fs, e := diagnose(tx)
		if e != nil {
			return e
		}
		r := repairer{tx: tx, fs: fs, kinds: kinds, now: model.TimestampOf(env.Clock()), done: findings{}}
		if e := r.run(); e != nil {
			return e.WithPartial(RepairPartial{Repaired: r.done.report(nil, in.Kinds)})
		}
		tx.NextStep()
		left, e := diagnose(tx)
		if e != nil {
			return e
		}
		out = RepairOutput{Repaired: r.done.report(nil, in.Kinds), Healthy: left.healthy(), Findings: left.report(nil, in.Kinds)}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// repairer applies the repairs of the kinds asked for to the findings found,
// recording each one made in done.
type repairer struct {
	tx    *store.Tx
	fs    findings
	kinds []string
	now   model.Timestamp
	done  findings
}

// items is the items of kind that repair acts on: none unless kind is asked
// for, and only those with an action.
func (r *repairer) items(kind string) []Item {
	if !slices.Contains(r.kinds, kind) {
		return nil
	}
	var out []Item
	for _, it := range r.fs[kind] {
		if it.Action != nil {
			out = append(out, it)
		}
	}
	slices.SortStableFunc(out, compareItems)
	return out
}

func (r *repairer) run() *errs.Error {
	tx := r.tx
	for _, it := range r.items(kindTempLeftover) {
		if err := tx.RemoveAll(it.rel); err != nil {
			return tx.OSError(it.rel, err)
		}
		r.done.add(kindTempLeftover, it)
	}

	if items := r.items(kindMetadataMissing); len(items) > 0 {
		if e := tx.CreateMeta(*items[0].Migration); e != nil {
			return e
		}
		r.done.add(kindMetadataMissing, items[0])
	}

	if items := r.items(kindStateMissing); len(items) > 0 {
		if e := tx.CreateState(*items[0].LastID); e != nil {
			return e
		}
		r.done.add(kindStateMissing, items[0])
	}

	if items := r.items(kindIDAboveLastID); len(items) > 0 {
		if target := *items[0].LastID; target > tx.LastID() {
			if e := tx.SetLastID(target); e != nil {
				return e
			}
		}
		for _, it := range items {
			r.done.add(kindIDAboveLastID, it)
		}
	}

	// One rewrite per task file, with every missing ID in it, in tree order.
	dangling := r.items(kindDanglingReference)
	slices.SortStableFunc(dangling, func(a, b Item) int { return store.CompareLocations(a.loc, b.loc) })
	for i := 0; i < len(dangling); {
		j := i
		var missing []model.ID
		for ; j < len(dangling) && dangling[j].loc == dangling[i].loc; j++ {
			missing = append(missing, dangling[j].missing)
		}
		ld := tx.Load(dangling[i].loc)
		if ld.State != store.Usable {
			return errs.Internal("dangling reference in an unusable file " + tx.Path(dangling[i].loc.Rel()))
		}
		if _, e := removeBlockers(tx, ld, missing, r.now); e != nil {
			return e
		}
		for _, it := range dangling[i:j] {
			r.done.add(kindDanglingReference, it)
		}
		i = j
	}

	for _, it := range r.items(kindOrphanNotes) {
		// Checked again just before: one that changed is left, and the
		// findings after the repairs report it.
		if !r.stillRemovable(it) {
			continue
		}
		if err := tx.Remove(it.rel); err != nil {
			if isENOENT(err) {
				continue
			}
			return tx.OSError(it.rel, err)
		}
		r.done.add(kindOrphanNotes, it)
	}
	return nil
}

// stillRemovable reports whether an orphaned .md is still what doctor found
// it to be: empty, or the same file as its task's notes.
func (r *repairer) stillRemovable(it Item) bool {
	fi, err := r.tx.Lstat(it.rel)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	switch it.Reason {
	case orphanEmpty:
		return fi.Size() == 0
	case orphanLinked:
		other, err := r.tx.Lstat(it.linked)
		return err == nil && os.SameFile(fi, other)
	}
	return false
}
