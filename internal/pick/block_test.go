package pick

import (
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

// b's candidates: open tasks tree-wide, but the targets and every task from
// which a target is reached over blocked_by, complete tasks and every
// copy's edges included.
func TestBlockerCandidates(t *testing.T) {
	blocked := func(v model.TaskView, by ...model.ID) model.TaskView { v.BlockedBy = by; return v }
	l := &Load{Tasks: []model.TaskView{
		tv{id: 1, folder: "/", r: model.Ready}.view(),
		blocked(tv{id: 2, folder: "/", r: model.Blocked}.view(), 1),
		blocked(tv{id: 3, folder: "/", r: model.Blocked}.view(), 2),
		blocked(tv{id: 4, folder: "/", r: model.Complete, done: "2026-09-01T00:00:00Z"}.view(), 3),
		tv{id: 5, folder: "/x", r: model.Ready}.view(),
		blocked(tv{id: 6, folder: "/", r: model.Ready}.view(), 4),
		blocked(tv{id: 7, folder: "/a", r: model.Blocked}.view(), 1),
		tv{id: 7, folder: "/b", r: model.Ready}.view(),
	}}
	for _, tc := range []struct {
		targets []model.ID
		want    []string
	}{
		// 6 reaches 1 through the complete 4; 7's copy in /b by its
		// copy in /a's edge.
		{[]model.ID{1}, []string{"5@/x"}},
		{[]model.ID{5}, []string{"1@/", "6@/", "7@/b", "2@/", "3@/", "7@/a"}},
		{[]model.ID{3, 5}, []string{"1@/", "7@/b", "2@/", "7@/a"}},
	} {
		var targets []shownLine
		for _, id := range tc.targets {
			targets = append(targets, shownLine{ID: id})
		}
		if got := keys(blockerCandidates(l, targets)); !slices.Equal(got, tc.want) {
			t.Errorf("%v: %q, want %q", tc.targets, got, tc.want)
		}
	}
}

// b through pick whole: the chosen are added to each target, in the
// choose list's order; a cycle made meanwhile is refused by block.
func TestBlockAction(t *testing.T) {
	tr := newTestTree(t)
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		helper("act", "b", "3@/")
		if p, c := helper("text", "prompt"), helper("choices"); p != "blockers of 3> " || !strings.HasPrefix(c, "~1@/\t") || strings.Count(c, "\n") != 2 {
			t.Errorf("prompt %q, choices %q", p, c)
		}
		helper("enter", "", "~2@/", "~1@/")
		if f := helper("text", "footer"); f != "✓ blocked 3" {
			t.Errorf("footer %q", f)
		}
		// 3 is blocked by 1 and 2, so 3 can't block 1; 2 can.
		helper("act", "b", "1@/")
		if c := helper("choices"); !strings.HasPrefix(c, "~2@/\t") || strings.Count(c, "\n") != 1 {
			t.Errorf("1's choices %q", c)
		}
		helper("esc", "")

		// A cycle made meanwhile: 3 may block 4 when the list opens, then
		// 1 is blocked by 4, and 3 reaches 4 through 1.
		tr.run("create", map[string]any{"title": "four"})
		helper("act", "r")
		helper("act", "b", "4@/")
		tr.run("block", map[string]any{"id": 1, "blockers": []int{4}})
		helper("enter", "", "~3@/")
		if f := helper("text", "footer"); !strings.HasPrefix(f, "✗ block 4 ← 3: conflict (acyclic): ") {
			t.Errorf("cycle: footer %q", f)
		}
		helper("quit")
	}})
	if !strings.Contains(string(line), `"input":{"id":3,"blockers":[1,2]}`) {
		t.Errorf("%s", line)
	}
}
