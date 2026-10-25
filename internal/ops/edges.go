package ops

import (
	"fmt"
	"slices"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// GateMade is a gate a change made because an edge named it.
type GateMade struct {
	ID     int64    `json:"id"`
	Folder FolderAt `json:"folder"`
	Gate   string   `json:"gate"`
}

// edgeIn is one edge as given to the task being changed: an entry of its
// blocked_by, or, with blocks set, one written from the other end. At is
// its JSON Pointer.
type edgeIn struct {
	ref    core.Ref
	blocks bool
	at     string
}

// edgeRefs reads blocked_by and blocks, each entry checked as an
// edge-ref, as edges given in that order.
func (r *input) edgeRefs() []edgeIn {
	var out []edgeIn
	for _, field := range []string{"blocked_by", "blocks"} {
		for i, ref := range r.refs(field, core.WantEdge) {
			out = append(out, edgeIn{ref: ref, blocks: field == "blocks", at: fmt.Sprintf("%s/%d", r.at(field), i)})
		}
	}
	return out
}

// joinIn is one folder a join names. At is its JSON Pointer.
type joinIn struct {
	ref core.Ref
	at  string
}

// joinRefs reads the folders a join names, each checked as a folder-ref
// other than the root, which has no gates.
func (r *input) joinRefs(field string) []joinIn {
	var out []joinIn
	for i, ref := range r.refs(field, core.WantFolder) {
		at := fmt.Sprintf("%s/%d", r.at(field), i)
		if ref.IsRoot() {
			r.fault(at, "%q: the root has no gates, so it joins no folder and no folder joins it", ref.Raw)
			continue
		}
		out = append(out, joinIn{ref: ref, at: at})
	}
	return out
}

// edge is an edge resolved: From waits on To. At is where it was given.
type edge struct {
	core.Edge
	at string
}

// edges is what one change does to edges: the gates it made, in the
// order it made them, and the tasks it made, which were neither ready
// nor blocked before it. In a batch, labels maps each label to its task,
// and every refusal of an edge points at it.
type edges struct {
	tx     *store.Tx
	made   []GateMade
	new    map[int64]bool
	labels map[string]int64
}

func newEdges(tx *store.Tx) *edges { return &edges{tx: tx, made: []GateMade{}, new: map[int64]bool{}} }

// gate makes a folder's gate, as an edge naming one it lacks does: its
// row, and the step writing its empty notes, which keeps a file already
// there.
func (w *edges) gate(folder int64, end core.End) (int64, error) {
	id, err := w.tx.MakeGate(folder, end)
	if err != nil {
		return 0, err
	}
	path := w.tx.Path(folder)
	if _, err := fsstep.Add(w.tx, fsstep.Step{Op: fsstep.OpWrite, To: fsstep.TaskNotesPath(path, id, end)}); err != nil {
		return 0, err
	}
	w.new[id] = true
	w.made = append(w.made, GateMade{ID: id, Folder: FolderAt{ID: &folder, Path: path}, Gate: string(end)})
	return id, nil
}

// subject resolves the task block is given. With make set, a gate its
// folder lacks is made; else it is not-found, as anywhere a task is
// taken.
func (w *edges) subject(ref core.Ref, make bool) (int64, error) {
	if !make || ref.End == core.EndNone {
		res, err := resolve(w.tx, ref, core.WantTask, "task")
		return res.ID, err
	}
	// With its end given, the subject resolves as an edge does, so a gate
	// the folder lacks is found missing rather than not-found.
	res, err := resolve(w.tx, ref, core.WantEdge, "task")
	if err != nil || !res.MissingGate() {
		return res.ID, err
	}
	return w.gate(res.Folder, res.End)
}

// resolve resolves edges given to a task, in order. With make set, a gate
// an edge names that its folder lacks is made; else its edge is left out.
func (w *edges) resolve(task int64, in []edgeIn, make bool) ([]edge, error) {
	var out []edge
	for _, e := range in {
		var res core.Resolved
		var err error
		if w.labels != nil {
			res, err = core.Resolve(w.tx, e.ref, core.WantEdge, w.labels)
			err = pointAt(err, e.at)
		} else {
			res, err = resolveAt(w.tx, e.ref, core.WantEdge, e.at)
		}
		if err != nil {
			return nil, err
		}
		if res.MissingGate() {
			if !make {
				// A removal finds an edge that isn't there.
				continue
			}
			if res.ID, err = w.gate(res.Folder, res.End); err != nil {
				return nil, err
			}
		}
		x := core.Edge{From: task, To: res.ID}
		if e.blocks {
			x = core.Edge{From: res.ID, To: task}
		}
		out = append(out, edge{x, e.at})
	}
	return out, nil
}

// member resolves the subject a join or an unjoin is given, a task or a
// folder, never a gate or the root, which join refuses before it
// resolves, to its ID and the ends a join's edges name: a
// task is both its own ends, and a folder's are its start and done
// gates. With make set, a gate a folder lacks is made, the start gate
// first; else it is 0.
func (w *edges) member(ref core.Ref, make bool) (id, start, done int64, err error) {
	res, err := resolve(w.tx, ref, core.WantRef, "ref")
	if err != nil {
		return 0, 0, 0, err
	}
	if res.Kind != core.ResolvedTask {
		start, done, err = w.gates(res.ID, make)
		return res.ID, start, done, err
	}
	row, _, err := w.tx.Task(res.ID)
	if err != nil {
		return 0, 0, 0, err
	}
	if row.Gate != core.EndNone {
		return 0, 0, 0, &core.Error{Kind: core.KindInvalidInput, Problems: []string{"/ref"},
			Message: fmt.Sprintf("@%d is a gate, and a gate joins no folder", res.ID)}
	}
	return res.ID, res.ID, res.ID, nil
}

// gates gives a folder's start and done gates. With make set, a gate it
// lacks is made, the start gate first; else it is 0, a task no edge has.
func (w *edges) gates(folder int64, make bool) (start, done int64, err error) {
	var ids [2]int64
	for i, end := range []core.End{core.EndStart, core.EndDone} {
		id, ok := w.tx.Gate(folder, end)
		if !ok && make {
			if id, err = w.gate(folder, end); err != nil {
				return 0, 0, err
			}
		}
		ids[i] = id
	}
	return ids[0], ids[1], nil
}

// join resolves the edges of joining each folder given, in order: the
// subject, a task or a folder's start and done gates, waits on the
// folder's start gate, and the folder's done gate waits on the subject.
// With make set, a gate a folder lacks is made, its start gate first;
// else an edge naming one, or a subject's gate that is 0, is an edge that
// isn't there, and is left out.
func (w *edges) join(start, done int64, in []joinIn, make bool) ([]edge, error) {
	var out []edge
	for _, j := range in {
		var res core.Resolved
		var err error
		if w.labels != nil {
			res, err = core.Resolve(w.tx, j.ref, core.WantFolder, w.labels)
			err = pointAt(err, j.at)
		} else {
			res, err = resolveAt(w.tx, j.ref, core.WantFolder, j.at)
		}
		if err != nil {
			return nil, err
		}
		folderStart, folderDone, err := w.gates(res.ID, make)
		if err != nil {
			return nil, err
		}
		if start != 0 && folderStart != 0 {
			out = append(out, edge{core.Edge{From: start, To: folderStart}, j.at})
		}
		if folderDone != 0 && done != 0 {
			out = append(out, edge{core.Edge{From: folderDone, To: done}, j.at})
		}
	}
	return out, nil
}

// add writes edges: an edge to itself is invalid-input at it, one that
// closes a cycle is refused, and one already there is left as it is. It
// reports whether it wrote any, the ready tasks they made wait, and the
// complete-blocker and claimed-blocked warnings.
func (w *edges) add(es []edge) (changed bool, reblocked []int64, warnings []cli.Warning, err error) {
	var fresh []edge
	seen := map[core.Edge]bool{}
	for _, e := range es {
		if e.From == e.To {
			return false, nil, nil, &core.Error{Kind: core.KindInvalidInput, Problems: []string{e.at},
				Message: fmt.Sprintf("@%d can't wait on itself", e.From)}
		}
		has, err := w.tx.HasEdge(e.From, e.To)
		if err != nil {
			return false, nil, nil, err
		}
		if has || seen[e.Edge] {
			continue
		}
		seen[e.Edge] = true
		fresh = append(fresh, e)
	}
	plain := make([]core.Edge, len(fresh))
	for i, e := range fresh {
		plain[i] = e.Edge
	}
	if cycle, _ := core.FindCycle(w.tx.BlockedBy, plain); cycle != nil {
		err := core.Conflict(core.RuleCycle, cycle, nil,
			"@%d would wait on itself through %s", cycle[0], core.FormatIDs(cycle[1:]))
		for i := range cycle {
			step := core.Edge{From: cycle[i], To: cycle[(i+1)%len(cycle)]}
			for _, e := range fresh {
				// A join's two edges share their pointer, named once.
				if e.Edge == step && !slices.Contains(err.Problems, e.at) {
					err.Problems = append(err.Problems, e.at)
				}
			}
		}
		return false, nil, nil, err
	}
	// Only a task whose blocked_by gains an entry can come to wait.
	var waiting []int64
	for _, e := range fresh {
		if !w.new[e.From] {
			waiting = append(waiting, e.From)
		}
	}
	watched := watch(w.tx, waiting)
	for _, e := range fresh {
		if err := w.tx.AddEdge(e.From, e.To); err != nil {
			return false, nil, nil, err
		}
	}
	var complete []int64
	for _, id := range watched.ids {
		if !w.tx.Open(id) {
			complete = append(complete, id)
		}
	}
	if len(complete) > 0 {
		message := core.FormatIDs(complete) + " is complete: an edge added to its blocked_by doesn't reopen it, so what waits on it isn't held back."
		if len(complete) > 1 {
			message = core.FormatIDs(complete) + " are complete: edges added to their blocked_by don't reopen them, so what waits on them isn't held back."
		}
		warnings = append(warnings, cli.Warning{Kind: "complete-blocker", Message: message, IDs: complete})
	}
	reblocked, claimed, err := watched.reblocked()
	if err != nil {
		return false, nil, nil, err
	}
	return len(fresh) > 0, reblocked, append(warnings, claimed...), nil
}

// remove removes edges, leaving alone any that isn't there. It reports
// whether it removed any, and the open tasks it made ready.
func (w *edges) remove(es []edge) (changed bool, released []int64, err error) {
	var waiting []int64
	for _, e := range es {
		waiting = append(waiting, e.From)
	}
	watched := watch(w.tx, waiting)
	for _, e := range es {
		removed, err := w.tx.RemoveEdge(e.From, e.To)
		if err != nil {
			return false, nil, err
		}
		changed = changed || removed
	}
	return changed, watched.released(), nil
}

// watched is the tasks a change may make ready or make wait, lowest ID
// first, with their readiness before it.
type watched struct {
	tx      *store.Tx
	ids     []int64
	ready   map[int64]bool
	blocked map[int64]bool
}

// watch notes the readiness of tasks before a change.
func watch(tx *store.Tx, ids []int64) *watched {
	w := &watched{tx: tx, ready: map[int64]bool{}, blocked: map[int64]bool{}}
	for _, id := range ids {
		if _, seen := w.ready[id]; seen {
			continue
		}
		w.ids = append(w.ids, id)
		w.ready[id], w.blocked[id] = core.Ready(tx, id), core.Blocked(tx, id)
	}
	slices.Sort(w.ids)
	return w
}

// released is the tasks that were blocked and are ready now.
func (w *watched) released() []int64 {
	var out []int64
	for _, id := range w.ids {
		if w.blocked[id] && core.Ready(w.tx, id) {
			out = append(out, id)
		}
	}
	return nonNil(out)
}

// reblocked is the tasks that were ready and wait now, with the
// claimed-blocked warning for those of them that are claimed.
func (w *watched) reblocked() ([]int64, []cli.Warning, error) {
	var out, claimed []int64
	for _, id := range w.ids {
		if !w.ready[id] || !core.Blocked(w.tx, id) {
			continue
		}
		out = append(out, id)
		c, err := w.tx.Claimed(id)
		if err != nil {
			return nil, nil, err
		}
		if c {
			claimed = append(claimed, id)
		}
	}
	if len(claimed) == 0 {
		return nonNil(out), nil, nil
	}
	message := core.FormatIDs(claimed) + " is claimed, and waits again; its claim stays."
	if len(claimed) > 1 {
		message = core.FormatIDs(claimed) + " are claimed, and wait again; their claims stay."
	}
	return out, []cli.Warning{{Kind: "claimed-blocked", Message: message, IDs: claimed}}, nil
}

// pointAt points a refusal at the field at fault, as a batch's refusals
// point at its parts.
func pointAt(err error, at string) error {
	if e, ok := err.(*core.Error); ok && !slices.Contains(e.Problems, at) {
		e.Problems = append(e.Problems, at)
	}
	return err
}

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}
