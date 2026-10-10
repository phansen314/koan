package ops

import (
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/jsonio"
)

// Every operation that requires a usable root, with a valid input.
var rootOperations = []struct{ name, input string }{
	{"show", `{"id": 1}`},
	{"create", `{"title": "x"}`},
	{"create-batch", `{"tasks": [{"title": "x"}]}`},
	{"create-folder", `{"folder": "/a"}`},
	{"done", `{"id": 1}`},
	{"reopen", `{"id": 1}`},
	{"update", `{"id": 1, "title": "y"}`},
	{"block", `{"id": 1, "blockers": [2]}`},
	{"unblock", `{"id": 1, "blockers": [2]}`},
	{"delete", `{"id": 1}`},
	{"move", `{"id": 1, "to": "/a"}`},
	{"delete-folder", `{"folder": "/a"}`},
	{"move-folder", `{"folder": "/a", "to": "/b"}`},
	{"list", `{}`},
	{"frontier", `{}`},
	{"why", `{"id": 1}`},
}

// An operation that requires a usable root fails migration-pending on a
// koan.json in an older format and on one a step behind, with nothing
// written; unsupported-format with field on a step past the latest
// (operations.md, Root states).
func TestMigrationPendingRefusesEveryOperation(t *testing.T) {
	for _, tc := range []struct {
		name, meta, want string
	}{
		{"schema 1", `{"schema": 1, "last_id": 100}`, `migration-pending {"recorded":0,"latest":1}`},
		{"a step behind", `{"schema": 2, "migration": 0}`, `migration-pending {"recorded":0,"latest":1}`},
		{"past the latest step", `{"schema": 2, "migration": 2}`, `unsupported-format {"path":"~/tasks/koan.json","found":2,"supported":[1],"field":"migration"}`},
		{"a newer schema", `{"schema": 3, "last_id": 100}`, `unsupported-format {"path":"~/tasks/koan.json","found":3,"supported":[2]}`},
		{"schema 0", `{"schema": 0, "last_id": 100}`, `unsupported-format {"path":"~/tasks/koan.json","found":0,"supported":[2]}`},
		{"older format breaking its own rules", `{"schema": 1, "last_id": -1}`, `corrupt {"path":"~/tasks/koan.json","reason":"invalid","problems":[{"field":"/last_id","reason":"must be between 0 and 999999999999999"}]}`},
		{"older format with the counter", `{"schema": 1, "last_id": 1, "migration": 0}`, `corrupt {"path":"~/tasks/koan.json","reason":"invalid","problems":[{"field":"/migration","reason":"unknown field"}]}`},
	} {
		for _, op := range rootOperations {
			t.Run(tc.name+"/"+op.name, func(t *testing.T) {
				f := newFixture(t)
				f.task("", 1, false)
				f.task("", 2, false)
				f.write("tasks/koan.json", tc.meta)
				task1 := f.read("tasks/1.json")
				got := f.rel(opError(t, f, op.name, op.input))
				if got != tc.want {
					t.Errorf("got  %s\nwant %s", got, tc.want)
				}
				if f.read("tasks/koan.json") != tc.meta {
					t.Error("koan.json rewritten")
				}
				if f.read("tasks/1.json") != task1 || f.read("tasks/101.json") != "<none>" || f.read("tasks/a") != "<none>" {
					t.Error("the tree changed")
				}
			})
		}
	}
}

// The root states come before busy and before every walk error: a pending
// migration is reported even when a needed task file is corrupt.
func TestMigrationPendingBeforeTreeErrors(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	f.write("tasks/1.json", "{")
	if got := opError(t, f, "show", `{"id": 1}`); !strings.HasPrefix(got, "migration-pending") {
		t.Errorf("got %s", got)
	}
	// Unusable config first.
	f.write("cfg/config.toml", "garbage")
	if got := opError(t, f, "show", `{"id": 1}`); !strings.HasPrefix(got, "corrupt") {
		t.Errorf("got %s", got)
	}
}

// Invalid input still comes first.
func TestInvalidInputBeforeMigrationPending(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	if got := opError(t, f, "show", `{}`); !strings.HasPrefix(got, "invalid-input") {
		t.Errorf("got %s", got)
	}
}

// version, info, and init run on a tree that needs migration.
func TestMigrationPendingAllowsVersionInfoInit(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 100}`)
	for _, name := range []string{"version", "info", "doctor"} {
		if e := Run(name, parse(t, `{}`), nil, f.env); !e.OK {
			t.Errorf("%s: %+v", name, e.Error)
		}
	}
	e := Run("init", parse(t, `{"root": "`+f.root+`", "replace_config": true}`), nil, f.env)
	if !e.OK {
		t.Fatalf("init: %+v", e.Error)
	}
	if got := line(t, e); !strings.Contains(got, `"action":"attached","last_id":100`) {
		t.Errorf("init: %s", got)
	}
	if got := f.read("tasks/koan.json"); got != `{"schema": 1, "last_id": 100}` {
		t.Errorf("init changed koan.json: %q", got)
	}
}

// info reports each state of koan.json (info-output).
func TestInfoMigrationStates(t *testing.T) {
	for _, tc := range []struct{ name, meta, tree, rest string }{
		{"old-format", `{"schema": 1, "last_id": 7}`,
			`"metadata":"old-format","schema":1,"migration":0`, `"usable":false,"compatible":false,"migration_pending":true`},
		{"behind", `{"schema": 2, "migration": 0}`,
			`"metadata":"ok","schema":2,"migration":0`, `"usable":false,"compatible":true,"migration_pending":true`},
		{"current", `{"schema": 2, "migration": 1}`,
			`"metadata":"ok","schema":2,"migration":1`, `"usable":true,"compatible":true,"migration_pending":false`},
		{"past the latest", `{"schema": 2, "migration": 5}`,
			`"metadata":"unsupported-format","schema":2,"migration":5`, `"usable":false,"compatible":true,"migration_pending":false`},
		{"older, corrupt", `{"schema": 1, "last_id": "x"}`,
			`"metadata":"corrupt","schema":1,"migration":null`, `"usable":false,"compatible":false,"migration_pending":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.write("tasks/koan.json", tc.meta)
			got := line(t, Run("info", parse(t, `{}`), nil, f.env))
			for _, want := range []string{tc.tree, tc.rest} {
				if !strings.Contains(got, want) {
					t.Errorf("got  %s\nwant %s", got, want)
				}
			}
		})
	}
}

// An ID-issuing write moves the state file's counter and leaves koan.json
// alone: koan.json holds no counter (design-spec.md, Task IDs).
func TestWritesKeepMigration(t *testing.T) {
	f := newFixture(t)
	meta := f.read("tasks/koan.json")
	if got := opError(t, f, "create", `{"title": "x"}`); got != "" {
		t.Fatal(got)
	}
	if got := f.read("tasks/koan.json"); got != meta {
		t.Errorf("koan.json %q", got)
	}
	if got := f.read(stateRel); got != st(101) {
		t.Errorf("state file %q", got)
	}
}

// opError is the error an operation fails with, as kind and details, or "".
func opError(t *testing.T, f *fixture, name, input string) string {
	t.Helper()
	e := Run(name, parse(t, input), nil, f.env)
	line(t, e) // conforms to the envelope schema
	if e.OK {
		return ""
	}
	d, err := jsonio.MarshalLine(e.Error.Details)
	if err != nil {
		t.Fatal(err)
	}
	return string(e.Error.Kind) + " " + strings.TrimSpace(string(d))
}
