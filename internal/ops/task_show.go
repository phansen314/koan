package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// TaskShowResult is task show's result, in its schema's order.
type TaskShowResult struct {
	Task Task `json:"task"`
}

// TaskShow reads one task, a gate included.
func TaskShow() *cli.Command {
	return &cli.Command{
		Name:    "task show",
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
			var res TaskShowResult
			var notes string
			var making bool
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				got, err := resolve(tx, ref, core.WantTask, "task")
				if err != nil {
					return nil, err
				}
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, err
				}
				res.Task = t.record(got.ID)
				notes = t.notesRel(got.ID)
				if making, err = fsstep.Makes(tx, notes); err != nil {
					return nil, err
				}
				return taskPaths(Cut[Task]{Record: res.Task}), nil
			})
			if e != nil {
				return nil, nil, e
			}
			if !making {
				w, e := missingNotes(s.Root, res.Task.ID, notes)
				if e != nil {
					return nil, nil, e
				}
				warnings = append(warnings, w...)
			}
			return res, warnings, nil
		},
	}
}
