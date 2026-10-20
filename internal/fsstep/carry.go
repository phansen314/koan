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
// with its filesystem steps carried out here. It is how a command opens
// the store.
func Open(home string) (*store.Store, *cli.Error) {
	return store.Open(home, stepsFor)
}

// OpenDatabase opens a root's database, as store.OpenDatabase does, with
// its filesystem steps carried out here.
func OpenDatabase(r store.Root) (*store.Store, int, *cli.Error) {
	return store.OpenDatabase(r, stepsFor)
}

func stepsFor(r store.Root) store.Steps { return New(r) }

// Encode is a step as its pending row holds it, given the seq it will
// have. A case-only move gets its temporary name from that seq, so it
// records all three names. Anything but a Step is refused.
func (c *Steps) Encode(seq int64, step any) (json.RawMessage, error) {
	s, ok := step.(Step)
	if !ok {
		return nil, fmt.Errorf("%T is not a filesystem step", step)
	}
	if s.Op == OpRemove && s.Dir && !s.Force && len(s.Owns) == 0 {
		return nil, fmt.Errorf("a directory remove of %s that isn't forced records nothing it may remove", s.From)
	}
	if s.Op == OpMove && caseOnly(s.From, s.To) {
		s.Via = path.Join(path.Dir(s.From), CaseName(seq))
	}
	return json.Marshal(s)
}

// Carry carries out the live steps, oldest first, then flushes each
// directory they concern. A step that fails or sticks holds back the rest
// of its change, since those may depend on it; later changes go on,
// except that a step waits, holding back the rest of its change, while a
// step of an earlier change that overlaps it is still pending, stuck or
// not.
func (c *Steps) Carry(live, stuck []store.Pending) store.Outcome {
	var out store.Outcome
	held := map[int64]bool{}
	// waiting is every step still pending, by change.
	type row struct {
		change int64
		step   Step
	}
	var waiting []row
	for _, r := range stuck {
		if s, err := Parse(r.Step); err == nil {
			waiting = append(waiting, row{r.Change, s})
		}
	}
	type carried struct {
		seq  int64
		dirs []string
	}
	var done []carried
	for _, r := range live {
		s, err := Parse(r.Step)
		if err != nil {
			held[r.Change] = true
			continue
		}
		if !held[r.Change] && slices.ContainsFunc(waiting, func(w row) bool { return w.change != r.Change && overlaps(w.step, s) }) {
			held[r.Change] = true
		}
		if held[r.Change] {
			waiting = append(waiting, row{r.Change, s})
			continue
		}
		w := &work{root: c.root.Dir, seq: r.Seq}
		found, err := w.carry(s)
		switch {
		case err != nil:
			held[r.Change] = true
			waiting = append(waiting, row{r.Change, s})
		case found != "":
			held[r.Change] = true
			waiting = append(waiting, row{r.Change, s})
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

// overlaps reports whether two steps touch the same path, or one above
// the other, ignoring case, so the order is the same on every filesystem.
func overlaps(a, b Step) bool {
	for _, p := range a.Paths() {
		for _, q := range b.Paths() {
			if under(p, q) || under(q, p) {
				return true
			}
		}
	}
	return false
}

// under reports whether p is q or below it, ignoring case.
func under(p, q string) bool {
	return len(p) >= len(q) && strings.EqualFold(p[:len(q)], q) && (len(p) == len(q) || p[len(q)] == '/')
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
			Message: "A filesystem step found both its source and its destination holding content, or neither, or a remove found material, and is set aside with the rest of its change. Keep one copy, or move the material out, then run koan repair.",
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
	message := fmt.Sprintf("%d filesystem steps are pending with no write carrying them out. The next write, or koan repair, finishes them.", len(live))
	if len(live) == 1 {
		message = "1 filesystem step is pending with no write carrying it out. The next write, or koan repair, finishes it."
	}
	return cli.Warning{
		Kind:    "pending-step",
		Message: message,
		Paths:   paths,
	}, true
}

// abs is a step's path under the root as written.
func (c *Steps) abs(rel string) string { return Abs(c.root, rel) }

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

// touch records directories the step concerns, to flush once it is
// done, whether it changed them or found them already done: a step found
// done may have been carried out by a write that crashed before its
// flush.
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

// errCase is a name on a step's paths that equals the one it needs only
// ignoring case. The step fails, on every filesystem, and stays pending.
type errCase struct{ found, needed string }

func (e errCase) Error() string {
	return fmt.Sprintf("%s differs only in case from %s, which a step needs", e.found, e.needed)
}

// sameCase lists each directory on rel, from tree/ or archived/ down,
// and fails where a name in a listing equals rel's own name there only
// ignoring case. With leaf, only rel's own name is checked. allow names
// the other names a case-only rename has beside its own.
func (w *work) sameCase(rel string, leaf bool, allow ...string) error {
	parts := strings.Split(rel, "/")
	first := 1
	if leaf {
		first = len(parts) - 1
	}
	for i := first; i < len(parts); i++ {
		dir := strings.Join(parts[:i], "/")
		listing, ok, err := names(w.abs(dir))
		if err != nil || !ok {
			return err
		}
		for _, n := range listing {
			if n != parts[i] && strings.EqualFold(n, parts[i]) && !(i == len(parts)-1 && slices.Contains(allow, n)) {
				return errCase{path.Join(dir, n), path.Join(dir, parts[i])}
			}
		}
	}
	return nil
}

// carry carries out a step. found is what a stuck step found, or "".
func (w *work) carry(s Step) (found string, err error) {
	switch s.Op {
	case OpMkdir:
		w.touch(path.Dir(s.To), s.To)
		if err := w.sameCase(path.Join(s.To, FolderNotes), false); err != nil {
			return "", err
		}
		return "", w.mkdir(s.To, s.Content)
	case OpWrite:
		w.touch(path.Dir(s.To))
		if err := w.sameCase(s.To, false); err != nil {
			return "", err
		}
		return "", w.write(s.To, []byte(s.Content))
	case OpMove:
		var allow []string
		for _, p := range []string{s.From, s.Via, s.To} {
			if p != "" {
				w.touch(path.Dir(p))
				allow = append(allow, path.Base(p))
			}
		}
		if s.Via == "" {
			allow = nil
		}
		for _, p := range []string{s.From, s.Via, s.To} {
			if p == "" {
				continue
			}
			if err := w.sameCase(p, false, allow...); err != nil {
				return "", err
			}
		}
		return w.move(s)
	case OpRemove:
		w.touch(path.Dir(s.From))
		if err := w.sameCase(s.From, false); err != nil {
			return "", err
		}
		return w.remove(s)
	case OpArchive:
		return w.archive(s)
	}
	return "", fmt.Errorf("unknown step %q", s.Op)
}

// mkdir makes a folder's directory, done when it exists holding
// @notes.md, making whichever is missing. Notes with content are written
// through a temp file; empty ones are created in place.
func (w *work) mkdir(rel, content string) error {
	ok, err := w.listed(rel)
	if err != nil {
		return err
	}
	if !ok {
		if err := w.do("mkdir "+rel, func() error { return os.Mkdir(w.abs(rel), 0o755) }); err != nil {
			return err
		}
	}
	n := path.Join(rel, FolderNotes)
	if content != "" {
		return w.write(n, []byte(content))
	}
	if ok, err := w.listed(n); err != nil || ok {
		return err
	}
	return w.do("create "+n, func() error { return createEmpty(w.abs(n)) })
}

// write writes a file through its temp file, done when the file exists.
// A temp file left behind is removed once it is done.
func (w *work) write(rel string, data []byte) error {
	tmp := path.Join(path.Dir(rel), TempName(w.seq))
	if err := w.sameCase(tmp, true); err != nil {
		return err
	}
	ok, err := w.listed(rel)
	if err != nil {
		return err
	}
	if ok {
		return w.clean(tmp)
	}
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
	return w.do("remove "+tmp, func() error { return os.Remove(w.abs(tmp)) })
}

// remove removes notes or a directory, done when the path is gone. Unless
// its change was forced, a directory remove first looks for material,
// since what was checked before COMMIT can change before the step runs;
// finding any, it removes nothing and is stuck. Finding none, it removes
// only what isn't material, so material written while it runs stays.
func (w *work) remove(s Step) (string, error) {
	if ok, err := w.listed(s.From); err != nil || !ok {
		return "", err
	}
	if s.Dir && !s.Force {
		m, err := w.holdsMaterial(s.From, s.Owns)
		if m {
			return Material, nil
		}
		if err != nil {
			return "", err
		}
		return "", w.removeKoan(s.From, s.Owns)
	}
	return "", w.do("remove "+s.From, func() error {
		if s.Dir {
			return os.RemoveAll(w.abs(s.From))
		}
		return os.Remove(w.abs(s.From))
	})
}

// material reports whether an entry listed in a directory being removed
// is material in itself: any entry not named with @, other than
// .DS_Store, or an @ file the step doesn't record that holds a byte or
// more. An @ directory is material by what it holds.
func material(p string, de fs.DirEntry, owns []string) (bool, error) {
	n := de.Name()
	switch {
	case n == ".DS_Store" || de.IsDir() && strings.HasPrefix(n, "@"):
		return false, nil
	case !strings.HasPrefix(n, "@"):
		return true, nil
	case slices.Contains(owns, p):
		return false, nil
	}
	info, err := de.Info()
	if err != nil {
		return false, err
	}
	return info.Size() > 0, nil
}

// holdsMaterial reports whether a directory being removed holds material,
// in it or in any @ directory under it.
func (w *work) holdsMaterial(rel string, owns []string) (bool, error) {
	var entries []fs.DirEntry
	if err := w.do("look in "+rel, func() (err error) {
		entries, err = os.ReadDir(w.abs(rel))
		return err
	}); err != nil {
		return false, err
	}
	for _, de := range entries {
		p := path.Join(rel, de.Name())
		m, err := material(p, de, owns)
		if err == nil && !m && de.IsDir() && strings.HasPrefix(de.Name(), "@") {
			m, err = w.holdsMaterial(p, owns)
		}
		if err != nil || m {
			return m, err
		}
	}
	return false, nil
}

// MaterialIn is the material at the top level of a folder's directory,
// counted as a delete counts it, as step 4 of a write checks it before
// a directory remove that isn't forced: any entry not named with @ but
// .DS_Store, an @ file owns doesn't record that holds content, and an @
// directory owns doesn't record that holds material. An @ directory owns
// records is a folder being deleted, whose entries are counted as its
// own. Names come in byte order; a directory missing from its parent's
// listing, as spelled, holds none.
func MaterialIn(r store.Root, dir string, owns []string) ([]string, error) {
	w := &work{root: r.Dir}
	if ok, err := w.listed(dir); err != nil || !ok {
		return nil, err
	}
	entries, err := os.ReadDir(w.abs(dir))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, de := range entries {
		p := path.Join(dir, de.Name())
		folder := de.IsDir() && strings.HasPrefix(de.Name(), "@")
		if folder && slices.Contains(owns, p) {
			continue
		}
		m, err := material(p, de, owns)
		if err == nil && !m && folder {
			m, err = w.holdsMaterial(p, owns)
		}
		if err != nil {
			return nil, err
		}
		if m {
			out = append(out, de.Name())
		}
	}
	return out, nil
}

// removeKoan removes a directory bottom-up, never by a recursive remove: in each
// directory it unlinks only the entries it listed there that aren't
// material, then removes the directory with rmdir, which the operating
// system refuses while it isn't empty. Material written since the walk is
// left, and the step fails: an @ file the step doesn't record is looked
// at again as it is unlinked, and left if it now holds content.
func (w *work) removeKoan(rel string, owns []string) error {
	entries, err := os.ReadDir(w.abs(rel))
	if err != nil {
		return err
	}
	for _, de := range entries {
		n := de.Name()
		p := path.Join(rel, n)
		m, err := material(p, de, owns)
		switch {
		case err != nil || m:
		case de.IsDir() && strings.HasPrefix(n, "@"):
			err = w.removeKoan(p, owns)
		default:
			err = w.do("unlink "+p, func() error {
				if n != ".DS_Store" && !slices.Contains(owns, p) {
					if info, err := os.Lstat(w.abs(p)); err != nil || info.Size() > 0 {
						return errors.Join(fmt.Errorf("%s holds content", p), err)
					}
				}
				return os.Remove(w.abs(p))
			})
		}
		if err != nil {
			return err
		}
	}
	return w.do("rmdir "+rel, func() error { return os.Remove(w.abs(rel)) })
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
			return false, w.do("remove empty "+side, func() error { return removeEmpty(w.abs(side), dir) })
		}
	}
	return true, nil
}

// rename renames without replacing. Finding the destination there is no
// failure: the move judges the sides again.
func (w *work) rename(from, to string) error {
	err := w.do("rename "+from, func() error { return renameNew(w.abs(from), w.abs(to)) })
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	return err
}

// archive moves notes or a directory to the archive, done when it sits
// there: it makes the directories above it, using any already there in
// whatever case, writes its JSON through a temp file renamed into place
// beside where it goes, then renames it out of the tree. Notes missing
// from the tree leave the JSON alone; a missing directory is stuck.
func (w *work) archive(s Step) (string, error) {
	w.touch(path.Dir(s.From))
	if err := w.sameCase(s.From, false); err != nil {
		return "", err
	}
	dir, err := w.above(path.Dir(s.To))
	if err != nil {
		return "", err
	}
	w.touch(dir)
	to, file := path.Join(dir, path.Base(s.To)), path.Join(dir, path.Base(s.File))
	for _, p := range []string{to, file} {
		if err := w.sameCase(p, true); err != nil {
			return "", err
		}
	}
	if err := w.write(file, s.Rows); err != nil {
		return "", err
	}
	for range 2 {
		if ok, err := w.listed(to); err != nil || ok {
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
		if err := w.rename(s.From, to); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("archiving %s to %s did not settle", s.From, s.To)
}

// above makes the directories of rel, below archived/, that are missing,
// and gives rel as it is found on disk: a directory already there in
// another case is used as it is, and never gets a second spelling beside
// it.
func (w *work) above(rel string) (string, error) {
	parts := strings.Split(rel, "/")
	dir := parts[0]
	for _, p := range parts[1:] {
		listing, _, err := names(w.abs(dir))
		if err != nil {
			return "", err
		}
		name := p
		if !slices.Contains(listing, p) {
			slices.Sort(listing)
			if i := slices.IndexFunc(listing, func(n string) bool { return strings.EqualFold(n, p) }); i >= 0 {
				name = listing[i]
			}
		}
		next := path.Join(dir, name)
		w.touch(dir, next)
		if !slices.Contains(listing, name) {
			if err := w.do("mkdir "+next, func() error { return os.Mkdir(w.abs(next), 0o755) }); err != nil {
				return "", err
			}
		}
		dir = next
	}
	return dir, nil
}
