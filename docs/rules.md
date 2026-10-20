# koan: invariants and rules

2026-10-20 · 

These are the rules every koan tree obeys, whatever stores it. A rewrite is correct when it keeps all of them; how it keeps them is in the implementation doc.

## Structure

A tree is the root, folders nested to any depth, and tasks. Each folder and the root can hold tasks, folders and material; each folder also has its notes, and up to two gate tasks.

| Kind | Holds | Named | On disk |
| --- | --- | --- | --- |
| Root | folders, tasks, material | no name and no ID; written `/` | the tree's directory |
| Folder | folders, tasks, material, its notes, up to two gates | the folder pattern (Fields), unique among its siblings ignoring case | `@<name>/`; paths drop the `@` |
| Task | nothing: a leaf | its ID, `^[1-9][0-9]{0,14}$` | its notes, `@<id>.md`; a gate's are `@start.md` or `@done.md` |
| Folder notes | nothing: a leaf | one per folder, none in the root | `@notes.md` |
| Material | whatever it holds | any name not starting with `@` | itself; koan never reads it, and it moves and goes with its folder |

*[Diagram, in the live doc only: the kinds of node and entry · two branches, three leaves. The text around it says what it shows.]*

## Readiness

Ordering is a directed graph of tasks, and nothing else. An edge is an entry in a task's `blocked_by`, pointing at a task it waits on. Folders take no part: containment never orders, so a move never changes readiness.

*[Diagram, in the live doc only: the waits-on graph of /work · 3 tasks, and 3 folders with 2 gates each. The text around it says what it shows.]*

**Gates**

A folder can have two tasks of its own, its **gates**. A folder used only to organize has none, and so takes no part in ordering at all.

| Gate | Written | Its `blocked_by` | What waits on it |
| --- | --- | --- | --- |
| Start gate | `@F.start` | what the folder's work waits on before it begins | the folder's members |
| Done gate | `@F.done` | the folder's members | what comes after the folder |

- **A gate is made the first time a change names it.** A change that writes an edge to `@F.start` or `@F.done` makes that gate in the same change, if the folder doesn't have it yet. Changes are serialized and whole (I10), so two changes naming the same gate never make two. Reads never make one: naming a gate the folder doesn't have is `not-found`.
- **A made gate's fields:** the title `Start` or `Done`, `null` priority, no tags, and an empty `extra`. Its `gate` field and its folder say whose it is.
- **Gates are ordinary tasks.** A gate blocks while it is open, and is ready when nothing it waits on blocks. Closing one is an act: someone marks it complete, as with any task. A ready gate is on the frontier and `claim-next` can hand it out: closing a gate is a step like any other, even one that waits on nothing. It can be reopened, deleted or archived like any task, and the next change that names it makes a new one, with a new ID. A gate made again starts open and empty: the old edges stay gone, so whatever comes to wait on it is held back until someone closes it.
- **A gate stays in its folder.** It can't be moved.
- **The root has no gates.**

**Joining a folder** is the structured way to group work: the task waits on `@F.start`, and `@F.done` waits on the task. These are two ordinary edges, so a move keeps them. A folder joins another through its gates the same way: `@sub.start` waits on `@F.start`, and `@F.done` waits on `@sub.done`.

**A member** of F is a task joined to it. Membership is edges and containment is place, and they can differ: a task can be a member of a folder it doesn't sit in, and sit in a folder it isn't a member of.

**Writing an edge from either end.** `--blocked-by` writes an edge from the task being changed; `--blocks` writes the same edge from the task it points at. Given to a task X:

| Input | Edge written |
| --- | --- |
| `--blocked-by @42` | X waits on `@42` |
| `--blocks @42` | `@42` waits on X |
| `--blocks @9.start` | folder 9's start gate waits on X |
| `--blocks @9.done`, or `--blocks @9` | folder 9's done gate waits on X |

**Containment never orders.** A folder holds its contents without waiting on them or making them wait. In the picture, `@1` contains `@9`, but no edge joins their gates.

**The example, as text.** The picture shows this tree, every task in it open:

| Item | Is |
| --- | --- |
| `@1` | the folder `/work`, in the root; it has a done gate only |
| `@7` | the folder `/work/phase1`, in `@1`; it has both gates |
| `@9` | the folder `/work/phase2`, in `@1`; it has both gates |
| `@4` | a task in `@1` |
| `@8` | a task in `@7` |
| `@10` | a task in `@9` |

Its edges, each written as the task that waits, then what it waits on: `@8` → `@7.start`, `@7.done` → `@8`, `@10` → `@9.start`, `@9.done` → `@10`, `@9.start` → `@7.done`, `@1.done` → `@7.done`, `@4` → `@7.done`. There are no others. The gates' own IDs aren't shown: a test may give them any IDs not used above.

**Changes, in the example above**

| Change | Result |
| --- | --- |
| `task block 8 --blocked-by 10` | Refused: `@8` → `@10` → `@9.start` → `@7.done` → `@8` |
| `task block @7.start --blocked-by @9` | Refused: `@7.start` → `@9.done` → `@10` → `@9.start` → `@7.done` → `@8` → `@7.start` |
| `task block 8 --blocked-by @7` | Refused: `@8` → `@7.done` → `@8`. A member can't wait for its own folder's done gate. |
| `task block 4 --blocked-by @1` | Allowed. `@1.done` waits only on `@7.done`, and nothing there waits on `@4`. |

**The rules**

- **Derived, never stored.** Readiness is worked out when read, from the edges as they stand.
- **A task blocks while it is open.** Gates included.
- **Ready.** An open task is ready when nothing in its `blocked_by` blocks, and blocked otherwise. A complete task is neither.
- **Completion.** Any task, a gate included, can be marked complete at any time, blocked or not, and reopened.
- **A folder has no state of its own.** Its gates, when it has them, say where its work stands: a complete start gate means its work may begin, and a complete done gate means it is signed off.
- **Finished.** A folder is finished when every task under it, at any depth, is complete, its gates and those of the folders under it included. This is containment, and it decides archiving. A folder can be finished with its done gate open, or have its done gate complete while work under it is still open.
- **Write the edges before anything waits on them.** A gate that waits on nothing is ready at once, so a plan's joins and blockers go in together, in one batch (see Changes).

**Orders**

- **Work order:** by `priority`, highest first, `null` last; then by ID, lowest first.
- **Tree order:** a folder before everything under it. Within a folder, its own tasks come first, gates included, by ID; then its folders, by name compared byte for byte, so `Zeta` sorts before `alpha`.

## Claims

A claim reserves a task, so that two workers taking work from one tree are never handed the same one. Reading the list of ready tasks can't give that; a claim can.

- **The frontier** is the ready tasks that have no claim, in work order. Gates are tasks, so a ready gate is on it like any other.
- **Claiming.** `task claim` takes an open task that has no claim; a second claim of it fails at once, as `conflict` (`claimed`), and a claim of a complete task fails as conflict (complete). `task claim-next` claims the first task of the frontier, in the whole tree or under one folder. That scope is the one place containment affects which work is handed out: it chooses where to look, never what is ready.
- **A blocked task can be claimed.** A claim reserves work ahead of time; it doesn't make the task ready.
- **A claim refuses other claims and takes the task off the frontier, and nothing else.** Every other change works on a claimed task as on any other, and `task release` works whoever made the claim.
- **A claim ends** with `task release`, or when its task is marked complete or deleted. A reopened task starts unclaimed. Archiving never ends one, since only complete tasks leave.
- **It never expires.** It outlasts the process that made it. Who holds it, and whether they are still alive, is the caller's business: a claim carries `claimed_at`, and an `extra` object koan stores and never interprets. A claim is its own record, not a field of its task.
- **It doesn't lock the notes.** Nothing stops two editors overwriting each other in a task's notes; the convention is that whoever holds the claim edits them.
- **A claimed task can become blocked.** A new edge to an open task, or a blocker reopened, makes it wait again, and the claim stays. A move never does this, since containment never orders.

## References

A reference names a task, a folder, or material in a folder: in commands, and in text kept beside the tree, such as notes and prompts. This section is what can be written; Resolution, next, is how each form is read.

| Written | Names |
| --- | --- |
| `42` or `@42` | the task or folder with ID 42 |
| `@9.start` | folder 9's start gate, a task |
| `@9.done` | folder 9's done gate, a task |
| `/` | the root |
| `/work/api` | a folder, by its path from the root |
| `/work/api.start`, `/work/api.done` | that folder's start or done gate |
| `41/phase2` or `@41/phase2` | a folder, by its path from folder 41 |
| `41:docs/plan.md` or `@41:docs/plan.md` | material in folder 41 |
| `/work/api:docs/plan.md` | material in a folder, by path |
| `@41/phase2:docs/plan.md` | material in a folder, by its path from folder 41 |
| `$a` | a task being made in the same batch, by its label; it names nothing outside that batch |

The grammar:

```text
reference = target [ "." end ] | target ":" material
target    = id | path | anchored | label
end       = "start" | "done"
id        = [ "@" ] digits                 digits: ^[1-9][0-9]{0,14}$
path      = "/" | "/" name { "/" name }    name: the folder pattern
anchored  = id "/" name { "/" name }
label     = "$" name                     only within one batch
material  = part { "/" part }              no empty part, no "." or ".."; the first not starting with "@"
```

- **The `@` is optional on input.** koan always writes it, since `@41` stands out in prose, where a bare `41` does not.
- **`.start` and `.done` name a folder's gates.** Each resolves to that gate's own task ID, so wherever a task is taken, a gate can be named either way.
- **The anchored form is for typing only.** koan accepts it and never writes it.

**Kept text** writes the target's own ID, with its full path as a hint: `@9 (/work/phase2)`, `@9:docs/plan.md (/work/phase2:docs/plan.md)`. The ID survives moves and renames; the path is there to be read. A gate is written by its own ID, with its folder's path and its end as the hint: `@57 (/work/api.done)`, `@56 (/work/api.start)`. Kept text never writes an end on the ID side: in prose, `@9` is the folder. Labels never appear in kept text: they name nothing once their batch is over.

**Keep references out of anything that outlives the tree.** koan is one person's tool on one machine, and its IDs mean something only to this tree. Don't write koan references into commit messages, code, or anything else that leaves the machine or lasts longer than the work it names.

## Resolution

Resolution turns a reference into a task or a folder, and a material reference into a place in a folder's directory. For a change, it happens inside the change, which is serialized with every other change, so a reference names what exists when the change is made: nothing can be deleted between resolving a reference and writing an edge to it.

1. **Split at the first `:`.** What comes after is the material. A folder path can't hold a `:`, so the first one always splits; the material may hold more.
2. **Split off a final `.start` or `.done`.** The end is split from the target, never from the material: `41:notes.done` is the file `notes.done` in folder 41. A folder name can't hold a `.`, so a final one on a target always starts an end. An end before the `:`, as in `41.start:x`, is `invalid-input`.
3. **Resolve the target.**
      - **An ID** names whichever has it, task or folder, since they share one counter. Where a command takes only one kind, the other is `not-found`, with a message saying what the ID is.
      - **A path** is followed from the root, one name at a time, each matching a child folder's name exactly. A name that matches only ignoring case (see Case) is `case-mismatch`, naming the folder as it is spelled; one that matches nothing is `not-found`.
      - **An anchored path** starts at the folder its ID names, then is followed as a path. An ID that names a task is `invalid-input`: a task holds no folders.
      - **`/`** is the root. It has no ID and no gates, so no edge can name it.
      - **A label** names a task made in the same batch, wherever in the batch its part comes. Labels are compared byte for byte. A label defined twice in one batch, used outside a batch, or never defined in it is `invalid-input`. A label takes no end and no material: `$a.start` and `$a:x` are `invalid-input`.
4. **Resolve the end.**
      - **On a folder**, an end names that folder's gate. A change that writes an edge to a gate the folder doesn't have yet makes it; anywhere else, a missing gate is not-found. In an edge (`--blocked-by`, `--blocks`), a folder with no end means its done gate: "after this folder" is the common case, and `.start` must be written. Where a command takes a folder, as `folder show` or `task move --to` do, an end is `invalid-input`.
      - **On a task**, an end is `invalid-input`: a task has no gates.
5. **Resolve the material.** It names where a file is or would be inside the target's directory. The target must be a folder or the root, since a task has no directory. koan never checks that the file exists, and never reads it. Below the first part, `@` means nothing: `@41:node_modules/@types/x` is a file in material.

What a reference resolves to then holds:

- **Stored edges name tasks by ID.** A gate is stored by its own ID, however it was written, so a later move or rename never changes what an edge names.
- **koan names tasks and folders by ID** in everything it returns, and marks a gate as its folder's start or done gate. `where` gives the kept-text pair, the `@<id>` form with its path.
- **In kept text, the ID wins.** Reading `@9 (/work/phase2)`, `@9` decides, and a path that no longer matches is a stale hint. koan never reads notes, so this is a convention, not a check.

## Fields

A folder holds four fields and a task ten, and each has its notes. Every field is required, and no other field exists. A claim is its own record, not a field of its task (see Claims).

**Folders**

| Field | Rule |
| --- | --- |
| `id` | The folder's identity. Never changes. |
| `name` | The folder pattern: 1–64 ASCII letters, digits and hyphens, not starting or ending with a hyphen, `^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}[A-Za-z0-9])?$`. So a name never holds a `.`, a `:` or a `/`. Unique among its siblings, ignoring case. |
| `title` | `null`, or a title by the task rule. |
| `extra` | Any JSON object. koan stores it and never interprets it. |

A folder's place in the tree (its parent) is structure, not a field: moving it changes no field. Nor are its gates: they are tasks in it, with fields of their own.

**Tasks**

| Field | Rule |
| --- | --- |
| `id` | The task's identity. Never changes. |
| `gate` | `null` for an ordinary task; `start` or `done` for one of its folder's gates. Never changes. |
| `title` | 1–200 characters after trimming whitespace, with no control characters or line breaks. |
| `priority` | An integer or `null`. Higher comes first; `null` ranks last. |
| `created_at` | When the task was made. Never changes. |
| `completed_at` | `null` while the task is open, a timestamp once it is complete. This alone is the task's state; complete covers cancelled too. |
| `updated_at` | When one of these fields last changed, by whatever change: an entry added to or removed from its `blocked_by` by a change to another task counts, so `X --blocks @42` moves `@42`'s and not X's. A move or a notes edit does not. |
| `blocked_by` | The set of tasks it waits on, gates included. Never itself. |
| `tags` | A set of labels matching `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`: the folder pattern, lowercase only. |
| `extra` | Any JSON object, never interpreted. A workflow `status` goes here. |
| notes | The `@<id>.md` file, or `@start.md` or `@done.md` for a gate: prose, possibly empty. koan never reads meaning into it. |

**Values**

- Integers are within ±(2^53 − 1), written without a fraction or an exponent.
- Timestamps are UTC to the second, `YYYY-MM-DDTHH:MM:SSZ`, and name a real date and time.
- Sets hold no duplicates, and keys are matched exactly: `Title` is not `title`.

## Changes

Every change is whole (I10), resolves its references inside itself (see Resolution), and is refused if it would break an invariant. This is what each one does. Beyond the refusals listed, any change is `not-found` for a reference that names nothing, and `invalid-input` for a value that breaks its rule; each listed refusal is a `conflict`, named by its rule, unless it says `invalid-input`.

| Change | What it does | Refused when |
| --- | --- | --- |
| Create a folder | Makes the folder at its path, with its directory and empty notes. It has no gates until one is named. | a sibling's name differs from it only in case (`case-mismatch`); the path is taken (`exists`) |
| Create a task | Makes the task in a folder or the root, with any edges given. A gate an edge names is made if the folder lacks it. | an edge would close a cycle (`cycle`) |
| Update a task or folder | Changes its title, priority, tags or `extra`. | — |
| Block, unblock | Adds or removes edges. A gate an added edge names is made if the folder lacks it. An added edge can block a claimed task. | the edge would close a cycle (`cycle`), or point a task at itself (`invalid-input`) |
| Complete, reopen | Sets or clears `completed_at`. Completing ends the task's claim. Reopening blocks again whatever waits on it. | — |
| Batch | Makes tasks and folders, and writes edges, as one change: all of it, or none. It only creates and blocks; completing, moving and deleting are changes of their own. A task it makes can carry a label, such as `$a`, that names it only within the batch, so any other part, before or after it, can write edges to it; a folder it makes is named by its path. The cycle check runs over the whole batch, and several parts naming the same gate make one gate. Labels appear only in a batch's input, which comes from a file or standard input, never from command-line arguments, so a shell never sees them. The result maps each label to the ID it got. A batch resolves its paths and labels against the tree as the whole batch would leave it, so a part can name a folder or a task that another part of the same batch makes, whatever their order. | whatever would refuse one of its parts |
| Move a task | Puts it in another folder or the root; its notes move with it. No edge changes, so no readiness does. | it is a gate (`gate`) |
| Move or rename a folder | Changes its parent or its name; everything under it moves with it, material included. No edge changes. | it would land inside itself (`into-itself`); beside a name that differs only in case (`case-mismatch`); on a path that is taken (`exists`) |
| Delete a task | Removes the task, its notes, its claim, and every edge to and from it (I5). Whatever it was the last open blocker of becomes ready, and the result lists what it released. | — |
| Delete a folder | Removes the folder and everything under it, gates included, the same way as a task. | it holds tasks or folders other than its own gates, and `contents` isn't given (`not-empty`); it holds material and `force` isn't given (`has-material`) |
| Archive | See Archiving. | something leaving is open (`not-finished`) |
| Claim, release | See Claims. | the task is already claimed (`claimed`), or complete (complete) |

- **`updated_at` follows the fields.** It moves on every task whose fields a change touches, including a task whose `blocked_by` gains or loses an entry because another task was blocked, deleted or archived.
- **Nothing happens on its own.** No task is reopened, closed or made except by a change that says so; the one exception, a gate made when first named, is reported in the result.
- **Writing into a complete task warns.** An edge added to the `blocked_by` of a task that is already complete, a gate included, doesn't reopen it, so whatever waits on it isn't held back; the result says so.
- **A delete never splices.** The deleted task's own blockers are not passed on to what waited on it. Re-point that work with `block` if it should keep waiting.

## Archiving

Archiving takes a task or a folder out of the tree for good, with everything under it, and keeps it where koan never reads it again. It is how finished work leaves the tree without being lost, and it keeps the tree small.

**The index.** For each archived ID, koan records where in the archive it went. It reads that index, never the archived files, so that a reference to an archived ID can say where it went. The archive and its index are not part of the tree: they sit beside the tree's directory, never inside it, so T1–T6 never apply to them.

**What leaves.** The task, or the folder with every task and folder under it, gates included, their notes, and all of its material, hidden files included.

**What the archive keeps.** Each task's and folder's fields, its notes, and every edge it had, to or from anything. That is enough to recreate the work later.

**When it is allowed.** A task, a gate included, can be archived when it is complete. A folder can be archived when it is finished: every task under it, at any depth, is complete, its gates and those of the folders under it included. Nothing else is checked, and nothing more needs to be. A complete task never blocks, so every edge that archiving removes pointed at something that no longer held anything back, and the readiness of what stays never changes.

A nested `.git`, or a worktree inside the folder, is material, so it leaves with the folder.

**What changes in the tree.**

- Every edge between what stays and what leaves is removed in the same change (I5). The archive keeps them, as part of what left.
- The IDs that leave are retired. They are never issued again (I2), and never resolve again: a reference to one is `not-found`, and the error says where in the archive it went, from the index (I12).
- No claim ends, since complete tasks hold none (I8).

**Bringing work back** is not a change koan makes. It means recreating the work from the archive as new tasks and folders, with new IDs; the old IDs stay retired.

## Invariants

Every state of a tree that a reader can see satisfies all of these. koan refuses any change that would break one.

- **I1. Unique IDs.** No two tasks or folders share an ID.
- **I2. IDs are never reissued.** An ID once issued is never issued again, even after its task or folder is deleted or archived.
- **I3. The hierarchy is a tree.** Every task and folder has exactly one place, and following parents from any of them reaches the root. No folder is inside itself.
- **I4. Siblings differ ignoring case.** No two folders with one parent have names equal ignoring case.
- **I5. No dangling edges.** Every edge names an existing task. Removing a task removes every edge to it; removing a folder removes every edge to any task under it, gates included, in the same change.
- **I6. No edge to itself.** No task waits on itself.
- **I7. Acyclic.** The graph (see Readiness) has no cycle: no task waits on itself, directly or through other tasks.
- **I8. Claims are on open tasks.** Every claim names an existing, open task. Completing it or deleting it ends the claim in the same change.
- **I9. Values follow their rules**, as Structure and Fields state them.
- **I10. Changes are whole.** A reader sees a change entirely or not at all, never part of one.
- **I11. Gates stay with their folder.** Each folder has at most one start gate and one done gate, directly in it, and a gate never moves out of its folder. The root has no gates.
- **I12. The archive index is complete.** Every archived ID is in the index, with where it went, and no ID in the tree is.

## The tree on disk

The tree's directory lays the structure out as directories, and holds only files people edit. Where the files and the structure disagree, the files are wrong. koan puts them right in two places only: when a write finishes the filesystem steps of its own change, and when `repair` is run. Reads only report. Repair never deletes anything that holds content, material or not: a stray directory is moved back where the structure says it belongs, or reported when its place can't be told, and a stray `@` entry, such as a hand-made `@Notes.md`, is only reported.

**Who owns what**

- **koan's entries are those named with `@`.** Only koan creates, renames, moves or removes them. Anyone may edit the contents of the notes among them.
- **Material is every other entry**, hidden ones such as `.git` included. koan never reads it or walks into it, moves it only with its folder, and removes it only when told to.
- **A directory named with `@` inside material is material**, so npm's `@types/` is never taken for a folder.
- **`.DS_Store` is disposable**, and goes with its folder.

**Invariants of the files**

These hold whenever no change is in progress.

- **T1.** Every folder has a directory at its path, named in the folder's exact case: `/work/api` is `@work/@api`.
- **T2.** Every `@<name>` directory in the root or in a folder's directory is the folder at that path.
- **T3.** A task's notes are `@<id>.md` in the directory of the folder it sits in, or in the root; a gate's are `@start.md` or `@done.md` in its folder's directory. A missing notes file reads as empty notes.
- **T4.** Every `@<id>.md` belongs to an existing task, and sits where T3 puts that task's notes.
- **T5.** A folder's notes are `@notes.md` in its directory; the root has none. A missing one reads as empty.
- **T6.** No other `@` entry sits in the root or in a folder's directory, beyond what koan itself keeps there to mark the tree.

## Case

A tree behaves the same on a filesystem that tells case apart, as most Linux filesystems do, and on one that ignores it, as macOS's default filesystem and Linux directories with casefolding do. The root may be on either. koan gets there by never relying on either: it keeps every name exactly as given, and refuses anything whose meaning would depend on the filesystem.

| Rule | So that |
| --- | --- |
| **Names are kept as given and compared byte for byte.** `Work` and `work` are different names. | Nothing depends on how the filesystem compares. |
| **Siblings never differ only in case** (I4). With `/work` there, creating `/Work` beside it is refused. | Every tree is valid on a filesystem that ignores case, so a tree made on Linux moves to a Mac intact. |
| **A path names a folder only in its exact case, on every platform.** A part that matches only ignoring case is `case-mismatch`, and the error spells the folder as it is. | `/Work` never reaches `/work`, even on a Mac where the filesystem would open it. A command means the same thing on both. |
| **One input never names two paths that differ only in case** in the same part, such as `/Proj/a` and `/proj/b`: `invalid-input`. | One command can't mean two folders on Linux and one on a Mac. |
| **Changing a folder's case is a move:** `folder move /work --to /Work`. It is the one input allowed to name both spellings. | A case-only rename has one way to happen, and works on both kinds of filesystem. |
| **A folder's directory is spelled exactly as its name** (T1). | What Zed shows matches what koan says. |
| **koan's own names are matched exactly:** `@notes.md`, `@start.md`, `@done.md`, `@<id>.md`, and `@.case-<seq>`, the temporary name a case-only rename passes through, which belongs to the pending step that named it. Any other spelling is a stray entry. | A hand-made `@Notes.md` is never taken for a folder's notes. |
| **Tags are lowercase only, and IDs are digits.** | Matching them never involves case. |
| **Material names are never compared.** The part after `:` in a reference is not resolved against the disk. | Case in your own files is the filesystem's business, not koan's. |
