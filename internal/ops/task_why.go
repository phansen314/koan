package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// TaskWhyResult is task why's result, in its schema's order.
type TaskWhyResult struct {
	ID        int64     `json:"id"`
	Readiness *string   `json:"readiness"`
	Blockers  []EdgeEnd `json:"blockers"`
	Ready     []int64   `json:"ready"`
}

// TaskWhy says what a task waits on: its open blockers, and the ready
// tasks it waits on at any depth through open blockers.
func TaskWhy() *cli.Command {
	return &cli.Command{
		Name:    "task why",
		Subject: &cli.Option{Field: "task", Required: true},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("task", core.WantTask, "")
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			res := TaskWhyResult{Blockers: []EdgeEnd{}, Ready: []int64{}}
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				got, err := resolve(tx, ref, core.WantTask, "task")
				if err != nil {
					return nil, err
				}
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, err
				}
				res.ID, res.Readiness = got.ID, t.readiness(got.ID)
				if !core.Blocked(t, got.ID) {
					return nil, nil
				}
				var open []int64
				for _, b := range t.blockedBy[got.ID] {
					if t.Open(b) {
						open = append(open, b)
					}
				}
				res.Blockers = t.ends(open)
				// A complete task holds nothing back, so the walk never
				// passes one; a ready task waits on nothing open, so it
				// stops there.
				seen := map[int64]bool{got.ID: true}
				var ready []core.Ranked
				for len(open) > 0 {
					id := open[0]
					open = open[1:]
					if seen[id] {
						continue
					}
					seen[id] = true
					if core.Ready(t, id) {
						ready = append(ready, core.Ranked{ID: id, Priority: t.rows[id].Priority})
						continue
					}
					for _, b := range t.blockedBy[id] {
						if t.Open(b) {
							open = append(open, b)
						}
					}
				}
				core.SortWorkOrder(ready)
				for _, x := range ready {
					res.Ready = append(res.Ready, x.ID)
				}
				return nil, nil
			})
			if e != nil {
				return nil, nil, e
			}
			return res, warnings, nil
		},
	}
}
