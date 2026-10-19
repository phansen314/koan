package ops

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
)

// lowCounter is a tree whose counter is below its IDs, as only an outside
// change can leave it: /a is 1, its tasks 2 to 10, and last_id is 3.
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

// setCounter rewrites @koan.json's last_id.
func (w *world) setCounter(n int64) {
	w.t.Helper()
	var m map[string]any
	json.Unmarshal([]byte(w.file("koan.json")), &m)
	m["last_id"] = n
	data, _ := json.Marshal(m)
	if err := os.WriteFile(w.root("koan.json"), data, 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) counter() float64 {
	w.t.Helper()
	return w.ok("info", `{}`)["last_id"].(float64)
}

// stateFile is the state file's content.
func (w *world) stateFile() string {
	w.t.Helper()
	data, err := os.ReadFile(w.env.StatePath())
	if err != nil {
		w.t.Fatal(err)
	}
	return string(data)
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

// The counter is in @koan.json, and a command that issues IDs raises it
// there.
func TestIssueSavesTheCounterInTheTree(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"title":"a","folder":"/p"},{"title":"b"}]}`)
	if got := w.file("koan.json"); got != "{\n  \"schema\": 4,\n  \"last_id\": 3\n}\n" {
		t.Errorf("@koan.json is %q", got)
	}
	if strings.Contains(w.stateFile(), "last_id") {
		t.Errorf("the state file holds a counter: %s", w.stateFile())
	}
}

// The counter alone decides new IDs: IDs an outside change brought in above
// it are doctor's to find, and repair raises the counter past them.
func TestIssueTakesTheCounter(t *testing.T) {
	w := lowCounter(t)
	env := w.run("folder create", `{"folder":"/b"}`)
	if !env.OK || result(env)["id"] != float64(4) || len(env.Warnings) != 0 {
		t.Fatalf("folder create on a low counter: %+v", env)
	}
	if f := w.findings(); !slices.Contains(f, "id-above-counter @a/@5.json,@a/@6.json,@a/@7.json,@a/@8.json,@a/@9.json,@a/@10.json") {
		t.Errorf("findings %v, want id-above-counter", f)
	}
	w.ok("repair", `{"kinds":["id-above-counter"]}`)
	if n := w.counter(); n != 10 {
		t.Errorf("last_id %v after repair, want 10", n)
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

// A lost state file loses only the claims: init makes a new one, and the
// counter, in the tree, is where it was.
func TestLostStateKeepsTheCounter(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`)
	w.ok("task create", `{"title":"b"}`)
	w.ok("task claim", `{"id":1}`)
	w.ok("task delete", `{"id":2}`)
	os.Remove(w.env.StatePath())
	if got := w.ok("init", `{}`); !slices.Equal(got["created"].([]any), []any{"state"}) {
		t.Errorf("init: %v", got)
	}
	if id := w.ok("task create", `{"title":"c"}`)["id"]; id != float64(3) {
		t.Errorf("new ID %v, want 3: 2 was issued before", id)
	}
	if got := w.ok("task show", `{"id":1}`); task(got)["claim"] != nil {
		t.Error("a claim outlived its state file")
	}
}

// init never changes a @koan.json that is there, its counter included.
func TestInitKeepsTheCounter(t *testing.T) {
	w := lowCounter(t)
	if got := w.ok("init", `{}`); len(got["created"].([]any)) != 0 || w.counter() != 3 {
		t.Errorf("init on a low counter: %v, last_id %v", got, w.counter())
	}
}

// A @koan.json that init makes in a directory holding a tree starts its
// counter at the highest ID there: of a task, of notes, or of a folder file.
func TestInitStartsAboveTheTree(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/a"}`) // 1
	w.ok("task create", `{"title":"t","folder":"/a"}`)
	os.Remove(w.root("koan.json"))
	if got := w.ok("init", `{}`); !slices.Equal(got["created"].([]any), []any{"metadata"}) || w.counter() != 2 {
		t.Errorf("init beside task 2: %v, last_id %v", got, w.counter())
	}
	os.WriteFile(w.root("a/9.md"), []byte("old"), 0o644)
	os.Remove(w.root("koan.json"))
	if w.ok("init", `{}`); w.counter() != 9 {
		t.Errorf("init beside notes 9: last_id %v", w.counter())
	}
	os.WriteFile(w.root("a/folder.json"), []byte(strings.Replace(w.file("a/folder.json"), `"id": 1`, `"id": 50`, 1)), 0o644)
	os.Remove(w.root("koan.json"))
	if w.ok("init", `{}`); w.counter() != 50 {
		t.Errorf("init beside folder 50: last_id %v", w.counter())
	}
}

// A format-3 tree kept its counter in the state file: migrate moves it into
// @koan.json, above every ID in the tree, and keeps the claims.
func TestMigrateMovesTheCounter(t *testing.T) {
	w := lowCounter(t) // IDs to 10
	w.ok("task claim", `{"id":2}`)
	os.WriteFile(w.root("koan.json"), []byte(`{"schema":3}`), 0o644)
	var st map[string]any
	json.Unmarshal([]byte(w.stateFile()), &st)
	st["schema"], st["last_id"] = 1, 7
	data, _ := json.Marshal(st)
	os.WriteFile(w.env.StatePath(), data, 0o600)
	info := w.ok("info", `{}`)
	if info["state"] != "needs-migration" || info["last_id"] != float64(7) {
		t.Errorf("info on format 3: %v", info)
	}
	if got := w.ok("migrate", `{}`); got["from"] != float64(3) || got["to"] != float64(4) {
		t.Errorf("migrate: %v", got)
	}
	if got := w.file("koan.json"); got != "{\n  \"schema\": 4,\n  \"last_id\": 10\n}\n" {
		t.Errorf("@koan.json is %q", got)
	}
	if got := w.ok("task show", `{"id":2}`); task(got)["claim"] == nil {
		t.Error("migrate dropped a claim")
	}
	if f := w.findings(); len(f) != 0 {
		t.Errorf("findings %v", f)
	}
}
