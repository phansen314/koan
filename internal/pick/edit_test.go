package pick

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/ops"
)

// e records the notes' hashes and has fzf run the editor, then after-edit;
// notes_edited lists the notes whose content changed, in the order first
// edited, once each.
func TestEditAction(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one", "notes": "first notes"})
	tr.run("create", map[string]any{"title": "two"})
	tr.run("create", map[string]any{"title": "three"})
	execute := "execute('/bin/ftask' __pick edit)+transform('/bin/ftask' __pick after-edit)"
	out, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		notes := shownNotes(t, tr.session)
		edit := func(keys []string, change func()) string {
			t.Helper()
			if got := helper(append([]string{"act", "e"}, keys...)...); got != execute {
				t.Fatalf("e printed %q", got)
			}
			if change != nil {
				change()
			}
			if got := helper("after-edit"); !strings.Contains(got, "reload-sync(") {
				t.Errorf("after-edit printed %q", got)
			}
			return helper("text", "footer")
		}
		write := func(id model.ID, s string) func() {
			return func() {
				if err := os.WriteFile(notes[id], []byte(s), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if got := edit([]string{"3@/", "1@/"}, nil); got != "notes unchanged" {
			t.Errorf("unchanged: %q", got)
		}
		if got := edit([]string{"3@/", "1@/"}, write(3, "new")); got != "✓ edited notes 3" {
			t.Errorf("one changed: %q", got)
		}
		// A note written with what it held is unchanged; no notes file
		// reads as empty.
		if got := edit([]string{"2@/", "1@/"}, func() { write(1, "first notes")(); write(2, "")() }); got != "notes unchanged" {
			t.Errorf("same content: %q", got)
		}
		if got := edit([]string{"1@/", "3@/"}, func() { write(1, "changed")(); write(3, "newer")() }); got != "✓ edited notes 2: 1, 3" {
			t.Errorf("two changed: %q", got)
		}
		helper("quit")
	}})
	if !out.OK {
		t.Fatalf("%s", line)
	}
	res := result(t, line)
	if !strings.Contains(string(res), `"actions":[],"notes_edited":[3,1]`) {
		t.Errorf("result %s", res)
	}
}

// e opens the notes where they are now, not where the last load saw them:
// a task moved by another process meanwhile has its notes edited in its
// new folder, and one deleted meanwhile refuses the e.
func TestEditActionFresh(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/b"})
	tr.run("create", map[string]any{"title": "one"})
	tr.run("create", map[string]any{"title": "two"})
	execute := "execute('/bin/ftask' __pick edit)+transform('/bin/ftask' __pick after-edit)"
	tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		// Elsewhere, meanwhile.
		tr.run("move", map[string]any{"id": 1, "to": "/b"})
		tr.run("delete", map[string]any{"id": 2})
		if got := helper("act", "e", "1@/"); got != execute {
			t.Fatalf("e printed %q", got)
		}
		s, e := openSession(fsys.OS{}, []string{SessionVar + "=" + tr.session})
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		var es []edited
		if e := readJSON(s, editFile, &es); e != nil {
			t.Fatal(e)
		}
		moved := tr.run("show", map[string]any{"id": 1}).Result.(ops.ShowOutput).Tasks[0].NotesPath
		if len(es) != 1 || es[0].Path != moved {
			t.Errorf("editing %+v, want %s", es, moved)
		}
		helper("after-edit")
		helper("act", "e", "2@/")
		if f := helper("text", "footer"); !strings.HasPrefix(f, "✗ e: 2: not-found") {
			t.Errorf("deleted: footer %q", f)
		}
		helper("quit")
	}})
}

// shownNotes is each shown task's notes path, from the session.
func shownNotes(t *testing.T, dir string) map[model.ID]string {
	t.Helper()
	s, e := openSession(fsys.OS{}, []string{SessionVar + "=" + dir})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var sh shown
	if e := readJSON(s, shownFile, &sh); e != nil {
		t.Fatal(e)
	}
	m := map[model.ID]string{}
	for _, l := range sh.Lines {
		m[l.ID] = l.Notes
	}
	return m
}

// The editor: $VISUAL, else $EDITOR, else vi, run through sh with every
// note as an argument, with interrupts caught meanwhile; a failure is shown
// by after-edit.
func TestEditVerb(t *testing.T) {
	for _, tc := range []struct {
		environ []string
		editor  string
	}{
		{[]string{"VISUAL=code --wait", "EDITOR=nano"}, "code --wait"},
		{[]string{"VISUAL=", "EDITOR=nano"}, "nano"},
		{nil, "vi"},
	} {
		s := testSession(t)
		writeJSON(s, editFile, []edited{{ID: 1, Path: "/n/1.md"}, {ID: 2, Path: "/n/it's 2.md"}})
		var argv []string
		caught := false
		env := Env{Sys: System{
			Environ:         func() []string { return tc.environ },
			CatchInterrupts: func() func() { caught = true; return func() { caught = false } },
			RunEditor: func(a, _ []string) (int, error) {
				if !caught {
					t.Error("interrupts not caught while the editor ran")
				}
				argv = a
				return 0, nil
			},
		}}
		if out, e := editVerb(s, nil, env); len(out) != 0 || e != nil {
			t.Errorf("printed %q, %v", out, e)
		}
		if want := []string{"sh", "-c", tc.editor + ` "$@"`, tc.editor, "/n/1.md", "/n/it's 2.md"}; !slices.Equal(argv, want) {
			t.Errorf("argv %q, want %q", argv, want)
		}
	}

	// A real sh: the editor's words split, the paths whole.
	dir := t.TempDir()
	script := filepath.Join(dir, "ed")
	// It writes its arguments into the last, the note.
	os.WriteFile(script, []byte("#!/bin/sh\neval last=\\${$#}\nprintf '%s|' \"$@\" > \"$last\"\n"), 0o755)
	path := filepath.Join(dir, "it's a note.md")
	s := testSession(t)
	writeJSON(s, editFile, []edited{{ID: 1, Path: path}})
	env := Env{Sys: System{
		Environ:         func() []string { return []string{"EDITOR=" + script + " --flag", "PATH=" + os.Getenv("PATH")} },
		CatchInterrupts: func() func() { return func() {} },
		RunEditor:       runEditor,
	}}
	if _, e := editVerb(s, nil, env); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(path); string(b) != "--flag|"+path+"|" {
		t.Errorf("note %q", b)
	}
	if _, ok, _ := s.Read(editFailedFile); ok {
		t.Error("success recorded as failure")
	}

	// A failure, for after-edit to show.
	env.Sys.Environ = func() []string { return []string{"EDITOR=false"} }
	if _, e := editVerb(s, nil, env); e != nil {
		t.Fatal(e)
	}
	if b, _, _ := s.Read(editFailedFile); string(b) != "e: editor exited with status 1" {
		t.Errorf("failure %q", b)
	}
}

// after-edit shows the editor's failure after what changed.
func TestAfterEditFailure(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one"})
	tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("act", "e", "1@/")
		s, _ := openSession(fsys.OS{}, []string{SessionVar + "=" + tr.session})
		s.Write(editFailedFile, []byte("e: editor exited with status 1"))
		s.Close()
		helper("after-edit")
		if got := helper("text", "footer"); got != "notes unchanged · ✗ e: editor exited with status 1" {
			t.Errorf("footer %q", got)
		}
		// The next e starts clean.
		helper("act", "e", "1@/")
		helper("after-edit")
		if got := helper("text", "footer"); got != "notes unchanged" {
			t.Errorf("footer %q", got)
		}
		helper("quit")
	}})
}
