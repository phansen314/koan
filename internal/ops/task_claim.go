package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// TaskClaimResult is claim's result, in its schema's order.
type TaskClaimResult struct {
	Claimed []int64 `json:"claimed"`
}

// TaskClaimNextResult is claim-next's result, in its schema's
// order: the task it claimed, its claim included, or null when the
// frontier is empty.
type TaskClaimNextResult struct {
	Task *Task `json:"task"`
}

// TaskClaim claims open tasks with no claim, blocked or not, as one
// change: if any is refused, none is claimed. extra is kept with every
// claim it makes.
func TaskClaim() *cli.Command {
	return &cli.Command{
		Name:    "claim",
		Subject: tasksSubject(claimable),
		Options: []cli.Option{{Field: "extra", Kind: cli.JSON}},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			refs, at := r.taskRefs()
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
				ids, err := resolveTasks(tx, refs, at)
				if err != nil {
					return nil, nil, err
				}
				for _, id := range ids {
					claimed, err := tx.Claimed(id)
					switch {
					case err != nil:
						return nil, nil, err
					case claimed:
						return nil, nil, core.Conflict(core.RuleClaimed, []int64{id}, nil, "@%d is already claimed", id)
					case !tx.Open(id):
						return nil, nil, core.Conflict(core.RuleAlreadyComplete, []int64{id}, nil, "@%d is complete; only an open task can be claimed", id)
					}
					if _, err := tx.InsertClaim(id, extra); err != nil {
						return nil, nil, err
					}
				}
				return TaskClaimResult{Claimed: ids}, nil, nil
			})
		},
	}
}

// TaskClaimNext claims the first task of the frontier within a scope, as
// frontier finds it. The record it returns shows the new claim.
func TaskClaimNext() *cli.Command {
	return &cli.Command{
		Name:    "claim-next",
		Subject: &cli.Option{Field: "folder", Complete: foldersOnly.complete()},
		Options: []cli.Option{
			{Field: "file", Kind: cli.Path},
			{Field: "direct", Kind: cli.Bool},
			{Field: "tags_any", Kind: cli.List},
			{Field: "tags_all", Kind: cli.List},
			{Field: "extra", Kind: cli.JSON},
		},
		Here: &cli.Here{Field: "file", Flag: "--here"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			q, sc := r.scope()
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
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, nil, err
				}
				var stale []cli.Warning
				if sc.folder, _, stale, err = subjectFolder(tx, t, q); err != nil {
					return nil, nil, err
				}
				matches := t.frontier(sc)
				if len(matches) == 0 {
					return TaskClaimNextResult{}, stale, nil
				}
				id := matches[0].ID
				c, err := tx.InsertClaim(id, extra)
				if err != nil {
					return nil, nil, err
				}
				t.claims[id] = c
				rec := t.record(id)
				return TaskClaimNextResult{Task: &rec}, stale, nil
			})
		},
	}
}

// TaskUnclaimResult is unclaim's result, in its schema's order.
type TaskUnclaimResult struct {
	Unclaimed []int64 `json:"unclaimed"`
}

// TaskUnclaim ends the claims of tasks, whoever made them, as one change.
func TaskUnclaim() *cli.Command {
	return tasksCommand("unclaim", claimedOnes, func(tx *store.Tx, ids []int64) (any, []cli.Warning, error) {
		unclaimed := []int64{}
		for _, id := range ids {
			changed, err := tx.EndClaim(id)
			if err != nil {
				return nil, nil, err
			}
			if changed {
				unclaimed = append(unclaimed, id)
			}
		}
		return TaskUnclaimResult{Unclaimed: unclaimed}, nil, nil
	})
}

// claimOf is a claim's record.
func claimOf(c store.ClaimRow) Claim {
	return Claim{Task: c.Task, ClaimedAt: c.ClaimedAt, Extra: c.Extra}
}
