package main_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/store"
	"github.com/phansen314/koan/internal/testrun"
)

// base is a fresh root with a little of everything: /work (1) with a
// start gate (6) and its notes, /work/api (2), a task in each (3, 4), and
// one in the root (5) that waits on the gate.
func base(t *testing.T) string {
	t.Helper()
	home := root(t)
	ok(t, home, "folder", "create", "/work/api")
	ok(t, home, "task", "create", "In work", "--folder", "/work", "--notes", "work notes")
	ok(t, home, "task", "create", "In api", "--folder", "/work/api")
	ok(t, home, "task", "create", "Top", "--blocked-by", "/work.start")
	if r := ok(t, home, "doctor"); len(r.Result()["findings"].([]any)) != 0 {
		t.Fatalf("base: %s", r.Line())
	}
	return home
}

// treeDir is a root's tree directory.
func treeDir(home string) string { return filepath.Join(home, "koan-root", "tree") }

// hand changes the tree as a person might, failing the test when the
// operating system refuses.
func hand(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// write writes a file in the tree by hand.
func write(t *testing.T, home, rel, content string) {
	t.Helper()
	hand(t, os.WriteFile(filepath.Join(treeDir(home), filepath.FromSlash(rel)), []byte(content), 0o644))
}

// rename renames an entry in the tree by hand. A case-only rename goes
// through another name, as on a filesystem that ignores case.
func rename(t *testing.T, home, from, to string) {
	t.Helper()
	f, d := filepath.Join(treeDir(home), filepath.FromSlash(from)), filepath.Join(treeDir(home), filepath.FromSlash(to))
	if strings.EqualFold(from, to) {
		via := f + ".via"
		hand(t, os.Rename(f, via))
		f = via
	}
	hand(t, os.Rename(f, d))
}

// edit edits a root's database by hand, as a person with the sqlite3 tool
// might, with SQLite's own checks off.
func edit(t *testing.T, home string, stmts ...string) {
	t.Helper()
	d, err := sql.Open("sqlite", filepath.Join(home, "koan-root", "koan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	for _, s := range append([]string{"PRAGMA ignore_check_constraints = ON", "PRAGMA foreign_keys = OFF"}, stmts...) {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// summary is each finding in a list as kind, detail, IDs and whether it
// is repairable.
func summary(list any) []string {
	out := []string{}
	for _, f := range list.([]any) {
		f := f.(map[string]any)
		out = append(out, fmt.Sprintf("%v %v %v %v", f["kind"], f["detail"], f["ids"], f["repairable"]))
	}
	return out
}

func sameList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// repaired runs repair, which must succeed, with the warnings named.
func repaired(t *testing.T, home string, warnings []string, args ...string) *testrun.Result {
	t.Helper()
	r := koan(t, home, append([]string{"repair"}, args...)...)
	if r.Exit != 0 || !r.OK() {
		t.Fatalf("repair: exit %d: %s", r.Exit, r.Line())
	}
	var got []string
	for _, w := range r.Envelope["warnings"].([]any) {
		got = append(got, w.(map[string]any)["kind"].(string))
	}
	sameList(t, "repair's warnings", got, warnings)
	return r
}

// The PRD's Repair test: each finding made by hand on a fresh root.
// doctor reports it under the right kind, repair does what the PRD's
// table says, and a second doctor reports only what repair leaves to a
// person.
func TestRepair(t *testing.T) {
	for _, c := range []struct {
		name string
		make func(t *testing.T, home string)
		// found, fixed and left are summaries: what doctor finds, what
		// repair fixes, and what it leaves, which a second doctor finds.
		found, fixed, left []string
		// tree, when given, is entries the tree must then hold.
		tree []string
	}{{
		// A change cut short after COMMIT: the folder's row is there, its
		// directory isn't, and the step that makes it is pending.
		name: "pending-step",
		make: func(t *testing.T, home string) {
			db(t, home,
				"INSERT INTO issued (id, kind, state) VALUES (7, 'folder', 'live')",
				"INSERT INTO folder (id, parent, name, extra) VALUES (7, NULL, 'home', '{}')",
				`INSERT INTO pending (seq, change, step) VALUES (1, 1, '{"op":"mkdir","to":"tree/@home","content":"# home\n"}')`)
		},
		found: []string{"pending-step <nil> [] true", "missing-directory <nil> [7] true"},
		fixed: []string{"pending-step <nil> [] true", "missing-directory <nil> [7] true"},
		tree:  []string{"@home/", "@home/@notes.md=# home\n"},
	}, {
		// A folder renamed in Zed: one stray, one folder missing, sharing
		// a parent. Its subfolder comes back with it.
		name:  "stray-directory",
		make:  func(t *testing.T, home string) { rename(t, home, "@work", "@job") },
		found: []string{"stray-directory <nil> [1] true", "missing-directory <nil> [1] true", "missing-directory <nil> [2] true"},
		fixed: []string{"stray-directory <nil> [1] true", "missing-directory <nil> [1] true", "missing-directory <nil> [2] true"},
		tree:  []string{"@work/@notes.md=", "@work/@start.md=", "@work/@3.md=work notes", "@work/@api/@4.md="},
	}, {
		// A folder dragged into another: every @<id>.md in it belongs to
		// the folder whose directory is missing.
		name: "stray-directory-dragged",
		make: func(t *testing.T, home string) {
			ok(t, home, "folder", "create", "/home") // 7
			rename(t, home, "@work/@api", "@home/@moved")
		},
		found: []string{"stray-directory <nil> [2] true", "missing-directory <nil> [2] true"},
		fixed: []string{"stray-directory <nil> [2] true", "missing-directory <nil> [2] true"},
		tree:  []string{"@work/@api/@4.md=", "@home/@notes.md="},
	}, {
		// Two strays share a parent with the one folder missing: not one
		// to one, so neither moves, and the directory is made empty.
		name: "stray-directory-ambiguous",
		make: func(t *testing.T, home string) {
			write(t, home, "@work/@api/@4.md", "api notes")
			rename(t, home, "@work/@api", "@work/@x")
			hand(t, os.Mkdir(filepath.Join(treeDir(home), "@work", "@y"), 0o755))
		},
		found: []string{"stray-directory <nil> [] false", "stray-directory <nil> [] false", "missing-directory <nil> [2] true"},
		fixed: []string{"missing-directory <nil> [2] true"},
		left:  []string{"stray-directory <nil> [] false", "stray-directory <nil> [] false"},
		tree:  []string{"@work/@api/@notes.md=", "@work/@x/@4.md=api notes", "@work/@y/"},
	}, {
		// A stray named as the folder missing, but in another folder and
		// holding no notes of its tasks: a name alone never decides.
		name: "stray-directory-name-alone",
		make: func(t *testing.T, home string) {
			ok(t, home, "folder", "create", "/home") // 7
			hand(t, os.Remove(filepath.Join(treeDir(home), "@work", "@api", "@4.md")))
			rename(t, home, "@work/@api", "@home/@api")
		},
		found: []string{"stray-directory <nil> [] false", "missing-directory <nil> [2] true"},
		fixed: []string{"missing-directory <nil> [2] true"},
		left:  []string{"stray-directory <nil> [] false"},
		tree:  []string{"@work/@api/@notes.md=", "@home/@api/@notes.md="},
	}, {
		// A folder's directory removed: an empty one is made, and its
		// notes are gone.
		name: "missing-directory",
		make: func(t *testing.T, home string) {
			hand(t, os.RemoveAll(filepath.Join(treeDir(home), "@work")))
		},
		found: []string{"missing-directory <nil> [1] true", "missing-directory <nil> [2] true"},
		fixed: []string{"missing-directory <nil> [1] true", "missing-directory <nil> [2] true"},
		tree:  []string{"@work/@notes.md=", "@work/@api/@notes.md="},
	}, {
		// Directories respelled by hand, one inside the other: renamed
		// back top-down.
		name: "case-differs",
		make: func(t *testing.T, home string) {
			rename(t, home, "@work/@api", "@work/@API")
			rename(t, home, "@work", "@WORK")
		},
		found: []string{"case-differs <nil> [1] true", "case-differs <nil> [2] true"},
		fixed: []string{"case-differs <nil> [1] true", "case-differs <nil> [2] true"},
		tree:  []string{"@work/@3.md=work notes", "@work/@api/@4.md="},
	}, {
		// Notes moved to another folder's directory are moved back; notes
		// copied, so that some are already there, are left.
		name: "misplaced-notes",
		make: func(t *testing.T, home string) {
			write(t, home, "@work/@api/@4.md", "api notes")
			rename(t, home, "@work/@api/@4.md", "@4.md")
			write(t, home, "@work/@api/@3.md", "a copy")
		},
		found: []string{"misplaced-notes <nil> [4] true", "misplaced-notes <nil> [3] false"},
		fixed: []string{"misplaced-notes <nil> [4] true"},
		left:  []string{"misplaced-notes <nil> [3] false"},
		tree:  []string{"@work/@api/@4.md=api notes", "@work/@api/@3.md=a copy", "@work/@3.md=work notes"},
	}, {
		// Notes of nothing: removed when empty, else left.
		name: "orphan-notes",
		make: func(t *testing.T, home string) {
			ok(t, home, "delete", "3")
			write(t, home, "@work/@3.md", "kept")
			ok(t, home, "task", "create", "Gone") // 7
			ok(t, home, "delete", "7")
			write(t, home, "@7.md", "")
			ok(t, home, "complete", "4")
			ok(t, home, "archive", "4")
			write(t, home, "@work/@api/@4.md", "")
			write(t, home, "@99.md", "never")
			write(t, home, "@work/@api/@done.md", "")
			write(t, home, "@start.md", "")
		},
		// By path, byte for byte: @7.md, @99.md, @start.md, then in
		// @work: @3.md, then @api/@4.md and @api/@done.md.
		found: []string{
			"orphan-notes deleted [7] true", "orphan-notes never-issued [99] false", "orphan-notes no-gate [] true",
			"orphan-notes deleted [3] false", "orphan-notes archived [4] true", "orphan-notes no-gate [] true",
		},
		fixed: []string{"orphan-notes deleted [7] true", "orphan-notes no-gate [] true", "orphan-notes archived [4] true", "orphan-notes no-gate [] true"},
		left:  []string{"orphan-notes never-issued [99] false", "orphan-notes deleted [3] false"},
		tree:  []string{"@99.md=never", "@work/@3.md=kept"},
	}, {
		// Any other @ entry is only reported.
		name: "stray-entry",
		make: func(t *testing.T, home string) {
			write(t, home, "@Notes.md", "by hand")
			write(t, home, "@work/@1.md", "")
			write(t, home, "@work/@.tmp-9", "")
			write(t, home, "@work/@api/@todo", "")
		},
		found: []string{"stray-entry <nil> [] false", "stray-entry <nil> [] false", "stray-entry <nil> [] false", "stray-entry <nil> [] false"},
		left:  []string{"stray-entry <nil> [] false", "stray-entry <nil> [] false", "stray-entry <nil> [] false", "stray-entry <nil> [] false"},
		tree:  []string{"@Notes.md=by hand", "@work/@1.md=", "@work/@.tmp-9=", "@work/@api/@todo="},
	}, {
		// Rows that break an invariant SQL can't check, after hand edits
		// of the database: a person fixes them.
		name: "invalid-row",
		make: func(t *testing.T, home string) {
			ok(t, home, "folder", "create", "/a/b") // 7, 8
			hand(t, os.RemoveAll(filepath.Join(treeDir(home), "@a")))
			// SQLite's integrity check checks CHECK constraints too, so a
			// person breaking those would have to cut them out of the
			// schema.
			edit(t, home,
				"PRAGMA writable_schema = ON",
				"UPDATE sqlite_schema SET sql = replace(sql, 'CHECK (task <> blocker)', 'CHECK (1)') WHERE name = 'edge'",
				"UPDATE sqlite_schema SET sql = replace(sql, 'CHECK (gate IS NULL OR folder IS NOT NULL)', 'CHECK (1)') WHERE name = 'task'",
				"UPDATE sqlite_schema SET sql = replace(sql, 'CHECK ((state = ''archived'') = (archive IS NOT NULL))', 'CHECK (1)') WHERE name = 'issued'")
			edit(t, home,
				"DROP INDEX folder_sibling",
				"DROP INDEX task_gate",
				// I3: /a and /a/b each other's parents.
				"UPDATE folder SET parent = 8 WHERE id = 7",
				// I4: under them, so no directory is looked for.
				"INSERT INTO issued (id, kind, state) VALUES (9, 'folder', 'live'), (10, 'folder', 'live')",
				"INSERT INTO folder (id, parent, name, extra) VALUES (9, 7, 'x', '{}'), (10, 7, 'X', '{}')",
				// I6 and I7.
				"INSERT INTO edge (task, blocker) VALUES (3, 3), (3, 4), (4, 3)",
				// I9.
				"UPDATE task SET title = ' padded', priority = 1.5 WHERE id = 4",
				"UPDATE task SET created_at = 'yesterday' WHERE id = 5",
				"INSERT INTO tag (task, tag) VALUES (5, 'BAD')",
				"UPDATE folder SET name = 'b b' WHERE id = 8",
				// I11: a second start gate in /work, and a gate in the root.
				"INSERT INTO issued (id, kind, state) VALUES (11, 'task', 'live'), (12, 'task', 'live')",
				`INSERT INTO task (id, folder, gate, title, created_at, updated_at, extra)
					VALUES (11, 1, 'start', 'Start', '2026-10-20T10:00:00Z', '2026-10-20T10:00:00Z', '{}'),
					(12, NULL, 'done', 'Done', '2026-10-20T10:00:00Z', '2026-10-20T10:00:00Z', '{}')`,
				// I12: a live task the index says was archived, and an
				// archived ID it doesn't say where.
				"UPDATE issued SET state = 'archived', archive = 'archived/@5.md' WHERE id = 5",
				"INSERT INTO issued (id, kind, state) VALUES (13, 'task', 'archived')")
		},
		found: []string{
			"invalid-row I6 [3] false", "invalid-row I7 [3 4] false", "invalid-row I9 [4] false", "invalid-row I9 [5] false",
			"invalid-row I12 [5] false", "invalid-row I11 [6 11] false", "invalid-row I3 [7 8] false", "invalid-row I9 [8] false",
			"invalid-row I4 [9 10] false", "invalid-row I11 [12] false", "invalid-row I12 [13] false",
		},
		left: []string{
			"invalid-row I6 [3] false", "invalid-row I7 [3 4] false", "invalid-row I9 [4] false", "invalid-row I9 [5] false",
			"invalid-row I12 [5] false", "invalid-row I11 [6 11] false", "invalid-row I3 [7 8] false", "invalid-row I9 [8] false",
			"invalid-row I4 [9 10] false", "invalid-row I11 [12] false", "invalid-row I12 [13] false",
		},
	}, {
		// An edge naming a task that isn't there: the database fails its
		// foreign-key check, and doctor stops there.
		name: "corrupt-foreign-key",
		make: func(t *testing.T, home string) {
			edit(t, home, "INSERT INTO edge (task, blocker) VALUES (3, 999)")
			write(t, home, "@99.md", "")
		},
		found: []string{"corrupt foreign-key [] false"},
		left:  []string{"corrupt foreign-key [] false"},
		tree:  []string{"@99.md="},
	}, {
		// An index cut out of the schema by hand leaves its pages unused:
		// the database fails its integrity check.
		name: "corrupt-integrity",
		make: func(t *testing.T, home string) {
			edit(t, home, "PRAGMA writable_schema = ON", "DELETE FROM sqlite_schema WHERE name = 'task_folder'")
		},
		found: []string{"corrupt integrity [] false"},
		left:  []string{"corrupt integrity [] false"},
	}} {
		t.Run(c.name, func(t *testing.T) {
			home := base(t)
			c.make(t, home)
			r := ok(t, home, "doctor")
			golden(t, r, "doctor-"+c.name)
			sameList(t, "doctor", summary(r.Result()["findings"]), c.found)
			r = repaired(t, home, nil)
			golden(t, r, "repair-"+c.name)
			sameList(t, "fixed", summary(r.Result()["fixed"]), c.fixed)
			sameList(t, "left", summary(r.Result()["left"]), c.left)
			r = ok(t, home, "doctor")
			sameList(t, "doctor after repair", summary(r.Result()["findings"]), c.left)
			got := tree(t, home)
			for _, want := range c.tree {
				if !slices.Contains(got, want) {
					t.Errorf("tree has no %q: %q", want, got)
				}
			}
		})
	}
}

// A stuck step waits for a person: repair leaves it, and once the person
// has kept one copy, retries every stuck change in seq order, clearing
// each that now succeeds and leaving the rest stuck.
func TestRepairStuckStep(t *testing.T) {
	home := base(t)
	// Two moves of the root task's notes, each finding both sides holding
	// content; the second change's step waits behind the first's.
	write(t, home, "@5.md", "root copy")
	write(t, home, "@work/@5.md", "work copy")
	write(t, home, "@work/@api/@5.md", "api copy")
	db(t, home,
		`INSERT INTO pending (seq, change, step, stuck) VALUES (1, 1, '{"op":"move","from":"tree/@work/@5.md","to":"tree/@5.md","stuck":"both"}', 1)`,
		`INSERT INTO pending (seq, change, step, stuck) VALUES (2, 2, '{"op":"move","from":"tree/@work/@api/@5.md","to":"tree/@5.md","stuck":"both"}', 1)`)
	r := ok(t, home, "doctor")
	golden(t, r, "doctor-stuck-step")
	found := []string{"stuck-step both [] false", "stuck-step both [] false", "misplaced-notes <nil> [5] false", "misplaced-notes <nil> [5] false"}
	sameList(t, "doctor", summary(r.Result()["findings"]), found)

	// Nobody has acted: both stick again.
	r = repaired(t, home, []string{"stuck-step"})
	golden(t, r, "repair-stuck-step")
	sameList(t, "fixed", summary(r.Result()["fixed"]), nil)
	sameList(t, "left", summary(r.Result()["left"]), found)

	// The person keeps the root's copy over the first; the second still
	// holds content. The first change succeeds, the second sticks again.
	hand(t, os.Remove(filepath.Join(treeDir(home), "@work", "@5.md")))
	r = repaired(t, home, []string{"stuck-step"})
	sameList(t, "fixed", summary(r.Result()["fixed"]), []string{"stuck-step both [] false"})
	sameList(t, "left", summary(r.Result()["left"]), []string{"stuck-step both [] false", "misplaced-notes <nil> [5] false"})

	// repair given other kinds retries nothing.
	hand(t, os.Remove(filepath.Join(treeDir(home), "@5.md")))
	r = repaired(t, home, []string{"stuck-step"}, "--kinds", "orphan-notes")
	sameList(t, "fixed", summary(r.Result()["fixed"]), nil)
	r = repaired(t, home, nil, "--kinds", "stuck-step")
	sameList(t, "fixed", summary(r.Result()["fixed"]), []string{"stuck-step both [] false", "misplaced-notes <nil> [5] true"})
	sameList(t, "left", summary(r.Result()["left"]), nil)
	checkTreeHas(t, home, "@5.md=api copy")
	ok(t, home, "doctor")
}

func checkTreeHas(t *testing.T, home string, want ...string) {
	t.Helper()
	got := tree(t, home)
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("tree has no %q: %q", w, got)
		}
	}
}

// repair takes only the kinds given, in the table's order: a folder
// renamed by hand is moved back before an empty directory could be made
// in its place. Steps left pending are finished first, as by every
// write. Kinds it doesn't take are invalid-input.
func TestRepairKinds(t *testing.T) {
	home := base(t)
	rename(t, home, "@work/@api", "@work/@x")
	write(t, home, "@99.md", "")
	r := repaired(t, home, nil, "--kinds", "orphan-notes")
	sameList(t, "fixed", summary(r.Result()["fixed"]), []string{"orphan-notes never-issued [99] true"})
	sameList(t, "left", summary(r.Result()["left"]), []string{"stray-directory <nil> [2] true", "missing-directory <nil> [2] true"})

	r = repaired(t, home, nil, "--kinds", "missing-directory,stray-directory")
	sameList(t, "fixed", summary(r.Result()["fixed"]), []string{"stray-directory <nil> [2] true", "missing-directory <nil> [2] true"})
	sameList(t, "left", summary(r.Result()["left"]), nil)
	checkTreeHas(t, home, "@work/@api/@4.md=")

	r = fails(t, home, "invalid-input", "", "repair", "--kinds", "stray-entry,orphan-notes,orphan-notes")
	golden(t, r, "repair-invalid")
	fails(t, home, "invalid-input", "", "repair", "--kinds", "")
	fails(t, t.TempDir(), "not-initialized", "", "repair")
	fails(t, t.TempDir(), "not-initialized", "", "doctor")
}

// doctor reads one snapshot and takes no lock, so a write holding the
// lock never keeps it waiting.
func TestDoctorTakesNoLock(t *testing.T) {
	home := base(t)
	lock, e := store.TakeLock(filepath.Join(home, "koan-root"), filepath.Join(home, "koan-root"))
	if e != nil {
		t.Fatal(e.Message)
	}
	defer lock.Release()
	start := time.Now()
	ok(t, home, "doctor")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("doctor waited %v", d)
	}
}

// A clean root after every kind of change: doctor finds nothing, and
// repair fixes and leaves nothing.
func TestDoctorClean(t *testing.T) {
	home := base(t)
	ok(t, home, "folder", "create", "/home/x/y")
	ok(t, home, "mv", "-T", "/home/x", "/work/X")
	ok(t, home, "mv", "-T", "/work/X", "/work/x")
	ok(t, home, "mv", "4", "/work/x/y")
	ok(t, home, "complete", "3")
	ok(t, home, "archive", "3")
	ok(t, home, "delete", "/home")
	hand(t, os.Mkdir(filepath.Join(treeDir(home), "@work", "docs"), 0o755))
	write(t, home, "@work/docs/@types", "material")
	write(t, home, "@work/.DS_Store", "")
	r := ok(t, home, "doctor")
	golden(t, r, "doctor-clean")
	r = repaired(t, home, nil)
	golden(t, r, "repair-clean")
}

// A directory doctor can't list is the operating system refusing: io,
// with its path.
func TestDoctorRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := base(t)
	dir := filepath.Join(treeDir(home), "@work", "@api")
	hand(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	r := fails(t, home, "io", "", "doctor")
	if paths := r.Error()["paths"].([]any); len(paths) != 1 || paths[0] != dir {
		t.Errorf("paths: %s", r.Line())
	}
}
