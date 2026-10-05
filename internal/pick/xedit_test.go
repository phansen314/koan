package pick

import (
	"os"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
)

func TestXChanges(t *testing.T) {
	wrote := `{"title":"a","priority":null,"tags":["x","y"],"extra":{"k":1,"m":"s"}}`
	for edited, want := range map[string]string{
		wrote: `{}`,
		`{"title":"b","priority":null,"tags":["x","y"],"extra":{"k":1,"m":"s"}}`: `{"title":"b"}`,
		`{"priority":3}`:                      `{"priority":3}`,
		`{"tags":["y","x","x"]}`:              `{}`,
		`{"tags":["x"]}`:                      `{"tags":{"replace_all":["x"]}}`,
		`{"extra":{"m":"s","k":1.0}}`:         `{}`,
		`{"extra":{"k":2,"n":true}}`:          `{"extra":{"merge":{"k":2,"n":true},"remove":["m"]}}`,
		`{"extra":{"k":1}}`:                   `{"extra":{"remove":["m"]}}`,
		`{"extra":[]}`:                        `{"extra":{"merge":[]}}`,
		`{"title":"a","tags":"x","extra":{}}`: `{"tags":{"replace_all":"x"},"extra":{"remove":["k","m"]}}`,
	} {
		w, _, _ := jsonio.ParseObject([]byte(wrote))
		e, _, err := jsonio.ParseObject([]byte(edited))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := jsonio.MarshalLine(xChanges(w, e))
		if got := strings.TrimSuffix(string(b), "\n"); got != want {
			t.Errorf("%s: %s, want %s", edited, got, want)
		}
	}
}

// x through pick whole, the editor played by writing the file between e's
// execute and after-x.
func TestXAction(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one", "priority": 2, "tags": []string{"a"}, "extra": map[string]any{"k": 1}})
	tr.run("create", map[string]any{"title": "two"})
	tr.run("create", map[string]any{"title": "three"})
	execute := "execute('/bin/koan' __pick edit)+transform('/bin/koan' __pick after-x)"
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		footer := func() string { return helper("text", "footer") }
		// x opens; edit, if not nil, writes the file; after-x applies.
		x := func(key string, edit func(path, content string) string) string {
			t.Helper()
			if got := helper("act", "x", key); got != execute {
				t.Fatalf("x printed %q", got)
			}
			st := xNow(t, tr.session)
			b, _ := os.ReadFile(st.Path)
			if edit != nil {
				if err := os.WriteFile(st.Path, []byte(edit(st.Path, string(b))), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Changed or not, valid or not, the marks are cleared.
			if got := helper("after-x"); !strings.HasPrefix(got, "clear-selection+") {
				t.Errorf("after-x printed %q", got)
			}
			return string(b)
		}

		got := x("1@/", nil)
		want := "{\n  \"title\": \"one\",\n  \"priority\": 2,\n  \"tags\": [\n    \"a\"\n  ],\n  \"extra\": {\n    \"k\": 1\n  }\n}\n"
		if got != want {
			t.Errorf("file\n%s\nwant\n%s", got, want)
		}
		if f := footer(); f != "x 1: no change" {
			t.Errorf("unchanged: %q", f)
		}

		// Not valid: kept, and reopened with the edits.
		x("1@/", func(_, c string) string { return c + "oops" })
		if f := footer(); !strings.HasPrefix(f, "✗ x 1: not a JSON object: ") {
			t.Errorf("invalid: %q", f)
		}
		if got := x("1@/", func(_, c string) string { return strings.Replace(c, "oops", "", 1) + "" }); !strings.HasSuffix(got, "}\noops") {
			t.Errorf("not reopened with the edits: %q", got)
		}
		if f := footer(); f != "x 1: no change" {
			t.Errorf("fixed back: %q", f)
		}
		x("1@/", func(_, c string) string { return strings.Replace(c, `"extra"`, `"notes": "", "extra"`, 1) })
		if f := footer(); f != "✗ x 1: notes can't be edited here; only title, priority, tags, extra" {
			t.Errorf("unknown key: %q", f)
		}

		// Another action discards the kept file: x starts over.
		helper("act", "c", "2@/")
		if got := x("1@/", nil); strings.Contains(got, "notes") {
			t.Errorf("not discarded: %q", got)
		}

		// update refuses: kept too.
		x("1@/", func(_, c string) string { return strings.Replace(c, `"priority": 2`, `"priority": "high"`, 1) })
		if f := footer(); !strings.HasPrefix(f, "✗ update 1: invalid-input: ") {
			t.Errorf("refused: %q", f)
		}
		if got := x("1@/", func(_, c string) string { return strings.Replace(c, `"high"`, `2`, 1) }); !strings.Contains(got, `"high"`) {
			t.Errorf("refused file not kept: %q", got)
		}

		// The tree busy: kept too, to try again.
		r, err := fsys.OS{}.OpenRoot(tr.root)
		if err != nil {
			t.Fatal(err)
		}
		l, err := r.Lock()
		if err != nil {
			t.Fatal(err)
		}
		x("1@/", func(_, c string) string { return strings.Replace(c, `"a"`, `"b"`, 1) })
		if f := footer(); !strings.HasPrefix(f, "✗ update 1: busy") {
			t.Errorf("busy: %q", f)
		}
		l.Unlock()
		r.Close()
		if got := x("1@/", nil); !strings.Contains(got, `"b"`) {
			t.Errorf("busy file not kept: %q", got)
		}
		if f := footer(); f != "✓ updated 1" {
			t.Errorf("after busy: %q", f)
		}

		// Only what was edited is sent: a change made elsewhere meanwhile
		// to another field survives.
		helper("act", "x", "1@/")
		st := xNow(t, tr.session)
		tr.run("update", map[string]any{"id": 1, "priority": 9})
		b, _ := os.ReadFile(st.Path)
		os.WriteFile(st.Path, []byte(strings.Replace(strings.Replace(string(b), `"one"`, `"uno"`, 1), `"k": 1`, `"k": 1, "z": [1]`, 1)), 0o600)
		helper("after-x")
		if f := footer(); f != "✓ updated 1" {
			t.Errorf("updated: %q", f)
		}

		// One task only.
		helper("act", "x", "1@/", "3@/")
		if f := footer(); f != "✗ x takes one task: 2 marked" {
			t.Errorf("two: %q", f)
		}
		helper("enter", "", "1@/")
	}})
	res := string(line)
	for _, want := range []string{
		`"input":{"id":1,"priority":"high"}`,
		`"input":{"id":1,"tags":{"replace_all":["b"]}},"output":{"ok":false,"error":{"kind":"busy"`,
		`"input":{"id":1,"tags":{"replace_all":["b"]}},"output":{"ok":true`,
		`"input":{"id":1,"title":"uno","extra":{"merge":{"z":[1]}}}`,
		`"title":"uno","priority":9,`,
	} {
		if !strings.Contains(res, want) {
			t.Errorf("no %s in %s", want, res)
		}
	}
	if n := strings.Count(res, `"operation":"update"`); n != 4 {
		t.Errorf("%d updates", n)
	}
}

// xNow is the open x, from the session.
func xNow(t *testing.T, dir string) xState {
	t.Helper()
	s, e := openSession(fsys.OS{}, []string{SessionVar + "=" + dir})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	st, e := readX(s)
	if e != nil || st == nil {
		t.Fatalf("no x: %v", e)
	}
	return *st
}
