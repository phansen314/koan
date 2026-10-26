package core

import (
	"fmt"
	"strings"
)

// End names one of a folder's gates.
type End string

const (
	EndNone  End = ""
	EndStart End = "start"
	EndDone  End = "done"
)

// TargetKind is the form a reference's target is written in.
type TargetKind int

const (
	// TargetID is "42" or "@42".
	TargetID TargetKind = iota
	// TargetPath is a path from the root; with no names it is "/" itself.
	TargetPath
	// TargetAnchored is a path from a folder's ID, as "@41/phase2".
	TargetAnchored
	// TargetLabel is "$a", a task made in the same batch.
	TargetLabel
)

// Ref is a reference as written, split into its target, its end and its
// material. It names nothing until it is resolved.
type Ref struct {
	Raw   string
	Kind  TargetKind
	ID    int64    // TargetID and TargetAnchored
	Names []string // TargetPath and TargetAnchored: the names after the root or the ID
	Label string   // TargetLabel, with its "$"
	End   End

	Material    string
	HasMaterial bool
}

// IsRoot reports whether the target is "/".
func (r Ref) IsRoot() bool { return r.Kind == TargetPath && len(r.Names) == 0 }

// TargetString writes the target as given, without end or material.
func (r Ref) TargetString() string {
	switch r.Kind {
	case TargetID:
		return fmt.Sprintf("@%d", r.ID)
	case TargetPath:
		return "/" + strings.Join(r.Names, "/")
	case TargetAnchored:
		return fmt.Sprintf("@%d/%s", r.ID, strings.Join(r.Names, "/"))
	default:
		return r.Label
	}
}

// ParseRef splits a reference as Resolution steps 1 and 2 say, and checks
// each part against the References grammar.
func ParseRef(s string) (Ref, error) {
	ref := Ref{Raw: s}

	// Step 1: split at the first ":"; the material may hold more.
	target := s
	if i := strings.IndexByte(s, ':'); i >= 0 {
		target, ref.Material, ref.HasMaterial = s[:i], s[i+1:], true
		if err := CheckMaterial(ref.Material); err != nil {
			return Ref{}, err
		}
	}

	// Step 2: split off a final ".start" or ".done", from the target only.
	// A name never holds a ".", so a final one always starts an end.
	if i := strings.LastIndexByte(target, '.'); i >= 0 {
		switch End(target[i+1:]) {
		case EndStart:
			ref.End = EndStart
		case EndDone:
			ref.End = EndDone
		default:
			return Ref{}, invalid("%q: an end is .start or .done", s)
		}
		target = target[:i]
		if ref.HasMaterial {
			return Ref{}, invalid("%q: an end can't come before the material", s)
		}
	}

	switch {
	case target == "":
		return Ref{}, invalid("%q names no target", s)

	case target[0] == '$':
		if !ValidLabel(target) {
			return Ref{}, invalid("%q is not a label", target)
		}
		if ref.End != EndNone || ref.HasMaterial {
			return Ref{}, invalid("%q: a label takes no end and no material", s)
		}
		ref.Kind, ref.Label = TargetLabel, target

	case target == "/":
		ref.Kind = TargetPath

	case target[0] == '/':
		names, err := parseNames(s, target[1:])
		if err != nil {
			return Ref{}, err
		}
		ref.Kind, ref.Names = TargetPath, names

	default:
		digits, rest, anchored := strings.Cut(strings.TrimPrefix(target, "@"), "/")
		id, ok := ParseID(digits)
		if !ok {
			return Ref{}, invalid("%q is not an ID or a path", s)
		}
		ref.Kind, ref.ID = TargetID, id
		if anchored {
			names, err := parseNames(s, rest)
			if err != nil {
				return Ref{}, err
			}
			ref.Kind, ref.Names = TargetAnchored, names
		}
	}
	return ref, nil
}

func parseNames(s, joined string) ([]string, error) {
	names := strings.Split(joined, "/")
	for _, n := range names {
		if !ValidName(n) {
			return nil, invalid("%q: %q is not a folder name", s, n)
		}
	}
	return names, nil
}

// CheckCase refuses one input naming two paths that differ only in case in
// the same part, such as /Proj/a and /proj/b. Paths are compared only from
// the same start: the root, or the same anchoring ID. Two written from
// different starts are caught as they resolve, where the tree holds at most
// one of the spellings, so the other is case-mismatch.
func CheckCase(refs []Ref) error {
	if pairs := CasePairs(refs); pairs != nil {
		a, b := refs[pairs[0][0]], refs[pairs[0][1]]
		return invalid("%q and %q differ only in case", a.Raw, b.Raw)
	}
	return nil
}

// CasePairs is every pair of refs, by index, earlier first, that name two
// paths differing only in case in the same part, compared as CheckCase
// compares them.
func CasePairs(refs []Ref) [][2]int {
	var out [][2]int
	for i := range refs {
		for j := i + 1; j < len(refs); j++ {
			if SpelledTwice(refs[i], refs[j]) {
				out = append(out, [2]int{i, j})
			}
		}
	}
	return out
}

// SpelledTwice reports whether two refs name paths that differ only in
// case in the same part.
func SpelledTwice(a, b Ref) bool {
	if a.Kind != b.Kind || (a.Kind != TargetPath && a.Kind != TargetAnchored) || a.ID != b.ID {
		return false
	}
	for k := 0; k < len(a.Names) && k < len(b.Names); k++ {
		if a.Names[k] == b.Names[k] {
			continue
		}
		return strings.EqualFold(a.Names[k], b.Names[k])
	}
	return false
}

// Kept writes a target as kept text: its ID, then its path as a hint.
// For a gate, path is its folder's path and end its end.
func Kept(id int64, path string, end End) string {
	if end != EndNone {
		return fmt.Sprintf("@%d (%s.%s)", id, path, end)
	}
	return fmt.Sprintf("@%d (%s)", id, path)
}

// KeptMaterial writes material in a folder as kept text.
func KeptMaterial(id int64, path, material string) string {
	return fmt.Sprintf("@%d:%s (%s:%s)", id, material, path, material)
}

// KeptRootMaterial writes material in the root as kept text: the root has
// no ID, and its path never changes, so there is no hint.
func KeptRootMaterial(material string) string { return "/:" + material }
