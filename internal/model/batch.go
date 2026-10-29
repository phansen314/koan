package model

import (
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/core"
)

// BatchFolder is a folder a batch makes.
type BatchFolder struct {
	Path  string
	Title *string
	Join  []string
	Extra map[string]any
}

// BatchTask is a task a batch makes. An empty Folder means the batch's
// folder; an empty Label means none; a nil Join means the batch's join,
// and an empty one none.
type BatchTask struct {
	Label string
	TaskInput
}

// BatchInput is a batch file.
type BatchInput struct {
	Folders []BatchFolder
	Folder  string   // where tasks go that name no folder; empty means the root
	Join    []string // what tasks join that give no join of their own
	Tasks   []BatchTask
}

// BatchResult is what a batch returns.
type BatchResult struct {
	Folders   []FolderAt
	Tasks     []int64
	Labels    map[string]int64
	GatesMade []GateMade
	Reblocked []int64
	Warnings  []Warning
}

// Batch makes folders and tasks and writes edges as one change: all of it,
// or none. Its references resolve against the tree as the whole batch
// would leave it, so folders are made first, in tree order, then tasks in
// input order, then the batch's join is resolved, then the folder parts'
// joins, then each task part's edges and joins.
func (t *Tree) Batch(in BatchInput) (out BatchResult, err error) {
	err = t.change(func(c *Tree) error {
		// Every part's shape first, in the batch file's field order.
		var s shape
		if n := len(in.Folders) + len(in.Tasks); n < 1 || n > 1000 {
			s.faultf("", "a batch holds 1 to 1000 parts, not %d", n)
		}
		folderRefs := make([]core.Ref, len(in.Folders))
		titles := make([]*string, len(in.Folders))
		folderJoins := make([][]joinIn, len(in.Folders))
		for i, f := range in.Folders {
			folderRefs[i] = s.path(fmt.Sprintf("/folders/%d/path", i), f.Path)
			titles[i] = s.folderTitle(fmt.Sprintf("/folders/%d/title", i), f.Title)
			folderJoins[i] = s.joinRefs(fmt.Sprintf("/folders/%d/join", i), f.Join, true)
		}
		batchFolder := in.Folder
		if batchFolder == "" {
			batchFolder = "/"
		}
		batchRef := s.ref("/folder", batchFolder, core.WantFolder, true)
		batchJoins := s.joinRefs("/join", in.Join, true)
		type part struct {
			title  string
			folder core.Ref
			tags   map[string]bool
			bb, bl []core.Ref
			joins  []joinIn
		}
		parts := make([]part, len(in.Tasks))
		labels := map[string]int64{}
		for i, x := range in.Tasks {
			prefix := fmt.Sprintf("/tasks/%d", i)
			if x.Label != "" {
				switch _, ok := labels[x.Label]; {
				case !core.ValidLabel(x.Label):
					s.faultf(prefix+"/label", "%q is not a label", x.Label)
				case ok:
					s.faultf(prefix+"/label", "label %s is defined twice", x.Label)
				}
				labels[x.Label] = 0
			}
			p := &parts[i]
			p.title = s.title(prefix+"/title", x.Title)
			p.folder = batchRef
			if x.Folder != "" {
				p.folder = s.ref(prefix+"/folder", x.Folder, core.WantFolder, true)
			}
			s.priority(prefix+"/priority", x.Priority)
			p.tags = s.tags(prefix+"/tags", x.Tags)
			p.bb, p.bl = s.edgeRefs(prefix, x.BlockedBy, x.Blocks, true)
			p.joins = batchJoins
			if x.Join != nil {
				p.joins = s.joinRefs(prefix+"/join", x.Join, true)
			}
		}
		if err := s.done(nil); err != nil {
			return err
		}

		// Folders are made parents first, in tree order, whatever the
		// input's order.
		order := make([]int, len(in.Folders))
		for i := range order {
			order[i] = i
		}
		slices.SortStableFunc(order, func(a, b int) int {
			return slices.Compare(folderRefs[a].Names, folderRefs[b].Names)
		})
		before := c.readySet()
		ids := make([]int64, len(in.Folders))
		for _, i := range order {
			made, err := c.createFolder(folderRefs[i], fmt.Sprintf("/folders/%d/path", i), titles[i], in.Folders[i].Extra)
			if err != nil {
				return problem(err, "/folders/%d/path", i)
			}
			out.Folders = append(out.Folders, made...)
			ids[i] = made[len(made)-1].ID
		}

		// The batch's folder resolves whether or not a task takes it.
		inBatch, err := core.Resolve(c, batchRef, core.WantFolder, labels)
		if err != nil {
			return problem(err, "/folder")
		}
		for i, x := range in.Tasks {
			p := parts[i]
			r := inBatch
			if x.Folder != "" {
				if r, err = core.Resolve(c, p.folder, core.WantFolder, labels); err != nil {
					return problem(err, "/tasks/%d/folder", i)
				}
			}
			task := c.newTask(p.title, x.Priority, p.tags, x.Extra, r.ID)
			out.Tasks = append(out.Tasks, task.ID)
			if x.Label != "" {
				labels[x.Label] = task.ID
			}
		}

		// The batch's join resolves whether or not a task takes it.
		for _, j := range batchJoins {
			if _, err := core.Resolve(c, j.ref, core.WantFolder, labels); err != nil {
				return pointAt(err, j.pointer, true)
			}
		}
		// A folder part's joins name its own gates, then those of each
		// folder joined, before any task part's edges.
		var resolved []edge
		var made []GateMade
		for i, joins := range folderJoins {
			if len(joins) == 0 {
				continue
			}
			start, done, gates := c.gates(ids[i], true)
			js, joinMade, err := c.joinEdges(start, done, joins, labels, true, true)
			if err != nil {
				return err
			}
			resolved, made = append(resolved, js...), append(append(made, gates...), joinMade...)
		}
		for i, id := range out.Tasks {
			p, prefix := parts[i], fmt.Sprintf("/tasks/%d", i)
			es, edgeMade, err := c.resolveEdges(slices.Concat(
				edgeInputs(id, p.bb, false, prefix),
				edgeInputs(id, p.bl, true, prefix)), labels, true, true)
			if err != nil {
				return err
			}
			js, joinMade, err := c.joinEdges(id, id, p.joins, labels, true, true)
			if err != nil {
				return err
			}
			resolved = append(resolved, append(es, js...)...)
			made = append(append(made, edgeMade...), joinMade...)
		}
		warnings, _, err := c.addEdges(resolved)
		if err != nil {
			return err
		}
		out.Labels, out.GatesMade, out.Reblocked = labels, made, c.reblocked(before)
		out.Warnings = append(warnings, c.claimedBlocked(out.Reblocked)...)
		return nil
	})
	return out, err
}

// problem points a core error at the part of the input at fault.
func problem(err error, format string, args ...any) error {
	if e, ok := err.(*core.Error); ok && !slices.Contains(e.Problems, fmt.Sprintf(format, args...)) {
		e.Problems = append(e.Problems, fmt.Sprintf(format, args...))
	}
	return err
}
