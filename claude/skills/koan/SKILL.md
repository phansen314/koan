---
name: koan
description: Track and work through the user's tasks with the koan CLI — a local task tree with dependencies, kept in SQLite, with notes as plain files. Use when the user mentions tasks, todos, their task list, "what should I work on next", blocking or unblocking work, marking something done, starting work in a worktree or finding which folder of work it belongs to, or koan by name; and to offer (not silently create) a task for a follow-up found during other work.
---

# koan

`koan` keeps the user's tasks on this machine, under `~/koan-root`:

- `koan.db`, a SQLite database, is the truth about tasks, folders, the edges between tasks, and claims. Only koan touches it.
- `tree/` holds a directory per folder, and a Markdown notes file per task and per folder, which people and agents edit with any editor. Everything else in a folder's directory is the user's **material** (code, docs), which koan never reads.
- `archived/` holds finished work that was archived.

Every command prints **exactly one line of JSON** on standard output. The examples below run in order on one tree, whose first ID is @40.

`task` and `folder` keep only `create` and `list`, where no reference names the kind. Every other command is its verb alone, since its subject, or the verb itself, says what it acts on: `koan show 42` shows @42 whether it is a task or a folder, `koan update` and `koan delete` take either, and `koan task show 42` is `usage`. So `koan task <verb>` or `koan folder <verb>` is `usage` for every verb but those two: drop the noun. A command that takes both kinds refuses an option the subject's kind doesn't take, such as `--priority` on a folder, as `invalid-input`.

## Before the first command

```sh
koan info      # .result.state: usable, not-initialized, unsupported-format or corrupt
koan version   # .result.version: v0.12.0 or later is this skill's koan
```

Ready when `.result.state` is `usable`. Otherwise `.result.error` is what every other command fails with:

- `not-initialized`: there is no root, or no database in it. Tell the user and suggest `koan init`. **Never run `init` unasked.** If `init` fails with `conflict`, rule `not-a-root`, `~/koan-root` holds files that aren't this koan's, often a tree from the original koan (an `@koan.json`): the user must move it aside first. Never move it yourself.
- `unsupported-format`: the database is in a format this binary doesn't read; the user needs a matching koan.
- `corrupt`: report it; the user restores from a backup (see below).

`version` printing `v0.12.0` or later, a pseudo-version (`v0.0.0-…`) or `(devel)` is this koan. An earlier `v` release lacks commands this skill names, and anything else, such as `0.3.0` without the `v`, is the original koan, which works differently: either way, tell the user to upgrade with `go install github.com/phansen314/koan/cmd/koan@latest`. A `usage` error for a command this skill names means the same.

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

### Errors

- `invalid-input`: `.error.problems` lists every bad field, each as a JSON Pointer (`/blocked_by/0`) with a reason. Fix and rerun. A reference to the wrong kind of thing is this too, such as a gate as `join`'s or `mv`'s subject, a task where a folder is taken, or a field the subject's kind doesn't take, with the subject's ID in `.error.ids`; so is a missing subject, as `koan complete` alone, and a subject given twice. The same name twice in a set, such as `--tags-add a,a`, counts once.
- `not-found`: a reference names nothing; one that names something of the wrong kind is `invalid-input`. `.error.ids` or `.error.paths` is the first one, `.error.problems` points at its option or field, and the message says whether it was deleted, archived or never issued. When it was archived, `.error.archived` says where it went: each item has `id`, `notes_path` and `dir`, as a move's does.
- `busy`: the lock stayed held for 5 seconds; koan already waited. It is always safe to retry, but not in a loop: retry once, then tell the user something is holding it.
- `not-initialized`, `unsupported-format`, `corrupt`, `io`, `internal`: stop and report, quoting `.error.message`. Never edit `koan.db` or koan's files to work around them.
- `conflict`: valid input the tree refuses. `.error.rule` says why:
  - `cycle`: the edge would make a task wait on itself; `.error.ids` is the cycle, in order. Tell the user rather than rearranging their edges.
  - `claimed`: someone holds a claim on one of the tasks (see Claims). `already-complete`: claiming a complete task; it needs `reopen` first, if the user wants it worked on again.
  - `case-mismatch`: a path matched a folder only ignoring case; `.error.paths` has the path as given, then as it is. Use the folder's spelling. Don't rename the folder to match unless asked.
  - `exists`: the destination is taken: a folder path for a create or a move, an entry on disk there, or a non-empty directory for a backup. `.error.problems` points at the option, and `.error.ids` has the folder that holds the path.
  - `not-finished`: archiving work that isn't complete; `.error.ids` is the open tasks. Ask the user; never complete them to get past it.
  - `not-empty`, `has-material`: a `delete` of folders without `--contents` or `--material`. Never add them on your own (see Moving, deleting and archiving).
  - `into-itself`: a folder can't move inside itself.
  - `checkout`, `bound`, `foreign-files`: `bind` refused the directory (see Worktrees).
  - `not-a-root`: `init` found files that aren't koan's (above).
  - `no-ids`: the root has issued its last ID. Tell the user.

### Warnings

Warnings never stop a command, and come with failures too. **Always tell the user about them.** Each has a `kind`, a `message`, and the `ids` and `paths` it concerns.

- `claimed-blocked`: the change made a claimed task wait again. Tell whoever holds the claim.
- `moved-claimed`: a move changed a claimed task's notes path under its holder.
- `complete-blocker`: an edge was added to a complete task's `blocked_by`. It doesn't reopen the task, so what waits on it isn't held back.
- `stale-marker`: a worktree's marker has an old path hint; `bind` again refreshes it.
- `missing-notes`: a notes file is missing; it reads as empty.
- `pending-step`, `stuck-step`, `pending-filesystem`: the tree on disk is behind the database. A pending step is finished by the next write; a stuck one needs the user (see When the tree is damaged).

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
koan frontier --limit 10 --fields ref,title   # ready, unclaimed tasks, in work order: only @45, the start gate
koan why 43                                   # .result.ready: @45, what to finish to move @43
koan complete 45                              # .result.released: [42], the tasks it made ready
koan claim-next /work --extra '{"by":"agent-1"}'   # claims @42, the first of the frontier
koan show 42                                  # the whole task: .result.task, with its notes_path
koan complete 42                              # ends the claim; .result.released: [43]
```

- **Work order** is highest `priority` first, `null` last, then lowest ID. Scope the frontier, and `claim-next`, with the folder as the subject, `--direct`, `--tags-any` and `--tags-all`. With no folder they look in the whole tree, the root; every list, `context` and `export` do the same. In a worktree, `--here` looks under its folder: `koan frontier --here`.
- `why` gives a blocked task's open `blockers`, and in `ready` the ready tasks it waits on at any depth, each with its `id`, `ref` and whether it is `claimed`: what to work on to move it. Don't walk `blocked_by` by hand.
- **Notes are plain files.** Read and edit a task's at its `notes_path` with your file tools; there is no koan command for notes after creation. A folder's `@notes.md` is where what the work is about goes.
- `complete` reports in `released` the open tasks it made ready, and in `unclaimed` the claims it ended; `reopen` reports in `reblocked` the ready tasks it made wait again. Tell the user about these when they matter.

### Claims

Other agents may be working from the same tree. A **claim** reserves a task: no one else can claim it, and it leaves the frontier, until it is unclaimed or completed.

```sh
koan claim 43 --extra '{"by":"agent-1"}'      # this task: .result.claimed is [43]
koan claim 43                                 # a second claim fails: conflict, rule claimed
koan task list --claimed --limit 20 --fields title,claim   # what is taken, by whom, since when
koan unclaim 43                               # you won't do it after all: .result.unclaimed is [43]
```

- **Claim a task before starting it**, whether you picked it or the user named it. `claim-next` picks and claims in one step and returns the task's record in `task`, its `claim` included, so prefer it when the choice is yours. Its `task` is `null` when nothing ready is free. `claim` and `unclaim` take several tasks, as one change.
- **On `claimed`**, someone else has it: pick another if the choice was yours; tell the user if they named it.
- **`complete` ends the claim**, and says so in `unclaimed`. If you stop without finishing, `unclaim` it.
- **A claim is a reservation, not a lock.** It refuses other claims and nothing else, never expires, and koan doesn't know who made it: `claim.extra` is whatever the claimer stored. So the rules are yours to keep: never change, move, complete or unclaim a task someone else has claimed, or edit its notes, unless the user says so. Whoever holds the claim edits the notes.
- **A claimed task can be blocked again** by a new edge or a reopened blocker, and the claim stays: the change warns `claimed-blocked`. A blocked task can be claimed too, to reserve it ahead.

### If a coordinator started you with one task

When another agent started you with a task's ID and notes path, you are a worker: edit **only that task's notes file**, run only koan's reads (`show`, `task list`, `frontier`, `why`, `folder list`, `where`, `context`, `export`, `info`), and put everything you want changed, follow-ups and the task being complete included, in your final report. The coordinator makes those changes.

## Writing

```sh
koan task create 'Write the migration' --folder /work/api --priority 1 --tags db --blocked-by 42 --blocks /work/api
koan task create 'Investigate the flaky test' --notes-file - <<'EOF'
Seen in CI on 2026-10-19; fails about 1 run in 20.
EOF
koan update 48 --priority null --tags-add urgent --extra-merge '{"status":"waiting"}'
koan block 44 --blocked-by 48              # @44 waits on @48 too
koan unblock 44 --blocked-by 48
koan block 42 --blocked-by 43              # refused: conflict, rule cycle, since @43 waits on @42
```

- **A write returns identities, not records**: `.result.id`, or the IDs it changed, such as `completed`, `updated`, `claimed`, `moved` and `removed`, and what else it touched, such as `released`, `reblocked` and `gates_made`, or `changed: false` when it changed nothing. `show` it when you need its fields.
- `task create` puts the task in an existing folder, `--folder`, or the root. It never makes a folder: a path that names none is `not-found`.
- What the task waits on is `--blocked-by`, and `--blocks` lists what waits on it, on `task create`, `block` and `unblock`: `block` and `unblock` take the task as one word and at least one of the two, so `koan block 44 48` is `usage`. `unblock` of a gate that was never made changes nothing (`changed: false`). Both take tasks (`42`, `@42`), gates (`/work/api.start`, `@41.done`), and folders, which mean their done gate. They make any gate they name, and say so in `gates_made`.
- **Titles** are one line, up to 200 characters; quote them with single quotes, and put one starting with `-` after `--`. **Tags** are lowercase letters, digits and hyphens. **Priority** is an integer, higher first; `null` clears it. Option lists are comma-separated or repeated, and `null` is written `null`. Where a command takes several tasks or folders, as `complete`, `update`, `mv` and `delete` do, give them as more words or comma-separated: `koan complete 41 42` is `koan complete 41,42`.
- `update` changes the title, priority, tags (`--tags-add`, `--tags-remove`, `--tags-replace-all`) and `extra` (`--extra-merge`, top level only; `--extra-remove key`, once per key; `--extra-replace-all`), of one task or folder or several, as one change. Priority and tags are for tasks only: a folder among the subjects refuses them as `invalid-input`. `--title null` clears a folder's title, and a task's is refused. Edges change only through `block` and `unblock`, `join` and `unjoin`, and `--join`; state only through `complete` and `reopen`.
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
- `task list` filters by `--state open,complete` (open by default), `--readiness ready,blocked`, `--claimed` or `--claimed=false`, `--tags-any a,b`, `--tags-all a,b`, and the folder as the subject, with `--direct` for only its own tasks, or `--here` for the folder the working directory is in.
- `show` gives one whole record, `.result.task` or `.result.folder` by what the reference names. A record is exact only as of the call.

**With `jq`, a failure looks like success**: the pipe's status is jq's, and the error vanishes. Never trim a write's or a show's envelope: print it as is. When a big read needs `jq`, keep the envelope and shape only the result, and when the filter drops tasks, say how many it didn't see:

```sh
koan task list --fields title,extra --limit 200 | jq -c 'if .ok then .result |= {tasks: (.tasks | map(select(.extra.status == "waiting"))), unscanned: (.total - (.tasks | length))} else . end'
```

**`koan export` is for tools, not for reading.** `koan export /work/api` (or `--file`, or `--here` in a worktree, as for `context`; with none of them, the root) gives the folder's whole subtree in one snapshot: its folders, every task under it, open and complete, gates included, as `tasks`; the tasks outside it at the other end of an edge as `external`; and the `edges` as `{task, blocker}`. It has no `--limit` and no `--fields`, and at 10,000 tasks it is megabytes, so never print it into your context: write it to a file, or pipe it into the tool that draws the graph. To read the tree, use `task list` with `--limit` and `--fields`.

```sh
koan export /work/api > api.json   # the whole subtree, into a file
jq .ok api.json                     # true; when false, .error says why
```

## Worktrees: markers and context

A **marker** binds a directory outside the tree, usually a git worktree, to a folder, so koan can tell which folder of work you are in from anywhere inside it. It is a `.koan/` directory there, which git ignores by itself.

```sh
koan bind @41 ~/code/api-v2                        # right after git worktree add; <dir> defaults to the working directory
cd ~/code/api-v2 && koan context --here            # the brief: the folder, the folders above it, its notes, its open tasks
koan where --file ~/code/api-v2/src/main.go        # which folder a file is in: .result.ref is @41 (/work/api)
koan mv -T /work/api /work/api2                    # a rename: the marker still binds @41, by ID
koan where --file ~/code/api-v2                    # still @41, with a stale-marker warning
koan bind @41 ~/code/api-v2                        # refreshes the hint: .result.replaced and .result.changed are true
koan mv -T /work/api2 /work/api
```

- **Starting work in a worktree, run `koan context --here` first**, and print it as is. Without `--here` it briefs the root: `.result.folder` is the folder, `.result.ancestors` the folders above it (top first), `.result.notes` its `@notes.md` whole, `.result.tasks` its own open tasks (not its subfolders'), and `.result.marker` the marker it came by. `tasks` is limited like a list: `--limit` (100 by default, `null` for all), with `total` and `truncated`. `koan context <folder>` briefs any folder, and `--file <path>` the folder a file is in. `export` and `where` take `--here` and `--file` too.
- **Check the marker is the worktree's own**: in a git worktree it should be `$(git rev-parse --show-toplevel)/.koan/marker.json`. A marker above the worktree means it was never bound and is borrowing an outer folder: say so, rather than working in that folder.
- **Bind worktrees from their top, never the checkout that holds them**: a marker binds every directory under it. `bind` refuses a checkout, a directory whose `.git` is a directory (`checkout`), and a directory bound to another live folder (`bound`); `--force` overrides either, so ask the user first. It refuses a `.koan/` holding files that aren't koan's (`foreign-files`), and any directory under `~/koan-root`.
- A marker copied or moved with its directory, or whose folder was deleted or archived, binds nothing: `where`, `context` and `export` fail `not-found` and say why. Binding again fixes a moved one. `koan unbind <dir>` removes one; `bind` and `unbind` both return `dir`, `marker` and `changed`, false when there was nothing to write or remove; removing the worktree removes it too.
- **As a worker**, run `context` and `where`, never `bind` or `unbind`: report a stale or broken marker to the coordinator.

## Moving, deleting and archiving

```sh
koan mv -t /work/api 48                          # its notes move with it; .result.moved[0].notes_path is the new path
koan mv -T /work/web/client /work/client         # -T: the new path; -t: the folder to move into
koan task list /work/client --state open,complete --fields title,state,claim   # before deleting: what goes
koan delete /work/client                         # refused without --contents: conflict, rule not-empty
koan delete /work/client --contents              # only once the user said so: .result.deleted, .result.removed, .result.released
```

- Always pass `-t` or `-T` to `mv`, so what you type never depends on what exists.
- koan paths are never globbed: a shell expands `/work/old/*` against the disk. Name each folder, or use completion or `$(koan pick)`.
- Moving never changes an edge, so it never changes readiness. Don't move a task someone has claimed: its notes path changes under them, and the move warns `moved-claimed`. A gate never moves.
- **Deleting is permanent**, and there is no undo but a backup. **Always confirm with the user before `delete`**, naming what goes: list the folder's tasks first, complete ones too. Never delete to tidy up on your own initiative. Prefer cancelling (below) when the user means "won't do", and archiving when the work is finished: both keep the record.
- `delete` of a folder without `--contents` is refused (`not-empty`) when the folder holds tasks or folders beyond its gates, and without `--material` (`has-material`) when it holds the user's files: `.error.paths` lists them, such as `/work/client:docs`. **Never add `--contents` or `--material` on your own**: show the user what would go and ask. `--material` deletes their files for good.
- `deleted` is the subjects, and `removed` everything that left, their contents too. A delete removes every edge to what went, so what waited on it may become ready: `released` says which. Tell the user. Deleting a claimed task ends its claim without asking anyone, and `unclaimed` says which.

### Archiving finished work

Archiving moves complete work out of the tree into `~/koan-root/archived/`, kept as files with a JSON of its fields beside it, so the tree stays small. There is no unarchive.

```sh
koan archive /work/api                              # refused while anything under it is open: conflict, rule not-finished
koan update 48 --tags-add cancelled && koan complete 48 && koan archive 48   # one task: .result.archived[0].json
koan show 48                                        # not-found: .error.archived says where it went
```

- **When the last open task in a folder is complete**, or the user says its work has shipped, offer to archive it. Never archive on your own initiative. A folder archives when every task under it is complete, gates included; otherwise `not-finished` lists the open tasks in `.error.ids`: ask the user, don't complete them to get past it.
- `archive` takes several tasks and folders, as one change. `.result.archived` says where each went, in a move's shape plus its `json`, and `.result.removed` is every ID that left the tree. They never resolve again: a command naming one fails `not-found`, saying where it went. A worktree bound to an archived folder binds nothing: mention it, so the user can remove the worktree.
- **Finding archived work**: search it with `rg '<text>' ~/koan-root/archived`. Each item keeps its ID in its name, such as `archived/work/@41_api/` for a folder or `archived/work/api/@48.md` for a task, with its fields in the `.json` beside it. Read archived files; never change them. To pick the work up again, make new tasks from what you find.

## When the tree is damaged

```sh
koan doctor      # .result.findings: what is wrong, and whether repair can fix it; changes nothing
koan repair      # only when the user says so: fixes what loses nothing, and reports fixed and left
```

After a `pending-step` or `stuck-step` warning, a `corrupt` error, or the user renaming or dragging folders by hand, run `koan doctor` once and summarize each finding's `kind`, `ids`, `paths` and `message`, which says what to do.

- Findings with `repairable: true` are what `koan repair` fixes: it moves a folder renamed or dragged by hand back, makes a missing directory, fixes a directory's case, moves notes back, and removes empty orphan notes. It never changes the database to match the files. **Never run `repair` unasked**: offer it, saying what it would change. `koan repair --kinds case-differs,misplaced-notes` limits it.
- `stuck-step`, `stray-entry`, `invalid-row` and `corrupt` are the user's: pass on the message. A stuck step needs a person to keep one copy, or move material out, before `repair` retries it.
- **Backups**: `koan backup <dir>` copies everything into an empty or new directory and checks the copy. Restoring is the user's: putting a backup in place of `~/koan-root`, then `koan doctor`.

```sh
koan backup ~/backups/koan-$(date +%F)     # .result.clean is true when the copy checks out
```

## Hard rules

- **Never touch koan's entries by hand**: anything in the tree named with `@` (folder directories, `@<id>.md`, `@start.md`, `@done.md`, `@notes.md`) is created, renamed, moved and removed only by koan. Editing the *contents* of notes is fine. Never touch `koan.db`, its `-wal` and `-shm`, or `archived/`. A directory made by hand is material, not a folder: `koan folder create` makes one.
- **Never run `init`, `repair` or `delete` unasked.** They ask for permission anyway.
- **Don't create tasks the user didn't ask for.** When a follow-up turns up during other work, offer it: "Want me to add a task for X?"
- Prefer options to `-i`, except for `batch`.

## Conventions

- **Cancelled**: tag it, then complete it: `koan update 48 --tags-add cancelled && koan complete 48`.
- **Waiting on someone**: `--extra-merge '{"status":"waiting","on":"vendor quote"}'`; clear it with `--extra-remove status --extra-remove on`.
- **Where a task came from**: links to the PR, issue or ticket go in its notes, not its title.
