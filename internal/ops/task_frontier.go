package ops

import (
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// TaskFrontier lists the ready tasks with no claim under a folder, gates
// included, in work order. The folder chooses where to look, never what
// is ready.
func TaskFrontier() *cli.Command {
	return &cli.Command{
		Name: "task frontier",
		Options: []cli.Option{
			{Field: "folder", Kind: cli.String, Complete: foldersOnly.complete()},
			{Field: "recursive", Kind: cli.Bool},
			{Field: "tags_any", Kind: cli.List},
			{Field: "tags_all", Kind: cli.List},
			{Field: "limit", Kind: cli.Integer, Nullable: true},
			{Field: "fields", Kind: cli.List},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			ref, sc := r.scope()
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
				var err error
				if sc.folder, err = resolveFolder(tx, ref, "folder"); err != nil {
					return nil, err
				}
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, err
				}
				matches := t.frontier(sc)
				res.Total = len(matches)
				if limit != nil && int64(len(matches)) > *limit {
					matches, res.Truncated = matches[:*limit], true
				}
				var paths []fsstep.Path
				for _, m := range matches {
					c := Cut[Task]{Record: t.record(m.ID), Fields: fields}
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

// scope is where the frontier is looked for, as task list's filter gives
// it: a folder or the root, at any depth unless recursive is false, cut to
// the tasks holding any of tagsAny and all of tagsAll.
type scope struct {
	folder           int64
	recursive        bool
	tagsAny, tagsAll []string
}

// scope reads task list's filter, but for its folder, which resolves
// in the store.
func (r *input) scope() (core.Ref, scope) {
	ref := r.ref("folder", core.WantFolder, "/")
	return ref, scope{recursive: r.boolean("recursive", true), tagsAny: r.tags("tags_any"), tagsAll: r.tags("tags_all")}
}

// frontier is the ready tasks with no claim within a scope, in work
// order. The scope chooses where to look, never what is ready.
func (t *tasks) frontier(s scope) []core.Ranked {
	var candidates []core.Ranked
	for _, id := range t.order {
		has := t.tags[id]
		if !t.in(id, s.folder, s.recursive) ||
			len(s.tagsAny) > 0 && !slices.ContainsFunc(s.tagsAny, func(tag string) bool { return slices.Contains(has, tag) }) ||
			slices.ContainsFunc(s.tagsAll, func(tag string) bool { return !slices.Contains(has, tag) }) {
			continue
		}
		candidates = append(candidates, core.Ranked{ID: id, Priority: t.rows[id].Priority})
	}
	claimed := func(id int64) bool { _, ok := t.claims[id]; return ok }
	return core.Frontier(t, claimed, candidates)
}
