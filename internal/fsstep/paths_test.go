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

// A folder's path is taken on disk by an entry where its directory would
// go, spelled the same or differing only in case, file or directory; the
// exact spelling first. A missing parent directory holds nothing.
// Missing judges a path by its parent's listing, byte for byte.
func TestTakenAndMissing(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@A/", "tree/@b=", "tree/@work/", "tree/@work/@Api/", "tree/@work/@notes.md=", "tree/c/")
	if !ignoresCase(t, s) {
		files(t, s, "tree/@a/")
	}
	for _, c := range []struct {
		folder, want string
		ok           bool
	}{
		{"/b", "tree/@b", true},
		{"/B", "tree/@b", true},
		{"/work/api", "tree/@work/@Api", true},
		{"/work/new", "", false},
		{"/c", "", false},
		{"/gone/x", "", false},
	} {
		got, ok, err := Taken(s.Root, c.folder)
		if err != nil || ok != c.ok || got != c.want {
			t.Errorf("%s: got %q %v %v, want %q", c.folder, got, ok, err, c.want)
		}
	}
	want := "tree/@A"
	if !ignoresCase(t, s) {
		want = "tree/@a"
	}
	if got, ok, err := Taken(s.Root, "/a"); err != nil || !ok || got != want {
		t.Errorf("/a: got %q %v %v, want %q", got, ok, err, want)
	}
	for _, c := range []struct {
		rel  string
		want bool
	}{
		{"tree/@work/@notes.md", false},
		{"tree/@work/@Notes.md", true},
		{"tree/@gone/@notes.md", true},
	} {
		if got, err := Missing(s.Root, c.rel); err != nil || got != c.want {
			t.Errorf("%s: got %v %v", c.rel, got, err)
		}
	}
}
