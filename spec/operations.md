# koan operations

What each command takes, does, and returns. The data model and the rules every command follows are in [design.md](design.md); the exact shapes are the JSON Schemas in `schemas/` (`<command>-input.json`, `<command>-output.json`, and the shared ones named below). How the CLI spells inputs as arguments and options is in `cli.md`.

## Conventions

### Input and output

A command takes one JSON object and returns one JSON object, the **envelope**:

```text
{ "ok": true,  "result": { … }, "warnings": [ … ] }
{ "ok": false, "error":  { … }, "warnings": [ … ] }
```

An unknown input field, a missing required one, or a value of the wrong type is `invalid-input`. Every problem in the input is reported at once.

### Errors

```json
{
  "kind": "conflict",
  "rule": "claimed",
  "message": "task 42 is already claimed",
  "ids": [42],
  "folders": [],
  "paths": [],
  "problems": [],
  "changed_paths": []
}
```

| Field | Holds |
|---|---|
| `kind` | One of the kinds in [design.md](design.md#errors). |
| `rule` | For `conflict`, which rule refused the input; each command lists its own. `null` for every other kind. |
| `message` | One sentence for a person. Not for parsing. |
| `ids`, `folders`, `paths` | The tasks, folder paths, and files the error is about. `not-found` lists everything missing at once. |
| `problems` | For `invalid-input`: one `{field, reason}` per problem, `field` a JSON pointer into the input. For `corrupt`: one per rule the file breaks. |
| `changed_paths` | Files and folders the command created, rewrote, or removed before it failed. Empty means nothing changed. |

Two `conflict` rules are shared, and are not repeated below. Any command that walks the tree can fail with `duplicate-id` (`ids`: the ID; `paths`: every copy). Any command given a folder path can fail with `not-a-folder` (something on the path is a file or a symlink).

### Warnings

```json
{ "kind": "dangling-reference", "message": "task 42 is blocked by 17, which does not exist", "ids": [42, 17], "paths": ["/home/paul/koan-root/@proj/@42.json"] }
```

There are two kinds, `unusable-file` and `dangling-reference`, both defined in [design.md](design.md#walking). A command reports each at most once per file.

### Rules for every command

- **A usable root.** Every command but `init`, `version`, and `info` fails unless the root is usable ([Root states](design.md#root-states)). `doctor` and `migrate` also run on a root that needs migration, when the binary has a conversion from its format.
- **Reads and writes.** A write holds the tree's lock alone; a read shares it with other reads, and changes nothing. So a read never sees part of a write. Either fails with `busy` if it can't get the lock in 5 seconds. `info` and `version` take no lock.
- **Folders are made as needed.** A command that puts something into a folder creates that folder, and any above it, when it is missing: `create`, `create-batch`, `move`, `move-folder`, and `create-folder`. Their results list what was created in `folders_created`. A command that only reads a folder, or removes one, fails with `not-found` when it is missing.
- **Each folder made takes an ID.** The counter is raised once for every folder and task a command makes, before any is made; folders take their IDs first, parents before children, and tasks after them. Each folder is made whole, with its folder file and empty notes ([Concurrency](design.md#concurrency)). A command that fails partway leaves the folders made so far, each whole, and the IDs set aside for the rest unused.
- **Asking for what already holds is not an error.** `done` on a done task, `block` with a blocker already there, `move` to the folder the task is in: the command succeeds, writes nothing, and returns `changed: false`.
- **`updated_at`** is set to the command's time whenever a command changes a field of a task file, and only then.
- **One time per command.** Every timestamp a command writes is the same instant.

### Task

Every command that returns a task returns it in one shape (`schemas/task.json`): the task file's fields, plus five derived ones.

| Field | Holds |
|---|---|
| `folder` | The folder path of the folder holding the task. |
| `notes_path` | The absolute filesystem path of its `@<id>.md`, for an editor. Notes are never returned inline. |
| `readiness` | `ready`, `blocked`, or `done`. |
| `blocking` | The IDs in `blocked_by` that block it now: open, missing, or unusable. Ascending; empty unless `readiness` is `blocked`. |
| `claim` | `{claimed_at, extra}`, or `null` when the task is unclaimed or done. |

### Orders

- **Tree order**: by folder, a parent before its children and siblings by name (`/`, `/Zed`, `/infra`, `/proj`, `/proj/travel`, `/proj-b`); within a folder, by ID. Names are compared byte for byte, so every uppercase letter sorts before every lowercase one.
- **Work order**: by `priority`, highest first, tasks with none last; then by ID, lowest first.

### Narrowing

`frontier` and `list` take the same optional fields to cut down what they return, and `list` takes `claimed` as well. They apply in this order:

| Field | Effect |
|---|---|
| `tags_any` | Keep tasks with at least one of these tags. |
| `tags_all` | Keep tasks with all of these tags. |
| `claimed` | `list` only. `true`: only claimed tasks. `false`: only unclaimed ones. |
| `limit` | Return at most this many, the first in the command's order. |
| `fields` | Return only these fields of each task. `id` is always included. |

Their results always carry `total`, the number of tasks before `limit`, and `truncated`, whether `limit` cut any. Warnings do not depend on narrowing. Filtering on anything else, such as `extra` or the title, is left to the caller.

## Commands

| Command | Kind | Does |
|---|---|---|
| [`init`](#init) | setup | Create the tree and the state file, or whichever is missing. |
| [`version`](#version) | — | Report the binary's version and format. |
| [`info`](#info) | read | Report the root's state. |
| [`doctor`](#doctor) | read, alone | Report everything wrong with the tree. |
| [`repair`](#repair) | write | Fix what can be fixed safely. |
| [`migrate`](#migrate) | write | Convert the tree to this binary's format. |
| [`create-folder`](#create-folder) | write | Create a folder. |
| [`move-folder`](#move-folder) | write | Move or rename a folder. |
| [`delete-folder`](#delete-folder) | write | Remove a folder and everything in it. |
| [`create`](#create) | write | Create a task. |
| [`create-batch`](#create-batch) | write | Create several tasks that may wait on each other. |
| [`show`](#show) | read | Return one task. |
| [`update`](#update) | write | Change a task's title, priority, tags, or `extra`. |
| [`done`](#done) | write | Mark a task done. |
| [`reopen`](#reopen) | write | Make a done task open again. |
| [`block`](#block) | write | Add blockers to a task. |
| [`unblock`](#unblock) | write | Remove blockers from a task. |
| [`move`](#move) | write | Move a task to another folder. |
| [`delete`](#delete) | write | Remove a task. |
| [`frontier`](#frontier) | read | Return the ready, unclaimed tasks, in work order. |
| [`list`](#list) | read | Return the tasks, and optionally folders, in scope. |
| [`why`](#why) | read | Say what a task is waiting on. |
| [`claim`](#claim) | write | Claim a task. |
| [`claim-next`](#claim-next) | write | Claim the first ready, unclaimed task. |
| [`release`](#release) | write | End a claim. |

For each command below: **Input** lists its fields (required ones marked), **Result** its output, and **Refuses** the `conflict` rules of its own. `invalid-input`, the root-state errors, `busy`, `not-found` for a task or folder it names, and `io` apply to every command as the conventions say, and are not repeated.

## Setup and diagnosis

### init

Create whatever is missing of `~/koan-root`, its `@koan.json`, and the state file. What is already there is left alone, so running it again changes nothing.

- **Input:** none.
- A state file created beside an existing tree starts `last_id` at the highest ID in the name of any task file or notes file, or in any folder file it can read; otherwise at `0`. A folder file it can't read is skipped, and `doctor` reports it.
- `init` makes the folder when it is missing, then holds the lock alone for everything else, so the highest ID is read from a tree at rest and two `init`s cannot overlap. It needs the whole tree for that: a folder it can't list fails it with `io`.
- A folder without a `@koan.json` is adopted as it is: `init` writes the `@koan.json` and leaves everything else in the folder alone. What it holds that is not named with `@` is material.
- A tree that needs migration, or is too new, is not an error here: its `@koan.json` is left alone, and a missing state file is still created.
- **Result:** `root` and `state_path` (the two paths), and `created`: which of `root`, `metadata`, `state` it made.
- On failure, `changed_paths` lists what it made.
- **Refuses:** `not-a-folder` when `~/koan-root` is a file or leads nowhere, and `busy` when it can't get the lock. An existing `@koan.json` or state file that is unusable fails with `corrupt` or `io`, and is never replaced.

### version

Report the binary: `version`, `commit`, `commit_time`, `dirty` (built with uncommitted changes), `go`, `platform`, and `format`, the one [format](design.md#formats) it reads and writes. It reads no file and cannot fail.

### info

Report the root's state without failing on it.

- **Input:** none.
- **Result:**

| Field | Holds |
|---|---|
| `root` | The path of the tree. |
| `state_path` | The path of the state file. |
| `state` | `not-initialized`, `unusable`, `needs-migration`, `too-new`, or `usable` ([Root states](design.md#root-states)). |
| `missing` | When `not-initialized`: which of `root`, `metadata`, `state`. |
| `format` | The tree's `schema`, or `null` when it can't be read. |
| `last_id` | The counter, or `null` when it can't be read. |
| `claims` | The number of claims, or `null`. |
| `error` | When `unusable`: the error other commands fail with. |

### doctor

Report every [finding](design.md#doctor-and-repair) in the tree. It holds the lock alone while it looks, as a write does, and changes nothing. It runs on a root that needs migration, when this binary has a conversion from the tree's format.

- **Input:** none.
- **Result:** `healthy` (no findings), and `findings`, each:

| Field | Holds |
|---|---|
| `kind` | A finding kind. |
| `message` | What is wrong and what to do about it. |
| `paths`, `ids` | The files and tasks involved. |
| `repairable` | Whether `repair` can fix it: by default, or, for `dangling-reference` and a `stale-claim` on a task that is gone, when asked for by name. |

- For `cycle`, `ids` is one cycle, each task blocked by the next and the last by the first. For `unusable-file`, `message` says why the file can't be used.
- **Order:** by kind, in the order [design.md](design.md#doctor-and-repair) lists them, then tree order.
- **Refuses:** `no-conversion`, on a tree in a format this binary has no conversion from.

### repair

Fix repairable findings, as the table in [design.md](design.md#doctor-and-repair) says, then report what is left.

- **Input:** `kinds`: the finding kinds to fix, one or more of `temp-leftover`, `id-above-counter`, `dangling-reference`, `stale-claim`, `orphan-notes`, and `missing-folder-file`. Without it, every one of them, but not `dangling-reference`, nor a `stale-claim` on a task that is gone.
- Findings of other kinds are left alone, and reported in `findings`.
- **Result:** `repaired` and `findings`, both lists of findings as `doctor` returns them.
- Running it again is always safe.

### migrate

Convert every file in an earlier format to this binary's, then record the format in `@koan.json` ([Formats](design.md#formats)). koan never runs it on its own.

- **Input:** `dry_run` (default `false`): report what would change and write nothing.
- **Result:** `from` and `to` (the tree's format before and after), `converted` (how many task files), and `unconverted` (paths of task files it could not read or convert, which are left as they are).
- A tree already current is not an error: `converted` is `0`.
- Fails with `unsupported-format` on a tree in a later format, and with `conflict` (`no-conversion`) on one in a format this binary has no conversion from ([Formats](design.md#formats)). Either way it converts nothing.
- **Refuses:** `no-conversion`.

## Folders

### create-folder

Create a folder, with any missing folders above it, each with its folder file and empty notes.

- **Input:** `folder` (required).
- **Result:** `folder`, and `folders_created`, empty when it already existed.

### move-folder

Move a folder, with everything under it, to a new path. Renaming is moving to a sibling path.

- **Input:** `folder` (required), and `to` (required): the folder's new path, not a folder to put it into.
- Neither may be `/`, and `to` may not be under `folder`: `invalid-input`.
- No file is rewritten: a task's folder is where its file is, and the folder's own files move with it, its ID included.
- **Result:** `folder` (the new path), `folders_created`, `changed`.
- **Refuses:** `destination-exists` when anything is at `to`.

### delete-folder

Remove a folder and everything under it, for good. koan keeps no trash; undo is git's.

- **Input:** `folder` (required, not `/`), `recursive` (default `false`), and `force` (default `false`).
- Without `recursive`, the folder must hold nothing but its folder file, empty notes, and hidden entries.
- With `recursive`, everything under it is removed. **Material blocks it unless `force` is given**: koan never deletes what it didn't write without being told to. Material here is every entry in the folder or in a folder under it that is not koan's and not hidden, an entry named with `@` that is not one of koan's included. Hidden entries (`.gitignore`, `.obsidian`) don't block, and are removed with the folder. Finding material takes a listing of each folder, never a look inside the material.
- Every task outside the folder loses the removed tasks' IDs from its `blocked_by`, and the removed tasks' claims end. The folder is removed first, as [`delete`](#delete) removes its task first.
- **Result:** `folder`, `deleted` (IDs of the tasks removed), and `dependents` (IDs of the tasks whose `blocked_by` changed).
- **Refuses:** `not-empty`, without `recursive`; `has-material`, with `recursive` and without `force`, listing in `paths` each material entry found, not what is inside it.

## Tasks

### create

Create an open task.

- **Input:**

| Field | Default | Holds |
|---|---|---|
| `title` (required) | | Trimmed before it is checked and stored. |
| `folder` | `/` | Where the task goes. |
| `priority` | `null` | |
| `tags` | `[]` | |
| `blocked_by` | `[]` | IDs of existing tasks, open or done. |
| `extra` | `{}` | |
| `notes` | `""` | The initial content of the `@<id>.md`. |

- The new ID is the counter's next one, after the IDs of any folders made for it. The counter is written first, then any folders, then the task file, then the notes. A failure after the counter uses up an ID. A failure writing the notes leaves the task created without them: the error's `ids` is the new task, and its `changed_paths` what was written. The `@<id>.md` is always written, empty when no notes are given, and never over a file already there: when one is, `create` fails with `io`, and the new task has that file as its notes.
- The folder's path is checked before an ID is used, so `not-a-folder` changes nothing.
- A new task can't close a cycle, since nothing waits on it yet.
- **Result:** `task`, `folders_created`.

### create-batch

Create several open tasks in one write, where later ones may wait on earlier ones. Everything is checked before anything is written.

- **Input:** `folder` (default `/`), the folder of every task that names none; and `tasks` (required, 1–1000), each with `create`'s fields plus:

| Field | Holds |
|---|---|
| `ref` | A name for this task within the batch, following the tag rule. Unique in the batch. Never stored. |
| `blocked_by` | A mix of integers, the IDs of existing tasks, and strings, the `ref`s of **earlier** tasks in the batch. |

- Refs may only point backwards, so a batch cannot contain a cycle.
- Every folder's path is checked too, before an ID is used.
- Task IDs are consecutive, in input order, after the IDs of the folders the batch makes, and every task gets the same `created_at`.
- The counter is raised once, by the number of folders and tasks the batch makes. Then the folders are made, parents first, and the tasks written in input order, each as `create` writes one.
- So a failure partway leaves the first tasks of the batch created and the rest not: the error's `ids` lists the created ones, in input order. To finish, drop those tasks from the input, replace each `ref` to one of them with its ID, and run the rest. The IDs set aside for the tasks not created are not reused.
- **Result:** `ids` (in input order), `refs` (each `ref` and the ID it got), `folders_created`.

### show

Return one task.

- **Input:** `id` (required).
- **Result:** `task`.

### update

Change a task's title, priority, tags, or `extra`. Fields not given are left alone. Other fields have their own commands, and notes are edited in the `@<id>.md`.

- **Input:** `id` (required), and at least one of:

| Field | Effect |
|---|---|
| `title` | Replaces the title, trimmed. |
| `priority` | Sets it; `null` clears it. |
| `tags_add`, `tags_remove` | Add and remove tags. They may not share a tag. |
| `tags_replace_all` | The whole new set. Not with the two above. |
| `extra_merge` | An object: each key is set to its value, replacing that key's old value whole. `null` is a value, not a removal. |
| `extra_remove` | Keys to delete. May not share a key with `extra_merge`. |
| `extra_replace_all` | The whole new object. Not with the two above. |

- **Result:** `task`, `changed`.

### done

Mark a task done: `completed_at` is set to the command's time, and its claim ends. Blockers do not matter: a task can be marked done at any time.

- **Input:** `id` (required).
- The task file is written first, then the state file. Run again after a failure between the two, it removes the claim that was left.
- **Result:** `task`, `changed`.

### reopen

Make a done task open again: `completed_at` becomes `null`. The task starts unclaimed.

- **Input:** `id` (required).
- **Result:** `task`, `changed`.

### block

Add blockers to a task. All of them are checked before anything is written.

- **Input:** `id` (required), and `blockers` (required): one or more IDs of existing tasks, open or done, not including `id`.
- Blockers already in `blocked_by` are ignored.
- **Result:** `task`, `changed`.
- **Refuses:** `cycle`, when a new blocker already waits on `id`, directly or through other tasks. `ids` is one such cycle: `id`, the blocker, and the tasks from it back to `id`.
- A cycle through a task file that can't be read can't be ruled out, so when the search from a new blocker meets one, `block` fails with that file's error, `corrupt` or `unsupported-format`.

### unblock

Remove blockers from a task.

- **Input:** `id` (required), and `blockers` (required): one or more IDs. They need not exist, which is how a dangling reference is cleared by hand, and need not be in `blocked_by`.
- **Result:** `task`, `changed`.

### move

Move a task, with its notes, to another folder. Nothing in the task file changes, `updated_at` included.

- **Input:** `id` (required), and `to` (required): the folder to move it into.
- The task file moves first, then the notes.
- **Result:** `task`, `folders_created`, `changed`.
- **Refuses:** `destination-exists`, when `to` already holds a non-empty `@<id>.md` with the task's name. An empty one is replaced.

### delete

Remove a task and its notes, for good, and remove its ID from every other task's `blocked_by`. Its claim ends. Tasks that waited only on it become ready, as if it had been done.

- **Input:** `id` (required).
- The task file need not be usable: `delete` goes by its filename, which makes it the way to remove a broken task.
- The notes are removed first, then the task file, then the other tasks are rewritten, then its claim ends. A kill after the notes leaves the task, and `delete` can be run again. A kill in between leaves references to a task that is gone, which `doctor` finds; the other order would free its dependents with nothing to show for it.
- **Result:** `id`, and `dependents` (IDs of the tasks whose `blocked_by` changed).

## Queries

### frontier

Return the tasks that are ready and unclaimed: what can be taken now. It is exactly what [`claim-next`](#claim-next) draws from. A ready task that is claimed is not here; `list` with `readiness: ["ready"]` shows those too, and with `claimed: true` shows what is out.

- **Input:** `folder` (default `/`), `recursive` (default `true`), and the [narrowing](#narrowing) fields.
- **Result:** `tasks`, `total`, `truncated`.
- **Order:** work order.

### list

Return the tasks in scope, whatever their readiness.

- **Input:** `folder` (default `/`), `recursive` (default `true`), the [narrowing](#narrowing) fields, and:

| Field | Default | Effect |
|---|---|---|
| `readiness` | `["ready", "blocked"]` | Which tasks to return. Done tasks pile up for ever, so they are left out unless asked for. |
| `include_folders` | `false` | Also return `folders`: `folder` itself and the folders under it, empty ones included. Never narrowed. |

- **Result:** `tasks`, `total`, `truncated`, and `folders` when asked for.
- **Order:** tree order.

### why

Say what a task is waiting on, by following its blockers, and theirs, through open tasks.

- **Input:** `id` (required).
- **Result:**

| Field | Holds |
|---|---|
| `readiness` | The task's own. |
| `ready` | The IDs of the ready tasks it waits on, in work order: working on these is what moves it. Just the task itself when it is ready. |
| `stuck` | The IDs no work will clear, ascending: blockers that are missing or unusable, and tasks that block each other. |

- A done task has both lists empty. An open task has at least one of them non-empty.

## Claims

What a claim is and when it ends is in [design.md](design.md#claims).

### claim

Claim a task.

- **Input:** `id` (required), and `extra` (default `{}`), which is stored with the claim and returned with it.
- The task may be ready or blocked. No task file is written.
- **Result:** `task`.
- **Refuses:** `task-done`; and `claimed`, whoever holds the claim, the caller included. The error's `message` and the task's `claim`, from `show`, say what the existing claim's `extra` is.

### claim-next

Claim the first task of the frontier. It is `frontier` and `claim` under one lock, so two callers asking at once never get the same task.

- **Input:** `folder`, `recursive`, `tags_any`, and `tags_all`, as [`frontier`](#frontier) takes them, and `extra` as [`claim`](#claim) does.
- **Result:** `task`, or `null` when the frontier is empty. That is a success, and writes nothing.
- Each call that finds a task claims one, so running it again after an unclear outcome may claim a second.

### release

End a task's claim, whoever made it.

- **Input:** `id` (required).
- The task need not exist: releasing goes by the entry in the state file.
- **Result:** `id`, `changed` (`false` when there was no claim).
