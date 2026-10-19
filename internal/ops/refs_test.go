package ops

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
)

// A task is 42, "42", or "@42" wherever an ID is taken.
func TestTaskIDsTakeTheirAt(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`) // 1
	w.ok("task create", `{"title":"b"}`) // 2
	for _, id := range []string{`1`, `"1"`, `"@1"`} {
		if tk := task(w.ok("task show", `{"id":`+id+`}`)); tk["id"] != float64(1) {
			t.Errorf("show %s: %v", id, tk)
		}
	}
	w.ok("task block", `{"id":"@2","blocked_by":["@1"]}`)
	wantIDs(t, "blocked_by", task(w.ok("task show", `{"id":2}`))["blocked_by"], 1)
	w.ok("task create", `{"title":"c","blocked_by":["2","@1"]}`) // 3
	wantIDs(t, "blocked_by", task(w.ok("task show", `{"id":3}`))["blocked_by"], 1, 2)
	// One ID twice, in two forms, is repeated.
	w.fail("task block", `{"id":2,"blocked_by":[1,"@1"]}`, errs.InvalidInput, "")
	for _, bad := range []string{`"@"`, `"@0"`, `"@x"`, `"@@1"`, `" 1"`, `"01"`, `"+1"`} {
		w.fail("task show", `{"id":`+bad+`}`, errs.InvalidInput, "")
	}
	// In batch too, every form is an ID, and a ref, which has a letter,
	// is never one.
	got := w.ok("batch", `{"tasks":[{"ref":"d1","title":"d"},{"title":"e","blocked_by":["d1","2","@1"]}]}`)
	w.fail("batch", `{"tasks":[{"title":"e","blocked_by":["@1",1]}]}`, errs.InvalidInput, "")
	w.fail("batch", `{"tasks":[{"title":"e","blocked_by":["1",1]}]}`, errs.InvalidInput, "")
	w.fail("batch", `{"tasks":[{"ref":"1","title":"d"}]}`, errs.InvalidInput, "")
	w.fail("batch", `{"tasks":[{"ref":"1-2","title":"d"}]}`, errs.InvalidInput, "")
	e := task(w.ok("task show", `{"id":5}`))
	wantIDs(t, "batch blocked_by", e["blocked_by"], 1, 2, 4)
	wantIDs(t, "batch ids", got["ids"], 4, 5)
}

// A folder is a path, or its ID in any form, wherever it must exist.
func TestFolderIDs(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q"}`) // 1, 2
	for _, ref := range []string{`2`, `"2"`, `"@2"`} {
		if r := w.ok("folder show", `{"folder":`+ref+`}`); r["folder"].(map[string]any)["path"] != "/p/q" {
			t.Errorf("folder show %s: %v", ref, r)
		}
	}
	w.ok("task create", `{"title":"a","folder":"@2"}`) // 3
	if tk := task(w.ok("task show", `{"id":3}`)); tk["folder"] != "/p/q" {
		t.Errorf("create in @2: %v", tk)
	}
	if got := w.ok("batch", `{"folder":1,"tasks":[{"title":"b"},{"title":"c","folder":"@2"}]}`); len(got["folders_created"].([]any)) != 0 {
		t.Errorf("batch made folders: %v", got)
	}
	if f := task(w.ok("task show", `{"id":4}`))["folder"]; f != "/p" {
		t.Errorf("task 4 is in %v", f)
	}
	wantIDs(t, "list @2", w.ok("task list", `{"folder":"@2"}`)["tasks"], 3, 5)
	wantIDs(t, "frontier 1", w.ok("task frontier", `{"folder":"1","recursive":false}`)["tasks"], 4)
	if c := task(w.ok("task claim-next", `{"folder":"@2"}`)); c["id"] != float64(3) {
		t.Errorf("claim-next: %v", c)
	}
	w.ok("folder update", `{"folder":"@2","title":"Q"}`)
	if r := w.record(w.ok("folder show", `{"folder":"/p/q"}`)); !strings.Contains(r, `"title":"Q"`) {
		t.Errorf("folder update by ID: %s", r)
	}
	w.ok("task move", `{"id":4,"to":"@2"}`)
	if tk := task(w.ok("task show", `{"id":4}`)); tk["folder"] != "/p/q" {
		t.Errorf("move to @2: %v", tk)
	}

	// An ID is never made as needed: one no folder has is not-found, and a
	// task's says so.
	before := w.ok("info", `{}`)
	if e := w.fail("task create", `{"title":"x","folder":"@99"}`, errs.NotFound, ""); len(e.IDs) != 1 || e.IDs[0] != 99 {
		t.Errorf("ids %v", e.IDs)
	}
	if e := w.fail("task move", `{"id":4,"to":"@3"}`, errs.NotFound, ""); !strings.Contains(e.Message, "task") {
		t.Errorf("message %q", e.Message)
	}
	if after := w.ok("info", `{}`); after["last_id"] != before["last_id"] {
		t.Errorf("a refused create used an ID: %v, then %v", before["last_id"], after["last_id"])
	}
	// Paths only where the folder is new.
	w.fail("folder create", `{"folder":"@2"}`, errs.InvalidInput, "")
	w.fail("folder move", `{"folder":"/p","to":"@2"}`, errs.InvalidInput, "")
	w.fail("folder show", `{"folder":"p"}`, errs.InvalidInput, "")

	// A folder given by ID can't go under itself, which only the tree tells.
	w.fail("folder move", `{"folder":1,"to":"/p/q/r"}`, errs.InvalidInput, "")
	got := w.ok("folder move", `{"folder":"@2","to":"/s"}`)
	if got["path"] != "/s" || got["id"] != float64(2) {
		t.Errorf("folder move by ID: %v", got)
	}
	wantIDs(t, "deleted", w.ok("folder delete", `{"folder":"@2","contents":true}`)["deleted"], 3, 4, 5)
	w.fail("folder show", `{"folder":"@2"}`, errs.NotFound, "")
}

// Finding a folder by ID reads every folder file: a copy of the ID is
// duplicate-id, and a folder file koan can't use is a warning.
func TestFolderIDsReadEveryFolderFile(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`) // 1
	w.ok("folder create", `{"folder":"/q"}`) // 2
	w.ok("folder create", `{"folder":"/r"}`) // 3
	os.WriteFile(w.root("r/folder.json"), []byte("{"), 0o644)
	env := w.run("folder show", `{"folder":"@1"}`)
	if !env.OK || len(env.Warnings) != 1 || env.Warnings[0].Kind != errs.WarnUnusableFile {
		t.Errorf("folder show @1: %+v", env)
	}
	// By path too: every command that reads a folder reads every folder
	// file, once.
	if env := w.run("folder show", `{"folder":"/p"}`); len(env.Warnings) != 1 {
		t.Errorf("folder show /p warnings: %+v", env.Warnings)
	}
	data, _ := os.ReadFile(w.root("p/folder.json"))
	os.WriteFile(w.root("q/folder.json"), data, 0o644)
	if e := w.fail("folder show", `{"folder":"@1"}`, errs.Conflict, errs.RuleDuplicateID); len(e.Paths) != 2 {
		t.Errorf("paths %v", e.Paths)
	}
	w.ok("task create", `{"title":"a"}`) // 4
	os.WriteFile(w.root("q/folder.json"), []byte(strings.Replace(string(data), `"id": 1`, `"id": 4`, 1)), 0o644)
	w.fail("task list", `{"folder":"@4"}`, errs.Conflict, errs.RuleDuplicateID)
	w.fail("where", `{"ref":"4"}`, errs.Conflict, errs.RuleDuplicateID)
}

// where gives a reference's disk paths, and a disk path's reference.
func TestWhere(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q","title":"Q"}`)          // 1, 2
	w.ok("task create", `{"title":"Book flights","folder":"/p/q"}`) // 3
	os.Mkdir(w.root("hand"), 0o755)
	cut := func(s string) string { return strings.ReplaceAll(s, w.disk(), "~") }
	str := func(v any) string {
		if v == nil {
			return "null"
		}
		return cut(v.(string))
	}
	cases := []struct{ in, ref, hint, folder, task, material, file string }{
		{`{"ref":"@2"}`, "@2", "/p/q", "/p/q", "null", "null", "null"},
		{`{"ref":"2"}`, "@2", "/p/q", "/p/q", "null", "null", "null"},
		{`{"ref":"/p/q"}`, "@2", "/p/q", "/p/q", "null", "null", "null"},
		{`{"ref":"/"}`, "/", "/", "/", "null", "null", "null"},
		{`{"ref":"/hand"}`, "/hand", "/hand", "/hand", "null", "null", "null"},
		{`{"ref":"@3"}`, "@3", "Book flights", "/p/q", "3", "null", "~/@p/@q/@3.md"},
		{`{"ref":"3"}`, "@3", "Book flights", "/p/q", "3", "null", "~/@p/@q/@3.md"},
		{`{"ref":"@2:docs/plan.md"}`, "@2:docs/plan.md", "/p/q:docs/plan.md", "/p/q", "null", "docs/plan.md", "~/@p/@q/docs/plan.md"},
		{`{"ref":"2:docs/plan.md"}`, "@2:docs/plan.md", "/p/q:docs/plan.md", "/p/q", "null", "docs/plan.md", "~/@p/@q/docs/plan.md"},
		{`{"ref":"/p/q:a:b"}`, "@2:a:b", "/p/q:a:b", "/p/q", "null", "a:b", "~/@p/@q/a:b"},
		{`{"ref":"@1:node_modules/@types/x"}`, "@1:node_modules/@types/x", "/p:node_modules/@types/x", "/p", "null", "node_modules/@types/x", "~/@p/node_modules/@types/x"},
		{`{"ref":"/:notes.md"}`, "/:notes.md", "/:notes.md", "/", "null", "notes.md", "~/notes.md"},
		{`{"ref":"/hand:x"}`, "/hand:x", "/hand:x", "/hand", "null", "x", "~/@hand/x"},
		{`{"file":"` + w.disk() + `"}`, "/", "/", "/", "null", "null", "null"},
		{`{"file":"` + w.root("p/q") + `"}`, "@2", "/p/q", "/p/q", "null", "null", "null"},
		{`{"file":"` + w.root("p/q/folder.md") + `"}`, "@2", "/p/q", "/p/q", "null", "null", "null"},
		{`{"file":"` + w.root("p/q/3.json") + `"}`, "@3", "Book flights", "/p/q", "3", "null", "~/@p/@q/@3.md"},
		{`{"file":"` + w.root("p/q") + `/docs/../docs/plan.md"}`, "@2:docs/plan.md", "/p/q:docs/plan.md", "/p/q", "null", "docs/plan.md", "~/@p/@q/docs/plan.md"},
		{`{"file":"` + w.root("p") + `/docs/@q/x"}`, "@1:docs/@q/x", "/p:docs/@q/x", "/p", "null", "docs/@q/x", "~/@p/docs/@q/x"},
	}
	for _, c := range cases {
		got := w.ok("where", c.in)
		task := "null"
		if got["task"] != nil {
			task = fmt.Sprint(got["task"])
		}
		g := []string{str(got["ref"]), str(got["hint"]), got["folder"].(map[string]any)["path"].(string), task, str(got["material"]), str(got["file"])}
		want := []string{c.ref, c.hint, c.folder, c.task, c.material, c.file}
		if strings.Join(g, " | ") != strings.Join(want, " | ") {
			t.Errorf("where %s:\n got %v\nwant %v", cut(c.in), g, want)
		}
	}

	// The link's own path and where it leads both count as in the tree.
	real := t.TempDir()
	os.Rename(w.disk(), real+"/tree")
	os.Symlink(real+"/tree", w.disk())
	if got := w.ok("where", `{"file":"`+real+`/tree/@p/x"}`); got["ref"] != "@1:x" {
		t.Errorf("through the link: %v", got)
	}
	if got := w.ok("where", `{"file":"`+w.disk()+`/@p/x"}`); got["ref"] != "@1:x" {
		t.Errorf("by the link: %v", got)
	}

	for _, in := range []string{`{}`, `{"ref":"@2","file":"/x"}`, `{"ref":""}`, `{"ref":"p"}`, `{"ref":"@2:"}`, `{"ref":"@2:/x"}`,
		`{"ref":"@2:@3.md"}`, `{"ref":"@2:a//b"}`, `{"ref":"@2:a/../b"}`, `{"ref":"@2:./a"}`, `{"ref":"/p/:x"}`, `{"file":"rel/x"}`} {
		w.fail("where", in, errs.InvalidInput, "")
	}
	for _, in := range []string{`{"ref":"@9"}`, `{"ref":"@3:x"}`, `{"ref":"/nope:x"}`,
		`{"file":"/elsewhere/x"}`, `{"file":"` + w.disk() + `-not/x"}`, `{"file":"` + w.root("nope") + `/x"}`,
		`{"file":"` + w.root("p/q/9.json") + `"}`, `{"file":"` + w.root("p/3.md") + `"}`, `{"file":"` + w.root("p/stray") + `"}`,
		`{"file":"` + w.root("koan.json") + `"}`, `{"file":"` + w.root("p/q/3.json") + `/x"}`} {
		w.fail("where", in, errs.NotFound, "")
	}
	if e := w.fail("where", `{"ref":"@3:x"}`, errs.NotFound, ""); !strings.Contains(e.Message, "task") {
		t.Errorf("message %q", e.Message)
	}
	os.WriteFile(w.root("p/folder.json"), []byte("{"), 0o644)
	w.fail("where", `{"ref":"/p"}`, errs.Corrupt, "")
}

// A task's ID where a folder is wanted is not-found, in every command that
// takes a folder by ID; and a folder's ID where a task is wanted.
func TestIDsOfTheOtherKind(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`) // 1
	w.ok("task create", `{"title":"a"}`)     // 2
	for _, c := range [][2]string{
		{"folder update", `{"folder":"@2","title":"x"}`},
		{"folder delete", `{"folder":"2"}`},
		{"folder move", `{"folder":2,"to":"/q"}`},
		{"folder show", `{"folder":2}`},
	} {
		if e := w.fail(c[0], c[1], errs.NotFound, ""); !strings.Contains(e.Message, "task") {
			t.Errorf("%s: message %q", c[0], e.Message)
		}
	}
	if e := w.fail("task delete", `{"id":1}`, errs.NotFound, ""); !strings.Contains(e.Message, "folder /p") {
		t.Errorf("delete: message %q", e.Message)
	}
	// A blocker may be either kind.
	w.ok("batch", `{"tasks":[{"title":"b","blocked_by":["@1"]}]}`)
	w.fail("batch", `{"tasks":[{"title":"b","blocked_by":["@9"]}]}`, errs.NotFound, "")
	if e := w.fail("batch", `{"tasks":[{"title":"b","blocked_by":[null]}]}`, errs.InvalidInput, ""); !strings.Contains(e.Message, "or the ref of an earlier task") {
		t.Errorf("null blocker: message %q", e.Message)
	}

	// A folder file that repeats a task's ID: a task found by ID is not
	// told, a folder found by ID, or where, is.
	data, _ := os.ReadFile(w.root("p/folder.json"))
	os.WriteFile(w.root("p/folder.json"), []byte(strings.Replace(string(data), `"id": 1`, `"id": 2`, 1)), 0o644)
	w.ok("task show", `{"id":2}`)
	w.fail("folder update", `{"folder":2,"title":"x"}`, errs.Conflict, errs.RuleDuplicateID)
	w.fail("folder delete", `{"folder":2}`, errs.Conflict, errs.RuleDuplicateID)
	w.fail("where", `{"ref":"2"}`, errs.Conflict, errs.RuleDuplicateID)
}

// An ID given to where reads every folder file, and warns once for each one
// koan can't use, however many times the command reads them.
func TestWhereWarnsOfUnusableFolderFiles(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`) // 1
	w.ok("folder create", `{"folder":"/q"}`) // 2
	w.ok("task create", `{"title":"a"}`)     // 3
	os.WriteFile(w.root("q/folder.json"), []byte("{"), 0o644)
	for _, c := range [][2]string{{"where", `{"ref":"3"}`}, {"folder list", `{"folder":"@1"}`}, {"task list", `{"folder":1}`}} {
		env := w.run(c[0], c[1])
		if !env.OK || len(env.Warnings) != 1 || env.Warnings[0].Kind != errs.WarnUnusableFile {
			t.Errorf("%s %s: %+v", c[0], c[1], env)
		}
	}
}

// where --file takes a folder name on anything but a folder for what the
// walk takes it: a stray entry, with no reference.
func TestWhereFileThroughStrayEntries(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`)
	os.WriteFile(w.root("p/stray"), nil, 0o644)
	os.Symlink(t.TempDir(), w.root("p/lnk"))
	for _, p := range []string{w.root("p/stray"), w.root("p/stray") + "/x", w.root("p/lnk") + "/x"} {
		w.fail("where", `{"file":"`+p+`"}`, errs.NotFound, "")
	}
	// A link of the user's own into the tree is followed, to a material
	// path that need not exist.
	os.MkdirAll(w.disk("@p", "docs"), 0o755)
	k := t.TempDir() + "/k"
	os.Symlink(w.disk("@p", "docs"), k)
	if got := w.ok("where", `{"file":"`+k+`/new/x.md"}`); got["ref"] != "@1:docs/new/x.md" {
		t.Errorf("through a link: %v", got)
	}
	w.fail("where", `{"file":"`+t.TempDir()+`/x"}`, errs.NotFound, "")
	// By reference it is a folder path, and not-a-folder, as for folder show.
	w.fail("where", `{"ref":"/p/stray"}`, errs.Conflict, errs.RuleNotAFolder)
	w.fail("where", `{"ref":"@1:a\u0000b"}`, errs.InvalidInput, "")
}
