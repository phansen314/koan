# ftask pick spec

`ftask pick`: the interactive picker, built on [fzf](https://github.com/junegunn/fzf). Fuzzy-search tasks by title and tags, act on them in place, and emit the ones chosen as JSON. It is the way a person works with ftask directly; agents use the other commands.

`pick` is a CLI command, specified on top of the [CLI spec](cli-spec.md) and the [operations](operations.md). It runs no operation of its own. It composes [`list`](operations.md#list) for what it shows with the write operations its keys run, each as its own call. Everything the CLI spec says holds for `pick` except where this document says otherwise. Those places are collected in [Departures from the CLI spec](#departures-from-the-cli-spec).

## Goals

- **Fuzzy search by title and tags** over the tree, with the task's details and notes in a preview.
- **Act without leaving.** Complete, edit, create, block, move, reprioritize and retag, then see the list reload.
- **A pipeline citizen.** Candidates can come from upstream (`ftask list … | ftask pick --from -`). The selection goes downstream as one [envelope](operations.md#output-envelope) (`ftask pick | jq …`). The interface draws on the terminal, never on stdin or stdout, so both can be redirected.
- **Nothing hidden from a caller.** Every change made inside the picker is reported in the output, so a script or an agent that hands the terminal to a person learns what the person changed.

## Non-goals

- **Agents.** `pick` is for a person at a terminal. Without one, it fails whenever it would show the picker (see [Errors](#errors)). The [ftask skill](claude/skills/ftask/SKILL.md) tells agents never to run it (see [Shipping](#shipping)).
- **Deleting.** No key deletes a task or a folder. Deletes stay with `ftask delete` and `ftask delete-folder`, which agents' permission rules make ask first.
- **A configurable keymap.** The keymap is fixed and documented here. fzf's own options restyle the picker (see [fzf options](#fzf-options)).
- **A tree view.** Folders are a column and a narrowing step, not a nested display. That is the design spec's [Tree view](design-spec.md#tree-view).
- **Its own fuzzy matcher.** Matching, ranking and the screen are fzf's.

## Requirements

- **A terminal.** `/dev/tty` must open for reading and writing, whenever the picker is shown. stdin and stdout may be anything. A run that [selects at once](#selecting-at-once) shows nothing and needs no terminal.
- **fzf**, always, found on `PATH`, version 0.63.0 or later: the first release with the footer that holds the [status line](#status-line), the newest fzf feature `pick` uses. Its version is checked with `fzf --version` before fzf is started: the first whitespace-separated word of the output (e.g. `0.74.4` in `0.74.4 (Fedora)`), without any suffix from the first `-` (`0.75.0-dev` is `0.75.0`), compared as three numbers. Output that doesn't parse that way is `fzf-too-old`, with `found` the first line as printed. `fzf --version` runs without `FZF_DEFAULT_OPTS` and `FZF_DEFAULT_OPTS_FILE` in its environment: a bad option in either makes it exit `2` with nothing on stdout, which would read as a version too old. A bad option is reported where fzf really starts, by the [`--filter` match](#selecting-at-once) or the picker, as `fzf-failed`. If `fzf --version` itself exits non-zero, that is `fzf-failed` too. The minimum is raised only deliberately, in a release that says so.
- **Optional:** `glow` or `bat` on `PATH` renders notes in the preview (see [Preview](#preview)).

## Command

Fuzzy-pick tasks, or with `--folders` folders, and write the selection as one envelope. Runs [`list`](operations.md#list) to load and reload the candidates, and the write operations of the [actions](#actions).

**Synopsis:** `ftask pick [--folder <path>] [--recursive=false] [--scope <scope>] [--tags-any <tags>] [--tags-all <tags>] [--ids <ids> | --from <file> | --source <command>] [--query <text>] [--select-one] [--exit-zero] [--fields <names>]`, `ftask pick --folders [--folder <path>] [--recursive=false] [--query <text>] [--select-one] [--exit-zero]`, or `ftask pick -i <file>`.

**Operation:** none of its own. Each load runs `list` once, over the whole tree (see [Session](#session)), and each action runs one write operation per target task. No write lock is held between them, and none is held while the person looks at the list.

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--folder <path>` | `/folder` | `/`. An exact [folder path](cli-spec.md#command-line). The initial scope folder. The [`f` action](#actions) changes it during the session. |
| `--recursive` | `/recursive` | `true`. `--recursive=false` leaves out tasks in `folder`'s subfolders. In the [folder picker](#folder-picker), it lists `folder` and its immediate subfolders only, as `list`'s `include_folders` does without recursion. |
| `--scope <scope>` | `/scope` | `open`, or `all` with `--ids`, `--from` or `--source` (see [Candidates](#candidates)). Which tasks show at first: `ready`, `open` (ready and blocked), or `all` (complete too). The [`s` action](#actions) cycles it. |
| `--tags-any <tags>` | `/tags_any` | None. Comma list. As for [`list`](cli-spec.md#list). |
| `--tags-all <tags>` | `/tags_all` | None. Comma list. As for `list`. |
| `--ids <id,id,…>` | `/ids` | None. Only these tasks are candidates: a [snapshot](#candidates). |
| `--from <file>` | `/ids` | —. Reads an envelope from `<file>` (`-` is stdin) and takes its tasks' IDs as `ids` (see Input). |
| `--source <command>` | `/source` | None. A shell command whose output gives the candidates, run again on every reload: a [live source](#candidates). |
| `--query <text>` | `/query` | `""`. The initial search text. |
| `--select-one` | `/select_one` | `false`. If `query` matches exactly one candidate, emit it without showing the picker. |
| `--exit-zero` | `/exit_zero` | `false`. If `query` matches no candidate, emit an empty selection without showing the picker. |
| `--fields <names>` | `/fields` | None: whole task views. Comma list of task view field names, `id` always included. Shapes only the emitted `tasks`, as for `list`. |
| `--folders` | `/folders` | `false`. Pick folders instead of tasks: the [folder picker](#folder-picker). |

`--ids` and `--from` both set `/ids`, so giving both is a [usage error](cli-spec.md#usage-errors). `--source` with either can be built, and is refused by the input schema as `invalid-input`, the same as through `-i` (see the CLI spec's *The CLI rejects only what it cannot build*). With `--folders`, only `--folder`, `--recursive`, `--query`, `--select-one` and `--exit-zero` apply. Any other field is `invalid-input`.

**Input:** pick's input schema, for `-i` and as the target of the options above:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "pick-input",
  "type": "object",
  "properties": {
    "folder": { "$ref": "folder-path", "default": "/" },
    "recursive": { "type": "boolean", "default": true },
    "scope": { "type": "string", "enum": ["ready", "open", "all"], "description": "Default open; all with ids or source." },
    "tags_any": { "$ref": "frontier-input#/properties/tags_any" },
    "tags_all": { "$ref": "frontier-input#/properties/tags_all" },
    "ids": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Only these tasks are candidates. May be empty." },
    "source": { "type": "string", "minLength": 1, "description": "A shell command whose output gives the candidates. Not with ids." },
    "query": { "type": "string", "default": "" },
    "select_one": { "type": "boolean", "default": false },
    "exit_zero": { "type": "boolean", "default": false },
    "fields": { "$ref": "frontier-input#/properties/fields" },
    "folders": { "type": "boolean", "default": false }
  },
  "additionalProperties": false,
  "not": { "required": ["ids", "source"] }
}
```

`--from` resolves to `ids`, as `create`'s `--notes-file` resolves to `notes`:

- <a id="accepted-envelopes"></a>**Accepted envelopes.** The file holds one envelope with `ok: true` whose `result` has either `tasks`, an array of objects each with an `id` (from `list`, `frontier`, `show` or `pick`), or an `id` of its own (from `create`, `complete` and the other single-task commands). Any `--fields` upstream will do, since `id` is always included. The IDs are taken in order, without duplicates.
- **Read in full first.** The whole file is read before fzf starts, so `--from -` never competes with the terminal.
- **Bad content** is `invalid-input` at `/ids`: not one JSON value, not an envelope, no tasks or ID in it, or an envelope with `ok: false`. The last says which error kind upstream reported. The upstream command has already written its own stderr line.
- **An empty `tasks` array** is fine. The picker opens with no candidates.

**Output:** one envelope, written after fzf has exited. See [Output](#output).

**Errors:** see [Errors](#errors).

**Composition:** each action's operation is its own call, with its own lock, so the session is never all-or-nothing:

- **A failure** of one action leaves the others, before and after it, in effect. Each is in `actions` with its own envelope.
- **A crash** of `pick` (a signal other than those it catches, or `kill -9`) leaves every action already run in effect, with no envelope to report them. The session directory may be left behind with its action log, but it is not a recovery interface: what changed is found as after any crash, by reading the tree.
- **Retry safety:** rerunning `pick` is safe. Until a person acts, it only reads. An action interrupted by a crash follows its operation's Retry safety, e.g. a `create` that may have happened is checked for before being repeated.

**Examples:**

```sh
ftask pick                                                  # open tasks; Enter → .result.tasks
ftask pick --folder /work --scope ready                     # what's ready under /work
ftask pick | jq -r '.result.tasks[].id'                     # the IDs picked
ftask pick --fields id,title,notes_path | jq -r '.result.tasks[].notes_path' | xargs -r -o "$EDITOR"   # -o: the editor gets the terminal
ftask list --readiness blocked --fields id | ftask pick --from -   # choose among the blocked ones
ftask frontier --tags-any today | ftask pick --from -
ftask pick --source 'ftask frontier --tags-any today'       # the same, kept live
ftask pick --source "ftask list | jq -c '.result.tasks |= map(select(.extra.status == \"waiting\"))'"
ftask pick --ids 41,42,43
ftask complete "$(ftask pick --query 'renew pass' --select-one | jq -r '.result.tasks[0].id')"   # no picker if only one matches
ftask pick --folders | jq -r '.result.folders[0]'           # a folder path, e.g. for create --folder
ftask pick > picked.json; jq '.result | {actions, notes_edited}' picked.json   # what the session changed
ftask pick | ftask pick --from -                            # narrow in two passes
```

## Candidates

Which tasks the picker lists, its **candidates**, comes from one of three places. They differ in whether the list's membership can change during the session:

| Candidates from | Membership | Scope default |
|---|---|---|
| The tree, through `pick`'s own `--folder`, `--recursive`, `--tags-any` and `--tags-all` | **Live**: every reload runs `list` again. | `open` |
| `--ids` or `--from`: a **snapshot** | **Frozen**: the IDs given, and no others, for the whole session. | `all` |
| `--source <command>`: a **live source** | **Live**: every reload runs the command again. | `all` |

- **Task data is always live.** Every reload reads the tree again, whatever the candidates came from, so titles, readiness and completion are always current. Only *which* tasks are listed can be frozen.
- **A snapshot never grows.** A task created in the session, a task unblocked by an action, or a task created by another process is not added to it, even if upstream would have listed it. The emitted selection is therefore always a subset of the IDs given, which is what a pipeline downstream of `--from` can rely on. For a list that keeps up, use a live source.
- **Narrowing applies on top.** With a snapshot or a live source, `pick`'s scope, folder and tag filters still narrow the list. The scope defaults to `all`, so by default the picker shows exactly what upstream chose: `ftask list --readiness complete | ftask pick --from -` lists the complete tasks. `s` cycles the scope as usual.
- **Order** is always `pick`'s own (see [Lines](#lines)), not upstream's.

### Live source

`--source` runs a command for the first load and again on every reload, and takes the candidates from its output:

- **Run with `sh -c`**, in `pick`'s working directory and environment, with stdin from `/dev/null`. It is the user's own command, trusted as fzf's callbacks are.
- **Its output** must be one of the [accepted envelopes](#accepted-envelopes), as for `--from`: from `list` or `frontier`, perhaps through `jq`, or anything else that prints an ftask envelope with tasks. Its IDs are the candidates.
- **Its stderr** is captured, not passed to the terminal, where it would garble the picker.
- **Synchronous, with a limit.** A reload waits for the command, and the picker doesn't respond meanwhile, not even to ctrl-c, which fzf queues until the callback returns. A later run that takes more than 10 seconds is killed, with its process group, and fails as `✗ source: timed out after 10s`; the list stays as it was. The first run has no limit: it runs before fzf opens, where ctrl-c still interrupts it.
- **The first run's failures** end `pick` before fzf opens, as `invalid-input` (`field`: `/source`): output that is not an accepted envelope, and an `ok: false` envelope, exactly as for `--from` (see [Accepted envelopes](#accepted-envelopes)). The `reason` gives the upstream error's kind and message, or else the first line of the command's stderr. The source's own kind is never passed through: a `usage` from a mistyped command would make `pick` exit `2`, saying that `pick`'s own command line was wrong.
- **Later runs' failures** show in the status line, as `✗ source: …`, and the list stays as it was.

## Selecting at once

`--select-one` and `--exit-zero` let `pick` finish without showing the picker, as fzf's `--select-1` and `--exit-0` do:

- **Matched first, headlessly.** After the first load, `pick` matches `query` against the candidate lines with `fzf --filter`, with the options the picker gets, in the same order: `FZF_DEFAULT_OPTS`, `pick`'s own (so only title and tags are matched), then `FTASK_PICK_OPTS`. So `--exact` or `--ignore-case` in the person's options applies to both. `fzf --filter` exits `0` with matches and `1` with none, which is the `--exit-zero` case, not a failure. Any other status is `unavailable` (`fzf-failed`). The selection is recorded by Enter's callback (see [fzf contract](#fzf-contract)), and fzf's own `--select-1` skips it, so `pick` makes the decision itself.
- **One match** with `--select-one`: emit that candidate, exactly as if it were picked with Enter.
- **No match** with `--exit-zero`: emit an empty selection, as a quit does.
- **Otherwise** the picker opens as usual, with `query` already typed.
- **No terminal is needed** when the picker isn't shown. The [`unavailable`](#errors) check for `/dev/tty` is made only when it is about to be. fzf is still needed, for the match.
- An empty `query` matches every candidate, so `--select-one` picks the only task when there is one.

## Display

### Lines

One line per candidate task, in this order:

1. **Ready** tasks, in [frontier order](operations.md#frontier): priority highest first, unprioritized last, then ID lowest first.
2. **Blocked** tasks, in the same order.
3. **Complete** tasks (scope `all` only), `completed_at` newest first, then ID.

With an empty query, fzf shows this order. As the person types, fzf ranks by match, and ties keep this order (`--tiebreak=index`).

Each line has these columns, the title last:

```text
●  42  p2   /trips/japan  #travel   Book flights
◐  43  →42  /trips/japan  #travel   Book hotel
✓  12       /trips                  Renew passport
```

| Column | Content |
|---|---|
| State | `●` ready, `◐` blocked, `✓` complete. |
| ID | The task's ID. |
| Detail | For a blocked task, `→` and its `blocking` IDs. Otherwise `p` and the priority, or nothing. |
| Folder | The folder path. |
| Tags | Each tag as `#tag`, space-separated. |
| Title | The title, whole. Titles hold no control characters, so a line is always one line. |

- **The title goes last** so that a long one never pushes the other columns off-screen. Only its end can be cut off, by the terminal's width.
- **Alignment.** Every column but the title is padded to the widest value in the current list, measured in terminal cells, not bytes or code points (wide characters take two). The detail and folder columns are capped at 24 cells, and a longer value is cut with `…`. Neither is matched, so cutting them never changes what the query finds. The tags and the title are never cut: fzf matches only text it displays (`--nth` counts the fields `--with-nth` shows), so a cut tag would stop matching.

- **Only the title and tags are matched.** The query never matches the ID, the folder or the state. Narrowing by folder is the [`f` action](#actions). (fzf's `--nth`.)
- **Color.** Blocked and complete lines are dimmed, and tags and folder are muted. With `NO_COLOR` set to a non-empty value, lines carry no color.
- **Duplicate IDs** show as one line per copy. The preview, marks and the selection follow the copy, by its [key](#line-keys). An action names the ID, so on a duplicated ID it fails with `conflict` (`rule`: `duplicate-id`), shown in the [status line](#status-line).

### Header, prompt and status line

- **Prompt** names the scope: `open> `. In a [prompt](#modes), it names the value being asked for (e.g. `priority 42> `).
- **Command mode hides the input line** (fzf's `hide-input`). That is the only way fzf drops typed keys: a key with no binding is otherwise typed into the query. The query is kept and still filters the list, and it reappears with insert mode.
- **Header** shows the [mode](#modes) when it isn't insert (e.g. `[cmd] query: renew`, since command mode hides the query), the scope folder, the filters in effect, and a one-line key hint for the current mode.
- <a id="status-line"></a>**Status line** (fzf's footer) is one line, showing the result of the last action until the next one, then, after ` · `, `N warnings` (`1 warning` for one) if the last load reported any:
  - one target: `✓ completed 42`, `✓ created 51`, or `✗ block 43 ← 7: conflict (acyclic): blocker(s) 7 would create a cycle`. A failure shows its error kind and its `message`.
  - several: outcomes grouped, successes first, with at most five IDs per group, then `+N`: `✓ completed 38: 41, 42, 44, 45, 47 +33 · ✗ 2 failed: 43 busy, 46 conflict (duplicate-id)`.
  - Too long for the terminal, it is cut with `…`. Every outcome is in `actions` in the output, whatever the status line shows.

### Preview

The preview pane shows the task under the cursor:

```text
#42 Book flights                       ready  p2
/trips/japan   #travel   created 2026-09-20   updated 2026-09-21
blocked by: —          blocks: 43
extra: status = waiting on quote
── notes ─────────────────────────────────────
Prefer ANA, aisle seat…
```

- **`blocks`** lists the open tasks whose `blocked_by` names this one. ftask stores no such list, so `pick` derives it from the [load](#session), which covers the whole tree, not from the scope.
- **`extra`** shows one `key = value` per key, each value as compact JSON, except that a string is shown bare.
- **Notes** are rendered with `glow` if it is on `PATH`, otherwise with `bat` (as Markdown, with color), otherwise as plain text. An empty or missing `.md` shows `(no notes)`.
- ctrl-/ toggles the pane in every mode.

## Modes

The picker always has one of four modes. Typing reaches the query only in insert mode and in the two modes that ask for input.

| Mode | Typing does | Enter | Esc |
|---|---|---|---|
| **insert** | Edits the query and filters the list. | Emits the selection and exits. | Enters command mode. |
| **command** | Runs [actions](#actions). Other keys do nothing: the input line is hidden. | Emits the selection and exits. | Quits: emits an empty selection and exits. |
| **prompt** | Edits a value (e.g. a priority) in the query line, in place of the search query, which comes back afterwards. Search is off, so the list stays put. | Applies the action, then back to command mode. | Cancels, back to command mode. |
| **choose** | Filters a second list, such as candidate blockers or folders. | Applies the action to the chosen items, then back to command mode with the task list. | Cancels, back to command mode with the task list. |

- **Start** in insert mode, with the query `--query` sets.
- **ctrl-space** enters command mode from insert mode, like Esc. In a prompt or choose list it cancels, like Esc. In command mode it stays there: unlike Esc, it never quits.
- **`i` or `/`** in command mode returns to insert mode, with the query kept.
- **Command mode is sticky.** Each action returns to it, so several actions in a row need no prefix.
- **Prompts borrow the query line.** On entering prompt mode, the helper saves the search query in the session and disables search. On leaving, by apply or cancel, it restores the query (`transform-query`, see [No data in action text](#fzf-contract)) and enables search again. The one exception is a successful `n`, which clears it (see [Actions](#actions)).
- **ctrl-c** in any mode [cancels](#output): exits at once, with error kind `cancelled`. So do fzf's other abort keys (ctrl-g, ctrl-q).
- **ctrl-d** only deletes the character under the cursor. fzf's default, `delete-char/eof`, also aborts on an empty query, which in a prompt would cancel the whole session, so `pick` binds it to `delete-char` in every mode.
- **Marks.** Tab (and, in command mode, space) marks or unmarks the line under the cursor, in insert, command and choose modes. Marks are cleared after every action, whether or not it ran an operation or its reload went through, except one refused for its number of targets (`✗ x takes one task: 3 marked`), which keeps them to be fixed; on every switch between the task list and a choose list, and on every reload. Each of these clears explicitly, even where fzf's reload would: fzf keeps marks across a reload when the person's options include `--track --id-nth`.

## Actions

An action works on its **targets**: the marked tasks, or, with none marked, the task under the cursor. With no task under the cursor and none marked, it does nothing.

Command mode's keys:

| Key | Action | Runs | Targets |
|---|---|---|---|
| `c` | Complete or reopen, by the readiness the lines show. If any target is shown open, it completes every target. If every target is shown complete, it reopens them all. A task completed or reopened elsewhere since the last load makes its call a no-op (`changed: false`), never a reversal. | [`complete`](operations.md#complete), [`reopen`](operations.md#reopen) | any |
| `e` | Edit notes: opens the targets' `notes_path`, all as arguments to one editor, with fzf suspended. `$VISUAL`, else `$EDITOR`, else `vi`. Each `notes_path` is read fresh by ID as `e` runs, not taken from the last load, so a task moved meanwhile has its notes edited where they now are; a target deleted meanwhile refuses the whole `e`, as does one now duplicated with no copy in its line's folder. | [`show`](operations.md#show), per target, for its current `notes_path`. ftask never sees notes edits; `pick` reports which notes changed in [`notes_edited`](#output). | any |
| `n` | New task. Prompt `new> `, filled with the query. Creates an open task with that title in the scope folder (see below). | [`create`](operations.md#create) | none |
| `b` | Block. Choose list `blockers of 42> ` (see below). Adds the chosen tasks to each target's `blocked_by`. | [`block`](operations.md#block) | any |
| `u` | Unblock. Choose list `unblock 42> ` of the target's `blocked_by`, missing IDs included: such a line says `(no such task)`, and its preview `no task with ID 50`. Removes the chosen ones. | [`list`](operations.md#list), then [`unblock`](operations.md#unblock) | one |
| `m` | Move. Choose list `move to> ` of every folder. Moves the targets there. | [`move`](operations.md#move) | any |
| `x` | Edit as JSON: opens the target's `title`, `priority`, `tags` and `extra` in `$VISUAL`, else `$EDITOR`, else `vi`, with fzf suspended, and applies what changed (see below). | `show`, then `update` | one |
| `p` | Priority. Prompt `priority 42> `, filled with the current priority for one target, empty for several (see below). An integer sets it; `null` clears it. | [`update`](operations.md#update) | any |
| `t` | Tags. Prompt `tags 42> `, filled with the current tags for one target, empty for several (see below). | `update` | any |
| `s` | Scope: cycles `ready` → `open` → `all` → `ready` and reloads. | `list` | none |
| `f` | Folder. Choose list `folder> ` of every folder, `/` first. Sets the scope folder and reloads. | `list` | none |
| `r` | Reload. | `list` | none |
| `j` / `k` | Down / up. | — | — |
| `g` / `G` | First / last line. | — | — |
| `?` | Shows this table in the preview pane until the cursor moves. | — | — |
| `q` | Quit, as Esc. | — | — |
| `i`, `/` | Insert mode. | — | — |

- **One call per target.** An action on several targets runs its operation once per target, in `pick`'s [line order](#lines) from the last load, not in the order fzf reports marks (the order they were marked), nor a query's rank order. Each call stands alone: one that fails doesn't stop the rest, and the status line reports every outcome. A `busy` is reported like any other failure, never retried silently.
- **Wrong number of targets.** `u` and `x` take one target. With several marked, they run nothing and say so in the status line (`✗ x takes one task: 3 marked`). The `m` and `f` choose lists take one folder: Tab and space are unbound in them, and Enter takes the folder under the cursor. `b`'s and `u`'s choose lists allow several.
- **Reload after every action** that runs an operation, so the list, the readiness and the preview reflect it. A task that falls out of scope (e.g. completed while the scope is `open`) leaves the list. The status line still names it.
- **Blocker candidates.** `b`'s choose list holds every open task in the tree, regardless of the scope, except the targets and every task from which a target can be reached by following `blocked_by`, in the [load](#session)'s graph: keyed by ID, with every copy's edges, complete tasks included, since `block`'s cycle check follows them too. Those would close a cycle. `block` still checks: a cycle created concurrently is refused with `conflict` (`acyclic`).
- **Tags syntax.** The value is a list of tags, separated by spaces or commas. If every item is bare (`travel urgent`), they replace all tags (`update`'s `tags.replace_all`). If every item is prefixed (`+urgent -later`), `+` adds and `-` removes. A mix is refused in the status line. A lone `-` clears all tags (`tags.replace_all: []`).
- **Several targets.** With one target, the `p` and `t` prompts start with its current value, and an empty value clears it, as the prompt showed what is being cleared. With several, they start empty, and an empty value does nothing (status `no change`): it never clears every target. Clearing then takes `null` (priority) or `-` (tags). A value of only separators (spaces, commas) is empty. An integer is sent in its plain form, so `+5` and `05` set `5`.
- **New tasks** get the scope folder and the scope's `tags_all`, and no priority or blockers. With pick's own filters, that makes a new task a candidate unless `--tags-any` is given, which names no tag a new task could be given without guessing.
- **A new task is listed only if it is a candidate.** `pick` never adds a task to the list because it created it. After a successful `n`, the search query is cleared, since it has become the new task's title. The list is then in `pick`'s own order, and the helper knows the new task's position in it. The cursor goes there through fzf's `load` event. fzf can only re-enable a binding, not add one, so `load` is bound from the start to `transform(ftask __pick on-load)` and unbound at `start`. A successful `n` records the position in the session and returns `rebind(load)+reload-sync(…)`: `load` is armed before the reload starts, since in fzf 0.63.0 a quick reload can fire it before a rebind later in the chain takes effect. `on-load` returns `pos(N)+unbind(load)` and clears the record, or only `unbind(load)` when there is none: fzf can fire `load` for the first list before `start:unbind(load)` takes effect, so the binding may run once unarmed. (A `pos` chained after `reload-sync` would run before the new list arrives.) If the new task isn't listed, the status line says so (`✓ created 51 (not in this list)`), and it is reported in `actions` like any other. This happens with `--tags-any`, with a [snapshot](#candidates), which never grows, and with a [live source](#live-source) whose command doesn't return it.
- **Editing as JSON.** `x` writes the target's editable fields to a temp file in the session, as a pretty-printed JSON object, and opens it:
  ```json
  {
    "title": "Book flights",
    "priority": 2,
    "tags": ["travel"],
    "extra": {
      "status": "waiting on quote"
    }
  }
  ```
  On exit, `pick` compares the file with what it wrote, and runs one `update` with only what changed: a new `title` or `priority`; `tags.replace_all` if the tags differ as sets; `extra.merge` for keys added or changed and `extra.remove` for keys removed.
  - **Unchanged** (or unsaved): no operation, and the status line says so.
  - **Not valid**: not one JSON object, or keys other than those four. Nothing is applied, the status line says what is wrong, and the file is kept: the next `x` on the same task reopens it with the edits, instead of starting over. Any other action or a reload discards it.
  - **Refused or busy**: if `update` fails with `invalid-input`, or with `busy` because another write holds the tree, the file is kept the same way, to be fixed or tried again. Any other failure discards it.
  - **Changed elsewhere meanwhile**: only the fields edited are sent, so a concurrent change to another field survives. A concurrent change to the same field is overwritten: `update` has no compare-and-swap (see [Concurrent updates](#concurrent-updates)).
  - Notes are not in the file; they are `e`.
- **Values are checked by the operation.** A bad priority or a bad tag fails as the operation's `invalid-input`, shown in the status line, and the prompt stays open with the value kept, to be fixed or cancelled.

## Folder picker

`ftask pick --folders` lists the folders in scope, in [tree order](operations.md#tree-order), and emits the ones chosen in `result.folders`. As for tasks, the selection is checked against a final read: a chosen folder that no longer exists, e.g. moved or deleted by another process meanwhile, is put in `result.missing` instead. It has insert and command modes, with movement, marking, `?`, Enter, Esc, `q` and ctrl-c as above, and no actions. The `f` and `m` choose lists use the same display.

## Output

`pick` writes one envelope to stdout after fzf exits, following the CLI spec's [Output](cli-spec.md#output) rules: compact, one line, delivered before exit.

- **Enter** emits the selection: the marked tasks or folders, or, with none marked, the one under the cursor. With an empty list, the selection is empty. `ok: true`, exit `0`.
- **Esc or `q` in command mode** quits: `ok: true` with an empty selection, exit `0`.
- **ctrl-c** (and fzf's other abort keys) cancels: `ok: false`, kind `cancelled`, exit `1`.

In every case, the envelope reports the actions taken.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "pick-output",
  "oneOf": [
    {
      "type": "object",
      "required": ["tasks", "missing", "actions", "notes_edited"],
      "properties": {
        "tasks": {
          "type": "array",
          "items": { "anyOf": [{ "$ref": "task-view" }, { "$ref": "task-projection" }] },
          "description": "The selected tasks, from the final read, in pick's line order. Task views, or Task projections with fields. May be empty."
        },
        "missing": {
          "type": "array",
          "items": { "$ref": "task-file#/properties/id" },
          "description": "Selected IDs the final read did not find: deleted by another process meanwhile, or, for a duplicated ID, no copy left in the selected copy's folder. Usually empty."
        },
        "actions": { "$ref": "#/$defs/actions" },
        "notes_edited": {
          "type": "array",
          "items": { "$ref": "task-file#/properties/id" },
          "description": "IDs whose notes file changed during an e, judged by its content before and after the editor ran. In the order first edited, without duplicates. Usually empty."
        }
      },
      "additionalProperties": false
    },
    {
      "type": "object",
      "required": ["folders", "missing", "actions"],
      "properties": {
        "folders": { "type": "array", "items": { "$ref": "folder-path" }, "description": "The selected folders that the final read found, in tree order. May be empty." },
        "missing": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Selected folders the final read did not find. Usually empty." },
        "actions": { "$ref": "#/$defs/actions", "description": "Always empty: the folder picker has no actions." }
      },
      "additionalProperties": false
    }
  ],
  "$defs": {
    "actions": {
      "type": "array",
      "description": "Every operation the session's actions ran, in the order run. Loads (list) are not included; notes edits are in notes_edited.",
      "items": {
        "type": "object",
        "required": ["operation", "input", "output"],
        "properties": {
          "operation": { "type": "string", "enum": ["complete", "reopen", "create", "block", "unblock", "move", "update"] },
          "input": { "type": "object", "description": "The operation's input, as passed." },
          "output": { "oneOf": [{ "$ref": "envelope" }, { "type": "null" }], "description": "The operation's envelope, unchanged: success or failure. null if the outcome is unknown (see Session)." }
        },
        "additionalProperties": false
      }
    }
  }
}
```

- **The selection is read fresh.** After fzf exits, `pick` runs one final `list` over the whole tree, as a [load](#session) does but without folders. It reads whole Task views and projects them to `fields` afterwards. For each selected [key](#line-keys) it emits the one task with that ID, wherever it now is; if the ID now has several copies, the copy in the key's folder; otherwise it puts the ID in `missing`. So a selected task completed, moved or retagged since is still emitted, as it now is. The emitted tasks are not what the screen last showed.
- **Order** is `pick`'s [line order](#lines), applied to the final read: not the order fzf reports marks in (the order they were marked), and not a query's rank order.
- **Actions are reported whether they succeeded or failed.** `jq '.result.actions[] | select(.output.ok | not)'` finds the failures.
- **Warnings** are the final read's, over the whole tree. An action's own warnings stay in its `output`.

## Errors

`pick` adds three CLI-only error kinds, like [`usage`](cli-spec.md#usage-errors). No operation raises them, and callers treat any kind they don't know as a generic failure.

| Kind | When | `details` |
|---|---|---|
| `unavailable` | The picker cannot run: no terminal, or no usable fzf. fzf is checked after `invalid-input`, and so after a [live source](#live-source)'s first run, whose failures are `invalid-input`, and before `pick` itself reads anything from the tree. The terminal is checked when the picker is about to be shown, after the first load (see [Selecting at once](#selecting-at-once)). | `reason`: `no-terminal` (`/dev/tty` doesn't open), `fzf-missing` (not on `PATH`), `fzf-too-old`, or `fzf-failed` (fzf exited with an error, e.g. a bad option in `FZF_DEFAULT_OPTS`, or `fzf --version` or `fzf --filter` did, or fzf couldn't be started or was killed, or `FTASK_PICK_OPTS` doesn't split into options). For `fzf-too-old`: `found` and `required`, the versions. For `fzf-failed`: `status`, fzf's exit status, if it exited; and `actions`, since fzf can fail after actions have run, except when `FTASK_PICK_OPTS` doesn't split, as fzf never ran then. |
| `cancelled` | The person cancelled with ctrl-c or another fzf abort key. | `actions`: as in the output, the operations already run. |
| `incomplete` | The session ended, but its result could not be read: the final read failed. | `actions`, and `error`: the final read's own error, whole, with its kind and details. |

**Details schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "pick-error-details",
  "$defs": {
    "unavailable": {
      "type": "object",
      "required": ["reason"],
      "properties": {
        "reason": { "type": "string", "enum": ["no-terminal", "fzf-missing", "fzf-too-old", "fzf-failed"] },
        "found": { "type": "string", "description": "For fzf-too-old: the version found." },
        "required": { "type": "string", "description": "For fzf-too-old: the minimum version." },
        "status": { "type": "integer", "description": "For fzf-failed: fzf's exit status." },
        "actions": { "$ref": "pick-output#/$defs/actions", "description": "For fzf-failed: the operations already run." }
      },
      "additionalProperties": false
    },
    "cancelled": {
      "type": "object",
      "required": ["actions"],
      "properties": { "actions": { "$ref": "pick-output#/$defs/actions" } },
      "additionalProperties": false
    },
    "incomplete": {
      "type": "object",
      "required": ["actions", "error"],
      "properties": {
        "actions": { "$ref": "pick-output#/$defs/actions" },
        "error": { "$ref": "error", "description": "The final read's error, unchanged." }
      },
      "additionalProperties": false
    }
  }
}
```

Otherwise:

- **The first load's errors** pass through (`pick`'s own `list`, not a `--source` command's; see [Live source](#live-source)): if the first `list` fails (e.g. `not-initialized`, or a `--folder` that doesn't exist), `pick` writes that envelope, with the operation's own kind and details, and never opens fzf.
- **Later loads' errors** (e.g. the scope folder deleted by another process) show in the status line. The list stays as it was, and so does the scope: an `s` or `f` whose load fails changes nothing.
- **Actions' errors** never end the picker. They show in the status line and are reported in `actions`.
- **A failure after the session.** If the final read fails after fzf exits (e.g. the root became unusable), `pick` reports `incomplete`, with that read's error in `details.error`. The error is wrapped, not passed through: each operation error kind's `details` is closed, with no room for `actions`.
- **`.error.details.actions`** is present in `cancelled`, `incomplete`, and `unavailable` (`fzf-failed`), except a `fzf-failed` for an `FTASK_PICK_OPTS` that doesn't split, which comes before fzf runs. It is `[]` when no action ran, e.g. when `fzf --version` or `fzf --filter` failed. A caller that finds it knows what the session changed, whatever the kind.

**stderr** follows the CLI spec's [one-line rule](cli-spec.md#output), with one addition: fzf's own stderr (e.g. its message about a bad option) passes through to the terminal.

**Exit codes** are the CLI spec's: `0` for `ok: true`, `1` for `cancelled`, `unavailable`, `incomplete` and other errors, `2` for usage errors.

**Signals.** In the picker, ctrl-c is a key, not a signal: it cancels with an envelope. While fzf runs, `pick` also catches SIGINT and SIGQUIT and discards them. When fzf is suspended for an editor (`e`, `x`), the terminal is in cooked mode, and ctrl-c sends SIGINT to the whole foreground process group, `pick` included. The editor handles it as it does, and `pick` carries on. The signals are caught, not ignored: an ignored signal stays ignored in every program fzf starts, so ctrl-c would stop working in the editor and in `--source`. Default handling returns once fzf exits, before the final read. Any other signal (SIGTERM, SIGHUP, `kill -9`), or SIGINT outside the session (e.g. during the first load), is a crash, as the CLI spec's [Exit codes](cli-spec.md#exit-codes) say, and the actions already run stay in effect with no report.

## fzf contract

How `pick` drives fzf. This section is normative for behavior. The option spellings are illustrative.

- **One fzf process per session.** Modes, prompts and choose lists all switch inside it with `reload-sync`, `rebind`/`unbind`, `enable-search`/`disable-search`, `hide-input`/`show-input`, `transform-prompt`, `transform-query` and `transform-footer`. No nested fzf.
- **The terminal.** fzf draws on `/dev/tty`. `pick` gives fzf the first candidate lines on stdin, and sends fzf's stdout to `/dev/null`. The picker's selection never comes from fzf's output. [Selecting at once](#selecting-at-once) does read `fzf --filter`'s output, so options that change what fzf reads or writes are undone, as are those that end fzf without a callback (see [fzf options](#fzf-options)).
- **Callbacks.** Every key that does more than move or mark is bound to `transform(…)` calling back into the ftask binary (its own absolute path, from `os.Executable`) through an internal helper (see [Session](#session)). The helper does the work and prints the fzf actions to take next, e.g. `reload-sync(…)+transform-footer(…)+rebind(…)`.
- **What runs concurrently.** fzf runs `transform` synchronously, so transforms never overlap one another. The commands they start may overlap them: the preview runs on every cursor move, and a reload's command runs while the next key is handled. So reloads are `reload-sync`, which changes the list, marks and cursor only once the new list is complete. A key handled meanwhile passes the lines fzf still shows: a task line the last load dropped is still acted on as shown, but the lines of a choose list being left, or of the task list a choose list is replacing, do nothing, and the status line says `✗ list still loading: nothing done`. The input is hidden on leaving a prompt or choose list only if command mode is still the mode once the new list is in, since a key handled meanwhile (`i`, or one opening a prompt) may have left it; every session file is written atomically, to a temp file in the session renamed over the old one; and the preview treats a missing or unreadable session file as `loading…`, never as an error.
- **No data in action text.** What the helper prints names actions only, with fixed arguments: key names, mode names, and the helper's own command lines, whose only variables are fzf placeholders such as `{+1}`. fzf parses everything a transform prints as actions, and is lenient about parentheses, so a message such as `)+execute-silent(…)+(` would run a command. Text that comes from data — a status message, an error's `message`, the query, a prompt's value, a source's stderr — is written to the session, and fzf fetches it through an action whose command output fzf shows literally: `transform-footer(ftask __pick text footer)`, `transform-query(…)`, `transform-prompt(…)`, `transform-header(…)`. The `text` verb writes one line, with control characters, U+2028 and U+2029 escaped as in the CLI's [stderr line](cli-spec.md#output).
- **Editors run through `execute`,** never inside a callback. A transform's stdout is fzf's action channel, and fzf still owns the terminal while it runs. So `e` and `x` return `execute(ftask __pick edit …)+transform(ftask __pick after-edit)`: fzf suspends itself and gives the editor the terminal, and the second callback, once the editor exits, compares hashes, applies an `x`, and returns the reload and status line.
- **The selection** is recorded by the helper: Enter's callback writes the selected lines' keys to the session, then returns `accept`, and quit's writes an empty selection, then returns `accept`.
- **The outcome** is decided from the session first, then from fzf's exit status. A recorded selection is Enter or quit, whatever the status: fzf's `accept` exits `1` when no line matches, e.g. on an empty list or a query that matches nothing. With no selection recorded, `130` is cancel, and any other status, `0` included, is `unavailable` (`fzf-failed`).
- **A shell of known syntax.** fzf runs callbacks with `--with-shell 'sh -c'`, whatever the user's `$SHELL`, and every argument is quoted for `sh`.
- <a id="line-keys"></a>**Line fields.** Each line is tab-delimited, starting with a hidden **key** that callbacks receive as `{1}` / `{+1}`. For a task the key is `<id>@<folder>` (e.g. `42@/trips/japan`): two copies of a duplicated ID are never in one folder, so the key is unique. For a folder the key is its path. A choose list's line keys are marked (`~42@/trips`, `~/trips`), so that a callback can tell them from the task list's. Only the key identifies a line, never the displayed text.

### fzf options

- **`FZF_DEFAULT_OPTS`** (and `FZF_DEFAULT_OPTS_FILE`) are honored, as fzf honors them: colors, layout, borders, history.
- **Options `pick` undoes.** After `FZF_DEFAULT_OPTS` and before `FTASK_PICK_OPTS`, `pick` passes `--no-select-1 --no-exit-0 --no-expect --no-tmux --no-read0 --no-header-lines --no-print0 --no-print-query --accept-nth ..`. The first three would end fzf without running a callback, so no selection would be recorded. `--read0` would read the candidates as one line, and `--header-lines` would make the first tasks a header that can't be chosen. `--print0`, `--print-query` and `--accept-nth` change `fzf --filter`'s output, which [Selecting at once](#selecting-at-once) reads: two matches would read as one, or the query as a match, or a line without its key. fzf has no `--no-accept-nth`; `..`, every field, restores the whole line. `--tmux` (in 0.74 an alias of `--popup`) would run fzf in a tmux or Zellij popup, a separate process that `pick`'s terminal check, environment and signal handling were not designed or tested for. `--select-1` and `--exit-0` are `pick`'s own `--select-one` and `--exit-zero`, decided by `pick` (see [Selecting at once](#selecting-at-once)).
- **`FTASK_PICK_OPTS`** is appended after `pick`'s own options, so it wins: e.g. `FTASK_PICK_OPTS='--height 60% --layout reverse'`. It is split as fzf splits `FZF_DEFAULT_OPTS`, comments included.
- **Rebinding is at your own risk.** An option that rebinds a key `pick` uses (or `--disabled`, `--no-multi`, `--with-shell`) can break the modes. `pick` does not detect that.
- **`FZF_DEFAULT_COMMAND`** is never used: `pick` supplies every list.

## Session

The state a session keeps outside fzf, in a private temp directory (mode `0700`, under `$XDG_RUNTIME_DIR` if set, else the system temp directory, made absolute if relative). It is removed when `pick` exits, including on cancel. A crash may leave it behind. It is never under the root, so it is never part of the tree.

It holds:

- the **scope**: folder, recursive, readiness scope, filters, and `ids` or `source`;
- the **mode**, and for prompt and choose modes the action and its targets;
- the **action log**: one entry per operation. Each entry is written, with `output` `null`, before the operation runs, and completed with its envelope after. An entry still `null` at the end means the helper died between the two: the operation may or may not have taken effect, and the entry is emitted as it is;
- the **selection**, once recorded;
- the **load**: the result of one `list` call per load, with `folder` `/`, `recursive`, `readiness` `["ready", "blocked", "complete"]`, `include_folders`, and no filters. `pick` derives from it the candidate lines (applying the scope folder, recursion, readiness scope and tag filters with `list`'s meanings), the preview's `blocks`, `b`'s candidates, and the `f` and `m` folder lists, so all of them agree. The status line's `N warnings` counts this read's warnings, never a `--source` command's. With a live source, a reload therefore reads the tree twice: once in the source's command, and once in this load, which supplies the data. A snapshot or source ID that the load does not find is left out of the list, and the header says how many (`2 given IDs not found`);
- the **notes hashes** of the targets of an `e`, from before the editor ran, for `notes_edited`.

**The helper** is a hidden command, `ftask __pick <verb> …`, that fzf's callbacks run. It reads the session directory from `FTASK_PICK_SESSION`, which `pick` sets in fzf's environment. It is internal: not listed in help, not part of the contract, and it may change in any release. Run with no valid session, it fails with `usage`. Inside a session, a callback's own failure (e.g. an unreadable session file) never prints an envelope, which fzf would parse as actions or list as a line: a callback whose output fzf runs shows the error in the status line instead (`✗ internal: …`), or does nothing if even that fails; a list's callback prints no lines; and one whose output fzf shows as it is, such as the preview, prints the error as one line.

## Departures from the CLI spec

Where `pick` differs from the [CLI spec](cli-spec.md)'s global rules, and why:

- **The intended user is a person** at a terminal, not an agent (CLI spec, introduction). `pick` is the one command that needs one, and fails fast without one.
- **Rendering for people.** The CLI renders nothing for people. `pick` renders lines and a preview, but only on the terminal. What it writes to stdout is still one JSON envelope.
- **Not the same everywhere.** A command's output doesn't depend on whether a terminal is attached. `pick` needs `/dev/tty`, but its stdout is the same whether stdout is a terminal, a pipe or a file.
- **An outside program.** fzf is a runtime dependency of `pick` alone. No other command needs it.
- **Three CLI-only error kinds,** `cancelled`, `unavailable` and `incomplete`, besides `usage`. They carry `actions` in `details`, and `incomplete` wraps an operation's error instead of passing it through.
- **Interrupts.** The CLI spec makes an interrupt a crash. In `pick`, ctrl-c cancels with an envelope, and SIGINT is discarded while fzf runs (see [Errors](#errors)). This also departs from the implementation spec's [Exit and signals](implementation-spec.md#exit-and-signals), which installs no SIGINT handler.
- **stderr.** The CLI spec allows ftask one stderr line. fzf's own stderr also reaches the terminal (see [Errors](#errors)), since fzf reports its own problems, such as a bad option, there.
- **stdin.** The CLI spec names the values that read stdin, `--input -` and `create`'s `--notes-file -`. `pick`'s `--from -` is a third, read in full before fzf starts.
- **Operations without passthrough.** The operations `pick` runs inside the session write nothing to stdout. Their envelopes are reported in `actions` instead.

## Testing

- **The helper is the unit.** Every action, mode switch and callback is a call to `ftask __pick` with a session directory, so it is tested without fzf: given a session and a key's arguments, check the fzf actions printed, the session after, and the operations run.
- **fzf end to end.** A smoke test drives a real fzf in a pseudo-terminal, with a scripted key sequence through fzf's `--listen` (or by writing keys to the pty). It covers each mode switch, one action of each kind, Enter, quit and cancel, and checks the envelope. It also covers:
  - **Outcomes.** Enter on an empty list, and quit with a query that matches nothing, each give `ok: true` with an empty selection. `FZF_DEFAULT_OPTS='--select-1 --exit-0 --expect=esc'` changes nothing.
  - **Hostile text.** A task title, a `--source` stderr line and an error message containing `)+execute-silent(touch X)+change-footer(` and a newline are shown literally, and `X` is never created. The text must make an action chain fzf would run if it were injected: `)+execute-silent(touch X)+(` does not, since fzf refuses a chain with the bare `(` it leaves, so a test with it passes even when the text is injected.
  - **Interrupts.** ctrl-c inside the `e` editor, followed by Enter, gives an envelope with the session's actions.
  - **A failed final read** gives `incomplete`, with the read's error and the actions.
  - **ctrl-d** on an empty prompt deletes nothing and keeps the session.
  - **The first load.** Started repeatedly, the picker always opens with the cursor on the first line, whether or not fzf ran `on-load` for the first list.
  - **`FZF_DEFAULT_OPTS='--tmux'`** inside tmux still runs fzf in the terminal.
- **Minimum version.** The end-to-end test runs against fzf 0.63.0, the minimum, as well as the current release. Only actions and options that 0.63.0 has are used, `--no-tmux` included: marks are cleared with `clear-selection`, 0.63.0's name for clearing every mark, which later releases keep as an alias of `clear-multi`. `deselect-all` is not used: it leaves marks on lines the query hides.
- **Pipelines.** `--from` with each accepted envelope shape, and the rejections; stdin and stdout redirected while the terminal is the pty.

## Shipping

Done with the implementation, not before, since they describe a command that exists:

- **The skill** gets a hard rule: never run `ftask pick`. It needs the user's terminal; when the user wants to choose for themselves, suggest they run it. An envelope from `pick` that the user hands over is read like any other, `result.actions` included: it says what they changed.
- **The README** gets a section on picking tasks yourself, with the keymap's essentials and the pipeline examples.
- **cli-spec.md** moves `pick` from Planned commands to Commands.

## Future work

### Fields at creation

A mini-language in `n`'s prompt that sets more than the title, e.g. `Book hotel #travel !2` for tags `travel` and priority 2. Left out for now: titles are otherwise taken literally, and this would mean escaping `#` and `!` in them. Meanwhile, after `n` the cursor is on the new task when it is listed, so `t`, `p` and `x` are one key away.

### Concurrent updates

A change made elsewhere to a field being edited with `x` (or `p`, `t`) is overwritten. Letting [`update`](operations.md#update) take the `updated_at` the caller last saw, and refuse with `conflict` when the task has changed since, would close that: `pick` would pass the `updated_at` it loaded, and on a conflict keep the edits and show the newer task. This is an operation change, not a `pick` one.
