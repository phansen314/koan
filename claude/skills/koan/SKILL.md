---
name: koan
description: Track and work through the user's tasks with the koan CLI — a local, file-based task tree with dependencies. Use when the user mentions tasks, todos, their task list, "what should I work on next", blocking or unblocking work, marking something done, or koan by name; and to offer (not silently create) a task for a follow-up found during other work.
---

# koan

`koan` keeps the user's tasks as files under `~/koan-root`. Tasks live in folders, can block each other, and each has a notes file. Every command prints **one line of JSON** and nothing else.

## Before the first command

```sh
koan info
koan version
```

Ready when `.result.state` is `usable`. Otherwise:

- `not-initialized`: `.result.missing` says what is absent. Tell the user and suggest `koan init`. **Never run `init` unasked**: it changes this machine's setup.
- `needs-migration`: the tree is in an older format; see `migration-pending` below.
- `too-new`: a newer koan wrote the tree; the user needs to upgrade this machine's binary.
- `unusable`: `.result.error` is what every command would fail with. Report it.

If `version`'s `.result.version` is below `0.2.0` (`0.0.0-dev` is a build from source: treat it as current), the binary is older than this skill: tell the user to upgrade with `go install github.com/phansen314/koan/cmd/koan@latest`. A `usage` error saying `unknown command`, or `unknown flag`, for a command or option this skill names means the same.

## Reading output

Every command writes one envelope:

- success: `{"ok":true,"result":{…},"warnings":[…]}`
- failure: `{"ok":false,"error":{"kind":"…","rule":…,"message":"…","ids":[…],"folders":[…],"paths":[…],"problems":[…],"changed_paths":[…]},"warnings":[…]}`

Branch on `.error.kind`, not the exit code. **Always tell the user about any `warnings`** (`unusable-file`, `dangling-reference`): they mean the tree needs attention.

| Exit | Meaning | What to do |
|---|---|---|
| 0 | Success | — |
| 1 | The command failed; see `.error.kind` | See below |
| 2 | Usage error (bad command line) | Fix the command; check `koan <cmd> --help` |
| other | Outcome unknown (killed, stdout lost) | Look before running it again: `show`, `list`, or `doctor`. `create` and `create-batch` make new tasks each time, and `claim-next` a new claim, so check with `list` (or `list --claimed`) first. Every other write is safe to rerun. |

A failed command may still have changed something: **`.error.changed_paths` lists what it created, rewrote, or removed before it failed**. Empty means nothing changed. When it isn't empty, tell the user what was left half done.

Error kinds worth handling:

- `busy` — the tree's lock stayed held for 5 seconds; koan already waited. Reads can get this too. Don't retry in a loop: tell the user something is holding the lock (a `doctor`, `repair`, or `migrate` on a very large tree, or a stuck `koan` process), and retry once they say so.
- `not-found` — `.error.ids` and `.error.folders` name everything that's missing.
- `invalid-input` — `.error.problems[]` lists every bad field.
- `conflict` — `.error.rule` says which rule refused it:
  - `cycle` — the block would make a cycle; `.error.ids` shows it. (A `corrupt` from `block` means the check met a task file it can't read: report it, as below.)
  - `not-empty` — `delete-folder` without `--recursive` on a folder that holds anything. Don't add `--recursive` on your own: ask the user.
  - `has-material` — `delete-folder --recursive` on a folder holding the user's own files (material); `.error.paths` lists them. Don't add `--force` on your own: show the user the list and ask. `--force` deletes those files for good.
  - `destination-exists` — `move-folder`'s `--to` already exists (folders are never merged), or `move` would overwrite a `@<id>.md` with text in the target folder; show it to the user.
  - `not-a-folder` — something on a folder path is a file or a symlink.
  - `claimed` — someone has claimed the task; `.error.message` says since when and with what `extra`. See Claims.
  - `task-done` — `claim` on a done task; `reopen` it first if the user wants it worked on again.
  - `duplicate-id` — two task files, or a task and a folder, have one ID; `.error.paths` lists them. The tree is damaged: check it (below), don't work around it.
- `migration-pending` — koan was upgraded and the tree is in an older format. Stop and tell the user to run `koan migrate`, committing the tree first if it's a git repository. **Never run `migrate` unasked**: it rewrites task files and holds the write lock while it runs.
- `not-initialized`, `corrupt`, `unsupported-format`, `io`, `internal` — stop and report to the user, quoting `.error.message`; don't try to fix files by hand.

## When the tree is damaged

After a `duplicate-id` or `corrupt` error, or any warning, run `koan doctor` — once per session, not after every command — and summarize `.result.findings` for the user: each finding's `kind`, its `paths` and `ids`, and its `message`, which says what to do. `doctor` changes nothing, and takes the write lock while it runs, so other writes wait for it.

- Findings with `repairable: true` are what `koan repair` can fix. A plain `koan repair` fixes leftover temp files and folders, claims on tasks that are done, notes that lost their task, an ID counter that is too low, and a folder with no `@folder.json` (it writes one, with a new ID); it leaves the two below. **Never run `repair` unasked**: offer it, saying what it would change. It asks for permission anyway.
- `dangling-reference` findings (blockers that no longer exist) and `stale-claim` findings on tasks that no longer exist are left by a plain `repair`. Ask the user first whether a folder was moved or renamed by hand: if so, moving it back restores the links. Otherwise offer `koan repair --kinds dangling-reference` or `--kinds stale-claim`.
- Findings with `repairable: false` (duplicate IDs, cycles, unusable files, notes with text and no task) are the user's to resolve. Pass on `message`; don't edit files to fix them yourself.
- `old-format` findings are fixed by `koan migrate`, never by `repair`.

## Keep output small

Everything koan prints lands in your context, and stays there for the rest of the session. A whole task is about 400 bytes, so a bare `koan list` of a few hundred tasks is tens of KB. **Always pass `--limit` and `--fields` to `frontier` and `list`**, as below, and widen only when the question needs it:

- `--fields id,title,…` returns only those fields of each task (`id` always). Fields: `title`, `priority`, `folder`, `tags`, `readiness`, `blocking`, `blocked_by`, `claim`, `extra`, `created_at`, `completed_at`, `updated_at`, `notes_path`, `schema`.
- `--limit N` returns the first N in the command's order. The result always says `total` and `truncated`: when `truncated` is true there are `total` tasks and you got N. Say so rather than presenting N as everything, and fetch more only if the user needs them.
- `--tags-any a,b` / `--tags-all a,b` filter by tag; `list` also takes `--readiness …`, and `--claimed` / `--claimed=false`. Anything else (`extra`, title words) is `jq`'s, still with `--fields` so less comes through.
- `--limit 1 --fields id` gives just the count, in `total`.
- For one task's full detail, `show` it.

With `jq`, a trimmed failure looks like success: the pipe's exit status is jq's, so the error kind and warnings just vanish. So:

- **Never trim a write's, `show`'s, or any small output's envelope**: print it as is.
- **When a big read needs `jq`**, keep `ok`, `error` and `warnings` with `jq -c 'if .ok then .result |= <shape> else . end'`, where `<shape>` gives one value (`map(…)`, never `.[]`).
- **A filter that drops tasks** replaces `total` and `truncated`, which count what koan returned, with `scanned` (tasks it checked) and `unscanned` (tasks `--limit` cut, never checked), as in the `extra` example below. Before saying there are none, or giving a count, make `unscanned` 0 by raising or dropping `--limit`.

## The daily loop

```sh
koan frontier --limit 10 --fields id,title,priority,folder,tags   # what can be taken now, in work order
koan show 42                                                      # one task, whole: .result.task
koan claim 42                                                     # it's yours: no one else can claim it
koan done 42                                                      # finished, or cancelled: see below
```

`frontier` lists the tasks that are open, unblocked, and unclaimed, in the order to work on them: highest priority first, unprioritized after, ties oldest (lowest ID) first. Scope it with `--folder /proj` and `--recursive=false`, or by tag with `--tags-any`.

### Claims

Other agents (another Claude Code or OpenCode session, say) may be working from the same tree. A **claim** reserves a task: once you have claimed it, no one else can, and it leaves the frontier, until it is released or done.

```sh
koan claim 42                                          # this task
koan claim-next --folder /proj                         # or the first task of the frontier; .result.task is null if it is empty
koan list --claimed --limit 20 --fields id,title,claim # what's taken, and since when
koan release 42                                        # you won't do it after all
```

- **Claim a task before starting it**, whether you picked it or the user named it. `koan claim-next` picks and claims in one step, so prefer it to `frontier` then `claim` when the choice is yours.
- **On `conflict` with `rule: "claimed"`**, someone else has it: pick another if the choice was yours, and tell the user if they named the task. After `claim-next` returns `task: null`, nothing ready is free.
- **The one time `claimed` may be your own claim** is a rerun of `koan claim` whose first run ended with its outcome unknown. koan can't tell you whose it is, so don't start the task on that alone: tell the user that the task is claimed, possibly by your earlier attempt, and let them say whether to go on.
- **`done` ends the claim.** If you stop without finishing, `koan release` it, so the task isn't left reserved.
- **A claim is a reservation, not a lock.** koan refuses a second claim and nothing else: every other command, and any editor, still works on a claimed task. So the rules are yours to keep:
  - **Never change a task someone else has claimed**, or its notes, unless the user says so.
  - **Never release a claim you didn't make** unless the user says so: its holder may still be working.
  - **Whoever holds the claim edits the task's notes.** Don't edit the notes of a task you haven't claimed while it is claimed.
- A claim never expires, and koan doesn't know who made one. `claim.extra` is whatever the claimer stored with it (`--extra '<json>'`); koan never reads it.

### If a coordinator started you with one task

When you were started by another agent and handed a task's ID and notes path, you are a worker: edit **only that task's notes file**, run only koan's read commands (`show`, `list`, `frontier`, `why`), and put everything else you want changed (follow-up tasks, tags, the task being done) in your final report. The coordinator makes those changes.

### Reading tasks

`show` returns `.result.task`. Every task koan returns has `readiness` (`ready`/`blocked`/`done`), `blocking` (the blocker IDs still holding it up), `claim` (`null`, or `{claimed_at, extra}`), and `notes_path`. **Notes are a plain Markdown file:** read and edit it directly with your file tools — there is no koan command for notes after creation.

Overview of everything:

```sh
koan list --limit 50 --fields id,title,readiness,folder           # open tasks, in tree order
koan list --readiness blocked --limit 20 --fields id,title,blocking   # what's stuck, and on what
koan list --folder /proj --tags-any urgent --limit 20 --fields id,title,readiness
koan list --readiness done --limit 1 --fields id                  # how many are done: .result.total
koan list --include-folders --limit 1 --fields id                 # every folder: .result.folders
koan list --fields id,title,extra --limit 200 | jq -c 'if .ok then .result |= {tasks: (.tasks | map(select(.extra.status == "waiting"))), scanned: (.tasks | length), unscanned: (.total - (.tasks | length))} else . end'
```

`list` returns open tasks (`ready` and `blocked`), claimed or not, unless `--readiness` says otherwise: `--readiness done` for finished ones, `--readiness ready,blocked,done` for all.

Why a task isn't moving — don't walk `blocking` by hand:

```sh
koan why 42          # {"readiness":"blocked","ready":[17,23],"stuck":[31]}
```

`ready` is the IDs of the ready tasks upstream of 42, in work order: finishing them is what moves it. `stuck` is blocking IDs no work clears: a missing or unusable task, or tasks blocking each other in a cycle. Report those to the user rather than working around them.

## Writing

```sh
koan create 'Write migrations' --folder /proj/api --priority 2 --tags db,backend --blocked-by 41
koan create 'Investigate flaky test' --notes-file - <<'EOF'
Seen in CI on 2026-09-28. Fails ~1 in 20 runs.
EOF
koan update 42 --priority null --tags-add urgent --extra-merge '{"status":"waiting"}'
koan block 42 --blockers 40,41                      # 40 and 41 must finish before 42
koan unblock 42 --blockers 41
koan reopen 42
koan create 'Pick a library' --blocks 42           # the new task must finish before 42
```

`--blocks` is `--blocked-by` the other way round: the tasks named wait on the new one (`.result.dependents`). A new task that would close a cycle is refused with `cycle`, as `block` refuses one.

### Folders

A folder has an ID (from the same counter as tasks), an optional title, an `extra` object, and notes in its `@folder.md`: the place for what a folder of work is about.

```sh
koan create-folder /work/HOME-12345 --title 'Payment retries' --extra '{"jira":"HOME-12345"}' --notes 'Context …'
koan show-folder /work/HOME-12345           # .result.folder: id, path, dir, title, extra, notes_path
koan update-folder /work/HOME-12345 --title null --extra-merge '{"status":"review"}'
koan list --folder /work --include-folders --limit 0 | jq '.result.folders[] | {id, path, title}'
```

`create-folder` sets a title, `extra`, or notes only on a folder it makes: on one already there it refuses with `destination-exists`, and `update-folder` is the way. Edit a folder's notes in its `@folder.md`, as a task's in its `@<id>.md`. The root, `/`, has no ID, title, or notes.

**Folders are created as needed.** `create`, `create-batch`, `move`, `move-folder`, and `create-folder` make any folder they are given that doesn't exist, so a typo in a folder path makes a new folder. Their results list what was made in `folders_created`: **check it, and tell the user about any folder you didn't mean to make.**

A command asked for what already holds (`done` on a done task, a blocker already there) succeeds with `changed: false`.

### A plan: several tasks at once

When a request breaks down into several tasks, especially with dependencies among them, create them in **one** `create-batch` instead of a chain of `create`s. Give a task a `ref` to let later tasks in the batch wait on it; a `blocked_by` item is an existing task's ID (a number) or an earlier task's ref (a string). List blockers before what they block.

```sh
koan create-batch -i - <<'EOF'
{"folder": "/proj/api", "tasks": [
  {"ref": "schema", "title": "Design schema", "priority": 2, "tags": ["db"]},
  {"ref": "migrate", "title": "Write migrations", "blocked_by": ["schema"]},
  {"title": "Deploy", "blocked_by": ["migrate", 41], "notes": "Needs a maintenance window."}
]}
EOF
```

- The result is small: `ids` (in input order), `refs` (ref → ID), and `folders_created`.
- Everything is checked before anything is written: an `invalid-input` names every bad field as `/tasks/<i>/…`, and a `not-found` every missing ID. Fix and rerun.
- To make an existing task wait on the plan, `block` it afterwards: `koan block 40 --blockers <ids>`.
- A `create-batch` error with IDs in `.error.ids` stopped partway through: the first `len(.error.ids)` tasks of the input were created, in order, and the rest weren't. Don't rerun the whole batch (it would duplicate them). Rerun only the rest, with each ref to a created task replaced by its ID, and tell the user what happened.

Rules the commands enforce:

- **Folder paths are exact**, from the tree's root: `/`, `/proj/api`. Never `proj/api` or `/proj/`, and never derived from the working directory. Segments: letters (either case), digits, hyphens, and case must match exactly (`/Work` is not `/work`). Tags: lowercase letters, digits, hyphens.
- **Titles**: one line, up to 200 characters. Quote them with single quotes. A title starting with `-` goes last, after `--`.
- **Priority**: an integer; **higher** sorts first in `frontier`. `null` means none (sorts after every priority, even negative ones).
- **Notes**: short text with `--notes`, anything longer through a quoted heredoc with `--notes-file -` (no escaping needed).
- `update` changes title, priority, tags (`--tags-add`, `--tags-remove`, `--tags-replace-all`), and `extra` (`--extra-merge`, `--extra-remove key`, `--extra-replace-all`). `--extra-merge` replaces each key's whole value; it does not merge inside nested objects. Blockers change only through `block`/`unblock`; completion only through `done`/`reopen`.

## Moving and deleting

```sh
koan move 42 --to /proj/api                   # a task into a folder
koan move-folder /proj/api --to /archive/api  # --to is the folder's new path, never a folder to put it into
koan delete 42                                # .result.dependents: the tasks it no longer blocks
koan delete-folder /proj/old --recursive      # .result.deleted: what went; .result.dependents: as for delete
```

- Moving never changes blockers: tasks are named by ID, not location. Notes move with the task.
- **Don't move or delete a task someone has claimed** without the user saying so: its `notes_path` changes under whoever is working on it.
- **Deleting is permanent.** koan keeps no trash; the only undo is git, if the user keeps the tree in a repo, and only back to their last commit. So:
  - **Always confirm with the user before `delete` or `delete-folder`**, naming what goes (for a folder, list its tasks first, done ones too: `koan list --folder /x --readiness ready,blocked,done --limit 50 --fields id,title,readiness,claim`). Never delete to tidy up on your own initiative.
  - Prefer cancelling (below) when the user just means "won't do": it keeps the record.
  - A deleted task's ID is removed from its dependents' blockers, so they may become ready. Tell the user which (`dependents` in the output).
  - Deleting a claimed task ends its claim without asking anyone. Say so when a task to be deleted has one.

## Hard rules

- **Never** hand-edit, create, rename, or delete anything under `~/koan-root` named with `@`, except a task's notes `@<id>.md` and a folder's `@folder.md` — use `move`, `move-folder`, `delete`, and `delete-folder`. Task files, folder files, and `@koan.json` belong to koan; a hand edit can break invariants no command will repair. Anything not named with `@` is the user's material, which koan never reads: a `docs/` beside a folder's tasks is fine, but a folder made by hand is material, not a koan folder (`koan create-folder` makes one). The same goes for `~/.config/koan/state.json`.
- Don't pass `--input` unless building input from other JSON, or for `create-batch`, which takes nothing else; flags are clearer.
- Don't create tasks the user didn't ask for. When a follow-up turns up during other work, offer it: "Want me to add a task for X?"

## Conventions

- **Cancelled**: tag it, then mark it done — `koan update 42 --tags-add cancelled && koan done 42`.
- **Waiting on someone/something**: `koan update 42 --extra-merge '{"status":"waiting","on":"vendor quote"}'`; clear with `--extra-remove status --extra-remove on`.
- **Where a task came from**: put links (PR, issue, ticket) in the notes, not the title.
