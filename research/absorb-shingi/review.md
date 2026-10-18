# Review of the design notes

2026-10-17. A review of [design-notes.md](design-notes.md) against koan's current specs (`spec/design.md`, `spec/operations.md`, `spec/cli.md`) and shingi's `design-spec.md`, and the decisions taken on it. Where this file and the notes disagree, this file wins.

## Overall

The main argument holds. shingi already says the working tree decides and koan follows (its "The hierarchy" section). Given that, the second store adds no information, and all of shingi's partial states come from writing across two processes. Three choices are especially good:

- **The `@` prefix** makes ownership visible on disk, so a stray `docs/` is safe by default.
- **Gate titles are derived**, so they never go stale.
- **`--gates` must be asked for**, so `create` stays literal.

The notes' biggest effect is one they don't state: they reverse a basic rule in `spec/design.md`, "A folder is a directory. It has no fields and no identity." Folders now have identity, a metadata file and pointers into tasks, and the invariants, `doctor`, `repair` and the per-command rules all need to account for it.

## Decisions (2026-10-17)

### Ownership on disk

- **`@` on every entry koan owns,** files included: `@name/`, `@41.json`, `@41.md`, `@folder.json`, `@folder.md`. Everything else is material. This replaces the notes' unprefixed `41.json` and `folder.json`, and removes the clash between material named `2026.md` and task notes, so `repair` keeps its `orphan-notes` fixes, which only ever touch `@N.md`. The disk name `@41.json` matches the reference `@41`. A task `@41.json` and a folder `@41/` can't clash, since folder names have no dots. In-folder links read `[research](@research/@folder.md)`.
- **`folder.md` is always written**, empty when no notes are given, like an ordinary task's `.md`. Only gates skip an empty `.md`.
- **`folder.json` has every key**, as task files do: `schema`, `id`, `title`, `start`, `done`, `extra`, with `title`, `start` and `done` as `null` when unset. The field is `id`, not the notes' `number`: folders and tasks draw from one counter, so a folder's ID and a task's are the same kind of thing, and are called the same. Every koan folder has an ID; the root has none and can't be gated.
- **Case: no check.** Names stay byte for byte, as koan decided before. The batch refuses exact duplicate paths only.

### References

- **Outline numbers (`@41.4.2`): cut entirely,** for input and display. The `@41.<n>:x` form goes with them; material in a child folder is written with the child's own `@id:x`.
- **A task is written `42` or `@42`.** A folder is `/path` or `@id`.

### Gates

- **Gate tasks can't be deleted or moved** with `delete` or `move` (`conflict`, rule `gate`). They go away only with their folder's gates.
- **`update --title` on a gate is refused**, as the notes say.
- **`--gates` on a task in an ungated folder is an error.**
- **`--gates` into a folder whose done gate is done:** wired as usual, with a `folder-done` warning that the done gate doesn't wait on the new task. The same applies to a gated folder created under a done parent.
- **Gated means direct links:** the task is directly in the start gate's dependents (`blocked_by` the start) and directly in the done gate's `blocked_by`. `create` and `move` warn `ungated-task` at that moment. `doctor` doesn't report ungated tasks, so prep work left ungated on purpose isn't flagged forever.
- **`koan update <id> --gates`** wires an existing task into its folder's gates in one command, matching `create --gates`. (The notes' "one `koan block` fixes it" was two commands: one block each way.)
- **`update-folder --gates` is literal:** it creates and links the two gates and wires no existing task. Each existing task is warned `ungated-task`, listed in the result.
- **`update-folder --no-gates`** deletes both gate tasks, which removes every link to them as `delete` does, and clears the pointers. The result's `dependents` shows what became free to start.
- **Moving a gate-wired task to another folder** keeps its old links, as moves never rewire, and warns `foreign-gates`: it is still linked to the old folder's gates. `ungated-task` is also warned when the new folder is gated.
- **`move-folder` checks for cycles** through the gate links it makes, including the parent and child links (X after Y, then X moved under Y, is a cycle), and refuses with the folders it runs through.
- **Gates are ordinary tasks on `frontier` and `claim-next`.** Both take `--gates start|done|none` to filter. A coordinator handing out work passes `--gates none`. `frontier` stays exactly what `claim-next` draws from. Every task gets a derived `gate` field: `start`, `done`, or `null`.
- **Rendered gate titles may exceed 200 characters.** The stored title is a bare `Start` or `Done`, so only the output schema's limit changes.

### Invariants and doctor

New invariants, each with a `doctor` finding when broken:

- A gate pointer names an existing task in the same folder.
- A folder's `start` and `done` are different tasks.
- A task is a gate of at most one folder.
- A folder's done gate is blocked by its start gate.
- IDs are unique across tasks and folders, and none is above `last_id`.
- A `blocked_by` never names a folder's ID (refused on input, too).

Plus the notes' own finding: an `@` folder in a koan folder with no `@folder.json`.

**Repairing dangling references becomes opt-in**, as the notes decided. `repair` fixes everything it fixes today except `dangling-reference`, which it fixes only when asked by name (for example `repair --kinds dangling-reference`). Its message says to move a hand-moved folder back first. A `stale-claim` on a task that is gone is handled the same way, for the same reason (decided in the unit 1 review); one on a done task is still fixed by default.

### Delete and forget

- **`forget-folder`** removes a folder's koan data and keeps its files: its gates, its `@folder.json`, and the `@` from its name. Its `@folder.md` becomes plain `folder.md` material, and it refuses when a `folder.md` is already there.
- **It refuses a folder with ordinary tasks** (`has-tasks`, listing them) unless forced; `--force` deletes them along with the gates.
- **It refuses a folder with child koan folders** (`has-children`), as the notes say. There is no recursive form.
- **`delete-folder` refuses visible material unless forced.** Hidden entries (`.gitignore`, `.obsidian/`) don't count, as today's non-recursive delete allows them, and are removed with the folder.

### Batch

- **A batch that fails partway** lists in its error the existing task files it rewrote (the parent's done gate, `before` targets), as well as the IDs and folders it created. Recovery stays the agent's, from that report.

### Cut or deferred

- **Kinds: cut.** koan has no kind feature, rules file, or check. `{"kind": "story"}` in a folder's `extra` is just data, like any key there.
- **Hooks: deferred**, until real use shows something only a hook can do.
- **Importing the shingi tree: not done.** The user is shingi's only user and won't import. koan's binary never learns about shingi. The `@` rename of koan's own tree is a format change, so it belongs in `koan migrate`, which must handle a half-renamed tree on re-run: in format 1 `@x` isn't a valid name, and in format 2 `x` is material.

### Other notes

- **Completion.** `spec/cli.md` has no "no shell completion" line to reverse. Its rule that koan "never completes [a folder path] or derives one from the working directory" is about inferring input, and stays.

## Still open

Not examined in the review; each is settled when its unit of work is specified. The first two come first, since other units build on them.

1. **Migrating the live tree.** Format 2 renames every folder and task file to `@` names and writes a `@folder.json`, with an ID from the counter, for every existing folder. That is a restructured tree, not only new file formats, so `migrate`'s file-by-file model needs extending to describe it.
2. **Folder output.** What `show-folder` and `list --include-folders` return: id, path, dir, title, gate IDs, state, notes_path, extra. State's rule when the done gate is done but start isn't.
3. **Worktree markers.** The `.koan/` marker's fields (id, path, dir, the folder's ID in place of shingi's UUID); `bind`, `unbind`, `where`; whether `context` is still needed.
4. **Batch input.** Whether task refs are global to the batch or per folder; whether a top-level task's `blocked_by` can name a folder's gate; `after`/`before` taking `@id` as well as `/path`.
5. **Ordering existing folders.** Whether `update-folder` takes `--after`/`--before`, rather than two `koan block`s on gate IDs.
6. **Implicitly created folders.** `create --folder /a/b` makes missing folders ungated, so under a gated parent they warn `ungated-folder`. Probably right; to be stated.
7. **Shell completion's lockless read.** Best-effort reads without the lock are an exception to "a read never sees part of a write" that the spec must name.
8. **Skill and docs.** shingi's skill sections (gates, batches, binding, the reference convention) move into koan's skill; `docs/coordination.md` gains `claim-next --gates none`; the shingi plugin is retired.

## Next

Break the work down. Rough order: the format change and `@`, then folder metadata and IDs, references, gates, batch, delete and forget, then markers and completion.
