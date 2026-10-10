# koan design spec

## Goals

Task management on one machine, with the filesystem as the database — no daemon, no index, no server.

Operations on this data model are specified in [operations.md](operations.md).

## Assumptions

- **koan is the only writer under the root.** Every file and folder under the root is created, changed, and removed by koan. The [invariants](#invariants) in this spec are guaranteed only for trees koan alone has written. Any change made another way is an **outside change**, and is outside the contract.
- **Notes are the exception.** A task's `.md` may be edited directly with any editor. Notes carry no invariants, so an outside change to them cannot violate any. It can leave stray entries, though: editor side files (e.g. `42.md~`), which koan ignores, and an orphaned `.md` if a task is moved or removed while its notes are open in an editor. Both are handled as [Walking the tree](#walking-the-tree) describes. If an editor save and a koan write to the same `.md` overlap, one of them may be lost.
- **One user.** koan serves a single OS user. That user's config names exactly one root. Every process running as the user and reaching the root — shells, agents, editors — shares one [write lock](#write-lock). A process whose environment points to a different config location (`HOME`, `XDG_CONFIG_HOME`) sees that config, or none, and gets [`not-initialized`](operations.md#error-kinds); one that gives no config location at all gets [`environment`](operations.md#error-kinds) (see [Config file](#config-file)). Sharing one root between OS users is not supported.
- **The root is on a local filesystem.** Network mounts (SMB, NFS, and the like) are not supported: their locking cannot be relied on, so the [write lock](#write-lock) may not exclude other writers. A synced folder is fine — its files are local, and a separate process syncs them.
- **Syncing and committing are allowed.** The root may be a git repository or a synced folder, since those tools carry files koan wrote. Every file koan keeps in the root is content, safe to commit, sync, and restore: the one piece of state that must never go backwards, the ID counter, is kept outside the tree (see [State file](#state-file)). Git is also the only undo for a delete (see [Undo](operations.md#undo)). Anything they leave inconsistent — a merge that introduces a cycle, a missing blocker, a conflicting file — is an outside change; finding it is the job of [`doctor`](operations.md#doctor), and repairing it, where that is safe, of [`repair`](operations.md#repair) — not of normal operation (see [Diagnosis and repair](#diagnosis-and-repair)).
- **Hidden entries are ignored.** Any entry under the root whose name starts with `.` (e.g. `.git`, `.DS_Store`) is ignored by read and write operations. Only `doctor` and `repair` look at them, to find koan's own leftover temp files; they too ignore every other hidden entry.

## Supported platforms

Only Linux and macOS, on amd64 and arm64, are supported. Windows is never supported.

## Terms

Terms used with one meaning throughout this spec and [operations.md](operations.md):

| Term | Meaning |
|---|---|
| **root** | The directory holding a tree; named by the config. "Root directory" only where the OS object matters. |
| **tree** | Everything under a root: folders, tasks, and `koan.json`. |
| **task file** | A task's `.json`. |
| **notes** | A task's prose; stored in its `.md` (the notes file). |
| **`koan.json`** | The root's metadata file (see [Root metadata](#root-metadata)). |
| **state file** | This machine's ID counter for the root, kept beside the config and never in the tree (see [State file](#state-file)). |
| **config** | The file `config.toml` naming the root (see [Configuration](#configuration)). |
| **write lock** | The lock that serializes write operations on a root (see [Write lock](#write-lock)). |
| **invariant** | A rule spanning several files (see [Invariants](#invariants)). |
| **file-level rule** | A rule checkable on one file alone (see [File validity](#file-validity)). |
| **unusable** | A file that is *unreadable*, *corrupt*, or in an *unsupported format* (see [File validity](#file-validity)). |
| **older format** | A format version below the one a binary supports, from 1 up (see [Format versions](#format-versions)). |
| **migration step**, **latest step** | A numbered conversion of file formats, and the highest one a binary knows (see [Migrations](#migrations)). |
| **process crash**, **system crash** | The two kinds of interruption (see [Crashes](#crashes)). |
| **outside change** | Any change under the root not made by koan (see [Assumptions](#assumptions)). |
| **open / done** | A task's state, determined solely by `completed_at` (see [Fields](#fields)). "Done" covers every way a task can end, including cancelled. |
| **read / write / setup / diagnostic / migration** | The kinds of [operation](operations.md#operation-kinds). "Read a file" means file I/O, not a read operation. |

## Data model

How tasks are stored in the filesystem.

### Folders

Folders are purely organizational: no stored fields, no identity, and never a dependency target. They are infinitely nestable, and any folder can contain any number of tasks.

An empty folder is valid, but does not survive git: git tracks files, not directories, so a clone recreates only folders that contain something. This is accepted; koan writes no placeholder files.

There is always a root, set by [`init`](operations.md#init), and it can never be deleted. In [folder paths](#folder-paths) the root is `/` — not to be confused with the machine's `/` directory.

koan writes nothing into the root but folders, tasks, and [`koan.json`](#root-metadata) — apart from transient hidden temp files, and the hidden temp folder of a [`delete-folder`](operations.md#delete-folder), during a write. No lock file, no index, no database: the write lock is the root directory itself (see [Write lock](#write-lock)). That is what lets the tree be committed, synced, or moved without carrying a machine's coordination state with it, and what makes a task file mean the same thing on any machine that reads it. Anything else found under the root — hidden entries, editor side files — is ignored (see [Walking the tree](#walking-the-tree)).

### Tasks

A task is two files sharing a stem: its task file (`.json`), holding the task's fields, and its notes file (`.md`). koan always creates both, with an empty `.md` when there are no notes.

The task file is **authoritative**: a task exists exactly when its task file does, and for existence the task's ID is the one in its filename — even if the file itself is unusable. The `.md` holds nothing but prose. A missing `.md` is allowed and reads as empty notes, so a crash between writing the two files leaves a valid task. Outside an interrupted write, koan never leaves a `.md` without its task file. A `.md` without one — left by an editor, or by a [`move`](operations.md#move) or [`delete`](operations.md#delete) interrupted by an error or a crash — is not a task: reads ignore it, and [`doctor`](operations.md#doctor) reports it as [`orphan-notes`](operations.md#finding-kinds).

#### Task file schema

The normative JSON Schema for a task file. A file that does not validate against it is `corrupt` (see [File validity](#file-validity)).

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task-file",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra"],
  "properties": {
    "schema": { "const": 1 },
    "id": { "type": "integer", "minimum": 1, "maximum": 999999999999999 },
    "title": { "type": "string", "minLength": 1, "maxLength": 200, "pattern": "^[^\\u0000-\\u001F\\u007F-\\u009F\\u2028\\u2029\\u0020\\u00A0\\u1680\\u2000-\\u200A\\u202F\\u205F\\u3000](?:[^\\u0000-\\u001F\\u007F-\\u009F\\u2028\\u2029]*[^\\u0000-\\u001F\\u007F-\\u009F\\u2028\\u2029\\u0020\\u00A0\\u1680\\u2000-\\u200A\\u202F\\u205F\\u3000])?$" },
    "priority": { "type": ["integer", "null"], "minimum": -9007199254740991, "maximum": 9007199254740991 },
    "created_at": { "$ref": "#/$defs/timestamp" },
    "completed_at": { "anyOf": [{ "$ref": "#/$defs/timestamp" }, { "type": "null" }] },
    "updated_at": { "$ref": "#/$defs/timestamp" },
    "blocked_by": { "type": "array", "items": { "$ref": "#/properties/id" }, "uniqueItems": true },
    "tags": { "type": "array", "items": { "$ref": "#/$defs/name" }, "uniqueItems": true },
    "extra": { "type": "object" }
  },
  "additionalProperties": false,
  "$defs": {
    "timestamp": { "type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$" },
    "name": { "type": "string", "pattern": "^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$" }
  }
}
```

Every key is required and no other top-level key is allowed. `extra` is the place for anything else: it accepts any keys and any JSON values, and its contents are never validated.

#### Fields

- **`schema`** — Version of the task file format (see [Format versions](#format-versions)).
- **`id`** — The task's identity (see [Task IDs](#task-ids)). Never changes after creation.
- **`title`** — Short human-readable summary of the task (see [Titles](#titles)).
- **`priority`** — Optional integer ranking the task's importance; `null` when unset, and koan never assigns a default. Any integer from −(2^53 − 1) to 2^53 − 1 is allowed — the range every JSON reader holds exactly — so users can choose their own granularity. Higher means more important. Tasks with no priority rank after all prioritized tasks.
- **`created_at`** — [Timestamp](#timestamps) of when the task was created. Never changes after creation.
- **`completed_at`** — [Timestamp](#timestamps) of when the task was completed, or `null`. This is the **sole** source of truth for a task's state:
  - **Open:** `completed_at` is `null`.
  - **Done:** `completed_at` is not `null`. Done covers every way a task can end — finished, implemented, cancelled, abandoned — and koan draws no distinction between them. The user can record the distinction in `extra` (e.g. a `status` key).
- **`updated_at`** — [Timestamp](#timestamps) of when koan last changed the task file's content: set to `created_at` at creation, then to the current time by every write that changes a field — including removing a deleted task from a dependent's `blocked_by`. A write that changes nothing does not rewrite the file, so leaves it alone. [`move`](operations.md#move) does not set it: the folder is not stored in the task file. Nor does [`migrate`](operations.md#migrate): a migration changes the file's format, not the task's fields (see [Migrations](#migrations)). Edits to the notes are not tracked: koan never sees them.
- **`blocked_by`** — Set of IDs of the tasks that must be done before this one is ready (see [Dependencies](#dependencies)).
- **`tags`** — Set of labels for grouping and filtering tasks across folders (see [Tags](#tags)).
- **`extra`** — Open map of user-defined data: string keys, any JSON values. Untyped and **never interpreted by koan** — koan stores it and returns it, and managing its keys and value types consistently is up to the user or agent. Entirely optional; a task that doesn't use it has an empty map. Common uses are a workflow `status` or project-specific fields.

`blocked_by` and `tags` are **sets**: stored as JSON arrays, but order carries no meaning and duplicates are not allowed.

> **Completion is deliberately reason-blind.** Because only `completed_at` determines state, completing a task unblocks everything that depends on it, whether it was finished or cancelled. "Done" means *no longer open*, whatever the reason. If a cancelled task leaves its dependents without a premise, re-planning them is the user's job. This is intentional: the user has to handle cancellation either way, and koan doesn't impose its own vocabulary of outcomes on top.

> **Why is `status` an `extra` key and not a tag?** Both are free-form user vocabulary that koan never interprets. The difference is structural: a task has **exactly one** workflow state but **any number** of tags. A tag set can't express "exactly one of" — nothing stops a task from being tagged both `waiting` and `in-progress`. A single key in `extra` holds one value at a time.

#### Example

Files:

```text
proj/42.json    task file
proj/42.md      notes (may be empty)
```

Task file:

```json
{
  "schema": 1,
  "id": 42,
  "title": "Book flights",
  "priority": 2,
  "created_at": "2026-09-20T18:31:51Z",
  "completed_at": null,
  "updated_at": "2026-09-20T18:31:51Z",
  "blocked_by": [],
  "tags": [
    "travel"
  ],
  "extra": {
    "status": "waiting on quote"
  }
}
```

### Naming and validation

Folder names, task filenames, and tags are restricted to ASCII. macOS (APFS) is case-insensitive by default and stores filenames in decomposed Unicode, while Linux does neither; restricting names to ASCII, and refusing folder names that differ only in case, keeps a tree meaning the same thing on both.

#### Folder names

Each path segment must match:

```text
^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$
```

- 1–64 characters: letters (upper or lower case), digits, and hyphens.
- Must not start or end with a hyphen. This also excludes `.`, `..`, and hidden names.
- Case is kept, and a path matches a folder only with the exact case: `/Proj` names `Proj`, never `proj`.
- Sibling folders must differ by more than case: koan refuses to create or move a folder next to one whose name differs from it only in case (`conflict`, `rule`: `case-clash`; see [Path walk](operations.md#path-walk)), since on a case-insensitive filesystem the two would be one folder. A tree that has such siblings anyway, made outside koan, is a [`case-clash`](operations.md#finding-kinds) finding.

#### Folder paths

Folders are addressed by their path from the root, written with `/` separators and a leading `/`:

- `/` is the root.
- `/proj/travel` is folder `travel` inside folder `proj`.
- Every segment follows the [folder name](#folder-names) rules. No trailing `/` (except the root itself), no empty segments, no `.` or `..`.

#### Task IDs

```text
^[1-9][0-9]{0,14}$
```

- Positive integer, no leading zeros, at most 15 digits. The **ID ceiling**, 999,999,999,999,999, is below 2^53, so every ID is exact in JSON readers that store numbers as doubles (JavaScript, `jq`). Issuing an ID past it fails with [`conflict`](operations.md#error-kinds) (`rule`: `id-exhausted`).
- Unique across the whole tree, not per folder, since `blocked_by` references tasks by ID alone (see [Invariants](#invariants)).
- Assigned from a monotonically increasing sequence, recorded as `last_id` in this machine's [state file](#state-file). It is kept out of the tree on purpose: restoring, reverting, or merging the tree's files can then never lower it. The sequence may have gaps: an ID can be consumed without a task being created (e.g. by a process crash mid-create).
- **Never reused**, even after its task is deleted — short of an outside change (e.g. a state file restored from a backup, or rebuilt after its highest tasks were deleted) or a system crash on a disk that ignores flushes, either of which can cause reuse (see `create` › Crash behavior in [`create`](operations.md#create)). [`doctor`](operations.md#doctor) detects it, as `id-above-last-id` before reuse and `duplicate-id` after.
- **One machine issues IDs.** The counter is this machine's, so a tree is written from one machine. A copy of the tree attached on a second machine (see [`init`](operations.md#init)) starts its own counter at the highest ID it finds, and tasks created on both would get the same IDs: merging them gives `duplicate-id`. Sharing one tree between machines that both create tasks is not supported. The same holds on one machine for two config directories naming one root: they share its lock, but not its counter. One root is served by one config.

#### Task filenames

A task's two files are named by its ID; this one pattern covers both:

```text
^(?<id>[1-9][0-9]{0,14})\.(?<ext>json|md)$
```

The `id` in the filename always equals the `id` in the task file.

#### Tags

Each tag follows the same rules as a [folder name](#folder-names), except that tags are lowercase-only, so matching is exact:

```text
^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$
```

#### Timestamps

UTC, whole seconds, always with a `Z` suffix: `YYYY-MM-DDTHH:MM:SSZ`, e.g. `2026-09-20T18:31:51Z`. No offsets, no fractional seconds. The value must also be a real calendar date and time (no month 13, no hour 24); a leap second (`:60`) is not allowed. The pattern below checks only the shape.

```text
^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$
```

#### Titles

A title given as input is processed in this order:

1. **Trim** leading and trailing whitespace, where whitespace is the Unicode `White_Space` property (so a pasted non-breaking space is trimmed too).
2. **Validate** the trimmed result:
   - 1–200 Unicode code points — the unit JSON Schema's `maxLength` counts.
   - No control characters (Unicode category `Cc`: U+0000–U+001F and U+007F–U+009F) and no line or paragraph separators (U+2028, U+2029). Together: no line breaks of any kind.
   - Everything else is allowed, including invisible format characters such as the zero-width joiner that composes emoji. Titles never become filenames.
3. **Store** the trimmed title exactly — no Unicode normalization, no collapsing of inner whitespace.

A stored title therefore never has leading or trailing whitespace. On disk this is enforced by the [task file schema](#task-file-schema); the pattern, with the whitespace set spelled out rather than `\s` (whose definition differs between regex engines):

```text
^[^\u0000-\u001F\u007F-\u009F      -   　](?:[^\u0000-\u001F\u007F-\u009F  ]*[^\u0000-\u001F\u007F-\u009F      -   　])?$
```

### Dependencies

`blocked_by` links tasks into a dependency graph: an edge from a task to each task in its `blocked_by`. For the *Acyclic* invariant and the cycle check, the graph is keyed by ID: if an ID has several task files, it is still one node, and its edges are the union of every copy's `blocked_by`. The graph is subject to the *Acyclic* and *No dangling references* [invariants](#invariants). Folders play no part in dependencies: a task may be blocked by a task in any folder.

Readiness is derived **per task file**, from that file's own `blocked_by` and `completed_at` — so two copies of a duplicated ID may differ in readiness:

- **Ready:** the task is open and every task in its `blocked_by` is done (or `blocked_by` is empty).
- **Blocked:** the task is open and at least one entry in its `blocked_by` is open, names no existing task, names a task whose task file is unusable, or names an ID that more than one task file has (see [Walking the tree](#walking-the-tree)).
- A done task is neither ready nor blocked. Every open task is exactly one of ready or blocked.

A `blocked_by` ID that names no existing task can only arise from a system crash or an outside change. It counts as blocking, so the task stays visible as blocked until someone repairs it, and reads report it as a [`dangling-reference`](operations.md#warning-kinds) warning. A blocker whose task file exists but is unusable also counts as blocking, since its state is unknown; it is reported only as `unusable-file`, not as a dangling reference.

Blockers constrain readiness, not completion: a task can be completed at any time, whatever the state of its blockers — e.g. cancelling a task that is still waiting on others.

### Invariants

Rules spanning several files. Every tree koan alone has written satisfies all four, and any change that would violate one is rejected:

- ***Acyclic.*** The dependency graph is a DAG: a task never blocks itself, directly or through a chain of other tasks. This holds for every task, open or done — a cycle among done tasks still violates it, since reopening any of them would expose it.
- ***No dangling references.*** Every ID in a `blocked_by` names a task that exists. Removing a task removes its ID from every `blocked_by` that contains it.
- ***Unique IDs.*** No two task files have the same ID.
- ***IDs within `last_id`.*** Every task's `id` is at most the `last_id` of this machine's [state file](#state-file).

A system crash or an outside change can leave a tree violating an invariant; see [File validity](#file-validity) for how such trees are read, and [Diagnosis and repair](#diagnosis-and-repair) for how it is found and repaired.

### Root metadata

The root holds one metadata file, `koan.json`. It marks the directory as a koan tree, and records the format the tree's files are in. Its normative JSON Schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "root-file",
  "type": "object",
  "required": ["schema", "migration"],
  "properties": {
    "schema": { "const": 2 },
    "migration": { "type": "integer", "minimum": 0, "maximum": 9007199254740991 }
  },
  "additionalProperties": false
}
```

- **`schema`** — Version of the `koan.json` format (see [Format versions](#format-versions)).
- **`migration`** — The last [migration step](#migrations) applied to the tree; `0` when none has been. Set by [`init`](operations.md#init) and [`migrate`](operations.md#migrate), and by [`repair`](operations.md#repair) when it rebuilds a lost `koan.json` (see *Starting points* in [Migrations](#migrations)). Every other write keeps it. A `koan.json` at schema 1, which predates migrations, has no `migration` and counts as `0`; it holds `last_id` instead, which now lives in the [state file](#state-file).

`koan.json` lives only at the root and is reserved there. It must be a regular file, not a symlink or a directory; anything else in its place makes it `corrupt`. Its name matches neither the folder-name nor the task-filename rules, so it can never be mistaken for either. It is content, not coordination state: it belongs to the tree and moves, syncs, and commits with it. Nothing in it is a counter: restoring an older `koan.json` together with the files beside it, as undoing a migration with git does, leaves a consistent tree.

### Format versions

Data formats are versioned by the `schema` field in each task file and in `koan.json`, independently of koan releases.

- **Exact match.** A binary supports exactly one format version for task files and one for `koan.json`, as reported by [`version`](operations.md#version). Every operation but [`migrate`](operations.md#migrate) reads and writes only those: a file whose `schema` differs is `unsupported-format` — whether older or newer. `migrate` alone also reads the older formats, to convert them (see [Migrations](#migrations)).
- **Older formats.** An **older format** is a version from 1 up to one below the supported one: a format some koan wrote, which `migrate` converts. Any other version that differs — a newer one, or one below 1, which never was a format — is one this binary doesn't know, and nothing converts it.
- **Writes preserve format.** A write never changes a file's `schema`. Formats change only through `migrate`.
- **Every format change is a migration.** Adding a field to a task file or `koan.json`, removing one, or changing what one may hold bumps that file's `schema` and ships a [migration step](#migrations), before 1.0 as after. A binary on either side of the change then sees the other's files as in another format, never as `corrupt`, and a tree is brought forward by running `migrate`, never fixed by hand. Adding the required `updated_at` field, before migrations existed, was the last change made in place. (The CLI's JSON output, `schemas/`, is not a data format: until 1.0 it may still change in a minor release, as [Versioning](operations.md#versioning) says.)
- **Whole tree.** A root is usable only when its `koan.json` has the supported version and records every migration step this binary knows (see [Root states](operations.md#root-states)). A task file with a different version is skipped by reads with an [`unusable-file`](operations.md#warning-kinds) warning, and is an `unsupported-format` error for any write that needs it; [`doctor`](operations.md#doctor) reports one in an older format as [`old-format`](operations.md#finding-kinds), for `migrate` to convert.

### Migrations

A format change ships with a **migration step**, which [`migrate`](operations.md#migrate) applies to bring an existing tree to the new format. Steps are numbered 1, 2, 3, … in the order they were released, and a binary knows every step up to its **latest step**, which [`version`](operations.md#version) reports. `koan.json`'s `migration` records the last step applied to the tree.

| Step | Name | Converts |
|---|---|---|
| 1 | `tree-marker` | `koan.json` 1 → 2: removes `last_id`, and adds `migration`, as `0`; `migrate` then records the latest step, as it does after every run. `migrate` itself carries the `last_id` it finds there into the [state file](#state-file) first. Task files are unchanged. |

- **A step converts each file on its own.** A step takes one or more kinds of file from one `schema` to the next, and what it writes for a file depends on that file alone. (What step 1 removes from `koan.json` is kept by `migrate`, not by the step: see [`migrate`](operations.md#migrate).) So files convert in any order, and a tree converted in pieces — most of it now, a few files a merge brings in later — ends the same as one converted at once.
- **A file's own `schema` decides what is done to it.** `migrate` applies to each file the steps from its `schema` on, in order, and writes the result once. A file already in this binary's format is left as it is, byte for byte, so no step is ever applied twice.
- **The step recorded says whether the tree is current; the files say what to convert.** Every operation reads `koan.json`, so `migration` tells it, without walking the tree, when a step is pending (the root is not usable, and operations fail with [`migration-pending`](operations.md#error-kinds)) or when a newer binary wrote the tree ([`unsupported-format`](operations.md#error-kinds)). It never tells `migrate` what to skip: `migrate` always walks the whole tree, since a merge can bring files in an older format into a tree whose recorded step is already current.
- **At rest, in one run.** `migrate` holds the write lock for its whole run, as [`doctor`](operations.md#doctor) and [`repair`](operations.md#repair) do, so no write ever sees a tree half migrated. It writes the state file first when it has a `last_id` to move there, then every task file, and `koan.json` last, so the step recorded moves only once every file it converts is written, and a run cut short leaves the step pending. On a large tree the run takes as long as one flushed write per task; writes that come meanwhile wait, then fail with `busy`, which is safe to retry. Run it while agents are idle.
- **One file never holds the tree back.** A task file `migrate` can't read or convert, or a folder it can't list, is reported and left as it is; the rest is converted, and the latest step recorded. Refusing instead would leave the whole tree unusable for one stray file. What was left is not lost track of: `doctor` reports a task file in an older format as `old-format` whatever the counter says, and the next `migrate` converts it.
- **Steps keep koan's file format.** A step's result is written as every koan write is ([File format](#file-format)): keys inside `extra` keep their order and its numbers are written back character for character, since a step never decodes and re-encodes `extra`.
- **A migration is not an edit.** It changes no field a task had: `updated_at` keeps its value, as does every other field, unless the step's entry above says otherwise. A tree in git shows a migration as one commit that changes formats and nothing else.
- **Released steps never change.** A step, once released, is never edited or removed, so a newer binary can always bring a tree at any step, `0` included, up to date.
- **Forward only.** There are no steps back. A binary that finds `koan.json` in a newer format, or a `migration` past its latest step, refuses the tree with `unsupported-format` and converts nothing. Undoing a migration is git's job: commit the whole tree before running `migrate`, and restoring that commit undoes it — `koan.json` and the step it records included, since both are in the tree. The state file is not, and needs no undoing: a migration never lowers `last_id`.
- **Starting points.** A tree [`init`](operations.md#init) creates records the latest step, since it has nothing to convert. What a lost `koan.json` recorded can't be known, so one that [`repair`](operations.md#repair) rebuilds records what `repair` found: the latest step when no task file is in an older format, and `0` when one is, so that the root needs migration until `migrate` has converted them.
- **Several machines.** A tree copied to another machine, or restored from one, is migrated on one of them, and `koan.json` carries the step with it. Each of the others needs a binary whose latest step is at least the tree's: until it has one, the tree is refused with `unsupported-format`, and nothing is written. A machine that hasn't pulled yet goes on writing the older format in its own copy; when its commits are merged, the files they bring stay in the older format until the next `migrate`, and `doctor` reports them as `old-format`. A merge that lowers `migration` in `koan.json` makes a step pending again, and `migrate` passes over the files already current.
- **Migrating is not repairing.** `repair` never converts a file's format, and `migrate` never repairs damage. Each has its own command, so an agent's permission rules can ask before either, and `doctor` points to `migrate` for what only it fixes (see [Diagnosis and repair](#diagnosis-and-repair)).

### File validity

Rules are checked at two levels, and they fail differently:

| Level | Checked against | Rules | When broken |
|---|---|---|---|
| **File** | The one file alone | Valid JSON (per step 1 below) with no duplicate keys; integer fields written as integer literals (`42`, never `42.0` or `4.2e1`); the file's JSON Schema; the [naming and validation](#naming-and-validation) rules the schema can't express (e.g. timestamps are real calendar date-times); the task's own ID not in its `blocked_by`; the filename ID equals the `id` field | The file is **`corrupt`**. Reads skip it with an [`unusable-file`](operations.md#warning-kinds) warning; a write that needs it fails with `corrupt`. |
| **Tree** | Several files together | The [invariants](#invariants) | The files stay usable. Reads report or absorb the violation (e.g. a dangling blocker counts as blocking); [`doctor`](operations.md#doctor) finds it (see [Diagnosis and repair](#diagnosis-and-repair)). |

Every versioned file (`koan.json`, the [state file](#state-file), a task file) is checked in three steps, stopping at the first failure:

1. **Parseable and versioned.** The file is valid JSON (with no `\u` escape of an unpaired UTF-16 surrogate, e.g. `\ud800` alone, and no more than 9,990 levels of nested objects and arrays), is a JSON object, and has a `schema` written as an integer literal (`2.0` does not count) from −(2^53 − 1) to 2^53 − 1, the range every JSON reader holds exactly. Otherwise: `corrupt`.
2. **Supported version.** `schema` is the version this binary supports. Otherwise: `unsupported-format` — and nothing further is checked, since the rest of the file follows a format this binary doesn't read (see [Format versions](#format-versions)). What the other version is decides only what to do about it: a file in an older format is converted by [`migrate`](operations.md#migrate), which checks it against its own version's rules first; any other needs a newer binary. `koan.json` in an older format is the one exception to *nothing further*: a binary carries the rules of every older `koan.json` format with its [migration steps](#migrations), and checks the file against them wherever it reads it. One that breaks them is `corrupt`; one that passes makes the root *need migration* (see [Root states](operations.md#root-states)).
3. **Valid.** The file passes every file-level rule above. Otherwise: `corrupt`.

A file that can't be opened or read at all is **unreadable**. A file is **unusable** when it is unreadable, `corrupt`, or `unsupported-format`.

A `corrupt` error says what is wrong: why the file failed step 1 if it isn't JSON, or else each rule it breaks, by where in the file (see [Error kinds](operations.md#error-kinds)). The `unusable-file` warning does not.

### File format

Every JSON file koan writes is written the same way, so that a tree kept in git shows only real changes in its diffs:

- Keys in the order the schema lists them. Keys inside `extra` in the order they were given; when `extra` is changed, existing keys keep their position and new keys are appended in the order given.
- Two-space indentation, one key per line.
- Every non-empty object and array is written multi-line, one member or item per line; empty ones are written `{}` and `[]`. `blocked_by` and `tags`, being sets, are written sorted: `blocked_by` in ascending numeric order, `tags` by name.
- Minimal string escaping: only `"`, `\`, U+0000–U+001F, and the line and paragraph separators U+2028 and U+2029 are escaped; everything else — including `/`, `<`, `>`, `&`, U+007F–U+009F, and all other non-ASCII — is written as raw UTF-8.
- Numbers inside `extra` are written back exactly as they were given, character for character (`1.10` stays `1.10`; `12345678901234567890` is not rounded).
- UTF-8, no byte-order mark, a single trailing newline.

This governs only what koan writes. Reading accepts any valid JSON, whatever its layout — though not a byte-order mark, which makes a file `corrupt` ([File validity](#file-validity)).

## Concurrency

Several processes may work one root at the same time — agents, a user's shell, an editor open on a task's notes — with no coordinator between them. Concurrent writes from other machines (via sync) are out of scope; see [Assumptions](#assumptions).

Most write operations must check an [invariant](#invariants) that spans the whole tree. Checking each write in isolation is not enough — two concurrent writes can each pass the check and together break it (e.g. one adds "A blocked by B" while another adds "B blocked by A"). The guarantees below exist to prevent that.

### Crashes

Two kinds of interruption are distinguished throughout:

- A **process crash** — the koan process crashing or being killed — leaves every completed file step in place, in order.
- A **system crash** — power loss or an operating-system crash — may not: the filesystem can lose recent steps, or keep a later step while losing an earlier one.

koan narrows that: every file it writes is flushed to disk before it is published, and its folder after, so a written file survives a system crash whole, and before any later step. The other steps — creating folders, and renaming or removing files and folders — are not flushed, so a system crash can still lose them or reorder them. The one exception is the hard link [`move`](operations.md#move) makes of a task's notes, which is flushed with its folder before the task file moves. All of this relies on the disk honoring flushes, which some consumer drives and virtual machines don't.

A bare "crash" means either.

### Guarantees

These apply to write operations (see [Operation kinds](operations.md#operation-kinds)); setup is exempt.

- **Serialized writes.** At most one write operation runs against a root at a time. The write lock is held only for the duration of one operation — or one CLI command composing several — and nothing is held between them.
- **Input is validated first.** A write rejects bad input (an invalid title, tag, or folder) before contending for the write lock, so malformed input is never reported as contention.
- **Bounded wait.** A write that finds the write lock held waits for it, up to 5 seconds, then fails with a distinct error (`busy`). A write holds the lock for milliseconds, so `busy` means something held it far longer: a `doctor`, `repair`, or `migrate` on a very large tree, or a stuck process. The wait comes after input is validated and changes no outcome, since nothing a write decides on is read until the lock is acquired. An interrupt while waiting is a crash in which nothing was done.
- **Writes decide on current state.** Everything a write's correctness depends on — the task it modifies, the graph it checks for cycles, the references it removes, the next ID — is read after the write lock is acquired, never before.
- **No lost updates.** Following from the above, two writes to the same task, one after the other, both take effect.
- **Atomic files.** Each file koan writes is replaced all-or-nothing. A reader never sees a partially written file from koan. Because each is flushed before it is published (see [Crashes](#crashes)), a file koan wrote comes back from a system crash whole: the version written, or, if the crash came before it was published, the one before. A read reports an empty or garbled task file as unusable (see [Reads](#reads)); only an outside change, or a disk that ignores flushes, can leave one.
- **Writes introduce no violations.** A completed write introduces no new invariant violation, provided the tree already satisfied the invariants the write checks. A tree damaged by a system crash or an outside change stays damaged until it is repaired (see [Diagnosis and repair](#diagnosis-and-repair)); see `create` › Crash behavior in [`create`](operations.md#create) for the one way a write can then compound the damage.
- **Partial work is ordered and reported.** A write that touches several files orders its steps so each intermediate state is as benign as possible. If it fails partway, it reports what it had already done rather than pretending nothing happened.
- **Crashes do not wedge koan.** A write interrupted by a process or system crash never prevents later writes from starting, and needs no manual cleanup to unblock them.
- **Crashes may leave inconsistency.** A write interrupted partway through a multi-file change may leave the tree violating an invariant. After a system crash this holds even for writes whose steps are ordered to be safe, when the steps a crash loses are ones koan does not flush (see [Crashes](#crashes)). Repairing that is the job of [Diagnosis and repair](#diagnosis-and-repair), not of normal operation.

### Walking the tree

These rules apply to every operation that walks the tree — reads and writes alike. (`version` and `info` inspect only a fixed set of files; `create-folder`, `move-folder`, and `create` without `blocked_by` only paths.)

**Which entries count.**

- An entry that matches neither the folder-name nor the task-filename rules (e.g. an editor's backup file, a `README.md`) is skipped silently. It is allowed: koan never needed it, so it is not damage, and [`doctor`](operations.md#doctor) lists it only when asked. A `.md` named like a task's notes but with no task file beside it is an orphan, which `doctor` reports. The root's own `koan.json` is exempt: it is the [root metadata](#root-metadata), not a stray entry.
- So is an entry whose name matches but whose type doesn't: a folder name must be a directory, a task filename a regular file.
- Symbolic links under the root are never followed. A symlink is skipped, whatever it points to, so a folder of tasks linked into the tree is not part of it.
- Unlike a non-matching name, those two are likely mistakes, and hide what they hold: `doctor` reports them.
- **Exception:** an entry the input names, or that its path passes through, is not skipped. It is checked by the [path walk](operations.md#path-walk), which reports a wrong type or a symlink as an error.

**Which problems are reported.** An operation reports only problems that bear on its own result (see [Precedence](operations.md#precedence)):

- Problems with **needed files** — the files the operation must read to do its job — are errors.
- Problems with **relevant files** — files that change the result, or explain it — are warnings. A relevant file that is present but unusable is reported as [`unusable-file`](operations.md#warning-kinds), never silently skipped.
- Problems with any other file the operation walks past are skipped silently, and left to [`doctor`](operations.md#doctor).

**Blockers.** When deriving readiness, a task's blockers are read — and their problems reported — only when the task is open, since a done task's readiness doesn't depend on them. (The cycle check in [`block`](operations.md#block) is different: it follows every task on its path, open or done.) For an open task, every blocker is evaluated, even once one is known to block, so the same tree always yields the same `blocking` list and the same warnings.

**Duplicates.** If several task files with one ID all exist, the IDs are genuine duplicates, and the operation never picks one:

- A read returns every copy **within its scope**, and reports a [`duplicate-id`](operations.md#warning-kinds) warning when more than one copy is in scope. Copies outside its scope are left to `doctor`.
- A duplicated ID among an open task's blockers counts as blocking, and is reported as `duplicate-id`.
- A write refuses to act on a duplicated ID (`conflict`, `rule`: `duplicate-id`): it must know which task it changes.

**Folders that can't be listed.** If the walk meets a folder it can't list (e.g. permission denied):

- Operations that return a collection (`frontier`, `list`) report an [`unreadable-folder`](operations.md#warning-kinds) warning and carry on; the folder's tasks are missing from the result.
- Operations that must find one ID, or prove it absent or unique, or find every reference to one (`show`, `why`, `done`, `reopen`, `block`, `unblock`, `update`, `move`, `delete`, `delete-folder`, `create` with `blocked_by`), fail with `io`: they cannot answer correctly without the whole tree.

### Reads

Reads take no lock. A read is not a snapshot: it may combine state from different instants, and may be stale by the time it is shown. Every file koan wrote is whole when a read uses it (short of a system crash; see [Crashes](#crashes)). Files written by other programs — an editor saving notes, git, a sync tool — may be seen mid-write. A partially written task file fails to parse and is reported as unusable; a later read sees it whole. A partially written `.md` cannot be detected, since notes have no structure; it is returned as found. Only writes guarantee invariants; a read that overlaps a multi-file write may see part of it (e.g. a deleted task's ID still in another task's `blocked_by`).

A read absorbs what concurrent writes cause:

- A file that disappears before it can be read was deleted or moved; it is skipped silently.
- A task seen twice because it moved mid-read — the first file no longer exists by the end of the read — is counted once, at the location seen last.
- A task moved mid-read may be missed entirely; a read cannot detect this.

### Non-guarantees

- **No reservation.** Asking which tasks are ready ([`frontier`](operations.md#frontier)) takes no lock, writes nothing, and marks nothing. Two callers asking a second apart are told the same task, and neither learns about the other. That is fine while a person hands out work, and a silent collision once agents pick work for themselves — see [Claims](#claims).

### Write lock

The write lock that serializes writes (see [Guarantees](#guarantees)) is the **root directory itself**:

- **No lock file.** Nothing is written to take the lock, so there is nothing to clean up, delete, or recreate out from under a holder, and nothing machine-specific travels with the tree.
- **One lock per tree.** Every process reaching the root — through any path spelling, symlink, or environment — contends on the same lock.
- **Released by the holder's end.** The lock is released when the write finishes, or when the process holding it exits or crashes. Programs koan starts never inherit it.
- **One directory per write.** A write stays in the directory it locked: repointing a symlinked root mid-write cannot split one write across two directories.
- **Local filesystems only**, per [Assumptions](#assumptions).

How the lock is taken, and how files are replaced atomically, is in the implementation spec's [Mechanism](implementation-spec.md#mechanism).

## Diagnosis and repair

A system crash or an outside change can leave a tree that breaks an [invariant](#invariants), or that holds entries no rule accounts for. Normal operations report or absorb such damage, and never repair it (see [Guarantees](#guarantees)). Two operations do: [`doctor`](operations.md#doctor) finds it, and [`repair`](operations.md#repair) fixes what can be fixed safely. Each problem they find is a [finding](operations.md#findings), of one of a fixed set of [kinds](operations.md#finding-kinds).

- **At rest.** Both take the write lock, even `doctor`, which changes nothing. With no write running, every koan temp file is a leftover, never a write in progress, and what `doctor` reports is the tree's state, not a write half done. If another write holds the lock, both wait for it like any write (see *Bounded wait* in [Guarantees](#guarantees)).
- **They run when nothing else can.** Both need the config and the root it names. `doctor` needs nothing else: a missing or unusable `koan.json` or state file is a finding, not an error. `repair` needs each of the two usable, or missing and named for rebuilding (see its [Preconditions](operations.md#repair)). They are the way out of a root that every other operation refuses. `doctor` also runs while a migration is pending, and reports it; `repair` doesn't, since it writes only this binary's formats (see [`repair`](operations.md#repair)).
- **Formats are `migrate`'s.** A pending migration, and each task file in an older format, are findings that `repair` never acts on: `doctor` reports them (`migration-pending`, `old-format`) with a suggestion to run [`migrate`](operations.md#migrate), which alone converts formats (see [Migrations](#migrations)).
- **The whole tree.** Both walk every folder, and look at what other operations skip: koan's own temp files and folders, entries that match no naming rule, and `.md` files with no task file. Other hidden entries (`.git`, `.DS_Store`) are still ignored.
- **Safe repairs only.** `repair` changes only what can't lose information or change meaning, given the tree is at rest: a temp file holds nothing anyone wrote; raising `last_id` only moves it up, as it always moves; a dangling reference names a task that isn't there; an empty `.md`, or a second name for notes that are also at their task, holds nothing that isn't kept elsewhere. Duplicate IDs, cycles, and unusable files all need someone to decide which version is right, so `doctor` explains them and suggests what to run, and `repair` leaves them to a person.
- **Repairs are ordinary writes.** Each file `repair` changes is written atomically, as every write's is, and running it again is always safe.

Every finding kind is in one of four classes:

| Class | What `repair` does | Kinds |
|---|---|---|
| ***auto*** | Repairs it whenever it runs, unless the caller names other kinds. | `temp-leftover`, `id-above-last-id`, `dangling-reference`, `orphan-notes` (only the items that are safe; see [Finding kinds](operations.md#finding-kinds)) |
| ***on-request*** | Repairs it only when the caller names the kind. | `metadata-missing`, `state-missing` |
| ***manual*** | Never repairs it. `doctor` reports it, with a suggestion. | `duplicate-id`, `cycle`, `unusable-file`, `metadata-unusable`, `state-unusable`, `migration-pending`, `old-format`, `nested-tree`, `skipped-entry`, `unreadable-folder`, `case-clash` |
| ***informational*** | Never repairs it. `doctor` reports it only when the caller names the kind, and it never makes a tree unhealthy: it is not damage. | `stray-entry` |

Rebuilding a missing state file is on-request because the `last_id` it writes is the highest ID found. A task with a higher ID that was deleted before the state file was lost would have its ID issued again, breaking *Never reused* (see [Task IDs](#task-ids)). Only the user knows, e.g. from git history, whether that happened. Rebuilding a missing `koan.json` guesses nothing, but is on-request too: it declares the directory the config names a koan tree, which is the user's call when the marker is gone.

`stray-entry` is informational so that a user's own files, and whatever their editor leaves, never make a tree look damaged. koan ignores those entries safely, and listing them in every report would only bury the findings that matter.

## Configuration

### Config file

The config is `config.toml` in koan's config directory, and has one entry, the root path:

```toml
root = "~/tasks"
```

| Platform | Config directory |
|---|---|
| Linux | `$XDG_CONFIG_HOME/koan` when `XDG_CONFIG_HOME` is set to an absolute path, otherwise `~/.config/koan`. A relative `XDG_CONFIG_HOME` is ignored, as the XDG Base Directory spec requires. |
| macOS | `~/Library/Application Support/koan` |

`~` is the user's home directory, taken from `HOME`, which must be set to an absolute path. When the config directory can't be determined — no usable `HOME`, and on Linux no absolute `XDG_CONFIG_HOME` either — every operation that needs the config fails with [`environment`](operations.md#error-kinds), and [`info`](operations.md#info) reports it as state.

The config must be a regular file, or a symlink to one (into a dotfiles checkout, say); anything else in its place makes it `corrupt`.

### State file

Beside the config, in the same directory, koan keeps `state.json`: this machine's ID counter for the root. It is the only state koan keeps outside the tree, and it is there, not in the tree, so that nothing done to the tree's files — a `git restore`, a revert, a merge, a sync — can move the counter backwards (see [Task IDs](#task-ids)). Its normative JSON Schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "state-file",
  "type": "object",
  "required": ["schema", "root", "last_id"],
  "properties": {
    "schema": { "const": 1 },
    "root": { "type": "string", "minLength": 1 },
    "last_id": { "type": "integer", "minimum": 0, "maximum": 999999999999999 }
  },
  "additionalProperties": false
}
```

- **`schema`** — Version of the state file's format. It is checked as every versioned file is (see [File validity](#file-validity)); there is no older format of it.
- **`root`** — The root this counter belongs to: the absolute path the config's `root` stands for, cleaned and with `~/` expanded. A state file that names any other root is not this root's: it counts as missing, so pointing the config at a different tree never issues IDs from another tree's counter.
- **`last_id`** — The highest task ID ever issued in this tree by this machine; `0` before the first task. Never decreases (see [Task IDs](#task-ids)).

The state file is written by [`init`](operations.md#init), by every write that issues an ID, by [`repair`](operations.md#repair) (raising `last_id`, or rebuilding a lost file), and by [`migrate`](operations.md#migrate) when it moves `last_id` out of an older `koan.json`. It is written as every file koan writes is, atomically and flushed, and only while the root's [write lock](#write-lock) is held (`init` creating a new tree apart: nothing else uses that root yet). A temp file that an interrupted write leaves beside it is removed by the next write of the state file. It must be a regular file; anything else in its place makes it `corrupt`.

A root with no state file, or one for another root, is *not initialized* (see [Root states](operations.md#root-states)): the tree is all there, but this machine has no counter for it. [`init`](operations.md#init) writes one when it attaches a tree, and `repair` rebuilds a lost one on request.

koan keeps no other per-machine state.

### Migrating from ftask

koan was called ftask, and a setup ftask made is moved to koan's names by the first command that reads files (every one but [`version`](operations.md#version)), before it does anything else:

- **The config.** When koan's config is missing and ftask's — `config.toml` in the config directory as above, named `ftask` instead of `koan` — is there, it is moved into koan's config directory (a symlink moves as the symlink). ftask's config directory is then removed if that left it empty.
- **The root's metadata.** When the root the config names has no `koan.json` but has `ftask.json`, the file is renamed to `koan.json`, holding the [write lock](#guarantees). Only the root's: an `ftask.json` below it is a `stray-entry`.
- **Temp files.** A `.ftask-tmp-` file or folder anywhere under the root is ftask's [temp leftover](operations.md#finding-kinds), and counts as koan's own.

Each move is reported as a [`migrated`](operations.md#warning-kinds) warning. A setup that has koan's file already is never touched, so ftask's stays where it is, ignored. A move that fails fails the command, with the [`io`](operations.md#error-kinds) error, and the next command tries again; a config or root that can't be used is left alone for the command to report as it would anyway.

This move is not a [migration step](#migrations): it changes names, never a `schema`. A tree it brings over may still be in an older format, and the command that moved it then fails with `migration-pending`, like any other, until [`migrate`](operations.md#migrate) runs.

### Root path

- **Form.** `root` is an absolute path, or a path beginning with `~/`, which koan expands to the user's home directory when reading the config. Any other form — a relative path, `~user/` — makes the config `corrupt`. `init` always writes an absolute path.
- **No `..`.** A root path may not contain `..` segments: removing them lexically can change which directory is meant when an earlier segment is a symlink. [`init`](operations.md#init) rejects such a path; in a hand-edited config it makes the config `corrupt`.
- **No NUL.** A root path may not contain a NUL character, which no OS path can hold. [`init`](operations.md#init) rejects such a path; in a hand-edited config (where TOML's `\u0000` can write one) it makes the config `corrupt`.
- **Stored as given, cleaned.** The path is kept as the user spelled it, after lexical cleanup only: no trailing `/`, no empty or `.` segments. A hand-edited config that isn't clean (e.g. a trailing `/`) is accepted and cleaned when read. Symlinks are **not** resolved, so a root reached through a symlink keeps working when the symlink is repointed.
- **Reported as stored.** Paths koan reports under the root (e.g. a task's `notes_path`) are built from the stored path (with `~/` expanded), not from a symlink-resolved one.
- **Resolving the root.** The root path itself is resolved through symlinks, and it counts as present only if it leads to a directory. (Symlinks *inside* the root are never followed; see [Walking the tree](#walking-the-tree).) A `~/` root with no home directory to expand it into counts as missing. A missing root is handled as [Root states](operations.md#root-states) describes.

## Future work

### Checking a path before init

Check a candidate path before [`init`](operations.md#init), with no root configured: report whether the path, or any directory above it, lies inside an existing tree. This is the check `init` deliberately does not make. It needs no root, so it does not belong in [`doctor`](operations.md#doctor), which diagnoses the configured one.

### Renumbering a duplicate ID

Let [`repair`](operations.md#repair) give one copy of a [`duplicate-id`](operations.md#finding-kinds) a fresh ID, when the user names the copy to keep. Which copy keeps the ID is the user's decision; doing the renumbering is not.

### Claims

Let a caller reserve a ready task so that concurrent callers are not handed the same one (see *No reservation* in [Non-guarantees](#non-guarantees)).

### Idempotent create

Let a caller pass an optional request key to [`create`](operations.md#create), so that retrying after a crash or an unclear outcome returns the task already created instead of creating a duplicate. Requires somewhere to remember keys.

### Blocking existing tasks in a batch

Let a task in a [`create-batch`](operations.md#create-batch) name existing tasks that should wait on it, so breaking a task down into steps is one call. The batch would then rewrite existing task files, which it otherwise never touches, and could close a cycle (a new task blocked by an existing one and blocking it), so it needs [`block`](operations.md#block)'s cycle check. Meanwhile, a `block` after the batch does the same in a second call.

### Explicit completion time

Let a caller set `completed_at` when completing a task (e.g. to backdate it), instead of always using the current time (see [`done`](operations.md#done)).

### Event hooks

Let the user name commands in koan's config that run after a task changes state — created, completed, reopened, deleted — each given the task, as [`show`](operations.md#show) returns it, as JSON on stdin. A hook runs after the operation has succeeded and never undoes it: a failing hook is a warning, not an error. Tools built on koan react to completions without polling: shingi's start and done tasks, matched by their `extra` keys, could remove a unit's worktree when it is done, archive its notes, or tell a coordinator a piece has finished. koan would know nothing of those tools; it only reports what changed.

### Tree view

A command that pretty-prints the tree — folders and tasks, nested — for people. Presentation only: built on [`list`](operations.md#list) (with `include_folders`), so it needs no new operation.

