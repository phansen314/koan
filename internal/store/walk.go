package store

import (
	"cmp"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
)

// Location is where a task file is: its folder and the ID in its filename.
type Location struct {
	Folder model.FolderPath
	ID     model.ID
}

// Rel is the task file's path relative to the root.
func (l Location) Rel() string { return l.file("json") }

// NotesRel is the path of the task's .md relative to the root.
func (l Location) NotesRel() string { return l.file("md") }

func (l Location) file(ext string) string {
	return joinPath(FolderRel(l.Folder), strconv.FormatInt(int64(l.ID), 10)+"."+ext)
}

// FolderRel is a folder's path relative to the root: "." for the root.
func FolderRel(f model.FolderPath) string {
	if f == model.RootFolder {
		return "."
	}
	return string(f)[1:]
}

func childFolder(f model.FolderPath, name string) model.FolderPath {
	if f == model.RootFolder {
		return model.FolderPath("/" + name)
	}
	return f + model.FolderPath("/"+name)
}

// WalkFolder runs the path walk (operations.md, Path walk) of f: each entry
// on its path, from the root down, must be a plain directory. It returns how
// many of f's segments exist, stopping at the first missing one; f exists
// when that is all of them. An entry that is present but not a directory, or
// is a symlink, is corrupt (unexpected-file). A segment matches only an
// entry of exactly its name; one that differs only in case is a case-clash
// conflict, so a path means the same on case-insensitive filesystems.
func (tx *Tx) WalkFolder(f model.FolderPath) (int, *errs.Error) {
	segs := f.Segments()
	parent := "."
	for i, s := range segs {
		rel := joinPath(parent, s)
		if e := tx.caseClash(parent, s); e != nil {
			return i, e
		}
		fi, err := tx.root.Lstat(rel)
		switch {
		case isErrno(err, syscall.ENOENT):
			return i, nil
		case isErrno(err, syscall.ELOOP), isErrno(err, syscall.ENOTDIR):
			return i, errs.Corrupt(tx.Path(rel), errs.CorruptUnexpectedFile)
		case err != nil:
			return i, tx.OSError(rel, err)
		case !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0:
			return i, errs.Corrupt(tx.Path(rel), errs.CorruptUnexpectedFile)
		}
		parent = rel
	}
	return len(segs), nil
}

// CaseClash checks folder f's last segment against its parent's entries, as
// the path walk checks each segment: a case-clash conflict if one differs
// from it only in case. f's parent exists.
func (tx *Tx) CaseClash(f model.FolderPath) *errs.Error {
	segs := f.Segments()
	if len(segs) == 0 {
		return nil
	}
	return tx.caseClash(FolderRel(model.FolderPath("/"+strings.Join(segs[:len(segs)-1], "/"))), segs[len(segs)-1])
}

// caseClash lists the directory parent and reports a case-clash conflict if
// an entry's name differs from name only in ASCII case and none is name.
func (tx *Tx) caseClash(parent, name string) *errs.Error {
	entries, err := tx.root.ReadDir(parent)
	if err != nil {
		return tx.OSError(parent, err)
	}
	var clash string
	for _, e := range entries {
		switch n := e.Name(); {
		case n == name:
			return nil
		case clash == "" && FoldName(n) == FoldName(name):
			clash = n
		}
	}
	if clash != "" {
		return errs.CaseClash(tx.Path(joinPath(parent, clash)))
	}
	return nil
}

// FoldName is name with ASCII letters lowercased: two folder names clash
// when they fold the same. Only ASCII folds, as folder names are ASCII.
func FoldName(name string) string {
	b := []byte(name)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// Index is the tree walked by name only, no file read (implementation-spec.md,
// The index). Folders and Tasks are in tree order.
type Index struct {
	Folders []model.FolderPath
	Tasks   []Location
	// Unreadable lists, in tree order, the folders that could not be listed;
	// their tasks and subfolders are missing from the index.
	Unreadable []UnreadableFolder
	// Survey is what the walk skipped, recorded in a diagnostic transaction
	// only; nil otherwise.
	Survey *Survey
	byID   map[model.ID][]Location
}

// Survey is what the walk of a diagnostic transaction found besides folders
// and task files (implementation-spec.md, The survey). Paths are relative to
// the root.
type Survey struct {
	// Temps are koan's temp files and folders, not descended into.
	Temps []string
	// Strays are the entries that are not hidden and the index skips.
	Strays []Stray
	// Notes are the .md files named like a task's notes that are regular
	// files, by the location of the task they are named for.
	Notes []Location
	// Nested are the koan.json files below the root.
	Nested []string
}

// Stray is an entry the index skips, and why: its name matches no rule
// (doctor's stray-entry), or it is a symlink or of the wrong type
// (skipped-entry).
type Stray struct {
	Rel    string
	Reason string
}

// Stray reasons; skipped-entry findings report the last two.
const (
	StrayName    = "name"
	StrayType    = "type"
	StraySymlink = "symlink"
)

// UnreadableFolder is a folder the walk could not list, with the OS error.
type UnreadableFolder struct {
	Folder model.FolderPath
	Err    error
}

// Locations returns every location of id's task files, in tree order.
func (x *Index) Locations(id model.ID) []Location { return x.byID[id] }

// Complete reports whether every folder was listed.
func (x *Index) Complete() bool { return len(x.Unreadable) == 0 }

// InScope returns the tasks under f — in f itself, or anywhere below it
// when recursive — and the folders in scope: f, then every folder below it
// when recursive, or only its immediate subfolders when not; all in tree
// order.
func (x *Index) InScope(f model.FolderPath, recursive bool) ([]model.FolderPath, []Location) {
	below := func(g model.FolderPath) bool {
		return f == model.RootFolder && g != f || strings.HasPrefix(string(g), string(f)+"/")
	}
	child := func(g model.FolderPath) bool {
		rest := strings.TrimPrefix(strings.TrimPrefix(string(g), string(f)), "/")
		return below(g) && !strings.Contains(rest, "/")
	}
	var folders []model.FolderPath
	for _, g := range x.Folders {
		if g == f || below(g) && (recursive || child(g)) {
			folders = append(folders, g)
		}
	}
	var tasks []Location
	for _, l := range x.Tasks {
		if l.Folder == f || recursive && below(l.Folder) {
			tasks = append(tasks, l)
		}
	}
	return folders, tasks
}

var (
	folderName   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$`)
	taskFileName = regexp.MustCompile(`^[1-9][0-9]{0,14}\.json$`)
	// notesFileName is the task-filename rule's other extension: a task's
	// notes (design-spec.md, Task filenames).
	notesFileName = regexp.MustCompile(`^[1-9][0-9]{0,14}\.md$`)
)

// Index walks the whole tree once, on first use, and returns the same index
// until NextStep. Folders that cannot be listed are
// recorded, not returned: whether one is an error or a warning is the
// operation's call (see RequireWholeTree, WarnUnreadable).
func (tx *Tx) Index() *Index {
	if tx.index == nil {
		x := &Index{byID: map[model.ID][]Location{}}
		if tx.survey {
			x.Survey = &Survey{}
		}
		tx.walk(x, model.RootFolder)
		tx.index = x
	}
	return tx.index
}

// walk lists f and descends into its subfolders: a folder before its
// subfolders, subfolders by name, tasks by numeric ID — tree order.
func (tx *Tx) walk(x *Index, f model.FolderPath) {
	entries, err := tx.root.ReadDir(FolderRel(f))
	if err != nil {
		// A folder other than the root that is gone, or is no longer a
		// directory, was moved or removed mid-read: skipped silently, like a
		// vanished file.
		if f != model.RootFolder && (isErrno(err, syscall.ENOENT) || isErrno(err, syscall.ENOTDIR) || isErrno(err, syscall.ELOOP)) {
			return
		}
		// It exists — its parent listed it — so it is a folder; only its
		// contents are unknown.
		x.Folders = append(x.Folders, f)
		x.Unreadable = append(x.Unreadable, UnreadableFolder{Folder: f, Err: err})
		return
	}
	var ids []model.ID
	var subs []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "."):
			if x.Survey != nil && fsys.IsTemp(name) {
				x.Survey.Temps = append(x.Survey.Temps, joinPath(FolderRel(f), name))
			}
		case folderName.MatchString(name) && e.Type().IsDir():
			subs = append(subs, name)
		case taskFileName.MatchString(name) && e.Type().IsRegular():
			id, _ := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
			ids = append(ids, model.ID(id))
		case x.Survey != nil:
			x.Survey.record(f, e)
		}
	}
	slices.Sort(ids)
	slices.SortFunc(subs, strings.Compare)
	x.Folders = append(x.Folders, f)
	for _, id := range ids {
		l := Location{Folder: f, ID: id}
		x.Tasks = append(x.Tasks, l)
		x.byID[id] = append(x.byID[id], l)
	}
	for _, s := range subs {
		tx.walk(x, childFolder(f, s))
	}
}

// record files an entry the index skipped: a task's notes, a nested tree's
// metadata, or a stray. The root's own koan.json is neither.
func (s *Survey) record(f model.FolderPath, e fs.DirEntry) {
	name, rel := e.Name(), joinPath(FolderRel(f), e.Name())
	switch {
	case name == MetaName && f == model.RootFolder:
	case e.Type()&fs.ModeSymlink != 0:
		s.Strays = append(s.Strays, Stray{rel, StraySymlink})
	case name == MetaName && e.Type().IsRegular():
		s.Nested = append(s.Nested, rel)
	case notesFileName.MatchString(name) && e.Type().IsRegular():
		id, _ := strconv.ParseInt(strings.TrimSuffix(name, ".md"), 10, 64)
		s.Notes = append(s.Notes, Location{Folder: f, ID: model.ID(id)})
	case folderName.MatchString(name) || taskFileName.MatchString(name) || notesFileName.MatchString(name):
		s.Strays = append(s.Strays, Stray{rel, StrayType})
	default:
		s.Strays = append(s.Strays, Stray{rel, StrayName})
	}
}

// RequireWholeTree is the error for an operation that must see the whole
// tree — to find an ID, or prove it absent or unique — when a folder could
// not be listed: io, for the first in tree order.
func (tx *Tx) RequireWholeTree(x *Index) *errs.Error {
	if x.Complete() {
		return nil
	}
	u := x.Unreadable[0]
	return tx.OSError(FolderRel(u.Folder), u.Err)
}

// WarnUnreadable records an unreadable-folder warning for each folder that
// could not be listed, for an operation that returns a collection.
func (tx *Tx) WarnUnreadable(x *Index) *errs.Error {
	for _, u := range x.Unreadable {
		rel := FolderRel(u.Folder)
		code, e := tx.code(rel, u.Err)
		if e != nil {
			return e
		}
		tx.Warn(errs.UnreadableFolder(tx.Path(rel), code))
	}
	return nil
}

// Copies returns the locations of id's task files that still exist, in tree
// order. When the index has several, each is checked again with Lstat and
// those gone (ENOENT) are dropped: a task moved mid-read is then counted
// once, at the location seen last, not reported as a duplicate
// (implementation-spec.md, Concurrent writes during a read). Any other
// error keeps the location: it was listed, so it is a copy, and loading it
// reports the error.
func (tx *Tx) Copies(id model.ID) []Location {
	locs := tx.Index().Locations(id)
	if len(locs) <= 1 {
		return locs
	}
	var out []Location
	for _, l := range locs {
		if _, err := tx.root.Lstat(l.Rel()); isErrno(err, syscall.ENOENT) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// CompareLocations orders locations in tree order: by folder (a parent before
// its children, siblings by name), then by ID.
func CompareLocations(a, b Location) int {
	return cmp.Or(CompareFolders(a.Folder, b.Folder), cmp.Compare(a.ID, b.ID))
}

// CompareFolders orders folder paths in tree order (operations.md, Tree
// order): segment by segment, so /proj/travel comes before /proj-b.
func CompareFolders(a, b model.FolderPath) int {
	return slices.Compare(a.Segments(), b.Segments())
}
