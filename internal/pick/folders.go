package pick

import (
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// moveAction is m (pick-spec.md, Actions): a choose list, move to> , of
// every folder, one chosen; each target is moved there, one move call
// each.
var moveAction = action{key: "m", arity: anyTargets, run: openMove, choose: moveTo}

// folderAction is f: a choose list, folder> , of every folder, one chosen;
// it becomes the scope folder, and the list reloads.
var folderAction = action{key: "f", arity: noTargets, run: openFolder, choose: setFolder}

// folderLines are the choose list of every folder, in tree order, / first,
// read fresh: each line's key is the folder's path, which is all it shows,
// as the second field shown, after an empty one: fzf matches only the
// second and third shown fields (lineNth), a task line's tags and title.
func folderLines(r *actionRun, what string) []string {
	l, failed := load(r.env.Ops, true)
	if failed != nil {
		r.status = "✗ " + what + ": " + errText(failed.Error)
		return nil
	}
	return folderLinesOf(l.Folders)
}

// folderLinesOf are folders' lines, as folderLines makes them.
func folderLinesOf(folders []model.FolderPath) []string {
	lines := make([]string, len(folders))
	for i, f := range folders {
		lines[i] = string(f) + "\t\t" + string(f)
	}
	return lines
}

// foldersIn are the load's folders in s, as list has them (pick-spec.md,
// Folder picker): the scope folder, then every folder under it, or, not
// recursive, its immediate subfolders; in tree order.
func (l *Load) foldersIn(s Scope) []model.FolderPath {
	var out []model.FolderPath
	for _, f := range l.Folders {
		switch {
		case f == s.Folder:
		case s.Recursive && inFolder(f, s.Folder, true):
		case !s.Recursive && inFolder(f, s.Folder, true) && len(f.Segments()) == len(s.Folder.Segments())+1:
		default:
			continue
		}
		out = append(out, f)
	}
	return out
}

func openMove(r *actionRun, targets []shownLine) {
	if lines := folderLines(r, "m"); lines != nil {
		r.openChoose("m", "move to> ", lines, true, targets)
	}
}

func moveTo(r *actionRun, chosen []string, targets []shownLine) {
	to := chosen[0]
	for _, t := range targets {
		in := &jsonio.Object{}
		in.Set("id", idNumber(t.ID))
		in.Set("to", to)
		r.call(t.ID, "move", in, "moved", "move "+string(idNumber(t.ID))+" → "+to)
	}
}

func openFolder(r *actionRun, _ []shownLine) {
	if lines := folderLines(r, "f"); lines != nil {
		r.openChoose("f", "folder> ", lines, true, nil)
	}
}

// setFolder makes the chosen folder the scope folder, once the reload going
// back to the task list shows it, from its first line. No operation runs.
func setFolder(r *actionRun, chosen []string, _ []shownLine) {
	var scope Scope
	if r.err = readJSON(r.s, scopeFile, &scope); r.err != nil {
		return
	}
	scope.Folder = model.FolderPath(chosen[0])
	r.nextScope, r.ifReloaded = &scope, "✓ folder: "+chosen[0]
}
