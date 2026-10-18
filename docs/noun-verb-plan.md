# Noun-verb command line: plan

The plan for the [noun-verb item](deferred.md#noun-verb-command-line): every command, option, and output field mapped to its new home, what to change, and what the decisions left open. It goes when the work merges.

## Commands and options

| Today | New | Changes |
|---|---|---|
| `init`, `version`, `info`, `doctor` | same | — |
| `repair --kinds` | same | — |
| `migrate --dry-run` | same | — |
| `create-folder <folder>` `--title --extra --notes --notes-file` | `folder create <folder>` | Result: identities. |
| `show-folder <folder>` | `folder show <folder>` | — |
| `update-folder <folder>` `--title --extra-merge --extra-remove --extra-replace-all` | `folder update <folder>` | Result: identities. |
| `move-folder <folder> --to` | `folder move <folder> --to` | Result: identities. |
| `delete-folder <folder>` `-r, --recursive`, `--force` | `folder delete <folder>` `--contents`, `--force` | Field `recursive` → `contents`. Result: the record. |
| `list --include-folders` | `folder list` `--folder --recursive --limit --fields` | Honours `recursive`; the folder itself is left out. |
| `create <title>` `--folder --priority --tags --blocked-by --blocks --extra --notes --notes-file` | `task create <title>` | Result: identities. |
| `create-batch -i` | `batch -i` | Folder entry `path` → `folder`. |
| `show <id>` | `task show <id>` | — |
| `update <id>` `--title --priority --tags-add --tags-remove --tags-replace-all --extra-merge --extra-remove --extra-replace-all` | `task update <id>` | Result: identities. |
| `done <id>`, `reopen <id>` | `task done`, `task reopen` | Result: identities. |
| `block <id> --blockers` | `task block <id> --blocked-by` | Field `blockers` → `blocked_by`. Result: identities. |
| `unblock <id> --blockers` | `task unblock <id> --blocked-by` | As `block`. |
| `move <id> --to` | `task move <id> --to` | Result: identities. |
| `delete <id>` | `task delete <id>` | Result: the record. |
| `frontier` `--folder --recursive --tags-any --tags-all --limit --fields` | `task frontier` | — |
| `list` `frontier`'s, `--claimed --readiness --include-folders` | `task list` | `--include-folders` and `folders` go. |
| `why <id>` | `task why <id>` | — |
| `where <ref>`, `--file` | same | — |
| `claim <id> --extra` | `task claim <id>` | Result: identities. |
| `claim-next` `--folder --recursive --tags-any --tags-all --extra` | `task claim-next` | — |
| `release <id>` | `task release <id>` | `changed` stays. |
| `-i, --input`, `-h, --help` | same, on every command | — |

## Results

| Command | Today | New |
|---|---|---|
| `task create` | `task`, `folders_created`, `dependents` | `id`, `changed`, `folders_created`, `dependents` |
| `task update`, `done`, `reopen`, `block`, `unblock` | `task`, `changed` | `id`, `changed` |
| `task move` | `task`, `folders_created`, `changed` | `id`, `changed`, `folders_created` |
| `task claim` | `task` | `id`, `changed` (always `true`: a claim already there is refused) |
| `task release` | `id`, `changed` | same |
| `task delete` | `id`, `dependents` | `id`, `task` (the record before deletion, `null` when the file was unusable), `dependents` |
| `task show` | `task` | same |
| `task list` | `tasks`, `total`, `truncated`, `folders` (with `include_folders`) | `tasks`, `total`, `truncated` |
| `task frontier`, `why`, `claim-next` | as now | same |
| `folder create` | `folder` (record), `folders_created` | `id`, `path`, `changed`, `folders_created` |
| `folder update` | `folder` (record), `changed` | `id`, `path`, `changed` |
| `folder move` | `folder` (record), `folders_created`, `changed` | `id`, `path` (the new one), `changed`, `folders_created` |
| `folder delete` | `folder` (path), `deleted`, `dependents` | `folder` (the record before deletion), `deleted`, `dependents` |
| `folder show` | `folder` | same |
| `folder list` | (`list`'s `folders`) | `folders` (partial records), `total`, `truncated` |
| `batch` | `ids`, `refs`, `folders`, `folders_created` | same |
| `where`, `init`, `info`, `version`, `doctor`, `repair`, `migrate` | as now | same: reports |

A folder identity's `id` is `null` where its record's would be: the root, or a folder with no usable folder file.

### The task record

| Field | Change |
|---|---|
| `schema` | Leaves the record, and `fields`. |
| `blocking` | Becomes `open_blockers`. |
| every other | Unchanged. |

### Errors

| Case | Today | New |
|---|---|---|
| Unknown command or option, missing or extra positional, option with no value | `usage` | same |
| A group with no command, or one it lacks (`koan task`, `koan task frob`) | — | `usage`, not cobra's help |
| `where` with neither a reference nor `--file`, or both | `invalid-input` | same |
| Missing required option (`task move 3` with no `--to`) | `invalid-input` | same |
| Value that is wrong (`task show abc`, `--limit x`, `--recursive=maybe`) | `invalid-input` | same |
| `--extra`, `--extra-merge`, `--extra-replace-all` not one JSON value | `usage` | `invalid-input`: passed on as a string |
| `--notes` with `--notes-file` | `usage` | same |
| `--input` with other arguments | `usage` | same |
| `--input` or `--notes-file` file that can't be read, or no working directory for `--file` | `usage` | `io` |
| A problem with no field (`task update 3`) | `the input is invalid:  gives nothing to change` | `the input gives nothing to change` |

## Checklist

- [x] **Spec**: `operations.md` (command names, the output rule once in Conventions, results, `blocked_by`, `open_blockers`, `contents`, batch `folder`, `folder list`), `cli.md` (command table, the usage split, the CLI conveniences, examples), `design.md` and `implementation.md` (every command named).
- [x] **Schemas**: rename each `<command>-input.json` and `-output.json` to the full name; new `folder-list-*`; rewrite the write outputs; `task.json` without `schema`, with `open_blockers`; `task-list-input` without `include_folders`; block and unblock `blocked_by`; `folder-delete-input` `contents`; batch folder entry `folder`.
- [x] **Code**, `internal/cli`: cobra groups `task` and `folder`, which answer a missing or unknown command with `usage`, as the root does (an `Args` hook and a `RunE` on each); the command table keyed by full name; JSON options passed on as strings; `--blocked-by` on block and unblock; `--contents`; drop `--include-folders`; a file it can't read is `io`.
- [x] **Code**, `internal/ops`: command names; write results as identities, without building the task's record, so its blockers' warnings go; delete results as records, `task delete` loading the file it used not to, with an `unusable-file` warning when it can't; `folder list` with `recursive`, `limit`, `fields`; drop `include_folders`; `blockers` → `blocked_by`; `recursive` → `contents`; batch `path` → `folder`; the message for a problem with no field; messages and finding texts that name a command or field (`doctor.go`'s `koan unblock` and `koan delete`, `update-folder` in `create.go` and `folders.go`, `recursive` in `folders.go`).
- [x] **Code**, `internal/model`: `Task` without `schema` (it embeds `TaskFile`); `Blocking` → `OpenBlockers`; `TaskFields` to match; a folder field list for `folder list --fields`.
- [x] **Tests**: `cli_test` (the table matches ops and schemas, by full name), `ops` tests, `e2e/` and the crash matrix (command names, result shapes), the schema checks.
- [x] **README**: A taste, Several agents, Undoing a delete, the permission rules.
- [x] **docs/coordination.md**: the command names; the worker's read commands (`task show`, `task list`, `task frontier`, `task why`, `folder show`, `folder list`, `where`); the guard script's subcommand match, which today takes one word after `koan`, and its own `koan show`; the coordinator-only row names `folder create`, `update`, `move`, `delete`, now that `folder show` and `folder list` are reads.
- [x] **Skill**: every command and example; `open_blockers`; `--fields` list without `schema`; `--blockers` → `--blocked-by`; batch folder entries' `folder`; `--include-folders` → `folder list`, which also gets `--limit` and `--fields` in "Keep output small"; `folder list` among the worker's reads; `koan <group> <command> --help`; `has-material` / `not-empty` with `--contents`; writes return identities, whose `.result.id` pipes into the next command, so `show` after a write when the record is needed; the minimum version stays `0.2.0` (unreleased).
- [x] **Permission rules** (README, `install.sh`): asks for `koan task delete` and `koan folder delete` in place of `koan delete` and `koan delete-folder`. Claude Code `Bash(koan task delete:*)`, `Bash(koan folder delete:*)`; OpenCode `"koan task delete *"`, `"koan folder delete *"`. Old rules are removed by hand.
- [ ] **deferred.md**: remove the item; this file goes.

## Decided

Settled 2026-10-18, with the spec and schemas updated to match.

1. **`--notes-file` and `where --file`** are CLI conveniences, in their own section of `cli.md`. `--notes` with `--notes-file` stays `usage`.
2. **`where`** keeps its folder record: it is a report.
3. **`init`, `repair`, `migrate`** are reports, with `doctor`, `info`, `version`.
4. **`folder list`** takes `folder`, `recursive`, `limit`, `fields`; returns the folders under `folder`, not itself, with `total` and `truncated`; `id` and `path` always included. `show` takes no `fields`.
5. **`usage`** keeps `--input` with other arguments; a file the CLI can't read is `io`.
6. **Names**: an operation is its command line without `koan`; its schemas use `-` for the space.
7. **Old permission rules** are removed by hand; no code.
8. **`--contents`** has no short form.

Found while specifying, and confirmed:

- **`task delete` of an unusable task file** has no record to return, so its result keeps `id` beside `task`, which is `null` then.
- **`task delete` of an unusable task file** warns `unusable-file`.
- **A folder file koan can't use**, met by `folder create`, `folder move`, `folder delete`, or `folder list`, gives `id` and `title` `null` and `extra` `{}`, with an `unusable-file` warning, stated once in operations.md's Folder section; `folder show`, `folder update`, and `where` fail with `corrupt`.
- **A write that returns identities** reads no task's blockers, so it no longer warns about them, as `task update` on a task with a missing blocker does today; reads still do.
- **`changed`** is always `true` for `task create` and `task claim`, and the schemas say so (`const: true`).
- **The folder writes still read their own folder file**, for the `id` they return.
