# koan CLI

How the `koan` command line maps onto the [operations](operations.md). The CLI adds no behaviour of its own: it builds one operation's input from the command line, runs it, and prints its envelope. The intended caller is an agent, or a person with `jq`.

The interactive picker is not part of this document.

## Output

- **One envelope, always.** Every run prints exactly one [envelope](operations.md#input-and-output) to stdout, on one line, success or failure. There is no other output format. `--help` is the exception: it prints usage text.
- **stderr** gets at most one line, for a person watching a pipeline: `koan: <kind>: <message>` on failure, or `koan: N warnings` on a success that has warnings. Callers read the envelope, never stderr.

| Exit | Meaning |
|---|---|
| `0` | Success (`ok: true`), with or without warnings. |
| `1` | The operation failed. The envelope's `error.kind` says how. |
| `2` | The command line could not be understood: `error.kind` is `usage`. |
| other | koan was killed, or could not write to stdout. The outcome is unknown: look before running it again ([Crashes and failures](design.md#crashes-and-failures)). |

Callers branch on `error.kind`, not on the exit code.

## Input

Every command takes its input in one of two ways, never both:

- **Arguments and options**, as the table below gives them.
- **`-i, --input <file>`**: the operation's whole input as one JSON object, read from `<file>`, or from stdin when `<file>` is `-`. This is the only way to run `create-batch`.

koan reads stdin only when `-` names it, so it is safe in a loop that feeds stdin to something else.

## Command line

Parsing is cobra's GNU-style parsing; these are the rules koan adds.

- **A command is named after its operation**, and an option after the input field it sets, in kebab-case: `blocked_by` is `--blocked-by`.
- **The one subject is positional**: the task's ID, the folder (its path, or its ID where it must exist), the new task's title, `where`'s reference. It is required, but for `where`, which takes `--file` in its place. Everything else is an option, so a bare word has one meaning.
- **Folder paths are exact**: always from the root, with a leading `/`. koan never completes one or derives one from the working directory.
- **References**: wherever an input takes a [reference](design.md#references), the command line does too: `koan show 42`, `koan block @42 --blockers 17,@18`, `koan list --folder 41`, with or without the `@`. An ID without `@` in a task position is passed to the operation as an integer; everything else as the string typed.
- **Booleans**: `--recursive` is `true`, `--recursive=false` is `false`.
- **Lists** are comma-separated, and the option may be repeated: `--tags travel,urgent` or `--tags travel --tags urgent`. The exception is `--extra-remove`, which takes one key per occurrence, since a key may contain a comma.
- **`null`** is written `null`: `--priority null` clears a priority.
- **JSON values** (`--extra`, `--extra-merge`, `--extra-replace-all`) are one JSON object in one argument.
- **Names are matched exactly**: no abbreviations, so adding a command or option never changes what an existing command line means.
- **`--`** ends the options, for a title that starts with `-`.

A command line the CLI can't turn into an input is a `usage` error: an unknown command or option, a missing or extra argument, an option without its value, or `--input` together with other arguments. A value in the right place that is wrong (`koan show abc`) is the operation's `invalid-input`, exactly as it would be through `--input`.

## Commands

| Command | Options |
|---|---|
| `koan init` | |
| `koan version` | |
| `koan info` | |
| `koan doctor` | |
| `koan repair` | `--kinds` |
| `koan migrate` | `--dry-run` |
| `koan create-folder <folder>` | `--title`, `--extra`, `--notes`, `--notes-file` |
| `koan show-folder <folder>` | |
| `koan update-folder <folder>` | `--title`, `--extra-merge`, `--extra-remove`, `--extra-replace-all` |
| `koan move-folder <folder> --to <folder>` | |
| `koan delete-folder <folder>` | `-r, --recursive`, `--force` |
| `koan create <title>` | `--folder`, `--priority`, `--tags`, `--blocked-by`, `--blocks`, `--extra`, `--notes`, `--notes-file` |
| `koan create-batch -i <file>` | |
| `koan show <id>` | |
| `koan update <id>` | `--title`, `--priority`, `--tags-add`, `--tags-remove`, `--tags-replace-all`, `--extra-merge`, `--extra-remove`, `--extra-replace-all` |
| `koan done <id>` | |
| `koan reopen <id>` | |
| `koan block <id> --blockers <ids>` | |
| `koan unblock <id> --blockers <ids>` | |
| `koan move <id> --to <folder>` | |
| `koan delete <id>` | |
| `koan frontier` | `--folder`, `--recursive`, `--tags-any`, `--tags-all`, `--limit`, `--fields` |
| `koan list` | `frontier`'s, and `--claimed`, `--readiness`, `--include-folders` |
| `koan why <id>` | |
| `koan where <ref>` | `--file` |
| `koan claim <id>` | `--extra` |
| `koan claim-next` | `--folder`, `--recursive`, `--tags-any`, `--tags-all`, `--extra` |
| `koan release <id>` | |

Every command also takes `-i, --input <file>` and `-h, --help`. `--to` and `--blockers` are required, and named, because a second bare word would not say what it is.

`koan where` takes either its reference or `--file <path>`, not both. A relative `--file` is taken from the working directory, the one place the CLI reads it, since a path on disk is what a shell has to hand.

One option sets a field under another name: `create`'s and `create-folder`'s `--notes-file <file>` reads the notes from a file, or from stdin for `-`, exactly as they are. It can't be given with `--notes`.

## Examples

```sh
koan create 'Book flights' --folder /proj/travel --priority 2 --tags travel
koan create 'Write report' --notes-file draft.md --blocked-by 41,42
jq -n '{tasks: [{ref: "a", title: "First"}, {title: "Second", blocked_by: ["a"]}]}' | koan create-batch -i -

koan frontier --limit 5 --fields id,title,priority
koan list --claimed --fields id,title,claim
koan list --fields id,title,extra | jq -c '.result.tasks[] | select(.extra.status == "waiting")'
koan why 42 | jq '.result.ready'
koan where @41:docs/plan.md | jq -r '.result.file'
koan where --file docs/plan.md | jq -r '.result | "\(.ref) (\(.hint))"'

koan update 42 --priority null --tags-add urgent --extra-merge '{"status":"waiting"}'
koan block 42 --blockers 17,18
koan move 42 --to /archive
koan done 42

koan claim-next --folder /proj --extra '{"agent":"shingi-3"}' | jq '.result.task.id'
koan release 42

koan doctor | jq '.result.findings[] | select(.repairable | not)'
koan repair --kinds dangling-reference
```
