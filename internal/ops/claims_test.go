package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/schematest"
	"github.com/phansen314/koan/internal/store"
)

// claimsRel is the claims file's path under the home.
const claimsRel = "cfg/" + store.ClaimsName

// tok is the n-th token the tests' fixed source issues.
func tok(n int) string { return fmt.Sprintf("%032x", n) }

// fixedTokens gives f a token source that issues tok(1), tok(2), …, and
// returns the tokens issued so far.
func (f *fixture) fixedTokens() *[]string {
	issued := &[]string{}
	f.env.Token = func() string {
		t := tok(len(*issued) + 1)
		*issued = append(*issued, t)
		return t
	}
	return issued
}

// claimsFile writes the claims file for the fixture's root: each entry is
// "id" for a claim without a token, "id:token" for one with. Each has its
// own claimed_at and extra, so a rewrite that loses either shows.
func (f *fixture) claimsFile(entries ...string) {
	f.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "{\n  \"schema\": 1,\n  \"root\": %q,\n  \"claims\": {", f.root)
	for i, e := range entries {
		id, token, locked := strings.Cut(e, ":")
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n    %q: {\n      \"claimed_at\": \"2026-09-25T09:00:%02dZ\",\n      \"extra\": {\n        \"by\": \"holder-%s\",\n        \"n\": 1.50\n      }", id, i, id)
		if locked {
			fmt.Fprintf(&b, ",\n      \"token\": %q", token)
		}
		b.WriteString("\n    }")
	}
	if len(entries) > 0 {
		b.WriteString("\n  ")
	}
	b.WriteString("}\n}\n")
	f.write(claimsRel, b.String())
}

// claimed is the IDs the claims file holds an entry for, as "1 2:locked 3",
// or "<none>" when there is no file.
func (f *fixture) claimed() string {
	f.t.Helper()
	data := f.read(claimsRel)
	if data == "<none>" {
		return data
	}
	var file struct {
		Claims map[string]struct {
			Token string `json:"token"`
		} `json:"claims"`
	}
	if err := json.Unmarshal([]byte(data), &file); err != nil {
		f.t.Fatalf("claims file: %v\n%s", err, data)
	}
	var ids []int
	for k := range file.Claims {
		var id int
		fmt.Sscan(k, &id)
		ids = append(ids, id)
	}
	slices.Sort(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
		if file.Claims[fmt.Sprint(id)].Token != "" {
			parts[i] += ":locked"
		}
	}
	return strings.Join(parts, " ")
}

// claimEntries is each entry of the claims file as it is written, by ID.
func (f *fixture) claimEntries() map[string]string {
	f.t.Helper()
	var file struct {
		Claims map[string]json.RawMessage `json:"claims"`
	}
	if data := f.read(claimsRel); data != "<none>" {
		if err := json.Unmarshal([]byte(data), &file); err != nil {
			f.t.Fatalf("claims file: %v\n%s", err, data)
		}
	}
	out := map[string]string{}
	for id, raw := range file.Claims {
		out[id] = string(raw)
	}
	return out
}

// do runs op on doc over f, checking the envelope, the error, and, on
// success, op's output schema. It returns the envelope and its line, the home
// as "~".
func (f *fixture) do(op, doc string) (Envelope, string) {
	f.t.Helper()
	e := Run(op, parse(f.t, doc), nil, f.env)
	l := line(f.t, e)
	if e.OK {
		b, err := jsonio.MarshalLine(e.Result)
		if err != nil {
			f.t.Fatal(err)
		}
		schema := op + "-output"
		if op == "create" {
			schema = "task" // create returns the shared Task schema
		}
		if ok, fl := schematest.Check(f.t, schema, b); !ok {
			f.t.Errorf("%s rejects at %s: %s", schema, fl, b)
		}
	} else {
		b, err := jsonio.MarshalLine(e.Error)
		if err != nil {
			f.t.Fatal(err)
		}
		if ok, fl := schematest.Check(f.t, "error", b); !ok {
			f.t.Errorf("error rejects at %s: %s", fl, b)
		}
	}
	return e, f.rel(l)
}

// outcome is e in short: "ok", or the error as "kind" or "conflict rule
// ids claims", each claim "null", "claim", or "locked".
func outcome(t *testing.T, l string) string {
	t.Helper()
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Kind    string `json:"kind"`
			Details struct {
				Rule   string `json:"rule"`
				IDs    []int  `json:"ids"`
				Claims []*struct {
					Locked bool `json:"locked"`
				} `json:"claims"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(l), &env); err != nil {
		t.Fatalf("%v: %s", err, l)
	}
	if env.OK {
		return "ok"
	}
	if env.Error.Kind != "conflict" {
		return env.Error.Kind
	}
	out := fmt.Sprintf("conflict %s %v", env.Error.Details.Rule, env.Error.Details.IDs)
	for _, c := range env.Error.Details.Claims {
		switch {
		case c == nil:
			out += " null"
		case c.Locked:
			out += " locked"
		default:
			out += " claim"
		}
	}
	return out
}

// warningKinds is the kinds of l's warnings, in order.
func warningKinds(t *testing.T, l string) string {
	t.Helper()
	var env struct {
		Warnings []struct {
			Kind  string   `json:"kind"`
			Paths []string `json:"paths"`
			IDs   []int    `json:"ids"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(l), &env); err != nil {
		t.Fatalf("%v: %s", err, l)
	}
	parts := []string{}
	for _, w := range env.Warnings {
		parts = append(parts, fmt.Sprintf("%s %v %v", w.Kind, w.Paths, w.IDs))
	}
	return strings.Join(parts, "; ")
}

// A claim is one entry in the claims file, written as koan writes files, and
// shows in the task's view. No task file is written.
func TestClaim(t *testing.T) {
	f := newFixture(t)
	f.fixedTokens()
	f.task("proj", 7, false)
	f.task("proj", 9, false, 7)
	before := f.read("tasks/proj/7.json")

	_, got := f.do("claim", `{"id": 7, "extra": {"agent": "a", "n": 1.50}}`)
	want := `{"ok":true,"result":{"task":{"schema":1,"id":7,"title":"task 7","priority":null,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":[],"tags":[],"extra":{},"folder":"/proj","notes_path":"~/tasks/proj/7.md","readiness":"ready","blocking":[],"claim":{"claimed_at":"2026-09-28T12:00:00Z","extra":{"agent":"a","n":1.50},"locked":false}}},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	wantFile := "{\n  \"schema\": 1,\n  \"root\": \"~/tasks\",\n  \"claims\": {\n    \"7\": {\n      \"claimed_at\": \"2026-09-28T12:00:00Z\",\n      \"extra\": {\n        \"agent\": \"a\",\n        \"n\": 1.50\n      }\n    }\n  }\n}\n"
	if got := f.rel(f.read(claimsRel)); got != wantFile {
		t.Errorf("claims file:\n%s\nwant\n%s", got, wantFile)
	}
	if got := f.read("tasks/proj/7.json"); got != before {
		t.Errorf("the task file changed:\n%s", got)
	}
	// The file holds tokens, so only the user can read it.
	if fi, err := os.Stat(filepath.Join(f.home, claimsRel)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("claims file mode %v, %v", fi.Mode(), err)
	}

	// A blocked task can be claimed, with a token: the token is in the
	// result and the file, and the view says only that it is locked.
	_, got = f.do("claim", `{"id": 9, "use_token": true}`)
	if !strings.Contains(got, `"readiness":"blocked","blocking":[7],"claim":{"claimed_at":"2026-09-28T12:00:00Z","extra":{},"locked":true}},"token":"`+tok(1)+`"}`) {
		t.Errorf("got %s", got)
	}
	if got := f.claimed(); got != "7 9:locked" {
		t.Errorf("claimed %s", got)
	}
	_, got = f.do("show", `{"id": 9}`)
	if strings.Contains(got, tok(1)) || !strings.Contains(got, `"locked":true`) {
		t.Errorf("show: %s", got)
	}

	// Claimed again, whoever asks: refused, with the claim and no token.
	for doc, want := range map[string]string{
		`{"id": 7}`:                    "conflict claimed [7] claim",
		`{"id": 9}`:                    "conflict claimed [9] locked",
		`{"id": 9, "use_token": true}`: "conflict claimed [9] locked",
		`{"id": 5}`:                    "not-found",
	} {
		if _, l := f.do("claim", doc); outcome(t, l) != want {
			t.Errorf("claim %s: %s, want %s", doc, outcome(t, l), want)
		} else if strings.Contains(l, tok(1)) {
			t.Errorf("claim %s shows a token: %s", doc, l)
		}
	}
	if got := f.claimed(); got != "7 9:locked" {
		t.Errorf("claimed %s", got)
	}
}

// A done task is refused, a duplicated ID too, and each before any claim
// on it is looked at.
func TestClaimRefusals(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, true)
	f.task("a", 8, false)
	f.task("b", 8, false)
	f.write("tasks/proj/9.json", "{")
	for doc, want := range map[string]string{
		`{"id": 7}`: "conflict task-done [7]",
		`{"id": 8}`: "conflict duplicate-id [8]",
		`{"id": 9}`: "corrupt",
	} {
		if _, l := f.do("claim", doc); outcome(t, l) != want {
			t.Errorf("claim %s: %s, want %s", doc, outcome(t, l), want)
		}
	}
	if got := f.claimed(); got != "<none>" {
		t.Errorf("claimed %s", got)
	}
}

// claim-next takes the first unclaimed task of the frontier, in frontier
// order, and says so when there is none.
func TestClaimNext(t *testing.T) {
	f := newFixture(t)
	f.fixedTokens()
	prio := func(folder string, id model.ID, p int64, tags ...model.Tag) {
		tf := model.TaskFile{Schema: model.TaskSchema, ID: id, Title: "t", Priority: &p, CreatedAt: "2026-09-20T18:31:51Z", UpdatedAt: "2026-09-20T18:31:51Z", Tags: tags, Extra: &jsonio.Object{}}
		data, err := tf.Encode()
		if err != nil {
			t.Fatal(err)
		}
		f.write(filepath.Join("tasks", folder, fmt.Sprintf("%d.json", id)), string(data))
	}
	prio("proj", 1, 1)
	prio("proj", 2, 5, "today")
	prio("other", 3, 9)
	f.task("proj", 4, false, 1) // blocked
	f.task("proj", 5, true)     // done

	next := func(doc string) string {
		t.Helper()
		e, l := f.do("claim-next", doc)
		if !e.OK {
			return outcome(t, l)
		}
		r := e.Result.(ClaimOutput)
		if r.Task == nil {
			return "null"
		}
		s := fmt.Sprint(r.Task.ID)
		if r.Task.Claim == nil {
			t.Errorf("the task returned has no claim: %s", l)
		}
		if r.Token != "" {
			s += " " + r.Token
		}
		return s
	}
	for _, step := range []struct{ doc, want string }{
		{`{"folder": "/proj", "tags_any": ["today"]}`, "2"},
		{`{"folder": "/proj", "tags_any": ["today"]}`, "null"},
		{`{}`, "3"},
		{`{"use_token": true}`, "1 " + tok(1)},
		{`{}`, "null"},
		{`{"folder": "/nope"}`, "not-found"},
	} {
		if got := next(step.doc); got != step.want {
			t.Errorf("claim-next %s: %s, want %s", step.doc, got, step.want)
		}
	}
	if got := f.claimed(); got != "1:locked 2 3" {
		t.Errorf("claimed %s", got)
	}
}

// A candidate with another copy anywhere in the tree is passed over, and
// reported with every copy; a folder that can't be listed costs no claim.
func TestClaimNextPassesOver(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 1, false)
	f.task("archive", 1, false) // a copy outside the scope
	f.task("proj", 2, false)
	f.task("locked", 3, false)
	f.claimsFile("3:" + tok(9))
	if err := os.Chmod(filepath.Join(f.root, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(f.root, "locked"), 0o755)

	e, l := f.do("claim-next", `{"folder": "/proj"}`)
	if !e.OK || e.Result.(ClaimOutput).Task == nil || e.Result.(ClaimOutput).Task.ID != 2 {
		t.Fatalf("got %s", l)
	}
	want := "duplicate-id [~/tasks/archive/1.json ~/tasks/proj/1.json] [1]; unreadable-folder [~/tasks/locked] []"
	if got := warningKinds(t, l); got != want {
		t.Errorf("warnings %s\nwant     %s", got, want)
	}
	if got := f.claimed(); got != "2 3:locked" {
		t.Errorf("claimed %s", got)
	}
}

// targeting is one targeting write: its input, changing the task or not,
// for task 7, with the token appended by the test.
type targeting struct {
	op, change, noop string
}

var targetingWrites = []targeting{
	{"done", `"id": 7`, ``},
	{"update", `"id": 7, "priority": 3`, `"id": 7, "priority": null`},
	{"move", `"id": 7, "to": "/other"`, `"id": 7, "to": "/proj"`},
	{"delete", `"id": 7`, ``},
	{"block", `"id": 7, "blockers": [1]`, `"id": 7, "blockers": [2]`},
	{"unblock", `"id": 7, "blockers": [2]`, `"id": 7, "blockers": [1]`},
	{"release", `"id": 7`, ``},
}

// Each cell of the targeting table (design-spec.md, Targeting writes), for
// each targeting write, changing the task and not.
func TestTargetingTable(t *testing.T) {
	const K, wrong = "000000000000000000000000000000aa", "000000000000000000000000000000bb"
	states := []struct {
		name  string
		done  bool
		entry string
		// the outcome with no token, with K, and with another token
		none, right, other string
	}{
		{"no claim", false, "", "ok", "conflict token-mismatch [7] null", "conflict token-mismatch [7] null"},
		{"inert", true, "7:" + K, "ok", "conflict token-mismatch [7] null", "conflict token-mismatch [7] null"},
		{"claimed", false, "7", "ok", "conflict token-mismatch [7] claim", "conflict token-mismatch [7] claim"},
		{"locked", false, "7:" + K, "conflict token-mismatch [7] locked", "ok", "conflict token-mismatch [7] locked"},
	}
	for _, w := range targetingWrites {
		for _, input := range []string{w.change, w.noop} {
			if input == "" {
				continue
			}
			for _, st := range states {
				for i, token := range []string{"", K, wrong} {
					want := []string{st.none, st.right, st.other}[i]
					// The one exception: done lets the holder of a done
					// task's leftover entry through, to drop it.
					if w.op == "done" && st.name == "inert" && token == K {
						want = "ok"
					}
					f := newFixture(t)
					f.task("", 1, false)
					f.task("", 2, false)
					f.task("proj", 7, st.done, 2)
					f.write("tasks/other/.keep", "")
					if st.entry != "" {
						f.claimsFile(st.entry)
					}
					doc := "{" + input + "}"
					if token != "" {
						doc = "{" + input + `, "token": "` + token + `"}`
					}
					tree := f.tree()
					_, l := f.do(w.op, doc)
					if got := outcome(t, l); got != want {
						t.Errorf("%s %s, %s, token %q: %s, want %s", w.op, doc, st.name, token, got, want)
					}
					if want != "ok" {
						if got := f.tree(); got != tree {
							t.Errorf("%s %s, %s: a refused write changed something:\n%s\nwas\n%s", w.op, doc, st.name, got, tree)
						}
					}
					if strings.Contains(l, K) {
						t.Errorf("%s %s, %s: the output shows a token: %s", w.op, doc, st.name, l)
					}
				}
			}
		}
	}
}

// tree is every file under the home with its content, for comparing before
// and after.
func (f *fixture) tree() string {
	f.t.Helper()
	var b strings.Builder
	err := filepath.Walk(f.home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(p)
		fmt.Fprintf(&b, "%s\n%s\n", strings.TrimPrefix(p, f.home), data)
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return b.String()
}

// No output ever shows a token, but the result of the claim that made it:
// every operation, its result and its errors, over a tree with locked claims.
func TestNoTokenInOutput(t *testing.T) {
	f := newFixture(t)
	issued := f.fixedTokens()
	for id := model.ID(1); id <= 6; id++ {
		f.task("proj", id, false)
	}
	f.task("proj", 7, false, 1)
	// A locked claim whose task is then done by hand, for doctor to report
	// as inert; and a done whose drop of a locked claim's entry fails.
	inert := func() { f.task("proj", 5, true) }
	failing := func() { f.failClaimsWrite(syscall.ENOSPC) }
	working := func() { f.env.FS = fsys.OS{} }
	steps := []struct {
		op, doc string
		before  func()
		must    string // what the output must hold, so the step reaches what it is here for
	}{
		{op: "claim", doc: `{"id": 5, "use_token": true}`},
		{op: "doctor", doc: `{"kinds": ["claim-inert"]}`, before: inert, must: `"kind":"claim-inert"`},
		{op: "claim", doc: `{"id": 6, "use_token": true}`},
		{op: "done", doc: `{"id": 6, "token": "` + tok(2) + `"}`, before: failing, must: `"kind":"claims-not-updated"`},
		{op: "doctor", doc: `{}`, before: working},
		{op: "repair", doc: `{}`},
		{op: "claim", doc: `{"id": 1, "use_token": true}`},
		{op: "claim-next", doc: `{"use_token": true}`, must: `"id":2,`},
		{op: "claim", doc: `{"id": 1, "use_token": true}`, must: `"rule":"claimed"`},
		{op: "claim", doc: `{"id": 3}`},
		{op: "show", doc: `{"id": 1}`},
		{op: "list", doc: `{}`},
		{op: "list", doc: `{"claimed": true, "fields": ["claim"]}`},
		{op: "frontier", doc: `{}`},
		{op: "why", doc: `{"id": 7, "include_tasks": true}`},
		{op: "doctor", doc: `{}`},
		{op: "info", doc: `{}`},
		{op: "done", doc: `{"id": 1}`, must: `"rule":"token-mismatch"`},
		{op: "update", doc: `{"id": 1, "priority": 1}`},
		{op: "move", doc: `{"id": 1, "to": "/proj"}`},
		{op: "block", doc: `{"id": 1, "blockers": [4]}`},
		{op: "unblock", doc: `{"id": 1, "blockers": [4]}`},
		{op: "delete", doc: `{"id": 1}`},
		{op: "delete-folder", doc: `{"folder": "/proj", "recursive": true}`, must: `"ids":[1,2]`},
		{op: "release", doc: `{"id": 1}`},
		{op: "release", doc: `{"id": 1, "token": "` + tok(99) + `"}`},
		{op: "update", doc: `{"id": 3, "priority": 1, "token": "` + tok(3) + `"}`},
		{op: "update", doc: `{"id": 1, "priority": 1, "token": "` + tok(3) + `"}`, must: `"changed":["priority"]`},
		{op: "force-release", doc: `{"id": 2}`, must: `"changed":true`},
		{op: "done", doc: `{"id": 1, "token": "` + tok(3) + `"}`, must: `"changed":true`},
		{op: "done", doc: `{"id": 1, "token": "` + tok(3) + `"}`, must: `"claims":[null]`},
		{op: "reopen", doc: `{"id": 1}`},
		{op: "repair", doc: `{}`},
		{op: "delete-folder", doc: `{"folder": "/proj", "recursive": true}`},
	}
	resultToken := regexp.MustCompile(`,"token":"[0-9a-f]{32}"\},"warnings"`)
	for _, s := range steps {
		if s.before != nil {
			s.before()
		}
		e, l := f.do(s.op, s.doc)
		shown := l
		if (s.op == "claim" || s.op == "claim-next") && e.OK {
			shown = resultToken.ReplaceAllString(l, `},"warnings"`)
		}
		for _, token := range *issued {
			if strings.Contains(shown, token) {
				t.Errorf("%s %s shows token %s: %s", s.op, s.doc, token, l)
			}
		}
		if !strings.Contains(l, s.must) {
			t.Errorf("%s %s: no %s in %s", s.op, s.doc, s.must, l)
		}
	}
	if len(*issued) != 4 {
		t.Errorf("%d tokens issued, want 4", len(*issued))
	}
}

// No write removes or rewrites another task's entry: only those of the tasks
// it named or removed are gone, and the rest keep their extra, claimed_at,
// and token. claim-next runs with a folder it can't list, whose task's claim
// it must not take for gone; the other writes need the whole tree, so they
// run with every folder listable.
func TestWritesKeepOtherEntries(t *testing.T) {
	for _, tc := range []struct{ op, doc, want string }{
		{"claim", `{"id": 4}`, "1 2 3:locked 4"},
		{"claim-next", `{}`, "1 2 3:locked 4"},
		{"release", `{"id": 1}`, "2 3:locked"},
		{"force-release", `{"id": 1}`, "2 3:locked"},
		{"done", `{"id": 1}`, "2 3:locked"},
		{"reopen", `{"id": 5}`, "1 2 3:locked"}, // drops the done task's entry
		{"update", `{"id": 1, "priority": 1}`, "1 2 3:locked"},
		{"move", `{"id": 1, "to": "/proj"}`, "1 2 3:locked"},
		{"block", `{"id": 1, "blockers": [4]}`, "1 2 3:locked"},
		{"unblock", `{"id": 1, "blockers": [4]}`, "1 2 3:locked"},
		{"delete", `{"id": 1}`, "2 3:locked"},
		{"delete-folder", `{"folder": "/a", "recursive": true}`, "2 3:locked"},
		{"create", `{"title": "x"}`, "1 2 3:locked"},
		{"move-folder", `{"folder": "/a", "to": "/b"}`, "1 2 3:locked"},
	} {
		f := newFixture(t)
		f.task("a", 1, false)
		f.task("proj", 2, false)
		f.task("proj", 4, false)
		f.task("proj", 5, true)
		f.task("locked", 3, false)
		f.claimsFile("1", "2", "3:"+tok(9), "5")
		before := f.claimEntries()
		if tc.op == "claim-next" {
			if err := os.Chmod(filepath.Join(f.root, "locked"), 0); err != nil {
				t.Fatal(err)
			}
		}
		_, l := f.do(tc.op, tc.doc)
		os.Chmod(filepath.Join(f.root, "locked"), 0o755)
		if got := outcome(t, l); got != "ok" {
			t.Errorf("%s %s: %s", tc.op, tc.doc, l)
		}
		want := tc.want
		if tc.op != "reopen" {
			want += " 5" // the done task's inert entry, which only its own writes and repair remove
		}
		if got := f.claimed(); got != want {
			t.Errorf("%s %s: claimed %s, want %s", tc.op, tc.doc, got, want)
		}
		// Every entry that stays is as it was, byte for byte.
		for id, entry := range f.claimEntries() {
			if was, had := before[id]; had && was != entry {
				t.Errorf("%s %s: entry %s rewritten:\n%s\nwas\n%s", tc.op, tc.doc, id, entry, was)
			}
		}
	}
}

// The first claim on a root replaces a claims file that names another root,
// and warns of the claims that went with it. Nothing else does either: a
// read, and a write that puts no claim, leave the file as it is.
func TestClaimReplacesOtherRoot(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	f.task("", 2, false)
	other := "{\n  \"schema\": 1,\n  \"root\": \"/elsewhere\",\n  \"claims\": {\n    \"1\": {\n      \"claimed_at\": \"2026-09-25T09:00:00Z\",\n      \"extra\": {}\n    },\n    \"5\": {\n      \"claimed_at\": \"2026-09-25T09:00:00Z\",\n      \"extra\": {},\n      \"token\": \"" + tok(9) + "\"\n    }\n  }\n}\n"
	f.write(claimsRel, other)
	if _, l := f.do("show", `{"id": 1}`); !strings.Contains(l, `"claim":null`) || warningKinds(t, l) != "" {
		t.Errorf("show: %s", l)
	}
	for _, op := range []string{"release", "done"} {
		if _, l := f.do(op, `{"id": 2}`); outcome(t, l) != "ok" || warningKinds(t, l) != "" {
			t.Errorf("%s: %s", op, l)
		}
	}
	if got := f.read(claimsRel); got != other {
		t.Fatalf("claims file changed before any claim:\n%s", got)
	}
	_, l := f.do("claim", `{"id": 1}`)
	if outcome(t, l) != "ok" {
		t.Fatalf("claim: %s", l)
	}
	if got, want := warningKinds(t, l), "claims-replaced [~/"+claimsRel+"] [1 5]"; got != want || !strings.Contains(l, "/elsewhere") || strings.Contains(l, tok(9)) {
		t.Errorf("warnings %s, want %s: %s", got, want, l)
	}
	if got := f.read(claimsRel); !strings.Contains(got, f.root) || strings.Contains(got, "/elsewhere") || f.claimed() != "1" {
		t.Errorf("claims file:\n%s", got)
	}

	// Another root's file that holds no claim is replaced with no warning,
	// by claim-next as by claim.
	f.write(claimsRel, "{\n  \"schema\": 1,\n  \"root\": \"/elsewhere\",\n  \"claims\": {}\n}\n")
	if _, l := f.do("claim-next", `{}`); outcome(t, l) != "ok" || warningKinds(t, l) != "" || f.claimed() != "1" {
		t.Errorf("claim-next: %s, claimed %q", l, f.claimed())
	}
}

// unusableClaims are the three ways a claims file is unusable, each set up
// on f, with the error an operation that needs it fails with.
var unusableClaims = []struct {
	name, kind, reason string
	setup              func(f *fixture)
}{
	{"corrupt", "corrupt", "corrupt", func(f *fixture) { f.write(claimsRel, "{") }},
	{"unsupported", "unsupported-format", "unsupported-format", func(f *fixture) { f.write(claimsRel, `{"schema": 2}`) }},
	{"unreadable", "io", "unreadable", func(f *fixture) {
		f.claimsFile()
		p := filepath.Join(f.home, claimsRel)
		f.hook(func(o fsys.Op) error {
			if o.Name == fsys.OpReadFile && o.Path == p {
				return syscall.EACCES
			}
			return nil
		})
	}},
}

// An unusable claims file fails every operation that needs it, with and
// without a token, changing nothing; reads carry on, with one warning and
// every task unclaimed, also over an empty scope.
func TestUnusableClaimsFile(t *testing.T) {
	writes := []struct{ op, doc string }{
		{"claim", `{"id": 7}`}, {"claim-next", `{}`}, {"release", `{"id": 7}`}, {"force-release", `{"id": 7}`},
		{"done", `{"id": 7}`}, {"reopen", `{"id": 7}`}, {"update", `{"id": 7, "priority": 1}`},
		{"move", `{"id": 7, "to": "/empty"}`}, {"block", `{"id": 7, "blockers": [1]}`},
		{"unblock", `{"id": 7, "blockers": [1]}`}, {"delete", `{"id": 7}`},
		{"delete-folder", `{"folder": "/proj", "recursive": true}`}, {"delete-folder", `{"folder": "/empty"}`},
		{"done", `{"id": 7, "token": "` + tok(1) + `"}`}, {"delete", `{"id": 7, "token": "` + tok(1) + `"}`},
	}
	reads := []struct{ op, doc string }{
		{"show", `{"id": 7}`}, {"list", `{}`}, {"frontier", `{}`}, {"why", `{"id": 7}`},
		{"why", `{"id": 7, "include_tasks": true}`}, {"list", `{"folder": "/empty"}`}, {"frontier", `{"claimed": true}`},
	}
	for _, u := range unusableClaims {
		for _, w := range writes {
			f := newFixture(t)
			f.task("", 1, false)
			f.task("proj", 7, false)
			f.write("tasks/empty/.keep", "")
			os.Remove(filepath.Join(f.root, "empty/.keep"))
			u.setup(f)
			tree := f.tree()
			_, l := f.do(w.op, w.doc)
			if got := outcome(t, l); got != u.kind {
				t.Errorf("%s: %s %s: %s, want %s", u.name, w.op, w.doc, l, u.kind)
			}
			if !strings.Contains(l, `"path":"~/`+claimsRel+`"`) {
				t.Errorf("%s: %s %s: the error names another file: %s", u.name, w.op, w.doc, l)
			}
			if got := f.tree(); got != tree {
				t.Errorf("%s: %s %s changed something", u.name, w.op, w.doc)
			}
		}
		for _, r := range reads {
			f := newFixture(t)
			f.task("", 1, false)
			f.task("proj", 7, false, 1)
			f.write("tasks/empty/.keep", "")
			u.setup(f)
			e, l := f.do(r.op, r.doc)
			if !e.OK {
				t.Errorf("%s: %s %s: %s", u.name, r.op, r.doc, l)
			}
			if got, want := warningKinds(t, l), "unusable-file [~/"+claimsRel+"] []"; got != want {
				t.Errorf("%s: %s %s: warnings %s, want %s", u.name, r.op, r.doc, got, want)
			}
			if !strings.Contains(l, `"reason":"`+u.reason+`"`) || strings.Contains(l, `"claim":{`) {
				t.Errorf("%s: %s %s: %s", u.name, r.op, r.doc, l)
			}
		}
	}
}

// The claims file's error comes after not-found, and before a needed task
// file's and every conflict (operations.md, Precedence).
func TestClaimsPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, op, doc, want string
		setup               func(f *fixture)
	}{
		{"missing task", "done", `{"id": 9}`, "not-found", func(f *fixture) {}},
		{"missing task", "claim", `{"id": 9}`, "not-found", func(f *fixture) {}},
		{"missing task", "delete", `{"id": 9}`, "not-found", func(f *fixture) {}},
		{"missing folder", "claim-next", `{"folder": "/nope"}`, "not-found", func(f *fixture) {}},
		{"missing folder and task", "move", `{"id": 9, "to": "/nope"}`, "not-found", func(f *fixture) {}},
		{"missing blocker", "block", `{"id": 7, "blockers": [9]}`, "not-found", func(f *fixture) {}},
		{"corrupt task file", "done", `{"id": 7}`, "corrupt claims", func(f *fixture) { f.write("tasks/proj/7.json", "{") }},
		{"corrupt task file", "update", `{"id": 7, "priority": 1}`, "corrupt claims", func(f *fixture) { f.write("tasks/proj/7.json", "{") }},
		{"corrupt task file", "move", `{"id": 7, "to": "/"}`, "corrupt claims", func(f *fixture) { f.write("tasks/proj/7.json", "{") }},
		{"duplicate", "done", `{"id": 7}`, "corrupt claims", func(f *fixture) { f.task("b", 7, false) }},
		{"duplicate", "delete", `{"id": 7}`, "corrupt claims", func(f *fixture) { f.task("b", 7, false) }},
		{"duplicate", "move", `{"id": 7, "to": "/"}`, "corrupt claims", func(f *fixture) { f.task("b", 7, false) }},
		{"duplicate", "block", `{"id": 7, "blockers": [1]}`, "corrupt claims", func(f *fixture) { f.task("b", 7, false) }},
		{"duplicate", "claim", `{"id": 7}`, "corrupt claims", func(f *fixture) { f.task("b", 7, false) }},
		{"duplicate in the folder", "delete-folder", `{"folder": "/proj", "recursive": true}`, "corrupt claims", func(f *fixture) { f.task("b", 7, false) }},
		// block reads its own task file first of all, as it always has.
		{"corrupt task file", "block", `{"id": 7, "blockers": [1]}`, "corrupt", func(f *fixture) { f.write("tasks/proj/7.json", "{") }},
		{"not empty", "delete-folder", `{"folder": "/proj"}`, "corrupt claims", func(f *fixture) {}},
		{"cycle", "block", `{"id": 7, "blockers": [1]}`, "corrupt claims", func(f *fixture) { f.task("", 1, false, 7) }},
	} {
		f := newFixture(t)
		f.task("", 1, false)
		f.task("proj", 7, false)
		f.write(claimsRel, "{")
		tc.setup(f)
		_, l := f.do(tc.op, tc.doc)
		got := outcome(t, l)
		if got == "corrupt" && strings.Contains(l, claimsRel) {
			got = "corrupt claims"
		}
		if got != tc.want {
			t.Errorf("%s: %s %s: %s, want %s\n%s", tc.name, tc.op, tc.doc, got, tc.want, l)
		}
	}
	// With the file usable: duplicate-id, then the claim rules, then the
	// operation's own.
	for _, tc := range []struct {
		name, op, doc, want string
		setup               func(f *fixture)
	}{
		{"duplicate, locked", "done", `{"id": 7}`, "conflict duplicate-id [7]", func(f *fixture) { f.task("b", 7, false) }},
		{"locked, cycle", "block", `{"id": 7, "blockers": [1]}`, "conflict token-mismatch [7] locked", func(f *fixture) { f.task("", 1, false, 7) }},
		{"locked, above last_id", "delete", `{"id": 7}`, "conflict token-mismatch [7] locked", func(f *fixture) { f.setLastID(3) }},
		{"not empty, locked", "delete-folder", `{"folder": "/proj"}`, "conflict not-empty [7]", func(f *fixture) {}},
		{"locked folder", "delete-folder", `{"folder": "/proj", "recursive": true}`, "conflict token-mismatch [7] locked", func(f *fixture) {}},
		{"duplicate, locked folder", "delete-folder", `{"folder": "/proj", "recursive": true}`, "conflict duplicate-id [7]", func(f *fixture) { f.task("b", 7, false) }},
		{"locked folder, above last_id", "delete-folder", `{"folder": "/proj", "recursive": true}`, "conflict token-mismatch [7] locked", func(f *fixture) { f.setLastID(3) }},
		{"duplicate, locked", "move", `{"id": 7, "to": "/"}`, "conflict duplicate-id [7]", func(f *fixture) { f.task("b", 7, false) }},
		{"duplicate, locked", "block", `{"id": 7, "blockers": [1]}`, "conflict duplicate-id [7]", func(f *fixture) { f.task("b", 7, false) }},
		{"done, claimed entry", "claim", `{"id": 7}`, "conflict task-done [7]", func(f *fixture) { f.task("proj", 7, true) }},
	} {
		f := newFixture(t)
		f.task("", 1, false)
		f.task("proj", 7, false)
		f.claimsFile("7:" + tok(1))
		tc.setup(f)
		if _, l := f.do(tc.op, tc.doc); outcome(t, l) != tc.want {
			t.Errorf("%s: %s %s: %s, want %s", tc.name, tc.op, tc.doc, outcome(t, l), tc.want)
		}
	}
}

// failClaimsWrite fails the publishing of the claims file: the rename over
// it, or the link that creates it. The temp file's own name is random, so
// the hook matches the target.
func (f *fixture) failClaimsWrite(errno syscall.Errno) {
	f.hook(func(o fsys.Op) error {
		if (o.Name == fsys.OpRename || o.Name == fsys.OpLink) && o.NewPath == store.ClaimsName {
			return errno
		}
		return nil
	})
}

// done ends the task's claim: the entry goes once the task is done. When it
// can't be removed the task is done all the same, with a warning, and the
// entry left is inert; a rerun removes it.
func TestDoneDropsClaim(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, false)
	f.task("proj", 8, false)
	f.claimsFile("7:"+tok(1), "8")
	if _, l := f.do("done", `{"id": 7, "token": "`+tok(1)+`"}`); outcome(t, l) != "ok" || warningKinds(t, l) != "" {
		t.Fatalf("done: %s", l)
	}
	if got := f.claimed(); got != "8" {
		t.Errorf("claimed %s", got)
	}
	// Rerun with the token: the claim it belonged to is gone.
	if _, l := f.do("done", `{"id": 7, "token": "`+tok(1)+`"}`); outcome(t, l) != "conflict token-mismatch [7] null" {
		t.Errorf("rerun: %s", l)
	}

	f.failClaimsWrite(syscall.ENOSPC)
	e, l := f.do("done", `{"id": 8}`)
	if !e.OK || !e.Result.(ChangedOutput).Changed {
		t.Fatalf("done: %s", l)
	}
	if got, want := warningKinds(t, l), "claims-not-updated [~/"+claimsRel+"] [8]"; got != want || !strings.Contains(l, `"code":"ENOSPC"`) {
		t.Errorf("warnings %s, want %s: %s", got, want, l)
	}
	if got := f.claimed(); got != "8" {
		t.Errorf("claimed %s", got)
	}
	// The entry left is inert: the task reads as unclaimed.
	if _, l := f.do("show", `{"id": 8}`); !strings.Contains(l, `"claim":null`) {
		t.Errorf("show: %s", l)
	}
	f.env.FS = fsys.OS{}
	e, l = f.do("done", `{"id": 8}`)
	if !e.OK || e.Result.(ChangedOutput).Changed || f.claimed() != "" {
		t.Errorf("rerun: %s, claimed %q", l, f.claimed())
	}
}

// A done that could not drop a locked claim's entry leaves it to its holder:
// a rerun with the token is let through and drops it, where any other token
// is refused, as the token is once the entry is gone.
func TestDoneRerunDropsOwnEntry(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, false)
	f.claimsFile("7:" + tok(1))
	doc := `{"id": 7, "token": "` + tok(1) + `"}`
	f.failClaimsWrite(syscall.ENOSPC)
	if e, l := f.do("done", doc); !e.OK || warningKinds(t, l) != "claims-not-updated [~/"+claimsRel+"] [7]" {
		t.Fatalf("done: %s", l)
	}
	f.env.FS = fsys.OS{}
	if _, l := f.do("done", `{"id": 7, "token": "`+tok(2)+`"}`); outcome(t, l) != "conflict token-mismatch [7] null" {
		t.Errorf("another token: %s", l)
	}
	if got := f.claimed(); got != "7:locked" {
		t.Fatalf("claimed %s", got)
	}
	e, l := f.do("done", doc)
	if !e.OK || e.Result.(ChangedOutput).Changed || warningKinds(t, l) != "" || f.claimed() != "" {
		t.Errorf("rerun: %s, claimed %q", l, f.claimed())
	}
	if _, l := f.do("done", doc); outcome(t, l) != "conflict token-mismatch [7] null" {
		t.Errorf("rerun with the entry gone: %s", l)
	}
}

// reopen drops a done task's entry first, and fails with nothing done when
// it can't, so a reopened task never comes back with an old claim. An open
// task's claim is left alone.
func TestReopenStartsUnclaimed(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, true)
	f.task("proj", 8, false)
	f.claimsFile("7:"+tok(1), "8")
	tree := f.tree()

	f.failClaimsWrite(syscall.ENOSPC)
	if _, l := f.do("reopen", `{"id": 7}`); outcome(t, l) != "io" {
		t.Errorf("reopen: %s", l)
	}
	if got := f.tree(); got != tree {
		t.Errorf("a failed reopen changed something:\n%s", got)
	}

	f.env.FS = fsys.OS{}
	e, l := f.do("reopen", `{"id": 7}`)
	if !e.OK || !e.Result.(ChangedOutput).Changed || f.claimed() != "8" {
		t.Errorf("reopen: %s, claimed %q", l, f.claimed())
	}
	if _, l := f.do("show", `{"id": 7}`); !strings.Contains(l, `"claim":null`) {
		t.Errorf("show: %s", l)
	}
	e, l = f.do("reopen", `{"id": 8}`)
	if !e.OK || e.Result.(ChangedOutput).Changed || f.claimed() != "8" {
		t.Errorf("reopen of an open task: %s, claimed %q", l, f.claimed())
	}
}

// release clears a claim, effective or inert; changed says whether an
// effective one went. force-release needs no token.
func TestRelease(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, false)
	f.task("proj", 8, true)
	f.task("proj", 9, false)
	f.claimsFile("7:"+tok(1), "8:"+tok(2), "9")
	e, got := f.do("release", `{"id": 9}`)
	want := `{"ok":true,"result":{"schema":1,"id":9,"title":"task 9","priority":null,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":[],"tags":[],"extra":{},"folder":"/proj","notes_path":"~/tasks/proj/9.md","changed":true},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	_ = e
	for _, step := range []struct{ op, doc, want, claimed string }{
		{"release", `{"id": 9}`, "ok false", "7:locked 8:locked"},
		{"release", `{"id": 7}`, "conflict token-mismatch [7] locked", "7:locked 8:locked"},
		{"release", `{"id": 8}`, "ok false", "7:locked"}, // inert: no token needed
		{"force-release", `{"id": 7}`, "ok true", ""},
		{"force-release", `{"id": 7}`, "ok false", ""},
		{"release", `{"id": 5}`, "not-found", ""},
	} {
		e, l := f.do(step.op, step.doc)
		got := outcome(t, l)
		if e.OK {
			got = fmt.Sprintf("ok %v", e.Result.(ChangedOutput).Changed)
		}
		if got != step.want || f.claimed() != step.claimed {
			t.Errorf("%s %s: %s, claimed %q; want %s, %q", step.op, step.doc, got, f.claimed(), step.want, step.claimed)
		}
	}
}

// delete and delete-folder drop the entries of what they remove, last; a
// locked task refuses delete-folder, which takes no token; and an unusable
// task file makes its claim inert, so the task can still be deleted.
func TestDeleteAndClaims(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 7, false)
	f.task("proj", 8, false)
	f.task("other", 9, false)
	f.claimsFile("7:"+tok(1), "8", "9")
	if _, l := f.do("delete-folder", `{"folder": "/proj", "recursive": true}`); outcome(t, l) != "conflict token-mismatch [7] locked" {
		t.Fatalf("delete-folder: %s", l)
	}
	if _, l := f.do("delete", `{"id": 7, "token": "`+tok(1)+`"}`); outcome(t, l) != "ok" || f.claimed() != "8 9" {
		t.Fatalf("delete: %s, claimed %q", l, f.claimed())
	}
	if _, l := f.do("delete-folder", `{"folder": "/proj", "recursive": true}`); outcome(t, l) != "ok" || f.claimed() != "9" {
		t.Fatalf("delete-folder: %s, claimed %q", l, f.claimed())
	}

	f.failClaimsWrite(syscall.ENOSPC)
	_, l := f.do("delete", `{"id": 9}`)
	if got, want := warningKinds(t, l), "claims-not-updated [~/"+claimsRel+"] [9]"; outcome(t, l) != "ok" || got != want {
		t.Errorf("delete: %s", l)
	}
	if f.read("tasks/other/9.json") != "<none>" || f.claimed() != "9" {
		t.Errorf("task %s, claimed %q", f.read("tasks/other/9.json"), f.claimed())
	}

	// A locked task whose file is unusable: the claim is inert.
	g := newFixture(t)
	g.write("tasks/proj/7.json", "{")
	g.claimsFile("7:" + tok(1))
	if _, l := g.do("delete", `{"id": 7, "token": "`+tok(1)+`"}`); outcome(t, l) != "conflict token-mismatch [7] null" {
		t.Errorf("delete with the token: %s", l)
	}
	if _, l := g.do("delete", `{"id": 7}`); outcome(t, l) != "ok" || g.claimed() != "" {
		t.Errorf("delete: %s, claimed %q", l, g.claimed())
	}
}

// claimed narrows list and frontier to tasks with, or without, an effective
// claim; an inert entry counts as none.
func TestClaimedFilter(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	f.task("", 2, false)
	f.task("", 3, true)
	f.task("", 4, false, 1)
	f.claimsFile("1", "3", "4:"+tok(1))
	ids := func(op, doc string) string {
		t.Helper()
		_, l := f.do(op, doc)
		var env struct {
			Result struct {
				Tasks []struct {
					ID    int `json:"id"`
					Claim *struct {
						Locked bool `json:"locked"`
					} `json:"claim"`
				} `json:"tasks"`
				Total int `json:"total"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(l), &env); err != nil {
			t.Fatal(err)
		}
		out := fmt.Sprintf("total=%d", env.Result.Total)
		for _, task := range env.Result.Tasks {
			out += fmt.Sprintf(" %d", task.ID)
			if task.Claim != nil {
				out += map[bool]string{false: "c", true: "L"}[task.Claim.Locked]
			}
		}
		return out
	}
	for _, tc := range []struct{ op, doc, want string }{
		{"list", `{"readiness": ["ready", "blocked", "done"]}`, "total=4 1c 2 3 4L"},
		{"list", `{"claimed": true}`, "total=2 1c 4L"},
		{"list", `{"claimed": false, "readiness": ["ready", "blocked", "done"]}`, "total=2 2 3"},
		{"list", `{"claimed": true, "limit": 1, "fields": ["claim"]}`, "total=2 1c"},
		{"frontier", `{}`, "total=2 1c 2"},
		{"frontier", `{"claimed": false}`, "total=1 2"},
		{"frontier", `{"claimed": true}`, "total=1 1c"},
	} {
		if got := ids(tc.op, tc.doc); got != tc.want {
			t.Errorf("%s %s: %s, want %s", tc.op, tc.doc, got, tc.want)
		}
	}
}

// doctor reports an unusable claims file, and the entries of done and gone
// tasks; an entry whose task may come back — duplicated, unusable, or behind
// a symlink — is kept. repair drops the inert ones, and resets the file only
// on request.
func TestClaimFindings(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)         // effective
	f.task("", 2, true)          // done
	f.task("a", 4, false)        // duplicated
	f.task("b", 4, false)        //
	f.write("tasks/5.json", "{") // unusable
	if err := os.Symlink("1.json", filepath.Join(f.root, "6.json")); err != nil {
		t.Fatal(err)
	}
	f.claimsFile("1", "2", "3", "4", "5", "6")
	_, l := f.do("doctor", `{"kinds": ["claim-inert"]}`)
	want := `{"kind":"claim-inert","class":"auto","count":2,"truncated":false,"items":[{"paths":["~/` + claimsRel + `"],"ids":[2],"action":"drop-claim","suggest":"koan repair","reason":"done"},{"paths":["~/` + claimsRel + `"],"ids":[3],"action":"drop-claim","suggest":"koan repair","reason":"missing"}]}`
	if !strings.Contains(l, want) {
		t.Errorf("doctor: %s\nwant %s", l, want)
	}
	if _, l := f.do("repair", `{}`); !strings.Contains(l, `"repaired":[{"kind":"claim-inert","class":"auto","count":2`) || f.claimed() != "1 4 5 6" {
		t.Errorf("repair: %s, claimed %q", l, f.claimed())
	}

	for _, u := range unusableClaims[:2] {
		g := newFixture(t)
		g.task("", 1, false)
		u.setup(g)
		_, l := g.do("doctor", `{}`)
		if !strings.Contains(l, `"healthy":false,"findings":[{"kind":"claims-unusable","class":"on-request","count":1,"truncated":false,"items":[{"paths":["~/`+claimsRel+`"],"ids":[],"action":"reset-claims"`) ||
			!strings.Contains(l, `"error":{"kind":"`+u.kind+`"`) {
			t.Errorf("%s: doctor: %s", u.name, l)
		}
		before := g.read(claimsRel)
		if _, l := g.do("repair", `{}`); !strings.Contains(l, `"repaired":[]`) || g.read(claimsRel) != before {
			t.Errorf("%s: a plain repair touched the claims file: %s", u.name, l)
		}
		if _, l := g.do("repair", `{"kinds": ["claims-unusable"]}`); !strings.Contains(l, `"repaired":[{"kind":"claims-unusable"`) || !strings.Contains(l, `"healthy":true`) {
			t.Errorf("%s: repair: %s", u.name, l)
		}
		if g.claimed() != "" {
			t.Errorf("%s: claimed %q", u.name, g.claimed())
		}
		if _, l := g.do("claim", `{"id": 1}`); outcome(t, l) != "ok" {
			t.Errorf("%s: claim after repair: %s", u.name, l)
		}
	}

	// A directory in the file's place is a person's to remove.
	d := newFixture(t)
	d.write(claimsRel+"/x", "")
	if _, l := d.do("doctor", `{}`); !strings.Contains(l, `"kind":"claims-unusable"`) || !strings.Contains(l, `"action":null`) {
		t.Errorf("doctor: %s", l)
	}
	if _, l := d.do("repair", `{"kinds": ["claims-unusable"]}`); !strings.Contains(l, `"repaired":[]`) {
		t.Errorf("repair: %s", l)
	}
}

// info reports the claims file's state; it enters into neither initialized
// nor usable.
func TestInfoClaims(t *testing.T) {
	for _, tc := range []struct {
		state string
		setup func(f *fixture)
	}{
		{"missing", func(f *fixture) {}},
		{"ok", func(f *fixture) { f.claimsFile("1") }},
		{"other-root", func(f *fixture) { f.write(claimsRel, `{"schema": 1, "root": "/elsewhere", "claims": {}}`) }},
		{"corrupt", func(f *fixture) { f.write(claimsRel, "{") }},
		{"unsupported-format", func(f *fixture) { f.write(claimsRel, `{"schema": 2}`) }},
	} {
		f := newFixture(t)
		tc.setup(f)
		_, l := f.do("info", `{}`)
		if !strings.Contains(l, `"claims":{"path":"~/`+claimsRel+`","state":"`+tc.state+`"},"initialized":true,"usable":true`) {
			t.Errorf("%s: %s", tc.state, l)
		}
	}
}

// A claim is all or nothing: when the task's view can't be built, nothing is
// recorded, and no token is lost with the error.
func TestClaimAllOrNothing(t *testing.T) {
	f := newFixture(t)
	issued := f.fixedTokens()
	f.task("", 1, false)
	f.task("", 2, false, 1)
	// An OS error with no symbolic name, on the blocker's file: internal.
	f.hook(func(o fsys.Op) error {
		if o.Name == fsys.OpReadFile && o.Path == "1.json" {
			return syscall.Errno(9999)
		}
		return nil
	})
	if _, l := f.do("claim", `{"id": 2, "use_token": true}`); outcome(t, l) != "internal" {
		t.Fatalf("claim: %s", l)
	}
	if got := f.claimed(); got != "<none>" || len(*issued) != 0 {
		t.Errorf("claimed %s, %d tokens issued", got, len(*issued))
	}
}

// A claims file that can't be written fails claim, claim-next, release, and
// force-release with nothing changed; done, delete, and delete-folder have
// done their work by then, so they succeed, with a warning that carries the
// OS error.
func TestClaimsWriteFails(t *testing.T) {
	for _, tc := range []struct{ op, doc string }{
		{"claim", `{"id": 2}`}, {"claim", `{"id": 2, "use_token": true}`}, {"claim-next", `{}`},
		{"release", `{"id": 1}`}, {"force-release", `{"id": 1}`}, {"reopen", `{"id": 3}`},
	} {
		f := newFixture(t)
		issued := f.fixedTokens()
		f.task("a", 1, false)
		f.task("a", 2, false)
		f.task("a", 3, true)
		f.claimsFile("1", "3")
		tree := f.tree()
		f.failClaimsWrite(syscall.ENOSPC)
		_, l := f.do(tc.op, tc.doc)
		if outcome(t, l) != "io" || !strings.Contains(l, `"path":"~/`+claimsRel+`","code":"ENOSPC"`) {
			t.Errorf("%s %s: %s", tc.op, tc.doc, l)
		}
		if got := f.tree(); got != tree {
			t.Errorf("%s %s changed something:\n%s", tc.op, tc.doc, got)
		}
		for _, token := range *issued {
			if strings.Contains(l, token) {
				t.Errorf("%s %s: a failed claim shows its token: %s", tc.op, tc.doc, l)
			}
		}
	}
	for _, tc := range []struct{ op, doc, gone string }{
		{"done", `{"id": 1}`, ""},
		{"delete", `{"id": 1}`, "tasks/a/1.json"},
		{"delete-folder", `{"folder": "/a", "recursive": true}`, "tasks/a/2.json"},
	} {
		f := newFixture(t)
		f.task("a", 1, false)
		f.task("a", 2, false)
		f.claimsFile("1")
		f.failClaimsWrite(syscall.EROFS)
		e, l := f.do(tc.op, tc.doc)
		if !e.OK {
			t.Fatalf("%s %s: %s", tc.op, tc.doc, l)
		}
		if got, want := warningKinds(t, l), "claims-not-updated [~/"+claimsRel+"] [1]"; got != want || !strings.Contains(l, `"code":"EROFS"`) {
			t.Errorf("%s %s: warnings %s, want %s with EROFS: %s", tc.op, tc.doc, got, want, l)
		}
		if tc.gone != "" && f.read(tc.gone) != "<none>" {
			t.Errorf("%s %s: %s is still there", tc.op, tc.doc, tc.gone)
		}
		if got := f.claimed(); got != "1" {
			t.Errorf("%s %s: claimed %s", tc.op, tc.doc, got)
		}
	}
}

// delete-folder refuses every task under the folder that has a locked
// effective claim, in ascending order, wherever it comes among them; a
// locked entry that is inert refuses nothing, and goes with its task.
func TestDeleteFolderSeveralLocked(t *testing.T) {
	f := newFixture(t)
	f.task("p", 3, false)
	f.task("p/q", 5, false)
	f.task("p", 7, false)
	f.task("p", 9, true) // done: its locked entry is inert
	f.task("", 11, false)
	f.claimsFile("3", "5:"+tok(1), "7:"+tok(2), "9:"+tok(3), "11:"+tok(4))
	tree := f.tree()
	_, l := f.do("delete-folder", `{"folder": "/p", "recursive": true}`)
	if got, want := outcome(t, l), "conflict token-mismatch [5 7] locked locked"; got != want {
		t.Fatalf("delete-folder: %s, want %s", got, want)
	}
	if got := f.tree(); got != tree {
		t.Errorf("a refused delete-folder changed something")
	}
	f.do("force-release", `{"id": 7}`)
	if _, l := f.do("delete-folder", `{"folder": "/p", "recursive": true}`); outcome(t, l) != "conflict token-mismatch [5] locked" {
		t.Errorf("delete-folder: %s", l)
	}
	f.do("force-release", `{"id": 5}`)
	if _, l := f.do("delete-folder", `{"folder": "/p", "recursive": true}`); outcome(t, l) != "ok" || f.claimed() != "11:locked" {
		t.Errorf("delete-folder: %s, claimed %q", l, f.claimed())
	}
}

// Only the task whose blocked_by changes is targeted: a locked blocker is
// never checked, and a blocker's token is not the task's.
func TestBlockNamesLockedBlocker(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	f.task("", 7, false)
	f.claimsFile("7:" + tok(1))
	for _, step := range []struct{ op, doc, want string }{
		{"block", `{"id": 1, "blockers": [7]}`, "ok"},
		{"block", `{"id": 1, "blockers": [7], "token": "` + tok(1) + `"}`, "conflict token-mismatch [1] null"},
		{"unblock", `{"id": 1, "blockers": [7]}`, "ok"},
		{"delete", `{"id": 1}`, "ok"},
	} {
		if _, l := f.do(step.op, step.doc); outcome(t, l) != step.want {
			t.Errorf("%s %s: %s, want %s", step.op, step.doc, outcome(t, l), step.want)
		}
	}
}

// claim-next's scope is frontier's: the folder, with or without its
// subfolders, and both tag filters; its extra is stored with the claim.
func TestClaimNextScope(t *testing.T) {
	f := newFixture(t)
	f.tagged("p", 1, "a")
	f.tagged("p/q", 2, "a", "b")
	f.tagged("p", 3, "a", "b")
	f.tagged("", 4, "a", "b")
	next := func(doc string) string {
		t.Helper()
		e, l := f.do("claim-next", doc)
		if !e.OK || e.Result.(ClaimOutput).Task == nil {
			return outcome(t, l) + " none"
		}
		return fmt.Sprint(e.Result.(ClaimOutput).Task.ID)
	}
	for _, step := range []struct{ doc, want string }{
		{`{"folder": "/p", "tags_all": ["a", "b"], "recursive": false, "extra": {"by": "x", "n": 2.50}}`, "3"},
		{`{"folder": "/p", "tags_all": ["a", "b"], "recursive": false}`, "ok none"},
		{`{"folder": "/p", "tags_all": ["a", "b"]}`, "2"},
		{`{"folder": "/p", "tags_any": ["b", "zz"]}`, "ok none"},
		{`{"folder": "/p"}`, "1"},
		{`{"tags_all": ["a", "zz"]}`, "ok none"},
		{`{}`, "4"},
	} {
		if got := next(step.doc); got != step.want {
			t.Errorf("claim-next %s: %s, want %s", step.doc, got, step.want)
		}
	}
	if got := f.claimEntries()["3"]; !strings.Contains(got, `"by": "x"`) || !strings.Contains(got, `"n": 2.50`) {
		t.Errorf("entry 3: %s", got)
	}
}

// Something other than a regular file in the claims file's place, or a file
// that can't be looked at, is unusable: writes fail, and reads warn. Neither
// counts as no claims.
func TestClaimsFileNotRegular(t *testing.T) {
	for _, tc := range []struct {
		name, kind, reason string
		setup              func(f *fixture)
	}{
		{"symlink", "corrupt", "corrupt", func(f *fixture) {
			f.write("cfg/real.json", "{\n  \"schema\": 1,\n  \"root\": \""+f.root+"\",\n  \"claims\": {}\n}\n")
			if err := os.Symlink("real.json", filepath.Join(f.home, claimsRel)); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", "corrupt", "corrupt", func(f *fixture) { f.write(claimsRel+"/x", "") }},
		{"lstat fails", "io", "unreadable", func(f *fixture) {
			f.claimsFile()
			p := filepath.Join(f.home, claimsRel)
			f.hook(func(o fsys.Op) error {
				if o.Name == fsys.OpLstat && o.Path == p {
					return syscall.EACCES
				}
				return nil
			})
		}},
	} {
		f := newFixture(t)
		f.task("", 1, false)
		tc.setup(f)
		for _, w := range []struct{ op, doc string }{{"claim", `{"id": 1}`}, {"done", `{"id": 1}`}, {"claim-next", `{}`}} {
			if _, l := f.do(w.op, w.doc); outcome(t, l) != tc.kind {
				t.Errorf("%s: %s: %s, want %s", tc.name, w.op, l, tc.kind)
			}
		}
		_, l := f.do("show", `{"id": 1}`)
		if got, want := warningKinds(t, l), "unusable-file [~/"+claimsRel+"] []"; got != want || !strings.Contains(l, `"reason":"`+tc.reason+`"`) {
			t.Errorf("%s: show: %s", tc.name, l)
		}
	}
}

// A claim shows on a view wherever one is returned, and never on a copy of
// a duplicated ID, whose claim is inert.
func TestClaimInViews(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	f.task("", 2, false, 1)
	f.task("a", 3, false)
	f.task("b", 3, false)
	f.claimsFile("1:"+tok(1), "2", "3")
	for _, tc := range []struct{ op, doc, want string }{
		{"show", `{"id": 1}`, `"locked":true`},
		{"why", `{"id": 2, "include_tasks": true, "fields": ["claim"]}`, `"tasks":[{"id":2,"claim":{"claimed_at":"2026-09-25T09:00:01Z","extra":{"by":"holder-2","n":1.50},"locked":false}},{"id":1,"claim":{"claimed_at":"2026-09-25T09:00:00Z","extra":{"by":"holder-1","n":1.50},"locked":true}}]`},
		{"list", `{"claimed": true, "fields": ["id"]}`, `"tasks":[{"id":1},{"id":2}],"total":2`},
	} {
		if _, l := f.do(tc.op, tc.doc); !strings.Contains(l, tc.want) {
			t.Errorf("%s %s: %s\nwant %s", tc.op, tc.doc, l, tc.want)
		}
	}
	_, l := f.do("show", `{"id": 3}`)
	if strings.Count(l, `"claim":null`) != 2 || strings.Contains(l, `"claim":{`) {
		t.Errorf("show of a duplicated ID: %s", l)
	}
	if _, l := f.do("claim", `{"id": 3}`); outcome(t, l) != "conflict duplicate-id [3]" {
		t.Errorf("claim of a duplicated ID: %s", l)
	}
}

// While a folder can't be listed, an entry whose task isn't found is not
// called inert: the task may be in that folder, and repair must not drop a
// live claim.
func TestClaimInertNeedsWholeTree(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, true)
	f.task("locked", 3, false)
	f.claimsFile("1", "3", "8")
	if err := os.Chmod(filepath.Join(f.root, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(f.root, "locked"), 0o755)
	_, l := f.do("doctor", `{"kinds": ["claim-inert"]}`)
	if !strings.Contains(l, `"count":1`) || !strings.Contains(l, `"ids":[1]`) || strings.Contains(l, `"missing"`) {
		t.Errorf("doctor: %s", l)
	}
	if _, l := f.do("repair", `{}`); f.claimed() != "3 8" {
		t.Errorf("repair: %s, claimed %q", l, f.claimed())
	}
	os.Chmod(filepath.Join(f.root, "locked"), 0o755)
	if _, l := f.do("repair", `{}`); f.claimed() != "3" {
		t.Errorf("repair with every folder listed: %s, claimed %q", l, f.claimed())
	}
}
