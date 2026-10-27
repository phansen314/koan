package main_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// eventRecord is an event as log list prints it, in its schema's order.
type eventRecord struct {
	Seq     int64           `json:"seq"`
	Change  int64           `json:"change"`
	At      string          `json:"at"`
	Command string          `json:"command"`
	Kind    string          `json:"kind"`
	ID      *int64          `json:"id"`
	Folder  eventFolder     `json:"folder"`
	Data    json.RawMessage `json:"data"`
}

type eventFolder struct {
	ID   *int64 `json:"id"`
	Path string `json:"path"`
}

// rowKinds are the kinds of event recorded from making, updating, state
// and claims. Moves, deletes, edges and derived state record the rest.
var rowKinds = []string{"created", "updated", "completed", "reopened", "claimed", "unclaimed"}

// logged reads the log's table straight from the database, oldest first,
// the events after seq after whose kind is among kinds, each checked
// against the event schema.
func logged(t *testing.T, home string, after int64, kinds []string) []eventRecord {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, "koan-root", "koan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT seq, change, at, command, kind, id, folder_id, folder_path, data FROM event WHERE seq > ? ORDER BY seq", after)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	s := testrun.Load(t)
	var out []eventRecord
	for rows.Next() {
		var e eventRecord
		var id, folder sql.NullInt64
		var data string
		if err := rows.Scan(&e.Seq, &e.Change, &e.At, &e.Command, &e.Kind, &id, &folder, &e.Folder.Path, &data); err != nil {
			t.Fatal(err)
		}
		if id.Valid {
			e.ID = &id.Int64
		}
		if folder.Valid {
			e.Folder.ID = &folder.Int64
		}
		e.Data = json.RawMessage(data)
		line, _ := json.Marshal(e)
		if err := s.Validate("event", "", string(line)); err != nil {
			t.Errorf("event fails its schema: %v\n%s", err, line)
		}
		if err := s.Order("event", "", string(line)); err != nil {
			t.Errorf("event: %v\n%s", err, line)
		}
		if kinds == nil || slices.Contains(kinds, e.Kind) {
			out = append(out, e)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// lastSeq is the highest seq in the log's table.
func lastSeq(t *testing.T, home string) int64 {
	t.Helper()
	all := logged(t, home, 0, nil)
	if len(all) == 0 {
		return 0
	}
	return all[len(all)-1].Seq
}

// claimedAt is an unclaimed event's time of claiming, which golden files
// write $CLAIMED_AT.
var claimedAt = regexp.MustCompile(`"claimed_at":"[^"]*"`)

// goldenEvents compares events, with their seq and change counted from
// the first and their time written $AT, to testdata/golden/<name>.json,
// one event a line, rewriting the file when -update is set. Every event
// must share the first one's time.
func goldenEvents(t *testing.T, name string, events []eventRecord) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("[\n")
	for i, e := range events {
		if e.At != events[0].At {
			t.Errorf("golden %s: event %d at %s, not %s", name, e.Seq, e.At, events[0].At)
		}
		e.At = "$AT"
		e.Data = json.RawMessage(claimedAt.ReplaceAllString(string(e.Data), `"claimed_at":"$$CLAIMED_AT"`))
		line, _ := json.Marshal(e)
		b.Write(line)
		if i < len(events)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("]\n")
	path := filepath.Join(testrun.Repo(), "testdata", "golden", name+".json")
	if *update {
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v (run with -update to write it)", name, err)
	}
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("golden %s differs:\ngot:\n%s\nwant:\n%s", name, b.Bytes(), want)
	}
}

// oneChange checks that events are one change's: they share its first
// seq as change, and its time.
func oneChange(t *testing.T, events []eventRecord) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events")
	}
	for _, e := range events {
		if e.Change != events[0].Seq || e.At != events[0].At {
			t.Errorf("event %d: change %d at %s, not %d at %s", e.Seq, e.Change, e.At, events[0].Seq, events[0].At)
		}
	}
}

// changeOf runs a command that must succeed and gives the events of this
// slice's kinds it recorded, checked to be one change.
func changeOf(t *testing.T, home string, args ...string) []eventRecord {
	t.Helper()
	before := lastSeq(t, home)
	koanOK(t, home, args...)
	events := logged(t, home, before, rowKinds)
	if len(events) > 0 {
		oneChange(t, logged(t, home, before, nil))
	}
	return events
}

// koanOK runs a command that must succeed, warnings allowed: an edge to
// a complete task warns.
func koanOK(t *testing.T, home string, args ...string) *testrun.Result {
	t.Helper()
	r := koan(t, home, args...)
	if r.Exit != 0 || !r.OK() || r.Stderr != "" {
		t.Fatalf("koan %q: exit %d, stderr %q: %s", args, r.Exit, r.Stderr, r.Line())
	}
	return r
}

// created is what folder create, task create, block and join make,
// each a created event in the order their IDs were issued: what was
// given, then what it needed, with readiness and finished as the change
// leaves them.
func TestEventsCreated(t *testing.T) {
	home := root(t)
	// A missing parent is not given; each folder stands at itself, as
	// made. Empty, both are finished.
	goldenEvents(t, "events-folder-create", changeOf(t, home, "folder", "create", "/work/api", "--title", "API", "--extra", `{"k":1}`))
	// A task with every field, in a folder, and one at the root.
	goldenEvents(t, "events-task-create", changeOf(t, home, "task", "create", "a", "--folder", "/work/api",
		"--priority", "2", "--tags", "y,x", "--extra", `{"b":true}`))
	goldenEvents(t, "events-task-create-root", changeOf(t, home, "task", "create", "r"))
	// A gate an edge names is made, not given, and is ready; the task
	// that waits on it is blocked at the end of the change.
	goldenEvents(t, "events-task-create-gate", changeOf(t, home, "task", "create", "b", "--folder", "/work",
		"--blocked-by", "/work/api.start"))
	// block makes its subject's gate first, then the gates its edges name.
	goldenEvents(t, "events-block", changeOf(t, home, "block", "/work.done", "--blocked-by", "/work/api.done"))
	// join makes the folder's gates, its start gate first.
	koanOK(t, home, "folder", "create", "/other")
	goldenEvents(t, "events-join", changeOf(t, home, "join", "/work/api", "/other"))
	// folder create --join on a root without /s makes /s, /s/impl, its
	// gates, then /s's, in that order; holding open gates, neither folder
	// is finished.
	goldenEvents(t, "events-folder-create-join", changeOf(t, home, "folder", "create", "/s/impl", "--join", "/s"))
}

// A batch is one change: one shared change and one time, a created
// event for each folder, task and gate it makes, in the order of their
// IDs.
func TestEventsBatch(t *testing.T) {
	home := root(t)
	before := lastSeq(t, home)
	batchOK(t, home, `{"folders": [{"path": "/b/c", "title": "C"}, {"path": "/a"}],
  "tasks": [{"label": "$x", "title": "x", "folder": "/b/c", "tags": ["t"]},
            {"title": "y", "blocked_by": ["$x", "/a.done"], "priority": 1}]}`)
	all := logged(t, home, before, nil)
	oneChange(t, all)
	goldenEvents(t, "events-batch", logged(t, home, before, rowKinds))
}

// An update records each field it changes, in the record's order, before
// and after, tags and extra whole; one event per subject changed, in
// input order, as one change.
func TestEventsUpdated(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/w", "--title", "W")
	koanOK(t, home, "task", "create", "a", "--folder", "/w", "--priority", "1", "--tags", "t")
	koanOK(t, home, "task", "create", "b")
	// Three fields given, one changed: only that one is listed.
	got := changeOf(t, home, "update", "2", "--title", "a", "--priority", "1", "--tags-add", "u")
	if len(got) != 1 || string(got[0].Data) != `{"changes":[{"field":"tags","before":["t"],"after":["t","u"]}]}` {
		t.Errorf("one field of three: %+v", got)
	}
	// Every field of a task.
	goldenEvents(t, "events-update", changeOf(t, home, "update", "2", "--title", "c", "--priority", "null",
		"--tags-replace-all", "v", "--extra-merge", `{"k":[1]}`))
	// 2 is left as it was, and records nothing.
	got = changeOf(t, home, "update", "3", "2", "--title", "c")
	if len(got) != 1 || *got[0].ID != 3 {
		t.Errorf("a subject left as it was: %+v", got)
	}
	goldenEvents(t, "events-update-folder", changeOf(t, home, "update", "1", "--title", "null", "--extra-merge", `{"x":"y"}`))
	// Nothing changed records nothing.
	if got := changeOf(t, home, "update", "1", "--extra-merge", `{"x":"y"}`); len(got) != 0 {
		t.Errorf("changed false: %+v", got)
	}
}

// complete records completed for each task it completes, then unclaimed
// for its claim; reopen records reopened with its readiness as the
// change leaves it.
func TestEventsCompleteReopen(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/w")
	koanOK(t, home, "task", "create", "a", "--folder", "/w")
	koanOK(t, home, "task", "create", "b", "--folder", "/w", "--blocked-by", "2")
	koanOK(t, home, "task", "create", "c")
	koanOK(t, home, "claim", "3", "--extra", `{"by":"w"}`)
	// Several tasks, one change; the claimed one ends its claim.
	claim := logged(t, home, 0, []string{"claimed"})
	got := changeOf(t, home, "complete", "4", "2", "3")
	goldenEvents(t, "events-complete", got)
	if len(got) == 4 && !strings.Contains(string(got[3].Data), `"claimed_at":"`+claim[0].At+`"`) {
		t.Errorf("unclaimed: %s, claimed at %s", got[3].Data, claim[0].At)
	}
	// Completing a complete task records nothing.
	if got := changeOf(t, home, "complete", "2"); len(got) != 0 {
		t.Errorf("complete of a complete task: %+v", got)
	}
	// Reopened together, 3 waits on 2 once both are open: readiness is as
	// of the end of the change, whatever the input order.
	goldenEvents(t, "events-reopen", changeOf(t, home, "reopen", "3", "2"))
	if got := changeOf(t, home, "reopen", "2"); len(got) != 0 {
		t.Errorf("reopen of an open task: %+v", got)
	}
}

// claim and claim-next record claimed with the claim's extra; unclaim
// records unclaimed with when it was claimed and its extra.
func TestEventsClaims(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/w")
	koanOK(t, home, "task", "create", "a", "--folder", "/w")
	koanOK(t, home, "task", "create", "b", "--folder", "/w", "--priority", "5")
	koanOK(t, home, "task", "create", "c")
	goldenEvents(t, "events-claim", changeOf(t, home, "claim", "4", "2", "--extra", `{"by":"me"}`))
	goldenEvents(t, "events-claim-next", changeOf(t, home, "claim-next", "/w"))
	// With nothing left to claim, claim-next records nothing.
	if got := changeOf(t, home, "claim-next", "/w"); len(got) != 0 {
		t.Errorf("an empty frontier: %+v", got)
	}
	claimedAt := map[int64]string{}
	for _, e := range logged(t, home, 0, []string{"claimed"}) {
		claimedAt[*e.ID] = e.At
	}
	got := changeOf(t, home, "unclaim", "3", "2")
	if len(got) != 2 {
		t.Fatalf("unclaim: %+v", got)
	}
	for i, id := range []int64{3, 2} {
		var data struct {
			ClaimedAt string          `json:"claimed_at"`
			Extra     json.RawMessage `json:"extra"`
		}
		json.Unmarshal(got[i].Data, &data)
		if got[i].Kind != "unclaimed" || *got[i].ID != id || data.ClaimedAt != claimedAt[id] {
			t.Errorf("unclaimed %d: %+v", id, got[i])
		}
	}
	if extra := string(got[1].Data); !strings.HasSuffix(extra, `"extra":{"by":"me"}}`) {
		t.Errorf("unclaimed's extra: %s", extra)
	}
	// A task with no claim is left as it was.
	if got := changeOf(t, home, "unclaim", "2"); len(got) != 0 {
		t.Errorf("unclaim of an unclaimed task: %+v", got)
	}
}

// A refused change records nothing, whatever it got through before it
// was refused.
func TestEventsRefused(t *testing.T) {
	home := root(t)
	koanOK(t, home, "folder", "create", "/w")
	koanOK(t, home, "task", "create", "a", "--folder", "/w")
	koanOK(t, home, "task", "create", "b", "--folder", "/w", "--blocked-by", "2")
	koanOK(t, home, "claim", "3")
	koanOK(t, home, "folder", "create", "/j")
	koanOK(t, home, "block", "/j.start", "--blocked-by", "3")
	before := lastSeq(t, home)
	fails(t, home, "conflict", "exists", "folder", "create", "/w")
	fails(t, home, "conflict", "cycle", "task", "create", "c", "--blocked-by", "3", "--blocks", "2")
	fails(t, home, "conflict", "cycle", "block", "2", "--blocked-by", "/w.start,3")
	fails(t, home, "conflict", "cycle", "join", "2", "/j")
	fails(t, home, "invalid-input", "", "update", "2", "1", "--priority", "3")
	fails(t, home, "invalid-input", "", "complete", "2", "@2")
	fails(t, home, "conflict", "claimed", "claim", "2", "3")
	if got := logged(t, home, before, nil); len(got) != 0 {
		t.Errorf("refusals recorded: %+v", got)
	}
	koanOK(t, home, "complete", "2")
	before = lastSeq(t, home)
	fails(t, home, "conflict", "already-complete", "claim", "2")
	fails(t, home, "invalid-input", "", "reopen", "2", "2")
	fails(t, home, "invalid-input", "", "unclaim", "3", "3")
	r := batch(t, home, `{"folders": [{"path": "/n"}], "tasks": [{"label": "$a", "title": "a", "blocked_by": ["$b", "/n.done"]}, {"label": "$b", "title": "b", "blocked_by": ["$a"]}]}`)
	if r.Exit != 1 || r.Kind() != "conflict" {
		t.Errorf("batch: %s", r.Line())
	}
	if got := logged(t, home, before, nil); len(got) != 0 {
		t.Errorf("refusals recorded: %+v", got)
	}
}
