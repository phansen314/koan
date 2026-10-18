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
	at        folderRef    // the folder, as the input names it
	folder    model.Folder // the folder, once resolved
	recursive bool
	tagsAny   []string
	tagsAll   []string
}

func readScope(f fields) scope {
	s := scope{at: f.folderRef("folder", false, folderRef{path: model.Root}), recursive: f.boolean("recursive", true)}
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
// It resolves s's folder.
func (c *call) tasks(s *scope, keep func(model.Task) bool) ([]model.Task, *errs.Error) {
	var e *errs.Error
	if s.folder, e = c.resolve(s.at); e != nil {
		return nil, e
	}
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

// readNarrowing reads limit, and fields, each one of allowed.
func readNarrowing(f fields, allowed []string) narrowing {
	var n narrowing
	if f.get("limit", false, &n.limit, "a positive integer") && n.limit < 1 {
		f.in.bad("/limit", "must be a positive integer")
	}
	n.fields, _ = f.oneOf("fields", allowed)
	return n
}

type listing struct {
	Tasks     []json.RawMessage `json:"tasks"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
}

func (n narrowing) apply(tasks []model.Task) (listing, *errs.Error) {
	l := listing{Total: len(tasks)}
	var e *errs.Error
	l.Tasks, l.Truncated, e = narrow(n, tasks, model.TaskFields, "id")
	return l, e
}

// narrow cuts items to the limit, and encodes each with only the chosen
// fields and those it always has, in order, the order its fields are
// written in. No fields chosen means all of them.
func narrow[T any](n narrowing, items []T, order []string, always ...string) ([]json.RawMessage, bool, *errs.Error) {
	truncated := n.limit > 0 && len(items) > n.limit
	if truncated {
		items = items[:n.limit]
	}
	out := []json.RawMessage{}
	for _, v := range items {
		raw, err := project(v, order, n.fields, always)
		if err != nil {
			return nil, false, errs.New(errs.Internal, "encode the result: %v", err)
		}
		out = append(out, raw)
	}
	return out, truncated, nil
}

// project encodes v with only the fields named in fields or always.
func project(v any, order, fields, always []string) (json.RawMessage, error) {
	whole, err := json.Marshal(v)
	if err != nil || fields == nil {
		return whole, err
	}
	m, err := model.Keys(whole)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for _, k := range order {
		if !slices.Contains(always, k) && !slices.Contains(fields, k) {
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
	s, n := readScope(in), readNarrowing(in, model.TaskFields)
	if e := c.begin(false); e != nil {
		return nil, e
	}
	tasks, e := c.tasks(&s, available)
	if e != nil {
		return nil, e
	}
	slices.SortFunc(tasks, workOrder)
	return n.apply(tasks)
}

func cmdList(c *call) (any, *errs.Error) {
	in := c.in.top()
	s, n := readScope(in), readNarrowing(in, model.TaskFields)
	var claimed bool
	hasClaimed := in.get("claimed", false, &claimed, "true or false")
	readiness, ok := in.oneOf("readiness", []string{"ready", "blocked", "done"})
	if !ok {
		readiness = []string{"ready", "blocked"}
	} else if len(readiness) == 0 {
		c.in.bad("/readiness", "must hold at least one of ready, blocked, done")
	}
	if e := c.begin(false); e != nil {
		return nil, e
	}
	tasks, e := c.tasks(&s, func(t model.Task) bool {
		return slices.Contains(readiness, string(t.Readiness)) && (!hasClaimed || claimed == (t.Claim != nil))
	})
	if e != nil {
		return nil, e
	}
	return n.apply(tasks)
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
