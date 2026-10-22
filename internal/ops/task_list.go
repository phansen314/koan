package ops

import (
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// TaskListResult is task list's result, in its schema's order.
type TaskListResult struct {
	Tasks     []Cut[Task] `json:"tasks"`
	Total     int         `json:"total"`
	Truncated bool        `json:"truncated"`
}

// TaskList lists the tasks under a folder, gates included, in tree
// order, as its filters say.
func TaskList() *cli.Command {
	return &cli.Command{
		Name: "task list",
		Options: []cli.Option{
			{Field: "folder", Kind: cli.String, Complete: foldersOnly.complete()},
			{Field: "recursive", Kind: cli.Bool},
			{Field: "state", Kind: cli.List},
			{Field: "readiness", Kind: cli.List},
			{Field: "tags_any", Kind: cli.List},
			{Field: "tags_all", Kind: cli.List},
			{Field: "claimed", Kind: cli.Bool, Nullable: true},
			{Field: "limit", Kind: cli.Integer, Nullable: true},
			{Field: "fields", Kind: cli.List},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref := r.ref("folder", core.WantFolder, "/")
			recursive := r.boolean("recursive", true)
			states := r.choices("state", []string{"open", "complete"}, []string{"open"})
			readiness := r.choices("readiness", []string{"ready", "blocked"}, []string{"ready", "blocked"})
			tagsAny := r.tags("tags_any")
			tagsAll := r.tags("tags_all")
			claimed := r.nullBool("claimed")
			limit := r.limit("limit", 100)
			fields := r.fields("fields", taskFields)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			res := TaskListResult{Tasks: []Cut[Task]{}}
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				folder, err := resolveFolder(tx, ref, "folder")
				if err != nil {
					return nil, err
				}
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, err
				}
				keep := func(id int64) bool {
					if !t.in(id, folder, recursive) || !slices.Contains(states, t.state(id)) {
						return false
					}
					if ready := t.readiness(id); ready != nil && !slices.Contains(readiness, *ready) {
						return false
					}
					has := t.tags[id]
					if len(tagsAny) > 0 && !slices.ContainsFunc(tagsAny, func(tag string) bool { return slices.Contains(has, tag) }) {
						return false
					}
					if slices.ContainsFunc(tagsAll, func(tag string) bool { return !slices.Contains(has, tag) }) {
						return false
					}
					_, isClaimed := t.claims[id]
					return claimed == nil || *claimed == isClaimed
				}
				var matches []int64
				for _, id := range t.order {
					if keep(id) {
						matches = append(matches, id)
					}
				}
				res.Total = len(matches)
				if limit != nil && int64(len(matches)) > *limit {
					matches, res.Truncated = matches[:*limit], true
				}
				var paths []fsstep.Path
				for _, id := range matches {
					c := Cut[Task]{Record: t.record(id), Fields: fields}
					res.Tasks = append(res.Tasks, c)
					paths = append(paths, taskPaths(c)...)
				}
				return paths, nil
			})
			if e != nil {
				return nil, nil, e
			}
			return res, warnings, nil
		},
	}
}
