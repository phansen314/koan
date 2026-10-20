package ops

import (
	"fmt"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// TaskCreateResult is task create's result, in its schema's order.
type TaskCreateResult struct {
	ID        int64      `json:"id"`
	Ref       string     `json:"ref"`
	NotesPath string     `json:"notes_path"`
	GatesMade []GateMade `json:"gates_made"`
	Reblocked []int64    `json:"reblocked"`
}

// GateMade is a gate a change made because an edge named it.
type GateMade struct {
	ID     int64    `json:"id"`
	Folder FolderAt `json:"folder"`
	End    string   `json:"end"`
}

// TaskCreate makes a task in a folder or the root, with its notes file.
func TaskCreate() *cli.Command {
	return &cli.Command{
		Name:    "task create",
		Subject: &cli.Option{Field: "title", Required: true},
		Options: []cli.Option{
			{Field: "folder", Kind: cli.String},
			{Field: "priority", Kind: cli.Integer, Nullable: true},
			{Field: "tags", Kind: cli.List},
			{Field: "blocked_by", Kind: cli.List},
			{Field: "blocks", Kind: cli.List},
			{Field: "extra", Kind: cli.JSON},
			{Field: "notes", Kind: cli.String},
		},
		Files: []cli.File{{Name: "--notes-file", Field: "notes"}},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			title := r.taskTitle("title")
			folderRef := r.ref("folder", core.WantFolder, "/")
			priority := r.priority("priority")
			tags := r.tags("tags")
			blockedBy := r.refs("blocked_by", core.WantEdge)
			blocks := r.refs("blocks", core.WantEdge)
			extra := r.object("extra")
			notes := r.text("notes", "")
			refs, at := []core.Ref{folderRef}, []string{"/folder"}
			for i, ref := range blockedBy {
				refs, at = append(refs, ref), append(at, fmt.Sprintf("/blocked_by/%d", i))
			}
			for i, ref := range blocks {
				refs, at = append(refs, ref), append(at, fmt.Sprintf("/blocks/%d", i))
			}
			r.sameCase(refs, at)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				folder, err := resolveFolder(tx, folderRef, "folder")
				if err != nil {
					return nil, nil, err
				}
				id, err := tx.Issue(core.NodeTask)
				if err != nil {
					return nil, nil, err
				}
				row := store.TaskRow{ID: id, Folder: folder, Title: title, Priority: priority, Extra: extra}
				if err := tx.InsertTask(row, tags); err != nil {
					return nil, nil, err
				}
				path := tx.Path(folder)
				notesPath := fsstep.TaskNotesPath(path, id, core.EndNone)
				if _, err := fsstep.Add(tx, fsstep.Step{Op: fsstep.OpWrite, To: notesPath, Content: notes}); err != nil {
					return nil, nil, err
				}
				// Edges aren't written yet: blockedBy and blocks have only
				// had their shape checked. They go here, blocked_by then
				// blocks, after the task's own row and step.
				return TaskCreateResult{
					ID: id, Ref: core.Kept(id, path, core.EndNone),
					NotesPath: fsstep.Abs(s.Root, notesPath),
					GatesMade: []GateMade{}, Reblocked: []int64{},
				}, nil, nil
			})
		},
	}
}
