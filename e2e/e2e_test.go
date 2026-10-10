// Package e2e tests the built binary: what only a real process shows — exit
// codes, delivery to stdout, signals, and crashes (implementation-spec.md,
// Where each kind of test runs).
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/schematest"
)

func TestExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		code  int
		kind  string
	}{
		{"success", []string{"version"}, "", 0, ""},
		{"stdin input", []string{"version", "-i", "-"}, "{}", 0, ""},
		{"invalid input", []string{"version", "-i", "-"}, `{"x": 1}`, 1, `"kind":"invalid-input"`},
		{"unreadable input", []string{"version", "-i", "/nonexistent/in.json"}, "", 1, `"kind":"io"`},
		{"usage", []string{"nosuch"}, "", 2, `"kind":"usage"`},
		{"bare", nil, "", 2, `"kind":"usage"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := koan(t, tc.args...)
			cmd.Stdin = strings.NewReader(tc.stdin)
			r := run(t, cmd)
			if r.code != tc.code {
				t.Fatalf("exit %d, want %d: %s%s", r.code, tc.code, r.stdout, r.stderr)
			}
			envelope(t, r)
			if !strings.Contains(r.stdout, tc.kind) {
				t.Errorf("want %s: %s", tc.kind, r.stdout)
			}
		})
	}
}

// stdin is never read unless named: a terminal-less, never-closed stdin
// must not block a command that does not ask for it.
func TestStdinNotRead(t *testing.T) {
	cmd := koan(t, "version")
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Close()
	cmd.Stdin = pr
	if r := run(t, cmd); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
}

func TestHelp(t *testing.T) {
	r := run(t, koan(t, "version", "--help"))
	if r.code != 0 || !strings.Contains(r.stdout, "Usage:") || r.stderr != "" {
		t.Errorf("exit %d: %q %q", r.code, r.stdout, r.stderr)
	}
}

// A closed pipe: EPIPE, not death by SIGPIPE, so exit 3 with the notice.
func TestClosedPipe(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	pr.Close()
	defer pw.Close()
	cmd := koan(t, "version")
	cmd.Stdout = pw
	r := run(t, cmd)
	if r.code != 3 || !strings.HasPrefix(r.stderr, "koan: result not delivered: ") {
		t.Errorf("exit %d, stderr %q; want 3 and the notice", r.code, r.stderr)
	}
}

// info locates the config from the real environment, and reports a machine
// with nothing set up, or no usable HOME, as state: exit 0.
func TestInfo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		unset bool // no HOME and no XDG_CONFIG_HOME
	}{
		{"fresh home", false},
		{"no home", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := koan(t, "info")
			var want any // config.path: null when it can't be located
			if tc.unset {
				cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			} else {
				want = filepath.Join(envHome(cmd), configDir, "config.toml")
			}
			r := run(t, cmd)
			if r.code != 0 {
				t.Fatalf("exit %d: %s%s", r.code, r.stdout, r.stderr)
			}
			envelope(t, r)
			var env struct {
				Result struct {
					Config      map[string]any `json:"config"`
					Initialized bool           `json:"initialized"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
				t.Fatal(err)
			}
			if c := env.Result.Config; c["path"] != want || c["state"] != "missing" || env.Result.Initialized {
				t.Errorf("got %s; want config.path %v, state missing, not initialized", r.stdout, want)
			}
		})
	}
}

// show against the real filesystem: not-initialized in a fresh home (exit
// 1), and a task found in a tree written by hand (init comes later).
func TestShow(t *testing.T) {
	cmd := koan(t, "show", "1")
	r := run(t, cmd)
	envelope(t, r)
	if r.code != 1 || !strings.Contains(r.stdout, `"missing":"config"`) {
		t.Fatalf("fresh home: exit %d: %s", r.code, r.stdout)
	}

	cmd = koan(t, "show", "1")
	home := envHome(cmd)
	root := filepath.Join(home, "tasks")
	for p, content := range map[string]string{
		filepath.Join(home, configDir, "config.toml"): `root = "` + root + "\"\n",
		filepath.Join(root, "koan.json"):              `{"schema": 2, "migration": 1}`,
		filepath.Join(home, configDir, "state.json"):  `{"schema": 1, "root": "` + root + `", "last_id": 1}`,
		filepath.Join(root, "proj", "1.json"):         `{"schema": 1, "id": 1, "title": "t", "priority": null, "created_at": "2026-09-27T00:00:00Z", "completed_at": null,"updated_at": "2026-09-27T00:00:00Z", "blocked_by": [2], "tags": [], "extra": {}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r = run(t, cmd)
	envelope(t, r)
	for _, want := range []string{`"folder":"/proj"`, `"readiness":"blocked"`, `"kind":"dangling-reference"`} {
		if r.code != 0 || !strings.Contains(r.stdout, want) {
			t.Errorf("exit %d, want %s: %s", r.code, want, r.stdout)
		}
	}
}

// init creates a tree the other commands then use; a second init refuses.
// ~/ is expanded by koan itself, as with no shell in between.
func TestInit(t *testing.T) {
	first := koan(t, "init", "~/tasks")
	home := envHome(first)
	same := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = first.Env
		return cmd
	}
	steps(t, []step{
		{first, 0, `"result":{"root":"` + home + `/tasks","action":"created","last_id":0}`},
		{same("info"), 0, `"usable":true`},
		{same("show", "1"), 1, `"kind":"not-found"`},
		{same("init", home+"/other"), 1, `"rule":"config-exists"`},
	})
}

// create writes tasks that show then reads, with notes piped in on stdin.
func TestCreate(t *testing.T) {
	tr := newTree(t)
	home, same := tr.home, tr.cmd
	steps(t, []step{
		{same("create", "Deploy"), 0, `"id":1,"title":"Deploy"`},
		{stdin(same("create", "Fix login bug", "--blocked-by", "1", "--notes-file", "-"), "call first\n"), 0, `"id":2,`},
		{same("show", "2"), 0, `"blocked_by":[1],"tags":[],"extra":{},"folder":"/","notes_path":"` + home + `/tasks/2.md","readiness":"blocked","blocking":[1]`},
		{same("create", "x", "--folder", "/nope", "--blocked-by", "9"), 1, `"details":{"folders":["/nope"],"ids":[9],"paths":[]}`},
		{same("create", "x", "--notes", "a", "--notes-file", "-"), 2, `"kind":"usage"`},
		{same("info"), 0, `"last_id":2`},
	})
	if b, err := os.ReadFile(filepath.Join(home, "tasks", "2.md")); err != nil || string(b) != "call first\n" {
		t.Errorf("2.md: %q %v", b, err)
	}
}

// create-batch writes a plan: refs become IDs, folders are made, and the
// tasks read back like any others.
func TestCreateBatch(t *testing.T) {
	tr := newTree(t)
	same := tr.cmd
	steps(t, []step{
		{same("create", "Existing"), 0, `"id":1,`},
		{stdin(same("create-batch", "-i", "-"), `{"folder": "/work/api", "tasks": [
			{"ref": "schema", "title": "Design schema", "notes": "draft"},
			{"ref": "migrate", "title": "Write migrations", "blocked_by": ["schema"]},
			{"title": "Deploy", "folder": "/ops", "blocked_by": ["migrate", 1]}]}`),
			0, `"result":{"ids":[2,3,4],"refs":{"schema":2,"migrate":3},"folders_created":["/ops","/work","/work/api"]}`},
		{same("show", "4"), 0, `"blocked_by":[1,3],"tags":[],"extra":{},"folder":"/ops"`},
		{same("frontier", "--fields", "id"), 0, `"tasks":[{"id":1},{"id":2}]`},
		{stdin(same("create-batch", "-i", "-"), `{"tasks": [{"title": "x", "blocked_by": ["later"]}, {"ref": "later", "title": "y"}]}`),
			1, `"field":"/tasks/0/blocked_by/0"`},
		{same("create-batch"), 2, `"reason":"missing required option --input"`},
		{same("info"), 0, `"last_id":4`},
	})
	if b, err := os.ReadFile(filepath.Join(tr.root(), "work", "api", "2.md")); err != nil || string(b) != "draft" {
		t.Errorf("2.md: %q %v", b, err)
	}
}

// create-folder makes the folders create then files tasks in.
func TestCreateFolder(t *testing.T) {
	same := newTree(t).cmd
	steps(t, []step{
		{same("create-folder", "/proj/travel"), 1, `"folders":["/proj"]`},
		{same("create-folder", "-p", "/proj/travel"), 0, `"result":{"folder":"/proj/travel","created":["/proj","/proj/travel"]}`},
		{same("create-folder", "-p", "/proj/travel"), 0, `"created":[]`},
		{same("create", "Book flights", "--folder", "/proj/travel"), 0, `"id":1,`},
		{same("show", "1"), 0, `"folder":"/proj/travel"`},
	})
}

// done makes a dependent ready, and reopen blocks it again; doing
// either twice changes nothing.
func TestDoneReopen(t *testing.T) {
	same := newTree(t).cmd
	steps(t, []step{
		{same("create", "Book flights"), 0, `"id":1,`},
		{same("create", "Pack bags", "--blocked-by", "1"), 0, `"id":2,`},
		{same("show", "2"), 0, `"readiness":"blocked","blocking":[1]`},
		{same("done", "1"), 0, `"changed":true`},
		{same("show", "2"), 0, `"readiness":"ready","blocking":[]`},
		{same("done", "1"), 0, `"changed":false`},
		{same("done", "9"), 1, `"ids":[9]`},
		{same("reopen", "1"), 0, `"completed_at":null,`},
		{same("show", "2"), 0, `"readiness":"blocked","blocking":[1]`},
		{same("reopen", "1"), 0, `"changed":false`},
	})
}

// update changes only what it names; the same update again changes nothing.
func TestUpdate(t *testing.T) {
	same := newTree(t).cmd
	steps(t, []step{
		{same("create", "Book flights", "--tags", "travel", "--extra", `{"status":"new"}`), 0, `"id":1,`},
		{same("update", "1", "--priority", "2", "--tags-add", "urgent", "--extra-merge", `{"status":"waiting"}`), 0,
			`"priority":2,"created_at":`},
		{same("show", "1"), 0, `"tags":["travel","urgent"],"extra":{"status":"waiting"}`},
		{same("update", "1", "--priority", "2", "--tags-add", "urgent", "--extra-merge", `{"status":"waiting"}`), 0, `"changed":[]`},
		{same("update", "1"), 1, `"kind":"invalid-input"`},
		{same("update", "1", "--tags-add", "x", "--tags-replace-all", "y"), 1, `"field":"/tags/add"`},
		{same("update", "9", "--title", "x"), 1, `"ids":[9]`},
	})
}

// block adds blockers that show then sees, and refuses a cycle; unblock
// removes them again.
func TestBlockUnblock(t *testing.T) {
	same := newTree(t).cmd
	steps(t, []step{
		{same("create", "a"), 0, `"id":1,`},
		{same("create", "b"), 0, `"id":2,`},
		{same("create", "c"), 0, `"id":3,`},
		{same("block", "1", "--blockers", "2"), 0, `"blocked_by":[2],`},
		{same("block", "2", "--blockers", "3"), 0, `"added":[3]`},
		{same("show", "1"), 0, `"readiness":"blocked","blocking":[2]`},
		{same("block", "3", "--blockers", "1,2"), 1, `"details":{"rule":"acyclic","ids":[1,2],"cycles":[[3,1,2],[3,2]]}`},
		{same("block", "1", "--blockers", "2"), 0, `"added":[]`},
		{same("block", "1", "--blockers", "1"), 1, `"kind":"invalid-input"`},
		{same("block", "1"), 2, `"kind":"usage"`},
		{same("unblock", "1", "--blockers", "2,9"), 0, `"blocked_by":[],`},
		{same("show", "1"), 0, `"readiness":"ready","blocking":[]`},
		{same("unblock", "1", "--blockers", "2"), 0, `"removed":[]`},
	})
}

// list returns tasks across folders in tree order, with their readiness;
// frontier returns only the ready ones, in the order to work on them.
func TestListFrontier(t *testing.T) {
	same := newTree(t).cmd
	steps(t, []step{
		{same("list"), 0, `"result":{"tasks":[],"total":0,"truncated":false}`},
		{same("create-folder", "-p", "/proj/travel"), 0, `"created"`},
		{same("create", "b", "--folder", "/proj/travel"), 0, `"id":1,`},
		{same("create", "a", "--blocked-by", "1"), 0, `"id":2,`},
		{same("done", "1"), 0, `"changed":true`},
		{same("list"), 0, `"tasks":[{"schema":1,"id":2,`},
		{same("list", "--readiness", "ready,blocked,done", "--include-folders"), 0, `"folders":["/","/proj","/proj/travel"],"tasks":[{"schema":1,"id":2,`},
		{same("list", "--folder", "/nope"), 1, `"folders":["/nope"]`},
		{same("create", "c", "--priority", "5", "--folder", "/proj"), 0, `"id":3,`},
		{same("create", "d", "--blocked-by", "3"), 0, `"id":4,`},
		{same("frontier"), 0, `"tasks":[{"schema":1,"id":3,`},
		{same("frontier", "--folder", "/proj", "--recursive=false"), 0, `"tasks":[{"schema":1,"id":3,`},
		{same("done", "3"), 0, `"changed":true`},
		{same("frontier", "--folder", "/proj"), 0, `"result":{"tasks":[],"total":0,"truncated":false}`},
	})
}

// why explains a blocked task from the command line, and narrows its tasks.
func TestWhy(t *testing.T) {
	same := newTree(t).cmd
	steps(t, []step{
		{same("create", "a"), 0, `"id":1,`},
		{same("create", "b", "--blocked-by", "1"), 0, `"id":2,`},
		{same("create", "c", "--blocked-by", "2"), 0, `"id":3,`},
		{same("why", "3"), 0, `"result":{"readiness":"blocked","ready":[1],"stuck":[]},"warnings":[]`},
		{same("why", "3", "--include-tasks", "--fields", "blocking"), 0, `"tasks":[{"id":3,"blocking":[2]},{"id":2,"blocking":[1]},{"id":1,"blocking":[]}]`},
		{same("why", "3", "--fields", "blocking"), 1, `"field":"/fields"`},
		{same("done", "1"), 0, `"changed":true`},
		{same("why", "3"), 0, `"ready":[2]`},
		{same("why", "9"), 1, `"kind":"not-found"`},
	})
}

// frontier and list narrowed from the command line: each output shape —
// projected, cut by a limit, the count alone, filtered — is exactly as
// expected and passes its output schema, and a warning survives narrowing.
func TestNarrowing(t *testing.T) {
	tr := newTree(t)
	same := tr.cmd
	steps(t, []step{
		{same("create", "a", "--priority", "1", "--tags", "db"), 0, `"id":1,`},
		{same("create", "b", "--priority", "3", "--tags", "db,backend"), 0, `"id":2,`},
		{same("create", "c", "--blocked-by", "1"), 0, `"id":3,`},
		{same("create", "d"), 0, `"id":4,`},
		{same("done", "4"), 0, `"changed":true`},
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"frontier", "--limit", "1", "--fields", "title,priority"},
			`{"tasks":[{"id":2,"title":"b","priority":3}],"total":2,"truncated":true}`},
		{[]string{"frontier", "--limit", "0"}, `{"tasks":[],"total":2,"truncated":true}`},
		{[]string{"frontier", "--tags-all", "db,backend", "--fields", "id"}, `{"tasks":[{"id":2}],"total":1,"truncated":false}`},
		{[]string{"list", "--readiness", "blocked", "--fields", "readiness,blocking"},
			`{"tasks":[{"id":3,"readiness":"blocked","blocking":[1]}],"total":1,"truncated":false}`},
		{[]string{"list", "--readiness", "done", "--fields", "readiness"},
			`{"tasks":[{"id":4,"readiness":"done"}],"total":1,"truncated":false}`},
		{[]string{"list", "--tags-any", "backend,nope", "--limit", "5", "--fields", "tags", "--include-folders"},
			`{"folders":["/"],"tasks":[{"id":2,"tags":["backend","db"]}],"total":1,"truncated":false}`},
	} {
		r := run(t, same(tc.args...))
		envelope(t, r)
		var env struct{ Result json.RawMessage }
		if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
			t.Fatal(err)
		}
		if r.code != 0 || string(env.Result) != tc.want {
			t.Errorf("%q: exit %d, result\n  %s\nwant\n  %s", tc.args, r.code, env.Result, tc.want)
		}
		if ok, f := schematest.Check(t, tc.args[0]+"-output", env.Result); !ok {
			t.Errorf("%q: %s-output rejects at %s", tc.args, tc.args[0], f)
		}
	}
	if err := os.WriteFile(filepath.Join(tr.home, "tasks", "9.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps(t, []step{
		{same("frontier", "--limit", "0"), 0, `"warnings":[{"kind":"unusable-file"`},
		{same("list", "--readiness", "nope"), 1, `"field":"/readiness/0"`},
		{same("frontier", "--fields", "id,nope"), 1, `"field":"/fields/1"`},
		{same("list", "--include-complete"), 2, `"kind":"usage"`},
	})
}

// A relative root is resolved against the working directory as the shell
// reports it: through a symlink, not with it resolved.
func TestInitRelative(t *testing.T) {
	cmd := koan(t, "init", "tasks")
	home := envHome(cmd)
	if err := os.Mkdir(filepath.Join(home, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(filepath.Join(home, "real"), link); err != nil {
		t.Fatal(err)
	}
	cmd.Dir = link
	cmd.Env = append(cmd.Env, "PWD="+link)
	r := run(t, cmd)
	envelope(t, r)
	if r.code != 0 || !strings.Contains(r.stdout, `"root":"`+link+`/tasks"`) {
		t.Fatalf("exit %d: %s", r.code, r.stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "real", "tasks", "koan.json")); err != nil {
		t.Error(err)
	}
}

func TestFullDisk(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is Linux-only")
	}
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skip(err)
	}
	defer full.Close()
	cmd := koan(t, "version")
	cmd.Stdout = full
	r := run(t, cmd)
	if r.code != 3 || !strings.HasPrefix(r.stderr, "koan: result not delivered: ") {
		t.Errorf("exit %d, stderr %q; want 3 and the notice", r.code, r.stderr)
	}
}

// A panic is a crash: SIGABRT, exit 134, never 2 (a usage error).
func TestCrash(t *testing.T) {
	cmd := koan(t, "version")
	cmd.Env = append(cmd.Env, "KOAN_E2E_PANIC=1")
	r := run(t, cmd)
	ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ws.Signaled() || ws.Signal() != syscall.SIGABRT {
		t.Errorf("exit %d (%v), want death by SIGABRT", r.code, cmd.ProcessState)
	}
	if r.stdout != "" {
		t.Errorf("stdout %q, want nothing", r.stdout)
	}
}

// The release build has no test hooks: their variables change nothing
// (implementation-spec.md, Test hooks).
func TestReleaseIgnoresHooks(t *testing.T) {
	cmd := koan(t, "version")
	cmd.Path, cmd.Args[0] = release, release
	cmd.Env = append(cmd.Env, "KOAN_E2E_PANIC=1", "KOAN_E2E_HOLD=1", "KOAN_E2E_CRASH_BEFORE=1", "KOAN_E2E_CLOCK=x")
	r := run(t, cmd)
	envelope(t, r)
	if r.code != 0 {
		t.Errorf("exit %d: %s", r.code, r.stdout)
	}
}

// stderr gets one line after the envelope: a failure's kind and message, a
// count of warnings, or nothing (cli-spec.md, Output).
func TestStderr(t *testing.T) {
	tr := newTree(t)
	for _, title := range []string{"a", "b", "c", "d"} {
		steps(t, []step{{tr.cmd("create", title), 0, `"ok":true`}})
	}
	for _, id := range []string{"2", "3", "4"} {
		if err := os.WriteFile(filepath.Join(tr.root(), id+".json"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		cmd    *exec.Cmd
		code   int
		stderr string
	}{
		{"failure", tr.cmd("done", "999"), 1, "koan: not-found: not found: task 999\n"},
		{"usage", tr.cmd("nosuch"), 2, "koan: usage: unknown command\n"},
		{"warnings", tr.cmd("list"), 0, "koan: 3 warnings (see .warnings in the output)\n"},
		{"clean", tr.cmd("version"), 0, ""},
		{"corrupt", tr.cmd("show", "2"), 1, "koan: corrupt: " + filepath.Join(tr.root(), "2.json") + ": corrupt: empty\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, tc.cmd)
			envelope(t, r)
			if r.code != tc.code || r.stderr != tc.stderr {
				t.Errorf("exit %d, stderr %q; want %d, %q", r.code, r.stderr, tc.code, tc.stderr)
			}
		})
	}

	t.Run("not delivered", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("/dev/full is Linux-only")
		}
		full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
		if err != nil {
			t.Skip(err)
		}
		defer full.Close()
		cmd := tr.cmd("show", "999") // a failure, so it would have a line
		cmd.Stdout = full
		r := run(t, cmd)
		if r.code != 3 || !strings.HasPrefix(r.stderr, "koan: result not delivered: ") || strings.Count(r.stderr, "\n") != 1 {
			t.Errorf("exit %d, stderr %q; want 3 and only the notice", r.code, r.stderr)
		}
	})

	t.Run("help", func(t *testing.T) {
		if r := run(t, tr.cmd("--help")); r.code != 0 || r.stderr != "" {
			t.Errorf("exit %d, stderr %q", r.code, r.stderr)
		}
	})

	// A newline in the root's path is escaped, so the line stays one line.
	t.Run("newline in path", func(t *testing.T) {
		cmd := koan(t, "init", "~/a\nb")
		r := run(t, cmd)
		envelope(t, r)
		home := envHome(cmd)
		if err := os.WriteFile(filepath.Join(home, "a\nb", "1.json"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		show := exec.Command(binary, "show", "1")
		show.Env = cmd.Env
		r = run(t, show)
		if want := "koan: corrupt: " + home + `/a\nb/1.json: corrupt: empty` + "\n"; r.code != 1 || r.stderr != want {
			t.Errorf("exit %d, stderr %q; want 1, %q", r.code, r.stderr, want)
		}
	})
}
