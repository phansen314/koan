# koan CLI

How the `koan` command line maps onto the [operations](operations.md). The CLI adds no behaviour of its own, but for the [conveniences](#cli-conveniences) below: it builds one operation's input from the command line, runs it, and prints its envelope. The intended caller is an agent, or a person with `jq`.

The interactive picker is not part of this document.

## Output

- **One envelope, always.** Every run prints exactly one [envelope](operations.md#input-and-output) to stdout, on one line, success or failure. There is no other output format. `--help` is the exception: it prints usage text.
- **stderr** gets at most one line, for a person watching a pipeline: `koan: <kind>: <message>` on failure, or `koan: N warnings` on a success that has warnings. Callers read the envelope, never stderr.

| Exit | Meaning |
|---|---|
| `0` | Success (`ok: true`), with or without warnings. |
| `1` | The command failed. The envelope's `error.kind` says how. |
| `2` | The command line could not be understood: `error.kind` is `usage`. |
| `3` | koan could not write the envelope to stdout. The outcome is unknown: look before running it again ([Crashes and failures](design.md#crashes-and-failures)). |
| other | koan was killed. The outcome is unknown, as for `3`. |

Callers branch on `error.kind`, not on the exit code.

## Input

Every command takes its input in one of two ways, never both:

- **Arguments and options**, as the table below gives them.
- **`-i, --input <file>`**: the operation's whole input as one JSON object, read from `<file>`, or from stdin when `<file>` is `-`. This is the only way to run `batch`.

koan reads stdin only when `-` names it, so it is safe in a loop that feeds stdin to something else.

## Command line

Parsing is cobra's GNU-style parsing; these are the rules koan adds.

- **A command is its operation's name**, `koan task create` running `task create`, and an option is named after the input field it sets, in kebab-case: `blocked_by` is `--blocked-by`.
- **The one subject is positional**: the task's ID, the folder (its path, or its ID where it must exist), the new task's title, `where`'s reference. It is required, but for `where`, which takes `--file` in its place. Everything else is an option, so a bare word has one meaning.
- **Folder paths are exact**: always from the root, with a leading `/`. koan never completes one or derives one from the working directory.
- **References**: wherever an input takes a [reference](design.md#references), the command line does too: `koan task show 42`, `koan task block @42 --blocked-by 17,@18`, `koan task list --folder 41`, with or without the `@`. An ID without `@` in a task position is passed to the operation as an integer; everything else as the string typed. A list of blockers mixes IDs and folder paths: `--blocked-by 17,/proj/research`, each item passed as an ID would be or as the path typed (a folder name has no comma).
- **Booleans**: `--recursive` is `true`, `--recursive=false` is `false`.
- **Lists** are comma-separated, and the option may be repeated: `--tags travel,urgent` or `--tags travel --tags urgent`. The exception is `--extra-remove`, which takes one key per occurrence, since a key may contain a comma.
- **`null`** is written `null`: `--priority null` clears a priority.
- **JSON values** (`--extra`, `--extra-merge`, `--extra-replace-all`) are one JSON object in one argument.
- **Names are matched exactly**: no abbreviations, so adding a command or option never changes what an existing command line means.
- **`--`** ends the options, for a title that starts with `-`.

**`usage` is only a command line the CLI can't turn into an input:** an unknown command or option, a group without a command (`koan task`) or with one it doesn't have (`koan task frob`), a missing or extra positional argument, an option without its value, `--input` with other arguments, or `--notes` with `--notes-file`. Every check of a value is the operation's, and is `invalid-input` exactly as it would be through `--input`: a missing required option, a value in the right place that is wrong (`koan task show abc`), or a JSON value that is not JSON, which the CLI passes on as a string. A file that `--input` or `--notes-file` names and koan can't read, or a working directory `--file` can't be resolved against, is `io`.

## Commands

| Command | Options |
|---|---|
| `koan init` | |
| `koan version` | |
| `koan info` | |
| `koan doctor` | |
| `koan repair` | `--kinds` |
| `koan migrate` | `--dry-run` |
| `koan folder create <folder>` | `--title`, `--extra`, `--notes`, `--notes-file`, `--blocked-by`, `--blocks` |
| `koan folder show <folder>` | |
| `koan folder update <folder>` | `--title`, `--extra-merge`, `--extra-remove`, `--extra-replace-all` |
| `koan folder move <folder> --to <folder>` | |
| `koan folder delete <folder>` | `--contents`, `--force` |
| `koan folder list` | `--folder`, `--recursive`, `--limit`, `--fields` |
| `koan folder block <folder> --blocked-by <ids>` | |
| `koan folder unblock <folder> --blocked-by <ids>` | |
| `koan folder why <folder>` | |
| `koan task create <title>` | `--folder`, `--priority`, `--tags`, `--blocked-by`, `--blocks`, `--extra`, `--notes`, `--notes-file` |
| `koan batch -i <file>` | |
| `koan task show <id>` | |
| `koan task update <id>` | `--title`, `--priority`, `--tags-add`, `--tags-remove`, `--tags-replace-all`, `--extra-merge`, `--extra-remove`, `--extra-replace-all` |
| `koan task done <id>` | |
| `koan task reopen <id>` | |
| `koan task block <id> --blocked-by <ids>` | |
| `koan task unblock <id> --blocked-by <ids>` | |
| `koan task move <id> --to <folder>` | |
| `koan task delete <id>` | |
| `koan task frontier` | `--folder`, `--recursive`, `--tags-any`, `--tags-all`, `--limit`, `--fields` |
| `koan task list` | `task frontier`'s, and `--claimed`, `--readiness` |
| `koan task why <id>` | |
| `koan where <ref>` | `--file` |
| `koan task claim <id>` | `--extra` |
| `koan task claim-next` | `--folder`, `--recursive`, `--tags-any`, `--tags-all`, `--extra` |
| `koan task release <id>` | |

Every command also takes `-i, --input <file>` and `-h, --help`. `--to` is required, as is `--blocked-by` on `task block`, `task unblock`, `folder block`, and `folder unblock`; they are named because a second bare word would not say what it is.

`koan where` takes either its reference or `--file <path>`: neither or both is `invalid-input`, as through `--input`.

## CLI conveniences

Two options do what an input does not, since they need a file or a working directory, which only a shell has to hand. Through `--input`, give what they stand for.

- **`--notes-file <file>`**, on `task create` and `folder create`, reads the notes from a file, or from stdin for `-`, exactly as they are, and sets `notes`. An input has no `notes_file`.
- **`where --file <path>`** takes a relative path from the working directory, where an input's `file` must be absolute.

## Examples

```sh
koan task create 'Book flights' --folder /proj/travel --priority 2 --tags travel
koan task create 'Write report' --notes-file draft.md --blocked-by 41,42
jq -n '{tasks: [{ref: "a", title: "First"}, {title: "Second", blocked_by: ["a"]}]}' | koan batch -i -

koan task frontier --limit 5 --fields id,title,priority
koan task list --claimed --fields id,title,claim
koan task list --fields id,title,extra | jq -c '.result.tasks[] | select(.extra.status == "waiting")'
koan task why 42 | jq '.result.ready'
koan folder list --folder /proj --recursive=false --fields id,path,title
koan where @41:docs/plan.md | jq -r '.result.file'
koan where --file docs/plan.md | jq -r '.result | "\(.ref) (\(.hint))"'

koan task update 42 --priority null --tags-add urgent --extra-merge '{"status":"waiting"}'
koan task block 42 --blocked-by 17,18
koan folder create /proj/phase2 --blocked-by /proj/phase1
koan folder why /proj/phase1 | jq '.result.ready'
koan task move 42 --to /archive
koan task done 42

koan task claim-next --folder /proj --extra '{"agent":"shingi-3"}' | jq '.result.task.id'
koan task release 42

koan doctor | jq '.result.findings[] | select(.repairable | not)'
koan repair --kinds dangling-reference
```
