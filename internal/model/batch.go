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
	Extra map[string]any
}

// BatchTask is a task a batch makes. An empty Folder means the batch's
// folder; an empty Label means none.
type BatchTask struct {
	Label string
	TaskInput
}

// BatchInput is a batch file.
type BatchInput struct {
	Folders []BatchFolder
	Folder  string // where tasks go that name no folder; empty means the root
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
// input order, then edges.
func (t *Tree) Batch(in BatchInput) (out BatchResult, err error) {
	err = t.change(func(c *Tree) error {
		// Every part's shape first, in the batch file's field order.
		var s shape
		if n := len(in.Folders) + len(in.Tasks); n < 1 || n > 1000 {
			s.faultf("", "a batch holds 1 to 1000 parts, not %d", n)
		}
		folderRefs := make([]core.Ref, len(in.Folders))
		titles := make([]*string, len(in.Folders))
		for i, f := range in.Folders {
			folderRefs[i] = s.path(fmt.Sprintf("/folders/%d/path", i), f.Path)
			titles[i] = s.folderTitle(fmt.Sprintf("/folders/%d/title", i), f.Title)
		}
		batchFolder := in.Folder
		if batchFolder == "" {
			batchFolder = "/"
		}
		batchRef := s.ref("/folder", batchFolder, core.WantFolder, true)
		type part struct {
			title  string
			folder core.Ref
			tags   map[string]bool
			bb, bl []core.Ref
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
		for _, i := range order {
			made, err := c.createFolder(folderRefs[i], titles[i], in.Folders[i].Extra)
			if err != nil {
				return problem(err, "/folders/%d/path", i)
			}
			out.Folders = append(out.Folders, made...)
		}

		var edges []edgeIn
		for i, x := range in.Tasks {
			p := parts[i]
			pointer := fmt.Sprintf("/tasks/%d/folder", i)
			if x.Folder == "" {
				pointer = "/folder"
			}
			r, err := core.Resolve(c, p.folder, core.WantFolder, labels)
			if err != nil {
				return problem(err, "%s", pointer)
			}
			task := c.newTask(p.title, x.Priority, p.tags, x.Extra, r.ID)
			out.Tasks = append(out.Tasks, task.ID)
			if x.Label != "" {
				labels[x.Label] = task.ID
			}
			prefix := fmt.Sprintf("/tasks/%d", i)
			edges = append(edges, edgeInputs(task.ID, p.bb, false, prefix)...)
			edges = append(edges, edgeInputs(task.ID, p.bl, true, prefix)...)
		}

		resolved, made, err := c.resolveEdges(edges, labels, true, true)
		if err != nil {
			return err
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
