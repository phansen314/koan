package model

import (
	"fmt"
	"strings"

	"github.com/phansen314/koan/internal/core"
)

// Check reports every invariant of the rules doc, I1 to I12, that the tree
// breaks; I10 is the model's own: a refused change leaves the tree as it
// was. It returns nil when all hold.
func (t *Tree) Check() []string {
	var bad []string
	fail := func(format string, args ...any) { bad = append(bad, fmt.Sprintf(format, args...)) }

	for id, x := range t.Tasks {
		if x.ID != id {
			fail("I1: task keyed %d has ID %d", id, x.ID)
		}
		if _, ok := t.Folders[id]; ok {
			fail("I1: @%d is both a task and a folder", id)
		}
	}
	for id, f := range t.Folders {
		if f.ID != id {
			fail("I1: folder keyed %d has ID %d", id, f.ID)
		}
	}
	for id := range t.Tasks {
		if id > t.Issued {
			fail("I2: @%d is past the last ID issued", id)
		}
	}
	for id := range t.Folders {
		if id > t.Issued {
			fail("I2: @%d is past the last ID issued", id)
		}
	}

	// I3: every parent chain reaches the root.
	for id, f := range t.Folders {
		seen := map[int64]bool{id: true}
		for p := f.Parent; p != core.RootID; p = t.Folders[p].Parent {
			if _, ok := t.Folders[p]; !ok {
				fail("I3: @%d's parent chain reaches @%d, which isn't a folder", id, p)
				break
			}
			if seen[p] {
				fail("I3: @%d is inside itself", id)
				break
			}
			seen[p] = true
		}
	}
	for id, x := range t.Tasks {
		if _, ok := t.Folders[x.Folder]; !ok && x.Folder != core.RootID {
			fail("I3: @%d sits in @%d, which isn't a folder", id, x.Folder)
		}
	}

	// I4: siblings differ ignoring case.
	for _, a := range t.Folders {
		for _, b := range t.Folders {
			if a.ID < b.ID && a.Parent == b.Parent && strings.EqualFold(a.Name, b.Name) {
				fail("I4: @%d and @%d are siblings named %q and %q", a.ID, b.ID, a.Name, b.Name)
			}
		}
	}

	for id, x := range t.Tasks {
		for to := range x.BlockedBy {
			if _, ok := t.Tasks[to]; !ok {
				fail("I5: @%d waits on @%d, which isn't a task", id, to)
			}
			if to == id {
				fail("I6: @%d waits on itself", id)
			}
		}
	}

	// I7: no cycle. Adding every edge to an empty graph finds one if any.
	var edges []core.Edge
	for _, id := range sortedKeys(keys(t.Tasks)) {
		for _, to := range t.BlockedBy(id) {
			edges = append(edges, core.Edge{From: id, To: to})
		}
	}
	if cycle, _ := core.FindCycle(func(int64) []int64 { return nil }, edges); cycle != nil {
		fail("I7: cycle %s", core.FormatIDs(cycle))
	}

	for id, cl := range t.Claims {
		x, ok := t.Tasks[id]
		switch {
		case cl.Task != id:
			fail("I8: claim keyed %d names @%d", id, cl.Task)
		case !ok:
			fail("I8: claim on @%d, which isn't a task", id)
		case !x.Open():
			fail("I8: claim on @%d, which is complete", id)
		}
	}

	bad = append(bad, t.checkValues()...)

	// I11: at most one gate of each end, in its folder; none in the root.
	gates := map[int64]map[core.End]int{}
	for id, x := range t.Tasks {
		if x.Gate == core.EndNone {
			continue
		}
		if x.Folder == core.RootID {
			fail("I11: gate @%d is in the root", id)
			continue
		}
		if gates[x.Folder] == nil {
			gates[x.Folder] = map[core.End]int{}
		}
		gates[x.Folder][x.Gate]++
		if gates[x.Folder][x.Gate] == 2 {
			fail("I11: @%d has two %s gates", x.Folder, x.Gate)
		}
	}

	for id := range t.Index {
		if _, ok := t.Node(id); ok {
			fail("I12: @%d is both archived and in the tree", id)
		}
	}
	for i, a := range t.Archives {
		for _, x := range a.Tasks {
			if t.Index[x.ID] != i {
				fail("I12: archived @%d is missing from the index", x.ID)
			}
		}
		for _, f := range a.Folders {
			if t.Index[f.ID] != i {
				fail("I12: archived @%d is missing from the index", f.ID)
			}
		}
	}
	return bad
}

// checkValues checks I9: values follow their rules.
func (t *Tree) checkValues() []string {
	var bad []string
	fail := func(format string, args ...any) { bad = append(bad, fmt.Sprintf("I9: "+format, args...)) }
	stamp := func(id int64, field, s string) {
		if _, err := core.ParseTimestamp(s); err != nil {
			fail("@%d %s %q", id, field, s)
		}
	}
	for id, f := range t.Folders {
		if !core.ValidID(id) || !core.ValidName(f.Name) {
			fail("folder @%d named %q", id, f.Name)
		}
		if f.Title != nil {
			if s, err := core.CheckTitle(*f.Title); err != nil || s != *f.Title {
				fail("folder @%d title %q", id, *f.Title)
			}
		}
		if f.Extra == nil {
			fail("folder @%d has no extra object", id)
		}
	}
	for id, x := range t.Tasks {
		if !core.ValidID(id) {
			fail("task ID %d", id)
		}
		if s, err := core.CheckTitle(x.Title); err != nil || s != x.Title {
			fail("@%d title %q", id, x.Title)
		}
		if x.Priority != nil && core.CheckInt(*x.Priority) != nil {
			fail("@%d priority %d", id, *x.Priority)
		}
		stamp(id, "created_at", x.CreatedAt)
		stamp(id, "updated_at", x.UpdatedAt)
		if x.CompletedAt != nil {
			stamp(id, "completed_at", *x.CompletedAt)
		}
		for tag := range x.Tags {
			if !core.ValidTag(tag) {
				fail("@%d tag %q", id, tag)
			}
		}
		if x.Extra == nil {
			fail("@%d has no extra object", id)
		}
	}
	for id, cl := range t.Claims {
		stamp(id, "claimed_at", cl.ClaimedAt)
		if cl.Extra == nil {
			fail("claim on @%d has no extra object", id)
		}
	}
	return bad
}

func keys[V any](m map[int64]V) map[int64]bool {
	out := make(map[int64]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
