package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// walker compares the tree's directory with the folders and tasks, by
// listing each folder's directory and comparing names byte for byte.
type walker struct {
	*checker
	// dirs is each folder's directory as it is on disk, relative to the
	// root's directory: spelled exactly, or in another case; for a folder
	// whose directory is missing, where it would go.
	dirs    map[int64]string
	missing map[int64]bool
	// owned is the @.case-<seq> and @.tmp-<seq> names pending steps use,
	// which are theirs.
	owned  map[string]bool
	strays []stray
	// notes is the @<id>.md entries found, judged once every folder's
	// directory is known.
	notes []found
}

// found is an entry of a folder's directory.
type found struct {
	folder int64
	dir    string
	entry  fs.DirEntry
}

// stray is an @ directory that is no folder at its path.
type stray struct {
	path   string
	parent int64 // the folder whose directory it sits in
	notes  []int64
}

func (c *checker) tree() error {
	w := &walker{checker: c, dirs: map[int64]string{core.RootID: store.TreeName}, missing: map[int64]bool{}, owned: map[string]bool{}}
	if err := w.own(); err != nil {
		return err
	}
	if err := w.walk(core.RootID, store.TreeName); err != nil {
		return err
	}
	for _, n := range w.notes {
		id, _ := core.ParseID(strings.TrimSuffix(strings.TrimPrefix(n.entry.Name(), "@"), ".md"))
		if err := w.note(n.folder, n.dir, n.entry, id); err != nil {
			return err
		}
	}
	return w.match()
}

// own finds the names pending steps use for themselves.
func (w *walker) own() error {
	live, stuck, err := w.tx.Pending()
	if err != nil {
		return err
	}
	for _, r := range append(live, stuck...) {
		s, err := fsstep.Parse(r.Step)
		if err != nil {
			continue
		}
		if s.Via != "" {
			w.owned[s.Via] = true
		}
		switch s.Op {
		case fsstep.OpWrite:
			w.owned[path.Join(path.Dir(s.To), fsstep.TempName(r.Seq))] = true
		case fsstep.OpMkdir:
			w.owned[path.Join(s.To, fsstep.TempName(r.Seq))] = true
		}
	}
	return nil
}

// list lists a directory, relative to the root's directory, in byte
// order. A missing one holds nothing.
func (w *walker) list(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(filepath.Join(w.root.Dir, filepath.FromSlash(dir)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}

func depth(folder string) int { return strings.Count(folder, "/") }

// walk judges a folder's directory, dir, and the folders under it.
func (w *walker) walk(folder int64, dir string) error {
	entries, err := w.list(dir)
	if err != nil {
		return err
	}
	used := map[string]bool{}
	kids := w.children[folder]
	// Exact names first, so a directory spelled exactly is never taken
	// for another folder's.
	for _, k := range kids {
		name := "@" + w.folders[k].name
		if i := slices.IndexFunc(entries, func(e fs.DirEntry) bool { return e.Name() == name }); i >= 0 && entries[i].IsDir() {
			used[name] = true
			w.dirs[k] = path.Join(dir, name)
		}
	}
	for _, k := range kids {
		if _, ok := w.dirs[k]; ok {
			continue
		}
		name := "@" + w.folders[k].name
		i := slices.IndexFunc(entries, func(e fs.DirEntry) bool {
			return e.IsDir() && !used[e.Name()] && e.Name() != name && strings.EqualFold(e.Name(), name)
		})
		if i < 0 {
			w.lost(k, path.Join(dir, name))
			continue
		}
		used[entries[i].Name()] = true
		at := path.Join(dir, entries[i].Name())
		w.dirs[k] = at
		f := newFinding(CaseDiffers, "", fmt.Sprintf("The directory of @%d (%s) is spelled %s. koan repair renames it %s.", k, w.paths[k], w.abs(at), name),
			[]int64{k}, []string{w.abs(at)})
		f.Repairable = true
		f.step = &fsstep.Step{Op: fsstep.OpMove, From: at, To: path.Join(dir, name), Dir: true}
		f.depth = depth(w.paths[k])
		w.add(f)
	}
	for _, e := range entries {
		if !used[e.Name()] {
			if err := w.entry(folder, dir, e); err != nil {
				return err
			}
		}
	}
	for _, k := range kids {
		if !w.missing[k] {
			if err := w.walk(k, w.dirs[k]); err != nil {
				return err
			}
		}
	}
	return nil
}

// lost reports a folder with no directory, at where its directory
// would go, and the folders under it the same way.
func (w *walker) lost(folder int64, at string) {
	w.missing[folder] = true
	w.dirs[folder] = at
	p := w.paths[folder]
	f := newFinding(MissingDirectory, "", fmt.Sprintf("@%d (%s) has no directory. koan repair makes an empty one, with empty notes: whatever notes and material it held are gone.", folder, p),
		[]int64{folder}, []string{w.abs(fsstep.FolderDir(p))})
	f.Repairable = true
	f.step = &fsstep.Step{Op: fsstep.OpMkdir, To: at}
	f.depth = depth(p)
	w.add(f)
	for _, k := range w.children[folder] {
		w.lost(k, path.Join(at, "@"+w.folders[k].name))
	}
}

// entry judges an entry of a folder's directory that is no folder's
// directory. Material is never looked at.
func (w *walker) entry(folder int64, dir string, e fs.DirEntry) error {
	n := e.Name()
	p := path.Join(dir, n)
	if !strings.HasPrefix(n, "@") || w.owned[p] {
		return nil
	}
	if e.IsDir() {
		return w.stray(folder, p)
	}
	switch {
	case n == fsstep.FolderNotes && folder != core.RootID:
		return nil
	case n == fsstep.StartNotes || n == fsstep.DoneNotes:
		end := strings.TrimSuffix(strings.TrimPrefix(n, "@"), ".md")
		if folder != core.RootID && len(w.gates[folder][end]) > 0 {
			return nil
		}
		where := "its folder doesn't have"
		if folder == core.RootID {
			where = "the root can't have"
		}
		return w.orphan(e, p, "no-gate", 0, fmt.Sprintf("%s names a %s gate %s.", w.abs(p), end, where))
	case n != fsstep.FolderNotes && fsstep.IsNotesName(n):
		w.notes = append(w.notes, found{folder, dir, e})
		return nil
	}
	w.strayEntry(p, fmt.Sprintf("%s is not one of koan's names.", w.abs(p)))
	return nil
}

func (w *walker) strayEntry(p, why string) {
	w.add(newFinding(StrayEntry, "", why+" koan only reports it: rename it without the @, or move it out.", nil, []string{w.abs(p)}))
}

// note judges an @<id>.md.
func (w *walker) note(folder int64, dir string, e fs.DirEntry, id int64) error {
	p := path.Join(dir, e.Name())
	if t, ok := w.tasks[id]; ok {
		home, ok := w.dirs[t.folder]
		if !ok {
			// Its folder's parent chain never reaches the root, so it has
			// no place to be judged by.
			return nil
		}
		want := "@" + strconv.FormatInt(id, 10) + ".md"
		gate := core.End(t.gate)
		if gate == core.EndStart || gate == core.EndDone {
			want = "@" + t.gate + ".md"
		}
		if t.folder == folder && want == e.Name() {
			return nil
		}
		belongs := fsstep.TaskNotesPath(w.paths[t.folder], id, gate)
		to := path.Join(home, want)
		missing, err := fsstep.Missing(w.root, to)
		if err != nil {
			return err
		}
		message := fmt.Sprintf("The notes of @%d sit in %s, and belong in %s. koan repair moves them back.", id, w.abs(p), w.abs(belongs))
		if !missing {
			message = fmt.Sprintf("The notes of @%d sit in %s, and belong in %s, where notes already are. Keep one copy by hand.", id, w.abs(p), w.abs(belongs))
		}
		f := newFinding(MisplacedNotes, "", message, []int64{id}, []string{w.abs(p), w.abs(belongs)})
		f.Repairable = missing
		f.step = &fsstep.Step{Op: fsstep.OpMove, From: p, To: to}
		f.depth = depth(w.paths[t.folder])
		w.add(f)
		return nil
	}
	if _, ok := w.folders[id]; ok {
		w.strayEntry(p, fmt.Sprintf("%s is named for @%d, which is a folder, not a task.", w.abs(p), id))
		return nil
	}
	i, issued := w.issued[id]
	switch {
	case !issued:
		return w.orphan(e, p, "never-issued", id, fmt.Sprintf("%s names @%d, which was never issued.", w.abs(p), id))
	case i.state == "deleted":
		return w.orphan(e, p, "deleted", id, fmt.Sprintf("%s is the notes of @%d, which was deleted.", w.abs(p), id))
	case i.state == "archived":
		return w.orphan(e, p, "archived", id, fmt.Sprintf("%s is the notes of @%d, which was archived to %s.", w.abs(p), id, w.tx.ArchivedAt(id)))
	}
	w.strayEntry(p, fmt.Sprintf("%s names @%d, which is issued and is neither a task nor a folder.", w.abs(p), id))
	return nil
}

// orphan reports notes that belong to nothing; they are repairable when
// empty.
func (w *walker) orphan(e fs.DirEntry, p, detail string, id int64, why string) error {
	empty := false
	if e.Type().IsRegular() {
		info, err := e.Info()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		empty = err == nil && info.Size() == 0
	}
	var ids []int64
	if id != 0 {
		ids = []int64{id}
	}
	message := why + " It is empty, and koan repair removes it."
	if !empty {
		message = why + " It holds content, so koan leaves it: keep what you want of it, then remove it."
	}
	f := newFinding(OrphanNotes, detail, message, ids, []string{w.abs(p)})
	f.Repairable = empty
	f.step = &fsstep.Step{Op: fsstep.OpRemove, From: p}
	w.add(f)
	return nil
}

// stray records an @ directory that is no folder at its path, with the
// IDs of the @<id>.md notes directly in it.
func (w *walker) stray(folder int64, p string) error {
	entries, err := w.list(p)
	if err != nil {
		return err
	}
	s := stray{path: p, parent: folder}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !fsstep.IsNotesName(n) || n == fsstep.FolderNotes || n == fsstep.StartNotes || n == fsstep.DoneNotes {
			continue
		}
		id, _ := core.ParseID(strings.TrimSuffix(strings.TrimPrefix(n, "@"), ".md"))
		s.notes = append(s.notes, id)
	}
	w.strays = append(w.strays, s)
	return nil
}

// match reports each stray directory, with the folder it would move back
// to when exactly one stray and one folder whose directory is missing
// match: they share a parent, or every @<id>.md in it, at least one,
// belongs to a task in that folder. A name alone never decides.
func (w *walker) match() error {
	var missing []int64
	for id := range w.missing {
		missing = append(missing, id)
	}
	slices.Sort(missing)
	matches := func(s stray, f int64) bool {
		if w.folders[f].parent == s.parent {
			return true
		}
		if len(s.notes) == 0 {
			return false
		}
		for _, id := range s.notes {
			if t, ok := w.tasks[id]; !ok || t.folder != f {
				return false
			}
		}
		return true
	}
	cands := make([][]int64, len(w.strays))
	count := map[int64]int{}
	for i, s := range w.strays {
		for _, f := range missing {
			if matches(s, f) {
				cands[i] = append(cands[i], f)
				count[f]++
			}
		}
	}
	for i, s := range w.strays {
		if len(cands[i]) != 1 || count[cands[i][0]] != 1 {
			w.add(newFinding(StrayDirectory, "", fmt.Sprintf("%s is no folder at its path, and koan can't tell which folder it is, so it only reports it. Move it where it belongs, or move its contents out.", w.abs(s.path)),
				nil, []string{w.abs(s.path)}))
			continue
		}
		f := cands[i][0]
		found := newFinding(StrayDirectory, "", fmt.Sprintf("%s is no folder at its path. It is @%d (%s), whose directory is missing: koan repair moves it back.", w.abs(s.path), f, w.paths[f]),
			[]int64{f}, []string{w.abs(s.path)})
		found.Repairable = true
		found.step = &fsstep.Step{Op: fsstep.OpMove, From: s.path, To: w.dirs[f], Dir: true}
		found.depth = depth(w.paths[f])
		w.add(found)
	}
	return nil
}
