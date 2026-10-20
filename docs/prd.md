# koan rewrite: PRD

Oct 20, 2026 · @SuperUser

This doc fixes how the rewrite builds koan: its layout, storage, writes and concurrency, and its protocol of commands, output and errors. What koan must guarantee is in the rules doc, koan: invariants and rules. The rules doc is normative: where the two disagree, it wins, and anything that would change a rule goes there, not here.

The command specification comes after this doc, as JSON Schemas and a command reference in the repo. It fixes every command's flags and every JSON field, the archive's included; this doc fixes the shape they take. Choices this doc makes that the rules doc leaves open are listed in Deferred and choices.

## Goals and non-goals

koan is task management for one person on one machine, driven mostly by agents such as Claude Code and OpenCode. Tasks and their structure live in a database; their notes are plain files in a directory tree that people and agents open and edit with any tool, Zed included.

**Goals**

- **Notes are files, edited directly.** No tool stands between an agent and a task's notes. Everything in the tree's directory may be edited; structure changes only through koan.
- **Structure is safe under many agents at once.** Any number of agents and the user work on one tree concurrently, and every invariant holds.
- **Fast at the sizes one person reaches.** Every command finishes within 100 ms at 10,000 live tasks on the CI machine, whose OS is named beside the benchmarks, and archiving keeps the live set well below that. Commands whose work grows with the whole tree or their input are budgeted apart: `doctor` and `repair` within 1 s, a `batch` within 100 ms plus 1 ms a part, and `backup` not at all, since it copies every file.
- **Built for agents.** Every command prints one line of JSON, every failure has a kind, and every write says what it changed.
- **Simple to install.** One `go install`, one binary, no system libraries.

**Non-goals**

- **Git.** koan neither uses nor supports git on its tree: no history, branches, worktrees or merges of the tree. Archiving is how finished work leaves; `koan backup` is how the whole of it is kept.
- **More than one machine or one tree.** No sync, no shared trees, no second tree per user.
- **Migrating today's trees.** The rewrite starts fresh with `koan init`.
- **Windows.** Linux and macOS only. The root may be on any local filesystem of theirs, whether it ignores case or not.

## What changes from today

**Why rewrite.** Today every task and folder is a JSON file in the tree, beside its notes, and anything may edit either. Most of today's code copes with that: walking the tree for every lookup, multi-file changes a crash can half finish, unusable files, duplicate IDs, dangling references, and a `doctor` for all of it. Agents also see a `.json` they must never touch beside every `.md` they may, and get it wrong. Folders double as ordering, so a move can change readiness, and an ordering group must be a subtree.

| Today | In the rewrite |
| --- | --- |
| Tasks and folders are JSON files in the tree | Rows in `koan.db`; the tree holds only notes and material |
| `~/koan-root` is the tree | `~/koan-root` holds `koan.db`, `tree/` and `archived/` |
| `@folder.md`, `@koan.json`, `@<id>.json` | `@notes.md`; no other koan file in the tree |
| A folder's `blocked_by`, inherited by everything in it; done when its contents are | Folders are containment only. Ordering is edges between tasks, and a folder orders work through its two gate tasks, `@F.start` and `@F.done`, made when first named |
| `folder block`, `folder unblock`, `folder why` | Gone: a folder is ordered through its gates, with `task block` and `task why` |
| `task done`, “done” | `task complete`, “complete”; the done gate keeps its name |
| Writes report `dependents` on delete only | Writes report what they released and blocked again, and the gates they made: `released`, `reblocked` and `gates_made` |
| Git as the undo, with rules against branching | No git. Archive for finished work, `koan backup` for everything |
| `doctor` tolerates damaged files of every kind | The database can't hold most of them; `doctor` checks the tree against it |
| Claims in `~/.config/koan/state.json` | Claims in `koan.db` |

**Unchanged:** references and kept text, the case rules, claims, the archive's paths and its one-way rule, markers (apart from how they meet archived and deleted folders), `batch` with labels, one line of JSON per command, Linux and macOS.

**No migration.** Today's trees aren't converted. Today's \~/koan-root is a tree, which init refuses as not-a-root, so move it aside first, and pass init --ids-from past its highest ID so today's references stay unique. The rewrite starts with `koan init`.

## Layout

Everything koan keeps is under one directory, `~/koan-root`. The database is the truth about structure; the tree is where notes and material live; the archive sits beside the tree.

&#91;embedded content: koan.db, tree/ and archived/ · one row per task or folder, one directory per folder\]

Each folder row has a directory at its path, and each task row its notes in its folder's directory; a gate's notes are `@start.md` or `@done.md`. No file in the tree holds fields.

| What | Path | Written by |
| --- | --- | --- |
| **Database:** tasks, folders, edges, tags, claims, the ID counter, the archive index, pending filesystem steps | `~/koan-root/koan.db`, with its `-wal` and `-shm` files, and the `-journal` SQLite writes while init creates it | koan only |
| **Tree:** a directory per folder, folder notes, task and gate notes, material | `~/koan-root/tree/` | koan for `@` entries; anyone for file contents |
| **Archive:** what was archived, as files | `~/koan-root/archived/` | koan only |
| **Markers:** which folder a code worktree is for | `.koan/` in that worktree | koan only |

- **One root, one filesystem.** The tree and the archive share a directory, so archiving is one rename within one filesystem. To keep the root elsewhere, make `~/koan-root` a symlink before `koan init`; koan resolves that one link and follows no other.
- **Open `~/koan-root/tree/` in Zed.** Nothing in it is koan's but `@`-named directories and notes, so nothing in it is off-limits to edit. The database sits outside it, where agents working in the tree never see it.
- **The root must be on a local filesystem**, never in a sync folder: SQLite's WAL mode needs shared memory, and the lock needs `flock`.

### The archive

The archive follows the tree by the path an item had when it was archived, so it can be browsed and searched (`rg`) like the tree. Only the archived item carries its ID; the directories above it are plain names.

**Archived notes keep their ID.** A task's notes are `@<id>.md` already. A gate archived on its own gets its ID too, as `@<id>_start.md` or `@<id>_done.md`, since its folder's next gate of that end would land on the same path. A gate archived with its folder keeps `@start.md` or `@done.md`, the folder's directory carrying the ID.

```text
folder 41, /work/HOME-12345        →  archived/work/@41_HOME-12345/ and @41_HOME-12345.json
folder 93, made later at that path →  archived/work/@93_HOME-12345/ and @93_HOME-12345.json
start gate 58, alone, in /work/api →  archived/work/api/@58_start.md and @58_start.json
task 42, in /work/HOME-777         →  archived/work/HOME-777/@42.md and @42.json
task 7, in the root                →  archived/@7.md and @7.json
```

- **An archived folder** is its directory, renamed to `@<id>_<name>`, with everything in it as it was: its notes, its gates' and tasks' notes, the folders under it, and its material. A folder name can't hold `_`, and an ID is never reissued, so no two archived folders collide.
- **The fields go beside it as JSON**, since the tree holds none. Beside every archived item is a file of the same name ending `.json`: `@41_HOME-12345.json` beside the folder `@41_HOME-12345/`, holding the rows of every folder and task in it, their tags, and every edge that touched them, to or from anything; `@42.json` beside a task's `@42.md`; `@58_start.json` beside a lone gate's `@58_start.md`. Only koan writes `archived/`, and every name there carries an ID, so nothing is ever in their way. Each file names its own format, so it can be read whatever koan's database format has become.
- **The directories above an item** are named after the folders it was in, without the `@`. They are not folders, items under one path share them, and nothing in them is overwritten, since every item carries its ID. Where such a directory already exists differing only in case, it is used, and the index records the name on disk, so archiving never refuses for case. The step that archives an item uses such a directory as it finds it, and never makes a second spelling beside it. The spelling the index records is found when the change is made, by listing each directory above the item and counting, beside what is on disk, the directories that pending archive steps, stuck or not, will make above their own items, matched ignoring case: a spelling on disk is used first, then the earliest pending step's. So it is still the one on disk when the step runs, whether an earlier step made it or this one does: only koan writes `archived/`, and steps that make a directory above an item all make it in the one spelling.

### Markers on disk

A marker is a `.koan/` directory in the directory it binds, outside the tree. How markers behave is under Markers.

```text
<dir>/.koan/.gitignore     *: git ignores the directory, the .gitignore included
<dir>/.koan/marker.json    the folder it is bound to
```

| `marker.json` field | Rule |
| --- | --- |
| `schema` | The file's format. |
| `id` | The folder's ID: what the marker binds. The root has none, so it can't be bound. |
| `path` | The folder's path when it was bound: a hint. |
| `dir` | The absolute real path of the directory holding `.koan/`. A marker found anywhere else was copied or moved there, and binds nothing. |

## Storage

The database is SQLite, through a pure-Go driver, in WAL mode. Every ID ever issued has a row in `issued`, which is how koan knows an ID is live, deleted or archived, and where an archived one went.

```sql
-- Every ID ever issued, never deleted. The counter is max(id).
CREATE TABLE issued (
  id      INTEGER PRIMARY KEY CHECK (id BETWEEN 1 AND 999999999999999),
  kind    TEXT NOT NULL CHECK (kind IN ('task', 'folder')),
  state   TEXT NOT NULL CHECK (state IN ('live', 'deleted', 'archived')),
  archive TEXT,                       -- where it went, when archived (I12)
  CHECK ((state = 'archived') = (archive IS NOT NULL))
);

CREATE TABLE folder (
  id     INTEGER PRIMARY KEY REFERENCES issued(id),
  parent INTEGER REFERENCES folder(id),          -- NULL is the root
  name   TEXT NOT NULL,
  title  TEXT,
  extra  TEXT NOT NULL CHECK (json_type(extra) = 'object')
);
CREATE UNIQUE INDEX folder_sibling ON folder(parent, name COLLATE NOCASE);
CREATE UNIQUE INDEX folder_top     ON folder(name COLLATE NOCASE) WHERE parent IS NULL;

CREATE TABLE task (
  id           INTEGER PRIMARY KEY REFERENCES issued(id),
  folder       INTEGER REFERENCES folder(id),    -- NULL is the root
  gate         TEXT CHECK (gate IN ('start', 'done')),
  title        TEXT NOT NULL,
  priority     INTEGER,
  created_at   TEXT NOT NULL,
  completed_at TEXT,
  updated_at   TEXT NOT NULL,
  extra        TEXT NOT NULL CHECK (json_type(extra) = 'object'),
  CHECK (gate IS NULL OR folder IS NOT NULL)      -- the root has no gates
);
CREATE UNIQUE INDEX task_gate ON task(folder, gate) WHERE gate IS NOT NULL;
CREATE INDEX task_folder ON task(folder);

CREATE TABLE edge (
  task    INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  blocker INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  PRIMARY KEY (task, blocker),
  CHECK (task <> blocker)
) WITHOUT ROWID;
CREATE INDEX edge_blocker ON edge(blocker);

CREATE TABLE tag   (task INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE, tag TEXT NOT NULL,
                    PRIMARY KEY (task, tag)) WITHOUT ROWID;
CREATE TABLE claim (task INTEGER PRIMARY KEY REFERENCES task(id) ON DELETE CASCADE,
                    claimed_at TEXT NOT NULL, extra TEXT NOT NULL CHECK (json_type(extra) = 'object'));

-- One row, written by init: the first ID this root issues (init --ids-from).
CREATE TABLE root (first_id INTEGER NOT NULL CHECK (first_id >= 1));

-- Filesystem steps committed with a change, and not yet carried out (see Writes).
-- change is the seq of its change's first step; stuck marks a step set aside, with the rest of its change.
CREATE TABLE pending (seq INTEGER PRIMARY KEY, change INTEGER NOT NULL,
                      step TEXT NOT NULL CHECK (json_valid(step)),
                      stuck INTEGER NOT NULL DEFAULT 0 CHECK (stuck IN (0, 1)));
```

- **Format.** `PRAGMA user_version` holds the database's format. The first format is 1. A binary reads exactly one; any other is `unsupported-format`. Before 1.0 a new format means a fresh `init`, with no conversion. The archive needs none, since its JSON is read with any tool. A fresh root issues IDs from 1, unless init is given --ids-from N. Starting past the old root's highest ID keeps every ID unique across every root you have had, so old notes and archives never meet a second @42. An old archived/ can then be moved into the new root, though its IDs aren't in the new index.
- **Timestamps** are stored as their text form, `YYYY-MM-DDTHH:MM:SSZ`, so they sort as text.
- **`folder.parent` has no cascade on purpose.** koan removes a folder's subtree itself, so it can check for material first and report what it removed.

## Writes

A write commits the database first and changes the tree after, from a list of filesystem steps committed with it. A crash at any point leaves either nothing done, or steps that the next writer finishes.

**The steps of a write**

1. Check the input's shape: fields, patterns, references as written. Nothing is read yet.
2. Take the lock, and finish any filesystem steps an earlier write left pending, skipping stuck ones.
3. `BEGIN IMMEDIATE`. Resolve every reference against the database as it stands.
4. Check the tree for what the change needs: a destination path is free, a folder to delete holds no material unless `force`. A destination is checked on disk as well, each missing parent a create, a folder move or a batch makes included: listing its parent directory, an entry by that name, or one differing from it only in case, refuses the change as `exists`, with that entry's path. A case-only rename's own directory is not in its way.
5. Change the rows, check for cycles, and insert into `pending` the filesystem steps that bring the tree in line.
6. `COMMIT`.
7. Carry out the steps, fsync each directory they concern, whether this write changed it or found the step already done, then delete their rows in one transaction.
8. Release the lock.

**Once COMMIT succeeds, the command succeeds.** It returns `ok` with its result whatever then happens to the filesystem steps, so an agent never retries a change that was made. The lock keeps other writers out, but not Zed or a person: a step fails when the operating system refuses it, or when a hand change got to its path first. A failed step stays pending, the rest of its change waits with it, since those may depend on it, and later changes go on, except where they overlap it (below); the result carries `pending-step`, and the next write finishes it. A step the operating system keeps refusing stays pending: every later write retries it, fails again and returns `pending-step`, until the cause is fixed. That is by design. A crash between the commit and the output is the one case left to the agent, which reads the tree to see what happened.

**Filesystem steps.** Each is stated as the result it wants, so carrying it out twice is harmless.

| Step | Done when | Carried out by |
| --- | --- | --- |
| Make a folder's directory | it exists, holding `@notes.md` | `mkdir`, then `@notes.md` holding the notes' first content the step holds, empty unless `folder create`'s input gives notes, making whichever is missing. Empty notes are created in place; content is written through a temp file (below), then one `rename` into place. A `@notes.md` already there, made by hand, is left as it is |
| Write notes | the file exists | writing the content the step holds to a temp file (below), then one `rename` into place |
| Move notes or a directory | the source is gone and the destination exists | one `rename`, atomic on one filesystem; a case-only rename takes two, through a temporary name (below) |
| Remove notes or a directory | the path is gone | `unlink` for notes. A directory remove whose change was forced is a recursive remove that can be run again. Unless its change was forced, a directory remove first looks for material, counted as a delete counts it (rules doc, Changes), as step 4 does, and finding any it removes nothing and is stuck (below). Since the rows may be gone, the step records what it may remove: the directories of the folders being deleted, and the notes of what is being deleted, each such folder's `@notes.md`, `@start.md` and `@done.md` and each such task's `@<id>.md`. It walks the directory and every `@`-named directory under it. Any entry not named with `@` is material, as in the tree. An `@` entry the step doesn't record is material when it holds content: a file of one byte or more, or a directory holding such a file or any entry not named with `@`. A zero-byte file and `.DS_Store` never are. Finding none, it never removes recursively. It works bottom-up: in each directory it unlinks only the entries it listed there that aren't material, never a name without `@` other than `.DS_Store`, then removes the directory with `rmdir`, which the operating system refuses while it isn't empty. So material written while the step runs, by Zed, a person or an agent, is never removed: the step fails and stays pending, and on the next write its walk finds the material and it is stuck. The step records whether its change was forced. |
| Archive | it sits in the archive | making the directories above it, writing its JSON through a temp file renamed into place beside where it goes, then one rename out of the tree |

**Done when is judged by listing the parent directory** and comparing names byte for byte, never by `stat`. On a filesystem that ignores case, `@work` and `@Work` stat as one entry, so a case-only rename would find both its source and its destination.

**Names that differ only in case.** A step lists each directory on its paths, from `tree/` or `archived/` down, not only the last one. Every name it uses counts, each needing to be absent or spelled exactly: its source, its destination, its temporary name, its temp file, a folder's `@notes.md`, and each directory above them. A name in the listing that equals one of them only ignoring case fails the step on every filesystem, so a remove of `@old` beside a hand-respelled `@Old` waits as pending rather than being judged done. The step stays pending with `pending-step`, as when the operating system refuses. Without this check, the filesystem would decide: one that ignores case opens `@Work` for `@work`, or refuses to make `@api` beside `@Api`, while one that tells case apart does the opposite. Doctor reports the other spelling as `case-differs` or `stray-directory`. Once repair or a person puts it right, the next write finishes the step. Two exceptions: a case-only rename's own source, temporary name and destination, and a directory above an archived item, which is used in whatever case it is there, as The archive says.

**A case-only rename** goes through a temporary name on every filesystem, since one that ignores case returns success from `rename("@work", "@Work")` and leaves the old spelling in place. The step renames `@work` to `@.case-<seq>`, with its own `seq`, then `@.case-<seq>` to `@Work`. It records all three names, so a step cut short resumes from whichever of them the listing shows. It is done when the listing shows the new spelling, byte for byte, and neither of the others.

**A temp file** for notes or archive JSON is `@.tmp-<seq>`, with its step's own `seq`, in the directory the file goes into. A step cut short writes it afresh, and once the step is done, one left behind is removed.

**A move that finds both** its source and its destination finishes when one side is empty: a zero-byte notes file, or a directory holding nothing but zero-byte `@` notes files, named exactly as koan names them, and any `.DS_Store`, which is disposable and goes with it. Any other file is content, however small, since material is full of meaningful empty files such as `__init__.py` and `.gitkeep`. An empty source is removed, an empty destination replaced. The usual case is an agent writing notes at a task's new path before the move ran, while the old file is still the empty one create wrote.

**A stuck step** finds both sides holding content, or neither when a directory is needed; a move of notes that finds neither is done, since missing notes read as empty (T3). A directory remove that wasn't forced is stuck when it finds material: what was checked at step 4 can change before the step runs, in a crash or a failed step, and koan removes material only when told to. Its row is marked `stuck`, with the rest of its change, since those may depend on it, and records what it found: `both`, `neither` or `material`. Only the step that found it is reported, by `stuck-step` and by doctor; the rest of its change is set aside with it. Later writes go on and skip stuck rows. A later change whose steps overlap a stuck step waits behind it (below), so it can become stuck only because an earlier one is, and not by working on paths the stuck step still holds. Every result carries `stuck-step` until a person acts: keeps one copy, or moves the material out; `repair` then retries every stuck change in `seq` order, and clears each that now succeeds. A step holds everything it needs, notes content and archived rows included, since the rows it came from may already be gone.

**Steps on the same paths run in order.** Two steps overlap when a path one touches, its source, destination, temporary name or archive JSON, is the same as a path the other touches, or above or below it. These paths are compared ignoring case, so the order is the same on every filesystem. A step waits while any step of an earlier change that overlaps it is still pending, whether stuck or not. A step that waits is held with the rest of its change, as a failed step's change is, and gives `pending-step`. It runs once the earlier step is done. That is why a step's paths, fixed when its change commits, are still right when it runs: whatever an earlier change was still doing on them has finished. For example, a task created in a folder whose directory a failed step hasn't made yet gets its notes only after the directory is there. A deleted folder's directory still on disk, its remove refused, is different: it takes its path (rules doc, Changes), so a folder created there is refused with `exists`.

**Notes files.** Creating a task writes its `@<id>.md`, and creating a folder its `@notes.md`, empty unless the input gives notes, so that it shows in Zed straight away. A folder's notes come with its directory, from the one step that makes both; a missing parent's are empty. A missing notes file still reads as empty notes (T3).

**Issuing IDs in a change.** A change issues IDs in the order it makes things: its folders, parents first, in tree order; then its tasks, in input order; then the gates its edges name: first the task `task block` is given, when that is a gate the folder lacks, then the rest in the order those edges are given, each task's `blocked_by` before its `blocks`. A change's edges resolve after its tasks are made, so an edge naming the ID a task in the same change gets names that task: given to that task itself, it is an edge to itself, refused as `invalid-input`. A batch follows the same order, below.

**Making a gate.** When an edge names a gate the folder lacks, the same transaction issues it an ID, inserts its row with the default fields (`Start` or `Done`, `null` priority, no tags, empty extra), and adds a step writing an empty `@start.md` or `@done.md`. The result lists the gates it made. A @start.md or @done.md already there, made by hand, becomes the gate's notes, since the step is done once the file exists.

**Removing rows.** Before a task row is deleted, the transaction looks up the tasks that wait on it through `edge_blocker`, gives each a new `updated_at`, and reports in `released` those it leaves ready. `ON DELETE CASCADE` then removes the edges; it is a backstop, never the only record that an edge went. Delete and archive both work this way.

**Resolving a batch.** A batch is one transaction, and is read as the tree it would leave:

1. Check every part's shape, and that no label is defined twice.
2. Make the batch's folders, parents first, in tree order.
3. Make its tasks, resolving each one's folder against the tree as it now stands, issuing IDs in input order and binding each label to its task's ID.
4. Resolve every edge's references against the tree as it now stands, labels included, and write every edge, making any gate named. A label no part defines is found here, as `invalid-input` pointing at the edge, since step 1 checks only that no label is defined twice.
5. Check for cycles over every edge the batch added.

Any refusal refuses the whole batch, and nothing is written.

**Archiving.** One transaction removes the rows of everything that leaves, and every edge between it and what stays; sets each leaving ID's `issued.state` to `archived` and its `archive` to where that ID itself sits in the archive: an archived folder's `…/@41_HOME-12345/`, a folder under it `…/@41_HOME-12345/@api/`, a task under it `…/@41_HOME-12345/@api/@42.md`, a gate `…/@41_HOME-12345/@start.md`, a task or gate archived alone its own `@42.md` or `@58_start.md`, or, when its notes file wasn't there, its JSON, `@42.json` or `@58_start.json`, so the index always names a file that exists; and adds the archive step. The step carries the removed rows. It makes the directories above the item in the archive, writes the rows as JSON to a temp file renamed into place beside where the item will go, then renames the item out of the tree. A task whose notes file is missing is archived as its JSON alone, since a missing file reads as empty notes.

## Concurrency

Writes are serialized by one lock; reads never wait on a writer or on that lock. Each CLI invocation is a short-lived process with its own connection.

| Rule | How |
| --- | --- |
| **One writer at a time, for the whole change** | A `flock` on `~/koan-root`, taken exclusively before the transaction and released after the change's last filesystem step. SQLite's own write lock ends at `COMMIT`, too early to cover the tree. Only writes (`repair` included), `init` and `backup`, for the spans it holds the lock, take it exclusively. |
| **A write decides on current state** | Inside the lock, the database work runs in `BEGIN IMMEDIATE`, and every reference is resolved there. A plain `BEGIN` that reads then writes can fail on a snapshot that went stale, which `busy_timeout` doesn't retry. |
| **Reads see one committed state, and never wait on a writer** | WAL mode: a read transaction sees the snapshot it started with, and takes koan's lock only for the brief try below, made only when steps are pending. SQLite may still make a reader wait briefly while it tidies its own files: the last connection to close checkpoints the WAL and deletes it, and the next to open rebuilds its index. `busy_timeout` makes that a wait, not an error; S0 measured waits of up to 54 ms (docs/spikes.md). |
| **Waiting is bounded** | A writer that finds the lock held waits up to 5 seconds, then fails with `busy`, which is always safe to retry. `busy_timeout = 5000` is the same backstop inside SQLite, for other writers of the file such as the `sqlite3` tool, and for SQLite's own tidying; a command that SQLite keeps busy for 5 seconds also fails with `busy`. |
| **Commits are durable** | `synchronous = FULL`, `fullfsync = ON` and `checkpoint_fullfsync = ON`, without which macOS doesn't flush the drive. A rename is made durable by fsyncing its directories, with `F_FULLFSYNC` on macOS, before its step's row is deleted, whichever process made it: a step found already done may have been carried out by a write that crashed before its fsync. |
| **Per connection** | `foreign_keys = ON`, `busy_timeout` and the three sync pragmas above are set on every open, since SQLite doesn't store them; `journal_mode = WAL` is stored in the file by `init`. |

- **`doctor` reads one snapshot and takes no koan lock**, so no writer waits for it. `repair` is a write, and takes the lock like any other.
- **A reader may see a change whose filesystem steps are still running**, between the commit and the last step. It then reports `pending-filesystem` beside any path it returns, and the path it gives is where the file is going. If steps are pending, the reader tries the lock shared, without waiting, so two readers never keep each other out. If it gets it, it checks pending again, since a write may have finished in between, and releases the lock at once. If it doesn't, something holds the lock exclusively, nearly always a write carrying the steps out, and the reader reports `pending-filesystem`; while `init` or `backup` holds the lock it says the same, which is harmless, since the steps are finished by the next write. Steps still pending have no write carrying them out, because one crashed or failed a step, or they wait behind an earlier change's step on the same paths, so the reader reports pending-step instead; the next write or repair finishes them.

## Invariants, enforced and tested

Every invariant of the rules doc has a mechanism that keeps it and a test that shows it held. The tests are described under Testing.

| Invariant | Enforced by | Tested by |
| --- | --- | --- |
| I1 unique IDs | `issued` primary key; `task.id` and `folder.id` reference it, kind checked on insert | properties |
| I2 never reissued | `issued` rows are never deleted; a new ID is `the greater of max(id) + 1 and root.first_id,` in the same transaction | properties; crash points |
| I3 a tree | `folder.parent` foreign key; a move is refused when its destination's parents pass through the folder | properties |
| I4 siblings differ ignoring case | `folder_sibling` and `folder_top`, since SQLite treats `NULL` parents as distinct; `NOCASE` folds ASCII only, which the name pattern guarantees | case; properties |
| I5 no dangling edges | `edge` foreign keys with `ON DELETE CASCADE`, as a backstop: before a delete, koan finds the dependents through edge\_blocker, updates their updated\_at and reports them (see Writes) | properties |
| I6 no edge to itself | `CHECK (task <> blocker)` | properties |
| I7 acyclic | in the transaction, a search from each new edge's blocker back to its task, over the edges as the change leaves them | properties; reference model |
| I8 claims on open tasks | `claim` foreign key with cascade; the completing transaction deletes the claim | properties; concurrency |
| I9 values | `CHECK`s where SQL can say it; koan for the name and tag patterns, titles and real dates | properties; output |
| I10 whole changes | one transaction per change, then filesystem steps that can be run again (see Writes) | crash points; concurrency |
| I11 gates | `task_gate`, the root `CHECK`, and a move refused for a gate | properties |
| I12 archive index | `issued.state` and `issued.archive`, written in the archiving transaction | properties; crash points |
| T1–T6 the files | the filesystem steps of each change; `doctor` checks them | crash points; properties |

## Command surface

### Commands

The surface is today's, with `task done` renamed `task complete`, and the folder blocking commands gone: a folder is ordered through its gates, which are tasks. Refusals are listed beyond the usual `not-found` and `invalid-input`; each is a `conflict` rule (spec Errors → Conflict rules).

**Tasks**

| Command | Does | Result reports | Refused when |
| --- | --- | --- | --- |
| `task create <title>` | Makes a task in `--folder` (the root by default), with `--priority`, `--tags`, `--extra`, `--notes`, `--blocked-by`, `--blocks` | its ID; `gates_made`; `reblocked`; the `complete-blocker` and `claimed-blocked` warnings | `cycle` |
| `task show <ref>` | Reads one task: its fields, folder and path, gate, readiness, blockers with their state, what it blocks, claim, notes path | the record |  |
| `task list` | Tasks in tree order, open ones unless `--state` says otherwise, gates included; filtered by `--folder` (a subtree, or with `--recursive=false` the folder alone), `--readiness`, `--tags-any`, `--tags-all`, `--claimed`, with `--limit` (100 by default) and `--fields` | records; the total |  |
| `task frontier` | Ready, unclaimed tasks in work order, under `--folder` or everywhere, with `--limit` (100 by default) | records; the total |  |
| `task why <ref>` | What a task waits on: its open blockers, and the ready tasks it waits on at any depth through open blockers, which are what to work on; a complete task holds nothing back, so the walk never passes one | blockers; ready tasks |  |
| `task update <ref>` | Changes title, priority, tags (add and remove) and `extra` (merged) | its ID |  |
| `task complete <ref>` | Marks it complete and ends its claim | its ID; `released` |  |
| `task reopen <ref>` | Marks it open again | its ID; `reblocked`; the `claimed-blocked` warning |  |
| `task block <ref>` | Adds edges, by `--blocked-by` and `--blocks` | its ID; `gates_made`; `reblocked`; the `complete-blocker` and `claimed-blocked` warnings | `cycle` |
| `task unblock <ref>` | Removes edges, by the same options | its ID; `released` |  |
| `task move <ref> --to <folder>` | Puts it in another folder or the root | its ID; its notes path; the `moved-claimed` warning | `gate` |
| `task delete <ref>` | Removes it, its notes, its claim and its edges | `released` |  |
| `task archive <ref>` | Moves a complete task to the archive | where it went | `not-finished` |
| `task claim <ref>` | Claims it, with an `--extra` the caller keeps | the claim | `claimed`, `complete` |
| `task claim-next` | Claims the first task of the frontier, under `--folder` or everywhere | the task and claim, or nothing |  |
| `task release <ref>` | Ends its claim | its ID |  |

**Folders**

| Command | Does | Result reports | Refused when |
| --- | --- | --- | --- |
| `folder create <path>` | Makes the folder and any missing parents, with `--title` and `--extra`; no gates | the IDs of folders made | `case-mismatch`, `exists` |
| `folder show <ref>` | Reads one folder: fields, path, its gates and their state, open and complete counts under it, whether it is finished, its notes path and directory | the record |  |
| `folder list` | Folders in tree order under `--folder`, with `--limit` (100 by default) and `--fields` | records; the total |  |
| `folder update <ref>` | Changes title and `extra` | its ID |  |
| `folder move <ref> --to <path>` | Moves or renames it, everything under it included, making any missing parents | its ID; its new path; the moved-claimed warning | `into-itself`, `case-mismatch`, `exists` |
| `folder delete <ref>` | Removes it and everything under it, with `--contents` when it holds tasks or folders, `--force` when it holds material | what it removed; `released` | `not-empty`, `has-material` |
| `folder archive <ref>` | Moves a finished folder to the archive | where it went | `not-finished` |

**Everything else**

| Command | Does | Result reports | Refused when |
| --- | --- | --- | --- |
| `batch -i <file>` | Applies a plan of folders, tasks and edges as one change, from a file or `-` for standard input | task IDs; `labels`; folders made; `gates_made` | whatever refuses a part |
| `where [path]` | Says which folder or task a path is in, in the tree or in a bound worktree | the kept-text pair, ID and path |  |
| `context [path]` | Briefs an agent on the folder a path is in: its notes, its gates, its open tasks | the brief |  |
| `marker bind <ref>`, `marker unbind` | Binds a directory, `--dir` or the current one, to a folder, or removes the binding | the marker | see Markers |
| `init` | Makes `~/koan-root`, its `tree/` and `archived/`, and the database, issuing IDs from --ids-from; changes nothing that is there | the paths | `not-a-root`, `exists` |
| `info`, `version` | Report the root's paths, format, state and next ID, and the binary's version; no lock | the report |  |
| `doctor` | Checks the database and the tree against it; changes nothing | findings |  |
| `repair` | Fixes the findings that lose nothing, by `--kinds` | what it fixed |  |
| `backup <dir>` | Copies the database, the tree and the archive into an empty directory, then checks the copy with doctor | what it wrote | `exists` |

### Output

Every command prints exactly one line of JSON to standard output, and exits 0 on success, 1 on failure, 2 when the command line can't be read, and 3 when koan couldn't write its output: the outcome is then unknown, and the caller looks before running it again.

```json
{"ok": true,  "result": { … }, "warnings": [ … ]}
{"ok": false, "error": {"kind": "conflict", "rule": "cycle", "message": "…", "ids": [8, 10, 57], "paths": [], "problems": [ … ], "archived": []}, "warnings": [ … ]}
```

- **Writes return identities; reads return records.** A write's result names what it changed by ID, ready to pass to the next command. A record is exact only as of the call.
- **IDs are plain integers in JSON.** A gate is marked by its `gate` field and its folder. Every record also carries `ref`, the kept-text pair such as `@57 (/work/api.done)`.

**Records**

| Record | Holds |
| --- | --- |
| Task | `id`, `gate`, `title`, `priority`, `folder` (ID and path), `created_at`, `completed_at`, `updated_at`, `state` (`open` or `complete`), `readiness` (`ready`, `blocked`, or `null` once complete), `blocked_by` (each with its state), `blocks`, `tags`, `extra`, `claim`, `notes_path`, `ref` |
| Folder | `id`, `name`, `path`, `title`, `extra`, `gates` (each gate's ID and state, or `null`), `open` and `complete` counts under it, `finished`, `notes_path`, `dir`, `ref` |
| Claim | `task`, `claimed_at`, `extra` |

**What a write's result reports**, beyond what it changed:

| Field | Reported by | Holds |
| --- | --- | --- |
| `released` | complete, unblock, delete, folder delete | open tasks that the change made ready |
| `reblocked` | create, block, reopen, a batch | ready tasks that the change made wait again, claimed ones included |
| `gates_made` | any change that writes edges | each gate made: folder, `start` or `done`, its new ID |
| `labels` | batch | each label and the ID it got |
| `removed` | delete | every task and folder removed, by ID |
| `archive`, `json`, `left` | archive | where in the archive it went, its JSON, and every ID that left |

With edges in an indexed table, finding what a change released or blocked again is a lookup on `edge_blocker`, so every write can afford to report it. Today's koan left `reblocked` out for want of that reverse lookup.

**Warnings** sit beside the result and never stop a command; the spec's Warnings lists them.

## Markers

A marker binds a directory outside the tree, usually a code worktree, to a folder, so that `where` and `context` find the folder from anywhere inside it. Markers carry over from today's koan, apart from how they meet archived and deleted folders.

- **The nearest marker is the one.** A path outside the tree is in the folder of the nearest marker at or above it, as with `.git`. The look stops at the first `marker.json`, whatever its state, so an unusable one is never passed over for one further up.
- **Inside the tree, markers are never looked at.** Where a path sits already says which folder it is in.
- **The ID decides; the path is a hint.** A marker follows its folder through moves and renames. A `path` that no longer matches is a `stale-marker` warning, and binding again refreshes it.
- **A marker whose folder is gone binds nothing.** For an archived folder, `where` and `context` say where in the archive it went, from the index; for a deleted one, that it was deleted. Nor does a marker bind anything when it was copied or moved (its `dir` isn't where it is), can't be read as JSON, lacks a field, or is in a format koan doesn't read. Every marker that binds nothing is treated alike: `where` and `context` fail with `not-found`, the message saying why, and the look still stops there. `marker bind` replaces such a marker freely, as it does one for the same folder; replacing one bound to another live folder needs --force (bound).
- **koan keeps no list of markers.** A worktree is removed by git or `rm` without koan knowing, so a list would go stale at once. A folder deleted or archived under a bound worktree shows when the worktree is next used, through `where` and `context`.
- **Reads never write a marker.** Only `marker bind` and `marker unbind` do.
- **Out of git's way.** The `*` in `.koan/.gitignore` matches everything there, itself included, so `git status` never shows it and `git worktree remove` needs no `--force`.
- **Bind worktrees, not checkouts.** A marker binds every directory under it with no nearer one, so `marker bind` refuses a directory whose `.git` is a directory, unless forced (checkout).
- **`.koan/` holds only koan's own files.** `marker bind` and `marker unbind` refuse to touch a `.koan/` that holds anything else (foreign-files).
- **Written whole.** `.gitignore` first, then `marker.json` through a temp file renamed into place. `marker unbind` removes them in the other order, `marker.json`, then `.gitignore`, then `.koan/` itself.
- **Checked in order.** `marker bind` checks a directory under `~/koan-root` (`invalid-input`) first, then `foreign-files`, then `checkout`, then `bound`.

## Operations

Everything here acts on the root as a whole rather than on tasks. Recovery runs in this order: a crash leaves pending steps, which the next write finishes; `doctor` finds what is left; `repair` fixes what loses nothing; a backup restores the rest.

### Install, init, backup and restore

```sh
GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@latest   # Go 1.25 or later
koan init
```

- **One binary, no system libraries.** The SQLite driver is `modernc.org/sqlite`, SQLite translated to Go, so there is no cgo and no C compiler at install time. It adds several MB to the binary and lengthens the first build. **Chosen** over `ncruces/go-sqlite3` (SQLite as WebAssembly), which would also work.
- **`init`** makes `~/koan-root`, its `tree/` and `archived/`, and `koan.db` in WAL mode with the current format. Run again, it changes nothing that is there and reports what it found. init --ids-from N makes the root issue IDs from N; info reports the next ID a root would issue, which is the N to pass when a new root replaces it. Given --ids-from for a root that already has a database, it refuses (`exists`), since that root has issued IDs already. It refuses a root that holds anything else (`not-a-root`), and checks that first: koan's own names there are exactly `koan.db`, `koan.db-wal`, `koan.db-shm`, `koan.db-journal` as regular files, and `tree/` and `archived/` as directories, compared byte for byte and judged without following a symlink, and a `~/koan-root` that isn't a directory is not a root either. After making the root directory, init takes the write lock for the rest, like any write. Under the lock it checks the root and any database already there, its format, whether it is corrupt and `--ids-from`, before it makes anything else, so an init that fails for what it found makes nothing. It makes the database in one transaction, schema, format and `root` row together, so a crash leaves either no database or one with format 0 and no tables at all; init takes that one as unfinished and finishes it, while every other command sees it as `unsupported-format`. To keep the root elsewhere, make `~/koan-root` a symlink first.
- **`backup <dir>`** writes a copy of everything into an empty directory, without holding the write lock while it copies. It takes the lock just long enough for `VACUUM INTO`, copies the tree and the archive as files, then runs doctor on the copy and on the live root. Findings the live root has too, such as a stuck step, are copied faithfully and reported, not fixed. Findings only the copy has mean a write landed mid-copy, at worst a folder moved and missed altogether; backup then copies once more, holding the lock throughout. A writer kept waiting more than 5 seconds by that copy fails with busy, which is safe to retry; it happens only in this rare fallback, and is not a bug. If that copy still has findings of its own, backup reports them and stops. Files are copied, never hard-linked, since an editor that writes in place would change the backup too. Copying `koan.db` by hand while koan runs is not a backup.
- **Restoring** is putting a backup in place of `~/koan-root` and running `koan doctor`. A backup carries the claims of its moment, which a restore brings back with it.
- **Uninstalling** is removing the binary, and `~/koan-root` if its work isn't wanted.

### Doctor and repair

`doctor` compares the tree with the database and checks the database itself; it reads one snapshot, takes no koan lock and changes nothing. `repair` puts the tree back to what the database says, for the kinds it is given. It never changes the database to match the tree, never invents a task or folder from what it finds on disk, and never deletes anything that holds content.

The only outside changes left are hand changes to `@` entries in the tree (a rename or a drag in Zed) and hand edits of the database file. Editing a file's contents is never one.

| Finding | Means | `repair` |
| --- | --- | --- |
| `pending-step` | A change's filesystem steps were cut short | Carries them out, as the next write would. |
| `stuck-step` | A step found both its source and destination holding content, or neither, or a remove found material | — a person keeps one copy, or moves the material out; repair then retries every stuck change, in seq order. The finding is never marked repairable, since a person must act first, but repair retries it all the same, and a change that now succeeds is fixed |
| `stray-directory` | An `@<name>` directory that is no folder at that path (T2), often a folder renamed by hand. An `@.case-<seq>` directory named by a pending step is that step's, and shows as `pending-step` | Moves it back to a folder whose directory is missing, when exactly one stray and one such folder match: they share a parent (a rename in Zed), or every @\<id>.md in it, at least one, belongs to that folder (a drag). Otherwise reports it, and never decides by name alone. Works top-down, so a parent moved back takes its subfolders with it. |
| `missing-directory` | A folder has no directory at its path (T1) | Makes an empty one. Its notes and material are gone, and the finding says so. |
| `case-differs` | A folder's directory is spelled in another case (T1) | Renames it to the folder's name, through a temporary name as a case-only rename is. |
| `misplaced-notes` | A task's notes sit in another folder's directory (T4) | Moves them back, unless notes are already there. |
| `orphan-notes` | An `@<id>.md` with no live task, whether deleted, archived or never issued, or a `@start.md` or `@done.md` with no gate (T4, T6). The finding says which | Removes them when empty; otherwise leaves them. |
| `stray-entry` | Any other `@` entry, such as a hand-made `@Notes.md`, or an `@<id>.md` whose ID is a live folder's (T6). An `@.tmp-<seq>` file named by a pending step is that step's, and shows as `pending-step` | — reported only |
| `invalid-row` | A row breaks an invariant SQL can't check, after a hand edit of the database. Doctor checks I3 (every parent chain reaches the root), I4, I6, I7, I9 (names, tags, titles, timestamps and integers), I11 and I12, and names the invariant broken | — a person fixes it |
| `corrupt` | The database fails `PRAGMA integrity_check` or `foreign_key_check` | — restore from a backup |

Repair runs only when asked: as `repair`, and for its own pending steps, as the first thing a write does. Reads never repair; they report what they trip over beside their results. repair takes the kinds in the order of the table, so a directory renamed by hand is moved back before an empty one could be made in its place.

## Agents

The koan skill is rewritten for the new model, and ships as today's does, through the Claude Code plugin and `scripts/install.sh` for OpenCode.

What the skill teaches, and how a coordinator hands work to workers, are the skill's to say, not this doc's. What koan gives them is in its output: `released`, `reblocked`, `gates_made`, and warnings such as `claimed-blocked` and `moved-claimed`.

**Permissions.** The permission rules let every command run without asking except `init`, `task delete`, `folder delete` and `repair`, which always ask. Archive is safe to run unasked, since it takes only finished work.

## Testing

These are the tests named in the Tested by column of Invariants, enforced and tested. Every rule of the rules doc is something one of them checks.

| Test | What it shows |
| --- | --- |
| **Invariants as properties** | Random sequences of commands against a fresh root. After every command, `doctor` finds nothing, and I1–I12 and T1–T6 hold, checked directly. |
| **A reference model** | A small in-memory model of the rules doc answers readiness, the frontier, `released` and `reblocked` for the same random sequences; koan must agree with it on every answer. |
| **Crash points** | A fault injected before and after the commit, and at each filesystem step. Readers never see part of a change, and the next write, or `repair`, brings the tree back to T1–T6. |
| **Concurrency** | Hundreds of processes writing and reading one root at once. No claim is ever handed out twice, every read sees a committed state, and `busy` appears only after 5 seconds of waiting. |
| **Case** | The Case section's rules as table tests, run in CI on Linux and on macOS with its default case-insensitive filesystem. On both they include a case-only folder move and the case-differs repair, with a crash at each step. |
| **Output** | Every command's JSON checked against its schema, and golden results for each refusal. |
| **Performance** | Benchmarks at 1,000 and 10,000 live tasks. CI fails on the Linux runner when a command passes the budget set in Goals. macOS runs the same benchmarks and reports them, but doesn't fail the build until its numbers have been measured (docs/spikes.md). |
| **Repair** | Each finding made by hand on a fresh root. doctor reports it under the right kind, repair does what the table says, and a second doctor reports only what repair leaves to a person. |
| **Markers** | The nearest marker wins and stops the look; a copied or moved marker binds nothing; archived and deleted folders give their messages; git ignores .koan/; checkout and foreign-files refuse. |
| **Backup and restore** | A backup taken during the concurrency run restores to a root doctor finds clean; a folder moved mid-copy makes backup copy again under the lock. |

`go test -short` skips the long concurrency and crash matrices, as today.

## Deferred and choices

Anything that would change a rule goes to the rules doc, not to this list.

**Deferred:** decided on, not needed to start.

- **A `join` shorthand.** `--join <folder>` on `task create`, and a `join` field in `batch`, writing the two edges of joining. Joining a folder whose done gate is already complete needs nothing new: the edge into the complete gate warns `complete-blocker`, as any such edge does.
- **Unarchive.** Bringing an archived item back, with its IDs. For now, work needed again is made again.
- **Shell completion.** Paths folder by folder, IDs with their titles, gates as `.start` and `.done`.
- **`--no-gates` on `task list`**, if gates in the list turn out to be noise.

**Choices** this doc makes where the rules doc leaves room. Each can change without touching a rule.

| Choice | Instead of |
| --- | --- |
| Everything under `~/koan-root` | a path set by configuration |
| The database in the root | `~/.local/share/koan` |
| `modernc.org/sqlite` | `ncruces/go-sqlite3` |
| A `flock` on the root as the write lock | SQLite's write lock alone, which ends too early |
| A 5-second wait before `busy` | another bound |
| `synchronous = FULL`, with `fullfsync = ON` and `checkpoint_fullfsync = ON` on macOS, whatever the cost there turns out to be | `NORMAL`, which can lose the last commits on power loss |
| Every case-only rename through a temporary name | one `rename`, which a Linux directory that ignores case turns into a silent no-op |
| `task list`, `task frontier` and `folder list` return 100 results unless `--limit` says otherwise | everything, 3 MB of JSON at 10,000 tasks for an agent to take in |
| The archive laid out by path, items named by ID | an archive laid out by ID only |
| An empty notes file written at task create | notes made on first edit |
| 100 ms at 10,000 live tasks; 1 s for `doctor` and `repair`; 100 ms plus 1 ms a part for `batch`; none for `backup` | another budget |
| A stuck step set aside, later writes going on | every write failing until a person fixes it |
| A backup copied without the lock, then checked by doctor | the lock held throughout, or hard links |
| No list of markers in the database | a list, so delete and archive could warn about bound worktrees |
| Directories fsynced before a step's row is deleted | leaving a rename lost to a power cut for doctor to find |
| Archived fields as JSON beside each archived item | a table in koan.db, lost at every format change before 1.0; JSON inside the folder, where a stray file can be in the way |
| init --ids-from N, carrying the ID sequence from root to root | IDs from 1 in every root, so each format change before 1.0 makes a second @42 |
