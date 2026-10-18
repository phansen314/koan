package ops

import (
	"path"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
)

type whereResult struct {
	Ref      string             `json:"ref"`
	Hint     string             `json:"hint"`
	Folder   model.FolderRecord `json:"folder"`
	Task     *model.ID          `json:"task"`
	Material *string            `json:"material"`
	File     *string            `json:"file"`
}

// target is what a reference names: folder at, or the task or folder with
// ID any, and material in the folder when material is not "".
type target struct {
	at       folderRef
	any      model.ID
	material string
}

// parseRef reads a reference: a task or folder ID, or a folder by path or
// ID followed by : and material.
func parseRef(s string) (target, bool) {
	head, material, hasMaterial := strings.Cut(s, ":")
	if hasMaterial && !model.ValidMaterial(material) {
		return target{}, false
	}
	if strings.HasPrefix(head, "/") {
		f, ok := model.ParseFolder(head)
		return target{at: folderRef{path: f}, material: material}, ok
	}
	id, ok := model.ParseIDRef(head)
	if hasMaterial {
		return target{at: folderRef{id: id}, material: material}, ok
	}
	return target{any: id}, ok
}

func cmdWhere(c *call) (any, *errs.Error) {
	in := c.in.top()
	ref, hasRef := in.text("ref")
	file, hasFile := in.text("file")
	var to target
	switch {
	case !c.in.has("ref") && !c.in.has("file"):
		c.in.bad("/ref", "is required, unless file is given")
	case c.in.has("ref") && c.in.has("file"):
		c.in.bad("/file", "may not be given with ref")
	case hasRef:
		var ok bool
		if to, ok = parseRef(ref); !ok {
			c.in.bad("/ref", "must be a reference, like 42, /proj, @41, or @41:docs/plan.md")
		}
	case hasFile && !path.IsAbs(file):
		c.in.bad("/file", "must be an absolute path")
	}
	if e := c.begin(false); e != nil {
		return nil, e
	}
	if hasFile {
		var e *errs.Error
		if to, e = c.fromFile(file); e != nil {
			return nil, e
		}
	}
	if to.any != 0 {
		f, isTask, e := c.holder(to.any)
		if e != nil {
			return nil, e
		}
		if isTask {
			return c.placeTask(to.any)
		}
		to.at = folderRef{path: f}
	}
	f, e := c.resolve(to.at)
	if e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	r, e := c.record(f)
	if e != nil {
		return nil, e
	}
	res := whereResult{Ref: string(f), Hint: string(f), Folder: r}
	if r.ID != nil {
		res.Ref = model.IDRef(*r.ID)
	}
	if to.material != "" {
		file := c.t.Abs(path.Join(f.Rel(), to.material))
		res.Ref += ":" + to.material
		res.Hint += ":" + to.material
		res.Material, res.File = &to.material, &file
	}
	return res, nil
}

// placeTask is where task id is.
func (c *call) placeTask(id model.ID) (any, *errs.Error) {
	f, tf, e := c.find(id)
	if e != nil {
		return nil, e
	}
	r, e := c.record(f)
	if e != nil {
		return nil, e
	}
	notes := c.t.Abs(model.NotesRel(f, id))
	return whereResult{Ref: model.IDRef(id), Hint: tf.Title, Folder: r, Task: &id, File: &notes}, nil
}

// fromFile is what the absolute path p names: a folder, a task, or material
// in a folder. The folders on the way must exist, as must a task; material
// is never looked at.
func (c *call) fromFile(p string) (target, *errs.Error) {
	p = path.Clean(p)
	rel, ok := c.inTree(p)
	if !ok {
		rel, ok = c.inTree(c.resolveLinks(p))
	}
	if !ok {
		return target{}, errs.New(errs.NotFound, "%s is not in the tree, %s", p, c.t.Abs(".")).WithPaths(p)
	}
	segs := strings.Split(rel, "/")
	if rel == "" {
		segs = nil
	}
	f := model.Root
	for len(segs) > 0 {
		name, ok := model.FolderName(segs[0])
		if !ok {
			break
		}
		f, segs = f.Join(name), segs[1:]
		if e := c.folderOrNotFound(f); e != nil {
			// A folder name on something else is a stray entry, as a
			// walk sees it: not koan's.
			if e.HasRule(errs.RuleNotAFolder) {
				e = errs.New(errs.NotFound, "%s is not one of koan's entries, and has no reference", p).WithPaths(p)
			}
			return target{}, e
		}
	}
	switch {
	case len(segs) == 0:
		return target{at: folderRef{path: f}}, nil
	case !strings.HasPrefix(segs[0], model.Mark):
		return target{at: folderRef{path: f}, material: strings.Join(segs, "/")}, nil
	case len(segs) == 1 && f != model.Root && (segs[0] == model.FolderFileName || segs[0] == model.FolderNotesName):
		return target{at: folderRef{path: f}}, nil
	}
	if id, _, isTask := model.TaskFilename(segs[0]); isTask && len(segs) == 1 {
		x, e := c.index()
		if e != nil {
			return target{}, e
		}
		if x.Tasks[id] != f {
			return target{}, errs.New(errs.NotFound, "%s is not a task's file: task %d is not in %s", p, id, f).WithIDs(id).WithPaths(p)
		}
		return target{any: id}, nil
	}
	return target{}, errs.New(errs.NotFound, "%s is not one of koan's entries, and has no reference", p).WithPaths(p)
}

// resolveLinks is p with the symlinks in its longest existing prefix
// resolved, for a path that reaches the tree through a link of its own.
// What lies beyond that prefix, which does not exist, is kept as it is.
func (c *call) resolveLinks(p string) string {
	for q, rest := p, ""; ; q, rest = path.Dir(q), path.Join(path.Base(q), rest) {
		if real, err := c.env.FS.EvalSymlinks(q); err == nil {
			return path.Join(real, rest)
		}
		if q == "/" {
			return p
		}
	}
}

// inTree is p relative to the tree's directory, "" for the directory
// itself, given as ~/koan-root or as where that leads.
func (c *call) inTree(p string) (string, bool) {
	roots := []string{c.t.Abs(".")}
	if real, err := c.env.FS.EvalSymlinks(roots[0]); err == nil && real != roots[0] {
		roots = append(roots, real)
	}
	for _, root := range roots {
		if p == root {
			return "", true
		}
		if rel, ok := strings.CutPrefix(p, root+"/"); ok {
			return rel, true
		}
	}
	return "", false
}
