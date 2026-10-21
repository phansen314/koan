package doctor

import (
	"path"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// Repair puts the tree back to what the database says, for the kinds
// given, taken in the order of Kinds, holding the write lock throughout.
// Like every write, it first finishes the steps earlier writes left
// pending. Each fix is a change whose filesystem steps are carried out
// as a write's are. It reports what it fixed: every finding there before
// it began that is gone once it is done, such as the missing directories
// a stray one moved back brings with it; and every finding left,
// repairable or not, with the warnings the steps still pending give.
func Repair(s *store.Store, kinds []string) (fixed, left []Finding, warnings []cli.Warning, e *cli.Error) {
	e = s.Hold(func(h *store.Held) *cli.Error {
		before, e := Check(s)
		if e != nil {
			return e
		}
		if e := h.Finish(); e != nil {
			return e
		}
		r := &repair{s: s, h: h}
		for _, kind := range RepairKinds {
			if !slices.Contains(kinds, kind) {
				continue
			}
			switch kind {
			case PendingStep:
				// Finished above, as by every write.
			case StuckStep:
				e = r.retry()
			case StrayDirectory, CaseDiffers:
				e = r.oneByOne(kind)
			default:
				e = r.together(kind)
			}
			if e != nil {
				return e
			}
		}
		if left, e = Check(s); e != nil {
			return e
		}
		for _, f := range before {
			if !among(f, left) {
				fixed = append(fixed, f)
			}
		}
		warnings = h.Left()
		return nil
	})
	if e != nil {
		return nil, nil, nil, e
	}
	return fixed, left, warnings, nil
}

type repair struct {
	s *store.Store
	h *store.Held
}

// of is the findings of one kind.
func of(found []Finding, kind string) []Finding {
	var out []Finding
	for _, f := range found {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// retry retries every stuck change, in seq order, now that a person has
// acted. A change that sticks again is marked stuck again.
func (r *repair) retry() *cli.Error {
	var stuck []int64
	e := r.s.Read(func(tx *store.Tx) error {
		_, st, err := tx.Pending()
		for _, p := range st {
			if !slices.Contains(stuck, p.Change) {
				stuck = append(stuck, p.Change)
			}
		}
		return err
	})
	if e != nil {
		return e
	}
	for _, ch := range stuck {
		if e := r.h.Retry(ch); e != nil {
			return e
		}
	}
	return nil
}

// fix carries out the steps that fix findings, as one change.
func (r *repair) fix(found []Finding) *cli.Error {
	if len(found) == 0 {
		return nil
	}
	_, _, e := r.h.Change(func(tx *store.Tx) (any, []cli.Warning, error) {
		for _, f := range found {
			if _, err := fsstep.Add(tx, *f.step); err != nil {
				return nil, nil, err
			}
		}
		return nil, nil, nil
	})
	return e
}

// ready is the repairable findings of a kind whose steps can run: a
// step's destination directory must be there, since a fix of another
// kind makes it.
func (r *repair) ready(found []Finding, kind string) ([]Finding, *cli.Error) {
	var out []Finding
	for _, f := range of(found, kind) {
		if !f.Repairable || f.step == nil {
			continue
		}
		if f.step.Op == fsstep.OpMove {
			there, err := fsstep.There(r.s.Root, path.Dir(f.step.To))
			if err != nil {
				return nil, cli.IO("could not list "+fsstep.Abs(r.s.Root, path.Dir(f.step.To))+": "+err.Error(), fsstep.Abs(r.s.Root, path.Dir(f.step.To)))
			}
			if !there {
				continue
			}
		}
		out = append(out, f)
	}
	// Top-down, so a folder's directory is put right before those under
	// it.
	slices.SortStableFunc(out, func(a, b Finding) int { return a.depth - b.depth })
	return out, nil
}

// oneByOne fixes the findings of a kind one at a time, top-down, looking
// again after each: a stray directory moved back, or a directory renamed,
// takes the folders under it with it.
func (r *repair) oneByOne(kind string) *cli.Error {
	var tried []Finding
	for {
		found, e := Check(r.s)
		if e != nil {
			return e
		}
		ready, e := r.ready(found, kind)
		if e != nil {
			return e
		}
		ready = slices.DeleteFunc(ready, func(f Finding) bool { return among(f, tried) })
		if len(ready) == 0 {
			return nil
		}
		tried = append(tried, ready[0])
		if e := r.fix(ready[:1]); e != nil {
			return e
		}
	}
}

// together fixes the findings of a kind as one change: directories made
// parents first, notes moved back, empty orphan notes removed. Notes are
// moved back from one place only, since once they are there, any other
// copy is no longer repairable.
func (r *repair) together(kind string) *cli.Error {
	found, e := Check(r.s)
	if e != nil {
		return e
	}
	ready, e := r.ready(found, kind)
	if e != nil {
		return e
	}
	var steps []Finding
	var moved []string
	for _, f := range ready {
		if f.Kind == MisplacedNotes {
			if slices.ContainsFunc(moved, func(to string) bool { return strings.EqualFold(to, f.step.To) }) {
				continue
			}
			moved = append(moved, f.step.To)
		}
		steps = append(steps, f)
	}
	return r.fix(steps)
}
