# koan

[![CI](https://github.com/phansen314/koan/actions/workflows/ci.yml/badge.svg)](https://github.com/phansen314/koan/actions/workflows/ci.yml)

**Your agent already runs commands and reads files. Why is your task list trapped in someone else's app?**

koan keeps your tasks and their dependencies in a database your agent drives with commands, and mirrors every folder of work as a real directory. Open it in your editor: each task's Markdown notes sit next to your material — the specs, drafts and research the work is about. Your code stays in its own repo; bind a worktree to a folder, and `koan context --here` briefs your agent from inside it with that folder's notes and open tasks.

- **Your agent drives it.** Every command prints one line of JSON, so Claude Code and OpenCode use it natively. Ask "what should I work on next?" and koan hands back the ready tasks, highest priority first, or have your agent lay out a whole plan, dependencies included, in one call.
- **Safe with several agents.** A claim reserves a task, so two agents are never handed the same one.
- **Yours to drive too.** `koan pick` opens an fzf picker for choosing tasks and folders by hand.

One `go install`, no server, and your agent has a memory for the work ahead.

Linux and macOS only.

**Pre-1.0:** until 1.0, the JSON output may change in a minor release, and its release notes say what changed. A release that changes the database's format needs a fresh root, with no conversion: `koan info` then reports `unsupported-format`, its `format` below `koan version`'s. Back the old root up, move it aside and `koan init`.

## Quick start

Needs Go 1.25 or later. The SQLite driver is SQLite translated to Go, so there is no cgo and no C compiler, just one binary.

**1. Install koan:**

```shell
GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@v0.13.1   # any GOBIN on your PATH
```

**2. Make this machine's root** (to keep it somewhere else, see [The root](#the-root) first):

```sh
koan init
```

**3. Give your agent the koan skill.** In Claude Code, from the plugin marketplace this repo is:

```shell
claude plugin marketplace add phansen314/koan
claude plugin install koan@koan
```

**4. Decide what your agent may run without asking.** Unless your settings already allow them, your agent asks before running each koan command. The repo's suggested rules let every `koan` and `jq` command run without asking, except `koan init`, `koan delete` and `koan repair`, which always ask. [Use it from Claude Code and OpenCode](#use-it-from-claude-code-and-opencode) lists them for each agent. Apply them with the repo's script, which also links the skill for OpenCode, or add your own by hand:

```shell
git clone https://github.com/phansen314/koan && cd koan
scripts/install.sh               # every agent whose CLI is on PATH; --claude or --opencode for one
```

That section also says what the script changes and how to undo it.

Then ask your agent to plan some work, or "what should I work on next?". To try the commands yourself, see [A taste](#a-taste). For shell completion and the `koan pick` picker on a key, see [docs/shell.md](docs/shell.md).

## The root

Everything is under `~/koan-root`:

| Path | Holds |
|---|---|
| `koan.db` | the database: tasks, folders, the edges between tasks, claims, and the log of every change. Only koan writes it |
| `tree/` | a directory per folder, named `@<name>`, holding its notes `@notes.md`, each task's notes `@<id>.md`, and anything else you keep there (your *material*, which koan never reads) |
| `archived/` | finished work that was archived, as files |
| `log/` | old events of the log, moved out of the database by `koan log rotate`, as JSON Lines files |

Open `~/koan-root/tree` in your editor and edit any notes file. Rename or move folders with koan, not by hand: `koan doctor` finds a hand rename and `koan repair` puts it back.

To keep the root somewhere else, make `~/koan-root` a symlink to it before `koan init`. It must be on a local filesystem, never a sync folder: SQLite's WAL mode and koan's lock need one.

## Coming from the original koan

There is no migration: this koan starts from a fresh root. `koan init` refuses a `~/koan-root` left by the original koan (versions up to v0.3.0), with `conflict`, rule `not-a-root`, listing what it found that isn't its own, such as `@koan.json`. Move the old root aside, and start a new one:

```shell
mv ~/koan-root ~/koan-root-v0.3   # the old tree stays readable: its notes are Markdown files
koan init
```

Then recreate the work you still want, by hand or with `koan batch`. The original's claims file, `~/.config/koan/state.json`, is read by nothing now and can be deleted. Install this koan over the old binary with the `go install` above, update the Claude Code plugin with `claude plugin marketplace update koan` and `claude plugin update koan@koan`, and rerun `scripts/install.sh`, which also removes the old `koan migrate` permission rule.

## A taste

Folders and tasks share one ID sequence. On a new root:

```sh
koan folder create /work/api --title 'API rework'                        # makes /work too: @1, and /work/api @2
koan task create 'Design the endpoints' --folder /work/api --priority 2  # @3
koan task create 'Build them' --folder /work/api --blocked-by 3          # @4, which waits on @3
koan frontier --limit 10 --fields ref,title                              # what can be taken now: only @3
koan claim-next --extra '{"by":"me"}'                                    # claims @3 and returns it, so no one else takes it
koan complete 3                                                          # .result.released: [4], now ready
koan show 4                                                              # the whole task, with its notes_path
koan log list --task 3 | jq -c '[.result.events[].kind]'                 # its history: ["created","claimed","completed","unclaimed"]
```

`task` and `folder` hold only `create` and `list`; every other command is its verb alone, for tasks and folders both (`koan show 4`, `koan update 4 --priority 1`, `koan mv 4 /work`). A list or an export with no folder looks at the whole tree; in a worktree, `--here` looks under its folder, as in `koan context --here`. A write returns what it changed, by ID; a read returns whole records. Errors carry a `kind`, and a refusal its `rule`, such as `cycle`; a `usage` error names the sibling option you probably meant.

### Ordering folders

Edges are only ever between tasks: where a task sits never orders it. To order whole folders, a folder has two **gates**, tasks of its own made the first time an edge names them: `/work/api.start`, which its work waits on, and `/work/api.done`, which waits on its work and which what comes after waits on. In an edge, a folder means its done gate.

```sh
koan task create 'Announce it' --folder /work --blocked-by /work/api    # @5 waits on /work/api's done gate, made as @6
koan block /work/api.done --blocked-by 3,4                              # the done gate waits on the folder's work
koan why 5                                                              # .result.ready: @4, what to finish to move @5
koan export /work/api | jq '.result.external[].ref'                     # the tasks outside /work/api at the other end of an edge: @5
```

A plan with several tasks goes in at once, as one change, with labels for its tasks to name each other by; here every task joins the folder through the batch's `join`:

```sh
koan batch -i - <<'EOF'
{"folders": [{"path": "/home/trip", "title": "Lisbon in May"}],
 "folder": "/home/trip",
 "join": ["/home/trip"],
 "tasks": [{"label": "$flights", "title": "Book flights"},
           {"title": "Book a hotel", "blocked_by": ["$flights"]}]}
EOF
```

Several agents can work from one tree: a **claim** reserves a task, so two workers are never handed the same one. It never expires; `complete` or `unclaim` ends it, and says which claims it ended. Finished work leaves the tree with `koan archive`, for a task or a folder, into `~/koan-root/archived`, where `rg` finds it.

## Use it from Claude Code and OpenCode

The [koan skill](claude/skills/koan/SKILL.md) teaches the agent the commands. Claude Code gets it from the `koan` plugin, installed in the [Quick start](#quick-start).

What your agent may run without asking is your call. `scripts/install.sh`, from a clone of this repo, applies the suggested rules: every `koan` and `jq` command runs without asking, except `koan init`, which makes a root, `koan delete`, which removes work, and `koan repair`, which changes the tree on disk. Those always ask. To add them by hand instead:

| Agent | File | Allow | Ask |
|---|---|---|---|
| Claude Code | `~/.claude/settings.json`, under `permissions` | `Bash(koan:*)`, `Bash(jq:*)` | `Bash(koan init:*)`, `Bash(koan delete:*)`, `Bash(koan repair:*)` |
| OpenCode | `~/.config/opencode/opencode.json`, under `permission.bash`, in this order, since the last matching rule wins | `"koan *"`, `"jq *"` | `"koan init*"`, `"koan delete *"`, `"koan repair*"` |

For OpenCode the script also links the skill into `~/.config/opencode/skills/koan`.

```shell
scripts/install.sh               # every agent whose CLI is on PATH
scripts/install.sh --opencode    # or name them: --claude, --opencode
scripts/install.sh --uninstall   # take it all out again
```

The script is safe to rerun. It backs a settings file up, to `.bak.<timestamp>`, before changing it, and adds or removes only koan's rules. It never edits an `opencode.jsonc`, or an `opencode.json` that isn't plain JSON: it prints the rules to add by hand. It needs `jq`, and runs under macOS's `/bin/bash`.

## Backup and restore

`koan backup` copies the database, the tree, the archive and the log's rotated files into a new or empty directory, while koan keeps running, and checks the copy:

```sh
koan backup ~/backups/koan-$(date +%F)   # .result.clean is true when the copy checks out
```

Copying `koan.db` by hand while koan runs is not a backup. To restore, put a backup in place of `~/koan-root` and run `koan doctor`:

```sh
mv ~/koan-root ~/koan-root.before-restore
cp -R ~/backups/koan-$(date +%F) ~/koan-root   # or whichever backup you want
koan doctor                                    # .result.findings: what is wrong, if anything
```

A backup carries the claims and the log of its moment, and a restore brings them back with it. So the IDs and log `seq` numbers issued after the backup will be issued again, and `koan log list --after` a `seq` from after it is `not-found`, until the restored root has issued that many again. The restored root's `log/` is the backup's: don't copy the replaced root's rotated files into it, since the restored root will issue their `seq` again.

## History

Every change is recorded in the log, in the database: what was made, changed, moved, completed, claimed, deleted or archived, which tasks became ready, and which folders became finished. `koan log list` reads it back, oldest first, filtered by folder path or ID (matched as recorded, so a renamed or deleted folder still finds its history), `--task`, `--kinds`, and `--since` a time or `--after` a `seq`. To keep the table small, move old events out to files in `~/koan-root/log/`. `log list` reads only the table, so a full history since a point reads those files too; cut well in the past, such as a month back, so no agent's saved place falls behind the cut:

```sh
koan log list /work/api --since 2026-10-01T00:00:00Z --kinds completed   # what was finished under /work/api this month
koan log rotate --before 2026-10-01T00:00:00Z                            # move everything older out to ~/koan-root/log/
```

## Checking and repairing

A crash, or a folder renamed or dragged by hand in your editor, can leave the tree on disk out of step with the database. `koan doctor` finds it and changes nothing; `koan repair` fixes what it can without losing anything, and reports what it left for you. Every finding's message says what to do.

## Uninstalling

Remove the binary, `scripts/install.sh --uninstall`, `claude plugin uninstall koan@koan`, and `~/koan-root` if its work isn't wanted.

## Docs

- [docs/shell.md](docs/shell.md): shell completion, and the `koan pick` picker on a key.
- [docs/rules.md](docs/rules.md): the invariants and rules every tree obeys.
- [docs/prd.md](docs/prd.md): how koan is built: layout, storage, writes, concurrency, recovery.
- [docs/spec.md](docs/spec.md): exactly what every command takes and prints.
- [schemas/](schemas): the spec's JSON Schemas, one file each.
- [claude/skills/koan/SKILL.md](claude/skills/koan/SKILL.md): the skill agents use.

## Development

```shell
go test ./...                              # everything, the skill's and this README's examples included
go test -short ./...                       # without the long concurrency and crash matrices
python3 scripts/extract-schemas.py --check # schemas/ matches the spec
```

## License

[MIT](LICENSE)
