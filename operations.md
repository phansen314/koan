# koan operations

An operation is a single change to, or query of, the data defined in the [design spec](design-spec.md), specified in terms of that data model. Operations are the domain layer: small and orthogonal. They are not CLI commands — the CLI may compose several operations into one command, run under a single write lock (e.g. create a task and make an existing task wait on it, in one step).

Terms follow the design spec's [Terms](design-spec.md#terms).

## Conventions

- **JSON in, JSON out.** Input and output are always JSON with published schemas, so users and agents can construct requests and parse results without scraping text. Every result is wrapped in the [output envelope](#output-envelope).
- **Input follows the file-level rules.** Input is held to the same [file-level rules](design-spec.md#file-validity) for integer literals (`2`, not `2.0` or `2e0`), duplicate keys (in the input itself or anywhere inside `extra`), unpaired surrogate escapes, and nesting depth. A violation is `invalid-input`.
- **Schema identifiers.** Shared schemas have short `$id`s (`envelope`, `error`, `warning`, `task`, `task-view`, `task-projection`, `task-field`, `folder-path`, and the design spec's `task-file`, `root-file`, and `state-file`). Each operation's schemas are `<op>-input`, `<op>-output`, and `<op>-partial`. Where a property has the same meaning and constraints as a task file field, the schema `$ref`s it, and its meaning is the one in [Fields](design-spec.md#fields).
- **Referring to operations and kinds.** Operation names, error kinds, and warning kinds are written in code (`create`, `conflict`, `duplicate-id`), linked on their first mention in a section. Error qualifiers are written `` `kind` (`field`: `value`) ``, e.g. `conflict` (`rule`: `id-exhausted`).
- **Parameters.** An operation takes a parameter only if it changes the meaning of the result or the work done (e.g. `frontier`'s `folder`, which limits which files are read). Narrowing or shaping output — filtering on fields, taking the first N — is left to the caller (e.g. `jq`) or the CLI. One exception: [`frontier`](#frontier) and [`list`](#list) take the few parameters of [Narrowing tasks](#narrowing-tasks) — a limit, a choice of fields, and filters on tags and readiness — and [`why`](#why) takes its choice of fields. Their output goes straight into the context of the agent driving koan, its main caller, where every byte is a cost that later turns pay for too; so the small result must be the one the tool itself makes easy, not one the caller has to remember to cut down. Anything past those few is still left to `jq`.
- **Versioning.** The schemas in this document are koan's public contract (see [Versioning](#versioning)).

## Operation kinds

Every operation is one of these kinds:

- ***read*** — Takes no lock and changes nothing. Read operations that walk the tree follow the design spec's [Walking the tree](design-spec.md#walking-the-tree) and [Reads](design-spec.md#reads) rules. Some reads (`version`, `info`) do not require a usable root.
- ***write*** — Changes the tree. Requires a usable root, takes the write lock, and follows the design spec's [Guarantees](design-spec.md#guarantees) (and [Walking the tree](design-spec.md#walking-the-tree), when it walks). Uses the general [Precedence](#precedence).
- ***setup*** — Creates what writes depend on. Only [`init`](#init) is a setup operation. It takes no lock, the write guarantees do not apply to it, and it defines its own error precedence.
- ***diagnostic*** — Finds, and repairs, what a system crash or an outside change left in the tree (see the design spec's [Diagnosis and repair](design-spec.md#diagnosis-and-repair)). [`doctor`](#doctor) and [`repair`](#repair) are the diagnostic operations. They take the write lock, even `doctor`, which changes nothing, but do not require a usable root: they need the config and the root, and report a missing or unusable `koan.json` or [state file](design-spec.md#state-file) as a [finding](#findings). They walk the whole tree, and define their own error precedence. `repair` follows the write [Guarantees](design-spec.md#guarantees).
- ***migration*** — Converts the tree's files to this binary's formats (see the design spec's [Migrations](design-spec.md#migrations)). Only [`migrate`](#migrate) is a migration operation. It takes the write lock for its whole run, and does not require a usable root: it needs the config, the root, and a `koan.json` in this binary's format or an [older one](design-spec.md#format-versions). It walks the whole tree, follows the write [Guarantees](design-spec.md#guarantees), and defines its own error precedence.

## Operation template

Every operation is specified with the same parts, in this order. Every part is always present except **Order**, which appears only for operations that return a collection. An empty part is written `**Part:** none.`, optionally followed by one sentence saying why. A part may be followed by short unlabeled notes that belong to it.

| Part | Content |
|---|---|
| **Summary** | Unlabeled first paragraph: what the operation does, in one or two sentences. |
| **Kind** | One of the [operation kinds](#operation-kinds), then, in this order: whether it takes the write lock, and whether it requires a usable root. |
| **Input schema** | [JSON Schema](https://json-schema.org/) (draft 2020-12), `$id` `<op>-input`. |
| **Additional validation** | Input rules the schema can't express (e.g. per [Naming and validation](design-spec.md#naming-and-validation)). All raise `invalid-input`. |
| **Preconditions** | State that must hold beforehand. The corresponding errors are listed under Errors. A table when the outcome branches on state. |
| **Needed files** | The [needed files](#precedence), whose problems are errors (including any input path, checked by the [path walk](#path-walk)); the *relevant* files, whose problems are warnings; and how far the operation walks the tree. |
| **Effects** | The resulting state, in data-model terms — not file steps. |
| **Invariants at risk** | Which [invariants](design-spec.md#invariants) the operation could break, and how it prevents that. |
| **Output schema** | JSON Schema of `result` in the envelope on success, `$id` `<op>-output` — or a reference to a [shared schema](#shared-schemas). Structure only; rendering belongs to the CLI. |
| **Order** | Only for operations that return a collection: the order of its items, usually by reference to [Tree order](#tree-order). |
| **Errors** | Table of [error kinds](#error-kinds) and when each is raised, in [precedence](#precedence) order. Operations that require a usable root list the [Root states](#root-states) errors as one row. `io` and `internal` are omitted: any operation can raise them. |
| **Warnings** | Table of [warning kinds](#warning-kinds) and when each is reported. |
| **Partial schema** | For a write or setup that touches several files: JSON Schema of `error.partial`, `$id` `<op>-partial`, describing what took effect before it failed. |
| **Crash behavior** | What a process crash or system crash partway through can leave behind, and which [findings](#finding-kinds) [`doctor`](#doctor) would report. |
| **Retry safety** | Whether running it again after `busy`, an error with `partial`, or a crash is safe, and with what outcome. |

## Output envelope

Every operation returns one of two shapes:

```text
{ "ok": true,  "result": { }, "warnings": [ ] }
{ "ok": false, "error":  { }, "warnings": [ ] }
```

- **`result`** — the operation's output, per its Output schema.
- **`error`** — why the operation failed, per the [error schema](#error-schema). An error means the operation's effects did not take place, except as described by `partial`.
- **`warnings`** — problems found along the way that did not stop the operation, per the [warning schema](#warning-schema). Present, possibly empty, on success and failure alike.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "envelope",
  "oneOf": [
    {
      "type": "object",
      "required": ["ok", "result", "warnings"],
      "properties": {
        "ok": { "const": true },
        "result": { "type": "object" },
        "warnings": { "type": "array", "items": { "$ref": "warning" } }
      },
      "additionalProperties": false
    },
    {
      "type": "object",
      "required": ["ok", "error", "warnings"],
      "properties": {
        "ok": { "const": false },
        "error": { "$ref": "error" },
        "warnings": { "type": "array", "items": { "$ref": "warning" } }
      },
      "additionalProperties": false
    }
  ]
}
```

## Errors

An error means the operation failed. `kind` and `details` are the contract; `message` is for humans and may change between releases.

### Error kinds

| Kind | Meaning | `details` |
|---|---|---|
| `invalid-input` | Input failed validation. Always raised before the write lock is sought, and before any needed file is read (a check on an input path itself, like `init`'s, may inspect that path). Reports **every** invalid input, not just the first. | `problems`: list of `{field, reason}`; `field` is a JSON Pointer into the input (e.g. `/tags/2`), `reason` a human-readable string. Sorted by `field`, then by `reason` (both compared as strings, byte by byte), so the same input always yields the same list; at most the first 20 are listed, with `problems_truncated: true` when more were found. |
| `not-initialized` | The root is *not initialized* (see [Root states](#root-states)). A file that exists but is unusable is never `not-initialized`. | `missing`: `config`, `root`, `metadata` (meaning `koan.json`), or `state` (the [state file](design-spec.md#state-file): absent, or naming another root) — the first absent piece. |
| `migration-pending` | The root *needs migration* (see [Root states](#root-states)): `koan.json` is in an older format, or records a [migration step](design-spec.md#migrations) behind this binary's latest. Nothing was done; [`migrate`](#migrate) brings the tree forward. | `recorded`: the step `koan.json` records (`0` at schema 1); `latest`: this binary's latest step. |
| `environment` | The process's environment lacks what koan needs to locate its files: the home directory, from which the config location is derived (see [Config file](design-spec.md#config-file)). Not a root state — no config was looked for. | `variable`: the environment variable that is unset or unusable; currently always `HOME`. |
| `not-found` | A task or folder named by the input, or a filesystem directory it requires, does not exist. | `folders`: tree folder paths; `ids`: task IDs; `paths`: filesystem paths (e.g. `init`'s missing parent directory). All three always present, empty when not applicable. |
| `conflict` | The operation was refused because it would violate an invariant, overwrite state it must not, or act on a task the tree cannot identify uniquely. | `rule`: the rule that refused it — currently `acyclic`, `id-exhausted` (no ID left under the [ID ceiling](design-spec.md#task-ids)), `config-exists`, `root-not-empty`, `duplicate-id` (a write, or [`why`](#why), names an ID that more than one task file has), `id-above-last-id` (a task the write removes, or names as a blocker, has an ID above `last_id`), `not-empty` (a folder to delete holds tasks, folders, or other files), `destination-exists` (something is already where a folder, or a task's notes, would move), `case-clash` (a folder path names a folder whose name differs only in case from an entry already there; see [Path walk](#path-walk)). `ids`: the tasks involved, always present, possibly empty. For `acyclic`, also `cycles`: `cycles[i]` is one cycle through `ids[i]`, chosen deterministically (see [`block`](#block)). |
| `busy` | Another write (or `doctor`, `repair`, or `migrate`) held the write lock for the whole wait, 5 seconds (see *Bounded wait* in [Guarantees](design-spec.md#guarantees)). Nothing was done. Safe to retry, but repeated `busy` means something is holding the lock. | none (`{}`). |
| `corrupt` | A needed file — or an entry on an input path — is present and readable but its content or type is wrong (see [File validity](design-spec.md#file-validity)); or koan found a file where, under its own invariants, none can exist (e.g. creating a task file that already exists). | `path`; `reason`: `not-json` (not parseable, or not an object), `invalid` (fails a file-level rule, including a missing `schema` or one not written as an integer literal within ±(2^53 − 1)), or `unexpected-file` (wrong entry type, e.g. `koan.json` is a symlink or directory; or a file exists that must not). What is wrong, for `not-json` and `invalid`: `problems`, for a JSON file that is `invalid` — a list of `{field, reason}` as in `invalid-input`, but with `field` a JSON Pointer into the file (e.g. `/updated_at`), sorted the same way, and at most the first 20, with `problems_truncated: true` when more were found; or `detail`, a human-readable string — why the file is `not-json`, or why the config, which is not JSON, is `invalid`. `unexpected-file` has neither. |
| `io` | The environment refused an operation: an unreadable file, permission denied, disk full, read-only filesystem, and similar. An OS error with no symbolic name is `internal`, not `io`. | `path`: built from the root as stored (see [Root path](design-spec.md#root-path)), or the file's own path for an error on the config or the state file; `code`: the symbolic OS error, e.g. `ENOSPC`, never a number. |
| `unsupported-format` | A needed file's `schema` is not the version this binary supports (see [Format versions](design-spec.md#format-versions)); or `koan.json` records a [migration step](design-spec.md#migrations) past this binary's latest, so a newer binary has written the tree. A `koan.json` in an older format is [`migration-pending`](#error-kinds) instead, wherever a usable root is required. | `path`; `found`: the file's version; `supported`: the versions this binary supports. For a step past the latest, also `field`: `migration`, with `found` the step recorded and `supported` `[latest]`; absent when what is unsupported is the `schema`. |
| `internal` | A bug koan detects. Every failure koan reports has a kind: anything not covered above is `internal`. A crash reports nothing at all (see the CLI's [exit codes](cli-spec.md#exit-codes)). | none (`{}`). |

The `reason` fields of `invalid-input` problems, of `corrupt`, and of the `unusable-file` warning are independent: each has its own values.

### Precedence

An operation's **needed files** are the files it must read to do its job: the config, `koan.json`, and the state file for anything that requires a usable root, the entries along any input path, plus whatever its Needed files part lists. A problem with a needed file is an error. A problem with a **relevant** file — one that changes or explains the operation's result, as its Needed files part lists — is a warning. Problems with unrelated files the operation walks past are skipped silently; [`doctor`](#doctor) is the operation for finding those. The design spec's [Walking the tree](design-spec.md#walking-the-tree) applies these rules to blockers, duplicates, and unlistable folders.

A read or write operation reports one error. When several apply, it reports the first in this order:

1. `invalid-input` — checked before anything else is read.
2. `environment` (the config can't be located), then `not-initialized`, `corrupt` or `unsupported-format` for the config and `koan.json`, `migration-pending`, and last the state file's: `not-initialized` (`missing`: `state`), `corrupt`, or `unsupported-format` — the [Root states](#root-states) errors.
3. `busy`.
4. `not-found`, and `corrupt` (`reason`: `unexpected-file`) for an entry on an input path — whichever the [path walk](#path-walk) meets first.
5. `corrupt` or `unsupported-format` for needed task files.
6. `conflict`.

Steps 4–6 are checked under the write lock. A write re-reads `koan.json` and the state file after acquiring the lock (it decides on current state); a problem found only then is reported with the step 2 kinds. `io` and `internal` are reported wherever they occur. Setup, diagnostic, and migration operations define their own order (see [`init`](#init), [`doctor`](#doctor), [`repair`](#repair), and [`migrate`](#migrate)).

When several errors of one step apply and the kind reports only one (e.g. two needed task files are both `corrupt`), the one reported is the first in [tree order](#tree-order). A kind that lists every instance (e.g. `not-found`'s `ids`) lists them all.

A needed task file that the walk found but that has disappeared by the time it is read is treated as never found: `not-found` if it was the only file with that ID. In a write this is possible only through an outside change, since the write lock excludes other writes; in a read, a concurrent write may cause it.

A kind that lists every instance lists them across the whole step: e.g. [`create`](#create) with both a missing `folder` and missing `blocked_by` IDs reports one `not-found` with both `folders` and `ids` filled.

[`block`](#block) reads its own task file(s) before looking up its blockers, since which blockers are new depends on the task's `blocked_by`: an unusable copy of `id` is therefore reported before a missing blocker. When `id` is duplicated, a new blocker is one that is in no copy's `blocked_by`.

### Error schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "error",
  "type": "object",
  "required": ["kind", "message", "details"],
  "properties": {
    "kind": { "type": "string", "description": "One of the Error kinds; callers treat unknown values as a generic failure." },
    "message": { "type": "string", "description": "Human-readable explanation. Not part of the contract." },
    "details": { "type": "object", "description": "Kind-specific structured data (see Error kinds)." },
    "partial": { "type": "object", "description": "Present only when a multi-file write or setup failed after some of its effects took place. Shape is defined per operation (see its Partial schema)." }
  },
  "additionalProperties": false,
  "allOf": [
    { "if": { "properties": { "kind": { "const": "invalid-input" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/invalid-input" } } } },
    { "if": { "properties": { "kind": { "const": "environment" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/environment" } } } },
    { "if": { "properties": { "kind": { "const": "not-initialized" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/not-initialized" } } } },
    { "if": { "properties": { "kind": { "const": "migration-pending" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/migration-pending" } } } },
    { "if": { "properties": { "kind": { "const": "not-found" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/not-found" } } } },
    { "if": { "properties": { "kind": { "const": "conflict" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/conflict" } } } },
    { "if": { "properties": { "kind": { "const": "corrupt" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/corrupt" } } } },
    { "if": { "properties": { "kind": { "const": "io" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/io" } } } },
    { "if": { "properties": { "kind": { "const": "unsupported-format" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/unsupported-format" } } } }
  ],
  "$defs": {
    "invalid-input": {
      "type": "object",
      "required": ["problems"],
      "properties": {
        "problems": {
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "object",
            "required": ["field", "reason"],
            "properties": {
              "field": { "type": "string", "description": "JSON Pointer into the input." },
              "reason": { "type": "string", "description": "Human-readable." }
            },
            "additionalProperties": false
          },
          "maxItems": 20
        },
        "problems_truncated": { "const": true, "description": "Present only when problems omits some of those found." }
      },
      "additionalProperties": false
    },
    "environment": {
      "type": "object",
      "required": ["variable"],
      "properties": { "variable": { "type": "string", "description": "The environment variable koan needed, e.g. HOME." } },
      "additionalProperties": false
    },
    "not-initialized": {
      "type": "object",
      "required": ["missing"],
      "properties": { "missing": { "type": "string", "enum": ["config", "root", "metadata", "state"] } },
      "additionalProperties": false
    },
    "migration-pending": {
      "type": "object",
      "required": ["recorded", "latest"],
      "properties": {
        "recorded": { "$ref": "root-file#/properties/migration", "description": "The step koan.json records; 0 at schema 1." },
        "latest": { "$ref": "root-file#/properties/migration", "description": "This binary's latest step." }
      },
      "additionalProperties": false
    },
    "not-found": {
      "type": "object",
      "required": ["folders", "ids", "paths"],
      "properties": {
        "folders": { "type": "array", "items": { "$ref": "folder-path" } },
        "ids": { "type": "array", "items": { "$ref": "task-file#/properties/id" } },
        "paths": { "type": "array", "items": { "type": "string" } }
      },
      "additionalProperties": false
    },
    "conflict": {
      "type": "object",
      "required": ["rule", "ids"],
      "properties": {
        "rule": { "type": "string", "description": "One of the known conflict rules (see Error kinds); callers treat unknown values as a generic conflict." },
        "ids": { "type": "array", "items": { "$ref": "task-file#/properties/id" } },
        "cycles": {
          "type": "array",
          "minItems": 1,
          "items": { "type": "array", "minItems": 2, "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" } },
          "description": "For rule acyclic: cycles[i] is the cycle for ids[i], as a list of distinct IDs, each task blocked by the next, and the last blocked by the first."
        }
      },
      "additionalProperties": false,
      "if": { "properties": { "rule": { "const": "acyclic" } } },
      "then": { "required": ["cycles"] },
      "else": { "not": { "required": ["cycles"] } }
    },
    "corrupt": {
      "type": "object",
      "required": ["path", "reason"],
      "properties": {
        "path": { "type": "string" },
        "reason": { "type": "string", "enum": ["not-json", "invalid", "unexpected-file"] },
        "problems": {
          "type": "array",
          "minItems": 1,
          "maxItems": 20,
          "items": {
            "type": "object",
            "required": ["field", "reason"],
            "properties": {
              "field": { "type": "string", "description": "JSON Pointer into the file." },
              "reason": { "type": "string", "description": "Human-readable." }
            },
            "additionalProperties": false
          },
          "description": "For invalid, in a JSON file: what is wrong, sorted as invalid-input's problems; the first 20."
        },
        "problems_truncated": { "const": true, "description": "Present only when problems omits some of those found." },
        "detail": { "type": "string", "minLength": 1, "description": "For not-json, and for invalid in the config: what is wrong. Human-readable." }
      },
      "additionalProperties": false,
      "dependentRequired": { "problems_truncated": ["problems"] },
      "allOf": [
        { "if": { "properties": { "reason": { "const": "not-json" } } }, "then": { "required": ["detail"], "not": { "required": ["problems"] } } },
        { "if": { "properties": { "reason": { "const": "invalid" } } }, "then": { "oneOf": [{ "required": ["problems"] }, { "required": ["detail"] }] } },
        { "if": { "properties": { "reason": { "const": "unexpected-file" } } }, "then": { "not": { "anyOf": [{ "required": ["problems"] }, { "required": ["detail"] }] } } }
      ]
    },
    "io": {
      "type": "object",
      "required": ["path", "code"],
      "properties": {
        "path": { "type": "string" },
        "code": { "type": "string", "description": "Symbolic OS error, e.g. ENOSPC." }
      },
      "additionalProperties": false
    },
    "unsupported-format": {
      "type": "object",
      "required": ["path", "found", "supported"],
      "properties": {
        "path": { "type": "string" },
        "found": { "type": "integer", "minimum": -9007199254740991, "maximum": 9007199254740991 },
        "supported": { "type": "array", "items": { "type": "integer" } },
        "field": { "const": "migration", "description": "Present when koan.json records a migration step past this binary's latest: found is that step, and supported is [latest]. Absent when the schema is what is unsupported." }
      },
      "additionalProperties": false
    }
  }
}
```

## Warnings

A warning reports a problem, relevant to the operation's result, that did not stop the operation. Operations never fail because of a warning, and never warn about files unrelated to their result (see [Precedence](#precedence)). Which entries produce no warning at all — non-matching entries, hidden entries — is set out in the design spec's [Walking the tree](design-spec.md#walking-the-tree).

- **One warning per distinct problem:** per file for `unusable-file`, per folder for `unreadable-folder`, per ID for `duplicate-id`, per (referring, missing) pair for `dangling-reference`, per `.md` for `notes-missing` — however many tasks the problem affects.
- **Deterministic order:** `warnings` is sorted by `kind`, then by the first entry of `paths`, then by `ids` (compared element by element, as numbers). The same tree always yields the same warnings in the same order.

### Warning kinds

| Kind | Meaning | `paths` | `ids` | Other fields |
|---|---|---|---|---|
| `unusable-file` | A task file was skipped because it is [unusable](design-spec.md#file-validity). | the file | the task, from the filename | `reason`: `unreadable`, `corrupt`, or `unsupported-format`; `code` when `unreadable` |
| `duplicate-id` | Several task files with one ID all exist, and the duplication bears on the result (see [Walking the tree](design-spec.md#walking-the-tree)). | the files in the operation's scope, in [tree order](#tree-order) | the one ID | — |
| `dangling-reference` | A task's `blocked_by` names an ID with no task (see [Dependencies](design-spec.md#dependencies)). Not reported while a folder the walk had to list was unreadable, since the blocker may be in it; the `unreadable-folder` warning explains why the task counts as blocked. | the referring task file | `[referring, missing]`, in that order | — |
| `cycle` | Tasks that block each other were met by [`why`](#why): a group of them, as [`doctor`](#doctor)'s [`cycle` finding](#finding-kinds). Only an outside change or a system crash leaves one. | none | one cycle in the group, as the finding's: the shortest through its lowest ID, from that ID back to it, e.g. `[12, 15, 12]` | — |
| `unreadable-folder` | A folder the walk had to list could not be listed; its tasks are missing from the result (see [Walking the tree](design-spec.md#walking-the-tree)). | the folder's filesystem path | none | `code` |
| `notes-missing` | A task was written but its `.md` could not be. The task is valid; its notes are empty, or stale if a stray `.md` could not be replaced. | the `.md` | the task | `code`, unless the OS error has no symbolic name |
| `migrated` | A file ftask, koan's former name, left was moved to koan's name for it (see [Migrating from ftask](design-spec.md#migrating-from-ftask)): its config, or the root's `ftask.json`. | the old path, then the new | none | — |

An `unusable-file` warning says which file and, in `reason`, one word for why — never what is wrong inside it: no `problems` or `detail` as a [`corrupt`](#error-kinds) error has. The file was one the operation did not need, a damaged tree can produce dozens, and they recur on every call that walks past them. For the diagnosis, [`show`](#show) the task: its file is needed there, so it fails with the full `corrupt` error.

### Warning schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "warning",
  "type": "object",
  "required": ["kind", "message", "paths", "ids"],
  "properties": {
    "kind": { "type": "string", "description": "One of the Warning kinds; callers ignore unknown values." },
    "message": { "type": "string", "description": "Human-readable explanation. Not part of the contract." },
    "paths": { "type": "array", "items": { "type": "string" }, "description": "Filesystem paths involved; meaning per kind (see Warning kinds)." },
    "ids": { "type": "array", "items": { "type": "integer" }, "description": "Task IDs involved; order and meaning per kind (see Warning kinds)." },
    "reason": { "type": "string", "description": "For unusable-file: unreadable, corrupt, or unsupported-format." },
    "code": { "type": "string", "description": "Symbolic OS error (e.g. EACCES), for unusable-file with reason unreadable, unreadable-folder, and notes-missing (absent there when the OS error has no symbolic name)." }
  },
  "additionalProperties": false,
  "allOf": [
    { "if": { "properties": { "kind": { "const": "unusable-file" } } }, "then": { "required": ["reason"] } },
    { "if": { "properties": { "kind": { "const": "unusable-file" }, "reason": { "const": "unreadable" } }, "required": ["reason"] }, "then": { "required": ["code"] } },
    { "if": { "properties": { "kind": { "const": "unreadable-folder" } } }, "then": { "required": ["code"] } }
  ]
}
```

## Findings

A finding is a problem with the tree that [`doctor`](#doctor) reports and [`repair`](#repair) may fix: the damage a system crash or an outside change leaves, which other operations report as warnings, absorb, or skip silently (see the design spec's [Diagnosis and repair](design-spec.md#diagnosis-and-repair)). Findings are not warnings: they describe the whole tree, not one operation's result, and they are the result of `doctor` itself.

- **Grouped by kind.** Findings come as one group per kind present, each with its kind's repair class, its item `count`, and its `items`. A tree with no findings, other than *informational* ones, is **healthy**.
- **Informational kinds only when asked.** A kind of the *informational* class (see [Diagnosis and repair](design-spec.md#diagnosis-and-repair)) is reported only when the input names it in `kinds`. So, by default, a tree is healthy exactly when `findings` is empty.
- **Short by default.** A group lists at most its first 20 items, with `truncated: true` when it has more; `count` is always the total. A kind the input names in `kinds` is listed in full.
- **Deterministic order.** Groups are sorted by `kind`; the items of a group by the first entry of `paths`, then by `ids` (compared element by element, as numbers), as warnings are. The same tree always yields the same findings in the same order.
- **What `repair` would do.** Each item's `action` names what `repair` does to it, or is `null` when `repair` leaves it to a person. `suggest` says what a person could do: a command to run, or a step to take. It is for humans, like `message`, and not part of the contract.

### Finding kinds

| Kind | Class | One item per | `paths` | `ids` | Other fields | `action` |
|---|---|---|---|---|---|---|
| `temp-leftover` | auto | koan temp file or folder (its name starts with `.koan-tmp-`, or ftask's `.ftask-tmp-`) anywhere under the root: a write's leftover file, or the folder an interrupted [`delete-folder`](#delete-folder) renamed aside. Its contents are not looked at. | the entry | none | — | `remove` |
| `metadata-missing` | on-request | root with no `koan.json`: one item. | where `koan.json` belongs | none | `migration`: the step `repair` would record — this binary's latest when no task file is in an older format, else `0` | `create-metadata` |
| `metadata-unusable` | manual | root whose `koan.json` is unusable, other than in an older format: one item. | `koan.json` | none | `error`: the error every operation that requires a usable root fails with — `corrupt` (with its full `problems` or `detail`), `unsupported-format` (a format this binary doesn't know, or a step past its latest), or `io` | `null` |
| `state-missing` | on-request | root with no [state file](design-spec.md#state-file), or with one that names another root: one item. Not reported while `koan.json` is in an older format that holds `last_id` itself: [`migrate`](#migrate) writes the state file then. | where the state file belongs | none | `last_id`: the highest ID in any task filename, or `0` | `create-state`; `null` while any folder can't be listed, since a task in it may have a higher ID |
| `state-unusable` | manual | root whose state file is unusable: one item. | the state file | none | `error`: the error every operation that requires a usable root fails with — `corrupt` (with its full `problems` or `detail`), `unsupported-format`, or `io` | `null` |
| `migration-pending` | manual | root whose `koan.json` is in an older format, or records a [migration step](design-spec.md#migrations) behind this binary's latest: one item. | `koan.json` | none | `error`: the [`migration-pending`](#error-kinds) error every operation that requires a usable root fails with | `null` |
| `old-format` | manual | task file in an [older format](design-spec.md#format-versions) than this binary's, which [`migrate`](#migrate) converts. | the task file | its ID, from the filename | — | `null` |
| `id-above-last-id` | auto | task file whose filename ID is above `last_id`. Only when the state file is usable. | the task file | its ID | `last_id`: the highest ID in any task filename | `raise-last-id` |
| `dangling-reference` | auto | pair of a task file and an ID in its `blocked_by` that names no task, as the [warning](#warning-kinds) of that name. Not reported while any folder can't be listed, since the task may be in it. | the referring task file | `[referring, missing]`, in that order | — | `remove-reference` |
| `orphan-notes` | auto, for `empty` and `linked` items | `.md` named like a task's notes, with no task file of its ID beside it. | the `.md`, then each task file with its ID elsewhere, in [tree order](#tree-order) | its ID | `reason`, below; `code` when `reason` is `unreadable` | `remove` when `reason` is `empty` or `linked`; else `null` |
| `duplicate-id` | manual | ID that more than one task file has. | every copy, in tree order | the one ID | `identical`: whether every copy is the same, byte for byte | `null` |
| `cycle` | manual | group of tasks that block each other: a strongly connected component of the [dependency graph](design-spec.md#dependencies). | none | one cycle in the group: the shortest through its lowest ID, from that ID back to it, e.g. `[12, 15, 12]` (12 is blocked by 15, which is blocked by 12) | `group`: every ID in the group, ascending | `null` |
| `unusable-file` | manual | task file that is [unusable](design-spec.md#file-validity), other than in an older format (`old-format`). | the task file | its ID, from the filename | `error`: the error a write that needed the file would fail with — `corrupt` (with its full `problems` or `detail`), `unsupported-format` (a format this binary doesn't know), or `io` | `null` |
| `nested-tree` | manual | `koan.json` in a folder other than the root: another tree's metadata, inside this one. | the file | none | — | `null` |
| `skipped-entry` | manual | entry that is not hidden, and that the walk skips although it may hold part of the tree: a symlink, or an entry with a folder's or task file's name but the wrong type (see [Walking the tree](design-spec.md#walking-the-tree)). | the entry | none | `reason`, below | `null` |
| `stray-entry` | informational | entry that is not hidden and whose name matches neither the folder-name nor the task-filename rule (e.g. `notes.txt`, `42.md~`), other than the root's `koan.json` and a nested tree's. | the entry | none | — | `null` |
| `unreadable-folder` | manual | folder that can't be listed. Its entries are not looked at. | the folder | none | `code`: the symbolic OS error | `null` |
| `case-clash` | manual | set of sibling folders whose names differ only in case, e.g. `proj` and `Proj` — possible only on a case-sensitive filesystem, and made outside koan, whose [path walk](#path-walk) refuses them. On a case-insensitive one (macOS's default) they would be one folder. | each folder, in tree order | none | — | `null` |

Kind by kind:

- **`orphan-notes`** — `reason` says what the notes are, and whether `repair` can remove them without losing anything:
  - `empty`: zero bytes. Holds nothing.
  - `linked`: the same file (same device and inode) as the notes of a task with this ID elsewhere — what an interrupted [`move`](#move) leaves. Its text is kept at the task.
  - `task-elsewhere`: a task with this ID exists elsewhere, and its notes are a different file — e.g. an editor saved the old `.md` after a move. `suggest` says to merge the text into that task's notes.
  - `no-task`: no task has this ID — e.g. a [`delete`](#delete) was interrupted, or the task was removed by hand. The text may be the user's only copy, so it is left to them.
  - `unreadable`: the `.md` can't be looked at — e.g. its folder can be listed but not searched; `code` is the symbolic OS error. What it holds is unknown, so it is left to the user. A `.md` gone since the walk is not reported.
- **`cycle`** — the graph is built as for the [cycle check](#block): from usable task files only, with an ID's edges the union of every copy's. A cycle is reported once per group, not once per cycle, since a group can hold more cycles than tasks. Removing any one blocker on the example cycle breaks that cycle; `suggest` gives one [`unblock`](#unblock) that does.
- **`nested-tree`** — reported as the cause. The nested tree's tasks and folders are still walked as part of this tree, so its symptoms (duplicate IDs, IDs above `last_id`) are reported under their own kinds. The nested `koan.json` itself is not also a `stray-entry`.
- **`skipped-entry`** — `reason` is `symlink` for a symbolic link, whatever its name and whatever it leads to: koan never follows one, so a folder linked into the tree is not part of it. It is `type` when the name is a folder's or a task file's (or its notes') but the entry's type is wrong, e.g. a directory named `42.json`, or a `.md` that is a folder. A root `koan.json` that is not a regular file is `metadata-unusable` instead.
- **`stray-entry`** — a file or folder koan has no use for: a user's own file, or an editor's backup. koan ignores it, safely, so it is reported only when asked for.
- **`unusable-file`** — the full diagnosis that the [`unusable-file`](#warning-kinds) warning leaves out. A task file whose filename ID differs from its `id` field is reported here, as `corrupt`.
- **`migration-pending`** and **`old-format`** — not damage `repair` can fix: converting formats is [`migrate`](#migrate)'s job alone (see the design spec's [Migrations](design-spec.md#migrations)), so `suggest` says to run `koan migrate`, after committing the tree when it is a git repository. While a step is pending, the task files it covers are `old-format`, and `count` says how many `migrate` has to convert. With no step pending, an `old-format` file came from a merge, from a copy made by a binary that hadn't migrated yet, or from a place an earlier `migrate` couldn't read. `doctor` reads an `old-format` file's `schema` only, never checking it against its own format's rules: that check is `migrate`'s. So `suggest` also says that a file `migrate` lists under `unconverted` breaks those rules, and no step can read it: a person fixes or removes it.
- **`state-missing`** — the tree is there, but this machine has no ID counter for it: the state file was lost, the tree was copied here without [`init`](#init), or the config was pointed at it by hand. Rebuilding it is on-request because of what the rebuilt `last_id` can't know (see the design spec's [Diagnosis and repair](design-spec.md#diagnosis-and-repair)).
- **`id-above-last-id`** and **`state-missing`** — every task file counts, usable or not, by the ID in its filename: that is the ID it occupies (see [Tasks](design-spec.md#tasks)).

### Finding schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "finding",
  "type": "object",
  "required": ["kind", "class", "count", "truncated", "items"],
  "properties": {
    "kind": { "type": "string", "description": "One of the Finding kinds; callers ignore unknown values." },
    "class": { "type": "string", "enum": ["auto", "on-request", "manual", "informational"], "description": "The kind's repair class (see Diagnosis and repair)." },
    "count": { "type": "integer", "minimum": 1, "description": "How many items the kind has, including any not listed." },
    "truncated": { "type": "boolean", "description": "True when items lists fewer than count." },
    "items": { "type": "array", "minItems": 1, "items": { "$ref": "finding-item" } }
  },
  "additionalProperties": false,
  "allOf": [
    { "if": { "properties": { "kind": { "enum": ["metadata-unusable", "state-unusable", "migration-pending", "unusable-file"] } } }, "then": { "properties": { "items": { "items": { "required": ["error"] } } } } },
    { "if": { "properties": { "kind": { "enum": ["state-missing", "id-above-last-id"] } } }, "then": { "properties": { "items": { "items": { "required": ["last_id"] } } } } },
    { "if": { "properties": { "kind": { "const": "metadata-missing" } } }, "then": { "properties": { "items": { "items": { "required": ["migration"] } } } } },
    { "if": { "properties": { "kind": { "enum": ["orphan-notes", "skipped-entry"] } } }, "then": { "properties": { "items": { "items": { "required": ["reason"] } } } } },
    { "if": { "properties": { "kind": { "const": "duplicate-id" } } }, "then": { "properties": { "items": { "items": { "required": ["identical"] } } } } },
    { "if": { "properties": { "kind": { "const": "cycle" } } }, "then": { "properties": { "items": { "items": { "required": ["group"] } } } } },
    { "if": { "properties": { "kind": { "const": "unreadable-folder" } } }, "then": { "properties": { "items": { "items": { "required": ["code"] } } } } }
  ]
}
```

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "finding-item",
  "type": "object",
  "required": ["paths", "ids", "action", "suggest"],
  "properties": {
    "paths": { "type": "array", "items": { "type": "string" }, "description": "Filesystem paths involved; meaning per kind (see Finding kinds)." },
    "ids": { "type": "array", "items": { "type": "integer" }, "description": "Task IDs involved; order and meaning per kind (see Finding kinds)." },
    "action": { "type": ["string", "null"], "description": "What repair does to this item: remove, raise-last-id, remove-reference, create-metadata, or create-state; null when repair leaves it to a person. In repair's repaired list, what it did." },
    "suggest": { "type": ["string", "null"], "description": "What a person could do, for humans. Not part of the contract." },
    "reason": { "type": "string", "description": "For orphan-notes: empty, linked, task-elsewhere, no-task, or unreadable. For skipped-entry: symlink or type." },
    "code": { "type": "string", "description": "For unreadable-folder, and orphan-notes with reason unreadable: the symbolic OS error (e.g. EACCES)." },
    "identical": { "type": "boolean", "description": "For duplicate-id: whether every copy is the same, byte for byte." },
    "group": { "type": "array", "items": { "type": "integer" }, "description": "For cycle: every ID in the group, ascending." },
    "last_id": { "$ref": "state-file#/properties/last_id", "description": "For state-missing and id-above-last-id: the last_id repair writes." },
    "migration": { "$ref": "root-file#/properties/migration", "description": "For metadata-missing: the migration step repair records in the koan.json it creates." },
    "error": { "$ref": "error", "description": "For metadata-unusable, state-unusable, migration-pending, and unusable-file: the error an operation that needed the file would fail with." }
  },
  "additionalProperties": false
}
```

## Root states

Whether read and write operations can run against this machine's root is one of four states. Finding the root starts with locating the config; when that is impossible, operations that require a usable root fail with `environment` before any state applies (see [Config file](design-spec.md#config-file)).

| State | Holds when | Operations that require a usable root fail with |
|---|---|---|
| ***Not initialized*** | Something is missing: the config, the root it names (the path must lead, through symlinks, to a directory), `koan.json` in that root, or this machine's [state file](design-spec.md#state-file) for that root. The state file counts only once `koan.json` is usable and current (see below). | `not-initialized`, with `missing` naming the first absent piece. Remedy depends on `missing` — see below. |
| ***Initialized, not usable*** | The config, the root, and `koan.json` are there, but the config is unusable; or, with `koan.json` usable and current, the state file is; or `koan.json` is: unreadable, `corrupt` (by its own format's rules, when it is in an older one), in a format this binary doesn't know, or recording a [migration step](design-spec.md#migrations) past this binary's latest. | `corrupt` or `unsupported-format`, or `io` if the file is unreadable. Remedy: repair the file or its permissions, or use a binary that supports the format. [`doctor`](#doctor) reports `koan.json`'s problem in full. |
| ***Needs migration*** | The config, the root, and `koan.json` are there, the config is usable, and `koan.json` is valid but behind, whatever the state file's state: in an [older format](design-spec.md#format-versions), by whose own rules it is valid; or in this binary's format, passing every check in [File validity](design-spec.md#file-validity), and recording a step behind this binary's latest. | `migration-pending`, with the step recorded and the latest. Remedy: run [`migrate`](#migrate), after committing the tree when it is a git repository (see the design spec's [Migrations](design-spec.md#migrations)). |
| ***Usable*** | Nothing is missing, the config, `koan.json`, and the state file all pass every check in [File validity](design-spec.md#file-validity), and `koan.json` records this binary's latest step. | — |

Remedies for *not initialized*, by `missing`:

- **`config`** — nothing is set up on this machine. Run [`init`](#init).
- **`root`** — the config names a root that isn't there. Check the path first (an unmounted drive, a moved folder). Run `init` with `replace_config` only if a new or different tree is really intended: on an unmounted drive's mount point it would create a fresh empty tree.
- **`metadata`** — the root exists but has lost its `koan.json`. Run [`doctor`](#doctor) to see the tree's state, then [`repair`](#repair) naming `metadata-missing`, which rebuilds it. `init` refuses (the config exists, and the root isn't empty).
- **`state`** — the tree is there, but this machine has no ID counter for it: no state file, or one that names another root. Run [`doctor`](#doctor), then [`repair`](#repair) naming `state-missing`, which rebuilds it from the highest ID in the tree (see [Finding kinds](#finding-kinds) for the risk). On a machine the tree was just copied to, [`init`](#init) writes it.

*Initialized* is about presence; *usable* additionally about content. A file that exists but is unusable never makes a root *not initialized*. (A state file that is valid but names another root is not this root's file at all: for this root it is absent.) In particular, a config that exists but is unusable — it doesn't parse, or names a root in an illegal [form](design-spec.md#root-path) — leaves the root *initialized, not usable*, even though no root can be read from it; operations fail with `corrupt`.

A root that *needs migration* is fully there: only its formats are behind. It is not *usable*, so that no operation reads a tree whose task files it would skip as `unsupported-format`: a `frontier` that left out every task in the older format, with only warnings to say so, would be worse than an error. [`version`](#version), [`info`](#info), [`doctor`](#doctor), and [`migrate`](#migrate) don't require a usable root, and run.

The state file is looked at last, once `koan.json` is usable and current: a root that *needs migration* says so whatever the state file's state. A `koan.json` in an older format holds `last_id` itself, so such a tree may have no state file yet, and `migrate`, the remedy, is what writes it.

## Shared rules

### Tree order

The order koan uses whenever it lists folders or tasks by location:

1. By folder path, a parent before its children, siblings by name: `/`, `/infra`, `/proj`, `/proj/travel`, `/proj-b`. This is *not* a plain string sort of the paths (which would put `/proj-b` before `/proj/travel`, since `-` sorts before `/`).
2. Within a folder, tasks by `id`, lowest first.

### Path walk

How an operation checks a folder path given as input (e.g. `folder`). Entries are checked from the root down, one segment at a time, stopping at the first that is not a plain directory:

- **Differs only in case** — no entry has the segment's exact name, but one has it in another case (e.g. `/Proj` where `proj` exists) → `conflict` (`rule`: `case-clash`). Checked by listing the parent, so a path means the same on a case-insensitive filesystem as on a case-sensitive one.
- **Missing** → `not-found` (`folders`: that folder path) — or, for `create-folder` with `parents`, created.
- **Present but not a directory, or a symlink** → `corrupt` (`reason`: `unexpected-file`). Symlinks are never followed.
- **A directory** → continue to the next segment.

So a folder "exists" only if every entry on its path is a plain directory, not a symlink.

### Narrowing tasks

[`frontier`](#frontier) and [`list`](#list) share these input fields, which narrow the tasks they return:

- **`tags_any`** — keep only tasks with at least one of these tags.
- **`tags_all`** — keep only tasks with every one of these tags. Given with `tags_any`, a task must pass both.
- **`limit`** — return at most this many tasks: the first ones in the operation's order. `0` returns none, for the count alone.
- **`fields`** — return each task as a [Task projection](#task-projection) holding only these fields, in the order the [Task view](#task-view) lists them. `id` is always included, named or not, so any task returned can be followed up with [`show`](#show).

`list` also filters by `readiness` (see [`list`](#list)), a scope rule of its own. [`why`](#why) takes only `fields`, for its `tasks`; the rest of this section is about `frontier` and `list`.

They apply in this order, after the tree is read and every warning recorded:

1. **Scope** — `folder` and `recursive`, and which readiness the operation returns (`frontier`: ready; `list`: its `readiness`).
2. **Filters** — `tags_any`, then `tags_all`.
3. **Order** — the operation's own, unchanged.
4. **Count** — `total` is the number of tasks left.
5. **Limit** — the first `limit` of them; `truncated` says whether any were cut.
6. **Fields** — each task projected to `fields`.

Narrowing changes only which tasks are returned and how much of each. It never changes the work done, readiness, the order, or the **warnings**: those are exactly the ones the same call without these fields reports, including those about tasks the filters or the limit leave out. So a warning is never lost to narrowing. `list`'s `folders` are not narrowed.

Every result of `frontier` and `list` carries `total` and `truncated`, whether or not `limit` is given, so it has one shape: without `limit`, `total` is the number of tasks returned and `truncated` is `false`. A caller that got N tasks can tell "there are N" from "there are more, and you got N".

### Undo

[`delete`](#delete) and [`delete-folder`](#delete-folder) remove files for good: koan keeps no trash and no history. Undo comes from git, when the root is a repository (see *Syncing and committing are allowed* in [Assumptions](design-spec.md#assumptions)), and reaches back only to the last commit — koan never commits. Restoring from git is an [outside change](design-spec.md#assumptions):

- **Restore only the removed paths.** Restoring the whole tree would undo every other change made since that commit. It can't make koan reuse an ID, though: `last_id` is kept outside the tree, in the [state file](design-spec.md#state-file), and no git command moves it. A delete not yet committed is undone with `git restore -- proj/travel`; a committed one from a commit that still has the files, usually the parent of the one that removed them: `git restore --source=<commit>^ -- proj/travel`. A task is two paths, `42.json` and `42.md`.
- **References don't come back.** The delete removed the task's ID from its dependents' `blocked_by`, and restoring their files too would undo any other change to them since. Re-[`block`](#block) the `dependents` the delete reported instead.
- **What koan would have checked is left to [`doctor`](#doctor):** a restored task's `blocked_by` may name tasks removed since (`dangling-reference`, which [`repair`](#repair) removes), and its edges may close a cycle added while it was gone (`cycle`, which a person breaks).

## Shared schemas

### Task

Every operation that returns a task returns it in this shape: exactly the [task file schema](design-spec.md#task-file-schema), plus two keys saying where the task lives.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path"],
  "properties": {
    "schema": { "$ref": "task-file#/properties/schema" },
    "id": { "$ref": "task-file#/properties/id" },
    "title": { "$ref": "task-file#/properties/title" },
    "priority": { "$ref": "task-file#/properties/priority" },
    "created_at": { "$ref": "task-file#/properties/created_at" },
    "completed_at": { "$ref": "task-file#/properties/completed_at" },
    "updated_at": { "$ref": "task-file#/properties/updated_at" },
    "blocked_by": { "$ref": "task-file#/properties/blocked_by" },
    "tags": { "$ref": "task-file#/properties/tags" },
    "extra": { "$ref": "task-file#/properties/extra" },
    "folder": { "$ref": "folder-path", "description": "The folder holding the task." },
    "notes_path": { "type": "string", "description": "Absolute filesystem path of the task's .md, for opening in an editor. Built from the root path as stored (see Root path)." }
  },
  "additionalProperties": false
}
```

### Task view

A [Task](#task) plus its derived readiness. Returned by read operations that report readiness (`show`, `frontier`, `list`).

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task-view",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path", "readiness", "blocking"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "updated_at": { "$ref": "task#/properties/updated_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "readiness": { "type": "string", "enum": ["ready", "blocked", "done"], "description": "Derived per Dependencies (see Dependencies)." },
    "blocking": {
      "type": "array",
      "items": { "$ref": "task-file#/properties/id" },
      "uniqueItems": true,
      "description": "IDs in blocked_by that currently block the task — open, missing, unusable, or duplicated — in ascending order. Non-empty exactly when readiness is blocked."
    }
  },
  "additionalProperties": false,
  "allOf": [
    { "if": { "properties": { "readiness": { "const": "blocked" } } }, "then": { "properties": { "blocking": { "minItems": 1 } } }, "else": { "properties": { "blocking": { "maxItems": 0 } } } },
    { "if": { "properties": { "readiness": { "const": "done" } } }, "then": { "properties": { "completed_at": { "type": "string" } } }, "else": { "properties": { "completed_at": { "type": "null" } } } }
  ]
}
```

### Task projection

Some of a [Task view](#task-view)'s fields — always including `id` — in the Task view's order. Returned by [`frontier`](#frontier) and [`list`](#list) in place of Task views when `fields` is given (see [Narrowing tasks](#narrowing-tasks)); each field present has exactly its Task view meaning and value.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task-projection",
  "type": "object",
  "required": ["id"],
  "properties": {
    "schema": { "$ref": "task-view#/properties/schema" },
    "id": { "$ref": "task-view#/properties/id" },
    "title": { "$ref": "task-view#/properties/title" },
    "priority": { "$ref": "task-view#/properties/priority" },
    "created_at": { "$ref": "task-view#/properties/created_at" },
    "completed_at": { "$ref": "task-view#/properties/completed_at" },
    "updated_at": { "$ref": "task-view#/properties/updated_at" },
    "blocked_by": { "$ref": "task-view#/properties/blocked_by" },
    "tags": { "$ref": "task-view#/properties/tags" },
    "extra": { "$ref": "task-view#/properties/extra" },
    "folder": { "$ref": "task-view#/properties/folder" },
    "notes_path": { "$ref": "task-view#/properties/notes_path" },
    "readiness": { "$ref": "task-view#/properties/readiness" },
    "blocking": { "$ref": "task-view#/properties/blocking" }
  },
  "additionalProperties": false
}
```

The names a caller may choose are these properties' names:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task-field",
  "type": "string",
  "enum": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path", "readiness", "blocking"]
}
```

### Folder path

A [folder path](design-spec.md#folder-paths).

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "folder-path",
  "type": "string",
  "pattern": "^/$|^(/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)+$",
  "description": "Folder path from the root, e.g. /proj/travel."
}
```

## Versioning

koan releases follow [semantic versioning](https://semver.org/). The operation input, output, partial, error, warning, and finding schemas are koan's public contract: a breaking change to any of them requires a new major version. There is no separate API version.

**Before 1.0**, the contract is not yet stable: a breaking change bumps the **minor** version instead (0.1 → 0.2), and its release says what changed. The data formats are not part of this allowance: they change only through a [migration step](design-spec.md#migrations), before 1.0 as after (see [Format versions](design-spec.md#format-versions)).

Adding a new error kind, warning kind, finding kind, or `conflict` rule is a **minor** change. Callers must therefore treat an unknown error kind as a generic failure, and ignore unknown warning kinds and finding kinds. For the same reason the published schemas type `kind` (and `rule`) as a plain string, not a closed enum; the known values are listed in this document.

Data formats are versioned separately; see the design spec's [Format versions](design-spec.md#format-versions). A release reports its format versions, and its latest migration step, via [`version`](#version).

## Setup and diagnostics

### init

Create a new tree, or attach an existing one, and make it this machine's configured root. A new tree gets a fresh `koan.json`; an existing tree — one that already contains `koan.json`, e.g. cloned or moved from another machine — is left untouched: `init` writes only this machine's files for it, the [state file](design-spec.md#state-file) and the config naming it. Afterwards the root is at least *initialized* (see [Root states](#root-states)).

**Kind:** setup. Takes no lock to create a new tree — its root may not exist until `init` creates it — and never changes an existing tree's content. When the root already exists, it takes the write lock while it reads and writes the state file, so that a write still running under an earlier config can't have its `last_id` overwritten. Does not require a usable root. Concurrent `init` runs are not supported.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "init-input",
  "type": "object",
  "required": ["root"],
  "properties": {
    "root": { "type": "string", "description": "Absolute root path, with no .. segments. Resolving relative paths or ~ is the caller's job." },
    "replace_config": { "type": "boolean", "default": false, "description": "Allow replacing an existing config." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `root` must be an absolute path with no `..` segments and no NUL character (see [Root path](design-spec.md#root-path)). It is cleaned first; every check below runs on the cleaned path, which is also what the config records. If anything exists at that path, it must lead (through symlinks) to a directory; a regular file or a dangling symlink there is `invalid-input`.

**Preconditions:**

| State of `root` | Outcome |
|---|---|
| Does not exist, parent directory exists | Created as a new tree. |
| Does not exist, parent directory missing | `not-found`. `init` never creates the root's parent directories. |
| Empty directory (hidden entries allowed, e.g. `.git`) | Initialized as a new tree. |
| Non-empty directory without `koan.json` | `conflict` (`rule`: `root-not-empty`) — koan never adopts a directory with other contents. |
| Directory containing `koan.json` | Attached as-is — also when `koan.json` is in an older format, or records a [migration step](design-spec.md#migrations) behind this binary's latest. The root then *needs migration* (see [Root states](#root-states)), and [`migrate`](#migrate), which needs the config `init` writes, is the next step. |

If a config already exists — whatever it names, and whether or not it parses — and `replace_config` is false: `conflict` (`rule`: `config-exists`). `init` never compares roots and never overwrites a config unless told to.

**Needed files:** `koan.json` in `root`, when present, checked as [File validity](design-spec.md#file-validity) describes — one in an older format against that format's own rules. `init` never reads the config; it only checks whether one exists. When attaching, it also lists every folder of the tree, for the highest ID in any task filename (a folder it can't list fails with `io`); it reads no task file. A state file already there is read, for a new tree as for an attached one: one that is usable and names `root` gives its `last_id` as a floor, and any other is replaced.

**Effects:**

- `root` is a directory containing `koan.json`. A new tree has `{"schema": 2, "migration": <latest>}`, with this binary's latest [migration step](design-spec.md#migrations), since it has nothing to convert; an existing tree's `koan.json` is unchanged, whatever its format.
- The state file names `root`. Its `last_id` is the highest of: `0`; for an attached tree, the highest ID in any task filename, and the `last_id` of a `koan.json` in an older format that holds one; and, for a new tree too, the `last_id` of a usable state file that already names `root`, so that the counter for a root never goes down. So a tree attached on this machine never has an ID issued that one of its tasks holds. (An ID whose task was deleted before the copy was made can be issued again: see [Task IDs](design-spec.md#task-ids).)
- The config names `root`. `init` creates the config directory, and any missing ancestors of it, as needed.
- With `replace_config`, the config file itself is replaced: a config that is a symlink (into a dotfiles checkout, say) becomes a regular file, and the file it pointed to is left as it was.

**Invariants at risk:** none. `init` never modifies an existing tree's content; creating a new tree produces an empty tree, which satisfies every invariant.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "init-output",
  "type": "object",
  "required": ["root", "action", "last_id"],
  "properties": {
    "root": { "type": "string", "description": "Absolute root path, as recorded in the config." },
    "action": { "type": "string", "enum": ["created", "attached"], "description": "created: a new, empty tree was created. attached: an existing tree (one already containing koan.json) was attached to this machine." },
    "last_id": { "$ref": "state-file#/properties/last_id", "description": "The last_id the state file now holds." }
  },
  "additionalProperties": false
}
```

**Errors,** in `init`'s own precedence order:

| Kind | When |
|---|---|
| `invalid-input` | `root` is not absolute, contains `..` or a NUL character, or something exists there that doesn't lead to a directory. |
| `environment` | The config can't be located (see [Config file](design-spec.md#config-file)). |
| `conflict` | (`rule`: `config-exists`) A config already exists and `replace_config` is false. |
| `not-found` | `root` does not exist and neither does its parent directory (the parent in `paths`). |
| `conflict` | (`rule`: `root-not-empty`) `root` is a non-empty directory without `koan.json`. |
| `corrupt` | `koan.json` exists but is corrupt, including not being a regular file, and breaking its own format's rules when it is in an older one. |
| `unsupported-format` | `koan.json` exists in a format this binary doesn't know, or records a migration step past this binary's latest (`field`: `migration`). |
| `busy` | `root` exists, and another write holds its write lock. |

**Warnings:** none.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "init-partial",
  "type": "object",
  "required": ["root_created", "metadata_created", "state_created"],
  "properties": {
    "root_created": { "type": "boolean", "description": "init created the root directory." },
    "metadata_created": { "type": "boolean", "description": "init created koan.json." },
    "state_created": { "type": "boolean", "description": "init wrote the state file." }
  },
  "additionalProperties": false
}
```

Present when `init` fails after creating the root directory, `koan.json`, or the state file — e.g. the config cannot be written. What it created stays; rerunning `init` with the same input completes it.

**Crash behavior:** a crash may leave some of the pieces in place and not others — e.g. a new root directory without `koan.json`, or a tree with `koan.json` and a state file but no config. `koan.json` is written first, then the state file, and the config last, so until it exists nothing else uses the root, and a partial `init` is never mistaken for a usable one. With `replace_config` naming a different root there is one window: once the state file is written and before the config is, the old config still names the old root while the state file names the new one, so the old root is *not initialized* (`missing`: `state`). Rerunning `init` finishes the switch; to stay with the old root instead, [`repair`](#repair) naming `state-missing` rebuilds its counter. Apart from possible leftover temp files — in the root, which [`doctor`](#doctor) finds as `temp-leftover`, or in the config directory, which the next `init` removes — there is nothing for `doctor` to find.

**Retry safety:** after a crash or an error with `partial`, safe: the config is written last, so an interrupted `init` did not change the config, and rerunning it with the same input finishes the job (reporting `attached` if it had already written `koan.json` — an empty tree it created itself). After a success, rerunning fails with `conflict` (`rule`: `config-exists`). A crash after the config is written — while only its temp file remains to remove — is a success, and a rerun fails the same way.

### version

Report the version and build of the koan binary, and the data format versions it supports. Describes the binary only, never a root.

**Kind:** read. Takes no lock. Does not require a usable root, so it works before `init` and whatever the root state.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "version-input",
  "type": "object",
  "properties": {},
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** none.

**Needed files:** none.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "version-output",
  "type": "object",
  "required": ["version", "commit", "commit_time", "uncommitted_changes", "go", "platform", "schemas", "migration"],
  "properties": {
    "version": { "type": "string", "description": "Release version (semver), e.g. 1.4.0." },
    "commit": { "type": "string", "description": "Source commit the binary was built from." },
    "commit_time": { "$ref": "task-file#/$defs/timestamp", "description": "Timestamp of that commit." },
    "uncommitted_changes": { "type": "boolean", "description": "True if the binary was built from a working tree with uncommitted changes, so it does not exactly match commit." },
    "go": { "type": "string", "description": "Go toolchain the binary was built with, e.g. go1.25.1." },
    "platform": { "type": "string", "description": "OS and architecture, e.g. linux/amd64." },
    "schemas": {
      "type": "object",
      "required": ["task", "root", "state"],
      "properties": {
        "task": { "type": "integer", "description": "The one task file format version this binary reads and writes." },
        "root": { "type": "integer", "description": "The one koan.json format version this binary reads and writes." },
        "state": { "type": "integer", "description": "The one state file format version this binary reads and writes." }
      },
      "additionalProperties": false
    },
    "migration": { "$ref": "root-file#/properties/migration", "description": "This binary's latest migration step: the step koan.json records once migrate has run (see Migrations)." }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | The input has any property (the schema allows none). |

**Warnings:** none.

**Partial schema:** none.

**Crash behavior:** none. `version` changes nothing.

**Retry safety:** safe.

### info

Report the state of this machine's configured root: what is configured, what exists, and whether it is usable by this binary. Diagnostic by design — it reports a root that is not initialized or not usable as state, rather than failing on it.

**Kind:** read. Takes no lock. Does not require a usable root. Inspects a fixed set of files only; it never walks the tree, so its cost does not grow with the number of tasks.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "info-input",
  "type": "object",
  "properties": {},
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** none.

**Needed files:** none. `info` inspects the config, `koan.json`, and the state file but reports any problem with them as state, not as an error.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "info-output",
  "type": "object",
  "required": ["config", "tree", "state", "initialized", "usable", "compatible", "migration_pending"],
  "properties": {
    "config": {
      "type": "object",
      "required": ["path", "state", "root"],
      "properties": {
        "path": { "type": ["string", "null"], "description": "Path of the config; null when it can't be located (see Config file)." },
        "state": { "type": "string", "enum": ["missing", "unreadable", "corrupt", "ok"], "description": "State of the config; missing also when it can't be located." },
        "root": { "type": ["string", "null"], "description": "Root path named by the config, cleaned and with ~/ expanded (the same form as notes_path); null if the config is missing, unreadable, doesn't parse, or names a root in an illegal form." }
      },
      "additionalProperties": false
    },
    "tree": {
      "type": ["object", "null"],
      "required": ["root_exists", "metadata", "schema", "migration"],
      "properties": {
        "root_exists": { "type": "boolean", "description": "Whether the root path leads, through symlinks, to a directory." },
        "metadata": { "type": "string", "enum": ["missing", "unreadable", "corrupt", "old-format", "unsupported-format", "ok"], "description": "State of koan.json, per the three-step check (see File validity): corrupt fails step 1 or 3 (or is not a regular file), or is in an older format and breaks that format's rules; old-format fails step 2 with an older version, valid by its own rules, which migrate converts; unsupported-format fails step 2 with any other version, or passes every step but records a migration step past this binary's latest. ok says nothing about a step behind it: see migration_pending." },
        "schema": { "type": ["integer", "null"], "minimum": -9007199254740991, "maximum": 9007199254740991, "description": "koan.json's schema value; set whenever step 1 passes — whether metadata ends up ok, old-format, unsupported-format, or corrupt after step 1 — otherwise null." },
        "migration": { "anyOf": [{ "$ref": "root-file#/properties/migration" }, { "type": "null" }], "description": "The migration step koan.json records: its migration when metadata is ok, or unsupported-format for a step past the latest; for old-format, what that format records (0 at schema 1, which predates migrations); otherwise null." }
      },
      "additionalProperties": false,
      "description": "State of the configured root; null when config.root is null."
    },
    "state": {
      "type": ["object", "null"],
      "required": ["path", "state", "last_id"],
      "properties": {
        "path": { "type": "string", "description": "Path of the state file, beside the config." },
        "state": { "type": "string", "enum": ["missing", "other-root", "unreadable", "corrupt", "unsupported-format", "ok"], "description": "State of the state file, per the three-step check (see File validity). other-root: it is valid but names a root other than config.root, so this root has none. missing and other-root both make the root not initialized." },
        "last_id": { "anyOf": [{ "$ref": "state-file#/properties/last_id" }, { "type": "null" }], "description": "Highest task ID issued; null unless state is ok." }
      },
      "additionalProperties": false,
      "description": "State of this machine's state file for the configured root; null when config.root is null."
    },
    "initialized": { "type": "boolean", "description": "True unless the root is not initialized (see Root states). True, with tree null, when the config exists but is unusable. A missing state file makes it false only once koan.json is ok: a root that needs migration is initialized." },
    "usable": { "type": "boolean", "description": "True when the root is usable (see Root states)." },
    "compatible": { "type": ["boolean", "null"], "description": "Whether tree.schema equals this binary's supported koan.json version (see Format versions); null when tree.schema is null." },
    "migration_pending": { "type": ["boolean", "null"], "description": "Whether the root needs migration (see Root states): koan.json is old-format, or records a step behind this binary's latest. When true, run migrate. null when tree.migration is null." }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | The input has any property (the schema allows none). |

Every problem with the root is reported as state in the output, not as an error.

**Warnings:** none.

**Partial schema:** none.

**Crash behavior:** none. `info` changes nothing.

**Retry safety:** safe.

### doctor

Report everything wrong with the tree, as [findings](#findings): what each is, and what [`repair`](#repair) would do about it, or what a person could. Changes nothing.

**Kind:** diagnostic. Takes the write lock, so that what it reports is the tree at rest (see [Diagnosis and repair](design-spec.md#diagnosis-and-repair)). Does not require a usable root: it needs the config and the root, and reports a missing or unusable `koan.json` or state file as a finding. Walks the whole tree.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "doctor-input",
  "type": "object",
  "properties": {
    "kinds": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "type": "string" }, "description": "Finding kinds to report, each listed in full. Omitted: every kind, at most 20 items each." }
  },
  "additionalProperties": false
}
```

**Additional validation:** each of `kinds` is one of the [finding kinds](#finding-kinds).

**Preconditions:** the config is usable and names a root that exists. A pending migration is not an error: it is the `migration-pending` finding, and each task file in an older format is an `old-format` one.

**Needed files:** the config, and the root directory, which `doctor` opens and locks. Nothing else is needed: a problem with `koan.json`, with the state file, or with any entry under the root, is a finding, never an error. `doctor` lists every folder; reads `koan.json`, the state file, and every task file in full; and checks each `.md` named like a task's notes that has no task file beside it, for its size and its identity (see `orphan-notes`).

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "doctor-output",
  "type": "object",
  "required": ["healthy", "findings"],
  "properties": {
    "healthy": { "type": "boolean", "description": "True when the tree has no findings of any kind but informational ones, whether or not kinds named them." },
    "findings": { "type": "array", "items": { "$ref": "finding" }, "description": "One group per kind found, but informational kinds; or only the kinds named in kinds." }
  },
  "additionalProperties": false
}
```

**Order:** as in [Findings](#findings).

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `kinds` is empty, repeats a kind, or names a kind that is not a finding kind. |
| `environment` | The config can't be located. |
| `not-initialized` | `missing`: `config` or `root`. Never `metadata` or `state`: a missing `koan.json` is the `metadata-missing` finding, and a missing state file the `state-missing` one. |
| `corrupt` | The config is corrupt. |
| `busy` | Another write holds the write lock. |

Errors are checked in the order above. Everything found after the lock is taken is a finding, not an error; `io` is still reported for the config, and for the root directory if it can't be opened or locked.

**Warnings:** none. Every problem `doctor` finds is a finding.

**Partial schema:** none.

**Crash behavior:** none. `doctor` changes nothing, and the lock it holds is released when it exits or crashes.

**Retry safety:** safe.

### repair

Apply the safe repairs: for each kind it repairs, every item whose `action` is not `null` (see [Finding kinds](#finding-kinds)). Then report what is left, as [`doctor`](#doctor) would.

**Kind:** diagnostic. Takes the write lock. Does not require a usable root: it needs the config and the root, and a usable `koan.json` and state file — or, for either, none when it is asked to create it. Walks the whole tree.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "repair-input",
  "type": "object",
  "properties": {
    "kinds": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "type": "string" }, "description": "Finding kinds to repair, each listed in full in the output. Omitted: every auto kind. Naming an on-request kind is the only way to repair it." }
  },
  "additionalProperties": false
}
```

**Additional validation:** each of `kinds` is one of the [finding kinds](#finding-kinds), and not a *manual* or *informational* one: those are never repaired, and the problem's reason says to see `doctor` — or, for `migration-pending` and `old-format`, to run [`migrate`](#migrate).

A task file in an older format that `repair` meets is left as it is and reported among the remaining findings as `old-format`, whose `suggest` says to run `migrate`.

**Preconditions:**

| `koan.json` | Outcome |
|---|---|
| usable | `repair` runs. |
| missing, and `kinds` names `metadata-missing` | `repair` runs, and creates it. |
| missing, otherwise | `not-initialized` (`missing`: `metadata`). Creating it is the user's decision. |
| in an older format, or recording a [migration step](design-spec.md#migrations) behind this binary's latest | `migration-pending`. `repair` writes only this binary's formats, and converting the tree is [`migrate`](#migrate)'s job: run it first, then `repair`. |
| present but otherwise unusable | `corrupt`, `unsupported-format`, or `io`. `repair` changes nothing in a tree whose `koan.json` it can't read, or whose format it doesn't know. `doctor` reports the problem in full, for a person to fix. |

And, for a `koan.json` that is usable or is about to be created:

| State file | Outcome |
|---|---|
| usable, and naming this root | `repair` runs. |
| missing or naming another root, and `kinds` names `state-missing` | `repair` runs, and creates it. |
| missing or naming another root, otherwise | `not-initialized` (`missing`: `state`). Raising `last_id` needs it, and rebuilding it is the user's decision. |
| unusable | `corrupt`, `unsupported-format`, or `io`. `doctor` reports the problem in full, for a person to fix. |

Naming `metadata-missing` when `koan.json` is present, or `state-missing` when the state file is, is not an error: there is nothing to repair for it.

Both are judged once, under the lock and before any repair is made, so a refusal changes nothing. A run that creates `koan.json` carries on with its other repairs whatever step the new file records: they touch only files in this binary's format.

**Needed files:** as for `doctor`, plus `koan.json`, the state file, and each task file it rewrites (a usable file, since `doctor` read its `blocked_by`).

**Effects:** for each kind it repairs, each item's `action` is applied:

- **`remove`** — the entry is removed: a `temp-leftover` file, or folder with everything in it; an `orphan-notes` `.md`.
- **`create-metadata`** — `koan.json` is created, with this binary's `schema` and the item's `migration`. The step says what `repair` found, since what the lost file recorded can't be known: this binary's latest step when no task file is in an older format, so there is nothing to convert and the root is *usable*; `0` when one is, so the root *needs migration*, and the next [`migrate`](#migrate) converts those files and records the latest step (see the design spec's [Migrations](design-spec.md#migrations)).
- **`create-state`** — the state file is created, naming this root, with the item's `last_id`: the highest ID in any task filename.
- **`raise-last-id`** — the state file's `last_id` is set to the item's `last_id`, the highest ID in any task filename. One write for every item.
- **`remove-reference`** — the missing ID is removed from the task file's `blocked_by`, and `updated_at` is set to now, as [`unblock`](#unblock) does. A copy of a duplicated ID is rewritten like any other task file: the change is to that file alone.

Afterwards, the findings that remain are reported, for every kind, as `doctor` would report them for the same `kinds`.

**Invariants at risk:** none. Each repair removes a violation and adds none: removing a reference can't close a cycle, and `last_id` only goes up.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "repair-output",
  "type": "object",
  "required": ["repaired", "healthy", "findings"],
  "properties": {
    "repaired": { "type": "array", "items": { "$ref": "finding" }, "description": "What repair changed, grouped as findings; each item's action is what was done. Empty when there was nothing to repair." },
    "healthy": { "type": "boolean", "description": "True when the tree has no findings left of any kind but informational ones." },
    "findings": { "type": "array", "items": { "$ref": "finding" }, "description": "The findings left after the repairs, as doctor reports them." }
  },
  "additionalProperties": false
}
```

**Order:** as in [Findings](#findings), for both `repaired` and `findings`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `kinds` is empty, repeats a kind, or names a kind that is not a finding kind, or a *manual* or *informational* one. |
| `environment` | The config can't be located. |
| `not-initialized` | `missing`: `config` or `root`; or `metadata`, when `koan.json` is missing and `kinds` doesn't name `metadata-missing`; or `state`, when the state file is missing, or names another root, and `kinds` doesn't name `state-missing`. |
| `corrupt` | The config, `koan.json`, or the state file is corrupt. |
| `busy` | Another write holds the write lock. |
| `unsupported-format` | `koan.json` is in a format this binary doesn't know, or records a migration step past this binary's latest (`field`: `migration`); or the state file is in a format this binary doesn't know. |
| `migration-pending` | `koan.json` is in an older format, or records a migration step behind this binary's latest. Nothing is repaired: run [`migrate`](#migrate) first. |

Errors are checked in the order above, except that `koan.json` and the state file are read only once the lock is taken, since `repair` decides on current state: their `not-initialized`, `corrupt`, `unsupported-format`, and `migration-pending` come after `busy`, `koan.json`'s before the state file's.

**Warnings:** none. Every problem `repair` finds is a finding.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "repair-partial",
  "type": "object",
  "required": ["repaired"],
  "properties": {
    "repaired": { "type": "array", "items": { "$ref": "finding" }, "description": "What repair changed before the error, grouped as in the output." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after at least one repair. Each repair listed is complete; the rest were not made. The tree is no worse than before: every repair removes a finding and adds none.

**Crash behavior:** steps run in this order, one item at a time:

1. `temp-leftover` items are removed. A crash while a temp folder is being removed leaves part of it, still a `temp-leftover`.
2. `koan.json` is created, when `kinds` names `metadata-missing` and its item's `action` is not `null`.
3. The state file is created, when `kinds` names `state-missing` and its item's `action` is not `null`.
4. `last_id` is raised.
5. `dangling-reference` items are removed, one task file at a time.
6. `orphan-notes` items are removed. Each is checked again just before: an `empty` one must still be empty, a `linked` one still the same file as its task's notes. One that changed is left, and reported among the remaining findings.

Creating the state file before raising `last_id` means a rebuilt one never needs raising. A [process crash](design-spec.md#crashes) between any two steps leaves a tree with fewer findings, and no new ones. A [system crash](design-spec.md#crashes) keeps the order of the files `repair` writes — `koan.json`, the state file, and rewritten task files are flushed like any write's — but can undo removals, which are not flushed; `doctor` then reports those items again.

**Retry safety:** safe. After `busy`, an error with `partial`, or a crash, rerunning repairs what is left. After success, rerunning repairs nothing and returns an empty `repaired`.

## Migration

### migrate

Bring the tree to this binary's formats: convert every file in an [older format](design-spec.md#format-versions) by the [migration steps](design-spec.md#migrations) from its `schema` on, then record this binary's latest step in `koan.json`. koan never runs it on its own: run it after installing a binary that reports the root *needs migration* (see [Root states](#root-states)).

**Kind:** migration. Takes the write lock, and holds it for the whole run. Does not require a usable root: it needs the config, the root, and a `koan.json` in this binary's format or an older one. Walks the whole tree.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "migrate-input",
  "type": "object",
  "properties": {
    "dry_run": { "type": "boolean", "default": false, "description": "Report what would change, and write nothing." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:**

| `koan.json` | Outcome |
|---|---|
| In this binary's format or an older one, recording a step at or behind this binary's latest | `migrate` runs. With every file already current and the latest step recorded, it changes nothing: `changed` is `false`. |
| Missing | `not-initialized` (`missing`: `metadata`). [`repair`](#repair) naming `metadata-missing` rebuilds it; then run `migrate`, if the root *needs migration*. |
| In a format this binary doesn't know, or recording a step past this binary's latest | `unsupported-format`: a newer binary wrote the tree. Nothing is converted down. |
| Corrupt, or unreadable | `corrupt` or `io`. For one in an older format, `corrupt` means it breaks that format's own rules, so no step can read it. [`doctor`](#doctor) reports the problem in full, for a person to fix. |

`migrate` issues no ID, so it doesn't need the [state file](design-spec.md#state-file) — except to write it. A `koan.json` at schema 1 holds `last_id`, which the state file holds now, and `migrate` moves it there. A state file that is missing, or names another root, is written. One that is unusable stops the run before anything is written, with its `corrupt`, `unsupported-format`, or `io`: `migrate` never overwrites a counter it can't read.

**Needed files:** the config, and `koan.json`. Relevant files, whose problems are warnings: every folder, and every task file. `migrate` converts what it can reach, and leaves the rest as it is:

- A folder that can't be listed, and a task file that can't be read.
- A task file that fails the first step of [File validity](design-spec.md#file-validity), so that its format can't be told.
- A task file in a format this binary doesn't know.

None of these stops the run or keeps the latest step from being recorded: one stray file must not leave the whole tree refused. A file in an older format that the run couldn't reach is [`doctor`](#doctor)'s `old-format` finding once it can be read, and the next `migrate` converts it.

A task file in an older format is checked against its own format's rules before any step is applied to it. One in this binary's format is checked for its `schema` only, and left as it is, byte for byte: damage in it is `doctor`'s to find.

**Effects:**

1. **Every file is read and converted, in memory.** Each task file in an older format, and `koan.json` if it is in one, has the steps from its `schema` on applied in order, and the result is checked against this binary's rules. When `koan.json` holds a `last_id`, the state file is read too, and an unusable one fails the run here, with `dry_run` as without. Nothing is written in this pass.
2. **With `dry_run`, it stops here**, and reports what the run would change.
3. **The state file is written first**, when `koan.json` is in a format that holds `last_id`: naming this root, with that `last_id`, or the `last_id` of a usable state file that already names this root if that is higher. The counter is in its new place before the file that held it is replaced, so no interruption can lose it. A state file that already says exactly this is not rewritten.
4. **Each converted task file is written**, in [tree order](#tree-order), replacing the old one. Its `.md` is untouched, and so is every field's value, `updated_at` included: a migration changes formats, not tasks.
5. **`koan.json` is written last**, converted if it was in an older format, with `migration` set to this binary's latest step. It is not rewritten when nothing about it would change.

A task file in an older format that breaks its own format's rules, or whose converted result breaks this binary's, can only come from an outside change. It is left as it is and listed in `unconverted`, and the latest step is recorded anyway: every step ran, and the file stays an `old-format` finding until a person fixes or removes it.

Afterwards the root is *usable*, unless something other than its formats keeps it from being so.

**Invariants at risk:** none. A step converts one file on its own, and changes no ID, `blocked_by`, or `last_id` (see the design spec's [Migrations](design-spec.md#migrations)), so every invariant holds after a migration exactly when it held before.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "migrate-output",
  "type": "object",
  "required": ["dry_run", "from", "to", "applied", "changed", "metadata_converted", "state_written", "tasks_converted", "unconverted", "unconverted_count"],
  "properties": {
    "dry_run": { "type": "boolean" },
    "from": { "$ref": "root-file#/properties/migration", "description": "The step koan.json recorded before the run; 0 at schema 1." },
    "to": { "$ref": "root-file#/properties/migration", "description": "This binary's latest step, which koan.json now records (with dry_run, would record)." },
    "applied": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["step", "name"],
        "properties": {
          "step": { "type": "integer", "minimum": 1 },
          "name": { "type": "string", "description": "The step's name in the design spec's table, e.g. tree-marker." }
        },
        "additionalProperties": false
      },
      "description": "The steps after from, up to to; empty when none was pending, even if files a merge brought in were converted."
    },
    "changed": { "type": "boolean", "description": "Whether any file was written (with dry_run, would be)." },
    "metadata_converted": { "type": "boolean", "description": "Whether koan.json's format was converted (with dry_run, would be). Recording a new step alone doesn't count." },
    "state_written": { "type": "boolean", "description": "Whether the state file was written, to take the last_id an older koan.json held (with dry_run, would be)." },
    "tasks_converted": { "type": "integer", "minimum": 0, "description": "How many task files were converted (with dry_run, would be)." },
    "unconverted": {
      "type": "array",
      "maxItems": 20,
      "items": {
        "type": "object",
        "required": ["path", "id", "schema", "detail"],
        "properties": {
          "path": { "type": "string" },
          "id": { "$ref": "task-file#/properties/id", "description": "From the filename." },
          "schema": { "type": "integer", "description": "The file's own format version." },
          "detail": { "type": "string", "description": "Human-readable: which rule it breaks, its own format's or, after conversion, this binary's." }
        },
        "additionalProperties": false
      },
      "description": "Task files in an older format that no step could convert, left as they are; the first 20."
    },
    "unconverted_count": { "type": "integer", "minimum": 0, "description": "How many there are, including any not listed." }
  },
  "additionalProperties": false
}
```

The converted tasks are not listed: a migration usually converts every task in the tree, and the list would land in the caller's context. [`doctor`](#doctor) lists what is left in an older format, as `old-format`.

**Order:** `applied` by step; `unconverted` in [tree order](#tree-order).

**Errors,** in `migrate`'s own precedence order:

| Kind | When |
|---|---|
| `invalid-input` | A bad field. |
| `environment` | The config can't be located. |
| `not-initialized` | `missing`: `config`, `root`, or `metadata`. |
| `corrupt` | The config is corrupt. |
| `busy` | Another write holds the write lock. |
| `corrupt` | `koan.json` is corrupt: it fails the first step of [File validity](design-spec.md#file-validity), isn't a regular file, or breaks its own format's rules. |
| `unsupported-format` | `koan.json` is in a format this binary doesn't know, or records a step past this binary's latest (`field`: `migration`). |
| `corrupt`, `unsupported-format` | The state file is unusable, and `koan.json` holds a `last_id` to move into it. |

`koan.json` and the state file are read only once the lock is taken, since `migrate` decides on current state: their `not-initialized` (`metadata`), `corrupt`, `unsupported-format`, and `io` come after `busy`. An `io` while writing ends the run with a `partial`.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A task file can't be read (`reason`: `unreadable`); fails the first step of [File validity](design-spec.md#file-validity), so its format can't be told (`reason`: `corrupt`); or is in a format this binary doesn't know (`reason`: `unsupported-format`). It is left as it is. |
| `unreadable-folder` | A folder could not be listed; the task files in it are left as they are. |

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "migrate-partial",
  "type": "object",
  "required": ["state_written", "tasks_converted"],
  "properties": {
    "state_written": { "type": "boolean", "description": "Whether the state file was written before the failure." },
    "tasks_converted": { "type": "integer", "minimum": 0, "description": "Task files written before the failure. koan.json was not, so the step is still pending." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io` for a full disk) comes after the state file or at least one task file was written, or while `koan.json` is being written. The converted files stay converted; the rest, and `koan.json`, are as they were, so a root that needed migration still does.

**Crash behavior:** the state file is written first, when it is written at all; then task files, in tree order, each flushed before it is published; `koan.json` is written last. A crash after the state file leaves the counter in both places, which is harmless: the root still *needs migration*, and the rerun finds the state file already right. A [process crash](design-spec.md#crashes) leaves some task files converted and `koan.json` as it was: the step is still pending, and the root refuses every operation that requires a usable root until `migrate` runs again. A [system crash](design-spec.md#crashes) keeps that order too, so `koan.json` never records the latest step before every file the run converted is on disk (short of a disk that ignores flushes). A leftover temp file is a `temp-leftover`, which [`repair`](#repair) removes once the migration is done.

**Retry safety:** safe, after anything. A file already converted is left as it is, and the latest step is recorded only once every file the run converts is written. After success, rerunning walks the tree again and changes nothing — unless files in an older format have arrived, or become readable, since, which it then converts.

## Folder operations

### create-folder

Create a folder, and optionally any missing parent folders.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-folder-input",
  "type": "object",
  "required": ["folder"],
  "properties": {
    "folder": { "$ref": "folder-path" },
    "parents": { "type": "boolean", "default": false, "description": "Create missing parent folders instead of failing." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:**

| State of the path | Outcome |
|---|---|
| The [path walk](#path-walk) finds every entry, including `folder`, a plain directory | Nothing to do; succeeds with `created` empty. `/` always exists. |
| It stops at `folder` itself, missing | `folder` is created. |
| It stops at a missing parent, `parents` false | `not-found` (`folders`: that parent — the outermost missing one). |
| It stops at a missing parent, `parents` true | That folder and every folder below it on the path are created, outermost first. |
| It stops at an entry that is not a directory, or is a symlink — including `folder` itself | `corrupt` (`reason`: `unexpected-file`). A symlink is never followed: creating through it would place the folder outside the tree. |

**Needed files:** the entries along `folder`'s path, checked by the [path walk](#path-walk). `create-folder` does not walk the tree.

**Effects:** `folder` exists as a directory, along with every folder above it.

**Invariants at risk:** none. Folders carry no invariants.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-folder-output",
  "type": "object",
  "required": ["folder", "created"],
  "properties": {
    "folder": { "$ref": "folder-path" },
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders this operation created, outermost first; empty if folder already existed." }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` is not a valid folder path. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | Whichever the [path walk](#path-walk) meets first: a missing parent while `parents` is false (`not-found`, `folders`: the outermost missing parent); an entry that is not a directory, or is a symlink (`corrupt`, `reason`: `unexpected-file`). |

**Warnings:** none.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-folder-partial",
  "type": "object",
  "required": ["created"],
  "properties": {
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created before the failure, outermost first." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) interrupts a `parents` chain after at least one folder was created. The created folders stay.

**Crash behavior:** creating one folder is a single atomic step. With `parents`, a crash can leave some of the missing folders created and not others. Empty folders are valid, so the tree satisfies every invariant and there is nothing for `doctor` to find.

**Retry safety:** safe. An existing folder is not an error, so rerunning with the same input completes any interrupted chain and otherwise does nothing.

### delete-folder

Permanently remove a folder and everything under it, and remove the IDs of the tasks under it from every `blocked_by` outside it. koan keeps no copy; see [Undo](#undo).

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-folder-input",
  "type": "object",
  "required": ["folder"],
  "properties": {
    "folder": { "$ref": "folder-path" },
    "recursive": { "type": "boolean", "default": false, "description": "Delete the folder even if it holds tasks or folders." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `folder` is not `/`: the root can never be deleted (see [Folders](design-spec.md#folders)).

**Preconditions:**

- `folder` exists.
- Without `recursive`, `folder` holds nothing but hidden entries and empty `.md` files, which are removed with it. A task, a folder, or any other entry — a user's file, an editor's backup, a `.md` with text but no task file — makes it non-empty, since it may be the user's only copy.
- No task under `folder` has an ID with more than one task file, anywhere in the tree. A write must know which task it removes.
- No task under `folder` has an ID above `last_id`: that state only arises from a system crash or an outside change, and removing the task would let its ID be reissued undetectably (see [Task IDs](design-spec.md#task-ids)).

The tasks under `folder` may be open or done, and their task files may be unusable: `delete-folder` needs only their filenames, never their contents.

**Needed files:** the entries along `folder`'s path ([path walk](#path-walk)); the names of every entry under `folder`, at every depth — and, without `recursive`, the type and size of each entry directly in it; and, outside `folder`, every task file whose `blocked_by` names a task under it — those are rewritten. It lists everything under `folder` first, and fails with `io` if it meets a folder there it can't list: it must know every ID it removes. If `folder` holds a task, it then walks the rest of the tree — there is no index — and fails with `io` on a folder it can't list there too, since it must find every reference; if `folder` holds none, nothing outside it is read. Relevant files: every other task file outside `folder` — one that is unusable may hold a reference that can't be removed, so it is a warning, and the reference is left for [`doctor`](#doctor).

**Effects:**

- Every task file outside `folder` whose `blocked_by` contains an ID of a task under `folder` has those IDs removed and its `updated_at` set to the current time. Every other field, the task file's `schema`, and the `.md` are unchanged.
- `folder` no longer exists, nor does anything under it: its tasks and their notes, its folders, and every other entry, hidden ones included.

Tasks that were blocked only by tasks under `folder` become ready, as they would if those tasks had been completed. References between tasks under `folder` go with it.

**Invariants at risk:**

- *No dangling references* — every reference to a removed task is removed, under the write lock, before the folder is.
- *Unique IDs*, *IDs within `last_id`* — removing tasks cannot break either; the `last_id` precondition keeps a removed ID from being reissued.
- *Acyclic* — cannot be broken: removing edges and tasks cannot create a cycle.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-folder-output",
  "type": "object",
  "required": ["folder", "folders", "ids", "dependents"],
  "properties": {
    "folder": { "$ref": "folder-path", "description": "The folder deleted." },
    "folders": { "type": "array", "minItems": 1, "items": { "$ref": "folder-path" }, "description": "Every folder removed — folder itself and each folder under it — in tree order." },
    "ids": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "The IDs of the tasks removed, in ascending order; empty if the folder held none." },
    "dependents": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Tasks outside folder whose blocked_by lost at least one of ids, in ascending order." }
  },
  "additionalProperties": false
}
```

`ids` is what to look for in git to restore a task; `dependents` is what to re-`block` after restoring one (see [Undo](#undo)).

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` is not a valid folder path, or is `/`. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | `folder` fails the [path walk](#path-walk) (`not-found`, `folders`: the outermost missing folder; or `corrupt`, `reason`: `unexpected-file`). |
| `conflict` | (`rule`: `not-empty`) `recursive` is false and `folder` holds a task, a folder, or another entry that is neither hidden nor an empty `.md`. `ids`: the tasks under `folder`, ascending (empty if it holds none). |
| `conflict` | (`rule`: `duplicate-id`) A task under `folder` has an ID with more than one task file. `ids`: every such ID, ascending. |
| `conflict` | (`rule`: `id-above-last-id`) A task under `folder` has an ID above `last_id`. `ids`: every such ID, ascending. Run [`repair`](#repair) first, which raises `last_id`. |

The `conflict` rules are checked in the order listed; the first that applies is reported.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A task file outside `folder` is unusable, so any reference it holds to a removed task can't be removed. |

Every unusable task file outside `folder` is reported, not only those known to reference a removed task: an unusable file's `blocked_by` can't be read, so any of them may be one. None is reported when `folder` holds no task, since there is then no reference to remove.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-folder-partial",
  "type": "object",
  "required": ["dependents"],
  "properties": {
    "dependents": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Tasks whose references were removed before the failure, in ascending order. The folder was not removed." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after at least one dependent was rewritten. The folder and everything under it are still in place; the rewritten dependents stay rewritten. Removing a reference to a task that still exists breaks no invariant, so the tree is valid.

**Crash behavior:** steps run in this order:

1. Each dependent is rewritten, one file at a time. A process crash here leaves some references removed and the folder in place — a valid tree.
2. `folder` is renamed to a hidden temp name in the root — one atomic step, however large the folder. From here the folder is gone from the tree, and the operation has succeeded.
3. The temp folder is removed. A process crash, or an error, here leaves a hidden leftover, which reads ignore, `doctor` reports as `temp-leftover`, and [`repair`](#repair) removes; the operation still succeeds.

A [system crash](design-spec.md#crashes) keeps this order too: each dependent is flushed before the rename. The rename and the removal are not flushed, so a system crash can undo them, bringing the folder back — a valid tree — or leaving the hidden leftover.

**Retry safety:** after `busy`, an error with `partial`, or a crash before step 2, safe: rerunning removes what is left. After success, rerunning fails with `not-found`.

### move-folder

Move a folder, and everything under it, to a new place in the tree — which also renames it. Like `mv`: into `to` if `to` is an existing folder, otherwise to the path `to`.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-folder-input",
  "type": "object",
  "required": ["folder", "to"],
  "properties": {
    "folder": { "$ref": "folder-path", "description": "The folder to move." },
    "to": { "$ref": "folder-path", "description": "An existing folder to move it into, or its new path." },
    "parents": { "type": "boolean", "default": false, "description": "Create missing folders above the new path instead of failing." }
  },
  "additionalProperties": false
}
```

**Additional validation:**

- `folder` is not `/`: the root can never be moved.
- `to` is neither `folder` nor under it: a folder can't be moved into itself.

**Preconditions:** `folder` exists. Where it goes — its **target** — depends on `to`:

| State of `to` | Target |
|---|---|
| The [path walk](#path-walk) finds `to`, a plain directory | `to` + `/` + `folder`'s name: `/proj/travel` into `/archive` is `/archive/travel`. |
| It stops at `to` itself, missing | `to`: `/proj/travel` to `/archive/travel-2025` moves and renames it. |
| It stops at a folder above `to`, missing, `parents` false | `not-found` (`folders`: the outermost missing folder). |
| It stops at a folder above `to`, missing, `parents` true | `to`. That folder and every folder below it on the path, up to but not including `to`, are created, outermost first. |
| It stops at an entry that is not a directory, or is a symlink — including `to` itself | `corrupt` (`reason`: `unexpected-file`). |

Then:

- **Target is `folder` itself** (e.g. `/proj/travel` into `/proj`): nothing to do; succeeds with `changed` false.
- **Anything exists at the target** — a folder, a file, a symlink: `conflict` (`rule`: `destination-exists`). Folders are never merged.
- **An entry at the target's place differs from it only in case** — e.g. moving `/a/Proj` into `/b`, which holds `proj`: `conflict` (`rule`: `case-clash`). So a rename that changes only case (`/proj` to `/Proj`) is refused too; move through a temporary name instead.

**Needed files:** the entries along `folder`'s and `to`'s paths, checked by the [path walk](#path-walk) — `folder` first — and the entry at the target. `move-folder` does not walk the tree and reads no task file: a task's identity is its ID, not its location, so nothing inside the folder changes.

**Effects:** `folder`, with everything under it, is at the target. Every task under it keeps its ID and fields; only its folder changes. With `parents`, any missing folders above the target exist.

**Invariants at risk:** none. `blocked_by` names tasks by ID, so moving them changes no reference.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-folder-output",
  "type": "object",
  "required": ["folder", "from", "created", "changed"],
  "properties": {
    "folder": { "$ref": "folder-path", "description": "The folder's path after the operation: the target." },
    "from": { "$ref": "folder-path", "description": "The folder's path before the operation." },
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created above the target (parents), outermost first; empty if none." },
    "changed": { "type": "boolean", "description": "True if the folder moved; false if it was already at the target." }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` or `to` is not a valid folder path, `folder` is `/`, or `to` is `folder` or under it. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | Whichever the [path walk](#path-walk) meets first, `folder`'s path before `to`'s: a missing `folder`, or a missing folder above `to` while `parents` is false (`not-found`, `folders`: every missing one, both paths together); an entry that is not a directory, or is a symlink (`corrupt`, `reason`: `unexpected-file`). |
| `conflict` | (`rule`: `destination-exists`) Something already exists at the target (`ids`: `[]`). (`rule`: `case-clash`) An entry differing from the target, or from a folder on `folder`'s or `to`'s path, only in case exists (`ids`: `[]`). |

**Warnings:** none.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-folder-partial",
  "type": "object",
  "required": ["created"],
  "properties": {
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created before the failure, outermost first. The folder did not move." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after `parents` created at least one folder. The created folders stay.

**Crash behavior:** moving the folder is one `rename`, so it is either at its old path or at the target, whole. With `parents`, a crash can leave some of the missing folders created and the folder not yet moved. Empty folders are valid, so there is nothing for `doctor` to find.

**Retry safety:** after `busy`, an error with `partial`, or a crash, safe: folders already created are not an error, and a folder not yet moved is moved. After success, rerunning fails with `not-found`, since `folder` is gone; a caller unsure whether the move happened checks the target with [`list`](#list).

## Task operations

### create

Create a new, open task.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-input",
  "type": "object",
  "required": ["title"],
  "properties": {
    "title": { "type": "string", "description": "Untrimmed; trimmed and validated per Titles (see Additional validation)." },
    "folder": { "$ref": "folder-path", "default": "/" },
    "priority": { "$ref": "task-file#/properties/priority", "default": null },
    "tags": { "$ref": "task-file#/properties/tags", "default": [] },
    "blocked_by": { "$ref": "task-file#/properties/blocked_by", "default": [] },
    "extra": { "$ref": "task-file#/properties/extra", "default": {} },
    "notes": { "type": "string", "default": "", "description": "Initial notes, written to the task's .md." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `title` is trimmed, then validated, per [Titles](design-spec.md#titles). `id`, `schema`, `created_at`, `completed_at`, and `updated_at` are never input — koan sets them.

**Preconditions:** `folder` exists; every ID in `blocked_by` names an existing task, open or done. A `blocked_by` ID that has several task files exists. Every ID in `blocked_by` is at most `last_id`: one above it only arises from a system crash or an outside change, and the new task could be given that ID and block itself (see [Task IDs](design-spec.md#task-ids)).

**Needed files:** the entries along `folder`'s path ([path walk](#path-walk)), and the task files whose filename ID is in `blocked_by`. When `blocked_by` is non-empty, `create` walks the whole tree to look its IDs up, and fails with `io` if it meets a folder it can't list; when `blocked_by` is empty, `create` does not walk the tree. Relevant files: the same task files — an ID in `blocked_by` with more than one task file is a warning, not an error.

**Effects:**

- `last_id` is incremented; the new task's `id` is the new `last_id`.
- A task exists in `folder` with the given fields, `schema` set to the supported task file format, `created_at` and `updated_at` set to now, and `completed_at` `null`.
- Its `.md` exists, containing `notes` (empty if none were given). A stray `.md` already at that path (left by an editor, or by a system crash) is replaced. If writing the `.md` fails, `create` still succeeds, with a `notes-missing` warning.

**Invariants at risk:**

- *Unique IDs* and *IDs within `last_id`* — the ID is allocated from `last_id` under the write lock. The exception is a tree where a system crash or an outside change left a task with an ID above `last_id`; see Crash behavior.
- *No dangling references* — `blocked_by` is checked against the tree under the write lock.
- *Acyclic* — cannot be broken: no task depends on the new one yet, so it cannot close a cycle.

**Output schema:** the created task, per the [Task](#task) schema.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | Any input fails validation (bad title, tag, folder path, types, non-integer literals, duplicate keys). |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | `folder` fails the [path walk](#path-walk) (`not-found`, or `corrupt` with `reason` `unexpected-file`); or an ID in `blocked_by` has no task file (`not-found`). A missing folder and missing blockers are reported in one `not-found`. |
| `corrupt`, `unsupported-format` | A needed task file is corrupt or has an unsupported `schema`; or the new task's task file already exists in `folder` (`corrupt`, `reason`: `unexpected-file`). |
| `conflict` | (`rule`: `id-above-last-id`) An ID in `blocked_by` is above `last_id`. `ids`: every such ID, ascending. Run [`repair`](#repair) first, which raises `last_id`. |
| `conflict` | (`rule`: `id-exhausted`) The next ID would exceed the [ID ceiling](design-spec.md#task-ids). |

The `conflict` rules are checked in the order listed; the first that applies is reported.

**Warnings:**

| Kind | When |
|---|---|
| `notes-missing` | The task was created but its `.md` could not be written. The operation still succeeds and returns the task. |
| `duplicate-id` | An ID in `blocked_by` has more than one task file. |

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-partial",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "type": "integer", "description": "The ID consumed from last_id. This operation created no task; retrying is safe and uses a new ID." }
  },
  "additionalProperties": false
}
```

Present only when the failure came after `last_id` was incremented. Once the task file is written, `create` cannot fail: anything that goes wrong afterwards — writing the `.md` (a `notes-missing` warning), removing a temp file (no warning; [`doctor`](#doctor) finds it as `temp-leftover`) — leaves the operation successful. So a `partial` always means this operation consumed an ID and created no task. It does not mean no task has that ID: see Crash behavior.

**Crash behavior:** after a [process crash](design-spec.md#crashes), the tree satisfies every invariant, leaving nothing for [`doctor`](#doctor) beyond a possible `temp-leftover`, because steps run in this order:

1. `last_id` is incremented. A process crash here consumes an ID without creating a task — an allowed gap.
2. The task file is created. A process crash here leaves a valid task whose `.md` is missing, which reads as empty notes.
3. The `.md` is created.

A [system crash](design-spec.md#crashes) keeps this order too: the state file is flushed before the task file is created. Only an outside change — a state file restored from a backup or rebuilt too low, or task files brought in from another copy of the tree — or a system crash on a disk that ignores flushes, can leave a task file without the `last_id` increment. The task then has an ID above `last_id`, and the next `create` issues that ID again:

- **In a different folder**, it succeeds, producing two tasks with one ID. Reads report them as `duplicate-id`.
- **In the same folder**, the existing task file blocks it: `create` fails with `corrupt` (`reason`: `unexpected-file`) and a `partial` for the consumed ID. A retry uses the next ID and succeeds.

`doctor` finds the state before reuse as `id-above-last-id`, which [`repair`](#repair) fixes by raising `last_id`, and the state after it as `duplicate-id`, which a person resolves.

**Retry safety:** after `busy`, safe — nothing happened. After an error with `partial`, safe — `partial` confirms this operation created no task. After a crash or an unclear outcome, **not** safe: the task may already exist, and retrying creates a duplicate with a new ID. Callers that retry should first check whether the task was created. See [Idempotent create](design-spec.md#idempotent-create).

### create-batch

Create one or more new, open tasks, in order, and any folders they need, with dependencies on existing tasks and on earlier tasks in the batch. All-or-nothing as far as checks go: every task is checked before any is written, and if any check fails, nothing changes.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-batch-input",
  "type": "object",
  "required": ["tasks"],
  "properties": {
    "folder": { "$ref": "folder-path", "default": "/", "description": "The folder of every task that names none. Created if missing, as every task's folder is." },
    "tasks": {
      "type": "array",
      "minItems": 1,
      "maxItems": 1000,
      "items": {
        "type": "object",
        "required": ["title"],
        "properties": {
          "ref": { "$ref": "task-file#/$defs/name", "description": "Local name for this task, for later tasks' blocked_by. Unique within the batch." },
          "title": { "type": "string", "description": "Untrimmed; trimmed and validated per Titles (see Additional validation)." },
          "folder": { "$ref": "folder-path", "description": "Defaults to the batch's folder. Created if missing, with any missing folders above it." },
          "priority": { "$ref": "task-file#/properties/priority", "default": null },
          "tags": { "$ref": "task-file#/properties/tags", "default": [] },
          "blocked_by": {
            "type": "array",
            "uniqueItems": true,
            "default": [],
            "items": {
              "oneOf": [
                { "$ref": "task-file#/properties/id" },
                { "$ref": "task-file#/$defs/name" }
              ]
            },
            "description": "IDs of existing tasks, and refs of earlier tasks in the batch."
          },
          "extra": { "$ref": "task-file#/properties/extra", "default": {} },
          "notes": { "type": "string", "default": "", "description": "Initial notes, written to the task's .md." }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

A task may have a **`ref`**: a name, local to the batch, that later tasks in it use in their `blocked_by` to wait on it. A `blocked_by` item is either an **integer**, the ID of an existing task, or a **string**, the `ref` of an earlier task in the batch. JSON already tells the two apart, so a ref needs no prefix and an ID no quoting.

- **Earlier only.** A ref names a task that comes *before* the referring one in `tasks`: blockers first, then what they block. This rules out cycles within the batch without a cycle check, and lets the tasks be written in input order with every blocker already in place (see Crash behavior).
- **Names.** A `ref` follows the [tag](design-spec.md#tags) name rule, and is unique within the batch. It is optional: a task nothing in the batch waits on needs none.
- **Never stored.** Refs exist only in the input and the output. The tasks created are ordinary tasks, and a ref means nothing in a later call.

**Additional validation:**

- Each `title` is trimmed, then validated, per [Titles](design-spec.md#titles), as in [`create`](#create).
- Each `ref` is unique within `tasks` (`field`: the later one's `/tasks/<i>/ref`).
- Each string in a `blocked_by` is the `ref` of an earlier task in `tasks` (`field`: `/tasks/<i>/blocked_by/<j>`). A ref that names a later task, the task itself, or no task in the batch is `invalid-input`; the `reason` says which. A repeated `ref` (itself `invalid-input`) names the first task that has it, so it is never reported as the task itself.
- `id`, `schema`, `created_at`, `completed_at`, and `updated_at` are never input, as in `create`.

Like every `invalid-input`, every problem in every task is reported, sorted by `field`, the first 20 listed.

**Preconditions:** every task's folder either exists or can be created: the [path walk](#path-walk) meets no entry that is not a plain directory, or is a symlink, nor one that differs from a segment only in case; and no two folders the batch would create differ only in case (e.g. `/s/JIRA-1` and `/s/jira-1`). Every integer in any `blocked_by` names an existing task, open or done, and is at most `last_id`, as for [`create`](#create). An ID with several task files exists. `last_id` plus the number of tasks is within the [ID ceiling](design-spec.md#task-ids).

**Needed files:** the entries along each distinct folder's path ([path walk](#path-walk)), up to the first missing one, and the task files whose filename ID is an integer in any `blocked_by`. When any `blocked_by` holds an integer, `create-batch` walks the whole tree once to look them up, and fails with `io` if it meets a folder it can't list; otherwise it does not walk the tree. Relevant files: the same task files — an ID with more than one task file is a warning, not an error.

**Effects:**

- Every task's folder exists, with every folder above it. Those that were missing are created.
- `last_id` is raised by the number of tasks, *n*, in one write. The tasks' IDs are the *n* IDs above the old `last_id`, in input order: the first task gets old `last_id` + 1. They are consecutive, since the write lock excludes other writes.
- Each task exists in its folder with the given fields, as [`create`](#create) would create it, except that each string in its `blocked_by` is replaced by the ID of the task with that `ref`. `blocked_by` is stored as a set of IDs, as always.
- Every task has the same `created_at` and `updated_at`: the time the batch started writing.
- Each task's `.md` exists, containing its `notes`, as for `create`, including the replacement of a stray `.md`. If writing one fails, `create-batch` still succeeds, with a `notes-missing` warning for that task.

Every folder a task names is created if missing, with any missing folders above it, as [`create-folder`](#create-folder) with `parents` does; there is no option to turn this off, since a batch is written with thought given to its structure. A typo therefore makes a folder rather than failing: `folders_created` in the output lists every folder created, so a stray one shows up there. An entry on a folder's path that is not a plain directory, or is a symlink, fails the batch before anything is written.

**Invariants at risk:**

- *Unique IDs* and *IDs within `last_id`* — the IDs are allocated from `last_id` under the write lock, all at once, as in `create`; the same exception for a task above `last_id` applies (see Crash behavior).
- *No dangling references* — integers in `blocked_by` are checked against the tree under the write lock; refs are checked against the batch before anything is written, and resolve to tasks written earlier.
- *Acyclic* — cannot be broken. No existing task depends on a new one, and within the batch a task waits only on earlier tasks, so every new edge points to an older task.

Folders carry no invariants.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-batch-output",
  "type": "object",
  "required": ["ids", "refs", "folders_created"],
  "properties": {
    "ids": {
      "type": "array",
      "minItems": 1,
      "items": { "$ref": "task-file#/properties/id" },
      "description": "The created tasks' IDs, in input order: ids[i] is tasks[i]'s."
    },
    "refs": {
      "type": "object",
      "propertyNames": { "$ref": "task-file#/$defs/name" },
      "additionalProperties": { "$ref": "task-file#/properties/id" },
      "description": "Each ref given, and the ID of the task it named. Empty when no task had a ref."
    },
    "folders_created": {
      "type": "array",
      "items": { "$ref": "folder-path" },
      "description": "The folders this operation created, in tree order. Empty when every folder existed."
    }
  },
  "additionalProperties": false
}
```

The tasks themselves are not returned: the caller wrote them, and the result of an agent's call lands in its context (see the [Parameters](#conventions) convention). [`show`](#show) returns any of them whole, `notes_path` included.

**Order:** `ids` is in input order; `folders_created` in [tree order](#tree-order), so a parent comes before its children.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | Any input fails validation: a bad title, tag, folder path, or type; a duplicate `ref`; a string in `blocked_by` that names no earlier task in the batch; more than 1,000 tasks, or none. Every problem in every task is reported. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | An entry on a folder's path is not a plain directory, or is a symlink (`corrupt`, `reason`: `unexpected-file`); or an integer in a `blocked_by` has no task file (`not-found`, `ids`: every missing one, across all tasks). A missing folder is never an error: it is created. |
| `corrupt`, `unsupported-format` | A needed task file is corrupt or has an unsupported `schema`; or a new task's task file already exists in its folder (`corrupt`, `reason`: `unexpected-file`, with a `partial`). |
| `conflict` | (`rule`: `id-above-last-id`) An integer in a `blocked_by` is above `last_id`. `ids`: every such ID, across all tasks, ascending. Run [`repair`](#repair) first, which raises `last_id`. |
| `conflict` | (`rule`: `id-exhausted`) The last of the *n* IDs would exceed the ID ceiling. Checked before `last_id` is raised, so nothing is consumed. |
| `conflict` | (`rule`: `case-clash`) A folder on a task's folder path differs only in case from an entry already there, or two folders the batch would create differ only in case (`ids`: `[]`). Checked before anything is written. |

The `conflict` rules are checked in the order listed; the first that applies is reported.

Every row but the `corrupt` for an existing task file is found before anything is written. That one, and `io`, can occur partway through the writes, and come with a `partial` when anything had been written.

**Warnings:**

| Kind | When |
|---|---|
| `notes-missing` | A task was created but its `.md` could not be written. One warning per such task. |
| `duplicate-id` | An integer in a `blocked_by` has more than one task file. One warning per ID, however many tasks name it. |

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-batch-partial",
  "type": "object",
  "required": ["folders_created", "consumed", "ids", "refs"],
  "properties": {
    "folders_created": {
      "type": "array",
      "items": { "$ref": "folder-path" },
      "description": "The folders created before the failure, in tree order. They stay."
    },
    "consumed": {
      "type": "array",
      "items": { "$ref": "task-file#/properties/id" },
      "description": "Every ID raised from last_id for the batch, ascending: consumed[i] was tasks[i]'s. Empty when the failure came before last_id was raised."
    },
    "ids": {
      "type": "array",
      "items": { "$ref": "task-file#/properties/id" },
      "description": "The tasks created before the failure: the first len(ids) tasks, in input order. May be empty."
    },
    "refs": {
      "type": "object",
      "propertyNames": { "$ref": "task-file#/$defs/name" },
      "additionalProperties": { "$ref": "task-file#/properties/id" },
      "description": "The refs of the tasks in ids, and their IDs."
    }
  },
  "additionalProperties": false
}
```

Present only when the failure came after something was written: a folder created, or `last_id` raised. The tasks are written in input order, and a failure stops the batch, so the tasks created are always a prefix of `tasks`: `ids` are tasks 0 to *k*−1, and tasks *k* onwards were not created; their IDs, the rest of `consumed`, are allowed gaps. As in `create`, once a task's file is written that task is created: a failure writing its `.md` is a `notes-missing` warning, not a failure.

**Crash behavior:** after a [process crash](design-spec.md#crashes), the tree satisfies every invariant, leaving nothing for [`doctor`](#doctor) beyond a possible `temp-leftover`, because steps run in this order:

1. Missing folders are created, in tree order. A crash here leaves some of them created, and empty folders are valid.
2. `last_id` is raised by *n*. A crash here consumes *n* IDs without creating a task — allowed gaps.
3. For each task in input order: its task file is created, then its `.md`. A crash here leaves the tasks before it created, the one in progress either created (with its `.md` possibly missing, which reads as empty notes) or not, and the rest not. Each created task's blockers exist: refs name earlier tasks, which were written first.

A [system crash](design-spec.md#crashes) keeps the order of steps 2 and 3, since the state file and each task file are flushed before the next step. Creating a folder is not flushed (see [Crashes](design-spec.md#crashes)), so a system crash can lose a new folder and, with it, the tasks written into it: their IDs become gaps, and a task elsewhere that a lost one blocked is left with a `dangling-reference`, which `doctor` finds and [`repair`](#repair) removes. The other exception is the one [`create`](#create) describes: an outside change, or a disk that ignores flushes, can leave tasks with IDs above `last_id`, which the batch's IDs may then collide with. A collision in the same folder fails the batch at that task with `corrupt` and a `partial`; in a different folder it produces a `duplicate-id`. `doctor` finds both, as for `create`.

**Retry safety:** after `busy`, safe — nothing happened. After an error before the writes (every kind but those with a `partial`), safe — nothing changed. After an error with `partial`, rerunning the whole batch is safe only if `ids` is empty: folders already created are not an error, and the IDs are consumed afresh. Otherwise it is **not** safe: it creates the tasks in `ids` again, with new IDs. Rerun only the tasks not created — `tasks` from index `len(ids)` on — with each string in their `blocked_by` that names a created task replaced by its ID from `partial.refs`. After a crash or an unclear outcome, **not** safe, as for `create`: check which tasks exist first, e.g. by title with [`list`](#list). See [Idempotent create](design-spec.md#idempotent-create), which would cover a batch with one key.

### show

Return one task by ID: its fields, where it lives (including `notes_path`, for opening the notes in an editor), and its readiness.

**Kind:** read. Takes no lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "show-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** a task with `id` exists.

**Needed files:** every task file whose filename ID is `id`. There is no index, so `show` walks the whole tree — which is also the only way to find duplicates — and fails with `io` if it meets a folder it can't list. Its scope is the whole tree, so every copy of `id` is returned. Relevant files, whose problems are warnings: if the task is open, the task files of every ID in its `blocked_by`. `show` does not open the `.md`; it only reports `notes_path`.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "show-output",
  "type": "object",
  "required": ["tasks"],
  "properties": {
    "tasks": {
      "type": "array",
      "minItems": 1,
      "description": "The task with the requested ID: one item normally; every copy, in tree order, when the ID is duplicated.",
      "items": { "$ref": "task-view" }
    }
  },
  "additionalProperties": false
}
```

**Order:** copies of a duplicated ID in [tree order](#tree-order).

Each item is a [Task view](#task-view). Notes are not included; `notes_path` locates them, so output stays small whatever the notes' size. When the ID is duplicated, each copy's readiness is derived from its own `blocked_by`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | A task file with ID `id` is unusable — including one copy of a duplicated ID. |

**Warnings:**

| Kind | When |
|---|---|
| `duplicate-id` | `id` has more than one task file; or, for an open task, an ID in its `blocked_by` does. |
| `dangling-reference` | The task is open and an ID in its `blocked_by` has no task file. |
| `unusable-file` | The task is open and a blocker's task file is unusable; the blocker counts as blocking. |

**Partial schema:** none.

**Crash behavior:** none. `show` changes nothing.

**Retry safety:** safe.

### done

Mark a task done by setting its `completed_at` to the current time. Marking an already done task changes nothing.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "done-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** exactly one task file has ID `id`. The task may be open or done, and its blockers may be open — a task can be marked done at any time (see [Dependencies](design-spec.md#dependencies)).

**Needed files:** every task file whose filename ID is `id`. There is no index, so `done` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. No relevant files beyond that: `done` reads neither the task's blockers nor its dependents.

**Effects:**

- If the task is open: its `completed_at` and `updated_at` are set to the current time. Every other field, the task file's `schema`, and the `.md` are unchanged.
- If the task is already done: nothing changes; `completed_at` keeps its original value.

Marking a task done can make its dependents ready. Readiness is always derived when read (see [Dependencies](design-spec.md#dependencies)), so no other file is rewritten.

**Invariants at risk:** none. `completed_at` takes part in no invariant.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "done-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path", "changed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "updated_at": { "$ref": "task#/properties/updated_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "changed": { "type": "boolean", "description": "True if this operation marked the task done; false if it was already done." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `changed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; [`doctor`](#doctor) reports the copies, for a person to resolve first. |

**Warnings:** none.

**Partial schema:** none. `done` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible `temp-leftover`, which [`repair`](#repair) removes.

**Retry safety:** safe. Rerunning on a task that is now done changes nothing and returns `changed: false`.

### reopen

Reopen a done task by clearing its `completed_at`. Reopening an already open task changes nothing. The counterpart of [`done`](#done).

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "reopen-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** exactly one task file has ID `id`. The task may be open or done.

**Needed files:** every task file whose filename ID is `id`. There is no index, so `reopen` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. No relevant files beyond that: `reopen` reads neither the task's blockers nor its dependents.

**Effects:**

- If the task is done: its `completed_at` is set to `null` and its `updated_at` to the current time. Every other field, the task file's `schema`, and the `.md` are unchanged.
- If the task is already open: nothing changes.

Reopening a task can make its dependents blocked again, and the reopened task itself may be ready or blocked. Readiness is derived when read (see [Dependencies](design-spec.md#dependencies)), so no other file is rewritten.

**Invariants at risk:** none. `completed_at` takes part in no invariant; *Acyclic* already holds for done tasks, so reopening one cannot expose a cycle.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "reopen-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path", "changed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "updated_at": { "$ref": "task#/properties/updated_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "changed": { "type": "boolean", "description": "True if this operation reopened the task; false if it was already open." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `changed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; [`doctor`](#doctor) reports the copies, for a person to resolve first. |

**Warnings:** none.

**Partial schema:** none. `reopen` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible `temp-leftover`, which [`repair`](#repair) removes.

**Retry safety:** safe. Rerunning on a task that is now open changes nothing and returns `changed: false`.

### block

Add one or more blockers to a task's `blocked_by`. All-or-nothing: every blocker is checked before anything is written, and if any check fails, nothing changes.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "block-input",
  "type": "object",
  "required": ["id", "blockers"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task to block." },
    "blockers": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "IDs to add to the task's blocked_by." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `blockers` must not contain `id` — a task blocking itself is detectable from the input alone, so it is `invalid-input`, not a `conflict`.

**Preconditions:**

- Exactly one task file has ID `id`. The task may be open or done.
- Every **new** blocker — an ID in `blockers` not already in the task's `blocked_by` — names an existing task, open or done. A blocker ID with several task files exists.
- Adding the new blockers creates no cycle.

Blockers already present are no-ops: they are neither checked for existence nor for cycles. A dangling or cyclic blocker already in `blocked_by` (after a system crash or an outside change) is left for [`doctor`](#doctor), which reports it as `dangling-reference` or `cycle`.

**Needed files:** The whole tree is walked (there is no index); a folder that can't be listed fails with `io`, since a write must *prove* the result acyclic. Needed:

- the task file(s) with ID `id`;
- every task file reachable from the new blockers by following `blocked_by`, stopping at `id` — the cycle check reads exactly these. The walk follows every task on its path, **open or done** (*Acyclic* holds for both). An unusable file on the way could hide a cycle, so it is an error. A duplicated ID on the way is followed through every copy (the graph is keyed by ID; see [Dependencies](design-spec.md#dependencies)). `id` itself is never expanded, so nothing beyond `id` is read. Every other reachable task file is read, even when a cycle is found early: which files are needed depends only on the graph, not on the order of any search.

Relevant files: the new blockers' own task files — a blocker ID with more than one task file is a `duplicate-id` warning. A `blocked_by` ID further down the chain that names no task has no edges, so cannot lie on a cycle; it is skipped silently.

How the cycle check decides: adding "`id` is blocked by B" creates a cycle exactly when `id` is already reachable from B by following `blocked_by`. Each new edge starts at `id`, so no cycle can pass through two of them: each blocker is judged on its own, and the result doesn't depend on their order. Every offending blocker is found, not just the first. For each, the cycle reported is the **shortest**, ties broken by the lexicographically smallest sequence of IDs (IDs compared as numbers); it is written starting at `id`, then the blocker, then the path back.

**Effects:**

- The task's `blocked_by` becomes `blocked_by` ∪ `blockers`. Blockers already present are left as they are.
- If `blocked_by` changed, `updated_at` is set to the current time; if not, the task file is not rewritten.
- Every other field, the task file's `schema`, and the `.md` are unchanged.

The task may become blocked; readiness is derived when read (see [Dependencies](design-spec.md#dependencies)), so no other file is rewritten.

**Invariants at risk:**

- *Acyclic* — the cycle check above, under the write lock.
- *No dangling references* — every blocker is confirmed to exist, under the write lock.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "block-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path", "added"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "updated_at": { "$ref": "task#/properties/updated_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "added": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "The blockers this operation added, in ascending order; empty if all were already present." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `added`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` or `blockers` is missing or invalid, `blockers` is empty or repeats an ID, or `blockers` contains `id`. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | `id`, or a blocker, has no task file (`ids`: every missing one). |
| `corrupt`, `unsupported-format` | The task file, or a task file the cycle check reaches, is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; [`doctor`](#doctor) reports the copies, for a person to resolve first. |
| `conflict` | (`rule`: `acyclic`) A blocker would create a cycle. `ids`: every offending blocker, ascending; `cycles[i]`: the shortest cycle through `ids[i]` (ties by smallest ID sequence), starting at `id`. |

**Warnings:**

| Kind | When |
|---|---|
| `duplicate-id` | A blocker ID has more than one task file. |

**Partial schema:** none. `block` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible `temp-leftover`, which [`repair`](#repair) removes.

**Retry safety:** safe. Blockers already present are no-ops and are not rechecked, so rerunning adds nothing new and returns `added: []` — whatever has happened to the tree since. After a `conflict` (`acyclic`), drop the offending blockers and rerun the rest.

### unblock

Remove one or more blockers from a task's `blocked_by`. Removing an ID that isn't there changes nothing.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "unblock-input",
  "type": "object",
  "required": ["id", "blockers"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task to unblock." },
    "blockers": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "IDs to remove from the task's blocked_by." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none. An ID in `blockers` that isn't in the task's `blocked_by` — including `id` itself — is simply not there to remove.

**Preconditions:** exactly one task file has ID `id`. The task may be open or done. The blockers need not exist: removing an ID that names no task is how a dangling reference is cleared.

**Needed files:** the task file(s) with ID `id`. There is no index, so `unblock` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. It reads no other task file: removing an edge cannot create a cycle, and the blockers' existence doesn't matter. No relevant files.

**Effects:**

- The task's `blocked_by` becomes `blocked_by` − `blockers`.
- If `blocked_by` changed, `updated_at` is set to the current time; if not, the task file is not rewritten.
- Every other field, the task file's `schema`, and the `.md` are unchanged.

The task may become ready; readiness is derived when read (see [Dependencies](design-spec.md#dependencies)), so no other file is rewritten.

**Invariants at risk:** none. Removing edges cannot create a cycle or a dangling reference.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "unblock-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path", "removed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "updated_at": { "$ref": "task#/properties/updated_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "removed": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "The blockers this operation removed, in ascending order; empty if none were present." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `removed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` or `blockers` is missing or invalid, or `blockers` is empty or repeats an ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; [`doctor`](#doctor) reports the copies, for a person to resolve first. |

**Warnings:** none.

**Partial schema:** none. `unblock` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible `temp-leftover`, which [`repair`](#repair) removes.

**Retry safety:** safe. Blockers already absent are left absent, so rerunning removes nothing more and returns `removed: []`.

### update

Change one or more of a task's user-owned fields: `title`, `priority`, `tags`, `extra`. Fields not named in the input are left unchanged.

Other fields have their own operations: `blocked_by` ([`block`](#block), [`unblock`](#unblock)), `completed_at` ([`done`](#done), [`reopen`](#reopen)), the folder ([`move`](#move)). Notes are edited directly in the `.md`. `id`, `schema`, and `created_at` never change; `updated_at` changes only as a side effect of a write.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "update-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task to update." },
    "title": { "type": "string", "description": "Replace the title. Untrimmed; trimmed and validated per Titles (see Additional validation)." },
    "priority": { "$ref": "task-file#/properties/priority", "description": "Set the priority; null clears it." },
    "tags": {
      "oneOf": [
        {
          "type": "object",
          "required": ["replace_all"],
          "properties": { "replace_all": { "$ref": "task-file#/properties/tags", "description": "The complete new tag set; [] clears it." } },
          "additionalProperties": false
        },
        {
          "type": "object",
          "minProperties": 1,
          "properties": {
            "add": { "$ref": "#/$defs/tag-list", "description": "Tags to add." },
            "remove": { "$ref": "#/$defs/tag-list", "description": "Tags to remove." }
          },
          "additionalProperties": false
        }
      ]
    },
    "extra": {
      "oneOf": [
        {
          "type": "object",
          "required": ["replace_all"],
          "properties": { "replace_all": { "$ref": "task-file#/properties/extra", "description": "The complete new extra map; {} clears it." } },
          "additionalProperties": false
        },
        {
          "type": "object",
          "minProperties": 1,
          "properties": {
            "merge": { "type": "object", "minProperties": 1, "description": "Keys to set; each replaces that key's whole value (shallow merge)." },
            "remove": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "type": "string" }, "description": "Keys to delete." }
          },
          "additionalProperties": false
        }
      ]
    }
  },
  "additionalProperties": false,
  "anyOf": [
    { "required": ["title"] },
    { "required": ["priority"] },
    { "required": ["tags"] },
    { "required": ["extra"] }
  ],
  "$defs": {
    "tag-list": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "task-file#/$defs/name" } }
  }
}
```

**Additional validation:**

- `title` is trimmed, then validated, per [Titles](design-spec.md#titles).
- `tags.add` and `tags.remove` must not share a tag; `extra.merge` and `extra.remove` must not share a key.

The schema itself enforces the rest: at least one field to change (`{"id": 42}` alone is `invalid-input`); `replace_all` standing alone; and no empty `tags`/`extra` object or empty `add`, `remove`, or `merge`. With these rules, the order in which `add`/`remove` (or `merge`/`remove`) apply doesn't matter.

**Preconditions:** exactly one task file has ID `id`. The task may be open or done.

**Needed files:** the task file(s) with ID `id`. `update` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. It reads no other task file. No relevant files.

**Effects:** only the named fields change:

- **`title`** — replaced by the trimmed title.
- **`priority`** — set to the given integer, or cleared by `null`. Absent means unchanged.
- **`tags`** — `replace_all`: becomes exactly the given set. `add`/`remove`: `tags` ∪ `add` − `remove`. Adding a tag already present, or removing one that isn't, changes nothing.
- **`extra`** — `replace_all`: becomes exactly the given map. `merge`: each given key is set to the given value, replacing any earlier value wholesale (no recursive merge into objects); `null` is an ordinary value, not a deletion. `remove`: each given key is deleted; a key that isn't present changes nothing. Key order follows [File format](design-spec.md#file-format): existing keys keep their position; new keys are appended in the order given.

`updated_at` is set to the current time when any field changes. Every other field, the task file's `schema`, and the `.md` are unchanged.

A field counts as **changed** when its new value differs from its old one *as a JSON value*: maps compare regardless of key order, numbers by numeric value, `tags` as a set. If no field changes by that comparison, the task file is **not rewritten** at all — a no-op `update` leaves the file, and its layout, untouched.

**Invariants at risk:** none. None of these fields takes part in an invariant.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "update-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path", "changed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "updated_at": { "$ref": "task#/properties/updated_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "changed": {
      "type": "array",
      "uniqueItems": true,
      "items": { "type": "string", "enum": ["title", "priority", "tags", "extra"] },
      "description": "The fields whose value changed, compared as JSON values (see Effects), in the order title, priority, tags, extra; empty if none did, in which case the file was not rewritten."
    }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `changed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | Any input fails validation — including no field to change, `replace_all` combined with another form, or `add`/`remove` (`merge`/`remove`) sharing an entry. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; [`doctor`](#doctor) reports the copies, for a person to resolve first. |

**Warnings:** none.

**Partial schema:** none. `update` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible `temp-leftover`, which [`repair`](#repair) removes.

**Retry safety:** safe. Every form is idempotent: rerunning with the same input leaves the task as it is and returns `changed: []`.

### delete

Permanently remove a task, and remove its ID from every other task's `blocked_by`. koan keeps no copy; see [Undo](#undo).

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:**

- Exactly one task file has ID `id`. The task may be open or done, and its task file may be unusable: `delete` needs only its filename, never its contents — so it is also how an unusable task is removed.
- `id` is at most `last_id`: a task above it only arises from a system crash or an outside change, and removing it would let its ID be reissued undetectably (see [Task IDs](design-spec.md#task-ids)).

**Needed files:** every task file whose filename ID is `id`, and every task file whose `blocked_by` contains `id` — those are rewritten. There is no index, so `delete` walks the whole tree, and fails with `io` if it meets a folder it can't list: it must find every reference. Relevant files: every other task file — one that is unusable may hold a reference that can't be removed, so it is a warning, and the reference is left for [`doctor`](#doctor).

**Effects:**

- Every task file whose `blocked_by` contains `id` has it removed and its `updated_at` set to the current time. Every other field, the task file's `schema`, and the `.md` are unchanged.
- The task no longer exists, nor does its `.md`.

Tasks that were blocked only by this one become ready, as they would if it had been completed: deleting a task says its work won't happen, like cancelling it.

**Invariants at risk:**

- *No dangling references* — every reference to `id` is removed, under the write lock, before the task is.
- *Unique IDs*, *IDs within `last_id`* — removing a task cannot break either; the `last_id` precondition keeps `id` from being reissued.
- *Acyclic* — cannot be broken: removing edges and a task cannot create a cycle.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-output",
  "type": "object",
  "required": ["id", "folder", "dependents"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task deleted." },
    "folder": { "$ref": "folder-path", "description": "The folder it was in." },
    "dependents": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Tasks whose blocked_by contained id, in ascending order; each had it removed." }
  },
  "additionalProperties": false
}
```

`id` and `folder` locate the task in git; `dependents` is what to re-`block` after restoring it (see [Undo](#undo)).

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it removes; [`doctor`](#doctor) reports the copies, for a person to resolve first. |
| `conflict` | (`rule`: `id-above-last-id`) `id` is above `last_id` (`ids`: `[id]`). Run [`repair`](#repair) first, which raises `last_id`. |

The `conflict` rules are checked in the order listed; the first that applies is reported.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | Another task file is unusable, so any reference it holds to `id` can't be removed. |

Every unusable task file in the tree is reported, not only those known to reference `id`: an unusable file's `blocked_by` can't be read, so any of them may be one, and each bears on whether the delete left a dangling reference.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-partial",
  "type": "object",
  "required": ["dependents"],
  "properties": {
    "dependents": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Tasks whose reference to id was removed before the failure, in ascending order. The task was not removed." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after at least one dependent was rewritten. The task, with its notes, still exists; the rewritten dependents stay rewritten. Removing a reference to a task that still exists breaks no invariant, so the tree is valid. Once the task file is removed, `delete` cannot fail: removing the `.md` afterwards is cleanup, and if it fails the orphaned `.md` is left for `doctor`, with no warning.

**Crash behavior:** steps run in this order, so a [process crash](design-spec.md#crashes) leaves a valid tree:

1. Each dependent is rewritten, one file at a time. A crash here leaves some references removed and the task in place.
2. The task file is removed. This is the moment the task is gone. Removing it before the `.md` means a failure never leaves a surviving task without its notes.
3. The `.md` is removed. A crash before this leaves an orphaned `.md`, which reads ignore and `doctor` reports as `orphan-notes`. `repair` removes it if it is empty; otherwise it is `no-task`, left to a person, since notes the user asked to delete can't be told from notes kept on purpose.

A [system crash](design-spec.md#crashes) keeps this order too: each dependent is flushed before the task file is removed. The removals are not flushed, so a system crash can undo them, bringing the task back — a valid tree — or leaving the orphaned `.md`.

**Retry safety:** after `busy`, an error with `partial`, or a crash before step 2, safe: rerunning removes what is left, and reports only the dependents it rewrote itself. After step 2, rerunning fails with `not-found`; an orphaned `.md` is left for `doctor`.

### move

Move a task into a folder. Its ID, fields, and notes go with it; moving a task to the folder it is already in changes nothing.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-input",
  "type": "object",
  "required": ["id", "to"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" },
    "to": { "$ref": "folder-path", "description": "The folder to move the task into." },
    "parents": { "type": "boolean", "default": false, "description": "Create to, and any missing folders above it, instead of failing." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** exactly one task file has ID `id`; the task may be open or done. No `.md` with text is in `to` under the task's name, unless it is the same file (same device and inode) as the task's notes — what an interrupted move leaves. `to` exists, or `parents` is true, as for [`create-folder`](#create-folder):

| State of `to` | Outcome |
|---|---|
| The [path walk](#path-walk) finds every entry, including `to`, a plain directory | The task moves into `to` — or, if it is already there, nothing changes. |
| It stops at a missing folder, `parents` false | `not-found` (`folders`: the outermost missing folder). |
| It stops at a missing folder, `parents` true | That folder and every folder below it on the path, `to` included, are created, outermost first; the task moves into `to`. |
| It stops at an entry that is not a directory, or is a symlink | `corrupt` (`reason`: `unexpected-file`). |

**Needed files:** the entries along `to`'s path ([path walk](#path-walk)), and every task file whose filename ID is `id`. There is no index, so `move` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. No relevant files beyond that: a task's identity is its ID, not its location, so neither its blockers nor its dependents are read.

**Effects:**

- The task is in `to`, with every field (including `updated_at`: the folder is not stored in the task file), its task file's `schema`, and its `.md` unchanged. A stray `.md` already in `to` under the task's name that is empty, or the same file as the task's notes, is replaced by the task's notes, or removed if the task has none. One with text may hold edits an editor saved after an earlier move (see [Assumptions](design-spec.md#assumptions)), so it is a `conflict`, never overwritten.
- With `parents`, `to` and every folder above it exist.
- If the task was already in `to`: nothing changes.

**Invariants at risk:** none. `blocked_by` names tasks by ID, so moving one changes no reference.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "updated_at", "blocked_by", "tags", "extra", "folder", "notes_path", "from", "created", "changed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "updated_at": { "$ref": "task#/properties/updated_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "from": { "$ref": "folder-path", "description": "The folder the task was in before the operation." },
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created (parents), outermost first; empty if none." },
    "changed": { "type": "boolean", "description": "True if the task moved; false if it was already in to." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `from`, `created`, and `changed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` or `to` is missing or invalid. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | `to` fails the [path walk](#path-walk) while `parents` is false (`not-found`, or `corrupt` with `reason` `unexpected-file`); or no task file has ID `id` (`not-found`). A missing folder and a missing task are reported in one `not-found`. |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it moves; [`doctor`](#doctor) reports the copies, for a person to resolve first. |
| `conflict` | (`rule`: `destination-exists`) A `.md` with text is in `to` under the task's name, and is not the same file as the task's notes (`ids`: `[id]`). Merge its text into the task's notes, or remove it, first; [`doctor`](#doctor) reports it as `orphan-notes` (`task-elsewhere`). |

The `conflict` rules are checked in the order listed; the first that applies is reported.

**Warnings:** none.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-partial",
  "type": "object",
  "required": ["created"],
  "properties": {
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created before the failure, outermost first. The task did not move." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after `parents` created at least one folder. The created folders stay. Once the task file has moved, `move` cannot fail: removing the old `.md` afterwards is cleanup, and if it fails the stray `.md` is left for `doctor`, with no warning.

**Crash behavior:** a task is two files, which can't move in one step. Moving each with a plain `rename` would make the notes look lost if a crash fell between the two, whichever went first. So the notes are copied before they are removed:

1. With `parents`, missing folders are created, outermost first.
2. The `.md` is hard-linked into `to`, replacing an empty stray `.md` there, or a second name for the same notes, and `to` is flushed. A [process crash](design-spec.md#crashes) here leaves the task in place with its notes, and a stray `.md` in `to` that a retry replaces.
3. The task file is renamed into `to`. This is the moment the task moves; its notes are already there.
4. The old `.md` is removed. A crash before this leaves a stray `.md` in the old folder, which reads ignore and `doctor` reports as `orphan-notes` (`linked`: a second name for the notes now at the task), which `repair` removes.

A task with no `.md` skips steps 2 and 4, and instead removes an empty stray `.md` in `to` under its name before step 3, so a stray can never become its notes. The notes are never lost, and the tree always satisfies every invariant.

A [system crash](design-spec.md#crashes) keeps step 2 before the later steps, since its link is flushed. Steps 3 and 4 are not flushed, so a system crash can undo the rename yet keep the removal: the task is back in its old folder without notes, and its notes are in `to`, where `doctor` reports them as `orphan-notes` (`task-elsewhere`). The notes are still not lost.

**Retry safety:** safe. After `busy`, an error with `partial`, or a crash, rerunning completes the move. After success, rerunning finds the task already in `to`, changes nothing, and returns `changed: false`.

### frontier

Return the ready tasks — open, and not blocked — in the order to work on them.

**Kind:** read. Takes no lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "frontier-input",
  "type": "object",
  "properties": {
    "folder": { "$ref": "folder-path", "default": "/", "description": "Only tasks in this folder are returned." },
    "recursive": { "type": "boolean", "default": true, "description": "Also return tasks in the folder's subfolders." },
    "tags_any": { "$ref": "#/$defs/tag-set", "description": "Only tasks with at least one of these tags (see Narrowing tasks)." },
    "tags_all": { "$ref": "#/$defs/tag-set", "description": "Only tasks with every one of these tags (see Narrowing tasks)." },
    "limit": { "type": "integer", "minimum": 0, "maximum": 9007199254740991, "description": "At most this many tasks, the first in the operation's order; absent, no limit (see Narrowing tasks)." },
    "fields": {
      "type": "array",
      "minItems": 1,
      "uniqueItems": true,
      "items": { "$ref": "task-field" },
      "description": "Return each task as a Task projection of these fields, id always included; absent, whole Task views (see Narrowing tasks)."
    }
  },
  "additionalProperties": false,
  "$defs": {
    "tag-set": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "task-file#/$defs/name" } }
  }
}
```

With no input, `frontier` returns every ready task in the tree. `tags_any`, `tags_all`, `limit`, and `fields` narrow what is returned, per [Narrowing tasks](#narrowing-tasks).

**Additional validation:** none.

**Preconditions:** `folder` exists.

**Needed files:** the entries along `folder`'s path, checked by the [path walk](#path-walk) — and no task files: one bad task file must not make the whole frontier fail, so every task-file problem is a warning. `frontier` walks the whole tree, since blockers may be in any folder; a folder it can't list is an `unreadable-folder` warning. It reads the contents of every task file in scope, and of the blockers' task files of every open task in scope. Relevant files: those same task files.

`folder` and `recursive` limit only which tasks are *returned*. Blockers are looked up anywhere in the tree, since folders play no part in dependencies: a task in `/proj` blocked by an open task in `/infra` is not on `/proj`'s frontier.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "frontier-output",
  "type": "object",
  "required": ["tasks", "total", "truncated"],
  "properties": {
    "tasks": {
      "type": "array",
      "items": { "anyOf": [{ "$ref": "task-view" }, { "$ref": "task-projection" }] },
      "description": "Every ready task in scope that passes the filters, in frontier order — the first limit of them, with a limit. Task views, or Task projections with fields. May be empty."
    },
    "total": { "type": "integer", "minimum": 0, "description": "How many ready tasks in scope pass the filters, before the limit." },
    "truncated": { "type": "boolean", "description": "Whether the limit left some out: total is more than the number of tasks returned." }
  },
  "additionalProperties": false
}
```

Each item is a [Task view](#task-view), or with `fields` a [Task projection](#task-projection); for `frontier`, `readiness` is always `ready` and `blocking` always empty. The shape is shared with `show` so that callers parse one form.

**Order:**

1. `priority`, highest first; tasks with no priority after all prioritized tasks.
2. Then `id`, lowest first — the oldest first, since IDs only increase.
3. Copies of a duplicated ID in [tree order](#tree-order).

A `limit` takes a prefix of this order, and the filters leave it unchanged (see [Narrowing tasks](#narrowing-tasks)). Narrowing further — by `extra` fields, by title — is left to the caller (e.g. `jq`).

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` is not a valid folder path, `recursive` is not a boolean, or a [narrowing](#narrowing-tasks) field is invalid: a tag set empty, repeating a tag, or holding an invalid one; `limit` not an integer from 0 up; `fields` empty, repeating a name, or naming no Task view field. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `not-found`, `corrupt` | `folder` fails the [path walk](#path-walk) (`not-found`, or `corrupt` with `reason` `unexpected-file`). |

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A task file in scope is unusable, so the task may be missing from the frontier; or a blocker's task file of an open task in scope is unusable, so that task counts as blocked. |
| `duplicate-id` | An ID has more than one task file in scope (each copy is judged separately, and every ready copy is returned); or a blocker's ID of an open task in scope does, so that task counts as blocked. |
| `dangling-reference` | An open task in scope has a `blocked_by` ID with no task file, so it counts as blocked. |
| `unreadable-folder` | A folder could not be listed; tasks in it are missing from the frontier, and tasks blocked by tasks in it count as blocked. |

Narrowing never removes a warning: filters and `limit` apply after every one is recorded (see [Narrowing tasks](#narrowing-tasks)).

**Partial schema:** none.

**Crash behavior:** none. `frontier` changes nothing.

**Retry safety:** safe. The result reflects the tree at the time of the read; with no reservation, two callers may be handed the same task (see [Non-guarantees](design-spec.md#non-guarantees)).

### list

Return every task in scope, whatever its readiness, with its readiness shown — and optionally the folders in scope. Done tasks are included only on request.

**Kind:** read. Takes no lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "list-input",
  "type": "object",
  "properties": {
    "folder": { "$ref": "folder-path", "default": "/", "description": "Only tasks (and folders) in this folder are returned." },
    "recursive": { "type": "boolean", "default": true, "description": "Also return tasks (and folders) in the folder's subfolders." },
    "readiness": {
      "type": "array",
      "minItems": 1,
      "uniqueItems": true,
      "items": { "$ref": "task-view#/properties/readiness" },
      "default": ["ready", "blocked"],
      "description": "Only tasks with one of these readiness values. The default leaves out done tasks."
    },
    "include_folders": { "type": "boolean", "default": false, "description": "Also return the folders in scope: with recursive, every folder under folder; without, folder and its immediate subfolders." },
    "tags_any": { "$ref": "frontier-input#/properties/tags_any" },
    "tags_all": { "$ref": "frontier-input#/properties/tags_all" },
    "limit": { "$ref": "frontier-input#/properties/limit" },
    "fields": { "$ref": "frontier-input#/properties/fields" }
  },
  "additionalProperties": false
}
```

With no input, `list` returns every open task in the tree. `tags_any`, `tags_all`, `limit`, and `fields` narrow what is returned, per [Narrowing tasks](#narrowing-tasks).

Notes: the default `readiness` and `include_folders` change what the result *is* — "open tasks" versus "all tasks", "tasks" versus "tasks and folders" — so they would be parameters even without [Narrowing tasks](#narrowing-tasks) (see [Conventions](#conventions)). Done tasks accumulate forever; leaving them out by default keeps the ordinary result about current work. Every task, done ones included, is `["ready", "blocked", "done"]`; only finished ones, `["done"]`.

**Additional validation:** none.

**Preconditions:** `folder` exists.

**Needed files:** as for [`frontier`](#frontier): the entries along `folder`'s path ([path walk](#path-walk)), and no task files. `list` walks the whole tree (a folder it can't list is an `unreadable-folder` warning), and reads the contents of every task file in scope and of the blockers' task files of every open task in scope, since readiness needs them. Relevant files: those same task files.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "list-output",
  "type": "object",
  "required": ["tasks", "total", "truncated"],
  "properties": {
    "folders": {
      "type": "array",
      "items": { "$ref": "folder-path" },
      "description": "Present only when include_folders is true: folder itself, then — with recursive — every folder under it, or — without — its immediate subfolders; empty folders included; in tree order. Never narrowed."
    },
    "tasks": {
      "type": "array",
      "items": { "anyOf": [{ "$ref": "task-view" }, { "$ref": "task-projection" }] },
      "description": "Every task in scope with one of the readiness values asked for that passes the filters, in tree order — the first limit of them, with a limit. Task views, or Task projections with fields. May be empty."
    },
    "total": { "type": "integer", "minimum": 0, "description": "How many tasks in scope, of the readiness asked for, pass the filters, before the limit." },
    "truncated": { "type": "boolean", "description": "Whether the limit left some out: total is more than the number of tasks returned." }
  },
  "additionalProperties": false
}
```

Each task is a [Task view](#task-view), the same shape `show` and `frontier` return — or with `fields`, a [Task projection](#task-projection).

**Order:** [tree order](#tree-order), for both `folders` and `tasks`. A `limit` takes a prefix of it.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` is not a valid folder path, a flag is not a boolean, `readiness` is empty or repeats or doesn't name a readiness value, or a [narrowing](#narrowing-tasks) field is invalid, as for [`frontier`](#frontier). |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `not-found`, `corrupt` | `folder` fails the [path walk](#path-walk) (`not-found`, or `corrupt` with `reason` `unexpected-file`). |

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A task file in scope is unusable, so the task may be missing from the list; or a blocker's task file of an open task in scope is unusable, so that task counts as blocked. |
| `duplicate-id` | An ID has more than one task file in scope (every copy in scope is listed); or a blocker's ID of an open task in scope does, so that task counts as blocked. |
| `dangling-reference` | An open task in scope has a `blocked_by` ID with no task file, so it counts as blocked. |
| `unreadable-folder` | A folder could not be listed; tasks in it are missing from the list, and tasks blocked by tasks in it count as blocked. |

Narrowing never removes a warning: `readiness`, the filters, and `limit` apply after every one is recorded (see [Narrowing tasks](#narrowing-tasks)).

**Partial schema:** none.

**Crash behavior:** none. `list` changes nothing.

**Retry safety:** safe.

### why

Explain why a task isn't ready: follow its blockers, and their blockers, down to the ready tasks that would move it, and to the blockers no work clears.

**Kind:** read. Takes no lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "why-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task to explain." },
    "include_tasks": { "type": "boolean", "default": false, "description": "Also return the task and every open task upstream of it, as tasks." },
    "fields": { "$ref": "frontier-input#/properties/fields", "description": "Return each of tasks as a Task projection of these fields, id always included; absent, whole Task views. Only with include_tasks." }
  },
  "additionalProperties": false
}
```

Notes: `include_tasks` changes what the result *is* — an answer versus the answer and its explanation — so it is a parameter (see [Conventions](#conventions)). It is off by default because the upstream of a task can be large, and the answer alone is a few IDs whatever its size. `fields` is the one field of [Narrowing tasks](#narrowing-tasks) `why` takes, for the same reason `frontier` and `list` take it: `tasks` lands in an agent's context.

**Additional validation:** `fields` is given without `include_tasks` being `true` (`field`: `/fields`): there are no tasks for it to shape.

**Preconditions:** a task with `id` exists, and only one task file has it.

**Terms.** *Upstream* of the task: every task reached from it by following `blocking` — the task's own [`blocking`](#task-view), then each of those tasks', and so on — through open tasks only, since a done task blocks nothing. A blocking ID is followed only when exactly one usable task file has it; a missing, unusable, or duplicated blocker is not followed (see *Stuck*). The task itself counts as upstream of itself.

- **Ready** — every upstream task that is ready. Working on these, and only these, is what moves the task. If the task itself is ready, `ready` is just the task.
- **Stuck** — every blocking ID, met on the way, that no amount of work on ready tasks clears:
  - a blocker with no task file, an unusable one, or several (see [Dependencies](design-spec.md#dependencies));
  - every upstream task in a cycle: a strongly connected component of the upstream tasks, with an edge from each to the upstream tasks in its `blocking`, holding more than one task. (A task can't block itself alone: a task file whose `blocked_by` holds its own ID is unusable.) Since koan never writes a cycle (*Acyclic* in [Invariants](design-spec.md#invariants)), one is only met after an outside change or a system crash. Completing (e.g. cancelling) any task in it, or [`unblock`](#unblock)ing one of its edges, breaks it; that is a person's decision, as for [`doctor`](#doctor)'s `cycle` finding.

  A task blocked only by stuck tasks is not itself stuck: it is listed in `tasks` but neither in `ready` nor in `stuck`, since clearing what is stuck is what moves it.

The task is open exactly when `ready` or `stuck` is non-empty: following open blockers from an open task always ends at a ready task, a problem blocker, or a cycle. Both may be non-empty: ready work can move part of the upstream while another part stays stuck.

**Needed files:** every task file whose filename ID is `id`. As for [`show`](#show), there is no index, so `why` walks the whole tree, and fails with `io` if it meets a folder it can't list: it must find one ID and prove it unique, and blockers may be in any folder. Relevant files, whose problems are warnings: the task files of every ID in the `blocked_by` of every upstream task.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "why-output",
  "type": "object",
  "required": ["readiness", "ready", "stuck"],
  "properties": {
    "readiness": { "$ref": "task-view#/properties/readiness", "description": "The task's own readiness." },
    "ready": {
      "type": "array",
      "uniqueItems": true,
      "items": { "$ref": "task-file#/properties/id" },
      "description": "Every ready task upstream of the task, in frontier order: the task alone when it is ready itself; empty when it is done, or when every path from it ends at something stuck."
    },
    "stuck": {
      "type": "array",
      "uniqueItems": true,
      "items": { "$ref": "task-file#/properties/id" },
      "description": "Every blocking ID met upstream that no work on ready tasks clears — missing, unusable, or duplicated, or a task in a cycle — in ascending order."
    },
    "tasks": {
      "type": "array",
      "minItems": 1,
      "items": { "anyOf": [{ "$ref": "task-view" }, { "$ref": "task-projection" }] },
      "description": "Present only when include_tasks is true: the task first, then every other open task upstream of it. Task views, or Task projections with fields."
    }
  },
  "additionalProperties": false,
  "allOf": [
    { "if": { "properties": { "readiness": { "const": "done" } } }, "then": { "properties": { "ready": { "maxItems": 0 }, "stuck": { "maxItems": 0 } } }, "else": { "anyOf": [{ "properties": { "ready": { "minItems": 1 } } }, { "properties": { "stuck": { "minItems": 1 } } }] } },
    { "if": { "properties": { "readiness": { "const": "ready" } } }, "then": { "properties": { "ready": { "minItems": 1, "maxItems": 1 }, "stuck": { "maxItems": 0 } } } }
  ]
}
```

Each item of `tasks` is a [Task view](#task-view), or with `fields` a [Task projection](#task-projection) — the same shapes `show`, `frontier`, and `list` return. No new edge schema is needed: each task's `blocking` names the tasks below it, so `tasks` holds the whole upstream graph. A stuck ID that is missing, unusable, or duplicated has no item in `tasks`; one in a cycle does.

**Order:**

- `ready`: [frontier order](#frontier) — the order to work on them.
- `stuck`: ascending ID.
- `tasks`: the task first; then by the length of the shortest path to it from the task, nearest first; then by `id`, lowest first. Each upstream task has one item, since a duplicated ID is never followed.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID, `include_tasks` is not a boolean, `fields` is invalid as for [`frontier`](#frontier), or `fields` is given without `include_tasks`. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format`, `migration-pending` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id`. Each copy has its own `blocked_by`, so there is no one upstream to explain; [`show`](#show) lists the copies. |
| `corrupt`, `unsupported-format` | The task file with ID `id` is unusable. |

**Warnings:**

| Kind | When |
|---|---|
| `duplicate-id` | An ID in the `blocked_by` of an upstream task has more than one task file; it is in `stuck`. |
| `dangling-reference` | An ID in the `blocked_by` of an upstream task has no task file; it is in `stuck`. |
| `unusable-file` | A blocker's task file of an upstream task is unusable; it is in `stuck`. |
| `cycle` | Upstream tasks block each other; every task in the group is in `stuck`. |

`include_tasks` and `fields` never change the walk, `ready`, `stuck`, or the warnings: every call for a task does the same work and reports the same warnings.

**Partial schema:** none.

**Crash behavior:** none. `why` changes nothing.

**Retry safety:** safe. Like every read, the result may be stale by the time it is used (see [Reads](design-spec.md#reads)).
