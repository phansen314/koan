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
	json.Unmarshal([]byte(w.fileAt(w.top("@koan.json"))), &m)
	m["last_id"] = n
	data, _ := json.Marshal(m)
	if err := os.WriteFile(w.top("@koan.json"), data, 0o644); err != nil {
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
	if got := w.fileAt(w.top("@koan.json")); got != "{\n  \"schema\": 5,\n  \"last_id\": 3\n}\n" {
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
	os.Remove(w.top("@koan.json"))
	if got := w.ok("init", `{}`); !slices.Equal(got["created"].([]any), []any{"metadata"}) || w.counter() != 2 {
		t.Errorf("init beside task 2: %v, last_id %v", got, w.counter())
	}
	os.WriteFile(w.root("a/9.md"), []byte("old"), 0o644)
	os.Remove(w.top("@koan.json"))
	if w.ok("init", `{}`); w.counter() != 9 {
		t.Errorf("init beside notes 9: last_id %v", w.counter())
	}
	os.WriteFile(w.root("a/folder.json"), []byte(strings.Replace(w.file("a/folder.json"), `"id": 1`, `"id": 50`, 1)), 0o644)
	os.Remove(w.top("@koan.json"))
	if w.ok("init", `{}`); w.counter() != 50 {
		t.Errorf("init beside folder 50: last_id %v", w.counter())
	}
}

// No earlier tree format converts to format 5, which moved the tree into
// tree/: migrate and doctor refuse them, and the files are left alone.
func TestMigrateRefusesEarlierFormats(t *testing.T) {
	w := lowCounter(t)
	for _, old := range []string{`{"schema":3}`, `{"schema":4,"last_id":10}`} {
		os.WriteFile(w.top("@koan.json"), []byte(old), 0o644)
		if info := w.ok("info", `{}`); info["state"] != "needs-migration" {
			t.Errorf("info on %s: %v", old, info)
		}
		w.fail("migrate", `{}`, errs.Conflict, errs.RuleNoConversion)
		w.fail("doctor", `{}`, errs.Conflict, errs.RuleNoConversion)
		if got := w.fileAt(w.top("@koan.json")); got != old {
			t.Errorf("@koan.json is %q", got)
		}
	}
}
