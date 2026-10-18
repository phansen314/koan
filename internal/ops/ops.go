// Package ops is koan's commands: each takes one JSON object, does its work,
// and returns a result or an error (spec/operations.md).
package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// Envelope is the one object every command returns.
type Envelope struct {
	OK       bool
	Result   any
	Error    *errs.Error
	Warnings []errs.Warning
}

// MarshalJSON writes the envelope in its success or failure shape, leaving
// <, >, and & as they are.
func (e Envelope) MarshalJSON() ([]byte, error) {
	w := e.Warnings
	if w == nil {
		w = []errs.Warning{}
	}
	var shape any = struct {
		OK       bool           `json:"ok"`
		Error    *errs.Error    `json:"error"`
		Warnings []errs.Warning `json:"warnings"`
	}{false, e.Error, w}
	if e.OK {
		shape = struct {
			OK       bool           `json:"ok"`
			Result   any            `json:"result"`
			Warnings []errs.Warning `json:"warnings"`
		}{true, e.Result, w}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(shape); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// Failure is the envelope of a command that failed before it ran.
func Failure(e *errs.Error) Envelope { return Envelope{Error: e} }

// command is one command: it reads its input from c.in, calls c.begin, and
// does its work.
type command func(c *call) (any, *errs.Error)

var commands = map[string]command{
	"init": cmdInit, "version": cmdVersion, "info": cmdInfo,
	"doctor": cmdDoctor, "repair": cmdRepair, "migrate": cmdMigrate,
	"create-folder": cmdCreateFolder, "move-folder": cmdMoveFolder, "delete-folder": cmdDeleteFolder,
	"show-folder": cmdShowFolder, "update-folder": cmdUpdateFolder,
	"create": cmdCreate, "create-batch": cmdCreateBatch, "show": cmdShow, "update": cmdUpdate,
	"done": cmdDone, "reopen": cmdReopen, "block": cmdBlock, "unblock": cmdUnblock,
	"move": cmdMove, "delete": cmdDelete,
	"frontier": cmdFrontier, "list": cmdList, "why": cmdWhy, "where": cmdWhere,
	"claim": cmdClaim, "claim-next": cmdClaimNext, "release": cmdRelease,
}

// Names lists every command, sorted.
func Names() []string {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Run runs the command named name on the input raw, one JSON object; empty
// input is the empty object.
func Run(env store.Env, name string, raw []byte) Envelope {
	cmd, ok := commands[name]
	if !ok {
		return Failure(errs.New(errs.Usage, "unknown command %q", name))
	}
	in, e := parseInput(raw)
	if e != nil {
		return Failure(e)
	}
	c := &call{env: env, in: in, now: model.Stamp(env.Now()), warned: map[string]bool{}}
	result, e := cmd(c)
	if c.t != nil {
		if e != nil {
			e.WithChanged(c.t.Changed()...)
		}
		c.t.Close()
	}
	if e != nil {
		return Envelope{Error: e, Warnings: c.warnings}
	}
	return Envelope{OK: true, Result: result, Warnings: c.warnings}
}

// call is one run of a command.
type call struct {
	env      store.Env
	in       *input
	now      string // the one time every timestamp of this command is
	t        *store.Tree
	x        *store.Index
	byID     map[model.ID][]model.Folder // the folders with each ID, once read
	warnings []errs.Warning
	warned   map[string]bool
}

// begin ends input checking and opens the tree: input errors come first,
// then the root's state, then busy.
func (c *call) begin(write bool) *errs.Error { return c.beginNeed(store.NeedUsable, write) }

func (c *call) beginNeed(need store.Need, write bool) *errs.Error {
	if e := c.checked(); e != nil {
		return e
	}
	t, e := store.Open(c.env, need, write)
	if e != nil {
		return e
	}
	c.t = t
	return nil
}

// checked ends input checking for a command that uses the root: the input's
// problems come first, then HOME, which says where the root is.
func (c *call) checked() *errs.Error {
	if e := c.in.done(); e != nil {
		return e
	}
	if !path.IsAbs(c.env.Home) {
		return errs.New(errs.IO, "HOME is not set to an absolute path")
	}
	return nil
}

// index walks the tree, once.
func (c *call) index() (*store.Index, *errs.Error) {
	if c.x != nil {
		return c.x, nil
	}
	x, e := c.t.Walk(false)
	if e != nil {
		return nil, e
	}
	c.x = x
	return x, nil
}

// warn reports a problem that does not stop the command, at most once per
// kind and file.
func (c *call) warn(kind, msg string, ids []model.ID, paths ...string) {
	key := kind + "\x00" + strings.Join(paths, "\x00")
	if c.warned[key] {
		return
	}
	c.warned[key] = true
	if paths == nil {
		paths = []string{}
	}
	c.warnings = append(c.warnings, errs.Warning{Kind: kind, Message: msg, IDs: ids, Paths: paths})
}

// find returns the task a command was asked for: not-found when it does not
// exist, and the file's own error when it can't be used.
func (c *call) find(id model.ID) (model.Folder, *model.TaskFile, *errs.Error) {
	x, e := c.index()
	if e != nil {
		return "", nil, e
	}
	f, ok := x.Tasks[id]
	if !ok {
		return "", nil, c.notFoundTasks(id)
	}
	l := c.t.Load(f, id)
	if l.Err != nil {
		return "", nil, l.Err
	}
	return f, l.File, nil
}

// notFoundTasks is the error for IDs that name no task: it says which name
// a folder instead, which it reads every folder file to tell.
func (c *call) notFoundTasks(ids ...model.ID) *errs.Error {
	e := notFoundTask(ids...)
	var folders []string
	eachFolderFile(c.t, c.x, func(f model.Folder, ff *model.FolderFile, _ *errs.Error) {
		if ff != nil && slices.Contains(ids, ff.ID) {
			folders = append(folders, fmt.Sprintf("%d is the ID of folder %s", ff.ID, f))
		}
	})
	if len(folders) > 0 {
		e.Message += ": " + strings.Join(folders, ", ")
	}
	return e
}

func notFoundTask(ids ...model.ID) *errs.Error {
	if len(ids) == 1 {
		return errs.New(errs.NotFound, "task %d does not exist", ids[0]).WithIDs(ids...)
	}
	return errs.New(errs.NotFound, "tasks %v do not exist", ids).WithIDs(ids...)
}

// pass loads a task a command only passes: an unusable file is a warning,
// and nil is returned for it and for one that vanished.
func (c *call) pass(id model.ID) *model.TaskFile {
	f, ok := c.x.Tasks[id]
	if !ok {
		return nil
	}
	l := c.t.Load(f, id)
	if l.Err != nil {
		if !l.Gone {
			c.warn(errs.WarnUnusableFile, l.Err.Message, []model.ID{id}, c.t.Abs(store.TaskRel(f, id)))
		}
		return nil
	}
	return l.File
}

// look is the graph's view of a task. It needs the index. Each blocker of
// the task that does not exist is reported as a dangling reference.
func (c *call) look(id model.ID) (graph.Node, bool) {
	tf := c.pass(id)
	if tf == nil {
		return graph.Node{}, false
	}
	var gone []model.ID
	for _, b := range tf.BlockedBy {
		if _, ok := c.x.Tasks[b]; !ok {
			gone = append(gone, b)
		}
	}
	if len(gone) > 0 {
		c.warn(errs.WarnDanglingReference,
			fmt.Sprintf("task %d is blocked by %v, which do not exist", id, gone),
			append([]model.ID{id}, gone...), c.t.Abs(store.TaskRel(c.x.Tasks[id], id)))
	}
	return graph.Node{Open: tf.Open(), Priority: tf.Priority, BlockedBy: tf.BlockedBy}, true
}

// task builds the shape every command returns a task in. It needs the index.
func (c *call) task(f model.Folder, tf *model.TaskFile) model.Task {
	node, _ := c.look(tf.ID)
	readiness, blocking := graph.Readiness(node, c.look)
	t := model.Task{
		TaskFile:  *tf,
		Folder:    f,
		NotesPath: c.t.Abs(store.NotesRel(f, tf.ID)),
		Readiness: readiness,
		Blocking:  blocking,
	}
	if tf.Open() {
		t.Claim = c.t.Claim(tf.ID)
	}
	return t
}

// folderIDs maps each folder ID to the folders that have it, reading every
// folder file once per command. A folder file koan can't use is a warning:
// it may have held the ID asked for.
func (c *call) folderIDs() (map[model.ID][]model.Folder, *errs.Error) {
	if c.byID != nil {
		return c.byID, nil
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	c.byID = map[model.ID][]model.Folder{}
	eachFolderFile(c.t, x, func(f model.Folder, ff *model.FolderFile, e *errs.Error) {
		if e != nil {
			c.warn(errs.WarnUnusableFile, e.Message, []model.ID{}, c.t.Abs(store.FolderFileRel(f)))
			return
		}
		c.byID[ff.ID] = append(c.byID[ff.ID], f)
	})
	return c.byID, nil
}

// eachFolderFile reads the folder file of every folder in x that has one,
// in tree order, and calls fn with it, or with the error that makes it
// unusable. A file that is gone by the time it is read is skipped.
func eachFolderFile(t *store.Tree, x *store.Index, fn func(f model.Folder, ff *model.FolderFile, e *errs.Error)) {
	for _, f := range x.Folders {
		if !x.Filed[f] {
			continue
		}
		if ff, _, e := t.LoadFolder(f); ff != nil || e != nil {
			fn(f, ff, e)
		}
	}
}

// loadFolderFiles reads every folder file in x: the usable ones by folder,
// and the error of each that can't be used.
func loadFolderFiles(t *store.Tree, x *store.Index) (map[model.Folder]*model.FolderFile, map[model.Folder]*errs.Error) {
	files, bad := map[model.Folder]*model.FolderFile{}, map[model.Folder]*errs.Error{}
	eachFolderFile(t, x, func(f model.Folder, ff *model.FolderFile, e *errs.Error) {
		if e != nil {
			bad[f] = e
		} else {
			files[f] = ff
		}
	})
	return files, bad
}

// highestID is the highest ID in the tree: of a task, of a folder whose file
// is in folders, or of notes, which hold their ID when their task is gone.
func highestID(x *store.Index, folders map[model.Folder]*model.FolderFile) model.ID {
	var highest model.ID
	for id := range x.Tasks {
		highest = max(highest, id)
	}
	for id := range x.Notes {
		highest = max(highest, id)
	}
	for _, ff := range folders {
		highest = max(highest, ff.ID)
	}
	return highest
}

// holder finds what has ID id: a task, in folder f, or folder f itself.
// Nothing is not-found; more than one is duplicate-id.
func (c *call) holder(id model.ID) (f model.Folder, isTask bool, e *errs.Error) {
	byID, e := c.folderIDs()
	if e != nil {
		return "", false, e
	}
	tf, isTask := c.x.Tasks[id]
	folders := byID[id]
	switch {
	case isTask && len(folders) == 0:
		return tf, true, nil
	case !isTask && len(folders) == 1:
		return folders[0], false, nil
	case !isTask && len(folders) == 0:
		return "", false, errs.New(errs.NotFound, "no task or folder has ID %d", id).WithIDs(id)
	}
	e = errs.NewConflict(errs.RuleDuplicateID, "ID %d is held by more than one task or folder", id).WithIDs(id)
	if isTask {
		e.WithPaths(c.t.Abs(store.TaskRel(tf, id)))
	}
	for _, f := range folders {
		e.WithPaths(c.t.Abs(store.FolderFileRel(f)))
	}
	return "", false, e
}

// resolve is the folder r names. One named by ID must exist.
func (c *call) resolve(r folderRef) (model.Folder, *errs.Error) {
	if r.id == 0 {
		return r.path, nil
	}
	f, isTask, e := c.holder(r.id)
	if e != nil {
		if e.Kind == errs.NotFound {
			e.Message = fmt.Sprintf("no folder has ID %d", r.id)
		}
		return "", e
	}
	if isTask {
		return "", errs.New(errs.NotFound, "%d is the ID of a task, in %s, not of a folder", r.id, f).WithIDs(r.id)
	}
	return f, nil
}

// folderOrNotFound checks that folder f exists.
func (c *call) folderOrNotFound(f model.Folder) *errs.Error {
	ok, e := c.t.FolderExists(f)
	if e != nil {
		return e
	}
	if !ok {
		return errs.New(errs.NotFound, "folder %s does not exist", f).WithFolders(string(f))
	}
	return nil
}

// plan lists the folders missing on the paths to the folders fs, each
// once, in tree order: the order they are made in, and take IDs in.
func (c *call) plan(fs ...model.Folder) ([]model.Folder, *errs.Error) {
	var missing []model.Folder
	for _, f := range fs {
		m, e := c.t.Missing(f)
		if e != nil {
			return nil, e
		}
		for _, g := range m {
			if !slices.Contains(missing, g) {
				missing = append(missing, g)
			}
		}
	}
	slices.SortFunc(missing, model.TreeOrder)
	return missing, nil
}

// newFolder is a folder asked for by create-folder or a create-batch
// folder entry, with what it is made with.
type newFolder struct {
	folder model.Folder
	title  *string
	extra  map[string]any
	notes  string
}

// makeFolders makes the planned folders, parents first, the first taking
// the ID first and each the next. Each folder asked for gets its title,
// extra, and notes; one that is already there, which only an outside change
// can have put there, stops it with destination-exists. It returns the
// folders it made.
func (c *call) makeFolders(missing []model.Folder, first model.ID, asked ...newFolder) ([]model.Folder, *errs.Error) {
	made := []model.Folder{}
	for i, f := range missing {
		ff, notes := model.NewFolderFile(first+model.ID(i)), ""
		j := slices.IndexFunc(asked, func(n newFolder) bool { return n.folder == f })
		if j >= 0 {
			ff.Title, notes = asked[j].title, asked[j].notes
			if asked[j].extra != nil {
				ff.Extra = asked[j].extra
			}
		}
		ok, e := c.t.MakeFolder(f, ff, notes)
		if e != nil {
			return made, e
		}
		if !ok && j >= 0 {
			return made, c.destinationExists("appeared while it was being made: its title, extra, and notes were not set", f)
		}
		if ok {
			made = append(made, f)
		}
	}
	return made, nil
}

// destinationExists is the conflict over folders fs, which are already
// there: msg follows their names.
func (c *call) destinationExists(msg string, fs ...model.Folder) *errs.Error {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = string(f)
	}
	e := errs.NewConflict(errs.RuleDestinationExists, "%s %s", strings.Join(names, ", "), msg).WithFolders(names...)
	for _, f := range fs {
		e.WithPaths(c.t.Abs(f.Rel()))
	}
	return e
}

// ensure makes folder f and any missing folders above it, each with an ID
// of its own, and returns the folders it made.
func (c *call) ensure(f model.Folder) ([]model.Folder, *errs.Error) {
	missing, e := c.plan(f)
	if e != nil || len(missing) == 0 {
		return []model.Folder{}, e
	}
	first, e := c.issue(len(missing))
	if e != nil {
		return []model.Folder{}, e
	}
	return c.makeFolders(missing, first)
}

// dropClaim ends a task's claim, if it has one, and saves the state file.
func (c *call) dropClaims(ids ...model.ID) *errs.Error {
	dropped := false
	for _, id := range ids {
		if _, ok := c.t.State.Claims[model.ClaimKey(id)]; ok {
			delete(c.t.State.Claims, model.ClaimKey(id))
			dropped = true
		}
	}
	if !dropped {
		return nil
	}
	return c.t.SaveState()
}
