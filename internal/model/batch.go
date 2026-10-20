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
		if n := len(in.Folders) + len(in.Tasks); n < 1 || n > 1000 {
			return invalid("a batch holds 1 to 1000 parts, not %d", n)
		}
		batchFolder := in.Folder
		if batchFolder == "" {
			batchFolder = "/"
		}

		// Every reference in the input, so one input never names two
		// spellings of a path.
		all := []string{batchFolder}
		for _, f := range in.Folders {
			all = append(all, f.Path)
		}
		for _, x := range in.Tasks {
			if x.Folder != "" {
				all = append(all, x.Folder)
			}
			all = append(all, x.BlockedBy...)
			all = append(all, x.Blocks...)
		}
		if _, err := parse(all...); err != nil {
			return err
		}

		labels := map[string]int64{}
		for i, x := range in.Tasks {
			if x.Label == "" {
				continue
			}
			if !core.ValidLabel(x.Label) {
				return problem(invalid("%q is not a label", x.Label), "/tasks/%d/label", i)
			}
			if _, ok := labels[x.Label]; ok {
				return problem(invalid("label %s is defined twice", x.Label), "/tasks/%d/label", i)
			}
			labels[x.Label] = 0
		}

		// Folders are made parents first, in tree order, whatever the
		// input's order.
		folderRefs := make([]core.Ref, len(in.Folders))
		order := make([]int, len(in.Folders))
		for i, f := range in.Folders {
			ref, err := pathRef(f.Path)
			if err != nil {
				return problem(err, "/folders/%d/path", i)
			}
			folderRefs[i], order[i] = ref, i
		}
		slices.SortStableFunc(order, func(a, b int) int {
			return slices.Compare(folderRefs[a].Names, folderRefs[b].Names)
		})

		before := c.readySet()
		for _, i := range order {
			f := in.Folders[i]
			title, err := checkFolderTitle(f.Title)
			if err != nil {
				return problem(err, "/folders/%d/title", i)
			}
			made, err := c.createFolder(folderRefs[i], title, f.Extra)
			if err != nil {
				return problem(err, "/folders/%d/path", i)
			}
			out.Folders = append(out.Folders, made...)
		}

		var edges []edgeIn
		for i, x := range in.Tasks {
			folderRef, pointer := x.Folder, fmt.Sprintf("/tasks/%d/folder", i)
			if folderRef == "" {
				folderRef, pointer = batchFolder, "/folder"
			}
			r, err := core.ResolveString(c, folderRef, core.WantFolder, labels)
			if err != nil {
				return problem(err, "%s", pointer)
			}
			task, err := c.newTask(x.TaskInput, r.ID)
			if err != nil {
				return problem(err, "/tasks/%d", i)
			}
			out.Tasks = append(out.Tasks, task.ID)
			if x.Label != "" {
				labels[x.Label] = task.ID
			}
			prefix := fmt.Sprintf("/tasks/%d", i)
			for _, group := range []struct {
				refs   []string
				blocks bool
			}{{x.BlockedBy, false}, {x.Blocks, true}} {
				refs, err := parse(group.refs...)
				if err != nil {
					return err
				}
				edges = append(edges, edgeInputs(task.ID, refs, group.blocks, prefix)...)
			}
		}

		resolved, made, err := c.resolveEdges(edges, labels, true)
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
