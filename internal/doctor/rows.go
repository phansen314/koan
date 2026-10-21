package doctor

import (
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// folderRow is a folder's row as doctor reads it: as stored, whatever a
// hand edit left.
type folderRow struct {
	id     int64
	parent int64 // core.RootID for the root
	name   string
	title  *string
}

// taskRow is a task's row as doctor reads it.
type taskRow struct {
	id                int64
	folder            int64 // core.RootID for the root
	gate              string
	title             string
	priority, priType string
	created, updated  string
	completed         *string
	tags              []string
	claimed           *string
}

// issuedRow is an ID's entry in issued.
type issuedRow struct {
	state   string
	archive *string
}

// rows is every row doctor checks, and the tree they make.
type rows struct {
	folders map[int64]*folderRow
	tasks   map[int64]*taskRow
	issued  map[int64]issuedRow
	edges   []core.Edge
	// children is each folder's folders, by name byte for byte; paths
	// is each folder's path, for the folders whose parent chain reaches
	// the root, and the root's.
	children map[int64][]int64
	paths    map[int64]string
	gates    map[int64]map[string][]int64
}

func load(tx *store.Tx) (*rows, error) {
	r := &rows{folders: map[int64]*folderRow{}, tasks: map[int64]*taskRow{}, issued: map[int64]issuedRow{},
		children: map[int64][]int64{}, paths: map[int64]string{core.RootID: "/"}, gates: map[int64]map[string][]int64{}}
	err := query(tx, "SELECT id, parent, name, title FROM folder", func(rs *sql.Rows) error {
		f := &folderRow{}
		var parent sql.NullInt64
		var title sql.NullString
		if err := rs.Scan(&f.id, &parent, &f.name, &title); err != nil {
			return err
		}
		f.parent = parent.Int64
		if title.Valid {
			f.title = &title.String
		}
		r.folders[f.id] = f
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = query(tx, `SELECT id, folder, gate, title, priority, typeof(priority), created_at, completed_at, updated_at FROM task`, func(rs *sql.Rows) error {
		t := &taskRow{}
		var folder sql.NullInt64
		var gate, priority, completed sql.NullString
		if err := rs.Scan(&t.id, &folder, &gate, &t.title, &priority, &t.priType, &t.created, &completed, &t.updated); err != nil {
			return err
		}
		t.folder, t.gate, t.priority = folder.Int64, gate.String, priority.String
		if completed.Valid {
			t.completed = &completed.String
		}
		r.tasks[t.id] = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = query(tx, "SELECT task, tag FROM tag ORDER BY task, tag COLLATE BINARY", func(rs *sql.Rows) error {
		var id int64
		var tag string
		if err := rs.Scan(&id, &tag); err != nil {
			return err
		}
		if t, ok := r.tasks[id]; ok {
			t.tags = append(t.tags, tag)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = query(tx, "SELECT task, claimed_at FROM claim", func(rs *sql.Rows) error {
		var id int64
		var at string
		if err := rs.Scan(&id, &at); err != nil {
			return err
		}
		if t, ok := r.tasks[id]; ok {
			t.claimed = &at
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = query(tx, "SELECT task, blocker FROM edge ORDER BY task, blocker", func(rs *sql.Rows) error {
		var e core.Edge
		if err := rs.Scan(&e.From, &e.To); err != nil {
			return err
		}
		r.edges = append(r.edges, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = query(tx, "SELECT id, state, archive FROM issued", func(rs *sql.Rows) error {
		var id int64
		var i issuedRow
		var archive sql.NullString
		if err := rs.Scan(&id, &i.state, &archive); err != nil {
			return err
		}
		if archive.Valid {
			i.archive = &archive.String
		}
		r.issued[id] = i
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, f := range r.folders {
		r.children[f.parent] = append(r.children[f.parent], f.id)
	}
	for _, c := range r.children {
		slices.SortFunc(c, func(a, b int64) int { return strings.Compare(r.folders[a].name, r.folders[b].name) })
	}
	// Paths go down from the root, so a folder on a cycle of parents has
	// none.
	var walk func(id int64)
	walk = func(id int64) {
		for _, c := range r.children[id] {
			if id == core.RootID {
				r.paths[c] = "/" + r.folders[c].name
			} else {
				r.paths[c] = r.paths[id] + "/" + r.folders[c].name
			}
			walk(c)
		}
	}
	walk(core.RootID)
	for _, t := range r.tasks {
		if t.gate != "" {
			if r.gates[t.folder] == nil {
				r.gates[t.folder] = map[string][]int64{}
			}
			r.gates[t.folder][t.gate] = append(r.gates[t.folder][t.gate], t.id)
		}
	}
	return r, nil
}

func query(tx *store.Tx, q string, row func(*sql.Rows) error) error {
	rs, err := tx.Query(q)
	if err != nil {
		return err
	}
	defer rs.Close()
	for rs.Next() {
		if err := row(rs); err != nil {
			return err
		}
	}
	return rs.Err()
}

// sorted is the keys of a map, lowest first.
func sorted[V any](m map[int64]V) []int64 {
	var out []int64
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// invalid finds the rows that break an invariant SQL can't check.
func (c *checker) invalid() {
	c.parentCycles()
	c.siblings()
	c.edgeCycles()
	c.values()
	c.gateRows()
	c.index()
}

func (c *checker) invalidRow(invariant string, ids []int64, format string, args ...any) {
	slices.Sort(ids)
	c.add(newFinding(InvalidRow, invariant, fmt.Sprintf(format, args...)+" A person fixes it in the database.", ids, nil))
}

// parentCycles finds folders whose parent chain never reaches the root
// (I3): one finding for each cycle of parents.
func (c *checker) parentCycles() {
	done := map[int64]bool{}
	for _, id := range sorted(c.folders) {
		var chain []int64
		at := id
		for at != core.RootID && !done[at] && !slices.Contains(chain, at) {
			chain = append(chain, at)
			f, ok := c.folders[at]
			if !ok {
				break
			}
			at = f.parent
		}
		if i := slices.Index(chain, at); i >= 0 {
			cycle := slices.Clone(chain[i:])
			if len(cycle) == 1 {
				c.invalidRow("I3", cycle, "Folder @%d is its own parent, so its parent chain never reaches the root.", at)
			} else {
				c.invalidRow("I3", cycle, "Folders %s are each other's parents, so their parent chain never reaches the root.", core.FormatIDs(sortedIDs(cycle)))
			}
		}
		for _, f := range chain {
			done[f] = true
		}
	}
}

func sortedIDs(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// siblings finds folders in one parent whose names are equal ignoring
// case (I4).
func (c *checker) siblings() {
	for _, parent := range sorted(c.children) {
		var seen []int64
		for _, id := range c.children[parent] {
			if slices.Contains(seen, id) {
				continue
			}
			group := []int64{id}
			for _, other := range c.children[parent] {
				if other != id && strings.EqualFold(c.folders[other].name, c.folders[id].name) {
					group = append(group, other)
				}
			}
			seen = append(seen, group...)
			if len(group) > 1 {
				c.invalidRow("I4", group, "Folders %s share a parent and have names equal ignoring case.", core.FormatIDs(sortedIDs(group)))
			}
		}
	}
}

// edgeCycles finds tasks that wait on themselves (I6), and cycles of
// tasks that wait on each other (I7), one finding for each set of tasks
// that wait on each other.
func (c *checker) edgeCycles() {
	next := map[int64][]int64{}
	for _, e := range c.edges {
		if e.From == e.To {
			c.invalidRow("I6", []int64{e.From}, "@%d waits on itself.", e.From)
			continue
		}
		next[e.From] = append(next[e.From], e.To)
	}
	for _, scc := range components(next) {
		c.invalidRow("I7", scc, "Tasks %s wait on each other in a cycle.", core.FormatIDs(scc))
	}
}

// components is the strongly connected components of a graph with more
// than one node, each sorted, lowest first.
func components(next map[int64][]int64) [][]int64 {
	index := map[int64]int{}
	low := map[int64]int{}
	on := map[int64]bool{}
	var stack []int64
	var out [][]int64
	n := 0
	var visit func(v int64)
	visit = func(v int64) {
		index[v], low[v] = n, n
		n++
		stack = append(stack, v)
		on[v] = true
		for _, w := range next[v] {
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if on[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var scc []int64
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 {
				slices.Sort(scc)
				out = append(out, scc)
			}
		}
	}
	for _, v := range sorted(next) {
		if _, seen := index[v]; !seen {
			visit(v)
		}
	}
	return out
}

// values finds rows whose values break their rules (I9): names, tags,
// titles, timestamps and integers. One finding for each task or folder,
// naming every value at fault.
func (c *checker) values() {
	for _, id := range sorted(c.folders) {
		f := c.folders[id]
		var bad []string
		if !core.ValidName(f.name) {
			bad = append(bad, fmt.Sprintf("its name %q isn't a folder name", f.name))
		}
		if f.title != nil && !storedTitle(*f.title) {
			bad = append(bad, fmt.Sprintf("its title %q isn't a title", *f.title))
		}
		if len(bad) > 0 {
			c.invalidRow("I9", []int64{id}, "Folder @%d: %s.", id, strings.Join(bad, "; "))
		}
	}
	for _, id := range sorted(c.tasks) {
		t := c.tasks[id]
		var bad []string
		if !storedTitle(t.title) {
			bad = append(bad, fmt.Sprintf("its title %q isn't a title", t.title))
		}
		switch t.priType {
		case "null":
		case "integer":
			if n, err := strconv.ParseInt(t.priority, 10, 64); err != nil || core.CheckInt(n) != nil {
				bad = append(bad, fmt.Sprintf("its priority %s is outside ±(2^53 − 1)", t.priority))
			}
		default:
			bad = append(bad, fmt.Sprintf("its priority %q isn't an integer", t.priority))
		}
		for _, s := range []struct {
			field string
			value *string
		}{{"created_at", &t.created}, {"completed_at", t.completed}, {"updated_at", &t.updated}, {"claim's claimed_at", t.claimed}} {
			if s.value != nil {
				if _, err := core.ParseTimestamp(*s.value); err != nil {
					bad = append(bad, fmt.Sprintf("its %s %q isn't a timestamp", s.field, *s.value))
				}
			}
		}
		for _, tag := range t.tags {
			if !core.ValidTag(tag) {
				bad = append(bad, fmt.Sprintf("its tag %q isn't a tag", tag))
			}
		}
		if len(bad) > 0 {
			c.invalidRow("I9", []int64{id}, "Task @%d: %s.", id, strings.Join(bad, "; "))
		}
	}
}

// storedTitle reports whether a title is one as koan stores it: trimmed,
// and a title by the task rule.
func storedTitle(s string) bool {
	t, err := core.CheckTitle(s)
	return err == nil && t == s
}

// gateRows finds gates out of place (I11): one in the root, or a second
// gate at one end of a folder.
func (c *checker) gateRows() {
	for _, id := range sorted(c.tasks) {
		if t := c.tasks[id]; t.gate != "" && t.folder == core.RootID {
			c.invalidRow("I11", []int64{id}, "@%d is a %s gate in the root, which has none.", id, t.gate)
		}
	}
	for _, folder := range sorted(c.gates) {
		if folder == core.RootID {
			continue
		}
		for _, end := range []string{"start", "done"} {
			if ids := c.gates[folder][end]; len(ids) > 1 {
				c.invalidRow("I11", slices.Clone(ids), "Folder @%d has %d %s gates: %s.", folder, len(ids), end, core.FormatIDs(sortedIDs(ids)))
			}
		}
	}
}

// index finds archived IDs the index doesn't say where they went, and
// IDs in the tree the index holds (I12).
func (c *checker) index() {
	for _, id := range sorted(c.issued) {
		i := c.issued[id]
		_, task := c.tasks[id]
		_, folder := c.folders[id]
		switch {
		case i.state == "archived" && (i.archive == nil || *i.archive == ""):
			c.invalidRow("I12", []int64{id}, "@%d is archived, and the index doesn't say where it went.", id)
		case i.state == "archived" && (task || folder):
			c.invalidRow("I12", []int64{id}, "@%d is in the tree, and the index says it was archived.", id)
		}
	}
}
