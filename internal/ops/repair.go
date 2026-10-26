package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/doctor"
)

// RepairResult is repair's result, in its schema's order.
type RepairResult struct {
	Fixed []Finding `json:"fixed"`
	Left  []Finding `json:"left"`
}

// Repair puts the tree back to what the database says, for the kinds
// given, all of them when none are: a write, holding the lock.
func Repair() *cli.Command {
	return &cli.Command{
		Name:    "repair",
		Options: []cli.Option{{Field: "kinds", Kind: cli.List}},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			kinds := r.choices("kinds", doctor.RepairKinds, doctor.RepairKinds)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			fixed, left, warnings, e := doctor.Repair(s, kinds)
			if e != nil {
				return nil, nil, e
			}
			return RepairResult{Fixed: findings(fixed), Left: findings(left)}, warnings, nil
		},
	}
}
