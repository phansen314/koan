# Breakdown into units of work

2026-10-17. Units of work for [review.md](review.md)'s decisions, with changes to existing features first and new features last.

## Status and how the work goes (2026-10-18)

- **Where:** the worktree `.claude/worktrees/absorb-shingi` of `/home/phansen/code/koan`, branch `repair-kinds`. Every unit builds on that branch, one after another; nothing is merged to `main` or pushed yet.
- **Done:** units 1–4, each committed with the fixes its review found. Unit 6 is dropped (gates deferred, below). **Next: unit 5.**
- **Per unit:** agree the decisions with the user; draft the spec (`spec/`, `schemas/`) and show it; implement with tests (`go vet ./...`, `go test -count=1 ./...`, crash tests included); commit when the user says so; spawn a read-only Opus review session with sesshin (job `review-<unit>`, findings written to a file in the scratchpad); check each finding against the code, fix, commit.
- **Conventions in the tests:** `w.root("p/3.json")` is `<root>/@p/@3.json` (it adds the `@`); `w.disk(...)` takes names as they are on disk, for material. Folders take IDs from the shared counter, so task IDs in tests account for the folders made first. `w.findings()` lists doctor's findings as `kind paths`; `paths(records)` and `w.record(res)` read folder records.
- **Decisions are recorded here and in review.md**; where they and design-notes.md disagree, these win.

## Gates deferred (decided 2026-10-18)

Gates are not implemented in this work. The user is still deciding how they should work; the best idea so far is a `--blocks` option on `create` (the new task blocks the tasks named), then seeing in real use whether gates are needed at all. So:

- `@folder.json` has no `start` or `done`: its fields are `schema`, `id`, `title`, `extra`.
- Unit 6 (gates) is dropped, and every gate part of the other units with it: gate links in `create-batch` (`after`, `before`, wiring inline tasks), and removing gates in `forget-folder`.
- Everything else goes ahead.

## One format bump (decided 2026-10-17)

koan's rule is that every change to a file's fields is a new format, with a conversion in `migrate`. So that the live tree is migrated once, unit 2 introduces format 2 whole: the `@` names, and a `@folder.json` in every koan folder in its final shape (`schema`, `id`, `title`, `extra`, the last two `null` and `{}`; `start` and `done` were dropped with gates), with an empty `@folder.md`. Commands that read or set those fields come in units 4 and 6. Unit 2 does part of a new feature's work early, by design. (The alternative, a format bump per feature, would have meant up to three migrations.)

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

- The files exist from unit 2; this unit makes `title`, `extra`, and the notes usable.
- `show-folder` and `update-folder` (`--title`, `--extra-merge`, `--extra-remove`, `--extra-replace-all`), mirroring `show` and `update`. `create-folder` takes `--title`, `--extra`, `--notes`.
- The folder output shape, used by `show-folder`, `list --include-folders`, and every result that names a folder: id, path, dir, title, extra, notes_path. Settles review open item 2.
- Invariant and finding: a `blocked_by` never names a folder's ID. Already held: `create` and `block` refuse an ID that is no task with `not-found` (now saying when it is a folder's), and `doctor` reports one that got in as `dangling-reference`.
- Only folder commands return folder records; a task's `folder` and `folders_created` stay paths (decided 2026-10-18).
- `create --blocks <ids>`: the new task is added to each named task's `blocked_by`, and a cycle through it is refused before an ID is used (added 2026-10-18, in place of gates for now).

### 5. References

Proposed 2026-10-18, **not yet confirmed by the user** (items a–d below are the open choices):

- **`@id` for a folder** in every input that names an existing folder: `show-folder`, `update-folder`, `delete-folder`, `move-folder`'s `folder`, the `folder` scope of `list`, `frontier`, and `claim-next`, and `create`'s and `create-batch`'s `folder` when it exists. (a) A destination that may not exist yet stays a path only: `move-folder`'s `to`, and a folder `create` would make. Resolving `@41` reads the folder files, only when an `@` reference is given; an unknown ID is `not-found`, a task's ID `not-found` saying it is a task.
- **`@42` for a task** wherever a task ID is taken: `id`, `blocked_by`, `blockers`, `blocks`. In JSON an integer or the string `"@42"`; in create-batch's `blocked_by`, a ref can't clash, since a ref holds no `@`.
- **Material references:** `/path:docs/plan.md` and `@41:docs/plan.md`. The part after the colon is a disk path: no `..`, no empty segment, and no segment starting with `@`, which would enter what is koan's. (c) The file need not exist, so an agent can ask where to write one.
- **`koan where`**, both ways. `where <ref>` returns `path`, `dir`, the folder's `id`, and for material `material` and its absolute `file`; (b) for a task's `@42`, its folder and notes path. `where <disk path>` returns a path inside `~/koan-root` as its stable reference and its hint: `@41:docs/plan.md` and `/work/HOME-12345/research:docs/plan.md`. (d) A path outside the tree is `not-found` until worktree markers (unit 9).
- Dropped from the first plan: "every output that names a folder carries `path` and `dir`". Folder records already do, and unit 4 decided other results keep plain paths.

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
