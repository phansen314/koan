package store

import (
	"cmp"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
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
func Publish(r *fsys.Root, rel string, data []byte, perm fs.FileMode, replace bool) error {
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
	publish := r.Link
	if replace {
		publish = r.Rename
	}
	if err := publish(tmp, rel); err != nil {
		r.Remove(tmp)
		return err
	}
	r.SyncDir(path.Dir(rel))
	if !replace {
		r.Remove(tmp)
	}
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
	if e := t.put(model.TaskRel(f, tf.ID), data, !create); e != nil {
		return e
	}
	t.files[tf.ID] = &Loaded{File: tf}
	return nil
}

// WriteNotes writes a new task's notes. Like every file that must be new, it
// is published by link, so a .md already there is never overwritten: the
// task then has that file as its notes, and the error says so.
func (t *Tree) WriteNotes(f model.Folder, id model.ID, notes string) *errs.Error {
	rel := model.NotesRel(f, id)
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
		return errs.New(errs.Internal, "encode %s: %v", model.MetaName, err)
	}
	if e := t.put(model.MetaName, data, !create); e != nil {
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
	dir, err := env.FS.OpenRoot(env.StateDir())
	if errs.IsErrno(err, syscall.ENOENT) {
		if err := env.FS.MkdirAll(env.StateDir(), 0o700); err != nil {
			return errs.FromOS(env.StateDir(), err)
		}
		t.noteAbs(env.StateDir())
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
	t.noteAbs(env.StatePath())
	return nil
}

// Missing lists the folders on the path to f, f included, that are not
// there, outermost first. Something on the path that is not a folder is a
// not-a-folder conflict, and a part there only in another case a
// case-mismatch conflict.
func (t *Tree) Missing(f model.Folder) ([]model.Folder, *errs.Error) {
	var missing []model.Folder
	cur := model.Root
	for _, seg := range f.Segments() {
		parent := cur
		cur = cur.Join(seg)
		if len(missing) > 0 {
			missing = append(missing, cur)
			continue
		}
		ent, other, e := t.lookup(parent, seg)
		switch {
		case e != nil:
			return nil, e
		case other != "":
			return nil, caseMismatch(t, cur, parent.Join(other))
		case ent == nil:
			missing = append(missing, cur)
		case !ent.IsDir():
			return nil, notAFolder(t, cur)
		}
	}
	return missing, nil
}

// Taken reports whether anything is at folder path f, found as Missing
// finds a part: by its exact name, a name there only in another case being
// a case-mismatch conflict. That name is no conflict when it is self's, in
// the same folder: a folder renamed to another case.
func (t *Tree) Taken(f, self model.Folder) (bool, *errs.Error) {
	missing, e := t.Missing(f.Parent())
	if e != nil || len(missing) > 0 {
		return false, e
	}
	ent, other, e := t.lookup(f.Parent(), f.Name())
	if e == nil && other != "" && f.Parent().Join(other) != self {
		e = caseMismatch(t, f, f.Parent().Join(other))
	}
	return ent != nil, e
}

// lookup finds folder name seg in folder dir by its exact name, in a
// listing of dir, since on a filesystem that ignores case an Lstat of @Proj
// finds @proj. Without an exact match, other is the folder name of an @
// entry that has seg's name ignoring case.
func (t *Tree) lookup(dir model.Folder, seg string) (ent fs.DirEntry, other string, e *errs.Error) {
	ents, err := t.root.ReadDir(dir.Rel())
	if err != nil {
		return nil, "", errs.FromOS(t.Abs(dir.Rel()), err)
	}
	name := model.FolderDir(seg)
	for _, en := range ents {
		switch {
		case en.Name() == name:
			return en, "", nil
		case other == "" && model.SameButCase(en.Name(), name):
			other = strings.TrimPrefix(en.Name(), "@")
		}
	}
	return nil, other, nil
}

func caseMismatch(t *Tree, f, there model.Folder) *errs.Error {
	return errs.NewConflict(errs.RuleCaseMismatch, "%s exists as %s: a folder's name is matched in its exact case", f, there).
		WithFolders(string(there)).WithPaths(t.Abs(there.Rel()))
}

// MakeFolder creates folder f, whose parent is there, then writes its folder
// file ff and its notes in it. A kill or a failure between the steps leaves
// the folder without its folder file, or without its notes, as it is: the
// first is doctor's missing-folder-file. made is false when something was
// already at f, which only an outside change can do: a not-a-folder conflict
// when it is not a folder, and otherwise left as it is.
func (t *Tree) MakeFolder(f model.Folder, ff *model.FolderFile, notes string) (made bool, e *errs.Error) {
	ff.Normalize()
	data, err := model.Encode(ff)
	if err != nil {
		return false, errs.New(errs.Internal, "encode the folder file of %s: %v", f, err)
	}
	err = t.root.Mkdir(f.Rel(), FolderMode)
	if errs.IsErrno(err, syscall.EEXIST) {
		return false, t.mustBeFolder(f)
	}
	if err != nil {
		return false, errs.FromOS(t.Abs(f.Rel()), err)
	}
	t.root.SyncDir(f.Parent().Rel())
	t.note(f.Rel())
	// The folder's own path covers its files in changed_paths.
	if err := Publish(t.root, model.FolderFileRel(f), data, FileMode, false); err != nil {
		return true, errs.FromOS(t.Abs(model.FolderFileRel(f)), err)
	}
	if err := Publish(t.root, model.FolderNotesRel(f), []byte(notes), FileMode, false); err != nil {
		return true, errs.FromOS(t.Abs(model.FolderNotesRel(f)), err)
	}
	return true, nil
}

// WriteFolderFile writes folder f's folder file: a new one when create is
// set, which fails if a file is already there (repair's way to give a folder
// an ID), otherwise over the old one.
func (t *Tree) WriteFolderFile(f model.Folder, ff *model.FolderFile, create bool) *errs.Error {
	ff.Normalize()
	data, err := model.Encode(ff)
	if err != nil {
		return errs.New(errs.Internal, "encode the folder file of %s: %v", f, err)
	}
	return t.put(model.FolderFileRel(f), data, !create)
}

func notAFolder(t *Tree, f model.Folder) *errs.Error {
	return errs.NewConflict(errs.RuleNotAFolder, "%s is not a folder", f).
		WithFolders(string(f)).WithPaths(t.Abs(f.Rel()))
}

// mustBeFolder checks what is at f, which mkdir found there: a folder by
// f's exact name, or a conflict.
func (t *Tree) mustBeFolder(f model.Folder) *errs.Error {
	ent, other, e := t.lookup(f.Parent(), f.Name())
	switch {
	case e != nil:
		return e
	case other != "":
		return caseMismatch(t, f, f.Parent().Join(other))
	case ent == nil:
		return errs.New(errs.IO, "%s was there and is gone", t.Abs(f.Rel())).WithPaths(t.Abs(f.Rel()))
	case !ent.IsDir():
		return notAFolder(t, f)
	}
	return nil
}

// FolderExists reports whether f is a folder. A missing one is not an
// error; a file or symlink on the path is a not-a-folder conflict.
func (t *Tree) FolderExists(f model.Folder) (bool, *errs.Error) {
	missing, e := t.Missing(f)
	return e == nil && len(missing) == 0, e
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

// MoveCase renames a folder to its own name in another case. Where the
// filesystem ignores case the two names are one entry, which the no-replace
// rename refuses; it is renamed with plain rename(2), which can replace
// nothing, since it is the same entry and the lock is held. Elsewhere it is
// Move.
func (t *Tree) MoveCase(oldRel, newRel string) (exists bool, e *errs.Error) {
	a, errA := t.root.Lstat(oldRel)
	b, errB := t.root.Lstat(newRel)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		return t.Move(oldRel, newRel)
	}
	if err := t.root.Rename(oldRel, newRel); err != nil {
		return false, errs.FromOS(t.Abs(oldRel), err)
	}
	t.root.SyncDir(path.Dir(newRel))
	t.note(oldRel)
	t.note(newRel)
	return false, nil
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

// Discard removes a folder with everything under it. A removal that fails or
// is interrupted leaves the folder partly removed, which a second Discard
// finishes; it is reported as changed either way.
func (t *Tree) Discard(rel string) *errs.Error {
	err := t.discard(rel)
	t.note(rel)
	if err != nil {
		return errs.FromOS(t.Abs(rel), err)
	}
	return nil
}

// discard removes folder rel depth first, so that a removal cut short leaves
// nothing repair would get wrong: in each folder, everything else goes
// before the task files, so no notes are left without their task, and the
// folder file goes last, so no folder is left without one, which repair would
// give a new ID. A symlink is removed, never followed; an entry already gone
// is not an error.
func (t *Tree) discard(rel string) error {
	ents, err := t.root.ReadDir(rel)
	if err != nil {
		return ignoreGone(err)
	}
	slices.SortStableFunc(ents, func(a, b fs.DirEntry) int { return cmp.Compare(discardRank(a), discardRank(b)) })
	for _, e := range ents {
		p := path.Join(rel, e.Name())
		if e.IsDir() {
			err = t.discard(p)
		} else {
			err = ignoreGone(t.root.Remove(p))
		}
		if err != nil {
			return err
		}
	}
	return ignoreGone(t.root.Remove(rel))
}

// discardRank orders a folder's entries for removal: the folder file last,
// task files before it, and everything else first.
func discardRank(e fs.DirEntry) int {
	if e.Name() == model.FolderFileName {
		return 2
	}
	if _, suffix, ok := model.TaskFilename(e.Name()); ok && suffix == model.TaskSuffix && !e.IsDir() {
		return 1
	}
	return 0
}

func ignoreGone(err error) error {
	if errs.IsErrno(err, syscall.ENOENT) {
		return nil
	}
	return err
}
