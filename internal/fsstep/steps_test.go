package fsstep

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/store"
)

// The scenario is one change with a step of every kind, some depending on
// earlier ones: the move of notes runs inside the directory the case-only
// rename just respelled.
var (
	scenarioBefore = []string{
		"tree/@work/", "tree/@work/@notes.md=work notes", "tree/@work/@1.md=one", "tree/@work/@2.md=", "tree/@work/material.txt=m",
		"tree/@old/", "tree/@old/@notes.md=", "tree/@old/junk.txt=x",
		"tree/@fin/", "tree/@fin/@notes.md=fin", "tree/@fin/@3.md=three",
	}
	scenarioSteps = []Step{
		{Op: OpMkdir, To: "tree/@a"},
		{Op: OpWrite, To: "tree/@a/@5.md", Content: "five"},
		{Op: OpMove, From: "tree/@work", To: "tree/@Work", Dir: true},
		{Op: OpMove, From: "tree/@Work/@1.md", To: "tree/@a/@1.md"},
		{Op: OpRemove, From: "tree/@old", Dir: true, Force: true, Owns: []string{"tree/@old", "tree/@old/@notes.md"}},
		{Op: OpArchive, From: "tree/@fin", To: "archived/@9_fin", File: "archived/@9_fin.json", Dir: true, Rows: json.RawMessage(`{"schema":1,"kind":"folder"}`)},
		{Op: OpArchive, From: "tree/@Work/@2.md", To: "archived/work/@2.md", File: "archived/work/@2.json", Rows: json.RawMessage(`{"schema":1,"kind":"task"}`)},
	}
	scenarioAfter = sorted(
		"tree/@a/", "tree/@a/@notes.md=", "tree/@a/@5.md=five", "tree/@a/@1.md=one",
		"tree/@Work/", "tree/@Work/@notes.md=work notes", "tree/@Work/material.txt=m",
		"archived/@9_fin/", "archived/@9_fin/@notes.md=fin", "archived/@9_fin/@3.md=three",
		"archived/@9_fin.json="+`{"schema":1,"kind":"folder"}`,
		"archived/work/", "archived/work/@2.md=", "archived/work/@2.json="+`{"schema":1,"kind":"task"}`,
	)
)

func scenario(t *testing.T) (string, *store.Store) {
	t.Helper()
	home, s := newRoot(t)
	files(t, s, scenarioBefore...)
	return home, s
}

// Every kind of step, carried out after COMMIT, its rows deleted.
func TestStepKinds(t *testing.T) {
	_, s := scenario(t)
	if w := write(t, s, scenarioSteps...); len(w) != 0 {
		t.Errorf("warnings %s", describe(w))
	}
	checkTree(t, s, scenarioAfter)
	noPending(t, s)
	// Carried out again, the steps change nothing.
	c := New(s.Root)
	var again []store.Pending
	for i, st := range scenarioSteps {
		b, _ := c.Encode(int64(i+1), st)
		again = append(again, store.Pending{Seq: int64(i + 1), Change: 1, Step: b})
	}
	if out := c.Carry(again, nil); len(out.Done) != len(again) || len(out.Stuck) != 0 {
		t.Errorf("again: %+v", out)
	}
	checkTree(t, s, scenarioAfter)
}

// Done when is judged by the listing: notes or a directory already there,
// a hand-made @start.md, say, is the step done, and is left as it is. A
// folder's directory is done once it holds @notes.md.
func TestDoneWhenListed(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@api/", "tree/@api/@start.md=by hand")
	write(t, s,
		Step{Op: OpMkdir, To: "tree/@api"},
		Step{Op: OpWrite, To: "tree/@api/@start.md"},
		Step{Op: OpRemove, From: "tree/@gone.md"},
	)
	checkTree(t, s, sorted("tree/@api/", "tree/@api/@notes.md=", "tree/@api/@start.md=by hand"))
	noPending(t, s)

	files(t, s, "tree/@b/@notes.md=kept")
	write(t, s, Step{Op: OpMkdir, To: "tree/@b"})
	checkTree(t, s, sorted("tree/@api/", "tree/@api/@notes.md=", "tree/@api/@start.md=by hand", "tree/@b/", "tree/@b/@notes.md=kept"))
}

// A case-only rename goes through @.case-<seq>, recording all three
// names, and resumes from whichever the listing shows.
func TestCaseOnly(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@work/", "tree/@work/@notes.md=w")
	var points []string
	faults(t, func(p string) error { points = append(points, p); return nil })
	_, _, e := s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
		seq, err := Add(tx, Step{Op: OpMove, From: "tree/@work", To: "tree/@Work", Dir: true})
		if err != nil {
			return nil, nil, err
		}
		live, _, _ := tx.Pending()
		st, _ := Parse(live[0].Step)
		if st.Via != "tree/"+CaseName(seq) || st.From != "tree/@work" || st.To != "tree/@Work" {
			t.Errorf("step %+v", st)
		}
		return nil, nil, nil
	})
	if e != nil {
		t.Fatal(e.Message)
	}
	if !slices.Contains(points, "step 1: rename tree/@work") || !slices.Contains(points, "step 1: rename tree/@.case-1") {
		t.Errorf("points %q", points)
	}
	checkTree(t, s, sorted("tree/@Work/", "tree/@Work/@notes.md=w"))

	for _, c := range []struct {
		name   string
		before []string
		after  []string
		stuck  string
	}{
		{"from the old name", []string{"tree/@x/@notes.md=n"}, []string{"tree/@X/", "tree/@X/@notes.md=n"}, ""},
		{"from the temporary name", []string{"tree/@.case-9/@notes.md=n"}, []string{"tree/@X/", "tree/@X/@notes.md=n"}, ""},
		{"already done", []string{"tree/@X/@notes.md=n"}, []string{"tree/@X/", "tree/@X/@notes.md=n"}, ""},
		{"an empty old name beside the new", []string{"tree/@x/@notes.md=", "tree/@X/@notes.md=n"}, []string{"tree/@X/", "tree/@X/@notes.md=n"}, ""},
		{"an empty new name beside the temporary", []string{"tree/@.case-9/@notes.md=n", "tree/@X/@notes.md="}, []string{"tree/@X/", "tree/@X/@notes.md=n"}, ""},
		{"content on both", []string{"tree/@x/@notes.md=a", "tree/@X/@notes.md=b"}, []string{"tree/@x/", "tree/@x/@notes.md=a", "tree/@X/", "tree/@X/@notes.md=b"}, Both},
		{"none of the three", nil, nil, Neither},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, s := newRoot(t)
			if len(c.before) == 2 && ignoresCase(t, s) {
				t.Skip("both spellings can't be made where the filesystem ignores case")
			}
			files(t, s, c.before...)
			w := &work{root: s.Root.Dir, seq: 9}
			found, err := w.move(Step{Op: OpMove, From: "tree/@x", To: "tree/@X", Via: "tree/@.case-9", Dir: true})
			if err != nil || found != c.stuck {
				t.Errorf("found %q, %v", found, err)
			}
			checkTree(t, s, sorted(c.after...))
		})
	}
}

// A move that finds both sides finishes when one is empty: a zero-byte
// notes file, or a directory of nothing but zero-byte notes files and any
// .DS_Store. Anything else is content.
func TestBothSides(t *testing.T) {
	for _, c := range []struct {
		name   string
		dir    bool
		before []string
		after  []string
		stuck  bool
	}{
		{"empty source notes", false,
			[]string{"tree/@1.md=", "tree/@a/@1.md=written early"},
			[]string{"tree/@a/", "tree/@a/@1.md=written early"}, false},
		{"empty destination notes", false,
			[]string{"tree/@1.md=mine", "tree/@a/@1.md="},
			[]string{"tree/@a/", "tree/@a/@1.md=mine"}, false},
		{"both notes hold content", false,
			[]string{"tree/@1.md=a", "tree/@a/@1.md=b"},
			[]string{"tree/@1.md=a", "tree/@a/", "tree/@a/@1.md=b"}, true},
		{"empty source directory", true,
			[]string{"tree/@s/@notes.md=", "tree/@s/@4.md=", "tree/@s/.DS_Store=finder", "tree/@a/@d/@notes.md=x"},
			[]string{"tree/@a/", "tree/@a/@d/", "tree/@a/@d/@notes.md=x"}, false},
		{"empty destination directory", true,
			[]string{"tree/@s/@notes.md=x", "tree/@a/@d/@notes.md=", "tree/@a/@d/.DS_Store=finder"},
			[]string{"tree/@a/", "tree/@a/@d/", "tree/@a/@d/@notes.md=x"}, false},
		{"an empty __init__.py is content", true,
			[]string{"tree/@s/__init__.py=", "tree/@a/@d/@notes.md=x"},
			[]string{"tree/@s/", "tree/@s/__init__.py=", "tree/@a/", "tree/@a/@d/", "tree/@a/@d/@notes.md=x"}, true},
		{"an empty @Notes.md is content", true,
			[]string{"tree/@s/@Notes.md=", "tree/@a/@d/@notes.md=x"},
			[]string{"tree/@s/", "tree/@s/@Notes.md=", "tree/@a/", "tree/@a/@d/", "tree/@a/@d/@notes.md=x"}, true},
		{"a subdirectory is content", true,
			[]string{"tree/@s/@sub/", "tree/@a/@d/@notes.md=x"},
			[]string{"tree/@s/", "tree/@s/@sub/", "tree/@a/", "tree/@a/@d/", "tree/@a/@d/@notes.md=x"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, s := newRoot(t)
			files(t, s, c.before...)
			st := Step{Op: OpMove, From: "tree/@1.md", To: "tree/@a/@1.md"}
			if c.dir {
				st = Step{Op: OpMove, From: "tree/@s", To: "tree/@a/@d", Dir: true}
			}
			w := write(t, s, st)
			checkTree(t, s, sorted(c.after...))
			_, stuck := rows(t, s)
			if got := len(stuck) == 1; got != c.stuck {
				t.Errorf("stuck %v, warnings %s", seqs(stuck), describe(w))
			}
		})
	}
}

// A stuck step is set aside with the rest of its change, records what it
// found, and every later result carries stuck-step, naming its source and
// destination; later writes go on, skipping it.
func TestStuck(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@1.md=a", "tree/@a/@1.md=b")
	w := write(t, s,
		Step{Op: OpMove, From: "tree/@1.md", To: "tree/@a/@1.md"},
		Step{Op: OpWrite, To: "tree/@a/@7.md", Content: "after the stuck one"},
		Step{Op: OpMove, From: "tree/@gone", To: "tree/@a/@gone", Dir: true},
	)
	want := absAll(s, "tree/@1.md", "tree/@a/@1.md")
	if got, ok := warning(w, "stuck-step"); !ok || !slices.Equal(got.Paths, want) || len(w) != 1 {
		t.Errorf("warnings %s", describe(w))
	}
	live, stuck := rows(t, s)
	if len(live) != 0 || !slices.Equal(seqs(stuck), []int64{1, 2, 3}) {
		t.Fatalf("live %v, stuck %v", seqs(live), seqs(stuck))
	}
	if st, _ := Parse(stuck[0].Step); st.Stuck != Both {
		t.Errorf("stuck step %+v", st)
	}
	if st, _ := Parse(stuck[1].Step); st.Stuck != "" {
		t.Errorf("the rest of the change %+v", st)
	}

	// A later change goes on; its own dir move finding neither sticks too,
	// and is listed after the first.
	w = write(t, s,
		Step{Op: OpWrite, To: "tree/@a/@8.md", Content: "later"},
		Step{Op: OpMove, From: "tree/@none", To: "tree/@a/@none", Dir: true},
	)
	want = append(want, absAll(s, "tree/@none", "tree/@a/@none")...)
	if got, ok := warning(w, "stuck-step"); !ok || !slices.Equal(got.Paths, want) {
		t.Errorf("warnings %s", describe(w))
	}
	checkTree(t, s, sorted("tree/@1.md=a", "tree/@a/", "tree/@a/@1.md=b", "tree/@a/@8.md=later"))
	if _, stuck := rows(t, s); !slices.Equal(seqs(stuck), []int64{1, 2, 3, 5}) {
		t.Errorf("stuck %v", seqs(stuck))
	}
	if st, _ := Parse(func() []store.Pending { _, st := rows(t, s); return st }()[3].Step); st.Stuck != Neither {
		t.Errorf("neither %+v", st)
	}

	// Every result carries it, a refused one too.
	_, w, e := s.Write(func(tx *store.Tx) (any, []cli.Warning, error) { return nil, nil, errors.New("refused") })
	if e == nil || !slices.Equal(kinds(w), []string{"stuck-step"}) {
		t.Errorf("refused: %v, %s", e, describe(w))
	}
}

// Neither side is stuck only when a directory is needed: notes missing on
// both sides are done, since missing notes read as empty, and archived
// notes that are missing leave the JSON alone.
func TestNeither(t *testing.T) {
	_, s := newRoot(t)
	w := write(t, s,
		Step{Op: OpMove, From: "tree/@1.md", To: "tree/@2.md"},
		Step{Op: OpArchive, From: "tree/@3.md", To: "archived/@3.md", File: "archived/@3.json", Rows: json.RawMessage(`{}`)},
	)
	if len(w) != 0 {
		t.Errorf("warnings %s", describe(w))
	}
	checkTree(t, s, sorted("archived/@3.json={}"))
	noPending(t, s)

	w = write(t, s, Step{Op: OpArchive, From: "tree/@f", To: "archived/@4_f", File: "archived/@4_f.json", Dir: true, Rows: json.RawMessage(`{}`)})
	if got, ok := warning(w, "stuck-step"); !ok || !slices.Equal(got.Paths, absAll(s, "tree/@f", "archived/@4_f")) {
		t.Errorf("warnings %s", describe(w))
	}
}

// The archive makes the directories above an item, using any already
// there, and never lands the item on one: names in archived/ carry IDs.
func TestArchive(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@work/@api/@start.md=go", "archived/work/@1.md=", "archived/work/@1.json={}")
	write(t, s, Step{Op: OpArchive, From: "tree/@work/@api/@start.md", To: "archived/work/api/@58_start.md", File: "archived/work/api/@58_start.json", Rows: json.RawMessage(`{"id":58}`)})
	checkTree(t, s, sorted("tree/@work/", "tree/@work/@api/",
		"archived/work/", "archived/work/@1.md=", "archived/work/@1.json={}",
		"archived/work/api/", "archived/work/api/@58_start.md=go", "archived/work/api/@58_start.json={\"id\":58}"))
}

// A step the operating system refuses stays pending, holding back the
// rest of its change, which may depend on it; later changes go on. The
// result carries pending-step, and the next write finishes the change.
func TestFailedStep(t *testing.T) {
	_, s := newRoot(t)
	var points []string
	faults(t, func(p string) error {
		points = append(points, p)
		if p == "step 1: mkdir tree/@a" {
			return errors.New("refused")
		}
		return nil
	})
	w := write(t, s, Step{Op: OpMkdir, To: "tree/@a"}, Step{Op: OpWrite, To: "tree/@a/@1.md", Content: "one"})
	got, ok := warning(w, "pending-step")
	if !ok || !slices.Equal(got.Paths, absAll(s, "tree/@a", "tree/@a/@1.md")) || len(got.IDs) != 0 {
		t.Errorf("warnings %s", describe(w))
	}
	if slices.ContainsFunc(points, func(p string) bool { return strings.HasPrefix(p, "step 2:") }) {
		t.Errorf("the rest of the change ran: %q", points)
	}
	// The refusal persists; a later change goes on past it.
	write(t, s, Step{Op: OpMkdir, To: "tree/@b"})
	checkTree(t, s, sorted("tree/@b/", "tree/@b/@notes.md="))
	if live, _ := rows(t, s); !slices.Equal(seqs(live), []int64{1, 2}) {
		t.Errorf("live %v", seqs(live))
	}

	store.Fault = nil
	if w := write(t, s); len(w) != 0 {
		t.Errorf("warnings %s", describe(w))
	}
	checkTree(t, s, sorted("tree/@a/", "tree/@a/@notes.md=", "tree/@a/@1.md=one", "tree/@b/", "tree/@b/@notes.md="))
	noPending(t, s)
}

// A temp file a crash left behind is written afresh, and once the step
// is done, one left beside its file is removed.
func TestTempLeftBehind(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@a/"+TempName(1)+"=half", "tree/@b/@2.md=by hand", "tree/@b/"+TempName(2)+"=old")
	write(t, s, Step{Op: OpWrite, To: "tree/@a/@1.md", Content: "whole"}, Step{Op: OpWrite, To: "tree/@b/@2.md", Content: "mine"})
	checkTree(t, s, sorted("tree/@a/", "tree/@a/@1.md=whole", "tree/@b/", "tree/@b/@2.md=by hand"))
}

// A step whose destination appears just before its rename never replaces
// it: a move then finds both sides, and a write is done.
func TestNeverReplaces(t *testing.T) {
	_, s := newRoot(t)
	files(t, s, "tree/@1.md=mine", "tree/@a/")
	faults(t, func(p string) error {
		switch p {
		case "step 1: rename tree/@1.md":
			files(t, s, "tree/@a/@1.md=raced")
		case "step 2: rename tree/@a/" + TempName(2):
			files(t, s, "tree/@a/@2.md=raced")
		}
		return nil
	})
	w := write(t, s, Step{Op: OpMove, From: "tree/@1.md", To: "tree/@a/@1.md"})
	if !slices.Equal(kinds(w), []string{"stuck-step"}) {
		t.Errorf("warnings %s", describe(w))
	}
	write(t, s, Step{Op: OpWrite, To: "tree/@a/@2.md", Content: "mine"})
	checkTree(t, s, sorted("tree/@1.md=mine", "tree/@a/", "tree/@a/@1.md=raced", "tree/@a/@2.md=raced"))
}

// oldOwns is what a delete of the folder at tree/@old, with its subfolder
// @sub and its task 5, records it may remove.
var oldOwns = []string{"tree/@old", "tree/@old/@notes.md", "tree/@old/@start.md", "tree/@old/@done.md", "tree/@old/@5.md",
	"tree/@old/@sub", "tree/@old/@sub/@notes.md", "tree/@old/@sub/@start.md", "tree/@old/@sub/@done.md"}

// A directory remove whose change wasn't forced looks for material first,
// walking every @ directory under it: finding any, it removes nothing and
// sticks with material, holding back the rest of its change. Any entry
// not named with @ is material. An @ entry the step doesn't record is
// material when it holds content: a file of a byte or more, or a
// directory holding one or any entry not named with @. The notes it
// records, a zero-byte file and .DS_Store never are. A forced remove
// takes it all.
func TestRemoveMaterial(t *testing.T) {
	koans := []string{"tree/@old/@notes.md=notes", "tree/@old/@5.md=five", "tree/@old/@Notes.md=",
		"tree/@old/.DS_Store=finder", "tree/@old/@sub/@notes.md=", "tree/@old/@sub/@start.md=s",
		"tree/@old/@7.md=", "tree/@old/@stray/@x.md=", "tree/@old/@stray/.DS_Store=finder"}
	for _, c := range []struct {
		name     string
		material []string
		force    bool
		stuck    bool
	}{
		{"none", nil, false, false},
		{"at the top", []string{"tree/@old/thesis.tex=t"}, false, true},
		{"an empty file", []string{"tree/@old/@sub/__init__.py="}, false, true},
		{"a hidden directory", []string{"tree/@old/.git/"}, false, true},
		{"under @ directories", []string{"tree/@old/@sub/@types/x.d.ts=x"}, false, true},
		{"a stray @ file with content", []string{"tree/@old/@Notes.md=by hand"}, false, true},
		{"another task's notes", []string{"tree/@old/@sub/@7.md=seven"}, false, true},
		{"a stray @ directory holding content", []string{"tree/@old/@stray/@x.md=x"}, false, true},
		{"a stray @ directory holding material", []string{"tree/@old/@stray/@deep/a="}, false, true},
		{"a done gate's notes the step records", []string{"tree/@old/@sub/@done.md=d"}, false, false},
		{"forced", []string{"tree/@old/thesis.tex=t", "tree/@old/@sub/.git/", "tree/@old/@Notes.md=by hand"}, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, s := newRoot(t)
			files(t, s, koans...)
			files(t, s, c.material...)
			before := snapshot(t, s)
			w := write(t, s, Step{Op: OpRemove, From: "tree/@old", Dir: true, Force: c.force, Owns: oldOwns}, Step{Op: OpMkdir, To: "tree/@after"})
			_, stuck := rows(t, s)
			if !c.stuck {
				if len(w) != 0 {
					t.Errorf("warnings %s", describe(w))
				}
				checkTree(t, s, sorted("tree/@after/", "tree/@after/@notes.md="))
				noPending(t, s)
				return
			}
			got, ok := warning(w, "stuck-step")
			if !ok || !slices.Equal(got.Paths, absAll(s, "tree/@old")) || len(w) != 1 {
				t.Errorf("warnings %s", describe(w))
			}
			checkTree(t, s, before)
			if !slices.Equal(seqs(stuck), []int64{1, 2}) {
				t.Fatalf("stuck %v", seqs(stuck))
			}
			if st, _ := Parse(stuck[0].Step); st.Stuck != Material || st.Force {
				t.Errorf("stuck step %+v", st)
			}
		})
	}
}

// A directory remove whose change wasn't forced never removes material
// written while it runs: mid-walk, in a directory the walk already
// passed, or mid-removal, in a directory already listed or into a file
// that was empty when listed. It removes only what it listed there that
// isn't material, and each directory with rmdir, so the step fails and
// stays pending; the next write's walk finds the material and the step
// sticks.
func TestRemoveMaterialMidway(t *testing.T) {
	koans := []string{"tree/@old/@notes.md=notes", "tree/@old/@5.md=five", "tree/@old/.DS_Store=finder",
		"tree/@old/@sub/@notes.md=", "tree/@old/@sub/@start.md=s", "tree/@old/@sub/@8.md="}
	for _, c := range []struct {
		name, at, late string
	}{
		{"mid-walk", "step 1: look in tree/@old/@sub", "tree/@old/late.txt=late"},
		{"mid-removal, in a listed directory", "step 1: unlink tree/@old/@notes.md", "tree/@old/late.txt=late"},
		{"mid-removal, in a directory walked", "step 1: unlink tree/@old/@5.md", "tree/@old/@sub/late.txt=late"},
		{"mid-removal, in an emptied directory", "step 1: rmdir tree/@old/@sub", "tree/@old/@sub/late.txt=late"},
		{"before the last rmdir", "step 1: rmdir tree/@old", "tree/@old/.git/"},
		{"a stray @ file, mid-walk", "step 1: look in tree/@old/@sub", "tree/@old/@Notes.md=by hand"},
		{"a stray @ file, mid-removal", "step 1: unlink tree/@old/@notes.md", "tree/@old/@Notes.md=by hand"},
		{"another task's notes, mid-walk", "step 1: look in tree/@old/@sub", "tree/@old/@7.md=seven"},
		{"another task's notes, mid-removal", "step 1: rmdir tree/@old/@sub", "tree/@old/@sub/@7.md=seven"},
		{"another task's empty notes written as it is unlinked", "step 1: unlink tree/@old/@sub/@8.md", "tree/@old/@sub/@8.md=eight"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, s := newRoot(t)
			files(t, s, koans...)
			reached := false
			faults(t, func(p string) error {
				if p == c.at {
					reached = true
					files(t, s, c.late)
				}
				return nil
			})
			late := strings.TrimSuffix(c.late, "/")
			name, _, _ := strings.Cut(late, "=")
			has := func() bool {
				return slices.ContainsFunc(snapshot(t, s), func(e string) bool { return strings.TrimSuffix(e, "/") == late })
			}
			w := write(t, s, Step{Op: OpRemove, From: "tree/@old", Dir: true, Owns: oldOwns}, Step{Op: OpMkdir, To: "tree/@after"})
			if !reached {
				t.Fatalf("never reached %s", c.at)
			}
			if !has() {
				t.Fatalf("%s was removed", name)
			}
			if !slices.Equal(kinds(w), []string{"pending-step"}) {
				t.Errorf("warnings %s", describe(w))
			}
			if live, stuck := rows(t, s); !slices.Equal(seqs(live), []int64{1, 2}) || len(stuck) != 0 {
				t.Errorf("live %v, stuck %v", seqs(live), seqs(stuck))
			}

			store.Fault = nil
			w = write(t, s)
			got, ok := warning(w, "stuck-step")
			if !ok || !slices.Equal(got.Paths, absAll(s, "tree/@old")) || len(w) != 1 {
				t.Errorf("next write: warnings %s", describe(w))
			}
			_, stuck := rows(t, s)
			if !slices.Equal(seqs(stuck), []int64{1, 2}) {
				t.Fatalf("stuck %v", seqs(stuck))
			}
			if st, _ := Parse(stuck[0].Step); st.Stuck != Material {
				t.Errorf("stuck step %+v", st)
			}
			if !has() {
				t.Errorf("%s was removed by the next write", name)
			}
		})
	}
}

// A step found already done, carried out by a write that crashed before
// its flush, still has each directory it concerns flushed before its row
// is deleted.
func TestDoneStepsFlushed(t *testing.T) {
	_, s := newRoot(t)
	files(t, s,
		"tree/@a/@notes.md=", "tree/@a/@5.md=five", "tree/@a/@1.md=one",
		"archived/@9.md=nine", "archived/@9.json={}",
	)
	var flushed []string
	faults(t, func(p string) error {
		if d, ok := strings.CutPrefix(p, "flush "); ok {
			flushed = append(flushed, d)
		}
		return nil
	})
	write(t, s,
		Step{Op: OpMkdir, To: "tree/@a"},
		Step{Op: OpWrite, To: "tree/@a/@5.md", Content: "five"},
		Step{Op: OpMove, From: "tree/@1.md", To: "tree/@a/@1.md"},
		Step{Op: OpRemove, From: "tree/@gone", Dir: true},
		Step{Op: OpArchive, From: "tree/@9.md", To: "archived/@9.md", File: "archived/@9.json", Rows: []byte(`{}`)},
	)
	slices.Sort(flushed)
	if want := []string{"archived", "tree", "tree/@a"}; !slices.Equal(flushed, want) {
		t.Errorf("flushed %q, want %q", flushed, want)
	}
	noPending(t, s)
}

// A step is added only through Add: the store refuses anything else, so
// no step lacks what Add gives it, such as a case-only move's temporary
// name.
func TestAddOnlySteps(t *testing.T) {
	_, s := newRoot(t)
	_, _, e := s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
		_, err := tx.AddStep(json.RawMessage(`{"op":"move","from":"tree/@work","to":"tree/@Work","dir":true}`))
		return nil, nil, err
	})
	if e == nil || e.Kind != cli.KindInternal {
		t.Errorf("a raw step: %+v", e)
	}
	noPending(t, s)
}
