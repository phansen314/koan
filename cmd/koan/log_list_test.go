package main_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// logResult is log list's result.
type logResult struct {
	Events    []eventRecord `json:"events"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated"`
	LastSeq   *int64        `json:"last_seq"`
}

// logList runs log list, which must succeed with no warnings, checks it
// recorded nothing, and gives its result.
func logList(t *testing.T, home string, args ...string) logResult {
	t.Helper()
	before := lastSeq(t, home)
	r := ok(t, home, append([]string{"log", "list"}, args...)...)
	var env struct {
		Result logResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Line()), &env); err != nil {
		t.Fatal(err)
	}
	if after := lastSeq(t, home); after != before {
		t.Errorf("log list %q recorded events: the last seq went from %d to %d", args, before, after)
	}
	return env.Result
}

// seqs are the events' seqs, in order.
func seqs(events []eventRecord) []int64 {
	out := []int64{}
	for _, e := range events {
		out = append(out, e.Seq)
	}
	return out
}

// where is the seqs of the events in the table that keep keeps.
func where(t *testing.T, home string, keep func(e eventRecord) bool) []int64 {
	t.Helper()
	out := []int64{}
	for _, e := range logged(t, home, 0, nil) {
		if keep(e) {
			out = append(out, e.Seq)
		}
	}
	return out
}

// newID runs a command that makes something and gives the ID its result
// names.
func newID(t *testing.T, home string, args ...string) int64 {
	t.Helper()
	r := ok(t, home, args...)
	return num(r.Result()["id"])
}

// matches checks a log list returns exactly want, all of them, oldest
// first.
func matches(t *testing.T, home string, want []int64, args ...string) logResult {
	t.Helper()
	got := logList(t, home, append(args, "--limit", "null")...)
	if !slices.Equal(seqs(got.Events), want) {
		t.Errorf("log list %q: seqs %v, want %v", args, seqs(got.Events), want)
	}
	if got.Total != len(want) || got.Truncated {
		t.Errorf("log list %q: total %d, truncated %v, want %d, false", args, got.Total, got.Truncated, len(want))
	}
	return got
}

// logTree is the history the filters are tried on:
//
//	/work/api/v2    made with its parents /work and /work/api
//	/work/api-v2    a sibling whose name starts with api
//	A in /work/api, behind /work/api's start gate
//	B in /work/api/v2, C in /work/api-v2
//	/work renamed /Work; then A claimed, D made in /Work/api, A deleted
//	/work/api-v2 deleted with its contents
type logTree struct {
	home                  string
	work, api, v2, apiV2  int64
	a, b, c, d, startGate int64
	renamedAt             int64 // the seq of the rename's event
}

func makeLogTree(t *testing.T) logTree {
	t.Helper()
	l := logTree{home: root(t)}
	home := l.home
	l.v2 = newID(t, home, "folder", "create", "/work/api/v2")
	l.work, l.api = l.v2-2, l.v2-1
	l.apiV2 = newID(t, home, "folder", "create", "/work/api-v2")
	l.a = newID(t, home, "task", "create", "A", "--folder", "/work/api")
	l.b = newID(t, home, "task", "create", "B", "--folder", "/work/api/v2")
	l.c = newID(t, home, "task", "create", "C", "--folder", "/work/api-v2")
	r := ok(t, home, "block", fmt.Sprint(l.a), "--blocked-by", "/work/api.start")
	l.startGate = num(r.Result()["gates_made"].([]any)[0].(map[string]any)["id"])
	l.renamedAt = lastSeq(t, home) + 1
	ok(t, home, "mv", "-T", "/work", "/Work")
	ok(t, home, "claim", fmt.Sprint(l.a))
	l.d = newID(t, home, "task", "create", "D", "--folder", "/Work/api")
	ok(t, home, "delete", fmt.Sprint(l.a))
	ok(t, home, "delete", "/Work/api-v2", "--contents")
	return l
}

// pathUnder is whether a stored path is p or under it, byte for byte.
func pathUnder(path, p string) bool {
	return p == "/" || path == p || strings.HasPrefix(path, p+"/")
}

// The folder subject as a path: itself and under it, byte for byte, and
// / for everything; a path that names no folder now still finds its
// history (spec log list).
func TestLogListPath(t *testing.T) {
	l := makeLogTree(t)
	home := l.home
	all := where(t, home, func(eventRecord) bool { return true })
	matches(t, home, all, "/")
	matches(t, home, all)

	api := matches(t, home, where(t, home, func(e eventRecord) bool { return pathUnder(e.Folder.Path, "/work/api") }), "/work/api")
	var sawV2 bool
	for _, e := range api.Events {
		if strings.HasPrefix(e.Folder.Path, "/work/api-v2") || strings.HasPrefix(e.Folder.Path, "/Work") {
			t.Errorf("/work/api matched an event at %s", e.Folder.Path)
		}
		sawV2 = sawV2 || e.Folder.Path == "/work/api/v2"
	}
	if !sawV2 {
		t.Errorf("/work/api matched nothing under /work/api/v2: %v", seqs(api.Events))
	}

	// After the rename, what was stored as /Work/api, and nothing else.
	upper := matches(t, home, where(t, home, func(e eventRecord) bool { return pathUnder(e.Folder.Path, "/Work/api") }), "/Work/api")
	if len(upper.Events) == 0 {
		t.Fatal("/Work/api matched nothing")
	}
	for _, e := range upper.Events {
		if e.Seq < l.renamedAt {
			t.Errorf("/Work/api matched seq %d, from before the rename", e.Seq)
		}
	}

	// A deleted folder's path finds its history: made, and C made in
	// it, which left it unfinished, at the path it had then; both deleted
	// at the path it had after the rename.
	made := matches(t, home, where(t, home, func(e eventRecord) bool { return pathUnder(e.Folder.Path, "/work/api-v2") }), "/work/api-v2")
	deleted := matches(t, home, where(t, home, func(e eventRecord) bool { return pathUnder(e.Folder.Path, "/Work/api-v2") }), "/Work/api-v2")
	for _, c := range []struct {
		got   logResult
		kinds []string
	}{{made, []string{"created", "created", "unfinished"}}, {deleted, []string{"deleted", "deleted"}}} {
		var kinds []string
		for _, e := range c.got.Events {
			kinds = append(kinds, e.Kind)
		}
		if !slices.Equal(kinds, c.kinds) {
			t.Errorf("kinds %v, want %v", kinds, c.kinds)
		}
	}
	matches(t, home, []int64{}, "/nowhere")
}

// The folder subject as an ID: that folder's own events and its tasks',
// gates included, through renames and after a delete; never its
// subfolders' (spec log list).
func TestLogListFolderID(t *testing.T) {
	l := makeLogTree(t)
	home := l.home
	for _, form := range []string{"%d", "@%d"} {
		api := matches(t, home, where(t, home, func(e eventRecord) bool { return e.Folder.ID != nil && *e.Folder.ID == l.api }),
			fmt.Sprintf(form, l.api))
		about := map[int64]bool{}
		for _, e := range api.Events {
			if e.ID != nil {
				about[*e.ID] = true
			}
		}
		for _, id := range []int64{l.api, l.a, l.d, l.startGate} {
			if !about[id] {
				t.Errorf("%s: no event about @%d", fmt.Sprintf(form, l.api), id)
			}
		}
		for _, id := range []int64{l.v2, l.b, l.work} {
			if about[id] {
				t.Errorf("%s: an event about @%d, which isn't in it", fmt.Sprintf(form, l.api), id)
			}
		}
	}
	// A deleted folder's ID: its own events and C's.
	gone := matches(t, home, where(t, home, func(e eventRecord) bool { return e.Folder.ID != nil && *e.Folder.ID == l.apiV2 }),
		fmt.Sprint(l.apiV2))
	if len(gone.Events) != 5 {
		t.Errorf("@%d: %d events, want made and deleted for it and C, and its unfinished once C was made", l.apiV2, len(gone.Events))
	}
	// An ID never issued matches nothing, and is no error.
	matches(t, home, []int64{}, "999999")
	// Through --input, an ID as an integer.
	r := testrun.Run{Home: home, Stdin: strings.NewReader(fmt.Sprintf(`{"folder": %d, "limit": null}`, l.api))}.Koan(t, "log", "list", "-i", "-")
	r.Check(t, "log list")
	if !r.OK() || int(num(r.Result()["total"])) != len(where(t, home, func(e eventRecord) bool { return e.Folder.ID != nil && *e.Folder.ID == l.api })) {
		t.Errorf("--input folder %d: %s", l.api, r.Line())
	}
}

// --task: the events about that task, never a folder's; a deleted task's
// ID gives its whole history (spec log list).
func TestLogListTask(t *testing.T) {
	l := makeLogTree(t)
	home := l.home
	about := func(id int64) []int64 {
		return where(t, home, func(e eventRecord) bool {
			return e.ID != nil && *e.ID == id && (e.Folder.ID == nil || *e.Folder.ID != id)
		})
	}
	a := matches(t, home, about(l.a), "--task", fmt.Sprint(l.a))
	var kinds []string
	for _, e := range a.Events {
		kinds = append(kinds, e.Kind)
	}
	if len(kinds) < 3 || kinds[0] != "created" || !slices.Contains(kinds, "claimed") || kinds[len(kinds)-1] != "deleted" {
		t.Errorf("--task %d: kinds %v, want its whole history, made to deleted", l.a, kinds)
	}
	matches(t, home, about(l.a), "--task", fmt.Sprintf("@%d", l.a))
	// A folder's ID matches nothing.
	matches(t, home, []int64{}, "--task", fmt.Sprint(l.api))
	// With a folder, both filters hold.
	matches(t, home, []int64{}, "/work/api-v2", "--task", fmt.Sprint(l.a))
	matches(t, home, where(t, home, func(e eventRecord) bool { return e.ID != nil && *e.ID == l.c && e.Kind == "created" }),
		"/work/api-v2", "--task", fmt.Sprint(l.c), "--kinds", "created")
	// A gate is a task.
	matches(t, home, about(l.startGate), "--task", fmt.Sprint(l.startGate))
}

// --since is inclusive, and --kinds a set (spec log list).
func TestLogListSinceKinds(t *testing.T) {
	l := makeLogTree(t)
	home := l.home
	all := logged(t, home, 0, nil)
	for _, e := range []eventRecord{all[0], all[len(all)/2], all[len(all)-1]} {
		got := matches(t, home, where(t, home, func(x eventRecord) bool { return x.At >= e.At }), "--since", e.At)
		if !slices.Contains(seqs(got.Events), e.Seq) {
			t.Errorf("--since %s left out seq %d, at its bound", e.At, e.Seq)
		}
	}
	matches(t, home, []int64{}, "--since", "2999-01-01T00:00:00Z")
	matches(t, home, where(t, home, func(e eventRecord) bool { return e.Kind == "created" || e.Kind == "deleted" }),
		"--kinds", "deleted,created,created")
	matches(t, home, where(t, home, func(e eventRecord) bool { return e.Kind == "moved" }), "--kinds", "moved")
	matches(t, home, []int64{}, "--kinds", "rotated")
}

// --after is the cursor: after the last seq it gives nothing, past it
// not-found, and last_seq falls back to it (spec log list).
func TestLogListAfter(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "one")
	last := lastSeq(t, home)

	got := logList(t, home)
	if got.LastSeq == nil || *got.LastSeq != last || got.Total != int(last) || got.Truncated {
		t.Errorf("log list: last_seq %v, total %d, truncated %v, want %d, %d, false", got.LastSeq, got.Total, got.Truncated, last, last)
	}
	got = logList(t, home, "--after", fmt.Sprint(last))
	if len(got.Events) != 0 || got.Total != 0 || got.LastSeq == nil || *got.LastSeq != last {
		t.Errorf("--after %d: %+v, want no events and last_seq %d", last, got, last)
	}
	got = logList(t, home, "--after", "1")
	if !slices.Equal(seqs(got.Events), where(t, home, func(e eventRecord) bool { return e.Seq > 1 })) || *got.LastSeq != last {
		t.Errorf("--after 1: %v, last_seq %v", seqs(got.Events), *got.LastSeq)
	}
	// None returned and no --after: null.
	got = logList(t, home, "--kinds", "rotated")
	if got.LastSeq != nil {
		t.Errorf("--kinds rotated: last_seq %d, want null", *got.LastSeq)
	}
	// None returned, with --after: the --after given, though filters
	// left out events after it.
	got = logList(t, home, "--after", "1", "--kinds", "rotated")
	if got.LastSeq == nil || *got.LastSeq != 1 {
		t.Errorf("--after 1 --kinds rotated: last_seq %v, want 1", got.LastSeq)
	}

	// Past the last seq issued: not-found at /after, naming it.
	r := fails(t, home, "not-found", "", "log", "list", "--after", fmt.Sprint(last+1))
	if ps := problemFields(r); ps != "/after" {
		t.Errorf("--after %d: problems at %q, want /after", last+1, ps)
	}
	if !strings.Contains(message(r), fmt.Sprint(last)) {
		t.Errorf("--after %d: message %q doesn't name the last seq %d", last+1, message(r), last)
	}
	fails(t, home, "not-found", "", "log", "list", "/nowhere", "--kinds", "rotated", "--after", "9007199254740991")
}

// Paging with --limit until truncated is false sees every event once, and
// --limit defaults to 100 (spec log list).
func TestLogListPaging(t *testing.T) {
	home := root(t)
	var tasks []string
	for i := range 120 {
		tasks = append(tasks, fmt.Sprintf(`{"title": "t%d"}`, i))
	}
	batchOK(t, home, `{"tasks": [`+strings.Join(tasks, ",")+`]}`)
	all := where(t, home, func(eventRecord) bool { return true })

	got := logList(t, home)
	if len(got.Events) != 100 || got.Total != len(all) || !got.Truncated || *got.LastSeq != all[99] {
		t.Errorf("log list: %d events, total %d, truncated %v, last_seq %v; want 100, %d, true, %d",
			len(got.Events), got.Total, got.Truncated, *got.LastSeq, len(all), all[99])
	}
	got = logList(t, home, "--limit", "null")
	if !slices.Equal(seqs(got.Events), all) || got.Truncated {
		t.Errorf("--limit null: %d events, truncated %v", len(got.Events), got.Truncated)
	}

	for _, filter := range [][]string{nil, {"--kinds", "created"}, {"/"}} {
		var seen []int64
		args := append([]string{"--limit", "7"}, filter...)
		for range 100 {
			page := logList(t, home, args...)
			seen = append(seen, seqs(page.Events)...)
			if !page.Truncated {
				break
			}
			args = append([]string{"--limit", "7", "--after", fmt.Sprint(*page.LastSeq)}, filter...)
		}
		want := all
		if len(filter) > 0 && filter[0] == "--kinds" {
			want = where(t, home, func(e eventRecord) bool { return e.Kind == "created" })
		}
		if !slices.Equal(seen, want) {
			t.Errorf("paging %q: saw %d events, want %d, each once", filter, len(seen), len(want))
		}
	}
}

// What log list refuses as the input is read, and the command line it
// can't read (spec log list, Invocation).
func TestLogListRefused(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api")
	for _, c := range []struct {
		field string
		args  []string
	}{
		// Anchored paths, gates and labels need the tree to resolve.
		{"/folder", []string{"@1/api"}},
		{"/folder", []string{"/work.start"}},
		{"/folder", []string{"@1.done"}},
		{"/folder", []string{"/work:x"}},
		{"/folder", []string{"$a"}},
		{"/folder", []string{"0"}},
		{"/task", []string{"--task", "@1.done"}},
		{"/task", []string{"--task", "/work/api.start"}},
		{"/task", []string{"--task", "/work"}},
		{"/task", []string{"--task", "@1/api"}},
		{"/task", []string{"--task", "0"}},
		{"/since", []string{"--since", "2026-10-26"}},
		{"/since", []string{"--since", "2026-02-30T00:00:00Z"}},
		{"/after", []string{"--after", "0"}},
		{"/after", []string{"--after", "9007199254740992"}},
		{"/kinds/0", []string{"--kinds", "frob"}},
		{"/limit", []string{"--limit", "0"}},
		{"", []string{"--since", "2026-10-26T00:00:00Z", "--after", "1"}},
	} {
		invalidAt(t, home, c.field, append([]string{"log", "list"}, c.args...)...)
	}
	for _, in := range []struct{ field, json string }{
		{"/folder", `{"folder": "@1/api"}`},
		{"/folder", `{"folder": 0}`},
		{"/task", `{"task": "/work"}`},
		{"/task", `{"task": true}`},
		{"/after", `{"after": "1"}`},
		{"/since", `{"since": 1}`},
		{"/kinds", `{"kinds": []}`},
		{"", `{"since": "2026-10-26T00:00:00Z", "after": 1}`},
	} {
		r := testrun.Run{Home: home, Stdin: strings.NewReader(in.json)}.Koan(t, "log", "list", "-i", "-")
		r.Check(t, "log list")
		if f := problemFields(r); r.Kind() != "invalid-input" || f != in.field && !strings.HasPrefix(f, in.field+"/") {
			t.Errorf("--input %s: want invalid-input at %q: %s", in.json, in.field, r.Line())
		}
	}
	// Through --input, null since or after is no filter, so either with
	// the other's null is taken.
	for _, in := range []string{`{"since": null, "after": 1}`, `{"since": "2026-10-26T00:00:00Z", "after": null}`} {
		r := testrun.Run{Home: home, Stdin: strings.NewReader(in)}.Koan(t, "log", "list", "-i", "-")
		r.Check(t, "log list")
		if !r.OK() {
			t.Errorf("--input %s: %s", in, r.Line())
		}
	}

	usageAt(t, home, "", "log")
	usageAt(t, home, "frob", "log", "frob")
	usageAt(t, home, "show", "log", "show")
	usageAt(t, home, "--after", "log", "list", "--after", "x")
	usageAt(t, home, "--limit", "log", "list", "--limit", "1.0")
	usageAt(t, home, "/b", "log", "list", "/a", "/b")
	for _, c := range []struct {
		args []string
		opt  string
		hint string
	}{
		{[]string{"log", "list", "--folder", "/work"}, "--folder", "the folder is a word of its own"},
		{[]string{"log", "list", "--file", "."}, "--file", "matched as written"},
		{[]string{"log", "list", "--here"}, "--here", "matched as written"},
	} {
		r := usageAt(t, home, c.opt, c.args...)
		if m := message(r); !strings.Contains(m, "unknown flag: "+c.opt+"; ") || !strings.Contains(m, c.hint) {
			t.Errorf("koan %q: message %q lacks hint %q", c.args, m, c.hint)
		}
	}
}

// An event's data is printed as every result is, without escaping <, >
// and &.
func TestLogListNoHTMLEscape(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "a<b & c>d")
	r := ok(t, home, "log", "list", "--kinds", "created")
	if line := r.Line(); !strings.Contains(line, `"title":"a<b & c>d"`) {
		t.Errorf("log list printed the title escaped: %s", line)
	}
}
