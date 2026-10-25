package main_test

import (
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// sameFault runs a command line and the same input through --input: both
// must fail with kind, exit 1 or 2 by kind, and problems pointing at
// field, whichever way the input came. in is "" when the input has no
// --input form to compare (a usage line).
func sameFault(t *testing.T, home, kind, field string, args []string, command, in string) {
	t.Helper()
	want := 1
	if kind == "usage" {
		want = 2
	}
	run := func(r *testrun.Result, how string) string {
		t.Helper()
		ps, _ := r.Error()["problems"].([]any)
		if r.Exit != want || r.Kind() != kind || len(ps) == 0 || ps[0].(map[string]any)["field"] != field {
			t.Errorf("%s: exit %d, want %s at %q: %s", how, r.Exit, kind, field, r.Line())
		}
		if kind == "invalid-input" {
			e := r.Error()
			if len(e["ids"].([]any)) != 0 || len(e["paths"].([]any)) != 0 {
				t.Errorf("%s: invalid-input names ids or paths: %s", how, r.Line())
			}
		}
		return problemFields(r)
	}
	line := run(testrun.Run{Home: home}.Koan(t, args...), "koan "+strings.Join(args, " "))
	if in != "" {
		via := run(testrun.Run{Home: home, Stdin: strings.NewReader(in)}.Koan(t, append(strings.Fields(command), "-i", "-")...),
			"koan "+command+" -i with "+in)
		if line != via {
			t.Errorf("koan %q has problems at %q, but --input has them at %q", args, line, via)
		}
	}
}

// invalidAt runs a command line that must be invalid-input, exit 1, with
// its first problem at field.
func invalidAt(t *testing.T, home, field string, args ...string) *testrun.Result {
	t.Helper()
	r := koan(t, home, args...)
	ps, _ := r.Error()["problems"].([]any)
	if r.Exit != 1 || r.Kind() != "invalid-input" || len(ps) == 0 || ps[0].(map[string]any)["field"] != field {
		t.Errorf("koan %q: exit %d, want invalid-input at %q: %s", args, r.Exit, field, r.Line())
	}
	return r
}

// setup runs a command that must succeed. It checks no schema: a later
// slice changes the results it makes.
func setup(t *testing.T, home string, args ...string) {
	t.Helper()
	if r := (testrun.Run{Home: home}).Koan(t, args...); r.Exit != 0 {
		t.Fatalf("koan %q: %s", args, r.Line())
	}
}

// A missing subject or object, and mv's destination given wrongly, can be
// turned into an input, so the input refuses them, as invalid-input at the
// pointer --input gives; they are not usage.
func TestMissingIsInvalidInput(t *testing.T) {
	home := root(t)
	setup(t, home, "folder", "create", "/w") // 1
	setup(t, home, "task", "create", "a")    // 2
	for _, c := range []struct {
		args    []string
		command string
		in      string
		field   string
	}{
		{[]string{"complete"}, "complete", `{}`, "/tasks"},
		{[]string{"reopen"}, "reopen", `{}`, "/tasks"},
		{[]string{"why"}, "why", `{}`, "/task"},
		{[]string{"claim"}, "claim", `{}`, "/task"},
		{[]string{"unclaim"}, "unclaim", `{}`, "/task"},
		{[]string{"block"}, "block", `{}`, "/task"},
		{[]string{"show"}, "show", `{}`, "/ref"},
		{[]string{"join"}, "join", `{}`, "/ref"},
		{[]string{"join", "2"}, "join", `{"ref":2}`, "/join"},
		{[]string{"unjoin", "2"}, "unjoin", `{"ref":2}`, "/join"},
		{[]string{"archive"}, "archive", `{}`, "/ref"},
		{[]string{"task", "create"}, "task create", `{}`, "/title"},
		{[]string{"folder", "create"}, "folder create", `{}`, "/path"},
		{[]string{"update"}, "update", `{}`, "/refs"},
		{[]string{"update", "2"}, "update", `{"refs":[2]}`, ""},
		{[]string{"delete"}, "delete", `{}`, "/refs"},
		{[]string{"mv"}, "mv", `{}`, "/refs"},
		{[]string{"mv", "-t", "/w"}, "mv", `{"into":"/w"}`, "/refs"},
		{[]string{"mv", "2"}, "mv", `{"refs":[2]}`, ""},
		{[]string{"mv", "/w"}, "mv", `{"refs":["/w"]}`, ""},
		{[]string{"mv", "-T", "/w"}, "mv", `{"refs":["/w"],"rename":true}`, ""},
		{[]string{"mv", "-T", "-t", "/w", "/w"}, "mv", `{"refs":["/w"],"rename":true,"into":"/w"}`, ""},
		{[]string{"mv", "-T", "/w", "/x", "/y"}, "mv", `{"refs":["/w","/x"],"dest":"/y","rename":true}`, "/refs/1"},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			sameFault(t, home, "invalid-input", c.field, c.args, c.command, c.in)
		})
	}
}

// What stays usage: a line that can't become an input. An extra
// positional is one, whatever the command.
func TestUsageStays(t *testing.T) {
	home := root(t)
	setup(t, home, "folder", "create", "/w") // 1
	setup(t, home, "task", "create", "a")    // 2
	setup(t, home, "task", "create", "b")    // 3
	for _, c := range []struct {
		args  []string
		field string
	}{
		{[]string{"why", "2", "3"}, "3"},
		{[]string{"claim", "2", "3"}, "3"},
		{[]string{"show", "2", "3"}, "3"},
		{[]string{"context", "/w", "/w"}, "/w"},
		{[]string{"join", "2", "/w", "-i", "-"}, "2"},
		{[]string{"why", "--frob"}, "--frob"},
		{[]string{"frob"}, "frob"},
	} {
		usageAt(t, home, c.field, c.args...)
	}
	usageAt(t, home, "", "task")
}

// An ID of the wrong kind is invalid-input at the reference's field, with
// a reason saying what it is; not-found means only that nothing has the
// name. The command line and --input agree.
func TestWrongKindIsInvalidInput(t *testing.T) {
	home := root(t)
	setup(t, home, "folder", "create", "/w") // 1
	setup(t, home, "task", "create", "a")    // 2
	setup(t, home, "folder", "create", "/v") // 3
	for _, c := range []struct {
		args    []string
		command string
		in      string
		field   string
		reason  string
	}{
		{[]string{"why", "1"}, "why", `{"task":1}`, "/task", "folder"},
		{[]string{"complete", "1"}, "complete", `{"tasks":[1]}`, "/tasks/0", "folder"},
		{[]string{"complete", "2", "@1"}, "complete", `{"tasks":[2,"@1"]}`, "/tasks/1", "folder"},
		{[]string{"reopen", "1"}, "reopen", `{"tasks":[1]}`, "/tasks/0", "folder"},
		{[]string{"claim", "1"}, "claim", `{"task":1}`, "/task", "folder"},
		{[]string{"unclaim", "1"}, "unclaim", `{"task":1}`, "/task", "folder"},
		{[]string{"claim", "/w"}, "claim", `{"task":"/w"}`, "/task", "folder"},
		{[]string{"update", "1", "--priority", "1"}, "update", `{"refs":[1],"priority":1}`, "/priority", "folder"},
		{[]string{"block", "1", "2"}, "block", `{"task":1,"blocked_by":[2]}`, "/task", "folder"},
		{[]string{"context", "2"}, "context", `{"folder":2}`, "/folder", "task"},
		{[]string{"export", "2"}, "export", `{"folder":2}`, "/folder", "task"},
		{[]string{"task", "list", "2"}, "task list", `{"folder":2}`, "/folder", "task"},
		{[]string{"frontier", "2"}, "frontier", `{"folder":2}`, "/folder", "task"},
		{[]string{"folder", "list", "2"}, "folder list", `{"folder":2}`, "/folder", "task"},
		{[]string{"task", "create", "x", "--folder", "2"}, "task create", `{"title":"x","folder":2}`, "/folder", "task"},
		{[]string{"mv", "2", "2"}, "mv", `{"refs":[2],"dest":2}`, "/dest", "task"},
		{[]string{"mv", "-t", "2", "2"}, "mv", `{"refs":[2],"into":2}`, "/into", "task"},
		{[]string{"mv", "/v", "2"}, "mv", `{"refs":["/v"],"dest":2}`, "/dest", "task"},
		{[]string{"mv", "-t", "2", "/v"}, "mv", `{"refs":["/v"],"into":2}`, "/into", "task"},
		{[]string{"bind", "2"}, "bind", `{"folder":2,"dir":"/tmp"}`, "/folder", "task"},
		{[]string{"join", "2", "2"}, "join", `{"ref":2,"join":[2]}`, "/join/0", "task"},
		{[]string{"join", "2", "/w", "2"}, "join", `{"ref":2,"join":["/w",2]}`, "/join/1", "task"},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			sameFault(t, home, "invalid-input", c.field, c.args, c.command, c.in)
			r := testrun.Run{Home: home}.Koan(t, c.args...)
			if m, _ := r.Error()["message"].(string); !strings.Contains(m, c.reason) {
				t.Errorf("message %q doesn't say it is a %s", m, c.reason)
			}
		})
	}
	// An ID that names nothing is still not-found.
	fails(t, home, "not-found", "", "why", "99")
	fails(t, home, "not-found", "", "context", "99")
	// A path anchored at a task is invalid-input, as before.
	r := testrun.Run{Home: home}.Koan(t, "mv", "/v", "@2/x")
	if r.Exit != 1 || r.Kind() != "invalid-input" || problemFields(r) != "/dest" {
		t.Errorf("anchored at a task: %s", r.Line())
	}
}
