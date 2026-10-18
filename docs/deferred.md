# Deferred work

Work decided on but not built yet. Each item gets its own PR: agree the decisions, spec it (`spec/`, `schemas/`), implement with tests, review.

## Noun-verb command line

Decided 2026-10-18. The commands move into cobra command groups, `gh` style, so that each command has one input shape and one output shape:

```text
koan task   create | show | update | move | delete
            done | reopen | block | unblock | why
            list | frontier | claim | claim-next | release
koan folder create | show | update | move | delete | list
koan batch  (today's create-batch: folders and tasks)
koan where | init | info | version | doctor | repair | migrate
```

- **Full noun-verb**: task-only commands go under `task` too (`koan task done 4`), not only the folder ones.
- **`batch` is top-level**, since it makes folders as well as tasks.
- **`move` keeps one meaning per group:** `task move 4 --to /p` moves the task into `/p`; `folder move /a --to /b` gives the folder a new path.
- **`list --include-folders` becomes `folder list`**, so `task list` returns only tasks and no command returns a mix.
- No back-compat: the old names go. Rename everything that names a command: `spec/`, the schema files, the skill, the README, `docs/coordination.md`, the permission rules, and `scripts/install.sh`.
- The decisions below, from the same review, ship with it.

### Output: identities from writes, records from reads

Decided 2026-10-18. Command–query separation: a command that changes the tree returns identities, which pipe straight into the next command; a command that reads returns records. A record is exact only as of the call; an identity can always be looked up again with `show`.

- **References** are a task's ID, and a folder's ID and path. The ID is the stable one: a path changes when the folder, or one above it, moves.
- **Writes return identities and side effects:** `id` (and `path` for a folder), `changed` (always present), `dependents`, `folders_created`. This covers `create`, `update`, `move`, `done`, `reopen`, `block`, `unblock`, `claim`, `release`, and their folder counterparts.
- **Reads return records:** `task show`, `folder show`, `task list`, `folder list`, `frontier`, trimmed with `--fields`.
- **Exceptions:**
  - **`delete`** (task and folder) returns the record as it was before deletion, since there is nothing left to `show`. Its `dir` and `notes_path` name paths that are gone.
  - **`claim-next`** returns the claimed task's record: the caller doesn't know which task it got, and needs its title and `notes_path` to start.
  - **`batch`** returns identities: its `refs` map to IDs, and each folder made with its ID and path.
  - **`where`** returns paths and a reference, which is its job.
  - **`why`, `doctor`, `info`, `version`** are reports with their own shapes.
- Inside a record, a field naming another task or folder holds a reference, as `blocked_by` and a task's `folder` already do.
- `operations.md` states this rule once, in its output section.

### Blocker naming: one word per direction

Decided 2026-10-18. A dependency `3 → 4` reads two ways: 4 is *blocked by* 3 (upstream, stored in 4's `blocked_by`), and 3 *blocks* 4 (downstream).

- Upstream is always `blocked_by`: `task block 4 --blocked-by 3` and `task unblock 4 --blocked-by 3`, in place of `--blockers`.
- The output field `blocking` (the open part of `blocked_by`) becomes `open_blockers`, so the "blocks" root word means downstream only, as in `create --blocks`.
- `batch` entries still take no `blocks`: inside a batch every edge is a `blocked_by`, and making an existing task wait on a new one stays with `create --blocks` until gates are decided.

### Folder flags

Decided 2026-10-18.

- `folder delete`'s `--recursive` (`-r`, remove a folder that is not empty) becomes `--contents`, so `--recursive` has one meaning everywhere: whether a folder scope includes the folders under it.
- `folder list` honours `recursive`. Today `list --include-folders` ignores it and always returns every folder below.

### Error kinds: where the CLI stops

Decided 2026-10-18. Today the same mistake can be `usage` (exit 2) in one command and `invalid-input` (exit 1) in another: `show` with no ID is `usage`, `move 3` with no `--to` is `invalid-input`, `--extra nope` is `usage` and `--extra '[1]'` is `invalid-input`.

- **`usage`** is only a command line the CLI can't turn into an input: an unknown command or option, a missing or extra positional argument, an option with no value.
- **`invalid-input`** is every value check, done by the operation: a missing required option, a malformed value, bad JSON in `--extra` (the CLI passes it on as a string).
- `cli.md` states the split once.
- Fix the message for a problem with no field (`update 3` with nothing to change), which renders as `the input is invalid:  gives nothing to change`.

### Input and output odds and ends

- A `batch` folder entry names its path `path`; `folder create` calls it `folder`. Use `folder` in both.
- `--notes-file` exists only on the command line (through `-i`, `notes_file` is refused), and `where --file` is made absolute only by the CLI (through `-i`, a relative path is `invalid-input`). Either state these as CLI conveniences in `cli.md`, or accept both through `-i`.
- Task output carries the file's `schema` field, which means nothing to a caller. Leave it out of results.

## Cycles without Tarjan

`graph.Cycles` finds cycles with a hand-written Tarjan's strongly connected components (`components`, about 65 lines). Trees are small, so a task is in a cycle exactly when `Path` leads from one of its blockers back to it, and the cycles can be built from that with the `Path` already there. Do it with the graph tests as the check: `doctor`'s `cycle` findings, their order, and their `ids` must not change.

## `forget-folder` and `forget-task`

Turn a koan folder or task back into plain files, leaving the work itself in place. Designed together, since what happens to a forgotten task's koan data and notes is `forget-folder`'s core question.

- `forget-folder` removes the folder's `@folder.json` and the `@` from its name; `@folder.md` becomes `folder.md`, refused when one is there.
- It refuses a folder holding tasks (`has-tasks`) unless `--force`, and one holding folders (`has-children`).

## Worktree markers

- `koan bind` and `unbind`: a self-ignoring `.koan/` marker in a working directory, keyed by folder ID, holding the folder's ID, path, and dir.
- `koan where` resolves from a bound directory. Still open: whether a separate `context` command is needed.

## Shell completion

- `koan completion <shell>`, on cobra's scripts: paths folder by folder, `@` IDs of folders and tasks with their titles, material after `:`, and the bash colon workaround.
- Completion reads without the lock, best effort. That is an exception to "a read never sees part of a write", and the spec must name it.

## Retiring the shingi plugin

The rest of the shingi absorption is done and koan's skill covers it. The plugin can go once nothing uses shingi's `bind`, so this waits on worktree markers.

## Gates

Not designed yet. A gate would be a start or done task per folder, letting folders be ordered against each other. Before designing them, try `create --blocks` (the new task blocks the tasks named) in real use, and see whether gates are needed at all. Until then, `@folder.json` stays `schema`, `id`, `title`, `extra`, with no `start` or `done`.
