package fsstep

import (
	"path"
	"path/filepath"
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
