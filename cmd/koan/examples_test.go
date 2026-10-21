package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// docStep is one command of a doc's examples and what it must give.
// cmd is the command as the doc writes it, its trailing comment left
// out. want holds one entry per line of standard output: a JSON object
// that the line must hold, field by field, or else the line's exact text.
type docStep struct {
	cmd  string
	exit int
	want []string
}

// skillSteps are the skill's examples, in the order it gives them. They
// run on one root, made by koan init --ids-from 40, as the skill says.
var skillSteps = []docStep{
	{cmd: "koan info", want: []string{`{"ok":true,"result":{"state":"usable","next_id":40},"warnings":[]}`}},
	{cmd: "koan version", want: []string{`{"ok":true,"result":{"format":1},"warnings":[]}`}},

	// The model
	{cmd: `koan batch -i - <<'EOF'
{"folders": [{"path": "/work/api", "title": "API rework", "notes": "Move the API to v2.\n"}],
 "folder": "/work/api",
 "tasks": [
  {"label": "$design", "title": "Design the endpoints", "priority": 2, "blocked_by": ["/work/api.start"], "blocks": ["/work/api"]},
  {"label": "$build", "title": "Build them", "blocked_by": ["$design"], "blocks": ["/work/api"]},
  {"title": "Announce the new API", "folder": "/work", "blocked_by": ["/work/api"]}]}
EOF`, want: []string{`{"ok":true,"result":{"folders":[{"id":40,"path":"/work"},{"id":41,"path":"/work/api"}],"tasks":[42,43,44],
		"labels":{"$build":43,"$design":42},"gates_made":[{"id":45,"end":"start"},{"id":46,"end":"done"}],"reblocked":[]},"warnings":[]}`}},

	// The daily loop
	{cmd: "koan task frontier --limit 10 --fields ref,title", want: []string{`{"ok":true,"result":{"tasks":[{"id":45,"ref":"@45 (/work/api.start)","title":"Start"}],"total":1,"truncated":false}}`}},
	{cmd: "koan task why 43", want: []string{`{"ok":true,"result":{"id":43,"readiness":"blocked","ready":[45]}}`}},
	{cmd: "koan task complete 45", want: []string{`{"ok":true,"result":{"id":45,"changed":true,"released":[42]}}`}},
	{cmd: `koan task claim-next --folder /work --extra '{"by":"agent-1"}'`, want: []string{`{"ok":true,"result":{"task":{"id":42,"claim":{"task":42,"extra":{"by":"agent-1"}}},"claim":{"task":42}}}`}},
	{cmd: "koan task show 42", want: []string{`{"ok":true,"result":{"task":{"id":42,"title":"Design the endpoints","notes_path":"~/koan-root/tree/@work/@api/@42.md"}},"warnings":[]}`}},
	{cmd: "koan task complete 42", want: []string{`{"ok":true,"result":{"id":42,"changed":true,"released":[43]}}`}},

	// Claims
	{cmd: `koan task claim 43 --extra '{"by":"agent-1"}'`, want: []string{`{"ok":true,"result":{"claim":{"task":43,"extra":{"by":"agent-1"}}}}`}},
	{cmd: "koan task claim 43", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"claimed","ids":[43]}}`}},
	{cmd: "koan task list --claimed --limit 20 --fields title,claim", want: []string{`{"ok":true,"result":{"tasks":[{"id":43,"title":"Build them","claim":{"task":43}}],"total":1}}`}},
	{cmd: "koan task release 43", want: []string{`{"ok":true,"result":{"id":43,"changed":true}}`}},

	// Writing
	{cmd: "koan task create 'Write the migration' --folder /work/api --priority 1 --tags db --blocked-by 42 --blocks /work/api",
		want: []string{`{"ok":true,"result":{"id":47,"ref":"@47 (/work/api)","gates_made":[],"reblocked":[]},"warnings":[]}`}},
	{cmd: `koan task create 'Investigate the flaky test' --notes-file - <<'EOF'
Seen in CI on 2026-10-19; fails about 1 run in 20.
EOF`, want: []string{`{"ok":true,"result":{"id":48,"ref":"@48 (/)","notes_path":"~/koan-root/tree/@48.md"}}`}},
	{cmd: `koan task update 48 --priority null --tags-add urgent --extra-merge '{"status":"waiting"}'`, want: []string{`{"ok":true,"result":{"id":48,"changed":true}}`}},
	{cmd: "koan task block 44 --blocked-by 48", want: []string{`{"ok":true,"result":{"id":44,"changed":true}}`}},
	{cmd: "koan task unblock 44 --blocked-by 48", want: []string{`{"ok":true,"result":{"id":44,"changed":true}}`}},
	{cmd: "koan task block 42 --blocked-by 43", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"cycle","ids":[42,43]}}`}},

	// Folders
	{cmd: "koan folder create /work/web/client --title 'Web client'", want: []string{`{"ok":true,"result":{"id":50,"created":[{"id":49,"path":"/work/web"},{"id":50,"path":"/work/web/client"}]}}`}},
	{cmd: `koan folder update /work/web/client --extra-merge '{"jira":"WEB-12"}'`, want: []string{`{"ok":true,"result":{"id":50,"changed":true}}`}},
	{cmd: "koan folder show /work/api", want: []string{`{"ok":true,"result":{"folder":{"id":41,"gates":{"start":{"id":45,"state":"complete"},"done":{"id":46,"state":"open"}},
		"finished":false,"notes_path":"~/koan-root/tree/@work/@api/@notes.md"}}}`}},
	{cmd: "koan task create 'Port the client' --folder /work/web/client --blocked-by /work/web/client.start --blocks /work/web/client",
		want: []string{`{"ok":true,"result":{"id":51,"gates_made":[{"id":52,"end":"start"},{"id":53,"end":"done"}]}}`}},
	{cmd: "koan task block /work/web/client.start --blocked-by /work/api", want: []string{`{"ok":true,"result":{"id":52,"changed":true,"gates_made":[],"reblocked":[52]}}`}},

	// References
	{cmd: "koan where @41:docs/plan.md", want: []string{`{"ok":true,"result":{"ref":"@41:docs/plan.md (/work/api:docs/plan.md)","kind":"material","id":41,"file":"~/koan-root/tree/@work/@api/docs/plan.md"}}`}},
	{cmd: "koan where /work/api.done", want: []string{`{"ok":true,"result":{"ref":"@46 (/work/api.done)","kind":"task","id":46,"gate":"done","file":"~/koan-root/tree/@work/@api/@done.md"}}`}},

	// Keep output small
	{cmd: "koan task list --limit 20 --fields ref,title,readiness", want: []string{`{"ok":true,"result":{"tasks":[{"id":48},{"id":44},{"id":43},{"id":46},{"id":47},{"id":51},{"id":52},{"id":53}],"total":8,"truncated":false}}`}},
	{cmd: "koan task list --state complete --limit 1 --fields id", want: []string{`{"ok":true,"result":{"tasks":[{"id":42}],"total":2,"truncated":true}}`}},
	{cmd: "koan folder list --folder /work --recursive=false --fields path,title,open,finished", want: []string{`{"ok":true,"result":{"folders":[{"id":41,"path":"/work/api"},{"id":49,"path":"/work/web"}],"total":2}}`}},
	{cmd: `koan task list --fields title,extra --limit 200 | jq -c 'if .ok then .result |= {tasks: (.tasks | map(select(.extra.status == "waiting"))), unscanned: (.total - (.tasks | length))} else . end'`,
		want: []string{`{"ok":true,"result":{"tasks":[{"id":48,"title":"Investigate the flaky test","extra":{"status":"waiting"}}],"unscanned":0},"warnings":[]}`}},

	// Worktrees
	{cmd: "koan marker bind @41 --dir ~/code/api-v2", want: []string{`{"ok":true,"result":{"marker":{"id":41,"path":"/work/api"},"replaced":false}}`}},
	{cmd: "cd ~/code/api-v2 && koan context", want: []string{`{"ok":true,"result":{"folder":{"id":41},"ancestors":[{"id":40}],"notes":"Move the API to v2.\n",
		"tasks":[{"id":43},{"id":46},{"id":47}],"marker":"~/code/api-v2/.koan/marker.json"}}`}},
	{cmd: "koan where --file ~/code/api-v2/src/main.go", want: []string{`{"ok":true,"result":{"ref":"@41 (/work/api)","kind":"folder","marker":"~/code/api-v2/.koan/marker.json"},"warnings":[]}`}},
	{cmd: "koan folder move /work/api --to /work/api2", want: []string{`{"ok":true,"result":{"id":41,"changed":true,"path":"/work/api2"}}`}},
	{cmd: "koan where --file ~/code/api-v2", want: []string{`{"ok":true,"result":{"ref":"@41 (/work/api2)"},"warnings":[{"kind":"stale-marker","ids":[41]}]}`}},
	{cmd: "koan marker bind @41 --dir ~/code/api-v2", want: []string{`{"ok":true,"result":{"marker":{"id":41,"path":"/work/api2"},"replaced":true}}`}},
	{cmd: "koan folder move /work/api2 --to /work/api", want: []string{`{"ok":true,"result":{"id":41,"changed":true,"path":"/work/api"}}`}},

	// Moving, deleting and archiving
	{cmd: "koan task move 48 --to /work/api", want: []string{`{"ok":true,"result":{"id":48,"changed":true,"notes_path":"~/koan-root/tree/@work/@api/@48.md"}}`}},
	{cmd: "koan folder move /work/web/client --to /work/client", want: []string{`{"ok":true,"result":{"id":50,"changed":true,"path":"/work/client","created":[]}}`}},
	{cmd: "koan task list --folder /work/client --state open,complete --fields title,state,claim", want: []string{`{"ok":true,"result":{"tasks":[{"id":51},{"id":52},{"id":53}],"total":3}}`}},
	{cmd: "koan folder delete /work/client", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"not-empty","ids":[51]}}`}},
	{cmd: "koan folder delete /work/client --contents", want: []string{`{"ok":true,"result":{"removed":[50,51,52,53],"released":[]}}`}},
	{cmd: "koan folder archive /work/api", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"not-finished","ids":[43,46,47,48]}}`}},
	{cmd: "koan task update 48 --tags-add cancelled && koan task complete 48 && koan task archive 48", want: []string{
		`{"ok":true,"result":{"id":48,"changed":true}}`,
		`{"ok":true,"result":{"id":48,"changed":true}}`,
		`{"ok":true,"result":{"id":48,"archive":"~/koan-root/archived/work/api/@48.md","left":[48]}}`}},
	{cmd: "koan task show 48", exit: 1, want: []string{`{"ok":false,"error":{"kind":"not-found","ids":[48],"archived":[{"id":48,"archive":"~/koan-root/archived/work/api/@48.md"}]}}`}},

	// When the tree is damaged
	{cmd: "koan doctor", want: []string{`{"ok":true,"result":{"findings":[]},"warnings":[]}`}},
	{cmd: "koan repair", want: []string{`{"ok":true,"result":{"fixed":[],"left":[]},"warnings":[]}`}},
	{cmd: "koan backup ~/backups/koan-$(date +%F)", want: []string{`{"ok":true,"result":{"recopied":false,"clean":true,"findings":[]},"warnings":[]}`}},
}

// TestSkill runs every command in the skill's sh blocks, as written, in a
// shell with the built koan first on PATH, and checks each gives what
// skillSteps says, so the skill can't drift from the binary.
func TestSkill(t *testing.T) {
	// The worktree the skill binds, beside the checkout it comes from.
	runExamples(t, filepath.Join("claude", "skills", "koan", "SKILL.md"), []string{
		"koan init --ids-from 40",
		"git init -q ~/code/api",
		"git -C ~/code/api -c user.name=koan -c user.email=koan@example.com commit -q --allow-empty -m start",
		"git -C ~/code/api worktree add -q ~/code/api-v2",
	}, skillSteps)
}

// readmeSteps are the README's sh examples, in order, on a new root. Its
// shell blocks, which install things or act on a root of the original
// koan, aren't run.
var readmeSteps = []docStep{
	{cmd: "koan init", want: []string{`{"ok":true,"result":{"created":["root","tree","archive","database"],"first_id":1},"warnings":[]}`}},

	// A taste
	{cmd: "koan folder create /work/api --title 'API rework'", want: []string{`{"ok":true,"result":{"id":2,"created":[{"id":1,"path":"/work"},{"id":2,"path":"/work/api"}]},"warnings":[]}`}},
	{cmd: "koan task create 'Design the endpoints' --folder /work/api --priority 2", want: []string{`{"ok":true,"result":{"id":3}}`}},
	{cmd: "koan task create 'Build them' --folder /work/api --blocked-by 3", want: []string{`{"ok":true,"result":{"id":4,"reblocked":[]}}`}},
	{cmd: "koan task frontier --limit 10 --fields ref,title", want: []string{`{"ok":true,"result":{"tasks":[{"id":3,"ref":"@3 (/work/api)","title":"Design the endpoints"}],"total":1}}`}},
	{cmd: `koan task claim-next --extra '{"by":"me"}'`, want: []string{`{"ok":true,"result":{"task":{"id":3},"claim":{"task":3,"extra":{"by":"me"}}}}`}},
	{cmd: "koan task complete 3", want: []string{`{"ok":true,"result":{"id":3,"changed":true,"released":[4]}}`}},
	{cmd: "koan task show 4", want: []string{`{"ok":true,"result":{"task":{"id":4,"readiness":"ready","notes_path":"~/koan-root/tree/@work/@api/@4.md"}},"warnings":[]}`}},

	// Ordering folders
	{cmd: "koan task create 'Announce it' --folder /work --blocked-by /work/api", want: []string{`{"ok":true,"result":{"id":5,"gates_made":[{"id":6,"folder":{"id":2},"end":"done"}]}}`}},
	{cmd: "koan task block /work/api.done --blocked-by 3,4", want: []string{`{"ok":true,"result":{"id":6,"changed":true,"reblocked":[6]}}`}},
	{cmd: "koan task why 5", want: []string{`{"ok":true,"result":{"id":5,"readiness":"blocked","blockers":[{"id":6}],"ready":[4]}}`}},
	{cmd: `koan batch -i - <<'EOF'
{"folders": [{"path": "/home/trip", "title": "Lisbon in May"}],
 "folder": "/home/trip",
 "tasks": [{"label": "$flights", "title": "Book flights"},
           {"title": "Book a hotel", "blocked_by": ["$flights"]}]}
EOF`, want: []string{`{"ok":true,"result":{"folders":[{"id":7,"path":"/home"},{"id":8,"path":"/home/trip"}],"tasks":[9,10],"labels":{"$flights":9}},"warnings":[]}`}},

	// Backup and restore
	{cmd: "koan backup ~/backups/koan-$(date +%F)", want: []string{`{"ok":true,"result":{"clean":true,"findings":[]},"warnings":[]}`}},
	{cmd: "mv ~/koan-root ~/koan-root.before-restore"},
	{cmd: "cp -R ~/backups/koan-$(date +%F) ~/koan-root"},
	{cmd: "koan doctor", want: []string{`{"ok":true,"result":{"findings":[]},"warnings":[]}`}},
}

// TestReadme runs the README's sh examples as TestSkill runs the skill's.
func TestReadme(t *testing.T) {
	runExamples(t, "README.md", nil, readmeSteps)
}

// runExamples runs the commands of doc's sh blocks in a scratch HOME, after
// setup, and checks them against steps.
func runExamples(t *testing.T, doc string, setup []string, steps []docStep) {
	for _, tool := range []string{"bash", "jq", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("the examples need %s", tool)
			}
			t.Skipf("the examples need %s", tool)
		}
	}
	text, err := os.ReadFile(filepath.Join(testrun.Repo(), doc))
	if err != nil {
		t.Fatal(err)
	}
	cmds := exampleCommands(string(text))
	var want []string
	for _, s := range steps {
		want = append(want, s.cmd)
	}
	if !slices.Equal(cmds, want) {
		t.Fatalf("the commands of %s and its steps differ: update the steps to match.\n%s:\n%s\n\nsteps:\n%s",
			doc, doc, strings.Join(cmds, "\n"), strings.Join(want, "\n"))
	}

	home := t.TempDir()
	path := filepath.Dir(testrun.Binary(t)) + string(os.PathListSeparator) + os.Getenv("PATH")
	sh := func(cmd string) (string, int) {
		t.Helper()
		c := exec.Command("bash", "-c", cmd)
		c.Dir = home
		c.Env = append(exampleEnv(home), "PATH="+path)
		var stdout, stderr bytes.Buffer
		c.Stdout, c.Stderr = &stdout, &stderr
		err := c.Run()
		if exit, ok := err.(*exec.ExitError); ok {
			return stdout.String(), exit.ExitCode()
		} else if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return stdout.String(), 0
	}
	for _, cmd := range setup {
		if out, exit := sh(cmd); exit != 0 {
			t.Fatalf("%s: exit %d\n%s", cmd, exit, out)
		}
	}

	for _, s := range steps {
		out, exit := sh(s.cmd)
		var lines []string
		if out != "" {
			lines = strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		}
		if exit != s.exit || len(lines) != len(s.want) {
			t.Errorf("%s:\nexit %d, want %d; %d lines, want %d:\n%s", s.cmd, exit, s.exit, len(lines), len(s.want), out)
			continue
		}
		names := exampleCommandNames(s.cmd)
		for i, line := range lines {
			// The docs write paths under the scratch HOME as ~.
			if err := holds(strings.ReplaceAll(line, home, "~"), s.want[i]); err != nil {
				t.Errorf("%s:\nline %d: %v\ngot:  %s\nwant: %s", s.cmd, i+1, err, line, s.want[i])
			}
			if !strings.HasPrefix(line, `{"ok":`) {
				continue
			}
			r := &testrun.Result{Args: []string{s.cmd}, Stdout: line + "\n"}
			dec := json.NewDecoder(strings.NewReader(line))
			dec.UseNumber()
			if err := dec.Decode(&r.Envelope); err != nil {
				t.Errorf("%s: line %d: %v", s.cmd, i+1, err)
				continue
			}
			if len(names) == len(lines) {
				r.Check(t, names[i])
			} else if err := testrun.Load(t).Validate("envelope", "", line); err != nil {
				t.Errorf("%s: line %d fails the envelope schema: %v", s.cmd, i+1, err)
			}
		}
	}
}

// exampleEnv is the environment for the examples' shell: HOME the scratch
// directory, and no PATH or PWD, which the caller sets.
func exampleEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k != "HOME" && k != "PATH" && k != "PWD" {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+home)
}

var (
	shBlock  = regexp.MustCompile("(?s)```sh\n(.*?)```")
	heredoc  = regexp.MustCompile(`<<-?'?(\w+)'?`)
	trailing = regexp.MustCompile(`\s{2,}# .*$`)
)

// exampleCommands is every command in a doc's sh blocks, in order: each
// line, with its heredoc when it has one, and without its trailing
// comment, which the docs set off with two spaces or more. Blocks marked
// shell instead of sh aren't examples to run.
func exampleCommands(text string) []string {
	var cmds []string
	for _, m := range shBlock.FindAllStringSubmatch(text, -1) {
		lines := strings.Split(strings.TrimSuffix(m[1], "\n"), "\n")
		for i := 0; i < len(lines); i++ {
			line := trailing.ReplaceAllString(lines[i], "")
			if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if h := heredoc.FindStringSubmatch(line); h != nil {
				body := []string{line}
				for i++; i < len(lines) && lines[i] != h[1]; i++ {
					body = append(body, lines[i])
				}
				line = strings.Join(append(body, h[1]), "\n")
			}
			cmds = append(cmds, line)
		}
	}
	return cmds
}

// exampleCommandNames is the koan command each part of a command line runs,
// such as "task create", when the line is koan commands joined by && with
// nothing piped; otherwise nil.
func exampleCommandNames(cmd string) []string {
	first, _, _ := strings.Cut(cmd, "\n")
	if strings.Contains(first, "|") {
		return nil
	}
	var names []string
	for _, part := range strings.Split(first, " && ") {
		words := strings.Fields(part)
		if len(words) < 2 || words[0] != "koan" {
			continue
		}
		name := words[1]
		if (name == "task" || name == "folder" || name == "marker") && len(words) > 2 {
			name += " " + words[2]
		}
		names = append(names, name)
	}
	return names
}

// holds reports whether a line of output holds want: when want is a JSON
// object, the line must be one with every field want has, at every depth,
// arrays of the same length; otherwise the line must be want exactly.
func holds(line, want string) error {
	if !strings.HasPrefix(want, "{") {
		if line != want {
			return fmt.Errorf("text differs")
		}
		return nil
	}
	var g, w any
	if err := decodeNumbers(line, &g); err != nil {
		return fmt.Errorf("not JSON: %v", err)
	}
	if err := decodeNumbers(want, &w); err != nil {
		return fmt.Errorf("want is not JSON: %v", err)
	}
	return subset(g, w, "")
}

func decodeNumbers(s string, v any) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	return dec.Decode(v)
}

func subset(got, want any, at string) error {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: not an object", at)
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return fmt.Errorf("%s/%s: missing", at, k)
			}
			if err := subset(gv, wv, at+"/"+k); err != nil {
				return err
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Errorf("%s: want an array of %d", at, len(w))
		}
		for i := range w {
			if err := subset(g[i], w[i], fmt.Sprintf("%s/%d", at, i)); err != nil {
				return err
			}
		}
	default:
		if got != want {
			return fmt.Errorf("%s: %v, want %v", at, got, want)
		}
	}
	return nil
}
