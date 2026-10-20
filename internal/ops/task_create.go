package ops

import (
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

// TaskCreate makes a task in a folder or the root, with its notes file
// and its edges, making any gate they name.
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
			edgesIn := r.edgeRefs()
			extra := r.object("extra")
			notes := r.text("notes", "")
			refs, at := []core.Ref{folderRef}, []string{"/folder"}
			for _, e := range edgesIn {
				refs, at = append(refs, e.ref), append(at, e.at)
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
				w := newEdges(tx)
				w.new[id] = true
				es, err := w.resolve(id, edgesIn, true)
				if err != nil {
					return nil, nil, err
				}
				_, reblocked, warnings, err := w.add(es)
				if err != nil {
					return nil, nil, err
				}
				return TaskCreateResult{
					ID: id, Ref: core.Kept(id, path, core.EndNone),
					NotesPath: fsstep.Abs(s.Root, notesPath),
					GatesMade: w.made, Reblocked: reblocked,
				}, warnings, nil
			})
		},
	}
}
