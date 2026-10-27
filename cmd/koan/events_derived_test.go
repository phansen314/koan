package main_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// derivedKinds are the kinds of event recorded from derived state.
var derivedKinds = []string{"released", "reblocked", "finished", "unfinished"}

// fullChange runs a command that must succeed and gives every event it
// recorded, as log list returns them, checked to be one change.
func fullChange(t *testing.T, home string, args ...string) []eventRecord {
	t.Helper()
	before := lastSeq(t, home)
	koanOK(t, home, args...)
	return listedAll(t, home, before)
}

// listedAll is every event after seq before, as log list returns them,
// checked to be one change.
func listedAll(t *testing.T, home string, before int64) []eventRecord {
	t.Helper()
	got := logList(t, home, "--after", fmt.Sprint(before), "--limit", "null").Events
	if len(got) > 0 {
		oneChange(t, got)
	}
	return got
}

// derived is an event of derived state as a test expects it: its kind,
// what it is about, and where.
type derived struct {
	kind string
	id   int64
	path string
}

// sameDerived checks the derived events among events are exactly want,
// in order, each with empty data, and that they come after every other
// event of the change.
func sameDerived(t *testing.T, what string, events []eventRecord, want []derived) {
	t.Helper()
	var got []derived
	first := -1
	for i, e := range events {
		if !slices.Contains(derivedKinds, e.Kind) {
			if first >= 0 {
				t.Errorf("%s: %s at %d comes after a derived event", what, e.Kind, e.Seq)
			}
			continue
		}
		if first < 0 {
			first = i
		}
		if string(e.Data) != "{}" {
			t.Errorf("%s: %s of %v carries %s", what, e.Kind, e.ID, e.Data)
		}
		d := derived{kind: e.Kind, path: e.Folder.Path}
		if e.ID != nil {
			d.id = *e.ID
		}
		got = append(got, d)
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: derived events %+v, want %+v", what, got, want)
	}
}

// resultIDs runs a command that must succeed and gives its result's list
// field, with the events it recorded.
func resultIDs(t *testing.T, home, field string, args ...string) ([]int64, []eventRecord) {
	t.Helper()
	before := lastSeq(t, home)
	r := koanOK(t, home, args...)
	var ids []int64
	for _, v := range r.Result()[field].([]any) {
		ids = append(ids, num(v))
	}
	return ids, listedAll(t, home, before)
}

// complete of a folder's last open task records finished for it and for
// each folder above that became finished, deepest first, after the rest
// of the change, and released for what it made ready, as the result
// lists it; a folder still holding open work records nothing.
func TestEventsFinished(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/a/b/c")
	koanOK(t, home, "folder", "create", "/o")
	x := newID(t, home, "task", "create", "x", "--folder", "/a/b/c")
	w := newID(t, home, "task", "create", "w")
	koanOK(t, home, "block", fmt.Sprint(w), "--blocked-by", fmt.Sprint(x))
	goldenEvents(t, "events-complete-finished", fullChange(t, home, "complete", fmt.Sprint(x)))
	// /o was finished before and is still; /a holds y, so only /a/b goes.
	y := newID(t, home, "task", "create", "y", "--folder", "/a")
	z := newID(t, home, "task", "create", "z", "--folder", "/a/b")
	got := fullChange(t, home, "complete", fmt.Sprint(z))
	sameDerived(t, "complete under an open folder", got, []derived{{"finished", 2, "/a/b"}})
	// The root never records finished.
	got = fullChange(t, home, "complete", fmt.Sprint(y), fmt.Sprint(w))
	sameDerived(t, "the last open tasks", got, []derived{{"finished", 1, "/a"}})
}

// reopen records reblocked as its result lists them, then unfinished for
// each finished folder it made hold open work, deepest first.
func TestEventsUnfinished(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/a/b")
	x := newID(t, home, "task", "create", "x", "--folder", "/a/b")
	w := newID(t, home, "task", "create", "w", "--folder", "/a")
	v := newID(t, home, "task", "create", "v")
	koanOK(t, home, "block", fmt.Sprint(v), "--blocked-by", fmt.Sprintf("%d,%d", x, w))
	koanOK(t, home, "complete", fmt.Sprint(x), fmt.Sprint(w))
	reblocked, got := resultIDs(t, home, "reblocked", "reopen", fmt.Sprint(x))
	if !slices.Equal(reblocked, []int64{v}) {
		t.Fatalf("reblocked %v", reblocked)
	}
	goldenEvents(t, "events-reopen-unfinished", got)
	sameDerived(t, "reopen", got, []derived{{"reblocked", v, "/"}, {"unfinished", 2, "/a/b"}, {"unfinished", 1, "/a"}})
}

// mv of an open task between two folders records finished for the one
// it left and unfinished for the one it went to, at one depth by ID; a
// folder above both, holding the task before and after, records nothing,
// though its open count went to none and back.
func TestEventsMvFinished(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/p/y")
	koanOK(t, home, "folder", "create", "/p/x")
	t1 := newID(t, home, "task", "create", "t1", "--folder", "/p/x")
	goldenEvents(t, "events-mv-finished", fullChange(t, home, "mv", fmt.Sprint(t1), "/p/y"))
	// A complete task moving changes nothing derived.
	koanOK(t, home, "complete", fmt.Sprint(t1))
	koanOK(t, home, "folder", "create", "/q")
	sameDerived(t, "a complete task moved", fullChange(t, home, "mv", fmt.Sprint(t1), "/q"), nil)
	// A folder moving takes its open work with it: from /q into /p/x,
	// so /q is finished and /p/x and /p hold open work again.
	koanOK(t, home, "folder", "create", "/q/f")
	koanOK(t, home, "task", "create", "f1", "--folder", "/q/f")
	sameDerived(t, "a folder moved", fullChange(t, home, "mv", "/q/f", "/p/x"),
		[]derived{{"unfinished", 3, "/p/x"}, {"unfinished", 1, "/p"}, {"finished", 5, "/q"}})
}

// unblock records released exactly as its result lists them, each where
// its task is; complete and delete too.
func TestEventsReleased(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/w")
	a := newID(t, home, "task", "create", "a")
	c := newID(t, home, "task", "create", "c", "--folder", "/w")
	b := newID(t, home, "task", "create", "b")
	d := newID(t, home, "task", "create", "d")
	koanOK(t, home, "block", fmt.Sprint(a), "--blocks", fmt.Sprintf("%d,%d,%d", c, b, d))
	koanOK(t, home, "block", fmt.Sprint(d), "--blocked-by", fmt.Sprint(b))
	released, got := resultIDs(t, home, "released", "unblock", fmt.Sprint(a), "--blocks", fmt.Sprintf("%d,%d,%d", d, c, b))
	var want []derived
	for _, id := range released {
		path := "/"
		if id == c {
			path = "/w"
		}
		want = append(want, derived{"released", id, path})
	}
	if len(released) != 2 {
		t.Fatalf("released %v", released)
	}
	goldenEvents(t, "events-unblock-released", got)
	sameDerived(t, "unblock", got, want)
	// delete releases d, and /w, holding c alone, is finished once c goes.
	released, got = resultIDs(t, home, "released", "delete", fmt.Sprint(b), fmt.Sprint(c))
	if !slices.Equal(released, []int64{d}) {
		t.Fatalf("released %v", released)
	}
	goldenEvents(t, "events-delete-released", got)
	sameDerived(t, "delete", got, []derived{{"released", d, "/"}, {"finished", 1, "/w"}})
}

// block, task create and join record reblocked as their results list
// them, and unfinished for a finished folder whose gate they make, a gate
// being a task.
func TestEventsReblocked(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/f")
	x := newID(t, home, "task", "create", "x")
	goldenEvents(t, "events-block-reblocked", fullChange(t, home, "block", fmt.Sprint(x), "--blocked-by", "/f.done"))
	y := newID(t, home, "task", "create", "y")
	reblocked, got := resultIDs(t, home, "reblocked", "task", "create", "n", "--folder", "/f", "--blocks", fmt.Sprint(y))
	if !slices.Equal(reblocked, []int64{y}) {
		t.Fatalf("reblocked %v", reblocked)
	}
	sameDerived(t, "task create", got, []derived{{"reblocked", y, "/"}})
	koanOK(t, home, "folder", "create", "/g")
	z := newID(t, home, "task", "create", "z")
	reblocked, got = resultIDs(t, home, "reblocked", "join", fmt.Sprint(z), "/g")
	if !slices.Equal(reblocked, []int64{z}) {
		t.Fatalf("reblocked %v", reblocked)
	}
	sameDerived(t, "join", got, []derived{{"reblocked", z, "/"}, {"unfinished", 6, "/g"}})
}

// A folder the change makes records its state in its created, and none
// of finished or unfinished; a folder it deletes or archives neither.
func TestEventsDerivedMade(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/n")
	koanOK(t, home, "folder", "create", "/j")
	// /n/m and its gates are made, so /n holds open work; /j's gates are
	// made, so /j does too. /n/m records nothing.
	got := fullChange(t, home, "folder", "create", "/n/m", "--join", "/j")
	goldenEvents(t, "events-folder-create-unfinished", got)
	// A batch: /n/k and /b are made, /n is already unfinished.
	koanOK(t, home, "folder", "create", "/e")
	before := lastSeq(t, home)
	made := batchOK(t, home, `{"folders": [{"path": "/b/c"}], "tasks": [{"title": "t", "folder": "/b/c"}, {"title": "u", "folder": "/e"}]}`)
	u := num(made.Result()["tasks_made"].([]any)[1])
	got = listedAll(t, home, before)
	goldenEvents(t, "events-batch-derived", got)
	sameDerived(t, "batch", got, []derived{{"unfinished", 8, "/e"}})
	// Deleting /e/d, the only open work under /e: /e is finished, and
	// /e/d, deleted, records nothing.
	koanOK(t, home, "folder", "create", "/e/d")
	koanOK(t, home, "mv", fmt.Sprint(u), "/e/d")
	sameDerived(t, "delete", fullChange(t, home, "delete", "/e/d", "--contents"), []derived{{"finished", 8, "/e"}})
	// Archiving takes only finished work, so nothing derived changes.
	koanOK(t, home, "folder", "create", "/e/r")
	r := newID(t, home, "task", "create", "r", "--folder", "/e/r")
	koanOK(t, home, "complete", fmt.Sprint(r))
	sameDerived(t, "archive", fullChange(t, home, "archive", "/e/r"), nil)
}

// Every command the Event record names for each derived kind can record
// it, and only those do.
func TestEventsDerivedCommands(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/f")
	a := newID(t, home, "task", "create", "a", "--folder", "/f")
	b := newID(t, home, "task", "create", "b", "--blocked-by", fmt.Sprint(a))
	koanOK(t, home, "complete", fmt.Sprint(a))
	koanOK(t, home, "reopen", fmt.Sprint(a))
	koanOK(t, home, "unblock", fmt.Sprint(b), "--blocked-by", fmt.Sprint(a))
	koanOK(t, home, "join", fmt.Sprint(b), "/f")
	koanOK(t, home, "unjoin", fmt.Sprint(b), "/f")
	allowed := map[string][]string{
		"released":   {"unblock", "unjoin", "complete", "delete"},
		"reblocked":  {"task create", "folder create", "block", "join", "reopen", "batch"},
		"finished":   {"task create", "folder create", "block", "join", "batch", "complete", "reopen", "mv", "delete"},
		"unfinished": {"task create", "folder create", "block", "join", "batch", "complete", "reopen", "mv", "delete"},
	}
	seen := map[string]bool{}
	for _, e := range logged(t, home, 0, derivedKinds) {
		if !slices.Contains(allowed[e.Kind], e.Command) {
			t.Errorf("%s recorded %s", e.Command, e.Kind)
		}
		seen[e.Kind+" "+e.Command] = true
	}
	for _, want := range []string{"released complete", "reblocked reopen", "released unblock", "reblocked join", "released unjoin",
		"finished complete", "unfinished reopen"} {
		if !seen[want] {
			t.Errorf("no %s", strings.Replace(want, " ", " from ", 1))
		}
	}
}
