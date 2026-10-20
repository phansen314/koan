package fsstep

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/store"
)

// Steps carries out a root's filesystem steps. It is the store's Steps.
type Steps struct{ root store.Root }

var _ store.Steps = (*Steps)(nil)

// New is the steps of a root.
func New(root store.Root) *Steps { return &Steps{root: root} }

// Open opens the database of the root under home, as store.Open does,
// with its filesystem steps carried out here.
func Open(home string) (*store.Store, *cli.Error) {
	s, e := store.Open(home)
	if e != nil {
		return nil, e
	}
	s.Steps = New(s.Root)
	return s, nil
}

// Carry carries out the steps, oldest first, then flushes each directory
// they touched. A step that fails or sticks holds back the rest of its
// change, since those may depend on it; later changes go on.
func (c *Steps) Carry(rows []store.Pending) store.Outcome {
	var out store.Outcome
	held := map[int64]bool{}
	type carried struct {
		seq  int64
		dirs []string
	}
	var done []carried
	for _, r := range rows {
		if held[r.Change] {
			continue
		}
		s, err := Parse(r.Step)
		if err != nil {
			held[r.Change] = true
			continue
		}
		w := &work{root: c.root.Dir, seq: r.Seq}
		found, err := w.carry(s)
		switch {
		case err != nil:
			held[r.Change] = true
		case found != "":
			held[r.Change] = true
			s.Stuck = found
			b, _ := json.Marshal(s)
			out.Stuck = append(out.Stuck, store.Pending{Seq: r.Seq, Change: r.Change, Step: b})
		default:
			done = append(done, carried{r.Seq, w.dirs})
		}
	}
	flushed := map[string]error{}
	for _, d := range done {
		ok := true
		for _, dir := range d.dirs {
			err, seen := flushed[dir]
			if !seen {
				err = inject("flush " + dir)
				if err == nil {
					err = flush(filepath.Join(c.root.Dir, filepath.FromSlash(dir)))
				}
				flushed[dir] = err
			}
			ok = ok && err == nil
		}
		if ok {
			out.Done = append(out.Done, d.seq)
		}
	}
	return out
}

// Warnings is what the rows left say: pending-step for the steps still
// pending, with the paths they touch, and stuck-step for those that stuck,
// with each one's source, then its destination.
func (c *Steps) Warnings(live, stuck []store.Pending) []cli.Warning {
	var out []cli.Warning
	if w, ok := c.pendingStep(live); ok {
		out = append(out, w)
	}
	var paths []string
	for _, r := range stuck {
		s, err := Parse(r.Step)
		if err != nil || s.Stuck == "" {
			continue
		}
		for _, p := range []string{s.From, s.To} {
			if p != "" {
				paths = append(paths, c.abs(p))
			}
		}
	}
	if len(paths) > 0 {
		out = append(out, cli.Warning{
			Kind:    "stuck-step",
			Message: "A filesystem step found both its source and its destination holding content, or neither, and is set aside with the rest of its change. Keep one copy, then run koan repair.",
			Paths:   paths,
		})
	}
	return out
}

// pendingStep is the warning for steps pending with no write carrying
// them out.
func (c *Steps) pendingStep(live []store.Pending) (cli.Warning, bool) {
	if len(live) == 0 {
		return cli.Warning{}, false
	}
	var paths []string
	seen := map[string]bool{}
	for _, r := range live {
		s, err := Parse(r.Step)
		if err != nil {
			continue
		}
		for _, p := range s.Paths() {
			if p = c.abs(p); !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return cli.Warning{
		Kind:    "pending-step",
		Message: fmt.Sprintf("%d filesystem steps are pending with no write carrying them out. The next write, or koan repair, finishes them.", len(live)),
		Paths:   paths,
	}, true
}

// abs is a step's path under the root as written.
func (c *Steps) abs(rel string) string { return filepath.Join(c.root.Path, filepath.FromSlash(rel)) }

// inject calls the test's fault hook at a point; its error is the
// operating system refusing there.
func inject(point string) error {
	if store.Fault == nil {
		return nil
	}
	return store.Fault(point)
}

// work is one step being carried out.
type work struct {
	root string // the root's directory
	seq  int64
	dirs []string // the directories it touched, relative to the root
}

func (w *work) abs(rel string) string { return filepath.Join(w.root, filepath.FromSlash(rel)) }

// do runs one operation of the step, after the fault hook.
func (w *work) do(what string, fn func() error) error {
	if err := inject(fmt.Sprintf("step %d: %s", w.seq, what)); err != nil {
		return err
	}
	return fn()
}

// touch records directories the step changes, to flush.
func (w *work) touch(dirs ...string) {
	for _, d := range dirs {
		if !slices.Contains(w.dirs, d) {
			w.dirs = append(w.dirs, d)
		}
	}
}

// listed reports whether the listing of rel's parent holds it.
func (w *work) listed(rel string) (bool, error) {
	return listed(w.abs(path.Dir(rel)), path.Base(rel))
}

// carry carries out a step. found is what a stuck step found, or "".
func (w *work) carry(s Step) (found string, err error) {
	switch s.Op {
	case OpMkdir:
		return "", w.mkdir(s.To, true)
	case OpWrite:
		return "", w.write(s.To, []byte(s.Content))
	case OpMove:
		return w.move(s)
	case OpRemove:
		return "", w.remove(s.From, s.Dir)
	case OpArchive:
		return w.archive(s)
	}
	return "", fmt.Errorf("unknown step %q", s.Op)
}

// mkdir makes a directory, done when it exists; a folder's is done when
// it holds @notes.md too, and gets an empty one if it doesn't.
func (w *work) mkdir(rel string, notes bool) error {
	ok, err := w.listed(rel)
	if err != nil {
		return err
	}
	if !ok {
		w.touch(path.Dir(rel), rel)
		if err := w.do("mkdir "+rel, func() error { return os.Mkdir(w.abs(rel), 0o755) }); err != nil {
			return err
		}
	}
	if !notes {
		return nil
	}
	n := path.Join(rel, FolderNotes)
	if ok, err := w.listed(n); err != nil || ok {
		return err
	}
	w.touch(rel)
	return w.do("create "+n, func() error { return createEmpty(w.abs(n)) })
}

// write writes a file through its temp file, done when the file exists.
// A temp file left behind is removed once it is done.
func (w *work) write(rel string, data []byte) error {
	tmp := path.Join(path.Dir(rel), TempName(w.seq))
	ok, err := w.listed(rel)
	if err != nil {
		return err
	}
	if ok {
		return w.clean(tmp)
	}
	w.touch(path.Dir(rel))
	if err := w.do("write "+tmp, func() error { return writeFile(w.abs(tmp), data) }); err != nil {
		return err
	}
	err = w.do("rename "+tmp, func() error { return renameNew(w.abs(tmp), w.abs(rel)) })
	if errors.Is(err, fs.ErrExist) {
		// A hand change got there first, and the step is done once the
		// file exists.
		if ok, lerr := w.listed(rel); lerr != nil || !ok {
			return errors.Join(err, lerr)
		}
		return w.clean(tmp)
	}
	return err
}

// clean removes a temp file left behind.
func (w *work) clean(tmp string) error {
	if ok, err := w.listed(tmp); err != nil || !ok {
		return err
	}
	w.touch(path.Dir(tmp))
	return w.do("remove "+tmp, func() error { return os.Remove(w.abs(tmp)) })
}

// remove removes notes or a directory, done when the path is gone.
func (w *work) remove(rel string, dir bool) error {
	if ok, err := w.listed(rel); err != nil || !ok {
		return err
	}
	w.touch(path.Dir(rel))
	return w.do("remove "+rel, func() error {
		if dir {
			return os.RemoveAll(w.abs(rel))
		}
		return os.Remove(w.abs(rel))
	})
}

// move moves notes or a directory, done when the source is gone and the
// destination exists. A case-only rename passes through Via, and resumes
// from whichever of its three names the listing shows; it is done when
// the listing shows the new spelling and neither of the others. Where two
// sides are there, an empty one goes: an empty source is removed, an
// empty destination replaced; both holding content is stuck. Neither is
// stuck for a directory, and done for notes, since missing notes read as
// empty.
func (w *work) move(s Step) (string, error) {
	for range 8 {
		f, err := w.listed(s.From)
		if err != nil {
			return "", err
		}
		v := false
		if s.Via != "" {
			if v, err = w.listed(s.Via); err != nil {
				return "", err
			}
		}
		t, err := w.listed(s.To)
		if err != nil {
			return "", err
		}
		var stuck bool
		switch {
		case t && !f && !v:
			return "", nil
		case !t && !f && !v:
			if s.Dir {
				return Neither, nil
			}
			return "", nil
		case f && v:
			stuck, err = w.settle(s.From, s.Via, s.Dir)
		case f && t:
			stuck, err = w.settle(s.From, s.To, s.Dir)
		case v && t:
			stuck, err = w.settle(s.Via, s.To, s.Dir)
		case f && s.Via != "":
			err = w.rename(s.From, s.Via)
		case f:
			err = w.rename(s.From, s.To)
		default:
			err = w.rename(s.Via, s.To)
		}
		if stuck {
			return Both, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("moving %s to %s did not settle", s.From, s.To)
}

// settle finds which of two sides is empty and removes it, the source
// first. stuck is true when both hold content.
func (w *work) settle(src, dst string, dir bool) (stuck bool, err error) {
	for _, side := range []string{src, dst} {
		empty, err := emptySide(w.abs(side), dir)
		if err != nil {
			return false, err
		}
		if empty {
			w.touch(path.Dir(side))
			return false, w.do("remove empty "+side, func() error { return removeEmpty(w.abs(side), dir) })
		}
	}
	return true, nil
}

// rename renames without replacing. Finding the destination there is no
// failure: the move judges the sides again.
func (w *work) rename(from, to string) error {
	w.touch(path.Dir(from), path.Dir(to))
	err := w.do("rename "+from, func() error { return renameNew(w.abs(from), w.abs(to)) })
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	return err
}

// archive moves notes or a directory to the archive, done when it sits
// there: it makes the directories above it, writes its JSON through a
// temp file renamed into place beside where it goes, then renames it out
// of the tree. Notes missing from the tree leave the JSON alone; a missing
// directory is stuck.
func (w *work) archive(s Step) (string, error) {
	parts := strings.Split(path.Dir(s.To), "/")
	for i := range parts {
		if err := w.mkdir(strings.Join(parts[:i+1], "/"), false); err != nil {
			return "", err
		}
	}
	if err := w.write(s.File, s.Rows); err != nil {
		return "", err
	}
	for range 2 {
		if ok, err := w.listed(s.To); err != nil || ok {
			return "", err
		}
		ok, err := w.listed(s.From)
		if err != nil {
			return "", err
		}
		if !ok {
			if s.Dir {
				return Neither, nil
			}
			return "", nil
		}
		if err := w.rename(s.From, s.To); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("archiving %s to %s did not settle", s.From, s.To)
}
