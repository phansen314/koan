package fsstep

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/phansen314/koan/internal/store"
)

// caseCase is a step meeting a name that differs from one on its paths
// only in case. Each gives the same result on every filesystem, so the
// casefold helper runs them too.
type caseCase struct {
	name   string
	before []string
	step   Step
	after  []string
	warn   []string
}

var caseCases = []caseCase{
	{"a directory beside one differing in case",
		[]string{"tree/@Api/"},
		Step{Op: OpMkdir, To: "tree/@api"},
		[]string{"tree/@Api/"}, []string{"pending-step"}},
	{"a folder's notes beside a hand-made @Notes.md",
		[]string{"tree/@b/@Notes.md=by hand"},
		Step{Op: OpMkdir, To: "tree/@b"},
		[]string{"tree/@b/", "tree/@b/@Notes.md=by hand"}, []string{"pending-step"}},
	{"a move onto a directory differing in case",
		[]string{"tree/@x/@notes.md=x", "tree/@a/@X/"},
		Step{Op: OpMove, From: "tree/@x", To: "tree/@a/@x", Dir: true},
		[]string{"tree/@x/", "tree/@x/@notes.md=x", "tree/@a/", "tree/@a/@X/"}, []string{"pending-step"}},
	{"notes into a directory respelled by hand",
		[]string{"tree/@Work/@notes.md=w"},
		Step{Op: OpWrite, To: "tree/@work/@5.md", Content: "five"},
		[]string{"tree/@Work/", "tree/@Work/@notes.md=w"}, []string{"pending-step"}},
	{"notes beside a file differing in case",
		[]string{"tree/@5.MD=by hand"},
		Step{Op: OpWrite, To: "tree/@5.md", Content: "five"},
		[]string{"tree/@5.MD=by hand"}, []string{"pending-step"}},
	{"a remove of a directory respelled by hand",
		[]string{"tree/@Old/@notes.md="},
		Step{Op: OpRemove, From: "tree/@old", Dir: true, Owns: []string{"tree/@old", "tree/@old/@notes.md"}},
		[]string{"tree/@Old/", "tree/@Old/@notes.md="}, []string{"pending-step"}},
	{"the archive uses a directory above as it finds it",
		[]string{"tree/@2.md=two", "archived/Work/@1.md=", "archived/Work/@1.json={}"},
		Step{Op: OpArchive, From: "tree/@2.md", To: "archived/work/@2.md", File: "archived/work/@2.json", Rows: json.RawMessage(`{}`)},
		[]string{"archived/Work/", "archived/Work/@1.md=", "archived/Work/@1.json={}", "archived/Work/@2.md=two", "archived/Work/@2.json={}"}, nil},
	{"a case-only rename's own names",
		[]string{"tree/@work/@notes.md=w"},
		Step{Op: OpMove, From: "tree/@work", To: "tree/@Work", Dir: true},
		[]string{"tree/@Work/", "tree/@Work/@notes.md=w"}, nil},
}

// caseResult is what a case gave.
type caseResult struct {
	Tree     []string `json:"tree"`
	Warnings []string `json:"warnings"`
}

// runCase runs a case in a fresh root under home.
func runCase(home string, c caseCase) (caseResult, error) {
	first := int64(1)
	if _, _, _, e := store.Init(home, &first); e != nil {
		return caseResult{}, fmt.Errorf("init: %s", e.Message)
	}
	s, e := Open(home)
	if e != nil {
		return caseResult{}, fmt.Errorf("open: %s", e.Message)
	}
	defer s.Close()
	if err := makeFiles(s.Root.Dir, c.before...); err != nil {
		return caseResult{}, err
	}
	_, w, e := s.Write(change(c.step))
	if e != nil {
		return caseResult{}, fmt.Errorf("write: %s", e.Message)
	}
	tree, err := snap(s.Root.Dir)
	return caseResult{Tree: tree, Warnings: kinds(w)}, err
}

func (c caseCase) check(t *testing.T, r caseResult) {
	t.Helper()
	if !slices.Equal(r.Tree, sorted(c.after...)) || !slices.Equal(r.Warnings, c.warn) {
		t.Errorf("%s:\ngot  %q %q\nwant %q %q", c.name, r.Tree, r.Warnings, sorted(c.after...), c.warn)
	}
}

// A name on a step's paths that equals the one it needs only ignoring
// case fails the step on every filesystem, but for a case-only rename's
// own names and the directories above an archived item.
func TestCaseDiffers(t *testing.T) {
	for _, c := range caseCases {
		t.Run(c.name, func(t *testing.T) {
			r, err := runCase(t.TempDir(), c)
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, r)
		})
	}
}

// caseHomes runs every case under home, for the casefold helper.
func caseHomes(home string) (map[string]caseResult, error) {
	out := map[string]caseResult{}
	for i, c := range caseCases {
		h := filepath.Join(home, "case-"+strconv.Itoa(i))
		if err := os.Mkdir(h, 0o755); err != nil {
			return nil, err
		}
		r, err := runCase(h, c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		out[c.name] = r
	}
	return out, nil
}
