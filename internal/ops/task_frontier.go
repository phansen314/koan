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
		Name:    "frontier",
		Subject: &cli.Option{Field: "folder", Complete: foldersOnly.complete()},
		Options: []cli.Option{
			{Field: "file", Kind: cli.Path},
			{Field: "direct", Kind: cli.Bool},
			{Field: "tags_any", Kind: cli.List},
			{Field: "tags_all", Kind: cli.List},
			{Field: "tags_none", Kind: cli.List},
			{Field: "limit", Kind: cli.Integer, Nullable: true},
			{Field: "fields", Kind: cli.List},
		},
		Here: &cli.Here{Field: "file", Flag: "--here"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			q, sc := r.scope()
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
			var stale []cli.Warning
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, err
				}
				if sc.folder, _, stale, err = subjectFolder(tx, t, q); err != nil {
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
			return res, append(warnings, stale...), nil
		},
	}
}

// scope is where the frontier is looked for, as task list's filter gives
// it: a folder or the root, at any depth unless direct, cut to
// the tasks that pass its tag filter.
type scope struct {
	folder int64
	direct bool
	tags   tagFilter
}

// tagFilter is the filter's three tag fields: a task must pass each one
// given.
type tagFilter struct {
	any, all, none []string
}

// tagFilter reads the filter's tag fields. A tag in tags_none and in
// tags_any or tags_all is refused, at its entry in tags_none.
func (r *input) tagFilter() tagFilter {
	f := tagFilter{any: r.tags("tags_any"), all: r.tags("tags_all"), none: r.tags("tags_none")}
	r.both("tags_none", "is both wanted and left out", func(tag string) bool {
		return slices.Contains(f.any, tag) || slices.Contains(f.all, tag)
	})
	return f
}

// given is whether any of the three filters anything.
func (f tagFilter) given() bool {
	return len(f.any) > 0 || len(f.all) > 0 || len(f.none) > 0
}

// passes is whether a task holding has passes the filter.
func (f tagFilter) passes(has []string) bool {
	holds := func(tag string) bool { return slices.Contains(has, tag) }
	return (len(f.any) == 0 || slices.ContainsFunc(f.any, holds)) &&
		!slices.ContainsFunc(f.all, func(tag string) bool { return !holds(tag) }) &&
		!slices.ContainsFunc(f.none, holds)
}

// scope reads task list's filter, but for its folder, which resolves
// in the store.
func (r *input) scope() (*query, scope) {
	return r.foldering("folder"), scope{direct: r.boolean("direct", false), tags: r.tagFilter()}
}

// frontier is the ready tasks with no claim within a scope, in work
// order. The scope chooses where to look, never what is ready.
func (t *tasks) frontier(s scope) []core.Ranked {
	var candidates []core.Ranked
	for _, id := range t.order {
		if !t.in(id, s.folder, s.direct) || !s.tags.passes(t.tags[id]) {
			continue
		}
		candidates = append(candidates, core.Ranked{ID: id, Priority: t.rows[id].Priority})
	}
	claimed := func(id int64) bool { _, ok := t.claims[id]; return ok }
	return core.Frontier(t, claimed, candidates)
}
