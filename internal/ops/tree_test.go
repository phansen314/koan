package ops

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/schematest"
)

func TestTreeOrderPutsChildrenBeforeLaterSiblings(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"title":"a","folder":"/proj-b"},{"title":"b","folder":"/proj/travel"},{"title":"c","folder":"/proj"},{"title":"d","folder":"/Zed"}]}`)
	// The four folders take 1 to 4; the tasks, in input order, 5 to 8.
	wantIDs(t, "list", w.ok("list", `{}`)["tasks"], 8, 7, 6, 5)
	folders := paths(w.ok("list", `{"include_folders":true}`)["folders"])
	if folders != `["/","/Zed","/proj","/proj/travel","/proj-b"]` {
		t.Errorf("folders %s", folders)
	}
}

func TestDuplicateIDIsAConflict(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	w.ok("create-folder", `{"folder":"/x"}`)
	data, _ := os.ReadFile(w.root("1.json"))
	os.WriteFile(w.root("x/1.json"), data, 0o644)
	e := w.fail("show", `{"id":1}`, errs.Conflict, errs.RuleDuplicateID)
	if len(e.Paths) != 2 {
		t.Errorf("want both copies, got %v", e.Paths)
	}
	w.fail("list", `{}`, errs.Conflict, errs.RuleDuplicateID)
}

func TestWarnings(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]},{"title":"c"}]}`)
	os.Remove(w.root("1.json"))
	os.WriteFile(w.root("3.json"), []byte("not json"), 0o644)
	env := w.run("list", `{}`)
	kinds := []string{}
	for _, wn := range env.Warnings {
		kinds = append(kinds, wn.Kind)
		schematest.Must(t, "warning", wn)
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{"dangling-reference", "unusable-file"}) {
		t.Errorf("warnings %v", kinds)
	}
	w.fail("show", `{"id":3}`, errs.Corrupt, "")
	wantIDs(t, "stuck", w.ok("why", `{"id":2}`)["stuck"], 1)
	w.ok("delete", `{"id":3}`) // delete goes by filename
}

func TestWarningsOncePerFileWhateverTheNarrowing(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a"},{"ref":"b","title":"b"},{"title":"c","blocked_by":["a","b"],"tags":["x"]}]}`)
	os.Remove(w.root("1.json"))
	os.Remove(w.root("2.json"))
	for _, input := range []string{`{}`, `{"tags_any":["zzz"]}`, `{"limit":1}`, `{"readiness":["done"]}`} {
		env := w.run("list", input)
		if len(env.Warnings) != 1 || len(env.Warnings[0].IDs) != 3 {
			t.Errorf("list %s: warnings %+v, want one naming the task and both blockers", input, env.Warnings)
		}
	}
}

func TestFilesInOtherShapes(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	good := w.file("1.json")
	// A key that differs only in case is not the key.
	os.WriteFile(w.root("1.json"), []byte(strings.Replace(good, `"extra": {}`, `"extra": {}, "TITLE": "x"`, 1)), 0o644)
	w.fail("show", `{"id":1}`, errs.Corrupt, "")
	os.WriteFile(w.root("1.json"), []byte(good), 0o644)

	w.fail("show", `{"id":1}}`, errs.InvalidInput, "")
	w.fail("show", `{"id":1}]`, errs.InvalidInput, "")
	w.fail("show", `{"id":1} {"id":1}`, errs.InvalidInput, "")

	// A @koan.json from a newer koan is that, whatever else it holds.
	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 3, "name": "x"}`), 0o644)
	w.fail("show", `{"id":1}`, errs.UnsupportedFormat, "")
	os.WriteFile(w.root("koan.json"), []byte(`{"schema": 2, "name": "x"}`), 0o644)
	w.fail("show", `{"id":1}`, errs.Corrupt, "")
}

func TestStateFileKeysAreExact(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a"}`)
	w.ok("claim", `{"id":1}`)
	state, _ := os.ReadFile(w.env.StatePath())
	os.WriteFile(w.env.StatePath(), []byte(strings.Replace(string(state), `"claimed_at"`, `"Claimed_At"`, 1)), 0o600)
	w.fail("show", `{"id":1}`, errs.Corrupt, "")
}

// Only what is named with @ is koan's: everything else is material, never
// walked into, whatever its name, @ folders inside it included.
func TestMaterialIsNeverRead(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/p"}`) // /p is 1, the task 2
	for _, dir := range []string{"@p/docs/@inner", "docs", "@p/node_modules/@types"} {
		os.MkdirAll(w.disk(dir), 0o755)
	}
	for _, f := range []string{"@p/docs/@3.json", "@p/docs/@inner/@folder.json", "@p/2026.md", "@p/7.json", "notes.md", ".hidden"} {
		os.WriteFile(w.disk(f), []byte("material"), 0o644)
	}
	if fs := w.findings(); len(fs) != 0 {
		t.Errorf("doctor found %v in material", fs)
	}
	got := w.ok("list", `{"include_folders":true}`)
	if folders := paths(got["folders"]); folders != `["/","/p"]` {
		t.Errorf("folders %s", folders)
	}
	wantIDs(t, "tasks", got["tasks"], 2)
	// A non-recursive delete-folder refuses visible material, and notes.
	w.fail("delete-folder", `{"folder":"/p"}`, errs.Conflict, errs.RuleNotEmpty)
	w.ok("create-folder", `{"folder":"/q"}`)
	os.WriteFile(w.root("q/folder.md"), []byte("notes"), 0o644)
	w.fail("delete-folder", `{"folder":"/q"}`, errs.Conflict, errs.RuleNotEmpty)
	os.WriteFile(w.root("q/folder.md"), nil, 0o644)
	os.WriteFile(w.disk("@q/.gitignore"), nil, 0o644)
	w.ok("delete-folder", `{"folder":"/q"}`)
}
