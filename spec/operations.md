# koan operations

What each command takes, does, and returns. The data model and the rules every command follows are in [design.md](design.md); the exact shapes are the JSON Schemas in `schemas/`: `task-create-input.json` and `task-create-output.json` for `task create`, a hyphen for the space, and the shared ones named below. How the CLI spells inputs as arguments and options is in `cli.md`.

## Conventions

### Input and output

A command takes one JSON object and returns one JSON object, the **envelope**:

```text
{ "ok": true,  "result": { … }, "warnings": [ … ] }
{ "ok": false, "error":  { … }, "warnings": [ … ] }
```

An unknown input field, a missing required one, or a value of the wrong type is `invalid-input`. Every problem in the input is reported at once.

A command that changes the tree returns **identities**, which go straight into the next command; a command that reads returns **records**. A record is exact only as of the call; an identity can always be looked up again, with `task show` or `folder show`.

- **A write** returns `id`, and `path` for a folder; `changed`; and, where the command can have them, `folders_created` and `dependents` (the other tasks and folders whose `blocked_by` it changed). A folder's `id` is `null` where its [record](#folder)'s is.
- **A read** returns [task](#task) and [folder](#folder) records.
- **The exceptions:**
  - `task delete` and `folder delete` return the record as it was, since there is nothing left to show: its `notes_path`, and a folder's `dir`, name paths that are gone. `task delete` returns the `id` beside it, since a task file it can't use gives no record.
  - `task claim-next` returns the record of the task it claimed, which the caller doesn't know yet.
  - `batch` returns the identities of everything it made.
  - `where`, `task why`, `folder why`, and the commands of [setup and diagnosis](#setup-and-diagnosis) are reports, with shapes of their own.
- **Inside a record**, a field naming another task or folder holds a reference: `blocked_by` and `open_blockers` hold IDs, a task's `folder` and an `inherited` entry's `folder` a path.

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

Three `conflict` rules are shared, and are not repeated below. Any command that walks the tree can fail with `duplicate-id` (`ids`: the ID; `paths`: every copy). Any command given a folder path can fail with `not-a-folder` (something on the path is a file or a symlink), and with `case-mismatch` (a part of the path is on disk only in another case: `folders` holds the path as it is on disk, as far as it goes, and the message says "exists as /proj"; see [Folders](design.md#folders)).

### Warnings

```json
{ "kind": "dangling-reference", "message": "task 42 is blocked by 17, which does not exist", "ids": [42, 17], "paths": ["/home/paul/koan-root/@proj/@42.json"] }
```

There are three kinds: `unusable-file` and `dangling-reference`, defined in [design.md](design.md#walking), and `id-above-counter`, from a command that issues IDs into a tree an outside change put IDs in above the counter ([IDs](design.md#ids)): its `ids` is the highest such ID, its `paths` the file that holds it, and its message says the counter was raised. A command reports each at most once per file, and only for files it reads: a write that returns identities reads no task's blockers to build a record, so it reports nothing about them.

### Rules for every command

- **A usable root.** Every command but `init`, `version`, and `info` fails unless the root is usable ([Root states](design.md#root-states)). `doctor` and `migrate` also run on a root that needs migration, when the binary has a conversion from its format.
- **Reads and writes.** A write holds the tree's lock alone; a read shares it with other reads, and changes nothing. So a read never sees part of a write. Either fails with `busy` if it can't get the lock in 5 seconds. `info` and `version` take no lock.
- **Folders are made as needed.** A command that puts something into a folder creates that folder, and any above it, when it is missing: `task create`, `batch`, `task move`, `folder move`, and `folder create`. Their results list what was created in `folders_created`. A command that only reads a folder, or removes one, fails with `not-found` when it is missing.
- **Each folder made takes an ID.** The counter is first brought up to the highest ID in the tree, if it is below it ([IDs](design.md#ids)), then raised once for every folder and task a command makes, before any is made; folders take their IDs first, parents before children, and tasks after them. Each folder is made, then its folder file and its notes, empty unless given, are written in it. A command that fails partway leaves the folders made so far, the last perhaps without its folder file or notes ([Crashes and failures](design.md#crashes-and-failures)), and the IDs set aside for the rest unused.
- **Asking for what already holds is not an error.** `task done` on a done task, `task block` or `folder block` with a blocker already there, `task move` to the folder the task is in: the command succeeds, writes nothing, and returns `changed: false`.
- **`updated_at`** is set to the command's time whenever a command changes a field of a task file, and only then.
- **One time per command.** Every timestamp a command writes is the same instant.

### Task

Every command that returns a task's record returns it in one shape (`schemas/task.json`): the task file's fields but `schema`, plus six derived ones.

| Field | Holds |
|---|---|
| `folder` | The folder path of the folder holding the task. |
| `notes_path` | The absolute filesystem path of its `@<id>.md`, for an editor. Notes are never returned inline. |
| `readiness` | `ready`, `blocked`, or `done` ([Dependencies](design.md#dependencies)). |
| `open_blockers` | Every ID that blocks it now, its own and those it inherits from the folders above it: open tasks, folders not done, and IDs missing or unusable. Ascending, each once; empty unless `readiness` is `blocked`. |
| `inherited` | One `{folder, ids}` for each folder above that blocks it now, outermost first: the folder's path, and the IDs in its `blocked_by` that block now, ascending (they are in `open_blockers` too). `ids` is `null` when the folder's file is missing or can't be used, so its blockers are unknown. Empty unless `readiness` is `blocked`. |
| `claim` | `{claimed_at, extra}`, or `null` when the task is unclaimed or done. |

A task is `blocked` exactly when `open_blockers` is non-empty or an `inherited` entry has `ids` `null`. So a task whose own `blocked_by` is empty says, in `inherited`, which folder holds it back.

### Folder

Every command that returns a folder's record returns it in one shape (`schemas/folder.json`): the folder file's fields, where it is, and its state.

| Field | Holds |
|---|---|
| `id` | The folder's ID; `null` for the root, and for a folder with no folder file or one koan can't use. |
| `path` | The folder path. |
| `dir` | The absolute filesystem path of its directory, `@`s and all. |
| `title` | Its title, or `null`. |
| `blocked_by` | Its `blocked_by`; `[]` for the root; `null` for a folder with no folder file or one koan can't use. |
| `extra` | Its `extra`; `{}` for the root. |
| `notes_path` | The absolute filesystem path of its `@folder.md`, for an editor; `null` for the root, which has none. |
| `readiness` | `done` when every task under it is; else `blocked` when something it waits on blocks or is unknown, else `ready` ([Dependencies](design.md#dependencies)). The root is never `blocked`. |
| `open_blockers`, `inherited` | As in a [task](#task)'s record, for what the folder's contents wait on. |
| `empty` | Whether it holds no task at any depth. An empty folder is `done`. |

A command that returns a folder's `id` or record without failing on its folder file (`folder create`, `folder move`, `folder delete`, `folder list`) gives a folder file it can't use `id` `null`, `title` `null`, `blocked_by` `null`, and `extra` `{}`, with an `unusable-file` warning. `folder show`, `folder update`, and `where` fail with `corrupt` instead.

Only `folder show`, `folder list`, `folder delete`, and `where` return folder records. Every other result names a folder by reference: `id` and `path` from a folder write, a path in a task's `folder` and in `folders_created`, and a path with its ID in `batch`'s `folders`.

### References

Inputs take [references](design.md#references) wherever they name a task or a folder that exists:

- **A task**, in every `id`: an integer, or a string `"42"` or `"@42"`.
- **A task or a folder**, in every `blocked_by` and `blocks` (`schemas/item-ref.json`): an ID in any of those forms, whichever kind has it, or a folder's path. A path is resolved to the folder's ID, and only the ID is stored. A path with no ID to store is refused: `/` is `invalid-input`, since the root has no ID; a path to nothing is `not-found`, since a blocker is never made as needed (a `batch` may name a folder it makes); a folder with no folder file is `not-found`, saying `koan repair` writes one; and a folder file koan can't use is `corrupt`. In `task unblock` and `folder unblock` an ID need not exist, but a path must resolve.
- **A folder**, in every `folder` and in `task move`'s `to`: a path, or an ID as an integer or a string `"41"` or `"@41"`. A folder given by ID must exist, `not-found` otherwise, and is never made as needed; one given by path is made as needed where the command says so.
- **Paths only** where the folder is new: `folder create`'s `folder`, a `batch` folder entry's `folder`, and `folder move`'s `to`.

JSON results use the plain forms: an ID is an integer. Only `where`'s `ref` writes a reference, `@` and all. A `not-found` for an ID lists it in `ids`, and its message says when the ID is the other kind's. Giving a folder by ID can also fail with `duplicate-id`, and warns `unusable-file` for each folder file koan can't use, since any of them might have held the ID.

### Cycles

A command that adds to a `blocked_by` or changes what is in a folder (`task create` and `folder create` with `blocked_by` or `blocks`, `task block`, `folder block`, `batch`, `task move`, `folder move`) refuses anything that would break the [acyclic invariant](design.md#invariants), with `conflict` (`cycle`). It checks in two steps, before it writes anything:

1. **Containment**, by comparing paths: nothing may be blocked by a folder that contains it, and no folder by anything under it. The error's `ids` are the two, the one that would wait first, and its message says which contains which (`/proj contains /proj/phase2`).
2. **The graph**: a search for a path back, over the [graph](design.md#invariants) with the change made. The error's `ids` are the tasks and folders of one such cycle, in order, starting at the one the command changed, each waiting on the next and the last on the first: by its `blocked_by`, by being in a folder whose blockers it inherits, or, for a folder, by holding what it waits on to be done. A folder with no ID is left out of `ids`. `folders` lists the paths of the cycle's folders in the same order, and the message gives each step in words, naming a step through a task not added yet as such ("a task added to /proj would wait on itself").

The search loads only the files it reaches. A cycle through a task file or folder file that can't be read can't be ruled out, so a search that meets one fails with that file's error, `corrupt` or `unsupported-format`. A plain `task create` into a folder, with no `blocked_by` or `blocks`, never closes a cycle, so it makes no search.

### Orders

- **Tree order**: by folder, a parent before its children and siblings by name (`/`, `/Zed`, `/infra`, `/proj`, `/proj/travel`, `/proj-b`); within a folder, by ID. Names are compared byte for byte, so every uppercase letter sorts before every lowercase one.
- **Work order**: by `priority`, highest first, tasks with none last; then by ID, lowest first.

### Narrowing

`task frontier` and `task list` take the same optional fields to cut down what they return, and `task list` takes `claimed` as well; `folder list` takes `limit` and `fields`. They apply in this order:

| Field | Effect |
|---|---|
| `tags_any` | Keep tasks with at least one of these tags. |
| `tags_all` | Keep tasks with all of these tags. |
| `claimed` | `task list` only. `true`: only claimed tasks. `false`: only unclaimed ones. |
| `limit` | Return at most this many, the first in the command's order. |
| `fields` | Return only these fields of each task or folder. `id` is always included, and a folder's `path`. |

Their results always carry `total`, the number of tasks or folders before `limit`, and `truncated`, whether `limit` cut any. Warnings do not depend on narrowing. Filtering on anything else, such as `extra` or the title, is left to the caller.

## Commands

| Command | Kind | Does |
|---|---|---|
| [`init`](#init) | setup | Create the tree and the state file, or whichever is missing. |
| [`version`](#version) | — | Report the binary's version and format. |
| [`info`](#info) | read | Report the root's state. |
| [`doctor`](#doctor) | read, alone | Report everything wrong with the tree. |
| [`repair`](#repair) | write | Fix what can be fixed safely. |
| [`migrate`](#migrate) | write | Convert the tree to this binary's format. |
| [`folder create`](#folder-create) | write | Create a folder. |
| [`folder show`](#folder-show) | read | Return one folder. |
| [`folder update`](#folder-update) | write | Change a folder's title or `extra`. |
| [`folder move`](#folder-move) | write | Move or rename a folder. |
| [`folder delete`](#folder-delete) | write | Remove a folder and everything in it. |
| [`folder list`](#folder-list) | read | Return the folders under a folder. |
| [`folder block`](#folder-block) | write | Add blockers to a folder. |
| [`folder unblock`](#folder-unblock) | write | Remove blockers from a folder. |
| [`folder why`](#folder-why) | read | Say what a folder is waiting on to be done. |
| [`task create`](#task-create) | write | Create a task. |
| [`batch`](#batch) | write | Create folders, and tasks that may wait on each other, in one write. |
| [`task show`](#task-show) | read | Return one task. |
| [`task update`](#task-update) | write | Change a task's title, priority, tags, or `extra`. |
| [`task done`](#task-done) | write | Mark a task done. |
| [`task reopen`](#task-reopen) | write | Make a done task open again. |
| [`task block`](#task-block) | write | Add blockers to a task. |
| [`task unblock`](#task-unblock) | write | Remove blockers from a task. |
| [`task move`](#task-move) | write | Move a task to another folder. |
| [`task delete`](#task-delete) | write | Remove a task. |
| [`task frontier`](#task-frontier) | read | Return the ready, unclaimed tasks, in work order. |
| [`task list`](#task-list) | read | Return the tasks in scope. |
| [`task why`](#task-why) | read | Say what a task is waiting on. |
| [`where`](#where) | read | Turn a reference into disk paths, or a disk path into a reference. |
| [`task claim`](#task-claim) | write | Claim a task. |
| [`task claim-next`](#task-claim-next) | write | Claim the first ready, unclaimed task. |
| [`task release`](#task-release) | write | End a claim. |

For each command below: **Input** lists its fields (required ones marked), **Result** its output, and **Refuses** the `conflict` rules of its own. `invalid-input`, the root-state errors, `busy`, `not-found` for a task or folder it names, and `io` apply to every command as the conventions say, and are not repeated.

## Setup and diagnosis

### init

Create whatever is missing of `~/koan-root`, its `@koan.json`, and the state file, and raise the counter to the tree's highest ID when it is below it. What is already there is otherwise left alone, so running it again changes nothing.

- **Input:** none.
- **The tree's highest ID** is the highest ID in the name of any task file or notes file, or in any folder file it can read; `0` for an empty tree. A folder file it can't read is skipped, and `doctor` reports it.
- A state file created beside an existing tree starts `last_id` at the tree's highest ID. A state file already there whose `last_id` is below it is raised to it, its claims left as they are. Raising the counter never harms, so `init` stays safe to run again.
- `init` makes the folder when it is missing, then holds the lock alone for everything else, so the highest ID is read from a tree at rest and two `init`s cannot overlap. It needs the whole tree for that: a folder it can't list fails it with `io`, whether or not the state file is there.
- A folder without a `@koan.json` is adopted as it is: `init` writes the `@koan.json` and leaves everything else in the folder alone. What it holds that is not named with `@` is material.
- A tree that needs migration, or is too new, is not an error here: its `@koan.json` is left alone, a missing state file is still created, and a low counter still raised.
- **Result:** `root` and `state_path` (the two paths); `created`: which of `root`, `metadata`, `state` it made; and `counter_raised`: whether it raised the `last_id` of a state file that was already there.
- On failure, `changed_paths` lists what it made.
- **Refuses:** `not-a-folder` when `~/koan-root` is a file or leads nowhere, and `busy` when it can't get the lock. An existing `@koan.json` or state file that is unusable fails with `corrupt` or `io`, and is never replaced.

### version

Report the binary: `version`, `commit`, `commit_time`, `dirty` (built with uncommitted changes), `go`, `platform`, and `format`, the one tree [format](design.md#formats) it reads and writes. It reads no file and cannot fail.

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
| `paths`, `ids` | The files, and the tasks and folders, involved. |
| `repairable` | Whether `repair` can fix it: by default, or, for `dangling-reference` and a `stale-claim` on a task that is gone, when asked for by name. |

- For `cycle`, `ids` is one cycle, as a `cycle` error gives it ([Cycles](#cycles)), and `message` gives each step, saying whether tasks are stuck now or only a task added to a folder would be. For `unusable-file`, `message` says why the file can't be used.
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

### folder create

Create a folder, with any missing folders above it, each with its folder file and notes: empty but for the folder asked for, which gets `notes`.

- **Input:** `folder` (required, a path), and, for the folder itself, `title` (default `null`), `extra` (default `{}`), `notes` (default `""`), `blocked_by` (default `[]`), and `blocks` (default `[]`). The first four are its folder file's fields and its notes; `blocks` names existing tasks and folders that wait on the new folder, which is added to each one's `blocked_by`. The folders above it get none of them.
- `blocked_by` and `blocks` take [tasks and folders](#references), as `task create`'s do, and are checked the same way: every one is read before an ID is used, and a [cycle](#cycles) is refused before an ID is used. Only `blocked_by` and `blocks` can close one, since a new folder holds nothing yet: a folder in `blocks` above the new one, say, or something in `blocks` that its `blocked_by` already waits on.
- A folder already there is not an error, and is left as it is, when none of `title`, `extra`, `notes`, `blocked_by`, and `blocks` is given: `changed` is `false`.
- Written as `task create` writes: the counter, then each task and folder in `blocks`, ascending, then the folders, the new one last with its folder file and notes. A kill between leaves those in `blocks` waiting on an ID with no folder, a dangling reference `doctor` finds, beside a `missing-folder-file`.
- **Result:** `id`, `path`, `changed`, `folders_created`, empty when it already existed, and `dependents`: the IDs in `blocks`, ascending.
- **Refuses:** `destination-exists`, when the folder is already there and `title`, `extra`, `notes`, `blocked_by`, or `blocks` is given, even one that appeared while it was being made: they are set with `folder update` and `folder block`, and notes edited in the `@folder.md`. `cycle`.

### folder show

Return one folder.

- **Input:** `folder` (required).
- **Result:** `folder`, its record.
- A folder file koan can't use fails it with `corrupt`. A folder with no folder file is returned with `id` `null`.

### folder update

Change a folder's title or `extra`. Fields not given are left alone, and notes are edited in the `@folder.md`.

- **Input:** `folder` (required, not `/`: the root has no folder file), and at least one of:

| Field | Effect |
|---|---|
| `title` | Replaces the title, trimmed; `null` clears it. |
| `extra_merge`, `extra_remove`, `extra_replace_all` | As `task update`'s. |

- A folder with no folder file, or one koan can't use, fails it: `not-found` for the first (`koan repair` writes one), `corrupt` for the second.
- **Result:** `id`, `path`, `changed`.

### folder move

Move a folder, with everything under it, to a new path. Renaming is moving to a sibling path.

- **Input:** `folder` (required), and `to` (required): the folder's new path, not a folder to put it into, so a path only.
- Neither may be `/`, and `to` may not be under `folder`: `invalid-input`. For a `folder` given by ID, the second is known only once the tree is read, so it is reported after the root's state and `busy`.
- No file is rewritten: a task's folder is where its file is, and the folder's own files move with it, its ID included, so every `blocked_by` naming it still does.
- Under its new parents the folder and everything in it inherit their blockers, and they hold it, so the move is checked for a [cycle](#cycles) before anything is changed. That takes a walk, every folder file, and the files the search reaches.
- **A rename that changes only case** (`/proj` to `/Proj`: same parent, names equal ignoring case) is allowed on every platform. It is the one way to fix a folder's case, and on a filesystem that ignores case `to` is the folder itself, so it can't be refused as `destination-exists` there.
- **Result:** `id`, `path` (the new one), `changed`, `folders_created`.
- **Refuses:** `destination-exists` when anything is at `to` other than the folder itself; `cycle`.

### folder delete

Remove a folder and everything under it, for good. koan keeps no trash; undo is git's.

- **Input:** `folder` (required, not `/`), `contents` (default `false`), and `force` (default `false`).
- Without `contents`, the folder must hold nothing but its folder file, empty notes, and **disposable** entries: temp files koan left, and a `.DS_Store`.
- With `contents`, everything under it is removed. **Material blocks it unless `force` is given**: koan never deletes what it didn't write without being told to. Material here is every entry in the folder or in a folder under it that is not koan's and not disposable: an entry named with `@` that is not one of koan's, and hidden entries (`.git`, `.gitignore`, `.env`, `.venv`), included. A hidden entry may be something git can't restore, untracked or ignored, or another repository's history. Finding material takes a listing of each folder, never a look inside the material.
- Every task and folder outside the folder loses from its `blocked_by` the IDs of the removed tasks, of the folder, and of every folder under it; and the removed tasks' claims end. The folder is removed first, as [`task delete`](#task-delete) removes its task first.
- **A removal that fails partway** (a file the operating system won't remove, say in a read-only directory of material) still finishes the rest for the tasks and folders it did remove: those whose task file or folder file is gone leave every remaining `blocked_by`, in task files and folder files, and the tasks' claims end. Then it fails with the removal's error: `ids` lists the tasks removed, ascending, the message says how many other tasks and folders were rewritten, and `changed_paths` lists the folder and every task file and folder file rewritten. Tasks it did not remove are left as they were, and their references with them; running `folder delete` again, once the cause is fixed, removes the rest. If the cleanup itself fails, the error is still the removal's, with the cleanup's failure added to its message, and `doctor` finds the references left (`dangling-reference`, `stale-claim`).
- **Result:** `folder` (its record, as it was), `deleted` (IDs of the tasks removed), and `dependents` (IDs of the tasks and folders whose `blocked_by` changed).
- **Refuses:** `not-empty`, without `contents`; `has-material`, with `contents` and without `force`, listing in `paths` each material entry found, not what is inside it.

### folder list

Return the folders under a folder.

- **Input:** `folder` (default `/`), `recursive` (default `true`), and the [narrowing](#narrowing) fields `limit` and `fields`.
- Without `recursive`, only the folders directly in `folder`. The folder itself is not in the list: `folder show` returns it.
- **Result:** `folders`, `total`, `truncated`.
- **Order:** tree order.
- Every record carries its `readiness` and `empty`, so `folder list` loads every task file under `folder`, besides every folder file. `fields` cuts down what is returned, not what is loaded.

### folder block

Add blockers to a folder: everything in it, at any depth, then waits on them too. All of them are checked before anything is written.

- **Input:** `folder` (required, not `/`, which has no folder file), and `blocked_by` (required): one or more [tasks and folders](#references), open or done, not the folder itself, to add to its `blocked_by`.
- Blockers already there are ignored.
- A folder with no folder file, or one koan can't use, fails it, as for `folder update`.
- **Result:** `id`, `path`, `changed`.
- **Refuses:** `cycle`: a new blocker that contains the folder or is under it, or that already waits, through other tasks and folders, on the folder being done.

### folder unblock

Remove blockers from a folder.

- **Input:** `folder` (required, not `/`), and `blocked_by` (required): one or more IDs to remove from its `blocked_by`, which need not exist and need not be there, or paths, which must resolve.
- A folder with no folder file, or one koan can't use, fails it, as for `folder update`.
- **Result:** `id`, `path`, `changed`.

### folder why

Say what a folder is waiting on to be done, by following its open tasks, at any depth, as [`task why`](#task-why) follows a task.

- **Input:** `folder` (required).
- **Result:** as `task why`'s: `readiness` (the folder's own), `ready` (the IDs of the ready tasks under it, and of the ready tasks those under it wait on, in work order: working on these is what makes it done), `stuck`, and `stuck_folders`.
- A done folder, an empty one included, has every list empty. A folder not done has at least one of them non-empty.

## Tasks

### task create

Create an open task.

- **Input:**

| Field | Default | Holds |
|---|---|---|
| `title` (required) | | Trimmed before it is checked and stored. |
| `folder` | `/` | Where the task goes. |
| `priority` | `null` | |
| `tags` | `[]` | |
| `blocked_by` | `[]` | Existing [tasks and folders](#references), open or done. |
| `blocks` | `[]` | Existing [tasks and folders](#references), open or done, that wait on the new task: it is added to each one's `blocked_by`. A folder in `blocks` holds back everything in it until the new task is done. |
| `extra` | `{}` | |
| `notes` | `""` | The initial content of the `@<id>.md`. |

- The new ID is the next one above both the counter and the tree's highest ID ([IDs](design.md#ids)), after the IDs of any folders made for it. A failure after the counter is raised uses up the ID.
- The new task never exists without everything in `blocks` waiting on it. A kill partway can instead leave some of them waiting on an ID with no task, a dangling reference `doctor` finds, as [`task delete`](#task-delete) can leave one.
- A failure writing the notes leaves the task created without them: the error's `ids` is the new task, and its `changed_paths` what was written. The `@<id>.md` is always written, empty when no notes are given, and never over a file already there: when one is, `task create` fails with `io`, and the new task has that file as its notes.
- The folder's path is checked before an ID is used, so `not-a-folder` changes nothing.
- A new task closes a [cycle](#cycles) only through its `blocked_by` and `blocks`: when one of them contains its folder or is it (`blocked_by` naming its own folder, `blocks` naming a folder above it), when something in `blocks` is in `blocked_by` too, or when something in `blocked_by` already waits, directly or through other tasks and folders, on something in `blocks` or on the new task's folder being done. That is refused before an ID is used. With neither, a task created in any folder closes none.
- Everything in `blocks` is read before an ID is used, so a task file or folder file that can't be used refuses the command with its error, and changes nothing. Only an I/O error after one is rewritten can leave tasks and folders waiting on the new one when it was not created: the error's `ids` then lists those rewritten, in ascending order, its `message` names them, and its `changed_paths` what was written. Rewritten ones that wait on a task never created are a dangling reference: create it again and point them at it with `task block` or `folder block`, or remove the reference with `task unblock` or `folder unblock`.
- **Result:** `id`, `changed` (always `true`), `folders_created`, and `dependents`: the IDs in `blocks`, ascending.
- **Refuses:** `cycle`, as [Cycles](#cycles) says; the new task, which has no ID yet, is left out of `ids`.

### batch

Create folders, and open tasks in them, in one write, where later tasks may wait on earlier ones: a piece of work laid out whole. Everything is checked before anything is written.

- **Input:** `folders` (default `[]`), the folders to make; `folder` (default `/`), the folder of every task that names none; and `tasks` (default `[]`), each with `task create`'s fields but `blocks`. The two lists hold 1–1000 entries between them.
- **No `blocks`**, in folder entries or task entries: inside a batch every edge is a `blocked_by`. Making something already in the tree wait on something new stays with `task create --blocks` and `folder create --blocks`, or `task block` and `folder block` after the batch.
- **A folder entry** has `folder create`'s fields but `blocks`: `folder` (required, a path, not `/`), `title` (default `null`), `extra` (default `{}`), `notes` (default `""`), and `blocked_by` (default `[]`). No two entries have one path. A task goes into a folder of the batch by naming its path in its own `folder`, as into any other.
- **A folder entry's `blocked_by`** holds IDs of existing tasks and folders, in any form, and folder paths: of folders already there, or of folders the batch makes, as entries or as needed for a task. Not refs: the folders are made before the tasks.
- **A task entry** has, besides `task create`'s fields:

| Field | Holds |
|---|---|
| `ref` | A name for this task within the batch, following the tag rule, with at least one letter, so that no ref reads as an ID. Unique in the batch. Never stored. |
| `blocked_by` | A mix of existing tasks' and folders' IDs, in any form (`42`, `"42"`, `"@42"`); folder paths, as for a folder entry; and the `ref`s of **earlier** tasks in the batch. |

- Refs are global to the batch, and may only point backwards. The three forms can't be mistaken for each other: a ref has a letter in it, a path starts with `/`, and an ID is digits.
- Folder paths can make a cycle in a batch, where refs alone can't: two folder entries that wait on each other, or a task waiting on a folder that holds it. So a batch is checked for [cycles](#cycles) as a whole, with the tree, before an ID is used.
- A `blocked_by` string that is a valid ref but names no earlier task is `invalid-input` saying so; any other string that is not an ID or a folder path is `invalid-input` saying what an entry may be: an ID, like `42` or `@42`, a folder path, like `/proj/research`, or the ref of an earlier task. A path that names no folder of the tree or of the batch is `not-found`, before an ID is used.
- No two folder paths in the batch, among its folder entries, its tasks' folders, its `blocked_by` entries, and its `folder`, differ only in case ([Folders](design.md#folders)): `invalid-input`, before the tree is read.
- Every folder is checked before an ID is used: each folder entry, that nothing is at its `folder`; each task's folder given by path, that it can be made; one given by ID, that it exists; and the batch's `folder`, the same way, even when no task falls back to it.
- A folder entry's missing parents, and a task's missing folder, are made as needed, with no title, as by any command.
- **IDs**: the folders the batch makes take theirs first, in tree order, parents before children, entries and folders made as needed alike; then the tasks, consecutively, in input order. Every task gets the same `created_at`.
- A folder entry is made with its fields and notes, its `blocked_by` holding IDs: a path names its folder's ID, set aside before anything is written, so a folder may name one the batch makes after it.
- A failure partway leaves the folders made so far, parents first, and the first tasks of the batch, and not the rest: the error's `folders` lists the folders made, in tree order, the last perhaps without its folder file, its `ids` the tasks created, in input order, and `changed_paths` everything written. To finish, drop the folders and tasks made from the input, replace each `ref` to a task made with its ID, and run the rest. The IDs set aside for what was not made are not reused.
- **Result:** `ids` (the tasks', in input order), `refs` (each `ref` and the ID it got), `folders` (each folder the batch made, by path, and the ID it got), and `folders_created` (their paths, in tree order).
- **Refuses:** `cycle`; and `destination-exists`, when something is at a folder entry's `folder`, listing every such path in `folders` and `paths`: a folder entry means one to make. To put tasks into a folder already there, name it in their `folder`; to set its fields, use `folder update`. A folder entry that appears while the batch runs, only by an outside change, is refused the same way, after the folders before it are made: then it is named in the message and `paths`, and `folders` lists those made, as for any failure partway.

### task show

Return one task.

- **Input:** `id` (required).
- **Result:** `task`, its record.

### task update

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

- **Result:** `id`, `changed`.

### task done

Mark a task done: `completed_at` is set to the command's time, and its claim ends. Blockers do not matter: a task can be marked done at any time.

- **Input:** `id` (required).
- The task file is written first, then the state file. Run again after a failure between the two, it removes the claim that was left.
- **Result:** `id`, `changed`.

### task reopen

Make a done task open again: `completed_at` becomes `null`. The task starts unclaimed.

- **Input:** `id` (required).
- **Result:** `id`, `changed`.

### task block

Add blockers to a task. All of them are checked before anything is written.

- **Input:** `id` (required), and `blocked_by` (required): one or more existing [tasks and folders](#references), open or done, not including `id`, to add to its `blocked_by`.
- Blockers already there are ignored.
- **Result:** `id`, `changed`.
- **Refuses:** `cycle`, as [Cycles](#cycles) says: a new blocker that is a folder holding the task, or that already waits on `id`, directly or through other tasks and folders.

### task unblock

Remove blockers from a task.

- **Input:** `id` (required), and `blocked_by` (required): one or more IDs to remove from its `blocked_by`, or folder paths. IDs need not exist, which is how a dangling reference is cleared by hand, and need not be there; a path must name a folder with an ID ([References](#references)).
- **Result:** `id`, `changed`.

### task move

Move a task, with its notes, to another folder. Nothing in the task file changes, `updated_at` included.

- **Input:** `id` (required), and `to` (required): the folder to move it into.
- The task file moves first, then the notes.
- In `to` the task inherits new blockers, and `to` and the folders above it hold it, so the move is checked for a [cycle](#cycles) first: a task blocked by `to` or a folder above it, or one that waits, through others, on something waiting on `to`. That takes a walk, every folder file, and the files the search reaches.
- **Result:** `id`, `changed`, `folders_created`.
- **Refuses:** `destination-exists`, when `to` already holds a non-empty `@<id>.md` with the task's name. An empty one is replaced. `cycle`.

### task delete

Remove a task and its notes, for good, and remove its ID from every other task's and every folder's `blocked_by`. Its claim ends. What waited only on it becomes ready, as if it had been done.

- **Input:** `id` (required).
- The task file need not be usable: `task delete` goes by its filename, which makes it the way to remove a broken task. Its record is then `null`, with an `unusable-file` warning.
- A kill partway leaves either the task, perhaps without its notes, and `task delete` can be run again; or the task gone and other tasks or folders still waiting on it, a dangling reference `doctor` finds. It never leaves them freed with the task still there, nor notes without their task.
- **Result:** `id`, `task` (its record, as it was), and `dependents` (IDs of the tasks and folders whose `blocked_by` changed).

## Queries

### task frontier

Return the tasks that are ready and unclaimed: what can be taken now. It returns tasks only; folders change it only through readiness. It is exactly what [`task claim-next`](#task-claim-next) draws from. A ready task that is claimed is not here; `task list` with `readiness: ["ready"]` shows those too, and with `claimed: true` shows what is out.

- **Input:** `folder` (default `/`), `recursive` (default `true`), and the [narrowing](#narrowing) fields.
- **Result:** `tasks`, `total`, `truncated`.
- **Order:** work order.

### task list

Return the tasks in scope, whatever their readiness.

- **Input:** `folder` (default `/`), `recursive` (default `true`), the [narrowing](#narrowing) fields, and:

| Field | Default | Effect |
|---|---|---|
| `readiness` | `["ready", "blocked"]` | Which tasks to return. Done tasks pile up for ever, so they are left out unless asked for. |

- **Result:** `tasks`, `total`, `truncated`.
- **Order:** tree order.

### task why

Say what a task is waiting on, by following what blocks it, its own blockers and inherited ones, and what blocks those, through open tasks and folders not done. A folder is followed into the open tasks under it, at any depth, since they are what it waits on to be done.

- **Input:** `id` (required).
- **Result:**

| Field | Holds |
|---|---|
| `readiness` | The task's own. |
| `ready` | The IDs of the ready tasks it waits on, in work order: working on these is what moves it. Just the task itself when it is ready. Always tasks, never folders. |
| `stuck` | The IDs no work will clear, ascending: blockers that are missing or unusable, and tasks and folders that wait on each other. |
| `stuck_folders` | The paths of folders it reaches whose blockers are unknown, since their folder file is missing or can't be used, in tree order. `koan repair` or a person clears these, not work. |

- A done task has every list empty. An open task has at least one of them non-empty.

### where

Turn a [reference](design.md#references) into where it is on disk, or a path on disk into its reference: the two forms for the key-plus-hint convention, and the absolute paths to open.

- **Input:** exactly one of `ref`, a reference given as a string (`@42`, `/proj`, `/proj:docs/plan.md`, `@41:docs/plan.md`, each ID with or without the `@`), and `file`, an absolute path on disk.
- An ID in `ref` names whichever task or folder has it; with material after it, a folder. Either way it reads every folder file, as a folder given by ID does: two holders is `duplicate-id`, and each folder file koan can't use is an `unusable-file` warning.
- **From `file`:** the path is made clean without touching the disk: `a/../b` is `b`.
- It must be the tree's directory or under it, as `~/koan-root` or as the directory it links to.
- A path that is neither is tried again with the symlinks in its longest existing prefix resolved, so one reaching the tree through a link of its own (`~/k` for `~/koan-root`) is found. This reads only the path's own directories.
- Anywhere else is `not-found`.
- Its leading `@` folder names lead through folders, each of which must exist. What follows names the folder itself (nothing, `@folder.json`, or `@folder.md`); a task (`@42.json` or `@42.md`, the task file existing); or material, the rest of the path, whose first entry is not named with `@`. Any other `@` entry there, a folder name on something that is not a folder included, is not one of koan's and has no reference: `not-found`.
- The folders must exist, and a task must; material need not, and is never looked at. An ID given for material that is a task's is `not-found`, saying so.
- **Result:**

| Field | Holds |
|---|---|
| `ref` | The stable reference: `@<id>`, followed by `:` and the material for material. A folder with no ID (the root, or one with no folder file) is given by its path instead. |
| `hint` | The readable form: the folder's path, followed by `:` and the material for material; a task's title for a task. |
| `folder` | The record of the folder named, the folder holding the material, or the task's folder. |
| `task` | The task's ID, or `null`. |
| `material` | The material part, or `null`. |
| `file` | The absolute path of the material, or of the task's `@<id>.md`; `null` for a folder, whose record has `dir`. |

- A folder file koan can't use fails it with `corrupt`, as for `folder show`.
- Kept text writes `<ref> (<hint>)`: `koan where @41:docs/plan.md | jq -r '.result | "\(.ref) (\(.hint))"'`.

## Claims

What a claim is and when it ends is in [design.md](design.md#claims).

### task claim

Claim a task.

- **Input:** `id` (required), and `extra` (default `{}`), which is stored with the claim and returned with it.
- The task may be ready or blocked. No task file is written.
- **Result:** `id`, `changed` (always `true`: a claim already there is refused).
- **Refuses:** `task-done`; and `claimed`, whoever holds the claim, the caller included. The error's `message` and the task's `claim`, from `task show`, say what the existing claim's `extra` is.

### task claim-next

Claim the first task of the frontier. It is `task frontier` and `task claim` under one lock, so two callers asking at once never get the same task.

- **Input:** `folder`, `recursive`, `tags_any`, and `tags_all`, as [`task frontier`](#task-frontier) takes them, and `extra` as [`task claim`](#task-claim) does.
- **Result:** `task`, the claimed task's record, or `null` when the frontier is empty. That is a success, and writes nothing.
- Each call that finds a task claims one, so running it again after an unclear outcome may claim a second.

### task release

End a task's claim, whoever made it.

- **Input:** `id` (required).
- The task need not exist: releasing goes by the entry in the state file.
- **Result:** `id`, `changed` (`false` when there was no claim).
