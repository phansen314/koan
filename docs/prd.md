# koan rewrite: PRD

Oct 20, 2026 · @SuperUser

This doc fixes how the rewrite builds koan: its layout, storage, writes and concurrency, and its protocol of commands, output and errors. What koan must guarantee is in the rules doc, koan: invariants and rules. The rules doc is normative: where the two disagree, it wins, and anything that would change a rule goes there, not here.

The command specification comes after this doc, as JSON Schemas and a command reference in the repo. It fixes every command's flags and every JSON field, the archive's included; this doc fixes the shape they take. Choices this doc makes that the rules doc leaves open are listed in Choices.

## Goals and non-goals

koan is task management for one person on one machine, driven mostly by agents such as Claude Code. Tasks and their structure live in a database; their notes are plain files in a directory tree that people and agents open and edit with any tool, Zed included.

**Goals**

- **Notes are files, edited directly.** No tool stands between an agent and a task's notes. Everything in the tree's directory may be edited; structure changes only through koan.
- **Structure is safe under many agents at once.** Any number of agents and the user work on one tree concurrently, and every invariant holds.
- **Fast at the sizes one person reaches.** Every command finishes within 100 ms at 10,000 live tasks and 100,000 events in the log on the CI machine, whose OS is named beside the benchmarks; archiving keeps the live set well below that, and `log rotate` the log. Commands whose work grows with the whole tree or their input are budgeted apart: `doctor` and `repair` within 1 s, `export` within 200 ms, a `batch` within 100 ms plus 1 ms a part, `log list --limit null` and `log rotate` within 1 s at 100,000 events, and `backup` not at all, since it copies every file.
- **Built for agents.** Every command prints one line of JSON, every failure has a kind, and every write says what it changed and records it in the log. Help, shell completion and `pick`, which are for people, print plain text (spec Invocation).
- **Simple to install.** One `go install`, one binary, no system libraries. `pick` alone needs fzf on `PATH` (Command surface).

**Non-goals**

- **Git.** koan neither uses nor supports git on its tree: no branches, worktrees or merges of the tree, and no history of its files. Archiving is how finished work leaves; `koan backup` is how the whole of it is kept; the log (Storage) records what each change did.
- **More than one machine or one tree.** No sync, no shared trees, no second tree per user.
- **Migrating today's trees.** The rewrite starts fresh with `koan init`.
- **Windows.** Linux and macOS only. The root may be on any local filesystem of theirs, whether it ignores case or not.

## What changes from today

**Why rewrite.** Today every task and folder is a JSON file in the tree, beside its notes, and anything may edit either. Most of today's code copes with that: walking the tree for every lookup, multi-file changes a crash can half finish, unusable files, duplicate IDs, dangling references, and a `doctor` for all of it. Agents also see a `.json` they must never touch beside every `.md` they may, and get it wrong. Folders double as ordering, so a move can change readiness, and an ordering group must be a subtree.

| Today | In the rewrite |
| --- | --- |
| Tasks and folders are JSON files in the tree | Rows in `koan.db`; the tree holds only notes and material |
| `~/koan-root` is the tree | `~/koan-root` holds `koan.db`, `tree/`, `archived/` and `log/` |
| Nothing records what happened: `updated_at` holds the last change, and completing again overwrites `completed_at` | The log: an event for everything every change did, in `koan.db`, read with `log list` |
| `@folder.md`, `@koan.json`, `@<id>.json` | `@notes.md`; no other koan file in the tree |
| A folder's `blocked_by`, inherited by everything in it; done when its contents are | Folders are containment only. Ordering is edges between tasks, and a folder orders work through its two gate tasks, `@F.start` and `@F.done`, made when first named |
| `folder block`, `folder unblock`, `folder why` | Gone: a folder is ordered through its gates, with `block` and `why` |
| `task done`, “done” | `complete`, “complete”; the done gate keeps its name |
| Writes report `dependents` on delete only | Writes report what they released and blocked again, and the gates they made: `released`, `reblocked` and `gates_made` |
| Git as the undo, with rules against branching | No git. Archive for finished work, `koan backup` for everything |
| `doctor` tolerates damaged files of every kind | The database can't hold most of them; `doctor` checks the tree against it |
| Claims in `~/.config/koan/state.json` | Claims in `koan.db` |

**Unchanged:** references and kept text, the case rules, claims, the archive's paths and its one-way rule, markers (apart from how they meet archived and deleted folders), `batch` with labels, one line of JSON per command, Linux and macOS.

**No migration.** Today's trees aren't converted. Today's \~/koan-root is a tree, which init refuses as not-a-root, so move it aside first, and pass init --ids-from past its highest ID so today's references stay unique. The rewrite starts with `koan init`.

## Layout

Everything koan keeps is under one directory, `~/koan-root`. The database is the truth about structure, and holds the log; the tree is where notes and material live; the archive sits beside the tree, and so do the log's rotated files.

&#91;embedded content: koan.db, tree/, archived/ and log/ · one row per task or folder, one directory per folder\]

Each folder row has a directory at its path, and each task row its notes in its folder's directory; a gate's notes are `@start.md` or `@done.md`. No file in the tree holds fields.

| What | Path | Written by |
| --- | --- | --- |
| **Database:** tasks, folders, edges, tags, claims, the ID counter, the archive index, pending filesystem steps, the log | `~/koan-root/koan.db`, with its `-wal` and `-shm` files, and the `-journal` SQLite writes while init creates it | koan only |
| **Tree:** a directory per folder, folder notes, task and gate notes, material | `~/koan-root/tree/` | koan for `@` entries; anyone for file contents |
| **Archive:** what was archived, as files | `~/koan-root/archived/` | koan only |
| **Rotated log:** events moved out of the database by `log rotate`, as JSON Lines files | `~/koan-root/log/` | koan only |
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
- **The fields go beside it as JSON**, since the tree holds none: `@41_HOME-12345.json` beside the folder `@41_HOME-12345/`, `@42.json` beside a task's `@42.md`, `@58_start.json` beside a lone gate's `@58_start.md`. What each holds, and how its format changes, is the spec's, under On-disk formats. Only koan writes `archived/`, and every name there carries an ID, so nothing is ever in their way.
- **The directories above an item** are named after the folders it was in, without the `@`. They are not folders, items under one path share them, and nothing in them is overwritten, since every item carries its ID. Where such a directory already exists differing only in case, it is used, and the index records the name on disk, so archiving never refuses for case. The step that archives an item uses such a directory as it finds it, and never makes a second spelling beside it. The spelling the index records is found when the change is made, by listing each directory above the item and counting, beside what is on disk, the directories that pending archive steps, stuck or not, will make above their own items, matched ignoring case: a spelling on disk is used first, then the earliest pending step's. So it is still the one on disk when the step runs, whether an earlier step made it or this one does: only koan writes `archived/`, and steps that make a directory above an item all make it in the one spelling.

### Markers on disk

A marker is a `.koan/` directory in the directory it binds, outside the tree. How markers behave is under Markers.

```text
<dir>/.koan/.gitignore     *: git ignores the directory, the .gitignore included
<dir>/.koan/marker.json    the folder it is bound to
```

Its fields are the spec's, under On-disk formats → marker.json.

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

-- The log: an event for everything every change did, oldest first (rules The log).
-- AUTOINCREMENT, so a seq is never issued again, even once log rotate has moved every row out.
-- No foreign keys: an event outlives the task or folder it names.
CREATE TABLE event (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  change      INTEGER NOT NULL,                  -- the seq of its change's first event
  at          TEXT NOT NULL,
  command     TEXT NOT NULL,
  kind        TEXT NOT NULL,
  id          INTEGER,                           -- the task or folder; NULL for the root or the log
  folder_id   INTEGER,                           -- where it happened, then; NULL is the root
  folder_path TEXT NOT NULL,
  data        TEXT NOT NULL CHECK (json_type(data) = 'object')
);
CREATE INDEX event_at     ON event(at);
CREATE INDEX event_id     ON event(id);
CREATE INDEX event_folder ON event(folder_id);
CREATE INDEX event_path   ON event(folder_path);
CREATE TRIGGER event_fixed BEFORE UPDATE ON event
  BEGIN SELECT RAISE(ABORT, 'events are never changed'); END;
```

- **Format.** `PRAGMA user_version` holds the database's format. The first format was 1; this one, which adds the log, is 2. A binary reads exactly one; any other is `unsupported-format`. Before 1.0 a new format means a fresh `init`, with no conversion. The archive and the log's rotated files need none, since each names its own format and is read with any tool. A fresh root issues IDs from 1, unless init is given --ids-from N. Starting past the old root's highest ID keeps every ID unique across every root you have had, so old notes and archives never meet a second @42. An old archived/ can then be moved into the new root, though its IDs aren't in the new index.
- **Timestamps** are stored as their text form, `YYYY-MM-DDTHH:MM:SSZ`, so they sort as text.
- **The log's indexes** serve `log list`'s filters (spec log list): the `seq` rowid serves `--after`, the order and `--limit`; `event_at` `--since`; `event_id` `--task`; `event_folder` a folder given by ID; and `event_path` a folder given by path, as a range over the index's `BINARY` order, `folder_path = P OR (folder_path >= P || '/' AND folder_path < P || '0')`, since `/` and `0` are adjacent bytes, and none for `/`, which every path is under: byte for byte, as names are compared, never `LIKE`, which ignores case. `kind` has no index: there are few kinds, and only the rarest would gain, so `--kinds` filters what the others find.
- **`folder.parent` has no cascade on purpose.** koan removes a folder's subtree itself, so it can check for material first and report what it removed.

## Writes

A write commits the database first and changes the tree after, from a list of filesystem steps committed with it. A crash at any point leaves either nothing done, or steps that the next writer finishes.

**The steps of a write**

1. Check the input's shape: fields, patterns, references as written. Nothing is read yet.
2. Take the lock, and finish any filesystem steps an earlier write left pending, skipping stuck ones.
3. `BEGIN IMMEDIATE`. Resolve every reference against the database as it stands.
4. Check the tree for what the change needs: a destination path is free, a folder to delete holds no material unless `material`. Every destination is checked on disk as well, the missing parents the rules doc counts as destinations included (Changes, A path is taken): listing its parent directory, an entry by that name, or one differing from it only in case, refuses the change as `exists`, with that entry's path.
5. Change the rows, check for cycles, insert into `pending` the filesystem steps that bring the tree in line, and insert the change's events into `event` (Events, below).
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
| Remove notes or a directory | the path is gone | `unlink` for notes. A directory remove whose change gave `material` is a recursive remove that can be run again. Unless its change gave `material`, a directory remove first looks for material, counted as a delete counts it (rules doc, Changes), as step 4 does, and finding any it removes nothing and is stuck (below). Since the rows may be gone, the step records what it may remove: the directories of the folders being deleted, and the notes of what is being deleted, as the rules doc's Changes names them. It walks the directory and every `@`-named directory under it. Any entry not named with `@` is material, as in the tree. An `@` entry the step doesn't record is material when it holds content: a file of one byte or more, or a directory holding such a file or any entry not named with `@`. A zero-byte file and `.DS_Store` never are. Finding none, it never removes recursively. It works bottom-up: in each directory it unlinks only the entries it listed there that aren't material, never a name without `@` other than `.DS_Store`, then removes the directory with `rmdir`, which the operating system refuses while it isn't empty. So material written while the step runs, by Zed, a person or an agent, is never removed: the step fails and stays pending, and on the next write its walk finds the material and it is stuck. The step records whether its change gave `material`. |
| Archive | it sits in the archive | making the directories above it, writing its JSON through a temp file renamed into place beside where it goes, then one rename out of the tree |

**Done when is judged by listing the parent directory** and comparing names byte for byte, never by `stat`. On a filesystem that ignores case, `@work` and `@Work` stat as one entry, so a case-only rename would find both its source and its destination.

**Names that differ only in case.** A step lists each directory on its paths, from `tree/` or `archived/` down, not only the last one. Every name it uses counts, each needing to be absent or spelled exactly: its source, its destination, its temporary name, its temp file, a folder's `@notes.md`, and each directory above them. A name in the listing that equals one of them only ignoring case fails the step on every filesystem, so a remove of `@old` beside a hand-respelled `@Old` waits as pending rather than being judged done. The step stays pending with `pending-step`, as when the operating system refuses. Without this check, the filesystem would decide: one that ignores case opens `@Work` for `@work`, or refuses to make `@api` beside `@Api`, while one that tells case apart does the opposite. Doctor reports the other spelling as `case-differs` or `stray-directory`. Once repair or a person puts it right, the next write finishes the step. Two exceptions: a case-only rename's own source, temporary name and destination, and a directory above an archived item, which is used in whatever case it is there, as The archive says.

**A case-only rename** goes through a temporary name on every filesystem, since one that ignores case returns success from `rename("@work", "@Work")` and leaves the old spelling in place. The step renames `@work` to `@.case-<seq>`, with its own `seq`, then `@.case-<seq>` to `@Work`. It records all three names, so a step cut short resumes from whichever of them the listing shows. It is done when the listing shows the new spelling, byte for byte, and neither of the others.

**A temp file** for notes or archive JSON is `@.tmp-<seq>`, with its step's own `seq`, in the directory the file goes into. A step cut short writes it afresh, and once the step is done, one left behind is removed.

**A move that finds both** its source and its destination finishes when one side is empty: a zero-byte notes file, or a directory holding nothing but zero-byte `@` notes files, named exactly as koan names them, and any `.DS_Store`, which is disposable and goes with it. Any other file is content, however small, since material is full of meaningful empty files such as `__init__.py` and `.gitkeep`. An empty source is removed, an empty destination replaced. The usual case is an agent writing notes at a task's new path before the move ran, while the old file is still the empty one create wrote.

**A stuck step** finds both sides holding content, or neither when a directory is needed; a move of notes that finds neither is done, since missing notes read as empty (T3). A directory remove whose change didn't give `material` is stuck when it finds material: what was checked at step 4 can change before the step runs, in a crash or a failed step, and koan removes material only when told to. Its row is marked `stuck`, with the rest of its change, since those may depend on it, and records what it found: `both`, `neither` or `material`. Only the step that found it is reported, by `stuck-step` and by doctor; the rest of its change is set aside with it. Later writes go on and skip stuck rows. A later change whose steps overlap a stuck step waits behind it (below), so it can become stuck only because an earlier one is, and not by working on paths the stuck step still holds. Every result carries `stuck-step` until a person acts: keeps one copy, or moves the material out; `repair` then retries every stuck change in `seq` order, and clears each that now succeeds. A step holds everything it needs, notes content and archived rows included, since the rows it came from may already be gone.

**Steps on the same paths run in order.** Two steps overlap when a path one touches, its source, destination, temporary name or archive JSON, is the same as a path the other touches, or above or below it. These paths are compared ignoring case, so the order is the same on every filesystem. A step waits while any step of an earlier change that overlaps it is still pending, whether stuck or not. A step that waits is held with the rest of its change, as a failed step's change is, and gives `pending-step`. It runs once the earlier step is done. That is why a step's paths, fixed when its change commits, are still right when it runs: whatever an earlier change was still doing on them has finished. For example, a task created in a folder whose directory a failed step hasn't made yet gets its notes only after the directory is there. A deleted folder's directory still on disk, its remove refused, is different: it takes its path (rules doc, Changes), so a folder created there is refused with `exists`.

**Notes files.** Creating a task writes its `@<id>.md`, and creating a folder its `@notes.md`, empty unless the input gives notes, so that it shows in Zed straight away. A folder's notes come with its directory, from the one step that makes both; a missing parent's are empty. A missing notes file still reads as empty notes (T3).

**Issuing IDs in a change.** A change issues IDs in the order it makes things: its folders, parents first, in tree order; then its tasks, in input order; then the gates its edges name: first the task `block` is given, when that is a gate the folder lacks, then the rest in the order those edges are given, each task's `blocked_by` before its `blocks`, then the edges of the folders it joins. A folder that joins names its own start gate, then its done gate, as `block`'s subject comes first; then each folder joined, in the order given, names its start gate, then its done gate. So `folder create /s/impl --join /s`, on a root without `/s`, makes `/s`, `/s/impl`, `/s/impl.start`, `/s/impl.done`, `/s.start` and `/s.done`, in that order. A change's edges resolve after its folders and tasks are made, so an edge naming the ID a task in the same change gets names that task, and a join can name a folder the same change makes, a missing parent included: given to that task or folder itself, it is an edge to itself, refused as `invalid-input`. A batch follows the same order, below: the gates its folder parts' joins name, in input order, before those its task parts' edges name. No ID past `999999999999999` is ever issued: a change that would need one is refused as `no-ids`, before anything is written, and `issued`'s `CHECK` is only the backstop. A root that has issued its last ID can still be read, and changed in every way that makes nothing.

**Making a gate.** When an edge names a gate the folder lacks, the same transaction issues it an ID, inserts its row with the default fields (`Start` or `Done`, `null` priority, no tags, empty extra), and adds a step writing an empty `@start.md` or `@done.md`. The result lists the gates it made. A @start.md or @done.md already there, made by hand, becomes the gate's notes, since the step is done once the file exists.

**Removing rows.** Before a task row is deleted, the transaction looks up the tasks that wait on it through `edge_blocker`, gives each a new `updated_at`, and reports in `released` those it leaves ready. `ON DELETE CASCADE` then removes the edges; it is a backstop, never the only record that an edge went. Delete and archive both work this way.

**Resolving a batch.** A batch is one transaction, and is read as the tree it would leave:

1. Check every part's shape, and that no label is defined twice.
2. Make the batch's folders, parents first, in tree order.
3. Make its tasks: resolve the batch's `folder` against the tree as it now stands, whether or not a task takes it, then make each task, resolving its own folder the same way, issuing IDs in input order and binding each label to its task's ID.
4. Resolve the batch's `join` against the tree as it now stands, whether or not a task takes it. Then resolve every edge's references the same way, labels included, joins included, the folder parts' first, and write every edge, making any gate named. A label no part defines is found here, as `invalid-input` pointing at the edge, since step 1 checks only that no label is defined twice.
5. Check for cycles over every edge the batch added.

Any refusal refuses the whole batch, and nothing is written.

**Archiving.** One transaction removes the rows of everything that leaves, and every edge between it and what stays; sets each leaving ID's `issued.state` to `archived` and its `archive` to where that ID itself sits in the archive: an archived folder's `…/@41_HOME-12345`, a folder under it `…/@41_HOME-12345/@api`, a task under it `…/@41_HOME-12345/@api/@42.md`, a gate `…/@41_HOME-12345/@start.md`, a task or gate archived alone its own `@42.md` or `@58_start.md`, or, when its notes file wasn't there, its JSON, `@42.json` or `@58_start.json`, so a lone item's entry always names a file that exists; and adds the archive step. A directory is written without a trailing `/`. Whether a lone item's notes file was there is judged by listing its folder's directory when the change is made, spelled exactly, whatever pending steps will still do: a notes file a pending step makes before the archive step runs still goes into the archive beside the JSON, but the index names the JSON, and the result's `notes_path` is null. Counting what pending steps will make instead could name a file that never comes, such as notes a pending move finds neither side of. Everything under an archived folder is indexed where it sits in the folder's directory, whether or not its notes or its directory are on disk; only a lone item falls back to its JSON. The step carries the removed rows. It makes the directories above the item in the archive, writes the rows as JSON to a temp file renamed into place beside where the item will go, then renames the item out of the tree. A task whose notes file is missing is archived as its JSON alone, since a missing file reads as empty notes.

**Events.** Step 5 inserts the change's events (rules The log) in the change's own transaction, so they commit with it or not at all (I10, I13), and a reader sees events and rows from one snapshot. Each takes:

- **`at`:** the change's time, the one it writes into `updated_at` and `completed_at`, the same for every event of the change.
- **`change`:** the `seq` SQLite issues the change's first event, which the rest repeat. A change's events are contiguous, since writes are serialized.
- **`command`:** the command that made the change, as its name is typed, such as `complete` or `task create`.
- **`folder_id` and `folder_path`:** for an event about a folder, that folder; about a task, the task's folder; about the root or the log, the root. Each is taken as the change found it, before anything moved, or for what the change makes, as it made it. So a folder's `moved` stands at the path it left, and its data gives the path it went to.

Derived state is compared, never worked out step by step. `released` and `reblocked` are the change's own result lists, found as they always are. `finished` comes from whether any open task is under each folder above what the change touches, the folders it moves from and to included, asked before the change alters anything and again after it, in the same transaction: a folder with open work before and none after is `finished`, and one with none before and some after is `unfinished`. Each question stops at the first open task it finds, and only those folders are asked, never the whole tree.

A change records its events in this order:

1. `created` for everything it makes, in the order it issues their IDs (Issuing IDs in a change).
2. `blocked` and `unblocked`, one for each edge it writes or removes, in the order its edges are given, as Issuing IDs in a change orders them: an edge given twice records once, at its first, and a join's edge to the start gate comes before its edge from the done gate. The edges a delete or archive removes between what goes and what stays come by task, then blocker.
3. Each subject's own events, in input order: `updated`; `completed`, then its `unclaimed`; `reopened`; `claimed`; `unclaimed`; `moved`.
4. A delete's `unclaimed` events, then its `deleted` events, each in `removed`'s order; an archive's `archived` events, in `removed`'s order.
5. `released` and `reblocked`, in the result's order.
6. `finished` and `unfinished`, from the deepest folder up, at the depth the change found it; folders at one depth by ID.

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
- **`log rotate` holds the lock throughout**, from listing `log/` to the commit that deletes the rows, so the events it writes to the file are the ones it deletes. A reader meanwhile sees every event in the table until that commit, and the `rotated` event after it: never a gap, and never an event twice.
- **`backup` holds the lock at most twice:** for `VACUUM INTO` and the copy of `log/`, and for the second copy when it makes one (Install, init, backup and restore).
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
| I13 the log is true | each change's events inserted in its own transaction (Writes, Events) | properties; crash points |
| I14 events fixed | `event`'s `AUTOINCREMENT`, which keeps the largest `seq` once its rows are gone; the `event_fixed` trigger refusing an update; rows deleted only by `log rotate`, once they are in a file | properties; crash points; concurrency |
| T1–T6 the files | the filesystem steps of each change; `doctor` checks them | crash points; properties |

## Command surface

The spec fixes the surface: every command's input, result, warnings and refusals (spec Commands), the envelope and exit codes (spec Invocation, Envelope), the records reads return (spec Records), and every warning and error (spec Warnings, Errors). Its shape follows from Goals: one line of JSON per command, a kind for every failure, and a result that names by ID what every write changed.

**The command line is cobra's.** koan reads its command line with cobra and pflag rather than a parser of its own, so help and shell completion come from the library. Where cobra's rules and a stricter one of koan's would differ, cobra's win, and the spec says so; only the envelope, the exit codes and the standard-error line stay koan's, since agents depend on them.

**`pick` is for people.** `koan pick` lets a person at a terminal choose tasks, gates and folders by what they are about, and prints their references, for `$(koan pick)` or for a key, bound in bash or zsh, that types them in at the cursor. Agents never need it: they have `task list`, `frontier` and `where`. So it takes the exceptions people need, and nothing that could hold up an agent: it prints plain text rather than an envelope, draws on `/dev/tty`, and fails at once when there is no terminal.

- **fzf draws the picker.** koan runs fzf as an outside program, writes its candidates to fzf's standard input, and reads the choice back from fzf's standard output. fzf draws on `/dev/tty` itself, so standard output carries only the choice. fzf is the one runtime dependency koan has, and only `pick` needs it. It adds no Go dependency, it gives fuzzy matching, marking several and a preview pane for free, and the same build is already proven in the user's own fzf pickers.
- **The candidates are koan's own reads.** pick runs `task list`, `frontier` and `folder list` in-process, with the same filters, and turns the records into lines. It adds no query, and completing references draws on the same candidates.
- **koan binds no key.** `pick --bash` and `pick --zsh` define a widget that runs pick and types what it prints at the cursor, and the person binds it to a key in their own startup file. No key suits everyone: Alt-K, escape then `k`, is also Esc then `k` typed quickly in vi insert mode, which recalls the previous line, and on macOS a terminal sends it for Option-K only when set up to. Which key is free depends on the terminal, the keyboard and the editing mode, which only the person knows, so `docs/shell.md` gives recipes instead.
- **Previews need no call back into koan.** Before fzf starts, pick writes a short header per candidate into a private temporary directory, and fzf's preview shows that header and the notes file. The directory is removed as soon as fzf returns.

**Completing references is for people too.** cobra's completion scripts call koan back for each Tab, and koan answers with the references the argument takes: paths folder by folder, IDs with their titles, and gates after a path (spec Completing references). Completion is for when you know the name; `pick` is for when you know only what it's about. Only references complete: fixed choices such as `--state`, and tags, don't.

- **The candidates are pick's.** Completion chooses them as pick does, with pick's filters and in pick's order, and renders each as a reference and a short description. It builds no records with edges and no previews, so a Tab costs one read of the tree: about 27 ms at 10,000 tasks, against about 80 ms for full records. It is a command like any other, held to the 100 ms budget.
- **koan filters, the shell only shows.** koan keeps only the candidates that start with what is typed, since cobra's bash script spends about 74 µs on each described candidate it's given: 0.74 s for 10,000. Past 100 it offers none, and a one-line hint says to type more or use `pick`, where bash would otherwise ask whether to display them all.
- **Agents never complete.** Completion runs only from a person's interactive shell, and prints nothing an agent reads.

**`export` is for tools.** `koan export` prints a folder's whole subtree as one result, for a tool to turn into a graph or a file: its folders, every task under it, the tasks outside it at the other end of an edge, and every edge with an end in it. It gives what the other reads can't, and is budgeted for it:

- **One snapshot.** Its records come from one read transaction, so no write lands between them, as one can between `show`, `folder list` and `task list`.
- **No limit.** It is always the whole subtree, every state, gates included, with nothing to narrow it: filtering is jq's job. A graph needs every task, which is just what the 100-result default of `task list` and `folder list` keeps out of an agent's context (Choices), so agents pipe export into a tool or a file, and never read it whole.
- **The other ends of edges.** A record's `blocked_by` and `blocks` name a task outside the subtree only as an edge-end, its ID, kept text and state. export gives each such task as a whole record, so a renderer needs no `show` per task.
- **Nothing new to read.** Its folders and tasks are the records every read returns, and its edges take Archive JSON's shape and order, so it adds no record and no shared type. It prints JSON only: Graphviz, Mermaid and D2 are drawn from it by tools outside koan.
- **Budgeted apart.** Its work grows with the subtree, and `export /` prints every live task as a whole record, about 7 MB at 10,000 tasks. The spikes never printed every task. The nearest measurement, `list` filling every field for all 10,000 tasks while printing the 4,223 open ones (3.0 MB), took 66.5 ms, and encoding costs about 5 ms per 1.5 MB, so `export /` comes to an estimated 85–90 ms on the spike machine (docs/spikes.md). 200 ms (Goals) gives it the margin the other whole-tree reads have under 100 ms. It is an estimate, which the Performance benchmarks check.

**The log is for catching up and looking back.** Every change records what it did (rules The log); `koan log list` reads the events back, oldest first, and `koan log rotate` moves the oldest out to files (Rotating the log). Both sit under `log`: the log is neither a task nor a folder, and no reference names it, which is where the command line keeps a noun (spec Invocation).

- **`--after` is the cursor.** Every result carries `last_seq`, the highest `seq` it returned, and an agent that keeps it asks for `--after` it next time: no clock, and nothing seen twice. Nothing is missed either, unless `log rotate` moved it first: `log list` reads only the table, so the full history since a `seq` below the table's oldest is the files in `log/` that cover it, then the table. The `rotated` event comes after every event the table kept, and a filter can leave it out, so an agent back after a long gap asks once `--after` its `last_seq` with no filter and `--limit 1`: since `seq` has no gap in the table, a first `seq` above `last_seq` + 1 means rotation moved what lies between, which is in the files of `log/`. It then asks its filtered question as usual. Cutting well in the past, such as a month back, keeps every cursor still in use out of reach. A result cut short by `--limit` is followed by asking again from its `last_seq`. `--since` is for people, who think in times.
- **Matched as written.** Its folder and `--task` are compared with what each event stored, never resolved against the tree, so a path that no longer exists, and an ID since deleted or archived, still find their history. A path matches itself and every path under it, byte for byte; an ID matches that folder's own events and its tasks', through renames and moves.
- **The table only.** It reads only the database, in one snapshot, and never `log/`: koan never reads a rotated file back. Each `rotated` event says which file holds the events it moved, and the files' names give their ranges.
- **A restore shows.** `--after` a `seq` the root never issued is `not-found` (spec log list), so an agent whose saved `last_seq` comes from before a restore, or from a root since replaced, learns it. That holds only until the restored root has issued that many events again: after that, the saved `seq` names an event again, and `--after` it quietly skips the new events below it. A restore is the user's act, and the user tells their agents.
- **Reads never log.** No read records an event, `log list` included: a read that logged would be a write, and wait on the lock.

**Every write reports what it released and blocked again.** With edges in an indexed table, finding what a change released or blocked again is a lookup on `edge_blocker`, so every write can afford to report it. Today's koan left `reblocked` out for want of that reverse lookup.

## Markers

A marker binds a directory outside the tree, usually a code worktree, to a folder, so that `where`, `context` and `export` find the folder from anywhere inside it. Markers carry over from today's koan, apart from how they meet archived and deleted folders.

- **The nearest marker is the one.** A path outside the tree is in the folder of the nearest marker at or above it, as with `.git`. The look walks up the path as given, listing each directory for `.koan/marker.json`, and stops at the first `marker.json`, whatever its state, so an unusable one is never passed over for one further up.
- **Inside the tree, markers are never looked at.** Where a path sits already says which folder it is in.
- **The ID decides; the path is a hint.** A marker follows its folder through moves and renames. A `path` that no longer matches is a `stale-marker` warning, and binding again refreshes it.
- **A marker whose folder is gone binds nothing.** For an archived folder, `where`, `context` and `export` say where in the archive it went, from the index; for a deleted one, that it was deleted. Nor does a marker bind anything when it was copied or moved (its `dir` isn't the real path of the directory it was found in), can't be read as JSON, lacks a field, or is in a format koan doesn't read. Every marker that binds nothing is treated alike: `where`, `context` and `export` fail with `not-found`, the message saying why, and the look still stops there.
- **koan keeps no list of markers.** A worktree is removed by git or `rm` without koan knowing, so a list would go stale at once. A folder deleted or archived under a bound worktree shows when the worktree is next used, through `where`, `context` and `export`.
- **Reads never write a marker.** Only `bind` and `unbind` do.
- **Out of git's way.** The `*` in `.koan/.gitignore` matches everything there, itself included, so `git status` never shows it and `git worktree remove` needs no `--force`.
- **Bind worktrees, not checkouts.** A marker binds every directory under it with no nearer one, so `bind` refuses a directory whose `.git` is a directory, unless forced (checkout).
- **`.koan/` holds only koan's own files.** `bind` and `unbind` refuse to touch a `.koan/` that holds anything else (foreign-files).
- **Written whole.** `.gitignore` first, then `marker.json` through the temp file `.koan/marker.json.tmp`, renamed into place. That temp file is koan's own too, so one left by a crash is never foreign: `bind` writes over it. `unbind` removes them in the other order: the temp file, `marker.json`, then `.gitignore`, then `.koan/` itself.
- **No lock.** `bind` and `unbind` write no row and nothing in the tree, so they take no lock and carry out no pending steps: they read the database in one read transaction, as a read does. Two run at once leave the marker written last, whole.
- **Paths as given.** Paths outside the root are printed as given, made absolute. Only `marker.json`'s `dir` is a real path, so that a marker is judged by where it is, whatever route reached it.
- **Real paths are spelled as on disk.** A real path follows every symlink and spells each name as the listing of the directory above it does. Below `~/koan-root` a name is found byte for byte, so a wrongly-cased path in the tree is never taken for the entry. Above it and outside it, the root's own name included, a name not found byte for byte is taken in the listing's spelling when an entry matches it ignoring case and the filesystem says the two are the same file, so on a filesystem that ignores case `~/Code/wt` and `~/code/wt` have one real path.

## Operations

Everything here acts on the root as a whole rather than on tasks. Recovery runs in this order: a crash leaves pending steps, which the next write finishes; `doctor` finds what is left; `repair` fixes what loses nothing; a backup restores the rest.

### Install, init, backup and restore

```sh
GOBIN=~/.local/bin go install github.com/phansen314/koan/cmd/koan@latest   # Go 1.25 or later
koan init
```

- **One binary, no system libraries.** The SQLite driver is `modernc.org/sqlite`, SQLite translated to Go, so there is no cgo and no C compiler at install time. It adds several MB to the binary and lengthens the first build. **Chosen** over `ncruces/go-sqlite3` (SQLite as WebAssembly), which would also work.
- **`init`** makes `~/koan-root`, its `tree/`, `archived/` and `log/`, and `koan.db` in WAL mode with the current format; what it accepts, refuses and reports is the spec's, under Operations → init. After making the root directory, init takes the write lock for the rest, like any write, and under the lock checks everything it found before it makes anything else. It makes the database in one transaction, schema, format, `root` row and the `initialized` event together, so a crash leaves either no database or one with format 0 and no tables at all; init takes that one as unfinished and finishes it, while every other command sees it as `unsupported-format`. `initialized` is the log's first event, `seq` 1. A run that finds the database made records nothing, even when it makes a directory that was missing: it only puts the root's own layout back.
- **`backup <dir>`** writes a copy of everything into an empty directory, or a new one, made with any parents it lacks, without holding the write lock while it copies. It takes the lock just long enough for `VACUUM INTO` and for copying `log/`, so the copy's table and rotated files agree, since `log rotate` takes the lock too; then it copies the tree and the archive as files, then runs doctor on the copy and on the live root. Findings the live root has too, such as a stuck step, are copied faithfully and reported, not fixed. Findings only the copy has mean a write landed mid-copy, at worst a folder moved and missed altogether; backup then copies once more, holding the lock throughout. A writer kept waiting more than 5 seconds by that copy fails with busy, which is safe to retry; it happens only in this rare fallback, and is not a bug. If that copy still has findings of its own, backup reports them and stops. Files are copied, never hard-linked, since an editor that writes in place would change the backup too. Copying `koan.db` by hand while koan runs is not a backup. backup records no event: it reads the root and writes elsewhere, so as far as the log goes it is a read (rules The log).
- **Restoring** is putting a backup in place of `~/koan-root` and running `koan doctor`. A backup carries the claims and the log of its moment, which a restore brings back with it. So a restore rewinds the root: the IDs and `seq` values issued after the backup will be issued again, to other tasks, folders and events, and an agent's saved `last_seq` from after it is `not-found` until the restored root has issued that many again (Command surface). `log/` comes from the backup too: restoring replaces the whole root, `log/` included. Don't copy the replaced root's rotated files into it: the restored root will issue their `seq` again, and `log rotate` takes one whose range the table still holds for a run cut short, and removes it.
- **Uninstalling** is removing the binary, and `~/koan-root` if its work isn't wanted.

### Rotating the log

`koan log rotate --before <timestamp>` moves the oldest events out of the table into a file in `log/`, and deletes their rows. The user chooses when, and the cut: there is no default, and koan never rotates on its own. Nothing is lost, only moved, so it runs without asking.

- **What moves** is the oldest events, in `seq` order, up to the first one at or after `--before`: always a prefix of the log, so `log/`'s files cover the oldest `seq` values and the table the newest, each range unbroken, even when the clock was set back between changes. A change's events share their `at`, so they move together.
- **The file** is `log/<first>-<last>.jsonl`, named by its first and last `seq` (spec On-disk formats). `log rotate` makes `log/` when it is missing.
- **Its own protocol, not a filesystem step.** Under the write lock, after carrying out pending steps as every write does, it writes the file through `<name>.tmp`, renames it into place and flushes it and `log/`, then deletes the rows and inserts its `rotated` event in one transaction. A pending step would carry every rotated event in its row, so they would sit twice in the database until it ran; writing the file first means a crash leaves the rows in place, never a gap.
- **Safe to rerun.** A crash after the rename leaves the file and the rows. Before writing, rotate lists `log/`: a regular file of koan's name whose whole range is still in the table was left by a run cut short, or one whose commit failed, and is removed, and so is a leftover `<name>.tmp`; then it carries on. It judges nothing else in `log/`.
- **On record.** The `rotated` event says which file holds the events it moved, so the table shows where that history went.
- **Budgeted apart:** within 1 s at 100,000 events (Goals). It writes each event once and deletes its row, about 25 MB at that size.

### Doctor and repair

`doctor` compares the tree with the database and checks the database itself, and changes nothing; how it reads is under Concurrency. `repair` puts the tree back to what the database says, for the kinds it is given. It never changes the database to match the tree, never invents a task or folder from what it finds on disk, and never deletes anything that holds content.

The only outside changes left are hand changes to `@` entries in the tree (a rename or a drag in Zed) and hand edits of the database file. Editing a file's contents is never one.

| Finding | Means | `repair` |
| --- | --- | --- |
| `pending-step` | A change's filesystem steps were cut short | Carries them out, as the next write would. |
| `stuck-step` | A step found both its source and destination holding content, or neither, or a remove found material | — a person acts first, as A stuck step under Writes says; repair then retries every stuck change, as the spec's repair says |
| `stray-directory` | An `@<name>` directory that is no folder at that path (T2), often a folder renamed by hand. An `@.case-<seq>` directory named by a pending step is that step's, and shows as `pending-step` | Moves it back to a folder whose directory is missing, when exactly one stray and one such folder match: they share a parent (a rename in Zed), or every @\<id>.md in it, at least one, belongs to that folder (a drag). Otherwise reports it, and never decides by name alone. Works top-down, so a parent moved back takes its subfolders with it. |
| `missing-directory` | A folder has no directory at its path (T1) | Makes an empty one. Its notes and material are gone, and the finding says so. |
| `case-differs` | A folder's directory is spelled in another case (T1) | Renames it to the folder's name, through a temporary name as a case-only rename is. |
| `misplaced-notes` | A task's notes sit in another folder's directory (T4) | Moves them back, unless notes are already there. |
| `orphan-notes` | An `@<id>.md` with no live task, whether deleted, archived or never issued, or a `@start.md` or `@done.md` with no gate (T4, T6). The finding says which | Removes them when empty; otherwise leaves them. |
| `stray-entry` | Any other `@` entry, such as a hand-made `@Notes.md`, or an `@<id>.md` whose ID is a live folder's (T6). An `@.tmp-<seq>` file named by a pending step is that step's, and shows as `pending-step` | — reported only |
| `invalid-row` | A row breaks an invariant, after a hand edit of the database. Doctor checks I3 (every parent chain reaches the root), I4, I6, I7, I9 (names, tags, titles, timestamps and integers), I11 and I12, and names the invariant broken. The parts the schema's CHECK constraints hold, such as I6 and a gate in the root, fail `PRAGMA integrity_check` first and are found as `corrupt`; only an edit of the schema itself gets past them | — a person fixes it |
| `corrupt` | The database fails `PRAGMA integrity_check` or `foreign_key_check` | — restore from a backup |

A `corrupt` finding stops doctor there: rows that fail either check can't be trusted, so it reports only them and the pending and stuck steps, checking no other row and comparing nothing with the tree.

Repair runs only when asked: as `repair`, and for its own pending steps, as the first thing a write does. Reads never repair; they report what they trip over beside their results. repair takes the kinds in the order of the table, so a directory renamed by hand is moved back before an empty one could be made in its place.

**doctor never looks in `log/`**, since no rule reads it. The log itself is never checked against the tree: no rule reads it (rules The log).

## Agents

The koan skill is rewritten for the new model, and ships through the Claude Code plugin.

What the skill teaches, and how a coordinator hands work to workers, are the skill's to say, not this doc's. What koan gives them is in its output: `released`, `reblocked`, `gates_made`, and warnings such as `claimed-blocked` and `moved-claimed`.

**Permissions.** The permission rules let every command run without asking except `init`, `delete` and `repair`, which always ask. Archive is safe to run unasked, since it takes only finished work, and so is `log rotate`, which moves events and loses none; when to rotate is still the user's call, as the skill says.

**Agents never run `pick`.** It needs a person at a terminal, and without one it fails at once (spec pick), so it can never hang an agent. The skill doesn't teach it.

## Testing

These are the tests named in the Tested by column of Invariants, enforced and tested. Every rule of the rules doc is something one of them checks.

| Test | What it shows |
| --- | --- |
| **Invariants as properties** | Random sequences of commands against a fresh root. After every command, `doctor` finds nothing, and I1–I14 and T1–T6 hold, checked directly: the events each command recorded are exactly those rules The log gives for what it changed, readiness and finished included, `seq` has no gap, and no earlier event changed. |
| **A reference model** | A small in-memory model of the rules doc answers readiness, the frontier, `released`, `reblocked` and which folders are finished for the same random sequences, and the events each change records; koan must agree with it on every answer. |
| **Crash points** | A fault injected before and after the commit, and at each filesystem step. Readers never see part of a change, or a change without its events, and the next write, or `repair`, brings the tree back to T1–T6. A fault at each point of `log rotate` leaves every event once in the table or in a file after a rerun. |
| **Concurrency** | Hundreds of processes writing and reading one root at once. No claim is ever handed out twice, every read sees a committed state, and `busy` appears only after 5 seconds of waiting. `seq` ascends with no gap, each change's events are contiguous, and a reader paging with `--after` during `log rotate` sees no event twice, and misses only what the rotation moved. |
| **Case** | The Case section's rules as table tests, run in CI on Linux and on macOS with its default case-insensitive filesystem. On both they include a case-only folder move and the case-differs repair, with a crash at each step. |
| **Output** | Every command's JSON checked against its schema, and golden results for each refusal. |
| **Performance** | Benchmarks at 1,000 and 10,000 live tasks, with 100,000 events in the log, run in CI on Linux and macOS and reported against the budget set in Goals. For now neither fails the build; the budget is enforced once performance is taken up again. |
| **Repair** | Each finding made by hand on a fresh root. doctor reports it under the right kind, repair does what the table says, and a second doctor reports only what repair leaves to a person. |
| **Markers** | The nearest marker wins and stops the look; a copied or moved marker binds nothing; archived and deleted folders give their messages, for `where`, `context` and `export` alike; git ignores .koan/; checkout and foreign-files refuse. |
| **Backup and restore** | A backup taken during the concurrency run restores to a root doctor finds clean; a folder moved mid-copy makes backup copy again under the lock; a backup taken while `log rotate` runs holds a table and `log/` that agree; after a restore, `log list --after` a `seq` from before it is `not-found`. |
| **Log** | Golden results for each kind of event, from the command that records it; a write of several subjects, and a batch, as one change; derived state compared before and after, a state that changes and changes back recording nothing; writes that change nothing, and refused ones, recording nothing; every `log list` filter at its bounds, the folder matched as written; `log rotate` run normally, with nothing to rotate, after a crash, rerun with the same and with another `--before`, and with every row rotated, `seq` carrying on. |
| **Export** | Golden results for a subtree with nested folders, gates, claims, and tasks outside it at both ends of edges, and for `/`: every folder and task under the subject once, `external` its direct neighbours only, every edge with an end under it once, and no material. Its `folders` and `tasks` match `folder list` and `task list` with no limit on the same state, and the same database exported twice prints the same bytes. |
| **pick** | A stand-in fzf first on `PATH` records the arguments and lines it is given and answers as told: a choice, several, nothing matched, a cancel, a failure, a version too old. pick prints what the spec says for each, with and without a terminal, and removes its preview directory every time. A run against the real fzf in a pseudo-terminal is skipped by `-short`. A real bash, 4.0 or later, and a real zsh, each driven in a pseudo-terminal with the stand-in fzf, load `--bash`'s or `--zsh`'s script, bind Alt-K to its widget, and press it: a choice is typed in at the cursor, at the end of the line and in the middle of it, and a cancel, nothing matched, or a koan that prints and then fails leaves the line as it was. Loading a script binds no key. Each is skipped by `-short`, and when its shell isn't installed. |
| **Completion** | `__complete` on a tree with every kind, for each argument in the spec's table and each form of word, against golden lines: what is offered, in what order, with what descriptions and directives, a list's comma, the cap and its hint, and a root that isn't usable. These lines are what every shell's script reads. A real bash, with bash-completion, and a real zsh, each driven in a pseudo-terminal, complete a path folder by folder, an ID with its title and a gate after a path, and show the hint. Each is skipped by `-short`, and when its shell isn't installed; CI installs both. fish and powershell are covered by the `__complete` lines alone. |

`go test -short` skips the long concurrency and crash matrices, as today.

## Choices

Anything that would change a rule goes to the rules doc, not to this list. Work decided on but not needed yet is in `docs/deferred.md`.

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
| `task list`, `frontier`, `folder list` and `context`'s tasks return 100 results unless `--limit` says otherwise, with `total` and `truncated` | everything, 3 MB of JSON at 10,000 tasks for an agent to take in |
| The archive laid out by path, items named by ID | an archive laid out by ID only |
| An empty notes file written at task create | notes made on first edit |
| 100 ms at 10,000 live tasks and 100,000 events; 1 s for `doctor` and `repair`; 200 ms for `export`; 100 ms plus 1 ms a part for `batch`; 1 s for `log list --limit null` and `log rotate`; none for `backup` | another budget |
| A stuck step set aside, later writes going on | every write failing until a person fixes it |
| A backup copied without the lock, then checked by doctor | the lock held throughout, or hard links |
| No list of markers in the database | a list, so delete and archive could warn about bound worktrees |
| Directories fsynced before a step's row is deleted | leaving a rename lost to a power cut for doctor to find |
| Archived fields as JSON beside each archived item | a table in koan.db, lost at every format change before 1.0; JSON inside the folder, where a stray file can be in the way |
| A command line of a subject, then an object, each position one kind of thing, a list in at most one of them, and everything else an option | one positional subject, everything else an option, which made the joins an exception and made `--to` a word to remember for a move |
| Verbs alone, with `task` and `folder` kept only for `create` and `list`, where no reference names the kind. `update`, `delete` and `mv` take tasks and folders, mixed, and an option the subject's kind doesn't take is `invalid-input` | every command under its noun, which made `task` a word to type before every task-only verb and gave show, archive and the joins two spellings each; and then `task` and `folder` kept for `update`, `delete` and `mv` too, whose options differ by kind, which made a caller know a thing's kind before changing it, and a task and a folder two changes to delete |
| `bind` and `unbind`, verbs alone | a `marker` group: a noun, where the only nouns are for kinds a reference can't name |
| `unclaim` to end a claim, paired with `claim` as `block` is with `unblock` | `release`, which read as the `released` field, the open tasks a change made ready, beside `complete`'s ending of claims |
| A command takes a Unix verb's name only with its semantics exactly: `mv` is Linux `mv`, with `-t` and `-T`, a task a file that can't be renamed, never replacing anything and never making a parent, and `delete` stays `delete` | `move` with `--to`, a word to learn; `rm`, whose `-f` is quieter where koan's `--material` deletes more |
| The command line read by cobra and pflag, help and `-h` anywhere, and option values their type refuses as `usage` | a parser of koan's own, which refused `--help` with anything else and `-h`, and left every value check to the command |
| One input, one error: `usage` only for a command line that can't be turned into an input. A missing subject or object, or two options that can't go together, is the input's fault, as `--input` reports it | `usage` for a missing positional and for some options that clash, so the same mistake had a kind that depended on how the input came |
| A write's subject is a list unless its object is, and a list is one change | a list on `complete`, `delete` and `mv`, and one subject on `update`, `archive`, `claim` and `unclaim`, for no reason a caller could tell |
| A folder left out is the root, on every command; `--here` and `--file` find one from the disk, on every command that looks under a folder | `context` and `export` taking the working directory when left out, where the lists took the root, and "here" written `--here` on `pick` and `--file` elsewhere |
| A boolean option false by default, so typing it turns it on: `--direct` for only what is directly in a folder. `--claimed`, whose null default means both, is a filter of three values, not a boolean that defaults to true | `--recursive`, true by default, so only ever typed as `--recursive=false` |
| `block` and `unblock` naming both directions, `--blocked-by` and `--blocks`, as `task create` does | the words after `block`'s subject as what it waits on, beside `--blocks` for the other way, which read as "42 blocks 17" and wrote a reversed edge without a word |
| A gate as `mv`'s subject is `invalid-input`, as on `join`: what a reference names is found as it resolves, with the subjects, before the destination and before the change is checked | a `gate` conflict rule for a refusal that never depends on the tree |
| `unblock` of a gate a folder hasn't made removes nothing, with changed false, as `unjoin` does | `not-found`, which failed a cleanup whose outcome already held |
| The rule `already-complete` for claiming a complete task | `complete`, also the name of a command and of a state |
| `why`'s `ready` gives each task's kept text and whether it is claimed | bare IDs, so an agent claimed a task someone held, or showed each one to learn what it was |
| `delete` returns `deleted`, its subjects, beside `removed`, as `archive` returns `archived` | `removed` alone, against the rule that a write lists what changed under its verb's past tense |
| `complete` and `delete` list in `unclaimed` the claims they ended | ending a claim without a word, where a move of a claimed task warns: the one sign that a change ended someone else's work; a warning, which would fire on every task an agent finishes |
| `archive` reports each item as `mv` does, with its JSON beside it; `init` and `info` name the archive's directory `archived` | `archive` for a task's notes, a folder's directory and the archive itself: three meanings for one word |
| `bind` and `unbind` both report `dir`, `marker` and `changed` | results of two shapes, and an identical rebind reported as a replacement |
| Every `not-found` and `exists` points at its field in `problems`, and a wrong-kind fault names the subject in `ids` | pointers inside a batch only, so a list's failing entry had to be matched back from its ID or the message |
| A set takes duplicates, as an edge given twice is one edge; a list of subjects refuses them | a duplicate refused everywhere but in edges, which failed a generated `--fields id,ref,id` |
| Messages state the facts and the options, in the terms the input came in, and never tell the caller to take a step the user decides | imperative messages, such as `complete them before archiving`, that told an agent to do what the skill forbids; messages in `--input`'s field names after a command line |
| A hint for an option a sibling command takes, such as `--folder` on `task list` | a bare `unknown flag`; hints for every spelling an earlier version took, kept forever for one migration |
| `pick` drawn by fzf, run as an outside program, version 0.63.0 or later, restyled only through fzf's own `FZF_DEFAULT_OPTS` | fzf linked in as a Go library, whose API isn't fzf's to keep stable; a UI of koan's own, such as bubbletea, with every part to build and many dependencies; a file browser such as yazi, which sees the files and none of the database; a `KOAN_PICK_OPTS` of koan's own |
| Completing references only | completing fixed choices and the tags in use as well |
| Past 100 candidates, a hint instead of a list | every candidate, at 0.74 s for 10,000 in bash before it asks whether to show them |
| Only a list's first item completes; the option is repeated for more | completing after a comma, which cobra's bash script drops: bash 5.3's `printf %q` escapes the comma |
| The log as a table in `koan.db`, each change's events in its own transaction | a JSONL file beside it, which commits apart from the change, so it could miss a change or record one that never committed, and which readers couldn't read in one snapshot with the rows |
| Every change logged, and no read | logging reads too, which would make each a write, waiting on the lock: completion on every Tab, and `log list` logging itself |
| Readiness and finished logged as they change, compared before and after the change | readiness replayed from edges and states, which needs every event since the task was made, and those may be rotated away |
| An event for each row and derived state a change actually changes, none for what it leaves as it was | an event for each subject or edge given, which would record changes that never happened |
| No event for what sits under a moved folder, nor for an edge between two things a delete or archive removes | an event for each, which would make a rename of a large folder thousands of events that say nothing the folder's own `moved` doesn't |
| `updated` with each changed field before and after | the new value alone, which needs the events before it to read, and rotation may have moved them |
| `log list`'s folder and `--task` matched as written, never resolved | resolving them against the tree, which would lose the history of what was renamed, moved, deleted or archived |
| `--after` a `seq` never issued is `not-found` | an empty list, which an agent can't tell from a quiet root after a restore |
| `log rotate` run by the user, with a `--before` they choose | rotating on its own at some size or age, which decides for the user how long history stays in the table |
| A rotated file written through a temp file before the rows are deleted, by rotation's own steps | a filesystem step, which would hold every rotated event in its row until it ran |
| `log list` and `log rotate` under `log` | verbs alone, where `rotate` says nothing of what it rotates, and `list` is already `task list`'s and `folder list`'s |
| Only changes to the database logged | events for `bind`, `unbind`, `repair` and `backup`, committed after their work on disk, which a crash between could leave done and not recorded, and which made `bind` and `unbind` take the lock |
| init --ids-from N, carrying the ID sequence from root to root | IDs from 1 in every root, so each format change before 1.0 makes a second @42 |
| `export` returns a folder's whole subtree, every state, gates included, with no limit and nothing to narrow it | the 100-result default `task list`, `frontier` and `folder list` keep for an agent's context, which would cut a graph short; and filters of its own, which jq already gives |
