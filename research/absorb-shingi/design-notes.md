# koan absorbs shingi: design notes

Oct 17, 2026

shingi's model of units fits entirely inside koan's folder tree, so koan can absorb it: one tree, one lock, one counter, and no mirror to keep in step. These notes record the argument and the design that falls out of it, for review.

## Why one store

A store of units separate from koan's folder tree is neither needed nor wanted. The key premise is shingi's own rule: the unit tree and the koan folder tree must be identical ("the working tree decides it, and its koan folder follows").

**Not needed.** Every fact shingi keeps is already in koan, derived from it, fits in folder metadata, or isn't stored data:

| shingi fact | In koan | How |
|---|---|---|
| Hierarchy, path, parent, children | already there | koan folders, the same shape by construction |
| Name rules | already there | shingi copied koan's folder-name rule |
| Start and done tasks | already there | koan tasks |
| Parent/child links, after, before | already there | `blocked_by` |
| Inner tasks | already there | koan tasks |
| Batch creation | better | one lock, one transaction, no partial across processes |
| State, outline order | derived | from tasks and `blocked_by` |
| Kind | folder metadata | `extra.kind` |
| Title and notes | folder metadata | `title` in `folder.json`; notes in `folder.md` |
| UUID id | unneeded | tasks live in the folder and move with it; markers use the folder number |
| `@41` number | folder metadata | koan's own counter, under koan's lock |
| Working material | files in the folder | plain folders and files, which koan doesn't own |
| Worktree markers | outside any tree | pointers keyed by folder number |
| Kinds file, hooks | configuration | settings, not stored units |

**Not wanted.** Because the trees must be identical, a second store adds no information. It only doubles every write across two locks in two processes, which is where shingi's partial states come from: `partial`, `orphan-task`, `missing-task`, `parent-unlinked`, `after-unlinked`, `koan-failed` as error and warning, mirrored moves, and the delete conflict. The canonical store is the one holding the transactional data and the lock: koan's.

**Premises checked.** The trees never need to differ. shingi without koan is out of scope. Different storage policies for material are handled by `.gitignore` in any folder. Material versus koan folders is settled by the `@` rule below.

## What shingi adds that koan lacks: gates

A koan-only user would fall into shingi's structure naturally (a folder per story, folders for its phases, notes beside them), but not into its discipline: a start task that keeps a folder's work off the frontier until someone decides to begin, and a done task that can't close while work is open. That discipline becomes two optional pointers in `folder.json`, not a new type of folder:

```json
{ "schema": 1, "number": 57, "title": "Research phase", "start": 58, "done": 59, "extra": { "kind": "phase" } }
```

The gates are ordinary tasks; the folder just names which two they are. A folder without `start` and `done` is a plain koan folder, exactly as today, so nobody is forced into gates.

`--gates`, one flag with one meaning: take part in the gates.

- **On a folder:** `koan create-folder /work/HOME-12345/spike --gates` creates its start and done tasks, sets the pointers, and links them to the parent's gates when the parent has them (the child's start waits on the parent's start; the parent's done waits on the child's done). In `create-batch`, `"gates": true` on a folder entry.
- **On a task:** `koan create 'Handle the backoff edge case' --folder /work/HOME-12345/implementation --gates` wires the task between that folder's gates: blocked by its start, blocking its done.
- **Inline tasks** in a `create-batch` folder entry with `"gates": true` are wired by construction, since that batch set up the gates explicitly.

**Explicit, and told when missing.** `koan create` stays literal: it puts a task where it's told and nothing else. A task created in a gated folder without `--gates` succeeds with an `ungated-task` warning, and one `koan block` fixes it. `koan doctor` reports any left over. This follows koan's rule of refusing only what's wrong from the start, and telling otherwise, and it avoids the costs automatic wiring would bring:

- A finished folder: creating a task there just warns, instead of a generic create being refused because of the folder's state.
- Prep work before starting ("estimate this story") simply omits `--gates` and isn't stuck behind the start gate.
- Moving a task never rewires it, so `koan move` carries no hidden behavior.
- Subfolders never surprise anyone: wiring is always asked for.

One rule for tasks and folders, on create and on move: warn when something enters a gated folder without joining its gates. A gated folder under an ungated parent is the normal case (every story under `/work`), so it never warns.

### What lands where

| Create or move | | Warning? |
|---|---|---|
| Gated folder into a gated parent | linked to the parent's gates; a move first unlinks it from the old parent | no |
| Gated folder into an ungated parent | no parent links; a move drops the old parent's links | no |
| Task without `--gates` into a gated folder | placed as-is | yes, `ungated-task` |
| Ungated folder into a gated parent | placed as-is | yes, `ungated-folder` |
| Anything into an ungated folder | placed as-is | no |

An ungated folder is fixed like an ungated task: give it gates (`koan update-folder --gates`, which links them to the parent's), or move it somewhere ungated.

**State is derived:** not started, started, or done, from the two gate tasks, never stored. Closing a parent stays a decision: its done gate becomes ready when its last child is done, and someone marks it.

**Gate titles are derived, never stored.** A gate's `.json` stores only the bare word `Start` or `Done`, so the file reads clearly in an editor and never goes stale. koan knows a task is a gate from its folder's pointers (no role field on the task, which would be a second copy that could disagree), and renders its title on every read from the folder's current path and its title in `folder.json`:

```text
58  Start: /work/HOME-12345/research (Research phase)
59  Done: /work/HOME-12345/research (Research phase)
61  Start: /work/HOME-99/spike                       ← the folder has no title
```

The path keeps titles unique in a flat list across stories; the folder's title adds context. Moves and title changes show up at once, with nothing to retitle. The rendered string is what `title` holds in koan's output. Rendering costs no extra reads: a walk already knows each task's folder and reads each `folder.json`, where the title lives. `koan update --title` on a gate is refused, pointing to `koan update-folder --title` or a move; filters use the role, as in `koan frontier --gates start`.

## On disk: `@` marks koan's folders

On disk, every koan folder's name starts with `@`. Any folder koan didn't create is material, automatically. Only koan writes `@` names, so an agent, editor, or build tool making an ad-hoc `docs/` can never create a koan folder by accident: the safe outcome is the default. The `@` exists on disk only; paths you type never carry it.

| Entry | Owner | koan reads it? |
|---|---|---|
| `@name/` | koan: a koan folder | yes |
| `41.json`, `41.md` | koan: a task and its notes | yes |
| `folder.json`, `folder.md` | koan: the folder's metadata and notes | yes |
| Any other folder or file | you: material | no |
| `.gitignore`, other hidden entries | you, and git | no |

A story, as a gated folder:

```text
~/koan-root/@work/@HOME-12345/
  @research/  @implementation/     koan folders (sub-units)
  docs/  logs/  diagram.png        your material
  folder.json  folder.md           the folder's metadata and notes
  41.json  41.md  42.json  42.md   its tasks, the gates among them
  .gitignore                       optional, e.g. logs/
```

- `folder.json` holds `schema`, the folder's `number`, its `title`, its gate pointers `start` and `done` when it has gates, and `extra` (kind and anything else, like a task's `extra`). `folder.md` is the folder's notes: pure prose, which koan never reads or interprets, as with a task's `.md`. Titles are data in JSON for folders and tasks alike, so notes never grow a format of their own.
- The name rule is `@` plus today's folder name rule: 65 characters on disk.
- A `.gitignore` in any folder keeps large or private material out of the tree's history. koan writes none and reads none.

## Paths and references

A reference is a folder, then optionally `:` and a path to material inside it, the way git writes `HEAD:src/main.go`. The colon can't appear in a folder name, so the split is never ambiguous.

| You write | Means | Stable? |
|---|---|---|
| `/work/HOME-12345/research` | a koan folder by path; on disk `@work/@HOME-12345/@research` | until a move |
| `@41` | koan folder number 41 | yes, until deleted |
| `@41.4.2` | outline: folder 41's 4th child's 2nd child | no: display and typing only |
| `/work/HOME-12345:docs/plan.md` | material inside a koan folder, by path | until a move |
| `@41:docs/plan.md` | material inside folder 41 | yes, as long as the file stays |

- The folder part never carries `@` segments, and the material part never enters a koan folder. `@41:research/x` is refused when `research` is a koan folder; write `@41.<n>:x` or its own `@number:x`. Each file then has one canonical reference.
- Paths start with `/`, numbers with `@`, so `/41` (a folder named 41) and `@41` (folder number 41) never collide.
- Every output carries both forms: `path` (the koan path) and `dir` (the absolute disk path, `@`s included). `koan where @41:docs/plan.md` resolves a material reference to its absolute path.
- Folder numbers come from koan's counter under koan's lock, as task IDs do. Folders and tasks share that one counter, so a number names exactly one thing and `@57` works for either.

**The key-plus-hint convention.** Anything kept (notes, task notes, prompts, commit messages) writes a stable key and a readable hint: `@41 (/work/HOME-12345/research)`, or `@41:docs/plan.md (/work/HOME-12345/research:docs/plan.md)`. The hint tells an agent or person what it is without a call. Reading one: resolve the key; if the path differs from the hint, the key wins, and the drift is mentioned. Outline numbers are never written down. koan never parses notes: this is a convention, not a format.

## Batch creation: shingi's semantics, koan's syntax

Yes: shingi's `create-batch` moves into koan. koan's `create-batch` gains folder entries alongside its task entries, so a whole story is checked and written under one lock, in one transaction. Names follow koan where the two differ (`blocked_by` for task order within a folder, as koan tasks already use).

```json
{"folders": [
  {"path": "/work/HOME-12345", "gates": true, "title": "Payment retries", "extra": {"kind": "story"},
   "notes": "Jira: https://jira.example.com/browse/HOME-12345"},
  {"path": "/work/HOME-12345/setup", "gates": true, "tasks": [
     {"ref": "branch", "title": "Choose repo and branch name"},
     {"ref": "worktree", "title": "Make the worktree and bind it", "blocked_by": ["branch"]}]},
  {"path": "/work/HOME-12345/research", "gates": true, "title": "Research phase", "after": ["/work/HOME-12345/setup"]},
  {"path": "/work/HOME-12345/implementation", "gates": true, "after": ["/work/HOME-12345/research"]},
  {"path": "/work/HOME-12345/review", "gates": true, "after": ["/work/HOME-12345/implementation"]}
],
 "tasks": []}
```

What carries over from shingi, unchanged in meaning:

- Folder entries in any order, full paths; a parent is in the batch or already a folder.
- `after` and `before` between folders, as references (`/path` or `@number`): a folder's start gate waits on the other's done gate.
- Checks before anything is written: duplicate paths (exact or by case), names, lineage (after an ancestor or descendant), and cycles through the gates, including the implicit parent and child links.
- Inline tasks per folder, wired between that folder's gates by construction.
- The result maps each folder to its number and each task ref to its ID.

What changes because it's one tool:

- Adding work later is koan's own `create` with `--gates`, which wires the task between its folder's gates; forgetting the flag gets an `ungated-task` warning. shingi's planned `add-task` isn't needed.
- Inserting a folder is `koan create-folder /work/HOME-12345/spike --gates --after …/research --before …/implementation`.
- Partial failure shrinks to koan's existing rule for a write cut short (the IDs made, in order, and run the rest). There are no more cross-process states: no orphans, no unlinked parents.

## Delete and move under clear ownership

The co-location deal-breaker was two tools owning one directory. With one owner and ownership visible on disk, each operation has a clean rule.

- **Move.** `move-folder` renames one directory: tasks, metadata, notes, material, and child folders move together. Task IDs and folder numbers don't change, so `@` references survive. Task titles never name paths, so nothing is retitled. Worktree markers keyed by number keep resolving.
- **Delete a folder's koan data, keep the files.** A new operation removes the folder's tasks (gates included), its `folder.json`, and its `@`, leaving `HOME-12345/` as a plain folder of material with its `folder.md` renamed or kept as plain notes. This is shingi's delete, the safe default for "this should never have been a unit". It refuses a folder with child koan folders (`has-children`, listing them): removing the `@` would turn the folder into material with koan folders inside, the orphans the nesting rule forbids. Deal with the leaves first: remove their koan data, move them, or delete them. No recursive form for now, so one mistyped path never drops a subtree's tasks.
- **Delete everything.** `delete-folder` removes only what koan owns. When material is present it refuses, unless forced: koan never deletes what it didn't write without being told to. Undo stays git's.
- **Finished or cancelled work is closed, not deleted:** its done gate is marked done, with the reason in its notes, as in shingi.

## In an editor

The `@41:docs/plan.md` syntax exists only in commands and notes; on disk everything stays plain folders and files, so opening `~/koan-root` in Zed keeps working as it does today, and gets better: a story's notes, its tasks' notes, and its material sit in one folder in one window, where today `uow.md` lives in `~/shingi-root` and task notes in `~/koan-root`.

- **Gate clutter.** koan always writes a task's `.md`, even empty, so each gated folder carries `41.json 41.md 42.json 42.md` for its gates. Gate notes are rarely written, since a folder's story lives in `folder.md`, so koan should write a gate's `.md` only when it has notes (decided: gates only; ordinary tasks keep their always-written notes file).
- **Links in notes split in two.** Within a unit, use relative disk links (`[plan](docs/plan.md)`, `[research](@research/folder.md)`): Zed follows them on click, and they move with the folder. Across units, use key plus hint (`@41 (/work/HOME-12345/research)`): it survives moves, and an agent resolves it with `koan where`. Zed can't follow `@41:` references.
- **The syntax is reversible.** `koan where <disk path>` returns a file's stable reference: `koan where ~/koan-root/@work/@HOME-12345/docs/plan.md` gives `@41:docs/plan.md` with its hint, so a file found while browsing can be handed to an agent in stable form.

## With git and Obsidian

git, Obsidian, Zed, and koan all see plain folders and files, so they combine freely. Each owns its own part of the tree: koan its `@` folders, tasks, and `folder.*` files; git `.git/`; Obsidian `.obsidian/`; you the material and the prose of the notes. koan writes and enforces, git keeps history, an editor is the window for people, and agents use the CLI.

### git

- `@` in folder names is ordinary, but pass paths after `--`: on its own `@` means `HEAD` to git. `git log -- @work/@HOME-12345`.
- `@41:docs/plan.md` reads like git's `HEAD:docs/plan.md` on purpose, but the two are separate namespaces: never paste a koan reference into a git command.
- Stable numbers make history searchable across moves. Commit messages carrying `@41 (/work/HOME-12345/research)` let `git log --grep '@41'` find a folder's history after a rename, which following paths does poorly.
- koan's tree is already built to be committed; its machine-local counter lives outside it, in `~/.config/koan`.

### Obsidian

- `@` is allowed in names. Obsidian forbids `[ ] # ^ | :`, and `:` never reaches disk.
- `@41` doesn't collide with Obsidian's syntax, where `#41` would be a tag: a second reason, after the shell comment, that `#` was the wrong prefix.
- Every folder's note is named `folder.md`, so the quick switcher, graph, and tabs show many notes called "folder", and links need full paths. Decided to keep it anyway (see open questions); a folder-note plugin and inline titles help.
- Task notes show as `41.md`, `42.md`: a task's title lives in its `.json`, which Obsidian doesn't display. Accepted: titles stay data in JSON, so notes never grow a format.
- `.obsidian/` is hidden, so koan ignores it. Commit Obsidian's settings if wanted, but gitignore `.obsidian/workspace*`, which changes on every click.
- `[[wikilinks]]` are for people and Obsidian; koan never reads notes. Stored references between folders still use `@57 (path)`.
- Moving a folder in Obsidian is mostly safe: its number travels in `folder.json` and task IDs don't change. Moving a single task file by hand is not: its gate wiring stays with the old folder. Use `koan move`; `koan doctor` catches it after the fact.

## What koan gains, and migrating from shingi

Each item below is useful to a koan-only user on its own, which is the test for putting it in koan.

1. **`@` folders on disk**, with everything else material. A schema bump; `koan migrate` renames every folder under the lock. Typed paths don't change.
2. **Folder metadata and notes:** `folder.json` (number, title, gate pointers, extra) and `folder.md` (prose koan never reads), with `show-folder` and `update-folder` commands mirroring the task ones.
3. **Folder numbers and references:** `@41`, `@41.4.2` outlines in listings, and `path:material` references.
4. **Gates:** optional start and done pointers in `folder.json`, set by `--gates` on a folder and linked to the parent's gates; `--gates` on a task to wire it between them; an `ungated-task` warning when a task in a gated folder lacks it.
5. **Folders in `create-batch`**, with `after`, `before`, and inline tasks, in one transaction.
6. **Ownership-based delete:** remove koan's data and keep the files; `delete-folder` refuses material unless forced.
7. **Worktree markers:** `koan bind` and `unbind`, the self-ignoring `.koan/` marker (as shingi's `.shingi/`), and `koan where` / `koan context` resolving from a bound worktree.
8. **Hooks and kinds**, carried over from shingi as koan configuration, if real use still wants them.
9. **Shell completion, for people:** paths folder by folder (`/wo` → `/work/`, `@` names never shown); `@` numbers listed with their path and title as descriptions in zsh and fish; material after `:` as ordinary file completion inside the folder; task IDs with their titles. Built on cobra's completion scripts (`koan completion zsh`) and its hidden callback command; JSON output is untouched. Completion never waits on the lock: it tries the shared lock without blocking and otherwise reads best-effort. bash splits words on `:`, so completing after `@57:` needs the usual colon workaround and its own test. This reverses "no shell completion" in the CLI spec.

Migrating a shingi tree is one koan command, run once:

- for each unit, in path order: number its koan folder; take the title from `uow.md`'s first `# ` line into `folder.json`'s `title` and move the rest of `uow.md` to `folder.md`; write `kind` into `extra`; and set the gate pointers to its existing start and done tasks (found by `extra.shingi-unit`, as shingi finds them);
- move each unit's working material from `~/shingi-root/<path>` into the koan folder, then retire `~/shingi-root`;
- rewrite worktree markers from `.shingi/` (UUID) to `.koan/` (number).

The skill becomes koan's: shingi's sections on gates, batches, binding, coordinating, and the reference convention move across.

## Open questions

- [x] **One counter: decided.** Folders and tasks draw from koan's single `last_id`, so a number names exactly one thing and `@57` needs no second syntax. `@57` is accepted wherever a folder or a task is, and refused only where the kind is wrong (`@57:docs/plan.md` when 57 is a task). `create-batch` reserves one block for its folders and tasks; `init` recovers `last_id` from the highest task filename or folder number; `doctor` reports a number used twice. Folder numbers are sparse, which doesn't matter.
- [x] **Gate titles: decided, derived.** Stored as a bare `Start` or `Done`; rendered as `Start: <path> (<folder title>)`, path only when the folder has no title; the folder title comes from `folder.json`, and the gate role from the folder's pointers, with no field on the task. See the gates section.
- [x] **Moving gated folders: decided.** A move relinks a gated folder to its new parent's gates when the parent is gated, and drops the old links otherwise, without a warning. Warnings follow one rule: something entering a gated folder without joining its gates (`ungated-task`, `ungated-folder`).
- [x] **Wiring tasks into gates: decided, explicit.** `--gates` on a task wires it; a task created in a gated folder without it gets an `ungated-task` warning, fixed with one `koan block`. `create` stays literal, so a finished folder, prep work before starting, task moves, and subfolders all behave without surprises.
- [x] **Nesting rule: decided.** Every koan folder sits in the root or in a koan folder, all the way up: no orphaned koan folders. Walks never enter material, so large material (a clone, `node_modules`) never slows `list` or `frontier`, and `@` folders inside material (npm's `@types/`, `@babel/`) are never mistaken for koan folders. A koan folder inside material is ignored, not warned: finding it would mean walking material. koan's commands can't create one, since paths only name koan folders; only a manual `mv` can. An `@` folder directly inside a koan folder without `folder.json` is reported by `doctor`, to adopt or rename.
- [x] **Doctor and dangling references: decided, opt-in fix.** `doctor` reports a dangling reference and leaves it in place; it removes the ID only when that fix is asked for by name. A task hidden by a hand-moved folder can't be told from a deleted one (both are below `last_id`), so removal must be a decision, and moving the folder back restores every link. The report says so: "if a koan folder was moved by hand, move it back first".
- [x] **Deleting koan data from a folder with child folders: decided, refuse.** `has-children` until every child koan folder is dealt with, since removing the parent's `@` would orphan them. No recursive form for now. `delete-folder` stays recursive by design, guarded by the material rule. See the delete section.
- [x] **Empty gate notes: decided, gates only.** koan writes a gate's `.md` only when it has notes; a missing file means no notes, `notes_path` is still reported, and writing to it creates the file (as when a cancellation reason goes in a done gate's notes). Ordinary tasks keep their always-written `.md`, a ready file to click and type into in an editor, with no risk of hand-creating `37.md` for task 73.
- [x] **Folder note naming: decided, keep `folder.md`.** Naming the note after its folder stores the name twice, so a hand rename in Zed or Obsidian would leave the note under the old name, where it silently turns into material: the same staleness that made gate titles derived. `README.md` has the same many-same-names problem and blurs ownership. In Obsidian, a folder-note plugin makes `folder.md` easy to reach; Zed already labels same-named tabs with their folder. Any heading written in it is prose; koan never reads it.
- [x] **Titles: decided, data in JSON.** A task's title stays in its `.json`, and a folder's title is a `title` field in `folder.json`; notes (`folder.md`, task `.md`) are pure prose koan never reads. Making a note's first heading the title would be the start of frontmatter, and then of a second file format beside the `.json`. The cost, accepted: in an editor, tasks show as `41.md`.
