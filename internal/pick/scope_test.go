package pick

import (
	"strings"
	"testing"
)

// s cycles the scope, ready → open → all → ready, reloading each time with
// the prompt naming it; r reloads, finding what changed meanwhile.
func TestScopeAndReload(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "ready"})
	tr.run("create", map[string]any{"title": "blocked", "blocked_by": []int{1}})
	tr.run("create", map[string]any{"title": "done"})
	tr.run("complete", map[string]any{"id": 3})
	lineKeys := func(helper func(...string) string) []string {
		var ks []string
		for _, l := range strings.Split(strings.TrimSuffix(helper("lines"), "\n"), "\n") {
			k, _, _ := strings.Cut(l, "\t")
			ks = append(ks, k)
		}
		return ks
	}
	tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		for _, step := range []struct {
			prompt string
			lines  string
		}{
			{"all> ", "1@/ 2@/ 3@/"},
			{"ready> ", "1@/"},
			{"open> ", "1@/ 2@/"},
		} {
			got := helper("act", "s")
			if !strings.HasPrefix(got, "clear-selection+reload-sync(") || !strings.Contains(got, "+transform-prompt('/bin/koan' __pick text 'prompt')+") {
				t.Errorf("s printed %q", got)
			}
			if p := helper("text", "prompt"); p != step.prompt {
				t.Errorf("prompt %q, want %q", p, step.prompt)
			}
			if f := helper("text", "footer"); f != "✓ scope: "+strings.TrimSuffix(step.prompt, "> ") {
				t.Errorf("footer %q", f)
			}
			if ks := strings.Join(lineKeys(helper), " "); ks != step.lines {
				t.Errorf("%s lines %s, want %s", step.prompt, ks, step.lines)
			}
		}
		tr.run("create", map[string]any{"title": "new"})
		if got := helper("act", "r"); !strings.HasPrefix(got, "clear-selection+reload-sync(") || strings.Contains(got, "prompt") {
			t.Errorf("r printed %q", got)
		}
		if f := helper("text", "footer"); f != "✓ reloaded" {
			t.Errorf("footer %q", f)
		}
		if ks := strings.Join(lineKeys(helper), " "); ks != "1@/ 4@/ 2@/" {
			t.Errorf("after r: %s", ks)
		}
		helper("quit")
	}})
}

// s whose reload fails keeps the scope the list shows: the prompt, and the
// next reload, are still in it.
func TestScopeFailedReload(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/a"})
	tr.run("create", map[string]any{"title": "one", "folder": "/a"})
	tr.pick(map[string]any{"folder": "/a"}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		tr.run("delete", map[string]any{"id": 1})
		tr.run("delete-folder", map[string]any{"folder": "/a"})
		got := helper("act", "s")
		if strings.Contains(got, "prompt") || strings.Contains(got, "reload-sync") {
			t.Errorf("s printed %q", got)
		}
		// The prompt is pick's first, never rewritten.
		if p, f := helper("text", "prompt"), helper("text", "footer"); p != "" || !strings.HasPrefix(f, "✗ reload: not-found: ") {
			t.Errorf("prompt %q, footer %q", p, f)
		}
		tr.run("create-folder", map[string]any{"folder": "/a"})
		helper("act", "r")
		if f := helper("text", "footer"); f != "✓ reloaded" {
			t.Errorf("r: footer %q", f)
		}
		// Still open, so s goes to all.
		helper("act", "s")
		if p := helper("text", "prompt"); p != "all> " {
			t.Errorf("s again: prompt %q", p)
		}
		helper("quit")
	}})
}
