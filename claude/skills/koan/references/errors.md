# koan: errors, warnings and root states

Read this when a command fails with a kind or rule that the skill's core doesn't cover, when a warning comes back and you aren't sure what it means, or when `koan info` doesn't report `usable`.

## Root states and versions

`koan info`'s `.result.state` is `usable`, `not-initialized`, `unsupported-format` or `corrupt`. Unless it is `usable`, `.result.error` is what every other command fails with:

- `not-initialized`: there is no root, or no database in it. Tell the user and suggest `koan init`. **Never run `init` unasked.** If `init` fails with `conflict`, rule `not-a-root`, `~/koan-root` holds files that aren't this koan's, often a tree from the original koan (an `@koan.json`): the user must move it aside first. Never move it yourself.
- `unsupported-format`: the database is in a format this binary doesn't read. When `.result.format` is below `koan version`'s `.result.format`, the root is older than this koan: there is no conversion before 1.0, so the user backs it up and starts a fresh root. Otherwise the user needs a newer koan. Either way, tell the user; never move or replace the root yourself.
- `corrupt`: report it; the user restores from a backup ([repair.md](repair.md)).

`version` printing `v0.13.0` or later, a pseudo-version (`v0.0.0-…`) or `(devel)` is this koan. An earlier `v` release lacks commands this skill names, and anything else, such as `0.3.0` without the `v`, is the original koan, which works differently: either way, tell the user to upgrade with `go install github.com/phansen314/koan/cmd/koan@latest`. A `usage` error for a command this skill names means the same.

## Errors

- `invalid-input`: `.error.problems` lists every bad field, each as a JSON Pointer (`/blocked_by/0`) with a reason. Fix and rerun. A reference to the wrong kind of thing is this too, such as a gate as `join`'s or `mv`'s subject, a task where a folder is taken, or a field the subject's kind doesn't take, with the subject's ID in `.error.ids`; so is a missing subject, as `koan complete` alone, and a subject given twice. The same name twice in a set, such as `--tags-add a,a`, counts once.
- `not-found`: a reference names nothing; one that names something of the wrong kind is `invalid-input`. `.error.ids` or `.error.paths` is the first one, `.error.problems` points at its option or field, and the message says whether it was deleted, archived or never issued. When it was archived, `.error.archived` says where it went: each item has `id`, `notes_path` and `dir`, as a move's does.
- `busy`: the lock stayed held for 5 seconds; koan already waited. It is always safe to retry, but not in a loop: retry once, then tell the user something is holding it.
- `not-initialized`, `unsupported-format`, `corrupt`, `io`, `internal`: stop and report, quoting `.error.message`. Never edit `koan.db` or koan's files to work around them.
- `conflict`: valid input the tree refuses. `.error.rule` says why:
  - `cycle`: the edge would make a task wait on itself; `.error.ids` is the cycle, in order. Tell the user rather than rearranging their edges.
  - `claimed`: someone holds a claim on one of the tasks (the skill's Claims). `already-complete`: claiming a complete task; it needs `reopen` first, if the user wants it worked on again.
  - `case-mismatch`: a path matched a folder only ignoring case; `.error.paths` has the path as given, then as it is. Use the folder's spelling. Don't rename the folder to match unless asked.
  - `exists`: the destination is taken: a folder path for a create or a move, an entry on disk there, or a non-empty directory for a backup. `.error.problems` points at the option, and `.error.ids` has the folder that holds the path.
  - `not-finished`: archiving work that isn't complete; `.error.ids` is the open tasks. Ask the user; never complete them to get past it.
  - `not-empty`, `has-material`: a `delete` of folders without `--contents` or `--material`. Never add them on your own ([moving.md](moving.md)).
  - `into-itself`: a folder can't move inside itself.
  - `checkout`, `bound`, `foreign-files`: `bind` refused the directory ([worktrees.md](worktrees.md)).
  - `not-a-root`: `init` found files that aren't koan's (above).
  - `no-ids`: the root has issued its last ID. Tell the user.

## Warnings

Warnings never stop a command, and come with failures too. **Always tell the user about them.** Each has a `kind`, a `message`, and the `ids` and `paths` it concerns.

- `claimed-blocked`: the change made a claimed task wait again. Tell whoever holds the claim.
- `moved-claimed`: a move changed a claimed task's notes path under its holder.
- `complete-blocker`: an edge was added to a complete task's `blocked_by`. It doesn't reopen the task, so what waits on it isn't held back.
- `stale-marker`: a worktree's marker has an old path hint; `bind` again refreshes it.
- `missing-notes`: a notes file is missing; it reads as empty.
- `pending-step`, `stuck-step`, `pending-filesystem`: the tree on disk is behind the database. A pending step is finished by the next write; a stuck one needs the user ([repair.md](repair.md)).
