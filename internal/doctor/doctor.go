// Package doctor compares the tree with the database and checks the
// database itself (doctor), puts the tree back to what the database says
// (repair), and copies a root whole (backup).
package doctor

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// The kinds of finding.
const (
	PendingStep      = "pending-step"
	StuckStep        = "stuck-step"
	StrayDirectory   = "stray-directory"
	MissingDirectory = "missing-directory"
	CaseDiffers      = "case-differs"
	MisplacedNotes   = "misplaced-notes"
	OrphanNotes      = "orphan-notes"
	StrayEntry       = "stray-entry"
	InvalidRow       = "invalid-row"
	Corrupt          = "corrupt"
)

// Kinds is every kind of finding, in the order doctor reports them and
// repair takes them.
var Kinds = []string{
	PendingStep, StuckStep, StrayDirectory, MissingDirectory, CaseDiffers,
	MisplacedNotes, OrphanNotes, StrayEntry, InvalidRow, Corrupt,
}

// RepairKinds is the kinds repair takes: those it fixes, and stuck-step,
// whose changes it retries.
var RepairKinds = Kinds[:7]

// The invariants invalid-row names, in the order findings of one row
// come.
var invariants = []string{"I3", "I4", "I6", "I7", "I9", "I11", "I12"}

// Finding is one thing doctor finds, in its schema's order.
type Finding struct {
	Kind       string   `json:"kind"`
	Detail     *string  `json:"detail"`
	Message    string   `json:"message"`
	IDs        []int64  `json:"ids"`
	Paths      []string `json:"paths"`
	Repairable bool     `json:"repairable"`

	// step is the step that fixes a repairable finding of the tree.
	step *fsstep.Step
	// depth is how deep the folder a step puts right sits, so repair
	// works top-down.
	depth int
}

func newFinding(kind, detail, message string, ids []int64, paths []string) Finding {
	f := Finding{Kind: kind, Message: message, IDs: ids, Paths: paths}
	if detail != "" {
		f.Detail = &detail
	}
	if f.IDs == nil {
		f.IDs = []int64{}
	}
	if f.Paths == nil {
		f.Paths = []string{}
	}
	return f
}

// detail is a finding's detail, or "".
func (f Finding) detail() string {
	if f.Detail == nil {
		return ""
	}
	return *f.Detail
}

// same reports whether two findings are the same, their paths compared
// relative to the roots they were found in.
func same(a Finding, aRoot string, b Finding, bRoot string) bool {
	if a.Kind != b.Kind || a.detail() != b.detail() || !slices.Equal(a.IDs, b.IDs) || len(a.Paths) != len(b.Paths) {
		return false
	}
	for i := range a.Paths {
		if strings.TrimPrefix(a.Paths[i], aRoot) != strings.TrimPrefix(b.Paths[i], bRoot) {
			return false
		}
	}
	return true
}

// among reports whether a finding is among others found in the same root.
func among(f Finding, others []Finding) bool {
	return slices.ContainsFunc(others, func(o Finding) bool { return same(f, "", o, "") })
}

// compare orders findings: by kind, then by path, then by IDs, and an
// invalid row's by the invariant it breaks.
func compare(a, b Finding) int {
	if c := cmp.Compare(slices.Index(Kinds, a.Kind), slices.Index(Kinds, b.Kind)); c != 0 {
		return c
	}
	if c := slices.Compare(a.Paths, b.Paths); c != 0 {
		return c
	}
	if c := slices.Compare(a.IDs, b.IDs); c != 0 {
		return c
	}
	return cmp.Compare(slices.Index(invariants, a.detail()), slices.Index(invariants, b.detail()))
}

// Check reads one snapshot of a root and reports every finding, in order.
// It takes no lock.
func Check(s *store.Store) ([]Finding, *cli.Error) {
	var out []Finding
	e := s.Read(func(tx *store.Tx) error {
		c := &checker{tx: tx, root: s.Root}
		err := c.check()
		out = c.found
		return err
	})
	if e != nil {
		return nil, e
	}
	slices.SortStableFunc(out, compare)
	return out, nil
}

// checker gathers the findings of one snapshot.
type checker struct {
	tx    *store.Tx
	root  store.Root
	found []Finding
	*rows
}

func (c *checker) add(f Finding) { c.found = append(c.found, f) }

func (c *checker) abs(rel string) string { return fsstep.Abs(c.root, rel) }

func (c *checker) check() error {
	corrupt, err := c.corrupt()
	if err != nil {
		return err
	}
	if err := c.steps(); err != nil {
		return err
	}
	// Rows that fail either check can't be trusted.
	if corrupt {
		return nil
	}
	if c.rows, err = load(c.tx); err != nil {
		return err
	}
	c.invalid()
	return c.tree()
}

// corrupt runs SQLite's own checks of the whole database.
func (c *checker) corrupt() (bool, error) {
	var problems []string
	rows, err := c.tx.Query("PRAGMA integrity_check")
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return false, err
		}
		// SQLite heads each database's problems with its name.
		for _, line := range strings.Split(p, "\n") {
			if line != "ok" && !strings.HasPrefix(line, "*** ") {
				problems = append(problems, line)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(problems) > 0 {
		c.add(newFinding(Corrupt, "integrity", "The database fails PRAGMA integrity_check: "+first(problems)+". Restore from a backup.", nil, []string{c.root.Database}))
	}
	rows, err = c.tx.Query("PRAGMA foreign_key_check")
	if err != nil {
		return false, err
	}
	var broken []string
	for rows.Next() {
		var table, parent string
		var rowid, fkid any
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			rows.Close()
			return false, err
		}
		row := "a row of " + table
		if rowid != nil {
			row += fmt.Sprintf(" (rowid %v)", rowid)
		}
		broken = append(broken, row+" names a missing row of "+parent)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(broken) > 0 {
		c.add(newFinding(Corrupt, "foreign-key", "The database fails PRAGMA foreign_key_check: "+first(broken)+". Restore from a backup.", nil, []string{c.root.Database}))
	}
	return len(problems) > 0 || len(broken) > 0, nil
}

// first is the first few problems SQLite reported, and how many more.
func first(problems []string) string {
	const shown = 3
	if len(problems) <= shown {
		return strings.Join(problems, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(problems[:shown], "; "), len(problems)-shown)
}

// steps finds the pending steps, one finding for each change, and the
// stuck steps, one for each step that found what it stuck on.
func (c *checker) steps() error {
	live, stuck, err := c.tx.Pending()
	if err != nil {
		return err
	}
	var changes []int64
	paths := map[int64][]string{}
	count := map[int64]int{}
	for _, r := range live {
		s, err := fsstep.Parse(r.Step)
		if err != nil {
			continue
		}
		if !slices.Contains(changes, r.Change) {
			changes = append(changes, r.Change)
		}
		count[r.Change]++
		for _, p := range s.Paths() {
			if p = c.abs(p); !slices.Contains(paths[r.Change], p) {
				paths[r.Change] = append(paths[r.Change], p)
			}
		}
	}
	for _, ch := range changes {
		message := fmt.Sprintf("A change's filesystem steps were cut short: %d steps are pending. koan repair, or the next write, carries them out.", count[ch])
		if count[ch] == 1 {
			message = "A change's filesystem steps were cut short: 1 step is pending. koan repair, or the next write, carries it out."
		}
		f := newFinding(PendingStep, "", message, nil, paths[ch])
		f.Repairable = true
		c.add(f)
	}
	for _, r := range stuck {
		s, err := fsstep.Parse(r.Step)
		if err != nil || s.Stuck == "" {
			continue
		}
		var paths []string
		for _, p := range []string{s.From, s.To} {
			if p != "" {
				paths = append(paths, c.abs(p))
			}
		}
		var message string
		switch s.Stuck {
		case fsstep.Both:
			message = "A step found both its source and its destination holding content, and is set aside with the rest of its change. Keep one copy, then run koan repair."
		case fsstep.Neither:
			message = "A step found neither its source nor its destination, and is set aside with the rest of its change. Put the directory back, then run koan repair."
		default:
			message = "A remove found material in a directory it was to remove, and is set aside with the rest of its change. Move the material out, then run koan repair."
		}
		c.add(newFinding(StuckStep, s.Stuck, message, nil, paths))
	}
	return nil
}
