package main_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// rotateResult is log rotate's result.
type rotateResult struct {
	Changed  bool    `json:"changed"`
	File     *string `json:"file"`
	FirstSeq *int64  `json:"first_seq"`
	LastSeq  *int64  `json:"last_seq"`
	Count    int     `json:"count"`
}

// rotate runs log rotate, which must succeed with no warnings, and gives
// its result.
func rotate(t *testing.T, home, before string) rotateResult {
	t.Helper()
	r := ok(t, home, "log", "rotate", "--before", before)
	var env struct {
		Result rotateResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Line()), &env); err != nil {
		t.Fatal(err)
	}
	return env.Result
}

// stampEvents sets the time of every event of the changes from first to last,
// as if they were made then: a hand edit, past the trigger that keeps
// events from changing.
func stampEvents(t *testing.T, home string, first, last int64, at string) {
	t.Helper()
	db(t, home, "DROP TRIGGER event_fixed",
		fmt.Sprintf("UPDATE event SET at = '%s' WHERE change BETWEEN %d AND %d", at, first, last),
		"CREATE TRIGGER event_fixed BEFORE UPDATE ON event BEGIN SELECT RAISE(ABORT, 'events are never changed'); END")
}

// logDir is a root's log/.
func logDir(home string) string { return filepath.Join(home, "koan-root", "log") }

var koanLogName = regexp.MustCompile(`^[0-9]{16}-[0-9]{16}\.jsonl$`)

// fileLines reads a rotated file's lines, each checked against the
// log-file schema and its order.
func fileLines(t *testing.T, file string) []string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := testrun.Load(t)
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if err := s.Validate("log-file", "", line); err != nil {
			t.Errorf("%s: a line fails log-file: %v\n%s", file, err, line)
		}
		if err := s.Order("log-file", "", line); err != nil {
			t.Errorf("%s: %v\n%s", file, err, line)
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// covered checks that a root's rotated files and its table hold every
// seq from 1 to the last issued exactly once, the files' names giving
// their ranges, and that no temp file is left. dir is the root's
// directory.
func covered(t *testing.T, dir string) {
	t.Helper()
	count := map[int64]int{}
	entries, err := os.ReadDir(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("%s left in log/", e.Name())
		}
		if !koanLogName.MatchString(e.Name()) {
			continue
		}
		var first, last int64
		fmt.Sscanf(e.Name(), "%016d-%016d.jsonl", &first, &last)
		lines := fileLines(t, filepath.Join(dir, "log", e.Name()))
		var seqs []int64
		for _, line := range lines {
			var ev struct {
				Seq int64 `json:"seq"`
			}
			json.Unmarshal([]byte(line), &ev)
			seqs = append(seqs, ev.Seq)
			count[ev.Seq]++
		}
		if len(seqs) == 0 || seqs[0] != first || seqs[len(seqs)-1] != last || int64(len(seqs)) != last-first+1 {
			t.Errorf("%s holds seqs %v", e.Name(), seqs)
		}
	}
	d, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "koan.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.Query("SELECT seq FROM event ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	var max int64
	for rows.Next() {
		var seq int64
		rows.Scan(&seq)
		count[seq]++
		max = seq
	}
	rows.Close()
	for seq := int64(1); seq <= max; seq++ {
		if count[seq] != 1 {
			t.Errorf("seq %d is held %d times", seq, count[seq])
		}
	}
	if len(count) != int(max) {
		t.Errorf("%d seqs held, the last %d", len(count), max)
	}
}

// rotateBase is a root with a history stamped in the past, then one
// change made now. It gives the home and the last seq stamped.
func rotateBase(t *testing.T) (string, int64) {
	t.Helper()
	home := root(t)
	ok(t, home, "folder", "create", "/work/api")
	a := fmt.Sprint(newID(t, home, "task", "create", "A <b> & c", "--folder", "/work/api", "--blocked-by", "/work.start"))
	ok(t, home, "claim", a)
	ok(t, home, "update", a, "--title", "A, again", "--tags-add", "x")
	last := lastSeq(t, home)
	stampEvents(t, home, 1, last, "2020-01-01T00:00:00Z")
	ok(t, home, "task", "create", "Now")
	return home, last
}

// A normal run: the oldest events, up to the first at or after --before,
// move to log/<first>-<last>.jsonl, one line each, the Event record as
// log list printed it with schema first; their rows go, and rotated is
// recorded after every event the table kept (spec log rotate, On-disk
// formats).
func TestLogRotate(t *testing.T) {
	home, last := rotateBase(t)
	hand(t, os.Remove(logDir(home)))
	var listed struct {
		Result struct {
			Events []json.RawMessage `json:"events"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(ok(t, home, "log", "list", "--limit", "null").Line()), &listed)
	printed := listed.Result.Events
	kept := lastSeq(t, home)

	got := rotate(t, home, "2021-01-01T00:00:00Z")
	file := filepath.Join(logDir(home), fmt.Sprintf("%016d-%016d.jsonl", 1, last))
	if !got.Changed || got.File == nil || *got.File != file || *got.FirstSeq != 1 || *got.LastSeq != last || got.Count != int(last) {
		t.Fatalf("result %+v, want %s, 1 to %d", got, file, last)
	}
	lines := fileLines(t, file)
	if int64(len(lines)) != last {
		t.Fatalf("%d lines, want %d", len(lines), last)
	}
	for i, line := range lines {
		if want := `{"schema":1,` + string(printed[i][1:]); line != want {
			t.Errorf("line %d:\n%s\nwant, as log list printed it,\n%s", i, line, want)
		}
	}
	// Printed as log list prints, without escaping <, > and &.
	if !strings.Contains(strings.Join(lines, "\n"), `"title":"A <b> & c"`) {
		t.Errorf("the title isn't written as log list prints it:\n%s", strings.Join(lines, "\n"))
	}

	table := logged(t, home, 0, nil)
	if len(table) != int(kept-last)+1 || table[0].Seq != last+1 {
		t.Fatalf("table holds %v", seqs(table))
	}
	r := table[len(table)-1]
	if r.Seq != kept+1 || r.Kind != "rotated" || r.Command != "log rotate" || r.ID != nil || r.Folder.ID != nil || r.Folder.Path != "/" {
		t.Errorf("last event %+v", r)
	}
	var data rotateResult
	json.Unmarshal(r.Data, &data)
	if *data.File != file || *data.FirstSeq != 1 || *data.LastSeq != last || data.Count != int(last) {
		t.Errorf("rotated's data %s", r.Data)
	}
	covered(t, filepath.Join(home, "koan-root"))
}

// With nothing before the cut, changed is false, file and the seqs null,
// count 0, and nothing is recorded (spec log rotate).
func TestLogRotateNothing(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "A")
	before := lastSeq(t, home)
	r := ok(t, home, "log", "rotate", "--before", "2020-01-01T00:00:00Z")
	if r.Line() != `{"ok":true,"result":{"changed":false,"file":null,"first_seq":null,"last_seq":null,"count":0},"warnings":[]}` {
		t.Errorf("got %s", r.Line())
	}
	if lastSeq(t, home) != before {
		t.Errorf("recorded an event with nothing to rotate")
	}
	if entries, _ := os.ReadDir(logDir(home)); len(entries) != 0 {
		t.Errorf("log/ holds %d entries", len(entries))
	}
}

// --before is required, a timestamp, and never in the future: each is
// invalid-input at /before (spec log rotate).
func TestLogRotateInput(t *testing.T) {
	home := root(t)
	invalidAt(t, home, "/before", "log", "rotate")
	invalidAt(t, home, "/before", "log", "rotate", "--before", "2999-01-01T00:00:00Z")
	invalidAt(t, home, "/before", "log", "rotate", "--before", "2026-10-26")
	invalidAt(t, home, "/before", "log", "rotate", "--before", "2026-02-30T00:00:00Z")
	for _, in := range []string{`{}`, `{"before": null}`, `{"before": 1}`} {
		r := testrun.Run{Home: home, Stdin: strings.NewReader(in)}.Koan(t, "log", "rotate", "-i", "-")
		ps, _ := r.Error()["problems"].([]any)
		if r.Kind() != "invalid-input" || len(ps) == 0 || ps[0].(map[string]any)["field"] != "/before" {
			t.Errorf("%s: %s", in, r.Line())
		}
	}
	if lastSeq(t, home) != 1 {
		t.Errorf("a refused rotate recorded events")
	}
}

// A clock set back between changes still moves a prefix of seq: what
// moves is everything before the first event at or after --before
// (PRD Rotating the log).
func TestLogRotateClockBack(t *testing.T) {
	home := root(t)                    // 1
	ok(t, home, "task", "create", "A") // 2
	ok(t, home, "task", "create", "B") // 3
	ok(t, home, "task", "create", "C") // 4
	stampEvents(t, home, 1, 2, "2020-01-01T00:00:10Z")
	stampEvents(t, home, 3, 3, "2020-01-01T00:00:05Z") // the clock set back
	stampEvents(t, home, 4, 4, "2020-01-01T00:00:20Z")

	// seq 3 is before the cut, but seq 1 is not: nothing moves.
	if got := rotate(t, home, "2020-01-01T00:00:08Z"); got.Changed {
		t.Errorf("moved %+v past an event after the cut", got)
	}
	got := rotate(t, home, "2020-01-01T00:00:15Z")
	if !got.Changed || *got.FirstSeq != 1 || *got.LastSeq != 3 {
		t.Errorf("got %+v, want 1 to 3", got)
	}
	covered(t, filepath.Join(home, "koan-root"))
}

// Every row rotated: the table holds only the rotated event, and the
// next write's seq carries on after it (PRD Testing, Log).
func TestLogRotateEveryRow(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "A")
	stampEvents(t, home, 1, 2, "2020-01-01T00:00:00Z")
	rotate(t, home, "2021-01-01T00:00:00Z") // 3
	stampEvents(t, home, 3, 3, "2020-06-01T00:00:00Z")
	got := rotate(t, home, "2021-01-01T00:00:00Z") // the rotated event alone, then 4
	if *got.FirstSeq != 3 || *got.LastSeq != 3 {
		t.Fatalf("got %+v", got)
	}
	if table := logged(t, home, 0, nil); len(table) != 1 || table[0].Seq != 4 {
		t.Fatalf("table holds %v", seqs(table))
	}
	stampEvents(t, home, 4, 4, "2020-07-01T00:00:00Z")
	rotate(t, home, "2021-01-01T00:00:00Z") // 5
	ok(t, home, "task", "create", "B")      // 6
	if table := logged(t, home, 0, nil); !slices.Equal(seqs(table), []int64{5, 6}) {
		t.Errorf("table holds %v", seqs(table))
	}
	if r := ok(t, home, "info"); num(r.Result()["last_seq"]) != 6 {
		t.Errorf("info: %s", r.Line())
	}
	covered(t, filepath.Join(home, "koan-root"))
}

// A file that isn't koan's, or one of koan's name whose range isn't all
// in the table, is left alone: rotate judges nothing else in log/ (PRD
// Rotating the log).
func TestLogRotateOthers(t *testing.T) {
	home, last := rotateBase(t)
	others := map[string]string{
		"notes.txt": "mine",
		fmt.Sprintf("%016d-%016d.jsonl.bak", 1, last): "a copy",
		fmt.Sprintf("%016d-%016d.jsonl", 1, 9999):     "from another root",
		"1-2.jsonl": "short",
	}
	for name, content := range others {
		hand(t, os.WriteFile(filepath.Join(logDir(home), name), []byte(content), 0o644))
	}
	rotate(t, home, "2021-01-01T00:00:00Z")
	for name, content := range others {
		if b, err := os.ReadFile(filepath.Join(logDir(home), name)); err != nil || string(b) != content {
			t.Errorf("%s: %q, %v", name, b, err)
		}
	}
}

// rotateCrash runs log rotate in a helper that crashes at point at, or
// runs to its end when at is -1.
func rotateCrash(t *testing.T, home, before string, at int) []string {
	t.Helper()
	points, code := crashRun(t, home, crashCase{args: []string{"log", "rotate", "--before", before}}, at)
	if at >= 0 && code != crashExit {
		t.Fatalf("crash at %d: exit %d", at, code)
	}
	return points
}

// A crash at each point of log rotate, then a rerun with the same
// --before, with another, or with nothing to rotate, leaves every event
// exactly once, in the table or in a file; a leftover temp file and a
// cut-short run's file are removed (PRD Rotating the log, Testing).
func TestLogRotateCrash(t *testing.T) {
	tmpl, last := rotateBase(t)
	stampEvents(t, tmpl, 1, 2, "2019-01-01T00:00:00Z")
	points := rotateCrash(t, clone(t, tmpl), "2021-01-01T00:00:00Z", -1)
	for _, want := range []string{"log written", "log renamed", "commit"} {
		if !slices.Contains(points, want) {
			t.Fatalf("points %q lack %q", points, want)
		}
	}
	for at, point := range points {
		reruns := map[string]string{
			"same":    "2021-01-01T00:00:00Z",
			"earlier": "2019-06-01T00:00:00Z",
			"nothing": "2018-01-01T00:00:00Z",
		}
		for name, before := range reruns {
			t.Run(fmt.Sprintf("%s/%s", point, name), func(t *testing.T) {
				home := clone(t, tmpl)
				rotateCrash(t, home, "2021-01-01T00:00:00Z", at)
				got := rotate(t, home, before)
				covered(t, filepath.Join(home, "koan-root"))
				entries, _ := os.ReadDir(logDir(home))
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				committed := point == "committed"
				switch {
				case committed && name == "same":
					// The first run finished; the rerun moves its rotated
					// event only if it was before the cut, which it isn't.
					if got.Changed || len(names) != 1 {
						t.Errorf("rerun %+v, log/ %q", got, names)
					}
				case name == "nothing" && !committed:
					if got.Changed || len(names) != 0 {
						t.Errorf("rerun %+v, log/ %q: the cut-short run's file stays", got, names)
					}
				case name == "earlier" && !committed:
					if !got.Changed || *got.LastSeq >= last || len(names) != 1 {
						t.Errorf("rerun %+v, log/ %q", got, names)
					}
				case name == "same":
					if !got.Changed || *got.LastSeq != last || len(names) != 1 {
						t.Errorf("rerun %+v, log/ %q", got, names)
					}
				}
			})
		}
	}
}

// The PRD's Backup and restore test for the log: a backup copies log/,
// counted in files and bytes, restores to a root doctor finds clean,
// whose log reads on from where it was.
func TestBackupLog(t *testing.T) {
	home, last := rotateBase(t)
	rotate(t, home, "2021-01-01T00:00:00Z")
	hand(t, os.WriteFile(filepath.Join(logDir(home), "notes.txt"), []byte("mine"), 0o600))
	dir := filepath.Join(home, "copy")
	r := ok(t, home, "backup", dir)
	if r.Result()["clean"] != true {
		t.Errorf("backup: %s", r.Line())
	}
	files, bytes := regular(t, dir)
	if r.Result()["files"] != json.Number(fmt.Sprint(files)) || r.Result()["bytes"] != json.Number(fmt.Sprint(bytes)) {
		t.Errorf("counted %d files, %d bytes: %s", files, bytes, r.Line())
	}
	if got, want := everything(t, filepath.Join(dir, "log")), everything(t, logDir(home)); !slices.Equal(got, want) {
		t.Errorf("log/:\n got %q\nwant %q", got, want)
	}
	covered(t, dir)
	want := logList(t, home, "--limit", "null")
	restored := restore(t, dir)
	if f := ok(t, restored, "doctor").Result()["findings"].([]any); len(f) != 0 {
		t.Errorf("doctor on the restored root: %v", f)
	}
	got := logList(t, restored, "--limit", "null")
	if !slices.Equal(seqs(got.Events), seqs(want.Events)) || got.Events[0].Seq != last+1 {
		t.Errorf("restored log %v, want %v", seqs(got.Events), seqs(want.Events))
	}

	// A root without log/ makes a backup without one.
	hand(t, os.RemoveAll(logDir(home)))
	ok(t, home, "backup", filepath.Join(home, "copy2"))
	if _, err := os.Lstat(filepath.Join(home, "copy2", "log")); err == nil {
		t.Errorf("the backup made log/ the root lacks")
	}
}

// A backup taken while log rotate runs holds a table and log/ that agree,
// since both copy and rotate hold the lock (PRD Concurrency, Testing).
func TestBackupWhileRotating(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrency")
	}
	home := root(t)
	const changes = 40
	for i := range changes {
		ok(t, home, "task", "create", fmt.Sprint("T", i))
	}
	for i := int64(1); i <= changes+1; i++ {
		stampEvents(t, home, i, i, fmt.Sprintf("2020-01-01T00:%02d:00Z", i))
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 2; i <= changes+1; i += 2 {
			r := koan(t, home, "log", "rotate", "--before", fmt.Sprintf("2020-01-01T00:%02d:00Z", i))
			if !r.OK() {
				t.Errorf("rotate: %s", r.Line())
			}
		}
	}()
	for i := range 8 {
		dir := filepath.Join(home, fmt.Sprint("copy", i))
		r := ok(t, home, "backup", dir)
		if r.Result()["clean"] != true {
			t.Errorf("backup %d: %s", i, r.Line())
		}
		covered(t, dir)
	}
	wg.Wait()
	covered(t, filepath.Join(home, "koan-root"))
}
