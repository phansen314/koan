package ops

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// BatchResult is batch's result, in its schema's order.
type BatchResult struct {
	FoldersMade []FolderAt       `json:"folders_made"`
	TasksMade   []int64          `json:"tasks_made"`
	Labels      map[string]int64 `json:"labels"`
	GatesMade   []GateMade       `json:"gates_made"`
	Reblocked   []int64          `json:"reblocked"`
}

// maxParts is the most folders and tasks a batch holds between them.
const maxParts = 1000

// batchFolder is a folder part, its shape checked. At is its path's
// pointer.
type batchFolder struct {
	at    string
	ref   core.Ref
	title *string
	joins []joinIn
	extra json.RawMessage
	notes string
}

// batchTask is a task part, its shape checked. At is its own pointer;
// folder is nil when it names none, and joins when it gives no join of
// its own.
type batchTask struct {
	at       string
	label    string
	title    string
	folder   *core.Ref
	priority *int64
	tags     []string
	edges    []edgeIn
	joins    []joinIn
	extra    json.RawMessage
	notes    string
}

// batchInput is a batch file, its shape checked.
type batchInput struct {
	folders []batchFolder
	folder  core.Ref
	joins   []joinIn
	tasks   []batchTask
}

// Batch makes folders and tasks and writes edges as one change, read as
// the tree it would leave: its folders first, parents first in tree
// order, then its tasks in input order, binding each label, then every
// edge and join, making any gate named, with the cycle check over them
// all. A task that gives no join of its own takes the batch's.
func Batch() *cli.Command {
	return &cli.Command{
		Name:   "batch",
		Fields: []string{"folders", "folder", "join", "tasks"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, batch: true}
			b := readBatch(&r)
			folders, tasks := b.folders, b.tasks
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				res := BatchResult{FoldersMade: []FolderAt{}, TasksMade: []int64{}, Labels: map[string]int64{}}
				order := make([]int, len(folders))
				for i := range order {
					order[i] = i
				}
				slices.SortStableFunc(order, func(a, b int) int {
					return slices.Compare(folders[a].ref.Names, folders[b].ref.Names)
				})
				ids := make([]int64, len(folders))
				for _, i := range order {
					f := folders[i]
					made, err := makeFolders(tx, s.Root, f.ref, f.title, f.extra, f.notes)
					if err != nil {
						return nil, nil, pointAt(err, f.at)
					}
					res.FoldersMade = append(res.FoldersMade, made...)
					ids[i] = *made[len(made)-1].ID
				}
				// The batch's folder resolves whether or not a task takes it.
				at, err := batchFolderAt(tx, b.folder, "/folder")
				if err != nil {
					return nil, nil, err
				}
				w := newEdges(tx)
				w.labels = res.Labels
				for _, x := range tasks {
					folder := at
					if x.folder != nil {
						if folder, err = batchFolderAt(tx, *x.folder, x.at+"/folder"); err != nil {
							return nil, nil, err
						}
					}
					id, _, err := makeTask(tx, folder, x.title, x.priority, x.tags, x.extra, x.notes)
					if err != nil {
						return nil, nil, err
					}
					w.new[id] = true
					res.TasksMade = append(res.TasksMade, id)
					if x.label != "" {
						res.Labels[x.label] = id
					}
				}
				// The batch's join resolves whether or not a task takes it,
				// before any other edge.
				for _, j := range b.joins {
					if _, err := core.Resolve(tx, j.ref, core.WantFolder, w.labels); err != nil {
						return nil, nil, pointAt(err, j.at)
					}
				}
				// A folder part's joins name its own gates, then those of
				// each folder joined, before any task part's edges.
				var all []edge
				for i, f := range folders {
					if len(f.joins) == 0 {
						continue
					}
					start, done, err := w.gates(ids[i], true)
					if err != nil {
						return nil, nil, err
					}
					js, err := w.join(start, done, f.joins, true)
					if err != nil {
						return nil, nil, err
					}
					all = append(all, js...)
				}
				for i, x := range tasks {
					id := res.TasksMade[i]
					es, err := w.resolve(id, x.edges, true)
					if err != nil {
						return nil, nil, err
					}
					joins := b.joins
					if x.joins != nil {
						joins = x.joins
					}
					js, err := w.join(id, id, joins, true)
					if err != nil {
						return nil, nil, err
					}
					all = append(all, append(es, js...)...)
				}
				_, reblocked, warnings, err := w.add(all)
				if err != nil {
					return nil, nil, err
				}
				res.GatesMade, res.Reblocked = w.made, reblocked
				return res, warnings, nil
			})
		},
	}
}

// readBatch checks a batch file's shape, part by part, in its schema's
// field order, then that no two paths in it differ only in case.
func readBatch(r *input) batchInput {
	n := 0
	for _, field := range []string{"folders", "tasks"} {
		if list, ok := r.in[field].([]any); ok {
			n += len(list)
		}
	}
	if n < 1 || n > maxParts {
		r.fault("", "a batch holds 1 to %d parts, folders and tasks together, not %d", maxParts, n)
	}
	var refs []core.Ref
	var at []string
	var folders []batchFolder
	list, _ := r.list("folders")
	for i, v := range list {
		p, ok := r.part(fmt.Sprintf("/folders/%d", i), v, "path", "title", "join", "extra", "notes")
		if !ok {
			continue
		}
		before := len(r.problems)
		f := batchFolder{at: p.at("path"), ref: p.path("path")}
		if len(r.problems) == before {
			refs, at = append(refs, f.ref), append(at, f.at)
		}
		f.title, f.joins = p.title("title"), p.joinRefs("join")
		for _, j := range f.joins {
			refs, at = append(refs, j.ref), append(at, j.at)
		}
		f.extra, f.notes = p.object("extra"), p.text("notes", "")
		folders = append(folders, f)
	}
	inBatch := r.ref("folder", core.WantFolder, "/")
	if _, given := r.value("folder"); given {
		refs, at = append(refs, inBatch), append(at, "/folder")
	}
	joins := r.joinRefs("join")
	for _, j := range joins {
		refs, at = append(refs, j.ref), append(at, j.at)
	}
	var tasks []batchTask
	defined := map[string]bool{}
	list, _ = r.list("tasks")
	for i, v := range list {
		p, ok := r.part(fmt.Sprintf("/tasks/%d", i), v,
			"label", "title", "folder", "priority", "tags", "blocked_by", "blocks", "join", "extra", "notes")
		if !ok {
			continue
		}
		x := batchTask{at: p.prefix, label: p.label("label")}
		if x.label != "" {
			if defined[x.label] {
				p.fault(p.at("label"), "label %s is defined twice", x.label)
			}
			defined[x.label] = true
		}
		x.title = p.taskTitle("title")
		if _, given := p.value("folder"); given {
			folder := p.ref("folder", core.WantFolder, "/")
			x.folder = &folder
			refs, at = append(refs, folder), append(at, p.at("folder"))
		}
		x.priority, x.tags = p.priority("priority"), p.tags("tags")
		x.edges = p.edgeRefs()
		for _, e := range x.edges {
			refs, at = append(refs, e.ref), append(at, e.at)
		}
		if _, given := p.value("join"); given {
			x.joins = p.joinRefs("join")
			if x.joins == nil {
				x.joins = []joinIn{}
			}
		}
		for _, j := range x.joins {
			refs, at = append(refs, j.ref), append(at, j.at)
		}
		x.extra, x.notes = p.object("extra"), p.text("notes", "")
		tasks = append(tasks, x)
	}
	r.sameCase(refs, at)
	return batchInput{folders: folders, folder: inBatch, joins: joins, tasks: tasks}
}

// batchFolderAt resolves a folder a batch's task goes in, given at a
// pointer, to a folder or the root's RootID; a refusal points at it.
func batchFolderAt(tx *store.Tx, ref core.Ref, at string) (int64, error) {
	res, err := resolveAt(tx, ref, core.WantFolder, at)
	if err != nil {
		return 0, pointAt(err, at)
	}
	if res.Kind == core.ResolvedRoot {
		return core.RootID, nil
	}
	return res.ID, nil
}
