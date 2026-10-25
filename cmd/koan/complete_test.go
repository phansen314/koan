package main_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// completeTree is the tree completion's tests complete from:
//
//	/           Root task @9, Notes @12
//	/old        @1 "Old work", finished: Shipped @8 (complete)
//	/work       @2 "Work": Announce @7 (after /work/api)
//	/work/api   @3: Design the endpoints @5, Build them @6 (claimed),
//	            its start gate @10 (complete) and done gate @11
//	/work/apps  @4, finished: empty
func completeTree(t *testing.T) string {
	t.Helper()
	home := root(t)
	batchOK(t, home, `{"folders": [{"path": "/work", "title": "Work"}, {"path": "/work/api"}, {"path": "/work/apps"},
	    {"path": "/old", "title": "Old work"}],
	  "tasks": [
	    {"label": "$design", "title": "Design the endpoints", "folder": "/work/api",
	     "blocked_by": ["/work/api.start"], "blocks": ["/work/api"]},
	    {"label": "$build", "title": "Build them", "folder": "/work/api", "blocked_by": ["$design"], "blocks": ["/work/api"]},
	    {"title": "Announce", "folder": "/work", "blocked_by": ["/work/api"]},
	    {"title": "Shipped", "folder": "/old"},
	    {"title": "Root task"}]}`)
	ok(t, home, "task", "create", "Notes")
	ok(t, home, "task", "complete", "8")
	ok(t, home, "task", "complete", "10")
	ok(t, home, "task", "claim", "6")
	return home
}

// completes runs __complete on a command line whose last word is the one
// being completed, and gives its standard output.
func completes(t *testing.T, home string, args ...string) string {
	t.Helper()
	r := testrun.Run{Home: home}.Koan(t, append([]string{"__complete"}, args...)...)
	if r.Exit != 0 {
		t.Errorf("__complete %q: exit %d, stderr %q", args, r.Exit, r.Stderr)
	}
	return r.Stdout
}

// offered is __complete's output: a candidate per line, then the
// directive.
func offered(directive int, cands ...string) string {
	var b strings.Builder
	for _, c := range cands {
		b.WriteString(c + "\n")
	}
	fmt.Fprintf(&b, ":%d\n", directive)
	return b.String()
}

// The directives: no file names and the order kept for a reference, no
// space after a path too, files as the shell completes them, and nothing
// for any other value.
const (
	byID      = 36
	byPath    = 38
	byFiles   = 0
	byNothing = 4
)

// The descriptions of the tree's candidates, by key without the @.
var described = map[string]string{
	"1": "/old  Old work", "2": "/work  Work", "3": "/work/api", "4": "/work/apps",
	"5": "Design the endpoints  /work/api", "6": "Build them  /work/api", "7": "Announce  /work",
	"8": "Shipped  /old", "9": "Root task  /", "12": "Notes  /",
	"1.start": "Start of /old", "1.done": "Done of /old",
	"2.start": "Start of /work", "2.done": "Done of /work",
	"3.start": "Start of /work/api", "3.done": "Done of /work/api",
	"4.start": "Start of /work/apps", "4.done": "Done of /work/apps",
}

// as is the candidate lines for keys, written with prefix: "@" or "".
func as(prefix string, keys ...string) []string {
	var out []string
	for _, k := range keys {
		d, ok := described[k]
		if !ok {
			panic("no description for " + k)
		}
		out = append(out, prefix+k+"\t"+d)
	}
	return out
}

// Each argument in the spec's table, in each form of word (spec
// Completing references).
func TestCompleteReferences(t *testing.T) {
	home := completeTree(t)
	all := []string{"9", "12", "8", "7", "5", "6", "3.start", "3.done"}
	allTasks := []string{"9", "12", "8", "7", "5", "6"}
	edges := []string{"9", "12", "1", "1.start", "1.done", "8", "2", "2.start", "2.done", "7",
		"3", "5", "6", "3.start", "3.done", "4", "4.start", "4.done"}
	folders := []string{"1", "2", "3", "4"}
	tasksAndFolders := []string{"9", "12", "1", "8", "2", "7", "3", "5", "6", "4"}
	for _, c := range []struct {
		args []string
		want string
	}{
		// Tasks and made gates, in pick's order.
		{[]string{"task", "show", ""}, offered(byID, as("@", all...)...)},
		{[]string{"task", "show", "@"}, offered(byID, as("@", all...)...)},
		{[]string{"task", "why", "@"}, offered(byID, as("@", all...)...)},
		{[]string{"task", "update", "@"}, offered(byID, as("@", all...)...)},
		{[]string{"task", "delete", "@"}, offered(byID, as("@", all...)...)},
		{[]string{"task", "unblock", "@"}, offered(byID, as("@", all...)...)},
		// An ID's digits, with or without the @, as typed; a gate by its
		// folder's ID and end.
		{[]string{"task", "show", "1"}, offered(byID, as("", "12")...)},
		{[]string{"task", "show", "@1"}, offered(byID, as("@", "12")...)},
		{[]string{"task", "show", "@3"}, offered(byID, as("@", "3.start", "3.done")...)},
		{[]string{"task", "show", "@3."}, offered(byID, as("@", "3.start", "3.done")...)},
		{[]string{"task", "show", "3.d"}, offered(byID, as("", "3.done")...)},
		{[]string{"task", "show", "@3.done"}, offered(byID, as("@", "3.done")...)},
		{[]string{"task", "show", "@10"}, offered(byID)},
		{[]string{"task", "show", "@3.x"}, offered(byID)},
		{[]string{"task", "show", "@x"}, offered(byID)},
		{[]string{"task", "show", "@.start"}, offered(byID)},
		{[]string{"task", "show", "@@1"}, offered(byID)},
		// By state and claim, gates as tasks.
		{[]string{"task", "complete", "@"}, offered(byID, as("@", "9", "12", "7", "5", "6", "3.done")...)},
		{[]string{"task", "reopen", "@"}, offered(byID, as("@", "8", "3.start")...)},
		{[]string{"task", "archive", "@"}, offered(byID, as("@", "8", "3.start")...)},
		{[]string{"task", "claim", "@"}, offered(byID, as("@", "9", "12", "7", "5", "3.done")...)},
		{[]string{"task", "release", "@"}, offered(byID, as("@", "6")...)},
		{[]string{"task", "mv", "@"}, offered(byID, as("@", allTasks...)...)},
		{[]string{"task", "join", "@"}, offered(byID, as("@", allTasks...)...)},
		{[]string{"task", "unjoin", "@"}, offered(byID, as("@", allTasks...)...)},
		// Gates made or not where an edge names them.
		{[]string{"task", "block", "@"}, offered(byID, as("@", "9", "12", "1.start", "1.done", "8", "2.start", "2.done", "7",
			"5", "6", "3.start", "3.done", "4.start", "4.done")...)},
		{[]string{"task", "block", "5", "@"}, offered(byID, as("@", edges...)...)},
		{[]string{"task", "block", "5", "--blocks", ""}, offered(byID, as("@", edges...)...)},
		{[]string{"task", "unblock", "5", "@"}, offered(byID, as("@", edges...)...)},
		{[]string{"task", "create", "t", "--blocked-by", "@2"}, offered(byID, as("@", "2", "2.start", "2.done")...)},
		{[]string{"task", "create", "t", "--blocks=2."}, offered(byID, as("", "2.start", "2.done")...)},
		// Folders.
		{[]string{"task", "create", "t", "--folder", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"task", "list", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"task", "frontier", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"task", "claim-next", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "list", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"pick", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"task", "mv", "5", "-t", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "show", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "update", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "mv", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "delete", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"context", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"export", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"marker", "bind", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "join", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "unjoin", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"task", "create", "t", "--join", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "create", "/new", "--join", "@"}, offered(byID, as("@", folders...)...)},
		// Each word after a join's subject offers folders.
		{[]string{"task", "join", "5", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"task", "unjoin", "5", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "join", "3", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "unjoin", "3", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"task", "join", "5", "/work/api", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "join", "/work/api", "/old", "2"}, offered(byID, as("", "2")...)},
		{[]string{"folder", "show", "3"}, offered(byID, as("", "3")...)},
		{[]string{"folder", "archive", "@"}, offered(byID, as("@", "1", "4")...)},
		// task mv: the first word offers tasks, and every word after it
		// tasks and folders, since any can be the last; after -t, every
		// word is a task.
		{[]string{"task", "mv", "5", "@"}, offered(byID, as("@", tasksAndFolders...)...)},
		{[]string{"task", "mv", "5", "6", ""}, offered(byID, as("@", tasksAndFolders...)...)},
		{[]string{"task", "mv", "5", "2"}, offered(byID, as("", "2")...)},
		{[]string{"task", "mv", "-t", "/work", "@"}, offered(byID, as("@", allTasks...)...)},
		{[]string{"task", "mv", "-t", "/work", "5", "@"}, offered(byID, as("@", allTasks...)...)},
		{[]string{"task", "mv", "5", "-t", "/work", "@"}, offered(byID, as("@", allTasks...)...)},
		{[]string{"task", "mv", "--into=/work", "5", "@"}, offered(byID, as("@", allTasks...)...)},
		// folder mv: every word offers folders, but with -T the second.
		{[]string{"folder", "mv", "3", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "mv", "3", "4", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "mv", "-t", "/old", "3", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "mv", "-T", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"folder", "mv", "-T", "3", "@"}, offered(byID)},
		{[]string{"folder", "mv", "3", "-T", ""}, offered(byID)},
		{[]string{"folder", "mv", "--rename", "3", "2"}, offered(byID)},
		{[]string{"folder", "mv", "-T", "3", "/old", ""}, offered(byNothing)},
		// Each word of a list subject offers what the subject takes.
		{[]string{"task", "complete", "9", "@"}, offered(byID, as("@", "9", "12", "7", "5", "6", "3.done")...)},
		{[]string{"task", "complete", "9", "12", "1"}, offered(byID, as("", "12")...)},
		{[]string{"task", "reopen", "8", "@"}, offered(byID, as("@", "8", "3.start")...)},
		{[]string{"task", "delete", "9", "@"}, offered(byID, as("@", all...)...)},
		{[]string{"folder", "delete", "3", "@"}, offered(byID, as("@", folders...)...)},
		{[]string{"task", "unblock", "5", "6", "@"}, offered(byID, as("@", edges...)...)},
		// where: tasks, made gates and folders.
		{[]string{"where", ""}, offered(byID, as("@", "9", "12", "1", "8", "2", "7", "3", "5", "6", "3.start", "3.done", "4")...)},
		{[]string{"where", "3"}, offered(byID, as("", "3", "3.start", "3.done")...)},
		// Paths as paths, folder by folder.
		{[]string{"folder", "show", "/"}, offered(byPath, "/old", "/work")},
		{[]string{"folder", "show", "/w"}, offered(byPath, "/work")},
		{[]string{"folder", "show", "/work"}, offered(byPath, "/work")},
		{[]string{"folder", "show", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"folder", "show", "/work/ap"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"folder", "show", "/work/api"}, offered(byPath, "/work/api")},
		{[]string{"folder", "show", "/work."}, offered(byPath)},
		{[]string{"folder", "show", "/Work/"}, offered(byPath)},
		{[]string{"folder", "show", "/nope/"}, offered(byPath)},
		{[]string{"folder", "show", "/work//"}, offered(byPath)},
		{[]string{"task", "list", "/w"}, offered(byPath, "/work")},
		{[]string{"task", "mv", "5", "-t", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"task", "join", "42", "/w"}, offered(byPath, "/work")},
		{[]string{"task", "unjoin", "5", "/work/api"}, offered(byPath, "/work/api")},
		{[]string{"folder", "join", "/work/api", "/o"}, offered(byPath, "/old")},
		{[]string{"folder", "unjoin", "/work/api", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"task", "create", "t", "--join", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"folder", "create", "/new", "--join", "/w"}, offered(byPath, "/work")},
		{[]string{"context", "/o"}, offered(byPath, "/old")},
		{[]string{"export", "/o"}, offered(byPath, "/old")},
		{[]string{"task", "frontier", "/w"}, offered(byPath, "/work")},
		{[]string{"task", "claim-next", "/w"}, offered(byPath, "/work")},
		{[]string{"folder", "list", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"pick", "/o"}, offered(byPath, "/old")},
		{[]string{"marker", "bind", "/o"}, offered(byPath, "/old")},
		{[]string{"task", "mv", "5", "/w"}, offered(byPath, "/work")},
		{[]string{"task", "mv", "5", "6", "/work/api"}, offered(byPath, "/work/api")},
		{[]string{"task", "mv", "-t", "/work", "5", "/"}, offered(byPath)},
		{[]string{"folder", "mv", "/work/api", "/o"}, offered(byPath, "/old")},
		{[]string{"folder", "mv", "/work/api", "/old", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"folder", "mv", "-T", "/w"}, offered(byPath, "/work")},
		{[]string{"folder", "delete", "/work/api", "/o"}, offered(byPath, "/old")},
		{[]string{"task", "complete", "9", "/work/api"}, offered(byPath, "/work/api", "/work/api.done")},
		// A folder is a step toward its gates, wherever a gate is taken.
		{[]string{"task", "show", "/"}, offered(byPath, "/old", "/work")},
		{[]string{"task", "show", "/work/api"}, offered(byPath, "/work/api", "/work/api.start", "/work/api.done")},
		{[]string{"task", "show", "/work/api."}, offered(byPath, "/work/api.start", "/work/api.done")},
		{[]string{"task", "show", "/work/api.d"}, offered(byPath, "/work/api.done")},
		{[]string{"task", "show", "/work/ap"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"task", "show", "/work"}, offered(byPath, "/work")},
		{[]string{"task", "complete", "/work/api"}, offered(byPath, "/work/api", "/work/api.done")},
		{[]string{"task", "reopen", "/work/api"}, offered(byPath, "/work/api", "/work/api.start")},
		{[]string{"task", "block", "/work"}, offered(byPath, "/work", "/work.start", "/work.done")},
		{[]string{"task", "block", "5", "/work."}, offered(byPath, "/work.start", "/work.done")},
		{[]string{"task", "block", "5", "/work/api"}, offered(byPath, "/work/api", "/work/api.start", "/work/api.done")},
		{[]string{"where", "/work/api"}, offered(byPath, "/work/api", "/work/api.start", "/work/api.done")},
		// Only tasks: a path offers nothing.
		{[]string{"task", "mv", "/"}, offered(byPath)},
		{[]string{"task", "join", "/"}, offered(byPath)},
		{[]string{"task", "unjoin", "/work/api"}, offered(byPath)},
		// Finished folders, and the steps toward them.
		{[]string{"folder", "archive", "/"}, offered(byPath, "/old", "/work")},
		{[]string{"folder", "archive", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		// Paths only, as steps toward a new path.
		{[]string{"folder", "create", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"folder", "create", "/work/api"}, offered(byPath, "/work/api")},
		{[]string{"folder", "mv", "-T", "/work/api", "/work/"}, offered(byPath, "/work/api", "/work/apps")},
		{[]string{"folder", "mv", "/work/api", "-T", "/o"}, offered(byPath, "/old")},
		{[]string{"folder", "create", "@"}, offered(byID)},
		{[]string{"folder", "create", ""}, offered(byID)},
		// Anything else offers nothing: an anchored path, material, a label.
		{[]string{"where", "@2/api"}, offered(byID)},
		{[]string{"where", "/work/api:"}, offered(byID)},
		{[]string{"where", "@3:docs"}, offered(byID)},
		{[]string{"task", "block", "5", "$design"}, offered(byID)},
		{[]string{"task", "show", "work"}, offered(byID)},
		// Only a list's first item completes.
		{[]string{"task", "block", "5", "6,@"}, offered(byID)},
		{[]string{"task", "block", "5", "6", "@3."}, offered(byID, as("@", "3.start", "3.done")...)},
		{[]string{"task", "join", "5", "/old,/w"}, offered(byID)},
		{[]string{"task", "create", "t", "--join", "/old,/w"}, offered(byID)},
		// A subject given, and values that aren't references.
		{[]string{"task", "show", "5", ""}, offered(byNothing)},
		{[]string{"task", "create", ""}, offered(byNothing)},
		{[]string{"task", "create", "t", "--tags", ""}, offered(byNothing)},
		{[]string{"task", "list", "--state", ""}, offered(byNothing)},
		{[]string{"task", "update", "5", "--priority", ""}, offered(byNothing)},
		{[]string{"task", "list", "2", ""}, offered(byNothing)},
		{[]string{"task", "mv", "-t", ""}, offered(byID, as("@", folders...)...)},
		// Files.
		{[]string{"backup", ""}, offered(byFiles)},
		{[]string{"task", "create", "t", "--notes-file", ""}, offered(byFiles)},
		{[]string{"where", "--file", ""}, offered(byFiles)},
		{[]string{"marker", "bind", "3", ""}, offered(byFiles)},
		{[]string{"marker", "unbind", ""}, offered(byFiles)},
		{[]string{"marker", "bind", "3", "/tmp", ""}, offered(byNothing)},
		{[]string{"marker", "unbind", "/tmp", ""}, offered(byNothing)},
		{[]string{"backup", "/tmp", ""}, offered(byNothing)},
		{[]string{"task", "show", "-i", ""}, offered(byFiles)},
	} {
		if got := completes(t, home, c.args...); got != c.want {
			t.Errorf("__complete %q:\ngot:\n%s\nwant:\n%s", c.args, got, c.want)
		}
	}
	// Without descriptions, the candidates alone.
	r := testrun.Run{Home: home}.Koan(t, "__completeNoDesc", "task", "show", "@3")
	if want := offered(byID, "@3.start", "@3.done"); r.Stdout != want {
		t.Errorf("__completeNoDesc: %q, want %q", r.Stdout, want)
	}
}

// manyTree is a tree of 101 tasks in the root, Task 1 to Task 101, @1
// to @101.
func manyTree(t *testing.T) string {
	t.Helper()
	home := root(t)
	var tasks []string
	for i := range 101 {
		tasks = append(tasks, fmt.Sprintf(`{"title": "Task %d"}`, i+1))
	}
	batchOK(t, home, `{"tasks": [`+strings.Join(tasks, ",")+`]}`)
	return home
}

// More than 100 candidates offers none, and a hint instead.
func TestCompleteTooMany(t *testing.T) {
	home := manyTree(t)
	hint := "_activeHelp_ 101 match: type more, or use koan pick"
	if got, want := completes(t, home, "task", "show", "@"), offered(byID, hint); got != want {
		t.Errorf("101: %q, want %q", got, want)
	}
	ok(t, home, "task", "complete", "101")
	got := completes(t, home, "task", "complete", "")
	if lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n"); len(lines) != 101 ||
		lines[0] != "@1\tTask 1  /" || lines[99] != "@100\tTask 100  /" || lines[100] != ":36" {
		t.Errorf("100: %d lines:\n%s", len(lines), got)
	}
	// Filtered by what is typed first, in pick's order, not as text.
	want := []string{"1\tTask 1  /"}
	for i := 10; i <= 19; i++ {
		want = append(want, fmt.Sprintf("%d\tTask %d  /", i, i))
	}
	want = append(want, "100\tTask 100  /", "101\tTask 101  /")
	if got := completes(t, home, "task", "show", "1"); got != offered(byID, want...) {
		t.Errorf("typed 1: %q", got)
	}
}

// A root that isn't usable offers nothing, and prints no envelope.
func TestCompleteFailures(t *testing.T) {
	home := t.TempDir()
	if got := completes(t, home, "task", "show", "@"); got != offered(byID) {
		t.Errorf("no root: %q", got)
	}
	if got := completes(t, home, "folder", "show", "/"); got != offered(byPath) {
		t.Errorf("no root, a path: %q", got)
	}
}
