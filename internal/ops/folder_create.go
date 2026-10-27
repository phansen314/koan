package ops

import (
	"encoding/json"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// FolderCreateResult is folder create's result, in its schema's order.
type FolderCreateResult struct {
	ID          int64      `json:"id"`
	Ref         string     `json:"ref"`
	NotesPath   string     `json:"notes_path"`
	Dir         string     `json:"dir"`
	FoldersMade []FolderAt `json:"folders_made"`
	GatesMade   []GateMade `json:"gates_made"`
	Reblocked   []int64    `json:"reblocked"`
}

// Derived is what the change made ready and made wait, as the log records
// them.
func (r FolderCreateResult) Derived() (released, reblocked []int64) { return nil, r.Reblocked }

// FolderCreate makes a folder at its path, with its directory and notes,
// and any missing parents the same way, with no title. Joining folders,
// it makes its own gates, then those of each folder it joins.
func FolderCreate() *cli.Command {
	return &cli.Command{
		Name:    "folder create",
		Subject: &cli.Option{Field: "path", Required: true, Complete: newPaths.complete()},
		Options: []cli.Option{
			{Field: "title", Kind: cli.String, Nullable: true},
			{Field: "join", Kind: cli.List, Complete: foldersOnly.complete()},
			{Field: "extra", Kind: cli.JSON},
			{Field: "notes", Kind: cli.String},
		},
		Files: []cli.File{{Name: "--notes-file", Field: "notes"}},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			ref := r.path("path")
			title := r.title("title")
			joins := r.joinRefs("join")
			extra := r.object("extra")
			notes := r.text("notes", "")
			refs, at := []core.Ref{ref}, []string{"/path"}
			for _, j := range joins {
				refs, at = append(refs, j.ref), append(at, j.at)
			}
			r.sameCase(refs, at)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			return s.Write(func(tx *store.Tx) (any, []cli.Warning, error) {
				created, err := makeFolders(tx, s.Root, ref, "/path", title, extra, notes)
				if err != nil {
					return nil, nil, err
				}
				last := created[len(created)-1]
				// Its joins resolve after the folders are made, so they
				// can name a missing parent it made.
				w := newEdges(tx)
				reblocked, warnings := []int64{}, []cli.Warning(nil)
				if len(joins) > 0 {
					start, done, err := w.gates(*last.ID, true)
					if err != nil {
						return nil, nil, err
					}
					es, err := w.join(start, done, joins, true)
					if err != nil {
						return nil, nil, err
					}
					if _, reblocked, warnings, err = w.add(es); err != nil {
						return nil, nil, err
					}
				}
				return FolderCreateResult{
					ID: *last.ID, Ref: core.Kept(*last.ID, last.Path, core.EndNone),
					Dir:         fsstep.Abs(s.Root, fsstep.FolderDir(last.Path)),
					NotesPath:   fsstep.Abs(s.Root, fsstep.FolderNotesPath(last.Path)),
					FoldersMade: created,
					GatesMade:   w.made, Reblocked: reblocked,
				}, warnings, nil
			})
		},
	}
}

// makeFolders makes the folder at a path, with its directory and notes,
// and any missing parents the same way, with no title and empty notes. It
// gives every folder made, parents first, this one last. pointer is the
// field that gives the path, where a refusal points.
func makeFolders(tx *store.Tx, root store.Root, ref core.Ref, pointer string, title *string, extra json.RawMessage, notes string) ([]FolderAt, error) {
	parent, missing, err := walkToMake(tx, ref, pointer)
	if err != nil {
		return nil, err
	}
	// Every folder made is a destination, and is taken by an entry on
	// disk where its directory would go.
	path := strings.TrimSuffix(tx.Path(parent), "/")
	var paths []string
	for _, name := range missing {
		above := fsstep.Abs(root, fsstep.FolderDir(path))
		path += "/" + name
		paths = append(paths, path)
		entry, taken, err := fsstep.Taken(root, path)
		if err != nil {
			return nil, store.Err(cli.IO("could not list "+above+": "+err.Error(), above))
		}
		if taken {
			at := fsstep.Abs(root, entry)
			return nil, core.ExistsAt(pointer, 0, at, "%s is taken on disk by %s", path, at)
		}
	}
	created := []FolderAt{}
	for i, name := range missing {
		// A missing parent is made because the folder given needs it.
		id, err := tx.Issue(core.NodeFolder, i == len(missing)-1)
		if err != nil {
			return nil, err
		}
		row := store.FolderRow{ID: id, Parent: parent, Name: name, Extra: []byte("{}")}
		step := fsstep.Step{Op: fsstep.OpMkdir, To: fsstep.FolderDir(paths[i])}
		if i == len(missing)-1 {
			row.Title, row.Extra, step.Content = title, extra, notes
		}
		if err := tx.InsertFolder(row); err != nil {
			return nil, err
		}
		if _, err := fsstep.Add(tx, step); err != nil {
			return nil, err
		}
		created = append(created, FolderAt{ID: &id, Path: paths[i]})
		parent = id
	}
	return created, nil
}

// walkToMake follows a path to make from the root, one exact name at a
// time, and gives the last folder there and the names still to make. A
// part that matches a folder only ignoring case is case-mismatch; the
// whole path there already is exists.
func walkToMake(tx *store.Tx, ref core.Ref, pointer string) (int64, []string, error) {
	at, given := core.RootID, ""
	for i, name := range ref.Names {
		given += "/" + name
		if id, ok := tx.Child(at, name); ok {
			if i == len(ref.Names)-1 {
				return 0, nil, core.ExistsAt(pointer, id, given, "%s is taken by @%d", given, id)
			}
			at = id
			continue
		}
		if id, spelled, ok := tx.ChildFold(at, name); ok {
			is := strings.TrimSuffix(tx.Path(at), "/") + "/" + spelled
			return 0, nil, core.Conflict(core.RuleCaseMismatch, []int64{id}, []string{given, is},
				"%s differs from %s only in case", given, is)
		}
		return at, ref.Names[i:], nil
	}
	return at, nil, nil
}
