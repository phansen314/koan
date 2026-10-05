package pick

import (
	"strings"
	"testing"
)

// n asks for a title in a prompt that starts with the search query. Enter
// creates the task; a refused title keeps the prompt open; Esc or
// ctrl-space cancels, with the search query back. After a create, the
// query is cleared and the cursor goes to the new task.
func TestNewAction(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one"})
	text := func(helper func(...string) string, name string) string { return helper("text", name) }
	keys := strings.Join(commandKeys(), ",")
	out, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "pass")
		got := helper("act", "n")
		if want := "show-input+unbind(" + keys + ")+disable-search+transform-prompt('/bin/ftask' __pick text 'prompt')+transform-query('/bin/ftask' __pick text 'query')+transform-header('/bin/ftask' __pick text 'header')"; got != want {
			t.Errorf("n printed\n%q\nwant\n%q", got, want)
		}
		if p, q, h := text(helper, "prompt"), text(helper, "query"), text(helper, "header"); p != "new> " || q != "pass" || !strings.HasPrefix(h, "[new]\n/\n") {
			t.Errorf("prompt %q, query %q, header %q", p, q, h)
		}

		// A title create refuses: the prompt stays, the status says why.
		if got := helper("enter", "", "1@/"); got != "transform-footer('/bin/ftask' __pick text 'footer')" {
			t.Errorf("empty title printed %q", got)
		}
		if f := text(helper, "footer"); !strings.HasPrefix(f, "✗ create: invalid-input: ") {
			t.Errorf("footer %q", f)
		}

		// Created: the prompt closes, the query is cleared, the cursor
		// goes to the new task, second in line order.
		got = helper("enter", "Buy tickets", "1@/")
		for _, part := range []string{
			// No hide-input: on-load does it, after the reload.
			"enable-search+change-query()+transform-prompt('/bin/ftask' __pick text 'prompt')+rebind(" + keys + ",tab)+transform-header(",
			"+rebind(load)+clear-selection+reload-sync('/bin/ftask' __pick lines)+",
		} {
			if !strings.Contains(got, part) {
				t.Errorf("create printed %q, without %q", got, part)
			}
		}
		if p, q, f := text(helper, "prompt"), text(helper, "query"), text(helper, "footer"); p != "open> " || q != "" || f != "✓ created 2" {
			t.Errorf("after: prompt %q, query %q, footer %q", p, q, f)
		}
		if got := helper("on-load"); got != "hide-input+pos(2)+unbind(load)" {
			t.Errorf("on-load %q", got)
		}
		if h := text(helper, "header"); !strings.HasPrefix(h, "[cmd]\n") {
			t.Errorf("header %q", h)
		}

		// Cancelled, by Esc and by ctrl-space: the search query is back.
		helper("insert")
		helper("command", "renew")
		for _, cancel := range []string{"esc", "command"} {
			helper("act", "n")
			helper("text", "query")
			got := helper(cancel, "half a title")
			if !strings.HasPrefix(got, "enable-search+transform-query(") || !strings.HasSuffix(got, "+rebind(load)+clear-selection+reload-sync('/bin/ftask' __pick lines)") {
				t.Errorf("%s printed %q", cancel, got)
			}
			if got := helper("on-load"); got != "hide-input+unbind(load)" {
				t.Errorf("%s: on-load %q", cancel, got)
			}
			if q, h := text(helper, "query"), text(helper, "header"); q != "renew" || !strings.HasPrefix(h, "[cmd] query: renew\n") {
				t.Errorf("%s: query %q, header %q", cancel, q, h)
			}
		}
		// Back in command mode, Esc quits.
		if got := helper("esc", "renew"); got != "accept" {
			t.Errorf("esc printed %q", got)
		}
	}})
	res := string(result(t, line))
	if !out.OK || strings.Count(res, `"operation":"create"`) != 2 || !strings.Contains(res, `"title":"Buy tickets"`) {
		t.Errorf("%s", res)
	}
}

// A new task gets the scope folder and tags_all; one that isn't a
// candidate, here under --tags-any, is created but not listed.
func TestNewActionScope(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/a"})
	tr.run("create", map[string]any{"title": "one", "folder": "/a", "tags": []string{"x", "y"}})
	_, line := tr.pick(map[string]any{"folder": "/a", "tags_all": []string{"x", "y"}}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		helper("act", "n")
		helper("enter", "two", "1@/a")
		if got := helper("on-load"); got != "hide-input+pos(2)+unbind(load)" {
			t.Errorf("on-load %q", got)
		}
		helper("enter", "", "2@/a")
	}})
	if !strings.Contains(string(line), `"id":2,"title":"two"`) || !strings.Contains(string(line), `"tags":["x","y"]`) || !strings.Contains(string(line), `"folder":"/a"`) {
		t.Errorf("%s", line)
	}

	tr.pick(map[string]any{"tags_any": []string{"x"}}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("command", "")
		helper("act", "n")
		helper("enter", "untagged", "1@/a")
		if got := helper("on-load"); got != "hide-input+unbind(load)" {
			t.Errorf("on-load %q", got)
		}
		if f := helper("text", "footer"); f != "✓ created 3 (not in this list)" {
			t.Errorf("footer %q", f)
		}
		helper("quit")
	}})
}
