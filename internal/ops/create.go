package ops

import (
	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

// CreateInput is create's input, defaults applied. Title is trimmed.
type CreateInput struct {
	Title     model.Title
	Folder    model.FolderPath
	Priority  *int64
	Tags      []model.Tag
	BlockedBy []model.ID
	Extra     *jsonio.Object
	Notes     string
}

func decodeCreate(f *model.Fields, p *model.Problems) any {
	in := CreateInput{Tags: []model.Tag{}, BlockedBy: []model.ID{}, Extra: &jsonio.Object{}}
	if v, ok := f.Required("title"); ok {
		if s, ok := p.String(v, "/title"); ok {
			in.Title, _ = p.TitleInput(s, "/title")
		}
	}
	in.Folder = optionalFolder(f, p, "folder")
	if v, ok := f.Optional("priority"); ok {
		in.Priority, _ = p.Priority(v, "/priority")
	}
	if v, ok := f.Optional("tags"); ok {
		in.Tags, _ = p.Tags(v, "/tags")
	}
	if v, ok := f.Optional("blocked_by"); ok {
		in.BlockedBy, _ = p.IDs(v, "/blocked_by")
	}
	if v, ok := f.Optional("extra"); ok {
		if _, ok := p.Object(v, "/extra"); ok {
			in.Extra = v.(*jsonio.Object)
		}
	}
	if v, ok := f.Optional("notes"); ok {
		in.Notes, _ = p.String(v, "/notes")
	}
	return in
}

// CreatePartial is create's partial result (create-partial): the ID it took
// from last_id before failing to create the task.
type CreatePartial struct {
	ID model.ID `json:"id"`
}

// runCreate creates the task under the write lock, in create's precedence
// order: the path walk of folder (a missing folder held, to be reported with
// any missing blockers), the blockers looked up and then loaded, then
// checked against last_id, then the ID ceiling. It writes in the order its
// Crash behavior relies on: last_id, the task file, then the .md. Once the
// task file is written, create succeeds.
func runCreate(env Env, in CreateInput, w *errs.Collector) (any, *errs.Error) {
	var out model.Task
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		var missingFolder []string
		n, e := tx.WalkFolder(in.Folder)
		if e != nil {
			return e
		}
		if n < len(in.Folder.Segments()) {
			missingFolder = []string{string(folderPrefix(in.Folder, n+1))}
		}
		var found map[model.ID][]*store.Loaded
		var missingIDs []int64
		if len(in.BlockedBy) > 0 {
			if found, missingIDs, e = lookupIDs(tx, in.BlockedBy); e != nil {
				return e
			}
		}
		if missingFolder != nil || missingIDs != nil {
			return errs.NotFound(missingFolder, missingIDs, nil)
		}
		if e := requireIDs(tx, found); e != nil {
			return e
		}
		if e := aboveLastID(tx, in.BlockedBy); e != nil {
			return e
		}
		if tx.Meta().LastID >= model.IDMax {
			return errs.Conflict(errs.RuleIDExhausted, nil)
		}

		id := model.ID(tx.Meta().LastID + 1)
		loc := store.Location{Folder: in.Folder, ID: id}
		now := model.TimestampOf(env.Clock())
		tf := model.TaskFile{
			Schema: model.TaskSchema, ID: id, Title: in.Title, Priority: in.Priority,
			CreatedAt: now, UpdatedAt: now, BlockedBy: in.BlockedBy, Tags: in.Tags, Extra: in.Extra,
		}
		data, err := tf.Encode()
		if err != nil {
			return errs.Internal("encoding the task file: " + err.Error())
		}
		if e := tx.SetLastID(int64(id)); e != nil {
			return e
		}
		if e := tx.Create(loc.Rel(), data); e != nil {
			return e.WithPartial(CreatePartial{ID: id})
		}
		if err := tx.ReplaceRaw(loc.NotesRel(), []byte(in.Notes)); err != nil {
			// The task exists, so create still succeeds: failing would invite
			// a retry that creates a duplicate.
			tx.NotesMissing(loc.NotesRel(), id, err)
		}
		tf.Normalize()
		out = model.Task{TaskFile: tf, Folder: in.Folder, NotesPath: tx.Path(loc.NotesRel())}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
