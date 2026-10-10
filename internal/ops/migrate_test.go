package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/migrations"
	"github.com/phansen314/koan/internal/schematest"
)

// No released step covers task files yet, so these tests add a second,
// test-only step: task files at schema 0 call their title `name`, and step 2
// renames it. With it the latest step is 2.
const taskSchema0 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task-file-0",
  "type": "object"
}`

func useRenameStep(t *testing.T) {
	t.Helper()
	real := migrations.Steps()
	step := migrations.Step{Number: 2, Name: "rename-title", Task: &migrations.Format{
		From:   0,
		Schema: taskSchema0,
		Check: func(obj *jsonio.Object, repeated []string) []errs.Problem {
			if v, ok := obj.Get("name"); !ok {
				return []errs.Problem{{Field: "/name", Reason: "required"}}
			} else if _, ok := v.(string); !ok {
				return []errs.Problem{{Field: "/name", Reason: "expected a string"}}
			}
			return nil
		},
		Convert: func(obj *jsonio.Object) (*jsonio.Object, error) {
			out := &jsonio.Object{}
			for _, m := range obj.Members {
				switch m.Key {
				case "schema":
					m.Value = json.Number("1")
				case "name":
					m.Key = "title"
				}
				out.Members = append(out.Members, m)
			}
			return out, nil
		},
	}}
	t.Cleanup(migrations.UseSteps(append(real, step)))
}

// taskJSON is a task file as koan writes it, at schema 1 with a title, or at
// schema 0 with a name.
func taskJSON(schema int, id int, title, updated string) string {
	key := "title"
	if schema == 0 {
		key = "name"
	}
	return fmt.Sprintf(`{
  "schema": %d,
  "id": %d,
  %q: %q,
  "priority": 3,
  "created_at": "2026-09-20T18:31:51Z",
  "completed_at": null,
  "updated_at": %q,
  "blocked_by": [],
  "tags": [
    "travel"
  ],
  "extra": {
    "z": 1.10,
    "big": 12345678901234567890,
    "text": "héllo ✓ <&>",
    "deep": {
      "b": [
        1e2,
        null
      ],
      "a": true
    }
  }
}
`, schema, id, key, title, updated)
}

const metaCurrent = "{\n  \"schema\": 2,\n  \"migration\": 2\n}\n"

func (f *fixture) migrate(input string) (Envelope, MigrateOutput) {
	f.t.Helper()
	e := Run("migrate", parse(f.t, input), nil, f.env)
	b, err := jsonio.MarshalLine(e)
	if err != nil {
		f.t.Fatal(err)
	}
	if !e.OK {
		line(f.t, e)
		return e, MigrateOutput{}
	}
	if ok, fl := schematest.Check(f.t, "migrate-output", mustMarshal(f.t, e.Result)); !ok {
		f.t.Errorf("migrate-output rejects at %s: %s", fl, b)
	}
	line(f.t, e)
	return e, e.Result.(MigrateOutput)
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := jsonio.MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// snapshot is every file under the home, by path.
func (f *fixture) snapshot() map[string]string {
	f.t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(f.home, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[strings.TrimPrefix(p, f.home+"/")] = string(b)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) renames() *[]string {
	var got []string
	f.hook(func(o fsys.Op) error {
		if o.Name == fsys.OpRename {
			got = append(got, o.NewPath)
		}
		return nil
	})
	return &got
}

// Tree fixtures under internal/migrations/testdata/step-<n>, as a binary
// whose latest step is n wrote them, migrate to exactly the bytes of golden/.
func TestMigrateFixtures(t *testing.T) {
	root := filepath.Join("..", "migrations", "testdata")
	golden := readTree(t, filepath.Join(root, "golden"))
	dirs, err := filepath.Glob(filepath.Join(root, "step-*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no step fixtures: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			f := newFixture(t)
			f.remove("tasks")
			f.remove(stateRel)
			copyTree(t, dir, f.root)
			wantPending := !reflect.DeepEqual(readTree(t, dir), golden)
			e, out := f.migrate(`{}`)
			if !e.OK {
				t.Fatalf("%+v", e.Error)
			}
			if out.Changed != wantPending || out.StateWritten != wantPending || out.To != migrations.Latest() || len(e.Warnings) != 0 {
				t.Errorf("output %+v warnings %v", out, e.Warnings)
			}
			if got := readTree(t, f.root); !reflect.DeepEqual(got, golden) {
				t.Errorf("migrated tree differs from golden:\n got %v\nwant %v", got, golden)
			}
			if got := f.read(stateRel); got != st(3) {
				t.Errorf("state file %q, want last_id 3", got)
			}
			// Idempotence: a second run changes nothing.
			e, out = f.migrate(`{}`)
			if !e.OK || out.Changed || out.MetadataConverted || out.TasksConverted != 0 || len(out.Applied) != 0 || out.From != out.To {
				t.Errorf("second run: %+v %+v", out, e.Error)
			}
			if got := readTree(t, f.root); !reflect.DeepEqual(got, golden) {
				t.Errorf("second run changed the tree")
			}
		})
	}
}

func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	for rel, data := range readTree(t, from) {
		p := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// koan.json from schema 1 is converted; the step recorded is the latest, and
// the run reports what it did.
func TestMigrateOutputFromSchema1(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	task := f.read("tasks/1.json")
	e, out := f.migrate(`{}`)
	if !e.OK {
		t.Fatal(e.Error)
	}
	want := MigrateOutput{From: 0, To: 1, Applied: []AppliedStep{{1, "tree-marker"}}, Changed: true, MetadataConverted: true, Unconverted: []Unconverted{}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %+v\nwant %+v", out, want)
	}
	if got := f.read("tasks/koan.json"); got != "{\n  \"schema\": 2,\n  \"migration\": 1\n}\n" {
		t.Errorf("koan.json %q", got)
	}
	if f.read("tasks/1.json") != task {
		t.Error("a task file changed")
	}
	// The root is usable now.
	if got := opError(t, f, "show", `{"id": 1}`); got != "" {
		t.Errorf("show: %s", got)
	}
}

// A step behind, with the format current: only the counter moves.
func TestMigrateBehindOnly(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 2, "migration": 0}`)
	_, out := f.migrate(`{}`)
	if !out.Changed || out.MetadataConverted || out.From != 0 || len(out.Applied) != 1 {
		t.Errorf("%+v", out)
	}
	if got := f.read("tasks/koan.json"); got != "{\n  \"schema\": 2,\n  \"migration\": 1\n}\n" {
		t.Errorf("koan.json %q", got)
	}
}

// dry_run writes nothing, and reports what the real run does.
func TestMigrateDryRun(t *testing.T) {
	useRenameStep(t)
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	f.write("tasks/1.json", taskJSON(0, 1, "a", "2026-09-21T10:00:00Z"))
	f.write("tasks/p/2.json", taskJSON(0, 2, "b", "2026-09-21T10:00:00Z"))
	f.write("tasks/3.json", "{")
	before := f.snapshot()
	_, dry := f.migrate(`{"dry_run": true}`)
	if !reflect.DeepEqual(f.snapshot(), before) {
		t.Error("dry_run wrote")
	}
	_, real := f.migrate(`{}`)
	dry.DryRun = false
	if !reflect.DeepEqual(dry, real) {
		t.Errorf("dry %+v\nreal %+v", dry, real)
	}
	if real.TasksConverted != 2 || !real.MetadataConverted || len(real.Applied) != 2 {
		t.Errorf("%+v", real)
	}
}

// A task file converted keeps every value, updated_at and extra included,
// and its notes are untouched; the files are written in tree order, koan.json
// last.
func TestMigrateTasks(t *testing.T) {
	useRenameStep(t)
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	f.write("tasks/2.json", taskJSON(0, 2, "second", "2026-09-22T11:11:11Z"))
	f.write("tasks/2.md", "notes\n")
	f.write("tasks/a/1.json", taskJSON(0, 1, "first", "2026-09-21T10:00:00Z"))
	f.write("tasks/10.json", taskJSON(1, 10, "current", "2026-09-23T10:00:00Z"))
	renames := f.renames()
	e, out := f.migrate(`{}`)
	if !e.OK || out.TasksConverted != 2 || out.Unconverted == nil || out.UnconvertedCount != 0 {
		t.Fatalf("%+v %+v", out, e.Error)
	}
	if want := []string{"2.json", "a/1.json", "koan.json"}; !slices.Equal(*renames, want) {
		t.Errorf("written %v, want %v", *renames, want)
	}
	for rel, want := range map[string]string{
		"tasks/2.json":    taskJSON(1, 2, "second", "2026-09-22T11:11:11Z"),
		"tasks/a/1.json":  taskJSON(1, 1, "first", "2026-09-21T10:00:00Z"),
		"tasks/10.json":   taskJSON(1, 10, "current", "2026-09-23T10:00:00Z"),
		"tasks/2.md":      "notes\n",
		"tasks/koan.json": metaCurrent,
	} {
		if got := f.read(rel); got != want {
			t.Errorf("%s:\n got %q\nwant %q", rel, got, want)
		}
	}
}

// Files a merge brings in, in an older format, into a tree whose counter is
// current: converted, with nothing applied and koan.json untouched. A
// lowered counter is restored without rewriting any task file.
func TestMigrateMerge(t *testing.T) {
	useRenameStep(t)
	f := newFixture(t)
	f.write("tasks/koan.json", metaCurrent)
	f.write("tasks/1.json", taskJSON(1, 1, "a", "2026-09-21T10:00:00Z"))
	f.write("tasks/2.json", taskJSON(0, 2, "b", "2026-09-21T10:00:00Z"))
	renames := f.renames()
	_, out := f.migrate(`{}`)
	if !out.Changed || out.MetadataConverted || out.TasksConverted != 1 || len(out.Applied) != 0 || out.From != 2 {
		t.Errorf("%+v", out)
	}
	if !slices.Equal(*renames, []string{"2.json"}) || f.read("tasks/koan.json") != metaCurrent {
		t.Errorf("written %v", *renames)
	}
	if f.read("tasks/2.json") != taskJSON(1, 2, "b", "2026-09-21T10:00:00Z") {
		t.Error("task 2 not converted")
	}

	f.write("tasks/koan.json", "{\n  \"schema\": 2,\n  \"migration\": 1\n}\n")
	*renames = nil
	_, out = f.migrate(`{}`)
	if !out.Changed || out.TasksConverted != 0 || len(out.Applied) != 1 || out.Applied[0].Step != 2 {
		t.Errorf("%+v", out)
	}
	if !slices.Equal(*renames, []string{"koan.json"}) || f.read("tasks/koan.json") != metaCurrent {
		t.Errorf("written %v, koan.json %q", *renames, f.read("tasks/koan.json"))
	}
}

// Task files in an older format that no step can convert are left as they
// are and listed in tree order; the latest step is recorded anyway.
func TestMigrateUnconverted(t *testing.T) {
	useRenameStep(t)
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	f.write("tasks/1.json", taskJSON(0, 1, "good", "2026-09-21T10:00:00Z"))
	f.write("tasks/p/2.json", strings.Replace(taskJSON(0, 2, "x", "2026-09-21T10:00:00Z"), `"name": "x"`, `"nom": "x"`, 1))
	f.write("tasks/p/3.json", taskJSON(0, 3, "", "2026-09-21T10:00:00Z")) // converted, the title is empty
	bad2, bad3 := f.read("tasks/p/2.json"), f.read("tasks/p/3.json")
	_, out := f.migrate(`{}`)
	if out.TasksConverted != 1 || out.UnconvertedCount != 2 || len(out.Unconverted) != 2 {
		t.Fatalf("%+v", out)
	}
	if u := out.Unconverted[0]; u.ID != 2 || u.Schema != 0 || f.rel(u.Path) != "~/tasks/p/2.json" || !strings.Contains(u.Detail, "/name") {
		t.Errorf("first %+v", u)
	}
	if u := out.Unconverted[1]; u.ID != 3 || !strings.Contains(u.Detail, "/title") {
		t.Errorf("second %+v", u)
	}
	if f.read("tasks/p/2.json") != bad2 || f.read("tasks/p/3.json") != bad3 {
		t.Error("an unconverted file changed")
	}
	if f.read("tasks/koan.json") != metaCurrent {
		t.Errorf("koan.json %q", f.read("tasks/koan.json"))
	}
	// doctor still reports both.
	got := f.rel(opDoctor(t, f))
	if !strings.Contains(got, `"kind":"old-format","class":"manual","count":2`) {
		t.Errorf("doctor: %s", got)
	}
}

// The list is capped at 20; the count is not.
func TestMigrateUnconvertedCap(t *testing.T) {
	useRenameStep(t)
	f := newFixture(t)
	f.write("tasks/koan.json", metaCurrent)
	for id := 1; id <= 25; id++ {
		f.write(fmt.Sprintf("tasks/%d.json", id), strings.Replace(taskJSON(0, id, "x", "2026-09-21T10:00:00Z"), `"name": "x"`, `"nom": "x"`, 1))
	}
	_, out := f.migrate(`{}`)
	if out.UnconvertedCount != 25 || len(out.Unconverted) != 20 || out.Changed {
		t.Errorf("count %d, listed %d, changed %v", out.UnconvertedCount, len(out.Unconverted), out.Changed)
	}
}

// Files migrate can't reach are warnings; the rest is converted and the
// latest step recorded. Once they can be read, doctor reports them and a
// second migrate converts them.
func TestMigrateUnreachable(t *testing.T) {
	useRenameStep(t)
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	f.write("tasks/1.json", taskJSON(0, 1, "good", "2026-09-21T10:00:00Z"))
	f.write("tasks/2.json", taskJSON(0, 2, "unreadable", "2026-09-21T10:00:00Z"))
	f.write("tasks/3.json", "{")                                                                                           // format can't be told
	f.write("tasks/4.json", strings.Replace(taskJSON(1, 4, "x", "2026-09-21T10:00:00Z"), `"schema": 1`, `"schema": 9`, 1)) // unknown format
	f.write("tasks/5.json", strings.Replace(taskJSON(1, 5, "x", "2026-09-21T10:00:00Z"), `"priority": 3`, `"priority": "high"`, 1))
	f.write("tasks/q/6.json", taskJSON(0, 6, "in a folder", "2026-09-21T10:00:00Z"))
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	f.hook(func(o fsys.Op) error {
		if o.Name == fsys.OpReadFile && o.Path == "2.json" {
			return syscall.EACCES
		}
		if o.Name == fsys.OpReadDir && o.Path == "q" {
			return syscall.EACCES
		}
		return nil
	})
	bad5 := f.read("tasks/5.json")
	e, out := f.migrate(`{}`)
	if !e.OK || out.TasksConverted != 1 || out.UnconvertedCount != 0 {
		t.Fatalf("%+v %+v", out, e.Error)
	}
	var got []string
	for _, w := range e.Warnings {
		got = append(got, f.rel(fmt.Sprintf("%s %v %v %s", w.Kind, w.IDs, w.Paths, w.Reason)))
	}
	want := []string{
		"unreadable-folder [] [~/tasks/q] ",
		"unusable-file [2] [~/tasks/2.json] unreadable",
		"unusable-file [3] [~/tasks/3.json] corrupt",
		"unusable-file [4] [~/tasks/4.json] unsupported-format",
	}
	if !slices.Equal(got, want) {
		t.Errorf("warnings:\n got %q\nwant %q", got, want)
	}
	if f.read("tasks/koan.json") != metaCurrent || f.read("tasks/5.json") != bad5 {
		t.Error("koan.json or the damaged current-format file wrong")
	}
	if f.read("tasks/1.json") != taskJSON(1, 1, "good", "2026-09-21T10:00:00Z") {
		t.Error("task 1 not converted")
	}

	// Readable again: doctor sees both old files; migrate converts them.
	f.env.FS = fsys.OS{}
	d := opDoctor(t, f)
	if !strings.Contains(d, `"kind":"old-format","class":"manual","count":2`) {
		t.Errorf("doctor: %s", d)
	}
	e, out = f.migrate(`{}`)
	if !e.OK || out.TasksConverted != 2 || len(out.Applied) != 0 || len(e.Warnings) != 2 {
		t.Errorf("second run: %+v %v %+v", out, e.Warnings, e.Error)
	}
	if f.read("tasks/2.json") != taskJSON(1, 2, "unreadable", "2026-09-21T10:00:00Z") || f.read("tasks/q/6.json") != taskJSON(1, 6, "in a folder", "2026-09-21T10:00:00Z") {
		t.Error("files not converted")
	}
}

// opDoctor is doctor's envelope as a line.
func opDoctor(t *testing.T, f *fixture) string {
	t.Helper()
	return line(t, Run("doctor", parse(t, `{}`), nil, f.env))
}

// A koan.json migrate can't use is its own error, after busy; nothing is
// written.
func TestMigrateRefusals(t *testing.T) {
	useRenameStep(t)
	for _, tc := range []struct{ name, meta, want string }{
		{"newer schema", `{"schema": 3, "last_id": 1}`, `unsupported-format {"path":"~/tasks/koan.json","found":3,"supported":[2]}`},
		{"schema 0", `{"schema": 0, "last_id": 1}`, `unsupported-format {"path":"~/tasks/koan.json","found":0,"supported":[2]}`},
		{"past the latest step", `{"schema": 2, "migration": 3}`, `unsupported-format {"path":"~/tasks/koan.json","found":3,"supported":[2],"field":"migration"}`},
		{"older, breaking its rules", `{"schema": 1, "last_id": -1}`, `corrupt {"path":"~/tasks/koan.json","reason":"invalid","problems":[{"field":"/last_id","reason":"must be between 0 and 999999999999999"}]}`},
		{"not JSON", `{`, `corrupt {"path":"~/tasks/koan.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"current, invalid", `{"schema": 2}`, `corrupt {"path":"~/tasks/koan.json","reason":"invalid","problems":[{"field":"/migration","reason":"required"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.write("tasks/koan.json", tc.meta)
			f.write("tasks/1.json", taskJSON(0, 1, "a", "2026-09-21T10:00:00Z"))
			before := f.snapshot()
			if got := f.rel(opError(t, f, "migrate", `{}`)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if !reflect.DeepEqual(f.snapshot(), before) {
				t.Error("a file changed")
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		f := newFixture(t)
		f.remove("tasks/koan.json")
		if got := opError(t, f, "migrate", `{}`); got != `not-initialized {"missing":"metadata"}` {
			t.Error(got)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		f := newFixture(t)
		f.fail(fsys.OpReadFile, "koan.json", syscall.EIO)
		if got := f.rel(opError(t, f, "migrate", `{}`)); got != `io {"path":"~/tasks/koan.json","code":"EIO"}` {
			t.Error(got)
		}
	})
	t.Run("invalid input first", func(t *testing.T) {
		f := newFixture(t)
		f.write("tasks/koan.json", "{")
		if got := opError(t, f, "migrate", `{"dry_run": 1}`); !strings.HasPrefix(got, "invalid-input") {
			t.Error(got)
		}
	})
	t.Run("corrupt config", func(t *testing.T) {
		f := newFixture(t)
		f.write("cfg/config.toml", "junk")
		if got := opError(t, f, "migrate", `{}`); !strings.HasPrefix(got, "corrupt") {
			t.Error(got)
		}
	})
	t.Run("no config", func(t *testing.T) {
		f := newFixture(t)
		f.remove("cfg/config.toml")
		if got := opError(t, f, "migrate", `{}`); got != `not-initialized {"missing":"config"}` {
			t.Error(got)
		}
	})
}

// busy comes before koan.json's errors; a held lock stops migrate, and
// migrate holds it while it runs.
func TestMigrateLock(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/koan.json", "{")
	r, err := fsys.OS{}.OpenRoot(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, err := r.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if got := opError(t, f, "migrate", `{}`); got != "busy {}" {
		t.Errorf("got %s", got)
	}

	f2 := newFixture(t)
	f2.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	f2.write("tasks/1.json", taskJSON(1, 1, "a", "2026-09-21T10:00:00Z"))
	f2.env.LockWait = 0
	var during string
	f2.hook(func(o fsys.Op) error {
		if o.Name == fsys.OpRename && during == "" {
			other := *f2
			other.env.FS = fsys.OS{}
			during = opError(t, &other, "doctor", `{}`)
			during += " | " + opError(t, &other, "migrate", `{}`)
		}
		return nil
	})
	f2.migrate(`{}`)
	if during != "busy {} | busy {}" {
		t.Errorf("during the run: %s", during)
	}
}

// An error while writing carries what was written: a failed task file after
// one was written, and a failed koan.json always. The step stays pending, and
// a rerun finishes.
func TestMigratePartial(t *testing.T) {
	useRenameStep(t)
	for _, tc := range []struct {
		name, fail string
		partial    string
	}{
		{"first task", "1.json", ""},
		{"second task", "2.json", `{"state_written":false,"tasks_converted":1}`},
		{"koan.json", "koan.json", `{"state_written":false,"tasks_converted":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
			f.write("tasks/1.json", taskJSON(0, 1, "a", "2026-09-21T10:00:00Z"))
			f.write("tasks/2.json", taskJSON(0, 2, "b", "2026-09-21T10:00:00Z"))
			f.hook(func(o fsys.Op) error {
				if o.Name == fsys.OpRename && o.NewPath == tc.fail {
					return syscall.ENOSPC
				}
				return nil
			})
			e := Run("migrate", parse(t, `{}`), nil, f.env)
			if e.OK || e.Error.Kind != errs.KindIO {
				t.Fatalf("%+v", e)
			}
			got := ""
			if e.Error.Partial != nil {
				got = string(mustMarshal(t, e.Error.Partial))
				got = strings.TrimSpace(got)
			}
			if got != tc.partial {
				t.Errorf("partial %q, want %q", got, tc.partial)
			}
			line(t, e)
			if !strings.HasPrefix(f.read("tasks/koan.json"), `{"schema": 1`) {
				t.Error("koan.json written")
			}
			f.env.FS = fsys.OS{}
			if got := opError(t, f, "show", `{"id": 1}`); !strings.HasPrefix(got, "migration-pending") {
				t.Errorf("root: %s", got)
			}
			f.migrate(`{}`)
			if f.read("tasks/koan.json") != metaCurrent || f.read("tasks/1.json") != taskJSON(1, 1, "a", "2026-09-21T10:00:00Z") ||
				f.read("tasks/2.json") != taskJSON(1, 2, "b", "2026-09-21T10:00:00Z") {
				t.Error("the rerun did not finish the job")
			}
		})
	}
}

// A moved ftask tree with a schema 1 koan.json is moved to koan's names by
// the first command, which then fails migration-pending.
func TestMigrateAfterFtaskMove(t *testing.T) {
	f := newFixture(t)
	f.write("old/config.toml", `root = "~/tasks"`+"\n")
	f.remove("cfg")
	f.remove("tasks/koan.json")
	f.write("tasks/ftask.json", `{"schema": 1, "last_id": 100}`)
	f.env.LegacyConfigDir = filepath.Join(f.home, "old")
	if got := f.rel(opError(t, f, "show", `{"id": 1}`)); got != `migration-pending {"recorded":0,"latest":1}` {
		t.Errorf("got %s", got)
	}
	if f.read("tasks/koan.json") != `{"schema": 1, "last_id": 100}` || f.read("tasks/ftask.json") != "<none>" {
		t.Error("not moved to koan's names")
	}
}

// A write holding the lock makes migrate busy.
func TestMigrateBusyDuringWrite(t *testing.T) {
	f := newFixture(t)
	other := *f
	var during string
	f.hook(func(o fsys.Op) error {
		if o.Name == fsys.OpRename && o.NewPath == "state.json" && during == "" {
			other.env.FS = fsys.OS{}
			other.env.LockWait = 0
			during = opError(t, &other, "migrate", `{}`)
		}
		return nil
	})
	if got := opError(t, f, "create", `{"title": "x"}`); got != "" {
		t.Fatal(got)
	}
	if during != "busy {}" {
		t.Errorf("migrate during a write: %s", during)
	}
}

// A koan.json in an older format, or behind, is one migration-pending
// finding, not metadata-unusable; doctor and repair take it as the specs say.
func TestDoctorMigrationPending(t *testing.T) {
	for _, tc := range []struct{ name, meta, recorded string }{
		{"schema 1", `{"schema": 1, "last_id": 100}`, `0`},
		{"a step behind", `{"schema": 2, "migration": 0}`, `0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.write("tasks/koan.json", tc.meta)
			got := f.rel(f.op("doctor", `{}`))
			want := `{"healthy":false,"findings":[{"kind":"migration-pending","class":"manual","count":1,"truncated":false,"items":[{"paths":["~/tasks/koan.json"],"ids":[],"action":null,"suggest":"` + suggestMigrate +
				`","error":{"kind":"migration-pending","message":"the tree needs migration: it records step ` + tc.recorded + `, this binary's latest is 1; run koan migrate","details":{"recorded":` + tc.recorded + `,"latest":1}}}]}]}`
			if got != want {
				t.Errorf("got  %s\nwant %s", got, want)
			}
			if ok, fl := schematest.Check(t, "doctor-output", []byte(f.op("doctor", `{}`))); !ok {
				t.Errorf("doctor-output rejects at %s", fl)
			}
			// repair: migration-pending, nothing changed; and busy before it.
			before := f.snapshot()
			if got := opError(t, f, "repair", `{}`); got != `migration-pending {"recorded":0,"latest":1}` {
				t.Errorf("repair: %s", got)
			}
			if got := opError(t, f, "repair", `{"kinds": ["metadata-missing"]}`); got != `migration-pending {"recorded":0,"latest":1}` {
				t.Errorf("repair naming metadata-missing: %s", got)
			}
			if !reflect.DeepEqual(f.snapshot(), before) {
				t.Error("repair changed the tree")
			}
		})
	}
}

// The kinds of formats are migrate's: repair rejects them as input, saying so.
func TestRepairRejectsFormatKinds(t *testing.T) {
	f := newFixture(t)
	for _, k := range []string{"migration-pending", "old-format"} {
		got := opError(t, f, "repair", `{"kinds": ["`+k+`"]}`)
		want := `invalid-input {"problems":[{"field":"/kinds/0","reason":"` + k + ` is not repaired: converting formats is koan migrate's job alone"}]}`
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
		if out := f.op("doctor", `{"kinds": ["`+k+`"]}`); out != `{"healthy":true,"findings":[]}` {
			t.Errorf("doctor %s: %s", k, out)
		}
	}
}

// A task file in an older format with no step pending is an old-format
// finding that repair leaves, reports, and does not count as unusable.
func TestDoctorOldFormat(t *testing.T) {
	useRenameStep(t)
	f := newFixture(t)
	f.write("tasks/koan.json", metaCurrent)
	f.write("tasks/5.json", taskJSON(0, 5, "old", "2026-09-21T10:00:00Z"))
	f.write("tasks/p/6.json", "{")
	f.write("tasks/.koan-tmp-a", "")
	got := f.rel(f.op("doctor", `{}`))
	wantOld := `{"kind":"old-format","class":"manual","count":1,"truncated":false,"items":[{"paths":["~/tasks/5.json"],"ids":[5],"action":null,"suggest":"` +
		suggestMigrate + `; a file migrate lists under unconverted breaks its own format's rules, so no step can read it: fix or remove it"}]}`
	if !strings.Contains(got, wantOld) || !strings.Contains(got, `"kind":"unusable-file"`) || strings.Count(got, `"paths":["~/tasks/5.json"]`) != 1 {
		t.Errorf("doctor: %s", got)
	}
	before := f.read("tasks/5.json")
	out := f.rel(f.op("repair", `{}`))
	if !strings.Contains(out, `"repaired":[{"kind":"temp-leftover"`) || !strings.Contains(out, wantOld) || !strings.Contains(out, `"healthy":false`) {
		t.Errorf("repair: %s", out)
	}
	if f.read("tasks/5.json") != before {
		t.Error("repair changed an old-format file")
	}
}

// A rebuilt koan.json records the latest step when every task file is
// current, and 0 when one is in an older format; the same run goes on to its
// other repairs, and the findings left say the root needs migration.
func TestRepairMetadataMissingMigration(t *testing.T) {
	useRenameStep(t)
	for _, tc := range []struct {
		name  string
		old   bool
		step  int
		extra string
	}{
		{"current files", false, 2, ""},
		{"an older file", true, 0, `"kind":"migration-pending"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.remove("tasks/koan.json")
			f.task("p", 7, false, 99) // dangling reference
			if tc.old {
				f.write("tasks/9.json", taskJSON(0, 9, "old", "2026-09-21T10:00:00Z"))
			}
			d := f.op("doctor", `{}`)
			if want := fmt.Sprintf(`"migration":%d}`, tc.step); !strings.Contains(d, want) || strings.Contains(d, `"last_id"`) {
				t.Errorf("doctor: %s\nwant %s", d, want)
			}
			out := f.op("repair", `{"kinds": ["metadata-missing", "dangling-reference"]}`)
			if want := fmt.Sprintf(`"migration": %d`, tc.step); !strings.Contains(f.read("tasks/koan.json"), want) {
				t.Errorf("koan.json %q, want %s", f.read("tasks/koan.json"), want)
			}
			if got := f.blockedBy("p/7.json"); got != "[]" {
				t.Errorf("the run stopped after creating koan.json: blocked_by %s", got)
			}
			if tc.extra != "" && !strings.Contains(out, tc.extra) {
				t.Errorf("repair: %s", out)
			}
			if tc.extra == "" && !strings.Contains(out, `"healthy":true`) {
				t.Errorf("repair: %s", out)
			}
		})
	}
}

// A process killed before each call that changes the disk (the e2e crash
// matrix can't, with no released step covering task files): task files are
// converted in tree order, koan.json is never ahead of them, and a rerun
// finishes the job.
func TestMigrateCrashBefore(t *testing.T) {
	useRenameStep(t)
	type killed struct{}
	setup := func() *fixture {
		f := newFixture(t)
		f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
		f.write("tasks/1.json", taskJSON(0, 1, "a", "2026-09-21T10:00:00Z"))
		f.write("tasks/p/2.json", taskJSON(0, 2, "b", "2026-09-21T10:00:00Z"))
		f.write("tasks/p/3.json", taskJSON(0, 3, "c", "2026-09-21T10:00:00Z"))
		return f
	}
	ref := setup()
	ref.migrate(`{}`)
	want := ref.snapshot()
	for k := 1; k < 60; k++ {
		f := setup()
		n, crashed := 0, false
		f.hook(func(o fsys.Op) error {
			if o.Mutating {
				if n++; n == k {
					panic(killed{})
				}
			}
			return nil
		})
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(killed); !ok {
						panic(r)
					}
					crashed = true
				}
			}()
			Run("migrate", parse(t, `{}`), nil, f.env)
		}()
		if !crashed {
			if k < 7 {
				t.Fatalf("k=%d: no crash", k)
			}
			return
		}
		f.env.FS = fsys.OS{}
		snap := f.snapshot()
		current := 0
		for _, id := range []string{"tasks/1.json", "tasks/p/2.json", "tasks/p/3.json"} {
			if strings.Contains(snap[strings.TrimPrefix(id, "")], `"schema": 1`) {
				current++
			}
		}
		metaNew := strings.Contains(snap["tasks/koan.json"], `"migration": 2`)
		if metaNew && current != 3 {
			t.Errorf("k=%d: koan.json current with %d of 3 task files converted", k, current)
		}
		if !metaNew {
			if got := opError(t, f, "show", `{"id": 1}`); !strings.HasPrefix(got, "migration-pending") {
				t.Errorf("k=%d: %s", k, got)
			}
		}
		f.migrate(`{}`)
		for p, w := range want {
			if !strings.HasPrefix(p, "tasks/") {
				continue
			}
			if got := f.read(p); got != w {
				t.Errorf("k=%d: after the rerun %s = %q", k, p, got)
			}
		}
	}
	t.Fatal("still crashing at k=59")
}
