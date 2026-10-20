// Package fsstep carries out the filesystem steps a change commits with
// it, as pending rows: making a folder's directory, writing notes, moving
// notes or a directory, removing them, and archiving. Each step is stated
// as the result it wants, judged by listing the parent directory and
// comparing names byte for byte, never by stat, so carrying it out twice
// is harmless.
package fsstep

import (
	"encoding/json"
	"path"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// The kinds of step.
const (
	OpMkdir   = "mkdir"
	OpWrite   = "write"
	OpMove    = "move"
	OpRemove  = "remove"
	OpArchive = "archive"
)

// What a stuck step found.
const (
	Both    = "both"
	Neither = "neither"
)

// The notes names koan keeps, matched exactly.
const (
	FolderNotes = "@notes.md"
	StartNotes  = "@start.md"
	DoneNotes   = "@done.md"
)

// Step is one filesystem step, as its pending row holds it. Paths are
// relative to the root's directory, with / between names, such as
// tree/@work/@42.md. A step holds everything it needs, notes content and
// archived rows included, since the rows it came from may already be
// gone.
type Step struct {
	Op string `json:"op"`
	// From is what a move, a remove or an archive takes away.
	From string `json:"from,omitempty"`
	// To is what a mkdir, a write, a move or an archive makes.
	To string `json:"to,omitempty"`
	// Via is the temporary name a case-only rename passes through.
	Via string `json:"via,omitempty"`
	// Dir is whether a move, a remove or an archive is of a directory.
	Dir bool `json:"dir,omitempty"`
	// Content is the notes a write puts at To.
	Content string `json:"content,omitempty"`
	// File is where an archive writes Rows, its JSON, beside To.
	File string          `json:"file,omitempty"`
	Rows json.RawMessage `json:"rows,omitempty"`
	// Stuck is what a stuck step found: both or neither.
	Stuck string `json:"stuck,omitempty"`
}

// CaseName is the temporary name a case-only rename passes through.
func CaseName(seq int64) string { return "@.case-" + strconv.FormatInt(seq, 10) }

// TempName is the temp file notes and archive JSON are written to before
// they are renamed into place.
func TempName(seq int64) string { return "@.tmp-" + strconv.FormatInt(seq, 10) }

// IsNotesName reports whether name is one of koan's notes names, exactly:
// @notes.md, @start.md, @done.md or @<id>.md.
func IsNotesName(name string) bool {
	switch name {
	case FolderNotes, StartNotes, DoneNotes:
		return true
	}
	id, ok := strings.CutPrefix(name, "@")
	if !ok {
		return false
	}
	id, ok = strings.CutSuffix(id, ".md")
	if !ok {
		return false
	}
	_, ok = core.ParseID(id)
	return ok
}

// caseOnly reports whether a move only changes the case of a name.
func caseOnly(from, to string) bool {
	return path.Dir(from) == path.Dir(to) && from != to && strings.EqualFold(path.Base(from), path.Base(to))
}

// Add adds a step to the change. A case-only move gets its temporary name
// from the seq it will have, so it records all three names.
func Add(tx *store.Tx, s Step) (int64, error) {
	if s.Op == OpMove && caseOnly(s.From, s.To) {
		seq, err := tx.NextSeq()
		if err != nil {
			return 0, err
		}
		s.Via = path.Join(path.Dir(s.From), CaseName(seq))
	}
	b, err := json.Marshal(s)
	if err != nil {
		return 0, err
	}
	return tx.AddStep(b)
}

// Parse reads a pending row's step.
func Parse(raw json.RawMessage) (Step, error) {
	var s Step
	err := json.Unmarshal(raw, &s)
	return s, err
}

// Paths is every path the step touches, in the order it touches them.
func (s Step) Paths() []string {
	var out []string
	for _, p := range []string{s.From, s.Via, s.File, s.To} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// targets is where the step brings things: what it makes.
func (s Step) targets() []string {
	var out []string
	for _, p := range []string{s.File, s.To} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
