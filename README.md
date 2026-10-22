# koan

[![CI](https://github.com/phansen314/koan/actions/workflows/ci.yml/badge.svg)](https://github.com/phansen314/koan/actions/workflows/ci.yml)

Task management for one person on one machine, built to be driven by agents such as Claude Code and OpenCode. Tasks live in nested folders, with dependencies between them; the structure is kept in a SQLite database, and each task and folder has a plain Markdown notes file in a tree of directories you can open in any editor. No daemon, no server. Every command prints exactly one line of JSON, apart from help, shell completion and the `koan pick` picker.

Linux and macOS only.

**Pre-1.0:** until 1.0, the JSON output may change in a minor release, and its release notes say what changed.

## Install

Needs Go 1.25 or later. The SQLite driver is SQLite translated to Go, so there is no cgo and no C compiler, just one binary.

```shell
GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@v0.5.0   # any GOBIN on your PATH
```

For shell completion of commands and options, load koan's script in your shell's startup file; `koan completion --help` lists the shells:

```shell
source <(koan completion bash)   # in ~/.bashrc; zsh: source <(koan completion zsh) in ~/.zshrc
```

Then make this machine's root:

```sh
koan init
```

Everything is under `~/koan-root`:

| Path | Holds |
|---|---|
| `koan.db` | the database: tasks, folders, the edges between tasks, claims. Only koan writes it |
| `tree/` | a directory per folder, named `@<name>`, holding its notes `@notes.md`, each task's notes `@<id>.md`, and anything else you keep there (your *material*, which koan never reads) |
| `archived/` | finished work that was archived, as files |

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
koan task frontier --limit 10 --fields ref,title                         # what can be taken now: only @3
koan task claim-next --extra '{"by":"me"}'                               # claims @3 and returns it, so no one else takes it
koan task complete 3                                                     # .result.released: [4], now ready
koan task show 4                                                         # the whole task, with its notes_path
```

A write returns what it changed, by ID; a read returns whole records. Errors carry a `kind`, and a refusal its `rule`, such as `cycle`.

### Ordering folders

Edges are only ever between tasks: where a task sits never orders it. To order whole folders, a folder has two **gates**, tasks of its own made the first time an edge names them: `/work/api.start`, which its work waits on, and `/work/api.done`, which waits on its work and which what comes after waits on. In an edge, a folder means its done gate.

```sh
koan task create 'Announce it' --folder /work --blocked-by /work/api    # @5 waits on /work/api's done gate, made as @6
koan task block /work/api.done --blocked-by 3,4                         # the done gate waits on the folder's work
koan task why 5                                                         # .result.ready: [4], what to finish to move @5
```

A plan with several tasks goes in at once, as one change, with labels for its tasks to name each other by:

```sh
koan batch -i - <<'EOF'
{"folders": [{"path": "/home/trip", "title": "Lisbon in May"}],
 "folder": "/home/trip",
 "tasks": [{"label": "$flights", "title": "Book flights"},
           {"title": "Book a hotel", "blocked_by": ["$flights"]}]}
EOF
```

Several agents can work from one tree: a **claim** reserves a task, so two workers are never handed the same one. It never expires; `task complete` or `task release` ends it. Finished work leaves the tree with `koan task archive` and `koan folder archive`, into `~/koan-root/archived`, where `rg` finds it.

## Use it from Claude Code and OpenCode

The [koan skill](claude/skills/koan/SKILL.md) teaches the agent the commands. Claude Code gets it from the `koan` plugin; this repo is a Claude Code plugin marketplace:

```shell
claude plugin marketplace add phansen314/koan
claude plugin install koan@koan
```

Then, from a clone of this repo, add the permission rules. They let every koan command run without asking, except `koan init`, `koan task delete`, `koan folder delete` and `koan repair`, which always ask. For OpenCode the script also links the skill into `~/.config/opencode/skills/koan`.

```shell
scripts/install.sh               # every agent whose CLI is on PATH
scripts/install.sh --opencode    # or name them: --claude, --opencode
scripts/install.sh --uninstall   # take it all out again
```

The script is safe to rerun. It backs a settings file up, to `.bak.<timestamp>`, before changing it, and adds or removes only koan's rules. It never edits an `opencode.jsonc`, or an `opencode.json` that isn't plain JSON: it prints the rules to add by hand. It needs `jq`, and runs under macOS's `/bin/bash`.

## Backup and restore

`koan backup` copies the database, the tree and the archive into a new or empty directory, while koan keeps running, and checks the copy:

```sh
koan backup ~/backups/koan-$(date +%F)   # .result.clean is true when the copy checks out
```

Copying `koan.db` by hand while koan runs is not a backup. To restore, put a backup in place of `~/koan-root` and run `koan doctor`:

```sh
mv ~/koan-root ~/koan-root.before-restore
cp -R ~/backups/koan-$(date +%F) ~/koan-root   # or whichever backup you want
koan doctor                                    # .result.findings: what is wrong, if anything
```

A backup carries the claims of its moment, and a restore brings them back with it.

## Checking and repairing

A crash, or a folder renamed or dragged by hand in your editor, can leave the tree on disk out of step with the database. `koan doctor` finds it and changes nothing; `koan repair` fixes what it can without losing anything, and reports what it left for you. Every finding's message says what to do.

## Uninstalling

Remove the binary, `scripts/install.sh --uninstall`, `claude plugin uninstall koan@koan`, and `~/koan-root` if its work isn't wanted.

## Docs

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
