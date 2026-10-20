package fsstep

import (
	"testing"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// A folder or a task is at one path on disk, relative to the root's
// directory, as a step holds it: each folder an @ directory spelled
// exactly as its name (T1), a task's notes @<id>.md in its folder's
// directory, a gate's @start.md or @done.md, a folder's @notes.md (T3,
// T5).
func TestPaths(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{FolderDir("/"), "tree"},
		{FolderDir("/work"), "tree/@work"},
		{FolderDir("/Work/api"), "tree/@Work/@api"},
		{FolderNotesPath("/work/api"), "tree/@work/@api/@notes.md"},
		{FolderNotesPath("/"), ""},
		{TaskNotesPath("/", 42, core.EndNone), "tree/@42.md"},
		{TaskNotesPath("/work", 42, core.EndNone), "tree/@work/@42.md"},
		{TaskNotesPath("/work", 58, core.EndStart), "tree/@work/@start.md"},
		{TaskNotesPath("/work/api", 59, core.EndDone), "tree/@work/@api/@done.md"},
		{Abs(store.Root{Path: "/home/x/koan-root"}, "tree/@work/@42.md"), "/home/x/koan-root/tree/@work/@42.md"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
