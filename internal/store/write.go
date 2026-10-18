package store

import (
	"io/fs"
	"path"
	"slices"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// Publish writes data to rel, a path within r, through a temp file in rel's
// directory (spec/implementation.md, Writing a file): created exclusively
// with mode perm, written in full and flushed to disk, then published — by
// rename when replace is set, else by link, which never clobbers an existing
// file — and the directory flushed, so the file survives a system crash whole
// and before any later step. The temp file is then removed. Failing to flush
// the directory or to remove the temp file is not an error: the file is
// already published (doctor finds leftovers). Any other failure returns the
// OS error, with nothing published.
func Publish(r fsys.Root, rel string, data []byte, perm fs.FileMode, replace bool) error {
	f, tmp, err := r.CreateTemp(path.Dir(rel), perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		r.Remove(tmp)
		return err
	}
	if replace {
		if err := r.Rename(tmp, rel); err != nil {
			r.Remove(tmp)
			return err
		}
		r.SyncDir(path.Dir(rel))
		return nil
	}
	if err := r.Link(tmp, rel); err != nil {
		r.Remove(tmp)
		return err
	}
	r.SyncDir(path.Dir(rel))
	r.Remove(tmp)
	return nil
}

// put publishes a file in the tree and records the change.
func (t *Tree) put(rel string, data []byte, replace bool) *errs.Error {
	if err := Publish(t.root, rel, data, FileMode, replace); err != nil {
		return errs.FromOS(t.Abs(rel), err)
	}
	t.note(rel)
	return nil
}

// WriteTask writes a task file into folder f: a new one when create is set,
// which fails if a file is already there, otherwise over the old one.
func (t *Tree) WriteTask(f model.Folder, tf *model.TaskFile, create bool) *errs.Error {
	tf.Normalize()
	data, err := model.Encode(tf)
	if err != nil {
		return errs.New(errs.Internal, "encode task %d: %v", tf.ID, err)
	}
	if e := t.put(TaskRel(f, tf.ID), data, !create); e != nil {
		return e
	}
	t.files[tf.ID] = &Loaded{File: tf}
	return nil
}

// WriteNotes writes a new task's notes. Like every file that must be new, it
// is published by link, so a .md already there is never overwritten: the
// task then has that file as its notes, and the error says so.
func (t *Tree) WriteNotes(f model.Folder, id model.ID, notes string) *errs.Error {
	rel := NotesRel(f, id)
	err := Publish(t.root, rel, []byte(notes), FileMode, false)
	switch {
	case err == nil:
		t.note(rel)
		return nil
	case errs.IsErrno(err, syscall.EEXIST):
		return errs.New(errs.IO, "%s was already there and was left as it is: task %d was created, and has that file as its notes", t.Abs(rel), id).
			WithPaths(t.Abs(rel))
	}
	return errs.FromOS(t.Abs(rel), err)
}

// WriteMeta writes @koan.json with the given format.
func (t *Tree) WriteMeta(schema int64, create bool) *errs.Error {
	data, err := model.Encode(model.Meta{Schema: schema})
	if err != nil {
		return errs.New(errs.Internal, "encode %s: %v", MetaName, err)
	}
	if e := t.put(MetaName, data, !create); e != nil {
		return e
	}
	t.Meta = &model.Meta{Schema: schema}
	return nil
}

// SaveState writes the state file, which is outside the tree: its directory
// is opened as a root of its own, and created if it is missing. Any temp file
// a killed write left there is removed first, since doctor never sees that
// directory.
func (t *Tree) SaveState() *errs.Error {
	env := t.env
	if t.State.Claims == nil {
		t.State.Claims = map[string]model.Claim{}
	}
	data, err := model.Encode(t.State)
	if err != nil {
		return errs.New(errs.Internal, "encode the state file: %v", err)
	}
	record := func(p string) {
		if !slices.Contains(t.changed, p) {
			t.changed = append(t.changed, p)
		}
	}
	dir, err := env.FS.OpenRoot(env.StateDir())
	if errs.IsErrno(err, syscall.ENOENT) {
		if err := env.FS.MkdirAll(env.StateDir(), 0o700); err != nil {
			return errs.FromOS(env.StateDir(), err)
		}
		record(env.StateDir())
		dir, err = env.FS.OpenRoot(env.StateDir())
	}
	if err != nil {
		return errs.FromOS(env.StateDir(), err)
	}
	defer dir.Close()
	if entries, err := dir.ReadDir("."); err == nil {
		for _, e := range entries {
			if fsys.IsTemp(e.Name()) {
				dir.Remove(e.Name())
			}
		}
	}
	if err := Publish(dir, StateName, data, StateMode, true); err != nil {
		return errs.FromOS(env.StatePath(), err)
	}
	record(env.StatePath())
	return nil
}

// Missing lists the folders on the path to f, f included, that are not
// there, outermost first. Something on the path that is not a folder is a
// not-a-folder conflict.
func (t *Tree) Missing(f model.Folder) ([]model.Folder, *errs.Error) {
	var missing []model.Folder
	cur := model.Root
	for _, seg := range f.Segments() {
		cur = cur.Join(seg)
		if len(missing) > 0 {
			missing = append(missing, cur)
			continue
		}
		fi, err := t.root.Lstat(cur.Rel())
		switch {
		case errs.IsErrno(err, syscall.ENOENT):
			missing = append(missing, cur)
		case err != nil:
			return nil, errs.FromOS(t.Abs(cur.Rel()), err)
		case !fi.IsDir():
			return nil, notAFolder(t, cur)
		}
	}
	return missing, nil
}

// MakeFolder creates folder f, whose parent is there, whole: a hidden temp
// folder in the parent gets the folder file, with ID id, and empty notes,
// and is then renamed into place, so the folder never appears without its
// folder file. made is false when something was already at f, which only an
// outside change can do: a not-a-folder conflict when it is not a folder,
// and otherwise left as it is. The temp folder is removed either way, so it
// is not a change; one a kill leaves behind is doctor's.
func (t *Tree) MakeFolder(f model.Folder, id model.ID) (made bool, e *errs.Error) {
	tmp := fsys.TempName(f.Parent().Rel())
	if err := t.root.Mkdir(tmp, FolderMode); err != nil {
		return false, errs.FromOS(t.Abs(tmp), err)
	}
	fail := func(rel string, err error) (bool, *errs.Error) {
		t.root.RemoveAll(tmp)
		return false, errs.FromOS(t.Abs(rel), err)
	}
	data, err := model.Encode(model.NewFolderFile(id))
	if err != nil {
		t.root.RemoveAll(tmp)
		return false, errs.New(errs.Internal, "encode the folder file of %s: %v", f, err)
	}
	if err := Publish(t.root, path.Join(tmp, FolderFileName), data, FileMode, false); err != nil {
		return fail(path.Join(tmp, FolderFileName), err)
	}
	if err := Publish(t.root, path.Join(tmp, FolderNotesName), nil, FileMode, false); err != nil {
		return fail(path.Join(tmp, FolderNotesName), err)
	}
	err = t.root.RenameNoReplace(tmp, f.Rel())
	switch {
	case err == nil:
		t.root.SyncDir(f.Parent().Rel())
		t.note(f.Rel())
		return true, nil
	case errs.IsErrno(err, syscall.EEXIST, syscall.ENOTEMPTY):
		t.root.RemoveAll(tmp)
		return false, t.mustBeFolder(f)
	}
	return fail(f.Rel(), err)
}

// WriteFolderFile writes a folder file into folder f, which must not have
// one: repair's way to give a folder an ID.
func (t *Tree) WriteFolderFile(f model.Folder, ff *model.FolderFile) *errs.Error {
	data, err := model.Encode(ff)
	if err != nil {
		return errs.New(errs.Internal, "encode the folder file of %s: %v", f, err)
	}
	return t.put(FolderFileRel(f), data, false)
}

func notAFolder(t *Tree, f model.Folder) *errs.Error {
	return errs.NewConflict(errs.RuleNotAFolder, "%s is not a folder", f).
		WithFolders(string(f)).WithPaths(t.Abs(f.Rel()))
}

func (t *Tree) mustBeFolder(f model.Folder) *errs.Error {
	fi, err := t.root.Lstat(f.Rel())
	if err != nil {
		return errs.FromOS(t.Abs(f.Rel()), err)
	}
	if !fi.IsDir() {
		return notAFolder(t, f)
	}
	return nil
}

// FolderExists reports whether f is a folder. A missing one is not an
// error; a file or symlink on the path is a not-a-folder conflict.
func (t *Tree) FolderExists(f model.Folder) (bool, *errs.Error) {
	cur := model.Root
	for _, seg := range f.Segments() {
		cur = cur.Join(seg)
		fi, err := t.root.Lstat(cur.Rel())
		switch {
		case errs.IsErrno(err, syscall.ENOENT):
			return false, nil
		case err != nil:
			return false, errs.FromOS(t.Abs(cur.Rel()), err)
		case !fi.IsDir():
			return false, notAFolder(t, cur)
		}
	}
	return true, nil
}

// Move renames a file or folder to a name that must be free. exists is true
// when something is already there, with nothing moved.
func (t *Tree) Move(oldRel, newRel string) (exists bool, e *errs.Error) {
	err := t.root.RenameNoReplace(oldRel, newRel)
	switch {
	case err == nil:
		t.root.SyncDir(path.Dir(newRel))
		t.note(oldRel)
		t.note(newRel)
		return false, nil
	case errs.IsErrno(err, syscall.EEXIST, syscall.ENOTEMPTY):
		return true, nil
	}
	return false, errs.FromOS(t.Abs(oldRel), err)
}

// Remove removes one file. A file already gone is not an error.
func (t *Tree) Remove(rel string) *errs.Error {
	err := t.root.Remove(rel)
	if err != nil && !errs.IsErrno(err, syscall.ENOENT) {
		return errs.FromOS(t.Abs(rel), err)
	}
	if err == nil {
		t.note(rel)
	}
	return nil
}

// Discard removes a folder in one step as far as the tree is concerned: it
// is renamed aside, to a hidden temp name in the root, then removed with
// everything under it. Once renamed it is gone from the tree; a removal that
// fails or is interrupted leaves a hidden leftover, which walks ignore and
// doctor reports.
func (t *Tree) Discard(rel string) *errs.Error {
	tmp := fsys.TempName(".")
	if err := t.root.RenameNoReplace(rel, tmp); err != nil {
		return errs.FromOS(t.Abs(rel), err)
	}
	t.note(rel)
	t.root.SyncDir(path.Dir(rel))
	t.root.RemoveAll(tmp)
	return nil
}

// RemoveAll removes a leftover temp file or folder.
func (t *Tree) RemoveAll(rel string) *errs.Error {
	if err := t.root.RemoveAll(rel); err != nil {
		return errs.FromOS(t.Abs(rel), err)
	}
	t.note(rel)
	return nil
}
