package main_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// extraOf is what show prints for a task's or folder's extra.
func extraOf(t *testing.T, home, ref, record string) string {
	t.Helper()
	b, err := json.Marshal(ok(t, home, "show", ref).Result()[record].(map[string]any)["extra"])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --extra-merge is a JSON Merge Patch, on a task and on a folder alike:
// each row of the spec's table under update.
func TestExtraMergePatch(t *testing.T) {
	home := root(t)
	for i, c := range []struct{ stored, patch, want string }{
		{`{"jira":"WEB-12"}`, `{"remind":{"at":"2026-11-09T09:00:00Z"}}`, `{"jira":"WEB-12","remind":{"at":"2026-11-09T09:00:00Z"}}`},
		{`{"remind":{"at":"A","note":"x"}}`, `{"remind":{"at":"B"}}`, `{"remind":{"at":"B","note":"x"}}`},
		{`{"remind":{"at":"A","note":"x"}}`, `{"remind":{"note":null}}`, `{"remind":{"at":"A"}}`},
		{`{"rel":{"dup":[42]}}`, `{"rel":{"dup":[42,57]}}`, `{"rel":{"dup":[42,57]}}`},
		{`{"remind":{"at":"A"}}`, `{"remind":null}`, `{}`},
		{`{"remind":{"at":"A"}}`, `{"remind":{"at":null}}`, `{"remind":{}}`},
		{`{"remind":"soon"}`, `{"remind":{"at":"A","note":null}}`, `{"remind":{"at":"A"}}`},
		{`{"remind":[1,2]}`, `{"remind":{"at":{"deep":null,"kept":1}}}`, `{"remind":{"at":{"kept":1}}}`},
		{`{"a":{"b":{"c":1,"d":2}}}`, `{"a":{"b":{"c":null,"e":[null]}}}`, `{"a":{"b":{"d":2,"e":[null]}}}`},
		{`{"a":{"b":1}}`, `{"a":"text"}`, `{"a":"text"}`},
		{`{"a":{"b":1}}`, `{"a":[{"x":null}]}`, `{"a":[{"x":null}]}`},
	} {
		task := fmt.Sprint(ok(t, home, "task", "create", "t", "--extra", c.stored).Result()["id"])
		if r := ok(t, home, "update", task, "--extra-merge", c.patch); fmt.Sprint(r.Result()["updated"]) != "["+task+"]" {
			t.Errorf("%s + %s: %s", c.stored, c.patch, r.Line())
		}
		if got := extraOf(t, home, task, "task"); got != c.want {
			t.Errorf("task: %s + %s = %s, want %s", c.stored, c.patch, got, c.want)
		}
		folder := fmt.Sprintf("/f%d", i)
		ok(t, home, "folder", "create", folder, "--extra", c.stored)
		ok(t, home, "update", folder, "--extra-merge", c.patch)
		if got := extraOf(t, home, folder, "folder"); got != c.want {
			t.Errorf("folder: %s + %s = %s, want %s", c.stored, c.patch, got, c.want)
		}
	}
}

// One patch for several subjects applies to each as it stands; applying it
// to one doesn't change what the next gets.
func TestExtraMergeSeveral(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "a", "--extra", `{"n":{"x":1}}`) // 1
	ok(t, home, "task", "create", "b", "--extra", `{"n":{"y":2}}`) // 2
	ok(t, home, "task", "create", "c", "--extra", `{"n":"text"}`)  // 3
	ok(t, home, "update", "1,2,3", "--extra-merge", `{"n":{"z":{"p":1,"q":null}}}`)
	for ref, want := range map[string]string{
		"1": `{"n":{"x":1,"z":{"p":1}}}`, "2": `{"n":{"y":2,"z":{"p":1}}}`, "3": `{"n":{"z":{"p":1}}}`,
	} {
		if got := extraOf(t, home, ref, "task"); got != want {
			t.Errorf("@%s: %s, want %s", ref, got, want)
		}
	}
}

// --extra-remove is a null merge: the same result, and the same event.
func TestExtraRemoveIsNullMerge(t *testing.T) {
	home := root(t)
	for _, title := range []string{"a", "b"} {
		ok(t, home, "task", "create", title, "--extra", `{"keep":1,"remind":{"at":"A"}}`)
	}
	removed := changeOf(t, home, "update", "1", "--extra-remove", "remind")
	merged := changeOf(t, home, "update", "2", "--extra-merge", `{"remind":null}`)
	if len(removed) != 1 || len(merged) != 1 || string(removed[0].Data) != string(merged[0].Data) {
		t.Errorf("events differ: %+v, %+v", removed, merged)
	}
	if string(removed[0].Data) != `{"changes":[{"field":"extra","before":{"keep":1,"remind":{"at":"A"}},"after":{"keep":1}}]}` {
		t.Errorf("event: %s", removed[0].Data)
	}
	if a, b := extraOf(t, home, "1", "task"), extraOf(t, home, "2", "task"); a != b || a != `{"keep":1}` {
		t.Errorf("results: %s, %s", a, b)
	}
}

// --extra-remove is a list like any other: comma-separated or repeated,
// and the same through --input.
func TestExtraRemoveList(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "t", "--extra", `{"a":1,"b":2,"c":3,"d":4,"e":5}`)
	ok(t, home, "update", "1", "--extra-remove", "a,b", "--extra-remove", "c")
	if got := extraOf(t, home, "1", "task"); got != `{"d":4,"e":5}` {
		t.Errorf("command line: %s", got)
	}
	input(t, home, "update", `{"refs":[1],"extra_remove":["d","e"]}`)
	if got := extraOf(t, home, "1", "task"); got != `{}` {
		t.Errorf("input: %s", got)
	}
	// Every comma splits, so the fault is at the part that is bad.
	r := invalidAt(t, home, "/extra_remove/1", "update", "1", "--extra-remove", "a,,c")
	if problemFields(r) != "/extra_remove/1" {
		t.Errorf("empty part: %s", r.Line())
	}
}

// A merge that changes nothing is changed false, moves no updated_at and
// records no event.
func TestExtraNoChange(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "t", "--extra", `{"a":1,"b":{},"c":{"d":[1]}}`)
	ok(t, home, "folder", "create", "/w", "--extra", `{"a":1,"b":{},"c":{"d":[1]}}`) // 2
	db(t, home, "UPDATE task SET updated_at = '"+fixed+"'")
	for _, ref := range []string{"1", "/w"} {
		for _, args := range [][]string{
			{"--extra-merge", `{}`},
			{"--extra-merge", `{"a":1}`},
			{"--extra-merge", `{"gone":null}`},
			{"--extra-merge", `{"b":{"gone":null}}`},
			{"--extra-merge", `{"c":{"d":[1]}}`},
			{"--extra-remove", "gone"},
		} {
			before := lastSeq(t, home)
			r := ok(t, home, append([]string{"update", ref}, args...)...)
			if fmt.Sprint(r.Result()) != "map[updated:[]]" {
				t.Errorf("%s %q: %s", ref, args, r.Line())
			}
			if lastSeq(t, home) != before {
				t.Errorf("%s %q recorded an event", ref, args)
			}
		}
	}
	if field(t, home, "1", "updated_at") != fixed {
		t.Error("updated_at moved")
	}
}

// An extra given at creation is the patch applied to {}: a null stores
// nothing, at any depth but inside an array, which is replaced whole.
func TestExtraGivenIsPatch(t *testing.T) {
	const given, want = `{"a":null,"b":{"c":null,"d":1},"e":[null]}`, `{"b":{"d":1},"e":[null]}`
	home := root(t)
	ok(t, home, "task", "create", "t", "--extra", given) // 1
	if got := extraOf(t, home, "1", "task"); got != want {
		t.Errorf("task create: %s", got)
	}
	ok(t, home, "folder", "create", "/w/x", "--extra", given) // 2, 3
	if got := extraOf(t, home, "/w/x", "folder"); got != want {
		t.Errorf("folder create: %s", got)
	}
	if got := extraOf(t, home, "/w", "folder"); got != `{}` {
		t.Errorf("a missing parent: %s", got)
	}
	input(t, home, "batch", `{"folders":[{"path":"/b","extra":`+given+`}],"tasks":[{"title":"u","extra":`+given+`}]}`) // 4, 5
	if got := extraOf(t, home, "/b", "folder"); got != want {
		t.Errorf("batch folder: %s", got)
	}
	if got := extraOf(t, home, "5", "task"); got != want {
		t.Errorf("batch task: %s", got)
	}
	claim := func(ref string) string {
		t.Helper()
		b, err := json.Marshal(field(t, home, ref, "claim").(map[string]any)["extra"])
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ok(t, home, "claim", "1", "--extra", given)
	if got := claim("1"); got != want {
		t.Errorf("claim: %s", got)
	}
	ok(t, home, "task", "create", "v", "--folder", "/w/x", "--priority", "9") // 6
	ok(t, home, "claim-next", "/w/x", "--extra", given)
	if got := claim("6"); got != want {
		t.Errorf("claim-next: %s", got)
	}
}

// A bad or reserved name is refused on every path that writes extra, at
// its pointer, a null's included; nothing is written. Below the top level
// names are part of the value.
func TestExtraNames(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "t", "--extra", `{"a":{"Any.Name/here":1}}`) // 1
	ok(t, home, "folder", "create", "/w")                                      // 2

	golden(t, fails(t, home, "invalid-input", "", "update", "1", "--extra-merge", `{"Remind":{}}`), "update-extra-merge-bad")
	golden(t, fails(t, home, "invalid-input", "", "update", "1", "--extra-merge", `{"koan":{}}`), "update-extra-merge-reserved")
	golden(t, fails(t, home, "invalid-input", "", "update", "1", "--extra-remove", "ok,Bad,koan"), "update-extra-remove-bad")
	golden(t, fails(t, home, "invalid-input", "", "task", "create", "x", "--extra", `{"koan":1,"a.b":1}`), "task-create-extra-bad")
	golden(t, fails(t, home, "invalid-input", "", "folder", "create", "/x", "--extra", `{"Bad":1}`), "folder-create-extra-bad")
	golden(t, fails(t, home, "invalid-input", "", "claim", "1", "--extra", `{"By":"me"}`), "claim-extra-bad")
	golden(t, fails(t, home, "invalid-input", "", "claim-next", "--extra", `{"koan":"me"}`), "claim-next-extra-reserved")
	golden(t, input(t, home, "batch", `{"folders":[{"path":"/y","extra":{"Bad":1}}],"tasks":[{"title":"y","extra":{"koan":1}}]}`), "batch-extra-bad")

	for _, c := range []struct {
		args   []string
		fields string
	}{
		{[]string{"update", "1", "--extra-merge", `{"z-":1,"Bad":null,"koan":null,"":1}`}, "/extra_merge/ /extra_merge/Bad /extra_merge/koan /extra_merge/z-"},
		{[]string{"update", "/w", "--extra-merge", `{"a/b":1,"a~b":1}`}, "/extra_merge/a~1b /extra_merge/a~0b"},
		{[]string{"update", "/w", "--extra-merge", `{"a,b":1}`}, "/extra_merge/a,b"},
		{[]string{"update", "1", "--extra-merge", `{"` + strings.Repeat("a", 65) + `":1}`}, "/extra_merge/" + strings.Repeat("a", 65)},
		{[]string{"update", "1", "--extra-remove", "koan"}, "/extra_remove/0"},
		// Bad in both is one fault at each, not a contradiction besides.
		{[]string{"update", "1", "--extra-merge", `{"Bad":1}`, "--extra-remove", "Bad"}, "/extra_merge/Bad /extra_remove/0"},
		{[]string{"update", "1", "--extra-merge", `{"k":null}`, "--extra-remove", "k"}, "/extra_remove/0"},
		{[]string{"task", "create", "x", "--extra", `{"A":null}`}, "/extra/A"},
		{[]string{"folder", "create", "/x", "--extra", `{"koan":null}`}, "/extra/koan"},
		{[]string{"claim", "1", "--extra", `{"A":null}`}, "/extra/A"},
		{[]string{"claim-next", "--extra", `{"A":null}`}, "/extra/A"},
	} {
		if r := fails(t, home, "invalid-input", "", c.args...); problemFields(r) != c.fields {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	for in, fields := range map[string]string{
		`{"refs":[1],"extra_merge":{"koan":1}}`: "/extra_merge/koan",
		`{"refs":[1],"extra_remove":["Bad"]}`:   "/extra_remove/0",
		`{"refs":[1],"extra_remove":[1]}`:       "/extra_remove/0",
		`{"refs":[1],"extra_replace_all":{}}`:   "/extra_replace_all",
	} {
		if r := input(t, home, "update", in); r.Kind() != "invalid-input" || problemFields(r) != fields {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	if got := fmt.Sprint(field(t, home, "1", "claim")); got != "<nil>" {
		t.Errorf("claimed: %s", got)
	}
	if got := extraOf(t, home, "1", "task"); got != `{"a":{"Any.Name/here":1}}` {
		t.Errorf("written: %s", got)
	}
	// The option is gone.
	if r := koan(t, home, "update", "1", "--extra-replace-all", "{}"); r.Kind() != "usage" {
		t.Errorf("--extra-replace-all: %s", r.Line())
	}
	// koan reserves itself, not what starts with it.
	ok(t, home, "update", "1", "--extra-merge", `{"koan-x":1,"koans":1}`)
}

// doctor reports a stored name that breaks the rule, or a null at the top
// level, as invalid-row under I9: on a folder, a task or a claim. Update
// leaves such a name where it is, and can't name it.
func TestDoctorExtra(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")                                  // 1
	ok(t, home, "task", "create", "a")                                     // 2
	ok(t, home, "task", "create", "b")                                     // 3
	ok(t, home, "task", "create", "c")                                     // 4
	ok(t, home, "task", "create", "fine", "--extra", `{"a":{"B.c":null}}`) // 5
	ok(t, home, "claim", "4")
	edit(t, home,
		`UPDATE folder SET extra = '{"Bad":1}' WHERE id = 1`,
		`UPDATE task SET extra = '{"koan":1,"ok":2,"a.b":3}' WHERE id = 2`,
		`UPDATE task SET extra = '{"gone":null}' WHERE id = 3`,
		`UPDATE claim SET extra = '{"By":"me"}' WHERE task = 4`)
	want := []string{"invalid-row I9 [1] false", "invalid-row I9 [2] false", "invalid-row I9 [3] false", "invalid-row I9 [4] false"}
	r := ok(t, home, "doctor")
	golden(t, r, "doctor-extra")
	sameList(t, "doctor", summary(r.Result()["findings"]), want)
	// repair leaves them for a person.
	r = repaired(t, home, nil)
	sameList(t, "left", summary(r.Result()["left"]), want)

	ok(t, home, "update", "2", "--extra-merge", `{"ok":3,"new":1}`)
	// A read prints the row as it is, which its schema can't take.
	if line := (testrun.Run{Home: home}).Koan(t, "show", "2").Line(); !strings.Contains(line, `"extra":{"a.b":3,"koan":1,"new":1,"ok":3}`) {
		t.Errorf("after a merge: %s", line)
	}
	for _, args := range [][]string{{"--extra-remove", "koan"}, {"--extra-merge", `{"a.b":null}`}} {
		fails(t, home, "invalid-input", "", append([]string{"update", "2"}, args...)...)
	}
	if !slices.Contains(summary(ok(t, home, "doctor").Result()["findings"]), "invalid-row I9 [2] false") {
		t.Error("still broken, doctor should say so")
	}
}
