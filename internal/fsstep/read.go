package fsstep

import (
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/store"
)

// Path is a path a read returns, absolute under the root as written, and
// the task or folder it belongs to; the root's ID is 0.
type Path struct {
	ID   int64
	Path string
}

// Read runs a read's fn in one read transaction, as store.Read does, and
// returns the warnings pending filesystem steps give its result; fn
// returns the paths the result gives. Reads never repair: when steps are
// pending, Read tries the lock shared, without waiting, so readers never
// keep each other out. If something holds it exclusively, nearly always a
// write, the steps are being carried out, and each path returned that
// they bring about is pending-filesystem: the path given is where the
// file is going. If not, Read checks the rows again, since a write may
// have finished in between, and releases the lock at once; steps still
// pending have no write carrying them out, and give pending-step instead.
// Stuck steps give stuck-step.
func Read(s *store.Store, fn func(tx *store.Tx) ([]Path, error)) ([]cli.Warning, *cli.Error) {
	var paths []Path
	var live, stuck []store.Pending
	e := s.Read(func(tx *store.Tx) error {
		// The rows first, so the snapshot is the one they were read in.
		var err error
		if live, stuck, err = tx.Pending(); err != nil {
			return err
		}
		paths, err = fn(tx)
		return err
	})
	if e != nil {
		return nil, e
	}
	c := New(s.Root)
	if len(live) == 0 {
		return c.Warnings(nil, stuck), nil
	}
	lock, ok, e := store.TryShared(s.Root.Dir, s.Root.Path)
	if e != nil {
		return nil, e
	}
	if !ok {
		var out []cli.Warning
		if w, ok := c.pendingFilesystem(live, paths); ok {
			out = append(out, w)
		}
		return append(out, c.Warnings(nil, stuck)...), nil
	}
	e = s.Read(func(tx *store.Tx) error {
		var err error
		live, stuck, err = tx.Pending()
		return err
	})
	lock.Release()
	if e != nil {
		return nil, e
	}
	return c.Warnings(live, stuck), nil
}

// pendingFilesystem is the warning for the paths returned that steps
// being carried out bring about: a path a step makes, or one under it.
func (c *Steps) pendingFilesystem(live []store.Pending, paths []Path) (cli.Warning, bool) {
	var targets []string
	for _, r := range live {
		if s, err := Parse(r.Step); err == nil {
			for _, t := range s.targets() {
				targets = append(targets, c.abs(t))
			}
		}
	}
	w := cli.Warning{
		Kind:    "pending-filesystem",
		Message: "filesystem steps are still being carried out for these paths; each path given is where the file is going",
	}
	for _, p := range paths {
		if slices.Contains(w.Paths, p.Path) {
			continue
		}
		if !slices.ContainsFunc(targets, func(t string) bool { return p.Path == t || strings.HasPrefix(p.Path, t+"/") }) {
			continue
		}
		w.Paths = append(w.Paths, p.Path)
		if p.ID > 0 && !slices.Contains(w.IDs, p.ID) {
			w.IDs = append(w.IDs, p.ID)
		}
	}
	return w, len(w.Paths) > 0
}

// Makes reports whether a pending step, stuck or not, is still to make a
// path: one whose target is the path or a directory above it. A read
// gives no missing-notes for such notes, since the step's own warning
// says why the file isn't there.
func Makes(tx *store.Tx, rel string) (bool, error) {
	live, stuck, err := tx.Pending()
	if err != nil {
		return false, err
	}
	for _, r := range append(live, stuck...) {
		s, err := Parse(r.Step)
		if err != nil {
			continue
		}
		for _, t := range s.targets() {
			if rel == t || strings.HasPrefix(rel, t+"/") {
				return true, nil
			}
		}
	}
	return false, nil
}
