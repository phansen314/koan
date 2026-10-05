# ftask

[![CI](https://github.com/phansen314/ftask/actions/workflows/ci.yml/badge.svg)](https://github.com/phansen314/ftask/actions/workflows/ci.yml)

Task management on one machine, with the filesystem as the database: tasks are JSON files in nested folders, with dependencies between them, and a Markdown notes file each. No daemon, no index, no server. Every command prints one line of JSON, so it is built to be driven by an agent such as Claude Code or OpenCode, with `jq` for anything a person reads.

Linux and macOS only.

**Pre-1.0:** until 1.0, the task file format, `ftask.json`, and the JSON output may change in place, with no `schema` bump or migration, so a new version can report an existing tree's files as `corrupt`. Each such change bumps the minor version (0.1 → 0.2), and its release notes say how to fix existing trees. See [Format versions](design-spec.md#format-versions).

## Install

Needs Go 1.25 or later.

```sh
GOBIN=~/.local/bin go install github.com/phansen314/ftask/cmd/ftask@latest   # any GOBIN on your PATH
ftask init ~/ftasks                                                          # this machine's task tree
```

## Use it from Claude Code and OpenCode

The [ftask skill](claude/skills/ftask/SKILL.md) teaches the agent the commands. Claude Code gets it from the `ftask` plugin (the repo is a Claude Code plugin marketplace):

```sh
claude plugin marketplace add phansen314/ftask
claude plugin install ftask@ftask
```

Then, from a clone of this repo, add the permission rules, which let ftask commands run without a prompt while `init`, which changes this machine's setup, the deletes, which can't be undone but through git, and `repair`, which changes files to repair the tree, still ask:

```sh
scripts/install.sh               # every agent whose CLI is on PATH
scripts/install.sh --opencode    # or name them: --claude, --opencode
scripts/install.sh --uninstall   # take it all out again
```

It needs `jq`, backs a settings file up (to `.bak.<timestamp>`, a new one each time) before changing it, touches only ftask's rules, and is safe to rerun. For OpenCode it also links the skill into `~/.config/opencode/skills/ftask`, so OpenCode's skill comes from this clone: `git pull` updates it. Claude Code's comes from the plugin, and tracks `main`: `claude plugin update ftask@ftask` picks up changes, or turn on auto-update for the `ftask` marketplace in `/plugin`. To try an edited skill in Claude Code before pushing, run `claude --plugin-dir .` in a clone.

Then ask your agent things like "what should I work on next?" or "add a task to review the migration PR, blocked by 12".

### The rules, to add by hand

Claude Code, in `~/.claude/settings.json`:

```json
{
  "permissions": {
    "allow": ["Bash(ftask:*)", "Bash(jq:*)"],
    "ask": ["Bash(ftask init:*)", "Bash(ftask delete:*)", "Bash(ftask delete-folder:*)", "Bash(ftask repair:*)"]
  }
}
```

OpenCode, in `~/.config/opencode/opencode.json` (the script leaves an `opencode.jsonc`, or a file with comments, alone, and prints these for you to add). In OpenCode the last matching rule wins, so order matters: these go after any other rule that matches ftask, and the asks after `"ftask *"`:

```json
{
  "permission": {
    "bash": {
      "ftask *": "allow",
      "jq *": "allow",
      "ftask init*": "ask",
      "ftask delete *": "ask",
      "ftask delete-folder *": "ask",
      "ftask repair*": "ask"
    }
  }
}
```

For OpenCode, link the skill too: `ln -s "$PWD/claude/skills/ftask" ~/.config/opencode/skills/ftask` from the clone. Not in `~/.claude/skills`: OpenCode reads that as well, and Claude Code would load the skill a second time next to the plugin's.

OpenCode also asks before its file tools touch anything outside the project, which includes a task's notes file. To let it read and edit notes without asking, add your tree's root, for example `"external_directory": { "~/ftasks/*": "allow" }` under `"permission"`.

## A taste

```sh
ftask create-folder -p /work/api
ftask create 'Design schema' --folder /work/api --priority 2 --tags db
ftask create 'Write migrations' --folder /work/api --blocked-by 1
ftask frontier --limit 10 --fields id,title                       # the next ready tasks, in work order
ftask complete 1
jq -n '{folder: "/work/api", tasks: [                             # a plan in one call: refs name earlier tasks
  {ref: "endpoints", title: "Add endpoints"},
  {title: "Document endpoints", blocked_by: ["endpoints"]}]}' | ftask create-batch -i -
```

## Picking tasks yourself

`ftask pick` is for working with the tree by hand. It's a [fzf](https://github.com/junegunn/fzf) picker that fuzzy-searches tasks by title and tags, with details and notes in a preview, lets you act on them in place, and prints the ones you choose as one JSON envelope. It needs a terminal and fzf 0.63.0 or later on `PATH`. If `glow` or `bat` is installed, it renders notes in the preview. Agents never run it: run it yourself and hand them the output.

Type to search. Esc switches to command mode, where single keys act on the marked tasks (Tab, or space in command mode, marks one), or else on the one under the cursor:

| Key | Does |
|---|---|
| Enter | Emits the marked tasks, or the one under the cursor, and exits. In a prompt (`n`, `p`, `t`) or a list to choose from (`b`, `u`, `m`, `f`), it applies what you entered or chose instead. |
| `c` | Completes, or reopens if every target is complete. |
| `e` | Edits notes in `$VISUAL` or `$EDITOR`. |
| `n` | Creates a task in the scope folder. Its title prompt starts with the query, to keep or edit. |
| `b` / `u` | Blocks on, or unblocks from, tasks chosen from a list. |
| `m` | Moves to a folder chosen from a list. |
| `p` / `t` | Sets the priority (`null` clears it) or the tags (`a b` replaces them, `+a -b` adds and removes). |
| `x` | Edits the title, priority, tags and `extra` as JSON. |
| `s` / `f` | Cycles the scope (ready, open, all), or changes the folder. |
| `j` `k` `g` `G` | Moves down, up, to the first line, to the last. |
| `?` | Shows every key. |
| `i` or `/` | Goes back to searching. |
| Esc or `q` | Quits with nothing selected. In a prompt or a list to choose from, Esc cancels it instead. |
| ctrl-c | Cancels, in any mode. |

Every change made in the picker is in the output, so whatever reads it learns what you did: each operation it ran under `result.actions`, and the tasks whose notes you edited under `result.notes_edited`:

```sh
ftask pick | jq -r '.result.tasks[].id'                            # the IDs picked
ftask pick --folder /work --scope ready                            # what's ready under /work
ftask list --readiness blocked --fields id | ftask pick --from -   # choose among the blocked ones
ftask pick --source 'ftask frontier --tags-any today'              # the same idea, reloaded after each action
ftask complete "$(ftask pick --query 'renew pass' --select-one | jq -r '.result.tasks[0].id')"   # no picker if only one matches
ftask pick --folders | jq -r '.result.folders[0]'                  # a folder path instead
ftask pick > picked.json; jq '.result | {actions, notes_edited}' picked.json   # what the session changed
```

`FZF_DEFAULT_OPTS` applies as usual, and `FTASK_PICK_OPTS` is passed after `pick`'s own options, e.g. `FTASK_PICK_OPTS='--height 60% --layout reverse'`. See [pick-spec.md](pick-spec.md) for the whole keymap and every option.

## Checking and repairing the tree

A crash, a git merge, or a hand edit can leave the tree damaged: a leftover temp file, a blocker that no longer exists, two tasks with one ID. Other commands report damage as warnings, or refuse to act on it; `doctor` finds all of it, and `repair` fixes what is safe to fix:

```sh
ftask doctor                         # .result.healthy, and .result.findings: what is wrong, and what repair would do
ftask repair                         # remove leftovers and dangling blockers, raise last_id; the rest is left to you
ftask repair --kinds metadata-missing   # rebuild a lost ftask.json; never done by default
```

`repair` never decides between versions: duplicate IDs, dependency cycles, and corrupt files are reported, with a suggestion, for you to resolve. See [Diagnosis and repair](design-spec.md#diagnosis-and-repair).

## Undoing a delete

`ftask delete` and `ftask delete-folder` remove files for good; ftask keeps no trash. Keep the tree in git (`git init` in it, and commit now and then) and a delete can be undone, back to the last commit:

```sh
cd ~/ftasks
git log --diff-filter=D --oneline -- '*/42.json' '42.json'     # the commit that removed task 42, if committed
git restore -- proj/42.json proj/42.md                          # not yet committed: from the last commit
git restore --source=<commit>^ -- proj/42.json proj/42.md       # committed: from the commit before
```

Restore only the removed paths, never the whole tree: `ftask.json` holds the last issued ID, and rolling it back lets IDs be reused. The delete also took the task's ID out of other tasks' blockers; its output lists them as `dependents`, to re-`block` after restoring. Then run `ftask doctor`: a restored task may name blockers deleted since. See [Undo](operations.md#undo).

## Specs

- [design-spec.md](design-spec.md): the data model, invariants, concurrency, and crashes.
- [operations.md](operations.md): every operation's input, output, errors, and retry safety.
- [cli-spec.md](cli-spec.md): how commands and options map to operations.
- [implementation-spec.md](implementation-spec.md): how it is built and tested.
- [pick-spec.md](pick-spec.md): `ftask pick`, the interactive fzf picker for people.

## Development

```sh
go test ./...        # unit and e2e tests
scripts/smoke.sh     # the built binary from a shell, in a throwaway home
```

## License

[MIT](LICENSE)
