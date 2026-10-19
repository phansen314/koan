package store

import (
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// Index is what a walk finds, by name alone: no file is opened.
type Index struct {
	Folders    []model.Folder              // every folder, the root included, in tree order
	Tasks      map[model.ID]model.Folder   // each task's folder; for a duplicated ID, its first
	Copies     map[model.ID][]model.Folder // every ID with more than one task file
	Notes      map[model.ID][]model.Folder // every notes file
	Filed      map[model.Folder]bool       // every folder with a folder file
	Temps      []string                    // temp files koan left, root-relative
	Skipped    []string                    // entries named with @ that are not koan's
	Unlistable []string                    // folders that can not be listed
}

// Unfiled lists the folders, the root aside, with no folder file, in tree
// order.
func (x *Index) Unfiled() []model.Folder {
	var fs []model.Folder
	for _, f := range x.Folders {
		if f != model.Root && !x.Filed[f] {
			fs = append(fs, f)
		}
	}
	return fs
}

// IDs returns every task ID, ascending.
func (x *Index) IDs() []model.ID {
	ids := make([]model.ID, 0, len(x.Tasks))
	for id := range x.Tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Walk lists the tree. A strict walk fails on a folder that can't be listed
// and on a duplicated ID; doctor's tolerant one records both and carries on.
func (t *Tree) Walk(tolerant bool) (*Index, *errs.Error) {
	x := &Index{
		Tasks:  map[model.ID]model.Folder{},
		Copies: map[model.ID][]model.Folder{},
		Notes:  map[model.ID][]model.Folder{},
		Filed:  map[model.Folder]bool{},
	}
	if e := t.walk(x, model.Root, tolerant); e != nil {
		return nil, e
	}
	if !tolerant && len(x.Copies) > 0 {
		id := slices.Min(slices.Collect(maps.Keys(x.Copies)))
		e := errs.NewConflict(errs.RuleDuplicateID, "task %d has %d task files", id, len(x.Copies[id])).WithIDs(id)
		for _, f := range x.Copies[id] {
			e.WithPaths(t.Abs(model.TaskRel(f, id)))
		}
		return nil, e
	}
	return x, nil
}

func (t *Tree) walk(x *Index, f model.Folder, tolerant bool) *errs.Error {
	entries, err := t.root.ReadDir(f.Rel())
	if err != nil {
		if tolerant {
			x.Unlistable = append(x.Unlistable, f.Rel())
			return nil
		}
		return errs.FromOS(t.Abs(f.Rel()), err)
	}
	x.Folders = append(x.Folders, f)
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var sub []model.Folder
	for _, e := range entries {
		name := e.Name()
		rel := path.Join(f.Rel(), name)
		if fsys.IsTemp(name) && e.Type().IsRegular() {
			x.Temps = append(x.Temps, rel)
			continue
		}
		// Everything not named with @ is material, never looked at.
		if !strings.HasPrefix(name, model.Mark) {
			continue
		}
		id, suffix, isTask := model.TaskFilename(name)
		folderName, isFolder := model.FolderName(name)
		own := f != model.Root && (name == model.FolderFileName || name == model.FolderNotesName)
		switch {
		case own && name == model.FolderFileName:
			// Whatever is there, a folder file koan can't use is
			// reported as that, by whoever reads it, never as missing.
			x.Filed[f] = true
		case (isTask || own) && !e.Type().IsRegular():
			x.Skipped = append(x.Skipped, rel)
		case own:
		case isTask && suffix == model.NotesSuffix:
			x.Notes[id] = append(x.Notes[id], f)
		case isTask:
			if first, dup := x.Tasks[id]; dup {
				if len(x.Copies[id]) == 0 {
					x.Copies[id] = []model.Folder{first}
				}
				x.Copies[id] = append(x.Copies[id], f)
			} else {
				x.Tasks[id] = f
			}
		case isFolder && e.IsDir():
			sub = append(sub, f.Join(folderName))
		default:
			x.Skipped = append(x.Skipped, rel)
		}
	}
	for _, s := range sub {
		if e := t.walk(x, s, tolerant); e != nil {
			return e
		}
	}
	return nil
}

// Loaded is a task file as read: the file when it is usable, or the error a
// command asked for this task fails with.
type Loaded struct {
	File      *model.TaskFile
	Err       *errs.Error
	Gone      bool  // the file vanished between the walk and the read
	OldFormat int64 // its schema, when that is an earlier format
}

// Load reads and checks the task file of id in folder f, once per command.
func (t *Tree) Load(f model.Folder, id model.ID) *Loaded {
	if l, ok := t.files[id]; ok {
		return l
	}
	l := t.load(f, id)
	t.files[id] = l
	return l
}

// LoadAt reads one copy of a task file without remembering it: doctor's and
// migrate's way, which must see every copy of a duplicated ID.
func (t *Tree) LoadAt(f model.Folder, id model.ID) *Loaded { return t.load(f, id) }

func (t *Tree) load(f model.Folder, id model.ID) *Loaded {
	p := t.Abs(model.TaskRel(f, id))
	data, err := t.root.ReadFile(model.TaskRel(f, id))
	if errs.IsErrno(err, syscall.ENOENT) {
		return &Loaded{Gone: true, Err: errs.New(errs.NotFound, "task %d does not exist", id).WithIDs(id)}
	}
	if err != nil {
		return &Loaded{Err: unreadable(p, err).WithIDs(id)}
	}
	schema, err := model.SchemaOf(data)
	switch {
	case err != nil:
		if _, kerr := model.Keys(data); kerr != nil {
			return &Loaded{Err: corrupt(p, nil, kerr).WithIDs(id)}
		}
		return &Loaded{Err: corrupt(p, []model.Problem{{Field: "/schema", Reason: err.Error()}}, nil).WithIDs(id)}
	case schema < model.TaskFormat:
		return &Loaded{OldFormat: schema, Err: errs.New(errs.MigrationPending,
			"%s is in format %d and this koan uses %d: run `koan migrate`", p, schema, model.TaskFormat).WithIDs(id).WithPaths(p)}
	case schema > model.TaskFormat:
		return &Loaded{Err: errs.New(errs.UnsupportedFormat,
			"%s is in format %d, newer than this koan's %d", p, schema, model.TaskFormat).WithIDs(id).WithPaths(p)}
	}
	tf, problems, err := model.DecodeTaskFile(data)
	if err == nil && len(problems) == 0 && tf.ID != id {
		problems = []model.Problem{{Field: "/id", Reason: "does not match the filename"}}
	}
	if e := corrupt(p, problems, err); e != nil {
		return &Loaded{Err: e.WithIDs(id)}
	}
	tf.Normalize()
	return &Loaded{File: tf}
}

// LoadFolder reads and checks folder f's folder file. gone is true when
// there is none. There is no conversion between folder file formats, so a
// file in an earlier one is corrupt, and one in a later one
// unsupported-format.
func (t *Tree) LoadFolder(f model.Folder) (ff *model.FolderFile, gone bool, e *errs.Error) {
	rel := model.FolderFileRel(f)
	p := t.Abs(rel)
	data, err := t.root.ReadFile(rel)
	if errs.IsErrno(err, syscall.ENOENT) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, unreadable(p, err)
	}
	if schema, err := model.SchemaOf(data); err == nil && schema > model.FolderFormat {
		return nil, false, errs.New(errs.UnsupportedFormat, "%s is in format %d, newer than this koan's %d", p, schema, model.FolderFormat).
			WithPaths(p).WithFolders(string(f))
	} else if err == nil && schema != model.FolderFormat {
		return nil, false, errs.New(errs.Corrupt, "%s is in format %d, and this koan reads only %d", p, schema, model.FolderFormat).
			WithPaths(p).WithFolders(string(f))
	}
	ff, problems, err := model.DecodeFolderFile(data)
	if e := corrupt(p, problems, err); e != nil {
		return nil, false, e.WithFolders(string(f))
	}
	ff.Normalize()
	return ff, false, nil
}

// ReadRaw reads a task file as it is, for migrate.
func (t *Tree) ReadRaw(rel string) ([]byte, error) { return t.root.ReadFile(rel) }

// PutRaw replaces a file with converted content, for migrate.
func (t *Tree) PutRaw(rel string, data []byte) *errs.Error { return t.put(rel, data, true) }

// Stat reports on an entry without following a symlink.
func (t *Tree) Stat(rel string) (fs.FileInfo, error) { return t.root.Lstat(rel) }

// Material lists the entries of folder f, not the root, that are not
// koan's, hidden ones included, but for the Disposable: what folder delete
// removes only when told to, sorted by name. It looks at names and types
// alone, never inside an entry.
func (t *Tree) Material(f model.Folder) ([]string, *errs.Error) {
	entries, e := t.List(f)
	if e != nil {
		return nil, e
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var material []string
	for _, en := range entries {
		name := en.Name()
		if Disposable(en) {
			continue
		}
		_, _, isTask := model.TaskFilename(name)
		_, isFolder := model.FolderName(name)
		own := isTask || name == model.FolderFileName || name == model.FolderNotesName
		if !(isFolder && en.IsDir()) && !(own && en.Type().IsRegular()) {
			material = append(material, path.Join(f.Rel(), name))
		}
	}
	return material, nil
}

// Disposable reports whether an entry that is not koan's goes with its
// folder without being asked for: a temp file koan left, or a .DS_Store.
// Any other hidden entry may be something git can't bring back, such as
// another repository's .git or an ignored .env.
func Disposable(en fs.DirEntry) bool {
	name := en.Name()
	return en.Type().IsRegular() && (fsys.IsTemp(name) || name == ".DS_Store")
}

// List lists a folder's entry names, hidden ones included.
func (t *Tree) List(f model.Folder) ([]fs.DirEntry, *errs.Error) {
	entries, err := t.root.ReadDir(f.Rel())
	if err != nil {
		return nil, errs.FromOS(t.Abs(f.Rel()), err)
	}
	return entries, nil
}
