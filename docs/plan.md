# koan rewrite: implementation plan

The rewrite is built in slices. Each slice is one agent task: it names the doc sections it builds, the tests that show it works, and what it may not touch. A slice starts only when the slices it depends on are done.

Section names refer to `docs/rules.md` (rules), `docs/prd.md` (PRD) and `docs/spec.md` (spec).

## Order

```
S0 spikes ──► S1 harness ──► S3 storage ──► S4 filesystem steps ──► S5a ─► S5b ─► S5c ─► S5d ─► S5e ─► S5f ─┬─► S6 locating ─┐
                 │                                                                                             └─► S7 recovery ─┤
S2 core ─────────┴──── (needed by S3 onward)                                                                                    │
                                                                         S9 ship ◄── S8 hardening ◄── all of S5, S6, S7 ────────┘
```

- S2 can run beside S1. Everything from S3 to S5f is in sequence: each slice extends the one before.
- S6 and S7 start once S5f is done, and can run beside each other. Both need deleted and archived folders and IDs to exist: S6 for `where` and `context` on them, S7 for findings such as `orphan-notes` on archived IDs and for backing up `archived/`. Delete arrives in S5d and archive in S5f.
- S8's test suites can be written alongside S5 to S7; they must pass before S9.

## Dependencies allowed

Ask before adding any other.

| Package | For |
|---|---|
| `modernc.org/sqlite` | the database, no cgo |
| `github.com/spf13/cobra` | the command line, as today |
| `github.com/santhosh-tekuri/jsonschema/v6` | tests only: checking output against `schemas/` |
| `golang.org/x/sys/unix` | `flock`, `fsync` of directories, `F_FULLFSYNC` |

---

## S0. Spikes

Three short experiments whose answers the rest depends on. Write the findings to `docs/spikes.md`; the code is thrown away.

1. **SQLite and the lock.** `modernc.org/sqlite` in WAL with `synchronous = FULL`, `fullfsync = ON`, `busy_timeout`, `BEGIN IMMEDIATE`, and a `flock` on the root, on Linux and macOS. Does a reader in WAL ever wait on a writer? What does one commit cost on each OS?
2. **Case-insensitive filesystems.** On macOS: a case-only rename of a directory, then the parent listed. Confirm that listing shows the new spelling and that `stat` can't tell the two apart.
3. **Speed.** 10,000 tasks and 1,000 folders with realistic edges: time `frontier`, `task list`, `task show` and one write. Is 100 ms realistic (PRD, Goals)?

**Done when** `docs/spikes.md` answers all three with numbers, and names anything in the PRD that the answers call into question.

## S1. Harness and skeleton

**Read:** spec Introduction, Shared types, Invocation, Envelope, Errors, Input formats → Option values; PRD Command surface → Output, Errors.

**Build**
- The Go module, `cmd/koan`, `internal/cli`.
- The envelope, every error kind, exit codes 0–3, the standard-error line.
- The command-line rules: one subject, lists, `null` only for nullable fields, booleans, JSON values, `--`, and `usage` versus `invalid-input`.
- `-i/--input` for every command.
- `version`, and `info` on a missing root.
- **The test runner:** builds the binary, runs it with `HOME` set to a temp directory, parses the one line of output, and validates it against `schemas/` by command name. Golden results go in `testdata/`.
- CI on Linux and macOS, including `scripts/extract-schemas.py --check`.

**Done when** `version` and `info` pass their schemas on both systems, and a table of malformed command lines gives `usage` with exit 2.

## S2. Core: pure logic, no I/O

**Read:** rules Readiness, Claims (the frontier), References, Resolution, Fields, Case; spec Errors → Conflict rules (the order of a cycle's `ids`).

**Build** `internal/core`:
- Parsing references: every form in the References table, split as Resolution steps 1 and 2 say.
- Resolution against an interface (look up an ID, a child by exact name, a child ignoring case), so the store can implement it later.
- Checks for names, tags, titles, integers, timestamps and labels.
- Readiness, the frontier, work order, tree order, the cycle check (returning the cycle in the order the spec's `cycle` rule gives).

**Also build** `internal/model`: an in-memory tree that applies every change in the rules doc's Changes table. It is the oracle for S8's property tests, so it follows the rules doc literally, with no performance work.

**Done when** table tests pass for:
- every row of the References table;
- every Resolution step;
- the rules doc's worked example: the tree in "The example, as text", and each row of "Changes, in the example above";
- the Case table.

## S3. Storage and the write protocol

**Read:** PRD Layout, Storage, Writes (the steps of a write, Removing rows, Making a gate), Concurrency; spec Operations → init, info.

**Build** `internal/store`:
- The schema exactly as in PRD Storage, with `user_version`, the pragmas on every open, and `unsupported-format`.
- `init`: `--ids-from`, `not-a-root`, `exists`.
- `info` in full.
- The lock: `flock` with a 5-second wait, then `busy`.
- The steps of a write, 1 to 8, as a function every write goes through.
- Issuing IDs, from `root.first_id`.
- The rule for removing rows: dependents found through `edge_blocker`, their `updated_at` moved, and the ones left ready reported as `released`.

No filesystem steps yet: step 7 calls an interface S4 fills in.

**Done when**
- `init` and `info` pass their schemas;
- two processes writing at once serialize;
- a writer kept waiting gets `busy` after 5 seconds;
- a database in another format gets `unsupported-format`.

## S4. Filesystem steps

**Read:** PRD Writes (Filesystem steps, and everything after them up to Notes files), Concurrency (the paragraph on readers and pending steps); rules The tree on disk.

**Build** `internal/fsstep`:
- The pending rows and every step kind.
- "Done when", judged by listing the parent directory.
- The empty-side rule, with "empty" meaning zero-byte `@` notes only.
- Marking stuck steps, with the rest of their change.
- `fsync` of touched directories, then deleting the rows in one transaction.
- Temp-file-and-rename for notes and archive JSON.
- On reads: `pending-filesystem`, then the lock tried without waiting, the pending rows checked again, and `pending-step`.
- A fault-injection hook at every step, for tests.

**Done when**
- with a fault injected before and after COMMIT and at each step, the next write finishes the change;
- a stuck step is set aside and later writes go on;
- on macOS, a case-only rename completes rather than sticking.

## S5. Commands

Each sub-slice is its own task.

**Read for all of them:**
- the spec's Commands entries for the commands named;
- the spec's Records, Warnings and Errors;
- the rows of the rules doc's Changes table for those commands;
- the sections in the slice's "Also read" column, which hold rules the Changes rows only point to.

**Done when** every command in the slice passes its schema, and each refusal and warning it lists has a golden test.

| Slice | Commands | Also covers | Also read |
|---|---|---|---|
| S5a | `folder create`, `folder show`, `folder list`, `task create`, `task show`, `task list` (no edges yet) | missing parents, notes files written at create, `missing-notes`, `--fields`, tree order | rules Fields, Case |
| S5b | `task block`, `task unblock`, `task create` with edges, `task why`, `task frontier` | gates made with their notes file, `gates_made`, `cycle`, `released`, `reblocked`, `complete-blocker`, `claimed-blocked` | rules Readiness |
| S5c | `task complete`, `task reopen`, `task update`, `folder update`, `task claim`, `task claim-next`, `task release` | `changed`, `complete` and `claimed`, claims ending on complete and delete | rules Claims |
| S5d | `task move`, `folder move`, `task delete`, `folder delete` | missing parents on move, case-only rename, `moved-claimed`, `gate`, `into-itself`, `not-empty`, `has-material` | rules Case |
| S5e | `batch` | labels, the batch read as the tree it leaves, the five steps of Resolving a batch (PRD Writes), `problems` pointing at parts | spec Input formats → Batch file |
| S5f | `task archive`, `folder archive` | archive layout, JSON beside each item, lone-gate names, the index, `not-found` with `archived` filled in | rules Archiving; PRD Layout → The archive, Writes → Archiving |

## S6. Locating and markers

**Read:** PRD Markers, Layout → Markers on disk; spec Locating, Markers, On-disk formats → marker.json.

**Build** `where`, `context`, `marker bind` and `marker unbind`. That covers:
- the nearest marker, and the lookup stopping at the first one found;
- markers copied or moved (binding nothing);
- `stale-marker`;
- archived and deleted folders;
- `checkout`, `foreign-files` and `bound`;
- the real-path check for directories under the root;
- every unusual path in the spec's `where` entry.

**Done when** the PRD's Markers test row passes, along with schema and golden tests for every case above.

## S7. Recovery

**Read:** PRD Operations (all of it); spec Records → Finding, Operations → doctor, repair, backup.

**Build** `internal/doctor`:
- `doctor`, with every finding kind.
- `repair`, which:
  - takes the kinds in table order;
  - moves a stray directory back only when the match is one-to-one;
  - removes orphan notes only when they're empty;
  - retries stuck changes in order once a person has acted.
- `backup`: the database snapshot under the lock, copying without the lock, `doctor` run on both the copy and the live root, at most one re-copy under the lock, then `clean`.

**Done when** the PRD's Repair and Backup and restore test rows pass: each finding made by hand, then found, then repaired as the table says.

## S8. Hardening

**Read:** PRD Testing, Invariants enforced and tested; rules Invariants, The tree on disk (where I1–I12 and T1–T6 are defined).

**Build** the remaining test suites:
- **Properties:** random command sequences checked against `internal/model`, with `doctor` clean and I1–I12 and T1–T6 holding after every command.
- **The full crash-point matrix.**
- **Concurrency:** hundreds of processes at once.
- **Case tests on macOS.**
- **Performance benchmarks,** with CI failing past the budget in PRD Goals.

**Done when** every row of PRD Testing passes in CI on both systems, and `go test -short` stays quick.

## S9. Ship

**Read:** PRD Agents, Install, init, backup and restore; the spec's Commands.

**Build:**
- the koan skill, rewritten against the spec;
- the Claude Code plugin;
- `scripts/install.sh` for OpenCode;
- the README.

Then use it for real work for a week before calling it done.

---

## Task brief template

Give each agent this, filled in, and nothing else of the plan:

```
Slice: S5b — edges
Read: spec Commands → task block/unblock, task create, task why, task frontier;
      spec Records, Warnings, Errors; rules Readiness, Changes (Block, unblock; Create a task)
Build: (the slice's Build list)
Done when: (the slice's Done when)
Don't: touch other slices' commands; add dependencies; add behaviour the docs don't have.
Report: what you built, the tests, and every gap or contradiction you found in the docs.
```

After each slice, a separate reviewer agent checks the code against the same sections before the next slice starts. A gap either of them finds is fixed in the docs first.
