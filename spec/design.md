# koan design

Task management on one machine, with the filesystem as the database: no daemon, no index, no server. This document is the data model and the rules every command follows. Commands are specified in `operations.md`; the JSON Schemas in `schemas/` are normative for every file and every input and output.

Linux and macOS only.

## Where things live

| What | Path | Travels with git or sync |
|---|---|---|
| **Tree**: folders, tasks, and `@koan.json` | `~/koan-root` | Yes |
| **State file**: the ID counter and the claims | `~/.config/koan/state.json` | No: it is this machine's |

Both paths are fixed, on both platforms, and are found from `HOME`, which must be an absolute path. There is no config file. To keep the tree somewhere else, make `~/koan-root` a symlink to it: koan resolves that one link, and follows no symlink inside the tree.

One tree per user, written from one machine. Separate bodies of work are folders of the one tree (`/work`, `/home`). The tree must be on a local filesystem, since the lock relies on `flock`.

## Assumptions

- **koan is the only writer of the tree and the state file.** Anything else that changes them is an **outside change**, and koan's guarantees hold only without one. Git and sync tools are welcome to carry the tree; what a merge or restore leaves inconsistent is an outside change, which [`doctor`](#doctor-and-repair) finds.
- **Notes are the exception.** A task's `@<id>.md` and a folder's `@folder.md` are edited with any editor. koan never reads meaning into them.
- **koan owns what is named with `@`.** Every other entry in the tree is **material**: yours, whatever its name, hidden entries (`.git`, `.obsidian`) included. koan never reads material, never walks into it, and changes it only by removing a folder that holds it, when told to ([`delete-folder`](operations.md#delete-folder) with `force`). Only koan writes `@` names, so a folder made by hand, by an editor, or by a build tool is material, never a folder of koan's.

## Data model

### Folders

A **folder** is a directory named `@` and a folder name, directly in the root or in another folder. It nests to any depth, and holds any number of tasks and folders, and any material:

```text
@proj/@folder.json     the folder file: its fields
@proj/@folder.md       its notes: prose, possibly empty
@proj/@travel/         a folder in it
@proj/@42.json         a task in it, with its notes @42.md
@proj/docs/            material
```

A directory named with `@` inside material is material too: a walk never looks there, so npm's `@types/` in a `node_modules/` is never taken for a folder.

The folder file holds the folder's fields:

```json
{
  "schema": 1,
  "id": 57,
  "title": null,
  "extra": {}
}
```

Every key is required and no other key is allowed.

| Field | Rule |
|---|---|
| `schema` | The format this file is in (see [Formats](#formats)). |
| `id` | The folder's identity, from the same counter as task IDs (see [IDs](#ids)). Never changes: moving or renaming the folder keeps it. |
| `title` | `null`, or a title by the task rule. |
| `extra` | Any JSON object. koan stores it and never interprets it. |

`create-folder`, `update-folder`, and a `create-batch` folder entry set `title` and `extra`; a folder made as needed by another command gets `null` and `{}`.

A folder exists when its directory does. A missing `@folder.md` reads as empty notes. A folder with no folder file is still a folder, whose tasks and folders count as usual; it has no ID until one is written, and `doctor` reports it.

The **root** is the folder `/`. It holds `@koan.json` in place of a folder file, and has no ID.

A folder name is 1–64 ASCII letters, digits, and hyphens, not starting or ending with a hyphen:

```text
^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$
```

Names are case-sensitive: koan compares them byte for byte, so `Proj` and `proj` are two folders. That holds on Linux. macOS filesystems usually ignore case, so there the two are one directory, and a tree that has both on Linux cannot be copied to a Mac intact. koan does not check for this; a tree that moves between the two should not hold names that differ only in case.

A **folder path** names a folder from the root: `/` is the root, `/proj/travel` is `travel` inside `proj`. Leading `/`, no trailing `/`, no empty segment, no `.` or `..`. Paths never carry the `@`: `/proj/travel` is `@proj/@travel` on disk, so a name is at most 65 characters there.

### Tasks

A task is two files in a folder, named by its ID:

```text
@proj/@42.json    the task file: its fields
@proj/@42.md      its notes: prose, possibly empty
```

The task file is the task. A task exists exactly when its task file does, and a missing `@<id>.md` reads as empty notes. A `@<id>.md` with no task file beside it is not a task: commands ignore it, and `doctor` reports it.

```json
{
  "schema": 1,
  "id": 42,
  "title": "Book flights",
  "priority": 2,
  "created_at": "2026-09-20T18:31:51Z",
  "completed_at": null,
  "updated_at": "2026-09-20T18:31:51Z",
  "blocked_by": [17],
  "tags": ["travel"],
  "extra": {"status": "waiting on quote"}
}
```

Every key is required and no other key is allowed.

| Field | Rule |
|---|---|
| `schema` | The format this file is in (see [Formats](#formats)). |
| `id` | The task's identity, equal to the ID in its filename. Never changes. |
| `title` | 1–200 characters after trimming whitespace, with no control characters or line breaks. |
| `priority` | An integer or `null`. Higher is more important; `null` ranks last. |
| `created_at` | When the task was created. Never changes. |
| `completed_at` | `null` while the task is **open**; a timestamp once it is **done**. This alone is the task's state. Done covers every way a task ends, cancelled included. |
| `updated_at` | When koan last changed a field of this file. Moving a task or editing its notes does not count. |
| `blocked_by` | The set of IDs of the tasks this one waits on, never its own. |
| `tags` | A set of labels, each following the folder-name rule but lowercase only: `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`. |
| `extra` | Any JSON object. koan stores it and returns it, and never interprets it. A workflow `status` goes here: one key holds one value, where tags could hold two. |

Integers are within ±(2^53 − 1), and written without a fraction or an exponent: `4`, never `4.0`. Timestamps are UTC to the second, `YYYY-MM-DDTHH:MM:SSZ`, and name a real date and time. Keys are matched exactly: `Title` is not `title`. The schemas cannot say these three things; koan checks them.

### IDs

An ID is a positive integer of at most 15 digits, unique across the whole tree, since `blocked_by` names tasks by ID alone. Tasks and folders draw from one counter, so an ID names exactly one task or one folder. IDs come from the counter `last_id` in the state file, which only goes up: an ID is never issued twice, even after its task or folder is deleted. Gaps are normal.

The counter is outside the tree on purpose, so that restoring or merging the tree's files can never lower it. That is also why one machine writes a tree: two machines creating tasks in copies of it would issue the same IDs.

### References

A **reference** names a task, a folder, or material in a folder, in commands and in anything kept outside koan: notes, prompts, commit messages.

| Written | Names | Holds |
|---|---|---|
| `42` or `@42` | task 42 | until it is deleted |
| `/proj/travel` | a folder, by path | until a move |
| `41` or `@41` | folder 41 | until it is deleted |
| `/proj/travel:docs/plan.md` | material in a folder, by path | until a move |
| `41:docs/plan.md` or `@41:docs/plan.md` | material in folder 41 | while the file stays |

- **The `@` is optional on input**, and koan always writes it, in `where`'s `ref` and in the convention below (JSON results carry IDs as plain integers): `@41` stands out as a reference in prose, and in `git log --grep '@41'`, where a bare `41` does not.
- **A reference by ID is stable**: IDs never change, so it survives moves and renames. A path is readable. Kept text writes both, the reference and its path as a hint: `@41 (/proj/travel)`, `@41:docs/plan.md (/proj/travel:docs/plan.md)`. Reading one, the reference wins; a hint that no longer matches has gone stale. koan never reads notes, so this is a convention, not a format; [`where`](operations.md#where) gives both forms.
- **An ID** names a task or a folder, whichever has it, since they share the counter. Where only one kind is taken, the other is `not-found`, with a message saying what the ID is. The root has no ID, and is only `/`.
- **The part after `:`** is a path to material, from the folder: segments separated by `/`, none empty, `.`, or `..`, and the first not starting with `@`, which would name one of koan's own entries. Further down is inside material, where `@` means nothing (`@41:node_modules/@types/x`). So a file has one reference per folder form. The colon can't occur in a folder path, so the first one splits the two; the material part may hold more.
- A material reference says where a file is or would be; the file need not exist, and koan never looks.
- **Finding folder 41** takes a walk that reads every folder file, since a folder's ID is only in its folder file. Two folder files with the ID, or a folder file and a task file, is `duplicate-id`. A folder whose folder file is missing or can't be used has no ID to be found by.

### Dependencies

`blocked_by` makes a graph, with an edge from a task to each task it waits on. Folders play no part in it.

- An open task is **ready** when every task in its `blocked_by` is done, and **blocked** otherwise. A blocker that does not exist, or whose file is unusable, counts as blocking.
- A done task is neither.
- Blockers constrain readiness, not completion: a task can be marked done at any time.

Readiness is derived when read, and never stored.

### Invariants

Every tree that only koan has written satisfies these, and koan refuses any change that would break one:

1. **Acyclic.** No task waits on itself, directly or through other tasks, whether they are open or done.
2. **No dangling references.** Every ID in a `blocked_by` names an existing task. Deleting a task removes its ID from every `blocked_by`.
3. **Unique IDs.** No two task files or folder files have the same ID.
4. **IDs within the counter.** No task's or folder's ID is above `last_id`.

### `@koan.json`

The root holds one metadata file, which marks the directory as a tree and records its format:

```json
{"schema": 2}
```

### The state file

```json
{
  "schema": 1,
  "last_id": 57,
  "claims": {
    "42": {"claimed_at": "2026-10-10T14:02:11Z", "extra": {"agent": "shingi-3"}}
  }
}
```

`last_id` is the highest ID ever issued, and `claims` has one entry per [claimed](#claims) task, keyed by ID. It is written only under the [lock](#concurrency).

### Formats

`schema` in `@koan.json` is the **tree format**: the tree's layout, what is named how. `schema` in a task file or a folder file is that **file's format**. The two are numbered apart, and change apart: format 2 of the tree holds task files and folder files in format 1. A binary reads and writes exactly one tree format, N, and one format of each kind of file; the table is about the tree format.

| `@koan.json` says | Then |
|---|---|
| N | The tree is usable. |
| Less than N | Every command but `migrate`, `doctor`, `init`, `info`, and `version` fails with `migration-pending`. |
| More than N | Every command but `init`, `info`, and `version` fails with `unsupported-format`: a newer koan wrote this tree. |

- **Every change to a file's fields is a new format**, and ships with a conversion from the one before.
- **`migrate` converts each file by its own `schema`**, to its kind's current format, under the lock, and writes `@koan.json` last. A file already in its current format is not touched, so a run cut short is finished by running it again, and old files a merge brings in later are converted by the next run.
- **A migration is not an edit**: it changes no field a task had, `updated_at` included.
- **Forward only.** Undoing a migration is git's job: commit the tree first.
- The state file is not in the tree, so `migrate` does not cover it: a binary reads every earlier format of it, and writes the current one.
- **Before the first release, a format may change without a conversion.** On a tree in a format the binary has no conversion from, `migrate` and `doctor` fail with `conflict` (`no-conversion`) as well: move the tree aside and start a new one with `init`.
- Format 2 (the `@` names, folder files, and `@koan.json`) has no conversion from format 1. A format-1 tree has no `@koan.json`, so this binary finds it not initialized; `init` there adopts it as any folder, and everything format 1 wrote is material.

### Written files

koan writes JSON with two-space indentation, keys in schema order, `blocked_by` and `tags` sorted, `extra`'s keys sorted, and a trailing newline, so that git diffs show only real changes. It reads any valid JSON.

## Reading a tree

### Root states

| State | When | Commands fail with |
|---|---|---|
| Not initialized | `~/koan-root`, its `@koan.json`, or the state file is missing. | `not-initialized`. Run `init`. |
| Unusable | `~/koan-root` is not a folder; or `@koan.json` or the state file is there but can't be read or is invalid. | `conflict` (`not-a-folder`), `corrupt`, or `io`. |
| Needs migration, or too new | See [Formats](#formats). | `migration-pending`, `unsupported-format`. |
| Usable | Otherwise. | — |

`init` creates whatever is missing and changes nothing that is there. A state file it creates beside an existing tree starts `last_id` at the highest ID in any filename in the tree. That is how a tree cloned onto a new machine is attached, and how a lost state file is replaced; in the second case an ID whose task was deleted can be issued again.

### Walking

There is no index: a command that looks a task up by ID walks the tree.

- A walk enters only folders: directories named `@` and a folder name, in the root or in a folder it entered. Within a folder, an entry named with `@` is a task file `@<id>.json`, notes `@<id>.md`, the folder file `@folder.json` or notes `@folder.md` (not in the root), or a folder. Every other entry is material, and is skipped without a look.
- An entry named with `@` that is none of these, a symlink, or of the wrong type (a directory named `@42.json`) is skipped too, and `doctor` reports it.
- A walk lists entries by name only. Folder files are read only by the commands that need folder IDs or records, by any command given a folder by ID, or `where` given any ID, and by a `not-found` for a task ID, to say when it is a folder's.
- A folder that can't be listed fails the command with `io`: it cannot answer without the whole tree.
- **A duplicated ID fails any command that meets it**, with `conflict` (`duplicate-id`). koan never picks a copy. `doctor` lists the copies, for a person to resolve.
- **An unusable task file** (unreadable, not valid JSON, breaking the schema, or in another format) fails a command that was asked for that task. A command that only passes it, or finds it as a blocker, reports an `unusable-file` warning and carries on.
- **A dangling reference** counts as blocking, and is reported as a warning.

## Concurrency

Several processes may use one tree at once, with no coordinator between them.

- **One lock**: a `flock` on the root directory itself. There is no lock file, and the lock is gone when its holder exits, however it exits.
- **Writes are serialized.** A write holds the lock alone, from before it reads anything to after its last change.
- **A read sees the tree between writes**, never part of one. Reads share the lock: any number run at once, a read waits for a write to finish, and a write for the reads before it. `info` and `version` take no lock.
- **Input is checked before the lock is taken.** A command that finds the lock held against it waits up to 5 seconds, then fails with `busy`, which is always safe to retry.
- **A write decides on current state**: everything it depends on is read after it has the lock. So two writes to one task both take effect, and no write breaks an [invariant](#invariants).
- **Files are replaced atomically**: written to a hidden temp file, flushed, then renamed. A reader never sees half a file koan wrote.
- **Folders are made whole**: built under a hidden temp name with their folder file and empty notes, then renamed into place. A reader never sees a new folder without its folder file.
- `doctor` holds the lock alone, as a write does, though it changes nothing: on a large tree it is the command others are most likely to wait for.

### Crashes and failures

A command that fails after changing something says what it changed. A command that is killed, or a machine that crashes, can leave a multi-file change half done: an ID used with no task or folder, a `@<id>.md` without its task file, a reference to a task that is gone or was never made, a temp file or folder. None of these stops later commands, and `doctor` finds all of them.

koan keeps no journal and takes no idempotency keys. After an unclear outcome, look (`show`, `list`, `doctor`), then decide. Commands that set a state (`done`, `reopen`, `block`, `unblock`, `update`, `release`) are safe to run again; `create` and `claim-next` make a new task or claim each time.

## Claims

A **claim** reserves a task: once it is claimed, no other `claim` of it succeeds until the claim ends. It is how two agents taking work from one tree are not handed the same task, which reading the list of ready tasks cannot give.

- `claim` takes an open task that has no claim, and fails at once (`conflict`, `claimed`) on one that has. `claim-next` claims the first ready, unclaimed task in scope.
- A claim is an entry in the state file. It outlasts the process that made it and never expires.
- A claim ends with `release`, or when its task is marked done or deleted. A reopened task starts unclaimed.
- **A claim refuses other claims and takes the task off the frontier, and nothing else.** Every command that changes a task treats a claimed one like any other, and `release` works whoever made the claim.
- **A claim does not lock the notes.** Nothing stops two editors from overwriting each other's changes to a `@<id>.md`. The convention is that whoever holds the claim is the one who edits the task's notes.
- A claim carries a `claimed_at` and an `extra` object, which koan returns and never interprets. Who holds a claim, and whether they are still alive, is the caller's business.
- Claims are this machine's: they do not travel with the tree.

`done` and `delete` change the task first and the state file second. An entry left behind by a crash between the two names a task that is done or gone; commands ignore such an entry, and `repair` removes it (an entry for a task that is gone, when asked for it by name).

## Doctor and repair

`doctor` walks the whole tree and reports what it finds, each finding with a kind, the paths and IDs involved, and a sentence saying what to do. `repair` fixes the kinds that can be fixed without losing anything or choosing between versions, and leaves the rest to a person. A reference or a claim to a task that seems gone it removes only when asked for its kind by name: the task may only be out of koan's sight, in a folder moved by hand, and removing its ID would lose the link for good, where moving the folder back would restore it.

| Finding | `repair` |
|---|---|
| `temp-leftover`: a temp file koan left behind | Removes it. |
| `id-above-counter`: a task or folder ID above `last_id` | Raises `last_id`. |
| `dangling-reference`: a `blocked_by` ID with no task | Removes the ID, when asked for by name. |
| `stale-claim`: a claim on a task that is done or gone | Removes the claim; on a task that is gone, when asked for by name. |
| `orphan-notes`: a `@<id>.md` with no task file beside it | Removes it when empty; moves it to its task when the task is elsewhere without notes. Otherwise leaves it. |
| `missing-folder-file`: a folder with no `@folder.json` | Writes one, with a new ID. |
| `duplicate-id`: several task files or folder files with one ID | — |
| `cycle`: tasks that block each other | — |
| `unusable-file`: a task file or folder file that can't be used, with the reason, a folder file in another format included (there is one folder file format yet) | — |
| `old-format`: a task file in an earlier format | — (`migrate` converts it) |
| `skipped-entry`: an entry named with `@` that is not one of koan's, a symlink or wrong-typed entry so named (a folder file is `unusable-file` instead), or a folder that can't be listed | — |

## Errors

Every failure has one **kind**:

| Kind | Meaning |
|---|---|
| `invalid-input` | The input breaks a rule in this document or the command's schema. |
| `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The root is not usable (see [Root states](#root-states)); or, for `corrupt` and `unsupported-format`, a task file the command needs is not. |
| `busy` | The lock was held against the command for 5 seconds. |
| `not-found` | A task or folder named in the input does not exist. |
| `conflict` | The input is valid but the tree's state refuses it; a `rule` says which: `duplicate-id`, `claimed`, `cycle`, and the commands' own. |
| `io` | The operating system refused something. |
| `internal` | A bug in koan. |

A command reports one error. Input is checked first, then the root's state, then `busy`; among errors after that, which one is reported is not specified.

A problem that does not stop the command is a **warning**, returned beside the result: `unusable-file` and `dangling-reference`.
