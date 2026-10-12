// Package ops is koan's commands: each takes one JSON object, does its work,
// and returns a result or an error (spec/operations.md).
package ops

import (
	"bytes"
	"encoding/json"
	"path"
	"slices"

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
	"create": cmdCreate, "create-batch": cmdCreateBatch, "show": cmdShow, "update": cmdUpdate,
	"done": cmdDone, "reopen": cmdReopen, "block": cmdBlock, "unblock": cmdUnblock,
	"move": cmdMove, "delete": cmdDelete,
	"frontier": cmdFrontier, "list": cmdList, "why": cmdWhy,
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
	if name != "version" && !path.IsAbs(env.Home) {
		return Failure(errs.New(errs.IO, "HOME is not set to an absolute path"))
	}
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
	warnings []errs.Warning
	warned   map[string]bool
}

// begin ends input checking and opens the tree: input errors come first,
// then the root's state, then busy.
func (c *call) begin(write bool) *errs.Error { return c.beginNeed(store.NeedUsable, write) }

func (c *call) beginNeed(need store.Need, write bool) *errs.Error {
	if e := c.in.done(); e != nil {
		return e
	}
	t, e := store.Open(c.env, need, write)
	if e != nil {
		return e
	}
	c.t = t
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

func (c *call) warn(kind, msg string, ids []model.ID, paths ...string) {
	key := kind + "\x00" + msg
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
		return "", nil, notFoundTask(id)
	}
	l := c.t.Load(f, id)
	if l.Err != nil {
		return "", nil, l.Err
	}
	return f, l.File, nil
}

func notFoundTask(ids ...model.ID) *errs.Error {
	if len(ids) == 1 {
		return errs.New(errs.NotFound, "task %d does not exist", ids[0]).WithIDs(ids...)
	}
	return errs.New(errs.NotFound, "%d tasks do not exist", len(ids)).WithIDs(ids...)
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
	for _, b := range tf.BlockedBy {
		if _, ok := c.x.Tasks[b]; !ok {
			c.warn(errs.WarnDanglingReference,
				"task "+model.ClaimKey(id)+" is blocked by "+model.ClaimKey(b)+", which does not exist",
				[]model.ID{id, b}, c.t.Abs(store.TaskRel(c.x.Tasks[id], id)))
		}
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
