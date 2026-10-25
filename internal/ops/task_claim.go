package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// TaskClaimResult is task claim's result, in its schema's order.
type TaskClaimResult struct {
	Claim Claim `json:"claim"`
}

// TaskClaimNextResult is task claim-next's result, in its schema's
// order: both null when the frontier is empty.
type TaskClaimNextResult struct {
	Task  *Task  `json:"task"`
	Claim *Claim `json:"claim"`
}

// TaskClaim claims an open task with no claim, blocked or not.
func TaskClaim() *cli.Command {
	return &cli.Command{
		Name:    "task claim",
		Subject: &cli.Option{Field: "task", Required: true, Complete: claimable.complete()},
		Options: []cli.Option{{Field: "extra", Kind: cli.JSON}},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("task", core.WantTask, "")
			extra := r.object("extra")
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				res, err := resolve(tx, ref, core.WantTask, "task")
				if err != nil {
					return nil, nil, err
				}
				id := res.ID
				claimed, err := tx.Claimed(id)
				switch {
				case err != nil:
					return nil, nil, err
				case claimed:
					return nil, nil, core.Conflict(core.RuleClaimed, []int64{id}, nil, "@%d is already claimed", id)
				case !tx.Open(id):
					return nil, nil, core.Conflict(core.RuleComplete, []int64{id}, nil, "@%d is complete; only an open task can be claimed", id)
				}
				c, err := tx.InsertClaim(id, extra)
				if err != nil {
					return nil, nil, err
				}
				return TaskClaimResult{Claim: claimOf(c)}, nil, nil
			})
		},
	}
}

// TaskClaimNext claims the first task of the frontier within a scope, as
// task frontier finds it. The record it returns shows the new claim.
func TaskClaimNext() *cli.Command {
	return &cli.Command{
		Name:    "task claim-next",
		Subject: &cli.Option{Field: "folder", Complete: foldersOnly.complete()},
		Options: []cli.Option{
			{Field: "recursive", Kind: cli.Bool},
			{Field: "tags_any", Kind: cli.List},
			{Field: "tags_all", Kind: cli.List},
			{Field: "extra", Kind: cli.JSON},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref, sc := r.scope()
			extra := r.object("extra")
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				var err error
				if sc.folder, err = resolveFolder(tx, ref, "folder"); err != nil {
					return nil, nil, err
				}
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, nil, err
				}
				matches := t.frontier(sc)
				if len(matches) == 0 {
					return TaskClaimNextResult{}, nil, nil
				}
				id := matches[0].ID
				c, err := tx.InsertClaim(id, extra)
				if err != nil {
					return nil, nil, err
				}
				t.claims[id] = c
				rec, claim := t.record(id), claimOf(c)
				return TaskClaimNextResult{Task: &rec, Claim: &claim}, nil, nil
			})
		},
	}
}

// TaskRelease ends a task's claim, whoever made it.
func TaskRelease() *cli.Command {
	return taskCommand("task release", claimedOnes, func(tx *store.Tx, id int64) (any, []cli.Warning, error) {
		changed, err := tx.EndClaim(id)
		if err != nil {
			return nil, nil, err
		}
		return TaskUpdateResult{ID: id, Changed: changed}, nil, nil
	})
}

// claimOf is a claim's record.
func claimOf(c store.ClaimRow) Claim {
	return Claim{Task: c.Task, ClaimedAt: c.ClaimedAt, Extra: c.Extra}
}
