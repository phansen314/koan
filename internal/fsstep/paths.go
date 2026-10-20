package fsstep

import (
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// FolderDir is a folder's directory, from its path, relative to the
// root's directory as a step holds it: tree for the root, and an @
// directory spelled exactly as each name below it.
func FolderDir(folder string) string {
	dir := store.TreeName
	for _, name := range strings.Split(strings.TrimPrefix(folder, "/"), "/") {
		if name != "" {
			dir = path.Join(dir, "@"+name)
		}
	}
	return dir
}

// FolderNotesPath is a folder's notes, @notes.md in its directory. The
// root has none, and gives "".
func FolderNotesPath(folder string) string {
	if folder == "/" {
		return ""
	}
	return path.Join(FolderDir(folder), FolderNotes)
}

// TaskNotesPath is a task's notes, @<id>.md in the directory of the
// folder it sits in; a gate's are @start.md or @done.md.
func TaskNotesPath(folder string, id int64, gate core.End) string {
	switch gate {
	case core.EndStart:
		return path.Join(FolderDir(folder), StartNotes)
	case core.EndDone:
		return path.Join(FolderDir(folder), DoneNotes)
	}
	return path.Join(FolderDir(folder), "@"+strconv.FormatInt(id, 10)+".md")
}

// Abs is a step's path under the root as written, as results give it.
func Abs(r store.Root, rel string) string { return filepath.Join(r.Path, filepath.FromSlash(rel)) }

// Taken is the entry on disk where a folder's directory would go: one in
// its parent's directory spelled as the directory, or differing from it
// only in case, given as a step's path. The exact spelling is preferred,
// then the first in byte order. A parent directory that is missing holds
// nothing.
func Taken(r store.Root, folder string) (entry string, ok bool, err error) {
	return TakenBeside(r, folder, "")
}

// TakenBeside is Taken for a case-only rename, whose own directory, own,
// given as a step's path, is not in its way.
func TakenBeside(r store.Root, folder, own string) (entry string, ok bool, err error) {
	dir := FolderDir(folder)
	parent, name := path.Dir(dir), path.Base(dir)
	listing, _, err := names(filepath.Join(r.Dir, filepath.FromSlash(parent)))
	if err != nil {
		return "", false, err
	}
	listing = slices.DeleteFunc(listing, func(n string) bool { return path.Join(parent, n) == own })
	slices.Sort(listing)
	i := slices.Index(listing, name)
	if i < 0 {
		i = slices.IndexFunc(listing, func(n string) bool { return strings.EqualFold(n, name) })
	}
	if i < 0 {
		return "", false, nil
	}
	return path.Join(parent, listing[i]), true, nil
}

// Missing reports whether nothing is at a step's path, judged by listing
// its parent directory and comparing names byte for byte.
func Missing(r store.Root, rel string) (bool, error) {
	ok, err := listed(filepath.Join(r.Dir, filepath.FromSlash(path.Dir(rel))), path.Base(rel))
	return !ok, err
}
