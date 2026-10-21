package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/doctor"
)

// Finding is what doctor reports and repair acts on.
type Finding = doctor.Finding

// DoctorResult is doctor's result, in its schema's order.
type DoctorResult struct {
	Findings []Finding `json:"findings"`
}

// Doctor compares the tree with the database and checks the database
// itself, in one snapshot, taking no lock. It changes nothing, and gives
// no warnings: its findings carry the pending and stuck steps.
func Doctor() *cli.Command {
	return &cli.Command{
		Name: "doctor",
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			found, e := doctor.Check(s)
			if e != nil {
				return nil, nil, e
			}
			return DoctorResult{Findings: findings(found)}, nil, nil
		},
	}
}

// findings is a list of findings, [] when there are none.
func findings(found []Finding) []Finding {
	if found == nil {
		return []Finding{}
	}
	return found
}
