---
name: koan
description: Track and work through the user's tasks with the koan CLI — a local task tree with dependencies, kept in SQLite, with notes as plain files. Use when the user mentions tasks, todos, their task list, "what should I work on next", blocking or unblocking work, marking something done, starting work in a worktree or finding which folder of work it belongs to, or koan by name; and to offer (not silently create) a task for a follow-up found during other work.
---

# koan

`koan` keeps the user's tasks on this machine, under `~/koan-root`:

- `koan.db`, a SQLite database, is the truth about tasks, folders, the edges between tasks, and claims. Only koan touches it.
- `tree/` holds a directory per folder, and a Markdown notes file per task and per folder, which people and agents edit with any editor. Everything else in a folder's directory is the user's **material** (code, docs), which koan never reads.
- `archived/` holds finished work that was archived.
- `log/` holds old events of the log, moved out of the database by `koan log rotate`.

Every command prints **exactly one line of JSON** on standard output. The examples below run in order on one tree, whose first ID is @40, and the reference files continue it.

`task` and `folder` keep only `create` and `list`, where no reference names the kind. Every other command is its verb alone, since its subject, or the verb itself, says what it acts on: `koan show 42` shows @42 whether it is a task or a folder, `koan update` and `koan delete` take either, and `koan task show 42` is `usage`. So `koan task <verb>` or `koan folder <verb>` is `usage` for every verb but those two: drop the noun. A command that takes both kinds refuses an option the subject's kind doesn't take, such as `--priority` on a folder, as `invalid-input`.

## When to read more

This file covers the daily work. Read a reference file, beside this one, before doing what it covers:

| Before you | Read |
|---|---|
| run `koan export` | [references/export.md](references/export.md) |
| catch up on what changed, or answer a question about history | [references/log.md](references/log.md) |
| bind or unbind a worktree, or a marker looks wrong | [references/worktrees.md](references/worktrees.md) |
| move, delete or archive anything, or look for archived work | [references/moving.md](references/moving.md) |
| run `doctor`, `repair` or `backup`, after a `pending-step` or `stuck-step` warning, or after the user restores a backup | [references/repair.md](references/repair.md) |
| handle an error kind, conflict rule, warning or root state not covered here | [references/errors.md](references/errors.md) |

## Before the first command

```sh
koan info      # .result.state: usable, not-initialized, unsupported-format or corrupt
koan version   # .result.version: v0.13.0 or later is this skill's koan
```

Ready when `.result.state` is `usable`. When it is `not-initialized`, tell the user and suggest `koan init`; **never run `init` unasked.** Any other state, a `version` earlier than `v0.13.0` or without the `v`, or a `usage` error for a command this skill names: tell the user, and see [references/errors.md](references/errors.md). Never move or replace the root yourself.

## Reading output

Every command prints one envelope:

- success: `{"ok":true,"result":{…},"warnings":[…]}`
- failure: `{"ok":false,"error":{"kind":…,"rule":…,"message":…,"ids":[…],"paths":[…],"problems":[…],"archived":[…]},"warnings":[…]}`

Every field is always there; a field with no value is `null` or empty. **Branch on `.error.kind`, never on the exit code or standard error.** `.error.message` is for people: quote it, never parse it.

| Exit | Means | What to do |
|---|---|---|
| 0 | Success, with or without warnings | Read `.result`, and the warnings |
| 1 | Failure: `.error.kind` says how | See below |
| 2 | `usage`: the command line couldn't be read; `.error.problems[0].field` is the argument at fault | Fix the command, taking the hint in `.error.message` when it names a sibling option; `koan --help`, `koan task create --help` |
| 3, or anything else | The outcome is unknown: koan couldn't print, or was killed | Look before running it again, below |

**After an unknown outcome**, check what landed before you rerun. Most writes are safe to rerun: a second `complete` lists nothing in `completed`, and a second `block` changes nothing (`changed: false`). These aren't:

- `task create` and `batch` make new tasks each time: look with `task list` first.
- `claim-next` claims another task: look with `task list --claimed` first.
- `claim` fails `claimed` when your first run took the claim, and koan can't tell your claim from someone else's: ask the user before starting the task. A `claim` of several tasks claims all or none.
- `folder create` fails `exists` when the first run landed.

### Errors and warnings

- `invalid-input`: `.error.problems` lists every bad field, each as a JSON Pointer (`/blocked_by/0`) with a reason. Fix and rerun.
- `not-found`: a reference names nothing; the message says whether it was deleted, archived or never issued, and `.error.archived` says where archived work went.
- `busy`: koan already waited 5 seconds for the lock. Retry once, never in a loop, then tell the user something is holding it.
- `not-initialized`, `unsupported-format`, `corrupt`, `io`, `internal`: stop and report, quoting `.error.message`. Never edit `koan.db` or koan's files to work around them.
- `conflict`: valid input the tree refuses, and `.error.rule` says why. The common ones: `cycle` (the edge would make a task wait on itself; tell the user rather than rearranging their edges), `claimed` (see Claims), `already-complete` (it needs `reopen` first, if the user wants it worked on again) and `case-mismatch` (use the folder's spelling from `.error.paths`; don't rename the folder to match unless asked). Every other rule is in [references/errors.md](references/errors.md).

Warnings never stop a command, and come with failures too. **Always tell the user about them.** Each has a `kind`, a `message`, and the `ids` and `paths` it concerns; [references/errors.md](references/errors.md) says what each kind means.

## The model

- **Folders** hold tasks and folders, nested, under the root `/`. A folder has a path (`/work/api`, exact case), an ID, an optional title, an `extra` object, and notes, `@notes.md` in its directory.
- **Tasks** sit in a folder or the root. A task has an ID, a title, a priority (an integer or `null`), tags, an `extra` object, and notes, `@<id>.md` in its folder's directory. It is `open` or `complete`; complete covers cancelled too.
- **Edges** are between tasks, and nothing else: a task's `blocked_by` lists the tasks it waits on. An open task is `ready` when nothing it waits on is open, and `blocked` otherwise. **Where a task sits never orders it**: a folder holds its tasks without waiting on them, so moving changes no readiness.
- **Gates** order whole folders. A folder can have a start gate, `/work/api.start`, which its work waits on, and a done gate, `/work/api.done`, which waits on its work and which what comes after waits on. Gates are ordinary tasks, made the first time an edge names them: someone completes a gate as they complete any task, and a ready gate is on the frontier like any other. In an edge, a folder with no end means its done gate: `--blocked-by /work/api` is "after /work/api".
- **Joining a folder** is two edges: the task waits on the start gate, and the done gate waits on the task. `--join /work/api` on `task create` writes both, `koan join 42 /work/api` joins a task that exists, and `koan join /work/api/impl /work/api` joins a folder through its gates; `unjoin` removes them. A gate that waits on nothing is ready at once, so write a plan's edges together, in one batch.

### A plan: several tasks at once

When a request breaks into several tasks, make them in **one** `batch`: all of it lands or none of it. Folders are named by path; a task gets a label, such as `$design`, for other parts of the batch to name it by, before or after it. Here every task joins `/work/api` through the batch's `join`, but the task in `/work`, whose `"join": []` opts out, waits for the whole folder:

```sh
koan batch -i - <<'EOF'
{"folders": [{"path": "/work/api", "title": "API rework", "notes": "Move the API to v2.\n"}],
 "folder": "/work/api",
 "join": ["/work/api"],
 "tasks": [
  {"label": "$design", "title": "Design the endpoints", "priority": 2},
  {"label": "$build", "title": "Build them", "blocked_by": ["$design"]},
  {"title": "Announce the new API", "folder": "/work", "blocked_by": ["/work/api"], "join": []}]}
EOF
```

The result names everything by ID: `folders_made`, parents first (`/work` @40, `/work/api` @41); `tasks_made` in input order (@42, @43, @44); `labels`; and `gates_made` (@45 the start gate, @46 the done gate). A batch with a fault makes nothing: `problems` points at the parts at fault, as `/tasks/1/blocked_by/0`. Fix it and rerun the whole batch.

## The daily loop

```sh
koan frontier --tags-none waiting,deferred --limit 10 --fields ref,title   # ready, unclaimed tasks, in work order, parked work left out: only @45, the start gate
koan why 43                                   # .result.ready: @45, what to finish to move @43
koan complete 45                              # .result.released: [42], the tasks it made ready
koan claim-next /work --tags-none waiting,deferred --extra '{"by":"agent-1"}'   # claims @42, the first of the frontier
koan show 42                                  # the whole task: .result.task, with its notes_path
koan complete 42                              # ends the claim; .result.released: [43]
```

- **Work order** is highest `priority` first, `null` last, then lowest ID. Scope the frontier, and `claim-next`, with the folder as the subject, `--direct`, `--tags-any`, `--tags-all` and `--tags-none`. With no folder they look in the whole tree, the root; every list, `context` and `export` do the same. In a worktree, `--here` looks under its folder: `koan frontier --here --tags-none waiting,deferred`.
- **Starting work in a worktree, run `koan context --here` first**, and print it as is: a bare `koan context` briefs the root, never the worktree's folder. It gives the folder, the folders above it, its notes and its open tasks. Before binding one, or when its marker looks wrong, read [references/worktrees.md](references/worktrees.md).
- `why` gives a blocked task's open `blockers`, and in `ready` the ready tasks it waits on at any depth, each with its `id`, `ref` and whether it is `claimed`: what to work on to move it. Don't walk `blocked_by` by hand. It takes no tag filter, so a task it names may be parked: `show` one before starting it.
- **Notes are plain files.** Read and edit a task's at its `notes_path` with your file tools; there is no koan command for notes after creation. A folder's `@notes.md` is where what the work is about goes.
- `complete` reports in `released` the open tasks it made ready, and in `unclaimed` the claims it ended; `reopen` reports in `reblocked` the ready tasks it made wait again. Tell the user about these when they matter.
- **When the last open task in a folder is complete**, or the user says its work has shipped, offer to archive it ([references/moving.md](references/moving.md)). Never archive on your own initiative.

### Claims

Other agents may be working from the same tree. A **claim** reserves a task: no one else can claim it, and it leaves the frontier, until it is unclaimed or completed.

```sh
koan claim 43 --extra '{"by":"agent-1"}'      # this task: .result.claimed is [43]
koan claim 43                                 # a second claim fails: conflict, rule claimed
koan task list --claimed --limit 20 --fields title,claim   # what is taken, by whom, since when
koan unclaim 43                               # you won't do it after all: .result.unclaimed is [43]
```

- **Claim a task before starting it**, whether you picked it or the user named it. `claim-next` picks and claims in one step and returns the task's record in `task`, its `claim` included, so prefer it when the choice is yours, with `--tags-none waiting,deferred` to leave parked work out. Its `task` is `null` when nothing ready is free among what it was asked to look at: that can mean only parked work is left, so check with `koan task list --tags-any waiting,deferred --readiness ready` before saying there's nothing to do. `claim` and `unclaim` take several tasks, as one change.
- **On `claimed`**, someone else has it: pick another if the choice was yours; tell the user if they named it.
- **`complete` ends the claim**, and says so in `unclaimed`. If you stop without finishing, `unclaim` it.
- **A claim is a reservation, not a lock.** It refuses other claims and nothing else, never expires, and koan doesn't know who made it: `claim.extra` is whatever the claimer stored. So the rules are yours to keep: never change, move, complete or unclaim a task someone else has claimed, or edit its notes, unless the user says so. Whoever holds the claim edits the notes.
- **A claimed task can be blocked again** by a new edge or a reopened blocker, and the claim stays: the change warns `claimed-blocked`. A blocked task can be claimed too, to reserve it ahead.

### If a coordinator started you with one task

When another agent started you with a task's ID and notes path, you are a worker: edit **only that task's notes file**, run only koan's reads (`show`, `task list`, `frontier`, `why`, `folder list`, `where`, `context`, `export`, `log list`, `info`), and put everything you want changed, follow-ups and the task being complete included, in your final report. The coordinator makes those changes. Never `bind` or `unbind`: report a stale or broken marker to the coordinator.

## Writing

```sh
koan task create 'Write the migration' --folder /work/api --priority 1 --tags db --blocked-by 42 --blocks /work/api
koan task create 'Investigate the flaky test' --notes-file - <<'EOF'
Seen in CI on 2026-10-19; fails about 1 run in 20.
EOF
koan update 48 --priority null --tags-add waiting --extra-merge '{"waiting":{"on":"vendor quote"}}'
koan block 44 --blocked-by 48              # @44 waits on @48 too
koan unblock 44 --blocked-by 48
koan block 42 --blocked-by 43              # refused: conflict, rule cycle, since @43 waits on @42
```

- **A write returns identities, not records**: `.result.id`, or the IDs it changed, such as `completed`, `updated`, `claimed`, `moved`, `deleted` and `removed`, and what else it touched, such as `released`, `reblocked` and `gates_made`, or `changed: false` when it changed nothing. `show` it when you need its fields.
- `task create` puts the task in an existing folder, `--folder`, or the root. It never makes a folder: a path that names none is `not-found`.
- What the task waits on is `--blocked-by`, and `--blocks` lists what waits on it, on `task create`, `block` and `unblock`: `block` and `unblock` take the task as one word and at least one of the two, so `koan block 44 48` is `usage`. `unblock` of a gate that was never made changes nothing (`changed: false`). Both take tasks (`42`, `@42`), gates (`/work/api.start`, `@41.done`), and folders, which mean their done gate. They make any gate they name, and say so in `gates_made`.
- **Titles** are one line, up to 200 characters; quote them with single quotes, and put one starting with `-` after `--`. **Tags** are lowercase letters, digits and hyphens. **Priority** is an integer, higher first; `null` clears it. Option lists are comma-separated or repeated, and `null` is written `null`. Where a command takes several tasks or folders, as `complete`, `update`, `mv` and `delete` do, give them as more words or comma-separated: `koan complete 41 42` is `koan complete 41,42`.
- `update` changes the title, priority, tags (`--tags-add`, `--tags-remove`, `--tags-replace-all`) and `extra` (`--extra-merge`, a JSON Merge Patch: an object merges in at any depth, `null` removes, anything else, arrays included, replaces whole; `--extra-remove`, whole namespaces, like tags a list), of one task or folder or several, as one change. Priority and tags are for tasks only: a folder among the subjects refuses them as `invalid-input`. `--title null` clears a folder's title, and a task's is refused. Edges change only through `block` and `unblock`, `join` and `unjoin`, and `--join`; state only through `complete` and `reopen`.
- Notes: short text with `--notes`, anything longer through a quoted heredoc with `--notes-file -`.
- `-i <file>` (or `-i -`) gives the whole input as JSON instead of options. Use it for `batch`, which takes nothing else, or when building input from other JSON.

### Folders

```sh
koan folder create /work/web/client --title 'Web client'    # makes /work/web too: .result.folders_made lists both
koan update /work/web/client --extra-merge '{"jira":"WEB-12"}'
koan show /work/api                                         # .result.folder: gates, open, complete, finished, notes_path
koan task create 'Port the client' --folder /work/web/client --blocked-by /work/web/client.start --blocks /work/web/client
koan block /work/web/client.start --blocked-by /work/api    # the client's work starts once the API is done
```

- `folder create` makes any missing parents, and lists every folder made in `folders_made`. It refuses a path that is taken (`exists`) or that differs from a sibling only in case (`case-mismatch`).
- **Paths are exact**, from the root: `/work/api`, never `work/api` or `/work/api/`. Folder names are letters, digits and hyphens, and case must match: `/Work` is not `/work`.
- A folder's record has `gates` (each `null` until made), `open` and `complete` (every task under it, at any depth, gates included), and `finished`, true when nothing under it is open.

### References

```sh
koan where @41:docs/plan.md    # material in folder @41: .result.file is its path on disk, which need not exist
koan where /work/api.done      # a gate: the task @46, with its notes file
```

A task or folder is `42` or `@42`; a gate is `@41.start` or `/work/api.done`; a folder is also its path, or a path from an ID, `@40/api`; material in a folder is `@41:docs/plan.md` or `/work/api:docs/plan.md`. koan always writes the `@`.

**When you write a reference down**, in notes or in a prompt for another agent, write koan's **kept text**: the ID with the path as a hint, `@42 (/work/api)`, `@46 (/work/api.done)`. Every record's `ref`, and `where`'s, gives it. IDs survive moves and renames; paths don't. **When you read one**, the ID decides: resolve it with `koan where`, and if the path no longer matches, say the hint is stale.

**Never put koan references in commit messages, code, pull requests or other repositories' files.** An ID means something only in this tree. Name the work there in words.

## Keep output small

Everything koan prints lands in your context. **Pass `--limit` and `--fields` to `task list`, `frontier` and `folder list`**, and widen only when the question needs it:

```sh
koan task list --limit 20 --fields ref,title,readiness           # open tasks, in tree order
koan task list --state complete --limit 1 --fields id            # how many are complete: .result.total
koan folder list /work --direct --fields path,title,open,finished
```

- `--fields` gives `id` first, then the fields named, in the record's order. Task fields: `ref`, `gate`, `title`, `priority`, `folder`, `created_at`, `completed_at`, `updated_at`, `state`, `readiness`, `blocked_by`, `blocks`, `tags`, `extra`, `claim`, `notes_path`. Folder fields: `ref`, `name`, `path`, `title`, `extra`, `gates`, `open`, `complete`, `finished`, `notes_path`, `dir`.
- `--limit` defaults to 100, and `--limit null` lists everything. The result always has `total` and `truncated`: when `truncated` is true you got the first `--limit` of `total`. Say so rather than presenting them as all.
- `task list` filters by `--state open,complete` (open by default), `--readiness ready,blocked`, `--claimed` or `--claimed=false`, `--tags-any a,b`, `--tags-all a,b`, `--tags-none a,b`, and the folder as the subject, with `--direct` for only its own tasks, or `--here` for the folder the working directory is in.
- `show` gives one whole record, `.result.task` or `.result.folder` by what the reference names. A record is exact only as of the call.

**With `jq`, a failure looks like success**: the pipe's status is jq's, and the error vanishes. Never trim a write's or a show's envelope: print it as is. koan filters on what it models: where work is, where it stands and its tags. Everything else is `jq`'s. When a big read needs `jq`, keep the envelope and shape only the result:

```sh
koan task list --fields ref,title --limit null | jq -c 'if .ok then .result.tasks |= map(select(.title | test("migration"; "i"))) else . end'   # titles containing a word, ignoring case
koan task list --claimed --fields ref,claim --limit null | jq -c 'if .ok then .result.tasks |= map(select(.claim.claimed_at < "2026-11-01T00:00:00Z")) else . end'   # claims older than a time: koan's timestamps sort as text
koan task list --fields ref,extra --limit null | jq -c 'if .ok then .result.tasks |= map(select(.extra.jira.project? == "WEB")) else . end'   # a value in extra
```

These look at open tasks; add `--state open,complete` for complete ones too. `--limit null` lets the filter see every task, and `total` stays the count before it. With a lower `--limit`, `truncated` true means the filter never saw `total` minus the limit of them: say how many.

**`koan export` is for tools, not for reading**: it has no `--limit` and no `--fields`, and at 10,000 tasks it is megabytes, so never print it into your context. Before running it, read [references/export.md](references/export.md).

## Hard rules

- **Never touch koan's entries by hand**: anything in the tree named with `@` (folder directories, `@<id>.md`, `@start.md`, `@done.md`, `@notes.md`) is created, renamed, moved and removed only by koan. Editing the *contents* of notes is fine. Never touch `koan.db`, its `-wal` and `-shm`, `archived/` or `log/`. A directory made by hand is material, not a folder: `koan folder create` makes one.
- **Never run `init`, `repair` or `delete` unasked.** They ask for permission anyway. Nor `log rotate`, which doesn't ask, nor `archive`: offer it.
- **Before any `delete`, confirm with the user, naming what goes**, complete tasks too: deleting is permanent. Never add `--contents` or `--material` on your own, and never complete open tasks to get an `archive` past `not-finished`: ask.
- **Don't create tasks the user didn't ask for.** When a follow-up turns up during other work, offer it: "Want me to add a task for X?"
- Prefer options to `-i`, except for `batch`.

## Conventions

- **Cancelled**: tag it, then complete it: `koan update 48 --tags-add cancelled && koan complete 48`. Before completing a task that didn't succeed, check what waits on it (`koan show <id>`, the entries in `blocks` still `open`). If anything does, ask the user whether that work should still go ahead, or wait on something new first: completing stops it blocking that work either way.
- **Parked work is a tag**, so that choosing work can leave it out with `--tags-none waiting,deferred`, as the daily loop does.
  - **Waiting on someone**: the `waiting` tag, with any detail under a `waiting` namespace in `extra`: `koan update 48 --tags-add waiting --extra-merge '{"waiting":{"on":"vendor quote"}}'`; clear it with `--tags-remove waiting --extra-remove waiting`. `koan task list --tags-any waiting` shows what is waiting.
  - **Held back until later**: the `deferred` tag, typically set and cleared by an outside tool such as a reminder program.
- **`extra` belongs to programs, one namespace each.** Its top-level keys are namespaces, named like tags (`koan` is reserved); under its own name a program keeps whatever it likes, which koan never reads. Nest everything it needs there, and change a field with a patch, so other programs' namespaces stay as they are: `--extra-merge '{"remind":{"at":"2026-11-09T09:00:00Z"}}'`.
- **Where a task came from**: links to the PR, issue or ticket go in its notes, not its title.
