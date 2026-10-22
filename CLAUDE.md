# koan rewrite: instructions for agents

This repo is the rewrite of koan: task management for one person on one machine, driven by agents. Tasks and structure live in SQLite; notes are plain files in a tree people edit in Zed. The design is finished and written down. Your job is to build exactly what it says.

## The docs decide

| Doc | Says | Path |
|---|---|---|
| Rules doc | What must always hold: invariants, rules, refusals | `docs/rules.md` |
| PRD | How koan is built: layout, storage, writes, concurrency, recovery | `docs/prd.md` |
| Command specification | Exactly what each command takes and prints | `docs/spec.md` |
| Schemas | The spec's JSON Schemas, one file each | `schemas/*.json` |

- **Precedence:** rules doc, then PRD, then spec. Where code and docs disagree, the docs win.
- **Never invent behaviour.** If the docs don't say what should happen, stop and say so: name the case, the doc and the section. Don't pick an answer and carry on.
- **A change of behaviour starts in the docs.** It goes into the rules doc or the PRD first, then the spec, then the code. Never change code to a behaviour the docs don't have.
- **Read only what your task names.** Each task lists the doc sections it builds. Search the docs (`rg`) for anything else you need; don't load them whole.

## How to work

- **One slice per task.** A task builds one slice of the implementation plan and nothing beyond it.
- **Tests first.** Write the slice's tests from the docs before the code. Command output is checked against `schemas/`; behaviour against the rules doc's rules and examples.
- **Done means:** the slice's tests pass, `go test ./...` passes, `go vet ./...` is clean, and no behaviour was added that the docs don't describe.
- **Report gaps, don't paper over them.** List any doc gap or contradiction you hit in your final message, even if you worked around it in a test.

## Layout (proposed; follow it unless the task says otherwise)

```
cmd/koan/            main: wires the CLI to the operations
internal/cli/        command line → input JSON, envelope, exit codes
internal/core/       pure logic, no I/O: references, names, readiness, orders, cycles
internal/model/      in-memory reference model of the rules doc, for tests only
internal/store/      SQLite: schema, pragmas, the lock, the steps of a write
internal/fsstep/     filesystem steps: pending rows, "done when", empty side, stuck
internal/ops/        one file per command
internal/markers/    .koan/ markers
internal/doctor/     doctor, repair, backup
schemas/             JSON Schemas, from docs/spec.md
testdata/            golden results
```

## Building and testing

- Go 1.25 or later. SQLite through `modernc.org/sqlite`: no cgo, ever.
- `go test ./...` runs everything; `go test -short ./...` skips the long concurrency and crash matrices.
- Tests run the binary with `HOME` set to a temp directory, so the root is `$HOME/koan-root` there.
- **Never run koan against the real `~/koan-root`.** Always set `HOME` to a scratch directory. The one exception: the installed `koan`, used through the koan skill, tracks work on this repo there, under `/koan`. The binary built from this repo still runs only with a scratch `HOME`.
- CI runs on Linux and on macOS with its default case-insensitive filesystem. Case and rename code must pass on both.

## Rules for the code

- Every command prints exactly one line of JSON on stdout, in the field order of its schema; help and completion, for people, print cobra's plain text. Exit codes are 0, 1, 2 and 3, as the spec says.
- Every failure has one error kind from the spec; a conflict also names its rule. Don't add kinds, rules, warnings or fields.
- Once COMMIT succeeds, the command succeeds; a failed filesystem step is a warning, never an error.
- Compare names byte for byte. Never rely on the filesystem's case rules; judge filesystem steps by listing the parent directory, never by `stat`.
- Add no dependency without asking.
- Don't write koan references (`@42`, folder paths) into commit messages or code comments.
