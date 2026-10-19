# koan

[![CI](https://github.com/phansen314/koan/actions/workflows/ci.yml/badge.svg)](https://github.com/phansen314/koan/actions/workflows/ci.yml)

Task management on one machine, with the filesystem as the database: tasks are JSON files in nested folders, with dependencies between them, and a Markdown notes file each. No daemon, no index, no server. Every command prints one line of JSON, so it is built to be driven by an agent such as Claude Code or OpenCode, with `jq` for anything a person reads.

Linux and macOS only.

**Pre-1.0:** until 1.0, the JSON output may change in a minor release, and its release notes say what changed.

## Install

Needs Go 1.25 or later.

```sh
GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@latest   # any GOBIN on your PATH
koan init                                                                  # this machine's task tree
```

The tree is always at `~/koan-root`, and koan's own state (the ID counter and the claims) at `~/.config/koan/state.json`. To keep the tree somewhere else, make `~/koan-root` a symlink to it before running `init`.

## A taste

Folders are made as needed, and folders and tasks share one ID sequence: below, `/work` gets 1, `/work/api` 2, and the first task 3.

```sh
koan task create 'Design schema' --folder /work/api --priority 2 --tags db
koan task create 'Write migrations' --folder /work/api --blocked-by 3
koan task frontier --limit 10 --fields id,title   # what can be taken now, in work order
koan task why 4                                   # .result.ready: the ready tasks 4 waits on
koan task done 3
koan folder list --folder /work --fields id,path,title
jq -n '{folder: "/work/api", tasks: [              # a plan in one call: refs name earlier tasks
  {ref: "endpoints", title: "Add endpoints"},
  {title: "Document endpoints", blocked_by: ["endpoints"]}]}' | koan batch -i -
```

A write returns what it changed, by ID (`.result.id`); a read returns the whole record. A task's notes are a plain file, edited with any editor: `koan task show 4 | jq -r .result.task.notes_path` says where.

Folder names use letters, digits, and hyphens, up to 64 characters, and are case-sensitive (`/work` does not find `/Work`; avoid names that differ only in case if the tree may move to a Mac). Tags are lowercase only, so matching one is always exact. See [Folders](spec/design.md#folders).

## Use it from Claude Code and OpenCode

The [koan skill](claude/skills/koan/SKILL.md) teaches the agent the commands. Claude Code gets it from the `koan` plugin (this repo is a Claude Code plugin marketplace):

```sh
claude plugin marketplace add phansen314/koan
claude plugin install koan@koan
```

Then, from a clone of this repo, add the permission rules. They let koan run without prompting, except for `init`, the deletes, `repair`, and `migrate`, which still ask. For OpenCode the script also links the skill.

```sh
scripts/install.sh               # every agent whose CLI is on PATH
scripts/install.sh --opencode    # or name them: --claude, --opencode
scripts/install.sh --uninstall   # take it all out again
```

[docs/agents.md](docs/agents.md) has the rules to add by hand, what the script changes, and how each agent picks up skill updates.

## Several agents, one tree

Two agents that each read the list of ready tasks would start the same one. A **claim** reserves a task: once it is claimed, no one else can claim it, and it leaves the frontier, until it is released or done.

```sh
koan task claim-next --folder /work --extra '{"worker":"w3"}'   # claim the first task of the frontier; task is null when it is empty
koan task claim 42                                              # or this task; a second claim is refused: conflict, rule claimed
koan task list --claimed --fields id,title,claim                # what is out, to whom, and since when
koan task done 42                                               # ends the claim
koan task release 42                                            # or give it back unfinished
```

A claim is a reservation and nothing more: it never expires, and every other command still works on a claimed task. See [Claims](spec/design.md#claims). [docs/coordination.md](docs/coordination.md) shows how a coordinator that starts its own workers builds safe edits on top of this, with a worked example for Claude Code subagents.

## Checking and repairing the tree

A crash, a git merge, or a hand edit can leave the tree damaged: a leftover temp file, a blocker that no longer exists, two tasks with one ID. Other commands report damage as warnings, or refuse to act on it; `doctor` finds all of it, and `repair` fixes what is safe to fix:

```sh
koan doctor      # .result.healthy, and .result.findings: what is wrong, and whether repair can fix it
koan repair      # fix what is safe to fix: leftovers, a low ID counter, a missing @folder.json, ...
koan repair --kinds dangling-reference,stale-claim   # remove blockers and claims on tasks that no longer exist
```

`repair` never chooses between versions, and removes a link to a task that seems gone only when asked by name. [Doctor and repair](spec/design.md#doctor-and-repair) lists every kind and what `repair` does with it.

## Keep the tree in git

koan keeps no trash and no history; git is the way back. `git init` in `~/koan-root` and commit now and then. Every file koan writes is safe to commit and restore, because the ID counter is kept outside the tree, where nothing git does can set it back. A crash can leave a `.koan-tmp-*` file; a root `.gitignore` with `.koan-tmp-*` keeps one out of a commit.

### Undoing a delete

`koan task delete` and `koan folder delete` remove files for good. With the tree in git, a delete can be undone back to the last commit:

```sh
cd ~/koan-root
git log --diff-filter=D --oneline -- '*/@42.json' '@42.json'     # the commit that removed task 42, if committed
git restore -- @proj/@42.json @proj/@42.md                        # not yet committed: from the last commit
git restore --source=<commit>^ -- @proj/@42.json @proj/@42.md     # committed: from the commit before
```

Restore only the removed paths: restoring the whole tree would undo every other change since that commit. Then:

- **Put back its blocks.** The delete took the task's ID out of other tasks' blockers; its output lists them as `dependents`, to `koan task block` again.
- **Run `koan doctor`.** A restored task may name blockers deleted since.

git restores only what it tracked:

- Untracked or ignored material (`.venv`, `.env`, build output) is gone for good, which is why `folder delete` lists it and removes it only with `--force`.
- A folder with its own `.git` inside is not in the tree's history at all: git keeps only a pointer to the inner repository, and restoring it gives an empty directory. Remove that directory rather than letting `koan repair` give it a folder file.

### More than one machine

A tree is written from one machine, and a machine has one tree; keep separate bodies of work as top-level folders (`/work`, `/home`).

To move a tree to another machine, clone or copy it to `~/koan-root` there and run `koan init`: it starts that machine's ID counter at the highest ID it finds, or raises a counter already there that is below it. Commands that issue IDs also start above the highest ID in the tree, so a pull or a restore can't make koan issue an ID that is already in use. Two machines that both create tasks in copies of one tree will issue the same IDs; merging them gives duplicate IDs, which `koan doctor` reports for you to resolve.

## Upgrading

```sh
GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@latest
koan info | jq -r .result.state
```

When a release changes the files a tree is made of, `state` is `needs-migration`, and every command but `migrate`, `doctor`, `init`, `info`, and `version` refuses the tree until you run `koan migrate`. There are no migrations in reverse, so commit the tree first: that commit is the way back. `koan migrate --dry-run` reports what would change.

## Docs

- [spec/design.md](spec/design.md): the data model, invariants, concurrency, and crashes.
- [spec/operations.md](spec/operations.md): every command's input, output, and refusals.
- [spec/cli.md](spec/cli.md): how the command line maps onto the commands.
- [spec/implementation.md](spec/implementation.md): how it is built and tested.
- [schemas/](schemas): the JSON Schemas of every file, input, and output.
- [docs/coordination.md](docs/coordination.md): coordinating workers on one tree.
- [docs/agents.md](docs/agents.md): setting up an agent by hand.
- [docs/deferred.md](docs/deferred.md): work decided on but not built yet.

## Development

```sh
go test ./...                          # unit and end-to-end tests
go test -short ./...                   # without the 800-process lock test and the crash matrix
```

[docs/releasing.md](docs/releasing.md) has the release steps.

## License

[MIT](LICENSE)
