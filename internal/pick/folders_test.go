package pick

import (
	"strings"
	"testing"
)

// m and f: a single-choice list of every folder, / first; m moves each
// target there, f makes it the scope folder.
func TestMoveAndFolder(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/a"})
	tr.run("create-folder", map[string]any{"folder": "/b"})
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		text := func(name string) string { return helper("text", name) }
		helper("command", "")
		helper("act", "m", "2@/", "1@/")
		if p, c, h := text("prompt"), helper("choices"), text("header"); p != "move to> " || c != "~/\t\t/\n~/a\t\t/a\n~/b\t\t/b\n" || h != "[move to]\n/\n"+pickOneHint {
			t.Errorf("m: prompt %q, choices %q, header %q", p, c, h)
		}
		if got := helper("preview", "~/a"); got != "/a\n" {
			t.Errorf("folder preview %q", got)
		}
		helper("enter", "", "~/b")
		if f := text("footer"); f != "✓ moved 2: 1, 2" {
			t.Errorf("m: footer %q", f)
		}

		helper("act", "f")
		if p, h := text("prompt"), text("header"); p != "folder> " || h != "[folder]\n/\n"+pickOneHint {
			t.Errorf("f: prompt %q, header %q", p, h)
		}
		helper("enter", "", "~/b")
		if f, h, l := text("footer"), text("header"), helper("lines"); f != "✓ folder: /b" || !strings.HasPrefix(h, "[cmd]\n/b\n") ||
			!strings.HasPrefix(l, "1@/b\t") || strings.Count(l, "\n") != 2 {
			t.Errorf("f: footer %q, header %q, lines %q", f, h, l)
		}
		if got := helper("on-load"); got != "hide-input+pos(1)+unbind(load)" {
			t.Errorf("f: on-load %q", got)
		}
		helper("quit")
	}})
	if !strings.Contains(string(line), `"input":{"id":1,"to":"/b"}`) || !strings.Contains(string(line), `"input":{"id":2,"to":"/b"}`) ||
		strings.Count(string(line), `"operation"`) != 2 {
		t.Errorf("%s", line)
	}
}

// f to a folder deleted while its list was open: the reload fails, and the
// scope stays the folder the list shows, so the next reload works.
func TestFolderGone(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/a"})
	tr.run("create", map[string]any{"title": "one"})
	tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		text := func(name string) string { return helper("text", name) }
		helper("command", "")
		helper("act", "f")
		helper("on-load")
		tr.run("delete-folder", map[string]any{"folder": "/a"})
		helper("enter", "", "~/a")
		if f, h := text("footer"), text("header"); !strings.HasPrefix(f, "✗ reload: not-found: ") || !strings.HasPrefix(h, "[cmd]\n/\n") {
			t.Errorf("footer %q, header %q", f, h)
		}
		if got := helper("on-load"); got != "hide-input+unbind(load)" {
			t.Errorf("on-load %q", got)
		}
		helper("act", "r")
		if f := text("footer"); f != "✓ reloaded" {
			t.Errorf("r: footer %q", f)
		}
		helper("quit")
	}})
}
