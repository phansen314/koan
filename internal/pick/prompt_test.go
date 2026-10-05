package pick

import (
	"strings"
	"testing"
)

// A prompt on targets, through p: it starts with the one target's value,
// or empty for several; a refused value keeps it open; with several, an
// empty value does nothing; any other failure closes it.
func TestPromptOnTargets(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one", "priority": 5})
	tr.run("create", map[string]any{"title": "two"})
	tr.run("create", map[string]any{"title": "three"})
	footerOnly := "transform-footer('/bin/ftask' __pick text 'footer')"
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		text := func(name string) string { return helper("text", name) }
		helper("command", "")

		// One target: its value, and its ID in the label.
		helper("act", "p", "1@/")
		if p, q := text("prompt"), text("query"); p != "priority 1> " || q != "5" {
			t.Errorf("one: prompt %q, query %q", p, q)
		}
		if got := helper("enter", "x"); got != footerOnly {
			t.Errorf("refused printed %q", got)
		}
		if f := text("footer"); !strings.HasPrefix(f, "✗ priority 1: invalid-input: ") {
			t.Errorf("refused: footer %q", f)
		}
		if got := helper("enter", "7"); !strings.Contains(got, "reload-sync(") {
			t.Errorf("applied printed %q", got)
		}
		if f := text("footer"); f != "✓ set priority 1" {
			t.Errorf("applied: footer %q", f)
		}
		// An integer in another form is sent as the number it is.
		for _, v := range []string{"+05", "-05"} {
			helper("act", "p", "1@/")
			helper("enter", v)
			if f := text("footer"); f != "✓ set priority 1" {
				t.Errorf("%s: footer %q", v, f)
			}
		}

		// Several: empty, counted in the label; empty does nothing.
		helper("act", "p", "2@/", "1@/")
		if p, q := text("prompt"), text("query"); p != "priority 2 tasks> " || q != "" {
			t.Errorf("several: prompt %q, query %q", p, q)
		}
		if got := helper("enter", ""); !strings.Contains(got, "reload-sync(") {
			t.Errorf("no change printed %q", got)
		}
		if f := text("footer"); f != "no change" {
			t.Errorf("no change: footer %q", f)
		}
		helper("act", "p", "3@/", "2@/")
		helper("enter", "3")
		if f := text("footer"); f != "✓ set priority 2: 2, 3" {
			t.Errorf("several: footer %q", f)
		}

		// null clears; with one target, so does an empty value.
		helper("act", "p", "1@/")
		helper("enter", " null ")
		helper("act", "p", "2@/")
		if q := text("query"); q != "3" {
			t.Errorf("2's priority %q", q)
		}
		helper("enter", "")
		if f := text("footer"); f != "✓ set priority 2" {
			t.Errorf("cleared: footer %q", f)
		}

		// A failure that isn't the value closes the prompt.
		helper("act", "p", "3@/")
		tr.run("delete", map[string]any{"id": 3})
		if got := helper("enter", "1"); !strings.Contains(got, "reload-sync(") {
			t.Errorf("not found printed %q", got)
		}
		if f := text("footer"); !strings.HasPrefix(f, "✗ priority 3: not-found: ") {
			t.Errorf("not found: footer %q", f)
		}
		helper("quit")
	}})
	// Every call, the refused one included; none for no change.
	if n := strings.Count(string(line), `"operation":"update"`); n != 9 ||
		!strings.Contains(string(line), `"input":{"id":1,"priority":5}`) || !strings.Contains(string(line), `"input":{"id":1,"priority":-5}`) ||
		!strings.Contains(string(line), `"input":{"id":1,"priority":null}`) || !strings.Contains(string(line), `"input":{"id":2,"priority":null}`) {
		t.Errorf("%d updates: %s", n, line)
	}
}
