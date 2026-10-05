package pick

import (
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/jsonio"
)

// tagsAction is t (pick-spec.md, Actions): the targets' tags, in a prompt
// that starts with the one target's tags, or empty for several. The value
// is a list of tags separated by spaces or commas: all bare, they replace
// the tags; all prefixed, + adds and - removes; a lone - clears them. A
// mix is refused, and the prompt stays open.
var tagsAction = action{key: "t", arity: anyTargets, run: openTags, apply: setTags}

func openTags(r *actionRun, targets []shownLine) {
	start := promptStart(targets, func(t shownLine) string {
		tags := make([]string, len(t.Tags))
		for i, tag := range t.Tags {
			tags[i] = string(tag)
		}
		return strings.Join(tags, " ")
	})
	r.openPrompt("t", targetLabel("tags", targets), start, targets)
}

func setTags(r *actionRun, value string, targets []shownLine) bool {
	change, ok := parseTags(value)
	if !ok {
		r.status = "✗ tags: a mix of bare and +/- tags"
		return true
	}
	// Only separators is empty, as parseTags reads it: with several
	// targets, it must not clear them all.
	if strings.Trim(value, ", \t") == "" {
		value = ""
	}
	return r.applyEach(strings.TrimSpace(value), targets, func(t shownLine) {
		in := &jsonio.Object{}
		in.Set("id", idNumber(t.ID))
		in.Set("tags", change)
		r.call(t.ID, "update", in, "set tags", "tags "+string(idNumber(t.ID)))
	})
}

// parseTags reads the tags syntax as update's tags change: replace_all for
// bare tags, nothing included, or a lone -; add and remove for prefixed
// ones. ok is false for a mix. Each list is without duplicates; what the
// tags may be is update's to check.
func parseTags(value string) (change *jsonio.Object, ok bool) {
	items := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	change = &jsonio.Object{}
	if len(items) == 1 && items[0] == "-" {
		change.Set("replace_all", []any{})
		return change, true
	}
	var bare, add, remove []any
	for _, it := range items {
		switch {
		case strings.HasPrefix(it, "+"):
			add = appendNew(add, it[1:])
		case strings.HasPrefix(it, "-"):
			remove = appendNew(remove, it[1:])
		default:
			bare = appendNew(bare, it)
		}
	}
	switch {
	case bare != nil && (add != nil || remove != nil):
		return nil, false
	case add == nil && remove == nil:
		if bare == nil {
			bare = []any{}
		}
		change.Set("replace_all", bare)
	default:
		if add != nil {
			change.Set("add", add)
		}
		if remove != nil {
			change.Set("remove", remove)
		}
	}
	return change, true
}

func appendNew(list []any, tag string) []any {
	if slices.Contains(list, any(tag)) {
		return list
	}
	return append(list, tag)
}
