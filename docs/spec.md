# koan: command specification

Oct 20, 2026 · @SuperUser

## Introduction

This doc fixes exactly what every koan command takes and prints: each flag, each JSON field, each error, and the files koan writes for people and other tools to read. The Output tests check every command against the schemas here.

It comes third. The rules doc, koan: invariants and rules, says what must hold; the PRD, koan rewrite: PRD, says how koan is built and what shape its protocol takes. Where this doc disagrees with either, they win and this doc is fixed. A change of behaviour starts in one of them, never here.

**Reading the schemas.** They are JSON Schema 2020-12. Each has an `$id` that is its name, such as `task`, and refers to the others by it: `types#/$defs/id` is the `id` type in Shared types. These conventions hold unless a schema says otherwise:

- **Records, results and inputs are closed.** Each has `unevaluatedProperties: false`, written out once here rather than in every schema. It sees through `allOf` and `$ref`, so a schema built from parts is closed as a whole, and a fragment under `$defs`, such as `task-list`'s `filter`, is closed only where it is used. Three things stay open: `extra`, which is any object; `envelope.result`, which the command's own result schema checks; and the on-disk formats, whose readers ignore fields they don't know.
- **Every listed field is always present.** A field that has no value is `null`, never left out. The one exception is a record cut down by `--fields`.
- **Fields print in the order the schema lists them**, so output can be diffed. An `extra`, whose keys no schema lists, prints them in byte order at every depth, whatever order they were given in.
- **ID lists are ascending**, unless the schema names another order, such as work order or cycle order.
- **Paths on disk are absolute,** and under `~/koan-root` as written: through the symlink when the root is one, never the real path behind it. A check of whether a path lies under the root, as backup and marker bind make, compares real paths, so no second route gets past it; how a real path is spelled is the PRD's, under Markers. Outside the root, paths are as given, made absolute; only marker.json's `dir` is a real path. Folder paths are from the root, such as `/work/api`.
- **Text is UTF-8.** Names, tags and IDs are ASCII by their patterns. Text given on the command line or read by `--notes-file` that isn't UTF-8 is `invalid-input`, pointing at its field. An `--input` that isn't UTF-8 isn't JSON, which must be, and is refused as any other that isn't JSON.

## Shared types

Every schema below builds on these. The patterns are the rules doc's, written as regular expressions; a rule a pattern can't say, such as a real date or a trimmed title, is checked by koan and reported as `invalid-input`.

| Type | Holds | Example |
| --- | --- | --- |
| `id` | An ID, task or folder, which share one counter | `57` |
| `name` | A folder name | `HOME-12345` |
| `tag` | A tag: the name pattern, lowercase only | `urgent` |
| `path` | A folder path from the root, exact case | `/work/api` |
| `title` | A title, as stored: trimmed, 1–200 code points, no control characters | `Book flights` |
| `priority` | An integer, or null | `2` |
| `timestamp` | UTC to the second | `2026-10-19T14:03:00Z` |
| `extra` | Any JSON object, never interpreted | `{"status":"waiting"}` |
| `end` | A gate's end | `start` |
| `label` | A batch label: $ then a name | `$a` |
| `folder-ref` | A folder: path, ID or anchored path | `/work/api`, `@41`, `@41/phase2` |
| `task-ref` | A task: ID, or a gate by its folder and end | `@57`, `@41.done`, `/work/api.start` |
| `edge-ref` | What `--blocked-by` and `--blocks` take: a task, or a folder with an optional end, the done gate when none is given | `@42`, `/work/api`, `@9.start` |
| `ref` | Anything `where` takes: the forms above, or material in a folder | `@41:docs/plan.md` |
| `kept` | The kept-text pair koan writes | `@57 (/work/api.done)` |
| `folder-at` | A folder named both ways, by ID and path; the root has a null ID | `{"id": 41, "path": "/work/api"}` |
| `edge-end` | The other end of an edge, as a record lists it | `{"id": 42, "ref": "@42 (/work)", "state": "open"}` |

A reference is a string, or an ID given as a JSON integer. Every form takes the `@` or leaves it out. Whether a reference names the right kind of thing, such as a task where a task is needed, is resolved by koan, not the pattern.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "types",
  "$defs": {
    "id":        { "type": "integer", "minimum": 1, "maximum": 999999999999999 },
    "name":      { "type": "string", "pattern": "^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$" },
    "tag":       { "type": "string", "pattern": "^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$" },
    "path":      { "type": "string", "pattern": "^(?:/|(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)+)$" },
    "title":     { "type": "string", "minLength": 1, "maxLength": 200,
                   "pattern": "^[^\\x00-\\x1f\\x7f-\\x9f\\u2028\\u2029]+$",
                   "description": "As stored. Input is trimmed of Unicode White_Space first, then checked. Lengths count code points." },
    "priority":  { "type": ["integer", "null"], "minimum": -9007199254740991, "maximum": 9007199254740991 },
    "timestamp": { "type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$",
                   "description": "A real date and time, which koan checks." },
    "extra":     { "type": "object" },
    "end":       { "enum": ["start", "done"] },
    "gate":      { "enum": ["start", "done", null] },
    "state":     { "enum": ["open", "complete"] },
    "readiness": { "enum": ["ready", "blocked", null], "description": "null once complete." },
    "label":     { "type": "string", "pattern": "^\\$[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$", "description": "Compared byte for byte." },

    "folder-ref": { "oneOf": [ { "$ref": "#/$defs/id" },
                               { "type": "string",
                                 "pattern": "^(?:/|(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)+|@?[1-9][0-9]{0,14}(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*)$" } ] },
    "task-ref":   { "oneOf": [ { "$ref": "#/$defs/id" },
                               { "type": "string",
                                 "pattern": "^(?:@?[1-9][0-9]{0,14}|(?:@?[1-9][0-9]{0,14}(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*|(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)+)\\.(?:start|done))$" } ] },
    "edge-ref":   { "oneOf": [ { "$ref": "#/$defs/id" },
                               { "type": "string",
                                 "pattern": "^(?:@?[1-9][0-9]{0,14}(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*|(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)+)(?:\\.(?:start|done))?$" } ] },
    "material":   { "type": "string",
                    "pattern": "^(?:[^\\x00/.@][^\\x00/]*|\\.[^\\x00/.][^\\x00/]*|\\.\\.[^\\x00/]+)(?:/(?:[^\\x00/.][^\\x00/]*|\\.[^\\x00/.][^\\x00/]*|\\.\\.[^\\x00/]+))*$",
                    "description": "A path inside a folder's directory: no empty, . or .. part, and no @ at the start of the first." },
    "ref":        { "anyOf": [ { "$ref": "#/$defs/folder-ref" }, { "$ref": "#/$defs/task-ref" },
                               { "type": "string", "description": "A folder-ref, then :, then a material path.",
                                 "pattern": "^(?:/|(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)+|@?[1-9][0-9]{0,14}(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*):(?:[^\\x00/.@][^\\x00/]*|\\.[^\\x00/.][^\\x00/]*|\\.\\.[^\\x00/]+)(?:/(?:[^\\x00/.][^\\x00/]*|\\.[^\\x00/.][^\\x00/]*|\\.\\.[^\\x00/]+))*$" } ] },

    "kept":      { "type": "string", "description": "@<id>, or @<id>:<material>, then a space and the hint in parentheses." },
    "abs-path":  { "type": "string", "pattern": "^/" },
    "ids":       { "type": "array", "items": { "$ref": "#/$defs/id" }, "uniqueItems": true },
    "folder-at": { "type": "object", "required": ["id", "path"],
                   "properties": { "id": { "anyOf": [ { "$ref": "#/$defs/id" }, { "type": "null" } ] },
                                   "path": { "$ref": "#/$defs/path" } } },
    "edge-end":  { "type": "object", "required": ["id", "ref", "state"],
                   "properties": { "id": { "$ref": "#/$defs/id" }, "ref": { "$ref": "#/$defs/kept" },
                                   "state": { "$ref": "#/$defs/state" } } },
    "gate-made": { "type": "object", "required": ["id", "folder", "end"],
                   "properties": { "id": { "$ref": "#/$defs/id" }, "folder": { "$ref": "#/$defs/folder-at" },
                                   "end": { "$ref": "#/$defs/end" } } }
  }
}
```

## Invocation

A command line is `koan <command> [<subject>] [<options>]`, and every command reads and writes the root at `~/koan-root`. There is no setting for the root; PRD Layout says how to keep it elsewhere. Tests point `HOME` at a scratch directory.

**Two ways to give the input, never both.** Every command has an input schema (under Commands).

- **Arguments and options.** Each option sets the input field of the same name, in kebab-case: `--blocked-by` sets `blocked_by`.
- **`-i, --input <file>`.** The whole input as one JSON object, from `<file>`, or from standard input when `<file>` is `-`. It is the only way to run `batch`: without it, `batch`'s input is `{}`, which holds no parts, and is `invalid-input`. koan reads standard input only when `-` names it, so it is safe inside a loop that feeds standard input to something else.

**The command line** is read by cobra (`github.com/spf13/cobra`), with pflag for options (PRD Command surface). These rules are what koan asks of it; where they say nothing, cobra's behaviour is the rule.

- **One subject, positional.** It is the task or folder the command acts on, or a new task's title or a new folder's path. Everything else is an option, so a bare word has one meaning.
- **References** are taken with or without the `@`: `koan task show 42`, `koan task block @42 --blocked-by 17,@41.start`.
- **Lists** are comma-separated, and the option can repeat: `--tags a,b` is `--tags a --tags b`. Every comma splits; there is no quoting. `--extra-remove` takes one key per occurrence, since a key can hold a comma.
- **`null`** is written `null`: `--priority null` clears a priority, and a boolean writes it `--claimed=null`. It means null only where the field can be null, such as a priority or a folder's title; anywhere else it is the word itself, so a task can be titled null.
- **Booleans:** `--contents` is true, `--contents=false` false, and `--claimed=null` null, where the field can be null.
- **Integers** (`--priority`, `--limit`, `--ids-from`) are written as JSON writes them, or `null`: `--priority 007` is not one.
- **JSON values** (`--extra`, `--extra-merge`, `--extra-replace-all`) are one JSON object in one argument.
- **Names are matched exactly.** There are no abbreviations, so adding an option never changes what an existing command line means.
- **`--`** ends the options, for a title that starts with `-`.
- **Values** follow their option as the next argument, taken as typed even when it starts with `-`, or after `=`: `--title -x` and `--title=-x` are the same. `-i f`, `-i=f` and `-if` are the same too. A boolean's value goes only after `=`, since a bare boolean is true: `--claimed null` is `--claimed` and a subject `null`.
- **Options go after the command's words.** cobra also takes some placed before them, such as `koan --priority 1 task create t`, but never a boolean, which takes the next word as its value (`koan --recursive task list` is `usage`); that is not a contract.
- **Once each.** An option that isn't a list can be given once.

**`usage` versus `invalid-input`.** `usage` is only a command line that can't be turned into an input: an unknown command or option, an argument that isn't an option's syntax, `koan task`, `koan folder` or `koan marker` with no command or an unknown one after it, `koan completion` with a shell it doesn't know, a missing or extra subject, an option without its value, a value its option's type refuses, an option that isn't a list given twice, `--input` with anything else, or both `--notes` and `--notes-file`. A value its option's type refuses is an integer option's that isn't an integer or a null it can take, or a boolean option's that isn't `true`, `false` or a null it can take. Its problem names what is at fault:

- an option by its long name: `--frob` for `--frob=1`, `--input` for a second `-i`, `--title` for a second `--title`;
- an unknown short option as `-` and its letter: `-x` for `-x`, and for `-xy` too, since pflag reads the letters one at a time;
- an argument pflag can't read as an option, such as `---x` or `--=x`, as typed;
- an unknown command, or an extra subject, as typed;
- for `--input` with anything else, the subject when one is given, else the first other option given, in byte order of their long names;
- `""` when what is at fault is missing, such as the command, `koan task` alone included, or the subject; for both `--notes` and `--notes-file`; and for any line `completion`'s shell commands refuse, such as `koan completion bash extra`.

When a line has several faults, which is named is cobra's: options are read before the command's words and subject are checked. Every check of a value other than its type's is the command's, and is `invalid-input` exactly as it would be through `--input`. So is an `--input` that isn't one JSON object, with its problem at `""`.

**Conveniences**, which need a shell's files or working directory. Through `--input`, give what they stand for.

| Convenience | On | Stands for |
| --- | --- | --- |
| `--notes-file <file>` | `task create`, `folder create` | `notes`, read from the file whole, or from standard input for `-` |
| `--file <path>`, relative | `where`, `context`, `export` | `file`, made absolute from the working directory |
| no subject | `context`, `export` | `file`, the working directory |
| `--here` | `pick` | `file`, the working directory |
| `--dir <path>`, relative or left out | `marker bind`, `marker unbind` | `dir`, made absolute; the working directory when left out |
| `<dir>`, relative | `backup` | `dir`, made absolute from the working directory |

A file koan can't read, or a working directory it can't resolve, is `io`, with the file's absolute path; standard input that can't be read is `io` with no path. koan reads a convenience's file as it turns the command line into an input, as it reads `--input`'s, so that `io` comes before any fault in the input's shape.

**The home directory.** `~` is `HOME`. A command that uses the root, which is every command but `version`, finds it as part of checking the root's state, after the input's shape: `HOME` unset, empty or not an absolute path is `io`, with no path. `version` never reads it.

**Output.** Every run prints exactly one envelope, on one line, to standard output, whatever happens, except help, completion and `pick` below. Standard error gets at most one line, except from completion's `__complete` commands, for a person watching: `koan: <kind>: <message>` on failure, or `koan: <n> warnings` on a success that has warnings, `koan: 1 warning` for one. Callers read the envelope, never standard error.

**Help and completion** are for people, and print plain text, not an envelope. Their text is cobra's, and is not a contract (Not covered).

- **Help.** `--help` or `-h`, anywhere on a command line before `--` but as an option's value (`--title -h` is a title), prints the help of the last command or group its words name, `koan` itself when the first word names none, to standard output with exit 0, whatever else is on the line, except an option pflag refuses: pflag reads every option before cobra looks at help, so `koan info --frob -h` is `usage`. `koan help [<command>]` is the same. So `koan frob -h` and `koan help frob` print `koan --help`'s text, and `koan task frob -h` prints `koan task --help`'s. `koan --help` lists the commands.
- **Completion.** `koan completion <shell>`, for `bash`, `zsh`, `fish` or `powershell`, prints a script that completes koan's commands and options in that shell; with no shell it prints its help, and with a shell it doesn't know it is `usage`, naming that shell, so a typo never installs a broken script. The script calls koan back through cobra's hidden `__complete` commands, which print candidates on standard output and may write more than one line to standard error. It completes the references an argument takes too, as [Completing references](#completing-references) says.
- **`pick`** prints the references a person picks, as plain text, and on failure nothing on standard output, only the standard-error line. It needs a terminal, draws on `/dev/tty`, and fails at once without one. Its outcomes and exits are under [pick](#pick).

| Exit | Means |
| --- | --- |
| 0 | Success: `ok` is true, with or without warnings. Help and completion exit 0 too, and `pick` when something was picked |
| 1 | Failure: `error.kind` says how. For `pick`, also nothing matched, with nothing printed |
| 2 | The command line couldn't be read: `error.kind` is `usage` |
| 3 | koan couldn't write the envelope. The outcome is unknown: look before running it again |
| 130 | `pick` only: the person cancelled in fzf |
| other | koan was killed. The outcome is unknown, as for 3 |

Callers branch on `error.kind`, not on the exit code.

## Envelope

Every command prints one envelope: `ok`, then either `result` or `error`, then `warnings`. A success carries the command's result, whose schema is under its command; a failure carries one error. Warnings can come with either, and never stop a command.

```json
{"ok": true,  "result": {"id": 57, "changed": true, "released": [58]}, "warnings": []}
{"ok": false, "error": {"kind": "conflict", "rule": "cycle", "message": "@8 would wait on itself through @10 and @57", "ids": [8, 10, 57], "paths": [], "problems": [{"field": "/blocked_by/0", "reason": "@8 would wait on itself through @10 and @57"}], "archived": []}, "warnings": []}
```

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "envelope",
  "oneOf": [
    { "type": "object", "required": ["ok", "result", "warnings"],
      "properties": { "ok": { "const": true },
                      "result": { "type": "object", "description": "The command's result schema." },
                      "warnings": { "type": "array", "items": { "$ref": "warning" } } } },
    { "type": "object", "required": ["ok", "error", "warnings"],
      "properties": { "ok": { "const": false },
                      "error": { "$ref": "error" },
                      "warnings": { "type": "array", "items": { "$ref": "warning" } } } }
  ]
}
```

Warnings are in the order koan met them, with no kind twice: a warning that applies to several tasks lists them all in one entry.

## Records

Reads return records; writes return identities. A record is exact only as of the call, and every record carries `ref`, its kept-text pair.

### Task

A task as every read returns it. `state` and `readiness` are derived: `state` from `completed_at`, `readiness` from the edges as they stand. A gate is an ordinary task whose `gate` says which end of its folder it is.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task",
  "$ref": "#/$defs/partial",
  "required": ["id", "ref", "gate", "title", "priority", "folder", "created_at", "completed_at", "updated_at",
               "state", "readiness", "blocked_by", "blocks", "tags", "extra", "claim", "notes_path"],
  "$defs": {
    "partial": {
      "type": "object", "required": ["id"],
      "description": "A task cut down by --fields: id, then the fields asked for, in this order.",
      "properties": {
        "id":           { "$ref": "types#/$defs/id" },
        "ref":          { "$ref": "types#/$defs/kept" },
        "gate":         { "$ref": "types#/$defs/gate" },
        "title":        { "$ref": "types#/$defs/title" },
        "priority":     { "$ref": "types#/$defs/priority" },
        "folder":       { "$ref": "types#/$defs/folder-at", "description": "Where it sits; the root has a null ID." },
        "created_at":   { "$ref": "types#/$defs/timestamp" },
        "completed_at": { "anyOf": [ { "$ref": "types#/$defs/timestamp" }, { "type": "null" } ] },
        "updated_at":   { "$ref": "types#/$defs/timestamp" },
        "state":        { "$ref": "types#/$defs/state" },
        "readiness":    { "$ref": "types#/$defs/readiness" },
        "blocked_by":   { "type": "array", "items": { "$ref": "types#/$defs/edge-end" }, "description": "What it waits on. Ascending by ID." },
        "blocks":       { "type": "array", "items": { "$ref": "types#/$defs/edge-end" }, "description": "What waits on it. Ascending by ID." },
        "tags":         { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true, "description": "Sorted byte for byte." },
        "extra":        { "$ref": "types#/$defs/extra" },
        "claim":        { "anyOf": [ { "$ref": "claim" }, { "type": "null" } ] },
        "notes_path":   { "$ref": "types#/$defs/abs-path", "description": "Its @<id>.md, or its folder's @start.md or @done.md." }
      }
    },
    "field": { "enum": ["id", "ref", "gate", "title", "priority", "folder", "created_at", "completed_at", "updated_at",
                        "state", "readiness", "blocked_by", "blocks", "tags", "extra", "claim", "notes_path"] }
  }
}
```

### Folder

A folder as every read returns it. The root can be returned too, wherever a read lands on it, with a null `id`, `name` and `notes_path`, an empty `extra`, and no gates. `open` and `complete` count every task under the folder, at any depth, gates included; `finished` is true when `open` is 0.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "folder",
  "$ref": "#/$defs/partial",
  "required": ["id", "ref", "name", "path", "title", "extra", "gates", "open", "complete", "finished", "notes_path", "dir"],
  "$defs": {
    "partial": {
      "type": "object", "required": ["id"],
      "description": "A folder cut down by --fields: id, then the fields asked for, in this order.",
      "properties": {
        "id":         { "anyOf": [ { "$ref": "types#/$defs/id" }, { "type": "null" } ] },
        "ref":        { "type": "string", "description": "The kept-text pair, such as @41 (/work/api); / for the root." },
        "name":       { "anyOf": [ { "$ref": "types#/$defs/name" }, { "type": "null" } ] },
        "path":       { "$ref": "types#/$defs/path" },
        "title":      { "anyOf": [ { "$ref": "types#/$defs/title" }, { "type": "null" } ] },
        "extra":      { "$ref": "types#/$defs/extra" },
        "gates":      { "type": "object", "required": ["start", "done"],
                        "properties": { "start": { "$ref": "#/$defs/gate-entry" }, "done": { "$ref": "#/$defs/gate-entry" } } },
        "open":       { "type": "integer", "minimum": 0 },
        "complete":   { "type": "integer", "minimum": 0 },
        "finished":   { "type": "boolean" },
        "notes_path": { "anyOf": [ { "$ref": "types#/$defs/abs-path" }, { "type": "null" } ], "description": "Its @notes.md." },
        "dir":        { "$ref": "types#/$defs/abs-path", "description": "Its directory in the tree." }
      }
    },
    "gate-entry": {
      "anyOf": [ { "type": "null", "description": "The folder has no gate at that end." },
                 { "type": "object", "required": ["id", "state", "readiness"],
                   "properties": { "id": { "$ref": "types#/$defs/id" }, "state": { "$ref": "types#/$defs/state" },
                                   "readiness": { "$ref": "types#/$defs/readiness" } } } ]
    },
    "field": { "enum": ["id", "ref", "name", "path", "title", "extra", "gates", "open", "complete", "finished", "notes_path", "dir"] }
  }
}
```

### Claim

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "claim",
  "type": "object",
  "required": ["task", "claimed_at", "extra"],
  "properties": {
    "task":       { "$ref": "types#/$defs/id" },
    "claimed_at": { "$ref": "types#/$defs/timestamp" },
    "extra":      { "$ref": "types#/$defs/extra", "description": "The caller's, never interpreted: who holds it, for instance." }
  }
}
```

### Brief

What `context` returns: enough for an agent to start work in a folder.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "brief",
  "type": "object",
  "required": ["folder", "ancestors", "notes", "tasks", "marker"],
  "properties": {
    "folder":    { "$ref": "folder" },
    "ancestors": { "type": "array", "items": { "$ref": "folder" }, "description": "The folders above it, from the top, the root left out." },
    "notes":     { "type": ["string", "null"], "description": "Its @notes.md, whole; empty when the file is missing; null for the root." },
    "tasks":     { "type": "array", "items": { "$ref": "task" }, "description": "Its own open tasks, gates included, in tree order." },
    "marker":    { "anyOf": [ { "$ref": "types#/$defs/abs-path" }, { "type": "null" } ],
                   "description": "The marker.json the folder was found by; null when found by path or reference." }
  }
}
```

### Location

What `where` returns: what a reference or a path on disk names, both ways.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "location",
  "type": "object",
  "required": ["ref", "kind", "id", "folder", "gate", "material", "file", "marker"],
  "properties": {
    "ref":      { "type": "string", "description": "The kept-text pair, such as @41:docs/plan.md (/work/api:docs/plan.md); / for the root; /:<material> for material in the root." },
    "kind":     { "enum": ["task", "folder", "root", "material"] },
    "id":       { "anyOf": [ { "$ref": "types#/$defs/id" }, { "type": "null" } ], "description": "The task's or folder's ID; for material, its folder's; null for the root." },
    "folder":   { "$ref": "types#/$defs/folder-at", "description": "The folder it is, or is in." },
    "gate":     { "$ref": "types#/$defs/gate" },
    "material": { "anyOf": [ { "$ref": "types#/$defs/material" }, { "type": "null" } ] },
    "file":     { "anyOf": [ { "$ref": "types#/$defs/abs-path" }, { "type": "null" } ],
                  "description": "On disk: the material, a task's notes, or a folder's directory; for a path given, that path. Never checked to exist." },
    "marker":   { "anyOf": [ { "$ref": "types#/$defs/abs-path" }, { "type": "null" } ] }
  }
}
```

### Finding

What `doctor` reports and `repair` acts on. The kinds are the PRD's.

| Kind | `detail` | `ids` | `paths` | `repairable` |
| --- | --- | --- | --- | --- |
| `pending-step` | null |  | the paths the steps touch | yes |
| `stuck-step` | `both`, `neither` or `material` |  | its source, then its destination when it has one | no |
| `stray-directory` | null | the folder it would move back to, when one matches | the directory | when one matches |
| `missing-directory` | null | the folder | its path | yes |
| `case-differs` | null | the folder | the directory as spelled | yes |
| `misplaced-notes` | null | the task | where they are, where they belong | yes, unless notes are already there |
| `orphan-notes` | `deleted`, `archived`, `never-issued` or `no-gate` | the ID in the name, if any | the file | when empty |
| `stray-entry` | null |  | the entry | no |
| `invalid-row` | the invariant broken: `I3`, `I4`, `I6`, `I7`, `I9`, `I11` or `I12` | the rows |  | no |
| `corrupt` | `integrity` or `foreign-key` |  | the database | no |

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "finding",
  "type": "object",
  "required": ["kind", "detail", "message", "ids", "paths", "repairable"],
  "properties": {
    "kind":       { "enum": ["pending-step", "stuck-step", "stray-directory", "missing-directory", "case-differs",
                             "misplaced-notes", "orphan-notes", "stray-entry", "invalid-row", "corrupt"] },
    "detail":     { "type": ["string", "null"] },
    "message":    { "type": "string", "description": "What is wrong and what to do about it, for a person." },
    "ids":        { "$ref": "types#/$defs/ids" },
    "paths":      { "type": "array", "items": { "$ref": "types#/$defs/abs-path" } },
    "repairable": { "type": "boolean" }
  }
}
```

## Warnings

Every warning has one shape: a kind, a message for a person, and the IDs and paths it concerns. What `ids` and `paths` hold depends on the kind.

| Kind | When | Given by | `ids` | `paths` |
| --- | --- | --- | --- | --- |
| `pending-filesystem` | A path returned belongs to a change whose filesystem steps are still running | any read | the tasks and folders whose paths are pending | the paths returned, where they are going |
| `pending-step` | Steps are pending with no write carrying them out: this write failed one, an earlier write crashed, or a step waits behind an earlier change's step on the same path | every command but `init`, `info`, `version`, `doctor` and `backup` |  | the paths the steps touch |
| `stuck-step` | A step is set aside as stuck | every command but `init`, `info`, `version`, `doctor` and `backup`, until repair clears it |  | each stuck step's source, then its destination when it has one, in seq order: the steps that found both, neither or material, not the rest of their changes |
| `missing-notes` | No file at a notes path; it reads as empty notes. Not given while a pending or stuck step is still to make the path, making it or a directory above it, since that step's own warning says why | `task show`, `folder show`, `context` | the task or folder | its notes path |
| `complete-blocker` | An edge was added to the `blocked_by` of a complete task, which holds nothing back | `task create`, `task block`, `batch` | the complete tasks |  |
| `claimed-blocked` | The change made a claimed task wait again: it was ready before the change, and is blocked after it, as for `reblocked` | `task create`, `task block`, `task reopen`, `batch` | the claimed tasks |  |
| `moved-claimed` | A move changed the notes path of a claimed task | `task move`, `folder move` | the claimed tasks | their new notes paths, in the same order |
| `stale-marker` | A marker's path hint no longer matches its folder | `where`, `context`, `export` | the folder | the marker.json |

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "warning",
  "type": "object",
  "required": ["kind", "message", "ids", "paths"],
  "properties": {
    "kind":    { "enum": ["pending-filesystem", "pending-step", "stuck-step", "missing-notes",
                          "complete-blocker", "claimed-blocked", "moved-claimed", "stale-marker"] },
    "message": { "type": "string" },
    "ids":     { "$ref": "types#/$defs/ids" },
    "paths":   { "type": "array", "items": { "$ref": "types#/$defs/abs-path" } }
  }
}
```

## Errors

A failure carries exactly one error. koan checks in this order and reports the first that fails: the input's shape, then the root's state, then the lock, then the references, then the change itself. A stuck filesystem step never fails a write: once COMMIT succeeds the write returns `ok`, with a warning.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "error",
  "type": "object",
  "required": ["kind", "rule", "message", "ids", "paths", "problems", "archived"],
  "properties": {
    "kind":     { "enum": ["usage", "invalid-input", "not-found", "conflict", "busy", "not-initialized",
                           "corrupt", "unsupported-format", "io", "internal"] },
    "rule":     { "enum": ["case-mismatch", "cycle", "claimed", "not-finished", "not-empty", "has-material", "gate",
                           "into-itself", "exists", "not-a-root", "checkout", "foreign-files", "complete", "bound", "no-ids", null],
                  "description": "The conflict rule; null for every other kind." },
    "message":  { "type": "string", "description": "One or two sentences for a person. Never parsed." },
    "ids":      { "type": "array", "items": { "$ref": "types#/$defs/id" }, "description": "In the order the kind or rule gives." },
    "paths":    { "type": "array", "items": { "type": "string" }, "description": "Folder paths or paths on disk, as the kind or rule gives." },
    "problems": { "type": "array",
                  "items": { "type": "object", "required": ["field", "reason"],
                              "properties": { "field":  { "type": "string", "description": "A JSON Pointer into the input; for usage, what is at fault, as Invocation says: an option by its long name, an unknown short option as `-` and its letter, an unknown command or extra subject as typed, or \"\" when what is at fault is missing." },
                                              "reason": { "type": "string", "description": "For a person, never parsed: the fault, for usage and invalid-input; for any other kind, which points at fields without faulting them, the message." } } } },
    "archived": { "type": "array",
                  "items": { "type": "object", "required": ["id", "archive"],
                              "properties": { "id": { "$ref": "types#/$defs/id" },
                                              "archive": { "$ref": "types#/$defs/abs-path" } } },
                  "description": "For not-found: where each archived ID the input named went, from the index." }
  },
  "allOf": [ { "if": { "properties": { "kind": { "const": "conflict" } } },
               "then": { "properties": { "rule": { "type": "string" } } },
               "else": { "properties": { "rule": { "const": null } } } } ]
}
```

### Kinds

| Kind | Exit | Means | Carries |
| --- | --- | --- | --- |
| `usage` | 2 | The command line couldn't be turned into an input | `problems`: the argument at fault |
| `invalid-input` | 1 | The input breaks a rule or its schema: a bad name, title or value, an end where none is taken, a label misused, two paths that differ only in case | `problems`: every fault found, by JSON Pointer. A fault in how fields go together, such as `tags_replace_all` with `tags_add`, nothing to change, or no edge given, points at the object holding them: `""`, or the batch part. A duplicate in a list points at its later occurrence, and so does a tag both added and removed or a key both merged and removed: its entry in `tags_remove` or `extra_remove`. Two paths that differ only in case, written from the same start, point at both references, in input order; written from different starts, one is `case-mismatch` when it resolves (rules Case). A required field that isn't given points at that field, such as `/folder`. A field the command doesn't take, such as `priority` on `folder update`, points at that field |
| `not-found` | 1 | A reference names nothing | The first reference that names nothing, in the order the change resolves them, since resolution stops there: its ID in `ids`, or its path in `paths`, a gate its folder lacks as its folder's path and end, such as `/work.start`; `archived` when it was archived. The order is the input's, its fields in its schema's order and a list's entries in order; a batch's is PRD Resolving a batch's. The message says whether it was deleted, archived or never issued, where an archived one went, and that an ID below the root's first ID came from an earlier root |
| `conflict` | 1 | The input is valid, but the tree's state refuses it | `rule`, and what the rule's row below says |
| `busy` | 1 | The write lock, or the database itself, stayed busy for 5 seconds. Always safe to retry |  |
| `not-initialized` | 1 | There is no root, or no database in it. Run `init` | `paths`: what is missing: the root when there is none, else the database |
| `corrupt` | 1 | SQLite reported the file corrupt, as `SQLITE_CORRUPT` or `SQLITE_NOTADB`, while a command ran, `doctor` included. No command but `doctor` checks the whole database. A failed integrity or foreign-key check in `doctor` is its `corrupt` finding, not this error | `paths`: the database |
| `unsupported-format` | 1 | The database is in a format this binary doesn't read | the message names both formats |
| `io` | 1 | The operating system refused something | `paths`: what it refused |
| `internal` | 1 | A bug in koan |  |
| `unavailable` | 1 | `pick` only: it can't run, since `/dev/tty` doesn't open, or fzf is missing, older than 0.63.0, or failed. It is only ever named in the standard-error line, since `pick` prints no envelope, so it isn't in the error schema | the message says which, and for an fzf too old, the version found and the one needed |

### Conflict rules

Each rule is a refusal the rules doc or the PRD makes, in the section its row names; this table says what the error carries.

| Rule | Refused because | From | `ids` | `paths` |
| --- | --- | --- | --- | --- |
| `case-mismatch` | A path matched a folder only ignoring case, or a new name differs from a sibling's only in case | rules Case, I4 | the folder | the path as given, up to the part that matched only ignoring case, then that path as it is |
| `cycle` | The change would close a cycle | rules I7 | one such cycle, in order: the task whose blocked\_by gains the closing edge, then the task it now waits on, then each task the one before waits on; the last waits on the first |  |
| `claimed` | The task already has a claim | rules Claims | the task |  |
| `not-finished` | Archiving a task that isn't complete, or a folder with an open task under it | rules Archiving | the open tasks |  |
| `not-empty` | Deleting a folder that holds tasks or folders other than its gates, without `--contents` | rules Changes | its direct contents, its gates left out |  |
| `has-material` | Deleting a folder that holds material, at any depth, without `--force`. Material here is as the rules doc's Changes counts it for a delete | rules Changes |  | the top-level material entries of its directory and of each folder under it, each written as the folder's path, `:`, then the entry's name, such as `/work/api:docs` or `/work/api:@Notes.md`, never a path on disk. They come in tree order: a folder's own entries, compared byte for byte, before those of the folders under it |
| `gate` | Moving a gate, even to its own folder | rules I11 | the gate |  |
| `into-itself` | Moving a folder inside itself | rules I3 | the folder | the destination |
| `exists` | The destination is taken: a path for a move or a create, by a folder or by an entry on disk; a directory for a backup; a database for init --ids-from | rules Changes, for a path; PRD Install, init, backup and restore otherwise |  | the taken path: a folder path when a folder has it, else the entry's path on disk |
| `not-a-root` | `init` found `~/koan-root` holding something that isn't koan's | PRD Install, init, backup and restore |  | what it found |
| `checkout` | `marker bind` on a directory whose `.git` is a directory, without `--force` | PRD Markers |  | the directory |
| `foreign-files` | `marker bind` or `marker unbind` on a `.koan/` that holds files that aren't koan's | PRD Markers |  | those files |
| `complete` | Claiming a task that is complete | rules Claims | the task |  |
| `bound` | marker bind on a directory already bound to another live folder, without --force | PRD Markers | the folder it is bound to | the marker.json |
| `no-ids` | The change would issue an ID past `999999999999999`, the last a root can have, to a task, a folder or a gate it makes. Any change that makes one can give it | rules Changes; PRD Writes, Issuing IDs in a change |  |  |

For `cycle`, the closing edge is the first new edge that closes a cycle with the tree's edges and the new edges before it, taken in the order they are given, as PRD Issuing IDs in a change orders them: each task's `blocked_by` before its `blocks`, a batch's parts in input order. `problems` also names every input field whose new edge lies on that cycle, in cycle order. A refused change issues no ID, so a task or gate it was making is named in `ids` by the ID it would have had, which a later change may issue to something else: `problems` points at the part that made it.

## Commands

Each command has one schema, named after it, holding its `input` and its `result`. Options are the input's fields in kebab-case; the subject is the field marked as such. Every command can also fail with `not-found` and `invalid-input`, and with `case-mismatch` wherever it takes a path; every command can give `pending-step` and `stuck-step`, and every read `pending-filesystem`. Only the warnings and refusals beyond those are listed.

A write that changes nothing, such as completing a complete task or removing an edge that isn't there, succeeds with changed false, and leaves updated\_at alone. An `extra` changes when it would print differently, since koan never interprets it: replacing `1.0` with `1` is a change. changed is in the result of every write that can change nothing: task and folder update, complete, reopen, block, unblock, release, and both moves, which change nothing when the destination is where the item already is. A gate is the exception: `task move` refuses it (`gate`) even to its own folder. An edge given more than once in one input, however it is written, counts once, as rules Changes says, so `blocked_by` and `blocks` take duplicates.

### Tasks

#### task create

`koan task create <title> [--folder] [--priority] [--tags] [--blocked-by] [--blocks] [--extra] [--notes | --notes-file]`

Makes a task, with its notes file, in a folder or the root. Its edges are written in the same change, and a gate they name is made. Warns `complete-blocker`, `claimed-blocked`. Refused: `cycle`.

```json
{ "$id": "task-create", "$defs": {
  "input": { "type": "object", "required": ["title"], "properties": {
    "title":      { "type": "string", "description": "Subject. Trimmed, then checked as a title." },
    "folder":     { "$ref": "types#/$defs/folder-ref", "default": "/" },
    "priority":   { "$ref": "types#/$defs/priority", "default": null },
    "tags":       { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true, "default": [] },
    "blocked_by": { "type": "array", "items": { "$ref": "types#/$defs/edge-ref" }, "default": [] },
    "blocks":     { "type": "array", "items": { "$ref": "types#/$defs/edge-ref" }, "default": [] },
    "extra":      { "$ref": "types#/$defs/extra", "default": {} },
    "notes":      { "type": "string", "default": "", "description": "The notes file's first content." } } },
  "result": { "type": "object", "required": ["id", "ref", "notes_path", "gates_made", "reblocked"], "properties": {
    "id":         { "$ref": "types#/$defs/id" },
    "ref":        { "$ref": "types#/$defs/kept" },
    "notes_path": { "$ref": "types#/$defs/abs-path" },
    "gates_made": { "type": "array", "items": { "$ref": "types#/$defs/gate-made" } },
    "reblocked":  { "$ref": "types#/$defs/ids", "description": "Ready tasks it made wait, through --blocks." } } } } }
```

#### task show

`koan task show <task>`. Reads one task. Warns `missing-notes`.

```json
{ "$id": "task-show", "$defs": {
  "input":  { "type": "object", "required": ["task"], "properties": { "task": { "$ref": "types#/$defs/task-ref", "description": "Subject." } } },
  "result": { "type": "object", "required": ["task"], "properties": { "task": { "$ref": "task" } } } } }
```

#### task list

`koan task list [--folder] [--recursive] [--state] [--readiness] [--tags-any] [--tags-all] [--claimed] [--limit] [--fields]`

Tasks in tree order: open ones unless `--state` says otherwise, gates included. At most 100 unless `--limit` says otherwise; `--limit null` lists every match. `total` counts every match; `truncated` says `--limit` cut the list.

```json
{ "$id": "task-list", "$defs": {
  "filter": { "type": "object", "properties": {
    "folder":    { "$ref": "types#/$defs/folder-ref", "default": "/" },
    "recursive": { "type": "boolean", "default": true, "description": "false: only the tasks directly in the folder." },
    "tags_any":  { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true, "default": [],
                   "description": "Only tasks holding at least one of these tags; empty filters nothing." },
    "tags_all":  { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true, "default": [],
                   "description": "Only tasks holding every one of these tags; empty filters nothing. Given with tags_any, a task must pass both." } } },
  "input": { "type": "object", "allOf": [ { "$ref": "#/$defs/filter" } ], "properties": {
    "state":     { "type": "array", "items": { "$ref": "types#/$defs/state" }, "uniqueItems": true, "minItems": 1, "default": ["open"] },
    "readiness": { "type": "array", "items": { "enum": ["ready", "blocked"] }, "uniqueItems": true, "minItems": 1,
                   "default": ["ready", "blocked"], "description": "Applies to open tasks; complete tasks that --state lets in pass through." },
    "claimed":   { "type": ["boolean", "null"], "default": null, "description": "true: only claimed; false: only unclaimed; null: both." },
    "limit":     { "type": ["integer", "null"], "minimum": 1, "default": 100, "description": "null: every match." },
    "fields":    { "type": "array", "items": { "$ref": "task#/$defs/field" }, "uniqueItems": true,
                   "description": "Left out: every field." } } },
  "result": { "type": "object", "required": ["tasks", "total", "truncated"], "properties": {
    "tasks":     { "type": "array", "items": { "$ref": "task#/$defs/partial" } },
    "total":     { "type": "integer", "minimum": 0 },
    "truncated": { "type": "boolean" } } } } }
```

#### task frontier

`koan task frontier [--folder] [--recursive] [--tags-any] [--tags-all] [--limit] [--fields]`. Ready, unclaimed tasks in work order, gates included. At most 100 unless `--limit` says otherwise, as for task list.

```json
{ "$id": "task-frontier", "$defs": {
  "input": { "type": "object", "allOf": [ { "$ref": "task-list#/$defs/filter" } ], "properties": {
    "limit":  { "type": ["integer", "null"], "minimum": 1, "default": 100, "description": "null: every match." },
    "fields": { "type": "array", "items": { "$ref": "task#/$defs/field" }, "uniqueItems": true } } },
  "result": { "$ref": "task-list#/$defs/result" } } }
```

#### task why

`koan task why <task>`. What a task waits on: its open blockers, and the ready tasks it waits on at any depth through open blockers, which are what to work on. A complete task holds nothing back, so the walk never passes one, and it stops at each ready task. Both are empty for a ready or complete task.

```json
{ "$id": "task-why", "$defs": {
  "input":  { "$ref": "task-show#/$defs/input" },
  "result": { "type": "object", "required": ["id", "readiness", "blockers", "ready"], "properties": {
    "id":        { "$ref": "types#/$defs/id" },
    "readiness": { "$ref": "types#/$defs/readiness" },
    "blockers":  { "type": "array", "items": { "$ref": "types#/$defs/edge-end" }, "description": "Its open blockers. Ascending by ID." },
    "ready":     { "type": "array", "items": { "$ref": "types#/$defs/id" }, "description": "Ready tasks it waits on, at any depth through open blockers, claimed ones included, since they are the work in progress. Work order." } } } } }
```

#### task update

`koan task update <task> [--title] [--priority] [--tags-add] [--tags-remove] [--tags-replace-all] [--extra-merge] [--extra-remove] [--extra-replace-all]`

Changes fields; at least one option is needed. `--tags-replace-all` can't go with `--tags-add` or `--tags-remove`, nor `--extra-replace-all` with `--extra-merge` or `--extra-remove`. A tag can't be both added and removed, nor a key both merged and removed. `--extra-merge` merges at the top level only.

```json
{ "$id": "task-update", "$defs": {
  "input": { "type": "object", "required": ["task"], "minProperties": 2, "properties": {
    "task":              { "$ref": "types#/$defs/task-ref", "description": "Subject." },
    "title":             { "type": "string" },
    "priority":          { "$ref": "types#/$defs/priority" },
    "tags_add":          { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true },
    "tags_remove":       { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true },
    "tags_replace_all":  { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true },
    "extra_merge":       { "$ref": "types#/$defs/extra" },
    "extra_remove":      { "type": "array", "items": { "type": "string" }, "uniqueItems": true },
    "extra_replace_all": { "$ref": "types#/$defs/extra" } },
    "not": { "anyOf": [ { "required": ["tags_replace_all", "tags_add"] }, { "required": ["tags_replace_all", "tags_remove"] },
                        { "required": ["extra_replace_all", "extra_merge"] }, { "required": ["extra_replace_all", "extra_remove"] } ] } },
  "result": { "$ref": "#/$defs/id-only" },
  "id-only": { "type": "object", "required": ["id", "changed"], "properties": {
    "id":      { "$ref": "types#/$defs/id" },
    "changed": { "type": "boolean", "description": "false when the write changed nothing." } } } } }
```

#### task complete, task reopen

`koan task complete <task>` marks it complete and ends its claim. `koan task reopen <task>` marks it open again. Reopen warns `claimed-blocked`.

```json
{ "$id": "task-complete", "$defs": {
  "input":  { "$ref": "task-show#/$defs/input" },
  "result": { "type": "object", "required": ["id", "changed", "released"], "properties": {
    "id":       { "$ref": "types#/$defs/id" },
    "changed":  { "type": "boolean" },
    "released": { "$ref": "types#/$defs/ids", "description": "Open tasks it made ready." } } } } }

{ "$id": "task-reopen", "$defs": {
  "input":  { "$ref": "task-show#/$defs/input" },
  "result": { "type": "object", "required": ["id", "changed", "reblocked"], "properties": {
    "id":        { "$ref": "types#/$defs/id" },
    "changed":   { "type": "boolean" },
    "reblocked": { "$ref": "types#/$defs/ids", "description": "Ready tasks it made wait, claimed ones included." } } } } }
```

#### task block, task unblock

`koan task block <task> [--blocked-by] [--blocks]` adds edges; `koan task unblock <task> [--blocked-by] [--blocks]` removes them. At least one list must hold an entry. Block makes any gate it names, its subject included, warns `complete-blocker` and `claimed-blocked`, and is refused by `cycle`. Unblock never makes a gate: naming one the folder lacks is `not-found`.

```json
{ "$id": "task-block", "$defs": {
  "input": { "type": "object", "required": ["task"], "properties": {
    "task":       { "$ref": "types#/$defs/task-ref", "description": "Subject." },
    "blocked_by": { "type": "array", "items": { "$ref": "types#/$defs/edge-ref" }, "default": [] },
    "blocks":     { "type": "array", "items": { "$ref": "types#/$defs/edge-ref" }, "default": [] } },
    "anyOf": [ { "required": ["blocked_by"], "properties": { "blocked_by": { "minItems": 1 } } },
               { "required": ["blocks"], "properties": { "blocks": { "minItems": 1 } } } ] },
  "result": { "type": "object", "required": ["id", "changed", "gates_made", "reblocked"], "properties": {
    "id":         { "$ref": "types#/$defs/id" },
    "changed":    { "type": "boolean" },
    "gates_made": { "type": "array", "items": { "$ref": "types#/$defs/gate-made" } },
    "reblocked":  { "$ref": "types#/$defs/ids" } } } } }

{ "$id": "task-unblock", "$defs": {
  "input":  { "$ref": "task-block#/$defs/input" },
  "result": { "$ref": "task-complete#/$defs/result" } } }
```

#### task move

`koan task move <task> --to <folder>`. Puts it in another folder or the root; its notes move with it. Warns `moved-claimed`. Refused: `gate`.

```json
{ "$id": "task-move", "$defs": {
  "input": { "type": "object", "required": ["task", "to"], "properties": {
    "task": { "$ref": "types#/$defs/task-ref", "description": "Subject." },
    "to":   { "$ref": "types#/$defs/folder-ref" } } },
  "result": { "type": "object", "required": ["id", "changed", "notes_path"], "properties": {
    "id":         { "$ref": "types#/$defs/id" },
    "changed":    { "type": "boolean" },
    "notes_path": { "$ref": "types#/$defs/abs-path" } } } } }
```

#### task delete

`koan task delete <task>`. Removes it, its notes, its claim and its edges.

```json
{ "$id": "task-delete", "$defs": {
  "input":  { "$ref": "task-show#/$defs/input" },
  "result": { "type": "object", "required": ["removed", "released"], "properties": {
    "removed":  { "$ref": "types#/$defs/ids" },
    "released": { "$ref": "types#/$defs/ids" } } } } }
```

#### task archive

`koan task archive <task>`. Moves a complete task to the archive, with its JSON beside it. Refused: `not-finished`.

```json
{ "$id": "task-archive", "$defs": {
  "input":  { "$ref": "task-show#/$defs/input" },
  "result": { "type": "object", "required": ["id", "archive", "json", "left"], "properties": {
    "id":      { "$ref": "types#/$defs/id" },
    "archive": { "anyOf": [ { "$ref": "types#/$defs/abs-path" }, { "type": "null" } ],
                 "description": "The archived notes, or for a folder its directory; null for a task archived without a notes file." },
    "json":    { "$ref": "types#/$defs/abs-path", "description": "The JSON beside it." },
    "left":    { "$ref": "types#/$defs/ids", "description": "Every ID that left the tree, now retired." } } } } }
```

#### task claim, task claim-next, task release

`koan task claim <task> [--extra]` claims a task, blocked or not, so long as it is open; refused: `claimed`, `complete`. `koan task claim-next [--folder] [--recursive] [--tags-any] [--tags-all] [--extra]` claims the first task of the frontier, or returns nulls when there is none; the task's record shows its new claim. `koan task release <task>` ends a claim, whoever made it.

```json
{ "$id": "task-claim", "$defs": {
  "input": { "type": "object", "required": ["task"], "properties": {
    "task":  { "$ref": "types#/$defs/task-ref", "description": "Subject." },
    "extra": { "$ref": "types#/$defs/extra", "default": {} } } },
  "result": { "type": "object", "required": ["claim"], "properties": { "claim": { "$ref": "claim" } } } } }

{ "$id": "task-claim-next", "$defs": {
  "input": { "type": "object", "allOf": [ { "$ref": "task-list#/$defs/filter" } ], "properties": {
    "extra": { "$ref": "types#/$defs/extra", "default": {} } } },
  "result": { "type": "object", "required": ["task", "claim"], "properties": {
    "task":  { "anyOf": [ { "$ref": "task" }, { "type": "null" } ] },
    "claim": { "anyOf": [ { "$ref": "claim" }, { "type": "null" } ] } } } } }

{ "$id": "task-release", "$defs": {
  "input":  { "$ref": "task-show#/$defs/input" },
  "result": { "$ref": "task-update#/$defs/id-only" } } }
```

### Folders

A folder subject is a `folder-ref`. The root can be read, but not updated, moved, deleted or archived: naming it there is `invalid-input`.

#### folder create

`koan folder create <path> [--title] [--extra] [--notes | --notes-file]`. Makes the folder, its directory and its notes, and any missing parents the same way, with no title. It has no gates until an edge names one. Refused: `case-mismatch`, `exists`.

```json
{ "$id": "folder-create", "$defs": {
  "input": { "type": "object", "required": ["path"], "properties": {
    "path":  { "allOf": [ { "$ref": "types#/$defs/path" }, { "not": { "const": "/" } } ], "description": "Subject." },
    "title": { "type": ["string", "null"], "default": null },
    "extra": { "$ref": "types#/$defs/extra", "default": {} },
    "notes": { "type": "string", "default": "", "description": "The @notes.md's first content, written by the step that makes the directory." } } },
  "result": { "type": "object", "required": ["id", "ref", "dir", "notes_path", "created"], "properties": {
    "id":         { "$ref": "types#/$defs/id" },
    "ref":        { "$ref": "types#/$defs/kept" },
    "dir":        { "$ref": "types#/$defs/abs-path" },
    "notes_path": { "$ref": "types#/$defs/abs-path" },
    "created":    { "type": "array", "items": { "$ref": "types#/$defs/folder-at" },
                    "description": "Every folder made, parents first, this one last." } } } } }
```

#### folder show

`koan folder show <folder>`. Reads one folder, or the root. Warns `missing-notes`.

```json
{ "$id": "folder-show", "$defs": {
  "input":  { "type": "object", "required": ["folder"], "properties": { "folder": { "$ref": "types#/$defs/folder-ref", "description": "Subject." } } },
  "result": { "type": "object", "required": ["folder"], "properties": { "folder": { "$ref": "folder" } } } } }
```

#### folder list

`koan folder list [--folder] [--recursive] [--limit] [--fields]`. The folders under `--folder`, itself left out, in tree order. At most 100 unless `--limit` says otherwise; `--limit null` lists every match.

```json
{ "$id": "folder-list", "$defs": {
  "input": { "type": "object", "properties": {
    "folder":    { "$ref": "types#/$defs/folder-ref", "default": "/" },
    "recursive": { "type": "boolean", "default": true, "description": "false: only the folders directly in the folder." },
    "limit":     { "type": ["integer", "null"], "minimum": 1, "default": 100, "description": "null: every match." },
    "fields":    { "type": "array", "items": { "$ref": "folder#/$defs/field" }, "uniqueItems": true } } },
  "result": { "type": "object", "required": ["folders", "total", "truncated"], "properties": {
    "folders":   { "type": "array", "items": { "$ref": "folder#/$defs/partial" } },
    "total":     { "type": "integer", "minimum": 0 },
    "truncated": { "type": "boolean" } } } } }
```

#### folder update

`koan folder update <folder> [--title] [--extra-merge] [--extra-remove] [--extra-replace-all]`. Changes title and extra, as `task update` does.

```json
{ "$id": "folder-update", "$defs": {
  "input": { "type": "object", "required": ["folder"], "minProperties": 2, "properties": {
    "folder":            { "$ref": "types#/$defs/folder-ref", "description": "Subject." },
    "title":             { "type": ["string", "null"] },
    "extra_merge":       { "$ref": "types#/$defs/extra" },
    "extra_remove":      { "type": "array", "items": { "type": "string" }, "uniqueItems": true },
    "extra_replace_all": { "$ref": "types#/$defs/extra" } },
    "not": { "anyOf": [ { "required": ["extra_replace_all", "extra_merge"] }, { "required": ["extra_replace_all", "extra_remove"] } ] } },
  "result": { "$ref": "task-update#/$defs/id-only" } } }
```

#### folder move

`koan folder move <folder> --to <path>`. Moves or renames it, everything under it included; `--to` is its whole new path; missing parents are made, as folder create makes them, and listed in created; each is a destination, as for folder create (rules doc, Changes). A case-only rename is a move. A move to its own path changes nothing. Warns `moved-claimed`. Refused: `into-itself`, `case-mismatch`, `exists`, checked in that order.

```json
{ "$id": "folder-move", "$defs": {
  "input": { "type": "object", "required": ["folder", "to"], "properties": {
    "folder": { "$ref": "types#/$defs/folder-ref", "description": "Subject." },
    "to":     { "allOf": [ { "$ref": "types#/$defs/path" }, { "not": { "const": "/" } } ] } } },
  "result": { "type": "object", "required": ["id", "changed", "path", "dir", "created"], "properties": {
    "id":      { "$ref": "types#/$defs/id" },
    "changed": { "type": "boolean" },
    "path":    { "$ref": "types#/$defs/path" },
    "dir":     { "$ref": "types#/$defs/abs-path" },
    "created": { "type": "array", "items": { "$ref": "types#/$defs/folder-at" },
                 "description": "Missing parents made for the new path, top first; empty when all were there." } } } } }
```

#### folder delete

`koan folder delete <folder> [--contents] [--force]`. Removes it and everything under it, gates included. Refused: `not-empty` without `--contents`, then `has-material` without `--force`, checked in that order.

```json
{ "$id": "folder-delete", "$defs": {
  "input": { "type": "object", "required": ["folder"], "properties": {
    "folder":   { "$ref": "types#/$defs/folder-ref", "description": "Subject." },
    "contents": { "type": "boolean", "default": false },
    "force":    { "type": "boolean", "default": false } } },
  "result": { "$ref": "task-delete#/$defs/result", "description": "removed holds every task and folder removed." } } }
```

#### folder archive

`koan folder archive <folder>`. Moves a finished folder to the archive, with its JSON beside it. The result is `task archive`'s. Refused: `not-finished`.

```json
{ "$id": "folder-archive", "$defs": {
  "input":  { "$ref": "folder-show#/$defs/input" },
  "result": { "$ref": "task-archive#/$defs/result" } } }
```

### Batch

`koan batch -i <file>`, or `-i -` for standard input. Makes folders and tasks and writes edges as one change: all of it or none. Its input is the batch file, under Input formats; there are no options. Warns `complete-blocker`, `claimed-blocked`. Refused by whatever refuses one of its parts, such as `cycle`, `case-mismatch` or `exists`, with `problems` pointing at the parts at fault.

```json
{ "$id": "batch", "$defs": {
  "input":  { "$ref": "batch-file" },
  "result": { "type": "object", "required": ["folders", "tasks", "labels", "gates_made", "reblocked"], "properties": {
    "folders":    { "type": "array", "items": { "$ref": "types#/$defs/folder-at" }, "description": "Every folder made, parents first." },
    "tasks":      { "type": "array", "items": { "$ref": "types#/$defs/id" }, "description": "Every task made, in input order." },
    "labels":     { "type": "object", "additionalProperties": { "$ref": "types#/$defs/id" },
                    "propertyNames": { "$ref": "types#/$defs/label" }, "description": "Each label and the ID it got." },
    "gates_made": { "type": "array", "items": { "$ref": "types#/$defs/gate-made" } },
    "reblocked":  { "$ref": "types#/$defs/ids" } } } } }
```

### Locating

#### where

`koan where <ref>` or `koan where --file <path>`: exactly one, and neither or both is `invalid-input` at `""`. Says what a reference or a path on disk names, both ways. A path outside the tree is found through the nearest marker; with none above it, it is not-found. A path elsewhere under \~/koan-root is not-found too, and one inside archived/ comes with archived filled in, from the ID in its name. An archived ID is `not-found`, with where it went. Warns `stale-marker`.

- **A path found through a marker** is `kind` `folder`, with the bound folder's `id`, `ref` and `folder`, `material` null, `file` the path as given, and `marker` the marker.json. A marker that binds nothing, for whatever reason, is `not-found`, with a message saying why and the marker.json in `paths`; when its ID names no live folder, that ID is in `ids`, and in `archived` when the folder was archived.
- **A path in the tree** names what koan keeps there: a folder's directory is the folder; its `@notes.md` is the folder; an `@<id>.md` where that task's notes belong, or a gate's `@start.md` or `@done.md`, is the task; an `@.tmp-<seq>` or `@.case-<seq>` is the folder whose directory holds it; and a path whose first part below a folder's directory doesn't start with `@` is material. Anything else is `not-found`: an `@<id>.md` where its task's notes don't belong, a stray `@` entry, an `@` directory that is no folder, or an `@` name koan doesn't have. A directory spelled in another case than its folder's name is `case-mismatch`. A path need not exist on disk. `file` is the path as given, under \~/koan-root as written however it reached the tree.
- **A path in `archived/`** is `not-found`, with `archived` filled in for the nearest name on the path, from the item upward, that carries an ID.

```json
{ "$id": "where", "$defs": {
  "input": { "type": "object", "properties": {
    "ref":  { "$ref": "types#/$defs/ref", "description": "Subject." },
    "file": { "$ref": "types#/$defs/abs-path" } },
    "oneOf": [ { "required": ["ref"] }, { "required": ["file"] } ] },
  "result": { "$ref": "location" } } }
```

#### context

`koan context [<folder>]` or `koan context --file <path>`, at most one; with neither, the working directory. Paths resolve as for where. Briefs an agent on the folder: its record, the folders above it, its notes and its open tasks. Warns `stale-marker`, `missing-notes`.

```json
{ "$id": "context", "$defs": {
  "input": { "type": "object", "properties": {
    "folder": { "$ref": "types#/$defs/folder-ref", "description": "Subject." },
    "file":   { "$ref": "types#/$defs/abs-path" } },
    "oneOf": [ { "required": ["folder"] }, { "required": ["file"] } ] },
  "result": { "$ref": "brief" } } }
```

### Export

`koan export [<folder>]` or `koan export --file <path>`, at most one; with neither, the working directory. The folder is found as for context, the root included. Returns the folder's whole subtree in one read transaction, for a tool to turn into a graph or a file (PRD Command surface): its folders and every task under it, the tasks outside it at the other end of an edge, and the edges. There is nothing to narrow it and no limit. Warns `stale-marker`.

- **`folders`** is what `folder list` returns for the folder with `--limit null`, and **`tasks`** what `task list` returns for it with `--state open,complete` and `--limit null`, gates included, but read in the same snapshot as the rest. For `/`, `folder` is the root's record, whose `open` and `complete` count the whole tree.
- **`external`** is every task outside the subtree at the other end of an edge with a task in `tasks`, in either direction, complete ones and other folders' gates included: direct neighbours only. A gate's `gate` and `folder` say whose it is. It is empty for `/`.
- **`edges`** is every edge with at least one end in `tasks`, each once, with Archive JSON's shape and order. Both ends of every edge are in `tasks` or `external`, so an edge between two tasks of `external` isn't listed.
- **Only the database is read.** export never lists the tree's directory, so material never appears, and never checks a notes file, so it gives no `missing-notes`. Its paths follow the read conventions, with `pending-filesystem` beside those whose steps are still running.
- **The same state prints the same result**, byte for byte: the conventions in Introduction fix every order in it.

```json
{ "$id": "export", "$defs": {
  "input": { "type": "object", "properties": {
    "folder": { "$ref": "types#/$defs/folder-ref", "description": "Subject." },
    "file":   { "$ref": "types#/$defs/abs-path" } },
    "oneOf": [ { "required": ["folder"] }, { "required": ["file"] } ] },
  "result": { "type": "object", "required": ["folder", "folders", "tasks", "external", "edges"], "properties": {
    "folder":   { "$ref": "folder", "description": "The subject; the root for /." },
    "folders":  { "type": "array", "items": { "$ref": "folder" }, "description": "Every folder under it, at any depth, itself left out, in tree order." },
    "tasks":    { "type": "array", "items": { "$ref": "task" }, "description": "Every task under it, at any depth, open and complete, gates included, in tree order." },
    "external": { "type": "array", "items": { "$ref": "task" }, "description": "Every task outside it at the other end of an edge with a task in tasks. Ascending by ID; empty for /." },
    "edges":    { "type": "array", "items": { "$ref": "#/$defs/edge" }, "description": "Every edge with an end in tasks, each once. Sorted by task, then blocker." } } },
  "edge": { "type": "object", "required": ["task", "blocker"], "properties": {
    "task":    { "$ref": "types#/$defs/id", "description": "The task that waits." },
    "blocker": { "$ref": "types#/$defs/id", "description": "The task it waits on." } } } } }
```

### Markers

`koan marker bind <folder> [--dir] [--force]` binds a directory outside the tree to a folder, replacing a marker for the same folder, or one that binds nothing, as PRD Markers says when. Replacing one bound to another live folder needs --force. The root as `<folder>` is `invalid-input`, and a `--dir` that doesn't exist is `io`. `koan marker unbind [--dir]` removes the binding, in the order PRD Markers gives; with none there, it succeeds and says so. A directory anywhere under \~/koan-root is `invalid-input`, since markers there are never read. Refused: `checkout` and bound (bind, without `--force`), `foreign-files` (both). After the input's shape and the root's state, bind checks the directory under the root, then that it exists, then the folder, then `foreign-files`, then `checkout`, then `bound`; unbind makes the same checks up to `foreign-files`, and a `--dir` that doesn't exist is `io` for it too.

```json
{ "$id": "marker-bind", "$defs": {
  "input": { "type": "object", "required": ["folder", "dir"], "properties": {
    "folder": { "$ref": "types#/$defs/folder-ref", "description": "Subject. Never the root, which has no ID." },
    "dir":    { "$ref": "types#/$defs/abs-path" },
    "force":  { "type": "boolean", "default": false } } },
  "result": { "type": "object", "required": ["marker", "file", "replaced"], "properties": {
    "marker":   { "$ref": "marker-file" },
    "file":     { "$ref": "types#/$defs/abs-path", "description": "The marker.json written." },
    "replaced": { "type": "boolean", "description": "Whether a marker was there before." } } } } }

{ "$id": "marker-unbind", "$defs": {
  "input": { "type": "object", "required": ["dir"], "properties": { "dir": { "$ref": "types#/$defs/abs-path" } } },
  "result": { "type": "object", "required": ["dir", "removed"], "properties": {
    "dir":     { "$ref": "types#/$defs/abs-path" },
    "removed": { "type": "boolean", "description": "false when there was no marker." } } } } }
```

### Operations

#### init

`koan init [--ids-from]`. Makes `~/koan-root`, its `tree/` and `archived/`, and the database; run again, it changes nothing that is there and reports what it found. `--ids-from` sets the first ID a new root issues; given for a root that already has a database, which has issued IDs already, it is refused with `exists`. koan's own names in the root are `koan.db`, `koan.db-wal`, `koan.db-shm`, `koan.db-journal` (the rollback journal SQLite writes while init creates the database, before WAL mode is set, and may leave behind when init is cut short) as regular files, and `tree/` and `archived/` as directories, byte for byte, without following a symlink; anything else is `not-a-root`, with every such path in `paths`, in byte order, and a `~/koan-root` that isn't a directory is `not-a-root` with the root as its path. not-a-root is checked before `exists`. init takes the write lock once the root directory exists, so it can fail with `busy`. A database with format 0 and no tables is an unfinished init, which init finishes and lists in `created`; any other database in another format, or a corrupt one, fails init as it fails every command. init checks the database it finds, and `--ids-from`, before it makes `tree/`, `archived/` or anything else, so an init that fails for what it found makes nothing. Refused: `not-a-root`, `exists`.

```json
{ "$id": "init", "$defs": {
  "input": { "type": "object", "properties": {
    "ids_from": { "anyOf": [ { "$ref": "types#/$defs/id" }, { "type": "null" } ], "default": null,
                  "description": "null: a new root issues from 1." } } },
  "result": { "type": "object", "required": ["root", "tree", "archive", "database", "created", "first_id"], "properties": {
    "root":     { "$ref": "types#/$defs/abs-path" },
    "tree":     { "$ref": "types#/$defs/abs-path" },
    "archive":  { "$ref": "types#/$defs/abs-path" },
    "database": { "$ref": "types#/$defs/abs-path" },
    "created":  { "type": "array", "items": { "enum": ["root", "tree", "archive", "database"] }, "uniqueItems": true,
                  "description": "What this run made; empty when all of it was there." },
    "first_id": { "$ref": "types#/$defs/id" } } } } }
```

#### info, version

`koan info` reports the root and its state without taking the lock, and never fails for the root's state: it reports it. A `~/koan-root` that isn't a directory, or that koan can't list, is the operating system refusing, not a state: info fails with `io`. `koan version` reports the binary. `next_id` is the `--ids-from` to give a root that replaces this one; it is null unless the state is usable, and null once the root has issued its last ID, `999999999999999`, since no `--ids-from` follows it. `format` is the database's whenever koan can read it, so an `unsupported-format` root reports it, and a corrupt one does when it can be read. If SQLite itself stays busy for 5 seconds, info fails with `busy`.

```json
{ "$id": "info", "$defs": {
  "input":  { "type": "object" },
  "result": { "type": "object",
    "required": ["root", "tree", "archive", "database", "state", "format", "next_id", "tasks", "folders", "claims", "pending", "stuck", "error"],
    "properties": {
      "root":     { "$ref": "types#/$defs/abs-path" },
      "tree":     { "$ref": "types#/$defs/abs-path" },
      "archive":  { "$ref": "types#/$defs/abs-path" },
      "database": { "$ref": "types#/$defs/abs-path" },
      "state":    { "enum": ["usable", "not-initialized", "unsupported-format", "corrupt"] },
      "format":   { "type": ["integer", "null"], "description": "The database's; null when there is none, or it can't be read." },
      "next_id":  { "anyOf": [ { "$ref": "types#/$defs/id" }, { "type": "null" } ], "description": "Null unless usable, and once the last ID is issued." },
      "tasks":    { "type": ["integer", "null"], "description": "Live tasks. This and the counts below are null unless usable." },
      "folders":  { "type": ["integer", "null"] },
      "claims":   { "type": ["integer", "null"] },
      "pending":  { "type": ["integer", "null"], "description": "Pending filesystem steps, stuck ones included." },
      "stuck":    { "type": ["integer", "null"] },
      "error":    { "anyOf": [ { "$ref": "error" }, { "type": "null" } ],
                    "description": "Unless usable, the error other commands fail with." } } } } }

{ "$id": "version", "$defs": {
  "input":  { "type": "object" },
  "result": { "type": "object", "required": ["version", "commit", "go", "format"], "properties": {
    "version": { "type": "string", "description": "The version Go stamped on the build: the tag, such as v0.4.0, for go install at a tag or a build of a clean checkout at one; a pseudo-version, such as v0.0.0-20261021043753-563ff471b641, for a build elsewhere in a checkout, ending +dirty when it has changes not committed; (devel) when Go stamped none, as for go run or a build outside a checkout." },
    "commit":  { "type": ["string", "null"], "description": "The commit Go stamped on a build in a checkout; null when it stamped none, as for go install at a tag or go run." },
    "go":      { "type": "string" },
    "format":  { "type": "integer", "description": "The database format this binary reads." } } } } }
```

#### doctor, repair

`koan doctor` reads one snapshot and reports every finding, in the order of the kinds in Finding, then by path, then by IDs. `koan repair [--kinds]` fixes the repairable findings of the kinds given, all of them when left out, in that same order, and reports what it fixed and what it left. `stuck-step` is never repairable, since a person must act first, by keeping one copy or moving the material out; but when its kind is given, or none are, repair retries every stuck change in seq order all the same. A change that now succeeds goes in `fixed`, the rest in `left`. doctor gives no warnings: its findings carry the pending and stuck steps, and it takes no lock to tell one from the other.

```json
{ "$id": "doctor", "$defs": {
  "input":  { "type": "object" },
  "result": { "type": "object", "required": ["findings"], "properties": {
    "findings": { "type": "array", "items": { "$ref": "finding" } } } } } }

{ "$id": "repair", "$defs": {
  "input": { "type": "object", "properties": {
    "kinds": { "type": "array", "uniqueItems": true, "minItems": 1,
               "items": { "enum": ["pending-step", "stuck-step", "stray-directory", "missing-directory",
                                   "case-differs", "misplaced-notes", "orphan-notes"] } } } },
  "result": { "type": "object", "required": ["fixed", "left"], "properties": {
    "fixed": { "type": "array", "items": { "$ref": "finding" } },
    "left":  { "type": "array", "items": { "$ref": "finding" }, "description": "Every finding still there, repairable or not." } } } } }
```

#### backup

`koan backup <dir>`. Copies the database, the tree and the archive into an empty or new directory, then checks the copy with `doctor` against the live root. `clean` is false when the copy, even after the second copy under the lock, has findings the live root doesn't; `findings` lists them, with the live root's own. Two findings are the same when their kind, detail and IDs match and their paths match relative to each root; `findings` gives the copy's paths. The directory takes the root's layout: `koan.db`, `tree/` and `archived/`. `files` and `bytes` count every regular file copied, the database included. A new directory is made with any parents it lacks. Refused: `exists`, when the directory holds anything. A directory under \~/koan-root is invalid-input, since the copy would copy itself. backup gives no warnings: its findings carry the pending and stuck steps.

```json
{ "$id": "backup", "$defs": {
  "input": { "type": "object", "required": ["dir"], "properties": {
    "dir": { "$ref": "types#/$defs/abs-path", "description": "Subject." } } },
  "result": { "type": "object", "required": ["dir", "files", "bytes", "recopied", "clean", "findings"], "properties": {
    "dir":      { "$ref": "types#/$defs/abs-path" },
    "files":    { "type": "integer", "minimum": 0 },
    "bytes":    { "type": "integer", "minimum": 0 },
    "recopied": { "type": "boolean", "description": "Whether it copied a second time, holding the lock." },
    "clean":    { "type": "boolean" },
    "findings": { "type": "array", "items": { "$ref": "finding" } } } } } }
```

### For people

#### Completing references

`koan completion <shell>`'s script (Invocation) completes the references an argument takes, as well as commands and options. It is for people, at a shell's Tab, and is never an agent's way in (PRD Agents). koan answers through cobra's `__complete` commands: a candidate per line, each with an optional description after a tab, then cobra's directive line. It offers what the command would take without refusing, read from the tree as it is, and changes nothing. Every argument in the table below completes as this section says; every other value, except the files below, offers nothing, never file names.

| Argument | Offers |
| --- | --- |
| `task show`, `task why`, `task update`, `task delete`, `task unblock` subject | tasks and made gates |
| `task complete` subject | open tasks and made gates |
| `task reopen`, `task archive` subject | complete tasks and made gates |
| `task claim` subject | open, unclaimed tasks and made gates |
| `task release` subject | claimed tasks and made gates |
| `task move` subject | tasks |
| `task block` subject | tasks and gates, made or not |
| `--blocked-by`, `--blocks` | tasks, gates made or not, and folders |
| `--folder` (`task create`, `task list`, `task frontier`, `task claim-next`, `folder list`, `pick`), `task move --to` | folders |
| `folder show`, `folder update`, `folder move`, `folder delete`, `context`, `export`, `marker bind` subject | folders |
| `folder archive` subject | finished folders |
| `folder create` subject, `folder move --to` | folders as paths only, as steps toward a new path: the last name is the person's to type |
| `where` subject | tasks, made gates, and folders |
| `backup` subject, `--dir`, `--file`, `--notes-file`, `--input` | files and directories, as the shell completes them |

"Tasks" here never includes gates, and "gates made or not" means both ends of every folder. A row's state or claim holds for its gates too, as `pick`'s filters do: `task complete` offers open tasks and open gates, and `task claim` open, unclaimed ones of each. A gate not made yet is offered only where an edge names it, since anywhere else it is `not-found` (rules Resolution).

**What is typed decides the form.** The word being completed is read as one of:

- **A path**, starting with `/`. Its part up to the last `/` must name a folder exactly, byte for byte, the root for a word with one `/`; otherwise nothing is offered. The candidates are that folder's child folders whose names start with the rest, as whole paths: `/wo` offers `/work`, and `/work/` offers `/work/api` and `/work/web`. A folder is offered as a step toward what is below it wherever the argument takes a folder or a gate, even where a folder itself isn't taken. When the word is a whole folder path, or one followed by `.` and the start of an end, the folder's gates the argument takes are offered too: `/work/api.start` and `/work/api.done`. A path candidate never ends in `/`, and the shell adds no space after it, so the person goes on with `/`, `.`, or a space. Path candidates carry no description.
- **An ID**: empty, `@`, or an optional `@` then digits, then for a gate `.` and the start of an end. The candidates are the tasks, gates and folders the argument takes whose IDs start with the digits typed, a gate by its folder's ID and end, as `pick` prints it (rules References): `@4` offers `@42`, `@41`, and `@41.start`. They are written with the `@` when the word starts with one or is empty, and without it otherwise: `4` offers `42`. Each carries a description: a task's title, two spaces, then its folder's path; a gate's `Start of <path>` or `Done of <path>`; a folder's path, then two spaces and its title when it has one.
- **Anything else**, such as an anchored path, material after a `:`, or a label, offers nothing.

**Lists.** Only a list's first item completes: a word holding a comma offers nothing. For more, repeat the option, `--blocked-by 42 --blocked-by @4`, as Invocation allows.

**Order.** Candidates come in `pick`'s order (pick, The candidates), and the script asks the shell to keep it.

**Too many.** More than 100 candidates offers none. Instead it gives one line of cobra's ActiveHelp, `<n> match: type more, or Alt-K to pick`, which bash and zsh show under the prompt; cobra's `KOAN_ACTIVE_HELP=0` turns it off.

**Failures** offer nothing: a root that isn't usable, `busy`, or any other error. Completion prints no envelope, and nothing a person would see.

**bash** needs the bash-completion package, as cobra's bash script does.

#### pick

`koan pick [--folder] [--recursive] [--here] [--state] [--readiness] [--claimed] [--tags-any] [--tags-all] [--frontier] [--kinds] [--query]`, or `koan pick --bash`, or `koan pick --zsh`.

Lets a person at a terminal choose tasks, gates and folders in fzf, and prints their references on standard output, for `$(koan pick)`, or for the key `--bash` or `--zsh` binds. It is for people, and departs from the rest of this spec where Invocation says: it prints plain text, never an envelope. Agents never run it (PRD Agents).

```json
{ "$id": "pick", "$defs": {
  "input": { "type": "object", "allOf": [ { "$ref": "task-list#/$defs/filter" } ], "properties": {
    "file":      { "$ref": "types#/$defs/abs-path",
                   "description": "Pick from the folder this path is in, found as where --file finds it. Given with folder, invalid-input at \"\"." },
    "state":     { "$ref": "task-list#/$defs/input/properties/state" },
    "readiness": { "$ref": "task-list#/$defs/input/properties/readiness" },
    "claimed":   { "$ref": "task-list#/$defs/input/properties/claimed" },
    "frontier":  { "type": "boolean", "default": false,
                   "description": "The frontier's tasks instead of task list's, in work order. Given with state, readiness or claimed, invalid-input at \"\"." },
    "kinds":     { "type": "array", "items": { "enum": ["task", "gate", "folder"] }, "uniqueItems": true, "minItems": 1,
                   "default": ["task", "gate", "folder"] },
    "query":     { "type": "string", "default": "", "description": "fzf's starting query, in fzf's syntax." },
    "bash":      { "type": "boolean", "default": false,
                   "description": "Print the bash keybinding script instead of picking. Given with any other field, invalid-input at \"\"." },
    "zsh":       { "type": "boolean", "default": false,
                   "description": "Print the zsh keybinding script instead of picking. Given with any other field, invalid-input at \"\"." } } } } }
```

**Requirements.** `/dev/tty` opens for reading and writing; standard input and output may be anything. fzf is on `PATH`, version 0.63.0 or later, as `fzf --version` reports it run without `FZF_DEFAULT_OPTS` and `FZF_DEFAULT_OPTS_FILE`: its first word, without any `-` suffix, compared as three numbers.

**The candidates.** `folder` (`/` by default, or the folder `file` is in) and everything under it, as `recursive` says, in tree order (rules Tree order), of the `kinds` asked for:

- **A folder** is a candidate, the one `folder` names included unless it is the root. It prints as `@13`.
- **A task** is one of `task list`'s with the same filter, `state`, `readiness` and `claimed`, or of `task frontier`'s with `frontier`. It prints as `@42`.
- **A gate** is a candidate for both ends of every folder that is one, whether or not it is made. It prints as `@13.start` or `@13.done`. A made gate passes the task filter as any task does; a gate not made yet passes only when there is no task filter. A gate is never also a task candidate.
- **A task filter** is any of `readiness` narrowed to one, `claimed` not null, `tags_any` or `tags_all` not empty, or `frontier`. With one, a folder is a candidate only when a task candidate is under it, at any depth `recursive` allows. `state` is no task filter: a new, empty folder is a candidate by default.
- **The order:** a folder, then its gates not made yet, start before done, then its own tasks, made gates among them, in tree order, then its folders. With `recursive` false, the folders directly in `folder` come as lines of their own, without their gates or tasks. With `frontier`, the task candidates come first, in work order, then the folder candidates in tree order.

**Lines.** fzf gets one line per candidate, fields split by a tab: the reference as it prints, then the notes file's path (empty for a gate not made yet), then what fzf shows and searches, columns separated by two spaces, each but the last padded to the widest in terminal cells:

| Column | Holds |
| --- | --- |
| kind | `task`, `gate` or `folder` |
| reference | as it prints |
| folder | the folder's path: a task's or gate's folder, a folder's own |
| title | a task's title; a folder's title, or its name when it has none; a gate's `Start` or `Done` |
| state | an open task's or made gate's readiness, `complete`, `not made` for a gate not made yet, or `<n> open` for a folder |
| more | when set: the priority as `p2`, each tag as `#tag`, and `claimed` |

**Preview.** Below the list (`down,50%`), the line under the cursor: a header, then its notes file whole. The header names it in kept text (rules References), then gives, a field to a line: for a task or made gate, its title, folder, state, readiness, priority, tags, what it waits on and what waits on it, each in kept text with its state, and its claim with when it was made; for a folder, its title, `open`, `complete`, `finished` and its gates; for a gate not made yet, that it isn't made, and that the first edge naming it makes it. Before fzf starts, pick writes each header to a file named by its reference, in a private directory `koan-pick-<random>` (mode `0700`, under `$XDG_RUNTIME_DIR` if set, else the system's temporary directory), and removes it as soon as fzf returns, whatever it returned. fzf's preview command is `cat` of that file, then of the notes path, quoted for `sh`.

**fzf's options.** `FZF_DEFAULT_OPTS` and `FZF_DEFAULT_OPTS_FILE` are honoured, for colours, borders and history. After them, pick passes `--no-select-1 --no-exit-0 --no-expect --no-tmux --no-read0 --no-header-lines --no-print0 --no-print-query --no-tac --accept-nth ..`, undoing any that would accept or abort without the person, change what fzf reads or prints, reverse the order, or move it into a popup. Then its own: `--multi`, `--delimiter '\t'`, `--with-nth 3..`, `--tiebreak index`, `--with-shell 'sh -c'`, `--preview`, `--preview-window down,50%`, and `--query`. The layout is fzf's own, the first line next to the prompt. `FZF_DEFAULT_COMMAND` is never used.

**Picking several.** Tab marks lines; Enter picks the marked lines, or the line under the cursor when none is marked. The references print in the candidates' order, whatever order they were marked in, joined with `,`, then a newline: `@42,@13,@9.start`.

**Outcomes.**

| What happened | Standard output | Exit |
| --- | --- | --- |
| Picked | the references, then a newline | 0 |
| Esc or ctrl-c in fzf (fzf exits 130) | nothing | 130 |
| Nothing matched (fzf exits 1), or no candidates, in which case fzf never opens | nothing | 1 |
| A failure, `usage` included | nothing; the standard-error line, `koan: <kind>: <message>` | 1, or 2 for `usage` |

**The order of checks.** The input's shape; the root's state; `unavailable` when `/dev/tty` doesn't open, then for fzf missing, too old, or `fzf --version` failing; the references (`folder`, or the marker `file` is under, as `where --file` resolves it); no candidates; then fzf. fzf exiting anything but 0, 1 or 130 is `unavailable`, and its own standard error, such as a bad option in `FZF_DEFAULT_OPTS`, reaches the terminal. While fzf runs, pick catches SIGINT and SIGQUIT and discards them: ctrl-c is fzf's key, and cancels. Warnings from the reads give the usual standard-error line once fzf has returned.

**`--bash`** prints a bash script on standard output, with exit 0, and never reads `HOME` or the root, as for `version`. `eval "$(koan pick --bash)"` in `.bashrc` defines the shell function `__koan_pick_widget` and binds it to Alt-K (`\ek`) in emacs and vi insert modes; `bind -x '"<key>": __koan_pick_widget'` binds another. The function runs `koan pick` and, when it exits 0, inserts what it printed, without the newline, at the cursor, and moves the cursor past it; any other exit inserts nothing. It needs bash 4.0 or later, for `READLINE_LINE` and `READLINE_POINT`; on an older bash the script binds nothing.

**`--zsh`** prints a zsh script on standard output, with exit 0, and never reads `HOME` or the root, as for `version`. `eval "$(koan pick --zsh)"` in `.zshrc` defines the shell function `__koan_pick_widget`, makes it a zle widget of that name, and binds it to Alt-K (`\ek`) in the `emacs` and `viins` keymaps; `bindkey '<key>' __koan_pick_widget` binds another. The widget runs `koan pick` and, when it exits 0, inserts what it printed, without the newline, at the cursor, and moves the cursor past it; any other exit inserts nothing. Either way it then redraws the prompt and the line, which fzf drew over.

**Alt-K** is the key that sends escape then `k`. A terminal on macOS sends that for Option-K only when it is set up to, which is the terminal's setting, not koan's; the README gives the settings for kitty, iTerm2 and Terminal.app.

## Input formats

### Batch file

A plan of folders, tasks and edges, applied as one change. It holds 1 to 1000 parts, folders and tasks together. The batch is read as the tree it would leave, so a part can name a folder or a label that another part makes, in any order.

- **Folders** are named by path. A path given twice, or one already in the tree, refuses the batch as `exists`; missing parents are made, as `folder create` makes them. Each folder made, a missing parent included, is a destination (rules doc, Changes).
- **Tasks** go in their own `folder`, or the batch's `folder`, or the root. A task's folder can be one the batch makes. The batch's `folder` is resolved whether or not a task takes it, so one that names nothing is `not-found`.
- **Labels** name a task within the batch only. Each is defined once, and takes no end and no material: `$a.start` is `invalid-input`. Labels are never stored.
- **Edges** are written from either end, as on the command line, with labels allowed wherever an `edge-ref` is, and nowhere else: a label where a folder or a task subject is taken is `invalid-input`.

```json
{
  "folders": [ { "path": "/work/api", "title": "API rework" } ],
  "folder": "/work/api",
  "tasks": [
    { "label": "$design", "title": "Design the endpoints", "blocked_by": ["/work/api.start"] },
    { "label": "$build",  "title": "Build them", "blocked_by": ["$design"], "blocks": ["/work/api"] },
    { "title": "Review", "folder": "/work", "blocked_by": ["/work/api"] }
  ]
}
```

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "batch-file",
  "type": "object",
  "properties": {
    "folders": { "type": "array", "default": [], "items": { "type": "object", "required": ["path"], "properties": {
      "path":  { "allOf": [ { "$ref": "types#/$defs/path" }, { "not": { "const": "/" } } ] },
      "title": { "type": ["string", "null"], "default": null },
      "extra": { "$ref": "types#/$defs/extra", "default": {} },
      "notes": { "type": "string", "default": "" } } } },
    "folder":  { "$ref": "types#/$defs/folder-ref", "default": "/", "description": "Where tasks go that name no folder." },
    "tasks":   { "type": "array", "default": [], "items": { "type": "object", "required": ["title"], "properties": {
      "label":      { "$ref": "types#/$defs/label" },
      "title":      { "type": "string" },
      "folder":     { "$ref": "types#/$defs/folder-ref" },
      "priority":   { "$ref": "types#/$defs/priority", "default": null },
      "tags":       { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true, "default": [] },
      "blocked_by": { "type": "array", "default": [], "items": { "anyOf": [ { "$ref": "types#/$defs/edge-ref" }, { "$ref": "types#/$defs/label" } ] } },
      "blocks":     { "type": "array", "default": [], "items": { "anyOf": [ { "$ref": "types#/$defs/edge-ref" }, { "$ref": "types#/$defs/label" } ] } },
      "extra":      { "$ref": "types#/$defs/extra", "default": {} },
      "notes":      { "type": "string", "default": "" } } } }
  },
  "anyOf": [ { "required": ["folders"], "properties": { "folders": { "minItems": 1 } } },
             { "required": ["tasks"],   "properties": { "tasks":   { "minItems": 1 } } } ],
  "description": "folders and tasks hold 1 to 1000 parts between them, which koan checks."
}
```

### Option values

| Value | On the command line | Through `--input` |
| --- | --- | --- |
| `extra`, `--extra-merge`, `--extra-replace-all` | One JSON object in one argument. Anything else is `invalid-input` | A JSON object |
| `--extra-remove` | One key per occurrence, repeated for more | An array of keys |
| Tags | Comma-separated, or the option repeated; lowercase | An array |
| References in a list | Comma-separated, or repeated: `--blocked-by 17,@41.start` | An array of strings or integers |
| `--fields` | Comma-separated field names from the record's `field` list | An array of names |
| `--state`, `--readiness`, `--kinds` | Comma-separated values | An array |
| Integers: `--priority`, `--limit`, `--ids-from` | An integer as JSON writes it, or `null`. Anything else is `usage` | An integer, or null |
| Booleans | `--name` or `--name=true`, `--name=false`, and `--claimed=null`. Anything else is `usage` | `true`, `false`, or null where it can be |

`--fields` returns `id` first whether named or not, then the named fields in the record's order. An unknown name is `invalid-input`.

## On-disk formats

These are the files koan writes for people and other tools to read. Each names its own format in `schema`, an integer, and is written whole through a temp file renamed into place.

**How they change.** These formats are separate from the database's, so a fresh `init` before 1.0 leaves them readable. A new field can be added without a new `schema`, so readers ignore fields they don't know, at every level of the file; removing or changing a field takes a new `schema`. koan never reads archive files back; it reads `marker.json` when it looks for a marker, and a marker in a `schema` it doesn't know binds nothing.

### Archive JSON

Beside every archived item, a file of the same name ending `.json`. A task's file, or a lone gate's, holds that task; a folder's holds every folder and task that left with it. `edges` holds every edge that touched what left, to or from anything, including those removed from tasks that stayed. Notes and material aren't in it: they are the files beside it.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "archive-file",
  "type": "object",
  "required": ["schema", "kind", "archived_at", "path", "folders", "tasks", "edges"],
  "properties": {
    "schema":      { "const": 1 },
    "kind":        { "enum": ["task", "folder"], "description": "What was archived." },
    "archived_at": { "$ref": "types#/$defs/timestamp" },
    "path":        { "$ref": "types#/$defs/path", "description": "The archived folder's path, or the archived task's folder's path." },
    "folders":     { "type": "array", "items": { "$ref": "#/$defs/folder" },
                     "description": "The archived folder, then the folders under it, in tree order; empty for a task." },
    "tasks":       { "type": "array", "items": { "$ref": "#/$defs/task" },
                     "description": "The archived task, or every task under the archived folder, gates included, in tree order." },
    "edges":       { "type": "array", "items": { "$ref": "#/$defs/edge" }, "description": "Sorted by task, then blocker." }
  },
  "$defs": {
    "folder": { "type": "object", "required": ["id", "parent", "name", "path", "title", "extra"], "properties": {
      "id":     { "$ref": "types#/$defs/id" },
      "parent": { "anyOf": [ { "$ref": "types#/$defs/id" }, { "type": "null" } ], "description": "null for the root." },
      "name":   { "$ref": "types#/$defs/name" },
      "path":   { "$ref": "types#/$defs/path" },
      "title":  { "anyOf": [ { "$ref": "types#/$defs/title" }, { "type": "null" } ] },
      "extra":  { "$ref": "types#/$defs/extra" } } },
    "task": { "type": "object",
      "required": ["id", "gate", "folder", "title", "priority", "created_at", "completed_at", "updated_at", "tags", "extra"],
      "properties": {
        "id":           { "$ref": "types#/$defs/id" },
        "gate":         { "$ref": "types#/$defs/gate" },
        "folder":       { "type": "object", "required": ["id", "path"], "properties": {
                            "id":   { "anyOf": [ { "$ref": "types#/$defs/id" }, { "type": "null" } ] },
                            "path": { "$ref": "types#/$defs/path" } } },
        "title":        { "$ref": "types#/$defs/title" },
        "priority":     { "$ref": "types#/$defs/priority" },
        "created_at":   { "$ref": "types#/$defs/timestamp" },
        "completed_at": { "$ref": "types#/$defs/timestamp", "description": "Never null: only complete tasks leave." },
        "updated_at":   { "$ref": "types#/$defs/timestamp" },
        "tags":         { "type": "array", "items": { "$ref": "types#/$defs/tag" }, "uniqueItems": true },
        "extra":        { "$ref": "types#/$defs/extra" } } },
    "edge": { "type": "object", "required": ["task", "blocker"], "properties": {
      "task":    { "$ref": "types#/$defs/id", "description": "The task that waited." },
      "blocker": { "$ref": "types#/$defs/id", "description": "The task it waited on." } } }
  }
}
```

### marker.json

`.koan/marker.json`, in a directory outside the tree, beside a `.gitignore` holding `*`. The ID decides; `path` is a hint; a marker whose `dir` isn't the directory holding it was copied or moved there, and binds nothing.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "marker-file",
  "type": "object",
  "required": ["schema", "id", "path", "dir"],
  "properties": {
    "schema": { "const": 1 },
    "id":     { "$ref": "types#/$defs/id", "description": "The folder bound." },
    "path":   { "allOf": [ { "$ref": "types#/$defs/path" }, { "not": { "const": "/" } } ],
                "description": "The folder's path when it was bound." },
    "dir":    { "$ref": "types#/$defs/abs-path",
                "description": "The real path of the directory holding .koan/: no empty, . or .. part, and no trailing /." }
  }
}
```

## Not covered

These are left out on purpose, since nothing outside koan reads them:

- **The pending step JSON** in the `pending` table. It is internal, and can change with any release.
- **The SQL schema**, which is in the PRD, under Storage.
- **Help text, and completion's scripts and its output for commands and options**, which are for people and are cobra's (Invocation). What a reference completes to is covered (Completing references).
- **What the koan skill teaches.** It is written against this doc, and lives with the skill.
