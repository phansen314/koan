package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/schematest"
	"github.com/phansen314/koan/internal/store"
)

// world is a HOME of its own with a fixed clock.
type world struct {
	t   *testing.T
	env store.Env
	now time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	w.env = store.Env{Home: t.TempDir(), Now: func() time.Time { return w.now }, FS: fsys.FS{}, LockWait: 200 * time.Millisecond}
	return w
}

// ready is a world with init run.
func ready(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.ok("init", `{}`)
	return w
}

// root is the path on disk of an entry of koan's, named as koan names it
// before the @: root("p/3.json") is <root>/@p/@3.json. A hidden name keeps
// its own. disk takes names as they are on disk, for material.
func (w *world) root(rel ...string) string {
	var segs []string
	for _, r := range rel {
		for _, s := range strings.Split(r, "/") {
			if !strings.HasPrefix(s, ".") {
				s = "@" + s
			}
			segs = append(segs, s)
		}
	}
	return w.disk(segs...)
}

// paths takes the paths of a list of folder records, as JSON.
func paths(records any) string {
	var ps []string
	for _, r := range records.([]any) {
		ps = append(ps, r.(map[string]any)["path"].(string))
	}
	data, _ := json.Marshal(ps)
	return string(data)
}

func (w *world) disk(rel ...string) string {
	return filepath.Join(append([]string{w.env.RootPath()}, rel...)...)
}

// run runs a command and checks its envelope against the schemas.
func (w *world) run(name, input string) Envelope {
	w.t.Helper()
	env := Run(w.env, name, []byte(input))
	data, err := json.Marshal(env)
	if err != nil {
		w.t.Fatalf("%s: encode: %v", name, err)
	}
	if err := schematest.Check(w.t, "envelope", data); err != nil {
		w.t.Errorf("%s %s: envelope: %v\n%s", name, input, err, data)
	}
	if env.OK {
		schematest.Must(w.t, schemaName(name)+"-output", env.Result)
	}
	// The input schema and the command must agree on what is valid.
	if json.Valid([]byte(input)) {
		schemaErr := schematest.Check(w.t, schemaName(name)+"-input", []byte(input))
		invalid := !env.OK && env.Error.Kind == errs.InvalidInput
		if (schemaErr != nil) != invalid && !crossField(env) {
			w.t.Errorf("%s %s: schema says %v, command says invalid=%v", name, input, schemaErr, invalid)
		}
	}
	return env
}

// crossField reports whether an invalid-input error is about a rule between
// fields, which the schemas do not express.
func crossField(env Envelope) bool {
	if env.OK || env.Error.Kind != errs.InvalidInput {
		return false
	}
	for _, p := range env.Error.Problems {
		for _, s := range []string{"may not be", "is also in", "is the task itself", "earlier task", "is used by",
			"is empty", "is longer than", "control character", "gives nothing", "same task as", "between it and folders"} {
			if strings.Contains(p.Reason, s) {
				return true
			}
		}
	}
	return false
}

// ok runs a command that must succeed and returns its result as JSON-decoded
// data.
func (w *world) ok(name, input string) map[string]any {
	w.t.Helper()
	env := w.run(name, input)
	if !env.OK {
		w.t.Fatalf("%s %s: %s", name, input, env.Error)
	}
	data, _ := json.Marshal(env.Result)
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

// fail runs a command that must fail with the given kind and, for a
// conflict, rule.
func (w *world) fail(name, input string, kind errs.Kind, rule string) *errs.Error {
	w.t.Helper()
	env := w.run(name, input)
	if env.OK {
		w.t.Fatalf("%s %s: succeeded, want %s %s", name, input, kind, rule)
	}
	if env.Error.Kind != kind || (rule != "" && !env.Error.HasRule(rule)) {
		w.t.Fatalf("%s %s: got %s, want %s %s", name, input, env.Error, kind, rule)
	}
	return env.Error
}

// task returns the `task` of a result.
func task(m map[string]any) map[string]any { return m["task"].(map[string]any) }

func ids(v any) []int64 {
	out := []int64{}
	for _, x := range v.([]any) {
		switch x := x.(type) {
		case float64:
			out = append(out, int64(x))
		case map[string]any:
			out = append(out, int64(x["id"].(float64)))
		}
	}
	return out
}

func wantIDs(t *testing.T, what string, got any, want ...int64) {
	t.Helper()
	if g := ids(got); !slices.Equal(g, want) {
		t.Errorf("%s: got %v, want %v", what, g, want)
	}
}

func readJSON(t *testing.T, p string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (w *world) file(rel string) string {
	w.t.Helper()
	data, err := os.ReadFile(w.root(rel))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(data)
}

// findings lists doctor's findings as "kind paths", the paths relative to
// the root.
func (w *world) findings() []string {
	var fs []string
	for _, f := range w.ok("doctor", `{}`)["findings"].([]any) {
		f := f.(map[string]any)
		var paths []string
		for _, p := range f["paths"].([]any) {
			paths = append(paths, strings.TrimPrefix(p.(string), w.disk()+"/"))
		}
		fs = append(fs, f["kind"].(string)+" "+strings.Join(paths, ","))
	}
	return fs
}

// record is the folder record in a result, as JSON with its keys sorted
// and the root's path cut to ~, for comparing.
func (w *world) record(res map[string]any) string {
	data, _ := json.Marshal(res["folder"])
	return strings.ReplaceAll(string(data), w.disk(), "~")
}

// schemaName is the name of a command's schemas: "task-create" for
// "task create".
func schemaName(command string) string { return strings.ReplaceAll(command, " ", "-") }
