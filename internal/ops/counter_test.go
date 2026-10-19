package ops

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
)

// lowCounter is a tree whose counter is below its IDs, as a clone or a
// restore onto a machine with a state file of its own leaves it: /a is 1,
// its tasks 2 to 10, and last_id is 3.
func lowCounter(t *testing.T) *world {
	t.Helper()
	w := ready(t)
	tasks := make([]string, 9)
	for i := range tasks {
		tasks[i] = `{"title":"t"}`
	}
	w.ok("batch", `{"folder":"/a","tasks":[`+strings.Join(tasks, ",")+`]}`)
	w.setCounter(3)
	return w
}

// setCounter rewrites the state file's last_id, keeping its claims.
func (w *world) setCounter(n int64) {
	w.t.Helper()
	data, err := os.ReadFile(w.env.StatePath())
	if err != nil {
		w.t.Fatal(err)
	}
	var st map[string]any
	json.Unmarshal(data, &st)
	st["last_id"] = n
	data, _ = json.Marshal(st)
	if err := os.WriteFile(w.env.StatePath(), data, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) counter() float64 {
	w.t.Helper()
	return w.ok("info", `{}`)["last_id"].(float64)
}

// aboveCounter reports whether env warned id-above-counter, naming id.
func aboveCounter(env Envelope, id model.ID) bool {
	return slices.ContainsFunc(env.Warnings, func(wn errs.Warning) bool {
		return wn.Kind == errs.WarnIDAboveCounter && slices.Equal(wn.IDs, []model.ID{id})
	})
}

// Every command that issues IDs starts above the tree's highest ID, not
// only the counter's, and says so when the tree was ahead.
func TestIssueStartsAboveTheTree(t *testing.T) {
	for _, c := range []struct {
		name, input string
		id          func(map[string]any) float64 // the first ID the command issued
	}{
		{"task create", `{"title":"x","folder":"/b"}`, func(r map[string]any) float64 { return r["id"].(float64) - 1 }},
		{"folder create", `{"folder":"/b"}`, func(r map[string]any) float64 { return r["id"].(float64) }},
		{"batch", `{"tasks":[{"title":"x","folder":"/b"}]}`, func(r map[string]any) float64 { return r["ids"].([]any)[0].(float64) - 1 }},
		{"task move", `{"id":2,"to":"/b"}`, nil},
		{"folder move", `{"folder":"/a","to":"/b/a"}`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := lowCounter(t)
			env := w.run(c.name, c.input)
			if !env.OK {
				t.Fatalf("%s: %s", c.name, env.Error)
			}
			if !aboveCounter(env, 10) {
				t.Errorf("warnings %+v, want id-above-counter naming 10", env.Warnings)
			}
			// /b took 11 in every case.
			if id, e := w.holderOf("/b"); e != nil || id != 11 {
				t.Errorf("/b has ID %v (%v), want 11", id, e)
			}
			if c.id != nil {
				if got := c.id(result(env)); got != 11 {
					t.Errorf("first ID %v, want 11", got)
				}
			}
			if f := w.findings(); len(f) != 0 {
				t.Errorf("findings %v", f)
			}
			// The counter caught up: the next command has nothing to say.
			env = w.run("task create", `{"title":"y","folder":"/c"}`)
			if !env.OK || len(env.Warnings) != 0 {
				t.Errorf("next create: %+v", env)
			}
		})
	}
}

// result is a successful envelope's result, as JSON-decoded data.
func result(env Envelope) map[string]any {
	data, _ := json.Marshal(env.Result)
	var r map[string]any
	json.Unmarshal(data, &r)
	return r
}

// holderOf is the ID of the folder at path p.
func (w *world) holderOf(p string) (float64, *errs.Error) {
	env := w.run("folder show", `{"folder":"`+p+`"}`)
	if !env.OK {
		return 0, env.Error
	}
	id, _ := result(env)["folder"].(map[string]any)["id"].(float64)
	return id, nil
}

// Notes left by a task that is gone still hold their ID.
func TestIssueCountsNotes(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/a"}`)
	os.WriteFile(w.root("a/9.md"), []byte("old"), 0o644)
	env := w.run("task create", `{"title":"x","folder":"/a"}`)
	if !env.OK || result(env)["id"] != float64(10) || !aboveCounter(env, 9) {
		t.Errorf("create beside orphan notes 9: %+v", env)
	}
}

// A counter that is already ahead of the tree stays where it is: deleted
// IDs are never issued again.
func TestIssueKeepsAHigherCounter(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`)
	w.ok("task create", `{"title":"b"}`)
	w.ok("task delete", `{"id":2}`)
	env := w.run("task create", `{"title":"c"}`)
	if !env.OK || result(env)["id"] != float64(3) || len(env.Warnings) != 0 {
		t.Errorf("create after deleting the highest: %+v", env)
	}
}

// A folder file koan can't read can't be counted: a warning, not a refusal.
func TestIssueWarnsOnAnUnreadableFolderFile(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/a"}`)
	os.WriteFile(w.root("a/folder.json"), []byte("{"), 0o644)
	env := w.run("task create", `{"title":"x"}`)
	if !env.OK || !slices.ContainsFunc(env.Warnings, func(wn errs.Warning) bool { return wn.Kind == errs.WarnUnusableFile }) {
		t.Errorf("create with an unreadable folder file: %+v", env)
	}
}

// init raises a counter that is below the tree, and says so.
func TestInitRaisesTheCounter(t *testing.T) {
	w := lowCounter(t)
	got := w.ok("init", `{}`)
	if got["counter_raised"] != true || len(got["created"].([]any)) != 0 {
		t.Errorf("init on a low counter: %v", got)
	}
	if n := w.counter(); n != 10 {
		t.Errorf("last_id %v, want 10", n)
	}
	if got := w.ok("init", `{}`); got["counter_raised"] != false {
		t.Errorf("second init: %v", got)
	}
	// A counter above the tree is left alone.
	w.setCounter(20)
	if got := w.ok("init", `{}`); got["counter_raised"] != false || w.counter() != 20 {
		t.Errorf("init on a high counter: %v, last_id %v", got, w.counter())
	}
}

// init keeps the claims of a state file it raises.
func TestInitRaiseKeepsClaims(t *testing.T) {
	w := lowCounter(t)
	w.ok("task claim", `{"id":2}`)
	w.ok("init", `{}`)
	if got := w.ok("task show", `{"id":2}`); task(got)["claim"] == nil {
		t.Error("init dropped a claim")
	}
}

// A rerun of init on a tree it can't list fails, as a first run does.
func TestInitRerunNeedsTheWholeTree(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can list anything")
	}
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/locked"}`)
	os.Chmod(w.root("locked"), 0)
	defer os.Chmod(w.root("locked"), 0o755)
	w.fail("init", `{}`, errs.IO, "")
}

// A folder file's ID counts as much as a task's: the counter is raised past
// it, and the warning names the folder file.
func TestIssueCountsFolderFiles(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/a"}`) // 1
	os.WriteFile(w.root("a/folder.json"), []byte(strings.Replace(w.file("a/folder.json"), `"id": 1`, `"id": 50`, 1)), 0o644)
	env := w.run("task create", `{"title":"x"}`)
	if !env.OK || result(env)["id"] != float64(51) || !aboveCounter(env, 50) ||
		!slices.ContainsFunc(env.Warnings, func(wn errs.Warning) bool { return slices.Contains(wn.Paths, w.root("a/folder.json")) }) {
		t.Errorf("create beside folder 50: %+v", env)
	}
}
