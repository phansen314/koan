package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/store"
	"github.com/phansen314/koan/internal/testrun"
)

// proc is one koan process of a concurrency run, and what it gave.
type proc struct {
	args    []string
	stdin   string
	line    string
	exit    int
	elapsed time.Duration
	env     map[string]any
	err     error
}

// run runs the process, as testrun does, but reports a failure to start
// in err rather than failing the test, so it can run off the test's
// goroutine.
func (p *proc) run(bin, home string) {
	cmd := exec.Command(bin, p.args...)
	cmd.Dir = home
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "PWD=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+home, "PWD="+home)
	cmd.Stdin = strings.NewReader(p.stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	start := time.Now()
	err := cmd.Run()
	p.elapsed = time.Since(start)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		p.exit = ee.ExitCode()
	} else if err != nil {
		p.err = err
		return
	}
	p.line = strings.TrimSuffix(out.String(), "\n")
	dec := json.NewDecoder(strings.NewReader(p.line))
	dec.UseNumber()
	if err := dec.Decode(&p.env); err != nil {
		p.err = fmt.Errorf("not one line of JSON: %v: %q", err, out.String())
	}
}

func (p *proc) ok() bool { return p.env["ok"] == true }

func (p *proc) kind() string {
	e, _ := p.env["error"].(map[string]any)
	k, _ := e["kind"].(string)
	return k
}

func (p *proc) result() map[string]any {
	r, _ := p.env["result"].(map[string]any)
	return r
}

// refPath is the folder path in a ref, @5 (/b/b3).
var refPath = regexp.MustCompile(`\((.*)\)$`)

// The PRD's Concurrency test, with the Backup and restore test's backup
// taken during it: hundreds of processes write and read one root at
// once. No claim is handed out twice, every read sees a committed state,
// never part of a batch or a delete, busy comes only after 5 seconds of
// waiting, and at the end doctor finds nothing and the invariants hold.
// A backup taken mid-run restores to a root doctor finds clean.
func TestConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("hundreds of processes")
	}
	const pool, movers, deleters, archivers, completers, batches, creators, claimers, readers = 40, 30, 20, 20, 20, 50, 30, 100, 100
	home := root(t)
	var folders, tasks []any
	folders = append(folders, map[string]any{"path": "/pool"}, map[string]any{"path": "/c"})
	for i := range pool {
		tasks = append(tasks, map[string]any{"title": fmt.Sprint("pool ", i), "folder": "/pool"})
	}
	for i := range completers {
		tasks = append(tasks, map[string]any{"title": fmt.Sprint("c ", i), "folder": "/c"})
	}
	for i := range movers {
		folders = append(folders, map[string]any{"path": fmt.Sprintf("/m%d", i)})
		tasks = append(tasks, map[string]any{"title": "m", "folder": fmt.Sprintf("/m%d", i)})
	}
	for i := range deleters {
		folders = append(folders, map[string]any{"path": fmt.Sprintf("/d%d/e", i)})
		tasks = append(tasks, map[string]any{"title": "d", "folder": fmt.Sprintf("/d%d", i)},
			map[string]any{"title": "e", "folder": fmt.Sprintf("/d%d/e", i)})
	}
	for i := range archivers {
		folders = append(folders, map[string]any{"path": fmt.Sprintf("/a%d", i)})
	}
	b, _ := json.Marshal(map[string]any{"folders": folders, "tasks": tasks})
	batchOK(t, home, string(b))
	for i := range movers {
		writeTree(t, home, fmt.Sprintf("@m%d/src/main.go", i), "package main")
	}
	for i := range archivers {
		writeTree(t, home, fmt.Sprintf("@a%d/notes.txt", i), "material")
	}
	// IDs of the /c tasks, which the completers complete.
	var cIDs []string
	for _, x := range ok(t, home, "task", "list", "--limit", "null", "--fields", "ref").Result()["tasks"].([]any) {
		x := x.(map[string]any)
		if strings.HasSuffix(x["ref"].(string), "(/c)") {
			cIDs = append(cIDs, fmt.Sprint(x["id"]))
		}
	}
	if len(cIDs) != completers {
		t.Fatalf("%d tasks in /c", len(cIDs))
	}

	var procs []*proc
	add := func(stdin string, args ...string) { procs = append(procs, &proc{args: args, stdin: stdin}) }
	for range claimers {
		add("", "task", "claim-next", "--folder", "/pool")
	}
	for i := range movers {
		add("", "folder", "move", fmt.Sprintf("/m%d", i), "--to", fmt.Sprintf("/moved/m%d", i))
	}
	for i := range deleters {
		add("", "folder", "delete", fmt.Sprintf("/d%d", i), "--contents")
	}
	for i := range archivers {
		add("", "folder", "archive", fmt.Sprintf("/a%d", i))
	}
	for _, id := range cIDs {
		add("", "task", "complete", id)
	}
	for i := range batches {
		add(fmt.Sprintf(`{"folders":[{"path":"/b/b%d"}],"folder":"/b/b%d","tasks":[
			{"label":"$x","title":"x","blocks":["$y"]},{"label":"$y","title":"y","blocks":["$z"]},{"label":"$z","title":"z"}]}`, i, i),
			"batch", "-i", "-")
	}
	for i := range creators {
		add("", "task", "create", fmt.Sprint("new ", i), "--blocked-by", cIDs[i%len(cIDs)])
	}
	for i := range readers {
		if i%2 == 0 {
			add("", "task", "list", "--state", "open,complete", "--limit", "null", "--fields", "ref,claim")
		} else {
			add("", "folder", "list", "--limit", "null", "--fields", "path")
		}
	}
	// Writers and readers interleaved, so they start together.
	order := make([]*proc, 0, len(procs))
	for i := range len(procs) {
		order = append(order, procs[(i*7919)%len(procs)])
	}
	seen := map[*proc]bool{}
	for _, p := range order {
		seen[p] = true
	}
	if len(seen) != len(procs) {
		t.Fatal("the order repeats a process")
	}
	t.Logf("%d processes", len(order))

	bin := testrun.Binary(t)
	backup := filepath.Join(t.TempDir(), "backup")
	backupRun := &proc{args: []string{"backup", backup}}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, p := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p.run(bin, home)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(50 * time.Millisecond)
		backupRun.run(bin, home)
	}()
	began := time.Now()
	close(start)
	wg.Wait()
	t.Logf("all done in %v", time.Since(began))

	claimed := map[string]int{}
	var nulls, busy int
	// A write that waited out the lock changed nothing, so what it would
	// have made, ended or claimed is counted apart, by command.
	busyBy := map[string]int{}
	for _, p := range append(order, backupRun) {
		if p.err != nil {
			t.Errorf("%q: %v", p.args, p.err)
			continue
		}
		if p.kind() == "busy" {
			busy++
			busyBy[p.args[0]+" "+p.args[1]]++
			if p.elapsed < 5*time.Second {
				t.Errorf("%q: busy after %v: %s", p.args, p.elapsed, p.line)
			}
			continue
		}
		if !p.ok() {
			t.Errorf("%q: exit %d: %s", p.args, p.exit, p.line)
			continue
		}
		for _, w := range p.env["warnings"].([]any) {
			if k := w.(map[string]any)["kind"]; k != "pending-filesystem" {
				t.Errorf("%q: warning %v: %s", p.args, k, p.line)
			}
		}
		res := p.result()
		switch p.args[0] + " " + p.args[1] {
		case "task claim-next":
			if c, _ := res["claim"].(map[string]any); c != nil {
				claimed[fmt.Sprint(c["task"])]++
			} else {
				nulls++
			}
		case "task list":
			// Every read is a committed state: a batch's folder holds
			// all three of its tasks, a folder being deleted both of
			// its own, each claim is on a task of the pool.
			counts := map[string]int{}
			claims := 0
			for _, x := range res["tasks"].([]any) {
				x := x.(map[string]any)
				counts[refPath.FindStringSubmatch(x["ref"].(string))[1]]++
				if x["claim"] != nil {
					claims++
					if !strings.HasSuffix(x["ref"].(string), "(/pool)") {
						t.Errorf("a claim on %v", x["ref"])
					}
				}
			}
			for path, n := range counts {
				if strings.HasPrefix(path, "/b/") && n != 3 {
					t.Errorf("a read sees %d tasks of a batch in %s", n, path)
				}
			}
			for i := range deleters {
				if d, e := counts[fmt.Sprintf("/d%d", i)], counts[fmt.Sprintf("/d%d/e", i)]; d != e {
					t.Errorf("a read sees part of a delete of /d%d: %d and %d", i, d, e)
				}
			}
			if claims > pool {
				t.Errorf("a read sees %d claims", claims)
			}
		case "folder list":
			seen := map[string]bool{}
			for _, f := range res["folders"].([]any) {
				seen[f.(map[string]any)["path"].(string)] = true
			}
			for i := range movers {
				if seen[fmt.Sprintf("/m%d", i)] == seen[fmt.Sprintf("/moved/m%d", i)] {
					t.Errorf("a read sees /m%d at neither path or both", i)
				}
			}
		}
	}
	if busy > 0 {
		t.Logf("%d busy, each after waiting 5 seconds", busy)
	}
	// No claim is handed out twice, and the pool is claimed whole.
	for id, n := range claimed {
		if n > 1 {
			t.Errorf("@%s claimed %d times", id, n)
		}
	}
	// A claimer that got busy took nothing; the pool is claimed whole
	// unless one did.
	if b := busyBy["task claim-next"]; len(claimed)+nulls+b != claimers || len(claimed) > pool || b == 0 && len(claimed) != pool {
		t.Errorf("%d claimed, %d nulls, %d busy", len(claimed), nulls, b)
	}

	// At the end the root is whole.
	if r := ok(t, home, "doctor"); len(r.Result()["findings"].([]any)) != 0 {
		t.Errorf("doctor: %s", r.Line())
	}
	if _, bad := testrun.CheckRoot(home, nil, testrun.Checks{Notes: true}); len(bad) > 0 {
		t.Errorf("invariants:\n%s", strings.Join(bad, "\n"))
	}
	r := ok(t, home, "info")
	want := pool + completers + movers + 3*(batches-busyBy["batch -i"]) + creators - busyBy["task create"] + 2*busyBy["folder delete"]
	if r.Result()["tasks"] != json.Number(fmt.Sprint(want)) {
		t.Errorf("info: want %d tasks: %s", want, r.Line())
	}

	// The backup taken mid-run restores to a root doctor finds clean.
	t.Logf("backup after %v: %s", backupRun.elapsed, backupRun.line)
	if res := backupRun.result(); res["clean"] != true {
		t.Errorf("backup: %s", backupRun.line)
	}
	restored := restore(t, backup)
	if r := ok(t, restored, "doctor"); len(r.Result()["findings"].([]any)) != 0 {
		t.Errorf("doctor on the restored root: %s", r.Line())
	}
	if _, bad := testrun.CheckRoot(restored, nil, testrun.Checks{Notes: true}); len(bad) > 0 {
		t.Errorf("the restored root's invariants:\n%s", strings.Join(bad, "\n"))
	}
	ok(t, restored, "task", "create", "after")
}

// A writer that finds the lock held waits 5 seconds, then fails with
// busy; a reader never waits on it.
func TestBusy(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 5 seconds")
	}
	home := root(t)
	ok(t, home, "task", "create", "a")
	r, _, e := store.FindRoot(home)
	if e != nil {
		t.Fatal(e.Message)
	}
	lock, e := store.TakeLock(r.Dir, r.Path)
	if e != nil {
		t.Fatal(e.Message)
	}
	bin := testrun.Binary(t)
	read := &proc{args: []string{"task", "list"}}
	read.run(bin, home)
	if !read.ok() || read.elapsed > time.Second {
		t.Errorf("a read with the lock held: %v: %s", read.elapsed, read.line)
	}
	began := time.Now()
	write := koan(t, home, "task", "create", "b")
	elapsed := time.Since(began)
	lock.Release()
	// The PRD's wait, plus slack for a loaded machine.
	if write.Kind() != "busy" || write.Exit != 1 || elapsed < 5*time.Second || elapsed > 10*time.Second {
		t.Errorf("a write with the lock held: %v: %s", elapsed, write.Line())
	}
	golden(t, write, "busy")
	ok(t, home, "task", "create", "b")
}
