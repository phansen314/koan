package ops

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestRandomCommands runs long random sequences of commands against a tree
// and, after every one, compares what koan says with a small model of what
// the specs promise: which commands succeed and how the others fail, every
// task's folder, readiness, blockers, and claim, the frontier's order, the
// folders, and that IDs are never reused. doctor must stay healthy
// throughout.
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
	lastID  int64
}

func (m *expected) addFolder(f string) {
	for ; f != "/"; f = parent(f) {
		m.folders[f] = true
	}
	m.folders["/"] = true
}

func parent(f string) string {
	if i := strings.LastIndex(f, "/"); i > 0 {
		return f[:i]
	}
	return "/"
}

func holds(f, g string) bool { return f == "/" || g == f || strings.HasPrefix(g, f+"/") }

func (m *expected) ids() []int64 {
	ids := []int64{}
	for id := range m.tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (m *expected) blocking(t *mtask) []int64 {
	out := []int64{}
	if !t.open {
		return out // a done task waits on nothing
	}
	for _, b := range t.blockers {
		if m.tasks[b].open {
			out = append(out, b)
		}
	}
	slices.Sort(out)
	return out
}

func (m *expected) readiness(t *mtask) string {
	switch {
	case !t.open:
		return "done"
	case len(m.blocking(t)) > 0:
		return "blocked"
	}
	return "ready"
}

// waitsOn reports whether task `from` waits on `to`, directly or not.
func (m *expected) waitsOn(from, to int64) bool {
	seen := map[int64]bool{}
	var walk func(int64) bool
	walk = func(n int64) bool {
		if n == to {
			return true
		}
		if seen[n] {
			return false
		}
		seen[n] = true
		for _, b := range m.tasks[n].blockers {
			if walk(b) {
				return true
			}
		}
		return false
	}
	return walk(from)
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

func (m *expected) remove(gone []int64) {
	for _, id := range gone {
		delete(m.tasks, id)
	}
	for _, t := range m.tasks {
		t.blockers = slices.DeleteFunc(t.blockers, func(b int64) bool { return slices.Contains(gone, b) })
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
		m: &expected{tasks: map[int64]*mtask{}, folders: map[string]bool{"/": true}}}
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
// and nest. Two differ only in case, where the filesystem tells them apart.
var folderNames = func() []string {
	if runtime.GOOS == "darwin" {
		return []string{"a", "b", "c", "d"}
	}
	return []string{"a", "b", "c", "A"}
}()

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

func (r *random) someIDs(max int) []int64 {
	out := []int64{}
	for n := r.rng.Intn(max + 1); n > 0; n-- {
		if id := r.id(); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (r *random) missing(ids ...int64) bool {
	return slices.ContainsFunc(ids, func(id int64) bool { return r.m.tasks[id] == nil })
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// do runs a command and checks that it failed the way the model says, or
// succeeded. want is "" for success, a kind, or "conflict/<rule>".
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
	if got != want {
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
	switch n := r.rng.Intn(100); {
	case n < 18: // create
		folder, blockers := r.folder(), r.someIDs(2)
		var pri *int64
		if r.rng.Intn(2) == 0 {
			p := int64(r.rng.Intn(5) - 1)
			pri = &p
		}
		want := cond(r.missing(blockers...), "not-found", "")
		res := r.do("create", js(map[string]any{"title": "t", "folder": folder, "blocked_by": blockers, "priority": pri}), want)
		if res != nil {
			r.created(int64(task(res)["id"].(float64)), &mtask{folder: folder, open: true, blockers: blockers, priority: pri})
		}
	case n < 24: // create-batch: a chain of three in two folders
		f1, f2 := r.folder(), r.folder()
		res := r.do("create-batch", js(map[string]any{"folder": f1, "tasks": []any{
			map[string]any{"ref": "x", "title": "x"},
			map[string]any{"ref": "y", "title": "y", "folder": f2, "blocked_by": []any{"x"}},
			map[string]any{"title": "z", "blocked_by": []any{"x", "y"}},
		}}), "")
		if res != nil {
			got := ids(res["ids"])
			r.created(got[0], &mtask{folder: f1, open: true})
			r.created(got[1], &mtask{folder: f2, open: true, blockers: []int64{got[0]}})
			r.created(got[2], &mtask{folder: f1, open: true, blockers: []int64{got[0], got[1]}})
		}
	case n < 30: // update
		id := r.id()
		tags := [][]string{{}, {"x"}, {"x", "y"}}[r.rng.Intn(3)]
		p := int64(r.rng.Intn(5) - 1)
		if r.do("update", js(map[string]any{"id": id, "priority": p, "tags_replace_all": tags}), cond(r.missing(id), "not-found", "")) != nil {
			m.tasks[id].priority, m.tasks[id].tags = &p, tags
		}
	case n < 40: // done
		id := r.id()
		if r.do("done", js(map[string]any{"id": id}), cond(r.missing(id), "not-found", "")) != nil {
			m.tasks[id].open, m.tasks[id].claimed = false, false
		}
	case n < 44: // reopen
		id := r.id()
		if r.do("reopen", js(map[string]any{"id": id}), cond(r.missing(id), "not-found", "")) != nil {
			m.tasks[id].open = true
		}
	case n < 54: // block
		id, bs := r.id(), r.someIDs(2)
		bs = slices.DeleteFunc(bs, func(b int64) bool { return b == id })
		if len(bs) == 0 {
			return
		}
		want := ""
		switch {
		case r.missing(append([]int64{id}, bs...)...):
			want = "not-found"
		case slices.ContainsFunc(bs, func(b int64) bool {
			return !slices.Contains(m.tasks[id].blockers, b) && m.waitsOn(b, id)
		}):
			want = "conflict/cycle"
		}
		if r.do("block", js(map[string]any{"id": id, "blockers": bs}), want) != nil {
			for _, b := range bs {
				if !slices.Contains(m.tasks[id].blockers, b) {
					m.tasks[id].blockers = append(m.tasks[id].blockers, b)
				}
			}
		}
	case n < 58: // unblock
		id, bs := r.id(), r.someIDs(2)
		if len(bs) == 0 {
			return
		}
		if r.do("unblock", js(map[string]any{"id": id, "blockers": bs}), cond(r.missing(id), "not-found", "")) != nil {
			m.tasks[id].blockers = slices.DeleteFunc(m.tasks[id].blockers, func(b int64) bool { return slices.Contains(bs, b) })
		}
	case n < 66: // move
		id, to := r.id(), r.folder()
		if r.do("move", js(map[string]any{"id": id, "to": to}), cond(r.missing(id), "not-found", "")) != nil {
			m.tasks[id].folder = to
			m.addFolder(to)
		}
	case n < 70: // create-folder
		f := r.folder()
		if r.do("create-folder", js(map[string]any{"folder": f}), "") != nil {
			m.addFolder(f)
		}
	case n < 77: // move-folder
		from, to := r.existingFolder(), r.folder()
		want := ""
		switch {
		case from == "/" || to == "/" || (from != to && holds(from, to)):
			want = "invalid-input"
		case !m.folders[from]:
			want = "not-found"
		case from != to && m.folders[to]:
			want = "conflict/destination-exists"
		}
		if r.do("move-folder", js(map[string]any{"folder": from, "to": to}), want) != nil && from != to {
			rename := func(f string) string { return to + strings.TrimPrefix(f, from) }
			var moved []string
			for f := range m.folders {
				if holds(from, f) {
					moved = append(moved, f)
				}
			}
			for _, f := range moved {
				delete(m.folders, f)
			}
			for _, f := range moved {
				m.addFolder(rename(f))
			}
			for _, t := range m.tasks {
				if holds(from, t.folder) {
					t.folder = rename(t.folder)
				}
			}
		}
	case n < 82: // delete
		id := r.id()
		if r.do("delete", js(map[string]any{"id": id}), cond(r.missing(id), "not-found", "")) != nil {
			m.remove([]int64{id})
		}
	case n < 86: // delete-folder
		f, recursive := r.existingFolder(), r.rng.Intn(3) > 0
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
		case !m.folders[f]:
			want = "not-found"
		case !recursive && (!empty || len(inside) > 0):
			want = "conflict/not-empty"
		}
		if r.do("delete-folder", js(map[string]any{"folder": f, "recursive": recursive}), want) != nil {
			for g := range m.folders {
				if holds(f, g) {
					delete(m.folders, g)
				}
			}
			m.remove(inside)
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
		if r.do("claim", js(map[string]any{"id": id}), want) != nil {
			m.tasks[id].claimed = true
		}
	case n < 96: // claim-next
		front := m.frontier()
		res := r.do("claim-next", `{}`, "")
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
	default: // release
		id := r.id()
		was := !r.missing(id) && m.tasks[id].claimed
		if res := r.do("release", js(map[string]any{"id": id}), ""); res != nil {
			if res["changed"] != was {
				r.failf("release %d: changed %v, want %v", id, res["changed"], was)
			}
			if was {
				m.tasks[id].claimed = false
			}
		}
	}
}

// created records a new task, and checks that its ID was never used before.
func (r *random) created(id int64, t *mtask) {
	if id <= r.m.lastID {
		r.failf("ID %d was issued again: the highest so far was %d", id, r.m.lastID)
	}
	r.m.lastID = id
	if t.tags == nil {
		t.tags = []string{}
	}
	r.m.tasks[id] = t
	r.m.addFolder(t.folder)
}

// compare checks everything koan reports about the tree against the model.
func (r *random) compare() {
	r.t.Helper()
	m := r.m
	res := r.w.ok("list", `{"readiness":["ready","blocked","done"],"include_folders":true}`)

	// Folders, in tree order: a parent before its children, siblings by
	// name, byte for byte.
	want := []string{}
	for f := range m.folders {
		want = append(want, f)
	}
	slices.SortFunc(want, func(a, b string) int {
		return slices.Compare(strings.Split(strings.Trim(a, "/"), "/"), strings.Split(strings.Trim(b, "/"), "/"))
	})
	got := []string{}
	for _, f := range res["folders"].([]any) {
		got = append(got, f.(string))
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
			!slices.Equal(ids(tk["blocked_by"]), blockers) || !slices.Equal(ids(tk["blocking"]), m.blocking(mt)) ||
			js(tk["tags"]) != js(tags) {
			r.failf("task %d is %v\n  the model has %+v, readiness %s, blocking %v", id, tk, *mt, m.readiness(mt), m.blocking(mt))
		}
	}

	if g := ids(r.w.ok("frontier", `{}`)["tasks"]); !slices.Equal(g, m.frontier()) {
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
