package ops

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/graph"
	"github.com/phansen314/koan/internal/model"
)

// scope is which tasks a query looks at.
type scope struct {
	folder    model.Folder
	recursive bool
	tagsAny   []string
	tagsAll   []string
}

func readScope(f fields) scope {
	s := scope{folder: f.folder("folder", false, model.Root), recursive: f.boolean("recursive", true)}
	s.tagsAny, _ = f.tags("tags_any")
	s.tagsAll, _ = f.tags("tags_all")
	return s
}

func (s scope) holds(f model.Folder) bool {
	return f == s.folder || (s.recursive && s.folder.Holds(f))
}

func (s scope) matches(tf *model.TaskFile) bool {
	if s.tagsAny != nil && !slices.ContainsFunc(s.tagsAny, func(t string) bool { return slices.Contains(tf.Tags, t) }) {
		return false
	}
	for _, t := range s.tagsAll {
		if !slices.Contains(tf.Tags, t) {
			return false
		}
	}
	return true
}

// tasks returns every usable task in scope that keep accepts, in tree order.
// An unusable file in scope is a warning.
func (c *call) tasks(s scope, keep func(model.Task) bool) ([]model.Task, *errs.Error) {
	if e := c.folderOrNotFound(s.folder); e != nil {
		return nil, e
	}
	x, e := c.index()
	if e != nil {
		return nil, e
	}
	out := []model.Task{}
	for _, l := range inTreeOrder(x) {
		if !s.holds(l.f) {
			continue
		}
		tf := c.pass(l.id)
		if tf == nil {
			continue
		}
		// Every task in scope is looked at before any is filtered out, so
		// that warnings do not depend on narrowing.
		if t := c.task(l.f, tf); s.matches(tf) && keep(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

func workOrder(a, b model.Task) int { return graph.WorkOrder(a.ID, a.Priority, b.ID, b.Priority) }

func available(t model.Task) bool { return t.Readiness == model.Ready && t.Claim == nil }

// narrowing is the limit and fields of a listing.
type narrowing struct {
	limit  int
	fields []string
}

func readNarrowing(f fields) narrowing {
	var n narrowing
	if f.get("limit", false, &n.limit, "a positive integer") && n.limit < 1 {
		f.in.bad("/limit", "must be a positive integer")
	}
	n.fields, _ = f.oneOf("fields", model.TaskFields)
	return n
}

type listing struct {
	Tasks     []json.RawMessage `json:"tasks"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
}

func (n narrowing) apply(tasks []model.Task) (listing, *errs.Error) {
	l := listing{Total: len(tasks), Tasks: []json.RawMessage{}}
	if n.limit > 0 && len(tasks) > n.limit {
		tasks, l.Truncated = tasks[:n.limit], true
	}
	for _, t := range tasks {
		raw, err := project(t, n.fields)
		if err != nil {
			return l, errs.New(errs.Internal, "encode task %d: %v", t.ID, err)
		}
		l.Tasks = append(l.Tasks, raw)
	}
	return l, nil
}

// project encodes a task with only the chosen fields, and always id, in the
// order a whole task has them. No fields chosen means all of them.
func project(t model.Task, fields []string) (json.RawMessage, error) {
	whole, err := json.Marshal(t)
	if err != nil || fields == nil {
		return whole, err
	}
	m, err := model.Keys(whole)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for _, k := range model.TaskFields {
		if k != "id" && !slices.Contains(fields, k) {
			continue
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteByte(':')
		b.Write(m[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func cmdFrontier(c *call) (any, *errs.Error) {
	in := c.in.top()
	s, n := readScope(in), readNarrowing(in)
	if e := c.begin(false); e != nil {
		return nil, e
	}
	tasks, e := c.tasks(s, available)
	if e != nil {
		return nil, e
	}
	slices.SortFunc(tasks, workOrder)
	return n.apply(tasks)
}

func cmdList(c *call) (any, *errs.Error) {
	in := c.in.top()
	s, n := readScope(in), readNarrowing(in)
	var claimed bool
	hasClaimed := in.get("claimed", false, &claimed, "true or false")
	readiness, ok := in.oneOf("readiness", []string{"ready", "blocked", "done"})
	if !ok {
		readiness = []string{"ready", "blocked"}
	} else if len(readiness) == 0 {
		c.in.bad("/readiness", "must hold at least one of ready, blocked, done")
	}
	folders := in.boolean("include_folders", false)
	if e := c.begin(false); e != nil {
		return nil, e
	}
	tasks, e := c.tasks(s, func(t model.Task) bool {
		return slices.Contains(readiness, string(t.Readiness)) && (!hasClaimed || claimed == (t.Claim != nil))
	})
	if e != nil {
		return nil, e
	}
	l, e := n.apply(tasks)
	if e != nil {
		return nil, e
	}
	if !folders {
		return l, nil
	}
	fs := []model.FolderRecord{}
	for _, f := range c.x.Folders {
		if s.folder.Holds(f) {
			fs = append(fs, c.passFolder(f))
		}
	}
	return struct {
		listing
		Folders []model.FolderRecord `json:"folders"`
	}{l, fs}, nil
}

type whyResult struct {
	Readiness model.Readiness `json:"readiness"`
	Ready     []model.ID      `json:"ready"`
	Stuck     []model.ID      `json:"stuck"`
}

func cmdWhy(c *call) (any, *errs.Error) {
	id := c.in.top().id("id", true)
	if e := c.begin(false); e != nil {
		return nil, e
	}
	f, tf, e := c.find(id)
	if e != nil {
		return nil, e
	}
	ready, stuck := graph.Why(id, c.look)
	return whyResult{Readiness: c.task(f, tf).Readiness, Ready: ready, Stuck: stuck}, nil
}
