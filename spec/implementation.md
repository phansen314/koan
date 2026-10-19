# koan implementation

How koan is built: the mechanisms behind what [design.md](design.md), [operations.md](operations.md), and [cli.md](cli.md) promise, and the decisions that are not obvious from the code. What the code makes obvious is not repeated here.

## Toolchain

- **Go**, standard library first. Go 1.25 or later, for `os.Root`'s `Link` and `Rename`.
- **Dependencies:** cobra, for the command line; and `golang.org/x/sys/unix`, for the no-replace rename the standard library lacks (below). A JSON Schema validator is a test-only dependency.
- **The version comes from Go's build information** (`runtime/debug.ReadBuildInfo`): the main module's version when it is a release tag, as for `go install …@vX.Y.Z`, else `0.0.0-dev`; the commit, its time, and uncommitted changes from the VCS stamp, or, for a binary installed from a pseudo-version, the commit and time the pseudo-version names.
- **JSON is `encoding/json`**, with nothing wrapped around it. Files and inputs decode into structs with `DisallowUnknownFields`; `extra` is a `map[string]any`. Files are written with `MarshalIndent`, two spaces, and a trailing newline, with HTML escaping off.

## Packages

Everything but `main` is under `internal/`: koan's contract is the JSON its CLI prints, and a public Go API would be a second one.

```text
cmd/koan            main: signals, run(), os.Exit
internal/cli        cobra commands, command line to input, envelope to stdout, exit codes
internal/ops        the commands, grouped by kind: decode and check input, do the work, build the result
internal/store      root states, the lock wait, writing and moving files, the walk, the state file, markers
internal/graph      readiness, the cycle check, why: pure functions, no I/O
internal/model      Task, @koan.json and the state file, names, IDs, timestamps, and their validation
internal/errs       error, warning, and finding types
internal/fsys       the only package that touches the disk, with a hook before each call for injecting faults
internal/buildinfo  the version a build is stamped with
internal/schematest test helper: validates inputs and envelopes against schemas/
schemas/            the JSON Schemas, written by hand
e2e/                tests against the built binary
```

Imports go one way: `cli → ops → store → fsys`. `errs`, `model`, and `graph` are importable from anywhere: `errs` imports nothing of koan's, `model` only `errs`, and `graph` only `model`.

- **`ops` is the unit of behaviour.** Each command is a function from a decoded input and an environment to a result or an error. The CLI knows nothing about tasks: it builds the input object, calls the command, and prints what comes back.
- **The environment is passed in**: `HOME`, the clock, and the `fsys` to use. Nothing in `ops` or below reads the process environment, or the wall clock for a timestamp, which is what lets tests fix the time and inject faults. The one use of the real clock is the lock wait, which measures elapsed time.

## The filesystem layer

Two layers:

- **`internal/fsys`** is a thin layer over the system calls: open a root, list, read, create a temp file, link, rename, flush, take the lock once.
- **`internal/store`** builds the sequences on it: waiting for the lock, publishing a file, moving, and removing a folder.

What the two provide together:

### One handle on the root

Every operation opens `~/koan-root` once, with `os.OpenRoot`, and does all its file work through that handle: `@koan.json`, the tree under `tree/`, and the archive under `archived/`. The path is resolved one time, so a write cannot be split across two directories by a symlink repointed under it, and an archive is a rename within the one handle.

- **The path is opened with `/.` appended.** `os.OpenRoot` checks the type only after opening, so a FIFO at the path would block and a regular file would give an error with no errno, where koan must report `not-a-folder`. With `/.`, the kernel refuses both with `ENOTDIR` before opening anything. This also resolves the one symlink the design allows.
- **No symlink inside the tree is followed.** `os.Root` follows symlinks that stay inside the root, which koan must not, so koan looks at an entry's type before it opens it: the walk by the type its listing gives, a folder path by `Lstat` on each folder in it, and a file read by `Lstat` just before, refusing a symlink with `ELOOP` and reading anything else that is not a regular file, a FIFO included, as empty. An entry swapped between the look and the open can only be an outside change, and is not defended against.

### The lock

- **`flock` on `~/koan-root` itself**, taken on the descriptor from `root.Open(".")`. There is no lock file to create, clean up, or delete from under a holder, and every path to the directory reaches the same lock.
- **`flock`, never `fcntl` record locks.** A record lock is dropped when the process closes *any* descriptor to the file, so one stray open-and-close inside a write silently releases it.
- **Exclusive for a write, shared for a read**: `LOCK_EX` or `LOCK_SH`. A read holds it from before its walk to after its last file is loaded, which is what makes a read see whole writes. `flock` promises no fairness, so a steady stream of reads could hold a write off until it gives up; koan's reads take milliseconds, and nothing is done about it.
- **Polled**: `fsys` makes one attempt with `LOCK_NB`, and `store` repeats it every 10 ms for up to 5 seconds, then reports `busy`. A blocking `flock` can only be cut short by a signal.
- **The `*os.File` must stay referenced** until the write ends, which is why the lock is a value that holds it. If it becomes unreachable, Go's finalizer may close it mid-write, which releases the lock.
- **Not inherited**: Go opens files close-on-exec, so a program koan starts never holds the lock.

`flock` on a directory is confirmed on Linux by test, and on macOS from the XNU source and by the same tests in CI.

### Writing a file

Every file koan writes goes through a temp file named `.koan-tmp-<random>` in the destination's own directory: hidden, so walks ignore it, and recognisable, so `doctor` can find one left behind.

1. Create it with `O_CREATE|O_EXCL`, write it whole, `fsync` it.
2. Publish it: `link` for a file that must be new, so that nothing already there is ever overwritten; `rename` to replace an existing one.
3. `fsync` the directory, then remove the temp name.

- **Why flush the file:** without it, a power loss can leave a published file empty. A file published by `link` gets no help from ext4's rename heuristics.
- **Why flush the directory:** so that one step is durable before the next starts. A task file must not survive a crash that loses the counter increment before it, or the ID is issued twice.
- A failed file flush fails the write, with nothing published. A failed directory flush is ignored: the file is already there, and an error would report a change as not made.
- Files are `0644`, folders `0755`, the state file `0600`.

### Moving and removing

- **Moves never replace.** `task move`, `folder move`, `task archive`, and `folder archive` rename with `renameat2(RENAME_NOREPLACE)` on Linux and `renameatx_np(RENAME_EXCL)` on macOS. Plain `rename(2)` silently replaces an empty directory, so a check made beforehand would be the only guard.
- **The one plain rename** is `folder move` changing only a folder's case where `to` is the folder itself (`os.SameFile` on an `Lstat` of each): on a filesystem that ignores case the no-replace rename refuses it, and since it is one entry, under the lock, nothing can be replaced. Where `to` is not the same entry, the rename is the usual no-replace one.
- **Exact case.** Each part of a folder path is looked for in a listing of its parent, not by `Lstat` alone, which on a filesystem that ignores case finds `@proj` when asked for `@Proj`. An entry with the exact name is the folder; failing that, a `@` entry whose name matches ignoring the case of ASCII letters is `case-mismatch`; otherwise the part is missing. Every lookup of a folder path goes through this one check, `Tree.lookup`: `Missing` for each part of a path, `Taken` for what is at `folder move`'s `to` and at a `batch` folder entry, and `MakeFolder` for what it finds already there. It costs one listing per path part, on every platform.
- **A folder is made with `mkdir`**, then its folder file and notes are written in it as any file is. A kill or failure between leaves a folder without its folder file, `doctor`'s `missing-folder-file`. A folder that appears under the name first, by an outside change, wins: the command does not list it in `folders_created`. `changed_paths` lists the folder, not its files.
- **A folder is removed depth first, one entry at a time**, in an order that leaves nothing `repair` would get wrong if cut short: in each folder, notes and everything else first, then task files, so no notes are left without their task, then `@folder.json`, so no folder is left without its folder file, which `repair` would give a new ID. A symlink is removed, never followed. The folder is in `changed_paths` even when the removal fails, since it may have removed part of it; running `folder delete` again removes the rest. After a failed removal, `folder delete` checks with `Lstat` which of the tasks it set out to remove have no task file left, and removes those tasks' IDs and claims as a complete delete would. Its walk still holds for everything else, since nothing but those tasks has gone.

### The order of a command's writes

A command that changes several files orders its writes so that a kill between any two leaves something `doctor` finds, and never a change that looks finished.

- **Issuing IDs**: every command that does writes `@koan.json` once, its counter raised past the IDs it takes ([IDs](design.md#ids)), before anything else.
- **`task create`**: the counter first, then any folders, then each task file and folder file in `blocks`, in ascending order of ID, then the task file, then the notes. What is in `blocks` goes before the task it waits on, so that a kill between leaves them waiting on an ID with no task, a dangling reference, as `task delete` leaves one. The other order would leave the new task with its dependents still free, and nothing to show for it.
- **`folder create` with `blocks`**: the same order: the counter, each task file and folder file in `blocks`, then the folders, the new one last with its folder file and notes.
- **`batch`**: the counter is raised once, by the number of folders and tasks the batch makes. Then the folders are made, parents first, each with its folder file and notes; then the tasks are written in input order, each as `task create` writes one.
- **`folder archive` and `task archive`**: the rename into the archive first (for a task, its task file, then its notes), then the task files and folder files naming what was archived, ascending by ID, then the state file, then the index, rewritten whole. Everything after the rename is something `repair` finishes by default: a reference to an ID the archive holds, a stale claim, notes left behind, a missing line. The other order would free dependents of something still in the tree.
- **`task delete`**: the notes first, then the task file, then the other task files and folder files naming it are rewritten, then its claim ends. A kill after the notes leaves the task, and `task delete` can be run again. A kill after the task file leaves references to a task that is gone; the other order would free its dependents with nothing to show for it.

### Fault injection

`fsys` has one implementation, the real one, with an optional hook that every call runs first. The hook is told the call, its paths, and whether it changes the disk; it can fail the call with an errno, or kill the process. It is the one seam for testing failures and crashes: the injections that count calls (fail or kill at the n-th change to the disk, or the n-th call of a kind) are one counter with an action. Unit tests set the hook directly; the code that sets it in the binary, for the end-to-end crash tests, is behind the build tag `e2e_hooks`, so a released binary cannot inject faults.

## The state file

`~/.config/koan/state.json` is outside the tree, so its directory is opened as a second root handle, and the file is read and published through that handle exactly as a file in the tree is. It is written only while the tree's lock is held; its own directory is never locked. `doctor` cannot see that directory, so each write of the state file first removes any `.koan-tmp-*` left there.

Every command reads the state file after taking the lock, never before.

## Markers

- **A real path, once.** `marker bind` and `marker unbind` resolve `dir` with `filepath.EvalSymlinks`, then open it as a root handle as the tree is, `/.` appended, and do all their work in `.koan/` through that handle: the marker's `dir` is the directory written to, even if a link on the way is repointed.
- **Against the tree, by identity.** `filepath.EvalSymlinks` resolves links but keeps the case it was given, so on a filesystem that ignores case one directory has several real paths. `in-tree` walks up from `dir`, and `holds-tree` from `~/koan-root` and from its real path, comparing each directory with the other by `os.SameFile` on an `Lstat` of each.
- **Files.** `.koan/` is made with `mkdir`, `0755`, then listed: an entry that is not a regular file, or not `marker.json`, `.gitignore`, or `.koan-tmp-*`, or a `.gitignore` holding anything but `*` or nothing, is material, and refuses the command. A new `.gitignore` is created with `O_EXCL`, written, and flushed in place, so no temp file sits in `.koan/` before it; `marker.json`, and an empty `.gitignore`, are written by temp file, flush, and `rename`, then the directory flush, as any replaced file is (above). `marker unbind` removes the temp files.
- **The lock.** `marker bind` takes the tree's lock exclusive, though it writes nothing there, so its read of the old marker, its `bound-elsewhere` check, and its write are one step against another `marker bind`.
- **The checkout test** is one `Lstat` of `dir/.git`: a directory is a checkout; a file, a missing entry, or anything else is not. A symlink is not followed, and is not a checkout.
- **The look up from a path** in `where` and `context` uses plain paths, outside any root handle, since it may cross any directory: from the starting directory to `/`, an `Lstat` of `<d>/.koan`, which must be a directory, not a symlink, then of `<d>/.koan/marker.json`, stopping at the first entry there of any type. A `.koan` that is anything else, or that has no `marker.json`, is passed over. The directory a marker is in is compared with its `dir` by `os.SameFile`, as above.

## Walking the tree

There is no index on disk; a command that needs one builds it.

- **By name only.** The walk lists folders depth-first, each folder's entries sorted, and classifies every entry by its name and type without opening any file. It enters only `@` folders, and never looks inside material. It returns each task ID with its path, and the list of folders. Two paths for one task ID is the `duplicate-id` error.
- **Every folder file, once, after the walk**, in every command that reads or computes readiness, checks for a cycle, resolves a blocker, or returns a folder: every command but `task release`, `task update`, `task done`, `task reopen`, `task claim`, `version`, `info`, `migrate`, `marker unbind`, `folder create` when it makes no folder and has no `blocked_by` or `blocks`, and `folder move` when it renames. `doctor`, `repair`, `migrate`, and `init` read every folder file their own way. The **folder index** this builds, each folder ID with its path and `blocked_by`, is the one place a folder is found by ID, a blocker ID that is no task's is looked up before it counts as dangling, and a folder file that is missing or can't be used is warned about, once. A dangling ID in a folder's `blocked_by` is warned about only when the command evaluates that folder's blockers. Folder files are small and few beside task files, so reading them all costs less than deciding which ones a command needs.
- **A duplicated ID fails only a command that looks it up**: an ID it is asked for, or a blocker it resolves, held by two files. `task show 3` still succeeds though a folder file holds 3 too, unless 3 is one of its blockers. `doctor` finds every duplicate.
- **Task files are loaded when needed**, and each at most once per command. Finding a task by ID, or proving it absent, costs one walk, the folder files, and one file read. Only commands that need every task's fields load them all: `task frontier`, `task list`, `task claim-next`, `folder list`, `task delete`, `folder delete`, `task archive`, and `folder archive` (to find references), `doctor`, and `migrate`. A task's readiness also loads the task files under each folder it waits on, to tell whether the folder is done; each folder's done-ness is computed once per command, bottom-up from the tasks loaded. A folder record carries `readiness` and `empty`, so every command that returns one loads every task file under that folder, and the blockers it waits on: `folder show`, `folder list`, `folder delete`, `where`, whose record is the folder holding a task or material too, and `context`, whose records are the folder and every folder above it. For the root that is the whole tree. Trees are small, and the cost is accepted.
- **Commands that need no walk don't make one:** `task release`, `version`, `info`, `marker unbind`, `folder create` when it makes no folder and has no `blocked_by` or `blocks`, and `folder move` when it renames, keeping the folder's parent, a folder given by path. `task move` and other moves of a folder walk, for the cycle check. `init` walks, and reads the folder files, only when it makes a `@koan.json`.
- **A file that vanishes** between the walk and its load can only be an outside change, since the command holds the lock. It is skipped; if it was the task the command was asked for, that is `not-found`.

- **The archive is walked only by `init`, `doctor`, and `repair`**, by the same code, from `archived/`: its plain directories are entered as if they were folders, an `@<id>_<name>` directory is an archived folder walked as one, and `@<id>.json` a task file. It reads folder files there only for their IDs. The index is read only to word a `not-found`, after the walk has found nothing.

## The graph

`internal/graph` works on tasks and folders already loaded.

- **Readiness is not a search.** Done-ness comes only from stored `completed_at`, never from readiness, so a task's readiness is direct: its own `blocked_by` and its folders' (from the folder index), each blocker checked as a task's state or a folder's done-ness. It is computed from all of them, not stopping at the first that blocks, so `open_blockers` and `inherited` are complete. On a tree where no `blocked_by` names a folder, no task's readiness needs a folder's done-ness; only folder records compute it.
- **The cycle graph** is the one in [design.md](design.md#invariants): a node for each task, and a wait node and a done node for each folder. They exist only in the search. A task's node is keyed by its ID; a folder's by its path from the walk, never its ID, since a folder with no folder file has none and still sits in the graph by its place in the tree; a `blocked_by` ID is mapped to a folder's done node through the folder index. Each node's edges come from what is loaded: a task's from its `blocked_by` and its folder; a wait node's from the folder file and the parent; a done node's from the walk's listing of the folder, and the edge to its own wait node. A folder with no folder file gives its wait node only the edge to its parent's; one whose file can't be used fails a search that reaches its wait node, as a task file that can't be used fails one that reaches it.
- **The containment check** runs first, on paths alone: a new blocker that is X itself or a folder that contains X, or a folder blocked by something under it, is refused without a search. For a move, X is everything under what is moved, against the blockers of its new ancestors as well as its own.
- **The cycle check**: a change adds edges, and closes a cycle exactly when one of them, from A to B, has a path back from B to A. The search runs over the graph with the change made, the edges a move takes away gone, and a breadth-first search from each new edge's end, through open and done tasks and folders alike, loads only the files it reaches. A new node, for a task or folder being made, has edges both out and in, so a cycle through it runs from one of its out-edges' ends to one of its in-edges' starts:
  - `task block`: from each new blocker to the task.
  - `folder block`: from each new blocker to the folder's wait node.
  - `task create`: out to each blocker and to its folder's wait node; in from each node in `blocks` (a folder's wait node) and from its folder's done node. So the search runs from each blocker and from the folder's wait node, to each node in `blocks` and to the folder's done node.
  - `folder create`, for each folder it makes: out from its wait node to each blocker and to its parent's wait node; in to its done node from each node in `blocks` and from its parent's done node. So the search runs from each blocker and from the parent's wait node, to each node in `blocks` and to the parent's done node.
  - A move: the new edges run from the moved task, or the moved folder's wait node, to its new folder's wait node, and from its new folder's done node to the moved task or the moved folder's done node. So the search runs from the new folder's wait node to the moved task or wait node, and from the moved task or done node to the new folder's done node. Folders the move makes as needed add their own edges, as `folder create` makes them. A move to the top level adds no edge, since the root has no nodes, and makes no search; a rename keeps the folder's parent, so the graph is the same but for a path, and the move is not checked at all. The edges a moved task or folder had already, its own blockers and those inside a moved folder, are not new: the containment check compares them with the new ancestors, and the search does not start from them.
  - `batch`: all its edges, searched as a whole.

  As a dependent, in `blocks`, a folder is its wait node; as a blocker, its done node. Each new edge is searched in turn, those from what the command changed first. The cycle reported is the new edge and the path found back, turned to start at the first of what the command changed in it, and collapsed to its tasks and folders as [operations.md](operations.md#cycles) says; no particular one is promised.
- **`doctor`'s cycles** are the strongly connected components of the whole graph with more than one node, each reported as one finding, whose `ids` is one cycle within it, found by the same search, starting at its lowest ID.

## Errors

- **Each call site decides what an OS error means**, because one errno means different things in different places: `ENOENT` on the root is `not-initialized`, on a folder in the input is `not-found`, and on a task file during a read is a file to skip. What no call site claims becomes `io`, with the path and the OS's message.
- **`internal` is for bugs koan detects as errors.** Panics are never recovered into it: reporting a failure would claim the command had no effect, which a panic mid-write cannot promise.

## Exit

- **One exit point.** `main` calls `run()`, which returns the exit code; nothing else calls `os.Exit`, so deferred cleanup always runs.
- **The envelope is written with one `Write`**, and stdout's `Close` is checked. Only then does koan exit `0`, `1`, or `2`, and only then is the stderr line written.
- **SIGPIPE is caught**, so a write to a closed pipe returns `EPIPE` and takes the same "could not write" path as a full disk, with exit `3`.
- **`debug.SetTraceback("crash")` is set at startup.** Go's default exit status for a panic is `2`, which is koan's code for a usage error, where nothing ran. With it, a panic ends by `SIGABRT`, exit 134: outcome unknown, as it should be.
- No handler is installed for `SIGINT` or `SIGTERM`: an interrupted koan dies by the signal, like any crash.

## Schemas

The files in `schemas/` are written by hand and are the contract. The binary contains no schema validator: input is checked by decoding and by `model`'s validation. Tests close the gap in both directions:

- every envelope any test produces is validated against its command's output schema;
- every input any test sends a command is also checked against that command's input schema, and the two must agree on whether it is valid. Rules between fields (two options that can't be combined) and the title rule are exempt: the schemas don't state them.
- the command table of the CLI, the commands in `ops`, and the input schemas must name the same commands and fields, but for the [CLI conveniences](cli.md#cli-conveniences).

## Testing

- **Unit tests** per package. `ops` tests run against a real temp directory, with `HOME` pointed at it and the clock fixed.
- **End-to-end tests** in `e2e/` run the built binary, with `HOME` set per test.
- **The lock**, in `e2e/` on both platforms: a second write, and a read, get `busy` while a write is held; every `task list` run while tasks and folders are moved returns every task; a holder killed with `SIGKILL` leaves nothing to clean up; 16 processes creating 50 tasks each produce 800 distinct IDs; two opposite `task block`s racing never both succeed.
- **Markers.** `marker bind` killed before each step leaves the old marker, the new one, or a `.koan/` without `marker.json`, perhaps with a temp file: the next `where` finds no marker or a whole one, the next `marker bind` completes it, and `marker unbind` removes it all. `where` and `context` from a nested directory, under a marker copied elsewhere, under one that can't be used with a usable one above it, after the folder's move (`stale-marker`) and its delete. `git status` in a bound worktree is clean, and `git worktree remove` succeeds without `--force`, run with git where CI has it; after a `marker bind` killed at each step, `git status` is clean but for the empty `.gitignore` of the one step design.md names.
- **Crashes.** For every write command and every step that changes the disk, the process is killed just before that step. Afterwards the next write must succeed, `doctor` must report nothing but the kinds [design.md](design.md#crashes-and-failures) names, and `repair`, then `repair` of the kinds it fixes only when asked by name, followed by `doctor`, must leave only findings a person has to resolve. The same steps with an injected errno must fail with `changed_paths` matching what took effect. A kill or failure between making a folder and writing its folder file leaves `missing-folder-file`, which `repair` fixes.
- **Case.** The case rule is the same on every platform, so its tests run everywhere: an alias refused as `case-mismatch` by every command that takes a folder path, a case-only `folder move` that keeps the folder's ID, and the input checks. On macOS, whose filesystem ignores case, the same tests go through the other paths: an `Lstat` that finds the folder in another case, and the case-only rename by plain `rename(2)`. The one test that needs a filesystem telling the cases apart, an existing pair where the exact match wins, finds out by creating a file and an `Lstat` of its name in upper case, and skips on macOS. The randomized test uses folder names that differ only in case on every platform.
- **CI** runs on Linux amd64 and macOS arm64: `gofmt`, `go vet`, `go test ./...`, a race-detector run over everything but `e2e/`, `staticcheck`, and `govulncheck`.
