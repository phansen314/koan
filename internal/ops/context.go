package ops

import (
	"path"
	"slices"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
)

type contextResult struct {
	Folder      model.FolderRecord   `json:"folder"`
	Ancestors   []model.FolderRecord `json:"ancestors"`
	Notes       *string              `json:"notes"`
	ParentNotes *string              `json:"parent_notes"`
	Tasks       []model.Task         `json:"tasks"`
	Marker      *string              `json:"marker"`
}

func cmdContext(c *call) (any, *errs.Error) {
	in := c.in.top()
	ref := in.folderRef("folder", false, folderRef{})
	file, hasFile := in.text("file")
	switch {
	case !c.in.has("folder") && !c.in.has("file"):
		c.in.bad("/folder", "is required, unless file is given")
	case c.in.has("folder") && c.in.has("file"):
		c.in.bad("/file", "may not be given with folder")
	case hasFile && !path.IsAbs(file):
		c.in.bad("/file", "must be an absolute path")
	}
	if e := c.begin(false); e != nil {
		return nil, e
	}
	to := target{at: ref}
	if hasFile {
		var e *errs.Error
		if to, e = c.fromFile(file); e != nil {
			return nil, e
		}
	}
	var f model.Folder
	if to.any != 0 {
		// fromFile names a task only by its own file, found in the index.
		f = c.x.Tasks[to.any]
	} else {
		var e *errs.Error
		if f, e = c.resolve(to.at); e != nil {
			return nil, e
		}
		if e := c.folderOrNotFound(f); e != nil {
			return nil, e
		}
	}
	r, e := c.record(f)
	if e != nil {
		return nil, e
	}
	res := contextResult{Folder: r, Ancestors: []model.FolderRecord{}, Notes: c.notes(f)}
	if to.marker != "" {
		res.Marker = &to.marker
	}
	for g := f.Parent(); f != model.Root && g != model.Root; g = g.Parent() {
		res.Ancestors = append(res.Ancestors, c.passFolder(g))
	}
	slices.Reverse(res.Ancestors)
	if f != model.Root {
		res.ParentNotes = c.notes(f.Parent())
	}
	s := scope{at: folderRef{path: f}}
	if res.Tasks, e = c.tasks(&s, func(t model.Task) bool { return t.Readiness != model.Done }); e != nil {
		return nil, e
	}
	return res, nil
}

// notes is folder f's notes, whole: nil for the root, and for notes koan
// can't read, which are a warning.
func (c *call) notes(f model.Folder) *string {
	if f == model.Root {
		return nil
	}
	rel := model.FolderNotesRel(f)
	p := c.t.Abs(rel)
	unusable := func(msg string) *string {
		c.warn(errs.WarnUnusableFile, msg, []model.ID{}, p)
		return nil
	}
	// The type is looked at first: a read gives a FIFO or a device as
	// empty, which would pass for no notes.
	fi, err := c.t.Stat(rel)
	switch {
	case errs.IsErrno(err, syscall.ENOENT):
		s := ""
		return &s
	case err != nil:
		return unusable(errs.FromOS(p, err).Message)
	case !fi.Mode().IsRegular():
		return unusable(p + " is not a regular file")
	}
	data, err := c.t.ReadRaw(rel)
	if errs.IsErrno(err, syscall.ENOENT) {
		data, err = nil, nil
	}
	if err != nil {
		return unusable(errs.FromOS(p, err).Message)
	}
	s := string(data)
	return &s
}
