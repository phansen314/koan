package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// createdID is the ID a create gave, or the error.
func createdID(t *testing.T, f *fixture) string {
	t.Helper()
	e := Run("create", parse(t, `{"title": "x"}`), nil, f.env)
	if !e.OK {
		return opError(t, f, "create", `{"title": "x"}`)
	}
	return fmt.Sprint(e.Result.(model.Task).ID)
}

// Every koan.json state against every state file state: the error a
// representative read and write give. koan.json's errors, and a pending
// migration, come first; the state file's last, and only when koan.json is
// usable and current (operations.md, Root states).
func TestRootStatesMatrix(t *testing.T) {
	metas := []struct{ name, content, want string }{
		{"ok", `{"schema": 2, "migration": 1}`, ""},
		{"missing", "<none>", `not-initialized {"missing":"metadata"}`},
		{"corrupt", `{`, `corrupt {"path":"~/tasks/koan.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"unsupported", `{"schema": 3}`, `unsupported-format {"path":"~/tasks/koan.json","found":3,"supported":[2]}`},
		{"past the latest step", `{"schema": 2, "migration": 2}`, `unsupported-format {"path":"~/tasks/koan.json","found":2,"supported":[1],"field":"migration"}`},
		{"old format", `{"schema": 1, "last_id": 100}`, `migration-pending {"recorded":0,"latest":1}`},
		{"a step behind", `{"schema": 2, "migration": 0}`, `migration-pending {"recorded":0,"latest":1}`},
	}
	states := []struct{ name, content, want string }{
		{"ok", "", ""},
		{"missing", "<none>", `not-initialized {"missing":"state"}`},
		{"other root", "{\n  \"schema\": 1,\n  \"root\": \"/elsewhere\",\n  \"last_id\": 100\n}\n", `not-initialized {"missing":"state"}`},
		{"corrupt", `{"schema": 1, "root": "~/tasks"}`, `corrupt {"path":"~/cfg/state.json","reason":"invalid","problems":[{"field":"/last_id","reason":"required"}]}`},
		{"not JSON", `[`, `corrupt {"path":"~/cfg/state.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"unsupported", `{"schema": 2}`, `unsupported-format {"path":"~/cfg/state.json","found":2,"supported":[1]}`},
		{"a directory", "<dir>", `corrupt {"path":"~/cfg/state.json","reason":"unexpected-file"}`},
	}
	for _, m := range metas {
		for _, s := range states {
			want := m.want
			if want == "" {
				want = s.want
			}
			for _, op := range []struct{ name, input string }{{"show", `{"id": 1}`}, {"done", `{"id": 1}`}, {"create", `{"title": "x"}`}} {
				t.Run(m.name+"/"+s.name+"/"+op.name, func(t *testing.T) {
					f := newFixture(t)
					f.task("", 1, false)
					if m.content == "<none>" {
						f.remove("tasks/koan.json")
					} else {
						f.write("tasks/koan.json", m.content)
					}
					switch s.content {
					case "":
					case "<none>":
						f.remove(stateRel)
					case "<dir>":
						f.remove(stateRel)
						if err := os.Mkdir(filepath.Join(f.home, stateRel), 0o755); err != nil {
							t.Fatal(err)
						}
					default:
						f.write(stateRel, strings.ReplaceAll(s.content, "~", f.home))
					}
					if got := f.rel(opError(t, f, op.name, op.input)); got != want {
						t.Errorf("got  %s\nwant %s", got, want)
					}
				})
			}
		}
	}
	// An unreadable state file is io, with the state file's own path.
	f := newFixture(t)
	f.fail(fsys.OpReadFile, filepath.Join(f.home, stateRel), syscall.EACCES)
	if got := f.rel(opError(t, f, "show", `{"id": 1}`)); got != `io {"path":"~/cfg/state.json","code":"EACCES"}` {
		t.Errorf("unreadable: %s", got)
	}
}

// The state file is checked once koan.json is fine, before the lock: a write
// under another root's state file fails before busy.
func TestStateErrorBeforeBusy(t *testing.T) {
	f := newFixture(t)
	f.remove(stateRel)
	r, err := fsys.OS{}.OpenRoot(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, err := r.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if got := opError(t, f, "create", `{"title": "x"}`); got != `not-initialized {"missing":"state"}` {
		t.Errorf("got %s", got)
	}
}

// The counter lives outside the tree: restoring the whole tree to an older
// state — koan.json and every task file — leaves it where it was, so an
// issued ID is never issued again.
func TestRestoreDoesNotMoveCounter(t *testing.T) {
	f := newFixture(t)
	older := f.snapshot()
	for i := 0; i < 3; i++ {
		if got := createdID(t, f); got != fmt.Sprint(101+i) {
			t.Fatalf("create %d: %s", i, got)
		}
	}
	for p, content := range older { // git restore, in effect
		if strings.HasPrefix(p, "tasks/") {
			f.write(p, content)
		}
	}
	for _, id := range []int{101, 102, 103} {
		f.remove(fmt.Sprintf("tasks/%d.json", id))
		f.remove(fmt.Sprintf("tasks/%d.md", id))
	}
	if f.lastID() != 103 {
		t.Fatalf("counter %d after the restore", f.lastID())
	}
	if got := createdID(t, f); got != "104" {
		t.Errorf("create after the restore: %s, want 104", got)
	}
}

// A tree copied to another machine: init attaches it with the highest ID in
// the copy, and the next create goes above.
func TestCloneAttach(t *testing.T) {
	src := newFixture(t)
	src.setLastID(0)
	for i := 0; i < 3; i++ {
		createdID(t, src)
	}
	src.write("tasks/p/150.json", src.read("tasks/101.json")) // a task in a folder, with a higher ID in its name
	dst := newFixture(t)
	dst.remove("tasks")
	dst.remove("cfg")
	copyTree(t, src.root, dst.root)
	e := Run("init", parse(t, `{"root": "`+dst.root+`"}`), nil, dst.env)
	if !e.OK {
		t.Fatalf("%+v", e.Error)
	}
	if got := line(t, e); !strings.Contains(got, `"action":"attached","last_id":150`) {
		t.Errorf("init: %s", got)
	}
	if dst.lastID() != 150 {
		t.Errorf("state file %q", dst.read(stateRel))
	}
	if got := createdID(t, dst); got != "151" {
		t.Errorf("next create: %s", got)
	}
	// The copied tree's koan.json is untouched.
	if dst.read("tasks/koan.json") != src.read("tasks/koan.json") {
		t.Error("koan.json changed")
	}
}

// init on a new tree, and with replace_config: the counter for a root never
// goes down.
func TestInitStateFloor(t *testing.T) {
	f := newFixture(t) // a usable root at tasks/, counter 100
	e := Run("init", parse(t, `{"root": "`+f.root+`", "replace_config": true}`), nil, f.env)
	if !e.OK || e.Result.(InitOutput).LastID != 100 || e.Result.(InitOutput).Action != "attached" {
		t.Fatalf("%+v %+v", e.Result, e.Error)
	}
	if f.lastID() != 100 {
		t.Errorf("counter %d", f.lastID())
	}
	// Another root: the counter for the old one is replaced, and comes back
	// when the config goes back.
	other := filepath.Join(f.home, "other")
	e = Run("init", parse(t, `{"root": "`+other+`", "replace_config": true}`), nil, f.env)
	if !e.OK || e.Result.(InitOutput).LastID != 0 || e.Result.(InitOutput).Action != "created" {
		t.Fatalf("%+v %+v", e.Result, e.Error)
	}
	// The state file now names other; tasks/ is not initialized for it.
	e = Run("init", parse(t, `{"root": "`+f.root+`", "replace_config": true}`), nil, f.env)
	if !e.OK || e.Result.(InitOutput).LastID != 0 {
		t.Fatalf("%+v %+v", e.Result, e.Error)
	}
}

// A new tree in a directory that already has a counter for it keeps it.
func TestInitNewTreeKeepsCounter(t *testing.T) {
	f := newFixture(t)
	f.remove("tasks")
	if err := os.Mkdir(f.root, 0o755); err != nil {
		t.Fatal(err)
	}
	f.remove("cfg/config.toml")
	e := Run("init", parse(t, `{"root": "`+f.root+`"}`), nil, f.env)
	if !e.OK || e.Result.(InitOutput).LastID != 100 || e.Result.(InitOutput).Action != "created" {
		t.Fatalf("%+v %+v", e.Result, e.Error)
	}
	if got := createdID(t, f); got != "101" {
		t.Errorf("create: %s", got)
	}
}

// A stale temp file in the config directory goes with the next write of the
// state file.
func TestStateWriteRemovesStaleTemp(t *testing.T) {
	f := newFixture(t)
	f.write("cfg/.koan-tmp-stale", "x")
	if got := createdID(t, f); got != "101" {
		t.Fatal(got)
	}
	if _, err := os.Lstat(filepath.Join(f.home, "cfg/.koan-tmp-stale")); !os.IsNotExist(err) {
		t.Errorf("stale temp file still there: %v", err)
	}
}

// migrate carries a schema 1 koan.json's last_id into the state file
// (implementation-spec.md, migrate tests: last_id moves).
func TestMigrateMovesLastID(t *testing.T) {
	setup := func() *fixture {
		f := newFixture(t)
		f.remove(stateRel)
		f.write("tasks/koan.json", `{"schema": 1, "last_id": 42}`)
		return f
	}
	f := setup()
	_, out := f.migrate(`{}`)
	if !out.StateWritten || !out.Changed || !out.MetadataConverted {
		t.Errorf("%+v", out)
	}
	if f.read(stateRel) != st(42) || f.read("tasks/koan.json") != "{\n  \"schema\": 2,\n  \"migration\": 1\n}\n" {
		t.Errorf("state file %q, koan.json %q", f.read(stateRel), f.read("tasks/koan.json"))
	}
	if got := createdID(t, f); got != "43" {
		t.Errorf("next create: %s", got)
	}

	// A state file for this root already at 50: it stays.
	f = setup()
	f.setLastID(50)
	before := f.read(stateRel)
	_, out = f.migrate(`{}`)
	if out.StateWritten || f.read(stateRel) != before || f.lastID() != 50 {
		t.Errorf("%+v state file %q", out, f.read(stateRel))
	}
	if got := createdID(t, f); got != "51" {
		t.Errorf("next create: %s", got)
	}

	// A state file for this root below koan.json's: raised.
	f = setup()
	f.setLastID(7)
	_, out = f.migrate(`{}`)
	if !out.StateWritten || f.lastID() != 42 {
		t.Errorf("%+v counter %d", out, f.lastID())
	}

	// Exactly koan.json's: not rewritten.
	f = setup()
	f.setLastID(42)
	_, out = f.migrate(`{}`)
	if out.StateWritten || f.lastID() != 42 {
		t.Errorf("%+v", out)
	}

	// A state file for another root: replaced.
	f = setup()
	f.write(stateRel, "{\n  \"schema\": 1,\n  \"root\": \"/elsewhere\",\n  \"last_id\": 90\n}\n")
	_, out = f.migrate(`{}`)
	if !out.StateWritten || f.read(stateRel) != st(42) {
		t.Errorf("%+v state file %q", out, f.read(stateRel))
	}

	// A corrupt, unsupported, or unreadable one: its error, nothing written,
	// dry_run or not.
	for name, content := range map[string]string{
		"corrupt":     `{"schema": 1}`,
		"unsupported": `{"schema": 2}`,
		"not JSON":    `[`,
	} {
		for _, input := range []string{`{}`, `{"dry_run": true}`} {
			f = setup()
			f.write(stateRel, content)
			before := f.snapshot()
			got := f.rel(opError(t, f, "migrate", input))
			if !strings.HasPrefix(got, "corrupt") && !strings.HasPrefix(got, "unsupported-format") {
				t.Errorf("%s %s: %s", name, input, got)
			}
			if fmt.Sprint(f.snapshot()) != fmt.Sprint(before) {
				t.Errorf("%s %s: a file changed", name, input)
			}
		}
	}
	f = setup()
	f.setLastID(5)
	f.fail(fsys.OpReadFile, filepath.Join(f.home, stateRel), syscall.EACCES)
	if got := f.rel(opError(t, f, "migrate", `{}`)); got != `io {"path":"~/cfg/state.json","code":"EACCES"}` {
		t.Errorf("unreadable: %s", got)
	}

	// dry_run writes neither file, and reports the same.
	f = setup()
	snap0 := f.snapshot()
	_, dry := f.migrate(`{"dry_run": true}`)
	if fmt.Sprint(f.snapshot()) != fmt.Sprint(snap0) || !dry.StateWritten || !dry.Changed {
		t.Errorf("dry_run: %+v", dry)
	}
	f2 := setup()
	_, real := f2.migrate(`{}`)
	dry.DryRun = false
	if dry.StateWritten != real.StateWritten || dry.Changed != real.Changed {
		t.Errorf("dry %+v real %+v", dry, real)
	}
}

// A schema-1 koan.json whose state file exists is not touched by an
// unreadable-folder warning: a state file failing to write ends the run with
// no partial before anything else was written, and one failing after it
// reports state_written.
func TestMigrateStatePartial(t *testing.T) {
	setup := func() *fixture {
		f := newFixture(t)
		f.remove(stateRel)
		f.write("tasks/koan.json", `{"schema": 1, "last_id": 42}`)
		return f
	}
	f := setup()
	f.failAt(fsys.OpRename, "state.json", syscall.ENOSPC)
	e := Run("migrate", parse(t, `{}`), nil, f.env)
	if e.OK || e.Error.Partial != nil || f.read(stateRel) != "<none>" || !strings.HasPrefix(f.read("tasks/koan.json"), `{"schema": 1`) {
		t.Errorf("state write failing: %+v", e.Error)
	}

	f = setup()
	f.failAt(fsys.OpRename, "koan.json", syscall.ENOSPC)
	e = Run("migrate", parse(t, `{}`), nil, f.env)
	if e.OK {
		t.Fatal("ok")
	}
	if got := strings.TrimSpace(string(mustMarshal(t, e.Error.Partial))); got != `{"state_written":true,"tasks_converted":0}` {
		t.Errorf("partial %s", got)
	}
	if f.read(stateRel) != st(42) || !strings.HasPrefix(f.read("tasks/koan.json"), `{"schema": 1`) {
		t.Error("the counter is not in both places")
	}
	// The root still needs migration; a rerun finishes.
	f.env.FS = fsys.OS{}
	if got := opError(t, f, "create", `{"title": "x"}`); !strings.HasPrefix(got, "migration-pending") {
		t.Errorf("%s", got)
	}
	_, out := f.migrate(`{}`)
	if out.StateWritten || !out.MetadataConverted {
		t.Errorf("rerun %+v", out)
	}
}

// state-missing and state-unusable: what doctor reports, and what repair
// does with each (operations.md, Finding kinds and repair).
func TestDoctorStateFindings(t *testing.T) {
	f := newFixture(t)
	f.task("", 7, false)
	f.task("p", 12, false)
	f.remove(stateRel)
	want := `{"healthy":false,"findings":[{"kind":"state-missing","class":"on-request","count":1,"truncated":false,"items":[` +
		`{"paths":["~/cfg/state.json"],"ids":[],"action":"create-state","suggest":"` + stateMissingSuggest(12) + `","last_id":12}]}]}`
	if got := f.rel(f.op("doctor", `{}`)); got != want {
		t.Errorf("missing:\ngot  %s\nwant %s", got, want)
	}
	// Another root's state file is a missing one.
	f.write(stateRel, "{\n  \"schema\": 1,\n  \"root\": \"/elsewhere\",\n  \"last_id\": 500\n}\n")
	if got := f.rel(f.op("doctor", `{}`)); got != want {
		t.Errorf("other root:\ngot  %s\nwant %s", got, want)
	}

	// repair: not-initialized (state) unless it names state-missing.
	if got := opError(t, f, "repair", `{}`); got != `not-initialized {"missing":"state"}` {
		t.Errorf("repair: %s", got)
	}
	if got := opError(t, f, "repair", `{"kinds": ["temp-leftover"]}`); got != `not-initialized {"missing":"state"}` {
		t.Errorf("repair: %s", got)
	}
	out := f.rel(f.op("repair", `{"kinds": ["state-missing"]}`))
	if !strings.HasPrefix(out, `{"repaired":[{"kind":"state-missing"`) || !strings.Contains(out, `"action":"create-state"`) || !strings.HasSuffix(out, `"healthy":true,"findings":[]}`) {
		t.Errorf("repair: %s", out)
	}
	if f.read(stateRel) != st(12) {
		t.Errorf("state file %q", f.read(stateRel))
	}
	// Naming it when the state file is there is not an error.
	if out := f.op("repair", `{"kinds": ["state-missing"]}`); out != `{"repaired":[],"healthy":true,"findings":[]}` {
		t.Errorf("second repair: %s", out)
	}

	// Unusable: manual, with the error; repair refuses it.
	f.write(stateRel, `{"schema": 1}`)
	got := f.rel(f.op("doctor", `{}`))
	if !strings.Contains(got, `"kind":"state-unusable","class":"manual"`) || !strings.Contains(got, `"action":null`) ||
		!strings.Contains(got, `"error":{"kind":"corrupt"`) || strings.Contains(got, `"kind":"state-missing"`) {
		t.Errorf("unusable:\n%s", got)
	}
	before := f.read(stateRel)
	for _, input := range []string{`{}`, `{"kinds": ["state-missing"]}`} {
		if got := f.rel(opError(t, f, "repair", input)); !strings.HasPrefix(got, `corrupt {"path":"~/cfg/state.json"`) {
			t.Errorf("repair %s: %s", input, got)
		}
	}
	if f.read(stateRel) != before {
		t.Error("repair changed an unusable state file")
	}
	if got := opError(t, f, "repair", `{"kinds": ["state-unusable"]}`); !strings.HasPrefix(got, "invalid-input") {
		t.Errorf("naming a manual kind: %s", got)
	}
}

func stateMissingSuggest(maxID int) string {
	return fmt.Sprintf("koan repair --kinds state-missing, unless a task with an ID above %d was ever deleted; then restore the state file from a backup, or write it by hand with that ID as last_id", maxID)
}

// state-missing is not reported while koan.json is an older format holding
// last_id (migrate writes the state file then); id-above-last-id only when
// the state file is usable.
func TestDoctorStateOrder(t *testing.T) {
	f := newFixture(t)
	f.task("", 7, false)
	f.remove(stateRel)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 5}`)
	got := f.rel(f.op("doctor", `{}`))
	if strings.Contains(got, "state-missing") || !strings.Contains(got, `"kind":"migration-pending"`) {
		t.Errorf("schema 1: %s", got)
	}
	// repair: pending first, whatever the state file.
	if got := opError(t, f, "repair", `{"kinds": ["state-missing"]}`); !strings.HasPrefix(got, "migration-pending") {
		t.Errorf("repair: %s", got)
	}
	// A schema 2 koan.json a step behind holds no last_id: reported.
	f.write("tasks/koan.json", `{"schema": 2, "migration": 0}`)
	if got := f.rel(f.op("doctor", `{}`)); !strings.Contains(got, `"kind":"state-missing"`) {
		t.Errorf("behind: %s", got)
	}
	// id-above-last-id with an unusable state file: not reported.
	f.write("tasks/koan.json", `{"schema": 2, "migration": 1}`)
	f.write(stateRel, `{`)
	if got := f.op("doctor", `{}`); strings.Contains(got, "id-above-last-id") {
		t.Errorf("unusable state: %s", got)
	}
}

// A missing koan.json and a missing state file in one run: repair rebuilds
// both when asked, koan.json first, and goes on to its other repairs.
func TestRepairBothMissing(t *testing.T) {
	f := newFixture(t)
	f.remove("tasks/koan.json")
	f.remove(stateRel)
	f.task("", 9, false, 99)
	out := f.op("repair", `{"kinds": ["metadata-missing", "state-missing", "dangling-reference"]}`)
	if !strings.Contains(out, `"kind":"metadata-missing"`) || !strings.Contains(out, `"kind":"state-missing"`) || !strings.Contains(out, `"healthy":true`) {
		t.Errorf("repair: %s", out)
	}
	if f.read(stateRel) != st(9) || f.blockedBy("9.json") != "[]" {
		t.Errorf("state file %q", f.read(stateRel))
	}
	// A refusal changes nothing: koan.json missing and not named, state named.
	f = newFixture(t)
	f.remove("tasks/koan.json")
	f.remove(stateRel)
	before := f.snapshot()
	if got := opError(t, f, "repair", `{"kinds": ["state-missing"]}`); got != `not-initialized {"missing":"metadata"}` {
		t.Errorf("got %s", got)
	}
	if fmt.Sprint(f.snapshot()) != fmt.Sprint(before) {
		t.Error("a refusal changed the tree")
	}
}

var _ = store.StateName
