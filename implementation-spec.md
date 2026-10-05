# koan implementation spec

How koan is built. The [design spec](design-spec.md), [operations](operations.md), and [CLI spec](cli-spec.md) say *what* koan does; this document says *how*. Where they state a guarantee, this document gives the mechanism that provides it, and links back to the guarantee.

## Mechanism

How the design spec's [write lock](design-spec.md#write-lock) and [Guarantees](design-spec.md#guarantees) are provided.

- **The write lock is the root directory itself.** A write opens the root with `os.OpenRoot` (see [Filesystem access](#filesystem-access)), opens the directory itself through it (`root.Open(".")`) and `flock`s that descriptor. No lock file exists anywhere: nothing can be cleaned up, deleted, or recreated out from under a holder, and the root can never be deleted (see [Folders](design-spec.md#folders)). Because `flock` locks the directory, not a path, every process reaching the root — through any path spelling, symlink, or environment — contends on the same lock.
- **`flock`, never POSIX record locks.** A record lock (`fcntl`/`lockf`) is per process and is released when *any* descriptor to the file closes, so one incidental open-and-close inside a critical section silently drops it. Measured in an earlier prototype: a 16-way read-modify-write kept 8 updates under `lockf` and 16 under `flock`. `flock` is advisory and scoped to the open file description, which is the right scope — the hazard is two koan processes, not two threads.
- **One resolution per write.** A write resolves the root path once, in `os.OpenRoot`, locks that directory through the same handle, and performs every file operation through that handle — never by re-resolving the path. Repointing a symlinked root mid-write therefore cannot split one write across two directories.
- **Close-on-exec.** The lock descriptor is close-on-exec so the lock cannot ride into a child program. Go opens every file close-on-exec by default.
- **Keep the lock's file alive.** The `*os.File` from `root.Open(".")` must stay referenced until the write ends. If it becomes unreachable, Go's finalizer may close the descriptor mid-write, which releases the lock. The write holds it explicitly and closes it (releasing the lock) only when done.
- **Polled acquisition.** `LOCK_EX | LOCK_NB`, retried every 10 ms while the lock is held, up to the wait in *Bounded wait* ([Guarantees](design-spec.md#guarantees)), then `busy`. Polling rather than a blocking `flock`, which only a signal can cut short. The wait is `store.Env.LockWait`; zero tries once, as unit tests use.
- **Platform check.** `flock` on a directory descriptor is verified on Linux. On macOS it is confirmed from the XNU kernel source: `flock` accepts any descriptor on a filesystem, directories included; the advisory-lock layer rejects only FIFOs; and the local lock code never checks the file type. Its behavior matches Linux: the lock belongs to the open file description, is shared by `fork` and `dup` copies, and is advisory. An empirical check on macOS — contention, release on crash, close-on-exec — belongs in the implementation's test suite. Network filesystems are excluded (see [Assumptions](design-spec.md#assumptions)).
- **Atomic file writes.** Write a complete temp file in the same directory (a hidden entry), then publish it: `rename` to replace an existing file, `link` to create a new one so an existing file is never clobbered. A process crash between writing the temp file and removing it can leave the temp file behind; it is ignored by reads and reported by [`doctor`](operations.md#doctor).
- **Moves never replace.** [`move`](operations.md#move)'s task file and [`move-folder`](operations.md#move-folder)'s folder move with a rename that fails with `EEXIST` rather than replace what is at the new name: `renameat2` with `RENAME_NOREPLACE` on Linux, `renameatx_np` with `RENAME_EXCL` on macOS, both from `golang.org/x/sys/unix`, between the two folders opened through the `os.Root`. Plain `rename(2)` silently replaces an empty folder, so a check made beforehand under the lock would be the only guard; the no-replace rename is the backstop against an outside change, and reports it as `corrupt` (`unexpected-file`).
- **Notes move by hard link.** `move` links the `.md` to a temp name in the destination folder, then renames that over an empty stray `.md` there (one with text is a conflict), before moving the task file; the folder is flushed after that rename, so a system crash keeps the link too; the old `.md` is removed last. The temp name is removed after the rename too, since renaming onto a second name of the same file — an interrupted move's link — does nothing and leaves it. The notes are therefore at the destination before the task is, and a crash never leaves the task without them.
- **Folders are removed by renaming them aside.** [`delete-folder`](operations.md#delete-folder) renames the folder to a temp name in the root (`.koan-tmp-<random>`, a hidden folder), then removes that with `RemoveAll`. The rename is the one step that takes the folder out of the tree; a crash or error during the removal leaves a hidden folder that reads ignore and `doctor` finds by its prefix.

## Toolchain

- **Go**, standard library first. The `version` operation's `go`, `commit`, `commit_time`, and `uncommitted_changes` come from the build information Go embeds (`runtime/debug.ReadBuildInfo`: the Go version and the `vcs.revision`, `vcs.time`, and `vcs.modified` settings); `version` itself is set at release build time; a build without it takes the main module's version when that is a release tag — a valid semantic version that is not a pseudo-version and has no build metadata (e.g. `+dirty`) — so `go install …@v0.1.0` reports `0.1.0`, and anything else reports `0.0.0-dev`. A **release build** must come from a git checkout, so this information is present; the release build fails otherwise. A build with no VCS information falls back to the main module's version: `go install …@<version>` builds from the module cache without git, but a pseudo-version (e.g. `v0.0.0-20260929021723-4df90bd5d3e3`) still names the commit, by its 12-character prefix, and its UTC commit time, parsed with `golang.org/x/mod/module`; `uncommitted_changes` is `false`, since a module download has none. Any other build without it — a tagged version, `(devel)` — reports `commit` `"unknown"` and `commit_time` `"1970-01-01T00:00:00Z"`, so the output still matches `version-output`.
- **No runtime dependencies** beyond the standard library, except cobra (and pflag) in the CLI (see [Argument parsing](#argument-parsing)), `golang.org/x/mod` for reading pseudo-versions, and two that only `pick` uses: `junegunn/go-shellwords`, fzf's own parser, to split `KOAN_PICK_OPTS` exactly as fzf splits `FZF_DEFAULT_OPTS`, and `mattn/go-runewidth`, to measure the picker's columns in terminal cells. A JSON Schema library is a **test-only** dependency (see [Validation](#validation)), as are `creack/pty` and `hinshun/vt10x`, which give the picker's end-to-end tests a terminal and render its screen.

## JSON reading

All JSON koan reads — operation input, task files, `koan.json` — goes through one reader, built on the standard library's token stream: `json.Decoder` with `UseNumber()`, read with `Token()`. The standard library does all the parsing: syntax, string escapes, number syntax. Decoding straight into structs or maps (`json.Unmarshal`) is never used, because it loses exactly what the specs need: repeated keys (the last one silently wins), the text of numbers (`2.0` and `2` become the same `float64`), and key order.

The reader builds an **ordered tree**: objects as ordered lists of members, arrays, strings, `true`/`false`/`null`, and numbers as `json.Number` — the literal's exact text. Its checks, and the standard library behavior each one covers:

| Check | Why the reader must do it |
|---|---|
| The raw bytes are valid UTF-8 (`utf8.Valid`), checked **before** decoding | The decoder silently replaces invalid bytes with U+FFFD; nothing after it can tell. |
| No byte-order mark | The decoder already rejects one; the reader reports it as its own reason. |
| Not empty | The decoder yields no tokens for empty input. |
| The top-level value is an object | The decoder accepts any value; operation input and every file must be an object (for a file, otherwise `not-json`). |
| No repeated key within an object | The token stream yields both keys; only the reader can see the repeat. |
| Nothing after the first value | The decoder is a stream and happily yields a second value. |
| No `\u` escape of an unpaired surrogate: a high one not followed by an escaped low one, or a low one alone | The decoder silently replaces it with U+FFFD, changing the string — and can make two distinct keys equal. Checked by a scan of the raw bytes after decoding. |
| At most 9,990 levels of nested objects and arrays | The token stream has no limit, but the encoder rejects output deeper than 10,000. The margin lets anything read be written back, even inside the output envelope, which adds at most 3 levels. |

How a failure is reported:

- **Operation input:** `invalid-input`, and nothing further is checked. `field` is `""`, except for a repeated key, where it is the pointer of that key (e.g. `/extra/status`).
- **A JSON option value** (e.g. `--extra`): reported at the option's field, with any inner pointer prefixed by it (a repeated key `status` in `--extra` is `/extra/status`), and other checks still run (see [Conversion](#conversion)). Its nesting counts from the input it sits in: `--extra` (at `/extra`) may nest 9,989 levels, `--extra-merge` (at `/extra/merge`) 9,988, so the input as a whole — and the task file written from it — stays within the limit.
- **A file:** a repeated key is a file-level rule, not a parse failure, so it must not pre-empt the version check ([File validity](design-spec.md#file-validity) step 2). The reader records it and keeps going; it is reported after the version check, as `corrupt` (`reason`: `invalid`). Every other failure above makes the file `corrupt` (`reason`: `not-json`).

**Adapters** then turn the tree into domain types (one per operation input, plus the task file and `koan.json`), validating as they go (see [Validation](#validation)).

**Numbers in `extra` stay `json.Number`** from reading to writing and are never converted to `float64`. That is what lets [File format](design-spec.md#file-format) write them back character for character. A test round-trips `1.10`, `-0`, `1e400`, and a 20-digit integer through `update` unchanged.

## JSON writing

All JSON koan writes — task files, `koan.json`, and the CLI's envelope — uses the standard `json.Encoder`, configured so its output is exactly the design spec's [File format](design-spec.md#file-format) and the CLI's [Output](cli-spec.md#output):

- **`SetEscapeHTML(false)`**, always. The encoder then escapes exactly `"`, `\`, U+0000–U+001F, U+2028, and U+2029 — the File format's list.
- **`SetIndent("", "  ")`** for files. No indent for the CLI envelope, which is compact.
- **Key order** comes from struct field order, which follows each schema's key order.
- **`extra`** is an ordered-object type with its own `MarshalJSON`, emitting its members in stored order, so existing keys keep their position and new ones are appended. Objects nested inside `extra` use the same type.
- **Sets are sorted before writing:** `blocked_by` ascending, `tags` by name. The encoder writes arrays in the order given.
- **Trailing newline:** `Encode` writes one.

A test pins the exact bytes of a task file covering every rule above (key order, nested `extra`, empty `{}` and `[]`, sorted sets, raw `<>&/` and non-ASCII, escaped U+2028, exact `extra` numbers).

## Validation

### Where it happens

**At runtime, the adapters validate.** Converting the ordered tree to a domain type already requires checking each field's type; the adapter checks the field's remaining rules — the schema's constraints and the operation's Additional validation — at the same point, and records every problem it finds with the field's JSON Pointer. The binary contains no JSON Schema validator. One Additional validation check runs in its operation instead, because it needs the filesystem: `init`'s check that anything existing at `root` leads to a directory. It runs whenever `root` itself passed the adapter, and its problem joins the adapter's in the one `invalid-input`, which still reports every invalid input and comes first in `init`'s precedence. The only other value checks outside the adapters are the CLI's: the reading of JSON option values, and the input resolution a command lists — `init`'s `~user/` rejection and `--notes-file`'s UTF-8 check (see [Conversion](#conversion)).

**The published schemas are checked by tests only.** They stay normative, in the docs (see [Schemas in tests](#schemas-in-tests)).

### Order of checks

Each step adds to one list of problems; a failed step stops only what depends on it:

1. **Reading** (see [JSON reading](#json-reading)): UTF-8, no byte-order mark, not empty, no repeated key, one value. For operation input, a failure here stops everything below.
2. **Integer literals.** Outside `extra`, every number must be an integer literal (`2`, not `2.0` or `2e0`). This is exact because every number outside `extra`, in every input and file schema, is an integer; a test fails if a schema ever gains a non-integer number field outside `extra`. So the rule is checked with each integer field, and a number anywhere else fails its field's type: every such number is rejected either way, with the more specific reason.
3. **Field rules**, per adapter: type, required and unknown fields, patterns, lengths, ranges, uniqueness, and the `oneOf`/`anyOf` forms (e.g. `update`'s `tags`).
4. **Additional validation and file-level rules** no schema can express — title trimming and validation, real calendar timestamps, a task not in its own `blocked_by`, the filename ID matching `id`, `blockers` not containing `id` — run only on fields that passed step 3, so no field is reported twice.

Problems are sorted as [Error kinds](operations.md#error-kinds) requires (by `field`, then `reason`). A problem's `reason` is written for its field (e.g. "expected a JSON object"), never a generic validator message.

For files, the same steps implement [File validity](design-spec.md#file-validity)'s three steps: after step 1, `schema` must be present as an integer literal within ±(2^53 − 1) (else `corrupt`, `reason` `invalid`) and supported (else `unsupported-format`); then a recorded repeated key, and steps 2–4, make the file `corrupt` (`reason`: `invalid`). A repeated `schema` key makes the file `corrupt` (`invalid`) with no version check, since its version is ambiguous.

### Schemas in tests

- **Extraction.** A generator (`go generate`) extracts every JSON code block with an `$id` from the five specs (design, operations, CLI, implementation and [pick](pick-spec.md)) into `schemas/`; an `$id` anywhere else is an error, so no schema is left out. A test regenerates and fails on any difference, so the docs and the tests' copies cannot drift.
- **Library.** The tests use a JSON Schema library that supports draft 2020-12 (including `$ref` by `$id` and `if`/`then`/`oneOf`), validates `json.Number` without converting it to `float64` (as a `float64`, `9007199254740993` rounds to a value that passes `priority`'s maximum), reports every error with its JSON Pointer, and loads schemas from local files under a fixed base URI. It is `github.com/santhosh-tekuri/jsonschema/v6`, with its regex engine replaced by Go's `regexp` after rewriting each `\uXXXX` escape to `\x{XXXX}`: the one ECMA-262 syntax the specs' patterns use that RE2 lacks. Syntax both accept with different meanings (`.` outside a class, `\s`, `\S`) is refused, so a pattern using it fails to compile. Only a test-helper package imports the library, so it never reaches the binary.
- **Agreement tests.** For every input schema and file schema, a corpus of cases — hand-picked edge cases plus valid inputs mutated one field at a time — goes through both the adapters and the schema library. Both must accept, or both must reject. On rejection, the adapter's problems must name exactly the fields the library names, with the library's errors about a child that its parent reports (a disallowed additional property, each array item equal to an earlier one, a missing required property) placed at that child, where the adapters report them. Where an `anyOf` or `oneOf` fails (`update`'s `tags` and `extra`, and its requirement of at least one field), the library reports every alternative's failures while the adapter reports only the form the input evidently meant: there the adapter's fields must be exactly one alternative's failures, and a property an alternative requires is placed at the object, since the alternative does not make it required overall. A file rejected for its `schema` at [File validity](design-spec.md#file-validity) step 1, or at step 2, is checked no further, so there the library need only also reject its `schema`. Integer literals and Additional validation are outside what the schemas express and are tested separately: adapters mark each problem from Additional validation (and each file-level rule beyond the file's schema), and the comparison leaves those out.
- **Output conformance.** Every envelope any test produces is validated against `envelope`, the operation's output or partial schema, `error`, `warning`, and — for usage errors — `usage-details`.

## Argument parsing

The command line is parsed by [cobra](https://github.com/spf13/cobra) (and pflag beneath it), which implements the [Command line](cli-spec.md#command-line) rules. koan is a task manager, not a parsing project: where a rule and cobra disagreed, the rule gave way. What koan adds is small: turning cobra's errors into envelopes, and building the operation's input from what cobra parsed.

### Command tables

Each command is declared by a small table: its name, the operation it runs, and one row per argument and option — name, any short form, the JSON Pointer of the field it sets (or none, e.g. `--notes-file`), its value type (boolean, integer, nullable integer, string, comma list, repeatable string, JSON value), and whether it is required. The cobra command and its flags, and the input-building, are generated from the table. Every flag but a boolean is registered as a string (a comma list or a repeatable string as a string array), so cobra judges only shape and every value reaches the adapters.

### Phases

1. **Parse.** cobra parses the command line and prints `--help` itself (plain text, exit `0`). `SilenceErrors` and `SilenceUsage` keep it from printing anything else. cobra's default `completion` command is disabled, and its `help` command is replaced by a hidden one that is a `usage` error, so neither prints text. Every error it returns — unknown command or flag, missing flag value, wrong argument count — is a `usage` error, reported with its message as the one problem.
2. **Shape checks cobra lacks.** Before the operation runs: mutually exclusive options given together (the problem names the one given later), a missing required option, and `--input` together with any argument or option that sets a field, are `usage` errors. A command name after `--` is an argument, not a command, so it is an unknown command; when it names a real one, the problem says the command must come before `--`. The argument count is checked by the command's `Args` function: the table's arguments, or none with `--input`.
3. **Build the input.** Place each value at its field, converting by type (below), and apply the command's input resolution (`init`'s `root`, `--notes-file`). Only flags actually given are placed; defaults are the operation's. With `--input`, read the file instead (then apply resolution).
4. **Validate** through the same adapters as `--input` (see [Validation](#validation)).

### Conversion

The CLI decides shape; values are judged by the adapters. A token that does not fit its field's type is passed on as it is, and the adapter rejects it at that field — exactly as it would the same value arriving through `--input`. The one exception is JSON option values:

- **Integer fields** (and each item of an ID list): a token shaped like a JSON integer (`-?(0|[1-9][0-9]*)`) becomes a number; the adapter checks its range. Anything else becomes a string, which the adapter rejects as the wrong type. `koan show abc` builds `{"id": "abc"}` and fails at `/id`.
- **Nullable fields:** the token `null` becomes `null`.
- **JSON values** (`--extra`, `--extra-merge`, `--extra-replace-all`): the CLI checks the token with `json.Valid`. A valid token is parsed (through [JSON reading](#json-reading)) into that value. An invalid one is an `invalid-input` problem at the option's field ("not valid JSON"), raised by the CLI; the field is left out of the input, the adapters still run on the rest, and the CLI's problems are merged into the adapters' list before sorting, so every problem is still reported once.
- **Comma lists:** each occurrence split on `,` exactly, occurrences joined in order; `''` is `[]`. Items are not trimmed.
- **Strings, folder paths:** as given.
- **Input resolution** runs while the input is built. Its `invalid-input` problems (`~user/` in `init`'s `root`, non-UTF-8 `--notes-file` contents) are merged like a JSON option's. Its `io` errors (an unreadable `--notes-file` or `--input` file, an undeterminable working directory) and `environment` errors (an undeterminable home directory) stop the command before validation: the input cannot be built.

Apart from reading JSON option values and the input resolution a command lists (`init`'s `root`, `--notes-file`), the CLI holds no copy of any value rule, and a bad value is the same error whether it came from an argument or from `--input`. (If a malformed JSON option was the only field given to `update`, the adapters also report that no field was given; both problems are accurate.)

## Filesystem access

Every file operation under the root — reads and writes — goes through one `os.Root` (Go 1.25+, for `Root.Link`, `Root.Rename`, and `Root.RemoveAll`), opened once per invocation with `os.OpenRoot` on the configured root path:

- **One resolution.** The root path is resolved once, when the `os.Root` is opened; every open, stat, `Mkdir`, `Link`, `Rename`, `Remove`, and `RemoveAll` is relative to that handle, and the no-replace rename (see [Mechanism](#mechanism)) is made between folders opened through it. This is the [Mechanism](#mechanism)'s *one resolution per write*, in the standard library. (The `syscall` package has no `openat`, `renameat`, or `linkat` on macOS, so the handle is the portable way to get it.)
- **The lock** is taken on `root.Open(".")` — the same directory, through the same handle.
- **No symlinks followed.** `os.Root` follows symlinks that stay inside the root; koan never does. `os.Root.OpenFile` ignores a caller's `O_NOFOLLOW`: it adds the flag itself and, on `ELOOP`, resolves the symlink when its target stays inside the root. So `fsys` opens a file or folder in two steps: its parent folder through the `os.Root`, then the entry itself with `openat(2)` and `O_NOFOLLOW` relative to that folder, from `golang.org/x/sys/unix`, which has `openat` on Linux and macOS alike (the `syscall` package lacks it on macOS). A symlink fails with `ELOOP`, decided by the one call: there is no window between a check and the open, so an entry swapped for a symlink is refused, and a file replaced by a concurrent write's `rename` — `koan.json`, read before the lock by every operation, under a burst of writes — is read whole, in one version or the other. `os.Root` also follows in-root symlinks in a name's *earlier* components, which is why the path walk `Lstat`s each component in turn. Files are opened with `O_NONBLOCK`, and anything that is not a regular file or folder — a FIFO, a socket, a device — reads as empty, so it can never block or read forever. Tests confirm each case — a symlink to a file, a symlink to a folder, a swap just before the open, and reads under concurrent replacement — on both platforms.
- **A non-directory root is `ENOTDIR`.** `os.OpenRoot` opens the path without `O_DIRECTORY` and checks its type only afterwards, so a FIFO would block the open, and a regular file is reported by an error with no errno inside. `fsys` therefore hands it the path with `/.` appended: resolving that requires a directory, so the kernel refuses a FIFO or regular file with `ENOTDIR` before opening anything, with no window for a swap. The error's path and the root's `Name` are the configured path. The [OS errors](#meaning-is-decided-where-the-call-is-made) row for `os.OpenRoot` applies. Every folder `fsys` opens by name through the root — a file's folder, to open the file in it or rename into or out of it, and a folder to flush — is opened the same way, so a FIFO swapped into a folder's place by an outside change fails with `ENOTDIR` instead of blocking with the write lock held.

## Tree walk

### The index

Operations that must find an ID, prove it absent, or return a collection walk the whole tree once, by **name only** — no file is read during the walk:

- **Order.** Depth-first, a folder before its subfolders, each folder's entries sorted by name — which yields folders in [tree order](operations.md#tree-order) (`/`, `/infra`, `/proj`, `/proj/travel`, `/proj-b`). Tasks within a folder are sorted by numeric ID (`9.json` before `10.json`).
- **Classification**, per [Walking the tree](design-spec.md#walking-the-tree): a hidden entry is skipped; a folder name that is a directory is descended into; `<id>.json` that is a regular file is a task; everything else, symlinks included, is skipped.
- **Result:** an index from each ID to every location that has it, in tree order; the list of folders; and the folders that could not be listed, with their errno.

`create` without `blocked_by`, `create-batch` without an ID in any `blocked_by`, `create-folder`, `version`, `info`, and `init` do not walk. `doctor` and `repair` walk with a recorder for what the index skips (see [The survey](#the-survey)).

### Loading task files

Task files are loaded on first use and cached for the rest of the operation — in a composed command, until the next operation starts (see [Operations and transactions](#operations-and-transactions)). A load runs [File validity](design-spec.md#file-validity) through [JSON reading](#json-reading) and [Validation](#validation) and ends in either a usable task or an unusable one with its reason (`unreadable` with its errno, `corrupt` with its reason, or `unsupported-format`).

### Queries

Operations use the index through a few helpers:

- **Find exactly one** — the targets of `show`, `done`, `reopen`, `block`, `unblock`, `update`, `move`, and `delete` (which reads only the filename, never the file).
- **Find every reference** — `delete` and `delete-folder`: every task file outside what is removed is loaded, and those whose `blocked_by` names a removed ID are rewritten; the unusable ones are `unusable-file` warnings.
- **Check existence** — `create`'s `blocked_by`, `create-batch`'s existing blockers, `block`'s blockers.
- **Filter by scope** — `frontier` and `list`: tasks and folders under `folder`, recursively or not. Every open task in scope gets its readiness derived, whatever `list`'s `readiness` keeps, so the warnings don't depend on it; then [Narrowing tasks](operations.md#narrowing-tasks) applies to the views, after every warning is recorded.
- **Derive readiness** — for an open task only, evaluating every blocker even once one is known to block, per [Dependencies](design-spec.md#dependencies): no task file → blocking, `dangling-reference` (unless a folder the walk had to list was unreadable — then no warning, per [Warning kinds](operations.md#warning-kinds)); several → blocking, `duplicate-id`; unusable → blocking, `unusable-file`; done → not blocking; open → blocking.

### Error precedence

[Precedence](operations.md#precedence) is the order the code runs in; each step returns on its first error:

1. validate input;
2. locate the config (`environment`), then check the root (config, `koan.json`);
3. take the lock (writes only), then re-read `koan.json`;
4. path walk of any input folder — a missing folder is held, not returned, so step 6 can report it in the same `not-found` as missing IDs; `corrupt` (`unexpected-file`) returns;
5. tree walk — a folder that cannot be listed is `io` here, for operations that must see the whole tree;
6. ID lookups: `not-found`, listing every missing ID together with any missing folder held from step 4. For `block`, `id`'s own task file(s) are loaded first — an unusable copy is reported then — because which blockers are new (and so looked up) depends on `id`'s `blocked_by`, across every copy;
7. load needed files (`corrupt`, `unsupported-format`: the first in tree order);
8. conflicts (`duplicate-id`, `acyclic`, `id-exhausted`).

`io` and `internal` return wherever they occur. `init`, `doctor`, and `repair` run their own orders.

### Warnings

One collector per invocation receives every warning, drops repeats by the spec's key (per file for `unusable-file`, per folder for `unreadable-folder`, per ID for `duplicate-id`, per (referring, missing) pair for `dangling-reference`, per `.md` for `notes-missing`), and sorts once at the end: by `kind`, then the first entry of `paths`, then `ids` ([Warnings](operations.md#warnings)).

### Concurrent writes during a read

Per the design spec's [Reads](design-spec.md#reads):

- A file that disappears between the walk and its load (`ENOENT`) is skipped silently.
- Before reporting an ID as duplicated, each of its locations is checked again with `root.Lstat`; locations that no longer exist (`ENOENT`) are dropped. A task moved mid-read is then counted once, at the location seen last, and is not reported as a duplicate. Any other error keeps the location: the file was listed, so it counts as a copy, and loading it reports the error — an `unusable-file` warning where the file is relevant, the needed-file error where it is needed.

A needed file that has disappeared — in a read or a write — is treated as never found: `not-found` if it was the ID's only file (see [Precedence](operations.md#precedence)). So `show` never returns an empty `tasks`.

## Cycle check

Implements [`block`](operations.md#block)'s cycle check: adding "`id` is blocked by B" creates a cycle exactly when `id` is reachable from B by following `blocked_by`. The graph is keyed by ID ([Dependencies](design-spec.md#dependencies)): a duplicated ID is one node, with the union of every copy's `blocked_by` as its edges; done tasks are followed like open ones; an ID with no task file has no edges.

### Phase 1: load the reachable subgraph

Starting from the new blockers (those not already in `id`'s `blocked_by`), follow `blocked_by` through the [index](#the-index), loading each task file once through the [task-file cache](#loading-task-files) and never expanding `id`:

- A node's edges are the union of all its copies' `blocked_by`, sorted ascending, without repeats.
- An ID with no task file is a node with no edges, skipped silently.
- An unusable copy is a needed-file error (`corrupt` or `unsupported-format`; the first in tree order, per [Precedence](operations.md#precedence)).

Phase 1 always completes before any cycle is looked for, so the set of files read — and therefore the errors — depends only on the graph.

### Phase 2: one breadth-first search per new blocker

For each new blocker B, in ascending order: breadth-first search from B over the loaded subgraph, with a first-in-first-out queue, visiting each node's neighbours in ascending ID order, never expanding `id`, and stopping when `id` is discovered. Parent pointers give the path `B → … → x → id`; the reported cycle is `[id, B, …, x]` — each task blocked by the next, the last blocked by `id`. If `id` is not reached, B creates no cycle.

Offending blockers are reported in ascending order in `ids`, with `cycles[i]` for `ids[i]`.

### Why the first path found is the one required

The spec requires the shortest cycle, ties broken by the lexicographically smallest ID sequence. Every candidate starts `[id, B]`, so this is the shortest, then lexicographically smallest, path from B to `id`. Breadth-first search with a FIFO queue and ascending neighbours finds exactly that, by induction on depth:

- **Claim:** nodes are dequeued in the lexicographic order of their shortest paths from B, and each node's parent lies on its lexicographically smallest shortest path.
- **Depth 0:** only B.
- **Step:** every depth-(k+1) path is a depth-k path with one node appended, so two such paths compare first by their depth-k prefixes, then by the appended ID. Depth-k nodes are dequeued in prefix order (by the claim), and each adds its undiscovered neighbours in ascending order; so depth-(k+1) nodes are discovered — and queued — in the lexicographic order of their paths, and a node's parent is set at its first discovery, which is through its smallest path.

So the first discovery of `id` yields the required path.

### Cycle-check tests

- **Brute force.** On thousands of small random graphs — with duplicated IDs, IDs with no task file, and done tasks — compare against enumerating every simple path from B to `id` and picking the shortest, then lexicographically smallest. Results must match exactly.
- **Needed files.** A graph where B reaches both `id` and a corrupt file X, with X ordered after `id`: the result is `corrupt`, not `acyclic`.

## OS errors

`io` errors and the `unusable-file` (`unreadable`), `unreadable-folder`, and `notes-missing` warnings carry `code`: the symbolic OS error, never a number (a `notes-missing` warning for an errno with no name has none; see below), and the same name on Linux and macOS ([Error kinds](operations.md#error-kinds)).

### Names

The standard library has no errno-to-name function (`syscall.Errno.Error()` is the message, e.g. "no space left on device"), and errno numbers differ by platform (`EAGAIN` is 11 on Linux, 35 on macOS). koan keeps its own table, one per OS (`errno_linux.go`, `errno_darwin.go`), each written with `syscall` constants — `map[syscall.Errno]string{syscall.ENOSPC: "ENOSPC", …}` — so each carries its platform's numbers. They are separate files because some names exist on only one OS (`EL2NSYNC` on Linux, `EBADRPC` on macOS), and the completeness test needs them all.

- **Aliases** get one fixed name on every platform: `EAGAIN` (not `EWOULDBLOCK`), `ENOTSUP` (not `EOPNOTSUPP`), `EDEADLK` (not `EDEADLOCK`). The rule is about names, not numbers: where a platform gives the other name its own number (`EOPNOTSUPP` is 102 on macOS, `ENOTSUP` 45), that number also maps to the canonical name, and the other name never appears in output. The table can therefore map two numbers to one name; a test checks that each alias maps to its canonical name on the current platform.
- **Completeness test**, run on each platform: for every errno from 1 to 255 whose message is a real one (not "errno N"), the table must have a name. A missing name fails CI rather than shipping.
- **An errno not in the table** (e.g. from a newer kernel) is `internal` — never an invented name. The one exception is `notes-missing`, which comes after the task is written: it is still raised, without `code`.

### Extraction

The errno is found with `errors.As(err, &errno)`, through `*os.PathError`, `*os.LinkError`, and `*os.SyscallError`. An error with no errno inside it is `internal`.

### Meaning is decided where the call is made

The same errno means different things in different places, so each call site classifies its own errors; only what no call site claims falls through to `io`:

| Where | errno | Becomes |
|---|---|---|
| `flock` on the root | `EAGAIN` | `busy` |
| `flock` | `EINTR` | retried |
| Path walk of an input folder | `ENOENT` | `not-found` |
| Path walk | `ELOOP` (a symlink; see [Filesystem access](#filesystem-access)), `ENOTDIR` | `corrupt` (`unexpected-file`) |
| Config or `koan.json` | `ENOENT` | `not-initialized` (`missing`: `config` or `metadata`) |
| `koan.json` | `ELOOP` (a symlink), `EISDIR` | `corrupt` (`unexpected-file`) |
| `os.OpenRoot` on the root | `ENOENT`, `ENOTDIR`, `ELOOP` (a symlink loop) | `not-initialized` (`missing`: `root`) |
| Loading a task file, in a read | `ENOENT` | skipped silently: it vanished ([Concurrent writes during a read](#concurrent-writes-during-a-read)) |
| Loading a task file, in a read | any other | `unusable-file` warning (`reason`: `unreadable`, with `code`) |
| Writing a task's `.md` (`create`, `create-batch`) | any | `notes-missing` warning, with `code` (none for an errno with no name); the operation succeeds |
| Loading a needed file, in a write | `ENOENT` | treated as never found ([Precedence](operations.md#precedence)) |
| Publishing a new file with `link` | `EEXIST` | `corrupt` (`unexpected-file`): a file where none can exist |
| Listing a folder, in `frontier` or `list` | any | `unreadable-folder` warning, with `code` |
| Anywhere else | any | `io`, with `path` and `code` |

[`info`](operations.md#info) classifies its own errors as state (see [info](#info)); no row applies to it.

### Paths

`path` is the root **as stored** in the config (with `~/` expanded), joined with the path relative to the root — as the design spec's [Root path](design-spec.md#root-path) requires for reported paths. Errors on the config itself use the config's path. `init`'s working-directory error uses `.` ([`init`](cli-spec.md#init)).

### OS-error tests

Beyond the completeness test, every row of the table above has a test. Faults that are easy to create for real are created: a folder with its permissions removed, a symlink in place of a folder, a file already at the publish path. The rest (`ENOSPC` on write, `EINTR` on `flock`) are injected through a test double for the filesystem calls.

## Exit and signals

Implements the CLI spec's [Output](cli-spec.md#output) and [Exit codes](cli-spec.md#exit-codes).

### One exit point

`main` calls `run()`, which returns the exit code; `main` then calls `os.Exit(code)`. Nothing else calls `os.Exit`, so deferred cleanup — removing temp files, closing the lock's file (which releases the lock) — always runs first. A process that dies by signal skips it; that is a crash, where a leftover temp file is expected and [`doctor`](operations.md#doctor) finds it.

### Writing the envelope

- The envelope and its newline are built in memory and written with one `os.Stdout.Write`, which retries short writes itself: the line is either written whole or the write returns an error.
- `os.Stdout.Close()` is then checked, since some errors surface only at close (e.g. stdout redirected to a network filesystem).
- Only when both succeed does koan exit `0`, `1`, or `2`. If either fails, it writes a notice to stderr ("koan: result not delivered: …"; human text, not part of the contract) and exits `3`.
- Once both succeed, and only then, `deliver` writes the CLI spec's one [stderr](cli-spec.md#output) line for a failure or for warnings, from the envelope it just wrote: one `Write` of one line, with each control character in the message (U+0000–U+001F, U+007F–U+009F, U+2028, U+2029) written as a Go escape (`\n`, `\x1b`, ` `), so none — a newline in a root path, or in a repeated key's pointer — can split the line or drive the terminal. Quotes, backslashes, and other text are left as they are. Its write error is ignored: the result was already delivered, and with SIGPIPE caught a closed stderr is just an `EPIPE`.
- `--help` text is written the same way, with nothing on stderr.

### SIGPIPE

By default the Go runtime kills a process with SIGPIPE when it writes to a closed pipe on stdout (exit 141). koan calls `signal.Notify` for SIGPIPE at startup, which makes such a write return `EPIPE` instead; the write fails as above, and koan exits `3` with the notice. Every "result not delivered" case therefore has the one exit code and the notice. (The CLI spec permits death by SIGPIPE; koan does not use that latitude.)

### Interrupts

No handlers are installed for SIGINT, SIGTERM, or SIGHUP, except by `pick`, which catches and discards SIGINT and SIGQUIT while fzf runs (see [pick-spec](pick-spec.md#errors)). Go's default terminates the process by the signal, which the shell sees as `128+n` — exactly the CLI spec's *interrupts are crashes*: no envelope, and the operation's Crash behavior and Retry safety apply.

### Crashes

An unrecovered panic or a fatal runtime error (out of memory, a detected data race) makes Go print a stack trace and exit with status **2** — the usage-error code, which tells the caller nothing ran. A crash mid-write means the opposite: the outcome is unknown.

- At startup, koan calls `debug.SetTraceback("crash")`. A panic or fatal error then ends by SIGABRT, exit 134 (`128+6`), which the CLI spec classes as *outcome unknown*.
- **Panics are never recovered into `internal`.** Reporting `internal` (exit `1`) would claim the operation failed without effect, which a panic mid-write cannot promise. `internal` is reserved for bugs koan detects as errors (an errno missing from the [table](#names), an impossible state); a write returns it like any other error, with `partial` where its operation defines one.

### Exit and signal tests

- Closed pipe: `koan version | true`, with the reader gone before koan writes → exit `3`, notice on stderr.
- Full disk: stdout redirected to `/dev/full` (Linux) → exit `3`.
- Signal: SIGTERM during a write held open by a test hook → exit 143, no envelope.
- Crash: a forced panic in a test build → exit 134, not 2.
- Delivery: every exit `0`, `1`, or `2` anywhere in the test suite comes with exactly one complete envelope line on stdout.
- stderr: exit `1` or `2` → exactly one line, also when the envelope has warnings; exit `0` with warnings → one summary line; exit `0` without warnings, and `--help` → nothing; exit `3` → only the notice; a path with a newline in it → still one line.

## Package layout

Module `github.com/phansen314/koan`. Everything but `main` is under `internal/`: koan's contract is the JSON its CLI emits ([Versioning](operations.md#versioning)), and a public Go API would be a second contract. The operations can be made a public package later if there is a reason to.

```text
cmd/koan/                 main: SetTraceback, SIGPIPE, run(), os.Exit — nothing else
internal/cli/              command tables, cobra commands, conversion, envelope output, exit codes
internal/pick/             pick: the fzf session and its helper, composing list and the write operations
internal/ops/              one file per operation: its input adapter and its steps, in precedence order
internal/model/            domain types: ID, Title, Tag, FolderPath, Priority, Timestamp, Extra (ordered), Task, TaskView
internal/store/            config, root states, the lock, tree walk and index, task-file cache, file validity, atomic writes
internal/graph/            readiness and the cycle check — pure functions, no I/O
internal/jsonio/           token-stream reader to ordered tree; encoder configuration; the ordered-object type
internal/fsys/             thin interface over os.Root and flock; the real implementation; a fault-injecting one
internal/errs/             error and warning kinds, the warning collector, the errno table
internal/buildinfo/        version, commit, commit time, uncommitted changes, Go version
internal/tools/schemagen/  go generate: extracts every $id schema from the five specs into schemas/
schemas/                   generated; used only by tests
e2e/                       end-to-end tests against the built binary
```

### Import direction

Each package imports only packages below it:

```text
cmd/koan → cli → ops → store → fsys
                      ↘ graph      ↘ jsonio
                      ↘ model ←── (store, graph)
cmd/koan → buildinfo (and ops → buildinfo, for version)
cli → pick → ops, model, fsys, for pick, which runs no operation of its own
cmd/koan → fsys, in the e2e_hooks build only (Test hooks)
errs and jsonio may be imported by any package, and import none of koan's own.
```

- **The CLI does not know the data model.** `cli` never imports `model` or `store`: it builds a `jsonio` tree from the command line, calls `ops.Run(name, tree, env)`, and writes the envelope it gets back. `pick`, which runs no operation of its own, is the one command the CLI hands to another package: `internal/pick` validates its tree with `ops.Validate`, through a `pick` input adapter that `ops` holds with the operations' adapters, and runs `list` and the write operations through `ops.Run`, each as its own call. A composed command is defined in `ops` as a named composition (see [Operations and transactions](#operations-and-transactions)) and exposed by the CLI like any other name, so `cli` still composes nothing itself. The CLI spec's "no behavior beyond parsing arguments and composing operations" is thereby enforced by the compiler. Command tables hold field pointers and value types, which is CLI-spec knowledge, not model knowledge.
- **`graph` is pure.** Readiness and the [cycle check](#cycle-check) take already-loaded nodes; `store` does the loading. The brute-force comparison runs in memory.
- **`fsys` is the only package that touches the disk.** Its real implementation wraps `os.Root` and `flock`, and refuses symlinks with `Lstat` and a same-file check after each open ([Filesystem access](#filesystem-access)). Its fault implementation wraps the real one and, at a chosen call, returns an injected errno or ends the process — the one seam for the [OS error](#os-errors) tests and for crash injection. `pick` also opens `/dev/tty` to check for a terminal, and starts processes that touch the tree's files outside `fsys`: the editor `e` runs writes notes files, and the preview's renderers (`glow`, `bat`) read them. `pick` itself reads notes and its session files only through `fsys`.

### Operations and transactions

An operation is a function over a transaction: `func(tx *store.Tx, in Input) (Result, error)`. `store.Read(fn)` runs it without the lock; `store.Write(fn)` runs it holding the write lock, after re-reading `koan.json`. A composed command is several operation functions inside one `store.Write` — the operations spec's "several operations under a single write lock" — so composition needs no change to this structure, with one exception. The transaction's [index](#the-index) and [task-file cache](#loading-task-files) never see its own writes, so the composition calls `tx.NextStep()` before each operation after the first. That drops both, keeping the lock and the open root, and the next operation sees what the earlier ones wrote.

### Environment

`ops.Env` is passed in, never global: the config directory, the filesystem (real or fault-injecting), and a **clock**. The real clock returns UTC truncated to whole seconds ([Timestamps](design-spec.md#timestamps)); tests use a fixed clock, so written files can be compared byte for byte.

### Where each kind of test runs

- **In-process**, for speed: everything that does not depend on the process itself — operations, validation, the parser, the tree walk, the cycle check.
- **`e2e/`, against the built binary**, for what only a real process shows: exit codes, SIGPIPE, `/dev/full`, signals, crashes, and the lock between processes. Each test gets its own temp directory as `HOME`, with `XDG_CONFIG_HOME` inside it; koan keeps its config there on Linux and in `~/Library/Application Support/koan` on macOS ([Config file](design-spec.md#config-file)).

## Writing files

Every file koan writes — task files, `.md` notes, `koan.json`, the config — is published through a temp file, per [Mechanism](#mechanism)'s atomic file writes:

- **Name.** `.koan-tmp-<random>`, in the directory of the file it will become: hidden (so reads ignore it), recognizably koan's (so [`doctor`](operations.md#doctor) can find leftovers), and random (so two writes never collide).
- **Created exclusively** (`O_CREATE|O_EXCL`), written in full, flushed (`fsync`), then published: `link` to create a new file, so an existing one is never clobbered (`EEXIST` is `corrupt`, `unexpected-file`); `rename` to replace one. The folder is then flushed, and the temp file removed.
- **Why flush.** Without it, a system crash can leave a published file empty — a new file published by `link` gets no help from ext4's `auto_da_alloc` — or keep a later step while losing it, e.g. a task file without the `last_id` increment before it, which reissues the ID. Flushing the temp file makes the published file whole; flushing the folder makes the publish itself durable before the next step starts.
- **Flush failures.** A failed file flush fails the write, with nothing published. A failed folder flush does not: the file is already published, so an error would report a change as not made. It is ignored, like a temp file that can't be removed.
- **Mode** `0644` for files, `0755` for folders, before the umask.

## Config file

**Location.** koan derives the config directory itself, per [Config file](design-spec.md#config-file), rather than with `os.UserConfigDir`, which fails on a relative `XDG_CONFIG_HOME` instead of ignoring it. The home directory is `$HOME`, used only when set to an absolute path; otherwise it is undeterminable, and anything that needs it fails with `environment` (`variable`: `HOME`).

The [config](design-spec.md#config-file) has exactly one key, so koan reads it with its own parser for a strict subset of TOML rather than a TOML library:

- Blank lines, and comment lines starting with `#`, are ignored.
- Exactly one other line: `root = "<string>"`, with optional spaces around `=`. The string is a TOML basic string: `\"`, `\\`, `\t`, `\n`, `\uXXXX`, and `\UXXXXXXXX` escapes are decoded; any other escape, a missing closing quote, or trailing text other than a comment is `corrupt`.
- Anything else — no `root`, a repeated `root`, another key, a table, a literal (single-quoted) or multi-line string — is `corrupt` (`invalid`).
- The file must be UTF-8 without a byte-order mark.

Every failure above is `corrupt` with `reason` `invalid`; `not-json` does not apply, since the config is not JSON. The parser reports which rule failed, and the error's `detail` says so, starting `line N: ` when one line is at fault (e.g. `line 3: expected root = "…"`); a root in an illegal [form](design-spec.md#root-path) is reported as such.

The config must be a regular file, or a symlink to one: it is checked with `stat` before it is read, and anything else — a directory, a FIFO — is `corrupt` (`unexpected-file`), and is never read, since reading a FIFO would block every command.

`init` writes the config as `root = "<path>"` plus a newline, escaping as TOML basic strings require.

**Move to a TOML library if the config ever gains more keys.** The subset parser is justified only because one key needs one line of syntax; a second key, a table, or any richer TOML makes a library (e.g. `github.com/BurntSushi/toml`) the right choice, with the same "anything unrecognized is `corrupt`" rule enforced on its result.

## `init`

`init` runs before any root exists, so it does not use the [`os.Root`](#filesystem-access) of other operations:

1. Validate and clean `root` ([`init`](cli-spec.md#init) path resolution first, in the CLI).
2. Check for an existing config (`config-exists` unless `replace_config`). Before failing with `config-exists`, remove any `.koan-tmp-*` in the config directory, as step 5 does: a crash after the config was published leaves its temp file, and the rerun that follows fails here.
3. Create the root directory if needed — `os.Mkdir`, never `MkdirAll`: `init` never creates the root's parent.
4. Open the root with `os.OpenRoot`, then create `koan.json` through it via a temp file and `link` (see [Writing files](#writing-files)), or read and check the existing one.
5. Create the config directory with `os.MkdirAll`, remove any `.koan-tmp-*` left there by an earlier interrupted `init`, and write the config via a temp file in that directory: `link` when no config exists, `rename` under `replace_config`.

The config is written last, as [`init`](operations.md#init)'s crash behavior requires. A temp file left in the config directory is outside `doctor`'s reach, which is why steps 2 and 5 remove stale ones.

## `info`

`info` inspects the config and `koan.json` and reports every problem as state ([`info`](operations.md#info)). Each output field is derived as follows:

| Field | Value |
|---|---|
| `config.path` | the config file's path; `null` when it can't be located |
| `config.state` | `missing` on `ENOENT` or when the config can't be located; `unreadable` on any other OS error; `corrupt` if it is not a regular file, fails the [subset parser](#config-file), or names a root in an illegal form; else `ok` |
| `config.root` | the root, cleaned and with `~/` expanded, when `config.state` is `ok`; else `null` |
| `tree` | `null` when `config.root` is `null` (including a `~/` root with no home directory to expand it into) |
| `tree.root_exists` | the root path leads, through symlinks, to a directory |
| `tree.metadata` | `missing` if there is no `koan.json` (or no root); `unreadable` on an OS error reading it; else the [File validity](design-spec.md#file-validity) outcome: `corrupt`, `unsupported-format`, or `ok` |
| `tree.schema` | `koan.json`'s `schema` whenever File validity step 1 passes; else `null` |
| `tree.last_id` | when `tree.metadata` is `ok`; else `null` |
| `initialized` | `false` exactly when the root is *not initialized* ([Root states](operations.md#root-states)): config `missing`, root missing, or `koan.json` `missing`; `true` otherwise, including an unusable config (then `tree` is `null`) |
| `usable` | `config.state` and `tree.metadata` both `ok`, and `tree.root_exists` |
| `compatible` | `tree.schema` equals the supported `koan.json` version; `null` when `tree.schema` is `null` |

## `doctor` and `repair`

How the [diagnostic](operations.md#operation-kinds) operations, [`doctor`](operations.md#doctor) and [`repair`](operations.md#repair), are built on the same transaction, walk, and cache as every other operation.

### Transaction

`store.Diagnose(env, w, fn)` runs `fn` as [`store.Write`](#operations-and-transactions) does — locate the config, check it and the root, take the lock (`busy` on `EAGAIN`) — except that it reads `koan.json` without failing on it. The transaction carries what it found as `MetaState()`: `ok` with the root file, or `missing`, `unreadable`, `corrupt`, or `unsupported-format` with the error each would raise. It allows writes, so `repair` uses the same `Create`, `Replace`, `SetLastID`, and `Remove` as every other write, and turns the state into its own [Preconditions](operations.md#repair) errors. `doctor` writes nothing through it.

### The survey

The [index](#the-index) walk takes an optional recorder, the **survey**. Normal operations pass none and pay nothing for it. A diagnostic transaction passes one, so the survey comes from the same single walk as the index, in the same tree order. The survey records:

- each entry whose name starts with `fsys.TempPrefix`, file or folder; a temp folder is not descended into;
- each entry the index skips that is not hidden: a symlink or a matching name of the wrong type, as a `skipped-entry` with its reason; a non-matching name, as a `stray-entry`;
- each `.md` named like a task's notes that is a regular file, with its folder;
- each `koan.json` below the root, which is not also recorded as a `stray-entry`.

Every other hidden entry is skipped, and not descended into, as by every walk. Folders that can't be listed are already in the index. `Tx.Survey()` is built and cached with `Tx.Index()`, and `NextStep` drops both.

### Checks

Each finding kind is one check: a function from the index, the survey, `MetaState()`, and the [task-file cache](#loading-task-files) to its items. Every task file is loaded once, through the cache, whichever checks need it. The checks run in a fixed order, and the [findings](operations.md#findings) are then grouped, sorted, and capped as the operations spec says.

- **`duplicate-id`.** `identical` compares the copies' bytes as read: the same bytes are the same task, whatever their validity.
- **`orphan-notes`.** `empty` is a size of zero from `Lstat`. `linked` compares the orphan's `Lstat` with that of the notes of each task with its ID, with `os.SameFile`; `fsys` passes the `FileInfo` through unchanged, so the fault-injecting implementation keeps the comparison working. An `Lstat` error other than `ENOENT` makes it `unreadable`, with its code; `ENOENT` (gone since the walk) is skipped.
- **`cycle`.** `graph.CycleGroups(edges)` finds the strongly connected components with Tarjan's algorithm, written iteratively so a long chain of blockers can't overflow the goroutine stack. A component of one task is not a cycle: a task's own ID in its `blocked_by` makes its file corrupt, so it has no edge to itself. Groups are sorted by their lowest ID, and each group's IDs ascending. `graph.ExampleCycle(group, edges)` runs the [cycle check](#cycle-check)'s breadth-first search, over the group's edges only, from its lowest ID L until an edge leads back to L. By the same argument as [Why the first path found is the one required](#why-the-first-path-found-is-the-one-required), that is the shortest cycle through L, then lexicographically smallest. The graph is built as the cycle check's is (from usable task files only, with a duplicated ID's edges the union of its copies'), but over the whole tree.
- **`suggest`.** Built by each check, as a command where one fits (e.g. `koan unblock 12 --blockers 15`). Tests check only that it is present where the operations spec gives one, since it is not part of the contract.

### Repair steps

`repair` computes the findings with the same checks, under the same lock. It keeps only the items of the kinds it repairs whose `action` is not `null` (all of them, not just the 20 the output lists), and applies them in its [Crash behavior](operations.md#repair) order. Then it calls `tx.NextStep()` and runs the checks again, for the findings left.

- **Temp folders** are removed with `RemoveAll` through the root, as [`delete-folder`](operations.md#delete-folder)'s last step does.
- **Dangling references** are removed by the function [`unblock`](operations.md#unblock) uses to take IDs out of one task file's `blocked_by` and set `updated_at`, so the two can't drift apart. `repair` calls it once per task file, with every missing ID in that file.
- **Orphan notes** are checked again just before removal, with a fresh `Lstat` (still zero bytes, or still the same file as the task's notes). A `.md` that no longer qualifies is left, and the second run of the checks reports it.

### `doctor` and `repair` tests

- **Cycle groups.** On thousands of small random graphs, `CycleGroups` must match the groups from a transitive closure (two IDs share a group exactly when each reaches the other), and `ExampleCycle` must match enumerating every simple cycle through L and picking the shortest, then lexicographically smallest.
- **One fixture per finding kind**, in-process: a tree built by hand, the exact item `doctor` reports for it, and what `repair` leaves. This covers the outside changes the crash matrix can't make: a `last_id` merged backwards, a duplicate ID, a cycle, a nested tree, a skipped entry, a stray entry (absent unless asked for, and healthy either way), an unreadable folder, sibling folders that differ only in case, a missing or unusable `koan.json`.
- **System-crash states**, as fixtures too, since crash injection kills processes, not machines: a task file above `last_id`, an empty task file, a removal undone.
- **Every `orphan-notes` reason**, including a `linked` `.md` that is changed, or given a different inode, between the check and the removal: it is left, and reported.
- **Caps.** 25 temp leftovers: 20 items, `count` 25, `truncated`; with `kinds` naming the kind, all 25.
- **Preconditions.** `repair` with `koan.json` missing, with and without `metadata-missing`; with it corrupt, and with a newer `schema`: an error, and nothing changed.

## Comparing values in `update`

[`update`](operations.md#update) reports a field as changed only when its value differs *as a JSON value*: maps regardless of key order, `tags` as a set, numbers by numeric value. Numbers inside `extra` are `json.Number`, compared exactly with `math/big.Rat` (`SetString` parses decimal and exponent forms exactly), never through `float64`, which would call `1e400` equal to `2e400` or merge distinct 20-digit integers. Numbers outside `extra` are integers within ±(2^53−1) and compare as `int64`.

## Testing

### Test hooks

Tests that must pause a write or crash it at an exact point use hooks compiled only into a binary built with `-tags e2e_hooks`. The shipped binary contains no hooks, so no environment variable can make a real koan pause or crash. The hooks set the process's environment before it runs — a fixed clock, a fault-injecting `fsys`, a pause once the write lock is taken — through variables named `KOAN_E2E_*`, documented in `cmd/koan/hook_e2e.go`. `e2e/` builds and runs the tagged binary; a short smoke suite also runs the release build, to confirm the tag changes nothing else.

### Tests specified elsewhere

| Area | Tests | Section |
|---|---|---|
| JSON reading | the edge-case table: repeated keys, `2.0`, invalid UTF-8, byte-order mark, empty input, a second value | [JSON reading](#json-reading) |
| JSON writing | exact bytes of a task file; `extra` numbers round-tripped unchanged | [JSON writing](#json-writing) |
| Validation | adapter–schema agreement; output conformance of every envelope | [Schemas in tests](#schemas-in-tests) |
| Cycle check | brute-force comparison on random graphs; corrupt file ordered after `id` | [Cycle check](#cycle-check) |
| `doctor` and `repair` | cycle groups by brute force; a fixture per finding kind; system-crash states; orphan rechecks; caps; preconditions | [`doctor` and `repair` tests](#doctor-and-repair-tests) |
| OS errors | errno-table completeness per platform; one test per call-site row | [OS errors](#os-errors) |
| Exit and signals | closed pipe, `/dev/full`, SIGTERM, forced panic → 134, one envelope per exit `0`/`1`/`2` | [Exit and signals](#exit-and-signals) |

### Lock

In `e2e/`, on Linux and macOS. Every command's wait for the lock is shortened to 100 ms (`KOAN_E2E_LOCK_WAIT`, a [test hook](#test-hooks)), so a `busy` comes quickly; test 8 uses the default.

1. **Contention.** A write held open at a test hook; a second write gets `busy` (exit `1`), while reads still succeed.
2. **One lock however the root is reached.** The second writer comes in through a symlinked root path, and through a different config (another `XDG_CONFIG_HOME`) naming the same root: both get `busy`.
3. **Release on crash.** The holder is killed with SIGKILL; the next write succeeds, with nothing to clean up.
4. **Keep-alive.** The hook forces a garbage collection while the lock is held; the lock is still held ([Mechanism](#mechanism)).
5. **Stress.** 16 processes each create 50 tasks, retrying on `busy`. Afterwards: 800 tasks, all IDs distinct, `last_id` 800, no lost update.
6. **Racing `block`s.** `block A --blockers B` and `block B --blockers A` run concurrently, each retrying on `busy`, many rounds: in each, exactly one succeeds and the other ends `conflict` (`acyclic`).
7. **Diagnostics take the lock.** A `doctor` held open at a test hook makes a write `busy`; a write held open makes `doctor` and `repair` `busy`.
8. **Waiting.** With the default wait, a write started while another holds the lock waits, and completes with the next ID once the holder releases.

The macOS run of this suite is the empirical check [Mechanism](#mechanism)'s platform check assigns to the test suite.

### Crash injection

Each write operation's **Crash behavior** lists what each step can leave behind; the fault-injecting [`fsys`](#import-direction) makes that testable. For every write, and every step *k* that changes the disk, the operation is run and the process killed with SIGKILL just before step *k*. Then:

- the tree is what Crash behavior says (e.g. `create` killed before step 2: `last_id` incremented, no task; before step 3: a task with no `.md`, whose notes read as empty);
- every invariant holds, as the design spec promises after a process crash;
- the next write succeeds — nothing is wedged;
- `doctor` reports exactly the findings Crash behavior predicts for that point, and nothing else: none, a `temp-leftover`, or the `orphan-notes` an interrupted `delete` or `move` leaves;
- `repair`, then `doctor`, reports `healthy: true` — except for the `no-task` `orphan-notes` an interrupted `delete` of a task with notes leaves, which is a person's to remove;
- rerunning the operation gives exactly the outcome its **Retry safety** claims.

`repair` is one of the writes in the matrix, run on a tree seeded with one item of each auto kind: after a crash before any step, `doctor` reports a subset of the seeded findings and nothing new, and rerunning `repair` finishes the job.

**Errors midway.** The same steps with an injected errno instead of a kill: the envelope's `partial` matches the operation's partial schema and what took effect.

System crashes — steps lost or reordered by power loss — are not simulated. Each published file is flushed, with its folder, before the next step ([Writing files](#writing-files)), so publishes keep their order; the steps that aren't flushed — creating folders, renames, removals — make no durability promise, and the design spec leaves their consequences to [`doctor` and `repair`](#doctor-and-repair), whose tests build those states as fixtures.

### Precedence tests

For each operation, a set of faults, each of which alone triggers one error kind: bad input, missing config, corrupt `koan.json`, the lock held, a missing folder, a corrupt needed file, a duplicated ID, a cycle, and so on. Every pair of faults that applies to the operation is combined in one tree, and the error reported must be the one earlier in the operation's [precedence](operations.md#precedence) (or `init`'s own order). Two faults at the same step (e.g. two corrupt needed files) must report the first in [tree order](operations.md#tree-order).

### Generated and cross-cutting

- **Fuzzing** (Go's native fuzzer) of the JSON reader and the CLI: any command line yields a defined envelope and never a panic, since a panic is a crash (exit 134).
- **Determinism.** Every read runs twice over the same tree; the bytes must match, warnings and `problems` order included.
- **The CLI spec's examples.** Every `sh` block in the [CLI spec](cli-spec.md) is to run against a fixture tree and exit as its context implies, producing JSON that `jq` accepts; examples needing outside tools (`gh`, `$EDITOR`) are marked and skipped. No test does this yet, so CI does not check the examples.
- **Race detector.** Every in-process test runs under `go test -race`.

### CI matrix

`.github/workflows/ci.yml` runs on every push to `main` and every pull request. A new commit on a pull request cancels that pull request's running check; every commit on `main` gets its own.

**Go version.** The `toolchain` line in `go.mod`, which `actions/setup-go` installs; each job prints `go version`. The `go` line stays the minimum needed to build koan. No other file names a Go version.

**Test matrix.** Linux amd64 (`ubuntu-latest`) and macOS arm64 (`macos-latest`), neither cancelled by the other's failure. Each runs, as separate steps:

1. `gofmt -l .`, which must print nothing;
2. `go vet ./...`, then `go vet -tags e2e_hooks ./...` for the [test hooks](#test-hooks);
3. `go build ./...`;
4. `scripts/fetch-fzf.sh`, which downloads fzf 0.63.0 and the current release, checked against pinned checksums, for the picker's [end-to-end tests](pick-spec.md#testing), which run against each fzf in `KOAN_E2E_FZF` (else the one on `PATH`, else skip);
5. `go test ./...`, never with `-short`: the [lock](#lock) stress tests (5 and 6) skip under it, and their macOS run is the check [Mechanism](#mechanism) relies on;
6. `scripts/smoke.sh`, installing `jq` first if the runner lacks it.

Every test runs on both, except the two `/dev/full` tests ([Exit and signals](#exit-and-signals)), which skip elsewhere: macOS has no `/dev/full`.

**Race detector.** A separate Linux job runs `go test -race` over every package except `e2e/`, whose binaries are built without `-race`, so the detector would see only the test harness.

**Vulnerabilities.** A separate Linux job runs `govulncheck ./...`, pinned to a version, and fails on any vulnerability koan's code can reach, in a dependency or in the standard library of the Go version in use. Vulnerabilities only in required modules, which koan never calls, are reported by `-show verbose` and do not fail it.

**Actions** are pinned to a full commit SHA, with the version in a comment.
