package store

import (
	"bytes"
	"os"
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
// directory, a symlink to one included.
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

// koanFiles is what is in .koan, sorted out: koan's own entries, and every
// other, which koan never changes or removes.
type koanFiles struct {
	marker, ignore bool     // marker.json and .gitignore are there
	ignoreAll      bool     // .gitignore holds exactly "*" and a newline
	temps          []string // temp files a write left, relative, sorted
	other          []string // everything else, absolute, sorted
}

// sortKoan sorts out what is in .koan. Only regular files are koan's: a
// marker.json of any content, a temp file, and a .gitignore that holds
// exactly "*", or nothing, as a first bind cut short leaves it.
func (b *Bound) sortKoan() (koanFiles, *errs.Error) {
	var k koanFiles
	ents, err := b.root.ReadDir(model.MarkerDir)
	if err != nil {
		return k, errs.FromOS(b.Abs(model.MarkerDir), err)
	}
	for _, ent := range ents {
		rel := path.Join(model.MarkerDir, ent.Name())
		regular := ent.Type().IsRegular()
		switch {
		case regular && ent.Name() == model.MarkerName:
			k.marker = true
		case regular && ent.Name() == model.IgnoreName:
			data, err := b.root.ReadFile(rel)
			if err != nil {
				return k, errs.FromOS(b.Abs(rel), err)
			}
			if len(data) > 0 && string(data) != model.IgnoreAll {
				k.other = append(k.other, b.Abs(rel))
				continue
			}
			k.ignore, k.ignoreAll = true, len(data) > 0
		case regular && fsys.IsTemp(ent.Name()):
			k.temps = append(k.temps, rel)
		default:
			k.other = append(k.other, b.Abs(rel))
		}
	}
	slices.Sort(k.temps)
	slices.Sort(k.other)
	return k, nil
}

// hasMaterial is the refusal of a .koan holding what koan did not write.
func (b *Bound) hasMaterial(k koanFiles) *errs.Error {
	return errs.NewConflict(errs.RuleHasMaterial, "%s holds what koan did not write: move it out first", b.Abs(model.MarkerDir)).
		WithPaths(k.other...)
}

// WriteMarker writes .koan/, its .gitignore, then m, each unless it already
// holds what it would, and reports whether it wrote anything. A .koan
// holding anything koan did not write refuses it, before anything is
// written.
func (b *Bound) WriteMarker(m *model.Marker) (bool, *errs.Error) {
	there, e := b.koanDir()
	if e != nil {
		return false, e
	}
	changed := false
	if !there {
		// A .koan made meanwhile, which only an outside change can do
		// under the lock, is sorted out as any other.
		err := b.root.Mkdir(model.MarkerDir, FolderMode)
		switch {
		case err == nil:
			b.changed = append(b.changed, b.Abs(model.MarkerDir))
			changed = true
		case !errs.IsErrno(err, syscall.EEXIST):
			return false, errs.FromOS(b.Abs(model.MarkerDir), err)
		}
	}
	k, e := b.sortKoan()
	if e != nil {
		return changed, e
	}
	if len(k.other) > 0 {
		return changed, b.hasMaterial(k)
	}
	if !k.ignoreAll {
		if e := b.writeIgnore(k.ignore); e != nil {
			return changed, e
		}
		changed = true
	}
	data, err := model.Encode(m)
	if err != nil {
		return changed, errs.New(errs.Internal, "encode the marker: %v", err)
	}
	if old, err := b.root.ReadFile(markerRel); err == nil && bytes.Equal(old, data) {
		return changed, nil
	}
	if err := Publish(b.root, markerRel, data, FileMode, true); err != nil {
		return changed, errs.FromOS(b.MarkerPath(), err)
	}
	b.changed = append(b.changed, b.MarkerPath())
	return true, nil
}

// writeIgnore writes .gitignore. A new one is written in place, not through
// a temp file, which git would show until a .gitignore is there to hide it:
// so all a kill can leave for git to see is an empty .gitignore, between
// its creation and its two bytes. An empty one is replaced as any file is.
func (b *Bound) writeIgnore(there bool) *errs.Error {
	p := b.Abs(ignoreRel)
	if there {
		if err := Publish(b.root, ignoreRel, []byte(model.IgnoreAll), FileMode, true); err != nil {
			return errs.FromOS(p, err)
		}
		b.changed = append(b.changed, p)
		return nil
	}
	f, err := b.root.Create(ignoreRel, FileMode)
	if err != nil {
		return errs.FromOS(p, err)
	}
	b.changed = append(b.changed, p)
	_, err = f.Write([]byte(model.IgnoreAll))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errs.FromOS(p, err)
	}
	b.root.SyncDir(model.MarkerDir)
	return nil
}

// RemoveMarker removes the directory's marker.json, any temp files, its
// .gitignore, then .koan itself, and returns what it removed, in order.
// Anything else in .koan refuses it, before anything is removed.
func (b *Bound) RemoveMarker() ([]string, *errs.Error) {
	there, e := b.koanDir()
	if e != nil || !there {
		return []string{}, e
	}
	k, e := b.sortKoan()
	if e != nil {
		return nil, e
	}
	if len(k.other) > 0 {
		return nil, b.hasMaterial(k)
	}
	var order []string
	if k.marker {
		order = append(order, markerRel)
	}
	order = append(order, k.temps...)
	if k.ignore {
		order = append(order, ignoreRel)
	}
	for _, rel := range append(order, model.MarkerDir) {
		if err := b.root.Remove(rel); err != nil && !errs.IsErrno(err, syscall.ENOENT) {
			return nil, errs.FromOS(b.Abs(rel), err)
		}
		b.changed = append(b.changed, b.Abs(rel))
	}
	return b.changed, nil
}

// FindMarker looks for the nearest marker at or above p, a real path, and
// returns the directory holding it: "" when there is none. A .koan that is
// not a directory, a symlink included, or that has no marker.json, is
// passed over.
func FindMarker(env Env, p string) (string, *errs.Error) {
	for d := p; ; d = path.Dir(d) {
		if found, e := holdsMarker(env, d); found || e != nil {
			return d, e
		}
		if d == "/" {
			return "", nil
		}
	}
}

// holdsMarker reports whether directory d holds .koan/marker.json, .koan
// being a directory and not a link to one: the look never follows a link
// out of d.
func holdsMarker(env Env, d string) (bool, *errs.Error) {
	for _, rel := range []string{model.MarkerDir, markerRel} {
		p := path.Join(d, rel)
		fi, err := env.FS.Lstat(p)
		switch {
		case errs.IsErrno(err, syscall.ENOENT, syscall.ENOTDIR):
			return false, nil
		case err != nil:
			return false, errs.FromOS(p, err)
		case rel == model.MarkerDir && !fi.IsDir():
			return false, nil
		}
	}
	return true, nil
}

// SameDir reports whether a and b are one directory, however each is
// spelled: on a filesystem that ignores case, /a/Code and /a/code are.
// A path that can't be looked at is no directory.
func SameDir(env Env, a, b string) bool {
	fa, err := env.FS.Lstat(a)
	if err != nil {
		return false
	}
	fb, err := env.FS.Lstat(b)
	return err == nil && os.SameFile(fa, fb)
}
