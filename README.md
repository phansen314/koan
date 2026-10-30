# koan

[![CI](https://github.com/phansen314/koan/actions/workflows/ci.yml/badge.svg)](https://github.com/phansen314/koan/actions/workflows/ci.yml)

koan is a durable task tree for you and your agents, with the notes, specs, drafts and research for each piece of work filed beside it.

**What should I work on next?**
Ask, and koan hands back the ready tasks, highest priority first: dependencies decide what's ready, and a claim makes sure two agents are never handed the same one. Every command prints one line of JSON, so Claude Code and OpenCode drive it natively, and your agent can lay out a whole plan, dependencies included, in one call.

**Where is your task list stored?**
On your machine, in `~/koan-root`, not on someone else's server behind an API and a token. Folders, tasks and the edges between them sit in one SQLite file, and every change is logged, so the plan outlasts every session, and an agent can catch up on what happened since it last looked.

**Where do the specs, drafts and research for the work live?**
In most tools, notes don't live on the filesystem, and the material is somewhere else again. In koan, every folder of work is a real directory you open in your editor: its notes and each task's are Markdown files, beside the material they're about, which koan can name (`@2:spec.md`) and archives with the work. Your code stays in its own repo; bind a worktree to a folder, and `koan context --here` briefs your agent from inside it with that folder's notes and open tasks.

One binary, no server, nothing to sign up for, and it works offline. `koan pick` lets you drive it by hand too.

Linux and macOS only. **Pre-1.0:** the JSON output and the database's format can change in a minor release; see [Upgrading](#upgrading).

## Quick start

Needs Go 1.25 or later; no cgo, no C compiler.

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

**4. Decide what your agent may run without asking.** Until your settings allow them, your agent asks before each koan command. The repo suggests rules, listed under [Use it from Claude Code and OpenCode](#use-it-from-claude-code-and-opencode), and a script that applies them and links the skill for OpenCode. Or add your own by hand.

```shell
git clone https://github.com/phansen314/koan && cd koan
scripts/install.sh               # every agent whose CLI is on PATH; --claude or --opencode for one
```

Then ask your agent to plan some work, or "what should I work on next?". To try the commands yourself, see [A taste](#a-taste) and the [tutorial](docs/tutorial.md). For shell completion and the `koan pick` picker on a key, see [docs/shell.md](docs/shell.md).

## A taste

On a new root:

```sh
koan folder create /work/api --title 'API rework'                        # makes /work too: @1, and /work/api @2
koan task create 'Design the endpoints' --folder /work/api --priority 2  # @3
koan task create 'Build them' --folder /work/api --blocked-by 3          # @4, which waits on @3
koan frontier --fields ref,title                                         # what can be done now: only @3
koan claim-next --extra '{"by":"me"}'                                    # claims @3, so no one else is handed it
koan complete 3
```

`complete` says what changed: `@3` is complete, its claim ended, and `@4` is now ready.

```json
{"ok":true,"result":{"completed":[3],"unclaimed":[3],"released":[4]},"warnings":[]}
```

The [tutorial](docs/tutorial.md) carries on from here: notes and material, ordering whole folders, laying out a plan in one call, working from a worktree, several agents, history, archiving, backup, and repair.

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

## Use it from Claude Code and OpenCode

The [koan skill](claude/skills/koan/SKILL.md) teaches the agent the commands. Claude Code gets it from the `koan` plugin, installed in the [Quick start](#quick-start).

What your agent may run without asking is your call. `scripts/install.sh`, from a clone of this repo, applies the suggested rules: every `koan` and `jq` command runs without asking, except `koan init`, which makes a root, `koan delete`, which removes work, and `koan repair`, which changes the tree on disk. Those always ask. To add them by hand instead:

| Agent | File | Allow | Ask |
|---|---|---|---|
| Claude Code | `~/.claude/settings.json`, under `permissions` | `Bash(koan:*)`, `Bash(jq:*)` | `Bash(koan init:*)`, `Bash(koan delete:*)`, `Bash(koan repair:*)` |
| OpenCode | `~/.config/opencode/opencode.json`, under `permission.bash` | `"koan *"`, `"jq *"` | `"koan init*"`, `"koan delete *"`, `"koan repair*"` |

In OpenCode the last matching rule wins, so the asks go after the allows. For OpenCode the script also links the skill into `~/.config/opencode/skills/koan`.

```shell
scripts/install.sh               # every agent whose CLI is on PATH
scripts/install.sh --opencode    # or name them: --claude, --opencode
scripts/install.sh --uninstall   # take it all out again
```

The script is safe to rerun. It backs a settings file up, to `.bak.<timestamp>`, before changing it, and adds or removes only koan's rules. It never edits an `opencode.jsonc`, or an `opencode.json` that isn't plain JSON: it prints the rules to add by hand. It needs `jq`, and runs under macOS's `/bin/bash`.

## Backup and repair

`koan backup` copies the database, the tree, the archive and the log's rotated files into a new or empty directory, while koan keeps running, and checks the copy. Copying `koan.db` by hand while koan runs is not a backup.

```sh
koan backup ~/backups/koan-$(date +%F)   # .result.clean is true when the copy checks out
```

To restore, put a backup in place of `~/koan-root` and run `koan doctor`. The [tutorial](docs/tutorial.md#12-backup-and-restore) says what a restore brings back with it.

```sh
mv ~/koan-root ~/koan-root.before-restore
cp -R ~/backups/koan-$(date +%F) ~/koan-root   # or whichever backup you want
koan doctor                                    # .result.findings: what is wrong, if anything
```

A crash, or a folder renamed or dragged by hand in your editor, can leave the tree on disk out of step with the database. `koan doctor` finds it and changes nothing; `koan repair` fixes what it can without losing anything, and reports what it left for you. Every finding's message says what to do.

## Upgrading

Until 1.0, the JSON output may change in a minor release, and its release notes say what changed. A release that changes the database's format needs a fresh root, with no conversion: `koan info` then reports `unsupported-format`, its `format` below `koan version`'s. Back the old root up, move it aside and `koan init`.

### Coming from the original koan

There is no migration: this koan starts from a fresh root. `koan init` refuses a `~/koan-root` left by the original koan (versions up to v0.3.0), with `conflict`, rule `not-a-root`, listing what it found that isn't its own, such as `@koan.json`. Move the old root aside, and start a new one:

```shell
mv ~/koan-root ~/koan-root-v0.3   # the old tree stays readable: its notes are Markdown files
koan init
```

Then recreate the work you still want, by hand or with `koan batch`. The original's claims file, `~/.config/koan/state.json`, is read by nothing now and can be deleted. Install this koan over the old binary with the `go install` above, update the Claude Code plugin with `claude plugin marketplace update koan` and `claude plugin update koan@koan`, and rerun `scripts/install.sh`, which also removes the old `koan migrate` permission rule.

## Uninstalling

Remove the binary, run `scripts/install.sh --uninstall` and `claude plugin uninstall koan@koan`, and delete `~/koan-root` if you don't want its work.

## Docs

Using koan:

- [docs/tutorial.md](docs/tutorial.md): one project from its first task to its archive, by hand.
- [docs/shell.md](docs/shell.md): shell completion, and the `koan pick` picker on a key.

How koan works, for contributors and agents:

- [docs/rules.md](docs/rules.md): the invariants and rules every tree obeys.
- [docs/prd.md](docs/prd.md): how koan is built: layout, storage, writes, concurrency, recovery.
- [docs/spec.md](docs/spec.md): exactly what every command takes and prints.
- [schemas/](schemas): the spec's JSON Schemas, one file each.
- [claude/skills/koan/SKILL.md](claude/skills/koan/SKILL.md): the skill agents use.

## Development

```shell
go test ./...                              # everything, the examples of the skill, this README and the tutorial included
go test -short ./...                       # without the long concurrency and crash matrices
python3 scripts/extract-schemas.py --check # schemas/ matches the spec
```

## License

[MIT](LICENSE)
