package ops

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// Pick lets a person at a terminal choose tasks, gates and folders in
// fzf, and prints their references, comma-joined. It is for people: it
// prints plain text, never an envelope.
func Pick() *cli.Command {
	return &cli.Command{
		Name:  "pick",
		Plain: true,
		Options: []cli.Option{
			{Field: "folder", Kind: cli.String, Complete: foldersOnly.complete()},
			{Field: "recursive", Kind: cli.Bool},
			{Field: "state", Kind: cli.List},
			{Field: "readiness", Kind: cli.List},
			{Field: "claimed", Kind: cli.Bool, Nullable: true},
			{Field: "tags_any", Kind: cli.List},
			{Field: "tags_all", Kind: cli.List},
			{Field: "frontier", Kind: cli.Bool},
			{Field: "kinds", Kind: cli.List},
			{Field: "query", Kind: cli.String},
			{Field: "bash", Kind: cli.Bool},
			{Field: "zsh", Kind: cli.Bool},
		},
		Here:   &cli.Here{Field: "file", Flag: "--here"},
		Fields: []string{"file"},
		Run:    runPick,
	}
}

// pickIn is pick's input, read.
type pickIn struct {
	folder    core.Ref
	file      string
	hasFile   bool
	recursive bool
	states    []string
	readiness []string
	claimed   *bool
	tagsAny   []string
	tagsAll   []string
	frontier  bool
	kinds     []string
	query     string
	// made leaves out gates not made yet, which only an edge can name
	// (rules Resolution); completion sets it, pick never does.
	made bool
}

// filtered is whether a task filter is set (spec pick, The candidates):
// with one, a folder is a candidate only when a task under it passes,
// and a gate not made yet never is.
func (p *pickIn) filtered() bool {
	return len(p.readiness) == 1 || p.claimed != nil || len(p.tagsAny) > 0 || len(p.tagsAll) > 0 || p.frontier
}

func (p *pickIn) wants(kind string) bool { return slices.Contains(p.kinds, kind) }

// readPick reads pick's input, and the keybinding script to print
// instead of picking, if --bash or --zsh asked for one.
func readPick(in cli.Input) (*pickIn, string, *cli.Error) {
	r := input{in: in}
	script := ""
	for _, sh := range []struct{ field, script string }{{"bash", bashScript}, {"zsh", zshScript}} {
		if r.boolean(sh.field, false) {
			if len(in) > 1 {
				r.fault("", "%s takes no other field", sh.field)
				break
			}
			script = sh.script
		}
	}
	p := &pickIn{
		folder:    r.ref("folder", core.WantFolder, "/"),
		file:      r.absPath("file", false),
		recursive: r.boolean("recursive", true),
		states:    r.choices("state", []string{"open", "complete"}, []string{"open"}),
		readiness: r.choices("readiness", []string{"ready", "blocked"}, []string{"ready", "blocked"}),
		claimed:   r.nullBool("claimed"),
		tagsAny:   r.tags("tags_any"),
		tagsAll:   r.tags("tags_all"),
		frontier:  r.boolean("frontier", false),
		kinds:     r.choices("kinds", []string{"task", "gate", "folder"}, []string{"task", "gate", "folder"}),
		query:     r.text("query", ""),
	}
	_, p.hasFile = r.value("file")
	_, hasFolder := r.value("folder")
	if p.hasFile && hasFolder {
		r.fault("", "give at most one of folder and file")
	}
	if p.frontier {
		for _, f := range []string{"state", "readiness", "claimed"} {
			if _, ok := r.value(f); ok {
				r.fault("", "frontier can't go with %s: the frontier is open, ready and unclaimed tasks", f)
				break
			}
		}
	}
	return p, script, r.err()
}

func runPick(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
	p, script, e := readPick(in)
	if e != nil {
		return nil, nil, e
	}
	if script != "" {
		return cli.Text{Out: script}, nil, nil
	}
	s, e := openStore(env)
	if e != nil {
		return nil, nil, e
	}
	defer s.Close()
	if e := openTTY(); e != nil {
		return nil, nil, e
	}
	fzf, e := findFzf()
	if e != nil {
		return nil, nil, e
	}
	var cands []candidate
	var stale []cli.Warning
	warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
		t, err := loadTasks(tx, s.Root)
		if err != nil {
			return nil, err
		}
		folder := int64(core.RootID)
		if p.hasFile {
			var loc Location
			if loc, _, stale, err = (&query{file: p.file}).locate(tx, t, "file"); err != nil {
				return nil, err
			}
			if loc.Folder.ID != nil {
				folder = *loc.Folder.ID
			}
		} else if folder, err = resolveFolder(tx, p.folder, "folder"); err != nil {
			return nil, err
		}
		cands = candidates(t, p, folder)
		var paths []fsstep.Path
		for _, c := range cands {
			if c.notes != "" {
				paths = append(paths, fsstep.Path{ID: c.id, Path: c.notes})
			}
		}
		return paths, nil
	})
	if e != nil {
		return nil, nil, e
	}
	warnings = append(warnings, stale...)
	if len(cands) == 0 {
		return cli.Text{Exit: 1}, warnings, nil
	}
	picked, exit, e := runFzf(env, fzf, cands, p.query)
	if e != nil {
		return nil, warnings, e
	}
	if exit != 0 {
		return cli.Text{Exit: exit}, warnings, nil
	}
	return cli.Text{Out: strings.Join(picked, ",") + "\n"}, warnings, nil
}

// candidate is one line pick offers.
type candidate struct {
	key    string // the reference it prints
	kind   string // task, gate or folder
	id     int64  // its task or folder; 0 for a gate not made yet
	notes  string // its notes file, "" for a gate not made yet
	folder string // the folder's path: a task's or gate's, a folder's own
	title  string
	state  string
	more   []string // priority, tags, claimed
	header string   // the preview's header
}

// choice is one thing pick offers, before it is drawn: a task, gate or
// folder by its ID, or a gate not made yet by its folder's ID and end.
type choice struct {
	kind string // task, gate or folder
	id   int64
	end  core.End // a gate not made yet's end; EndNone otherwise
}

// candidates is what pick offers under folder, in order, drawn for fzf.
func candidates(t *tasks, p *pickIn, folder int64) []candidate {
	var out []candidate
	for _, c := range choices(t, p, folder) {
		switch {
		case c.kind == "folder":
			out = append(out, t.folderCandidate(c.id))
		case c.end != core.EndNone:
			out = append(out, t.unmadeGate(c.id, c.end))
		default:
			out = append(out, t.taskCandidate(c.id))
		}
	}
	return out
}

// choices is what pick offers under folder, in order (spec pick, The
// candidates).
func choices(t *tasks, p *pickIn, folder int64) []choice {
	ownTasks := map[int64][]int64{}
	for _, id := range t.order {
		ownTasks[t.rows[id].Folder] = append(ownTasks[t.rows[id].Folder], id)
	}
	children := map[int64][]int64{}
	for _, id := range t.folders.order {
		children[t.folders.rows[id].Parent] = append(children[t.folders.rows[id].Parent], id)
	}

	// matched is every task, gates included, that passes the filter
	// within the scope, whatever kinds are asked for.
	var matched []int64
	if p.frontier {
		for _, r := range t.frontier(scope{folder: folder, recursive: p.recursive, tagsAny: p.tagsAny, tagsAll: p.tagsAll}) {
			matched = append(matched, r.ID)
		}
	} else {
		for _, id := range t.order {
			if t.in(id, folder, p.recursive) && t.passes(id, p) {
				matched = append(matched, id)
			}
		}
	}
	isMatched := map[int64]bool{}
	for _, id := range matched {
		isMatched[id] = true
	}
	holds := func(f int64) bool {
		if !p.filtered() {
			return true
		}
		return slices.ContainsFunc(matched, func(id int64) bool { return t.in(id, f, true) })
	}

	var out []choice
	addTask := func(id int64) {
		gate := t.rows[id].Gate != core.EndNone
		if gate && p.wants("gate") {
			out = append(out, choice{kind: "gate", id: id})
		} else if !gate && p.wants("task") {
			out = append(out, choice{kind: "task", id: id})
		}
	}
	addFolder := func(f int64) {
		if p.wants("folder") && holds(f) {
			out = append(out, choice{kind: "folder", id: f})
		}
	}
	var walk func(f int64, top bool)
	walk = func(f int64, top bool) {
		if f != core.RootID {
			addFolder(f)
			if p.wants("gate") && !p.filtered() && !p.made {
				for _, end := range []core.End{core.EndStart, core.EndDone} {
					if _, made := t.folders.gates[f][end]; !made {
						out = append(out, choice{kind: "gate", id: f, end: end})
					}
				}
			}
		}
		if !p.frontier {
			for _, id := range ownTasks[f] {
				if isMatched[id] {
					addTask(id)
				}
			}
		}
		for _, c := range children[f] {
			if top && !p.recursive {
				addFolder(c)
			} else {
				walk(c, false)
			}
		}
	}
	if p.frontier {
		for _, id := range matched {
			addTask(id)
		}
	}
	walk(folder, true)
	return out
}

// passes is whether a task passes task list's filter, but for its scope.
func (t *tasks) passes(id int64, p *pickIn) bool {
	if !slices.Contains(p.states, t.state(id)) {
		return false
	}
	if ready := t.readiness(id); ready != nil && !slices.Contains(p.readiness, *ready) {
		return false
	}
	has := t.tags[id]
	if len(p.tagsAny) > 0 && !slices.ContainsFunc(p.tagsAny, func(tag string) bool { return slices.Contains(has, tag) }) {
		return false
	}
	if slices.ContainsFunc(p.tagsAll, func(tag string) bool { return !slices.Contains(has, tag) }) {
		return false
	}
	_, isClaimed := t.claims[id]
	return p.claimed == nil || *p.claimed == isClaimed
}

// none is a preview value that isn't set.
const none = "—"

func (t *tasks) taskCandidate(id int64) candidate {
	rec := t.record(id)
	c := candidate{kind: "task", key: "@" + strconv.FormatInt(id, 10), id: id, notes: rec.NotesPath,
		folder: rec.Folder.Path, title: rec.Title, state: rec.State}
	if rec.Gate != nil {
		c.kind = "gate"
		c.key = fmt.Sprintf("@%d.%s", *rec.Folder.ID, *rec.Gate)
	}
	if rec.Readiness != nil {
		c.state = *rec.Readiness
	}
	if rec.Priority != nil {
		c.more = append(c.more, "p"+strconv.FormatInt(*rec.Priority, 10))
	}
	for _, tag := range rec.Tags {
		c.more = append(c.more, "#"+tag)
	}
	if rec.Claim != nil {
		c.more = append(c.more, "claimed")
	}

	priority, readiness, tags, claim := none, none, none, none
	if rec.Priority != nil {
		priority = strconv.FormatInt(*rec.Priority, 10)
	}
	if rec.Readiness != nil {
		readiness = *rec.Readiness
	}
	if len(rec.Tags) > 0 {
		tags = strings.Join(rec.Tags, ", ")
	}
	if rec.Claim != nil {
		claim = "since " + rec.Claim.ClaimedAt + " " + escapeC1(string(rec.Claim.Extra))
	}
	rows := [][2]string{
		{"title", rec.Title},
		{"folder", rec.Folder.Path},
		{"state", rec.State},
		{"readiness", readiness},
		{"priority", priority},
		{"tags", tags},
	}
	rows = append(rows, edgeRows("waits on", rec.BlockedBy)...)
	rows = append(rows, edgeRows("blocks", rec.Blocks)...)
	rows = append(rows, [2]string{"claim", claim})
	c.header = previewHeader(rec.Ref, rows)
	return c
}

// edgeRows are a preview's rows for one side of a task's edges: the
// label on the first, each other end in kept text with its state.
func edgeRows(label string, ends []EdgeEnd) [][2]string {
	if len(ends) == 0 {
		return [][2]string{{label, none}}
	}
	var rows [][2]string
	for i, end := range ends {
		l := ""
		if i == 0 {
			l = label
		}
		rows = append(rows, [2]string{l, end.Ref + " " + end.State})
	}
	return rows
}

func (t *tasks) folderCandidate(f int64) candidate {
	rec := t.folders.record(f)
	c := candidate{kind: "folder", key: "@" + strconv.FormatInt(f, 10), id: f, notes: *rec.NotesPath,
		folder: rec.Path, title: *rec.Name, state: strconv.FormatInt(rec.Open, 10) + " open"}
	title := none
	if rec.Title != nil {
		c.title, title = *rec.Title, *rec.Title
	}
	gate := func(g *Gate) string {
		switch {
		case g == nil:
			return "not made"
		case g.Readiness != nil:
			return fmt.Sprintf("@%d %s %s", g.ID, g.State, *g.Readiness)
		}
		return fmt.Sprintf("@%d %s", g.ID, g.State)
	}
	c.header = previewHeader(rec.Ref, [][2]string{
		{"title", title},
		{"open", strconv.FormatInt(rec.Open, 10)},
		{"complete", strconv.FormatInt(rec.Complete, 10)},
		{"finished", strconv.FormatBool(rec.Finished)},
		{"start gate", gate(rec.Gates.Start)},
		{"done gate", gate(rec.Gates.Done)},
	})
	return c
}

// unmadeGate is a gate a folder doesn't have yet, which the first edge
// naming it makes.
func (t *tasks) unmadeGate(f int64, end core.End) candidate {
	path := t.folders.paths[f]
	title := "Start"
	if end == core.EndDone {
		title = "Done"
	}
	key := fmt.Sprintf("@%d.%s", f, end)
	return candidate{kind: "gate", key: key, folder: path, title: title, state: "not made",
		header: previewHeader(fmt.Sprintf("%s (%s.%s)", key, path, end), [][2]string{
			{"state", "not made yet: the first edge naming it makes it"},
		})}
}

// previewHeader is a preview's header: the candidate named as kept text,
// then a field to a line, as "label:" padded to 12 columns and the value,
// then an empty line before the notes.
func previewHeader(name string, rows [][2]string) string {
	var b strings.Builder
	b.WriteString(name + "\n")
	for _, r := range rows {
		label := ""
		if r[0] != "" {
			label = r[0] + ":"
		}
		fmt.Fprintf(&b, "%-12s %s\n", label, r[1])
	}
	b.WriteString("\n")
	return b.String()
}

// escapeC1 replaces DEL and U+0080–U+009F, which JSON leaves raw, with
// their \u00XX escapes: they occur only inside strings, and some
// terminals act on U+009B.
func escapeC1(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == 0x7f || r >= 0x80 && r <= 0x9f {
			fmt.Fprintf(&b, "\\u%04x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// lines are fzf's lines for the candidates, without newlines (spec pick,
// Lines): the key, a tab, the notes path, a tab, then the columns, two
// spaces apart, each but the last padded to the widest in terminal cells.
func lines(cands []candidate) []string {
	rows := make([][]string, len(cands))
	var widths [5]int
	for i, c := range cands {
		rows[i] = []string{c.kind, c.key, c.folder, c.title, c.state, strings.Join(c.more, " ")}
		for col := range widths {
			widths[col] = max(widths[col], cells(rows[i][col]))
		}
	}
	out := make([]string, len(cands))
	for i, c := range cands {
		var b strings.Builder
		for col, s := range rows[i] {
			if col > 0 {
				b.WriteString("  ")
			}
			b.WriteString(s)
			if col < len(widths) {
				b.WriteString(strings.Repeat(" ", widths[col]-cells(s)))
			}
		}
		out[i] = c.key + "\t" + c.notes + "\t" + strings.TrimRight(b.String(), " ")
	}
	return out
}

// cells is how many terminal cells s takes: two for a wide or fullwidth
// rune, none for a combining mark or a zero-width format character.
func cells(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		case width.LookupRune(r).Kind() == width.EastAsianWide, width.LookupRune(r).Kind() == width.EastAsianFullwidth:
			n += 2
		default:
			n++
		}
	}
	return n
}
