package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/schematest"
	"github.com/phansen314/koan/internal/store"
)

func TestDoctorAndRepair(t *testing.T) {
	w := ready(t)
	if got := w.ok("doctor", `{}`); got["healthy"] != true {
		t.Fatalf("a new tree is not healthy: %v", got)
	}
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]},{"title":"c","notes":"keep"},{"title":"d"}]}`)
	w.ok("task move", `{"id":3,"to":"/p"}`) // /p takes ID 5, after the tasks
	w.ok("task claim", `{"id":1}`)

	os.Remove(w.root("1.json"))                       // dangling reference in 2, stale claim, orphan notes (empty)
	os.WriteFile(w.root(".koan-tmp-abc"), nil, 0o644) // temp leftover
	os.Rename(w.root("p/3.json"), w.root("3.json"))   // notes left in /p: orphan, movable
	data, _ := os.ReadFile(w.root("4.json"))          // id above counter
	os.WriteFile(w.root("9.json"), []byte(strings.Replace(string(data), `"id": 4`, `"id": 9`, 1)), 0o644)
	os.WriteFile(w.root("p/4.json"), data, 0o644)          // duplicate
	os.WriteFile(w.root("p/6.json"), []byte("{}"), 0o644)  // unusable (and above the counter)
	os.Symlink("nowhere", w.root("link"))                  // skipped entry
	os.WriteFile(w.root("p/7.md"), []byte("stray"), 0o644) // orphan with content and no task

	found := map[string]int{}
	for _, f := range w.ok("doctor", `{}`)["findings"].([]any) {
		f := f.(map[string]any)
		found[f["kind"].(string)]++
		if f["repairable"] == true && !slices.Contains(errs.RepairableKinds, f["kind"].(string)) {
			t.Errorf("repairable, but not a kind repair takes: %v", f)
		}
	}
	want := map[string]int{"temp-leftover": 1, "id-above-counter": 1, "dangling-reference": 1, "stale-claim": 1,
		"orphan-notes": 3, "duplicate-id": 1, "unusable-file": 1, "skipped-entry": 1}
	for k, n := range want {
		if found[k] != n {
			t.Errorf("doctor: %d %s findings, want %d (all: %v)", found[k], k, n, found)
		}
	}

	// A plain repair leaves the dangling reference and the claim on the task
	// that is gone, which only repair by name fixes.
	byName := []string{"dangling-reference", "stale-claim"}
	left := func(got map[string]any) map[string]int {
		n := map[string]int{}
		for _, f := range got["findings"].([]any) {
			f := f.(map[string]any)
			n[f["kind"].(string)]++
			if f["repairable"] == true && !slices.Contains(byName, f["kind"].(string)) {
				t.Errorf("repair left a repairable finding: %v", f)
			}
		}
		return n
	}
	got := left(w.ok("repair", `{}`))
	if got["duplicate-id"] != 1 || got["unusable-file"] != 1 || got["skipped-entry"] != 1 || got["orphan-notes"] != 1 || got["dangling-reference"] != 1 || got["stale-claim"] != 1 || len(got) != 6 {
		t.Errorf("after repair: %v", got)
	}
	got = left(w.ok("repair", `{"kinds":["dangling-reference","stale-claim"]}`))
	if got["dangling-reference"] != 0 || got["stale-claim"] != 0 || len(got) != 4 {
		t.Errorf("after repair by name: %v", got)
	}
	if n, _ := os.ReadFile(w.root("3.md")); string(n) != "keep" {
		t.Errorf("repair did not move the notes to their task: %q", n)
	}
	if n := w.ok("info", `{}`); n["last_id"] != float64(9) || n["claims"] != float64(0) {
		t.Errorf("after repair: %v", n)
	}
	if again := w.ok("repair", `{}`); len(again["repaired"].([]any)) != 0 {
		t.Errorf("a second repair repaired %v", again["repaired"])
	}
}

// The kinds repair takes are the ones its input schema lists.
func TestRepairKindsMatchTheSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(schematest.Dir(), "repair-input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Properties struct {
			Kinds struct{ Items struct{ Enum []string } }
		}
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Properties.Kinds.Items.Enum; !slices.Equal(got, errs.RepairableKinds) {
		t.Errorf("schema lists %v, repair takes %v", got, errs.RepairableKinds)
	}
}

func TestDoctorFindsCycles(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a"},{"title":"b","blocked_by":["a"]}]}`)
	data, _ := os.ReadFile(w.root("1.json"))
	os.WriteFile(w.root("1.json"), []byte(strings.Replace(string(data), `"blocked_by": []`, `"blocked_by": [2]`, 1)), 0o644)
	fs := w.ok("doctor", `{}`)["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["kind"] != "cycle" {
		t.Fatalf("findings %v", fs)
	}
	wantIDs(t, "cycle", fs[0].(map[string]any)["ids"], 1, 2)
	wantIDs(t, "why stuck", w.ok("task why", `{"id":2}`)["stuck"], 1, 2)
}

func TestDoctorTakesTheLock(t *testing.T) {
	w := ready(t)
	held, e := store.Open(w.env, store.NeedUsable, true)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	w.fail("doctor", `{}`, errs.Busy, "")
	w.fail("init", `{}`, errs.Busy, "")
}

// A command that succeeds leaves nothing for doctor to find.
func TestSuccessLeavesTheTreeHealthy(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a","folder":"/p/q","notes":"n"},{"title":"b","folder":"/p","blocked_by":["a"]},{"title":"c"}]}`)
	for _, step := range [][2]string{
		{"task claim", `{"id":3}`}, {"task update", `{"id":4,"tags_add":["x"]}`}, {"task move", `{"id":3,"to":"/r"}`},
		{"folder move", `{"folder":"/p","to":"/s/p"}`}, {"task done", `{"id":3}`}, {"task reopen", `{"id":3}`},
		{"task claim-next", `{}`}, {"task delete", `{"id":5}`}, {"folder delete", `{"folder":"/s","contents":true}`},
		{"folder delete", `{"folder":"/r","contents":true}`},
	} {
		w.ok(step[0], step[1])
		if got := w.ok("doctor", `{}`); got["healthy"] != true {
			t.Fatalf("after %s %s: %v", step[0], step[1], got["findings"])
		}
	}
}

func TestRepairReportsOnlyWhatItRepaired(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a","folder":"/home"}`) // task 2, in /home, 1
	os.Remove(w.root("home/2.md"))
	for _, dir := range []string{"x", "y"} {
		w.ok("folder create", `{"folder":"/`+dir+`"}`)
		os.WriteFile(w.root(dir, "2.md"), []byte(dir), 0o644)
	}
	got := w.ok("repair", `{}`)
	if len(got["repaired"].([]any)) != 1 || len(got["findings"].([]any)) != 1 {
		t.Errorf("repaired %v, left %v: want one of each", got["repaired"], got["findings"])
	}
}

// repair fixes only the kinds it is given. Without them, it fixes every kind
// but leaves what removes the ID of a task that is gone. It refuses a kind
// it can't fix.
func TestRepairFixesOnlyTheKindsAsked(t *testing.T) {
	w := ready(t)
	w.ok("batch", `{"tasks":[{"ref":"a","title":"a","notes":"keep"},{"title":"b","blocked_by":["a"]},{"title":"c"}]}`)
	w.ok("task claim", `{"id":1}`)
	w.ok("task claim", `{"id":3}`)
	state, _ := os.ReadFile(w.env.StatePath())
	w.ok("task done", `{"id":3}`)
	os.WriteFile(w.env.StatePath(), state, 0o644) // a claim on 3, which is done
	os.Remove(w.root("1.json"))                   // dangling reference in 2, claim on 1, which is gone, and orphan notes
	os.WriteFile(w.root(".koan-tmp-abc"), nil, 0o644)

	kinds := func(fs any) (ks []string) {
		for _, f := range fs.([]any) {
			f := f.(map[string]any)
			ks = append(ks, fmt.Sprintf("%s %v", f["kind"], f["ids"]))
		}
		return ks
	}
	check := func(input string, repaired, left []string) {
		t.Helper()
		got := w.ok("repair", input)
		if r := kinds(got["repaired"]); !slices.Equal(r, repaired) {
			t.Errorf("repair %s: repaired %v, want %v", input, r, repaired)
		}
		if f := kinds(got["findings"]); !slices.Equal(f, left) {
			t.Errorf("repair %s: left %v, want %v", input, f, left)
		}
	}
	check(`{}`, []string{"temp-leftover []", "stale-claim [3]"},
		[]string{"dangling-reference [2 1]", "stale-claim [1]", "orphan-notes [1]"})
	check(`{"kinds":["stale-claim"]}`, []string{"stale-claim [1]"},
		[]string{"dangling-reference [2 1]", "orphan-notes [1]"})
	check(`{"kinds":["temp-leftover","dangling-reference"]}`, []string{"dangling-reference [2 1]"},
		[]string{"orphan-notes [1]"})

	w.fail("repair", `{"kinds":["cycle"]}`, errs.InvalidInput, "")
	w.fail("repair", `{"kinds":["old-format"]}`, errs.InvalidInput, "")
	w.fail("repair", `{"kinds":[]}`, errs.InvalidInput, "")
	w.fail("repair", `{"kinds":["stale-claim","stale-claim"]}`, errs.InvalidInput, "")
}

func TestStaleClaimsInIDOrder(t *testing.T) {
	w := ready(t)
	tasks := make([]string, 10)
	for i := range tasks {
		tasks[i] = `{"title":"t"}`
	}
	w.ok("batch", `{"tasks":[`+strings.Join(tasks, ",")+`]}`)
	w.ok("task claim", `{"id":9}`)
	w.ok("task claim", `{"id":10}`)
	os.Remove(w.root("9.json"))
	os.Remove(w.root("10.json"))
	var got []int64
	for _, f := range w.ok("doctor", `{}`)["findings"].([]any) {
		if f := f.(map[string]any); f["kind"] == "stale-claim" {
			got = append(got, ids(f["ids"])...)
		}
	}
	if len(got) != 2 || got[0] != 9 {
		t.Errorf("stale claims on %v, want 9 then 10", got)
	}
}

// An entry named with @ that is none of koan's is reported, and skipped.
func TestStrayAtNamesAreSkipped(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`)
	os.WriteFile(w.disk("@p/@notes.txt"), nil, 0o644)
	os.WriteFile(w.disk("@folder.json"), nil, 0o644) // the root has no folder file
	os.Mkdir(w.disk("@p/@5.json"), 0o755)
	os.Symlink("nowhere", w.disk("@p/@link"))
	want := []string{"skipped-entry @folder.json", "skipped-entry @p/@5.json", "skipped-entry @p/@link", "skipped-entry @p/@notes.txt"}
	if fs := w.findings(); !slices.Equal(fs, want) {
		t.Errorf("findings %v, want %v", fs, want)
	}
}

// A folder made by hand, without a folder file, is still a folder; repair
// gives it a new ID, above every ID in the tree.
func TestRepairGivesAFolderItsFile(t *testing.T) {
	w := ready(t)
	w.ok("task create", `{"title":"a"}`)
	os.Mkdir(w.root("hand"), 0o755)
	data, _ := os.ReadFile(w.root("1.json"))
	os.WriteFile(w.root("hand/9.json"), []byte(strings.Replace(string(data), `"id": 1`, `"id": 9`, 1)), 0o644)
	if fs := w.findings(); !slices.Equal(fs, []string{"id-above-counter @hand/@9.json", "missing-folder-file @hand"}) {
		t.Errorf("findings %v", fs)
	}
	wantIDs(t, "tasks", w.ok("task list", `{}`)["tasks"], 1, 9)
	got := w.ok("repair", `{"kinds":["missing-folder-file"]}`)
	if len(got["repaired"].([]any)) != 1 {
		t.Errorf("repaired %v", got["repaired"])
	}
	if data, _ := os.ReadFile(w.root("hand/folder.json")); !strings.Contains(string(data), `"id": 10`) {
		t.Errorf("folder file %s, want ID 10", data)
	}
	if n := w.ok("info", `{}`)["last_id"]; n != float64(10) {
		t.Errorf("last_id %v, want 10", n)
	}
}

// A folder file koan can't use is reported as that, never as missing: a
// symlink, or one in another format. repair leaves it, and the counter.
func TestUnusableFolderFiles(t *testing.T) {
	w := ready(t)
	w.ok("folder create", `{"folder":"/p"}`)
	w.ok("folder create", `{"folder":"/q"}`)
	os.Remove(w.root("p/folder.json"))
	os.Symlink("nowhere", w.root("p/folder.json"))
	os.WriteFile(w.root("q/folder.json"), []byte(`{"schema":2,"id":2}`), 0o644)
	want := []string{"unusable-file @p/@folder.json", "unusable-file @q/@folder.json"}
	if fs := w.findings(); !slices.Equal(fs, want) {
		t.Errorf("findings %v, want %v", fs, want)
	}
	if got := w.ok("repair", `{}`); len(got["repaired"].([]any)) != 0 {
		t.Errorf("repaired %v", got["repaired"])
	}
	if n := w.ok("info", `{}`)["last_id"]; n != float64(2) {
		t.Errorf("last_id %v, want 2", n)
	}
	// Nor is a directory in the folder file's place the folder's own.
	w.ok("folder create", `{"folder":"/r"}`)
	os.Remove(w.root("r/folder.json"))
	os.Mkdir(w.root("r/folder.json"), 0o755)
	w.fail("folder delete", `{"folder":"/r"}`, errs.Conflict, errs.RuleNotEmpty)
}

// repair's new folder ID is above an ID that notes alone still hold.
func TestRepairSkipsIDsHeldByNotes(t *testing.T) {
	w := ready(t)
	os.Mkdir(w.root("hand"), 0o755)
	os.WriteFile(w.root("hand/6.md"), []byte("kept"), 0o644)
	w.ok("repair", `{}`)
	if data, _ := os.ReadFile(w.root("hand/folder.json")); !strings.Contains(string(data), `"id": 7`) {
		t.Errorf("folder file %s, want ID 7", data)
	}
}
