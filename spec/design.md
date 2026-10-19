# koan design

Task management on one machine, with the filesystem as the database: no daemon, no index, no server. This document is the data model and the rules every command follows. Commands are specified in `operations.md`; the JSON Schemas in `schemas/` are normative for every file and every input and output.

Linux and macOS only.

## Where things live

| What | Path | Travels with git or sync |
|---|---|---|
| **Tree**: folders, tasks, and `@koan.json`, which holds the ID counter | `~/koan-root` | Yes |
| **State file**: the claims | `~/.config/koan/state.json` | No: it is this machine's |
| **Markers**: which folder a directory outside the tree is for | `.koan/` in each directory bound to one | No: git ignores them |

The tree's and the state file's paths are fixed, on both platforms, and are found from `HOME`, which must be an absolute path. There is no config file. To keep the tree somewhere else, make `~/koan-root` a symlink to it: koan resolves that one link, and follows no symlink inside the tree.

One tree per user, written from one machine. Separate bodies of work are folders of the one tree (`/work`, `/home`). The tree must be on a local filesystem, since the lock relies on `flock`.

## Assumptions

- **koan is the only writer of the tree, the state file, and the [markers](#markers).** Anything else that changes them is an **outside change**, and koan's guarantees hold only without one. Git and sync tools are welcome to carry the tree; what a merge or restore leaves inconsistent is an outside change, which [`doctor`](#doctor-and-repair) finds. A merge of two lines of history, two machines writing one tree, or putting back an older `@koan.json` without the files beside it, is such a change: only a single line of history, written from one machine, keeps the [invariants](#invariants) (see the README's [Keep the tree in git](../README.md#keep-the-tree-in-git)).
- **Notes are the exception.** A task's `@<id>.md` and a folder's `@folder.md` are edited with any editor. koan never reads meaning into them.
- **koan owns what is named with `@`.** Every other entry in the tree is **material**: yours, whatever its name, hidden entries (`.git`, `.obsidian`) included. koan never reads material, never walks into it, and changes it only by removing a folder that holds it, when told to ([`folder delete`](operations.md#folder-delete) with `force`). Two kinds of entry are **disposable** instead, and go with their folder: a temp file koan left behind (`.koan-tmp-*`), which is koan's own, and a `.DS_Store`. Only koan writes `@` names, so a folder made by hand, by an editor, or by a build tool is material, never a folder of koan's.

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
  "schema": 2,
  "id": 57,
  "title": null,
  "blocked_by": [41],
  "extra": {}
}
```

Every key is required and no other key is allowed.

| Field | Rule |
|---|---|
| `schema` | The format this file is in (see [Formats](#formats)). |
| `id` | The folder's identity, from the same counter as task IDs (see [IDs](#ids)). Never changes: moving or renaming the folder keeps it. |
| `title` | `null`, or a title by the task rule. |
| `blocked_by` | The set of IDs of the tasks and folders that everything in this folder waits on, at any depth (see [Dependencies](#dependencies)). Never its own. |
| `extra` | Any JSON object. koan stores it and never interprets it. |

`folder create`, `folder update`, and a `batch` folder entry set `title` and `extra`; `folder create`, `folder block` and `folder unblock`, and a `batch` folder entry set `blocked_by`. A folder made as needed by another command gets `null`, `[]`, and `{}`.

A folder exists when its directory does. A missing `@folder.md` reads as empty notes. A folder with no folder file is still a folder, whose tasks and folders count as usual; it has no ID until one is written, and `doctor` reports it. Its `blocked_by` is unknown, so everything in it is held back (see [Dependencies](#dependencies)).

The **root** is the folder `/`. It holds `@koan.json` in place of a folder file, and has no ID.

A folder name is 1–64 ASCII letters, digits, and hyphens, not starting or ending with a hyphen:

```text
^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$
```

Names are compared byte for byte, but koan never lets two names in one folder differ only in case, on any platform. macOS filesystems usually ignore case, so there `Proj` and `proj` would be one directory; refusing the pair everywhere means a tree made on Linux always moves to a Mac intact.

- **A path names a folder only in its exact case.** When a part of a path matches no entry in its folder exactly but matches one ignoring case, the command fails with `conflict` (`case-mismatch`), naming the path as it is on disk. So `/Proj` never reaches `/proj`, on a Mac or elsewhere, and a new folder is never made beside one whose name differs from it only in case.
- **One input never names two paths that differ only in case** in the same part (`/Proj/a` and `/proj/b`): `invalid-input`. The one exception is `folder move` renaming a folder to the same name in another case.
- A tree that already holds such a pair, made by hand or by an older koan, still works on Linux: an exact match always wins.

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
| `blocked_by` | The set of IDs of the tasks and folders this one waits on, never its own. |
| `tags` | A set of labels, each following the folder-name rule but lowercase only: `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`. |
| `extra` | Any JSON object. koan stores it and returns it, and never interprets it. A workflow `status` goes here: one key holds one value, where tags could hold two. |

Integers are within ±(2^53 − 1), and written without a fraction or an exponent: `4`, never `4.0`. Timestamps are UTC to the second, `YYYY-MM-DDTHH:MM:SSZ`, and name a real date and time. Keys are matched exactly: `Title` is not `title`. A `blocked_by` never holds its own file's ID, in a task file or a folder file. The schemas cannot say these four things; koan checks them, and a file that breaks one can't be used.

### IDs

An ID is a positive integer of at most 15 digits, unique across the whole tree, since `blocked_by` names tasks and folders by ID alone. Tasks and folders draw from one counter, so an ID names exactly one task or one folder. IDs come from the counter `last_id` in [`@koan.json`](#koanjson), which only goes up: an ID is never issued twice, even after its task or folder is deleted. Gaps are normal.

**The counter is in the tree**, so it travels with it: a clone, a copy, or a restore of the whole tree carries the counter that matches its files. A command that issues IDs reads `last_id` and nothing else, with no walk for the highest ID, and raises it once, before it writes anything else.

- **Restoring the whole tree** to an earlier commit lowers the counter with it, and the IDs issued since can be issued again. Within the tree that is harmless, since the restore took away what held them; only a reference kept outside it can come to name something else, which is why kept text names things by path ([References](#references)).
- **One machine writes a tree**: two machines creating tasks in copies of it issue the same IDs. Kept in git, the two copies change the same line of `@koan.json`, so merging them is a conflict, not a quiet duplicate.
- **An ID above the counter** can only come from an outside change, such as files copied in from another tree. `doctor` finds it, and `repair` raises the counter past it ([Doctor and repair](#doctor-and-repair)).

### References

A **reference** names a task, a folder, or material in a folder, in commands, in notes, and in prompts.

| Written | Names | Holds |
|---|---|---|
| `42` or `@42` | task 42 | until it is deleted |
| `/proj/travel` | a folder, by path | until a move |
| `41` or `@41` | folder 41 | until it is deleted |
| `/proj/travel:docs/plan.md` | material in a folder, by path | until a move |
| `41:docs/plan.md` or `@41:docs/plan.md` | material in folder 41 | while the file stays |

- **The `@` is optional on input**, and koan always writes it, in `where`'s `ref` and in the convention below (JSON results carry IDs as plain integers): `@41` stands out as a reference in prose, where a bare `41` does not.
- **A reference by ID is stable**: IDs never change, so it survives moves and renames. A path is readable. Notes and prompts write both, the reference and its path as a hint: `@41 (/proj/travel)`, `@41:docs/plan.md (/proj/travel:docs/plan.md)`. Reading one, the reference wins; a hint that no longer matches has gone stale. koan never reads notes, so this is a convention, not a format; [`where`](operations.md#where) gives both forms.
- **Text kept outside the tree names things by path**, or a task by its title: commit messages, pull requests, other repositories' documents. An ID means something only in its tree, and after a restore can name something else ([IDs](#ids)).
- **An ID** names a task or a folder, whichever has it, since they share the counter. `blocked_by` and `blocks` take either kind, and a folder by path as well, which is stored as its ID. Where only one kind is taken, the other is `not-found`, with a message saying what the ID is. The root has no ID, and is only `/`.
- **The part after `:`** is a path to material, from the folder: segments separated by `/`, none empty, `.`, or `..`, and the first not starting with `@`, which would name one of koan's own entries. Further down is inside material, where `@` means nothing (`@41:node_modules/@types/x`). So a file has one reference per folder form. The colon can't occur in a folder path, so the first one splits the two; the material part may hold more.
- A material reference says where a file is or would be; the file need not exist, and koan never looks.
- **Finding folder 41** takes a walk that reads every folder file, since a folder's ID is only in its folder file. Two folder files with the ID, or a folder file and a task file, is `duplicate-id`. A folder whose folder file is missing or can't be used has no ID to be found by.

### Dependencies

`blocked_by` orders tasks and folders. A task's `blocked_by` names what the task waits on; a folder's names what everything in the folder waits on.

- **What something waits on**, a task or a folder: its own `blocked_by`, and the `blocked_by` of every folder above it, up to the root. A folder's blockers apply to everything in it at any depth, without being copied into any file.
- **Done.** A task is done when its `completed_at` is set. A folder is **done** when every task and every folder directly in it is done: when every task under it, at any depth, is done. An empty folder is done. A task file that can't be used counts as not done.
- **Done-ness ignores blockers.** A task can be marked done at any time, blocked or not, so a folder can be done while what it waits on is still open. Blockers constrain readiness, not completion.
- **A blocker blocks** while it is an open task, a folder that is not done, or an ID that names nothing or a task whose file is unusable. A folder's ID is only in its folder file, so the ID of a folder whose file is unusable reads as naming nothing, and blocks the same way.
- **Unknown blockers hold back.** A folder whose folder file is missing or can't be used has unknown blockers, so everything in it counts as blocked: where koan can't tell, it holds back.
- An open task is **ready** when nothing it waits on blocks, and **blocked** otherwise. A done task is neither.
- A folder is `done`, or else `blocked` when something it waits on blocks or is unknown, or else `ready`. The root has no blockers, so it is never blocked.

Readiness and done-ness are derived when read, and never stored, so they follow the tree as it changes. A task added to a done folder makes it not done again, and what is open and waits on the folder is blocked again; a done task that waited on it stays done, since `completed_at` is history. Nothing is ever reopened. **Ordering is continuous**: "B after A" holds whenever A has open work, work added after B started included.

An empty folder is done, so what waits on a folder not filled yet is ready. Whoever lays out the work fills the blockers first, or lays out the whole of it in one `batch` ([docs/coordination.md](../docs/coordination.md)).

### Invariants

Every tree that only koan has written satisfies these, and koan refuses any change that would break one:

1. **Acyclic.** Nothing waits on itself, directly or through other tasks and folders, whether they are open or done; and nothing would, whatever tasks were added to any folder. Exactly: the **graph** below has no cycle.

    | Node | Has an edge to |
    |---|---|
    | Each task | each ID in its `blocked_by`, a folder's being that folder's done node; and its folder's wait node, unless its folder is the root |
    | A folder's **wait** node: what its contents wait on | each ID in its `blocked_by`, as for a task; and its parent's wait node, unless its parent is the root |
    | A folder's **done** node: what it waits on to be done | each task directly in it; the done node of each folder directly in it; and its own wait node |

    The nodes are a way of reasoning about the tree, and nothing about them is stored. The root has no nodes: it has no blockers, and can't be named. The edge from a folder's done node to its own wait node stands for a task that might be added to it later: with it, adding a task to a folder never closes a cycle. In words, nothing may be blocked by a folder that contains it, no folder by anything under it, and no chain of blockers may lead from what is in a folder back to the folder.
2. **No dangling references.** Every ID in a `blocked_by`, in a task file or a folder file, names an existing task or folder. Deleting a task removes its ID from every `blocked_by`, and deleting a folder removes its ID and those of the folders and tasks under it.
3. **Unique IDs.** No two task files or folder files have the same ID.
4. **IDs within the counter.** No ID in a task file's or notes file's name, and no folder's ID, is above `last_id`. Only an outside change can break this one; `repair` restores it.

### `@koan.json`

The root holds one metadata file, which marks the directory as a tree, records its format, and holds the ID counter: `last_id`, the highest ID ever issued in the tree ([IDs](#ids)). It is written only under the [lock](#concurrency).

```json
{
  "schema": 4,
  "last_id": 57
}
```

### The state file

```json
{
  "schema": 2,
  "claims": {
    "42": {"claimed_at": "2026-10-10T14:02:11Z", "extra": {"agent": "shingi-3"}}
  }
}
```

`claims` has one entry per [claimed](#claims) task, keyed by ID. It is written only under the [lock](#concurrency). Format 1 held the ID counter too, which `migrate` moves into `@koan.json`.

### Formats

`schema` in `@koan.json` is the **tree format**: the tree's layout, what is named how, and the formats of the files in it. `schema` in a task file or a folder file is that **file's format**. They are numbered apart: format 4 of the tree holds task files in format 1 and folder files in format 2. The tree format goes up whenever the format of any kind of file in the tree does, so `@koan.json` alone says whether a tree is current. A binary reads and writes exactly one tree format, N, and one format of each kind of file; the table is about the tree format.

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
- Format 3 (folder files in format 2, with `blocked_by`) has no conversion from format 2: start a new tree with `init`.
- Format 4 moves the ID counter from the state file into `@koan.json`. `migrate` converts a format-3 tree by writing `@koan.json` with a `last_id` above both the state file's and every ID in the tree, as a format-3 command would have issued; no task file or folder file changes. Until then, `doctor` and `info` read a format-3 tree's counter from the state file. The state file goes to format 2 the next time a claim changes.

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

`init` creates whatever is missing, and changes nothing that is there. A tree cloned onto a new machine brings its counter, and `init` gives it a state file with no claims; a lost state file is replaced the same way, losing only its claims. A `@koan.json` it creates beside an existing tree, as when one was lost, starts `last_id` at the highest ID in the tree; an ID whose task was deleted can then be issued again.

### Walking

There is no index: a command that looks a task up by ID walks the tree.

- A walk enters only folders: directories named `@` and a folder name, in the root or in a folder it entered. Within a folder, an entry named with `@` is a task file `@<id>.json`, notes `@<id>.md`, the folder file `@folder.json` or notes `@folder.md` (not in the root), or a folder. Every other entry is material, and is skipped without a look.
- An entry named with `@` that is none of these, a symlink, or of the wrong type (a directory named `@42.json`) is skipped too, and `doctor` reports it.
- A walk lists entries by name only; folder files are read only by the commands that need them ([implementation.md](implementation.md)).
- A folder that can't be listed fails the command with `io`: it cannot answer without the whole tree.
- **A duplicated ID fails any command that meets it**, with `conflict` (`duplicate-id`): one that looks the ID up and finds two files holding it. A lookup as a task counts task files only; one as a folder or as a blocker counts task files and folder files. koan never picks a copy. `doctor` lists the copies, for a person to resolve.
- **An unusable task file** (unreadable, not valid JSON, breaking the schema, or in another format) fails a command that was asked for that task. A command that only passes it, or finds it as a blocker, reports an `unusable-file` warning and carries on.
- **An unusable or missing folder file** fails a command that was asked for that folder's fields. A command that only reads it, for readiness or an ID, reports an `unusable-file` warning and carries on: the folder has no ID, and its blockers are unknown.
- **A cycle check is the exception.** A cycle through an unusable task file or folder file can't be ruled out, so a check that reaches one fails with that file's error ([Cycles](operations.md#cycles)). A missing folder file holds no blockers to hide one, and fails nothing.
- **A dangling reference** counts as blocking, and is reported as a warning. While a folder file can't be used, a dangling ID may be that folder's, and the warning says so.

## Markers

A **marker** binds a directory outside the tree, usually a git worktree, to a folder, so that [`where`](operations.md#where) and [`context`](operations.md#context) find the folder from anywhere in that directory. It is a `.koan/` directory in it, written by [`marker bind`](operations.md#marker-bind):

```text
<dir>/.koan/.gitignore     *: git ignores the directory, the .gitignore included
<dir>/.koan/marker.json    the folder it is bound to
```

```json
{
  "schema": 1,
  "id": 41,
  "path": "/work/HOME-12345",
  "dir": "/home/paul/repos/foo/.claude/worktrees/HOME-12345"
}
```

Every key is required and no other key is allowed.

| Field | Rule |
|---|---|
| `schema` | The format this file is in. |
| `id` | The folder's ID: what the marker binds. A folder with no ID (the root, or one with no folder file) can't be bound. |
| `path` | The folder's path when it was bound: a hint, as in a [reference](#references). |
| `dir` | The absolute real path of the directory holding `.koan/`. A marker found in any other directory was copied there, or moved with its directory (`git worktree move`), and binds nothing. Directories are told apart by identity, not by spelling: on a filesystem that ignores case, `~/Code/wt` and `~/code/wt` are one. |

- **The nearest marker is the one.** A path outside the tree is in the folder of the nearest marker at or above it, as a `.git` or an `.editorconfig` works, so a worktree's subdirectories are its folder's too. The look stops at the first `.koan/marker.json`, whatever its state: one that can't be used is never passed over for one further up, which may be another folder's. A `.koan/` with no `marker.json`, left by a `marker bind` cut short, is not a marker, and is passed over, as is a `.koan` that is not a directory, a symlink to one included.
- **Inside the tree, markers are never looked at.** Where a path is in the tree already says which folder it is in, and a `.koan/` there is material.
- **The ID decides; the path is a hint.** A marker binds the folder with its ID, wherever the folder is now, so it survives moves and renames. A `path` that no longer matches is a `stale-marker` warning, and binding again refreshes it. A marker whose folder is gone binds nothing, and `marker bind` replaces it as freely as it does no marker.
- **Reads never write a marker.** Only `marker bind` and `marker unbind` do, and nothing keeps a list of them: koan can't find a folder's markers, so `folder delete` leaves them, and they say the folder is gone when next read.
- **Out of git's way.** git reads a `.gitignore` in every directory, and `*` matches everything in `.koan/`, its `.gitignore` included: `git status` never shows it, `git add -A` never stages it, and `git worktree remove` needs no `--force`, since ignored files leave a worktree clean. Removing a worktree removes its marker. The one exception is a first `marker bind` killed between creating the `.gitignore` and writing its two bytes: the empty `.gitignore` hides nothing, so git shows `.koan/` until the next `marker bind` or `marker unbind`. koan runs no git, and reads one thing of git's (below).
- **Bind worktrees, not checkouts.** A marker binds every directory under it that has no nearer one, so one in a checkout would hand its folder to every worktree inside it (`.claude/worktrees/`) that was never bound. `marker bind` refuses a directory whose `.git` is a directory, a checkout, unless forced. A worktree's `.git` is a file, and a directory with no `.git` binds like any other.
- **`.koan/` is koan's, and only its own files.** It holds `marker.json`, a `.gitignore` of `*` (or empty, as a first bind cut short leaves it), and temp files a write left, each a regular file. Anything else in it, a `.gitignore` with other content included, is material: `marker bind` and `marker unbind` refuse to touch a `.koan/` that holds any, so koan never changes or removes what it didn't write.
- **Written whole.** `.gitignore` comes first, so nothing in `.koan/` is ever there without it. A new `.gitignore` is written in place, since a temp file beside it would have nothing to hide it; `marker.json`, and a `.gitignore` being replaced, go through a temp file renamed into place, as the tree's files do. A reader sees the old marker or the new one, never part of one.

## Concurrency

Several processes may use one tree at once, with no coordinator between them.

- **One lock**: a `flock` on the root directory itself. There is no lock file, and the lock is gone when its holder exits, however it exits.
- **Writes are serialized.** A write holds the lock alone, from before it reads anything to after its last change.
- **A read sees the tree between writes**, never part of one. Reads share the lock: any number run at once, a read waits for a write to finish, and a write for the reads before it. `info`, `version`, and `marker unbind` take no lock.
- **Input is checked before the lock is taken.** A command that finds the lock held against it waits up to 5 seconds, then fails with `busy`, which is always safe to retry.
- **A write decides on current state**: everything it depends on is read after it has the lock. So two writes to one task both take effect, and no write breaks an [invariant](#invariants).
- **Files are replaced atomically**: written to a hidden temp file, flushed, then renamed. A reader never sees half a file koan wrote.
- `doctor` holds the lock alone, as a write does, though it changes nothing: on a large tree it is the command others are most likely to wait for.

### Crashes and failures

A command that fails after changing something says what it changed. A command that is killed, or a machine that crashes, can leave a multi-file change half done: an ID used with no task or folder, a `@<id>.md` without its task file, a reference to a task or folder that is gone or was never made, a temp file, or a folder without its folder file (a folder is made first, then its files are written in it). None of these stops later commands, and `doctor` finds all of them. A folder partly removed by a `folder delete` cut short shows up as some of these, in what is left of it: run `folder delete` again to finish. A `folder delete` that fails partway, rather than being killed, cleans up after itself: the tasks and folders it did remove leave every `blocked_by`, in task files and folder files, and the tasks' claims end.

koan keeps no journal and takes no idempotency keys. After an unclear outcome, look (`task show`, `task list`, `doctor`), then decide. The task commands that set a state (`done`, `reopen`, `block`, `unblock`, `update`, `release`) are safe to run again; `task create` and `batch` make new tasks each time, and `task claim-next` a new claim.

## Claims

A **claim** reserves a task: once it is claimed, no other `task claim` of it succeeds until the claim ends. It is how two agents taking work from one tree are not handed the same task, which reading the list of ready tasks cannot give.

- `task claim` takes an open task that has no claim, and fails at once (`conflict`, `claimed`) on one that has. `task claim-next` claims the first ready, unclaimed task in scope.
- A claim is an entry in the state file. It outlasts the process that made it and never expires.
- A claim ends with `task release`, or when its task is marked done or deleted. A reopened task starts unclaimed.
- **A claim refuses other claims and takes the task off the frontier, and nothing else.** Every command that changes a task treats a claimed one like any other, and `task release` works whoever made the claim.
- **A claim does not lock the notes.** Nothing stops two editors from overwriting each other's changes to a `@<id>.md`. The convention is that whoever holds the claim is the one who edits the task's notes.
- A claim carries a `claimed_at` and an `extra` object, which koan returns and never interprets. Who holds a claim, and whether they are still alive, is the caller's business.
- Claims are this machine's: they do not travel with the tree.

`task done` and `task delete` change the task first and the state file second. An entry left behind by a crash between the two names a task that is done or gone; commands ignore such an entry, and `repair` removes it (an entry for a task that is gone, when asked for it by name).

## Doctor and repair

`doctor` walks the whole tree and reports what it finds, each finding with a kind, the paths and IDs involved, and a sentence saying what to do. `repair` fixes the kinds that can be fixed without losing anything or choosing between versions, and leaves the rest to a person. A reference or a claim to a task that seems gone it removes only when asked for its kind by name: the task may only be out of koan's sight, in a folder moved by hand, and removing its ID would lose the link for good, where moving the folder back would restore it.

| Finding | `repair` |
|---|---|
| `temp-leftover`: a temp file koan left behind | Removes it. |
| `id-above-counter`: an ID above `last_id`, in a task file's or notes file's name or in a folder file, as only an outside change leaves | Raises `last_id`. |
| `dangling-reference`: a `blocked_by` ID, in a task file or a folder file, with no task or folder | Removes the ID, when asked for by name. Not while any folder file can't be used: the ID may be that folder's, and removing it would lose the link once the file is fixed. |
| `stale-claim`: a claim on a task that is done or gone | Removes the claim; on a task that is gone, when asked for by name. |
| `orphan-notes`: a `@<id>.md` with no task file beside it | Removes it when empty; moves it to its task when the task is elsewhere without notes. Otherwise leaves it. |
| `missing-folder-file`: a folder with no `@folder.json` | Writes one, with a new ID and an empty `blocked_by`. A `blocked_by` that named the folder's old ID then dangles, and the finding's message says so: re-point such references with `task block` or `folder block` before repairing `dangling-reference`. |
| `duplicate-id`: several task files or folder files with one ID | — |
| `cycle`: tasks and folders that wait on each other, or would once a task were added to a folder (see [Invariants](#invariants)) | — |
| `unusable-file`: a task file or folder file that can't be used, with the reason, one in another format included | — |
| `old-format`: a task file in an earlier format | — (`migrate` converts it) |
| `skipped-entry`: an entry named with `@` that is not one of koan's, a symlink or wrong-typed entry so named (a folder file is `unusable-file` instead), or a folder that can't be listed | — |

## Errors

Every failure has one **kind**:

| Kind | Meaning |
|---|---|
| `usage` | The command line could not be turned into an input (see [cli.md](cli.md#command-line)). |
| `invalid-input` | The input breaks a rule in this document or the command's schema. |
| `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The root is not usable (see [Root states](#root-states)); or, for `corrupt` and `unsupported-format`, a task file or folder file the command needs is not. |
| `busy` | The lock was held against the command for 5 seconds. |
| `not-found` | A task or folder named in the input does not exist. |
| `conflict` | The input is valid but the tree's state refuses it; a `rule` says which: `duplicate-id`, `case-mismatch`, `claimed`, `cycle`, and the commands' own. |
| `io` | The operating system refused something. |
| `internal` | A bug in koan. |

A command reports one error. Input is checked first, then the root's state, then `busy`; among errors after that, which one is reported is not specified.

A problem that does not stop the command is a **warning**, returned beside the result: `unusable-file`, `dangling-reference`, and `stale-marker` (see [Markers](#markers)).
