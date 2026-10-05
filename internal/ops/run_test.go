package ops

import (
	"slices"
	"testing"
	"time"

	"github.com/phansen314/ftask/internal/buildinfo"
	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/schematest"
)

func parse(t *testing.T, doc string) *jsonio.Object {
	t.Helper()
	obj, repeated, err := jsonio.ParseObject([]byte(doc))
	if err != nil || len(repeated) > 0 {
		t.Fatalf("parse %s: %v %v", doc, err, repeated)
	}
	return obj
}

// line is env's envelope as the CLI writes it, checked against the envelope
// schema.
func line(t *testing.T, env Envelope) string {
	t.Helper()
	b, err := jsonio.MarshalLine(env)
	if err != nil {
		t.Fatal(err)
	}
	if ok, f := schematest.Check(t, "envelope", b); !ok {
		t.Errorf("envelope schema rejects at %s: %s", f, b)
	}
	return string(b)
}

func fixedBuild(t *testing.T) {
	t.Helper()
	saved := buildInfo
	t.Cleanup(func() { buildInfo = saved })
	buildInfo = func() buildinfo.Info {
		return buildinfo.Info{
			Version: "1.4.0", Commit: "abc123", CommitTime: "2026-09-27T12:34:56Z",
			UncommittedChanges: true, Go: "go1.25.1", Platform: "linux/amd64",
		}
	}
}

func TestVersion(t *testing.T) {
	fixedBuild(t)
	got := line(t, Run("version", parse(t, `{}`), nil, Env{}))
	want := `{"ok":true,"result":{"version":"1.4.0","commit":"abc123","commit_time":"2026-09-27T12:34:56Z","uncommitted_changes":true,"go":"go1.25.1","platform":"linux/amd64","schemas":{"task":1,"root":1}},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// The real build information, whatever this test binary has, matches the
// output schema.
func TestVersionOutputSchema(t *testing.T) {
	env := Run("version", parse(t, `{}`), nil, Env{})
	line(t, env)
	b, err := jsonio.MarshalLine(env.Result)
	if err != nil {
		t.Fatal(err)
	}
	if ok, f := schematest.Check(t, "version-output", b); !ok {
		t.Errorf("version-output rejects at %s: %s", f, b)
	}
}

func TestRunInvalidInput(t *testing.T) {
	caller := []errs.Problem{{Field: "/z", Reason: "from the caller"}, {Field: "/a", Reason: "not valid JSON"}}
	for _, tc := range []struct {
		name, op, doc string
		caller        []errs.Problem
		want          []string
	}{
		{"adapter", "version", `{"x": 1}`, nil, []string{"/x"}},
		{"caller only", "version", `{}`, caller, []string{"/a", "/z"}},
		{"merged and sorted", "version", `{"x": 1}`, caller, []string{"/a", "/x", "/z"}},
		{"info", "info", `{"x": 1}`, nil, []string{"/x"}},
		{"show", "show", `{"id": 1, "x": 1}`, nil, []string{"/x"}},
		{"init relative", "init", `{"root": "tasks"}`, nil, []string{"/root"}},
		{"init dot-dot", "init", `{"root": "/a/../b", "replace_config": 1}`, nil, []string{"/replace_config", "/root"}},
		// The caller's problem at a field replaces the adapter's there.
		{"caller covers field", "init", `{"root": "~bob/t", "x": 1}`, []errs.Problem{{Field: "/root", Reason: "~user/"}}, []string{"/root", "/x"}},
		{"caller covers missing", "show", `{}`, []errs.Problem{{Field: "/id", Reason: "must be UTF-8"}}, []string{"/id"}},
		// Validation comes before the operation runs.
		{"before running", "frontier", `{"recursive": 1}`, nil, []string{"/recursive"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := Run(tc.op, parse(t, tc.doc), tc.caller, Env{})
			line(t, env)
			if env.OK || env.Error.Kind != errs.KindInvalidInput {
				t.Fatalf("got %+v, want invalid-input", env)
			}
			got := fields(env.Error.Details.(errs.InvalidInputDetails).Problems)
			if !slices.Equal(got, tc.want) {
				t.Errorf("fields %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunInternal(t *testing.T) {
	savedVersion, savedFrontier := runners["version"], runners["frontier"]
	t.Cleanup(func() { runners["version"], runners["frontier"] = savedVersion, savedFrontier })
	runners["version"] = func(Env, any, *errs.Collector) (any, *errs.Error) { return nil, nil }
	delete(runners, "frontier") // every operation is implemented: pretend one isn't

	for _, tc := range []struct{ name, op, doc string }{
		{"no such operation", "nosuch", `{}`},
		{"not implemented", "frontier", `{}`},
		{"no result", "version", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := Run(tc.op, parse(t, tc.doc), nil, Env{})
			line(t, env)
			if env.OK || env.Error.Kind != errs.KindInternal {
				t.Errorf("got %+v, want internal", env)
			}
		})
	}
}

func TestRunWrongInputType(t *testing.T) {
	r := typed(func(Env, InfoInput, *errs.Collector) (any, *errs.Error) { return struct{}{}, nil })
	if _, e := r(Env{}, VersionInput{}, &errs.Collector{}); e == nil || e.Kind != errs.KindInternal {
		t.Errorf("got %v, want internal", e)
	}
}

func TestRunWarnings(t *testing.T) {
	saved := runners["version"]
	t.Cleanup(func() { runners["version"] = saved })
	warn := errs.UnreadableFolder("/r/a", "EACCES")
	runners["version"] = func(_ Env, _ any, w *errs.Collector) (any, *errs.Error) {
		w.Add(warn)
		return nil, errs.Busy()
	}
	env := Run("version", parse(t, `{}`), nil, Env{})
	line(t, env)
	if env.OK || len(env.Warnings) != 1 || env.Warnings[0].Paths[0] != "/r/a" {
		t.Errorf("got %+v, want busy with the warning", env)
	}
}

func TestFailed(t *testing.T) {
	arg := "--bogus"
	got := line(t, Failed(errs.Usage([]errs.UsageProblem{{Argument: &arg, Reason: "unknown option"}})))
	want := `{"ok":false,"error":{"kind":"usage","message":"usage: unknown option","details":{"problems":[{"argument":"--bogus","reason":"unknown option"}]}},"warnings":[]}` + "\n"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRunnersHaveDecoders(t *testing.T) {
	for name := range runners {
		if _, ok := decoders[name]; !ok {
			t.Errorf("runner %s has no decoder", name)
		}
	}
}

func TestRealClock(t *testing.T) {
	now := RealClock()
	if now.Location() != time.UTC || now.Nanosecond() != 0 {
		t.Errorf("RealClock() = %v, want UTC whole seconds", now)
	}
}
