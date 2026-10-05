# koan CLI spec

The `koan` command-line interface: how each command maps to the [operations](operations.md), how input gets in, and what comes out. The CLI adds no behavior of its own beyond parsing arguments — including any input resolution a command's Input part lists, such as `init`'s path resolution or `create`'s `--notes-file` — and composing operations; everything about the data is specified by the operations and the [design spec](design-spec.md).

The intended user is a power user working through Claude, with `jq` for anything a person reads directly. The one exception is [`pick`](#pick), an interactive picker for people, specified in [pick-spec.md](pick-spec.md).

## Global behavior

### Output

- **JSON only.** Every invocation that exits `0`, `1`, or `2`, other than `--help`, writes exactly one [output envelope](operations.md#output-envelope) to stdout, success or failure. There is no human-readable output mode; `jq` does the pretty-printing.
- **Passthrough.** A command that runs one operation writes that operation's envelope unchanged. Where a command's output differs from its operation's, the command's entry says how.
- **Compact.** The envelope is written on a single line, followed by a newline. The format is the same whether or not stdout is a terminal. A complete envelope always ends in that newline.
- **Encoding.** Output is UTF-8, with the string escaping of the design spec's [File format](design-spec.md#file-format): only `"`, `\`, U+0000–U+001F, U+2028, and U+2029 are escaped; everything else is raw UTF-8.
- **Delivered before exit.** Exit `0`, `1`, or `2` is reported only once the whole envelope has been written. On any other exit status, stdout may hold nothing or an incomplete line (see [Exit codes](#exit-codes)).
- **stderr** gets at most one line from koan, written after the envelope has been delivered, so a failure stays visible when stdout goes into a pipeline (e.g. `koan create … | jq -r .result.id`):
  - exit `1` or `2`: `koan: <kind>: <message>` — even when the envelope also has warnings; a usage error's message drops its own leading `usage: `, so the line reads `koan: usage: unknown command`;
  - exit `0` with warnings: `koan: N warnings (see .warnings in the output)` (`1 warning` for one); the warnings themselves are never listed;
  - exit `0` without warnings, and `--help`: nothing;
  - exit `3`: only its notice (see [Exit codes](#exit-codes)); a crash (any other exit status) may add its own diagnostics.

  The line is the same whether or not stderr is a terminal, and kept to one line: control characters in it (e.g. a newline in a path) are escaped. It is human-readable and not part of the contract, like an error's `message`; callers read the envelope. A failure to write it is ignored and never changes the exit status.
- **Exception:** `--help` writes plain-text usage to stdout. It is not an operation.
- **Exception:** [`pick`](#pick) draws an interactive picker on the terminal (`/dev/tty`). Its stdout still gets exactly one envelope.

### Input

- **Flags and arguments** supply operation input for everyday use.
- **`-i, --input <file>`** supplies operation input read from `<file>`; `-` means stdin. Every command accepts it.
- **stdin is read only when a value names it:** `--input -`, or `create`'s `--notes-file -`. koan never reads stdin on its own, so it is safe inside loops and pipelines that feed stdin to something else (e.g. `while read id; do koan … "$id"; done < ids.txt`). When stdin is named, koan reads until end of input; if stdin is a terminal, it waits for it.
- **Any readable path.** `<file>` may be any path that can be read to the end, not only a regular file: process substitution (`-i <(jq -n …)`) and named pipes work. A file literally named `-` is given as `./-`.
- **Exactly one JSON value.** The input is one JSON object, optionally surrounded by whitespace. Empty input, a value that is not an object, a second value (e.g. several objects from `jq -c '.[]'`), or any other trailing bytes are `invalid-input` (`field`: `""`).
- **UTF-8.** The input is UTF-8 with no byte-order mark. A byte-order mark or invalid UTF-8 is `invalid-input` (`field`: `""`).
- **No unpaired surrogates, bounded nesting.** A `\u` escape of an unpaired UTF-16 surrogate (e.g. `\ud800` alone), or objects and arrays nested more than 9,990 levels deep, is `invalid-input` (`field`: `""`), as in a [file](design-spec.md#file-validity).
- **Operation rules apply.** The input is held to the operation's input schema and to the [input conventions](operations.md#conventions); violations are `invalid-input`.
- **Unreadable input.** A missing or unreadable `<file>`, or a directory, is `io`.

```sh
jq -n '{title: "x"}' | koan create -i -
koan create -i - < req.json
koan create -i req.json
koan create -i <(jq -n '{title: "x"}')
```

- **Either `--input` or field arguments, not both.** `--input` supplies the whole operation input. Giving it together with any argument or option that sets an input field is a [usage error](#usage-errors). Options that set no input field (e.g. `--help`) are unaffected.
- **`--input` is taken as-is,** as the operation's input, except for any resolution the command's Input part lists (e.g. `init`'s [path resolution](#init)), which applies to a field whether it comes from an argument or from `--input`.

### Command line

The command line is parsed in the conventional GNU style of Go's [cobra](https://github.com/spf13/cobra) and [pflag](https://github.com/spf13/pflag) libraries. The rules below are what koan relies on; anything they leave open is the libraries' behavior.

- **Command names are operation names.** A command that runs one operation has that operation's name (`create-folder`, `show`, `done`). A command that composes several operations gets a name of its own.
- **Option names are field names.** An option that sets an input field is named after that field, in kebab-case: `blocked_by` is `--blocked-by`, `replace_config` is `--replace-config`. A nested field is named by its path: `/tags/add` is `--tags-add`, `/extra/replace_all` is `--extra-replace-all`. Options that set no field under their own name (e.g. `--notes-file`) are the exceptions, and each command lists them.
- **Arguments are for the one required subject.** A command's single required subject — the task it acts on, the folder it creates, the title it needs — is a positional argument. Everything optional is an option. There are no optional arguments, so a bare token always has one meaning, and a field keeps one spelling across commands (e.g. `folder` is `--folder` everywhere except [`create-folder`](#create-folder), where it is the subject).
- **Booleans.** `--<field>` sets `true`; `--<field>=false` sets `false` (e.g. `--recursive=false`). A boolean never takes the next token as its value: in `--recursive false`, `false` is an argument.
- **Required options.** An option a command marks as required — typically one whose position alone would not make its meaning clear — is a usage error when missing, unless `--input` is given.
- **Short options are rare.** An option gets a one-letter form only when it has a strong Unix precedent (e.g. `-p` for `--parents`, as in `mkdir -p`) or is used constantly. Everything else is long-form only.
- **Folder paths are exact.** A folder path given as an argument or option is taken exactly as a [folder path](design-spec.md#folder-paths): from the root, which is `/` (e.g. `/proj/travel`). The CLI does not complete or normalize it — `proj/travel` and `/proj/` are `invalid-input` — and never derives one from the working directory.
- **Options and arguments follow the command,** in any order: `koan <command> [options and arguments]`. The exception is `--help`, which may also be given with no command (`koan --help`).
- **`--`** ends options. Everything after it is an argument, even if it starts with `-` (e.g. a title like `-urgent`). The command itself must come before it.
- **A lone `-`** is an ordinary argument, not an option.
- **Option values** may be given as `--flag value` or `--flag=value`, and for a short form as `-i value` or `-ivalue`. Short boolean options may be combined (`-pi file`).
- **Exact names.** Commands and options are matched exactly: no abbreviations (`--fold` for `--folder`, `koan reo` for `reopen`) and no other case (`--Folder`, `koan Show`), so adding an option or command never breaks an existing command line.
- **Empty values** are values: `--folder ''` or `--notes=` sets the empty string, which the operation then judges like any other value (an empty folder path is `invalid-input`; empty notes are fine). For a comma list, `''` is the empty list (see *Value formats*).
- **An option that takes a value always consumes the next token,** even one starting with `-`, so `--priority -3` works.
- **Arguments are single tokens.** A value with spaces, such as a title, is one argument and must be quoted for the shell (`'Book flights'`; single quotes also keep `$` literal). The quotes are shell syntax, not part of the value. Unquoted words are extra arguments, a usage error; they are never joined.
- **Value formats.** An argument or option value that sets an input field is converted by its field's type:
  - *Integers* are decimal, with an optional leading `-`: no `+`, no leading zeros, no fraction or exponent, the same rule as for [`--input`](#input).
  - *Null.* For a field that may be `null`, the value `null` converts to it: `--priority null` clears the priority. For any other field, `null` is an ordinary value.
  - *Lists of items that cannot contain a comma* (tags, IDs, field names, readiness values) are comma-separated: `--tags travel,urgent`. Items are taken exactly and passed on: `a, b` (with a space), `a,,b`, and a duplicate item all reach the operation, which rejects them as `invalid-input`. An empty value (`''`) is the empty list. The option may be repeated; its lists are joined in order (`--tags a --tags b,c` is `a,b,c`).
  - *Lists of items that can contain a comma* (e.g. `extra` keys, which are arbitrary strings) use a **repeatable** option instead, one item per occurrence: `--extra-remove status --extra-remove owner`. Each occurrence is exactly one item, so `--extra-remove 'a,b'` names the single key `a,b`.
  - *JSON values* (e.g. `--extra`) are exactly one JSON value, held to the [input conventions](operations.md#conventions) (integer literals, no duplicate keys). Problems are reported at the option's field (e.g. `/extra`); the value's type — e.g. that it is an object — is checked by the operation.
  - *Encoding.* Every value is UTF-8; one that is not is `invalid-input` at its field.
  - A value that cannot be converted is `invalid-input` (see [Usage errors](#usage-errors)).
- **Mutually exclusive options,** where a command lists them, are a usage error when given together.
- **The CLI rejects only what it cannot build.** A combination of options is a usage error only when the CLI cannot construct an input from it — e.g. two options that set the same field, like `--notes` and `--notes-file`. A combination the CLI can build but the operation forbids (e.g. `update`'s `--tags-replace-all` with `--tags-add`, or no field to change at all) is passed to the operation, which rejects it as `invalid-input`. Rules about input live in the operation, not in the CLI.
- **A repeated option** that takes one value (e.g. `-i a -i b`, `--priority 1 --priority 2`): the last one wins. List options accumulate (see *Value formats*).
- **Bare `koan`**, with no command, is a usage error.
- **`--help`** (or `-h`) writes help text and exits `0`, running no operation: the command's help after a command (`koan create --help`), otherwise the general help. Help text is for humans and not part of the contract; which other problems on the same command line it overrides is the libraries' behavior.
- **No `help` or `completion` command.** Help is only `--help`: `koan help` and `koan completion` are usage errors, like any unknown command.

### Usage errors

A usage error is a problem with the command line itself: an unknown command or option, a missing or extra argument, an option missing its value, mutually exclusive options given together, two options that set the same field (e.g. `--notes` with `--notes-file`), `--input` together with field arguments or options. It is reported as an envelope with error kind `usage`, and exits with code `2`. `invalid-input` stays reserved for operation input, whose `field` is a JSON Pointer into that input; problems with an `--input` file's content are `invalid-input`, not `usage` (see [Input](#input)).

**Shape, not values.** `usage` is about the shape of the command line: an unknown, missing, extra, or conflicting token. A token in the right place whose value is unacceptable — one that cannot be converted to its field's type (e.g. `koan show abc`, where the ID is an integer) or that fails the operation's validation — is `invalid-input`, with `field` the JSON Pointer of the input field it sets, per the command's Arguments and Options tables. A bad value is therefore the same error whether it arrives as an argument or through `--input`. The one exception is a boolean option given a value with `=`: cobra parses that itself, and a value it can't parse as a boolean (`--recursive=maybe`) is `usage`, naming the option.

`usage` is a CLI-only error kind: no operation raises it, and it is not listed in the operations' [error kinds](operations.md#error-kinds). So are [`pick`](#pick)'s `cancelled`, `unavailable` and `incomplete`. As with any kind, callers treat an unknown one as a generic failure.

`details` reports the **first** problem the parser finds, as a one-item list; the list leaves room to report more in a later release. Its `reason` is human-readable and may change between releases.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "usage-details",
  "type": "object",
  "required": ["problems"],
  "properties": {
    "problems": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "required": ["reason"],
        "properties": {
          "argument": { "type": "string", "description": "The offending command-line token, e.g. --limt, when the parser names one. Absent when the problem is something missing (an argument, a required option, the command) or the parser names no single token." },
          "reason": { "type": "string", "description": "Human-readable." }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success (`ok: true`), with or without warnings. Also `--help`, which writes help text instead of an envelope. |
| `1` | Operation error. The kind is in the envelope. |
| `2` | Usage error. |
| `3` | Outcome unknown: the envelope could not be written to stdout. |
| any other | Outcome unknown: koan was terminated before it finished (e.g. `128+n` for signal `n`). Handle like `3`. |

- **One code for all operation errors.** Callers branch on the envelope's `kind` (e.g. `jq -e '.error.kind == "busy"'`), not on the exit code. A new error kind, a minor change under [Versioning](operations.md#versioning), therefore needs no new exit code.
- **Outcome unknown.** On `3` or any status outside `0`–`2`, the operation may already have taken effect, and there may be no envelope, or only part of one. The caller treats this like a crash: whether to rerun follows the operation's **Retry safety**. Exiting `1` here would invite a retry that, for `create`, makes a duplicate. For a read, rerunning is always safe.
- **Unwritable stdout.** When stdout cannot be written (e.g. a closed pipe, a full disk behind a redirect), koan exits `3` with a notice on stderr, or, for a closed pipe, may instead be terminated by `SIGPIPE`. Both mean outcome unknown. This applies even when nothing was run, e.g. a usage error with stdout closed.
- **Interrupts are crashes.** An interrupt or termination signal (e.g. Ctrl-C, `kill`, a timeout in the calling harness) ends koan like a crash: no envelope, exit `128+n`, and the operation's **Crash behavior** and **Retry safety** apply.

### Global options

| Option | Meaning |
|---|---|
| `-i, --input <file>` | Read operation input from `<file>` (`-` for stdin). See [Input](#input). |
| `-h, --help` | Print plain-text usage. See [Command line](#command-line). |

## Command template

Every command is specified with the same parts, in this order. Every part is always present except **Composition**, which appears only for commands that run more than one operation. An empty part is written `**Part:** none.`, optionally followed by one sentence saying why. A part may be followed by short unlabeled notes that belong to it.

| Part | Content |
|---|---|
| **Summary** | Unlabeled first paragraph: what the command does, in one or two sentences, and the operation(s) it runs, linked. |
| **Synopsis** | The usage line(s), e.g. `koan create <title> [options]`, including any aliases. |
| **Operation** | The operation the command runs; for a composed command, the operations, their order, and whether they run under one write lock. |
| **Arguments** | Table of positional arguments and the input field each sets. |
| **Options** | Table of command-specific options (not the [global options](#global-options)), the input field each sets, and its default. |
| **Input** | Anything about input beyond the Arguments and Options mapping, e.g. fields that only `--input` can set. |
| **Output** | `Passthrough.`, or exactly how the output differs from the operation's (e.g. a composed command's combined result). |
| **Errors** | Errors the CLI adds beyond the operation's, and their kinds. Usually none. |
| **Composition** | Only for composed commands: what a failure in a later operation leaves behind after an earlier one succeeded, crash behavior, and retry safety. |
| **Examples** | One or more `sh` examples, with the `jq` side where it helps. |

Exit codes are not a part: they follow from the envelope and the global [Exit codes](#exit-codes) table (`ok: true` is `0`; `ok: false` is `1`, or `2` for kind `usage`).

## Commands

### version

Report the version and build of the koan binary, and the data format versions it supports. Runs [`version`](operations.md#version).

**Synopsis:** `koan version`, or `koan version -i <file>`.

**Operation:** [`version`](operations.md#version).

**Arguments:** none.

**Options:** none.

**Input:** none. With `--input`, the only valid input is `{}`.

**Output:** Passthrough.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan version
```

### info

Report the state of this machine's configured root: the config, the tree it names, and whether this binary can use it. Runs [`info`](operations.md#info).

**Synopsis:** `koan info`, or `koan info -i <file>`.

**Operation:** [`info`](operations.md#info).

**Arguments:** none.

**Options:** none.

**Input:** none. With `--input`, the only valid input is `{}`.

**Output:** Passthrough.

A root that is not initialized or not usable is reported as state (`ok: true`, `usable: false`), so it exits `0`. To turn usability into an exit status, use `jq -e .result.usable`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan info   # ready when .result.usable is true; if not, the rest of .result says why
```

### init

Create a new tree, or attach an existing one, and make it this machine's configured root. Runs [`init`](operations.md#init).

**Synopsis:** `koan init <root> [--replace-config]`, or `koan init -i <file>`.

**Operation:** [`init`](operations.md#init).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<root>` | `/root` | Required unless `--input` is given. Resolved to an absolute path first; see Input. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--replace-config` | `/replace_config` | `false`. |

**Input:** `root` — from `<root>` or from `--input` — is resolved to an absolute path before the operation runs, since the operation leaves that to its caller:

- **`~/`** at the start is expanded to the user's home directory. `~user/` is `invalid-input` (`/root`), as it is in the [config](design-spec.md#root-path).
- **A relative path** is resolved against the working directory, as the shell reports it — through any symlinks, not with them resolved — matching how the design spec keeps the root [as the user spelled it](design-spec.md#root-path).
- **`..` segments are not removed.** The resolved path goes to the operation as is, which rejects `..` (`invalid-input`, `/root`). Removing them lexically could change which directory is meant.

A relative `root` is resolved against the working directory even when it comes from an `--input` file elsewhere, not against that file's location. A `root` that is not a string is left alone and fails the operation's schema.

**Output:** Passthrough. `result.root` is the path as recorded in the config, so it shows how `<root>` was resolved.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | (`/root`) `root` begins with `~user/`. |
| `environment` | (`variable`: `HOME`) `root` begins with `~/` and the home directory cannot be determined. |
| `io` | The working directory, needed to resolve a relative `root`, cannot be determined (e.g. it was deleted). `path` is `.`. |

**Examples:**

```sh
koan init ~/tasks
koan init tasks                    # relative to the working directory
koan init /mnt/usb/tasks --replace-config
jq -n '{root: "~/tasks"}' | koan init -i -
```

### doctor

Report everything wrong with the tree, and what `repair` would do about each problem. Changes nothing. Runs [`doctor`](operations.md#doctor).

**Synopsis:** `koan doctor [--kinds <kinds>]`, or `koan doctor -i <file>`.

**Operation:** [`doctor`](operations.md#doctor).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--kinds <kinds>` | `/kinds` | None: every kind but the informational ones, at most 20 items each. Comma list of [finding kinds](operations.md#finding-kinds), each reported in full. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough. A tree with findings is reported as state (`ok: true`, `healthy: false`), so it exits `0`. To turn health into an exit status, use `jq -e .result.healthy`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan doctor                                   # healthy when .result.healthy is true; else .result.findings says why
koan doctor --kinds cycle,duplicate-id        # just these, in full
koan doctor --kinds stray-entry               # files koan ignores: listed only when asked for
koan doctor | jq '.result.findings[] | select(.class != "manual") | .kind'   # what repair would fix
```

### repair

Apply the repairs that are safe, then report what is left. Runs [`repair`](operations.md#repair).

**Synopsis:** `koan repair [--kinds <kinds>]`, or `koan repair -i <file>`.

**Operation:** [`repair`](operations.md#repair).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--kinds <kinds>` | `/kinds` | None: every *auto* kind. Comma list of [finding kinds](operations.md#finding-kinds) to repair. Naming `metadata-missing` is the only way to rebuild `koan.json`. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough: `repaired`, what it changed, and `findings`, what is left. Findings left for a person don't fail the command: it exits `0`, with `healthy: false`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan repair                                   # every auto repair
koan repair --kinds temp-leftover             # only the leftover temp files
koan repair --kinds metadata-missing          # rebuild a lost koan.json; never done by default
```

There is no `doctor --fix`. Repairing is its own command, so that agent permission rules, which match a command line by its start, can ask before `koan repair` whatever options follow.

### create-folder

Create a folder, and optionally any missing parent folders. Runs [`create-folder`](operations.md#create-folder).

**Synopsis:** `koan create-folder <folder> [-p]`, or `koan create-folder -i <file>`.

**Operation:** [`create-folder`](operations.md#create-folder).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<folder>` | `/folder` | Required unless `--input` is given. An exact [folder path](#command-line). |

**Options:**

| Option | Field | Default |
|---|---|---|
| `-p, --parents` | `/parents` | `false`. |

**Input:** none beyond the Arguments and Options mapping.

**Output:** Passthrough. A folder that already exists succeeds with `created` empty, like `mkdir -p`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan create-folder /proj
koan create-folder -p /proj/travel/2026   # .result.created: the folders it made
```

### delete-folder

Permanently remove a folder and everything under it, and the removed tasks' IDs from every `blocked_by` outside it. Runs [`delete-folder`](operations.md#delete-folder).

**Synopsis:** `koan delete-folder <folder> [-r]`, or `koan delete-folder -i <file>`.

**Operation:** [`delete-folder`](operations.md#delete-folder).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<folder>` | `/folder` | Required unless `--input` is given. An exact [folder path](#command-line); never `/`. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `-r, --recursive` | `/recursive` | `false`. Without it, a folder holding tasks or folders is refused, as with `rmdir`. |

**Input:** none beyond the Arguments and Options mapping.

**Output:** Passthrough: the folders removed, the IDs of the tasks removed, and the `dependents` that lost references to them. There is no undo but git (see [Undo](operations.md#undo)).

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan delete-folder /proj/old
koan delete-folder -r /proj/travel        # .result.ids: the tasks removed; .result.dependents: the tasks they no longer block
```

### move-folder

Move a folder, and everything under it, to a new place — which also renames it. Runs [`move-folder`](operations.md#move-folder).

**Synopsis:** `koan move-folder <folder> --to <folder> [-p]`, or `koan move-folder -i <file>`.

**Operation:** [`move-folder`](operations.md#move-folder).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<folder>` | `/folder` | Required unless `--input` is given. The folder to move. An exact [folder path](#command-line); never `/`. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--to <folder>` | `/to` | Required unless `--input` is given. An existing folder to move it into, or its new path. |
| `-p, --parents` | `/parents` | `false`. |

`--to` is an option rather than a second argument for the same reason as [`block`](#block)'s `--blockers`: the direction is explicit.

**Input:** none beyond the Arguments and Options mapping.

**Output:** Passthrough. A folder already at its target exits `0` with `changed: false`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan move-folder /proj/travel --to /archive              # → /archive/travel
koan move-folder /proj/travel --to /archive/travel-2025  # → moved and renamed
koan move-folder /proj/travel --to /archive/2025/trips -p  # → /archive/2025/trips, creating /archive/2025
```

### create

Create a new, open task. Runs [`create`](operations.md#create).

**Synopsis:** `koan create <title> [options]`, or `koan create -i <file>`.

**Operation:** [`create`](operations.md#create).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<title>` | `/title` | Required unless `--input` is given. One argument: quote a title with spaces. Trimmed and validated by the operation. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--folder <path>` | `/folder` | `/`. An exact [folder path](#command-line). |
| `--priority <int\|null>` | `/priority` | `null` (no priority). |
| `--tags <a,b,…>` | `/tags` | `[]`. |
| `--blocked-by <id,id,…>` | `/blocked_by` | `[]`. |
| `--extra <json>` | `/extra` | `{}`. A JSON object. |
| `--notes <text>` | `/notes` | `""`. Mutually exclusive with `--notes-file`. |
| `--notes-file <file>` | `/notes` | —. Reads the notes from `<file>`; `-` is stdin. Mutually exclusive with `--notes`. |

**Input:** `--notes-file` reads the file's contents exactly as they are, including any trailing newline, into `notes`. It accepts any readable path, as [`--input`](#input) does. Because `--input` excludes field options, `--notes-file -` and `--input -` never both read stdin.

- **`--notes`** suits short text.
- **`--notes-file <file>`** suits notes written ahead of time.
- **`--notes-file -`** suits notes produced by another command. It needs no shell escaping and has no argument size limit.

**Output:** Passthrough: the created task.

**Errors:**

| Kind | When |
|---|---|
| `io` | The `--notes-file` file is missing, unreadable, or a directory. |
| `invalid-input` | (`/notes`) The `--notes-file` contents are not valid UTF-8. |

A `create` that exits `3` or with any other [outcome-unknown](#exit-codes) status may have created the task. Per the operation's Retry safety, check before rerunning; a blind retry can create a duplicate.

**Examples:**

```sh
koan create 'Book flights' --folder /proj/travel --tags travel,urgent --priority 2
koan create 'Deploy' --blocked-by 41,42   # the new ID is .result.id
gh issue view 12 --json body -q .body | koan create 'Fix login bug' --notes-file -
koan create 'Wait on quote' --extra '{"status":"waiting"}'
```

### create-batch

Create several tasks in one call, with dependencies between them named by refs. Runs [`create-batch`](operations.md#create-batch).

**Synopsis:** `koan create-batch -i <file>`.

**Operation:** [`create-batch`](operations.md#create-batch).

**Arguments:** none. The tasks are a list of objects, which only `--input` can carry.

**Options:** none beyond the [global options](#global-options). `--input` is required; without it, the command is a [usage error](#usage-errors).

**Input:** none beyond `--input`, taken as-is.

**Output:** Passthrough: `ids`, `refs` and `folders_created`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
jq -n '{folder: "/work/api", tasks: [
  {ref: "schema", title: "Design schema", priority: 2, tags: ["db"]},
  {ref: "migrate", title: "Write migrations", blocked_by: ["schema"]},
  {ref: "backfill", title: "Backfill old rows", blocked_by: ["migrate"]},
  {title: "Deploy", blocked_by: ["migrate", "backfill", 12]}
]}' | koan create-batch -i -                     # creates /work/api if missing; .result.refs.schema is Design schema's ID

koan create-batch -i plan.json | jq -r '.result.ids | join(",")' \
  | xargs koan block 41 --blockers               # task 41 now waits on the whole plan
```

### show

Return one task by ID, with its readiness and where its notes live. Runs [`show`](operations.md#show).

**Synopsis:** `koan show <id>`, or `koan show -i <file>`.

**Operation:** [`show`](operations.md#show).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<id>` | `/id` | Required unless `--input` is given. A [task ID](design-spec.md#task-ids), converted as an integer: bare digits only (`42`, not `#42` or `042`). |

One ID per call. Several IDs are shown by running `show` once per ID (see Examples).

**Options:** none.

**Input:** none beyond the Arguments mapping.

**Output:** Passthrough. `result.tasks` is always an array: one task normally, every copy when the ID is duplicated (with a `duplicate-id` warning).

Notes are not included, as in the operation; `notes_path` locates them. The CLI does not read them itself.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan show 42                                   # readiness, blocking, notes_path: all in .result.tasks[0]
for id in 41 42 43; do koan show "$id"; done   # one envelope each
```

### done

Mark a task done. Marking an already done task changes nothing. Runs [`done`](operations.md#done).

**Synopsis:** `koan done <id>`, or `koan done -i <file>`.

**Operation:** [`done`](operations.md#done).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<id>` | `/id` | Required unless `--input` is given. Bare digits, as for [`show`](#show). One ID per call. |

**Options:** none.

**Input:** none beyond the Arguments mapping.

**Output:** Passthrough: the task, plus `changed`. An already done task exits `0` with `changed: false`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan done 42                               # .result.changed is false if it was already done
for id in 41 42; do koan done "$id"; done  # one envelope each
```

### reopen

Reopen a done task. Reopening an already open task changes nothing. The counterpart of [`done`](#done). Runs [`reopen`](operations.md#reopen).

**Synopsis:** `koan reopen <id>`, or `koan reopen -i <file>`.

**Operation:** [`reopen`](operations.md#reopen).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<id>` | `/id` | Required unless `--input` is given. Bare digits, as for [`show`](#show). One ID per call. |

**Options:** none.

**Input:** none beyond the Arguments mapping.

**Output:** Passthrough: the task, plus `changed`. An already open task exits `0` with `changed: false`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan reopen 42   # .result.completed_at is null again
```

### block

Add one or more blockers to a task's `blocked_by`, all-or-nothing. Runs [`block`](operations.md#block).

**Synopsis:** `koan block <id> --blockers <id,id,…>`, or `koan block -i <file>`.

**Operation:** [`block`](operations.md#block).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<id>` | `/id` | Required unless `--input` is given. The task to block. Bare digits, as for [`show`](#show). One ID per call. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--blockers <id,id,…>` | `/blockers` | Required unless `--input` is given. |

`--blockers` is an option rather than a second argument so that the direction is explicit: in `koan block 42 41` nothing would say which ID blocks which, and a swap would silently add the reverse dependency.

**Input:** none beyond the Arguments and Options mapping.

**Output:** Passthrough: the task, plus `added`. Blockers already present are no-ops; if all were, it exits `0` with `added: []`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan block 42 --blockers 41,43   # .result.added: the ones not already there
koan block 42 --blockers 7       # a cycle is refused: .error.details.cycles shows it
```

### unblock

Remove one or more blockers from a task's `blocked_by`. Removing an ID that isn't there changes nothing. The counterpart of [`block`](#block). Runs [`unblock`](operations.md#unblock).

**Synopsis:** `koan unblock <id> --blockers <id,id,…>`, or `koan unblock -i <file>`.

**Operation:** [`unblock`](operations.md#unblock).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<id>` | `/id` | Required unless `--input` is given. The task to unblock. Bare digits, as for [`show`](#show). One ID per call. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--blockers <id,id,…>` | `/blockers` | Required unless `--input` is given. |

**Input:** none beyond the Arguments and Options mapping.

**Output:** Passthrough: the task, plus `removed`. IDs that aren't there are no-ops; if none were, it exits `0` with `removed: []`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan unblock 42 --blockers 41   # .result.removed: the ones that were there
koan unblock 42 --blockers 99   # clears a dangling reference to a task that no longer exists
```

### update

Change one or more of a task's `title`, `priority`, `tags`, and `extra`. Fields not named are left unchanged. Runs [`update`](operations.md#update).

**Synopsis:** `koan update <id> [options]`, or `koan update -i <file>`.

**Operation:** [`update`](operations.md#update).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<id>` | `/id` | Required unless `--input` is given. The task to update. Bare digits, as for [`show`](#show). One ID per call. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--title <text>` | `/title` | Unchanged. |
| `--priority <int\|null>` | `/priority` | Unchanged. `null` clears the priority. |
| `--tags-add <a,b,…>` | `/tags/add` | Unchanged. |
| `--tags-remove <a,b,…>` | `/tags/remove` | Unchanged. |
| `--tags-replace-all <a,b,…>` | `/tags/replace_all` | Unchanged. `''` clears all tags. |
| `--extra-merge <json>` | `/extra/merge` | Unchanged. A JSON object. |
| `--extra-remove <key>` | `/extra/remove` | Unchanged. **Repeatable**, one key per occurrence. |
| `--extra-replace-all <json>` | `/extra/replace_all` | Unchanged. A JSON object; `{}` clears `extra`. |

At least one option is needed, and the operation's rules on combining them (e.g. `--tags-replace-all` stands alone; a tag may not be both added and removed) are the operation's: breaking them is `invalid-input`, not a usage error (see [Command line](#command-line)).

**Input:** none beyond the Arguments and Options mapping.

Notes are not set by `update`, as in the operation: they are edited directly in the file at the task's `notes_path`. `blocked_by` is changed by [`block`](#block) and [`unblock`](#unblock), and `completed_at` by [`done`](#done) and [`reopen`](#reopen).

**Output:** Passthrough: the task, plus `changed`. An update that changes nothing exits `0` with `changed: []`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan update 42 --priority 3 --tags-add urgent
koan update 42 --extra-merge '{"status":"waiting"}'   # .result.changed names the fields that changed
koan update 42 --priority null --tags-remove urgent --extra-remove status
koan update 42 --tags-replace-all ''
```

### delete

Permanently remove a task, and its ID from every other task's `blocked_by`. Runs [`delete`](operations.md#delete).

**Synopsis:** `koan delete <id>`, or `koan delete -i <file>`.

**Operation:** [`delete`](operations.md#delete).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<id>` | `/id` | Required unless `--input` is given. Bare digits, as for [`show`](#show). One ID per call. |

**Options:** none.

**Input:** none beyond the Arguments mapping.

**Output:** Passthrough: the task's ID and folder, and the `dependents` that lost their reference to it. There is no undo but git (see [Undo](operations.md#undo)).

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan delete 42                                              # .result.dependents: the tasks it no longer blocks
git log --diff-filter=D --oneline -- '*/42.json' '42.json'   # find it again later
```

### move

Move a task into a folder. Runs [`move`](operations.md#move).

**Synopsis:** `koan move <id> --to <folder> [-p]`, or `koan move -i <file>`.

**Operation:** [`move`](operations.md#move).

**Arguments:**

| Argument | Field | Notes |
|---|---|---|
| `<id>` | `/id` | Required unless `--input` is given. Bare digits, as for [`show`](#show). One ID per call. |

**Options:**

| Option | Field | Default |
|---|---|---|
| `--to <folder>` | `/to` | Required unless `--input` is given. |
| `-p, --parents` | `/parents` | `false`. Creates `--to` and any missing folders above it. |

**Input:** none beyond the Arguments and Options mapping.

**Output:** Passthrough: the task, plus `from`, `created`, and `changed`. A task already in `--to` exits `0` with `changed: false`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan move 42 --to /proj/travel
koan move 42 --to /archive/2025 -p   # creates /archive/2025; the notes move too
```

### frontier

Return the ready tasks — open, and not blocked — in the order to work on them. Runs [`frontier`](operations.md#frontier).

**Synopsis:** `koan frontier [--folder <path>] [--recursive=false] [--tags-any <tags>] [--tags-all <tags>] [--limit <n>] [--fields <names>]`, or `koan frontier -i <file>`.

**Operation:** [`frontier`](operations.md#frontier).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--folder <path>` | `/folder` | `/`. An exact [folder path](#command-line). |
| `--recursive` | `/recursive` | `true`. `--recursive=false` leaves out tasks in subfolders. |
| `--tags-any <tags>` | `/tags_any` | None. Comma list: only tasks with at least one of them. |
| `--tags-all <tags>` | `/tags_all` | None. Comma list: only tasks with every one of them. |
| `--limit <n>` | `/limit` | None: every task. At most `n`, the first in frontier order. |
| `--fields <names>` | `/fields` | None: whole task views. Comma list of task view field names; `id` is always included. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough. `result.tasks` is in frontier order and may be empty; an empty frontier exits `0`. `result.total` and `result.truncated` say whether `--limit` cut it (see [Narrowing tasks](operations.md#narrowing-tasks)).

Filtering by `extra` or title is left to `jq` (see [Not included](#not-included)).

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan frontier --limit 10 --fields id,title,priority,folder   # the next ten, briefly
koan frontier --limit 1                                     # the next task, whole
koan frontier --folder /proj --limit 20 --fields id,title   # ready under /proj
koan frontier --tags-any urgent,today --fields id,title
koan frontier --limit 0                                     # how many are ready: .result.total
```

### list

Return every task in scope, whatever its readiness, with its readiness shown — and optionally the folders in scope. Done tasks are included only on request. Runs [`list`](operations.md#list).

**Synopsis:** `koan list [--folder <path>] [--recursive=false] [--readiness <states>] [--include-folders] [--tags-any <tags>] [--tags-all <tags>] [--limit <n>] [--fields <names>]`, or `koan list -i <file>`.

**Operation:** [`list`](operations.md#list).

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--folder <path>` | `/folder` | `/`. An exact [folder path](#command-line). |
| `--recursive` | `/recursive` | `true`. `--recursive=false` leaves out tasks in subfolders, and folders below the immediate subfolders. |
| `--readiness <states>` | `/readiness` | `ready,blocked`. Comma list of `ready`, `blocked`, `done`: only tasks in one of these states. |
| `--include-folders` | `/include_folders` | `false`. |
| `--tags-any <tags>` | `/tags_any` | None. Comma list: only tasks with at least one of them. |
| `--tags-all <tags>` | `/tags_all` | None. Comma list: only tasks with every one of them. |
| `--limit <n>` | `/limit` | None: every task. At most `n`, the first in tree order. |
| `--fields <names>` | `/fields` | None: whole task views. Comma list of task view field names; `id` is always included. |

**Input:** none beyond the Options mapping.

**Output:** Passthrough. `result.tasks` is in tree order and may be empty. `result.total` and `result.truncated` say whether `--limit` cut it (see [Narrowing tasks](operations.md#narrowing-tasks)). `result.folders` is present only with `--include-folders`, and is never narrowed.

Filtering by `extra` or title, and grouping, are left to `jq`; rendering the result as a tree is [Future work](design-spec.md#tree-view) outside the CLI (see [Not included](#not-included)).

**Errors:** none beyond the operation's.

**Examples:**

```sh
koan list --limit 50 --fields id,title,readiness,folder
koan list --folder /proj --readiness done --limit 0                         # how many are done: .result.total
koan list --readiness blocked --fields id,title,blocking                    # what's stuck, and on what
koan list --readiness ready,blocked,done --tags-all db,backend --fields id,title,readiness
koan list --include-folders --limit 0                                      # every folder: .result.folders
koan list --fields folder | jq -c 'if .ok then .result |= (.tasks |= (group_by(.folder) | map({folder: .[0].folder, ids: map(.id)}))) else . end'
```

### pick

Fuzzy-pick tasks, or folders, in an interactive [fzf](https://github.com/junegunn/fzf) picker, act on them in place, and write the ones chosen as one envelope. For people at a terminal, not agents. It is specified in its own document, [pick-spec.md](pick-spec.md), which says where it departs from this spec's global rules.

**Synopsis:** `koan pick [--folder <path>] [--recursive=false] [--scope <scope>] [--tags-any <tags>] [--tags-all <tags>] [--ids <ids> | --from <file> | --source <command>] [--query <text>] [--select-one] [--exit-zero] [--fields <names>]`, `koan pick --folders [--folder <path>] [--recursive=false] [--query <text>] [--select-one] [--exit-zero]`, or `koan pick -i <file>`.

**Operation:** none of its own: [`list`](operations.md#list) for each load and for `u`'s list, [`show`](operations.md#show) for `e` and `x`, and one write operation per target for each action. See [pick-spec.md, Command](pick-spec.md#command).

**Arguments:** none.

**Options:** see [pick-spec.md, Command](pick-spec.md#command).

**Input:** pick's own input schema, `pick-input`; `--from` resolves to `ids`. See [pick-spec.md, Command](pick-spec.md#command).

**Output:** pick's own, `pick-output`: the selection, read fresh, and every action the session ran. See [pick-spec.md, Output](pick-spec.md#output).

**Errors:** three CLI-only kinds, `unavailable`, `cancelled` and `incomplete`. See [pick-spec.md, Errors](pick-spec.md#errors).

**Composition:** each action is its own call with its own lock; a failure leaves the others in effect. See [pick-spec.md, Command](pick-spec.md#command).

**Examples:**

```sh
koan pick | jq -r '.result.tasks[].id'                            # the IDs picked
koan list --readiness blocked --fields id | koan pick --from -   # choose among the blocked ones
koan pick --folders | jq -r '.result.folders[0]'                  # a folder path
```

## Not included

- **Filters beyond [Narrowing tasks](operations.md#narrowing-tasks)** (on `extra` fields, on title text, or anything query-like): output shaping, left to `jq` (see the operations' [Parameters](operations.md#conventions) convention). `--limit`, `--fields`, and the tag and readiness filters are the exception, since `frontier`'s and `list`'s output lands in an agent's context.
- **`--config` / `--root`**: there is one config and one root per user (see the design spec's [Assumptions](design-spec.md#assumptions)). A different config location is reached by setting `XDG_CONFIG_HOME` (or `HOME`), with the effects the design spec describes.
- **Tree view**: rendering for people, which the JSON-only CLI does not do. Stays in the design spec's [Future work](design-spec.md#tree-view).
