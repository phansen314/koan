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

```sh
koan create 'Design schema' --folder /work/api --priority 2 --tags db   # folders are made as needed
koan create 'Write migrations' --folder /work/api --blocked-by 1
koan frontier --limit 10 --fields id,title                       # what can be taken now, in work order
koan why 2                                                       # why 2 isn't ready: .result.ready would move it
koan done 1
jq -n '{folder: "/work/api", tasks: [                             # a plan in one call: refs name earlier tasks
  {ref: "endpoints", title: "Add endpoints"},
  {title: "Document endpoints", blocked_by: ["endpoints"]}]}' | koan create-batch -i -
```

A task's notes are a plain file, edited with any editor: `koan show 2 | jq -r .result.task.notes_path` says where.

## Use it from Claude Code and OpenCode

The [koan skill](claude/skills/koan/SKILL.md) teaches the agent the commands. Claude Code gets it from the `koan` plugin (the repo is a Claude Code plugin marketplace):

```sh
claude plugin marketplace add phansen314/koan
claude plugin install koan@koan
```

Then, from a clone of this repo, add the permission rules. They let koan commands run without a prompt, except the ones that should ask: `init`, which changes this machine's setup; the deletes, which can't be undone but through git; `repair`, which changes files to repair the tree; and `migrate`, which rewrites them to a new format.

```sh
scripts/install.sh               # every agent whose CLI is on PATH
scripts/install.sh --opencode    # or name them: --claude, --opencode
scripts/install.sh --uninstall   # take it all out again
```

It needs `jq`, backs a settings file up (to `.bak.<timestamp>`, a new one each time) before changing it, touches only koan's rules, and is safe to rerun. For OpenCode it also links the skill into `~/.config/opencode/skills/koan`, so OpenCode's skill comes from this clone: `git pull` updates it. Claude Code's comes from the plugin: `claude plugin update koan@koan` picks up changes. To try an edited skill in Claude Code before pushing, run `claude --plugin-dir .` in a clone.

### The rules, to add by hand

Claude Code, in `~/.claude/settings.json`:

```json
{
  "permissions": {
    "allow": ["Bash(koan:*)", "Bash(jq:*)"],
    "ask": ["Bash(koan init:*)", "Bash(koan delete:*)", "Bash(koan delete-folder:*)", "Bash(koan repair:*)", "Bash(koan migrate:*)"]
  }
}
```

OpenCode, in `~/.config/opencode/opencode.json` (the script leaves an `opencode.jsonc`, or a file with comments, alone, and prints these for you to add). In OpenCode the last matching rule wins, so order matters: these go after any other rule that matches koan, and the asks after `"koan *"`:

```json
{
  "permission": {
    "bash": {
      "koan *": "allow",
      "jq *": "allow",
      "koan init*": "ask",
      "koan delete *": "ask",
      "koan delete-folder *": "ask",
      "koan repair*": "ask",
      "koan migrate*": "ask"
    }
  }
}
```

For OpenCode, link the skill too: `ln -s "$PWD/claude/skills/koan" ~/.config/opencode/skills/koan` from the clone. Not in `~/.claude/skills`: OpenCode reads that as well, and Claude Code would load the skill a second time next to the plugin's.

OpenCode also asks before its file tools touch anything outside the project, which includes a task's notes file. To let it read and edit notes without asking, add `"external_directory": { "~/koan-root/*": "allow" }` under `"permission"`.

## Folder names and tags

Folder names can use upper and lower case letters, digits, and hyphens, up to 64 characters, and can't start or end with a hyphen: `/Work/API-v2` is fine. Case is kept and must match exactly, so `/work` does not find `/Work`.

On Linux, `/Work` and `/work` are two folders. macOS filesystems usually treat them as one, so a tree that holds both can't be copied to a Mac intact. koan does not check for this: if a tree moves between the two, don't use names that differ only in case.

Tags are lowercase only (`db`, `needs-review`), so matching a tag is always exact.

## Several agents, one tree

Two agents that each read the list of ready tasks would start the same one. A **claim** reserves a task: once it is claimed, no one else can claim it, and it leaves the frontier, until it is released or done.

```sh
koan claim-next --folder /work --extra '{"worker":"w3"}'   # claim the first task of the frontier; task is null when it is empty
koan claim 42                                              # or this task; a second claim is refused: conflict, rule claimed
koan list --claimed --fields id,title,claim                # what is out, to whom, and since when
koan done 42                                               # ends the claim
koan release 42                                            # or give it back unfinished
```

A claim is a reservation and nothing more. It refuses other claims and hides the task from `frontier`; every other command works on a claimed task, for any caller. It never expires and it outlasts the process that made it, so a dead worker's claim stays until someone releases it. What a caller wants to record about a claim goes in `--extra`, which koan stores and returns and never reads.

Claims are kept per machine, in the state file, not in the tree: they don't travel through git.

[docs/coordination.md](docs/coordination.md) shows how a coordinator that starts its own workers builds safe edits on top of this, with a worked example for Claude Code subagents.

## Checking and repairing the tree

A crash, a git merge, or a hand edit can leave the tree damaged: a leftover temp file, a blocker that no longer exists, two tasks with one ID. Other commands report damage as warnings, or refuse to act on it; `doctor` finds all of it, and `repair` fixes what is safe to fix:

```sh
koan doctor      # .result.healthy, and .result.findings: what is wrong, and whether repair would fix it
koan repair      # remove leftovers, dangling blockers, and stale claims, and raise the ID counter
```

`repair` never chooses between versions: duplicate IDs, dependency cycles, and unusable files are reported for you to resolve. See [Doctor and repair](spec/design.md#doctor-and-repair).

## Undoing a delete

`koan delete` and `koan delete-folder` remove files for good; koan keeps no trash. Keep the tree in git (`git init` in it, and commit now and then) and a delete can be undone, back to the last commit:

```sh
cd ~/koan-root
git log --diff-filter=D --oneline -- '*/42.json' '42.json'     # the commit that removed task 42, if committed
git restore -- proj/42.json proj/42.md                          # not yet committed: from the last commit
git restore --source=<commit>^ -- proj/42.json proj/42.md       # committed: from the commit before
```

Restore only the removed paths: restoring the whole tree would undo every other change since that commit. Every file in the tree is safe to commit and restore, with no `.gitignore` needed, because the ID counter is kept outside it. The delete also took the task's ID out of other tasks' blockers; its output lists them as `dependents`, to `block` again after restoring. Then run `koan doctor`: a restored task may name blockers deleted since.

## More than one machine

A tree is written from one machine. The ID counter is that machine's, kept in the state file and never in the tree, so that nothing git does to the tree can set it back. To use a tree on another machine, clone or copy it to `~/koan-root` there and run `koan init`: it starts that machine's counter at the highest ID it finds. Two machines that both create tasks in copies of one tree will issue the same IDs; merging them gives duplicate IDs, which `koan doctor` reports for you to resolve.

A machine has one tree. Keep separate bodies of work as top-level folders (`/work`, `/home`).

## Upgrading

```sh
GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@latest
koan info | jq -r .result.state
```

When a release changes the files a tree is made of, `state` is `needs-migration`, and every command but `doctor` and `migrate` refuses the tree until you run `koan migrate`. Commit the tree first if it is in git: that commit is the way back, since there are no migrations in reverse. `koan migrate --dry-run` reports what would change.

## Specs

- [spec/design.md](spec/design.md): the data model, invariants, concurrency, and crashes.
- [spec/operations.md](spec/operations.md): every command's input, output, and refusals.
- [spec/cli.md](spec/cli.md): how the command line maps onto the commands.
- [spec/implementation.md](spec/implementation.md): how it is built and tested.
- [schemas/](schemas): the JSON Schemas of every file, input, and output.

There is no interactive picker at the moment: [pick-spec.md](pick-spec.md) describes the one koan had, which is being redesigned.

## Development

```sh
go test ./...                          # unit and end-to-end tests
go test -short ./...                   # without the 800-process lock test and the crash matrix
```

### Releasing

The plugin serves the skill from the release tag, so the skill and `go install …@latest` move together.

1. CI is green on `main`.
2. Pick the version: before 1.0, a new command or any change to the formats or the JSON output bumps the minor version; anything else, the patch.
3. Set `ref` in [`.claude-plugin/marketplace.json`](.claude-plugin/marketplace.json) to the new tag (until the first release the plugin's `source` is `"./"`: replace it with `{ "source": "github", "repo": "phansen314/koan", "ref": "vX.Y.Z" }`), and the skill's minimum version if the skill now needs this release; commit.
4. Tag and push together: `git tag -a vX.Y.Z -m "…"` then `git push --atomic origin main vX.Y.Z`.
5. `gh release create vX.Y.Z` with notes on what changed and how to fix existing trees.
6. Check: `GOBIN=$(mktemp -d) GOPROXY=https://proxy.golang.org go install github.com/phansen314/koan/cmd/koan@vX.Y.Z`, and that binary's `koan version` reports `X.Y.Z`.

## License

[MIT](LICENSE)
