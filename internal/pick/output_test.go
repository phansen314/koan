package pick

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/schematest"
)

func TestSelected(t *testing.T) {
	vs := views(
		tv{id: 1, folder: "/", r: model.Ready},
		tv{id: 2, folder: "/moved", r: model.Done, done: "2026-09-01T00:00:00Z"},
		tv{id: 3, folder: "/a", r: model.Ready, priority: p(5)},
		tv{id: 3, folder: "/b", r: model.Ready, priority: p(5)},
		tv{id: 4, folder: "/c", r: model.Ready},
	)
	for _, tc := range []struct {
		name    string
		keys    []string
		tasks   []string
		missing []model.ID
	}{
		{"none", nil, []string{}, []model.ID{}},
		// Line order, not the order marked; a task found wherever it now is.
		{"order", []string{"1@/", "2@/", "3@/b"}, []string{"3@/b", "1@/", "2@/moved"}, []model.ID{}},
		// Copies: the one in the key's folder, or missing if none is there.
		{"copies", []string{"3@/a", "3@/z"}, []string{"3@/a"}, []model.ID{3}},
		// A copy elsewhere still counts when it's the only one left.
		{"one copy left", []string{"4@/gone"}, []string{"4@/c"}, []model.ID{}},
		{"deleted", []string{"9@/", "8@/", "9@/x"}, []string{}, []model.ID{8, 9}},
		{"twice", []string{"1@/", "1@/old"}, []string{"1@/"}, []model.ID{}},
		{"not a key", []string{"x", "1"}, []string{}, []model.ID{}},
	} {
		tasks, missing := selected(vs, tc.keys)
		if !slices.Equal(keys(tasks), tc.tasks) || !slices.Equal(missing, tc.missing) {
			t.Errorf("%s: got %q, missing %v", tc.name, keys(tasks), missing)
		}
	}
}

// tree is an initialized koan tree in a home of its own, for running pick
// whole.
type tree struct {
	t    *testing.T
	env  ops.Env
	root string
	// filter is fzf --filter's fake, for --select-one and --exit-zero.
	filter func(path string, args, env []string, stdin []byte) ([]byte, int, error)
	// session is the session directory of the pick running now.
	session string
	// source is a live source's fake: what the command printed, its
	// stderr, and whether it timed out.
	source func(command string, limit time.Duration) (stdout, stderr string, timedOut bool)
}

func newTestTree(t *testing.T) *tree {
	home := t.TempDir()
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	tr := &tree{t: t, env: ops.NewEnv(getenv, runtime.GOOS), root: filepath.Join(home, "tasks")}
	tr.run("init", map[string]any{"root": tr.root})
	return tr
}

// run runs an operation, which must succeed.
func (tr *tree) run(op string, in map[string]any) ops.Envelope {
	tr.t.Helper()
	b, _ := json.Marshal(in)
	obj, _, err := jsonio.ParseObject(b)
	if err != nil {
		tr.t.Fatal(err)
	}
	out := ops.Run(op, obj, nil, tr.env)
	if !out.OK {
		tr.t.Fatalf("%s: %+v", op, out.Error)
	}
	return out
}

// fzfDoes is a fake fzf: it runs do, which plays the person's keys by
// calling the helper as fzf's callbacks would, in fzf's environment, and
// exits with status.
type fzfDoes struct {
	do     func(t *testing.T, helper func(args ...string) string)
	status int
}

func (tr *tree) pick(in map[string]any, fzf fzfDoes) (ops.Envelope, []byte) {
	tr.t.Helper()
	runtimeDir := tr.t.TempDir()
	sys := System{
		LookPath:   func(string) (string, error) { return "/bin/fzf", nil },
		Output:     func(string, []string, []string) ([]byte, []byte, int, error) { return []byte("0.63.0\n"), nil, 0, nil },
		Environ:    func() []string { return []string{"XDG_RUNTIME_DIR=" + runtimeDir} },
		Executable: func() (string, error) { return "/bin/koan", nil },
		OpenTTY:    func() error { return nil },

		CatchInterrupts: func() func() { return func() {} },
		Filter:          tr.filter,
	}
	sys.RunSource = func(command string, _ []string, limit time.Duration) ([]byte, []byte, bool, error) {
		out, errOut, timedOut := tr.source(command, limit)
		return []byte(out), []byte(errOut), timedOut, nil
	}
	sys.RunFzf = func(_ string, _ []string, env []string, _ []byte) (int, error) {
		tr.session = lookupEnv(env, SessionVar)
		helper := func(args ...string) string {
			hsys := sys
			hsys.Environ = func() []string { return env }
			out, _, e := Helper(args, Env{Ops: tr.env, Sys: hsys})
			if e != nil {
				tr.t.Fatalf("helper %q: %v", args, e)
			}
			return string(out)
		}
		if fzf.do != nil {
			fzf.do(tr.t, helper)
		}
		return fzf.status, nil
	}
	b, _ := json.Marshal(in)
	obj, _, _ := jsonio.ParseObject(b)
	out := Run(obj, nil, Env{Ops: tr.env, Sys: sys})
	line, err := jsonio.MarshalLine(out)
	if err != nil {
		tr.t.Fatal(err)
	}
	if ok, f := schematest.Check(tr.t, "envelope", line); !ok {
		tr.t.Errorf("envelope rejected at %s: %s", f, line)
	}
	if des, _ := os.ReadDir(runtimeDir); len(des) != 0 {
		tr.t.Errorf("session left behind")
	}
	return out, line
}

// result is the result object of an ok envelope line, checked against
// pick-output.
func result(t *testing.T, line []byte) []byte {
	t.Helper()
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	json.Unmarshal(line, &env)
	if ok, f := schematest.Check(t, "pick-output", env.Result); !ok {
		t.Errorf("pick-output rejected at %s: %s", f, env.Result)
	}
	return env.Result
}

func TestRunOutcomes(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/a"})
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title, "folder": "/a"})
	}
	tr.run("update", map[string]any{"id": 3, "priority": 1})

	// Enter on marked lines: line order, as the final read has them.
	out, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		if got := helper("enter", "", "1@/a", "3@/a"); got != "accept" {
			t.Errorf("enter printed %q", got)
		}
	}})
	if !out.OK || !strings.Contains(string(result(t, line)), `"title":"three"`) {
		t.Fatalf("enter: %s", line)
	}
	if got := keys(out.Result.(Output).Tasks.Views); !slices.Equal(got, []string{"3@/a", "1@/a"}) {
		t.Errorf("enter: %q", got)
	}

	// Fields shape the tasks; actions and notes_edited are empty.
	_, line = tr.pick(map[string]any{"fields": []string{"title"}}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("enter", "", "2@/a")
	}})
	if got := string(result(t, line)); got != `{"tasks":[{"id":2,"title":"two"}],"missing":[],"actions":[],"notes_edited":[]}` {
		t.Errorf("fields: %s", got)
	}

	// Changed meanwhile: a moved task as it now is, a deleted one missing.
	// Enter's status is 0, or 1 when the query matches nothing: both are
	// Enter, as the selection is recorded.
	tr.run("create-folder", map[string]any{"folder": "/b"})
	_, line = tr.pick(map[string]any{}, fzfDoes{status: 1, do: func(t *testing.T, helper func(...string) string) {
		tr.run("move", map[string]any{"id": 1, "to": "/b"})
		tr.run("delete", map[string]any{"id": 2})
		helper("enter", "", "1@/a", "2@/a")
	}})
	if got := string(result(t, line)); !strings.Contains(got, `"folder":"/b"`) || !strings.Contains(got, `"missing":[2]`) {
		t.Errorf("changed meanwhile: %s", got)
	}

	// Quit, and Enter with nothing under the cursor: an empty selection.
	for _, do := range []func(t *testing.T, helper func(...string) string){
		func(t *testing.T, helper func(...string) string) { helper("quit") },
		func(t *testing.T, helper func(...string) string) { helper("enter", "") },
	} {
		_, line := tr.pick(map[string]any{}, fzfDoes{do: do, status: 1})
		if got := string(result(t, line)); got != `{"tasks":[],"missing":[],"actions":[],"notes_edited":[]}` {
			t.Errorf("empty selection: %s", got)
		}
	}

	// Without a selection: 130 is cancel, anything else fzf-failed.
	for status, want := range map[int]string{
		130: `{"kind":"cancelled","message":"cancelled","details":{"actions":[]}}`,
		0:   `"details":{"reason":"fzf-failed","status":0,"actions":[]}`,
		2:   `"details":{"reason":"fzf-failed","status":2,"actions":[]}`,
	} {
		out, line := tr.pick(map[string]any{}, fzfDoes{status: status})
		if out.OK || !strings.Contains(string(line), want) {
			t.Errorf("status %d: %s", status, line)
		}
	}
}

// The final read fails: incomplete, with its error whole.
func TestRunIncomplete(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one"})
	out, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("enter", "", "1@/")
		cfg := filepath.Join(tr.env.ConfigDir, "config.toml")
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
	}})
	if out.OK || out.Error.Kind != errs.KindIncomplete {
		t.Fatalf("got %s", line)
	}
	d, _ := json.Marshal(out.Error.Details)
	if ok, f := schematest.Check(t, "pick-error-details#/$defs/incomplete", d); !ok {
		t.Errorf("details rejected at %s: %s", f, d)
	}
	if !strings.Contains(string(d), `"actions":[],"error":{"kind":"not-initialized"`) {
		t.Errorf("details %s", d)
	}
}

// --select-one and --exit-zero decide without the picker when they can
// (pick-spec.md, Selecting at once); otherwise the picker opens with the
// query typed.
func TestRunAtOnce(t *testing.T) {
	tr := newTestTree(t)
	for _, title := range []string{"one", "two"} {
		tr.run("create", map[string]any{"title": title})
	}
	type fake struct {
		out    string
		status int
	}
	run := func(in map[string]any, f fake) (ops.Envelope, []byte, []string, bool) {
		t.Helper()
		var filterArgs []string
		picked := false
		tr.filter = func(_ string, args, _ []string, stdin []byte) ([]byte, int, error) {
			filterArgs = args
			if got := string(stdin); !strings.HasPrefix(got, "1@/\t") || strings.Count(got, "\n") != 2 {
				t.Errorf("filter stdin %q", got)
			}
			return []byte(f.out), f.status, nil
		}
		out, line := tr.pick(in, fzfDoes{status: 130, do: func(*testing.T, func(...string) string) { picked = true }})
		tr.filter = nil
		return out, line, filterArgs, picked
	}

	_, line, args, picked := run(map[string]any{"select_one": true, "query": "on", "fields": []string{"title"}}, fake{out: "1@/\t●  1 …\tone\n"})
	if got := string(result(t, line)); got != `{"tasks":[{"id":1,"title":"one"}],"missing":[],"actions":[],"notes_edited":[]}` || picked {
		t.Errorf("select-one, one match: %s, picker %v", got, picked)
	}
	if n := len(args); n < 2 || args[n-2] != "--filter" || args[n-1] != "on" || !slices.Contains(args, "--nth") {
		t.Errorf("filter args %q", args)
	}

	_, line, _, picked = run(map[string]any{"exit_zero": true, "query": "zz"}, fake{status: 1})
	if got := string(result(t, line)); got != `{"tasks":[],"missing":[],"actions":[],"notes_edited":[]}` || picked {
		t.Errorf("exit-zero, no match: %s, picker %v", got, picked)
	}

	for _, tc := range []struct {
		name string
		in   map[string]any
		f    fake
	}{
		{"select-one, two matches", map[string]any{"select_one": true}, fake{out: "1@/\ta\tone\n2@/\tb\ttwo\n"}},
		{"select-one, no match", map[string]any{"select_one": true}, fake{status: 1}},
		{"exit-zero, a match", map[string]any{"exit_zero": true}, fake{out: "1@/\ta\tone\n"}},
	} {
		out, line, _, picked := run(tc.in, tc.f)
		if !picked || out.OK || out.Error.Kind != errs.KindCancelled {
			t.Errorf("%s: picker %v, %s", tc.name, picked, line)
		}
	}

	out, line, _, picked := run(map[string]any{"exit_zero": true}, fake{status: 2})
	if out.OK || picked || !strings.Contains(string(line), `"details":{"reason":"fzf-failed","status":2,"actions":[]}`) {
		t.Errorf("filter fails: %s", line)
	}
}

func TestDecideAtOnce(t *testing.T) {
	one, two, none := []string{"1@/"}, []string{"1@/", "2@/"}, []string{}
	for _, tc := range []struct {
		matched             []string
		selectOne, exitZero bool
		keys                []string
		done                bool
	}{
		{one, true, false, one, true},
		{one, true, true, one, true},
		{two, true, true, nil, false},
		{none, true, false, nil, false},
		{none, false, true, none, true},
		{none, true, true, none, true},
		{one, false, true, nil, false},
	} {
		keys, done := decideAtOnce(tc.matched, tc.selectOne, tc.exitZero)
		if done != tc.done || !slices.Equal(keys, tc.keys) {
			t.Errorf("%v select %v exit %v: %v %v", tc.matched, tc.selectOne, tc.exitZero, keys, done)
		}
	}
}
