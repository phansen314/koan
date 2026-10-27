package main_test

import (
	"fmt"
	"strings"
	"testing"
)

// edgeKinds are the kinds this file checks: those recorded from rows,
// and from edges. Derived state records the rest.
var edgeKinds = append([]string{"blocked", "unblocked"}, rowKinds...)

// edgeChange runs a command that must succeed and gives the events of
// edgeKinds it recorded, as log list returns them, checked to be one
// change.
func edgeChange(t *testing.T, home string, args ...string) []eventRecord {
	t.Helper()
	before := lastSeq(t, home)
	koanOK(t, home, args...)
	return listedAfter(t, home, before)
}

// listedAfter is the events of edgeKinds after seq before, as log list
// returns them, checked to be one change.
func listedAfter(t *testing.T, home string, before int64) []eventRecord {
	t.Helper()
	got := logList(t, home, "--after", fmt.Sprint(before), "--kinds", strings.Join(edgeKinds, ","), "--limit", "null").Events
	if all := logged(t, home, before, nil); len(all) > 0 {
		oneChange(t, all)
	}
	return got
}

// edgeEvent is a blocked or unblocked event as a test expects it: the
// task that waits, its folder's path, and its blocker.
type edgeEvent struct {
	kind    string
	id      int64
	path    string
	blocker int64
}

// sameEdges checks events are exactly the edge events wanted, in order.
func sameEdges(t *testing.T, what string, got []eventRecord, want []edgeEvent) {
	t.Helper()
	var edges []eventRecord
	for _, e := range got {
		if e.Kind == "blocked" || e.Kind == "unblocked" {
			edges = append(edges, e)
		}
	}
	if len(edges) != len(want) {
		t.Fatalf("%s: %d edge events, want %d: %+v", what, len(edges), len(want), got)
	}
	for i, w := range want {
		e := edges[i]
		data := fmt.Sprintf(`{"blocker":%d}`, w.blocker)
		if e.Kind != w.kind || e.ID == nil || *e.ID != w.id || e.Folder.Path != w.path || string(e.Data) != data {
			t.Errorf("%s, event %d: got %s %v %s %s, want %s %d %s %s", what, i, e.Kind, e.ID, e.Folder.Path, e.Data, w.kind, w.id, w.path, data)
		}
	}
}

// block and unblock record one event per edge they add or remove, on the
// task that waits, in the order given: an edge already there, or not
// there, records nothing, and an edge given twice records once.
func TestEventsBlockUnblock(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/w")
	a := newID(t, home, "task", "create", "a")
	b := newID(t, home, "task", "create", "b")
	c := newID(t, home, "task", "create", "c", "--folder", "/w")
	koanOK(t, home, "block", fmt.Sprint(c), "--blocked-by", fmt.Sprint(a))
	// c already waits on a: the edge to b alone.
	goldenEvents(t, "events-block-edges", edgeChange(t, home, "block", fmt.Sprint(c), "--blocked-by", fmt.Sprintf("%d,%d", a, b)))
	// Twice, and each end written another way: once, at its first, on the
	// task that waits, wherever it is.
	d := newID(t, home, "task", "create", "d")
	sameEdges(t, "given twice", edgeChange(t, home, "block", fmt.Sprint(d), "--blocks", fmt.Sprintf("%d,%d", c, c), "--blocked-by", fmt.Sprint(a)),
		[]edgeEvent{{"blocked", d, "/", a}, {"blocked", c, "/w", d}})
	// Everything already there: nothing.
	before := lastSeq(t, home)
	koanOK(t, home, "block", fmt.Sprint(c), "--blocked-by", fmt.Sprint(a))
	if got := logged(t, home, before, nil); len(got) != 0 {
		t.Errorf("a block that changed nothing recorded: %+v", got)
	}
	// Unblock: one edge there, one not, one given twice.
	sameEdges(t, "unblock", edgeChange(t, home, "unblock", fmt.Sprint(c), "--blocked-by", fmt.Sprintf("%d,%d,%d", b, b, a), "--blocks", fmt.Sprint(a)),
		[]edgeEvent{{"unblocked", c, "/w", b}, {"unblocked", c, "/w", a}})
	before = lastSeq(t, home)
	koanOK(t, home, "unblock", fmt.Sprint(c), "--blocked-by", fmt.Sprint(b))
	koanOK(t, home, "unblock", fmt.Sprint(c), "--blocked-by", "/w.start")
	if got := logged(t, home, before, nil); len(got) != 0 {
		t.Errorf("an unblock that changed nothing recorded: %+v", got)
	}
}

// task create records blocked for each edge it writes, its blocked_by,
// then its blocks, then its joins', after the created of the task and
// the gates its edges name.
func TestEventsTaskCreateEdges(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/j")
	a := newID(t, home, "task", "create", "a")
	b := newID(t, home, "task", "create", "b")
	goldenEvents(t, "events-task-create-edges", edgeChange(t, home, "task", "create", "x", "--blocked-by", fmt.Sprintf("%d,%d", a, a),
		"--blocks", fmt.Sprint(b), "--join", "/j"))
}

// A join is its two edges, two blocked events after its gates' created;
// joining again records nothing, and unjoining records two unblocked.
func TestEventsJoinEdges(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/f")
	koanOK(t, home, "folder", "create", "/g")
	x := newID(t, home, "task", "create", "x")
	goldenEvents(t, "events-join-edges", edgeChange(t, home, "join", fmt.Sprint(x), "/f"))
	before := lastSeq(t, home)
	koanOK(t, home, "join", fmt.Sprint(x), "/f")
	if got := logged(t, home, before, nil); len(got) != 0 {
		t.Errorf("joining again recorded: %+v", got)
	}
	got := edgeChange(t, home, "unjoin", fmt.Sprint(x), "/f")
	if len(got) != 2 || got[0].Kind != "unblocked" || got[1].Kind != "unblocked" {
		t.Errorf("unjoin: %+v", got)
	}
	// A folder joins through its gates, each made first.
	goldenEvents(t, "events-join-folder-edges", edgeChange(t, home, "join", "/g", "/f"))
	goldenEvents(t, "events-folder-create-join-edges", edgeChange(t, home, "folder", "create", "/s/impl", "--join", "/s"))
}

// A batch records blocked for each edge, after every created: its folder
// parts' joins first, then each task part's edges, in input order.
func TestEventsBatchEdges(t *testing.T) {
	home := root(t)
	before := lastSeq(t, home)
	batchOK(t, home, `{"folders": [{"path": "/p"}, {"path": "/q", "join": ["/p"]}],
  "tasks": [{"label": "$x", "title": "x", "folder": "/p"},
            {"label": "$z", "title": "z"},
            {"title": "y", "blocked_by": ["$x", "$x", "/p.start"], "blocks": ["$z"], "join": ["/q"]}]}`)
	goldenEvents(t, "events-batch-edges", listedAfter(t, home, before))
}

// delete records unblocked for each edge between what goes and what
// stays, by task, then blocker, each where its waiting task was, before
// its unclaimed and deleted; an edge between two that go records
// nothing.
func TestEventsDeleteEdges(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/d")
	a := newID(t, home, "task", "create", "a", "--folder", "/d")
	b := newID(t, home, "task", "create", "b", "--folder", "/d")
	c := newID(t, home, "task", "create", "c")
	d := newID(t, home, "task", "create", "d")
	e := newID(t, home, "task", "create", "e")
	koanOK(t, home, "block", fmt.Sprint(b), "--blocked-by", fmt.Sprint(a))
	koanOK(t, home, "block", fmt.Sprint(c), "--blocked-by", fmt.Sprintf("%d,%d", b, a))
	koanOK(t, home, "block", fmt.Sprint(a), "--blocked-by", fmt.Sprint(e))
	koanOK(t, home, "block", fmt.Sprint(d), "--blocked-by", fmt.Sprint(b))
	koanOK(t, home, "claim", fmt.Sprint(b))
	got := edgeChange(t, home, "delete", fmt.Sprint(b), fmt.Sprint(a))
	goldenEvents(t, "events-delete-edges", got)
	sameEdges(t, "delete", got, []edgeEvent{{"unblocked", a, "/d", e}, {"unblocked", c, "/", a}, {"unblocked", c, "/", b}, {"unblocked", d, "/", b}})
	// A folder's: its gates and tasks go, and only edges out of it record.
	koanOK(t, home, "folder", "create", "/e")
	in := newID(t, home, "task", "create", "in", "--folder", "/e", "--blocked-by", "/e.start")
	koanOK(t, home, "block", fmt.Sprint(c), "--blocked-by", "/e.done")
	sameEdges(t, "a folder deleted", edgeChange(t, home, "delete", "/e", "--contents"), []edgeEvent{{"unblocked", c, "/", in + 2}})
}

// archive records unblocked for each edge between what stays and what
// leaves, by task, then blocker, before its archived; an edge between two
// that leave, in one subject or two, records nothing.
func TestEventsArchiveEdges(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/f")
	x := newID(t, home, "task", "create", "x", "--folder", "/f")
	y := newID(t, home, "task", "create", "y")
	z := newID(t, home, "task", "create", "z")
	o := newID(t, home, "task", "create", "o")
	koanOK(t, home, "block", fmt.Sprint(x), "--blocked-by", fmt.Sprintf("%d,%d", y, z))
	koanOK(t, home, "block", fmt.Sprint(o), "--blocked-by", fmt.Sprintf("%d,%d", x, z))
	koanOK(t, home, "block", "/f.done", "--blocked-by", fmt.Sprint(o))
	koanOK(t, home, "complete", fmt.Sprint(x), fmt.Sprint(y), fmt.Sprint(z), "/f.done")
	got := edgeChange(t, home, "archive", "/f", fmt.Sprint(z))
	done := o + 1
	sameEdges(t, "archive", got, []edgeEvent{{"unblocked", x, "/f", y}, {"unblocked", o, "/", x}, {"unblocked", o, "/", z}, {"unblocked", done, "/f", o}})
	var kinds []string
	for _, e := range got {
		kinds = append(kinds, e.Kind)
	}
	if want := "unblocked unblocked unblocked unblocked archived archived archived archived"; strings.Join(kinds, " ") != want {
		t.Errorf("archive's events: %s, want %s", strings.Join(kinds, " "), want)
	}
}
