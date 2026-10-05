package ops

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
	"github.com/phansen314/koan/internal/store"
)

// tree is a home's contents, relative to it: a file's bytes, with the home
// written "~", or "/" for a directory. Temp files and folders are left out.
func tree(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == home {
			return err
		}
		if strings.HasPrefix(d.Name(), fsys.TempPrefix) {
			if d.IsDir() {
				return filepath.SkipDir // delete-folder's folder, renamed aside
			}
			return nil
		}
		rel, _ := filepath.Rel(home, p)
		if d.IsDir() {
			out[rel] = "/"
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = strings.ReplaceAll(string(b), home, "~")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// midwayCase is one write, and what its partial promises once an error
// stops it midway (operations.md, each write's partial schema).
type midwayCase struct {
	name  string
	setup func(f *fixture) // nil: a fresh home with no config or tree
	input string           // ~ is the home
	// order is what the write changes, relative to the home, in the order
	// its steps make the changes; paths in one group change in one call.
	order [][]string
	// outcome is what the envelope must say when the write leaves the tree
	// at stage (0: nothing changed; len(order): everything): "ok", "error",
	// or "error partial <json>".
	outcome func(stage int) string
}

func openTask(f *fixture)       { f.task("", 5, false) }
func twoTasks(f *fixture)       { f.task("", 5, false); f.task("", 6, false) }
func replaced(stage int) string { return map[int]string{0: "error", 1: "ok"}[stage] }

var midwayCases = []midwayCase{
	{
		name:  "init",
		input: `{"root": "~/tasks"}`,
		order: [][]string{{"tasks"}, {"tasks/koan.json"}, {"cfg"}, {"cfg/config.toml"}},
		outcome: func(stage int) string {
			return []string{
				"error",
				`error partial {"root_created":true,"metadata_created":false}`,
				`error partial {"root_created":true,"metadata_created":true}`,
				`error partial {"root_created":true,"metadata_created":true}`,
				"ok",
			}[stage]
		},
	},
	{
		name:  "create",
		setup: func(*fixture) {},
		input: `{"title": "x", "notes": "n"}`,
		order: [][]string{{"tasks/koan.json"}, {"tasks/101.json"}, {"tasks/101.md"}},
		// Once the task file is written, create cannot fail: a missing .md
		// is a warning.
		outcome: func(stage int) string {
			return []string{"error", `error partial {"id":101}`, "ok", "ok"}[stage]
		},
	},
	{
		name:  "create-batch",
		setup: func(*fixture) {},
		input: `{"tasks": [{"ref": "a", "title": "x", "folder": "/p/q", "notes": "n"}]}`,
		order: [][]string{{"tasks/p"}, {"tasks/p/q"}, {"tasks/koan.json"}, {"tasks/p/q/101.json"}, {"tasks/p/q/101.md"}},
		// One task: a failure writing a .md is a warning and the batch goes
		// on, so with several tasks the stages would not be in a line
		// (TestCreateBatchCases has those partials). Once the task file is
		// written, the task is created.
		outcome: func(stage int) string {
			return []string{
				"error",
				`error partial {"folders_created":["/p"],"consumed":[],"ids":[],"refs":{}}`,
				`error partial {"folders_created":["/p","/p/q"],"consumed":[],"ids":[],"refs":{}}`,
				`error partial {"folders_created":["/p","/p/q"],"consumed":[101],"ids":[],"refs":{}}`,
				"ok",
				"ok",
			}[stage]
		},
	},
	{
		name:  "create-folder",
		setup: func(*fixture) {},
		input: `{"folder": "/a/b", "parents": true}`,
		order: [][]string{{"tasks/a"}, {"tasks/a/b"}},
		outcome: func(stage int) string {
			return []string{"error", `error partial {"created":["/a"]}`, "ok"}[stage]
		},
	},
	{name: "done", setup: openTask, input: `{"id": 5}`, order: [][]string{{"tasks/5.json"}}, outcome: replaced},
	{
		name: "reopen", setup: func(f *fixture) { f.task("", 5, true) },
		input: `{"id": 5}`, order: [][]string{{"tasks/5.json"}}, outcome: replaced,
	},
	{
		name: "update", setup: openTask,
		input: `{"id": 5, "title": "y", "tags": {"add": ["x"]}}`, order: [][]string{{"tasks/5.json"}}, outcome: replaced,
	},
	{
		name: "block", setup: twoTasks,
		input: `{"id": 5, "blockers": [6]}`, order: [][]string{{"tasks/5.json"}}, outcome: replaced,
	},
	{
		name: "unblock", setup: func(f *fixture) { f.task("", 5, false, 6); f.task("", 6, false) },
		input: `{"id": 5, "blockers": [6]}`, order: [][]string{{"tasks/5.json"}}, outcome: replaced,
	},
	{
		name: "delete", setup: func(f *fixture) { f.task("", 5, false); f.write("tasks/5.md", "n"); f.task("", 6, false, 5) },
		input: `{"id": 5}`, order: [][]string{{"tasks/6.json"}, {"tasks/5.json"}, {"tasks/5.md"}},
		// Once the task file is removed, delete cannot fail: removing the
		// .md is cleanup.
		outcome: func(stage int) string {
			return []string{"error", `error partial {"dependents":[6]}`, "ok", "ok"}[stage]
		},
	},
	{
		name: "delete-folder", setup: func(f *fixture) { f.task("p/a", 5, false); f.task("", 6, false, 5) },
		input: `{"folder": "/p", "recursive": true}`,
		order: [][]string{{"tasks/6.json"}, {"tasks/p", "tasks/p/a", "tasks/p/a/5.json"}},
		// Once the folder is renamed aside, delete-folder cannot fail:
		// removing it is cleanup.
		outcome: func(stage int) string {
			return []string{"error", `error partial {"dependents":[6]}`, "ok"}[stage]
		},
	},
	{
		name: "move", setup: func(f *fixture) { f.task("", 5, false); f.write("tasks/5.md", "n") },
		input: `{"id": 5, "to": "/p", "parents": true}`,
		order: [][]string{{"tasks/p"}, {"tasks/p/5.md"}, {"tasks/5.json", "tasks/p/5.json"}, {"tasks/5.md"}},
		// Once the task file has moved, move cannot fail: removing the old
		// .md is cleanup.
		outcome: func(stage int) string {
			return []string{"error", `error partial {"created":["/p"]}`, `error partial {"created":["/p"]}`, "ok", "ok"}[stage]
		},
	},
	{
		name: "move-folder", setup: func(f *fixture) { f.task("a", 5, false) },
		input: `{"folder": "/a", "to": "/x/y", "parents": true}`,
		order: [][]string{{"tasks/x"}, {"tasks/a", "tasks/a/5.json", "tasks/x/y", "tasks/x/y/5.json"}},
		outcome: func(stage int) string {
			return []string{"error", `error partial {"created":["/x"]}`, "ok"}[stage]
		},
	},
}

// fixture is the home c runs in.
func (c midwayCase) fixture(t *testing.T) *fixture {
	if c.setup == nil {
		home := t.TempDir()
		return &fixture{t: t, home: home, env: Env{
			Env:   store.Env{FS: fsys.OS{}, Home: home, ConfigDir: filepath.Join(home, "cfg")},
			Clock: fixedClock,
		}}
	}
	f := newFixture(t)
	c.setup(f)
	return f
}

// run runs c in f, returning the envelope in short (see outcome), after
// checking a partial against c's partial schema.
func (c midwayCase) run(t *testing.T, f *fixture) string {
	t.Helper()
	e := Run(c.name, parse(t, strings.ReplaceAll(c.input, "~", f.home)), nil, f.env)
	line(t, e) // the envelope schema
	switch {
	case e.OK:
		return "ok"
	case e.Error.Partial == nil:
		return "error"
	}
	b, err := jsonio.MarshalLine(e.Error.Partial)
	if err != nil {
		t.Fatal(err)
	}
	if ok, at := schematest.Check(t, c.name+"-partial", b); !ok {
		t.Errorf("%s-partial rejects at %s: %s", c.name, at, b)
	}
	return "error partial " + strings.TrimSuffix(string(b), "\n")
}

// An error at each call that changes the disk in turn stops the write at one
// of its stages, and its envelope says exactly what took effect: a partial
// where the write defines one, matching the stage, and success once the
// write can no longer fail (implementation-spec.md, Crash injection, Errors
// midway).
func TestErrorsMidway(t *testing.T) {
	for _, c := range midwayCases {
		t.Run(c.name, func(t *testing.T) {
			ref := c.fixture(t)
			before := tree(t, ref.home)
			if got, want := c.run(t, ref), c.outcome(len(c.order)); got != "ok" || want != "ok" {
				t.Fatalf("uninterrupted: %s; outcome of the last stage %s; want ok for both", got, want)
			}
			after := tree(t, ref.home)
			stages := []map[string]string{maps.Clone(before)}
			cur := maps.Clone(before)
			for _, group := range c.order {
				for _, p := range group {
					if v, ok := after[p]; ok {
						cur[p] = v
					} else {
						delete(cur, p)
					}
				}
				stages = append(stages, maps.Clone(cur))
			}
			if !maps.Equal(cur, after) {
				t.Fatalf("order does not cover every change:\nbefore %v\nafter  %v", before, after)
			}

			seen := make([]bool, len(stages))
			for k := 1; ; k++ {
				if k > 50 {
					t.Fatal("still failing at k=50")
				}
				f := c.fixture(t)
				fired := false
				inject := fsys.ErrnoAtMutating(k, syscall.EIO)
				f.hook(func(op fsys.Op) error {
					err := inject(op)
					fired = fired || err != nil
					return err
				})
				got := c.run(t, f)
				if !fired {
					break // fewer than k calls change the disk
				}
				now := tree(t, f.home)
				stage := slices.IndexFunc(stages, func(m map[string]string) bool { return maps.Equal(m, now) })
				if stage < 0 {
					t.Fatalf("k=%d: tree at no stage of %q: %v", k, c.order, now)
				}
				seen[stage] = true
				if want := c.outcome(stage); got != want {
					t.Errorf("k=%d, stage %d: got %s, want %s", k, stage, got, want)
				}
			}
			// The last stage is reached only when no call fails, or when the
			// failing call's error is ignored (a temp file's removal).
			seen[len(stages)-1] = true
			for i, ok := range seen {
				if !ok {
					t.Errorf("no error left stage %d", i)
				}
			}
		})
	}
}
