package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/markers"
	"github.com/phansen314/koan/internal/store"
)

// Location is what a reference or a path on disk names, in its schema's
// order.
type Location struct {
	Ref      string   `json:"ref"`
	Kind     string   `json:"kind"`
	ID       *int64   `json:"id"`
	Folder   FolderAt `json:"folder"`
	Gate     *string  `json:"gate"`
	Material *string  `json:"material"`
	File     *string  `json:"file"`
	Marker   *string  `json:"marker"`
}

// Where says what a reference or a path on disk names, both ways.
func Where() *cli.Command {
	return &cli.Command{
		Name:    "where",
		Uses:    []string{"<ref>", "--file <path>", "--here"},
		Subject: &cli.Option{Field: "ref", Complete: anyRef.complete()},
		Options: []cli.Option{{Field: "file", Kind: cli.Path}},
		Here:    &cli.Here{Field: "file", Flag: "--here"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			q := r.locating("ref", core.WantAny)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			var res Location
			var stale []cli.Warning
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				t, err := loadTasks(tx, s.Root)
				if err != nil {
					return nil, err
				}
				var at int64
				if res, at, stale, err = q.locate(tx, t, "ref"); err != nil {
					return nil, err
				}
				return []fsstep.Path{{ID: at, Path: *res.File}}, nil
			})
			if e != nil {
				return nil, nil, e
			}
			return res, append(warnings, stale...), nil
		},
	}
}

// query is what where and context are asked: a reference, or a path on
// disk.
type query struct {
	ref    core.Ref
	hasRef bool
	file   string
}

// locating reads a reference at field, or file: exactly one.
func (r *input) locating(field string, want core.Want) *query {
	q := &query{}
	_, q.hasRef = r.value(field)
	if q.hasRef {
		q.ref = r.ref(field, want, "")
	}
	q.file = r.absPath("file", false)
	_, hasFile := r.value("file")
	if q.hasRef == hasFile {
		r.fault("", "give exactly one of %s, file and --here", field)
	}
	return q
}

// foldering reads the folder a command looks under: its folder subject,
// the root when left out, or file, the folder a path on disk is in, found
// as where finds it. Both given is a fault.
func (r *input) foldering(field string) *query {
	q := &query{}
	_, hasFolder := r.value(field)
	q.file = r.absPath("file", false)
	_, hasFile := r.value("file")
	if hasFolder && hasFile {
		r.fault("", "give at most one of %s, file and --here", field)
	}
	q.hasRef = !hasFile
	if q.hasRef {
		q.ref = r.ref(field, core.WantFolder, "/")
	}
	return q
}

// The places a path on disk can be.
const (
	inTree = iota + 1
	inArchive
	inRoot
	outside
)

// place is where a path on disk lies, judged by real paths: in the tree,
// in the archive, elsewhere under the root, or outside it.
type place struct {
	kind int
	// printed is the path as given, or, when it reached the root by
	// another route, the same path under the root as written.
	printed string
	// parts are its names below tree/ or archived/, as the real path
	// gives them.
	parts []string
	// marker is the nearest marker outside the root, when there is one.
	marker *markers.Found
	// rootReal is the root's real path.
	rootReal string
}

// findPlace finds where a path on disk lies. Outside the root it looks
// for the nearest marker.
func findPlace(root store.Root, file string) (place, error) {
	rootReal, _, err := markers.Real(root.Path, "")
	if err != nil {
		return place{}, lookIO(err)
	}
	real, _, err := markers.Real(file, rootReal)
	if err != nil {
		return place{}, lookIO(err)
	}
	if !markers.Under(real, rootReal) {
		p := place{kind: outside, printed: file, rootReal: rootReal}
		f, ok, err := markers.Look(file)
		if err != nil {
			return place{}, lookIO(err)
		}
		if ok {
			p.marker = &f
		}
		return p, nil
	}
	rel, _ := filepath.Rel(rootReal, real)
	p := place{kind: inRoot, printed: file}
	if !markers.Under(file, root.Path) {
		p.printed = filepath.Join(root.Path, rel)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch parts[0] {
	case store.TreeName:
		p.kind = inTree
	case store.ArchiveName:
		p.kind = inArchive
	default:
		return p, nil
	}
	p.parts = parts[1:]
	return p, nil
}

// lookIO is the operating system refusing a look on disk.
func lookIO(err error) error {
	at := ""
	if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
		at = pe.Path
	}
	if at == "" {
		return store.Err(cli.IO("could not look on disk: " + err.Error()))
	}
	return store.Err(cli.IO("could not look in "+at+": "+err.Error(), at))
}

// locate finds what the query names, and the ID of the task or folder
// its file belongs to, 0 for the root. A path found through a marker
// whose path hint is stale also gives stale-marker.
func (q *query) locate(tx *store.Tx, t *tasks, field string) (Location, int64, []cli.Warning, error) {
	if q.hasRef {
		loc, err := t.locateRef(tx, q.ref, field)
		return loc, at(loc), nil, err
	}
	p, err := findPlace(t.root, q.file)
	if err != nil {
		return Location{}, 0, nil, err
	}
	switch p.kind {
	case inTree:
		loc, err := t.locateTree(tx, p)
		return loc, at(loc), nil, err
	case inArchive:
		return Location{}, 0, nil, archivedPath(tx, p)
	case inRoot:
		return Location{}, 0, nil, core.NotFound(nil, []string{p.printed},
			"%s is in %s but not in its tree, and names nothing koan keeps", p.printed, t.root.Path)
	}
	if p.marker == nil {
		return Location{}, 0, nil, core.NotFound(nil, []string{p.printed},
			"%s is outside the tree, and no marker is at or above it; bind one with koan bind", p.printed)
	}
	return t.locateMarker(tx, p)
}

// at is the ID a location's file belongs to: the task or folder it is,
// or its folder for material; 0 for the root.
func at(l Location) int64 {
	switch {
	case l.ID != nil:
		return *l.ID
	case l.Folder.ID != nil:
		return *l.Folder.ID
	}
	return 0
}

// locateRef is what a reference names.
func (t *tasks) locateRef(tx *store.Tx, ref core.Ref, field string) (Location, error) {
	res, err := resolve(tx, ref, core.WantAny, field)
	if err != nil {
		return Location{}, err
	}
	switch {
	case res.Kind == core.ResolvedTask:
		return t.taskLocation(res.ID, fsstep.Abs(t.root, t.notesRel(res.ID))), nil
	case res.HasMaterial:
		folder := res.ID
		dir := fsstep.FolderDir(t.paths[folder])
		return t.materialLocation(folder, res.Material, fsstep.Abs(t.root, dir+"/"+res.Material)), nil
	}
	return t.folderLocation(res.ID, fsstep.Abs(t.root, fsstep.FolderDir(t.paths[res.ID]))), nil
}

func (t *tasks) folderLocation(id int64, file string) Location {
	l := Location{Ref: "/", Kind: "root", Folder: FolderAt{Path: t.paths[id]}, File: &file}
	if id != core.RootID {
		l.Ref, l.Kind, l.ID, l.Folder.ID = core.Kept(id, t.paths[id], core.EndNone), "folder", &id, &id
	}
	return l
}

func (t *tasks) taskLocation(id int64, file string) Location {
	rec := t.record(id)
	return Location{Ref: rec.Ref, Kind: "task", ID: &id, Folder: rec.Folder, Gate: rec.Gate, File: &file}
}

func (t *tasks) materialLocation(folder int64, material, file string) Location {
	l := Location{Ref: core.KeptRootMaterial(material), Kind: "material", Folder: FolderAt{Path: t.paths[folder]},
		Material: &material, File: &file}
	if folder != core.RootID {
		l.Ref, l.ID, l.Folder.ID = core.KeptMaterial(folder, t.paths[folder], material), &folder, &folder
	}
	return l
}

// seqName is an @.tmp-<seq> or @.case-<seq>, a seq as koan writes one.
var seqName = regexp.MustCompile(`^@\.(?:tmp|case)-([1-9][0-9]*)$`)

// locateTree is what a path in the tree names: what koan keeps there,
// or material. Anything else is not-found; a directory spelled in
// another case than its folder's name is case-mismatch.
func (t *tasks) locateTree(tx *store.Tx, p place) (Location, error) {
	folder := int64(core.RootID)
	for i, part := range p.parts {
		if !strings.HasPrefix(part, "@") {
			return t.materialLocation(folder, strings.Join(p.parts[i:], "/"), p.printed), nil
		}
		name := part[1:]
		if id, ok := tx.Child(folder, name); ok {
			folder = id
			continue
		}
		if id, spelled, ok := tx.ChildFold(folder, name); ok {
			given := filepath.Join(append([]string{t.root.Tree}, p.parts[:i+1]...)...)
			is := fsstep.Abs(t.root, fsstep.FolderDir(joinPath(t.paths[folder], spelled)))
			return Location{}, core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{given, is},
				"%s matches %s only ignoring case", given, is)
		}
		if i == len(p.parts)-1 {
			if loc, ok := t.kept(tx, folder, part, p.printed); ok {
				return loc, nil
			}
		}
		return Location{}, core.NotFound(nil, []string{p.printed}, "%s is in the tree, but names nothing koan keeps there", p.printed)
	}
	return t.folderLocation(folder, p.printed), nil
}

// kept is what an entry koan keeps in a folder's directory, or the
// root's, names: its notes are the folder; an @<id>.md where that task's
// notes belong, or a gate's notes, is the task; a temporary name is the
// folder whose directory holds it.
func (t *tasks) kept(tx *store.Tx, folder int64, name, file string) (Location, bool) {
	switch {
	case name == fsstep.FolderNotes && folder != core.RootID, seqName.MatchString(name):
		return t.folderLocation(folder, file), true
	case name == fsstep.StartNotes || name == fsstep.DoneNotes:
		end := core.EndStart
		if name == fsstep.DoneNotes {
			end = core.EndDone
		}
		if folder == core.RootID {
			return Location{}, false
		}
		if gate, ok := tx.Gate(folder, end); ok {
			return t.taskLocation(gate, file), true
		}
	case strings.HasSuffix(name, ".md"):
		id, ok := core.ParseID(strings.TrimSuffix(name[1:], ".md"))
		if r, live := t.rows[id]; ok && live && r.Gate == core.EndNone && r.Folder == folder {
			return t.taskLocation(id, file), true
		}
	}
	return Location{}, false
}

func joinPath(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

// archivedName is a name in the archive that carries an ID: an archived
// folder's directory and its JSON, a task's notes and JSON, and a lone
// gate's.
var archivedName = regexp.MustCompile(`^@([1-9][0-9]{0,14})(?:\.md|\.json|_(?:start|done)\.(?:md|json)|_[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?(?:\.json)?)$`)

// archivedPath is the not-found for a path in the archive, with archived
// filled in for the nearest name on it, from the item upward, that
// carries an ID, from the index.
func archivedPath(tx *store.Tx, p place) error {
	for i := len(p.parts) - 1; i >= 0; i-- {
		m := archivedName.FindStringSubmatch(p.parts[i])
		if m == nil {
			continue
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		if !tx.Archived(id) {
			break
		}
		e := core.NotFound(nil, []string{p.printed}, "%s is in the archive: @%d was archived, to %s", p.printed, id, tx.ArchivedAt(id))
		e.Archived = []int64{id}
		return e
	}
	return core.NotFound(nil, []string{p.printed}, "%s is in the archive, and names nothing koan keeps", p.printed)
}

// locateMarker is the folder a path outside the tree is in, through the
// nearest marker. A marker that binds nothing, for whatever reason, is
// not-found, saying why, with the marker.json in paths and, when its ID
// names no live folder, that ID.
func (t *tasks) locateMarker(tx *store.Tx, p place) (Location, int64, []cli.Warning, error) {
	f := p.marker
	real, _, err := markers.Real(f.Dir, p.rootReal)
	if err != nil {
		return Location{}, 0, nil, lookIO(err)
	}
	m, why := markers.Read(f.File, real)
	if why != "" {
		return Location{}, 0, nil, core.NotFound(nil, []string{f.File}, "%s binds nothing: %s", f.File, why)
	}
	id, err := markerFolder(tx, m.ID)
	if err != nil {
		e := err.(*core.Error)
		e.Message = fmt.Sprintf("%s binds nothing: %s", f.File, e.Message)
		e.Paths = []string{f.File}
		return Location{}, 0, nil, e
	}
	loc := t.folderLocation(id, p.printed)
	loc.Marker = &f.File
	var warnings []cli.Warning
	if m.Path != t.paths[id] {
		warnings = append(warnings, cli.Warning{
			Kind:    "stale-marker",
			Message: fmt.Sprintf("%s was bound to @%d at %s, which is now at %s. Bind it again to refresh it.", f.File, id, m.Path, t.paths[id]),
			IDs:     []int64{id},
			Paths:   []string{f.File},
		})
	}
	return loc, id, warnings, nil
}

// markerFolder is the live folder a marker's ID names, or the not-found
// that says what the ID is instead: a task, archived, deleted, never
// issued or from an earlier root.
func markerFolder(tx *store.Tx, id int64) (int64, error) {
	res, err := core.Resolve(tx, core.Ref{Raw: fmt.Sprintf("@%d", id), Kind: core.TargetID, ID: id}, core.WantFolder, nil)
	if e, ok := err.(*core.Error); ok && e.Kind == core.KindInvalidInput {
		// A marker is no input: whatever it names that isn't a live
		// folder, it binds nothing.
		return 0, core.NotFound([]int64{id}, nil, "@%d is a task, not a folder", id)
	}
	if err != nil {
		return 0, err
	}
	return res.ID, nil
}
