package pick

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

// d decides by the readiness the lines show: any open marks all done, all
// done reopens all. A change made elsewhere since the last load makes
// its call a no-op, never a reversal.
func TestDoneAction(t *testing.T) {
	tr := newTestTree(t)
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	tr.run("done", map[string]any{"id": 3})
	footer := func(helper func(...string) string) string { return helper("text", "footer") }
	_, line := tr.pick(map[string]any{"scope": "all"}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		for _, step := range []struct {
			before func()
			keys   []string
			want   string
		}{
			// 3 is shown done, 1 open: mark both done.
			{nil, []string{"3@/", "1@/"}, "✓ done 1: 1 · ✓ already done 1: 3"},
			{nil, []string{"1@/", "2@/", "3@/"}, "✓ done 1: 2 · ✓ already done 2: 1, 3"},
			// All shown done: reopen all.
			{nil, []string{"1@/", "2@/"}, "✓ reopened 2: 1, 2"},
			// Completed elsewhere, still shown open: no reversal.
			{func() { tr.run("done", map[string]any{"id": 1}) }, []string{"1@/"}, "✓ already done 1"},
			// Reopened elsewhere, still shown done: no reversal.
			{func() { tr.run("reopen", map[string]any{"id": 3}) }, []string{"3@/"}, "✓ already open 3"},
		} {
			if step.before != nil {
				step.before()
			}
			helper(append([]string{"act", "d"}, step.keys...)...)
			if got := footer(helper); got != step.want {
				t.Errorf("%q: %q, want %q", step.keys, got, step.want)
			}
		}
		helper("quit")
	}})
	var got struct {
		Result struct {
			Actions []struct {
				Operation string `json:"operation"`
				Input     struct {
					ID int `json:"id"`
				} `json:"input"`
				Output struct {
					OK     bool `json:"ok"`
					Result struct {
						Changed bool `json:"changed"`
					} `json:"result"`
				} `json:"output"`
			} `json:"actions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatal(err)
	}
	var summary []string
	for _, a := range got.Result.Actions {
		s := a.Operation + " " + string(idNumber(model.ID(a.Input.ID)))
		if !a.Output.Result.Changed {
			s += " unchanged"
		}
		summary = append(summary, s)
	}
	want := []string{
		"done 1", "done 3 unchanged",
		// Line order: ready first, then done, newest completed first.
		"done 2", "done 1 unchanged", "done 3 unchanged",
		"reopen 1", "reopen 2",
		"done 1 unchanged",
		"reopen 3 unchanged",
	}
	if !slices.Equal(summary, want) {
		t.Errorf("actions\n%q\nwant\n%q", summary, want)
	}
}
