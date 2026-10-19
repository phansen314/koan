package ops

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

// TestRandomCommands runs long random sequences of commands against a tree
// and, after every one, compares what koan says with a small model of what
// the specs promise: which commands succeed and how the others fail, every
// task's folder, readiness, blockers, and claim, the frontier's order, the
// folders, their IDs, blockers, and readiness, and that IDs are never
// reused. The model has its own cycle check, a search of the whole graph of
// spec/design.md (Invariants). doctor must stay healthy throughout.
//
// KOAN_RANDOM_SEED reruns one seed; KOAN_RANDOM_STEPS sets the length.
func TestRandomCommands(t *testing.T) {
	seeds, steps := 40, 250
	if testing.Short() {
		seeds, steps = 5, 100
	}
	if s := os.Getenv("KOAN_RANDOM_STEPS"); s != "" {
		steps, _ = strconv.Atoi(s)
	}
	if s := os.Getenv("KOAN_RANDOM_SEED"); s != "" {
		seed, _ := strconv.ParseInt(s, 10, 64)
		runRandom(t, seed, steps)
		return
	}
	for seed := int64(1); seed <= int64(seeds); seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			t.Parallel()
			runRandom(t, seed, steps)
		})
	}
}

// mtask is the model's task.
type mtask struct {
	folder   string
	open     bool
	blockers []int64
	priority *int64
	tags     []string
	claimed  bool
}

// expected is what the specs say the tree holds after the commands so far.
type expected struct {
	tasks   map[int64]*mtask
	folders map[string]bool
	fids    map[string]int64   // each folder's ID, the root aside
	fblock  map[string][]int64 // each folder's blocked_by
	lastID  int64
}

// addFolder records folders fs and those above them: each one that is new
// takes an ID, in tree order.
func (m *expected) addFolder(fs ...string) {
	var missing []string
	for _, f := range fs {
		for ; f != "/"; f = parent(f) {
			if !m.folders[f] && !slices.Contains(missing, f) {
				missing = append(missing, f)
			}
		}
	}
	slices.SortFunc(missing, treeOrder)
	for _, g := range missing {
		m.folders[g] = true
		m.lastID++
		m.fids[g] = m.lastID
	}
	m.folders["/"] = true
}

// treeOrder sorts a parent before its children, and siblings by name, byte
// for byte.
func treeOrder(a, b string) int {
	return slices.Compare(strings.Split(strings.Trim(a, "/"), "/"), strings.Split(strings.Trim(b, "/"), "/"))
}

// clone is a copy of the model, to try a change on.
func (m *expected) clone() *expected {
	c := &expected{tasks: map[int64]*mtask{}, folders: map[string]bool{}, fids: map[string]int64{}, fblock: map[string][]int64{}, lastID: m.lastID}
	for id, t := range m.tasks {
		tc := *t
		tc.blockers = slices.Clone(t.blockers)
		c.tasks[id] = &tc
	}
	for f := range m.folders {
		c.folders[f] = true
	}
	for f, id := range m.fids {
		c.fids[f] = id
	}
	for f, bs := range m.fblock {
		c.fblock[f] = slices.Clone(bs)
	}
	return c
}

// folderWith is the folder with ID id, or "".
func (m *expected) folderWith(id int64) string {
	for f, fid := range m.fids {
		if fid == id {
			return f
		}
	}
	return ""
}

// exists reports whether a task or a folder has ID id.
func (m *expected) exists(id int64) bool { return m.tasks[id] != nil || m.folderWith(id) != "" }

// done reports whether every task under folder f is done.
func (m *expected) done(f string) bool {
	for _, t := range m.tasks {
		if holds(f, t.folder) && t.open {
			return false
		}
	}
	return true
}

// wait makes the task or folder with ID id wait on blocker too.
func (m *expected) wait(id, blocker int64) {
	if t := m.tasks[id]; t != nil {
		if !slices.Contains(t.blockers, blocker) {
			t.blockers = append(t.blockers, blocker)
		}
	} else if f := m.folderWith(id); f != "" && !slices.Contains(m.fblock[f], blocker) {
		m.fblock[f] = append(m.fblock[f], blocker)
	}
}

// blocks reports whether the blocker id blocks now.
func (m *expected) blocks(id int64) bool {
	if t := m.tasks[id]; t != nil {
		return t.open
	}
	if f := m.folderWith(id); f != "" {
		return !m.done(f)
	}
	return true
}

// above lists the folders from the root's child down to f, f included.
func above(f string) []string {
	var fs []string
	for ; f != "/"; f = parent(f) {
		fs = append(fs, f)
	}
	slices.Reverse(fs)
	return fs
}

// waitOn is what is in folder f waits on now: the blocking IDs in the
// blocked_by of f and every folder above it.
func (m *expected) waitOn(f string, own []int64) []int64 {
	out := []int64{}
	add := func(bs []int64) {
		for _, b := range bs {
			if m.blocks(b) && !slices.Contains(out, b) {
				out = append(out, b)
			}
		}
	}
	add(own)
	for _, g := range above(f) {
		add(m.fblock[g])
	}
	slices.Sort(out)
	return out
}

// folderReadiness is a folder's readiness: done, or blocked by its own and
// its parents' blockers, or ready.
func (m *expected) folderReadiness(f string) string {
	switch {
	case m.done(f):
		return "done"
	case f != "/" && len(m.waitOn(parent(f), m.fblock[f])) > 0:
		return "blocked"
	}
	return "ready"
}

// acyclic reports whether the graph of spec/design.md (Invariants) has no
// cycle: a node per task, and a wait and a done node per folder.
func (m *expected) acyclic() bool {
	target := func(b int64) string {
		if m.tasks[b] != nil {
			return fmt.Sprint("t", b)
		}
		if f := m.folderWith(b); f != "" {
			return "d" + f
		}
		return ""
	}
	edges := map[string][]string{}
	add := func(from string, to ...string) {
		for _, t := range to {
			if t != "" {
				edges[from] = append(edges[from], t)
			}
		}
	}
	for id, t := range m.tasks {
		n := fmt.Sprint("t", id)
		for _, b := range t.blockers {
			add(n, target(b))
		}
		if t.folder != "/" {
			add(n, "w"+t.folder)
			add("d"+t.folder, n)
		}
	}
	for f := range m.folders {
		if f == "/" {
			continue
		}
		for _, b := range m.fblock[f] {
			add("w"+f, target(b))
		}
		add("d"+f, "w"+f)
		if p := parent(f); p != "/" {
			add("w"+f, "w"+p)
			add("d"+p, "d"+f)
		}
	}
	state := map[string]int{} // 1 on the stack, 2 done
	var visit func(string) bool
	visit = func(n string) bool {
		switch state[n] {
		case 1:
			return false
		case 2:
			return true
		}
		state[n] = 1
		for _, m := range edges[n] {
			if !visit(m) {
				return false
			}
		}
		state[n] = 2
		return true
	}
	for n := range edges {
		if !visit(n) {
			return false
		}
	}
	return true
}

func parent(f string) string {
	if i := strings.LastIndex(f, "/"); i > 0 {
		return f[:i]
	}
	return "/"
}

func holds(f, g string) bool { return f == "/" || g == f || strings.HasPrefix(g, f+"/") }

// caseMismatch reports whether a part of f's path is no folder of the model
// but one is there with its name in another case, other than self.
func (m *expected) caseMismatch(f, self string) bool {
	cur := "/"
	for _, seg := range model.Folder(f).Segments() {
		par := cur
		cur = path.Join(cur, seg)
		if m.folders[cur] {
			continue
		}
		for g := range m.folders {
			if g != "/" && g != self && parent(g) == par && model.SameButCase(path.Base(g), seg) {
				return true
			}
		}
		return false
	}
	return false
}

// clash reports whether two paths of one input differ only in case.
func clash(a, b string) bool {
	i, _ := model.CaseClash([]model.Folder{model.Folder(a), model.Folder(b)})
	return i >= 0
}

// either is a want of two failures, when koan may report either.
func either(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "|" + b
}

func (m *expected) ids() []int64 {
	ids := []int64{}
	for id := range m.tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (m *expected) openBlockers(t *mtask) []int64 {
	if !t.open {
		return []int64{} // a done task waits on nothing
	}
	return m.waitOn(t.folder, t.blockers)
}

func (m *expected) readiness(t *mtask) string {
	switch {
	case !t.open:
		return "done"
	case len(m.openBlockers(t)) > 0:
		return "blocked"
	}
	return "ready"
}

// frontier is the ready, unclaimed tasks in work order.
func (m *expected) frontier() []int64 {
	out := []int64{}
	for _, id := range m.ids() {
		if t := m.tasks[id]; m.readiness(t) == "ready" && !t.claimed {
			out = append(out, id)
		}
	}
	slices.SortStableFunc(out, func(a, b int64) int {
		pa, pb := m.tasks[a].priority, m.tasks[b].priority
		switch {
		case pa != nil && pb != nil:
			return int(*pb - *pa)
		case pa != nil:
			return -1
		case pb != nil:
			return 1
		}
		return 0
	})
	return out
}

// remove takes the IDs in gone, of tasks and folders, out of the model and
// out of every blocked_by.
func (m *expected) remove(gone []int64) {
	for _, id := range gone {
		delete(m.tasks, id)
	}
	isGone := func(b int64) bool { return slices.Contains(gone, b) }
	for _, t := range m.tasks {
		t.blockers = slices.DeleteFunc(t.blockers, isGone)
	}
	for f, bs := range m.fblock {
		m.fblock[f] = slices.DeleteFunc(bs, isGone)
	}
}

// random drives one run.
type random struct {
	t   *testing.T
	w   *world
	m   *expected
	rng *rand.Rand
	log []string
}

func runRandom(t *testing.T, seed int64, steps int) {
	r := &random{t: t, w: ready(t), rng: rand.New(rand.NewSource(seed)),
		m: &expected{tasks: map[int64]*mtask{}, folders: map[string]bool{"/": true}, fids: map[string]int64{}, fblock: map[string][]int64{}}}
	for i := 0; i < steps && !t.Failed(); i++ {
		r.step()
		r.compare()
		if i%25 == 24 || i == steps-1 {
			if got := r.w.ok("doctor", `{}`); got["healthy"] != true {
				r.failf("doctor: %v", got["findings"])
			}
		}
	}
	if t.Failed() {
		t.Logf("seed %d; rerun with KOAN_RANDOM_SEED=%d. Commands:\n%s", seed, seed, strings.Join(r.log, "\n"))
	}
}

func (r *random) failf(format string, args ...any) {
	r.t.Helper()
	r.t.Errorf(format, args...)
}

// folderNames are the names folders are made of: few, so that paths collide
// and nest. Two differ only in case, which koan refuses on every platform.
var folderNames = []string{"a", "b", "c", "A"}

// folder picks a path up to three deep.
func (r *random) folder() string {
	if r.rng.Intn(6) == 0 {
		return "/"
	}
	f := ""
	for d := 1 + r.rng.Intn(3); d > 0; d-- {
		f += "/" + folderNames[r.rng.Intn(len(folderNames))]
	}
	return f
}

// existingFolder picks a folder of the model, sometimes a path that may not
// exist.
func (r *random) existingFolder() string {
	if r.rng.Intn(8) == 0 {
		return r.folder()
	}
	fs := []string{}
	for f := range r.m.folders {
		fs = append(fs, f)
	}
	slices.Sort(fs)
	return fs[r.rng.Intn(len(fs))]
}

// id picks a task of the model, sometimes one that does not exist.
func (r *random) id() int64 {
	ids := r.m.ids()
	if len(ids) == 0 || r.rng.Intn(15) == 0 {
		return r.m.lastID + 1 + int64(r.rng.Intn(3))
	}
	return ids[r.rng.Intn(len(ids))]
}

func (r *random) missing(ids ...int64) bool {
	return slices.ContainsFunc(ids, func(id int64) bool { return r.m.tasks[id] == nil })
}

// blocker is a blocker as an input names it, and the ID it stands for; 0
// for a path that names no folder.
type blocker struct {
	value any
	id    int64
}

// someBlockers picks up to max tasks and folders: by task ID, by folder ID,
// or by folder path, sometimes one that names nothing.
func (r *random) someBlockers(max int) []blocker {
	var out []blocker
	for n := r.rng.Intn(max + 1); n > 0; n-- {
		var b blocker
		switch k := r.rng.Intn(3); {
		case k == 0 || len(r.m.fids) == 0:
			b.id = r.id()
			b.value = b.id
		case k == 1:
			b.id = r.m.fids[r.existingFolder()]
			if b.id == 0 {
				b.id = r.id()
			}
			b.value = b.id
		default:
			f := r.existingFolder()
			if f == "/" {
				continue
			}
			b = blocker{value: f, id: r.m.fids[f]}
		}
		if !slices.ContainsFunc(out, func(o blocker) bool { return o.id == b.id && b.id != 0 || js(o.value) == js(b.value) }) {
			out = append(out, b)
		}
	}
	return out
}

// values are the blockers as the input names them.
func values(bs []blocker) []any {
	out := []any{}
	for _, b := range bs {
		out = append(out, b.value)
	}
	return out
}

// blockerWant is how naming blockers fails, or "": a path that names no
// folder, or an ID that names nothing.
func (r *random) blockerWant(bs []blocker) string {
	want := ""
	for _, b := range bs {
		switch {
		case b.id == 0 && r.m.caseMismatch(b.value.(string), ""):
			want = either(want, "conflict/case-mismatch|not-found")
		case b.id == 0 || !r.m.exists(b.id):
			want = either(want, "not-found")
		}
	}
	return want
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// do runs a command and checks that it failed the way the model says, or
// succeeded. want is "" for success, a kind, or "conflict/<rule>", or two
// of them joined by "|" when either may be reported.
func (r *random) do(name, input, want string) map[string]any {
	r.t.Helper()
	r.log = append(r.log, fmt.Sprintf("koan %s -i - <<< '%s'   # want %s", name, input, cond(want == "", "ok", want)))
	env := r.w.run(name, input)
	got := ""
	if !env.OK {
		got = string(env.Error.Kind)
		if env.Error.Rule != nil {
			got += "/" + *env.Error.Rule
		}
		if len(env.Error.ChangedPaths) > 0 {
			r.failf("%s %s: refused with %s, but changed %v", name, input, got, env.Error.ChangedPaths)
		}
	}
	if !slices.Contains(strings.Split(want, "|"), got) {
		r.failf("%s %s: got %s, want %s", name, input, cond(got == "", "ok", got), cond(want == "", "ok", want))
		return nil
	}
	if len(env.Warnings) > 0 {
		r.failf("%s %s: warnings on a tree only koan wrote: %+v", name, input, env.Warnings)
	}
	if !env.OK {
		return nil
	}
	data, _ := json.Marshal(env.Result)
	var res map[string]any
	json.Unmarshal(data, &res)
	return res
}

func cond(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

func (r *random) step() {
	m := r.m
	switch n := r.rng.Intn(114); {
	case n < 18: // create
		folder, bs, ds := r.folder(), r.someBlockers(2), r.someBlockers(1)
		var pri *int64
		if r.rng.Intn(2) == 0 {
			p := int64(r.rng.Intn(5) - 1)
			pri = &p
		}
		var blockers []int64
		for _, b := range bs {
			blockers = append(blockers, b.id)
		}
		want := either(r.blockerWant(append(slices.Clone(bs), ds...)), cond(m.caseMismatch(folder, ""), "conflict/case-mismatch", ""))
		if want == "" {
			try := m.clone()
			try.addFolder(folder)
			id := try.lastID + 1
			try.tasks[id] = &mtask{folder: folder, open: true, blockers: blockers}
			for _, d := range ds {
				try.wait(d.id, id)
			}
			want = cond(try.acyclic(), "", "conflict/cycle")
		}
		res := r.do("task create", js(map[string]any{"title": "t", "folder": folder, "blocked_by": values(bs), "blocks": values(ds), "priority": pri}), want)
		if res != nil {
			m.addFolder(folder)
			id := int64(res["id"].(float64))
			for _, d := range ds {
				m.wait(d.id, id)
			}
			r.created(id, &mtask{folder: folder, open: true, blockers: blockers, priority: pri})
		}
	case n < 24: // batch: a chain of three in two folders
		f1, f2 := r.folder(), r.folder()
		want := ""
		switch {
		case clash(f1, f2):
			want = "invalid-input"
		case m.caseMismatch(f1, "") || m.caseMismatch(f2, ""):
			want = "conflict/case-mismatch"
		default:
			// Refs alone close a cycle where folders order the tasks: y,
			// waiting on x, in a folder that what holds x waits on.
			try := m.clone()
			try.addFolder(f1, f2)
			x := try.lastID + 1
			try.tasks[x] = &mtask{folder: f1, open: true}
			try.tasks[x+1] = &mtask{folder: f2, open: true, blockers: []int64{x}}
			try.tasks[x+2] = &mtask{folder: f1, open: true, blockers: []int64{x, x + 1}}
			want = cond(try.acyclic(), "", "conflict/cycle")
		}
		res := r.do("batch", js(map[string]any{"folder": f1, "tasks": []any{
			map[string]any{"ref": "x", "title": "x"},
			map[string]any{"ref": "y", "title": "y", "folder": f2, "blocked_by": []any{"x"}},
			map[string]any{"title": "z", "blocked_by": []any{"x", "y"}},
		}}), want)
		if res != nil {
			got := ids(res["ids"])
			m.addFolder(f1, f2) // folders take their IDs before every task
			r.created(got[0], &mtask{folder: f1, open: true})
			r.created(got[1], &mtask{folder: f2, open: true, blockers: []int64{got[0]}})
			r.created(got[2], &mtask{folder: f1, open: true, blockers: []int64{got[0], got[1]}})
		}
	case n < 30: // update
		id := r.id()
		tags := [][]string{{}, {"x"}, {"x", "y"}}[r.rng.Intn(3)]
		p := int64(r.rng.Intn(5) - 1)
		if r.do("task update", js(map[string]any{"id": id, "priority": p, "tags_replace_all": tags}), cond(r.missing(id), "not-found", "")) != nil {
			m.tasks[id].priority, m.tasks[id].tags = &p, tags
		}
	case n < 40: // done
		id := r.id()
		if r.do("task done", js(map[string]any{"id": id}), cond(r.missing(id), "not-found", "")) != nil {
			m.tasks[id].open, m.tasks[id].claimed = false, false
		}
	case n < 44: // reopen
		id := r.id()
		if r.do("task reopen", js(map[string]any{"id": id}), cond(r.missing(id), "not-found", "")) != nil {
			m.tasks[id].open = true
		}
	case n < 54: // block
		id, bs := r.id(), r.someBlockers(2)
		bs = slices.DeleteFunc(bs, func(b blocker) bool { return b.id == id })
		if len(bs) == 0 {
			return
		}
		want := either(cond(r.missing(id), "not-found", ""), r.blockerWant(bs))
		if want == "" {
			try := m.clone()
			for _, b := range bs {
				if !slices.Contains(try.tasks[id].blockers, b.id) {
					try.tasks[id].blockers = append(try.tasks[id].blockers, b.id)
				}
			}
			want = cond(try.acyclic(), "", "conflict/cycle")
		}
		if r.do("task block", js(map[string]any{"id": id, "blocked_by": values(bs)}), want) != nil {
			for _, b := range bs {
				if !slices.Contains(m.tasks[id].blockers, b.id) {
					m.tasks[id].blockers = append(m.tasks[id].blockers, b.id)
				}
			}
		}
	case n < 58: // unblock
		id, bs := r.id(), r.someBlockers(2)
		if len(bs) == 0 {
			return
		}
		paths := slices.DeleteFunc(slices.Clone(bs), func(b blocker) bool { _, isPath := b.value.(string); return !isPath })
		want := either(cond(r.missing(id), "not-found", ""), r.blockerWant(paths))
		if r.do("task unblock", js(map[string]any{"id": id, "blocked_by": values(bs)}), want) != nil {
			m.tasks[id].blockers = slices.DeleteFunc(m.tasks[id].blockers, func(b int64) bool {
				return slices.ContainsFunc(bs, func(x blocker) bool { return x.id == b })
			})
		}
	case n < 66: // move
		id, to := r.id(), r.folder()
		want := either(cond(r.missing(id), "not-found", ""), cond(m.caseMismatch(to, ""), "conflict/case-mismatch", ""))
		if want == "" && m.tasks[id].folder != to {
			try := m.clone()
			try.addFolder(to)
			try.tasks[id].folder = to
			want = cond(try.acyclic(), "", "conflict/cycle")
		}
		if r.do("task move", js(map[string]any{"id": id, "to": to}), want) != nil {
			m.tasks[id].folder = to
			m.addFolder(to)
		}
	case n < 70: // folder create, sometimes with blockers and blocks
		f := r.folder()
		in := map[string]any{"folder": f}
		var bs, ds []blocker
		if f != "/" && r.rng.Intn(2) == 0 {
			if bs, ds = r.someBlockers(2), r.someBlockers(1); len(bs) > 0 {
				in["blocked_by"] = values(bs)
			}
			if len(ds) > 0 {
				in["blocks"] = values(ds)
			}
		}
		// It creates the folder with blockers and dependents.
		make := func(m *expected) int64 {
			m.addFolder(f)
			id := m.fids[f]
			for _, b := range bs {
				m.wait(id, b.id)
			}
			for _, d := range ds {
				m.wait(d.id, id)
			}
			return id
		}
		want := cond(m.caseMismatch(f, ""), "conflict/case-mismatch", "")
		switch {
		case want != "":
		case m.folders[f]:
			want = cond(len(in) > 1, "conflict/destination-exists", "")
		default:
			if want = r.blockerWant(append(slices.Clone(bs), ds...)); want == "" && len(in) > 1 {
				try := m.clone()
				make(try)
				want = cond(try.acyclic(), "", "conflict/cycle")
			}
		}
		if res := r.do("folder create", js(in), want); res != nil && !m.folders[f] {
			if id := make(m); res["id"] != float64(id) {
				r.failf("folder %s created with ID %v, want %d", f, res["id"], id)
			}
		}
	case n < 77: // folder move
		from, to := r.existingFolder(), r.folder()
		want := ""
		recase := parent(from) == parent(to) && model.SameButCase(path.Base(from), path.Base(to))
		switch {
		case from == "/" || to == "/" || (from != to && holds(from, to)) || (!recase && clash(from, to)):
			want = "invalid-input"
		case m.caseMismatch(from, ""):
			want = "conflict/case-mismatch"
		case !m.folders[from]:
			want = "not-found"
		case from == to:
		case m.caseMismatch(to, from):
			want = "conflict/case-mismatch"
		case m.folders[to]:
			want = "conflict/destination-exists"
		}
		moveFolder := func(m *expected) {
			rename := func(f string) string { return to + strings.TrimPrefix(f, from) }
			var moved []string
			for f := range m.folders {
				if holds(from, f) {
					moved = append(moved, f)
				}
			}
			ids, blocks := map[string]int64{}, map[string][]int64{}
			for _, f := range moved {
				ids[f], blocks[f] = m.fids[f], m.fblock[f]
				delete(m.folders, f)
				delete(m.fids, f)
				delete(m.fblock, f)
			}
			m.addFolder(parent(to)) // only the folders made above it are new
			for _, f := range moved {
				m.folders[rename(f)] = true
				m.fids[rename(f)], m.fblock[rename(f)] = ids[f], blocks[f]
			}
			for _, t := range m.tasks {
				if holds(from, t.folder) {
					t.folder = rename(t.folder)
				}
			}
		}
		if want == "" && from != to {
			try := m.clone()
			moveFolder(try)
			want = cond(try.acyclic(), "", "conflict/cycle")
		}
		if r.do("folder move", js(map[string]any{"folder": from, "to": to}), want) != nil && from != to {
			moveFolder(m)
		}
	case n < 82: // delete
		id := r.id()
		if r.do("task delete", js(map[string]any{"id": id}), cond(r.missing(id), "not-found", "")) != nil {
			m.remove([]int64{id})
		}
	case n < 86: // folder delete
		f, contents := r.existingFolder(), r.rng.Intn(3) > 0
		empty := true
		for g := range m.folders {
			empty = empty && (g == f || !holds(f, g))
		}
		var inside []int64
		for id, t := range m.tasks {
			if holds(f, t.folder) {
				inside = append(inside, id)
			}
		}
		want := ""
		switch {
		case f == "/":
			want = "invalid-input"
		case m.caseMismatch(f, ""):
			want = "conflict/case-mismatch"
		case !m.folders[f]:
			want = "not-found"
		case !contents && (!empty || len(inside) > 0):
			want = "conflict/not-empty"
		}
		if r.do("folder delete", js(map[string]any{"folder": f, "contents": contents}), want) != nil {
			gone := inside
			for g := range m.folders {
				if holds(f, g) {
					gone = append(gone, m.fids[g])
					delete(m.folders, g)
					delete(m.fids, g)
					delete(m.fblock, g)
				}
			}
			m.remove(gone)
		}
	case n < 91: // claim
		id := r.id()
		want := ""
		switch {
		case r.missing(id):
			want = "not-found"
		case !m.tasks[id].open:
			want = "conflict/task-done"
		case m.tasks[id].claimed:
			want = "conflict/claimed"
		}
		if r.do("task claim", js(map[string]any{"id": id}), want) != nil {
			m.tasks[id].claimed = true
		}
	case n < 96: // claim-next
		front := m.frontier()
		res := r.do("task claim-next", `{}`, "")
		switch {
		case res == nil:
		case len(front) == 0:
			if res["task"] != nil {
				r.failf("claim-next claimed %v from an empty frontier", res["task"])
			}
		case res["task"] == nil || int64(task(res)["id"].(float64)) != front[0]:
			r.failf("claim-next gave %v, want task %d", res["task"], front[0])
		default:
			m.tasks[front[0]].claimed = true
		}
	case n < 104: // folder block
		f, bs := r.existingFolder(), r.someBlockers(2)
		if len(bs) == 0 {
			return
		}
		want := ""
		switch {
		case f == "/":
			want = "invalid-input"
		case m.caseMismatch(f, ""):
			want = "conflict/case-mismatch"
		case !m.folders[f]:
			want = "not-found"
		default:
			if want = r.blockerWant(bs); want == "" {
				try := m.clone()
				for _, b := range bs {
					if !slices.Contains(try.fblock[f], b.id) {
						try.fblock[f] = append(try.fblock[f], b.id)
					}
				}
				want = cond(try.acyclic(), "", "conflict/cycle")
			}
		}
		if r.do("folder block", js(map[string]any{"folder": f, "blocked_by": values(bs)}), want) != nil {
			for _, b := range bs {
				if !slices.Contains(m.fblock[f], b.id) {
					m.fblock[f] = append(m.fblock[f], b.id)
				}
			}
		}
	case n < 106: // folder unblock
		f, bs := r.existingFolder(), r.someBlockers(2)
		if len(bs) == 0 || f == "/" || !m.folders[f] {
			return
		}
		paths := slices.DeleteFunc(slices.Clone(bs), func(b blocker) bool { _, isPath := b.value.(string); return !isPath })
		if r.do("folder unblock", js(map[string]any{"folder": f, "blocked_by": values(bs)}), r.blockerWant(paths)) != nil {
			m.fblock[f] = slices.DeleteFunc(m.fblock[f], func(b int64) bool {
				return slices.ContainsFunc(bs, func(x blocker) bool { return x.id == b })
			})
		}
	case n < 114: // batch: two folder entries, one waiting on the other, and a task
		f1, f2 := path.Join(r.folder(), "p"), path.Join(r.folder(), "q")
		tf := []string{f1, f2, r.folder()}[r.rng.Intn(3)]
		// A blocker path the batch makes as needed is a folder of the batch.
		made := func(b blocker) bool {
			p, isPath := b.value.(string)
			return isPath && b.id == 0 && (holds(p, f1) || holds(p, f2) || holds(p, tf))
		}
		b1, b2 := slices.DeleteFunc(r.someBlockers(1), made), slices.DeleteFunc(r.someBlockers(1), made)
		on1 := r.rng.Intn(2) == 0
		e2 := values(b2)
		if on1 {
			e2 = append(e2, f1)
		}
		var tb []any
		if r.rng.Intn(2) == 0 {
			tb = []any{[]string{f1, f2}[r.rng.Intn(2)]}
		}
		paths := []string{f1, f2, tf}
		for _, b := range append(slices.Clone(b1), b2...) {
			if p, isPath := b.value.(string); isPath {
				paths = append(paths, p)
			}
		}
		if slices.ContainsFunc(paths, func(p string) bool { return m.caseMismatch(p, "") }) {
			return // the case rules are tried elsewhere
		}
		want := ""
		for i, a := range paths {
			for _, b := range paths[i+1:] {
				if clash(a, b) {
					want = "invalid-input"
				}
			}
		}
		// It makes the batch: the folders, then the task.
		make := func(m *expected) int64 {
			m.addFolder(f1, f2, tf)
			for _, b := range b1 {
				m.wait(m.fids[f1], b.id)
			}
			for _, b := range b2 {
				m.wait(m.fids[f2], b.id)
			}
			if on1 {
				m.wait(m.fids[f2], m.fids[f1])
			}
			t := &mtask{folder: tf, open: true, tags: []string{}}
			for _, p := range tb {
				t.blockers = append(t.blockers, m.fids[p.(string)])
			}
			m.lastID++
			m.tasks[m.lastID] = t
			return m.lastID
		}
		switch {
		case want != "":
		case f1 == f2:
			want = "invalid-input"
		case m.folders[f1] || m.folders[f2]:
			want = "conflict/destination-exists"
		default:
			if want = r.blockerWant(append(slices.Clone(b1), b2...)); want == "" {
				try := m.clone()
				make(try)
				want = cond(try.acyclic(), "", "conflict/cycle")
			}
		}
		res := r.do("batch", js(map[string]any{
			"folders": []any{map[string]any{"folder": f1, "blocked_by": values(b1)}, map[string]any{"folder": f2, "blocked_by": e2}},
			"tasks":   []any{map[string]any{"title": "x", "folder": tf, "blocked_by": append([]any{}, tb...)}},
		}), want)
		if res != nil {
			if id := make(m); !slices.Equal(ids(res["ids"]), []int64{id}) {
				r.failf("batch made task %v, want %d", res["ids"], id)
			}
			for f, id := range res["folders"].(map[string]any) {
				if m.fids[f] != int64(id.(float64)) {
					r.failf("batch made %s with ID %v, want %d", f, id, m.fids[f])
				}
			}
		}
	default: // release
		id := r.id()
		was := !r.missing(id) && m.tasks[id].claimed
		if res := r.do("task release", js(map[string]any{"id": id}), ""); res != nil {
			if res["changed"] != was {
				r.failf("release %d: changed %v, want %v", id, res["changed"], was)
			}
			if was {
				m.tasks[id].claimed = false
			}
		}
	}
}

// created records a new task, whose folders are recorded, and checks that
// it took the next ID.
func (r *random) created(id int64, t *mtask) {
	if id != r.m.lastID+1 {
		r.failf("task %d created, want ID %d: the next after the folders made", id, r.m.lastID+1)
	}
	r.m.lastID = id
	if t.tags == nil {
		t.tags = []string{}
	}
	r.m.tasks[id] = t
}

// compare checks everything koan reports about the tree against the model.
func (r *random) compare() {
	r.t.Helper()
	m := r.m
	res := r.w.ok("task list", `{"readiness":["ready","blocked","done"]}`)

	// Folders, in tree order: a parent before its children, siblings by
	// name, byte for byte. folder list leaves out the root it lists.
	want := []string{}
	for f := range m.folders {
		want = append(want, f)
	}
	slices.SortFunc(want, treeOrder)
	got := []string{"/"}
	for _, f := range r.w.ok("folder list", `{}`)["folders"].([]any) {
		got = append(got, f.(map[string]any)["path"].(string))
	}
	if !slices.Equal(got, want) {
		r.failf("folders %v, want %v", got, want)
	}

	// Tasks, in tree order, each as the model has it.
	pos := map[string]int{}
	for i, f := range want {
		pos[f] = i
	}
	wantIDs := m.ids()
	slices.SortStableFunc(wantIDs, func(a, b int64) int { return pos[m.tasks[a].folder] - pos[m.tasks[b].folder] })
	if g := ids(res["tasks"]); !slices.Equal(g, wantIDs) {
		r.failf("list gave %v, want %v", g, wantIDs)
		return
	}
	for _, x := range res["tasks"].([]any) {
		tk := x.(map[string]any)
		id := int64(tk["id"].(float64))
		mt := m.tasks[id]
		var pri any
		if mt.priority != nil {
			pri = float64(*mt.priority)
		}
		blockers := slices.Clone(mt.blockers)
		slices.Sort(blockers)
		tags := slices.Clone(mt.tags)
		slices.Sort(tags)
		claimed := mt.claimed && mt.open
		if tk["folder"] != mt.folder || tk["readiness"] != m.readiness(mt) || tk["priority"] != pri ||
			(tk["claim"] != nil) != claimed || (tk["completed_at"] == nil) != mt.open ||
			!slices.Equal(ids(tk["blocked_by"]), blockers) || !slices.Equal(ids(tk["open_blockers"]), m.openBlockers(mt)) ||
			js(tk["tags"]) != js(tags) {
			r.failf("task %d is %v\n  the model has %+v, readiness %s, open blockers %v", id, tk, *mt, m.readiness(mt), m.openBlockers(mt))
		}
	}
	for _, x := range r.w.ok("folder list", `{}`)["folders"].([]any) {
		fr := x.(map[string]any)
		f := fr["path"].(string)
		blockers := slices.Clone(m.fblock[f])
		slices.Sort(blockers)
		if fr["id"] != float64(m.fids[f]) || !slices.Equal(ids(fr["blocked_by"]), blockers) || fr["readiness"] != m.folderReadiness(f) {
			r.failf("folder %s is %v\n  the model has ID %d, blocked_by %v, readiness %s", f, fr, m.fids[f], blockers, m.folderReadiness(f))
		}
	}

	if g := ids(r.w.ok("task frontier", `{}`)["tasks"]); !slices.Equal(g, m.frontier()) {
		r.failf("frontier %v, want %v", g, m.frontier())
	}
	info := r.w.ok("info", `{}`)
	claims := 0
	for _, t := range m.tasks {
		if t.claimed {
			claims++
		}
	}
	if info["last_id"] != float64(m.lastID) || info["claims"] != float64(claims) {
		r.failf("info says last_id %v and %v claims, want %d and %d", info["last_id"], info["claims"], m.lastID, claims)
	}
}
