package fsstep

import (
	"slices"
	"testing"
)

// A directory remove whose change wasn't forced must record what it may
// remove, since the rows may be gone when it runs: Encode refuses one
// that records nothing. A forced one, and a remove of notes, need not.
func TestEncodeRemoveOwns(t *testing.T) {
	_, s := newRoot(t)
	c := New(s.Root)
	if _, err := c.Encode(1, Step{Op: OpRemove, From: "tree/@old", Dir: true}); err == nil {
		t.Errorf("an unforced directory remove with no Owns was encoded")
	}
	for _, s := range []Step{
		{Op: OpRemove, From: "tree/@old", Dir: true, Owns: []string{"tree/@old"}},
		{Op: OpRemove, From: "tree/@old", Dir: true, Force: true},
		{Op: OpRemove, From: "tree/@old/@5.md"},
	} {
		if _, err := c.Encode(1, s); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
}

// MaterialIn counts material as the step does: for each case of the
// step's own test, it finds material in the directory of the folder
// being deleted or of a folder under it exactly when the step sticks,
// and names the top-level entries in byte order, a folder being deleted
// left to its own listing.
func TestMaterialIn(t *testing.T) {
	koans := []string{"tree/@old/@notes.md=notes", "tree/@old/@5.md=five", "tree/@old/@todo.md=",
		"tree/@old/.DS_Store=finder", "tree/@old/@sub/@notes.md=", "tree/@old/@sub/@start.md=s",
		"tree/@old/@7.md=", "tree/@old/@stray/@x.md=", "tree/@old/@stray/.DS_Store=finder"}
	for _, c := range []struct {
		name     string
		material []string
		old, sub []string
	}{
		{"none", nil, nil, nil},
		{"at the top", []string{"tree/@old/thesis.tex=t"}, []string{"thesis.tex"}, nil},
		{"an empty file", []string{"tree/@old/@sub/__init__.py="}, nil, []string{"__init__.py"}},
		{"a hidden directory", []string{"tree/@old/.git/"}, []string{".git"}, nil},
		{"under @ directories", []string{"tree/@old/@sub/@types/x.d.ts=x"}, nil, []string{"@types"}},
		{"a stray @ file with content", []string{"tree/@old/@todo.md=by hand"}, []string{"@todo.md"}, nil},
		{"another task's notes", []string{"tree/@old/@sub/@7.md=seven"}, nil, []string{"@7.md"}},
		{"a stray @ directory holding content", []string{"tree/@old/@stray/@x.md=x"}, []string{"@stray"}, nil},
		{"a stray @ directory holding material", []string{"tree/@old/@stray/@deep/a="}, []string{"@stray"}, nil},
		{"a done gate's notes the step records", []string{"tree/@old/@sub/@done.md=d"}, nil, nil},
		{"in byte order", []string{"tree/@old/b=", "tree/@old/B=", "tree/@old/a/"}, []string{"B", "a", "b"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, s := newRoot(t)
			if c.name == "in byte order" && ignoresCase(t, s) {
				c.material, c.old = []string{"tree/@old/b=", "tree/@old/a/"}, []string{"a", "b"}
			}
			files(t, s, koans...)
			files(t, s, c.material...)
			before := snapshot(t, s)
			old, err := MaterialIn(s.Root, "tree/@old", oldOwns)
			if err != nil || !slices.Equal(old, c.old) {
				t.Errorf("@old: %q %v, want %q", old, err, c.old)
			}
			sub, err := MaterialIn(s.Root, "tree/@old/@sub", oldOwns)
			if err != nil || !slices.Equal(sub, c.sub) {
				t.Errorf("@sub: %q %v, want %q", sub, err, c.sub)
			}
			if !slices.Equal(snapshot(t, s), before) {
				t.Errorf("looking changed the tree")
			}
		})
	}
	// A directory not there as spelled holds none, even where the
	// filesystem would open another spelling.
	_, s := newRoot(t)
	files(t, s, "tree/@Old/", "tree/@Old/thesis.tex=t")
	if got, err := MaterialIn(s.Root, "tree/@old", oldOwns); err != nil || got != nil {
		t.Errorf("another spelling: %q %v", got, err)
	}
}

// A case-only rename's own directory is not in its way; any other entry
// differing from its new name only in case is.
func TestTakenBeside(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@work/")
	if got, ok, err := TakenBeside(s.Root, "/Work", "tree/@work"); err != nil || ok {
		t.Errorf("own directory: %q %v %v", got, ok, err)
	}
	if got, ok, err := Taken(s.Root, "/Work"); err != nil || !ok || got != "tree/@work" {
		t.Errorf("without own: %q %v %v", got, ok, err)
	}
	if ignoresCase(t, s) {
		return
	}
	// Byte order puts @WORK before @work, so it must not hide behind it.
	files(t, s, "tree/@WORK/")
	if got, ok, err := TakenBeside(s.Root, "/Work", "tree/@work"); err != nil || !ok || got != "tree/@WORK" {
		t.Errorf("a stray beside its own: %q %v %v", got, ok, err)
	}
	if got, ok, err := TakenBeside(s.Root, "/Work", "tree/@WORK"); err != nil || !ok || got != "tree/@work" {
		t.Errorf("a stray after its own: %q %v %v", got, ok, err)
	}
}
