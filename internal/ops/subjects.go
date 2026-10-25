package ops

import (
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// subject is one task or folder a command that takes both acts on.
type subject struct {
	id     int64
	folder bool
}

// subjectRefs reads refs, the tasks and folders a command acts on, mixed,
// with each one's pointer; the root can't be one. verb says what is done
// to them.
func (r *input) subjectRefs(verb string) ([]core.Ref, []string) {
	if v, ok := r.required("refs"); ok {
		if list, isList := v.([]any); isList && len(list) == 0 {
			r.fault("/refs", "at least one task or folder is needed")
		}
	}
	refs := r.refs("refs", core.WantRef)
	at := make([]string, len(refs))
	for i, ref := range refs {
		at[i] = fmt.Sprintf("/refs/%d", i)
		if ref.IsRoot() {
			r.fault(at[i], "the root can't be %s", verb)
		}
	}
	return refs, at
}

// resolveSubjects resolves the references subjectRefs read, in order. One
// given twice is invalid-input at its later occurrence.
func resolveSubjects(tx *store.Tx, refs []core.Ref, at []string, verb string) ([]subject, error) {
	var out []subject
	for i, ref := range refs {
		res, err := resolveAt(tx, ref, core.WantRef, at[i])
		if err != nil {
			return nil, err
		}
		if res.Kind == core.ResolvedRoot {
			return nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{at[i]},
				Message: "the root can't be " + verb}
		}
		if slices.ContainsFunc(out, func(s subject) bool { return s.id == res.ID }) {
			return nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{at[i]},
				Message: fmt.Sprintf("@%d is given twice", res.ID)}
		}
		out = append(out, subject{id: res.ID, folder: res.Kind == core.ResolvedFolder})
	}
	return out, nil
}

// within refuses each subject that sits under a folder also given, at
// the one inside, since it would go with that folder.
func within(tx *store.Tx, subs []subject, at []string) error {
	given := map[int64]bool{}
	for _, s := range subs {
		if s.folder {
			given[s.id] = true
		}
	}
	if len(given) == 0 {
		return nil
	}
	var inside *core.Error
	for i, s := range subs {
		parent, err := placeOf(tx, s)
		if err != nil {
			return err
		}
		for f := parent; f != core.RootID; {
			if given[f] {
				if inside == nil {
					inside = &core.Error{Kind: core.KindInvalidInput,
						Message: fmt.Sprintf("@%d is inside @%d, also given", s.id, f)}
				}
				inside.Problems = append(inside.Problems, at[i])
				break
			}
			row, err := tx.Folder(f)
			if err != nil {
				return err
			}
			f = row.Parent
		}
	}
	if inside != nil {
		return inside
	}
	return nil
}

// placeOf is the folder a subject sits in, or the root.
func placeOf(tx *store.Tx, s subject) (int64, error) {
	if s.folder {
		row, err := tx.Folder(s.id)
		return row.Parent, err
	}
	row, _, err := tx.Task(s.id)
	return row.Folder, err
}

// firstOf is the first subject of a kind, or false when none is.
func firstOf(subs []subject, folder bool) (int64, bool) {
	for _, s := range subs {
		if s.folder == folder {
			return s.id, true
		}
	}
	return 0, false
}
