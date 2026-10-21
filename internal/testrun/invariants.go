package testrun

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/core"

	_ "modernc.org/sqlite"
)

// Snapshot is what CheckRoot read of a root: enough to check, against the
// next snapshot, what must hold across changes, such as I2.
type Snapshot struct {
	// Issued is every ID issued, with its kind and state.
	Issued map[int64][2]string
	// Gates is each live gate's folder, and Created each live task's
	// created_at, which never change.
	Gates   map[int64]int64
	Created map[int64]string
	// Pending is how many filesystem steps are pending, stuck ones
	// included.
	Pending int
}

// Checks says what CheckRoot checks beyond the invariants.
type Checks struct {
	// Notes is that every task and folder has its notes file, as koan
	// writes them when no hand has removed one.
	Notes bool
	// Pending is that steps may be pending: T1 to T6 hold only when no
	// change is in progress, so with steps pending they aren't checked.
	Pending bool
}

type folderRow struct {
	parent int64 // 0 is the root
	name   string
}

type taskRow struct {
	folder int64
	gate   string
}

// CheckRoot reads the root under home directly, its database and its
// directories, not through koan, and reports every invariant of the rules
// doc it finds broken: I1 to I12, then T1 to T6. prev, when not nil, is
// the snapshot of an earlier state of the same root, for what must hold
// across changes. It returns nil when all hold.
func CheckRoot(home string, prev *Snapshot, checks Checks) (*Snapshot, []string) {
	var bad []string
	fail := func(format string, args ...any) { bad = append(bad, fmt.Sprintf(format, args...)) }
	root := filepath.Join(home, "koan-root")
	d, err := sql.Open("sqlite", "file:"+filepath.Join(root, "koan.db")+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, []string{err.Error()}
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	tx, err := d.Begin()
	if err != nil {
		return nil, []string{err.Error()}
	}
	defer tx.Rollback()
	query := func(q string, fn func(rs *sql.Rows) error) {
		rs, err := tx.Query(q)
		if err != nil {
			fail("%s: %v", q, err)
			return
		}
		defer rs.Close()
		for rs.Next() {
			if err := fn(rs); err != nil {
				fail("%s: %v", q, err)
				return
			}
		}
		if err := rs.Err(); err != nil {
			fail("%s: %v", q, err)
		}
	}

	snap := &Snapshot{Issued: map[int64][2]string{}, Gates: map[int64]int64{}, Created: map[int64]string{}}
	archive := map[int64]string{}
	query("SELECT id, kind, state, coalesce(archive, '') FROM issued", func(rs *sql.Rows) error {
		var id int64
		var kind, state, at string
		if err := rs.Scan(&id, &kind, &state, &at); err != nil {
			return err
		}
		snap.Issued[id] = [2]string{kind, state}
		if state == "archived" {
			archive[id] = at
		}
		return nil
	})
	var firstID int64
	if err := tx.QueryRow("SELECT first_id FROM root").Scan(&firstID); err != nil {
		fail("root row: %v", err)
	}
	folders := map[int64]folderRow{}
	query("SELECT id, coalesce(parent, 0), name, title, extra FROM folder", func(rs *sql.Rows) error {
		var id int64
		var f folderRow
		var title sql.NullString
		var extra string
		if err := rs.Scan(&id, &f.parent, &f.name, &title, &extra); err != nil {
			return err
		}
		folders[id] = f
		if !core.ValidID(id) || !core.ValidName(f.name) {
			fail("I9: folder @%d named %q", id, f.name)
		}
		if title.Valid {
			if s, err := core.CheckTitle(title.String); err != nil || s != title.String {
				fail("I9: folder @%d title %q", id, title.String)
			}
		}
		if !object(extra) {
			fail("I9: folder @%d extra %s", id, extra)
		}
		return nil
	})
	tasks := map[int64]taskRow{}
	open := map[int64]bool{}
	query(`SELECT id, coalesce(folder, 0), coalesce(gate, ''), title, typeof(priority), coalesce(priority, 0),
		created_at, coalesce(completed_at, ''), updated_at, extra FROM task`, func(rs *sql.Rows) error {
		var id, priority int64
		var x taskRow
		var title, ptype, created, completed, updated, extra string
		if err := rs.Scan(&id, &x.folder, &x.gate, &title, &ptype, &priority, &created, &completed, &updated, &extra); err != nil {
			return err
		}
		tasks[id] = x
		open[id] = completed == ""
		snap.Created[id] = created
		if x.gate != "" {
			snap.Gates[id] = x.folder
		}
		if !core.ValidID(id) {
			fail("I9: task ID %d", id)
		}
		if s, err := core.CheckTitle(title); err != nil || s != title {
			fail("I9: @%d title %q", id, title)
		}
		if ptype != "null" && (ptype != "integer" || core.CheckInt(priority) != nil) {
			fail("I9: @%d priority %s %d", id, ptype, priority)
		}
		for _, s := range []string{created, completed, updated} {
			if _, err := core.ParseTimestamp(s); s != "" && err != nil {
				fail("I9: @%d timestamp %q", id, s)
			}
		}
		if !object(extra) {
			fail("I9: @%d extra %s", id, extra)
		}
		return nil
	})
	query("SELECT task, tag FROM tag", func(rs *sql.Rows) error {
		var id int64
		var tag string
		if err := rs.Scan(&id, &tag); err != nil {
			return err
		}
		if !core.ValidTag(tag) {
			fail("I9: @%d tag %q", id, tag)
		}
		if _, ok := tasks[id]; !ok {
			fail("I5: tag %q on @%d, which isn't a task", tag, id)
		}
		return nil
	})
	var edges []core.Edge
	query("SELECT task, blocker FROM edge ORDER BY task, blocker", func(rs *sql.Rows) error {
		var e core.Edge
		if err := rs.Scan(&e.From, &e.To); err != nil {
			return err
		}
		edges = append(edges, e)
		return nil
	})
	query("SELECT task, claimed_at, extra FROM claim", func(rs *sql.Rows) error {
		var id int64
		var at, extra string
		if err := rs.Scan(&id, &at, &extra); err != nil {
			return err
		}
		switch _, ok := tasks[id]; {
		case !ok:
			fail("I8: claim on @%d, which isn't a task", id)
		case !open[id]:
			fail("I8: claim on @%d, which is complete", id)
		}
		if _, err := core.ParseTimestamp(at); err != nil {
			fail("I9: claim on @%d claimed_at %q", id, at)
		}
		if !object(extra) {
			fail("I9: claim on @%d extra %s", id, extra)
		}
		return nil
	})
	query("SELECT count(*) FROM pending", func(rs *sql.Rows) error { return rs.Scan(&snap.Pending) })

	// I1: one ID, one live row, of the kind issued.
	for id, x := range snap.Issued {
		_, isTask := tasks[id]
		_, isFolder := folders[id]
		switch {
		case isTask && isFolder:
			fail("I1: @%d is both a task and a folder", id)
		case x[1] == "live" && !isTask && !isFolder:
			fail("I1: @%d is issued live with no row", id)
		case x[1] != "live" && (isTask || isFolder):
			fail("I1: @%d is %s with a row", id, x[1])
		case isTask && x[0] != "task", isFolder && x[0] != "folder":
			fail("I1: @%d has a row of another kind than its %s", id, x[0])
		}
	}
	for id := range tasks {
		if _, ok := snap.Issued[id]; !ok {
			fail("I1: task @%d was never issued", id)
		}
	}
	for id := range folders {
		if _, ok := snap.Issued[id]; !ok {
			fail("I1: folder @%d was never issued", id)
		}
	}

	// I2: IDs only ever grow, from the first, and keep their kind; a
	// deleted or archived ID never comes back.
	for id := range snap.Issued {
		if id < firstID {
			fail("I2: @%d is below the first ID, %d", id, firstID)
		}
	}
	if prev != nil {
		var top int64
		for id := range prev.Issued {
			top = max(top, id)
		}
		for id, was := range prev.Issued {
			now, ok := snap.Issued[id]
			switch {
			case !ok:
				fail("I2: @%d is no longer issued", id)
			case now[0] != was[0]:
				fail("I2: @%d was a %s and is a %s", id, was[0], now[0])
			case was[1] != "live" && now[1] != was[1]:
				fail("I2: @%d was %s and is %s", id, was[1], now[1])
			}
		}
		for id := range snap.Issued {
			if _, ok := prev.Issued[id]; !ok && id <= top {
				fail("I2: @%d was issued after @%d", id, top)
			}
		}
		for id, f := range prev.Gates {
			if g, ok := snap.Gates[id]; ok && g != f {
				fail("I11: gate @%d moved from @%d to @%d", id, f, g)
			}
		}
		for id, at := range prev.Created {
			if now, ok := snap.Created[id]; ok && now != at {
				fail("I9: @%d created_at changed from %s to %s", id, at, now)
			}
		}
	}

	// I3: every chain of parents reaches the root.
	for id, f := range folders {
		seen := map[int64]bool{id: true}
		for p := f.parent; p != 0; p = folders[p].parent {
			if _, ok := folders[p]; !ok {
				fail("I3: @%d's parents reach @%d, which isn't a folder", id, p)
				break
			}
			if seen[p] {
				fail("I3: @%d is inside itself", id)
				break
			}
			seen[p] = true
		}
	}
	for id, x := range tasks {
		if _, ok := folders[x.folder]; !ok && x.folder != 0 {
			fail("I3: @%d sits in @%d, which isn't a folder", id, x.folder)
		}
	}
	// I4.
	for a, fa := range folders {
		for b, fb := range folders {
			if a < b && fa.parent == fb.parent && strings.EqualFold(fa.name, fb.name) {
				fail("I4: siblings @%d %q and @%d %q", a, fa.name, b, fb.name)
			}
		}
	}
	// I5, I6, I7.
	for _, e := range edges {
		for _, id := range []int64{e.From, e.To} {
			if _, ok := tasks[id]; !ok {
				fail("I5: edge @%d → @%d names @%d, which isn't a task", e.From, e.To, id)
			}
		}
		if e.From == e.To {
			fail("I6: @%d waits on itself", e.From)
		}
	}
	if cycle, _ := core.FindCycle(func(int64) []int64 { return nil }, edges); cycle != nil {
		fail("I7: cycle %s", core.FormatIDs(cycle))
	}
	// I11.
	type end struct {
		folder int64
		gate   string
	}
	gates := map[end]int{}
	for id, x := range tasks {
		if x.gate == "" {
			continue
		}
		if x.gate != "start" && x.gate != "done" {
			fail("I9: @%d gate %q", id, x.gate)
		}
		if x.folder == 0 {
			fail("I11: gate @%d is in the root", id)
			continue
		}
		if gates[end{x.folder, x.gate}]++; gates[end{x.folder, x.gate}] == 2 {
			fail("I11: @%d has two %s gates", x.folder, x.gate)
		}
	}
	// I12: every archived ID says where it went, which is a file or
	// directory there for an item archived alone; one archived with its
	// folder sits where it was in the folder's directory.
	var ats []string
	for _, at := range archive {
		ats = append(ats, at)
	}
	for id, at := range archive {
		if !strings.HasPrefix(at, "archived/") || path.Clean(at) != at {
			fail("I12: @%d archived at %q", id, at)
			continue
		}
		alone := !slices.ContainsFunc(ats, func(o string) bool { return strings.HasPrefix(at, o+"/") })
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(at))); alone && err != nil && !checks.Pending {
			fail("I12: @%d archived alone at %s, which isn't there", id, at)
		}
	}

	if checks.Pending && snap.Pending > 0 {
		return snap, bad
	}
	if snap.Pending > 0 {
		fail("%d filesystem steps pending", snap.Pending)
	}
	bad = append(bad, checkTree(filepath.Join(root, "tree"), folders, tasks, checks)...)
	bad = append(bad, checkArchive(filepath.Join(root, "archived"))...)
	return snap, bad
}

func object(s string) bool {
	var m map[string]any
	return json.Unmarshal([]byte(s), &m) == nil && m != nil
}

// checkTree checks T1 to T6 against the rows.
func checkTree(tree string, folders map[int64]folderRow, tasks map[int64]taskRow, checks Checks) []string {
	var bad []string
	fail := func(format string, args ...any) { bad = append(bad, fmt.Sprintf(format, args...)) }
	dir := func(id int64) string {
		var parts []string
		for ; id != 0; id = folders[id].parent {
			parts = append([]string{"@" + folders[id].name}, parts...)
		}
		return filepath.Join(append([]string{tree}, parts...)...)
	}
	children := map[int64]map[string]int64{0: {}}
	for id, f := range folders {
		if children[f.parent] == nil {
			children[f.parent] = map[string]int64{}
		}
		children[f.parent][f.name] = id
	}
	notes := map[int64]map[string]int64{}
	for id, x := range tasks {
		name := "@" + strconv.FormatInt(id, 10) + ".md"
		if x.gate != "" {
			name = "@" + x.gate + ".md"
		}
		if notes[x.folder] == nil {
			notes[x.folder] = map[string]int64{}
		}
		notes[x.folder][name] = id
	}
	list := func(d string) map[string]os.DirEntry {
		es, err := os.ReadDir(d)
		if err != nil {
			return nil
		}
		out := map[string]os.DirEntry{}
		for _, e := range es {
			out[e.Name()] = e
		}
		return out
	}

	// T1: each folder's directory, listed in its parent's, in its case.
	for id, f := range folders {
		var parent map[string]os.DirEntry
		if f.parent == 0 {
			parent = list(tree)
		} else {
			parent = list(dir(f.parent))
		}
		if e, ok := parent["@"+f.name]; !ok || !e.IsDir() {
			fail("T1: folder @%d has no directory %s", id, dir(id))
		}
	}
	// T2 to T6, directory by directory.
	ids := []int64{0}
	for id := range folders {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		d := tree
		if id != 0 {
			d = dir(id)
		}
		entries := list(d)
		if entries == nil {
			continue
		}
		for name, e := range entries {
			p := filepath.Join(d, name)
			switch {
			case !strings.HasPrefix(name, "@"):
				// Material: never looked into.
			case e.IsDir():
				if _, ok := children[id][name[1:]]; !ok {
					fail("T2: %s is no folder", p)
				}
			case name == "@notes.md":
				if id == 0 {
					fail("T5: the root has notes, %s", p)
				}
			default:
				if _, ok := notes[id][name]; ok {
					break
				}
				switch {
				case name == "@start.md" || name == "@done.md":
					fail("T6: %s is no gate's notes", p)
				case taskNotes(name):
					fail("T4: %s belongs to no task in its directory", p)
				default:
					fail("T6: %s is a stray entry", p)
				}
			}
		}
		if checks.Notes {
			if _, ok := entries["@notes.md"]; id != 0 && !ok {
				fail("notes: folder @%d has no %s", id, filepath.Join(d, "@notes.md"))
			}
			for name, owner := range notes[id] {
				if _, ok := entries[name]; !ok {
					fail("notes: @%d has no %s", owner, filepath.Join(d, name))
				}
			}
		}
	}
	return bad
}

// taskNotes reports whether name is spelled as a task's notes, @<id>.md.
func taskNotes(name string) bool {
	id, ok := strings.CutSuffix(strings.TrimPrefix(name, "@"), ".md")
	_, valid := core.ParseID(id)
	return ok && valid
}

// checkArchive checks no temp file is left in the archive: only koan
// writes it, and a step done removes its temp file.
func checkArchive(archived string) []string {
	var bad []string
	filepath.WalkDir(archived, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			bad = append(bad, err.Error())
			return nil
		}
		if strings.HasPrefix(d.Name(), "@.tmp-") || strings.HasPrefix(d.Name(), "@.case-") {
			bad = append(bad, "archive: "+p+" left behind")
		}
		return nil
	})
	return bad
}
