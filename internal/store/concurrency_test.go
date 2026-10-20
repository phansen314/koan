package store

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
)

// TestMain lets the test binary run as a helper process, so tests can
// write from more than one process at once.
func TestMain(m *testing.M) {
	if h := os.Getenv("KOAN_STORE_HELPER"); h != "" {
		if err := helper(h, os.Getenv("HOME"), os.Args[len(os.Args)-1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helper(kind, home, arg string) error {
	switch kind {
	case "writer":
		// arg is how many writes to make. Each logs B when its change
		// runs and E when its filesystem step is carried out, both under
		// the lock.
		n, _ := strconv.Atoi(arg)
		s, e := Open(home)
		if e != nil {
			return fmt.Errorf("%s: %s", e.Kind, e.Message)
		}
		defer s.Close()
		log, err := os.OpenFile(filepath.Join(home, "log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		defer log.Close()
		s.Steps = logSteps{log}
		pid := os.Getpid()
		for range n {
			_, _, e := s.Write(func(tx *Tx) (any, []cli.Warning, error) {
				fmt.Fprintf(log, "B %d\n", pid)
				if _, err := addTask(tx, core.RootID, "t"); err != nil {
					return nil, nil, err
				}
				_, err := tx.AddStep([]byte(`{}`))
				return nil, nil, err
			})
			if e != nil {
				return fmt.Errorf("%s: %s", e.Kind, e.Message)
			}
		}
	case "hold-lock":
		r, _, e := FindRoot(home)
		if e != nil {
			return fmt.Errorf("%s", e.Message)
		}
		l, e := TakeLock(r.Dir, r.Path)
		if e != nil {
			return fmt.Errorf("%s", e.Message)
		}
		defer l.Release()
		fmt.Println("ready")
		d, _ := time.ParseDuration(arg)
		time.Sleep(d)
	case "hold-db":
		db, err := sql.Open("sqlite", filepath.Join(home, "koan-root", DatabaseName))
		if err != nil {
			return err
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
			return err
		}
		fmt.Println("ready")
		d, _ := time.ParseDuration(arg)
		time.Sleep(d)
	}
	return nil
}

// logSteps carries out every step by logging E, slowly enough that an
// overlapping writer would show.
type logSteps struct{ log *os.File }

func (l logSteps) Carry(rows []Pending) Outcome {
	var out Outcome
	time.Sleep(time.Millisecond)
	fmt.Fprintf(l.log, "E %d\n", os.Getpid())
	for _, r := range rows {
		out.Done = append(out.Done, r.Seq)
	}
	return out
}

func (logSteps) Warnings(live, stuck []Pending) []cli.Warning { return nil }

// startHelper runs the test binary as a helper.
func startHelper(t *testing.T, home, kind, arg string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$", arg)
	cmd.Env = append(os.Environ(), "KOAN_STORE_HELPER="+kind, "HOME="+home)
	return cmd
}

// startHolder starts a helper that holds something, and waits until it
// does.
func startHolder(t *testing.T, home, kind string, hold time.Duration) *exec.Cmd {
	t.Helper()
	cmd := startHelper(t, home, kind, hold.String())
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("helper %s: %q %v", kind, line, err)
	}
	return cmd
}

// Two processes writing at once serialize: each change, from its
// transaction to its last filesystem step, runs whole before the other's
// starts.
func TestWritersSerialize(t *testing.T) {
	home, s := newRoot(t, 1)
	n := 40
	if testing.Short() {
		n = 10
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	outs := make([][]byte, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], errs[i] = startHelper(t, home, "writer", strconv.Itoa(n)).CombinedOutput()
		}()
	}
	wg.Wait()
	for i := range 2 {
		if errs[i] != nil {
			t.Fatalf("writer %d: %v\n%s", i, errs[i], outs[i])
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4*n {
		t.Fatalf("%d log lines, want %d", len(lines), 4*n)
	}
	pids := map[string]bool{}
	for i := 0; i < len(lines); i += 2 {
		b, e := lines[i], lines[i+1]
		if !strings.HasPrefix(b, "B ") || e != "E "+strings.TrimPrefix(b, "B ") {
			t.Fatalf("writes overlap at line %d: %q then %q", i+1, b, e)
		}
		pids[b] = true
	}
	if len(pids) != 2 {
		t.Errorf("writers seen: %v", pids)
	}
	st, e := s.Stats()
	if e != nil || st.Tasks != int64(2*n) || st.NextID != int64(2*n+1) || st.Pending != 0 {
		t.Errorf("stats %+v %+v", st, e)
	}
}

// A writer that finds the lock held waits, then fails with busy.
func TestLockBusyQuick(t *testing.T) {
	_, s := newRoot(t, 1)
	held, e := TakeLock(s.Root.Dir, s.Root.Path)
	if e != nil {
		t.Fatal(e.Message)
	}
	defer held.Release()
	if _, ok, _ := TryLock(s.Root.Dir, s.Root.Path); ok {
		t.Error("TryLock got a held lock")
	}
	old := LockWait
	LockWait = 200 * time.Millisecond
	defer func() { LockWait = old }()
	start := time.Now()
	_, _, e = s.Write(func(tx *Tx) (any, []cli.Warning, error) { t.Error("change ran without the lock"); return nil, nil, nil })
	if e == nil || e.Kind != cli.KindBusy || time.Since(start) < LockWait {
		t.Errorf("after %v: %+v", time.Since(start), e)
	}
	held.Release()
	l, ok, e := TryLock(s.Root.Dir, s.Root.Path)
	if !ok || e != nil {
		t.Errorf("TryLock on a free lock: %v %+v", ok, e)
	}
	l.Release()
}

// A writer kept waiting by another process's lock gets busy after 5
// seconds.
func TestLockBusy(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 5 seconds")
	}
	home, s := newRoot(t, 1)
	startHolder(t, home, "hold-lock", 8*time.Second)
	start := time.Now()
	_, _, e := s.Write(func(tx *Tx) (any, []cli.Warning, error) { return nil, nil, nil })
	took := time.Since(start)
	if e == nil || e.Kind != cli.KindBusy {
		t.Fatalf("got %+v", e)
	}
	if took < 5*time.Second || took > 6*time.Second {
		t.Errorf("busy after %v, want 5 s", took)
	}
}

// A writer kept waiting by SQLite itself, as by the sqlite3 tool holding
// a write transaction, gets busy after 5 seconds too.
func TestDatabaseBusy(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 5 seconds")
	}
	home, s := newRoot(t, 1)
	startHolder(t, home, "hold-db", 8*time.Second)
	// Reads don't wait on the writer.
	start := time.Now()
	if _, e := s.Stats(); e != nil || time.Since(start) > time.Second {
		t.Errorf("read waited %v: %+v", time.Since(start), e)
	}
	start = time.Now()
	_, _, e := s.Write(func(tx *Tx) (any, []cli.Warning, error) { return nil, nil, nil })
	took := time.Since(start)
	if e == nil || e.Kind != cli.KindBusy {
		t.Fatalf("got %+v", e)
	}
	if took < 5*time.Second || took > 6*time.Second {
		t.Errorf("busy after %v, want 5 s", took)
	}
}

// A read sees the committed state while a write transaction is open, and
// doesn't wait for it.
func TestReadDoesNotWait(t *testing.T) {
	home, s := newRoot(t, 1)
	db := rawDB(t, home)
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.ExecContext(t.Context(), "BEGIN IMMEDIATE")
	conn.ExecContext(t.Context(), "INSERT INTO issued (id, kind, state) VALUES (1, 'task', 'live')")
	start := time.Now()
	st, e := s.Stats()
	if e != nil || st.NextID != 1 || time.Since(start) > time.Second {
		t.Errorf("read during a write: %+v %+v after %v", st, e, time.Since(start))
	}
	conn.ExecContext(t.Context(), "COMMIT")
	if st, _ := s.Stats(); st.NextID != 2 {
		t.Errorf("after commit: %+v", st)
	}
}
