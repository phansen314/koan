---
name: koan
description: Track and work through the user's tasks with the koan CLI — a local, file-based task tree with dependencies. Use when the user mentions tasks, todos, their task list, "what should I work on next", blocking or unblocking work, marking something done, or koan by name; and to offer (not silently create) a task for a follow-up found during other work.
---

# koan

`koan` keeps the user's tasks as files under one root directory per machine. Tasks live in folders, can block each other, and each has a notes file. Every command prints **one line of JSON** and nothing else.

## Before the first command

```sh
koan info
koan version
```

Ready when `.result.usable` is true; if not, the rest of `.result` says why. When `.result.migration_pending` is true, the tree needs `koan migrate` after a koan upgrade: see `migration-pending` below. If `version`'s `.result.version` is below `0.2.0` (`0.0.0-dev` is a build from source: treat it as current), the binary is older than this skill: tell the user to upgrade with `go install github.com/phansen314/koan/cmd/koan@latest`. A `usage` error saying `unknown command`, or `unknown flag`, for a command or option this skill names means the same. When no root is set up, tell the user and suggest `koan init ~/tasks` (or a path they choose). **Never run `init` unasked** — it changes this machine's setup. If `init` fails with `conflict` and `rule: "config-exists"`, report it; never pass `--replace-config` unless the user asks for it.

## Reading output

Every command writes one envelope:

- success: `{"ok":true,"result":{…},"warnings":[…]}`
- failure: `{"ok":false,"error":{"kind":"…","message":"…","details":{…}},"warnings":[…]}`

Branch on `.error.kind`, not the exit code. **Always tell the user about any `warnings`** (unusable files, dangling blockers, duplicate IDs): they mean the tree needs attention.

| Exit | Meaning | What to do |
|---|---|---|
| 0 | Success | — |
| 1 | Operation error; see `.error.kind` | See below |
| 2 | Usage error (bad command line) | Fix the command; check `koan <cmd> --help` |
| 3 or other | Outcome unknown (killed, stdout lost) | Reads: rerun. `create` and `create-batch`: check with `list` before rerunning, or they may duplicate. `claim-next`: check with `koan list --claimed --limit 20 --fields id,title,claim` first, or it may claim a second task. `claim`: a rerun that fails with `claimed` may be your own claim from the first run; see Claims. Either with `--use-token`: if the first run succeeded, its token is lost; see Claims. Other writes are safe to rerun. |

Error kinds worth handling:

- `busy` — the tree's write lock stayed held for 5 seconds; koan already waited. Don't retry in a loop: tell the user something is holding the lock (a `doctor`, `repair`, or `migrate` on a very large tree, or a stuck `koan` process), and retry once they say so.
- `not-found` — `.error.details.ids` / `.folders` name what's missing. Folders are created only by `create-folder`, `-p`, and `create-batch`.
- `invalid-input` — `.error.details.problems[]` lists every bad field.
- `conflict` with `rule: "acyclic"` — the block would make a cycle; `.error.details.cycles` shows it.
- `conflict` with `rule: "not-empty"` — `delete-folder` without `-r` on a folder that holds tasks, folders, or other files. Don't add `-r` on your own: ask the user.
- `conflict` with `rule: "destination-exists"` — `move-folder` would land on a folder that already exists (folders are never merged), or `move` would overwrite a `.md` with text in the target folder; show it to the user.
- `conflict` with `rule: "case-clash"` — a folder path differs only in case from an existing folder (e.g. `/Work` where `/work` exists); `.error.message` names it. Use the existing folder's exact name.
- `conflict` with `rule: "duplicate-id"` or `"id-above-last-id"` — the tree is damaged; check it (below), don't work around it.
- `conflict` with `rule: "claimed"` — someone has claimed the task; `.error.details.claims[0]` is their claim (`claimed_at`, `extra`, `locked`). See Claims.
- `conflict` with `rule: "token-mismatch"` — the task is locked and the write lacks its token, or you passed a token the task's claim doesn't have. See Claims.
- `conflict` with `rule: "task-done"` — `claim` on a done task; `reopen` it first if the user wants it worked on again.
- `not-initialized` — `.error.details.missing` says what is absent. `config` or `root`: nothing is set up here; see "Before the first command". `metadata`: the tree lost its `koan.json`. `state`: this machine has no ID counter for the tree. For the last two, `init` is not the fix: report it, offer `koan doctor`, and see the `repair --kinds` rule below.
- `migration-pending` — koan was upgraded and the tree's files are in an older format; every command that reads or changes tasks refuses it. Stop and tell the user to run `koan migrate`, committing the tree first if it's a git repository. **Never run `migrate` unasked**: it rewrites every task file and holds the write lock while it runs.
- `corrupt`, `unsupported-format`, `io`, `internal` — stop and report to the user, quoting `.error.message` (for `corrupt` it names what is wrong); don't try to fix files by hand. For `unsupported-format`, compare `.error.details.found` with `.error.details.supported`: above it (or with `.error.details.field == "migration"`), a newer koan wrote the file and the user needs to upgrade this machine's binary; below it, the file is in an older format, and `koan migrate` converts it.

## When the tree is damaged

After a `duplicate-id`, `id-above-last-id`, or `corrupt` error, or a `dangling-reference` warning, run `koan doctor` — once per session, not after every command — and summarize `.result.findings` for the user: each finding's `kind`, its `items` (`paths`, `ids`), and its `suggest`. `doctor` changes nothing, and takes the write lock while it runs, so other writes wait for it.

- Findings with a non-null `action` are what `koan repair` would fix. **Never run `repair` unasked**: offer it, saying what it would change. It asks for permission anyway.
- **Never run `repair --kinds state-missing` or `--kinds metadata-missing` unless the user explicitly agrees**: rebuilding this machine's ID counter can reissue the ID of a task that was deleted, and rebuilding `koan.json` declares the directory a koan tree.
- **Never run `repair --kinds claims-unusable` unless the user explicitly agrees** either: it replaces an unusable claims file with an empty one, forgetting every claim and lock on this machine. Until the file is fixed, claims and every write to a task fail with `corrupt`, `unsupported-format` or `io` naming `claims.json`, and reads warn `unusable-file` for it and show every task as unclaimed: report it, and offer `koan doctor`.
- Findings with `action: null` (duplicate IDs, cycles, unusable files, orphaned notes that may hold text) are the user's to resolve. Pass on `suggest`; don't edit files to fix them yourself.
- `migration-pending` and `old-format` findings are fixed by `koan migrate`, never by `repair`: pass that on, as for the `migration-pending` error above.
- Files koan ignores — the user's own, an editor's backups — are `stray-entry`, listed only by `koan doctor --kinds stray-entry`. They are not damage: ask for them only when the user wants to tidy up.
- `--kinds` filters only `findings`: `healthy` still reflects the whole tree, so `healthy: false` with no findings listed means damage of a kind you didn't ask for.

## Keep output small

Everything koan prints lands in your context, and stays there for the rest of the session. A whole task is about 300 bytes, so a bare `koan list` of a few hundred tasks is tens of KB. **Always pass `--limit` and `--fields` to `frontier` and `list`**, as below, and widen only when the question needs it:

- `--fields id,title,…` returns only those fields of each task (`id` always). Fields: `title`, `priority`, `folder`, `tags`, `readiness`, `blocking`, `blocked_by`, `claim`, `extra`, `created_at`, `completed_at`, `updated_at`, `notes_path`, `schema`.
- `--limit N` returns the first N in the command's order. The result always says `total` and `truncated`: when `truncated` is true there are `total` tasks and you got N. Say so rather than presenting N as everything, and fetch more only if the user needs them.
- `--tags-any a,b` / `--tags-all a,b` filter by tag, `--claimed` / `--claimed=false` by whether someone has claimed the task, and `list --readiness …` by readiness. Anything else (`extra`, title words) is `jq`'s, still with `--fields` so less comes through.
- `--limit 0` gives just the count, in `total`.
- For one task's full detail, `show` it.

With `jq`, a trimmed failure looks like success: the pipe's exit status is jq's, so the error kind and warnings just vanish. So:

- **Never trim a write's, `show`'s, or any small output's envelope**: print it as is.
- **When a big read needs `jq`**, keep `ok`, `error` and `warnings` with `jq -c 'if .ok then .result |= <shape> else . end'`, where `<shape>` gives one value (`map(…)`, never `.[]`).
- **A filter that drops tasks** replaces `total` and `truncated`, which count what koan returned, with `scanned` (tasks it checked) and `unscanned` (tasks `--limit` cut, never checked), as in the `extra` example below. Before saying there are none, or giving a count, make `unscanned` 0 by raising or dropping `--limit`.

## The daily loop

```sh
koan frontier --limit 10 --fields id,title,priority,folder,tags   # what's ready, in work order
koan show 42                                                      # one task, whole
koan claim 42                                                     # it's yours: no one else can claim it
koan done 42                                                      # finished, or cancelled: see below
```

`frontier` lists open, unblocked tasks in the order to work on them: highest priority first, unprioritized after, ties oldest (lowest ID) first. Scope it with `--folder /proj` and `--recursive=false`, or by tag with `--tags-any`.

### Claims

Other agents (another Claude Code or OpenCode session, say) may be working from the same tree, and `frontier` tells every one of them the same tasks. A **claim** reserves a task: once you have claimed it, no one else can, until it is released or done.

```sh
koan claim 42                                          # this task
koan claim-next --folder /proj                         # or the next ready task nobody has claimed; .result.task is null if there is none
koan frontier --claimed=false --limit 10 --fields id,title   # what's free
koan list --claimed --limit 20 --fields id,title,claim       # what's taken, and since when
koan release 42                                        # you won't do it after all
```

- **Claim a task before starting it**, whether you picked it or the user named it. `koan claim-next` picks and claims in one step, so prefer it to `frontier` then `claim` when the choice is yours.
- **On `conflict` with `rule: "claimed"`**, someone else has it: pick another if the choice was yours, and tell the user if they named the task. After `claim-next` returns `task: null`, nothing ready is free.
- **The one time `claimed` may be your own claim** is a rerun of `koan claim` whose first run ended with its outcome unknown. koan can't tell you whose it is, so don't start the task on that alone: tell the user that the task is claimed, possibly by your earlier attempt, and let them say whether to go on.
- **`done` ends the claim.** If you stop without finishing, `koan release` it, so the task isn't left reserved.
- **Never release a claim you didn't make** unless the user says so, and **never run `koan force-release` unasked**: it clears any claim, locked or not, and its holder may still be working. It asks for permission anyway.
- A claim never expires, and koan doesn't know who made one. `claim.extra` is whatever the claimer stored with it (`--extra '<json>'`); koan never reads it.

**Tokens.** `--use-token` on `claim` or `claim-next` also locks the task: every write to it then needs `--token`. Use it only when the user or a coordinator asks for it.

- Keep the token from `.result.token`: no other command ever shows it. Pass it as `--token` to every write on that task (`done`, `update`, `move`, `delete`, `release`, and `block` or `unblock` when it is the task being blocked, not a blocker) while you hold the claim. Never pass it to a write on another task.
- `done` and `release` end the claim: after either succeeds, drop the token, and later writes on that task take none.
- A `token-mismatch` on your own token means your claim is gone: stop and tell the user. The one exception is a rerun of `done` or `release` after an unknown outcome: check `koan show` first, since the first run may have succeeded.
- A `claims-replaced` warning on a `claim` or `claim-next` means the root was changed and the claims made under the old one, locks included, were just discarded. Tell the user at once, with the warning's `ids`: other holders' tokens no longer work.
- A `token-mismatch` with no token of your own means someone else locked the task: leave it alone, and tell the user if they asked for it.
- If a `claim` or `claim-next` with `--use-token` ended with its outcome unknown, check `koan show`: if the task's `claim.locked` is true, the first run may have succeeded, and then its token is lost and the task is locked against everyone, you included. Tell the user; only `force-release` clears it.

`show` returns `result.tasks`, always an array: one task, or every copy if the ID is duplicated (with a `duplicate-id` warning — report it). Each has `readiness` (`ready`/`blocked`/`done`), `blocking` (the blocker IDs still holding it up), and `notes_path`. **Notes are a plain Markdown file:** read and edit it directly with your file tools — there is no koan command for notes after creation.

Overview of everything:

```sh
koan list --limit 50 --fields id,title,readiness,folder           # open tasks, in tree order
koan list --readiness blocked --limit 20 --fields id,title,blocking   # what's stuck, and on what
koan list --folder /proj --tags-any urgent --limit 20 --fields id,title,readiness
koan list --readiness done --limit 0                              # how many are done: .result.total
koan list --include-folders --limit 0                             # every folder: .result.folders
koan list --fields id,title,extra --limit 200 | jq -c 'if .ok then .result |= {tasks: (.tasks | map(select(.extra.status == "waiting"))), scanned: (.tasks | length), unscanned: (.total - (.tasks | length))} else . end'
```

`list` returns open tasks (`ready` and `blocked`) unless `--readiness` says otherwise: `--readiness done` for finished ones, `--readiness ready,blocked,done` for all.

Why a task isn't moving — don't walk `blocking` by hand:

```sh
koan why 42                                                        # {"readiness":"blocked","ready":[17,23],"stuck":[31]}
koan why 42 --include-tasks --fields id,title,readiness,blocking   # and every open task in between
```

`ready` is the ready tasks upstream of 42, in work order: finishing them is what moves it. `stuck` is blocking IDs no work clears: a missing, unusable or duplicated task, or tasks blocking each other in a cycle. Each comes with a warning; report them to the user rather than working around them.

## Writing

```sh
koan create-folder -p /proj/api                     # -p creates missing parents
koan create 'Write migrations' --folder /proj/api --priority 2 --tags db,backend --blocked-by 41
koan create 'Investigate flaky test' --notes-file - <<'EOF'
Seen in CI on 2026-09-28. Fails ~1 in 20 runs.
EOF
koan update 42 --priority null --tags-add urgent --extra-merge '{"status":"waiting"}'
koan block 42 --blockers 40,41                      # 40 and 41 must finish before 42
koan unblock 42 --blockers 41
koan reopen 42
```

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

- The result is small: `ids` (in input order), `refs` (ref → ID), and `folders_created`. Folders a task names are **created if missing**, so check `folders_created` for one you didn't mean (a typo) and tell the user.
- Everything is checked before anything is written: an `invalid-input` names every bad field as `/tasks/<i>/…`, and a `not-found` every missing ID. Fix and rerun.
- To make an existing task wait on the plan, `block` it afterwards: `koan block 40 --blockers <ids>`.
- An error with `.error.partial` stopped partway through: the first `len(partial.ids)` tasks were created, the rest weren't. Don't rerun the whole batch (it would duplicate them). Rerun only the rest, with refs to created tasks replaced by their IDs from `partial.refs`, and tell the user what happened.

Rules the commands enforce:

- **Folder paths are exact**, from the tree's root: `/`, `/proj/api`. Never `proj/api` or `/proj/`, and never derived from the working directory. Segments: letters (either case), digits, hyphens, and case must match exactly (`/Work` is not `/work`). Tags: lowercase letters, digits, hyphens.
- **Titles**: one line, up to 200 characters. Quote them with single quotes. A title starting with `-` goes after `--`.
- **Priority**: an integer; **higher** sorts first in `frontier`. `null` means none (sorts after every priority, even negative ones).
- **Notes**: short text with `--notes`, anything longer through a quoted heredoc with `--notes-file -` (no escaping needed).
- `update` changes title, priority, tags (`--tags-add`, `--tags-remove`, `--tags-replace-all`), and `extra` (`--extra-merge`, `--extra-remove key`, `--extra-replace-all`). Blockers change only through `block`/`unblock`; completion only through `done`/`reopen`.

## Moving and deleting

```sh
koan move 42 --to /proj/api                   # a task into a folder; -p creates it
koan move-folder /proj/api --to /archive      # into an existing folder: /archive/api
koan move-folder /proj/api --to /proj/backend # otherwise to that path: a rename
koan delete 42                                # .result.dependents: the tasks it no longer blocks
koan delete-folder -r /proj/old               # .result.ids: what went; .result.dependents: as for delete
```

- Moving never changes blockers: tasks are named by ID, not location. Notes move with the task.
- **Deleting is permanent.** koan keeps no trash; the only undo is git, if the user keeps the tree in a repo, and only back to their last commit. So:
  - **Always confirm with the user before `delete` or `delete-folder`**, naming what goes (for a folder, list its tasks first, done ones too: `koan list --folder /x --readiness ready,blocked,done --limit 50 --fields id,title,readiness`). Never delete to tidy up on your own initiative.
  - Prefer cancelling (below) when the user just means "won't do": it keeps the record.
  - A deleted task's ID is removed from its dependents' blockers, so they may become ready. Tell the user which (`dependents` in the output).
  - `delete-folder` takes no `--token`: a folder holding a locked task is refused with `token-mismatch`, naming the tasks. Their claims have to be released first; that is the user's call.

## Hard rules

- **Never** hand-edit, create, rename, or delete anything under the root except a task's notes `.md` — use `move`, `move-folder`, `delete`, and `delete-folder`. Task `.json` files and `koan.json` belong to koan; a hand edit can break invariants no command will repair.
- **Never run `koan pick`.** It's an interactive picker that needs the user's terminal. When the user wants to choose tasks themselves, suggest they run it in their own terminal (`koan pick > picked.json`, say) and hand you the output. That envelope is read like any other: `result.tasks` (or `result.folders`) is their selection, `result.missing` what vanished meanwhile, and `result.actions` every change they made in the picker, each with its own `output` envelope, failures included; `result.notes_edited` lists tasks whose notes they edited. A failed `pick` (`cancelled`, `incomplete`, or `unavailable` after fzf ran) still lists its changes, in `.error.details.actions`.
- Don't pass `--input` unless building input from other JSON, or for `create-batch`, which takes nothing else; flags are clearer.
- Don't create tasks the user didn't ask for. When a follow-up turns up during other work, offer it: "Want me to add a task for X?"

## Conventions

- **Cancelled**: tag it, then mark it done — `koan update 42 --tags-add cancelled && koan done 42`.
- **Waiting on someone/something**: `koan update 42 --extra-merge '{"status":"waiting","on":"vendor quote"}'`; clear with `--extra-remove status --extra-remove on`.
- **Where a task came from**: put links (PR, issue, ticket) in the notes, not the title.
