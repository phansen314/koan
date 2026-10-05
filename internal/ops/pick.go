package ops

import (
	"cmp"
	"slices"
	"strings"

	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

// PickScope is which tasks pick shows at first (pick-spec.md, Command).
type PickScope string

const (
	PickReady PickScope = "ready"
	PickOpen  PickScope = "open"
	PickAll   PickScope = "all"
)

var pickScopes = []string{string(PickReady), string(PickOpen), string(PickAll)}

// PickInput is pick's input (pick-input), defaults applied. pick runs no
// operation of its own: its adapter is here, with the others, so that the
// CLI still knows nothing of the data model.
type PickInput struct {
	Folder    model.FolderPath
	Recursive bool
	Scope     PickScope
	TagsAny   []model.Tag // nil: no filter
	TagsAll   []model.Tag // nil: no filter
	// IDs is a snapshot's candidates; nil when not given. It may be empty.
	IDs       []model.ID
	Source    *string // a live source's command; nil when not given
	Query     string
	SelectOne bool
	ExitZero  bool
	Fields    []string // nil: whole views
	Folders   bool
}

// pickTaskFields are the fields only the task picker takes: with folders,
// each is refused.
var pickTaskFields = []string{"scope", "tags_any", "tags_all", "ids", "source", "fields"}

func decodePick(f *model.Fields, p *model.Problems) any {
	in := PickInput{
		Folder:    optionalFolder(f, p, "folder"),
		Recursive: optionalBool(f, p, "recursive", true),
		TagsAny:   optionalTagSet(f, p, "tags_any"),
		TagsAll:   optionalTagSet(f, p, "tags_all"),
		SelectOne: optionalBool(f, p, "select_one", false),
		ExitZero:  optionalBool(f, p, "exit_zero", false),
		Folders:   optionalBool(f, p, "folders", false),
	}
	if v, ok := f.Optional("ids"); ok {
		if ids, ok := p.IDs(v, "/ids"); ok {
			in.IDs = ids
		}
	}
	if v, ok := f.Optional("source"); ok {
		if s, ok := p.String(v, "/source"); ok {
			if s == "" {
				p.Add("/source", "must not be empty")
			}
			in.Source = &s
		}
	}
	if f.Has("ids") && f.Has("source") {
		p.Add("", "give ids or source, not both")
	}
	if v, ok := f.Optional("query"); ok {
		in.Query, _ = p.String(v, "/query")
	}
	if v, ok := f.Optional("fields"); ok {
		in.Fields, _ = nonEmptySet(p, v, "/fields", "field", nameSet(model.ViewFields))
	}
	in.Scope = PickOpen
	if f.Has("ids") || f.Has("source") {
		in.Scope = PickAll
	}
	if v, ok := f.Optional("scope"); ok {
		if s, ok := p.String(v, "/scope"); ok {
			if slices.Contains(pickScopes, s) {
				in.Scope = PickScope(s)
			} else {
				p.Add("/scope", "must be one of "+strings.Join(pickScopes, ", "))
			}
		}
	}
	if in.Folders {
		for _, key := range pickTaskFields {
			if f.Has(key) && !p.Failed(f.Ptr(key)) {
				p.AddAdditional(f.Ptr(key), "not with folders: the folder picker takes only folder, recursive, query, select_one and exit_zero")
			}
		}
	}
	return in
}

// PickOrder is the order of pick's lines (pick-spec.md, Lines): ready
// tasks, then blocked ones, each in frontier order; then complete ones,
// completed_at newest first, then ID, then, for copies of a duplicated ID,
// tree order. It is a total order.
func PickOrder(a, b model.TaskView) int {
	if c := cmp.Compare(readinessRank[a.Readiness], readinessRank[b.Readiness]); c != 0 {
		return c
	}
	if a.Readiness != model.Complete {
		return frontierOrder(a, b)
	}
	return cmp.Or(
		cmp.Compare(*b.CompletedAt, *a.CompletedAt), // fixed-width UTC: text order is time order
		cmp.Compare(a.ID, b.ID),
		store.CompareFolders(a.Folder, b.Folder),
	)
}

var readinessRank = map[model.Readiness]int{model.Ready: 0, model.Blocked: 1, model.Complete: 2}
