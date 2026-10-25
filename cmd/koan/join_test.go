package main_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// waitsOn is the IDs a task waits on, as show gives them, sorted.
func waitsOn(t *testing.T, home, ref string) []int64 {
	t.Helper()
	var out []int64
	for _, e := range field(t, home, ref, "blocked_by").([]any) {
		var id int64
		if _, err := fmt.Sscan(fmt.Sprint(e.(map[string]any)["id"]), &id); err != nil {
			t.Fatalf("blocked_by of %s: %v", ref, e)
		}
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// usageAt runs a command line that must be usage, exit 2, with its
// problem at field.
func usageAt(t *testing.T, home, field string, args ...string) *testrun.Result {
	t.Helper()
	r := koan(t, home, args...)
	if r.Exit != 2 || r.Kind() != "usage" || problemFields(r) != field {
		t.Errorf("koan %q: exit %d, want usage at %q: %s", args, r.Exit, field, r.Line())
	}
	return r
}

// The join rows of the rules doc's "Changes, in the example above", each
// on a fresh copy of the example. The first ID a change issues is 16.
func TestJoinExamples(t *testing.T) {
	t.Run("join 4 @1", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "join", "4", "@1")
		if fmt.Sprint(r.Result()) != "map[changed:true gates_made:[map[end:start folder:map[id:1 path:/work] id:16]] id:4 reblocked:[]]" {
			t.Errorf("result: %s", r.Line())
		}
		if got := waitsOn(t, home, "4"); fmt.Sprint(got) != "[12 16]" {
			t.Errorf("@4 waits on %v", got)
		}
		if got := waitsOn(t, home, "15"); fmt.Sprint(got) != "[4 12]" {
			t.Errorf("@1.done waits on %v", got)
		}
	})
	t.Run("join 8 @1", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "join", "8", "@1")
		if fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:1 path:/work] id:16]]" {
			t.Errorf("result: %s", r.Line())
		}
		if got := waitsOn(t, home, "8"); fmt.Sprint(got) != "[11 16]" {
			t.Errorf("@8 waits on %v", got)
		}
		if got := waitsOn(t, home, "15"); fmt.Sprint(got) != "[8 12]" {
			t.Errorf("@1.done waits on %v", got)
		}
	})
	t.Run("join 10 @7", func(t *testing.T) {
		home := example(t)
		r := fails(t, home, "conflict", "cycle", "join", "10", "@7")
		golden(t, r, "join-cycle")
		if fmt.Sprint(r.Error()["ids"]) != "[12 10 13]" || problemFields(r) != "/join/0" {
			t.Errorf("cycle: %s", r.Line())
		}
		if got := waitsOn(t, home, "10"); fmt.Sprint(got) != "[13]" {
			t.Errorf("a refused join wrote an edge: @10 waits on %v", got)
		}
	})
	t.Run("join 8 @7", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "join", "8", "@7")
		if fmt.Sprint(r.Result()) != "map[changed:false gates_made:[] id:8 reblocked:[]]" {
			t.Errorf("result: %s", r.Line())
		}
	})
	t.Run("join @7.start @1", func(t *testing.T) {
		home := example(t)
		r := fails(t, home, "invalid-input", "", "join", "@7.start", "@1")
		if problemFields(r) != "/ref" {
			t.Errorf("problems: %s", r.Line())
		}
	})
	t.Run("join @9 @1", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "join", "@9", "@1")
		if fmt.Sprint(r.Result()) != "map[changed:true gates_made:[map[end:start folder:map[id:1 path:/work] id:16]] id:9 reblocked:[]]" {
			t.Errorf("result: %s", r.Line())
		}
		if got := waitsOn(t, home, "13"); fmt.Sprint(got) != "[12 16]" {
			t.Errorf("@9.start waits on %v", got)
		}
		if got := waitsOn(t, home, "15"); fmt.Sprint(got) != "[12 14]" {
			t.Errorf("@1.done waits on %v", got)
		}
	})
	t.Run("join @1 @9", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "join", "@1", "@9")
		if fmt.Sprint(r.Result()) != "map[changed:true gates_made:[map[end:start folder:map[id:1 path:/work] id:16]] id:1 reblocked:[]]" {
			t.Errorf("result: %s", r.Line())
		}
		if got := waitsOn(t, home, "16"); fmt.Sprint(got) != "[13]" {
			t.Errorf("@1.start waits on %v", got)
		}
		if got := waitsOn(t, home, "14"); fmt.Sprint(got) != "[10 15]" {
			t.Errorf("@9.done waits on %v", got)
		}
	})
	t.Run("join @7 @1", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "join", "@7", "@1")
		if fmt.Sprint(r.Result()) != "map[changed:true gates_made:[map[end:start folder:map[id:1 path:/work] id:16]] id:7 reblocked:[11]]" {
			t.Errorf("result: %s", r.Line())
		}
		if got := waitsOn(t, home, "11"); fmt.Sprint(got) != "[16]" {
			t.Errorf("@7.start waits on %v", got)
		}
		if got := waitsOn(t, home, "15"); fmt.Sprint(got) != "[12]" {
			t.Errorf("@1.done waits on %v", got)
		}
	})
	t.Run("folder create /work/phase3 --join @1", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "folder", "create", "/work/phase3", "--join", "@1")
		res := r.Result()
		if fmt.Sprint(res["created"]) != "[map[id:16 path:/work/phase3]]" ||
			fmt.Sprint(res["gates_made"]) != "[map[end:start folder:map[id:16 path:/work/phase3] id:17] map[end:done folder:map[id:16 path:/work/phase3] id:18] map[end:start folder:map[id:1 path:/work] id:19]]" ||
			fmt.Sprint(res["reblocked"]) != "[]" {
			t.Errorf("result: %s", r.Line())
		}
		if got := waitsOn(t, home, "17"); fmt.Sprint(got) != "[19]" {
			t.Errorf("/work/phase3.start waits on %v", got)
		}
		if got := waitsOn(t, home, "15"); fmt.Sprint(got) != "[12 18]" {
			t.Errorf("@1.done waits on %v", got)
		}
		// Its done gate waits on nothing, and is ready at once.
		if field(t, home, "18", "readiness") != "ready" {
			t.Errorf("the new done gate is not ready")
		}
	})
	t.Run("unjoin 10 @9", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "unjoin", "10", "@9")
		if fmt.Sprint(r.Result()) != "map[changed:true id:10 released:[10 14]]" {
			t.Errorf("result: %s", r.Line())
		}
	})
	t.Run("unjoin 4 @7", func(t *testing.T) {
		home := example(t)
		r := ok(t, home, "unjoin", "4", "@7")
		if fmt.Sprint(r.Result()) != "map[changed:false id:4 released:[]]" {
			t.Errorf("result: %s", r.Line())
		}
		if got := waitsOn(t, home, "4"); fmt.Sprint(got) != "[12]" {
			t.Errorf("@4 waits on %v", got)
		}
	})
}

// --join on task create writes both edges, making the start gate, then
// the done gate; beside --blocked-by naming the same start gate, that
// edge is written once.
func TestTaskCreateJoin(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")
	r := ok(t, home, "task", "create", "x", "--join", "/a")
	golden(t, r, "task-create-join")
	if fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:1 path:/a] id:3] map[end:done folder:map[id:1 path:/a] id:4]]" {
		t.Errorf("gates: %s", r.Line())
	}
	if got := waitsOn(t, home, "2"); fmt.Sprint(got) != "[3]" {
		t.Errorf("@2 waits on %v", got)
	}
	if got := waitsOn(t, home, "4"); fmt.Sprint(got) != "[2]" {
		t.Errorf("/a.done waits on %v", got)
	}

	home = root(t)
	ok(t, home, "folder", "create", "/a")
	r = ok(t, home, "task", "create", "x", "--blocked-by", "/a.start", "--join", "/a")
	if fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:1 path:/a] id:3] map[end:done folder:map[id:1 path:/a] id:4]]" {
		t.Errorf("gates: %s", r.Line())
	}
	if got := waitsOn(t, home, "2"); fmt.Sprint(got) != "[3]" {
		t.Errorf("@2 waits on %v", got)
	}

	// A task that sits in one folder and is a member of another, joined
	// to two at once.
	ok(t, home, "folder", "create", "/b")
	r = ok(t, home, "task", "create", "y", "--folder", "/a", "--join", "/b,/a")
	if fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:5 path:/b] id:7] map[end:done folder:map[id:5 path:/b] id:8]]" {
		t.Errorf("gates: %s", r.Line())
	}
	if got := waitsOn(t, home, "6"); fmt.Sprint(got) != "[3 7]" {
		t.Errorf("@6 waits on %v", got)
	}

	// An end, the root, or a task where a folder joined is taken.
	for _, join := range []string{"/a.start", "@1.done", "/", "2"} {
		r := fails(t, home, "invalid-input", "", "task", "create", "z", "--join", join)
		if problemFields(r) != "/join/0" {
			t.Errorf("--join %s: %s", join, r.Line())
		}
	}
}

// folder create --join makes the new folder's gates, then those of the
// folder joined, and writes the two edges; without --join it makes no
// gates.
func TestFolderCreateJoin(t *testing.T) {
	home := root(t)
	r := ok(t, home, "folder", "create", "/f")
	if fmt.Sprint(r.Result()["gates_made"]) != "[]" || fmt.Sprint(r.Result()["reblocked"]) != "[]" {
		t.Errorf("without --join: %s", r.Line())
	}
	r = ok(t, home, "folder", "create", "/f/g", "--join", "/f")
	golden(t, r, "folder-create-join")
	if fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:2 path:/f/g] id:3] map[end:done folder:map[id:2 path:/f/g] id:4] map[end:start folder:map[id:1 path:/f] id:5] map[end:done folder:map[id:1 path:/f] id:6]]" {
		t.Errorf("gates: %s", r.Line())
	}
	if got := waitsOn(t, home, "3"); fmt.Sprint(got) != "[5]" {
		t.Errorf("/f/g.start waits on %v", got)
	}
	if got := waitsOn(t, home, "6"); fmt.Sprint(got) != "[4]" {
		t.Errorf("/f.done waits on %v", got)
	}

	// A missing parent the same command makes can be joined.
	home = root(t)
	r = ok(t, home, "folder", "create", "/s/impl", "--join", "/s")
	if fmt.Sprint(r.Result()["created"]) != "[map[id:1 path:/s] map[id:2 path:/s/impl]]" ||
		fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:2 path:/s/impl] id:3] map[end:done folder:map[id:2 path:/s/impl] id:4] map[end:start folder:map[id:1 path:/s] id:5] map[end:done folder:map[id:1 path:/s] id:6]]" {
		t.Errorf("ids: %s", r.Line())
	}

	// Itself, an end, or the root is invalid-input, and nothing is made.
	home = root(t)
	for _, join := range []string{"/g", "/g.start", "/"} {
		r := fails(t, home, "invalid-input", "", "folder", "create", "/g", "--join", join)
		if problemFields(r) != "/join/0" {
			t.Errorf("--join %s: %s", join, r.Line())
		}
	}
	checkTree(t, home)
}

// A join's warnings: an edge into a complete done gate warns
// complete-blocker, and a claimed, ready task made to wait warns
// claimed-blocked and is reblocked.
func TestJoinWarnings(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")
	ok(t, home, "task", "create", "x", "--blocks", "/a")
	ok(t, home, "complete", "3")
	r := warned(t, home, []string{"complete-blocker"}, "task", "create", "y", "--join", "/a")
	if fmt.Sprint(r.Envelope["warnings"].([]any)[0].(map[string]any)["ids"]) != "[3]" {
		t.Errorf("warning: %s", r.Line())
	}
	ok(t, home, "task", "create", "z")
	warned(t, home, []string{"complete-blocker"}, "join", "6", "/a")

	// A claimed, ready task joined to a folder whose start gate is new.
	home = root(t)
	ok(t, home, "folder", "create", "/b")
	ok(t, home, "task", "create", "x")
	ok(t, home, "claim", "2")
	r = warned(t, home, []string{"claimed-blocked"}, "join", "2", "/b")
	golden(t, r, "join-claimed-blocked")
	if fmt.Sprint(r.Result()["reblocked"]) != "[2]" {
		t.Errorf("reblocked: %s", r.Line())
	}

	// folder create --join: a done gate complete, then one claimed and
	// ready.
	home = root(t)
	ok(t, home, "folder", "create", "/c")
	ok(t, home, "task", "create", "x", "--blocks", "/c")
	ok(t, home, "complete", "3")
	r = warned(t, home, []string{"complete-blocker"}, "folder", "create", "/c/d", "--join", "/c")
	if fmt.Sprint(r.Result()["reblocked"]) != "[]" {
		t.Errorf("reblocked: %s", r.Line())
	}
	home = root(t)
	ok(t, home, "folder", "create", "/c")
	ok(t, home, "task", "create", "x", "--blocks", "/c")
	ok(t, home, "complete", "2")
	ok(t, home, "claim", "3")
	r = warned(t, home, []string{"claimed-blocked"}, "folder", "create", "/e", "--join", "/c")
	golden(t, r, "folder-create-join-claimed-blocked")
	if fmt.Sprint(r.Result()["reblocked"]) != "[3]" {
		t.Errorf("reblocked: %s", r.Line())
	}
}

// join writes a task's two edges once: joining again changes nothing.
// The folders are the words after the subject, comma-separated or not.
func TestTaskJoin(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")
	ok(t, home, "folder", "create", "/b")
	ok(t, home, "task", "create", "x")
	r := ok(t, home, "join", "3", "/a")
	golden(t, r, "join")
	r = ok(t, home, "join", "@3", "/a")
	if fmt.Sprint(r.Result()) != "map[changed:false gates_made:[] id:3 reblocked:[]]" {
		t.Errorf("again: %s", r.Line())
	}

	// /a,/b is /a /b.
	lines := map[string]string{}
	for _, args := range [][]string{{"/a,/b"}, {"/a", "/b"}} {
		home := root(t)
		ok(t, home, "folder", "create", "/a")
		ok(t, home, "folder", "create", "/b")
		ok(t, home, "task", "create", "x")
		r := ok(t, home, append([]string{"join", "3"}, args...)...)
		lines[fmt.Sprint(args)] = r.Line()
	}
	if lines["[/a,/b]"] != lines["[/a /b]"] {
		t.Errorf("forms differ: %v", lines)
	}

	// Through --input, as the words would give it.
	home = root(t)
	ok(t, home, "folder", "create", "/a")
	ok(t, home, "task", "create", "x")
	r = ok(t, home, "join", "-i", writeInput(t, `{"ref": 2, "join": ["/a"]}`))
	golden(t, r, "join-input-task")
	if fmt.Sprint(r.Result()["changed"]) != "true" {
		t.Errorf("--input: %s", r.Line())
	}
	golden(t, input(t, home, "unjoin", `{"ref": "@2", "join": ["/a"]}`), "unjoin-input")
	for _, in := range []string{`{"ref": 2}`, `{"ref": 2, "join": []}`} {
		r := fails(t, home, "invalid-input", "", "join", "-i", writeInput(t, in))
		if problemFields(r) != "/join" {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	fails(t, home, "invalid-input", "", "unjoin", "-i", writeInput(t, `{"ref": 2, "join": []}`))

	// The old subject fields are fields join doesn't take, and ref is
	// then missing.
	r = fails(t, home, "invalid-input", "", "join", "-i", writeInput(t, `{"folder": "/s", "join": ["/a"]}`))
	golden(t, r, "join-input-old-field")
	if problemFields(r) != "/folder /ref" {
		t.Errorf("old field: %s", r.Line())
	}
	r = fails(t, home, "invalid-input", "", "unjoin", "-i", writeInput(t, `{"task": 2, "join": ["/a"]}`))
	if problemFields(r) != "/ref /task" {
		t.Errorf("old field: %s", r.Line())
	}
}

// The command line: the folders are words after the subject, at least
// one, and there is no --join option.
func TestJoinUsage(t *testing.T) {
	home := root(t)
	for _, args := range [][]string{{"join"}, {"unjoin"}} {
		invalidAt(t, home, "/ref", args...)
		invalidAt(t, home, "/join", append(args, "42")...)
		usageAt(t, home, "--join", append(args, "42", "--join", "/a")...)
		usageAt(t, home, "--join", append(args, "42", "/a", "--join", "/b")...)
	}
	golden(t, usageAt(t, home, "--join", "join", "42", "/a", "--join", "/b"), "join-join")
}

// A gate is never join's or unjoin's subject, made or not, however it is
// written; the root is never a folder joined, nor the subject; a folder
// can't join itself.
func TestJoinRefused(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")
	ok(t, home, "folder", "create", "/b")
	ok(t, home, "task", "create", "x", "--blocks", "/a")
	gates := map[string]string{"/a.done": "join-gate-path", "@1.done": "join-gate-id", "4": "join-gate-own-id", "/b.start": "join-gate-not-made"}
	for _, cmd := range []string{"join", "unjoin"} {
		for _, subject := range []string{"/a.done", "@1.done", "4", "/a.start", "@1.start", "/b.start"} {
			r := fails(t, home, "invalid-input", "", cmd, subject, "/b")
			if problemFields(r) != "/ref" {
				t.Errorf("%s %s: %s", cmd, subject, r.Line())
			}
			if name, ok := gates[subject]; ok && cmd == "join" {
				golden(t, r, name)
			}
		}
		r := fails(t, home, "invalid-input", "", cmd, "3", "/")
		if problemFields(r) != "/join/0" {
			t.Errorf("%s 3 /: %s", cmd, r.Line())
		}
		r = fails(t, home, "invalid-input", "", cmd, "3", "/a.start")
		if problemFields(r) != "/join/0" {
			t.Errorf("%s 3 /a.start: %s", cmd, r.Line())
		}
		r = fails(t, home, "invalid-input", "", cmd, "/", "/a")
		if problemFields(r) != "/ref" {
			t.Errorf("%s / /a: %s", cmd, r.Line())
		}
		if cmd == "join" {
			golden(t, r, "join-root")
		}
		r = fails(t, home, "invalid-input", "", cmd, "/a", "/b", "/")
		if problemFields(r) != "/join/1" {
			t.Errorf("%s /a /b /: %s", cmd, r.Line())
		}
		// Material is no subject.
		r = fails(t, home, "invalid-input", "", cmd, "@1:x", "/b")
		if problemFields(r) != "/ref" {
			t.Errorf("%s @1:x /b: %s", cmd, r.Line())
		}
	}
	r := fails(t, home, "invalid-input", "", "join", "/b", "/b")
	golden(t, r, "join-folder-itself")
	if problemFields(r) != "/join/0" {
		t.Errorf("join /b /b: %s", r.Line())
	}
	// Nothing refused made a gate: /b has none.
	r = ok(t, home, "task", "list", "/b", "--fields", "id", "--state", "open,complete")
	if fmt.Sprint(r.Result()["tasks"]) != "[]" {
		t.Errorf("a refused change made a gate: %s", r.Line())
	}
}

// join writes a folder's two edges through the gates; joining again
// changes nothing. A folder is named by path or by ID, through the
// command line or -i.
func TestFolderJoin(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/g")
	ok(t, home, "folder", "create", "/h")
	r := ok(t, home, "join", "/g", "/h")
	golden(t, r, "join-folder")
	if fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:1 path:/g] id:3] map[end:done folder:map[id:1 path:/g] id:4] map[end:start folder:map[id:2 path:/h] id:5] map[end:done folder:map[id:2 path:/h] id:6]]" {
		t.Errorf("gates: %s", r.Line())
	}
	r = ok(t, home, "join", "1", "/h")
	if fmt.Sprint(r.Result()) != "map[changed:false gates_made:[] id:1 reblocked:[]]" {
		t.Errorf("again: %s", r.Line())
	}

	home = root(t)
	ok(t, home, "folder", "create", "/g")
	ok(t, home, "folder", "create", "/h")
	r = ok(t, home, "join", "@1", "2")
	golden(t, r, "join-folder-by-id")
	if got := waitsOn(t, home, "3"); fmt.Sprint(got) != "[5]" {
		t.Errorf("/g.start waits on %v", got)
	}
	if got := waitsOn(t, home, "6"); fmt.Sprint(got) != "[4]" {
		t.Errorf("/h.done waits on %v", got)
	}

	home = root(t)
	ok(t, home, "folder", "create", "/g")
	ok(t, home, "folder", "create", "/h")
	r = input(t, home, "join", `{"ref": "/g", "join": ["/h"]}`)
	golden(t, r, "join-input-folder")
	if fmt.Sprint(r.Result()["id"]) != "1" || fmt.Sprint(r.Result()["changed"]) != "true" {
		t.Errorf("-i: %s", r.Line())
	}
}

// unjoin removes a task's two edges, or the one there, and changes
// nothing with neither; it never makes a gate.
func TestTaskUnjoin(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")
	ok(t, home, "task", "create", "x", "--join", "/a")
	r := ok(t, home, "unjoin", "2", "/a")
	golden(t, r, "unjoin")
	if fmt.Sprint(r.Result()) != "map[changed:true id:2 released:[2 4]]" {
		t.Errorf("both: %s", r.Line())
	}
	ok(t, home, "join", "2", "/a")
	r = ok(t, home, "unjoin", "@2", "1")
	golden(t, r, "unjoin-task-by-id")
	if fmt.Sprint(r.Result()) != "map[changed:true id:2 released:[2 4]]" {
		t.Errorf("by ID: %s", r.Line())
	}
	ok(t, home, "block", "2", "/a.start")
	r = ok(t, home, "unjoin", "2", "/a")
	if fmt.Sprint(r.Result()) != "map[changed:true id:2 released:[2]]" {
		t.Errorf("one: %s", r.Line())
	}
	r = ok(t, home, "unjoin", "2", "/a")
	if fmt.Sprint(r.Result()) != "map[changed:false id:2 released:[]]" {
		t.Errorf("neither: %s", r.Line())
	}

	// From a folder with no gates: nothing changes, and no gate is made.
	ok(t, home, "folder", "create", "/b")
	r = ok(t, home, "unjoin", "2", "/b")
	if fmt.Sprint(r.Result()) != "map[changed:false id:2 released:[]]" {
		t.Errorf("no gates: %s", r.Line())
	}
	r = ok(t, home, "task", "list", "/b", "--fields", "id", "--state", "open,complete")
	if fmt.Sprint(r.Result()["tasks"]) != "[]" {
		t.Errorf("unjoin made a gate: %s", r.Line())
	}
}

// unjoin removes a folder's two edges, or the one there; unjoining a
// folder from itself changes nothing, and beside another removes only
// that one's edges.
func TestFolderUnjoin(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/g")
	ok(t, home, "folder", "create", "/h")
	ok(t, home, "join", "/g", "/h")
	r := ok(t, home, "unjoin", "/g", "/h")
	golden(t, r, "unjoin-folder")
	if fmt.Sprint(r.Result()) != "map[changed:true id:1 released:[3 6]]" {
		t.Errorf("both: %s", r.Line())
	}
	ok(t, home, "block", "/g.start", "/h.start")
	r = ok(t, home, "unjoin", "/g", "/h")
	if fmt.Sprint(r.Result()) != "map[changed:true id:1 released:[3]]" {
		t.Errorf("one: %s", r.Line())
	}
	r = ok(t, home, "unjoin", "/g", "/h")
	if fmt.Sprint(r.Result()) != "map[changed:false id:1 released:[]]" {
		t.Errorf("neither: %s", r.Line())
	}

	ok(t, home, "join", "/g", "/h")
	r = ok(t, home, "unjoin", "/g", "/g")
	if fmt.Sprint(r.Result()) != "map[changed:false id:1 released:[]]" {
		t.Errorf("itself: %s", r.Line())
	}
	r = ok(t, home, "unjoin", "/g", "/g", "/h")
	if fmt.Sprint(r.Result()) != "map[changed:true id:1 released:[3 6]]" {
		t.Errorf("itself and /h: %s", r.Line())
	}

	// A subject with no gates: nothing changes, and none is made.
	ok(t, home, "folder", "create", "/k")
	r = ok(t, home, "unjoin", "/k", "/h")
	golden(t, r, "unjoin-folder-missing-gate")
	if fmt.Sprint(r.Result()) != "map[changed:false id:7 released:[]]" {
		t.Errorf("no gates: %s", r.Line())
	}
	r = ok(t, home, "task", "list", "/k", "--fields", "id", "--state", "open,complete")
	if fmt.Sprint(r.Result()["tasks"]) != "[]" {
		t.Errorf("unjoin made a gate: %s", r.Line())
	}
}
