package ops

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

// CreateFolderInput is create-folder's input.
type CreateFolderInput struct {
	Folder  model.FolderPath
	Parents bool
}

func decodeCreateFolder(f *model.Fields, p *model.Problems) any {
	var in CreateFolderInput
	if v, ok := f.Required("folder"); ok {
		in.Folder, _ = p.FolderPath(v, "/folder")
	}
	in.Parents = optionalBool(f, p, "parents", false)
	return in
}

// CreateFolderOutput is create-folder's result (create-folder-output).
type CreateFolderOutput struct {
	Folder  model.FolderPath   `json:"folder"`
	Created []model.FolderPath `json:"created"`
}

// CreateFolderPartial is create-folder's partial result
// (create-folder-partial): the folders a parents chain created before
// failing, which stay.
type CreateFolderPartial struct {
	Created []model.FolderPath `json:"created"`
}

// runCreateFolder runs the path walk of folder under the write lock, then
// creates what is missing, outermost first: only folder itself unless
// parents is set. It never walks the tree.
func runCreateFolder(env Env, in CreateFolderInput, w *errs.Collector) (any, *errs.Error) {
	out := CreateFolderOutput{Folder: in.Folder, Created: []model.FolderPath{}}
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		n, e := tx.WalkFolder(in.Folder)
		if e != nil {
			return e
		}
		segs := len(in.Folder.Segments())
		if n < segs-1 && !in.Parents {
			return errs.NotFound([]string{string(folderPrefix(in.Folder, n+1))}, nil, nil)
		}
		for i := n + 1; i <= segs; i++ {
			f := folderPrefix(in.Folder, i)
			created, e := mkdirFolder(tx, f)
			if e != nil {
				if len(out.Created) > 0 {
					e = e.WithPartial(CreateFolderPartial{Created: out.Created})
				}
				return e
			}
			if created {
				out.Created = append(out.Created, f)
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// mkdirFolder creates folder f, whose parent exists. If something appeared
// there since the path walk — only an outside change can, under the lock —
// f is walked again: a plain directory counts as already there, anything
// else is corrupt.
func mkdirFolder(tx *store.Tx, f model.FolderPath) (bool, *errs.Error) {
	rel := store.FolderRel(f)
	err := tx.Mkdir(rel)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, fs.ErrExist):
		return false, tx.OSError(rel, err)
	}
	n, e := tx.WalkFolder(f)
	switch {
	case e != nil:
		return false, e
	case n < len(f.Segments()):
		return false, tx.OSError(rel, err) // there at mkdir, gone again
	}
	return false, nil
}

// folderPrefix is the folder made of f's first n segments: "/" for none.
func folderPrefix(f model.FolderPath, n int) model.FolderPath {
	return model.FolderPath("/" + strings.Join(f.Segments()[:n], "/"))
}
