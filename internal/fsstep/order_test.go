package fsstep

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/store"
)

// refuse makes the operating system refuse one fault point, and records
// every point reached.
func refuse(t *testing.T, point string) *[]string {
	t.Helper()
	var points []string
	faults(t, func(p string) error {
		points = append(points, p)
		if p == point {
			return errors.New("refused")
		}
		return nil
	})
	return &points
}

func reached(points []string, prefix string) bool {
	return slices.ContainsFunc(points, func(p string) bool { return strings.HasPrefix(p, prefix) })
}

// A folder made at the path of a deleted folder whose remove was refused
// gets its directory only once the old one is gone: its steps wait behind
// the held remove, and never find the old directory and notes done.
func TestOverlapWaitsBehindHeld(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@old/@notes.md=old notes")
	points := refuse(t, "step 1: remove tree/@old")
	w := write(t, s, Step{Op: OpRemove, From: "tree/@old", Dir: true})
	if _, ok := warning(w, "pending-step"); !ok {
		t.Fatalf("warnings %s", describe(w))
	}
	w = write(t, s, Step{Op: OpMkdir, To: "tree/@old"}, Step{Op: OpWrite, To: "tree/@old/@3.md", Content: "new"})
	got, ok := warning(w, "pending-step")
	if !ok || !slices.Equal(got.Paths, absAll(s, "tree/@old", "tree/@old/@3.md")) {
		t.Errorf("warnings %s", describe(w))
	}
	if reached(*points, "step 2:") || reached(*points, "step 3:") {
		t.Errorf("the later change ran: %q", *points)
	}
	checkTree(t, s, sorted("tree/@old/", "tree/@old/@notes.md=old notes"))
	if live, _ := rows(t, s); !slices.Equal(seqs(live), []int64{1, 2, 3}) {
		t.Errorf("live %v", seqs(live))
	}

	store.Fault = nil
	if w := write(t, s); len(w) != 0 {
		t.Errorf("warnings %s", describe(w))
	}
	checkTree(t, s, sorted("tree/@old/", "tree/@old/@notes.md=", "tree/@old/@3.md=new"))
	noPending(t, s)
}

// Paths overlap ignoring case: a move respelling a directory waits behind
// a held write into it, so the write lands before the move, on every
// filesystem.
func TestOverlapIgnoresCase(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@work/@notes.md=w")
	points := refuse(t, "step 1: write tree/@work/"+TempName(1))
	write(t, s, Step{Op: OpWrite, To: "tree/@work/@5.md", Content: "five"})
	w := write(t, s, Step{Op: OpMove, From: "tree/@work", To: "tree/@Work", Dir: true})
	if _, ok := warning(w, "pending-step"); !ok || reached(*points, "step 2:") {
		t.Errorf("warnings %s, points %q", describe(w), *points)
	}
	store.Fault = nil
	if w := write(t, s); len(w) != 0 {
		t.Errorf("warnings %s", describe(w))
	}
	checkTree(t, s, sorted("tree/@Work/", "tree/@Work/@notes.md=w", "tree/@Work/@5.md=five"))
	noPending(t, s)
}

// A later change overlapping a stuck step waits behind it too, as
// pending, never stuck itself: a remove of the directory above a stuck
// move never takes the content the person is to keep.
func TestOverlapWaitsBehindStuck(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@1.md=a", "tree/@a/@notes.md=", "tree/@a/@1.md=b")
	write(t, s, Step{Op: OpMove, From: "tree/@1.md", To: "tree/@a/@1.md"})
	w := write(t, s, Step{Op: OpRemove, From: "tree/@a", Dir: true, Force: true}, Step{Op: OpMkdir, To: "tree/@c"})
	if !slices.Equal(kinds(w), []string{"pending-step", "stuck-step"}) {
		t.Errorf("warnings %s", describe(w))
	}
	if got, _ := warning(w, "pending-step"); !slices.Equal(got.Paths, absAll(s, "tree/@a", "tree/@c")) {
		t.Errorf("pending-step %s", describe(w))
	}
	checkTree(t, s, sorted("tree/@1.md=a", "tree/@a/", "tree/@a/@notes.md=", "tree/@a/@1.md=b"))
	if live, stuck := rows(t, s); !slices.Equal(seqs(live), []int64{2, 3}) || !slices.Equal(seqs(stuck), []int64{1}) {
		t.Errorf("live %v, stuck %v", seqs(live), seqs(stuck))
	}
	// A change on other paths goes on.
	write(t, s, Step{Op: OpMkdir, To: "tree/@d"})
	checkTree(t, s, sorted("tree/@1.md=a", "tree/@a/", "tree/@a/@notes.md=", "tree/@a/@1.md=b", "tree/@d/", "tree/@d/@notes.md="))
}

// Overlap is a path the same, above or below, ignoring case; a shared
// parent alone is no overlap.
func TestOverlaps(t *testing.T) {
	for _, c := range []struct {
		a, b Step
		want bool
	}{
		{Step{Op: OpRemove, From: "tree/@a", Dir: true}, Step{Op: OpMkdir, To: "tree/@a"}, true},
		{Step{Op: OpRemove, From: "tree/@a", Dir: true}, Step{Op: OpWrite, To: "tree/@a/@1.md"}, true},
		{Step{Op: OpWrite, To: "tree/@a/@b/@1.md"}, Step{Op: OpMove, From: "tree/@A", To: "tree/@x/@A", Dir: true}, true},
		{Step{Op: OpWrite, To: "tree/@a/@1.md"}, Step{Op: OpWrite, To: "tree/@a/@2.md"}, false},
		{Step{Op: OpWrite, To: "tree/@ab"}, Step{Op: OpWrite, To: "tree/@a"}, false},
		{Step{Op: OpMove, From: "tree/@w", Via: "tree/@.case-4", To: "tree/@W", Dir: true}, Step{Op: OpMkdir, To: "tree/@.CASE-4/@x"}, true},
		{Step{Op: OpArchive, From: "tree/@1.md", To: "archived/w/@1.md", File: "archived/w/@1.json"}, Step{Op: OpArchive, From: "tree/@2.md", To: "archived/W/@1.json"}, true},
		{Step{Op: OpArchive, From: "tree/@1.md", To: "archived/Work/@1.md", File: "archived/Work/@1.json"}, Step{Op: OpArchive, From: "tree/@2.md", To: "archived/work/@2.md", File: "archived/work/@2.json"}, false},
	} {
		if got := overlaps(c.a, c.b); got != c.want || overlaps(c.b, c.a) != c.want {
			t.Errorf("%+v, %+v: %v", c.a, c.b, got)
		}
	}
}
