package ops

import (
	"os"
	"path"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
)

func TestFoldersAndMove(t *testing.T) {
	w := ready(t)
	if got := w.ok("create-folder", `{"folder":"/a/b"}`); len(got["folders_created"].([]any)) != 2 {
		t.Errorf("create-folder: %v", got)
	}
	if got := w.ok("create-folder", `{"folder":"/a/b"}`); len(got["folders_created"].([]any)) != 0 {
		t.Errorf("create-folder again: %v", got)
	}
	w.ok("create", `{"title":"t","folder":"/a/b","notes":"n"}`)

	got := w.ok("move", `{"id":3,"to":"/c/d"}`)
	if task(got)["folder"] != "/c/d" || got["changed"] != true || len(got["folders_created"].([]any)) != 2 {
		t.Errorf("move: %v", got)
	}
	if n, _ := os.ReadFile(w.root("c/d/3.md")); string(n) != "n" {
		t.Errorf("notes did not move: %q", n)
	}
	if got := w.ok("move", `{"id":3,"to":"/c/d"}`); got["changed"] != false {
		t.Error("move to the same folder changed something")
	}
	os.WriteFile(w.root("a/3.md"), []byte("someone's"), 0o644)
	w.fail("move", `{"id":3,"to":"/a"}`, errs.Conflict, errs.RuleDestinationExists)

	w.fail("move-folder", `{"folder":"/c","to":"/c/x"}`, errs.InvalidInput, "")
	w.fail("move-folder", `{"folder":"/nope","to":"/x"}`, errs.NotFound, "")
	w.fail("move-folder", `{"folder":"/c","to":"/a"}`, errs.Conflict, errs.RuleDestinationExists)
	if got := w.ok("move-folder", `{"folder":"/c","to":"/z/y"}`); got["folder"].(map[string]any)["path"] != "/z/y" || got["changed"] != true {
		t.Errorf("move-folder: %v", got)
	}
	if tk := task(w.ok("show", `{"id":3}`)); tk["folder"] != "/z/y/d" {
		t.Errorf("task after move-folder: %v", tk["folder"])
	}
	// The moved folder keeps its ID; the one made for it, /z, took the next.
	for f, want := range map[string]string{"z/y": `"id": 4`, "z": `"id": 6`} {
		if data, _ := os.ReadFile(w.root(f, "folder.json")); !strings.Contains(string(data), want) {
			t.Errorf("%s: %s, want %s", f, data, want)
		}
	}

	w.fail("delete-folder", `{"folder":"/z"}`, errs.Conflict, errs.RuleNotEmpty)
	w.fail("delete-folder", `{"folder":"/"}`, errs.InvalidInput, "")
	w.ok("create", `{"title":"outside","blocked_by":[3]}`)
	w.ok("claim", `{"id":3}`)
	got = w.ok("delete-folder", `{"folder":"/z","recursive":true}`)
	wantIDs(t, "deleted", got["deleted"], 3)
	wantIDs(t, "dependents", got["dependents"], 7)
	if n := w.ok("info", `{}`)["claims"]; n != float64(0) {
		t.Errorf("claims after delete-folder: %v", n)
	}
	os.WriteFile(w.root("afile"), nil, 0o644)
	w.fail("create-folder", `{"folder":"/afile/x"}`, errs.Conflict, errs.RuleNotAFolder)
}

// A folder's title, extra, and notes are set by create-folder, changed by
// update-folder, and shown by show-folder and list.
func TestFolderMetadata(t *testing.T) {
	w := ready(t)
	got := w.ok("create-folder", `{"folder":"/p/q","title":" Research ","extra":{"kind":"phase"},"notes":"n"}`)
	want := `{"dir":"~/@p/@q","extra":{"kind":"phase"},"id":2,"notes_path":"~/@p/@q/@folder.md","path":"/p/q","title":"Research"}`
	if r := w.record(got); r != want {
		t.Errorf("create-folder: %s, want %s", r, want)
	}
	if n, _ := os.ReadFile(w.root("p/q/folder.md")); string(n) != "n" {
		t.Errorf("notes %q", n)
	}
	// The folders above it get none of it.
	if r := w.record(w.ok("show-folder", `{"folder":"/p"}`)); r != `{"dir":"~/@p","extra":{},"id":1,"notes_path":"~/@p/@folder.md","path":"/p","title":null}` {
		t.Errorf("show-folder /p: %s", r)
	}
	if r := w.record(w.ok("show-folder", `{"folder":"/"}`)); r != `{"dir":"~","extra":{},"id":null,"notes_path":null,"path":"/","title":null}` {
		t.Errorf("show-folder /: %s", r)
	}
	w.fail("show-folder", `{"folder":"/nope"}`, errs.NotFound, "")
	// Fields for a folder already there are refused; without them, it is fine.
	w.fail("create-folder", `{"folder":"/p/q","title":"x"}`, errs.Conflict, errs.RuleDestinationExists)
	if got := w.ok("create-folder", `{"folder":"/p/q"}`); len(got["folders_created"].([]any)) != 0 {
		t.Errorf("create-folder again: %v", got)
	}

	w.fail("create-folder", `{"folder":"/p/q","notes":""}`, errs.Conflict, errs.RuleDestinationExists)

	got = w.ok("update-folder", `{"folder":"/p/q","title":null,"extra_merge":{"n":1}}`)
	if r := w.record(got); got["changed"] != true || !strings.Contains(r, `"extra":{"kind":"phase","n":1}`) || !strings.Contains(r, `"title":null`) {
		t.Errorf("update-folder: %s %v", r, got["changed"])
	}
	before, _ := os.Stat(w.root("p/q/folder.json"))
	if got := w.ok("update-folder", `{"folder":"/p/q","extra_merge":{"n":1}}`); got["changed"] != false {
		t.Errorf("an update that changes nothing changed %v", got)
	}
	if after, _ := os.Stat(w.root("p/q/folder.json")); !after.ModTime().Equal(before.ModTime()) {
		t.Error("an update that changes nothing wrote the folder file")
	}
	if r := w.record(w.ok("update-folder", `{"folder":"/p/q","extra_remove":["n"]}`)); !strings.Contains(r, `"extra":{"kind":"phase"}`) {
		t.Errorf("extra_remove: %s", r)
	}
	if r := w.record(w.ok("update-folder", `{"folder":"/p/q","extra_replace_all":{"z":true}}`)); !strings.Contains(r, `"extra":{"z":true}`) {
		t.Errorf("extra_replace_all: %s", r)
	}
	w.fail("update-folder", `{"folder":"/"}`, errs.InvalidInput, "")
	w.fail("update-folder", `{"folder":"/p"}`, errs.InvalidInput, "")
	w.fail("update-folder", `{"folder":"/p","extra_merge":{},"extra_replace_all":{}}`, errs.InvalidInput, "")
	w.fail("update-folder", `{"folder":"/p","title":"   "}`, errs.InvalidInput, "")

	// A moved folder keeps its fields.
	got = w.ok("move-folder", `{"folder":"/p","to":"/r"}`)
	if r := w.record(got); r != `{"dir":"~/@r","extra":{},"id":1,"notes_path":"~/@r/@folder.md","path":"/r","title":null}` {
		t.Errorf("move-folder: %s", r)
	}

	// A folder with no folder file, and one koan can't use.
	os.Mkdir(w.root("hand"), 0o755)
	w.fail("update-folder", `{"folder":"/hand","title":"x"}`, errs.NotFound, "")
	if r := w.record(w.ok("show-folder", `{"folder":"/hand"}`)); !strings.Contains(r, `"id":null`) {
		t.Errorf("show-folder /hand: %s", r)
	}
	os.WriteFile(w.root("r/q/folder.json"), []byte("{"), 0o644)
	w.fail("show-folder", `{"folder":"/r/q"}`, errs.Corrupt, "")
	w.fail("update-folder", `{"folder":"/r/q","title":"x"}`, errs.Corrupt, "")
	// list shows each folder's fields; the folder with no folder file has
	// no ID, and no warning (doctor reports it); the unusable one, a warning.
	env := w.run("list", `{"include_folders":true}`)
	if len(env.Warnings) != 1 || env.Warnings[0].Kind != errs.WarnUnusableFile {
		t.Errorf("warnings %+v", env.Warnings)
	}
	w.ok("update-folder", `{"folder":"/r","title":"R"}`)
	ids := map[string]any{}
	titles := map[string]any{}
	for _, r := range w.ok("list", `{"include_folders":true}`)["folders"].([]any) {
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
	w.ok("create", `{"title":"a","folder":"/p"}`) // /p is 1, the task 2
	w.ok("create-folder", `{"folder":"/q"}`)      // 3
	w.ok("create-folder", `{"folder":"/r"}`)      // 4
	os.WriteFile(w.root("q/folder.json"), []byte(`{"schema":1,"id":2,"title":null,"extra":{}}`), 0o644)
	os.WriteFile(w.root("r/folder.json"), []byte(`{"schema":1,"id":4}`), 0o644)
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
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a","folder":"/gone"},{"title":"b","blocked_by":["a"]}]}`)
	// /gone is 1, a 2, b 3. The rewrite of task 3 starts with a temp
	// file; fail it.
	w.env.FS = fsys.FS{Hook: fsys.ErrnoAt(fsys.OpCreateTemp, "", 1, syscall.ENOSPC)}
	w.fail("delete-folder", `{"folder":"/gone","recursive":true}`, errs.IO, "")
	w.env.FS = fsys.FS{}
	if tk := task(w.ok("show", `{"id":3}`)); tk["readiness"] != "blocked" {
		t.Errorf("the dependent was freed though delete-folder failed: %v", tk)
	}
	fs := w.ok("doctor", `{}`)["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["kind"] != "dangling-reference" {
		t.Errorf("doctor: %v", fs)
	}
}

// delete-folder removes each folder's folder file after everything else in
// it, so a removal cut short leaves no folder without one; and it removes a
// symlink without following it.
func TestDeleteFolderRemovesFolderFilesLast(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/p/q","notes":"n"}`)
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
	w.ok("delete-folder", `{"folder":"/p","recursive":true,"force":true}`)
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

// A recursive delete-folder removes material only when forced: it lists
// what it found, at any depth but never inside it. Hidden entries don't
// count.
func TestDeleteFolderRefusesMaterial(t *testing.T) {
	w := ready(t)
	w.ok("create", `{"title":"a","folder":"/p/q","notes":"n"}`)
	os.WriteFile(w.disk("@p/.gitignore"), nil, 0o644)
	os.MkdirAll(w.disk("@p/.obsidian"), 0o755)
	w.ok("delete-folder", `{"folder":"/p","recursive":true}`) // only koan's and hidden entries

	w.ok("create", `{"title":"b","folder":"/p/q"}`)
	os.MkdirAll(w.disk("@p/@q/docs/deep"), 0o755)
	os.WriteFile(w.disk("@p/@q/docs/deep/plan.md"), nil, 0o644)
	os.WriteFile(w.disk("@p/@stray.txt"), nil, 0o644)
	e := w.fail("delete-folder", `{"folder":"/p","recursive":true}`, errs.Conflict, errs.RuleHasMaterial)
	want := []string{w.disk("@p/@stray.txt"), w.disk("@p/@q/docs")}
	if !slices.Equal(e.Paths, want) {
		t.Errorf("paths %v, want %v", e.Paths, want)
	}
	if len(e.ChangedPaths) != 0 {
		t.Errorf("changed %v", e.ChangedPaths)
	}
	got := w.ok("delete-folder", `{"folder":"/p","recursive":true,"force":true}`)
	wantIDs(t, "deleted", got["deleted"], 6)
	if _, err := os.Stat(w.disk("@p")); !os.IsNotExist(err) {
		t.Errorf("the folder is still there: %v", err)
	}
}
