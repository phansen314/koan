package main_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/testrun"
)

// The sizes PRD Testing benchmarks at, in live tasks.
var perfSizes = []int{1000, 10000}

// perfRoot is a root of a benchmark's size: folders of chained tasks,
// a third of each chain complete and some of the rest claimed, under ten
// top folders.
type perfRoot struct {
	tb      testing.TB
	home    string
	folders []int64   // each chain's folder
	tasks   [][]int64 // each folder's tasks, in chain order
	moving  int64     // a folder only the folder move moves
	mover   int64     // a task only the task move moves
	away    map[int64]bool
	dir     string // a directory outside the tree, for markers
	seq     int
}

// perfBase is where benchmark roots go: KOAN_BENCH_DIR, or the user's
// cache directory, since the temp directory may be held in memory, which
// makes every flush free.
func perfBase(tb testing.TB) string {
	tb.Helper()
	base := os.Getenv("KOAN_BENCH_DIR")
	if base == "" {
		c, err := os.UserCacheDir()
		if err != nil {
			tb.Fatal(err)
		}
		base = c
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		tb.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "koan-bench-")
	if err != nil {
		tb.Fatal(err)
	}
	return dir
}

var (
	perfMu    sync.Mutex
	perfRoots = map[int]*perfRoot{}
	perfDirs  []string
)

// perfCleanup removes the benchmark roots, once the test or benchmark
// that timed them is done.
func perfCleanup() {
	perfMu.Lock()
	defer perfMu.Unlock()
	for _, d := range perfDirs {
		os.RemoveAll(d)
	}
	perfDirs, perfRoots = nil, map[int]*perfRoot{}
}

// getPerfRoot builds a root of n live tasks once, and hands it out again
// after: the commands timed keep its size.
func getPerfRoot(tb testing.TB, n int) *perfRoot {
	tb.Helper()
	perfMu.Lock()
	defer perfMu.Unlock()
	if r := perfRoots[n]; r != nil {
		r.tb = tb
		return r
	}
	base := perfBase(tb)
	perfDirs = append(perfDirs, base)
	home := filepath.Join(base, "home")
	os.Mkdir(home, 0o755)
	r := &perfRoot{tb: tb, home: home, away: map[int64]bool{}, dir: filepath.Join(base, "code")}
	os.Mkdir(r.dir, 0o755)
	start := time.Now()
	r.must("init")
	// 100 tasks a folder, its two gates among them.
	const chain = 98
	for f := range n / (chain + 2) {
		path := fmt.Sprintf("/p%d/f%d", f%10, f)
		var tasks []any
		for k := range chain {
			x := map[string]any{"label": fmt.Sprintf("$t%d", k), "title": fmt.Sprintf("task %d of %s", k, path)}
			if k == 0 {
				x["blocked_by"] = []string{path + ".start"}
			}
			if k < chain-1 {
				x["blocks"] = []string{fmt.Sprintf("$t%d", k+1)}
			} else {
				x["blocks"] = []string{path + ".done"}
			}
			if k%7 == 0 {
				x["tags"] = []string{"seven"}
			}
			tasks = append(tasks, x)
		}
		b, _ := json.Marshal(map[string]any{"folders": []any{map[string]any{"path": path}}, "folder": path, "tasks": tasks})
		res := r.input("batch", string(b))
		var ids []int64
		for _, id := range res["tasks"].([]any) {
			ids = append(ids, num(id))
		}
		r.tasks = append(r.tasks, ids)
		r.folders = append(r.folders, num(res["folders"].([]any)[0].(map[string]any)["id"]))
	}
	// A third of each chain complete, the start gate with it; a claim on
	// every tenth folder's first open task.
	var stmts []string
	for f, ids := range r.tasks {
		stmts = append(stmts, fmt.Sprintf(
			"UPDATE task SET completed_at = updated_at WHERE (folder = %d AND gate = 'start') OR id BETWEEN %d AND %d", r.folders[f], ids[0], ids[chain/3]))
		if f%10 == 0 {
			stmts = append(stmts, fmt.Sprintf("INSERT INTO claim (task, claimed_at, extra) VALUES (%d, '2026-10-20T10:00:00Z', '{}')", ids[chain/3+1]))
		}
	}
	r.db(stmts...)
	r.moving = num(r.must("folder", "create", "/mv/f")["created"].([]any)[1].(map[string]any)["id"])
	r.mover = num(r.must("task", "create", "mover", "--folder", "/mv/f")["id"])
	info := r.must("info")
	tb.Logf("a root of %v live tasks and %v folders, built in %v, in %s", info["tasks"], info["folders"], time.Since(start).Round(time.Millisecond), home)
	perfRoots[n] = r
	return r
}

func num(v any) int64 {
	n, _ := v.(json.Number).Int64()
	return n
}

// run runs koan in the root, and gives its envelope.
func (r *perfRoot) run(stdin string, args ...string) *testrun.Result {
	r.tb.Helper()
	return testrun.Run{Home: r.home, Stdin: strings.NewReader(stdin)}.Koan(r.tb, args...)
}

// must runs a command that must succeed, and gives its result.
func (r *perfRoot) must(args ...string) map[string]any {
	r.tb.Helper()
	res := r.run("", args...)
	if !res.OK() {
		r.tb.Fatalf("%q: %s", args, res.Line())
	}
	return res.Result()
}

func (r *perfRoot) input(command, stdin string) map[string]any {
	r.tb.Helper()
	res := r.run(stdin, command, "-i", "-")
	if !res.OK() {
		r.tb.Fatalf("%s: %s", command, res.Line())
	}
	return res.Result()
}

func (r *perfRoot) db(stmts ...string) {
	r.tb.Helper()
	d, err := sql.Open("sqlite", filepath.Join(r.home, "koan-root", "koan.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		r.tb.Fatal(err)
	}
	defer d.Close()
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			r.tb.Fatalf("%s: %v", s, err)
		}
	}
}

// task is the kth task of the chain in folder f, by ID.
func (r *perfRoot) task(f, k int) string { return fmt.Sprint(r.tasks[f%len(r.tasks)][k]) }

// folder is folder f, by ID.
func (r *perfRoot) folder(f int) string { return fmt.Sprint("@", r.folders[f%len(r.folders)]) }

// perfOp is one command timed: prep readies the root for the ith run,
// untimed, and args is the command. Every op can run any number of times.
type perfOp struct {
	name string
	// budget is the PRD's for the command, given how many parts its
	// input has; none for backup.
	budget time.Duration
	// once is that the test times it once, as backup, which is slow
	// and has no budget.
	once  bool
	prep  func(r *perfRoot, i int)
	args  func(r *perfRoot, i int) []string
	stdin func(r *perfRoot, i int) string
}

const ms = time.Millisecond

func always(args ...string) func(*perfRoot, int) []string {
	return func(*perfRoot, int) []string { return args }
}

// batchParts is how many tasks the timed batch makes.
const batchParts = 100

// perfOps is every command, at the PRD's budget: 100 ms each, doctor and
// repair 1 s, a batch 100 ms and 1 ms a part, and backup none.
var perfOps = []perfOp{
	{name: "task show", budget: 100 * ms, args: func(r *perfRoot, i int) []string { return []string{"task", "show", r.task(i, 50)} }},
	{name: "task list", budget: 100 * ms, args: always("task", "list")},
	{name: "task list all", budget: 100 * ms, args: always("task", "list", "--state", "open,complete", "--limit", "null")},
	{name: "task frontier", budget: 100 * ms, args: always("task", "frontier")},
	{name: "task frontier all", budget: 100 * ms, args: always("task", "frontier", "--limit", "null", "--recursive")},
	{name: "task why", budget: 100 * ms, args: func(r *perfRoot, i int) []string { return []string{"task", "why", r.task(i, 97)} }},
	{name: "folder show", budget: 100 * ms, args: func(r *perfRoot, i int) []string { return []string{"folder", "show", r.folder(i)} }},
	{name: "folder list", budget: 100 * ms, args: always("folder", "list", "--limit", "null", "--recursive")},
	{name: "where", budget: 100 * ms, args: func(r *perfRoot, i int) []string { return []string{"where", "@" + r.task(i, 50)} }},
	{name: "where --file", budget: 100 * ms, args: func(r *perfRoot, i int) []string {
		return []string{"where", "--file", filepath.Join(r.home, "koan-root", "tree", "@p3", "@f3", "@"+r.task(3, 50)+".md")}
	}},
	{name: "context", budget: 100 * ms, args: func(r *perfRoot, i int) []string { return []string{"context", r.folder(i)} }},
	{name: "info", budget: 100 * ms, args: always("info")},
	{name: "version", budget: 100 * ms, args: always("version")},
	{name: "init", budget: 100 * ms, args: always("init")},
	{name: "task create", budget: 100 * ms, args: func(r *perfRoot, i int) []string {
		return []string{"task", "create", fmt.Sprint("new ", i), "--folder", r.folder(i), "--blocked-by", r.task(i, 97), "--notes", "notes"}
	}},
	{name: "task update", budget: 100 * ms, args: func(r *perfRoot, i int) []string {
		return []string{"task", "update", r.task(i, 50), "--title", fmt.Sprint("updated ", i), "--tags-add", "x"}
	}},
	{name: "task complete", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.must("task", "reopen", r.task(i, 96)) },
		args: func(r *perfRoot, i int) []string { return []string{"task", "complete", r.task(i, 96)} }},
	{name: "task reopen", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.must("task", "complete", r.task(i, 95)) },
		args: func(r *perfRoot, i int) []string { return []string{"task", "reopen", r.task(i, 95)} }},
	{name: "task block", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.must("task", "unblock", r.task(i, 94), "--blocked-by", r.task(i+1, 10)) },
		args: func(r *perfRoot, i int) []string {
			return []string{"task", "block", r.task(i, 94), "--blocked-by", r.task(i+1, 10)}
		}},
	{name: "task unblock", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.must("task", "block", r.task(i, 93), "--blocked-by", r.task(i, 92)) },
		args: func(r *perfRoot, i int) []string {
			return []string{"task", "unblock", r.task(i, 93), "--blocked-by", r.task(i, 92)}
		}},
	{name: "task move", budget: 100 * ms, args: func(r *perfRoot, i int) []string {
		to := fmt.Sprint("@", r.moving)
		if r.away[r.mover] = !r.away[r.mover]; r.away[r.mover] {
			to = "/"
		}
		return []string{"task", "move", fmt.Sprint(r.mover), "--to", to}
	}},
	{name: "task delete", budget: 100 * ms,
		prep: func(r *perfRoot, i int) {
			r.seq = int(num(r.must("task", "create", "doomed", "--folder", r.folder(i), "--blocks", r.task(i, 97))["id"]))
		},
		args: func(r *perfRoot, i int) []string { return []string{"task", "delete", fmt.Sprint(r.seq)} }},
	{name: "task archive", budget: 100 * ms,
		prep: func(r *perfRoot, i int) {
			r.seq = int(num(r.must("task", "create", "done", "--folder", r.folder(i), "--notes", "notes")["id"]))
			r.must("task", "complete", fmt.Sprint(r.seq))
		},
		args: func(r *perfRoot, i int) []string { return []string{"task", "archive", fmt.Sprint(r.seq)} }},
	{name: "task claim", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.must("task", "release", r.task(i, 90)) },
		args: func(r *perfRoot, i int) []string { return []string{"task", "claim", r.task(i, 90)} }},
	{name: "task claim-next", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.must("task", "release", r.task(i, 33)) },
		args: func(r *perfRoot, i int) []string { return []string{"task", "claim-next", "--folder", r.folder(i)} }},
	{name: "task release", budget: 100 * ms,
		prep: func(r *perfRoot, i int) {
			if res := r.run("", "task", "claim", r.task(i, 89)); !res.OK() && res.Error()["rule"] != "claimed" {
				r.tb.Fatal(res.Line())
			}
		},
		args: func(r *perfRoot, i int) []string { return []string{"task", "release", r.task(i, 89)} }},
	{name: "folder create", budget: 100 * ms, args: func(r *perfRoot, i int) []string {
		r.seq++
		return []string{"folder", "create", fmt.Sprintf("/new/n%d/x", r.seq), "--notes", "notes"}
	}},
	{name: "folder update", budget: 100 * ms, args: func(r *perfRoot, i int) []string {
		return []string{"folder", "update", r.folder(i), "--title", fmt.Sprint("folder ", i)}
	}},
	{name: "folder move", budget: 100 * ms, args: func(r *perfRoot, i int) []string {
		to := "/mv/f"
		if r.away[r.moving] = !r.away[r.moving]; r.away[r.moving] {
			to = "/moved/f"
		}
		return []string{"folder", "move", fmt.Sprint(r.moving), "--to", to}
	}},
	{name: "folder delete", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.seq = r.smallFolder("/del", false) },
		args: func(r *perfRoot, i int) []string {
			return []string{"folder", "delete", fmt.Sprint(r.seq), "--contents"}
		}},
	{name: "folder archive", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.seq = r.smallFolder("/arc", true) },
		args: func(r *perfRoot, i int) []string { return []string{"folder", "archive", fmt.Sprint(r.seq)} }},
	{name: "batch", budget: 100*ms + batchParts*ms, args: always("batch", "-i", "-"), stdin: func(r *perfRoot, i int) string {
		r.seq++
		var tasks []any
		for k := range batchParts {
			x := map[string]any{"label": fmt.Sprintf("$t%d", k), "title": "batched"}
			if k > 0 {
				x["blocked_by"] = []string{fmt.Sprintf("$t%d", k-1)}
			}
			tasks = append(tasks, x)
		}
		path := fmt.Sprintf("/batched/b%d", r.seq)
		b, _ := json.Marshal(map[string]any{"folders": []any{map[string]any{"path": path}}, "folder": path, "tasks": tasks})
		return string(b)
	}},
	{name: "marker bind", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.must("marker", "unbind", "--dir", r.dir) },
		args: func(r *perfRoot, i int) []string {
			return []string{"marker", "bind", r.folder(i), "--dir", r.dir, "--force"}
		}},
	{name: "marker unbind", budget: 100 * ms,
		prep: func(r *perfRoot, i int) { r.must("marker", "bind", r.folder(i), "--dir", r.dir, "--force") },
		args: func(r *perfRoot, i int) []string { return []string{"marker", "unbind", "--dir", r.dir} }},
	{name: "doctor", budget: time.Second, args: always("doctor")},
	{name: "repair", budget: time.Second, args: always("repair")},
	{name: "backup", once: true, args: func(r *perfRoot, i int) []string {
		r.seq++
		return []string{"backup", filepath.Join(filepath.Dir(r.home), fmt.Sprint("backup-", r.seq))}
	}},
}

// smallFolder makes a folder of 20 tasks with notes under top, complete
// when done says, and gives its ID.
func (r *perfRoot) smallFolder(top string, done bool) int {
	r.seq++
	path := fmt.Sprintf("%s/s%d", top, r.seq)
	var tasks []any
	for range 20 {
		tasks = append(tasks, map[string]any{"title": "small"})
	}
	b, _ := json.Marshal(map[string]any{"folders": []any{map[string]any{"path": path}}, "folder": path, "tasks": tasks})
	res := r.input("batch", string(b))
	id := num(res["folders"].([]any)[len(res["folders"].([]any))-1].(map[string]any)["id"])
	if done {
		r.db(fmt.Sprintf("UPDATE task SET completed_at = updated_at WHERE folder = %d", id))
	}
	return int(id)
}

// time runs op once, its prep untimed, and gives how long the command's
// process took, failing on any error or warning.
func (op perfOp) time(r *perfRoot, i int) time.Duration {
	r.tb.Helper()
	if op.prep != nil {
		op.prep(r, i)
	}
	stdin := ""
	if op.stdin != nil {
		stdin = op.stdin(r, i)
	}
	args := op.args(r, i)
	// The process alone is timed, not the test decoding its output.
	p := &proc{args: args, stdin: stdin}
	p.run(testrun.Binary(r.tb), r.home)
	if p.err != nil || !p.ok() || len(p.env["warnings"].([]any)) != 0 {
		r.tb.Fatalf("%s %q: %v: %s", op.name, args, p.err, p.line)
	}
	return p.elapsed
}

// BenchmarkCommands times every command, as a process, at each size PRD
// Testing names. Its roots go where perfBase says.
func BenchmarkCommands(b *testing.B) {
	b.Cleanup(perfCleanup)
	for _, n := range perfSizes {
		for _, op := range perfOps {
			b.Run(fmt.Sprintf("%d/%s", n, strings.ReplaceAll(op.name, " ", "-")), func(b *testing.B) {
				r := getPerfRoot(b, n)
				var total time.Duration
				for i := range b.N {
					total += op.time(r, i)
				}
				b.ReportMetric(0, "ns/op")
				b.ReportMetric(float64(total.Microseconds())/float64(b.N)/1000, "ms/command")
			})
		}
	}
}

// The PRD's budget, as a test: each command, run five times at each size,
// takes no longer than Goals allows, by its median run, so one run slowed
// by the machine fails nothing. -short skips it.
func TestPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("builds roots of 10,000 tasks")
	}
	if os.Getenv("KOAN_CASEFOLD") != "" {
		t.Skip("timed on the machine's own disk, not the casefold mount")
	}
	t.Cleanup(perfCleanup)
	const runs = 5
	for _, n := range perfSizes {
		r := getPerfRoot(t, n)
		var over []string
		var table []string
		for _, op := range perfOps {
			var ds []time.Duration
			for i := range runs {
				if op.once && i > 0 {
					break
				}
				ds = append(ds, op.time(r, i))
			}
			slices.Sort(ds)
			median := ds[len(ds)/2]
			budget := "-"
			if op.budget > 0 {
				budget = op.budget.String()
				if median > op.budget {
					over = append(over, fmt.Sprintf("%s: %v, over %v", op.name, median.Round(100*time.Microsecond), op.budget))
				}
			}
			table = append(table, fmt.Sprintf("%-18s %8.1f ms median %8.1f ms max   budget %s",
				op.name, float64(median.Microseconds())/1000, float64(ds[len(ds)-1].Microseconds())/1000, budget))
		}
		t.Logf("%d live tasks, %s/%s:\n%s", n, runtime.GOOS, runtime.GOARCH, strings.Join(table, "\n"))
		for _, o := range over {
			t.Errorf("%d live tasks: %s", n, o)
		}
	}
}
