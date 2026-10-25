package ops

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// offers is what an argument completes to (spec Completing references):
// pick's candidates under the root, chosen by pick's filters.
type offers struct {
	kinds   []string // what the argument takes: task, gate, folder
	states  []string // the tasks' and gates' states; both when nil
	claimed *bool
	// unmade offers gates not made yet too, where an edge names them.
	unmade bool
	// finished offers only finished folders.
	finished bool
	// steps offers folder paths alone, as steps toward a new path.
	steps bool
}

var (
	yes, no = true, false

	tasksAndGates   = offers{kinds: []string{"task", "gate"}}
	openOnes        = offers{kinds: []string{"task", "gate"}, states: []string{"open"}}
	completeOnes    = offers{kinds: []string{"task", "gate"}, states: []string{"complete"}}
	claimable       = offers{kinds: []string{"task", "gate"}, states: []string{"open"}, claimed: &no}
	claimedOnes     = offers{kinds: []string{"task", "gate"}, claimed: &yes}
	tasksOnly       = offers{kinds: []string{"task"}}
	blockable       = offers{kinds: []string{"task", "gate"}, unmade: true}
	edgeEnds        = offers{kinds: []string{"task", "gate", "folder"}, unmade: true}
	foldersOnly     = offers{kinds: []string{"folder"}}
	tasksAndFolders = offers{kinds: []string{"task", "folder"}}
	archivable      = offers{kinds: []string{"task", "gate", "folder"}, states: []string{"complete"}, finished: true}
	newPaths        = offers{steps: true}
	anyRef          = offers{kinds: []string{"task", "gate", "folder"}}
)

// idWord is a word completed as an ID: empty, @, or an optional @ then
// digits, then for a gate . and the start of an end.
var idWord = regexp.MustCompile(`^@?$|^@?[0-9]+(\.[a-z]*)?$`)

// complete is the argument's completer. A path offers paths, folder by
// folder, with no space after; an ID offers IDs, with descriptions;
// anything else, and any failure, offers nothing.
func (o offers) complete() cli.Completer {
	return func(env *cli.Env, word string) ([]cli.Candidate, bool) {
		path := strings.HasPrefix(word, "/") && !strings.Contains(word, ":")
		if !path && (o.steps || !idWord.MatchString(word)) {
			return nil, false
		}
		s, e := openStore(env)
		if e != nil {
			return nil, path
		}
		defer s.Close()
		var out []cli.Candidate
		e = s.Read(func(tx *store.Tx) error {
			t, err := loadTasks(tx, s.Root)
			if err != nil {
				return err
			}
			if path {
				out = o.paths(t, word)
			} else {
				out = o.ids(t, word)
			}
			return nil
		})
		if e != nil {
			return nil, path
		}
		return out, path
	}
}

// choices is what the argument takes, in pick's order.
func (o offers) choices(t *tasks) []choice {
	if len(o.kinds) == 0 {
		return nil
	}
	states := o.states
	if states == nil {
		states = []string{"open", "complete"}
	}
	p := &pickIn{recursive: true, states: states, readiness: []string{"ready", "blocked"}, claimed: o.claimed,
		kinds: o.kinds, made: !o.unmade}
	var out []choice
	for _, c := range choices(t, p, core.RootID) {
		if c.kind == "folder" && o.finished && t.folders.counts[c.id].Open != 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// gateOf is a gate choice's folder and end.
func (t *tasks) gateOf(c choice) (folder int64, end core.End) {
	if c.end != core.EndNone {
		return c.id, c.end
	}
	return t.rows[c.id].Folder, t.rows[c.id].Gate
}

// ids is what an ID word offers: the choices whose IDs start with the
// digits typed, a gate by its folder's ID and end, with the @ when the
// word has it or is empty.
func (o offers) ids(t *tasks, word string) []cli.Candidate {
	at := ""
	if word == "" || strings.HasPrefix(word, "@") {
		at = "@"
	}
	typed := strings.TrimPrefix(word, "@")
	var out []cli.Candidate
	for _, c := range o.choices(t) {
		var key, desc string
		switch c.kind {
		case "task":
			r := t.rows[c.id]
			key, desc = strconv.FormatInt(c.id, 10), r.Title+"  "+t.paths[r.Folder]
		case "gate":
			f, end := t.gateOf(c)
			key, desc = strconv.FormatInt(f, 10)+"."+string(end), gateTitle(end)+" of "+t.paths[f]
		case "folder":
			key, desc = strconv.FormatInt(c.id, 10), t.paths[c.id]
			if title := t.folders.rows[c.id].Title; title != nil {
				desc += "  " + *title
			}
		}
		if strings.HasPrefix(key, typed) {
			out = append(out, cli.Candidate{Text: at + key, Description: desc})
		}
	}
	return out
}

func gateTitle(end core.End) string {
	if end == core.EndDone {
		return "Done"
	}
	return "Start"
}

// paths is what a path word offers: the child folders, whose names start
// with what follows the last /, of the folder named exactly by what
// precedes it, as steps; and after a whole folder path, or one then .
// and the start of an end, the folder's gates the argument takes.
func (o offers) paths(t *tasks, word string) []cli.Candidate {
	if !o.steps && !slices.Contains(o.kinds, "folder") && !slices.Contains(o.kinds, "gate") {
		return nil
	}
	cut := strings.LastIndex(word, "/")
	parent, ok := int64(core.RootID), true
	if dir := word[:cut]; dir != "" {
		parent, ok = t.folderAt(dir)
	}
	if !ok {
		return nil
	}
	name, end, dotted := strings.Cut(word[cut+1:], ".")
	gates := map[int64][]core.End{}
	for _, c := range o.choices(t) {
		if c.kind == "gate" {
			f, e := t.gateOf(c)
			gates[f] = append(gates[f], e)
		}
	}
	var out []cli.Candidate
	for _, f := range t.folders.order {
		r := t.folders.rows[f]
		if r.Parent != parent || !strings.HasPrefix(r.Name, name) || dotted && r.Name != name {
			continue
		}
		if !dotted {
			out = append(out, cli.Candidate{Text: t.paths[f]})
		}
		if r.Name != name {
			continue
		}
		for _, e := range gates[f] {
			if strings.HasPrefix(string(e), end) {
				out = append(out, cli.Candidate{Text: t.paths[f] + "." + string(e)})
			}
		}
	}
	return out
}

// folderAt is the folder whose path is exactly path, byte for byte.
func (t *tasks) folderAt(path string) (int64, bool) {
	for id, p := range t.folders.paths {
		if p == path && id != core.RootID {
			return id, true
		}
	}
	return 0, false
}
