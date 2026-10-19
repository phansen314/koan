package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
)

// outside is a new directory outside the tree, at its real path.
func outside(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func in(k, v string, more ...string) string {
	m := map[string]any{k: v}
	for i := 0; i+1 < len(more); i += 2 {
		m[more[i]] = more[i+1]
	}
	data, _ := json.Marshal(m)
	return string(data)
}

func bind(folder, dir string) string { return in("folder", folder, "dir", dir) }

func kinds(env Envelope) []string {
	var ks []string
	for _, w := range env.Warnings {
		ks = append(ks, w.Kind)
	}
	return ks
}

func TestMarkerBind(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q"}`) // 1, 2
	wt := outside(t)
	got := w.ok("marker bind", bind("/p/q", wt))
	marker := filepath.Join(wt, ".koan", "marker.json")
	if got["id"] != 2.0 || got["path"] != "/p/q" || got["dir"] != wt || got["marker"] != marker || got["changed"] != true {
		t.Errorf("bind: %v", got)
	}
	if data, _ := os.ReadFile(filepath.Join(wt, ".koan", ".gitignore")); string(data) != "*\n" {
		t.Errorf(".gitignore: %q", data)
	}
	want := fmt.Sprintf("{\n  \"schema\": 1,\n  \"id\": 2,\n  \"path\": \"/p/q\",\n  \"dir\": %q\n}\n", wt)
	if data, _ := os.ReadFile(marker); string(data) != want {
		t.Errorf("marker.json:\n%s", data)
	}
	if got := w.ok("marker bind", bind("@2", wt)); got["changed"] != false {
		t.Errorf("again: %v", got)
	}

	// A directory reached through a link is bound at its real path.
	link := filepath.Join(outside(t), "link")
	os.Symlink(wt, link)
	if got := w.ok("marker bind", bind("/p/q", link)); got["dir"] != wt || got["changed"] != false {
		t.Errorf("through a link: %v", got)
	}

	// Another folder's marker is replaced only when forced.
	e := w.fail("marker bind", bind("/p", wt), errs.Conflict, errs.RuleBoundElsewhere)
	wantIDs(t, "bound-elsewhere", toAny(e.IDs), 2)
}

// toAny is a list of IDs as a decoded result holds them.
func toAny(ids []int64) any {
	out := []any{}
	for _, id := range ids {
		out = append(out, float64(id))
	}
	return out
}

func TestMarkerBindRefuses(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q"}`) // 1, 2
	os.Mkdir(w.root("hand"), 0o755)
	wt := outside(t)

	w.fail("marker bind", bind("/", wt), errs.InvalidInput, "")
	w.fail("marker bind", `{"folder":"/p"}`, errs.InvalidInput, "")
	w.fail("marker bind", bind("/p", "rel"), errs.InvalidInput, "")
	w.fail("marker bind", bind("/p", wt+"/none"), errs.NotFound, "")
	os.WriteFile(wt+"/file", nil, 0o644)
	w.fail("marker bind", bind("/p", wt+"/file"), errs.NotFound, "")
	w.fail("marker bind", bind("/nope", wt), errs.NotFound, "")
	w.fail("marker bind", bind("@9", wt), errs.NotFound, "")
	w.fail("marker bind", bind("/hand", wt), errs.NotFound, "")

	w.fail("marker bind", bind("/p", w.disk()), errs.Conflict, errs.RuleInTree)
	w.fail("marker bind", bind("/p", w.root("p")), errs.Conflict, errs.RuleInTree)
	w.fail("marker bind", bind("/p", w.env.Home), errs.Conflict, errs.RuleHoldsTree)
	w.fail("marker bind", bind("/p", "/"), errs.Conflict, errs.RuleHoldsTree)

	// A checkout's .git is a directory; a worktree's is a file.
	checkout := outside(t)
	os.Mkdir(checkout+"/.git", 0o755)
	w.fail("marker bind", bind("/p", checkout), errs.Conflict, errs.RuleCheckout)
	w.ok("marker bind", `{"folder":"/p","dir":"`+checkout+`","force":true}`)
	worktree := outside(t)
	os.WriteFile(worktree+"/.git", []byte("gitdir: x\n"), 0o644)
	w.ok("marker bind", bind("/p", worktree))

	notDir := outside(t)
	os.WriteFile(notDir+"/.koan", nil, 0o644)
	w.fail("marker bind", bind("/p", notDir), errs.Conflict, errs.RuleNotAFolder)

	// Forced, another folder's marker is replaced.
	w.ok("marker bind", bind("/p/q", wt))
	w.fail("marker bind", bind("/p", wt), errs.Conflict, errs.RuleBoundElsewhere)
	if got := w.ok("marker bind", `{"folder":"/p","dir":"`+wt+`","force":true}`); got["id"] != 1.0 || got["changed"] != true {
		t.Errorf("forced: %v", got)
	}

	// A marker copied here, or one koan can't use, binds nothing, and is
	// replaced without force.
	copied := outside(t)
	os.CopyFS(copied+"/.koan", os.DirFS(wt+"/.koan"))
	if got := w.ok("marker bind", bind("/p/q", copied)); got["changed"] != true {
		t.Errorf("over a copy: %v", got)
	}
	os.WriteFile(copied+"/.koan/marker.json", []byte("{"), 0o644)
	w.ok("marker bind", bind("/p", copied))
}

func TestMarkerLookup(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q"}`)              // 1, 2
	w.ok("task create", `{"title":"Book","folder":"/p/q"}`) // 3
	outer := outside(t)
	inner := filepath.Join(outer, "inner")
	os.MkdirAll(inner+"/src", 0o755)
	w.ok("marker bind", bind("/p", outer))
	w.ok("marker bind", bind("/p/q", inner))

	where := func(file string) map[string]any { return w.ok("where", in("file", file)) }
	for _, c := range []struct {
		file, ref, marker string
	}{
		{outer, "@1", outer},
		{outer + "/other/x.go", "@1", outer},
		{inner, "@2", inner},
		{inner + "/src", "@2", inner},
		{inner + "/src/missing/x.go", "@2", inner},
		{inner + "/src/../src", "@2", inner},
	} {
		got := where(c.file)
		if got["ref"] != c.ref || got["marker"] != filepath.Join(c.marker, ".koan/marker.json") ||
			got["material"] != nil || got["file"] != nil || got["task"] != nil {
			t.Errorf("where %s: %v", c.file, got)
		}
	}
	if got := w.ok("where", `{"ref":"@2"}`); got["marker"] != nil {
		t.Errorf("by ref: %v", got)
	}

	// A .koan with no marker.json, left by a bind cut short, is passed over.
	os.Remove(inner + "/.koan/marker.json")
	if got := where(inner); got["ref"] != "@1" {
		t.Errorf("no marker.json: %v", got)
	}
	// One koan can't use is never passed over.
	os.WriteFile(inner+"/.koan/marker.json", []byte(`{"schema":1}`), 0o644)
	e := w.fail("where", in("file", inner+"/src"), errs.Corrupt, "")
	if !slices.Contains(e.Paths, inner+"/.koan/marker.json") {
		t.Errorf("corrupt paths: %v", e.Paths)
	}
	os.WriteFile(inner+"/.koan/marker.json", []byte(`{"schema":2,"x":1}`), 0o644)
	w.fail("where", in("file", inner), errs.Corrupt, "")

	// One copied from elsewhere binds nothing.
	w.ok("marker bind", bind("/p/q", inner))
	copied := outside(t)
	os.CopyFS(copied+"/.koan", os.DirFS(inner+"/.koan"))
	if e := w.fail("where", in("file", copied), errs.NotFound, ""); !strings.Contains(e.Message, "written for "+inner) {
		t.Errorf("copied: %s", e.Message)
	}

	// A move leaves the marker stale but working; binding again refreshes it.
	w.ok("folder move", `{"folder":"/p/q","to":"/p/r"}`)
	env := w.run("where", in("file", inner))
	if !env.OK || env.Result.(whereResult).Ref != "@2" || !slices.Equal(kinds(env), []string{errs.WarnStaleMarker}) {
		t.Errorf("stale: %+v", env)
	}
	w.ok("marker bind", bind("/p/r", inner))
	if env := w.run("where", in("file", inner)); len(env.Warnings) > 0 {
		t.Errorf("refreshed: %v", env.Warnings)
	}

	// A folder that is gone, or an ID that is a task's, names nothing.
	marker := func(id int) {
		os.WriteFile(inner+"/.koan/marker.json", fmt.Appendf(nil, `{"schema":1,"id":%d,"path":"/p/r","dir":%q}`, id, inner), 0o644)
	}
	marker(3)
	w.fail("where", in("file", inner), errs.NotFound, "")
	marker(9)
	if e := w.fail("where", in("file", inner), errs.NotFound, ""); !strings.Contains(e.Message, "marker") {
		t.Errorf("gone: %s", e.Message)
	}
	w.fail("where", in("file", outside(t)), errs.NotFound, "")
}

func TestMarkerUnbind(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`)
	wt := outside(t)
	w.ok("marker bind", bind("/p", wt))
	os.WriteFile(wt+"/.koan/.koan-tmp-b", nil, 0o644)
	os.WriteFile(wt+"/.koan/.koan-tmp-a", nil, 0o644)
	got := w.ok("marker unbind", in("dir", wt))
	var removed []string
	for _, p := range got["removed"].([]any) {
		removed = append(removed, strings.TrimPrefix(p.(string), wt+"/"))
	}
	if want := []string{".koan/marker.json", ".koan/.koan-tmp-a", ".koan/.koan-tmp-b", ".koan/.gitignore", ".koan"}; !slices.Equal(removed, want) || got["changed"] != true {
		t.Errorf("removed %v, want %v", removed, want)
	}
	if _, err := os.Lstat(wt + "/.koan"); !os.IsNotExist(err) {
		t.Errorf(".koan: %v", err)
	}
	if got := w.ok("marker unbind", in("dir", wt)); got["changed"] != false || len(got["removed"].([]any)) != 0 {
		t.Errorf("again: %v", got)
	}

	// Anything else in .koan refuses it, and nothing is removed.
	w.ok("marker bind", bind("/p", wt))
	os.WriteFile(wt+"/.koan/mine", nil, 0o644)
	e := w.fail("marker unbind", in("dir", wt), errs.Conflict, errs.RuleHasMaterial)
	if !slices.Equal(e.Paths, []string{wt + "/.koan/mine"}) {
		t.Errorf("paths %v", e.Paths)
	}
	if _, err := os.Stat(wt + "/.koan/marker.json"); err != nil {
		t.Errorf("marker removed: %v", err)
	}

	w.fail("marker unbind", in("dir", w.root("p")), errs.Conflict, errs.RuleInTree)
	w.fail("marker unbind", in("dir", wt+"/none"), errs.NotFound, "")
	w.fail("marker unbind", `{}`, errs.InvalidInput, "")
	notDir := outside(t)
	os.WriteFile(notDir+"/.koan", nil, 0o644)
	w.fail("marker unbind", in("dir", notDir), errs.Conflict, errs.RuleNotAFolder)

	// It needs no tree.
	bare := newWorld(t)
	os.Remove(wt + "/.koan/mine")
	if got := bare.ok("marker unbind", in("dir", wt)); got["changed"] != true {
		t.Errorf("without a tree: %v", got)
	}
}

func TestContext(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p","notes":"P notes"}`)             // 1
	w.ok("folder create", `{"folder":"/p/q","title":"Q","notes":"Q"}`)     // 2
	w.ok("folder create", `{"folder":"/p/q/r"}`)                           // 3
	w.ok("task create", `{"title":"Second","folder":"/p/q","priority":9}`) // 4
	w.ok("task create", `{"title":"First","folder":"/p/q"}`)               // 5
	w.ok("task create", `{"title":"Done","folder":"/p/q"}`)                // 6
	w.ok("task done", `{"id":6}`)
	w.ok("task create", `{"title":"Deeper","folder":"/p/q/r"}`) // 7
	w.ok("task create", `{"title":"Top"}`)                      // 8

	got := w.ok("context", `{"folder":"/p/q"}`)
	if got["folder"].(map[string]any)["path"] != "/p/q" || paths(got["ancestors"]) != `["/p"]` ||
		got["notes"] != "Q" || got["parent_notes"] != "P notes" || got["marker"] != nil {
		t.Errorf("context: %v", got)
	}
	wantIDs(t, "tasks", got["tasks"], 4, 5)
	for _, in := range []string{`{"folder":2}`, `{"folder":"@2"}`, `{"file":"` + w.root("p/q") + `"}`,
		`{"file":"` + w.root("p/q/4.md") + `"}`, `{"file":"` + w.root("p/q") + `/docs/x.md"}`} {
		if got := w.ok("context", in); got["folder"].(map[string]any)["path"] != "/p/q" {
			t.Errorf("context %s: %v", in, got["folder"])
		}
	}

	got = w.ok("context", `{"folder":"/p/q/r"}`)
	if paths(got["ancestors"]) != `["/p","/p/q"]` || got["notes"] != "" || got["parent_notes"] != "Q" {
		t.Errorf("deeper: %v", got)
	}
	got = w.ok("context", `{"folder":"/p"}`)
	if len(got["ancestors"].([]any)) != 0 || got["parent_notes"] != nil {
		t.Errorf("in the root: %v", got)
	}
	got = w.ok("context", `{"folder":"/"}`)
	if len(got["ancestors"].([]any)) != 0 || got["notes"] != nil || got["parent_notes"] != nil {
		t.Errorf("root: %v", got)
	}
	wantIDs(t, "root tasks", got["tasks"], 8)

	// From a bound directory.
	wt := outside(t)
	w.ok("marker bind", bind("/p/q", wt))
	got = w.ok("context", in("file", wt))
	if got["folder"].(map[string]any)["path"] != "/p/q" || got["marker"] != wt+"/.koan/marker.json" {
		t.Errorf("bound: %v", got)
	}

	// Notes koan can't read are null, with a warning; so is an ancestor's
	// folder file, whose record has no ID. The folder's own fails it.
	os.Remove(w.root("p/folder.md"))
	os.Mkdir(w.root("p/folder.md"), 0o755)
	os.WriteFile(w.root("p/folder.json"), []byte("{"), 0o644)
	env := w.run("context", `{"folder":"/p/q"}`)
	res := env.Result.(contextResult)
	if !env.OK || res.ParentNotes != nil || res.Ancestors[0].ID != nil ||
		!slices.Equal(kinds(env), []string{errs.WarnUnusableFile, errs.WarnUnusableFile}) {
		t.Errorf("unusable: %+v", env)
	}
	w.fail("context", `{"folder":"/p"}`, errs.Corrupt, "")

	for _, in := range []string{`{}`, `{"folder":"/p","file":"/x"}`, `{"file":"rel"}`, `{"folder":"p"}`} {
		w.fail("context", in, errs.InvalidInput, "")
	}
	w.fail("context", `{"folder":"/nope"}`, errs.NotFound, "")
	w.fail("context", `{"folder":4}`, errs.NotFound, "")
	w.fail("context", in("file", outside(t)), errs.NotFound, "")
}

// A .koan holding what koan did not write is never touched: not by bind,
// not by unbind.
func TestMarkerLeavesWhatIsNotKoans(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`)
	for _, c := range []struct {
		name  string
		setup func(koan string)
		other string // the entry reported, relative to .koan
	}{
		{"its own .gitignore", func(k string) { os.WriteFile(k+"/.gitignore", []byte("!keep\n"), 0o644) }, ".gitignore"},
		{"a file", func(k string) { os.WriteFile(k+"/keep", []byte("hi"), 0o644) }, "keep"},
		{"a directory as marker.json", func(k string) { os.MkdirAll(k+"/marker.json/x", 0o755) }, "marker.json"},
		{"a symlink as .gitignore", func(k string) { os.Symlink("/etc/hostname", k+"/.gitignore") }, ".gitignore"},
	} {
		d := outside(t)
		os.Mkdir(d+"/.koan", 0o755)
		c.setup(d + "/.koan")
		before := snapshotDir(t, d)
		for _, cmd := range []string{"marker bind", "marker unbind"} {
			input := in("dir", d)
			if cmd == "marker bind" {
				input = bind("/p", d)
			}
			e := w.fail(cmd, input, errs.Conflict, errs.RuleHasMaterial)
			if !slices.Equal(e.Paths, []string{filepath.Join(d, ".koan", c.other)}) || len(e.ChangedPaths) != 0 {
				t.Errorf("%s, %s: paths %v, changed %v", c.name, cmd, e.Paths, e.ChangedPaths)
			}
		}
		if after := snapshotDir(t, d); after != before {
			t.Errorf("%s: changed:\n%s\n%s", c.name, before, after)
		}
	}

	// An empty .gitignore, as a first bind cut short leaves it, is koan's.
	d := outside(t)
	os.Mkdir(d+"/.koan", 0o755)
	os.WriteFile(d+"/.koan/.gitignore", nil, 0o644)
	w.ok("marker bind", bind("/p", d))
	if data, _ := os.ReadFile(d + "/.koan/.gitignore"); string(data) != "*\n" {
		t.Errorf(".gitignore: %q", data)
	}
	os.WriteFile(d+"/.koan/.gitignore", nil, 0o644)
	w.ok("marker unbind", in("dir", d))
}

// snapshotDir lists what is under d, with each file's content.
func snapshotDir(t *testing.T, d string) string {
	t.Helper()
	var b strings.Builder
	filepath.WalkDir(d, func(p string, ent os.DirEntry, err error) error {
		data, _ := os.ReadFile(p)
		fmt.Fprintf(&b, "%s %v %q\n", p, ent.Type(), data)
		return nil
	})
	return b.String()
}

// A marker whose folder is gone binds nothing, and is replaced without
// force; so is one naming a task.
func TestMarkerBindReplacesAGoneFolder(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`) // 1
	w.ok("folder create", `{"folder":"/q"}`) // 2
	w.ok("task create", `{"title":"t"}`)     // 3
	d := outside(t)
	w.ok("marker bind", bind("/q", d))
	w.ok("folder delete", `{"folder":"/q"}`)
	if e := w.fail("where", in("file", d), errs.NotFound, ""); !strings.Contains(e.Message, "names folder 2") {
		t.Errorf("after delete: %s", e.Message)
	}
	if e := w.fail("context", in("file", d), errs.NotFound, ""); !strings.Contains(e.Message, "names folder 2") {
		t.Errorf("context after delete: %s", e.Message)
	}
	if got := w.ok("marker bind", bind("/p", d)); got["id"] != 1.0 || got["changed"] != true {
		t.Errorf("rebind: %v", got)
	}
	os.WriteFile(d+"/.koan/marker.json", fmt.Appendf(nil, `{"schema":1,"id":3,"path":"/x","dir":%q}`, d), 0o644)
	w.ok("marker bind", bind("/p", d))
}

func TestMarkerLookupMore(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q"}`) // 1, 2
	wt := outside(t)
	os.MkdirAll(wt+"/src", 0o755)
	w.ok("marker bind", bind("/p/q", wt))

	// Through a link to the directory, the marker is the directory's own.
	link := filepath.Join(outside(t), "link")
	os.Symlink(wt, link)
	if got := w.ok("where", in("file", link+"/src/x.go")); got["ref"] != "@2" || got["marker"] != wt+"/.koan/marker.json" {
		t.Errorf("through a link: %v", got)
	}

	// A .koan that is a link to a marker elsewhere is passed over.
	outer := outside(t)
	inner := filepath.Join(outer, "inner")
	os.Mkdir(inner, 0o755)
	w.ok("marker bind", bind("/p", outer))
	os.Symlink(wt+"/.koan", inner+"/.koan")
	if got := w.ok("where", in("file", inner)); got["ref"] != "@1" {
		t.Errorf("a linked .koan: %v", got)
	}
	w.fail("marker bind", bind("/p", inner), errs.Conflict, errs.RuleNotAFolder)
	w.fail("marker unbind", in("dir", inner), errs.Conflict, errs.RuleNotAFolder)

	// context goes by the same marker: stale after a move, copied, gone.
	w.ok("folder move", `{"folder":"/p/q","to":"/p/r"}`)
	env := w.run("context", in("file", wt+"/src"))
	if !env.OK || env.Result.(contextResult).Folder.Path != "/p/r" || !slices.Equal(kinds(env), []string{errs.WarnStaleMarker}) {
		t.Errorf("context, stale: %+v", env)
	}
	copied := outside(t)
	os.CopyFS(copied+"/.koan", os.DirFS(wt+"/.koan"))
	w.fail("context", in("file", copied), errs.NotFound, "")

	// unbind looks only in the directory it is given.
	if got := w.ok("marker unbind", in("dir", wt+"/src")); got["changed"] != false {
		t.Errorf("unbind below: %v", got)
	}
	if _, err := os.Stat(wt + "/.koan/marker.json"); err != nil {
		t.Errorf("the marker above went: %v", err)
	}
}

// With ~/koan-root a link, the tree is both where the link is and where it
// leads.
func TestMarkerBindAgainstALinkedTree(t *testing.T) {
	w := newWorld(t)
	real := outside(t)
	os.Mkdir(real+"/tree", 0o755)
	os.Symlink(real+"/tree", w.env.RootPath())
	w.ok("init", `{}`)
	w.ok("folder create", `{"folder":"/p"}`)
	w.fail("marker bind", bind("/p", real+"/tree/@p"), errs.Conflict, errs.RuleInTree)
	w.fail("marker bind", bind("/p", real), errs.Conflict, errs.RuleHoldsTree)
	w.fail("marker bind", bind("/p", w.env.Home), errs.Conflict, errs.RuleHoldsTree)
	w.ok("marker bind", bind("/p", outside(t)))
}

// A bind that fails partway says what it wrote.
func TestMarkerBindFailsPartway(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`)
	d := outside(t)
	w.env.FS.Hook = fsys.ErrnoAt(fsys.OpRename, "", 1, syscall.ENOSPC)
	e := w.fail("marker bind", bind("/p", d), errs.IO, "")
	w.env.FS.Hook = nil
	if want := []string{d + "/.koan", d + "/.koan/.gitignore"}; !slices.Equal(e.ChangedPaths, want) {
		t.Errorf("changed_paths %v, want %v", e.ChangedPaths, want)
	}
	w.ok("marker bind", bind("/p", d))
	w.env.FS.Hook = fsys.ErrnoAt(fsys.OpRemove, "", 2, syscall.EACCES)
	e = w.fail("marker unbind", in("dir", d), errs.IO, "")
	w.env.FS.Hook = nil
	if want := []string{d + "/.koan/marker.json"}; !slices.Equal(e.ChangedPaths, want) {
		t.Errorf("unbind changed_paths %v, want %v", e.ChangedPaths, want)
	}
	w.ok("marker unbind", in("dir", d))
}

// Notes that are not a regular file, or can't be read, are null.
func TestContextNotesKoanCantRead(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p/q"}`)
	os.Remove(w.root("p/q/folder.md"))
	if err := syscall.Mkfifo(w.root("p/q/folder.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := w.run("context", `{"folder":"/p/q"}`)
	if !env.OK || env.Result.(contextResult).Notes != nil || !slices.Equal(kinds(env), []string{errs.WarnUnusableFile}) {
		t.Errorf("a FIFO: %+v", env)
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	os.Chmod(w.root("p/folder.md"), 0)
	env = w.run("context", `{"folder":"/p/q"}`)
	if !env.OK || env.Result.(contextResult).ParentNotes != nil || len(env.Warnings) != 2 {
		t.Errorf("unreadable: %+v", env)
	}
}
