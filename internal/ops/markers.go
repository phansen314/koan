package ops

import (
	"path"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

type markerBound struct {
	ID      model.ID     `json:"id"`
	Path    model.Folder `json:"path"`
	Dir     string       `json:"dir"`
	Marker  string       `json:"marker"`
	Changed bool         `json:"changed"`
}

func cmdMarkerBind(c *call) (any, *errs.Error) {
	in := c.in.top()
	ref := in.folderRef("folder", true, folderRef{})
	if ref.id == 0 && ref.path == model.Root {
		c.in.bad("/folder", "may not be the root, which has no ID to bind")
	}
	dir := readDir(c, in)
	force := in.boolean("force", false)
	if e := c.begin(false); e != nil {
		return nil, e
	}
	real, e := c.boundDir(dir)
	if e != nil {
		return nil, e
	}
	for _, root := range c.treeDirs() {
		if real == "/" || strings.HasPrefix(root, real+"/") {
			return nil, errs.NewConflict(errs.RuleHoldsTree, "%s holds the tree, %s: its marker would bind every directory outside the tree that has none of its own", real, root).
				WithPaths(real)
		}
	}
	f, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	ff, _, e := c.t.LoadFolder(f)
	if e != nil {
		return nil, e
	}
	if ff == nil {
		return nil, errs.New(errs.NotFound, "folder %s has no folder file, so no ID to bind: koan repair writes one", f).WithFolders(string(f))
	}
	b, e := store.OpenBound(c.env, real)
	if e != nil {
		return nil, e
	}
	defer b.Close()
	if !force {
		checkout, e := b.IsCheckout()
		if e != nil {
			return nil, e
		}
		if checkout {
			return nil, errs.NewConflict(errs.RuleCheckout, "%s is a checkout, whose marker would bind every worktree inside it: bind each worktree instead, or give force", real).
				WithPaths(real)
		}
		// A marker koan can't use, or one copied here, binds nothing, and
		// is replaced.
		if old, _ := b.ReadMarker(); old != nil && old.Dir == real && old.ID != ff.ID {
			return nil, errs.NewConflict(errs.RuleBoundElsewhere, "%s is bound to folder %d (%s): give force to bind it to %s instead", real, old.ID, old.Path, f).
				WithIDs(old.ID).WithPaths(b.MarkerPath())
		}
	}
	m := &model.Marker{Schema: model.MarkerFormat, ID: ff.ID, Path: f, Dir: real}
	changed, e := b.WriteMarker(m)
	if e != nil {
		return nil, e.WithChanged(b.Changed()...)
	}
	return markerBound{ID: ff.ID, Path: f, Dir: real, Marker: b.MarkerPath(), Changed: changed}, nil
}

type markerUnbound struct {
	Dir     string   `json:"dir"`
	Removed []string `json:"removed"`
	Changed bool     `json:"changed"`
}

func cmdMarkerUnbind(c *call) (any, *errs.Error) {
	dir := readDir(c, c.in.top())
	if e := c.checked(); e != nil {
		return nil, e
	}
	real, e := c.boundDir(dir)
	if e != nil {
		return nil, e
	}
	b, e := store.OpenBound(c.env, real)
	if e != nil {
		return nil, e
	}
	defer b.Close()
	removed, e := b.RemoveMarker()
	if e != nil {
		return nil, e.WithChanged(b.Changed()...)
	}
	return markerUnbound{Dir: real, Removed: removed, Changed: len(removed) > 0}, nil
}

// readDir reads the required dir, an absolute path.
func readDir(c *call, in fields) string {
	var dir string
	if in.get("dir", true, &dir, "a string") && !path.IsAbs(dir) {
		c.in.bad("/dir", "must be an absolute path")
	}
	return dir
}

// boundDir is the real path of dir, a directory to bind or unbind, which
// must be outside the tree.
func (c *call) boundDir(dir string) (string, *errs.Error) {
	real, err := c.env.FS.EvalSymlinks(dir)
	if errs.IsErrno(err, syscall.ENOENT, syscall.ENOTDIR) {
		return "", errs.New(errs.NotFound, "%s does not exist", dir).WithPaths(dir)
	}
	if err != nil {
		return "", errs.FromOS(dir, err)
	}
	if _, ok := c.inTree(real); ok {
		return "", errs.NewConflict(errs.RuleInTree, "%s is in the tree, where a .koan is material: markers are for directories outside it", real).
			WithPaths(real)
	}
	return real, nil
}
