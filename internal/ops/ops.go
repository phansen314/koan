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
	"folder create": cmdFolderCreate, "folder show": cmdFolderShow, "folder update": cmdFolderUpdate,
	"folder move": cmdFolderMove, "folder delete": cmdFolderDelete, "folder archive": cmdFolderArchive, "folder list": cmdFolderList,
	"folder block": cmdFolderBlock, "folder unblock": cmdFolderUnblock, "folder why": cmdFolderWhy,
	"task create": cmdCreate, "batch": cmdBatch, "task show": cmdShow, "task update": cmdUpdate,
	"task done": cmdDone, "task reopen": cmdReopen, "task block": cmdBlock, "task unblock": cmdUnblock,
	"task move": cmdMove, "task delete": cmdDelete, "task archive": cmdTaskArchive,
	"task frontier": cmdFrontier, "task list": cmdList, "task why": cmdWhy, "where": cmdWhere,
	"context": cmdContext, "marker bind": cmdMarkerBind, "marker unbind": cmdMarkerUnbind,
	"task claim": cmdClaim, "task claim-next": cmdClaimNext, "task release": cmdRelease,
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

// Run runs the command named name, its command line without koan
// ("task create"), on the input raw, one JSON object; empty
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
	// A duplicated ID met as a blocker fails the command, though what it
	// was asked for did not name it.
	if e == nil && c.bt != nil && c.bt.err != nil {
		e = c.bt.err
	}
	if c.t != nil {
		if e != nil {
			c.whereArchived(e)
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
	fx       *folderIndex // every folder file, once read
	g        *graph.Graph // the graph of the tree as it is
	bt       *baseTree    // g's view of the tree
	light    bool         // a command that reads no folder file: it needs no readiness, ID, or cycle check
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

// index walks the tree, once, and then reads every folder file, but in a
// light command.
func (c *call) index() (*store.Index, *errs.Error) {
	if c.x != nil {
		return c.x, nil
	}
	x, e := c.t.Walk(false)
	if e != nil {
		return nil, e
	}
	c.x = x
	if !c.light {
		if _, e := c.folders(); e != nil {
			return nil, e
		}
	}
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
	if c.fx == nil {
		c.folders()
	}
	if c.fx != nil {
		for _, id := range ids {
			for _, f := range c.fx.byID[id] {
				folders = append(folders, fmt.Sprintf("%d is the ID of folder %s", id, f))
			}
		}
	}
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
			c.warn(errs.WarnUnusableFile, l.Err.Message, []model.ID{id}, c.t.Abs(model.TaskRel(f, id)))
		}
		return nil
	}
	return l.File
}

// task builds the shape every command returns a task in. It needs the
// index.
func (c *call) task(f model.Folder, tf *model.TaskFile) model.Task {
	c.dangling(fmt.Sprintf("task %d", tf.ID), tf.ID, tf.BlockedBy, c.t.Abs(model.TaskRel(f, tf.ID)))
	st := c.graph().TaskState(graph.Node{Open: tf.Open(), Priority: tf.Priority, BlockedBy: tf.BlockedBy}, f)
	t := model.Task{
		ID: tf.ID, Title: tf.Title, Priority: tf.Priority, CreatedAt: tf.CreatedAt, CompletedAt: tf.CompletedAt,
		UpdatedAt: tf.UpdatedAt, BlockedBy: tf.BlockedBy, Tags: tf.Tags, Extra: tf.Extra,
		Folder:       f,
		NotesPath:    c.t.Abs(model.NotesRel(f, tf.ID)),
		Readiness:    st.Readiness,
		OpenBlockers: st.Open,
		Inherited:    st.Inherited,
	}
	if tf.Open() {
		t.Claim = c.t.Claim(tf.ID)
	}
	return t
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
