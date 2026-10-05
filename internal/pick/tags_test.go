package pick

import (
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/jsonio"
)

func TestParseTags(t *testing.T) {
	for value, want := range map[string]string{
		"a b":       `{"replace_all":["a","b"]}`,
		"a,b, a":    `{"replace_all":["a","b"]}`,
		"":          `{"replace_all":[]}`,
		"  ":        `{"replace_all":[]}`,
		"-":         `{"replace_all":[]}`,
		"+a -b":     `{"add":["a"],"remove":["b"]}`,
		"+a,+a":     `{"add":["a"]}`,
		"-a":        `{"remove":["a"]}`,
		"- a":       "mix",
		"a +b":      "mix",
		"-b,c":      "mix",
		"+":         `{"add":[""]}`, // for update to refuse
		"Bad +tag ": "mix",
	} {
		change, ok := parseTags(value)
		got := "mix"
		if ok {
			b, err := jsonio.MarshalLine(change)
			if err != nil {
				t.Fatal(err)
			}
			got = strings.TrimSuffix(string(b), "\n")
		}
		if got != want {
			t.Errorf("%q: %s, want %s", value, got, want)
		}
	}
}

// t: the one target's tags to start; bare tags replace them, prefixed
// ones change them, a lone - clears them; a mix is refused without a call,
// and a tag update refuses keeps the prompt open too.
func TestTagsAction(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one", "tags": []string{"x", "y"}})
	tr.run("create", map[string]any{"title": "two"})
	footerOnly := "transform-footer('/bin/ftask' __pick text 'footer')"
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		text := func(name string) string { return helper("text", name) }
		helper("command", "")
		helper("act", "t", "1@/")
		if p, q := text("prompt"), text("query"); p != "tags 1> " || q != "x y" {
			t.Errorf("prompt %q, query %q", p, q)
		}
		if got := helper("enter", "x +z"); got != footerOnly {
			t.Errorf("mix printed %q", got)
		}
		if f := text("footer"); f != "✗ tags: a mix of bare and +/- tags" {
			t.Errorf("mix: footer %q", f)
		}
		if got := helper("enter", "Bad"); got != footerOnly {
			t.Errorf("bad tag printed %q", got)
		}
		helper("enter", "x z")
		if f := text("footer"); f != "✓ set tags 1" {
			t.Errorf("footer %q", f)
		}
		helper("act", "t", "2@/", "1@/")
		if p, q := text("prompt"), text("query"); p != "tags 2 tasks> " || q != "" {
			t.Errorf("several: prompt %q, query %q", p, q)
		}
		helper("enter", "")
		if f := text("footer"); f != "no change" {
			t.Errorf("several, empty: footer %q", f)
		}
		// Only separators is empty too: it never clears them all.
		helper("act", "t", "2@/", "1@/")
		helper("enter", ", ,")
		if f := text("footer"); f != "no change" {
			t.Errorf("several, separators only: footer %q", f)
		}
		helper("act", "t", "2@/", "1@/")
		helper("enter", "+u -x")
		helper("act", "t", "1@/")
		if q := text("query"); q != "u z" {
			t.Errorf("after +u -x: %q", q)
		}
		helper("enter", "-")
		helper("act", "t", "2@/")
		if q := text("query"); q != "u" {
			t.Errorf("2 after +u: %q", q)
		}
		helper("enter", "")
		helper("quit")
	}})
	res := string(line)
	for _, want := range []string{
		`"input":{"id":1,"tags":{"replace_all":["Bad"]}}`,
		`"input":{"id":1,"tags":{"replace_all":["x","z"]}}`,
		`"input":{"id":2,"tags":{"add":["u"],"remove":["x"]}}`,
		`"input":{"id":1,"tags":{"replace_all":[]}}`,
		`"input":{"id":2,"tags":{"replace_all":[]}}`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("no %s in %s", want, res)
		}
	}
	// The mix and the several's empty values made no call.
	if n := strings.Count(res, `"operation":"update"`); n != 6 {
		t.Errorf("%d updates", n)
	}
}
