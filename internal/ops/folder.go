package ops

import (
	"encoding/json"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// Folder is a folder as every read returns it, in its schema's order. The
// root has a null ID, name and notes path, an empty extra, and no gates.
type Folder struct {
	ID        *int64          `json:"id"`
	Ref       string          `json:"ref"`
	Name      *string         `json:"name"`
	Path      string          `json:"path"`
	Title     *string         `json:"title"`
	Extra     json.RawMessage `json:"extra"`
	Gates     Gates           `json:"gates"`
	Open      int64           `json:"open"`
	Complete  int64           `json:"complete"`
	Finished  bool            `json:"finished"`
	NotesPath *string         `json:"notes_path"`
	Dir       string          `json:"dir"`
}

// Gates is a folder's gates, each null when the folder has none at that
// end.
type Gates struct {
	Start *Gate `json:"start"`
	Done  *Gate `json:"done"`
}

// Gate is one of a folder's gates as its folder's record gives it.
type Gate struct {
	ID        int64   `json:"id"`
	State     string  `json:"state"`
	Readiness *string `json:"readiness"`
}

// FolderAt is a folder named both ways; the root has a null ID.
type FolderAt = store.FolderAt

// folderFields is the folder record's field list, as --fields takes it.
var folderFields = FieldNames(Folder{})

// folders is every folder as one read transaction sees it, with what
// their records need.
type folders struct {
	tx    *store.Tx
	root  store.Root
	rows  map[int64]store.FolderRow
	paths map[int64]string
	// order is every folder in tree order.
	order []int64
	// counts are the tasks under each folder at any depth, gates
	// included, and under the root.
	counts map[int64]store.Counts
	gates  map[int64]map[core.End]store.GateRow
}

func loadFolders(tx *store.Tx, root store.Root) (*folders, error) {
	rows, err := tx.Folders()
	if err != nil {
		return nil, err
	}
	direct, err := tx.TaskCounts()
	if err != nil {
		return nil, err
	}
	gates, err := tx.Gates()
	if err != nil {
		return nil, err
	}
	f := &folders{tx: tx, root: root, rows: map[int64]store.FolderRow{}, paths: map[int64]string{core.RootID: "/"},
		counts: map[int64]store.Counts{}, gates: gates}
	var placed []core.Placed
	for _, r := range rows {
		f.rows[r.ID] = r
		placed = append(placed, core.Placed{ID: r.ID, Folder: true, Parent: r.Parent, Name: r.Name})
	}
	f.order = core.TreeOrder(placed)
	for _, id := range f.order {
		r := f.rows[id]
		parent := f.paths[r.Parent]
		if r.Parent != core.RootID {
			parent += "/"
		}
		f.paths[id] = parent + r.Name
	}
	// Each folder's tasks count toward it and every folder above it.
	for folder, c := range direct {
		for id := folder; ; id = f.rows[id].Parent {
			sum := f.counts[id]
			sum.Open += c.Open
			sum.Complete += c.Complete
			f.counts[id] = sum
			if id == core.RootID {
				break
			}
		}
	}
	return f, nil
}

// under is the folders under a folder, or the root, itself left out, in
// tree order: at any depth, or with direct only those directly
// in it.
func (f *folders) under(folder int64, direct bool) []int64 {
	var out []int64
	for _, id := range f.order {
		for p := f.rows[id].Parent; ; p = f.rows[p].Parent {
			if p == folder {
				out = append(out, id)
				break
			}
			if p == core.RootID || direct {
				break
			}
		}
	}
	return out
}

// record is a folder's record, or the root's.
func (f *folders) record(id int64) Folder {
	path := f.paths[id]
	c := f.counts[id]
	rec := Folder{
		Ref: "/", Path: path, Extra: json.RawMessage("{}"),
		Open: c.Open, Complete: c.Complete, Finished: c.Open == 0,
		Dir: fsstep.Abs(f.root, fsstep.FolderDir(path)),
	}
	if id == core.RootID {
		return rec
	}
	r := f.rows[id]
	notes := fsstep.Abs(f.root, fsstep.FolderNotesPath(path))
	rec.ID, rec.Ref, rec.Name, rec.Title, rec.Extra, rec.NotesPath = &r.ID, core.Kept(id, path, core.EndNone), &r.Name, r.Title, r.Extra, &notes
	rec.Gates.Start = f.gate(id, core.EndStart)
	rec.Gates.Done = f.gate(id, core.EndDone)
	return rec
}

func (f *folders) gate(folder int64, end core.End) *Gate {
	g, ok := f.gates[folder][end]
	if !ok {
		return nil
	}
	if g.Complete {
		return &Gate{ID: g.ID, State: "complete"}
	}
	readiness := "blocked"
	if core.Ready(f.tx, g.ID) {
		readiness = "ready"
	}
	return &Gate{ID: g.ID, State: "open", Readiness: &readiness}
}

// recordPaths is the paths on disk a cut of a folder's record gives, for
// pending-filesystem; the root's ID is 0.
func recordPaths(c Cut[Folder]) []fsstep.Path {
	var id int64
	if c.Record.ID != nil {
		id = *c.Record.ID
	}
	var out []fsstep.Path
	if c.Has("notes_path") && c.Record.NotesPath != nil {
		out = append(out, fsstep.Path{ID: id, Path: *c.Record.NotesPath})
	}
	if c.Has("dir") {
		out = append(out, fsstep.Path{ID: id, Path: c.Record.Dir})
	}
	return out
}
