package store

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"path"
	"slices"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// ArchiveDirs finds or makes, in the archive, the directories of folder
// path f: plain names, without the `@` (spec/design.md, The archive). A
// directory already there that differs only in case is used, so archiving
// never refuses for case. It returns their path within the root, as on
// disk.
func (t *Tree) ArchiveDirs(f model.Folder) (string, *errs.Error) {
	rel := model.ArchiveDir
	if e := t.dirAt(rel); e != nil {
		return "", e
	}
	for _, seg := range f.Segments() {
		ents, err := t.root.ReadDir(rel)
		if err != nil {
			return "", errs.FromOS(t.Abs(rel), err)
		}
		name := ""
		for _, en := range ents {
			if en.Name() == seg || name == "" && model.SameButCase(en.Name(), seg) && en.IsDir() {
				name = en.Name()
			}
		}
		if name == "" {
			name = seg
		}
		rel = path.Join(rel, name)
		if e := t.dirAt(rel); e != nil {
			return "", e
		}
	}
	return rel, nil
}

// dirAt makes directory rel unless one is there: a not-a-folder conflict
// when something else is.
func (t *Tree) dirAt(rel string) *errs.Error {
	err := t.root.Mkdir(rel, FolderMode)
	switch {
	case err == nil:
		t.root.SyncDir(path.Dir(rel))
		t.note(rel)
		return nil
	case !errs.IsErrno(err, syscall.EEXIST):
		return errs.FromOS(t.Abs(rel), err)
	}
	if fi, err := t.root.Lstat(rel); err != nil || !fi.IsDir() {
		return errs.NewConflict(errs.RuleNotAFolder, "%s is not a folder", t.Abs(rel)).WithPaths(t.Abs(rel))
	}
	return nil
}

// MakeTree makes the tree's directory, for init.
func (t *Tree) MakeTree() *errs.Error { return t.dirAt(model.TreeDir) }

// IndexRel is the index's path within the root.
var IndexRel = model.ArchiveRel(model.IndexName)

// IndexLine is one line of the index: its entry when koan can use it, or
// why not.
type IndexLine struct {
	Entry   *model.ArchiveEntry
	Problem string
}

// ReadIndex reads the archive's index. None is no lines; one koan can't
// read is an error.
func (t *Tree) ReadIndex() ([]IndexLine, *errs.Error) {
	data, err := t.root.ReadFile(IndexRel)
	if errs.IsErrno(err, syscall.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, unreadable(t.Abs(IndexRel), err)
	}
	var lines []IndexLine
	for line := range bytes.Lines(data) {
		line = bytes.TrimSuffix(line, []byte("\n"))
		a, problems, err := model.DecodeArchiveEntry(line)
		switch {
		case err != nil:
			lines = append(lines, IndexLine{Problem: "is not valid JSON: " + err.Error()})
		case len(problems) > 0:
			lines = append(lines, IndexLine{Problem: problems[0].Field + " " + problems[0].Reason})
		default:
			lines = append(lines, IndexLine{Entry: a})
		}
	}
	return lines, nil
}

// AppendIndex adds a line to the index, rewriting it whole through a temp
// file, as every file koan writes. Lines already there are kept as they
// are.
func (t *Tree) AppendIndex(a *model.ArchiveEntry) *errs.Error {
	data, err := t.root.ReadFile(IndexRel)
	if err != nil && !errs.IsErrno(err, syscall.ENOENT) {
		return errs.FromOS(t.Abs(IndexRel), err)
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	line, err := model.EncodeLine(a)
	if err != nil {
		return errs.New(errs.Internal, "encode an index line: %v", err)
	}
	return t.put(IndexRel, append(data, line...), true)
}

// WriteIndex replaces the index with entries.
func (t *Tree) WriteIndex(entries []*model.ArchiveEntry) *errs.Error {
	var data []byte
	for _, a := range entries {
		line, err := model.EncodeLine(a)
		if err != nil {
			return errs.New(errs.Internal, "encode an index line: %v", err)
		}
		data = append(data, line...)
	}
	return t.put(IndexRel, data, true)
}

// Archive is what a walk of the archive finds: init's, doctor's, and
// repair's (spec/design.md, The archive).
type Archive struct {
	Items      []*model.ArchiveEntry // every archived item, read back, in walk order
	Holders    map[model.ID][]string // each ID's task files and folder files, absolute
	Named      map[model.ID][]string // every file or directory whose name or content holds an ID, absolute
	Mismatched []string              // archived folder files holding another ID than their directory's name
	Temps      []string              // temp files koan left, root-relative
}

// WalkArchive walks the archive. Nothing in it stops the walk: a directory
// it can't list, or a file it can't read, is skipped.
func (t *Tree) WalkArchive() *Archive {
	a := &Archive{Holders: map[model.ID][]string{}, Named: map[model.ID][]string{}}
	t.walkPlain(a, model.ArchiveDir, model.Root)
	return a
}

// walkPlain walks a directory above archived items, at folder path f.
func (t *Tree) walkPlain(a *Archive, rel string, f model.Folder) {
	for _, en := range t.sortedDir(rel) {
		name := en.Name()
		p := path.Join(rel, name)
		if t.temp(a, en, p) || rel == model.ArchiveDir && name == model.IndexName {
			continue
		}
		if id, fname, ok := model.ParseArchivedFolder(name); ok && en.IsDir() {
			t.walkArchived(a, p, id, f.Join(fname))
			continue
		}
		if id, suffix, ok := model.TaskFilename(name); ok && en.Type().IsRegular() {
			a.Named[id] = append(a.Named[id], t.Abs(p))
			if suffix == model.TaskSuffix {
				a.Holders[id] = append(a.Holders[id], t.Abs(p))
				_, title, _ := t.peek(p)
				a.Items = append(a.Items, &model.ArchiveEntry{Schema: model.ArchiveFormat, ID: id, Kind: model.ArchivedTask,
					Title: title, Path: f, Archived: archiveRel(p), IDs: []model.ID{id}})
			}
			continue
		}
		if model.ValidFolderName(name) && en.IsDir() {
			t.walkPlain(a, p, f.Join(name))
		}
	}
}

// walkArchived walks archived folder rel, whose name gives it ID id, at
// folder path f.
func (t *Tree) walkArchived(a *Archive, rel string, id model.ID, f model.Folder) {
	a.Named[id] = append(a.Named[id], t.Abs(rel))
	item := &model.ArchiveEntry{Schema: model.ArchiveFormat, ID: id, Kind: model.ArchivedFolder, Path: f, Archived: archiveRel(rel)}
	ffRel := path.Join(rel, model.FolderFileName)
	if fid, title, ok := t.peek(ffRel); ok {
		item.Title = title
		if fid != id {
			a.Mismatched = append(a.Mismatched, t.Abs(ffRel))
		}
	}
	a.Holders[id] = append(a.Holders[id], t.Abs(ffRel))
	ids := map[model.ID]bool{id: true}
	t.walkInside(a, rel, ids, true)
	item.IDs = slices.Sorted(func(yield func(model.ID) bool) {
		for id := range ids {
			if !yield(id) {
				return
			}
		}
	})
	a.Items = append(a.Items, item)
}

// walkInside walks what is in an archived folder as a walk of the tree
// does, collecting the IDs of its tasks and folders. top is the archived
// folder itself, whose folder file walkArchived read.
func (t *Tree) walkInside(a *Archive, rel string, ids map[model.ID]bool, top bool) {
	for _, en := range t.sortedDir(rel) {
		name := en.Name()
		p := path.Join(rel, name)
		if t.temp(a, en, p) {
			continue
		}
		switch id, suffix, isTask := model.TaskFilename(name); {
		case name == model.FolderFileName && en.Type().IsRegular() && !top:
			if fid, _, ok := t.peek(p); ok {
				ids[fid] = true
				a.Holders[fid] = append(a.Holders[fid], t.Abs(p))
				a.Named[fid] = append(a.Named[fid], t.Abs(p))
			}
		case isTask && en.Type().IsRegular():
			a.Named[id] = append(a.Named[id], t.Abs(p))
			if suffix == model.TaskSuffix {
				ids[id] = true
				a.Holders[id] = append(a.Holders[id], t.Abs(p))
			}
		default:
			if _, ok := model.FolderName(name); ok && en.IsDir() {
				t.walkInside(a, p, ids, false)
			}
		}
	}
}

// temp records en, at p, when it is a temp file koan left.
func (t *Tree) temp(a *Archive, en fs.DirEntry, p string) bool {
	if fsys.IsTemp(en.Name()) && en.Type().IsRegular() {
		a.Temps = append(a.Temps, p)
		return true
	}
	return false
}

// sortedDir lists rel by name, nothing when it can't be listed.
func (t *Tree) sortedDir(rel string) []fs.DirEntry {
	ents, err := t.root.ReadDir(rel)
	if err != nil {
		return nil
	}
	slices.SortFunc(ents, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return ents
}

// peek reads the id and title of a task file or folder file in any format,
// for what the archive holds: ok is false when it can't be read.
func (t *Tree) peek(rel string) (id model.ID, title *string, ok bool) {
	if fi, err := t.root.Lstat(rel); err != nil || !fi.Mode().IsRegular() {
		return 0, nil, false
	}
	data, err := t.root.ReadFile(rel)
	if err != nil {
		return 0, nil, false
	}
	m, err := model.Keys(data)
	if err != nil || json.Unmarshal(m["id"], &id) != nil {
		return 0, nil, false
	}
	if raw, has := m["title"]; has && json.Unmarshal(raw, &title) != nil {
		title = nil
	}
	return id, title, true
}

// archiveRel is a root-relative path in the archive as the index writes
// it: relative to archived/.
func archiveRel(rel string) string { return strings.TrimPrefix(rel, model.ArchiveDir+"/") }

// RootTemps lists the temp files koan left in the root itself, beside the
// tree and the archive, root-relative.
func (t *Tree) RootTemps() []string {
	var temps []string
	for _, en := range t.sortedDir(".") {
		if fsys.IsTemp(en.Name()) && en.Type().IsRegular() {
			temps = append(temps, en.Name())
		}
	}
	return temps
}
