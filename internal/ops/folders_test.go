package ops

import (
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
)

func TestFoldersAndMove(t *testing.T) {
	w := ready(t)
	if got := w.ok("folder create", `{"folder":"/a/b"}`); got["id"] != float64(2) || got["path"] != "/a/b" || got["changed"] != true || len(got["folders_created"].([]any)) != 2 {
		t.Errorf("folder create: %v", got)
	}
	if got := w.ok("folder create", `{"folder":"/a/b"}`); got["id"] != float64(2) || got["changed"] != false || len(got["folders_created"].([]any)) != 0 {
		t.Errorf("folder create again: %v", got)
	}
	w.ok("task create", `{"title":"t","folder":"/a/b","notes":"n"}`)

	got := w.ok("task move", `{"id":3,"to":"/c/d"}`)
	if got["id"] != float64(3) || got["changed"] != true || len(got["folders_created"].([]any)) != 2 {
		t.Errorf("move: %v", got)
	}
	if tk := task(w.ok("task show", `{"id":3}`)); tk["folder"] != "/c/d" {
		t.Errorf("task after move: %v", tk["folder"])
	}
	if n, _ := os.ReadFile(w.root("c/d/3.md")); string(n) != "n" {
		t.Errorf("notes did not move: %q", n)
	}
	if got := w.ok("task move", `{"id":3,"to":"/c/d"}`); got["changed"] != false {
		t.Error("move to the same folder changed something")
	}
	os.WriteFile(w.root("a/3.md"), []byte("someone's"), 0o644)
	w.fail("task move", `{"id":3,"to":"/a"}`, errs.Conflict, errs.RuleDestinationExists)

	w.fail("folder move", `{"folder":"/c","to":"/c/x"}`, errs.InvalidInput, "")
	w.fail("folder move", `{"folder":"/nope","to":"/x"}`, errs.NotFound, "")
	w.fail("folder move", `{"folder":"/c","to":"/a"}`, errs.Conflict, errs.RuleDestinationExists)
	if got := w.ok("folder move", `{"folder":"/c","to":"/z/y"}`); got["id"] != float64(4) || got["path"] != "/z/y" || got["changed"] != true {
		t.Errorf("folder move: %v", got)
	}
	if tk := task(w.ok("task show", `{"id":3}`)); tk["folder"] != "/z/y/d" {
		t.Errorf("task after folder move: %v", tk["folder"])
	}
	// The moved folder keeps its ID; the one made for it, /z, took the next.
	for f, want := range map[string]string{"z/y": `"id": 4`, "z": `"id": 6`} {
		if data, _ := os.ReadFile(w.root(f, "folder.json")); !strings.Contains(string(data), want) {
			t.Errorf("%s: %s, want %s", f, data, want)
		}
	}

	w.fail("folder delete", `{"folder":"/z"}`, errs.Conflict, errs.RuleNotEmpty)
	w.fail("folder delete", `{"folder":"/"}`, errs.InvalidInput, "")
	w.ok("task create", `{"title":"outside","blocked_by":[3]}`)
	w.ok("task claim", `{"id":3}`)
	got = w.ok("folder delete", `{"folder":"/z","contents":true}`)
	if r := w.record(got); r != `{"blocked_by":[],"dir":"~/@z","empty":false,"extra":{},"id":6,"inherited":[],"notes_path":"~/@z/@folder.md","open_blockers":[],"path":"/z","readiness":"ready","title":null}` {
		t.Errorf("folder delete: %s", r)
	}
	wantIDs(t, "deleted", got["deleted"], 3)
	wantIDs(t, "dependents", got["dependents"], 7)
	if n := w.ok("info", `{}`)["claims"]; n != float64(0) {
		t.Errorf("claims after folder delete: %v", n)
	}
	os.WriteFile(w.root("afile"), nil, 0o644)
	w.fail("folder create", `{"folder":"/afile/x"}`, errs.Conflict, errs.RuleNotAFolder)
}

// A folder's title, extra, and notes are set by folder create, changed by
// folder update, and shown by folder show and list.
func TestFolderMetadata(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q","title":" Research ","extra":{"kind":"phase"},"notes":"n"}`)
	want := `{"blocked_by":[],"dir":"~/@p/@q","empty":true,"extra":{"kind":"phase"},"id":2,"inherited":[],"notes_path":"~/@p/@q/@folder.md","open_blockers":[],"path":"/p/q","readiness":"done","title":"Research"}`
	if r := w.record(w.ok("folder show", `{"folder":"/p/q"}`)); r != want {
		t.Errorf("folder create: %s, want %s", r, want)
	}
	if n, _ := os.ReadFile(w.root("p/q/folder.md")); string(n) != "n" {
		t.Errorf("notes %q", n)
	}
	// The folders above it get none of it.
	if r := w.record(w.ok("folder show", `{"folder":"/p"}`)); r != `{"blocked_by":[],"dir":"~/@p","empty":true,"extra":{},"id":1,"inherited":[],"notes_path":"~/@p/@folder.md","open_blockers":[],"path":"/p","readiness":"done","title":null}` {
		t.Errorf("folder show /p: %s", r)
	}
	if r := w.record(w.ok("folder show", `{"folder":"/"}`)); r != `{"blocked_by":[],"dir":"~","empty":true,"extra":{},"id":null,"inherited":[],"notes_path":null,"open_blockers":[],"path":"/","readiness":"done","title":null}` {
		t.Errorf("folder show /: %s", r)
	}
	w.fail("folder show", `{"folder":"/nope"}`, errs.NotFound, "")
	// Fields for a folder already there are refused; without them, it is fine.
	w.fail("folder create", `{"folder":"/p/q","title":"x"}`, errs.Conflict, errs.RuleDestinationExists)
	if got := w.ok("folder create", `{"folder":"/p/q"}`); len(got["folders_created"].([]any)) != 0 {
		t.Errorf("folder create again: %v", got)
	}

	w.fail("folder create", `{"folder":"/p/q","notes":""}`, errs.Conflict, errs.RuleDestinationExists)

	got := w.ok("folder update", `{"folder":"/p/q","title":null,"extra_merge":{"n":1}}`)
	if got["id"] != float64(2) || got["path"] != "/p/q" || got["changed"] != true {
		t.Errorf("folder update: %v", got)
	}
	if r := w.record(w.ok("folder show", `{"folder":"/p/q"}`)); !strings.Contains(r, `"extra":{"kind":"phase","n":1}`) || !strings.Contains(r, `"title":null`) {
		t.Errorf("folder update: %s", r)
	}
	before, _ := os.Stat(w.root("p/q/folder.json"))
	if got := w.ok("folder update", `{"folder":"/p/q","extra_merge":{"n":1}}`); got["changed"] != false {
		t.Errorf("an update that changes nothing changed %v", got)
	}
	if after, _ := os.Stat(w.root("p/q/folder.json")); !after.ModTime().Equal(before.ModTime()) {
		t.Error("an update that changes nothing wrote the folder file")
	}
	w.ok("folder update", `{"folder":"/p/q","extra_remove":["n"]}`)
	if r := w.record(w.ok("folder show", `{"folder":"/p/q"}`)); !strings.Contains(r, `"extra":{"kind":"phase"}`) {
		t.Errorf("extra_remove: %s", r)
	}
	w.ok("folder update", `{"folder":"/p/q","extra_replace_all":{"z":true}}`)
	if r := w.record(w.ok("folder show", `{"folder":"/p/q"}`)); !strings.Contains(r, `"extra":{"z":true}`) {
		t.Errorf("extra_replace_all: %s", r)
	}
	w.fail("folder update", `{"folder":"/"}`, errs.InvalidInput, "")
	w.fail("folder update", `{"folder":"/p"}`, errs.InvalidInput, "")
	w.fail("folder update", `{"folder":"/p","extra_merge":{},"extra_replace_all":{}}`, errs.InvalidInput, "")
	w.fail("folder update", `{"folder":"/p","title":"   "}`, errs.InvalidInput, "")

	// A moved folder keeps its fields.
	w.ok("folder move", `{"folder":"/p","to":"/r"}`)
	if r := w.record(w.ok("folder show", `{"folder":"/r"}`)); r != `{"blocked_by":[],"dir":"~/@r","empty":true,"extra":{},"id":1,"inherited":[],"notes_path":"~/@r/@folder.md","open_blockers":[],"path":"/r","readiness":"done","title":null}` {
		t.Errorf("folder move: %s", r)
	}

	// A folder with no folder file, and one koan can't use.
	os.Mkdir(w.root("hand"), 0o755)
	w.fail("folder update", `{"folder":"/hand","title":"x"}`, errs.NotFound, "")
	if r := w.record(w.ok("folder show", `{"folder":"/hand"}`)); !strings.Contains(r, `"id":null`) {
		t.Errorf("folder show /hand: %s", r)
	}
	os.WriteFile(w.root("r/q/folder.json"), []byte("{"), 0o644)
	w.fail("folder show", `{"folder":"/r/q"}`, errs.Corrupt, "")
	w.fail("folder update", `{"folder":"/r/q","title":"x"}`, errs.Corrupt, "")
	// folder list shows each folder's fields; the folder with no folder
	// file has no ID, and the unusable one none either: each is a warning,
	// since their blockers are unknown. A folder write that returns the ID
	// warns about the one it reads; with nothing to make, it walks nothing.
	for _, c := range []struct {
		name, input string
		n           int
	}{{"folder list", `{}`, 2}, {"folder create", `{"folder":"/r/q"}`, 1}} {
		env := w.run(c.name, c.input)
		if len(env.Warnings) != c.n || slices.ContainsFunc(env.Warnings, func(w errs.Warning) bool { return w.Kind != errs.WarnUnusableFile }) {
			t.Errorf("%s: warnings %+v", c.name, env.Warnings)
		}
	}
	w.ok("folder update", `{"folder":"/r","title":"R"}`)
	ids := map[string]any{}
	titles := map[string]any{}
	for _, r := range w.ok("folder list", `{}`)["folders"].([]any) {
		r := r.(map[string]any)
		ids[r["path"].(string)], titles[r["path"].(string)] = r["id"], r["title"]
	}
	if ids["/hand"] != nil || ids["/r/q"] != nil || ids["/r"] != float64(1) || titles["/r"] != "R" {
		t.Errorf("ids %v titles %v", ids, titles)
	}
}

// Folders and tasks share one counter, so an ID in two of their files is a
// duplicate; a folder file koan can't use is reported, and init passes it.
func TestFolderFilesAndIDs(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/p"}`) // /p is 1, the task 2
	w.ok("folder create", `{"folder":"/q"}`)           // 3
	w.ok("folder create", `{"folder":"/r"}`)           // 4
	os.WriteFile(w.root("q/folder.json"), []byte(`{"schema":2,"id":2,"title":null,"blocked_by":[],"extra":{}}`), 0o644)
	os.WriteFile(w.root("r/folder.json"), []byte(`{"schema":2,"id":4}`), 0o644)
	want := []string{"duplicate-id @p/@2.json,@q/@folder.json", "unusable-file @r/@folder.json"}
	if fs := w.findings(); !slices.Equal(fs, want) {
		t.Errorf("findings %v, want %v", fs, want)
	}
	os.Remove(w.env.StatePath())
	w.ok("init", `{}`)
	if n := w.ok("info", `{}`)["last_id"]; n != float64(2) {
		t.Errorf("last_id %v, want 2: /r's file can't be read", n)
	}
}

func TestDeleteFolderRemovesTheFolderFirst(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a","folder":"/gone"},{"title":"b","blocked_by":["a"]}]}`)
	// /gone is 1, a 2, b 3. The rewrite of task 3 starts with a temp
	// file; fail it.
	w.env.FS = fsys.FS{Hook: fsys.ErrnoAt(fsys.OpCreateTemp, "", 1, syscall.ENOSPC)}
	w.fail("folder delete", `{"folder":"/gone","contents":true}`, errs.IO, "")
	w.env.FS = fsys.FS{}
	if tk := task(w.ok("task show", `{"id":3}`)); tk["readiness"] != "blocked" {
		t.Errorf("the dependent was freed though folder delete failed: %v", tk)
	}
	fs := w.ok("doctor", `{}`)["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["kind"] != "dangling-reference" {
		t.Errorf("doctor: %v", fs)
	}
}

// A removal that fails partway still takes the tasks it did remove out of
// every blocked_by, and ends their claims; the tasks it left keep theirs.
func TestDeleteFolderFailingPartwayCleansUp(t *testing.T) {
	w := ready(t)
	// /d is 1, /d/x 2, /d/y 3, /out 4; then a 5, b 6, and 7 waiting on both.
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a","folder":"/d/x"},{"ref":"b","title":"b","folder":"/d/y"},`+
		`{"title":"t","folder":"/out","blocked_by":["a","b"]}]}`)
	w.ok("task claim", `{"id":5}`)
	w.ok("task claim", `{"id":6}`)
	// Fail the removal of the second task file, whichever folder goes
	// first: the first task is gone, the other left.
	var removed []int64
	w.env.FS = fsys.FS{Hook: func(op fsys.Op) error {
		name := path.Base(op.Path)
		if op.Name != fsys.OpRemove || !strings.HasSuffix(name, ".json") || name == "@folder.json" {
			return nil
		}
		if len(removed) == 1 {
			return syscall.EACCES
		}
		id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(name, "@"), ".json"), 10, 64)
		removed = append(removed, id)
		return nil
	}}
	e := w.fail("folder delete", `{"folder":"/d","contents":true}`, errs.IO, "")
	w.env.FS = fsys.FS{}
	gone, left := removed[0], 11-removed[0]
	if !slices.Equal(e.IDs, []int64{gone}) || !slices.Contains(e.ChangedPaths, w.root("out/7.json")) || !strings.Contains(e.Message, "1 other task") {
		t.Errorf("error %+v: want ids [%d], the rewritten 7 in changed_paths, and the count in the message", e, gone)
	}
	if got := task(w.ok("task show", `{"id":7}`))["blocked_by"]; !slices.Equal(ids(got), []int64{left}) {
		t.Errorf("7 is blocked by %v, want only %d", got, left)
	}
	if got := w.ok("task list", `{"claimed":true,"fields":["id"]}`)["tasks"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != float64(left) {
		t.Errorf("claimed %v, want only %d", got, left)
	}
	if f := w.findings(); len(f) != 0 {
		t.Errorf("findings %v", f)
	}
	// Once the cause is gone, running it again removes the rest.
	r := w.ok("folder delete", `{"folder":"/d","contents":true}`)
	if !slices.Equal(ids(r["deleted"]), []int64{left}) || !slices.Equal(ids(r["dependents"]), []int64{7}) {
		t.Errorf("second delete: %v", r)
	}
}

// A cleanup that fails too does not hide the removal's error.
func TestDeleteFolderCleanupFailing(t *testing.T) {
	w := ready(t)
	// /d is 1, /out 2; a 3 in /d, and 4 waiting on it.
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a","folder":"/d"},{"title":"t","folder":"/out","blocked_by":["a"]}]}`)
	// The removal fails at /d's folder file, after the task; the rewrite of
	// 4 then fails at its temp file.
	w.env.FS = fsys.FS{Hook: func(op fsys.Op) error {
		if (op.Name == fsys.OpRemove && op.Path == "@d/@folder.json") || op.Name == fsys.OpCreateTemp {
			return syscall.EACCES
		}
		return nil
	}}
	e := w.fail("folder delete", `{"folder":"/d","contents":true}`, errs.IO, "")
	w.env.FS = fsys.FS{}
	if !slices.Equal(e.Paths, []string{w.root("d")}) || !slices.Equal(e.IDs, []int64{3}) || !strings.Contains(e.Message, "cleaning up after them failed") {
		t.Errorf("error %+v: want the removal's, naming 3 and the failed cleanup", e)
	}
	if f := w.findings(); !slices.Contains(f, "dangling-reference @out/@4.json") {
		t.Errorf("findings %v, want the reference left", f)
	}
}

// folder delete removes each folder's folder file after everything else in
// it, so a removal cut short leaves no folder without one; and it removes a
// symlink without following it.
func TestDeleteFolderRemovesFolderFilesLast(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/p/q","notes":"n"}`)
	os.MkdirAll(w.disk("keep"), 0o755)
	os.WriteFile(w.disk("keep/x"), nil, 0o644)
	os.Symlink("../../keep", w.disk("@p/@q/link"))
	removed := 0
	w.env.FS = fsys.FS{Hook: func(op fsys.Op) error {
		if op.Name == fsys.OpRemove && path.Base(op.Path) == "@folder.json" {
			removed++
			if ents, _ := os.ReadDir(w.disk(path.Dir(op.Path))); len(ents) != 1 {
				t.Errorf("%s removed before the rest of its folder: %d entries left", op.Path, len(ents))
			}
		}
		return nil
	}}
	w.ok("folder delete", `{"folder":"/p","contents":true,"force":true}`)
	if removed != 2 {
		t.Errorf("removed %d folder files, want 2", removed)
	}
	if _, err := os.Stat(w.disk("@p")); !os.IsNotExist(err) {
		t.Errorf("the folder is still there: %v", err)
	}
	if _, err := os.Stat(w.disk("keep/x")); err != nil {
		t.Errorf("followed a symlink: %v", err)
	}
}

// A folder delete with contents removes material only when forced: it
// lists what it found, at any depth but never inside it. Hidden entries
// count, but for a temp file koan left and a .DS_Store.
func TestDeleteFolderRefusesMaterial(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/p/q","notes":"n"}`)
	os.WriteFile(w.disk("@p/.DS_Store"), nil, 0o644)
	os.WriteFile(w.disk("@p/@q/.koan-tmp-1"), nil, 0o644)
	w.ok("folder delete", `{"folder":"/p","contents":true}`) // only koan's and disposable entries

	w.ok("task create", `{"title":"b","folder":"/p/q"}`)
	os.MkdirAll(w.disk("@p/@q/docs/deep"), 0o755)
	os.WriteFile(w.disk("@p/@q/docs/deep/plan.md"), nil, 0o644)
	os.WriteFile(w.disk("@p/@stray.txt"), nil, 0o644)
	os.MkdirAll(w.disk("@p/.venv/lib"), 0o755)
	os.WriteFile(w.disk("@p/@q/.env"), nil, 0o644)
	e := w.fail("folder delete", `{"folder":"/p","contents":true}`, errs.Conflict, errs.RuleHasMaterial)
	want := []string{w.disk("@p/.venv"), w.disk("@p/@stray.txt"), w.disk("@p/@q/.env"), w.disk("@p/@q/docs")}
	if !slices.Equal(e.Paths, want) {
		t.Errorf("paths %v, want %v", e.Paths, want)
	}
	if len(e.ChangedPaths) != 0 {
		t.Errorf("changed %v", e.ChangedPaths)
	}
	got := w.ok("folder delete", `{"folder":"/p","contents":true,"force":true}`)
	wantIDs(t, "deleted", got["deleted"], 6)
	if _, err := os.Stat(w.disk("@p")); !os.IsNotExist(err) {
		t.Errorf("the folder is still there: %v", err)
	}
}

// folder create with blocks, failing after what is in blocks waits on the
// new folder's ID, says what it left (spec/operations.md, folder create).
func TestFolderCreateBlocksFailing(t *testing.T) {
	for _, c := range []struct {
		what    string
		fail    func(op fsys.Op) bool
		ids     []int64
		folders []string
		message string
		id      any // the folder's ID afterwards, nil when it has none
	}{
		{"the folder", func(op fsys.Op) bool { return op.Name == fsys.OpMkdir && op.Path == "@c" }, []int64{1}, []string{}, "[1] wait on folder 2, which was not made", nil},
		{"its folder file", func(op fsys.Op) bool { return op.Name == fsys.OpLink && op.NewPath == "@c/@folder.json" }, []int64{1}, []string{"/c"}, "/c was made without its @folder.json", nil},
		{"its notes", func(op fsys.Op) bool { return op.Name == fsys.OpLink && op.NewPath == "@c/@folder.md" }, []int64{2}, []string{}, "/c was made without its notes", float64(2)},
	} {
		t.Run(c.what, func(t *testing.T) {
			w := ready(t)
			w.ok("task create", `{"title":"a"}`) // 1
			w.env.FS = fsys.FS{Hook: func(op fsys.Op) error {
				if c.fail(op) {
					return syscall.ENOSPC
				}
				return nil
			}}
			env := w.run("folder create", `{"folder":"/c","blocks":[1]}`)
			w.env.FS = fsys.FS{}
			if env.OK {
				t.Fatalf("got %+v", env)
			}
			e := env.Error
			if !slices.Equal(e.IDs, c.ids) || !slices.Equal(e.Folders, c.folders) || !strings.HasPrefix(e.Message, c.message) {
				t.Errorf("ids %v folders %v message %q", e.IDs, e.Folders, e.Message)
			}
			if c.id != nil {
				if id, _ := w.holderOf("/c"); id != c.id {
					t.Errorf("/c has ID %v, want %v", id, c.id)
				}
			}
			wantIDs(t, "task 1's blocked_by", task(w.ok("task show", `{"id":1}`))["blocked_by"], 2)
		})
	}
}

// The root is always there: folder create / is a success with nothing to
// make, and destination-exists with anything to set.
func TestFolderCreateRoot(t *testing.T) {
	w := ready(t)
	if got := w.ok("folder create", `{"folder":"/"}`); got["id"] != nil || got["changed"] != false {
		t.Errorf("folder create /: %v", got)
	}
	w.fail("folder create", `{"folder":"/","title":"x"}`, errs.Conflict, errs.RuleDestinationExists)
}

// A folder delete failing partway takes the IDs of the folders it did
// remove out of every blocked_by, and leaves those of the folders left.
func TestDeleteFolderFailingPartwayUnreferencesFolders(t *testing.T) {
	w := ready(t)
	// /d is 1, /d/x 2, /d/y 3, /out 4; then a 5 in /d/x, b 6 in /d/y, and 7
	// waiting on both folders.
	w.ok("batch", `{"tasks":[{"title":"a","folder":"/d/x"},{"title":"b","folder":"/d/y"},`+
		`{"title":"t","folder":"/out","blocked_by":["/d/x","/d/y"]}]}`)
	// Fail the removal of the second task file: the folder of the first is
	// gone with it, the other left.
	var removed []int64
	w.env.FS = fsys.FS{Hook: func(op fsys.Op) error {
		name := path.Base(op.Path)
		if op.Name != fsys.OpRemove || !strings.HasSuffix(name, ".json") || name == "@folder.json" {
			return nil
		}
		if len(removed) == 1 {
			return syscall.EACCES
		}
		id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(name, "@"), ".json"), 10, 64)
		removed = append(removed, id)
		return nil
	}}
	w.fail("folder delete", `{"folder":"/d","contents":true}`, errs.IO, "")
	w.env.FS = fsys.FS{}
	left := int64(2)
	if removed[0] == 5 {
		left = 3
	}
	if got := task(w.ok("task show", `{"id":7}`))["blocked_by"]; !slices.Equal(ids(got), []int64{left}) {
		t.Errorf("7 is blocked by %v, want only the folder left, %d", got, left)
	}
	if f := w.findings(); len(f) != 0 {
		t.Errorf("findings %v", f)
	}
}
