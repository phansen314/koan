package model

import (
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/core"
)

// shape collects the faults in an input's shape, each at its JSON
// Pointer, so that invalid-input names every one. A change checks its
// whole shape before any reference resolves, as the spec's check order
// says.
type shape struct {
	err *core.Error
	// refs are the references whose shape is good, each at its pointer,
	// for the two-spellings check.
	refs []core.Ref
	at   []string
}

// fault records a fault at pointer; the error's message is the first
// fault's.
func (s *shape) fault(pointer string, err error) {
	if s.err == nil {
		s.err = &core.Error{Kind: core.KindInvalidInput, Message: err.Error()}
		if e, ok := err.(*core.Error); ok {
			s.err.Message = e.Message
		}
	}
	s.err.Problems = append(s.err.Problems, pointer)
}

func (s *shape) faultf(pointer, format string, args ...any) {
	s.fault(pointer, fmt.Errorf(format, args...))
}

// faultIDs records a fault that names IDs.
func (s *shape) faultIDs(pointer string, ids []int64, format string, args ...any) {
	s.faultf(pointer, format, args...)
	for _, id := range ids {
		if !slices.Contains(s.err.IDs, id) {
			s.err.IDs = append(s.err.IDs, id)
		}
	}
}

// ref parses a reference and checks its shape where want is taken.
func (s *shape) ref(pointer, raw string, want core.Want, inBatch bool) core.Ref {
	r, err := core.ParseRef(raw)
	if err == nil {
		err = core.CheckShape(r, want, inBatch)
	}
	if err != nil {
		s.fault(pointer, err)
		return r
	}
	s.refs, s.at = append(s.refs, r), append(s.at, pointer)
	return r
}

// path parses a folder path to make.
func (s *shape) path(pointer, raw string) core.Ref {
	r, err := pathRef(raw)
	if err != nil {
		s.fault(pointer, err)
		return r
	}
	s.refs, s.at = append(s.refs, r), append(s.at, pointer)
	return r
}

// title checks a task's title, and gives it as stored.
func (s *shape) title(pointer, raw string) string {
	t, err := core.CheckTitle(raw)
	if err != nil {
		s.fault(pointer, err)
	}
	return t
}

// folderTitle checks a folder's title, which may be null.
func (s *shape) folderTitle(pointer string, title *string) *string {
	t, err := checkFolderTitle(title)
	if err != nil {
		s.fault(pointer, err)
	}
	return t
}

func (s *shape) priority(pointer string, p *int64) {
	if err := checkPriority(p); err != nil {
		s.fault(pointer, err)
	}
}

// tags checks a list of tags: each its pattern; a tag given twice counts
// once.
func (s *shape) tags(pointer string, tags []string) map[string]bool {
	out := map[string]bool{}
	for i, tag := range tags {
		if !core.ValidTag(tag) {
			s.faultf(fmt.Sprintf("%s/%d", pointer, i), "%q is not a tag", tag)
		}
		out[tag] = true
	}
	return out
}

// done checks that no two references name paths differing only in case,
// pointing at both, then gives every fault found, if any. exempt, when
// given, lets a pair through.
func (s *shape) done(exempt func(a, b core.Ref) bool) error {
	for _, p := range core.CasePairs(s.refs) {
		a, b := s.refs[p[0]], s.refs[p[1]]
		if exempt != nil && exempt(a, b) {
			continue
		}
		s.faultf(s.at[p[0]], "%q and %q differ only in case", a.Raw, b.Raw)
		s.err.Problems = append(s.err.Problems, s.at[p[1]])
	}
	if s.err == nil {
		return nil
	}
	return s.err
}

// pointAt points an error found once the shape is good at the field it came
// from: invalid-input and not-found always, since they name the field at
// fault; any other kind only in a batch, where a refusal points at the
// parts at fault.
func pointAt(err error, pointer string, batch bool) error {
	if e, ok := err.(*core.Error); ok && (batch || e.Kind == core.KindInvalidInput || e.Kind == core.KindNotFound) {
		return problem(err, "%s", pointer)
	}
	return err
}
