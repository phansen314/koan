package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
)

// folderEnds resolves a folder join's subject to the folder and its
// gates, its start gate made first.
func folderEnds(w *edges, ref core.Ref, make bool) (id, start, done int64, err error) {
	if id, err = resolveFolder(w.tx, ref, "folder"); err != nil {
		return 0, 0, 0, err
	}
	start, done, err = w.gates(id, make)
	return id, start, done, err
}

// FolderJoin joins a folder to each folder given, through its gates: its
// start gate waits on that folder's start gate, and that folder's done
// gate waits on its done gate. Any gate named is made, its own first.
func FolderJoin() *cli.Command {
	return joinCommand("folder join", cli.Option{Field: "folder", Complete: foldersOnly.complete()}, core.WantFolder, folderEnds, true)
}

// FolderUnjoin removes a folder join's two edges for each folder given.
// It never makes a gate, the subject's included.
func FolderUnjoin() *cli.Command {
	return joinCommand("folder unjoin", cli.Option{Field: "folder", Complete: foldersOnly.complete()}, core.WantFolder, folderEnds, false)
}
