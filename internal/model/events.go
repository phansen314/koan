package model

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/core"
)

// Event is one event a change records, as rules The log gives it: its
// kind, the task or folder it is about, zero for the root, where it
// happened as the change found it, and what its kind carries, as JSON
// makes it. An archived event's paths are written from its subject's
// archived notes and directory, which only the disk knows, as
// "$<id>.notes" and "$<id>.dir", the rest of a path after it.
type Event struct {
	Kind   string
	ID     int64
	Folder int64 // core.RootID for the root
	Path   string
	Data   map[string]any
}

// hints is what a change in progress notes that comparing the tree before
// and after it can't tell: what was given, the subjects in input order,
// and the edges in the order they were written or removed.
type hints struct {
	given    map[int64]bool
	subjects []int64
	edges    []core.Edge
}

// given notes something given to the change, as a subject or as what it
// makes.
func (c *Tree) given(id int64) {
	if c.h != nil {
		c.h.given[id] = true
	}
}

// subject notes a subject of the change, in input order.
func (c *Tree) subject(id int64) {
	if c.h != nil {
		c.h.subjects = append(c.h.subjects, id)
		c.h.given[id] = true
	}
}

// edged notes an edge the change wrote or removed, in that order.
func (c *Tree) edged(e core.Edge) {
	if c.h != nil {
		c.h.edges = append(c.h.edges, e)
	}
}

// events is what a change from b to a records, found by comparing the
// two, every field, state, place, edge and claim, readiness and finished,
// never by following the change's steps: so a change that alters
// something the rules log without saying so is still found. h orders
// what the comparison can't: the subjects, and the edges given.
func events(b, a *Tree, h *hints) []Event {
	var out []Event
	add := func(kind string, id, folder int64, path string, data map[string]any) {
		if data == nil {
			data = map[string]any{}
		}
		out = append(out, Event{Kind: kind, ID: id, Folder: folder, Path: path, Data: data})
	}
	// Where a task stands: as the change found it, or as it made it.
	taskAt := func(id int64) (int64, string) {
		if x, ok := b.Tasks[id]; ok {
			return x.Folder, b.Path(x.Folder)
		}
		x := a.Tasks[id]
		return x.Folder, a.Path(x.Folder)
	}

	// 1. What it made, in the order its IDs were issued.
	for id := b.Issued + 1; id <= a.Issued; id++ {
		if x, ok := a.Tasks[id]; ok {
			var gate any
			if x.Gate != core.EndNone {
				gate = string(x.Gate)
			}
			add("created", id, x.Folder, a.Path(x.Folder), map[string]any{
				"given": h.given[id], "gate": gate, "title": x.Title, "priority": priority(x.Priority),
				"tags": sortedKeys(x.Tags), "extra": orEmpty(x.Extra), "readiness": readiness(a, id),
			})
		} else if f, ok := a.Folders[id]; ok {
			add("created", id, id, a.Path(id), map[string]any{
				"given": h.given[id], "name": f.Name, "title": f.Title, "extra": orEmpty(f.Extra), "finished": a.Finished(id),
			})
		}
	}

	// 2. Each edge written or removed: in the order given, then, for
	// those a delete or archive removes, by task, then blocker. An edge
	// between two things that went records nothing.
	var edges []core.Edge
	blocked := map[core.Edge]bool{}
	for _, x := range a.Tasks {
		for to := range x.BlockedBy {
			if e := (core.Edge{From: x.ID, To: to}); !b.edge(e) {
				edges, blocked[e] = append(edges, e), true
			}
		}
	}
	for _, x := range b.Tasks {
		for to := range x.BlockedBy {
			e := core.Edge{From: x.ID, To: to}
			_, from := a.Tasks[e.From]
			_, blocker := a.Tasks[e.To]
			if !a.edge(e) && (from || blocker) {
				edges = append(edges, e)
			}
		}
	}
	rank := func(e core.Edge) int {
		if i := slices.Index(h.edges, e); i >= 0 {
			return i
		}
		return len(h.edges)
	}
	slices.SortFunc(edges, func(x, y core.Edge) int {
		return cmp.Or(cmp.Compare(rank(x), rank(y)), cmp.Compare(x.From, y.From), cmp.Compare(x.To, y.To))
	})
	for _, e := range edges {
		kind := "unblocked"
		if blocked[e] {
			kind = "blocked"
		}
		folder, path := taskAt(e.From)
		add(kind, e.From, folder, path, map[string]any{"blocker": e.To})
	}

	// 3. Each subject's own events, in input order; then any other
	// task or folder whose row changed, by ID, which no change should
	// have.
	var own []int64
	for _, id := range h.subjects {
		if !slices.Contains(own, id) {
			own = append(own, id)
		}
	}
	var others []int64
	for _, id := range slices.Sorted(maps.Keys(b.Tasks)) {
		others = append(others, id)
	}
	for _, id := range slices.Sorted(maps.Keys(b.Folders)) {
		others = append(others, id)
	}
	for _, id := range others {
		if !slices.Contains(own, id) {
			own = append(own, id)
		}
	}
	for _, id := range own {
		if was, ok := b.Tasks[id]; ok {
			x, ok := a.Tasks[id]
			if !ok {
				continue
			}
			at, path := was.Folder, b.Path(was.Folder)
			var changes []any
			field := func(name string, before, after any) {
				changes = append(changes, map[string]any{"field": name, "before": before, "after": after})
			}
			if x.Title != was.Title {
				field("title", was.Title, x.Title)
			}
			if !equalPtr(x.Priority, was.Priority) {
				field("priority", priority(was.Priority), priority(x.Priority))
			}
			if !maps.Equal(x.Tags, was.Tags) {
				field("tags", sortedKeys(was.Tags), sortedKeys(x.Tags))
			}
			if !equalJSON(x.Extra, was.Extra) {
				field("extra", orEmpty(was.Extra), orEmpty(x.Extra))
			}
			if changes != nil {
				add("updated", id, at, path, map[string]any{"changes": changes})
			}
			cb, claimedBefore := b.Claims[id]
			_, claimedAfter := a.Claims[id]
			switch {
			case was.Open() && !x.Open():
				add("completed", id, at, path, nil)
			case !was.Open() && x.Open():
				add("reopened", id, at, path, map[string]any{"readiness": readiness(a, id)})
			}
			switch {
			case !claimedBefore && claimedAfter:
				add("claimed", id, at, path, map[string]any{"extra": orEmpty(a.Claims[id].Extra)})
			case claimedBefore && !claimedAfter:
				add("unclaimed", id, at, path, map[string]any{"claimed_at": cb.ClaimedAt, "extra": orEmpty(cb.Extra)})
			}
			if x.Folder != was.Folder {
				add("moved", id, at, path, map[string]any{
					"before": folderAt(b, was.Folder), "after": folderAt(a, x.Folder),
				})
			}
		} else if was, ok := b.Folders[id]; ok {
			f, ok := a.Folders[id]
			if !ok {
				continue
			}
			var changes []any
			if !equalPtr(f.Title, was.Title) {
				changes = append(changes, map[string]any{"field": "title", "before": was.Title, "after": f.Title})
			}
			if !equalJSON(f.Extra, was.Extra) {
				changes = append(changes, map[string]any{"field": "extra", "before": orEmpty(was.Extra), "after": orEmpty(f.Extra)})
			}
			if changes != nil {
				add("updated", id, id, b.Path(id), map[string]any{"changes": changes})
			}
			if f.Parent != was.Parent || f.Name != was.Name {
				add("moved", id, id, b.Path(id), map[string]any{"before": b.Path(id), "after": a.Path(id)})
			}
		}
	}

	// 4. What left, ascending: a delete's ended claims, then its
	// deleted; an archive's archived.
	var gone []int64
	for _, id := range slices.Sorted(maps.Keys(b.Tasks)) {
		if _, ok := a.Tasks[id]; !ok {
			gone = append(gone, id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(b.Folders)) {
		if _, ok := a.Folders[id]; !ok {
			gone = append(gone, id)
		}
	}
	slices.Sort(gone)
	for _, id := range gone {
		if cl, ok := b.Claims[id]; ok && !a.Archived(id) {
			folder, path := taskAt(id)
			add("unclaimed", id, folder, path, map[string]any{"claimed_at": cl.ClaimedAt, "extra": orEmpty(cl.Extra)})
		}
	}
	for _, id := range gone {
		var folder int64
		var data map[string]any
		if x, ok := b.Tasks[id]; ok {
			folder = x.Folder
			data = map[string]any{"given": h.given[id], "ref": core.Kept(id, b.Path(x.Folder), x.Gate), "title": x.Title}
		} else {
			folder = id
			data = map[string]any{"given": h.given[id], "ref": core.Kept(id, b.Path(id), core.EndNone), "title": b.Folders[id].Title}
		}
		kind := "deleted"
		if a.Archived(id) {
			kind = "archived"
			data["notes_path"], data["dir"] = archivedAt(b, a, id)
		}
		add(kind, id, folder, b.Path(folder), data)
	}

	// 5. Readiness the change altered, of tasks open before and after.
	var released, reblocked []int64
	for _, id := range slices.Sorted(maps.Keys(b.Tasks)) {
		if !b.Open(id) || !a.Open(id) {
			continue
		}
		switch was, now := b.Ready(id), a.Ready(id); {
		case !was && now:
			released = append(released, id)
		case was && !now:
			reblocked = append(reblocked, id)
		}
	}
	for _, id := range released {
		folder, path := taskAt(id)
		add("released", id, folder, path, nil)
	}
	for _, id := range reblocked {
		folder, path := taskAt(id)
		add("reblocked", id, folder, path, nil)
	}

	// 6. Finished, of folders there before and after, the deepest first
	// as the change found them, then by ID.
	var folders []int64
	for id := range b.Folders {
		if _, ok := a.Folders[id]; ok && b.Finished(id) != a.Finished(id) {
			folders = append(folders, id)
		}
	}
	slices.SortFunc(folders, func(x, y int64) int {
		return cmp.Or(cmp.Compare(depth(b, y), depth(b, x)), cmp.Compare(x, y))
	})
	for _, id := range folders {
		kind := "unfinished"
		if a.Finished(id) {
			kind = "finished"
		}
		add(kind, id, id, b.Path(id), nil)
	}
	return out
}

func (t *Tree) edge(e core.Edge) bool {
	x, ok := t.Tasks[e.From]
	return ok && x.BlockedBy[e.To]
}

func priority(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func readiness(t *Tree, id int64) string {
	if t.Ready(id) {
		return "ready"
	}
	return "blocked"
}

// folderAt is a task's folder as events give it: its ID, null for the
// root, and its path.
func folderAt(t *Tree, folder int64) map[string]any {
	var id any
	if folder != core.RootID {
		id = folder
	}
	return map[string]any{"id": id, "path": t.Path(folder)}
}

func depth(t *Tree, folder int64) int {
	return strings.Count(t.Path(folder), "/")
}

// archivedAt is where an archived item's notes, and a folder's directory,
// went: for a subject, its own; for what went with a folder, where it
// sits in that folder's archived directory.
func archivedAt(b, a *Tree, id int64) (notes, dir any) {
	s := a.Archives[a.Index[id]].ID
	_, folder := b.Folders[id]
	if id == s {
		notes = fmt.Sprintf("$%d.notes", s)
		if folder {
			dir = fmt.Sprintf("$%d.dir", s)
		}
		return notes, dir
	}
	// The path below the subject, each folder as its directory.
	below := func(f int64) string {
		rel := strings.TrimPrefix(b.Path(f), b.Path(s))
		return fmt.Sprintf("$%d.dir", s) + strings.ReplaceAll(rel, "/", "/@")
	}
	if folder {
		return below(id) + "/@notes.md", below(id)
	}
	x := b.Tasks[id]
	name := fmt.Sprintf("@%d.md", id)
	if x.Gate != core.EndNone {
		name = "@" + string(x.Gate) + ".md"
	}
	return below(x.Folder) + "/" + name, nil
}
