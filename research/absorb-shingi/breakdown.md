# Breakdown into units of work

2026-10-17. Units of work for [review.md](review.md)'s decisions, with changes to existing features first and new features last. Each unit is one branch: its spec changes (`spec/`, `schemas/`) first, then the code and tests, merged before the next one starts unless it says otherwise.

## One format bump (decided 2026-10-17)

koan's rule is that every change to a file's fields is a new format, with a conversion in `migrate`. So that the live tree is migrated once, unit 2 introduces format 2 whole: the `@` names, and a `@folder.json` in every koan folder in its final shape (`schema`, `id`, `title`, `start`, `done`, `extra`, the last four `null` or `{}`), with an empty `@folder.md`. Commands that read or set those fields come in units 4 and 6. Unit 2 does part of a new feature's work early, by design. (The alternative, a format bump per feature, would have meant up to three migrations.)

## Changes to existing features

### 1. `repair`: dangling references by request only

- `repair` fixes every repairable kind except `dangling-reference`, which it fixes only when asked by name: `repair --kinds <kind>,...`, repeatable. With `--kinds`, it fixes only the kinds named.
- `doctor`'s `dangling-reference` message says to move a hand-moved folder back first. `repairable` stays a boolean: whether `repair` can fix it, by default or when asked by name.
- A `stale-claim` on a task that is gone is handled the same way, for the same reason; one on a done task is still fixed by default.
- Independent of everything else; can start now.

### 2. Format 2: `@` marks what koan owns

- On disk: `@name/` folders, `@N.json` and `@N.md` tasks, and in every koan folder a `@folder.json` (final shape, `id` drawn from the counter) and an empty `@folder.md`. Typed paths are unchanged.
- `create-folder`, and every command that makes folders as needed, writes the two folder files.
- `move-folder` and `delete-folder` carry or remove them with the folder; `list --include-folders` is unchanged until unit 4.
- Walks enter only `@` folders inside the root or a koan folder (the nesting rule). Every other entry is material and never read, `@` folders inside material included.
- Folder names: `@` plus today's rule, 65 characters on disk.
- `init` recovers `last_id` from `@` task names and folder IDs.
- IDs are unique across tasks and folders and within the counter (invariants, with `doctor` findings), since the counter is shared from this unit on.
- `orphan-notes` and the other findings now concern `@N.md` only. New finding: an `@` folder in a koan folder with no `@folder.json`.
- No conversion from format 1 (decided 2026-10-18: the user is koan's only user, and starts fresh on both machines). The root file is `@koan.json`, with `{"schema": 2}`, so every entry koan owns carries the `@`. This binary sees a format-1 tree (with only `koan.json`) as not initialized, and `init` there makes everything format 1 wrote material. An earlier binary on a new tree is not guarded against: the user doesn't care, starting fresh on both machines. Settles review open item 1.
- Starting over, once the units wanted have merged: install the new binary, move `~/koan-root` and `~/.config/koan/state.json` aside, `koan init`, and recreate the open tasks worth keeping.

### 3. `delete-folder`: refuse material unless forced

- Recursive `delete-folder` refuses when the subtree holds visible material, unless `--force`. Hidden entries don't count and are removed with the folder.
- The error (`conflict`, `has-material`) lists the material it found. Finding it needs only a listing of each koan folder, never a walk into material.
- An entry named with `@` that is not one of koan's counts as material: koan didn't write it either (decided 2026-10-18).

## New features

### 4. Folder metadata

- The files exist from unit 2; this unit makes `title`, `extra`, and the notes usable (`start` and `done` stay `null` until unit 6).
- `show-folder` and `update-folder` (`--title`, `--extra-merge`, `--extra-remove`, `--extra-replace-all`), mirroring `show` and `update`. `create-folder` takes `--title`, `--extra`, `--notes`.
- The folder output shape, used by `show-folder`, `list --include-folders`, and every result that names a folder: id, path, dir, title, extra, notes_path (gate fields join in unit 6). Settles review open item 2.
- Invariant and finding: a `blocked_by` never names a folder's ID.

### 5. References

- `@id` for a folder anywhere a folder path is taken; `42` or `@42` for a task.
- Material references: `/path:material` and `@id:material`, never entering a koan folder or leaving the folder with `..`.
- `koan where`: resolves a reference to its disk path, and a disk path to its stable reference with its hint.
- Every output that names a folder carries `path` and `dir`.

### 6. Gates

The largest unit; it can be split into 6a and 6b as two branches.

**6a. Gates on folders and tasks**

- `start` and `done` pointers; `create-folder --gates` and `update-folder --gates` create and link them to the parent's; `update-folder --no-gates` removes them.
- `create --gates` and `update <id> --gates` wire a task between its folder's gates.
- `create-folder --after` and `--before` (and on `update-folder`, if review open item 5 says so).
- Gate rules: `delete`, `move`, and `update --title` refuse a gate.
- Derived: gate titles (`Start: <path> (<title>)`), each task's `gate` field, the folder's state.
- Warnings: `ungated-task`, `ungated-folder`, `folder-done`. Implicitly created folders are ungated (review open item 6).
- Gate invariants and their `doctor` findings.
- `frontier` and `claim-next` take `--gates start|done|none`; `docs/coordination.md` gains `claim-next --gates none`.

**6b. Moves with gates**

- `move-folder` relinks a gated folder to its new parent's gates, or drops the old links, and refuses a cycle through gate links, naming the folders.
- `move` of a gate-wired task warns `foreign-gates`.

### 7. Folders in `create-batch`

- Folder entries with `gates`, `title`, `extra`, `notes`, `after`, `before`, and inline tasks; checks before anything is written (duplicate paths, names, lineage, cycles through gates).
- One counter block for folders and tasks; the result maps folders and refs to their IDs.
- A failure partway reports the existing task files rewritten, as well as what was created.
- Settles review open item 4 (refs' scope, top-level tasks naming gates, `@id` in `after`).

### 8. `forget-folder`

- Removes a folder's gates, `@folder.json`, and `@`; `@folder.md` becomes `folder.md`, refused when one is there.
- Refuses `has-tasks` (ordinary tasks) unless `--force`, and `has-children`.

### 9. Worktree markers

- `koan bind` and `unbind`, the self-ignoring `.koan/` marker keyed by folder ID; `koan where` (and `context`, if still wanted) resolving from a bound worktree. Settles review open item 3.

### 10. Shell completion

- `koan completion <shell>` on cobra's scripts: paths folder by folder, `@` IDs of folders and tasks with their titles, material after `:`, the bash colon workaround. Best-effort lockless reads, named in the spec as an exception (review open item 7).

### 11. Skill and docs

- koan's skill gains gates, batches, binding, and the reference convention from shingi's; the shingi plugin is retired (review open item 8).
- Could be done piecemeal, with each unit updating the skill for what it adds, leaving this unit as only retiring shingi.

## Dependencies

```text
1                      (independent)
2 → 3
2 → 4 → 5
      4 → 6a → 6b
      6a → 7
      4 → 8            (gates part needs 6a)
      5 → 9
      5 → 10
6a, 7, 9 → 11
```
