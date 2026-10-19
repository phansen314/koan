package store

import (
	"bytes"
	"path"
	"slices"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// Bound is a directory outside the tree, opened once to read, write, or
// remove its marker (spec/design.md, Markers). Dir is its real path.
type Bound struct {
	root    *fsys.Root
	Dir     string
	changed []string
}

// OpenBound opens the directory at dir, a real path: not-found when it is
// not a directory. The caller closes it.
func OpenBound(env Env, dir string) (*Bound, *errs.Error) {
	r, err := env.FS.OpenRoot(dir)
	if errs.IsErrno(err, syscall.ENOENT, syscall.ENOTDIR) {
		return nil, errs.New(errs.NotFound, "%s is not a directory", dir).WithPaths(dir)
	}
	if err != nil {
		return nil, errs.FromOS(dir, err)
	}
	return &Bound{root: r, Dir: dir}, nil
}

// Close closes the handle.
func (b *Bound) Close() { b.root.Close() }

// Changed lists what has been written or removed so far.
func (b *Bound) Changed() []string { return b.changed }

// Abs is the path of rel, relative to the directory.
func (b *Bound) Abs(rel string) string { return path.Join(b.Dir, rel) }

var (
	markerRel = path.Join(model.MarkerDir, model.MarkerName)
	ignoreRel = path.Join(model.MarkerDir, model.IgnoreName)
)

// MarkerPath is where the directory's marker is.
func (b *Bound) MarkerPath() string { return b.Abs(markerRel) }

// koanDir reports whether .koan is there: a conflict when it is not a
// directory.
func (b *Bound) koanDir() (bool, *errs.Error) {
	fi, err := b.root.Lstat(model.MarkerDir)
	switch {
	case errs.IsErrno(err, syscall.ENOENT):
		return false, nil
	case err != nil:
		return false, errs.FromOS(b.Abs(model.MarkerDir), err)
	case !fi.IsDir():
		p := b.Abs(model.MarkerDir)
		return false, errs.NewConflict(errs.RuleNotAFolder, "%s is not a directory", p).WithPaths(p)
	}
	return true, nil
}

// IsCheckout reports whether the directory's .git is a directory: a
// checkout, where a worktree's is a file.
func (b *Bound) IsCheckout() (bool, *errs.Error) {
	fi, err := b.root.Lstat(".git")
	switch {
	case errs.IsErrno(err, syscall.ENOENT):
		return false, nil
	case err != nil:
		return false, errs.FromOS(b.Abs(".git"), err)
	}
	return fi.IsDir(), nil
}

// ReadMarker reads the directory's marker: nil and nil when there is none,
// corrupt when it can't be used.
func (b *Bound) ReadMarker() (*model.Marker, *errs.Error) {
	p := b.MarkerPath()
	data, err := b.root.ReadFile(markerRel)
	if errs.IsErrno(err, syscall.ENOENT, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, unreadable(p, err)
	}
	if schema, err := model.SchemaOf(data); err == nil && schema != model.MarkerFormat {
		return nil, errs.New(errs.Corrupt, "%s is in format %d, and this koan reads only %d", p, schema, model.MarkerFormat).WithPaths(p)
	}
	m, problems, err := model.DecodeMarker(data)
	if e := corrupt(p, problems, err); e != nil {
		return nil, e
	}
	return m, nil
}

// WriteMarker writes .koan/, its .gitignore, then m, each unless it already
// holds what it would, and reports whether it wrote anything.
func (b *Bound) WriteMarker(m *model.Marker) (bool, *errs.Error) {
	there, e := b.koanDir()
	if e != nil {
		return false, e
	}
	changed := false
	if !there {
		if err := b.root.Mkdir(model.MarkerDir, FolderMode); err != nil {
			return false, errs.FromOS(b.Abs(model.MarkerDir), err)
		}
		b.changed = append(b.changed, b.Abs(model.MarkerDir))
		changed = true
	}
	data, err := model.Encode(m)
	if err != nil {
		return false, errs.New(errs.Internal, "encode the marker: %v", err)
	}
	for _, f := range []struct {
		rel  string
		data []byte
	}{{ignoreRel, []byte(model.IgnoreAll)}, {markerRel, data}} {
		if old, err := b.root.ReadFile(f.rel); err == nil && bytes.Equal(old, f.data) {
			continue
		}
		if err := Publish(b.root, f.rel, f.data, FileMode, true); err != nil {
			return changed, errs.FromOS(b.Abs(f.rel), err)
		}
		b.changed = append(b.changed, b.Abs(f.rel))
		changed = true
	}
	return changed, nil
}

// RemoveMarker removes the directory's marker.json, any temp files, its
// .gitignore, then .koan itself, and returns what it removed, in order.
// Anything else in .koan refuses it, before anything is removed.
func (b *Bound) RemoveMarker() ([]string, *errs.Error) {
	there, e := b.koanDir()
	if e != nil || !there {
		return []string{}, e
	}
	ents, err := b.root.ReadDir(model.MarkerDir)
	if err != nil {
		return nil, errs.FromOS(b.Abs(model.MarkerDir), err)
	}
	var marker, temps, ignore, other []string
	for _, ent := range ents {
		rel := path.Join(model.MarkerDir, ent.Name())
		switch {
		case ent.Name() == model.MarkerName:
			marker = append(marker, rel)
		case ent.Name() == model.IgnoreName:
			ignore = append(ignore, rel)
		case fsys.IsTemp(ent.Name()):
			temps = append(temps, rel)
		default:
			other = append(other, b.Abs(rel))
		}
	}
	if len(other) > 0 {
		slices.Sort(other)
		return nil, errs.NewConflict(errs.RuleHasMaterial, "%s holds what koan did not write: remove it first", b.Abs(model.MarkerDir)).
			WithPaths(other...)
	}
	slices.Sort(temps)
	for _, rel := range slices.Concat(marker, temps, ignore, []string{model.MarkerDir}) {
		if err := b.root.Remove(rel); err != nil && !errs.IsErrno(err, syscall.ENOENT) {
			return nil, errs.FromOS(b.Abs(rel), err)
		}
		b.changed = append(b.changed, b.Abs(rel))
	}
	return b.changed, nil
}

// FindMarker looks for the nearest marker at or above p, a real path, and
// returns the directory holding it: "" when there is none. A .koan that is
// not a directory, or has no marker.json, is passed over.
func FindMarker(env Env, p string) (string, *errs.Error) {
	for d := p; ; d = path.Dir(d) {
		mp := path.Join(d, markerRel)
		_, err := env.FS.Lstat(mp)
		switch {
		case err == nil:
			return d, nil
		case !errs.IsErrno(err, syscall.ENOENT, syscall.ENOTDIR):
			return "", errs.FromOS(mp, err)
		}
		if d == "/" {
			return "", nil
		}
	}
}
