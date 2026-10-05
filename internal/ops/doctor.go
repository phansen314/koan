package ops

import (
	"bytes"
	"cmp"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/graph"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

// Finding kinds (operations.md, Finding kinds).
const (
	kindTempLeftover      = "temp-leftover"
	kindMetadataMissing   = "metadata-missing"
	kindMetadataUnusable  = "metadata-unusable"
	kindIDAboveLastID     = "id-above-last-id"
	kindDanglingReference = "dangling-reference"
	kindOrphanNotes       = "orphan-notes"
	kindDuplicateID       = "duplicate-id"
	kindCycle             = "cycle"
	kindUnusableFile      = "unusable-file"
	kindNestedTree        = "nested-tree"
	kindSkippedEntry      = "skipped-entry"
	kindStrayEntry        = "stray-entry"
	kindUnreadableFolder  = "unreadable-folder"
)

// Repair classes (design-spec.md, Diagnosis and repair).
const (
	classAuto      = "auto"
	classOnRequest = "on-request"
	classManual    = "manual"
	// classInformational kinds are not damage: reported only when named,
	// and never making a tree unhealthy.
	classInformational = "informational"
)

// findingClass is each finding kind's repair class.
var findingClass = map[string]string{
	kindTempLeftover:      classAuto,
	kindMetadataMissing:   classOnRequest,
	kindMetadataUnusable:  classManual,
	kindIDAboveLastID:     classAuto,
	kindDanglingReference: classAuto,
	kindOrphanNotes:       classAuto,
	kindDuplicateID:       classManual,
	kindCycle:             classManual,
	kindUnusableFile:      classManual,
	kindNestedTree:        classManual,
	kindSkippedEntry:      classManual,
	kindStrayEntry:        classInformational,
	kindUnreadableFolder:  classManual,
}

// findingKinds is every finding kind, sorted.
var findingKinds = slices.Sorted(maps.Keys(findingClass))

// Actions: what repair does to an item.
const (
	actionRemove          = "remove"
	actionCreateMetadata  = "create-metadata"
	actionRaiseLastID     = "raise-last-id"
	actionRemoveReference = "remove-reference"
)

// orphan-notes reasons.
const (
	orphanEmpty         = "empty"
	orphanLinked        = "linked"
	orphanTaskElsewhere = "task-elsewhere"
	orphanNoTask        = "no-task"
	orphanUnreadable    = "unreadable"
)

// findingCap is how many items a kind lists unless the input names it.
const findingCap = 20

// KindsInput is doctor's and repair's input: the kinds named, nil when
// kinds is absent.
type KindsInput struct {
	Kinds []string
}

func decodeDoctor(f *model.Fields, p *model.Problems) any {
	return KindsInput{Kinds: optionalKinds(f, p)}
}

func optionalKinds(f *model.Fields, p *model.Problems) []string {
	v, ok := f.Optional("kinds")
	if !ok {
		return nil
	}
	kinds, _ := nonEmptySet(p, v, f.Ptr("kinds"), "kind", kindSet)
	return kinds
}

// kindSet checks a value as a set of finding kinds. The schema types them as
// strings, so an unknown kind is Additional validation.
func kindSet(p *model.Problems, v any, ptr string) ([]string, bool) {
	a, ok := p.Array(v, ptr)
	if !ok {
		return nil, false
	}
	kinds := make([]string, 0, len(a))
	valid := make([]bool, 0, len(a))
	for i, item := range a {
		at := jsonio.Pointer(ptr, strconv.Itoa(i))
		s, ok1 := p.String(item, at)
		kinds = append(kinds, s)
		valid = append(valid, ok1)
		ok = ok && ok1
		if ok1 && findingClass[s] == "" {
			p.AddAdditional(at, "must be one of "+strings.Join(findingKinds, ", "))
			ok = false
		}
	}
	return kinds, model.Unique(p, kinds, valid, ptr) && ok
}

// Finding is one kind's findings (finding).
type Finding struct {
	Kind      string `json:"kind"`
	Class     string `json:"class"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated"`
	Items     []Item `json:"items"`
}

// Item is one finding (finding-item). The unexported fields are what repair
// needs to act on it.
type Item struct {
	Paths     []string    `json:"paths"`
	IDs       []model.ID  `json:"ids"`
	Action    *string     `json:"action"`
	Suggest   *string     `json:"suggest"`
	Reason    string      `json:"reason,omitempty"`
	Code      string      `json:"code,omitempty"`
	Identical *bool       `json:"identical,omitempty"`
	Group     []model.ID  `json:"group,omitempty"`
	LastID    *int64      `json:"last_id,omitempty"`
	Error     *errs.Error `json:"error,omitempty"`

	rel     string         // the entry acted on, relative to the root
	loc     store.Location // dangling-reference: the referring task file
	missing model.ID       // dangling-reference: the ID it names
	linked  string         // orphan-notes linked: the task's notes it is
}

// findings is every finding in a tree, by kind, unsorted and uncapped.
type findings map[string][]Item

func (fs findings) add(kind string, it Item) {
	if it.Paths == nil {
		it.Paths = []string{}
	}
	if it.IDs == nil {
		it.IDs = []model.ID{}
	}
	fs[kind] = append(fs[kind], it)
}

// healthy reports whether there are no findings of any kind but
// informational ones.
func (fs findings) healthy() bool {
	for kind, items := range fs {
		if len(items) > 0 && findingClass[kind] != classInformational {
			return false
		}
	}
	return true
}

// report groups fs as findings are reported (operations.md, Findings): only
// the kinds in only (when nil, every kind but the informational ones), sorted
// by kind, each kind's items sorted and capped at findingCap unless full
// names it.
func (fs findings) report(only, full []string) []Finding {
	out := []Finding{}
	for _, kind := range slices.Sorted(maps.Keys(fs)) {
		items := fs[kind]
		switch {
		case len(items) == 0:
			continue
		case only != nil && !slices.Contains(only, kind):
			continue
		case only == nil && findingClass[kind] == classInformational:
			continue
		}
		items = slices.Clone(items)
		slices.SortStableFunc(items, compareItems)
		f := Finding{Kind: kind, Class: findingClass[kind], Count: len(items), Items: items}
		if len(items) > findingCap && !slices.Contains(full, kind) {
			f.Items, f.Truncated = items[:findingCap], true
		}
		out = append(out, f)
	}
	return out
}

// compareItems orders a kind's items: by the first entry of paths, then by
// ids, element by element.
func compareItems(a, b Item) int {
	first := func(it Item) string {
		if len(it.Paths) == 0 {
			return ""
		}
		return it.Paths[0]
	}
	return cmp.Or(strings.Compare(first(a), first(b)), slices.Compare(a.IDs, b.IDs))
}

// DoctorOutput is doctor's result (doctor-output).
type DoctorOutput struct {
	Healthy  bool      `json:"healthy"`
	Findings []Finding `json:"findings"`
}

// runDoctor reports every finding, under the write lock, changing nothing.
func runDoctor(env Env, in KindsInput, w *errs.Collector) (any, *errs.Error) {
	var out DoctorOutput
	e := store.Diagnose(env.Env, w, func(tx *store.Tx) *errs.Error {
		fs, e := diagnose(tx)
		if e != nil {
			return e
		}
		out = DoctorOutput{Healthy: fs.healthy(), Findings: fs.report(in.Kinds, in.Kinds)}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// diagnose runs every check (implementation-spec.md, Checks). Its only
// errors are internal: an OS error with no symbolic name.
func diagnose(tx *store.Tx) (findings, *errs.Error) {
	fs := findings{}
	x := tx.Index()
	sv := x.Survey
	if sv == nil {
		return nil, errs.Internal("diagnose outside a diagnostic transaction")
	}

	// Every task file counts toward last_id by the ID in its filename,
	// usable or not.
	var maxID int64
	for _, l := range x.Tasks {
		maxID = max(maxID, int64(l.ID))
	}
	metaPath := tx.Path(store.MetaName)
	switch state, metaErr := tx.MetaState(); state {
	case store.MetaMissing:
		it := Item{
			Paths: []string{metaPath}, LastID: &maxID, Action: ptr(actionCreateMetadata),
			Suggest: ptr("ftask repair --kinds metadata-missing, unless a task with an ID above " + fmt.Sprint(maxID) + " was ever deleted; then rebuild ftask.json by hand with that ID as last_id"),
		}
		// A folder that can't be listed may hold a higher ID, so last_id
		// can't be rebuilt from the walk.
		if !x.Complete() {
			it.Action, it.Suggest = nil, ptr("fix the unreadable folders first; then ftask repair --kinds metadata-missing")
		}
		fs.add(kindMetadataMissing, it)
	case store.MetaOK:
		lastID := tx.Meta().LastID
		for _, l := range x.Tasks {
			if int64(l.ID) > lastID {
				fs.add(kindIDAboveLastID, Item{
					Paths: []string{tx.Path(l.Rel())}, IDs: []model.ID{l.ID}, LastID: &maxID,
					Action: ptr(actionRaiseLastID), Suggest: ptr("ftask repair"),
				})
			}
		}
	default:
		fs.add(kindMetadataUnusable, Item{
			Paths: []string{metaPath}, Error: metaErr,
			Suggest: ptr("fix ftask.json by hand, or restore it from git; a binary that supports its format can use it as it is"),
		})
	}

	for _, rel := range sv.Temps {
		fs.add(kindTempLeftover, Item{Paths: []string{tx.Path(rel)}, Action: ptr(actionRemove), Suggest: ptr("ftask repair"), rel: rel})
	}
	for _, rel := range sv.Nested {
		fs.add(kindNestedTree, Item{Paths: []string{tx.Path(rel)}, Suggest: ptr("move the tree it belongs to out of this one, or remove this ftask.json if the folder is part of this tree")})
	}
	for _, s := range sv.Strays {
		p := []string{tx.Path(s.Rel)}
		switch s.Reason {
		case store.StraySymlink:
			fs.add(kindSkippedEntry, Item{Paths: p, Reason: s.Reason, Suggest: ptr("ftask never follows a symlink, so what it leads to is not part of the tree: move the real folder or file in instead, or remove the link")})
		case store.StrayType:
			fs.add(kindSkippedEntry, Item{Paths: p, Reason: s.Reason, Suggest: ptr("rename or remove it: it has a folder's or task file's name, but the wrong type, so ftask skips it")})
		default:
			fs.add(kindStrayEntry, Item{Paths: p, Suggest: ptr("nothing to do: ftask ignores it")})
		}
	}
	for _, u := range x.Unreadable {
		rel := store.FolderRel(u.Folder)
		code, e := tx.Code(rel, u.Err)
		if e != nil {
			return nil, e
		}
		fs.add(kindUnreadableFolder, Item{Paths: []string{tx.Path(rel)}, Code: code, Suggest: ptr("fix its permissions; until then its tasks are missing from every result")})
	}

	// Task files: unusable ones, and the usable ones' edges.
	usable := map[model.ID][]*store.Loaded{}
	for _, l := range x.Tasks {
		ld := tx.Load(l)
		switch ld.State {
		case store.Vanished:
		case store.Usable:
			usable[l.ID] = append(usable[l.ID], ld)
		default:
			fs.add(kindUnusableFile, Item{
				Paths: []string{tx.Path(l.Rel())}, IDs: []model.ID{l.ID}, Error: tx.Needed(ld),
				Suggest: ptr("fix the file by hand, or restore it from git"),
			})
		}
	}

	if e := duplicates(tx, x, fs); e != nil {
		return nil, e
	}

	// A blocker may be in a folder that could not be listed, so none is
	// called dangling then, as for the warning.
	if x.Complete() {
		for _, l := range x.Tasks {
			ld := tx.Load(l)
			if ld.State != store.Usable {
				continue
			}
			for _, b := range ld.Task.BlockedBy {
				if len(x.Locations(b)) == 0 {
					fs.add(kindDanglingReference, Item{
						Paths: []string{tx.Path(l.Rel())}, IDs: []model.ID{l.ID, b},
						Action: ptr(actionRemoveReference), Suggest: ptr("ftask repair"), loc: l, missing: b,
					})
				}
			}
		}
	}

	edges := map[model.ID][]model.ID{}
	for id, lds := range usable {
		var lists [][]model.ID
		for _, ld := range lds {
			lists = append(lists, ld.Task.BlockedBy)
		}
		edges[id] = graph.Edges(lists...)
	}
	for _, g := range graph.CycleGroups(edges) {
		c := graph.ExampleCycle(g, edges)
		fs.add(kindCycle, Item{
			IDs: c, Group: g,
			Suggest: ptr(fmt.Sprintf("remove any one blocker on the cycle, e.g. ftask unblock %d --blockers %d", c[0], c[1])),
		})
	}

	if e := orphans(tx, x, fs); e != nil {
		return nil, e
	}
	return fs, nil
}

// duplicates adds a duplicate-id finding for each ID with several task files,
// saying whether the copies are the same, byte for byte.
func duplicates(tx *store.Tx, x *store.Index, fs findings) *errs.Error {
	seen := map[model.ID]bool{}
	for _, l := range x.Tasks {
		locs := x.Locations(l.ID)
		if len(locs) < 2 || seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		var paths []string
		var first []byte
		identical := true
		for i, c := range locs {
			paths = append(paths, tx.Path(c.Rel()))
			data, err := tx.ReadFile(c.Rel())
			switch {
			case err != nil:
				identical = false
			case i == 0:
				first = data
			case !bytes.Equal(data, first):
				identical = false
			}
		}
		suggest := "keep the copy that is right, and remove the others, or move their task to a new ID with ftask create"
		if identical {
			suggest = "the copies are the same: remove all but one"
		}
		fs.add(kindDuplicateID, Item{Paths: paths, IDs: []model.ID{l.ID}, Identical: &identical, Suggest: &suggest})
	}
	return nil
}

// orphans adds an orphan-notes finding for each .md with no task file of its
// ID beside it; one that can't be looked at is unreadable, with its code.
func orphans(tx *store.Tx, x *store.Index, fs findings) *errs.Error {
	for _, n := range x.Survey.Notes {
		locs := x.Locations(n.ID)
		if slices.Contains(locs, n) {
			continue
		}
		rel := n.NotesRel()
		it := Item{Paths: []string{tx.Path(rel)}, IDs: []model.ID{n.ID}, rel: rel}
		for _, l := range locs {
			it.Paths = append(it.Paths, tx.Path(l.Rel()))
		}
		reason, linked, err := orphanReason(tx, rel, locs)
		if errno, ok := errs.ErrnoOf(err); ok && errno == syscall.ENOENT {
			continue // gone since the walk: nothing to say about it
		}
		if err != nil {
			code, e := tx.Code(rel, err)
			if e != nil {
				return e
			}
			reason, it.Code = orphanUnreadable, code
		}
		it.Reason, it.linked = reason, linked
		switch reason {
		case orphanUnreadable:
			it.Suggest = ptr("fix its permissions, then run ftask doctor again to see what it is")
		case orphanEmpty, orphanLinked:
			it.Action, it.Suggest = ptr(actionRemove), ptr("ftask repair")
		case orphanTaskElsewhere:
			it.Suggest = ptr("merge its text into " + tx.Path(locs[0].NotesRel()) + ", then remove it")
		default:
			it.Suggest = ptr("keep its text elsewhere if it is still wanted, then remove it")
		}
		fs.add(kindOrphanNotes, it)
	}
	return nil
}

// orphanReason is what an orphaned .md at rel is, given the task files of
// its ID elsewhere (locs): for linked, also the notes it is the same file as.
func orphanReason(tx *store.Tx, rel string, locs []store.Location) (reason, linked string, err error) {
	fi, err := tx.Lstat(rel)
	if err != nil {
		return "", "", err
	}
	if fi.Size() == 0 {
		return orphanEmpty, "", nil
	}
	for _, l := range locs {
		if other, err := tx.Lstat(l.NotesRel()); err == nil && os.SameFile(fi, other) {
			return orphanLinked, l.NotesRel(), nil
		}
	}
	if len(locs) > 0 {
		return orphanTaskElsewhere, "", nil
	}
	return orphanNoTask, "", nil
}

func ptr[T any](v T) *T { return &v }
