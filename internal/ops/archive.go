package ops

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

type archiveFolderResult struct {
	ID         model.ID     `json:"id"`
	Path       model.Folder `json:"path"`
	Changed    bool         `json:"changed"`
	Archived   string       `json:"archived"`
	IDs        []model.ID   `json:"ids"`
	Dependents []model.ID   `json:"dependents"`
}

// cmdFolderArchive moves a done folder, with everything under it, into the
// archive (spec/operations.md, folder archive).
func cmdFolderArchive(c *call) (any, *errs.Error) {
	ref := c.in.top().folderRef("folder", true, folderRef{})
	if ref.path == model.Root {
		c.in.bad("/folder", "may not be /, the tree itself")
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	f, e := c.resolve(ref)
	if e != nil {
		return nil, e
	}
	if e := c.folderOrNotFound(f); e != nil {
		return nil, e
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	// Every folder file under it must be usable: the archive records their
	// IDs.
	var ids []model.ID
	var own *model.FolderFile
	for _, g := range x.Folders {
		if !within(f, g) {
			continue
		}
		ff, e := c.folderFile(g)
		if e != nil {
			return nil, e
		}
		if g == f {
			own = ff
		}
		ids = append(ids, ff.ID)
	}
	var tasks, open []model.ID
	var openIn []model.Folder
	for _, id := range x.IDs() {
		g := x.Tasks[id]
		if !within(f, g) {
			continue
		}
		tasks = append(tasks, id)
		if l := c.t.Load(g, id); l.File == nil || l.File.Open() {
			open = append(open, id)
			if !slices.Contains(openIn, g) {
				openIn = append(openIn, g)
			}
		}
	}
	if len(open) > 0 {
		slices.SortFunc(openIn, model.TreeOrder)
		e := errs.NewConflict(errs.RuleNotDone, "%s is not done: %d tasks under it are open, or their files can't be used", f, len(open)).WithIDs(open...)
		for _, g := range openIn {
			e.WithFolders(string(g))
		}
		return nil, e
	}
	if e := c.met(); e != nil {
		return nil, e
	}
	ids = slices.Sorted(slices.Values(append(ids, tasks...)))
	dirs, e := c.t.ArchiveDirs(f.Parent())
	if e != nil {
		return nil, e
	}
	dest := path.Join(dirs, model.ArchivedFolderName(own.ID, f.Name()))
	taken, e := c.t.Move(f.Rel(), dest)
	if e != nil {
		return nil, e
	}
	if taken {
		return nil, errs.NewConflict(errs.RuleDestinationExists, "%s is already in the archive, which only an outside change leaves", c.t.Abs(dest)).
			WithFolders(string(f)).WithPaths(c.t.Abs(dest))
	}
	entry := &model.ArchiveEntry{Schema: model.ArchiveFormat, ID: own.ID, Kind: model.ArchivedFolder, Title: own.Title,
		Path: f, Archived: archived(dest), ArchivedAt: &c.now, IDs: ids}
	dependents, e := c.afterArchive(ids, tasks, f, entry, dest)
	if e != nil {
		return nil, e
	}
	return archiveFolderResult{ID: own.ID, Path: f, Changed: true, Archived: c.t.Abs(dest), IDs: ids, Dependents: dependents}, nil
}

type archiveTaskResult struct {
	ID         model.ID     `json:"id"`
	Path       model.Folder `json:"path"`
	Changed    bool         `json:"changed"`
	Archived   string       `json:"archived"`
	Dependents []model.ID   `json:"dependents"`
}

// cmdTaskArchive moves a done task, with its notes, into the archive
// (spec/operations.md, task archive).
func cmdTaskArchive(c *call) (any, *errs.Error) {
	id := c.in.top().id("id", true)
	if e := c.begin(true); e != nil {
		return nil, e
	}
	f, tf, e := c.find(id)
	if e != nil {
		return nil, e
	}
	if tf.Open() {
		return nil, errs.NewConflict(errs.RuleNotDone, "task %d is open: only a done task is archived", id).WithIDs(id)
	}
	if e := c.met(); e != nil {
		return nil, e
	}
	dirs, e := c.t.ArchiveDirs(f)
	if e != nil {
		return nil, e
	}
	dest := path.Join(dirs, path.Base(model.TaskRel(f, id)))
	notesDest := path.Join(dirs, path.Base(model.NotesRel(f, id)))
	notes := model.NotesRel(f, id)
	fi, err := c.t.Stat(notes)
	hasNotes := err == nil && fi.Mode().IsRegular()
	// Both places are checked before either move, so that a task file is
	// never moved without its notes but by a kill.
	for _, p := range []string{dest, notesDest} {
		if _, err := c.t.Stat(p); err == nil && (p == dest || hasNotes) {
			return nil, errs.NewConflict(errs.RuleDestinationExists, "%s is already in the archive, which only an outside change leaves", c.t.Abs(p)).
				WithIDs(id).WithPaths(c.t.Abs(p))
		}
	}
	taken, e := c.t.Move(model.TaskRel(f, id), dest)
	if e == nil && taken {
		e = errs.NewConflict(errs.RuleDestinationExists, "%s is already in the archive, which only an outside change leaves", c.t.Abs(dest)).
			WithIDs(id).WithPaths(c.t.Abs(dest))
	}
	if e != nil {
		return nil, e
	}
	if hasNotes {
		taken, e := c.t.Move(notes, notesDest)
		if e == nil && taken {
			e = errs.New(errs.IO, "%s appeared while the task was archived", c.t.Abs(notesDest)).WithPaths(c.t.Abs(notesDest))
		}
		if e != nil {
			return nil, archiveFailed(e, []model.ID{id}, c.t.Abs(dest))
		}
	}
	title := tf.Title
	entry := &model.ArchiveEntry{Schema: model.ArchiveFormat, ID: id, Kind: model.ArchivedTask, Title: &title,
		Path: f, Archived: archived(dest), ArchivedAt: &c.now, IDs: []model.ID{id}}
	dependents, e := c.afterArchive([]model.ID{id}, []model.ID{id}, "", entry, dest)
	if e != nil {
		return nil, e
	}
	return archiveTaskResult{ID: id, Path: f, Changed: true, Archived: c.t.Abs(dest), Dependents: dependents}, nil
}

// afterArchive finishes an archive once the rename is made: the IDs
// archived leave every blocked_by in the tree, the tasks' claims end, and
// the index gets its line. A failure here leaves what repair finishes.
func (c *call) afterArchive(ids, tasks []model.ID, removed model.Folder, entry *model.ArchiveEntry, dest string) ([]model.ID, *errs.Error) {
	dependents, e := c.unreference(ids, removed)
	if e == nil {
		e = c.dropClaims(tasks...)
	}
	if e == nil {
		e = c.t.AppendIndex(entry)
	}
	if e != nil {
		return nil, archiveFailed(e, ids, c.t.Abs(dest))
	}
	return dependents, nil
}

// archiveFailed is the error of an archive that failed after its item left
// the tree.
func archiveFailed(e *errs.Error, ids []model.ID, at string) *errs.Error {
	e.WithIDs(ids...)
	e.Message += fmt.Sprintf("; what was archived is in the archive, at %s, and `koan repair` finishes the rest", at)
	return e
}

// diagnoseIndex checks the index against the archive: one archive-index
// finding when they differ, whose fix keeps each line that matches and adds
// a line for each item with none, at the end, ascending by ID
// (spec/design.md, The archive).
func diagnoseIndex(d *diagnosis, t *store.Tree, ar *store.Archive) {
	indexPath := t.Abs(store.IndexRel)
	lines, ie := t.ReadIndex()
	var keep []*model.ArchiveEntry
	matched := map[*model.ArchiveEntry]bool{}
	var wrong []string
	badLines := 0
	for _, l := range lines {
		if l.Entry == nil {
			badLines++
			continue
		}
		var item *model.ArchiveEntry
		for _, a := range ar.Items {
			if !matched[a] && a.Matches(l.Entry) {
				item = a
				break
			}
		}
		if item == nil {
			wrong = append(wrong, t.Abs(model.ArchiveRel(l.Entry.Archived)))
			continue
		}
		matched[item] = true
		keep = append(keep, l.Entry)
	}
	var missed []*model.ArchiveEntry
	for _, a := range ar.Items {
		if !matched[a] {
			missed = append(missed, a)
		}
	}
	if ie == nil && badLines == 0 && len(wrong) == 0 && len(missed) == 0 {
		return
	}
	slices.SortStableFunc(missed, func(a, b *model.ArchiveEntry) int { return cmp.Compare(a.ID, b.ID) })
	paths := []string{indexPath}
	var parts []string
	if ie != nil {
		parts = append(parts, ie.Message)
	}
	if badLines > 0 {
		parts = append(parts, fmt.Sprintf("%d lines koan can't read", badLines))
	}
	if len(wrong) > 0 {
		parts = append(parts, fmt.Sprintf("%d lines that don't match what is in the archive", len(wrong)))
		paths = append(paths, wrong...)
	}
	if len(missed) > 0 {
		parts = append(parts, fmt.Sprintf("no line for %d archived items", len(missed)))
		for _, a := range missed {
			paths = append(paths, t.Abs(model.ArchiveRel(a.Archived)))
		}
	}
	d.add(errs.FindingArchiveIndex, fmt.Sprintf("the archive's index doesn't match the archive (%s): `koan repair` rewrites it from the archive, keeping each line that matches", strings.Join(parts, "; ")),
		nil, paths, func() (bool, *errs.Error) { return done(t.WriteIndex(append(keep, missed...))) })
}

// archived is a root-relative path in the archive as the index writes it.
func archived(rel string) string { return strings.TrimPrefix(rel, model.ArchiveDir+"/") }

// whereArchived adds to a not-found error where in the archive what it
// names went, when the index says it was archived (spec/operations.md,
// References). An index koan can't read changes nothing.
func (c *call) whereArchived(e *errs.Error) {
	if e == nil || e.Kind != errs.NotFound || c.t == nil || len(e.IDs) == 0 && len(e.Folders) == 0 {
		return
	}
	lines, ie := c.t.ReadIndex()
	if ie != nil || len(lines) == 0 {
		return
	}
	var said []string
	add := func(what, rel string) {
		p := c.t.Abs(model.ArchiveRel(rel))
		if !slices.Contains(e.Paths, p) {
			e.WithPaths(p)
		}
		said = append(said, fmt.Sprintf("%s was archived, to %s", what, p))
	}
	for _, id := range e.IDs {
		for _, l := range lines {
			if a := l.Entry; a != nil && slices.Contains(a.IDs, id) {
				if a.ID == id {
					add(fmt.Sprintf("%s %d", a.Kind, id), a.Archived)
				} else {
					add(fmt.Sprintf("%d, in %s %d,", id, a.Kind, a.ID), a.Archived)
				}
			}
		}
	}
	for _, s := range e.Folders {
		f := model.Folder(s)
		if ok, _ := c.t.FolderExists(f); ok {
			continue
		}
		for _, l := range lines {
			a := l.Entry
			if a == nil || a.Kind != model.ArchivedFolder || !a.Path.Holds(f) {
				continue
			}
			rel := a.Archived
			for _, seg := range f.Segments()[len(a.Path.Segments()):] {
				rel = path.Join(rel, model.FolderDir(seg))
			}
			if _, err := c.t.Stat(model.ArchiveRel(rel)); err == nil {
				add(string(f), rel)
			}
		}
	}
	if len(said) > 0 {
		e.Message += ": " + strings.Join(said, "; ")
	}
}
