package ops

import (
	"cmp"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// WhyInput is why's input, defaults applied.
type WhyInput struct {
	ID           model.ID
	IncludeTasks bool
	Fields       []string // nil: whole views
}

func decodeWhy(f *model.Fields, p *model.Problems) any {
	in := WhyInput{ID: requiredID(f, p, "id"), IncludeTasks: optionalBool(f, p, "include_tasks", false)}
	if v, ok := f.Optional("fields"); ok {
		in.Fields, _ = nonEmptySet(p, v, f.Ptr("fields"), "field", nameSet(model.ViewFields))
		if !in.IncludeTasks {
			p.AddAdditional(f.Ptr("fields"), "is allowed only with include_tasks")
		}
	}
	return in
}

// WhyOutput is why's result (why-output). Tasks is present only with
// include_tasks.
type WhyOutput struct {
	Readiness model.Readiness `json:"readiness"`
	Ready     []model.ID      `json:"ready"`
	Stuck     []model.ID      `json:"stuck"`
	Tasks     *Tasks          `json:"tasks,omitempty"`
}

// runWhy follows the task's blockers down to the ready tasks that would
// move it and the blockers no work clears (implementation-spec.md, Queries:
// follow blockers). The search goes level by level, each level by ID, so
// the tasks come out nearest first, then by ID. Only a blocker whose one
// task file is usable and open is followed; every other blocking ID is
// stuck, and so is every task in a cycle among those followed.
func runWhy(env Env, in WhyInput, w *errs.Collector) (any, *errs.Error) {
	out := WhyOutput{Ready: []model.ID{}, Stuck: []model.ID{}}
	e := store.Read(env.Env, w, func(tx *store.Tx) *errs.Error {
		copies, e := findCopies(tx, in.ID)
		if e != nil {
			return e
		}
		if len(copies) > 1 {
			return errs.Conflict(errs.RuleDuplicateID, []int64{int64(in.ID)})
		}
		seen := map[model.ID]bool{in.ID: true}
		edges := map[model.ID][]model.ID{}
		var views, ready []model.TaskView
		for level := copies; len(level) > 0; {
			var next []*store.Loaded
			for _, ld := range level {
				v, states, e := viewStates(tx, ld)
				if e != nil {
					return e
				}
				views = append(views, v)
				if v.Readiness == model.Ready {
					ready = append(ready, v)
				}
				for _, b := range v.Blocking {
					if states[b] != graph.BlockerOpen {
						out.Stuck = append(out.Stuck, b)
						continue
					}
					edges[v.ID] = append(edges[v.ID], b)
					if !seen[b] {
						seen[b] = true
						next = append(next, tx.Load(tx.Copies(b)[0]))
					}
				}
			}
			slices.SortFunc(next, func(a, b *store.Loaded) int { return cmp.Compare(a.Loc.ID, b.Loc.ID) })
			level = next
		}
		for _, g := range graph.CycleGroups(edges) {
			c := graph.ExampleCycle(g, edges)
			ids := make([]int64, len(c))
			for i, id := range c {
				ids[i] = int64(id)
			}
			tx.Warn(errs.Cycle(ids))
			out.Stuck = append(out.Stuck, g...)
		}
		slices.Sort(out.Stuck)
		out.Stuck = slices.Compact(out.Stuck)
		slices.SortFunc(ready, frontierOrder)
		for _, v := range ready {
			out.Ready = append(out.Ready, v.ID)
		}
		out.Readiness = views[0].Readiness
		if in.IncludeTasks {
			out.Tasks = &Tasks{Views: views, Fields: in.Fields}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
