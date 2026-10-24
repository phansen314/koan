package main_test

import (
	"fmt"
	"testing"
)

// gatesIn is the IDs of a folder's gates, start then done, as task list
// gives them.
func gatesIn(t *testing.T, home, folder string) string {
	t.Helper()
	r := ok(t, home, "task", "list", "--folder", folder, "--recursive=false", "--fields", "id", "--state", "open,complete")
	return fmt.Sprint(r.Result()["tasks"])
}

// A task part's join names a folder the same batch makes: both edges are
// written, and its gates made, start then done.
func TestBatchJoin(t *testing.T) {
	home := root(t)
	r := batchOK(t, home, `{"folders": [{"path": "/a"}], "tasks": [{"title": "x", "join": ["/a"]}]}`)
	golden(t, r, "batch-join")
	if fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:1 path:/a] id:3] map[end:done folder:map[id:1 path:/a] id:4]]" {
		t.Errorf("gates: %s", r.Line())
	}
	if got := waitsOn(t, home, "2"); fmt.Sprint(got) != "[3]" {
		t.Errorf("@2 waits on %v", got)
	}
	if got := waitsOn(t, home, "4"); fmt.Sprint(got) != "[2]" {
		t.Errorf("/a.done waits on %v", got)
	}

	// A task part joined to two folders, in the order given, beside a
	// blocked_by naming one of their gates, which is written once.
	home = root(t)
	r = batchOK(t, home, `{"folders": [{"path": "/a"}, {"path": "/b"}],
  "tasks": [{"label": "$x", "title": "x", "blocked_by": ["/a.start"], "join": ["/b", "@1"]}]}`)
	if fmt.Sprint(r.Result()["gates_made"]) != "[map[end:start folder:map[id:1 path:/a] id:4] map[end:start folder:map[id:2 path:/b] id:5] map[end:done folder:map[id:2 path:/b] id:6] map[end:done folder:map[id:1 path:/a] id:7]]" {
		t.Errorf("gates: %s", r.Line())
	}
	if got := waitsOn(t, home, "3"); fmt.Sprint(got) != "[4 5]" {
		t.Errorf("@3 waits on %v", got)
	}
	if waitsOn(t, home, "6")[0] != 3 || waitsOn(t, home, "7")[0] != 3 {
		t.Errorf("the done gates wait on @3")
	}
}

// A folder part's join joins only the folder at its path, its own gates
// first, then those of each folder joined. Folder parts' gates come in
// input order, before those task parts' edges name.
func TestBatchFolderJoin(t *testing.T) {
	home := root(t)
	r := batchOK(t, home, `{
  "folders": [{"path": "/t", "join": ["/s"]}, {"path": "/s/impl", "join": ["/s"]}, {"path": "/u"}],
  "tasks": [{"title": "x", "join": ["/u"]}]
}`)
	golden(t, r, "batch-folder-join")
	// Folders in tree order: /s 1, /s/impl 2, /t 3, /u 4; the task 5.
	gates := ""
	for _, g := range r.Result()["gates_made"].([]any) {
		m := g.(map[string]any)
		gates += fmt.Sprintf(" %v=%v.%v", m["id"], m["folder"].(map[string]any)["path"], m["end"])
	}
	if gates != " 6=/t.start 7=/t.done 8=/s.start 9=/s.done 10=/s/impl.start 11=/s/impl.done 12=/u.start 13=/u.done" {
		t.Errorf("gates:%s", gates)
	}
	for ref, want := range map[string]string{
		"6": "[8]", "9": "[7 11]", "10": "[8]", "5": "[12]", "13": "[5]",
	} {
		if got := waitsOn(t, home, ref); fmt.Sprint(got) != want {
			t.Errorf("@%s waits on %v, want %s", ref, got, want)
		}
	}

	// A missing parent made with the folder doesn't join, and a folder
	// part with "join": [] makes no gates.
	home = root(t)
	r = batchOK(t, home, `{"folders": [{"path": "/m/n", "join": ["/u"]}, {"path": "/u"}, {"path": "/v", "join": []}]}`)
	if fmt.Sprint(r.Result()["folders"]) != "[map[id:1 path:/m] map[id:2 path:/m/n] map[id:3 path:/u] map[id:4 path:/v]]" {
		t.Errorf("folders: %s", r.Line())
	}
	if gatesIn(t, home, "/m") != "[]" || gatesIn(t, home, "/v") != "[]" ||
		gatesIn(t, home, "/m/n") != "[map[id:5] map[id:6]]" || gatesIn(t, home, "/u") != "[map[id:7] map[id:8]]" {
		t.Errorf("gates: %s", r.Line())
	}

	// A folder part joined to itself is an edge to itself.
	home = root(t)
	batchFails(t, home, "invalid-input", "", "/folders/0/join/0", `{"folders": [{"path": "/g", "join": ["/g"]}]}`)
	checkTree(t, home)
}

// The batch's join is the default for task parts that give no join of
// their own: "join": [] opts out, and a part's own join replaces it.
// Folder parts never take it.
func TestBatchJoinDefault(t *testing.T) {
	home := root(t)
	r := batchOK(t, home, `{
  "folders": [{"path": "/a"}, {"path": "/b"}, {"path": "/c"}],
  "join": ["/a"],
  "tasks": [{"title": "x"}, {"title": "y", "join": []}, {"title": "z", "join": ["/b"]}]
}`)
	golden(t, r, "batch-join-default")
	// /a 1, /b 2, /c 3; x 4, y 5, z 6; /a's gates 7, 8, /b's 9, 10.
	for ref, want := range map[string]string{"4": "[7]", "5": "[]", "6": "[9]", "8": "[4]", "10": "[6]"} {
		if got := waitsOn(t, home, ref); fmt.Sprint(got) != want {
			t.Errorf("@%s waits on %v, want %s", ref, got, want)
		}
	}
	if gatesIn(t, home, "/c") != "[]" {
		t.Errorf("a folder part took the batch's join")
	}

	// A folder part that joins doesn't take it either.
	home = root(t)
	r = batchOK(t, home, `{"folders": [{"path": "/a"}, {"path": "/b", "join": ["/c"]}, {"path": "/c"}], "join": ["/a"], "tasks": [{"title": "x"}]}`)
	if got := waitsOn(t, home, "/b.start"); fmt.Sprint(got) != "[7]" {
		t.Errorf("/b.start waits on %v: %s", got, r.Line())
	}
}

// The batch's join is resolved whether or not a task takes it, before
// every other join; a reference in it that names nothing is not-found
// at it.
func TestBatchJoinNotFound(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w") // 1
	golden(t, batchFails(t, home, "not-found", "", "/join/1",
		`{"join": ["/w", "/nope"], "tasks": [{"title": "a", "join": []}]}`), "batch-join-not-found")
	for in, fields := range map[string]string{
		`{"join": ["/nope"], "folders": [{"path": "/x"}]}`:                                                      "/join/0",
		`{"join": ["@99"], "tasks": [{"title": "a", "join": ["/w"]}]}`:                                          "/join/0",
		`{"folder": "/nope", "join": ["/nope"], "tasks": [{"title": "a"}]}`:                                     "/folder",
		`{"join": ["/nope"], "tasks": [{"title": "a", "folder": "/nope"}]}`:                                     "/tasks/0/folder",
		`{"folders": [{"path": "/x", "join": ["/nope"]}], "join": ["/nope2"], "tasks": [{"title": "a"}]}`:       "/join/0",
		`{"folders": [{"path": "/x", "join": ["/nope"]}], "tasks": [{"title": "a", "blocked_by": [98]}]}`:       "/folders/0/join/0",
		`{"tasks": [{"title": "a", "blocked_by": [98]}], "folders": [{"path": "/x", "join": ["/nope"]}]}`:       "/folders/0/join/0",
		`{"tasks": [{"title": "a", "join": ["/nope"], "blocks": [98]}]}`:                                        "/tasks/0/blocks/0",
		`{"tasks": [{"title": "a", "join": ["/nope"]}, {"title": "b", "blocked_by": [98]}]}`:                    "/tasks/0/join/0",
		`{"folders": [{"path": "/x", "join": ["/w/y"]}, {"path": "/w/x", "join": ["/w/z"]}]}`:                   "/folders/0/join/0",
		`{"tasks": [{"title": "a", "join": ["/w/y"]}], "folders": [{"path": "/w/x", "join": ["/w"]}]}`:          "/tasks/0/join/0",
		`{"tasks": [{"label": "$a", "title": "a"}, {"title": "b", "join": ["/w", 3]}]}`:                         "/tasks/1/join/1",
		`{"folders": [{"path": "/q"}], "join": [3], "tasks": [{"title": "a"}]}`:                                 "/join/0",
		`{"folders": [{"path": "/q"}], "join": ["@2/r"], "tasks": [{"title": "a", "join": []}]}`:                "/join/0",
		`{"folders": [{"path": "/q", "join": ["@1/x"]}, {"path": "/w/y", "join": ["/w"]}]}`:                     "/folders/0/join/0",
		`{"folders": [{"path": "/w/y", "join": ["/w"]}], "tasks": [{"title": "a", "join": ["/w/y/z"]}]}`:        "/tasks/0/join/0",
		`{"folders": [{"path": "/q"}], "tasks": [{"title": "a", "join": ["/q"]}, {"title": "b", "join": [9]}]}`: "/tasks/1/join/0",
	} {
		batchFails(t, home, "not-found", "", fields, in)
	}
	checkTree(t, home, "@w/", "@w/@notes.md=")
	nextID(t, home, 2)
}

// A label, an end or the root in a join is invalid-input at it, the
// batch's join and a folder part's included; two paths differing only
// in case are named wherever they are.
func TestBatchJoinInvalid(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/Work") // 1
	for in, fields := range map[string]string{
		`{"tasks": [{"label": "$a", "title": "a"}, {"title": "b", "join": ["$a"]}]}`:              "/tasks/1/join/0",
		`{"join": ["$a"], "tasks": [{"label": "$a", "title": "a"}]}`:                              "/join/0",
		`{"folders": [{"path": "/x", "join": ["$a"]}], "tasks": [{"label": "$a", "title": "a"}]}`: "/folders/0/join/0",
		`{"join": ["/Work.start", "/"], "tasks": [{"title": "a", "join": ["@1.done", "/"]}]}`:     "/join/0 /join/1 /tasks/0/join/0 /tasks/0/join/1",
		`{"folders": [{"path": "/x", "join": "/Work"}]}`:                                          "/folders/0/join",
		`{"join": "/Work", "tasks": [{"title": "a", "join": {}}]}`:                                "/join /tasks/0/join",
		`{"folders": [{"path": "/x", "join": ["/WORK"]}], "join": ["/work"]}`:                     "/folders/0/join/0 /join/0",
		`{"tasks": [{"title": "a", "join": ["/Work", "/work/b"]}]}`:                               "/tasks/0/join/0 /tasks/0/join/1",
	} {
		batchFails(t, home, "invalid-input", "", fields, in)
	}
	// Against the tree, a path matching a folder only ignoring case is
	// case-mismatch at the join that gave it.
	for in, fields := range map[string]string{
		`{"join": ["/work"], "tasks": [{"title": "a", "join": []}]}`:        "/join/0",
		`{"folders": [{"path": "/x", "join": ["/WORK"]}]}`:                  "/folders/0/join/0",
		`{"tasks": [{"title": "a", "join": ["/work/b"]}]}`:                  "/tasks/0/join/0",
		`{"folders": [{"path": "/Work/x"}], "join": ["@1/X"], "tasks": []}`: "/join/0",
	} {
		batchFails(t, home, "conflict", "case-mismatch", fields, in)
	}
	checkTree(t, home, "@Work/", "@Work/@notes.md=")
	nextID(t, home, 2)
}

// A cycle through a join's edges is refused, naming the join entry once,
// in cycle order: the batch's own at /join/<n>, however many task parts
// take it, and a folder part's, whose gates other parts can name.
func TestBatchJoinCycle(t *testing.T) {
	// The rules example's task join 10 @7, as a batch's join two parts
	// take: the second, waiting on @10, closes it.
	home := example(t)
	r := batchFails(t, home, "conflict", "cycle", "/join/0 /tasks/1/blocked_by/0",
		`{"join": ["@7"], "tasks": [{"title": "a"}, {"title": "b", "blocked_by": [10]}]}`)
	golden(t, r, "batch-join-cycle")
	if fmt.Sprint(r.Error()["ids"]) != "[12 17 10 13]" {
		t.Errorf("cycle: %s", r.Line())
	}
	nextID(t, home, 16)

	// Both of one part's edges from the batch's join lie on the cycle.
	home = root(t)
	ok(t, home, "folder", "create", "/w") // 1
	r = batchFails(t, home, "conflict", "cycle", "/tasks/2/blocks/0 /tasks/2/blocked_by/0 /join/0",
		`{"join": ["/w"], "tasks": [{"title": "a"}, {"title": "b"}, {"title": "c", "join": [], "blocked_by": ["/w"], "blocks": ["/w.start"]}]}`)
	if fmt.Sprint(r.Error()["ids"]) != "[5 4 6 2]" {
		t.Errorf("cycle: %s", r.Line())
	}

	// A task part's own join.
	r = batchFails(t, home, "conflict", "cycle", "/tasks/1/blocks/0 /tasks/1/blocked_by/0 /tasks/0/join/0 /tasks/0/blocked_by/0",
		`{"tasks": [{"title": "a", "blocked_by": ["/w.start"], "join": ["/w"]}, {"title": "b", "blocks": ["/w.start"], "blocked_by": ["/w.done"]}]}`)
	if fmt.Sprint(r.Error()["ids"]) != "[4 3 5 2]" {
		t.Errorf("cycle: %s", r.Line())
	}

	// A folder part's join, closed by a task part naming its gates.
	r = batchFails(t, home, "conflict", "cycle", "/tasks/0/blocks/0 /tasks/0/blocked_by/0 /folders/0/join/0",
		`{"folders": [{"path": "/p", "join": ["/q"]}, {"path": "/q"}], "tasks": [{"title": "t", "blocked_by": ["/q"], "blocks": ["/p"]}]}`)
	if fmt.Sprint(r.Error()["ids"]) != "[6 4 8]" {
		t.Errorf("cycle: %s", r.Line())
	}
	checkTree(t, home, "@w/", "@w/@notes.md=")
	nextID(t, home, 2)
}

// A join's warnings in a batch: an edge into a complete done gate warns
// complete-blocker, and a claimed, ready gate made to wait warns
// claimed-blocked and is reblocked.
func TestBatchJoinWarnings(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/a")                // 1
	ok(t, home, "task", "create", "x", "--blocks", "/a") // 2, /a.done 3
	ok(t, home, "task", "complete", "3")
	warned(t, home, []string{"complete-blocker"}, "batch", "-i", writeInput(t, `{"join": ["/a"], "tasks": [{"title": "y"}]}`))

	home = root(t)
	ok(t, home, "folder", "create", "/c")                // 1
	ok(t, home, "task", "create", "x", "--blocks", "/c") // 2, /c.done 3
	ok(t, home, "task", "complete", "2")
	ok(t, home, "task", "claim", "3")
	r := warned(t, home, []string{"claimed-blocked"}, "batch", "-i", writeInput(t, `{"folders": [{"path": "/e", "join": ["/c"]}]}`))
	if fmt.Sprint(r.Result()["reblocked"]) != "[3]" {
		t.Errorf("reblocked: %s", r.Line())
	}
}
