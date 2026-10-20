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
	End    string   `json:"end"`
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
			out = append(out, edgeIn{ref: ref, blocks: field == "blocks", at: fmt.Sprintf("%s/%d", cli.Pointer(field), i)})
		}
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
// nor blocked before it.
type edges struct {
	tx   *store.Tx
	made []GateMade
	new  map[int64]bool
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
	w.made = append(w.made, GateMade{ID: id, Folder: FolderAt{ID: &folder, Path: path}, End: string(end)})
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
// an edge names that its folder lacks is made; else it is not-found.
func (w *edges) resolve(task int64, in []edgeIn, make bool) ([]edge, error) {
	var out []edge
	for _, e := range in {
		res, err := resolveAt(w.tx, e.ref, core.WantEdge, e.at)
		if err != nil {
			return nil, err
		}
		if res.MissingGate() {
			if !make {
				path := w.tx.Path(res.Folder)
				return nil, core.NotFound(nil, []string{path + "." + string(res.End)},
					"@%d (%s) has no %s gate", res.Folder, path, res.End)
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
				if e.Edge == step {
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

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}
