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
	{cmd: "koan version", want: []string{`{"ok":true,"result":{"format":2},"warnings":[]}`}},

	// The model
	{cmd: `koan batch -i - <<'EOF'
{"folders": [{"path": "/work/api", "title": "API rework", "notes": "Move the API to v2.\n"}],
 "folder": "/work/api",
 "join": ["/work/api"],
 "tasks": [
  {"label": "$design", "title": "Design the endpoints", "priority": 2},
  {"label": "$build", "title": "Build them", "blocked_by": ["$design"]},
  {"title": "Announce the new API", "folder": "/work", "blocked_by": ["/work/api"], "join": []}]}
EOF`, want: []string{`{"ok":true,"result":{"folders_made":[{"id":40,"path":"/work"},{"id":41,"path":"/work/api"}],"tasks_made":[42,43,44],
		"labels":{"$build":43,"$design":42},"gates_made":[{"id":45,"gate":"start"},{"id":46,"gate":"done"}],"reblocked":[]},"warnings":[]}`}},

	// The daily loop
	{cmd: "koan frontier --limit 10 --fields ref,title", want: []string{`{"ok":true,"result":{"tasks":[{"id":45,"ref":"@45 (/work/api.start)","title":"Start"}],"total":1,"truncated":false}}`}},
	{cmd: "koan why 43", want: []string{`{"ok":true,"result":{"id":43,"readiness":"blocked","ready":[{"id":45,"claimed":false}]}}`}},
	{cmd: "koan complete 45", want: []string{`{"ok":true,"result":{"completed":[45],"unclaimed":[],"released":[42]}}`}},
	{cmd: `koan claim-next /work --extra '{"by":"agent-1"}'`, want: []string{`{"ok":true,"result":{"task":{"id":42,"claim":{"task":42,"extra":{"by":"agent-1"}}}}}`}},
	{cmd: "koan show 42", want: []string{`{"ok":true,"result":{"task":{"id":42,"title":"Design the endpoints","notes_path":"~/koan-root/tree/@work/@api/@42.md"}},"warnings":[]}`}},
	{cmd: "koan complete 42", want: []string{`{"ok":true,"result":{"completed":[42],"unclaimed":[42],"released":[43]}}`}},

	// Claims
	{cmd: `koan claim 43 --extra '{"by":"agent-1"}'`, want: []string{`{"ok":true,"result":{"claimed":[43]}}`}},
	{cmd: "koan claim 43", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"claimed","ids":[43]}}`}},
	{cmd: "koan task list --claimed --limit 20 --fields title,claim", want: []string{`{"ok":true,"result":{"tasks":[{"id":43,"title":"Build them","claim":{"task":43,"extra":{"by":"agent-1"}}}],"total":1}}`}},
	{cmd: "koan unclaim 43", want: []string{`{"ok":true,"result":{"unclaimed":[43]}}`}},

	// Writing
	{cmd: "koan task create 'Write the migration' --folder /work/api --priority 1 --tags db --blocked-by 42 --blocks /work/api",
		want: []string{`{"ok":true,"result":{"id":47,"ref":"@47 (/work/api)","gates_made":[],"reblocked":[]},"warnings":[]}`}},
	{cmd: `koan task create 'Investigate the flaky test' --notes-file - <<'EOF'
Seen in CI on 2026-10-19; fails about 1 run in 20.
EOF`, want: []string{`{"ok":true,"result":{"id":48,"ref":"@48 (/)","notes_path":"~/koan-root/tree/@48.md"}}`}},
	{cmd: `koan update 48 --priority null --tags-add urgent --extra-merge '{"status":"waiting"}'`, want: []string{`{"ok":true,"result":{"updated":[48]}}`}},
	{cmd: "koan block 44 --blocked-by 48", want: []string{`{"ok":true,"result":{"id":44,"changed":true}}`}},
	{cmd: "koan unblock 44 --blocked-by 48", want: []string{`{"ok":true,"result":{"id":44,"changed":true}}`}},
	{cmd: "koan block 42 --blocked-by 43", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"cycle","ids":[42,43]}}`}},

	// Folders
	{cmd: "koan folder create /work/web/client --title 'Web client'", want: []string{`{"ok":true,"result":{"id":50,"folders_made":[{"id":49,"path":"/work/web"},{"id":50,"path":"/work/web/client"}]}}`}},
	{cmd: `koan update /work/web/client --extra-merge '{"jira":"WEB-12"}'`, want: []string{`{"ok":true,"result":{"updated":[50]}}`}},
	{cmd: "koan show /work/api", want: []string{`{"ok":true,"result":{"folder":{"id":41,"gates":{"start":{"id":45,"state":"complete"},"done":{"id":46,"state":"open"}},
		"finished":false,"notes_path":"~/koan-root/tree/@work/@api/@notes.md"}}}`}},
	{cmd: "koan task create 'Port the client' --folder /work/web/client --blocked-by /work/web/client.start --blocks /work/web/client",
		want: []string{`{"ok":true,"result":{"id":51,"gates_made":[{"id":52,"gate":"start"},{"id":53,"gate":"done"}]}}`}},
	{cmd: "koan block /work/web/client.start --blocked-by /work/api", want: []string{`{"ok":true,"result":{"id":52,"changed":true,"gates_made":[],"reblocked":[52]}}`}},

	// References
	{cmd: "koan where @41:docs/plan.md", want: []string{`{"ok":true,"result":{"ref":"@41:docs/plan.md (/work/api:docs/plan.md)","kind":"material","id":41,"file":"~/koan-root/tree/@work/@api/docs/plan.md"}}`}},
	{cmd: "koan where /work/api.done", want: []string{`{"ok":true,"result":{"ref":"@46 (/work/api.done)","kind":"task","id":46,"gate":"done","file":"~/koan-root/tree/@work/@api/@done.md"}}`}},

	// Keep output small
	{cmd: "koan task list --limit 20 --fields ref,title,readiness", want: []string{`{"ok":true,"result":{"tasks":[{"id":48},{"id":44},{"id":43},{"id":46},{"id":47},{"id":51},{"id":52},{"id":53}],"total":8,"truncated":false}}`}},
	{cmd: "koan task list --state complete --limit 1 --fields id", want: []string{`{"ok":true,"result":{"tasks":[{"id":42}],"total":2,"truncated":true}}`}},
	{cmd: "koan folder list /work --direct --fields path,title,open,finished", want: []string{`{"ok":true,"result":{"folders":[{"id":41,"path":"/work/api"},{"id":49,"path":"/work/web"}],"total":2}}`}},
	{cmd: `koan task list --fields title,extra --limit 200 | jq -c 'if .ok then .result |= {tasks: (.tasks | map(select(.extra.status == "waiting"))), unscanned: (.total - (.tasks | length))} else . end'`,
		want: []string{`{"ok":true,"result":{"tasks":[{"id":48,"title":"Investigate the flaky test","extra":{"status":"waiting"}}],"unscanned":0},"warnings":[]}`}},
	{cmd: "koan export /work/api > api.json"},
	{cmd: "jq .ok api.json", want: []string{"true"}},

	// Catching up: the log
	{cmd: "koan log list --after 29 --limit 50", want: []string{`{"ok":true,"result":{"events":[{"seq":30,"kind":"created","id":49},{},{},{},{},{},{},{},{},{},{},{"seq":41,"kind":"reblocked","id":52}],"total":12,"truncated":false,"last_seq":41},"warnings":[]}`}},
	{cmd: "koan log list /work/api --since 2026-10-20T00:00:00Z --kinds completed,reopened", want: []string{`{"ok":true,"result":{"events":[{"kind":"completed","id":45,"folder":{"id":41,"path":"/work/api"}},{"kind":"completed","id":42,"folder":{"id":41,"path":"/work/api"}}],"total":2,"truncated":false}}`}},
	{cmd: "koan log list --task 42 | jq -c '[.result.events[].kind]'", want: []string{`["created","blocked","released","claimed","completed","unclaimed"]`}},

	// Worktrees
	{cmd: "koan bind @41 ~/code/api-v2", want: []string{`{"ok":true,"result":{"binding":{"id":41,"path":"/work/api"},"replaced":false}}`}},
	{cmd: "cd ~/code/api-v2 && koan context --here", want: []string{`{"ok":true,"result":{"folder":{"id":41},"ancestors":[{"id":40}],"notes":"Move the API to v2.\n",
		"tasks":[{"id":43},{"id":46},{"id":47}],"marker":"~/code/api-v2/.koan/marker.json"}}`}},
	{cmd: "koan where --file ~/code/api-v2/src/main.go", want: []string{`{"ok":true,"result":{"ref":"@41 (/work/api)","kind":"folder","marker":"~/code/api-v2/.koan/marker.json"},"warnings":[]}`}},
	{cmd: "koan mv -T /work/api /work/api2", want: []string{`{"ok":true,"result":{"moved":[{"id":41,"ref":"@41 (/work/api2)"}]}}`}},
	{cmd: "koan where --file ~/code/api-v2", want: []string{`{"ok":true,"result":{"ref":"@41 (/work/api2)"},"warnings":[{"kind":"stale-marker","ids":[41]}]}`}},
	{cmd: "koan bind @41 ~/code/api-v2", want: []string{`{"ok":true,"result":{"binding":{"id":41,"path":"/work/api2"},"replaced":true}}`}},
	{cmd: "koan mv -T /work/api2 /work/api", want: []string{`{"ok":true,"result":{"moved":[{"id":41,"ref":"@41 (/work/api)"}]}}`}},

	// Moving, deleting and archiving
	{cmd: "koan mv -t /work/api 48", want: []string{`{"ok":true,"result":{"moved":[{"id":48,"notes_path":"~/koan-root/tree/@work/@api/@48.md"}]}}`}},
	{cmd: "koan mv -T /work/web/client /work/client", want: []string{`{"ok":true,"result":{"moved":[{"id":50,"ref":"@50 (/work/client)"}]}}`}},
	{cmd: "koan task list /work/client --state open,complete --fields title,state,claim", want: []string{`{"ok":true,"result":{"tasks":[{"id":51},{"id":52},{"id":53}],"total":3}}`}},
	{cmd: "koan delete /work/client", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"not-empty","ids":[51]}}`}},
	{cmd: "koan delete /work/client --contents", want: []string{`{"ok":true,"result":{"deleted":[50],"removed":[50,51,52,53],"unclaimed":[],"released":[]}}`}},
	{cmd: "koan archive /work/api", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"not-finished","ids":[43,46,47,48]}}`}},
	{cmd: "koan update 48 --tags-add cancelled && koan complete 48 && koan archive 48", want: []string{
		`{"ok":true,"result":{"updated":[48]}}`,
		`{"ok":true,"result":{"completed":[48],"unclaimed":[],"released":[]}}`,
		`{"ok":true,"result":{"archived":[{"id":48,"notes_path":"~/koan-root/archived/work/api/@48.md"}],"removed":[48]}}`}},
	{cmd: "koan show 48", exit: 1, want: []string{`{"ok":false,"error":{"kind":"not-found","ids":[48],"archived":[{"id":48,"notes_path":"~/koan-root/archived/work/api/@48.md"}]}}`}},

	// When the tree is damaged
	{cmd: "koan doctor", want: []string{`{"ok":true,"result":{"findings":[]},"warnings":[]}`}},
	{cmd: "koan repair", want: []string{`{"ok":true,"result":{"fixed":[],"left":[]},"warnings":[]}`}},
	{cmd: "koan backup ~/backups/koan-$(date +%F-%H%M%S)", want: []string{`{"ok":true,"result":{"recopied":false,"clean":true,"findings":[]},"warnings":[]}`}},
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
	{cmd: "koan init", want: []string{`{"ok":true,"result":{"made":["root","tree","archived","log","database"],"first_id":1},"warnings":[]}`}},

	// A taste
	{cmd: "koan folder create /work/api --title 'API rework'", want: []string{`{"ok":true,"result":{"id":2,"folders_made":[{"id":1,"path":"/work"},{"id":2,"path":"/work/api"}]},"warnings":[]}`}},
	{cmd: "koan task create 'Design the endpoints' --folder /work/api --priority 2", want: []string{`{"ok":true,"result":{"id":3}}`}},
	{cmd: "koan task create 'Build them' --folder /work/api --blocked-by 3", want: []string{`{"ok":true,"result":{"id":4,"reblocked":[]}}`}},
	{cmd: "koan frontier --fields ref,title", want: []string{`{"ok":true,"result":{"tasks":[{"id":3,"ref":"@3 (/work/api)","title":"Design the endpoints"}],"total":1}}`}},
	{cmd: `koan claim-next --extra '{"by":"me"}'`, want: []string{`{"ok":true,"result":{"task":{"id":3,"claim":{"task":3,"extra":{"by":"me"}}}}}`}},
	{cmd: "koan complete 3", want: []string{`{"ok":true,"result":{"completed":[3],"unclaimed":[3],"released":[4]},"warnings":[]}`}},

	// Backup and repair
	{cmd: "koan backup ~/backups/koan-$(date +%F-%H%M%S)", want: []string{`{"ok":true,"result":{"clean":true,"findings":[]},"warnings":[]}`}},
	{cmd: "mv ~/koan-root ~/koan-root.before-restore"},
	{cmd: `cp -R "$(ls -d ~/backups/koan-* | tail -n 1)" ~/koan-root`},
	{cmd: "koan doctor", want: []string{`{"ok":true,"result":{"findings":[]},"warnings":[]}`}},
}

// TestReadme runs the README's sh examples as TestSkill runs the skill's.
func TestReadme(t *testing.T) {
	runExamples(t, "README.md", nil, readmeSteps)
}

// tutorialSteps are the tutorial's sh examples, in order, on a new root:
// the scratch root it opens with, made by koan init.
var tutorialSteps = []docStep{
	// 1. A folder and two tasks
	{cmd: "koan folder create /work/api --title 'API rework'", want: []string{`{"ok":true,"result":{"id":2,"ref":"@2 (/work/api)","folders_made":[{"id":1,"path":"/work"},{"id":2,"path":"/work/api"}]},"warnings":[]}`}},
	{cmd: "koan task create 'Design the endpoints' --folder /work/api --priority 2", want: []string{`{"ok":true,"result":{"id":3,"ref":"@3 (/work/api)"}}`}},
	{cmd: "koan task create 'Build them' --folder /work/api --blocked-by 3", want: []string{`{"ok":true,"result":{"id":4,"ref":"@4 (/work/api)"}}`}},

	// 2. What can be done now
	{cmd: "koan frontier --fields ref,title", want: []string{`{"ok":true,"result":{"tasks":[{"id":3,"ref":"@3 (/work/api)","title":"Design the endpoints"}],"total":1,"truncated":false},"warnings":[]}`}},
	{cmd: "koan why 4", want: []string{`{"ok":true,"result":{"id":4,"readiness":"blocked","blockers":[{"id":3,"ref":"@3 (/work/api)","state":"open"}],"ready":[{"id":3,"ref":"@3 (/work/api)","claimed":false}]},"warnings":[]}`}},

	// 3. Taking and finishing work
	{cmd: `koan claim-next --extra '{"by":"me"}'`, want: []string{`{"ok":true,"result":{"task":{"id":3,"claim":{"task":3,"extra":{"by":"me"}}}}}`}},
	{cmd: "koan complete 3", want: []string{`{"ok":true,"result":{"completed":[3],"unclaimed":[3],"released":[4]},"warnings":[]}`}},

	// 4. Notes and material
	{cmd: "koan show 4", want: []string{`{"ok":true,"result":{"task":{"id":4,"readiness":"ready","notes_path":"~/koan-root/tree/@work/@api/@4.md"}},"warnings":[]}`}},
	{cmd: "echo 'Move the API to v2.' > ~/koan-root/tree/@work/@api/@notes.md"},
	{cmd: "echo 'Endpoints: /v2/users, /v2/orders' > ~/koan-root/tree/@work/@api/spec.md"},
	{cmd: "koan where @2:spec.md", want: []string{`{"ok":true,"result":{"ref":"@2:spec.md (/work/api:spec.md)","kind":"material","id":2,"file":"~/koan-root/tree/@work/@api/spec.md"},"warnings":[]}`}},

	// 5. How commands answer
	{cmd: "koan update 4 --priority 1", want: []string{`{"ok":true,"result":{"updated":[4]},"warnings":[]}`}},
	{cmd: "koan block 3 --blocked-by 4", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"cycle","message":"@3 would wait on itself through @4","ids":[3,4],"paths":[],
		"problems":[{"field":"/blocked_by/0","reason":"@3 would wait on itself through @4"}],"archived":[]},"warnings":[]}`}},

	// 6. Ordering folders with gates
	{cmd: "koan block /work/api.done --blocked-by 3,4", want: []string{`{"ok":true,"result":{"id":5,"changed":true,"gates_made":[{"id":5,"folder":{"id":2},"gate":"done"}],"reblocked":[]}}`}},
	{cmd: "koan task create 'Announce it' --folder /work --blocked-by /work/api", want: []string{`{"ok":true,"result":{"id":6,"gates_made":[]}}`}},
	{cmd: "koan why 6", want: []string{`{"ok":true,"result":{"id":6,"readiness":"blocked","blockers":[{"id":5,"ref":"@5 (/work/api.done)","state":"open"}],"ready":[{"id":4,"ref":"@4 (/work/api)","claimed":false}]},"warnings":[]}`}},
	{cmd: "koan export /work/api | jq '.result.external[].ref'", want: []string{`"@6 (/work)"`}},

	// 7. A whole plan in one call
	{cmd: `koan batch -i - <<'EOF'
{"folders": [{"path": "/home/trip", "title": "Lisbon in May"}],
 "folder": "/home/trip",
 "join": ["/home/trip"],
 "tasks": [{"label": "$flights", "title": "Book flights"},
           {"title": "Book a hotel", "blocked_by": ["$flights"]}]}
EOF`, want: []string{`{"ok":true,"result":{"folders_made":[{"id":7,"path":"/home"},{"id":8,"path":"/home/trip"}],"tasks_made":[9,10],"labels":{"$flights":9},
		"gates_made":[{"id":11,"folder":{"id":8},"gate":"start"},{"id":12,"folder":{"id":8},"gate":"done"}],"reblocked":[]},"warnings":[]}`}},
	{cmd: "koan frontier /home --fields ref,title", want: []string{`{"ok":true,"result":{"tasks":[{"id":11,"ref":"@11 (/home/trip.start)","title":"Start"}],"total":1,"truncated":false},"warnings":[]}`}},

	// 8. Working from a worktree
	{cmd: "git init -q ~/code/api"},
	{cmd: "git -C ~/code/api -c user.name=you -c user.email=you@example.com commit -q --allow-empty -m start"},
	{cmd: "git -C ~/code/api worktree add -q ~/code/api-v2"},
	{cmd: "koan bind /work/api ~/code/api-v2", want: []string{`{"ok":true,"result":{"marker":"~/code/api-v2/.koan/marker.json","binding":{"id":2,"path":"/work/api"},"replaced":false}}`}},
	{cmd: "cd ~/code/api-v2 && koan context --here", want: []string{`{"ok":true,"result":{"folder":{"id":2},"ancestors":[{"id":1}],"notes":"Move the API to v2.\n",
		"tasks":[{"id":4},{"id":5,"gate":"done"}],"marker":"~/code/api-v2/.koan/marker.json"}}`}},
	{cmd: "koan where --file ~/code/api-v2/src/main.go", want: []string{`{"ok":true,"result":{"ref":"@2 (/work/api)","kind":"folder"}}`}},

	// 9. Several agents
	{cmd: `koan claim 4 --extra '{"by":"agent-1"}'`, want: []string{`{"ok":true,"result":{"claimed":[4]}}`}},
	{cmd: "koan claim 4", exit: 1, want: []string{`{"ok":false,"error":{"kind":"conflict","rule":"claimed","ids":[4]}}`}},
	{cmd: "koan task list --claimed --fields title,claim", want: []string{`{"ok":true,"result":{"tasks":[{"id":4,"title":"Build them","claim":{"task":4,"extra":{"by":"agent-1"}}}],"total":1}}`}},
	{cmd: "koan unclaim 4", want: []string{`{"ok":true,"result":{"unclaimed":[4]}}`}},

	// 10. History
	{cmd: "koan log list --task 3 | jq -c '[.result.events[].kind]'", want: []string{`["created","claimed","completed","unclaimed"]`}},
	{cmd: "koan log list /work/api --since 2026-10-01T00:00:00Z --kinds completed", want: []string{`{"ok":true,"result":{"events":[{"kind":"completed","id":3,"folder":{"id":2,"path":"/work/api"}}],"total":1,"truncated":false},"warnings":[]}`}},
	{cmd: "koan log rotate --before 2026-10-01T00:00:00Z", want: []string{`{"ok":true,"result":{"changed":false,"count":0},"warnings":[]}`}},

	// 11. Finishing and archiving
	{cmd: "koan complete 4", want: []string{`{"ok":true,"result":{"completed":[4],"released":[5]}}`}},
	{cmd: "koan complete /work/api.done", want: []string{`{"ok":true,"result":{"completed":[5],"released":[6]}}`}},
	{cmd: "koan archive /work/api", want: []string{`{"ok":true,"result":{"archived":[{"id":2,"dir":"~/koan-root/archived/work/@2_api"}],"removed":[2,3,4,5]},"warnings":[]}`}},
	{cmd: "koan show 3", exit: 1, want: []string{`{"ok":false,"error":{"kind":"not-found","ids":[3],"archived":[{"id":3,"notes_path":"~/koan-root/archived/work/@2_api/@3.md"}]}}`}},

	// 12. Backup and restore
	{cmd: "koan backup ~/backups/koan-$(date +%F-%H%M%S)", want: []string{`{"ok":true,"result":{"clean":true,"findings":[]},"warnings":[]}`}},
	{cmd: "mv ~/koan-root ~/koan-root.before-restore"},
	{cmd: `cp -R "$(ls -d ~/backups/koan-* | tail -n 1)" ~/koan-root`},
	{cmd: "koan doctor", want: []string{`{"ok":true,"result":{"findings":[]},"warnings":[]}`}},

	// 13. When the tree is damaged
	{cmd: "mv ~/koan-root/tree/@home/@trip ~/koan-root/tree/@home/@lisbon"},
	{cmd: "koan doctor", want: []string{`{"ok":true,"result":{"findings":[{"kind":"stray-directory","ids":[8],"paths":["~/koan-root/tree/@home/@lisbon"],"repairable":true},{"kind":"missing-directory","ids":[8]}]}}`}},
	{cmd: "koan repair", want: []string{`{"ok":true,"result":{"fixed":[{"kind":"stray-directory"},{"kind":"missing-directory"}],"left":[]},"warnings":[]}`}},
}

// TestTutorial runs the tutorial's sh examples as TestSkill runs the skill's.
func TestTutorial(t *testing.T) {
	runExamples(t, filepath.Join("docs", "tutorial.md"), []string{"koan init"}, tutorialSteps)
}

// TestReadmeRules checks that the README's table of permission rules lists
// what scripts/install.sh adds, in its order, so the two can't drift.
func TestReadmeRules(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(testrun.Repo(), name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	script, readme := read(filepath.Join("scripts", "install.sh")), read("README.md")

	// The script's rules: a JSON list each.
	list := func(name string) []string {
		t.Helper()
		m := regexp.MustCompile(`(?ms)^` + name + `='(.*?)'$`).FindStringSubmatch(script)
		if m == nil {
			t.Fatalf("scripts/install.sh sets no %s", name)
		}
		var rules []string
		if err := json.Unmarshal([]byte(m[1]), &rules); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return rules
	}

	// The README's row: File | Allow | Ask, each rule in backticks.
	m := regexp.MustCompile("(?m)^\\| `~/.claude/settings.json`[^|]*\\|(.*)\\|$").FindStringSubmatch(readme)
	if m == nil {
		t.Fatal("the README's rules table has no row for ~/.claude/settings.json")
	}
	cells := strings.Split(m[1], "|")
	if len(cells) != 2 {
		t.Fatalf("the README's rules row has %d cells, want 3", len(cells)+1)
	}
	rules := func(cell string) []string {
		var out []string
		for _, r := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(cell, -1) {
			out = append(out, r[1])
		}
		return out
	}
	if allow, want := rules(cells[0]), list("claude_allow"); !slices.Equal(allow, want) {
		t.Errorf("allow: the README has %q, scripts/install.sh %q", allow, want)
	}
	if ask, want := rules(cells[1]), list("claude_ask"); !slices.Equal(ask, want) {
		t.Errorf("ask: the README has %q, scripts/install.sh %q", ask, want)
	}
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
	cmds, shown := exampleCommands(string(text))
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

	for n, s := range steps {
		out, exit := sh(s.cmd)
		var lines []string
		if out != "" {
			lines = strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		}
		if exit != s.exit || len(lines) != len(s.want) {
			t.Errorf("%s:\nexit %d, want %d; %d lines, want %d:\n%s", s.cmd, exit, s.exit, len(lines), len(s.want), out)
			continue
		}
		// What the doc prints for the command must be its output exactly.
		if printed, ok := shown[n]; ok {
			if got := strings.ReplaceAll(strings.TrimSuffix(out, "\n"), home, "~"); got != printed {
				t.Errorf("%s: the output %s prints differs from the command's:\ngot:     %s\nprinted: %s", s.cmd, doc, got, printed)
			}
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
	exampleBlock = regexp.MustCompile("(?s)```(sh|json)\n(.*?)```")
	heredoc      = regexp.MustCompile(`<<-?'?(\w+)'?`)
	trailing     = regexp.MustCompile(`\s{2,}# .*$`)
)

// exampleCommands is every command in a doc's sh blocks, in order: each
// line, with its heredoc when it has one, and without its trailing
// comment, which the docs set off with two spaces or more. Blocks marked
// shell instead of sh aren't examples to run. shown maps a command's index
// to the output a json block prints for it: the last command of the sh
// block before it.
func exampleCommands(text string) (cmds []string, shown map[int]string) {
	shown = map[int]string{}
	for _, m := range exampleBlock.FindAllStringSubmatch(text, -1) {
		if m[1] == "json" {
			if len(cmds) > 0 {
				shown[len(cmds)-1] = strings.TrimSuffix(m[2], "\n")
			}
			continue
		}
		lines := strings.Split(strings.TrimSuffix(m[2], "\n"), "\n")
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
	return cmds, shown
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
		if (name == "task" || name == "folder" || name == "log") && len(words) > 2 {
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
