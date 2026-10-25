package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
	"github.com/phansen314/koan/internal/testrun"
)

var stamp = regexp.MustCompile(`"20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z"`)

// goldenNow is golden with every timestamp a change made now written
// $NOW; the fixed ones the tests insert stay as they are.
func goldenNow(t *testing.T, r *testrun.Result, name string) {
	t.Helper()
	replace := map[string]string{}
	for _, s := range stamp.FindAllString(r.Line(), -1) {
		if s != `"2026-10-20T10:00:00Z"` {
			replace[s] = `"$NOW"`
		}
	}
	r.Golden(t, name, *update, replace)
}

// field is one field of a task's record, as show gives it.
func field(t *testing.T, home, ref, name string) any {
	t.Helper()
	return ok(t, home, "show", ref).Result()["task"].(map[string]any)[name]
}

// task create makes the task in a folder or the root, its fields as
// given, its title trimmed, its tags sorted, extra's keys in byte order,
// and its @<id>.md holding its notes; made and updated now, open and
// ready, with no edges and no claim. IDs follow the folders'.
func TestTaskCreate(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")
	r := ok(t, home, "task", "create", "  Book flights  ", "--folder", "/work", "--priority", "-3",
		"--tags", "urgent,a-1", "--tags", "home", "--extra", `{"z":"<x>","a":[1,2.50]}`, "--notes", "# Plan\n")
	golden(t, r, "task-create")
	r = ok(t, home, "show", "2")
	goldenNow(t, r, "show")
	rec := r.Result()["task"].(map[string]any)
	if rec["created_at"] != rec["updated_at"] {
		t.Errorf("created and updated differ: %s", r.Line())
	}

	// In the root by default, every field at its default.
	r = ok(t, home, "task", "create", "--", "-dash")
	if r.Result()["ref"] != "@3 (/)" || r.Result()["notes_path"] != filepath.Join(home, "koan-root", "tree", "@3.md") {
		t.Errorf("root: %s", r.Line())
	}
	rec = ok(t, home, "show", "@3").Result()["task"].(map[string]any)
	if rec["title"] != "-dash" || rec["priority"] != nil || fmt.Sprint(rec["tags"]) != "[]" || fmt.Sprint(rec["extra"]) != "map[]" ||
		fmt.Sprint(rec["folder"]) != "map[id:<nil> path:/]" {
		t.Errorf("defaults: %v", rec)
	}
	// The word null is a title; --priority null is no priority.
	ok(t, home, "task", "create", "null", "--priority", "null", "--folder", "@1")
	if field(t, home, "4", "title") != "null" || field(t, home, "4", "priority") != nil {
		t.Errorf("null")
	}
	// Through --input, as the options would give it, with the folder as
	// an ID and the largest priority.
	r = testrun.Run{Home: home, Stdin: strings.NewReader(
		`{"title":"T","folder":1,"priority":9007199254740991,"tags":[],"blocked_by":[],"blocks":[],"extra":{},"notes":"n"}`)}.Koan(t, "task", "create", "-i", "-")
	r.Check(t, "task create")
	if !r.OK() {
		t.Errorf("--input: %s", r.Line())
	}
	checkTree(t, home, "@3.md=", "@work/", "@work/@notes.md=", "@work/@2.md=# Plan\n", "@work/@4.md=", "@work/@5.md=n")
}

// --notes-file gives notes from a file whole, or from standard input; a
// file koan can't read is io with its absolute path; both --notes and
// --notes-file is usage; notes that aren't UTF-8 are invalid-input.
func TestTaskCreateNotesFile(t *testing.T) {
	home := root(t)
	os.WriteFile(filepath.Join(home, "n.md"), []byte("from a file\n"), 0o644)
	ok(t, home, "task", "create", "a", "--notes-file", "n.md")
	r := testrun.Run{Home: home, Stdin: strings.NewReader("from stdin")}.Koan(t, "task", "create", "b", "--notes-file", "-")
	r.Check(t, "task create")
	checkTree(t, home, "@1.md=from a file\n", "@2.md=from stdin")

	r = fails(t, home, "io", "", "task", "create", "c", "--notes-file", "missing.md")
	if paths := r.Error()["paths"].([]any); len(paths) != 1 || paths[0] != filepath.Join(home, "missing.md") {
		t.Errorf("io paths: %s", r.Line())
	}
	// The file is read as the input is made, before its shape is checked.
	fails(t, home, "io", "", "task", "create", " ", "--tags", "Bad", "--notes-file", "missing.md")
	r = koan(t, home, "task", "create", "c", "--notes-file", "n.md", "--notes", "x")
	if r.Exit != 2 || r.Kind() != "usage" || r.Error()["problems"].([]any)[0].(map[string]any)["field"] != "" {
		t.Errorf("both: %s", r.Line())
	}
	os.WriteFile(filepath.Join(home, "bad.md"), []byte("caf\xe9"), 0o644)
	golden(t, fails(t, home, "invalid-input", "", "task", "create", "c", "--notes-file", "bad.md"), "task-create-not-utf8")
	checkTree(t, home, "@1.md=from a file\n", "@2.md=from stdin")
}

// A bad input is invalid-input, naming every fault, before the root's
// state is looked at.
func TestTaskCreateInvalid(t *testing.T) {
	home := t.TempDir() // no root: the shape is checked first
	r := fails(t, home, "invalid-input", "", "task", "create", " \t ", "--folder", "/work.done", "--priority", "9007199254740992",
		"--tags", "ok,Bad,ok", "--blocked-by", "/a:docs", "--blocks", "/", "--extra", "[1]")
	golden(t, r, "task-create-invalid")
	for _, c := range []struct {
		args   []string
		fields string
	}{
		{[]string{strings.Repeat("t", 201)}, "/title"},
		{[]string{"a\nb"}, "/title"},
		{[]string{"a b"}, "/title"},
		{[]string{"t", "--priority", "9007199254740992"}, "/priority"},
		{[]string{"t", "--priority", "99999999999999999999"}, "/priority"},
		{[]string{"t", "--tags", "a,b,a"}, "/tags/2"},
		{[]string{"t", "--tags", ""}, "/tags/0"},
		{[]string{"t", "--tags", "-a"}, "/tags/0"},
		{[]string{"t", "--folder", "@3:docs"}, "/folder"},
		{[]string{"t", "--folder", "work"}, "/folder"},
		{[]string{"t", "--blocked-by", "@1,$a"}, "/blocked_by/1"},
		{[]string{"t", "--blocks", "@1:x"}, "/blocks/0"},
		{[]string{"t", "--extra", "{"}, "/extra"},
		{[]string{"t", "--notes", "caf\xe9"}, "/notes"},
		// Two spellings of one path in one input point at both.
		{[]string{"t", "--folder", "/Work/a", "--blocked-by", "@4,/work/b"}, "/folder /blocked_by/1"},
		{[]string{"t", "--blocked-by", "/p/a.start", "--blocks", "/P"}, "/blocked_by/0 /blocks/0"},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"task", "create"}, c.args...)...)
		var got []string
		for _, p := range r.Error()["problems"].([]any) {
			got = append(got, p.(map[string]any)["field"].(string))
		}
		if strings.Join(got, " ") != c.fields {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	for in, field := range map[string]string{
		`{}`:                            "/title",
		`{"title":4}`:                   "/title",
		`{"title":"t","priority":"1"}`:  "/priority",
		`{"title":"t","priority":1e2}`:  "/priority",
		`{"title":"t","tags":"a"}`:      "/tags",
		`{"title":"t","tags":[1]}`:      "/tags/0",
		`{"title":"t","folder":0}`:      "/folder",
		`{"title":"t","blocks":[true]}`: "/blocks/0",
		`{"title":"t","notes":null}`:    "/notes",
		`{"title":"t","extra":null}`:    "/extra",
	} {
		r := testrun.Run{Home: home, Stdin: strings.NewReader(in)}.Koan(t, "task", "create", "-i", "-")
		r.Check(t, "task create")
		problems, _ := r.Error()["problems"].([]any)
		if r.Kind() != "invalid-input" || len(problems) != 1 || problems[0].(map[string]any)["field"] != field {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	fails(t, home, "not-initialized", "", "task", "create", "t")
	if _, err := os.Lstat(filepath.Join(home, "koan-root")); !os.IsNotExist(err) {
		t.Errorf("made the root: %v", err)
	}
}

// The folder must resolve: a folder that isn't there, or an ID that is a
// task, is not-found; a part matching only ignoring case is
// case-mismatch; a path anchored at a task is invalid-input at the
// folder. Nothing is made, and no ID is issued.
func TestTaskCreateRefused(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/Work/api")
	ok(t, home, "task", "create", "t") // 3
	golden(t, fails(t, home, "not-found", "", "task", "create", "x", "--folder", "/Work/web"), "task-create-not-found")
	fails(t, home, "not-found", "", "task", "create", "x", "--folder", "@3")
	fails(t, home, "not-found", "", "task", "create", "x", "--folder", "@99")
	golden(t, fails(t, home, "conflict", "case-mismatch", "task", "create", "x", "--folder", "/work/api"), "task-create-case-mismatch")
	r := fails(t, home, "invalid-input", "", "task", "create", "x", "--folder", "@3/a")
	if fmt.Sprint(r.Error()["problems"]) != fmt.Sprint([]any{map[string]any{"field": "/folder", "reason": r.Error()["message"]}}) {
		t.Errorf("anchored at a task: %s", r.Line())
	}
	checkTree(t, home, "@3.md=", "@Work/", "@Work/@notes.md=", "@Work/@api/", "@Work/@api/@notes.md=")
	if r := ok(t, home, "task", "create", "y"); r.Result()["id"] != json.Number("4") {
		t.Errorf("an ID was issued to a refused create: %s", r.Line())
	}
}

// Once COMMIT succeeds the command succeeds: a notes file the operating
// system refuses leaves the task made, with pending-step, and the next
// write writes it.
func TestTaskCreateStepRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := root(t)
	ok(t, home, "folder", "create", "/work")
	dir := filepath.Join(home, "koan-root", "tree", "@work")
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	r := koan(t, home, "task", "create", "t", "--folder", "/work", "--notes", "n")
	if r.Exit != 0 || !r.OK() {
		t.Fatalf("exit %d: %s", r.Exit, r.Line())
	}
	golden(t, r, "task-create-pending-step")
	// The step's own warning says why the notes aren't there.
	goldenNow(t, koan(t, home, "show", "2"), "show-pending-step")
	os.Chmod(dir, 0o755)
	ok(t, home, "task", "create", "u")
	checkTree(t, home, "@work/", "@work/@notes.md=", "@work/@2.md=n", "@3.md=")
	ok(t, home, "show", "2")
}

// show reads a task by ID, or a gate by its folder and end: its
// readiness and edges as they stand, each edge's other end with its
// state, ascending by ID, and its claim.
func TestTaskShow(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	var stmts []string
	stmts = append(stmts, task(3, 1, "start", false)...)
	stmts = append(stmts, task(4, 1, "done", false)...)
	stmts = append(stmts, task(5, 2, "", false)...)
	stmts = append(stmts, task(6, 0, "", true)...)
	stmts = append(stmts, task(7, 2, "", false)...)
	stmts = append(stmts,
		"INSERT INTO edge (task, blocker) VALUES (5, 3), (5, 6), (4, 7), (4, 5), (7, 5)",
		"INSERT INTO tag (task, tag) VALUES (5, 'b'), (5, 'a-z'), (5, 'a')",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (5, '2026-10-20T10:00:00Z', '{"by":"agent"}')`,
		"UPDATE task SET priority = 7, extra = '{\"status\":\"waiting\"}' WHERE id = 5")
	db(t, home, stmts...)
	touch := func(rel string) { os.WriteFile(filepath.Join(home, "koan-root", "tree", rel), nil, 0o644) }
	for _, rel := range []string{"@work/@start.md", "@work/@done.md", "@work/@api/@5.md", "@work/@api/@7.md", "@6.md"} {
		touch(rel)
	}

	r := ok(t, home, "show", "5")
	golden(t, r, "show-edges")
	r = ok(t, home, "show", "/work.done")
	golden(t, r, "show-gate")
	for ref, id := range map[string]string{"@3": "3", "/work.start": "3", "@1.start": "3", "@1/api/..": "", "4": "4", "@6": "6"} {
		r := koan(t, home, "show", ref)
		if id == "" {
			if r.Kind() != "invalid-input" {
				t.Errorf("%s: %s", ref, r.Line())
			}
			continue
		}
		if !r.OK() || fmt.Sprint(r.Result()["task"].(map[string]any)["id"]) != id {
			t.Errorf("%s: %s", ref, r.Line())
		}
	}
	rec := ok(t, home, "show", "6").Result()["task"].(map[string]any)
	if rec["state"] != "complete" || rec["readiness"] != nil || fmt.Sprint(rec["blocks"]) != "[map[id:5 ref:@5 (/work/api) state:open]]" {
		t.Errorf("complete: %v", rec)
	}
	if field(t, home, "7", "readiness") != "blocked" || field(t, home, "3", "readiness") != "ready" {
		t.Errorf("readiness")
	}
	r = testrun.Run{Home: home, Stdin: strings.NewReader(`{"ref":4}`)}.Koan(t, "show", "-i", "-")
	r.Check(t, "show")
	if !r.OK() {
		t.Errorf("-i with an integer: %s", r.Line())
	}

	// A folder's ID names the folder.
	r = koan(t, home, "show", "@1")
	golden(t, r, "show-folder-by-id")
	if r.Result()["task"] != nil || r.Result()["folder"].(map[string]any)["path"] != "/work" {
		t.Errorf("@1: %s", r.Line())
	}
	golden(t, fails(t, home, "not-found", "", "show", "/work/api.start"), "show-no-gate")
	fails(t, home, "not-found", "", "show", "99")
	golden(t, fails(t, home, "conflict", "case-mismatch", "show", "/WORK.start"), "show-case-mismatch")
	for _, ref := range []string{"@5.start", "/work:docs", "@1:docs"} {
		r := fails(t, home, "invalid-input", "", "show", ref)
		if problemFields(r) != "/ref" {
			t.Errorf("%s: %s", ref, r.Line())
		}
	}
}

// A task whose @<id>.md is missing reads as empty notes, and warns; a
// hand-made one spelled in another case is not its notes. A gate's notes
// are its folder's @start.md or @done.md.
func TestTaskShowMissingNotes(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")
	ok(t, home, "task", "create", "t", "--folder", "/work")
	dir := filepath.Join(home, "koan-root", "tree", "@work")
	os.Remove(filepath.Join(dir, "@2.md"))
	os.WriteFile(filepath.Join(dir, "@2.MD"), []byte("by hand"), 0o644)
	r := koan(t, home, "show", "2")
	if r.Exit != 0 || r.Stderr != "koan: 1 warning\n" {
		t.Errorf("exit %d, stderr %q", r.Exit, r.Stderr)
	}
	goldenNow(t, r, "show-missing-notes")

	db(t, home, task(3, 1, "done", false)...)
	r = koan(t, home, "show", "/work.done")
	ws := r.Envelope["warnings"].([]any)
	if len(ws) != 1 || fmt.Sprint(ws[0].(map[string]any)["paths"]) != "["+filepath.Join(dir, "@done.md")+"]" {
		t.Errorf("gate: %s", r.Line())
	}
	// task list never looks at notes.
	ok(t, home, "task", "list")
}

// task list gives the tasks under its subject in tree order: a folder's own
// tasks first, gates included, by ID, then its folders by name compared
// byte for byte, so Zeta before alpha; open ones by default.
func TestTaskList(t *testing.T) {
	home := root(t)
	for _, p := range []string{"/alpha/b", "/Zeta"} { // 1, 2, 3
		ok(t, home, "folder", "create", p)
	}
	for _, c := range [][]string{
		{"b1", "--folder", "/alpha/b", "--tags", "x,y"}, // 4
		{"r1"}, // 5
		{"a1", "--folder", "/alpha", "--tags", "y"}, // 6
		{"z1", "--folder", "/Zeta", "--tags", "x"},  // 7
		{"r2", "--tags", "z"},                       // 8
		{"a2", "--folder", "/alpha"},                // 9
	} {
		ok(t, home, append([]string{"task", "create"}, c...)...)
	}
	var stmts []string
	stmts = append(stmts, task(10, 1, "done", false)...) // a gate of /alpha
	stmts = append(stmts, task(11, 0, "", true)...)      // complete, in the root
	stmts = append(stmts,
		"INSERT INTO edge (task, blocker) VALUES (10, 6), (9, 11)",
		`INSERT INTO claim (task, claimed_at, extra) VALUES (6, '2026-10-20T10:00:00Z', '{}'), (11, '2026-10-20T10:00:00Z', '{}')`)
	db(t, home, stmts...)

	ids := func(r *testrun.Result) string {
		var out []string
		for _, x := range r.Result()["tasks"].([]any) {
			out = append(out, fmt.Sprint(x.(map[string]any)["id"]))
		}
		return strings.Join(out, " ")
	}
	r := ok(t, home, "task", "list", "--fields", "title,folder,state,readiness")
	golden(t, r, "task-list")
	golden(t, ok(t, home, "task", "list", "/alpha", "--fields", "title,folder,state,readiness"), "task-list-subject")
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "5 8 7 6 9 10 4"},
		{[]string{"--state", "open,complete"}, "5 8 11 7 6 9 10 4"},
		{[]string{"--state", "complete"}, "11"},
		{[]string{"--readiness", "blocked"}, "10"},
		{[]string{"--readiness", "ready"}, "5 8 7 6 9 4"},
		{[]string{"--readiness", "blocked", "--state", "complete,open"}, "11 10"},
		{[]string{"/alpha"}, "6 9 10 4"},
		{[]string{"@1", "--recursive=false"}, "6 9 10"},
		{[]string{"/", "--recursive=false"}, "5 8"},
		{[]string{"/alpha/b"}, "4"},
		{[]string{"--tags-any", "x,z"}, "8 7 4"},
		{[]string{"--tags-all", "x,y"}, "4"},
		{[]string{"--tags-any", "y", "--tags-all", "x"}, "4"},
		{[]string{"--tags-all", "y", "--tags-any", "q"}, ""},
		{[]string{"--claimed"}, "6"},
		{[]string{"--claimed", "--state", "complete,open"}, "11 6"},
		{[]string{"--claimed=false"}, "5 8 7 9 10 4"},
		{[]string{"--claimed=null"}, "5 8 7 6 9 10 4"},
	} {
		r := ok(t, home, append([]string{"task", "list"}, c.args...)...)
		if got := ids(r); got != c.want {
			t.Errorf("%q: got %q, want %q", c.args, got, c.want)
		}
		if r.Result()["total"] != json.Number(fmt.Sprint(len(strings.Fields(c.want)))) || r.Result()["truncated"] != false {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}

	r = ok(t, home, "task", "list", "--limit", "2", "--fields", "id")
	golden(t, r, "task-list-truncated")
	r = ok(t, home, "task", "list", "--fields", "notes_path,tags", "--fields", "ref", "/alpha/b")
	golden(t, r, "task-list-fields")
	r = ok(t, home, "task", "list", "/alpha/b")
	if len(r.Result()["tasks"].([]any)[0].(map[string]any)) != len(taskFieldNames) {
		t.Errorf("every field: %s", r.Line())
	}

	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"--state", "done"}, "/state/0"},
		{[]string{"--state", "open,open"}, "/state/1"},
		{[]string{"--readiness", "complete"}, "/readiness/0"},
		{[]string{"--tags-any", "a,a"}, "/tags_any/1"},
		{[]string{"--tags-all", "A"}, "/tags_all/0"},
		{[]string{"--fields", "title,nope"}, "/fields/1"},
		{[]string{"--fields", "id,id"}, "/fields/1"},
		{[]string{"--limit", "0"}, "/limit"},
		{[]string{"/alpha.done"}, "/folder"},
	} {
		r := fails(t, home, "invalid-input", "", append([]string{"task", "list"}, c.args...)...)
		problems := r.Error()["problems"].([]any)
		if len(problems) != 1 || problems[0].(map[string]any)["field"] != c.field {
			t.Errorf("%q: %s", c.args, r.Line())
		}
	}
	for in, field := range map[string]string{`{"state":[]}`: "/state", `{"readiness":[]}`: "/readiness", `{"claimed":"true"}`: "/claimed"} {
		r := testrun.Run{Home: home, Stdin: strings.NewReader(in)}.Koan(t, "task", "list", "-i", "-")
		r.Check(t, "task list")
		problems, _ := r.Error()["problems"].([]any)
		if len(problems) != 1 || problems[0].(map[string]any)["field"] != field {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	fails(t, home, "conflict", "case-mismatch", "task", "list", "/ALPHA")
	fails(t, home, "not-found", "", "task", "list", "/gamma")
	// The folder is the subject, one word: never --folder, never two.
	golden(t, usageAt(t, home, "/Zeta", "task", "list", "/alpha", "/Zeta"), "task-list-extra")
	golden(t, usageAt(t, home, "--folder", "task", "list", "--folder", "/alpha"), "task-list-folder")
	golden(t, usageAt(t, home, "/alpha", "task", "list", "/alpha", "-i", "-"), "task-list-input-positional")
	for _, args := range [][]string{{"frontier"}, {"claim-next"}, {"folder", "list"}} {
		usageAt(t, home, "/Zeta", append(args, "/alpha", "/Zeta")...)
		usageAt(t, home, "--folder", append(args, "--folder", "/alpha")...)
	}
}

var taskFieldNames = []string{"id", "ref", "gate", "title", "priority", "folder", "created_at", "completed_at", "updated_at",
	"state", "readiness", "blocked_by", "blocks", "tags", "extra", "claim", "notes_path"}

// task list gives at most 100 tasks unless --limit says otherwise, with
// total counting every match.
func TestTaskListLimit(t *testing.T) {
	home := root(t)
	var stmts []string
	for id := int64(1); id <= 101; id++ {
		stmts = append(stmts, task(id, 0, "", false)...)
	}
	db(t, home, stmts...)
	r := ok(t, home, "task", "list", "--fields", "id")
	if len(r.Result()["tasks"].([]any)) != 100 || r.Result()["total"] != json.Number("101") || r.Result()["truncated"] != true {
		t.Errorf("default: total %v, truncated %v", r.Result()["total"], r.Result()["truncated"])
	}
	r = ok(t, home, "task", "list", "--fields", "id", "--limit", "null")
	if len(r.Result()["tasks"].([]any)) != 101 || r.Result()["truncated"] != false {
		t.Errorf("--limit null: total %v", r.Result()["total"])
	}
	r = ok(t, home, "task", "list", "--fields", "id", "--limit", "101")
	if r.Result()["truncated"] != false {
		t.Errorf("--limit 101: %v", r.Result()["truncated"])
	}
}

// With a write holding the lock, a notes path a pending step is making is
// pending-filesystem on show and task list; one a list doesn't
// return says nothing; a stuck step's own warning holds back
// missing-notes.
func TestTaskReadsPending(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "a") // 1
	db(t, home, task(2, 0, "", false)...)
	db(t, home, `INSERT INTO pending (seq, change, step) VALUES (1, 1, '{"op":"write","to":"tree/@2.md"}')`)
	lock, e := store.TakeLock(filepath.Join(home, "koan-root"), filepath.Join(home, "koan-root"))
	if e != nil {
		t.Fatal(e.Message)
	}
	r := koan(t, home, "show", "2")
	golden(t, r, "show-pending-filesystem")
	r = koan(t, home, "task", "list", "--fields", "notes_path")
	ws := r.Envelope["warnings"].([]any)
	if len(ws) != 1 || fmt.Sprint(ws[0].(map[string]any)["ids"]) != "[2]" {
		t.Errorf("list: %s", r.Line())
	}
	r = koan(t, home, "task", "list", "--fields", "title")
	lock.Release()
	if len(r.Envelope["warnings"].([]any)) != 0 {
		t.Errorf("no paths: %s", r.Line())
	}
	ok(t, home, "task", "create", "b")
	ok(t, home, "show", "2")

	db(t, home, task(4, 0, "", false)...)
	db(t, home, `INSERT INTO pending (seq, change, step, stuck) VALUES (9, 9, '{"op":"move","from":"tree/@x.md","to":"tree/@4.md","stuck":"both"}', 1)`)
	r = koan(t, home, "show", "4")
	golden(t, r, "show-stuck-step")
	ws = r.Envelope["warnings"].([]any)
	if len(ws) != 1 || ws[0].(map[string]any)["kind"] != "stuck-step" {
		t.Errorf("stuck: %s", r.Line())
	}
}

// The model's task create, run beside koan on the same inputs, agrees on
// the ID each gets and on each refusal, and task list's tree order is the
// model's.
func TestTaskCreateModel(t *testing.T) {
	home := root(t)
	m := model.New(1, func() string { return "2026-10-20T10:00:00Z" })
	for _, p := range []string{"/a/b", "/Zeta", "/a/C"} {
		if _, err := m.CreateFolder(p, nil, nil); err != nil {
			t.Fatal(err)
		}
		ok(t, home, "folder", "create", p)
	}
	for _, c := range []struct {
		title, folder string
		tags          []string
	}{
		{"t", "", nil}, {"t", "/a", []string{"x"}}, {"t", "/a/b", nil}, {" ", "/a", nil}, {"t", "/A", nil},
		{"t", "/a/c", nil}, {"t", "@5", nil}, {"t", "@99", nil}, {"t", "/Zeta", []string{"b", "a"}},
		{"t", "@1/C", nil}, {"t", "/a.start", nil}, {"t", "/", []string{"a", "a"}}, {"t", "@6/x", nil}, {"t", "/a/b", nil},
	} {
		created, err := m.CreateTask(model.TaskInput{Title: c.title, Folder: c.folder, Tags: c.tags})
		args := []string{"task", "create", c.title}
		if c.folder != "" {
			args = append(args, "--folder", c.folder)
		}
		if c.tags != nil {
			args = append(args, "--tags", strings.Join(c.tags, ","))
		}
		r := koan(t, home, args...)
		if err != nil {
			e := err.(*core.Error)
			got := r.Error()
			rule, _ := got["rule"].(string)
			var problems []string
			for _, p := range got["problems"].([]any) {
				problems = append(problems, p.(map[string]any)["field"].(string))
			}
			if r.Kind() != e.Kind || rule != e.Rule || fmt.Sprint(got["ids"]) != fmt.Sprint(nonNil(e.IDs)) ||
				fmt.Sprint(got["paths"]) != fmt.Sprint(nonNilS(e.Paths)) || fmt.Sprint(problems) != fmt.Sprint(e.Problems) {
				t.Errorf("%v: model %v %v, koan %s", c, e, e.Problems, r.Line())
			}
			continue
		}
		if r.Result()["id"] != json.Number(fmt.Sprint(created.ID)) {
			t.Errorf("%v: model %d, koan %s", c, created.ID, r.Line())
		}
		rec := ok(t, home, "show", fmt.Sprint(created.ID)).Result()["task"].(map[string]any)
		x := m.Tasks[created.ID]
		var tags []string
		for tag := range x.Tags {
			tags = append(tags, tag)
		}
		slices.Sort(tags)
		if rec["title"] != x.Title || rec["folder"].(map[string]any)["path"] != m.Path(x.Folder) || fmt.Sprint(rec["tags"]) != fmt.Sprint(nonNilS(tags)) {
			t.Errorf("%v: model %+v, koan %v", c, x, rec)
		}
	}
	var want []string
	for _, id := range m.TreeOrder() {
		if _, isTask := m.Tasks[id]; isTask {
			want = append(want, fmt.Sprint(id))
		}
	}
	var got []string
	for _, x := range ok(t, home, "task", "list", "--fields", "id").Result()["tasks"].([]any) {
		got = append(got, fmt.Sprint(x.(map[string]any)["id"]))
	}
	if !slices.Equal(got, want) {
		t.Errorf("tree order: koan %v, model %v", got, want)
	}
}

// Two steps left pending, by a write that crashed after COMMIT, are
// counted in the plural.
func TestTaskShowPendingSteps(t *testing.T) {
	home := root(t)
	db(t, home, task(1, 0, "", false)...)
	db(t, home, task(2, 0, "", false)...)
	db(t, home,
		`INSERT INTO pending (seq, change, step) VALUES (1, 1, '{"op":"write","to":"tree/@1.md"}')`,
		`INSERT INTO pending (seq, change, step) VALUES (2, 1, '{"op":"write","to":"tree/@2.md"}')`)
	golden(t, koan(t, home, "show", "1"), "show-pending-steps")
}

// An ID that names nothing is not-found, whatever command resolves it,
// and the message says why: it was deleted, it was never issued, or it
// is below the root's first ID and came from an earlier root.
func TestNotFoundID(t *testing.T) {
	home := t.TempDir()
	if r := (testrun.Run{Home: home}).Koan(t, "init", "--ids-from", "100"); r.Exit != 0 {
		t.Fatalf("init: %s", r.Line())
	}
	ok(t, home, "folder", "create", "/w") // 100
	ok(t, home, "task", "create", "t")    // 101
	ok(t, home, "task", "create", "u")    // 102
	ok(t, home, "task", "delete", "102")
	for _, c := range []struct {
		args    []string
		message string
	}{
		{[]string{"show", "102"}, "@102 was deleted"},
		{[]string{"show", "103"}, "@103 was never issued"},
		{[]string{"show", "99"}, "@99 is below this root's first ID, 100, so came from an earlier root"},
		{[]string{"show", "@5"}, "@5 is below this root's first ID, 100, so came from an earlier root"},
		{[]string{"show", "@200"}, "@200 was never issued"},
		{[]string{"task", "create", "x", "--folder", "@102"}, "@102 was deleted"},
		{[]string{"block", "101", "7"}, "@7 is below this root's first ID, 100, so came from an earlier root"},
		{[]string{"task", "update", "150", "--title", "x"}, "@150 was never issued"},
	} {
		r := fails(t, home, "not-found", "", c.args...)
		if r.Error()["message"] != c.message {
			t.Errorf("koan %q: %s, want %q", c.args, r.Line(), c.message)
		}
	}
	golden(t, fails(t, home, "not-found", "", "show", "99"), "show-earlier-root")
}
