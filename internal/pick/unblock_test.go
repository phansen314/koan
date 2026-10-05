package pick

import (
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

// u's choices: the target's blockers that exist, in line order, complete
// ones too, every copy of a duplicated ID; then those that don't, keyed by
// the ID alone.
func TestUnblockChoices(t *testing.T) {
	target := tv{id: 9, folder: "/", r: model.Blocked}.view()
	target.BlockedBy = []model.ID{4, 99, 2, 3, 50}
	l := &Load{Tasks: []model.TaskView{
		target,
		tv{id: 1, folder: "/", r: model.Ready}.view(),
		tv{id: 2, folder: "/", r: model.Complete, done: "2026-09-01T00:00:00Z"}.view(),
		tv{id: 3, folder: "/a", r: model.Ready}.view(),
		tv{id: 3, folder: "/b", r: model.Ready}.view(),
		tv{id: 4, folder: "/", r: model.Ready, priority: p(1)}.view(),
	}}
	lines, why := unblockChoices(l, shownLine{Key: "9@/", ID: 9}, false)
	var ks []string
	for _, line := range lines {
		k, _, _ := strings.Cut(line, "\t")
		ks = append(ks, k)
	}
	if why != "" || !slices.Equal(ks, []string{"4@/", "3@/a", "3@/b", "2@/", "50", "99"}) {
		t.Fatalf("%s: %q", why, ks)
	}
	if lines[4] != "50\t?  50\t\t(no such task)" {
		t.Errorf("missing line %q", lines[4])
	}
	// Moved: the one task with its ID, wherever it now is.
	if moved, why := unblockChoices(l, shownLine{Key: "9@/gone", ID: 9}, false); why != "" || !slices.Equal(moved, lines) {
		t.Errorf("moved: %s, %q", why, moved)
	}
	// No task with its ID, or several copies, none in its folder: each
	// said as it is.
	for _, tc := range []struct {
		sl   shownLine
		want string
	}{{shownLine{Key: "8@/", ID: 8}, "is gone"}, {shownLine{Key: "3@/c", ID: 3}, "has 2 copies, none in /c"}} {
		if _, why := unblockChoices(l, tc.sl, false); why != tc.want {
			t.Errorf("%s: %q, want %q", tc.sl.Key, why, tc.want)
		}
	}
}

// u through pick whole: one target; the chosen removed in one unblock; a
// task with no blockers gets no list.
func TestUnblockAction(t *testing.T) {
	tr := newTestTree(t)
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	tr.run("block", map[string]any{"id": 3, "blockers": []int{1, 2}})
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		if got := helper("act", "u", "1@/"); !strings.HasPrefix(got, "clear-selection+") {
			t.Errorf("no blockers printed %q", got)
		}
		if f := helper("text", "footer"); f != "1 has no blockers" {
			t.Errorf("no blockers: %q", f)
		}
		helper("act", "u", "3@/", "1@/")
		if f := helper("text", "footer"); f != "✗ u takes one task: 2 marked" {
			t.Errorf("two: %q", f)
		}
		helper("act", "u", "3@/")
		if p, c := helper("text", "prompt"), helper("choices"); p != "unblock 3> " || !strings.HasPrefix(c, "~1@/\t") || strings.Count(c, "\n") != 2 {
			t.Errorf("prompt %q, choices %q", p, c)
		}
		helper("enter", "", "~2@/", "~1@/")
		if f := helper("text", "footer"); f != "✓ unblocked 3" {
			t.Errorf("footer %q", f)
		}
		helper("quit")
	}})
	if !strings.Contains(string(line), `"input":{"id":3,"blockers":[1,2]}`) || strings.Count(string(line), `"operation":"unblock"`) != 1 {
		t.Errorf("%s", line)
	}
}

// u finds its target by ID when another process moved it meanwhile, as the
// final read does; one deleted meanwhile is gone.
func TestUnblockMoved(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/b"})
	tr.run("create", map[string]any{"title": "one"})
	tr.run("create", map[string]any{"title": "two"})
	tr.run("block", map[string]any{"id": 2, "blockers": []int{1}})
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		tr.run("move", map[string]any{"id": 2, "to": "/b"})
		helper("act", "u", "2@/")
		if p, c := helper("text", "prompt"), helper("choices"); p != "unblock 2> " || !strings.HasPrefix(c, "~1@/\t") {
			t.Errorf("moved: prompt %q, choices %q", p, c)
		}
		helper("enter", "", "~1@/")
		if f := helper("text", "footer"); f != "✓ unblocked 2" {
			t.Errorf("moved: footer %q", f)
		}
		tr.run("delete", map[string]any{"id": 2})
		helper("act", "u", "2@/b")
		if f := helper("text", "footer"); f != "✗ u: 2 is gone" {
			t.Errorf("deleted: footer %q", f)
		}
		helper("quit")
	}})
	if !strings.Contains(string(line), `"input":{"id":2,"blockers":[1]}`) {
		t.Errorf("%s", line)
	}
}
