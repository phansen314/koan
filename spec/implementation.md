# koan implementation

How koan is built: the mechanisms behind what [design.md](design.md), [operations.md](operations.md), and [cli.md](cli.md) promise, and the decisions that are not obvious from the code. What the code makes obvious is not repeated here.

## Toolchain

- **Go**, standard library first. Go 1.25 or later, for `os.Root`'s `Link`, `Rename`, and `RemoveAll`.
- **Dependencies:** cobra, for the command line; `golang.org/x/sys/unix`, for the two system calls the standard library lacks (below); and `golang.org/x/mod`, to check the version a build is stamped with. A JSON Schema validator is a test-only dependency.
- **JSON is `encoding/json`**, with nothing wrapped around it. Files and inputs decode into structs with `DisallowUnknownFields`; `extra` is a `map[string]any`. Files are written with `MarshalIndent`, two spaces, and a trailing newline, with HTML escaping off.

## Packages

Everything but `main` is under `internal/`: koan's contract is the JSON its CLI prints, and a public Go API would be a second one.

```text
cmd/koan         main: signals, run(), os.Exit
internal/cli     cobra commands, command line to input, envelope to stdout, exit codes
internal/ops     the commands, grouped by kind: decode and check input, do the work, build the result
internal/store   root states, the lock wait, writing and moving files, the walk, the state file
internal/graph   readiness, the cycle check, why: pure functions, no I/O
internal/model   Task, the state file, names, IDs, timestamps, and their validation
internal/errs    error, warning, and finding types
internal/fsys    the only package that touches the disk; a real and a fault-injecting implementation
schemas/         the JSON Schemas, written by hand
e2e/             tests against the built binary
```

Imports go one way: `cli → ops → store → fsys`. `errs`, `model`, and `graph` are importable from anywhere: `errs` imports nothing of koan's, `model` only `errs`, and `graph` only `model`.

- **`ops` is the unit of behaviour.** Each command is a function from a decoded input and an environment to a result or an error. The CLI knows nothing about tasks: it builds the input object, calls the command, and prints what comes back.
- **The environment is passed in**: `HOME`, the clock, and the `fsys` to use. Nothing in `ops` or below reads the process environment, or the wall clock for a timestamp, which is what lets tests fix the time and inject faults. The one use of the real clock is the lock wait, which measures elapsed time.

## The filesystem layer

Two layers, both carried over from the earlier code, where they were the part hardest to get right.

- **`internal/fsys`** is a thin interface over the system calls: open a root, list, read, create a temp file, link, rename, flush, take the lock once. It comes over with the calls that served the config file and the picker removed.
- **`internal/store`** builds the sequences on it: waiting for the lock, publishing a file, moving, and removing a folder. Those functions (`Publish`, `Move`, `Discard`, and the lock wait) come over as they are; the rest of `store` is rewritten.

What the two provide together:

### One handle on the root

Every operation opens `~/koan-root` once, with `os.OpenRoot`, and does all its file work through that handle. The path is resolved one time, so a write cannot be split across two directories by a symlink repointed under it.

- **The path is opened with `/.` appended.** `os.OpenRoot` checks the type only after opening, so a FIFO at the path would block and a regular file would give an error with no errno. With `/.`, the kernel refuses both with `ENOTDIR` before opening anything. This also resolves the one symlink the design allows.
- **No symlink inside the tree is followed.** `os.Root` follows symlinks that stay inside the root, which koan must not. So an entry is opened in two steps: its parent folder through the `os.Root`, then the entry itself with `openat(2)` and `O_NOFOLLOW`, from `x/sys/unix`. A symlink fails with `ELOOP` in the one call, with no window between checking and opening.

### The lock

- **`flock` on the root directory itself**, taken on the descriptor from `root.Open(".")`. There is no lock file to create, clean up, or delete from under a holder, and every path to the directory reaches the same lock.
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

- **Moves never replace.** `move` and `move-folder` rename with `renameat2(RENAME_NOREPLACE)` on Linux and `renameatx_np(RENAME_EXCL)` on macOS. Plain `rename(2)` silently replaces an empty directory, so a check made beforehand would be the only guard.
- **A folder is removed by renaming it aside** to `.koan-tmp-<random>` in the root, then removing that. The rename is the single step that takes the folder out of the tree; a failure during the removal leaves a hidden folder that walks ignore and `doctor` reports.

### Fault injection

The second `fsys` implementation wraps the real one and, at a chosen call, returns a chosen errno or kills the process. It is the one seam for testing failures and crashes. Unit tests use it directly; the hook that puts it into the binary, for the end-to-end crash tests, is behind the build tag `e2e_hooks`, so a released binary cannot inject faults.

## The state file

`~/.config/koan/state.json` is outside the tree, so its directory is opened as a second root handle, and the file is read and published through that handle exactly as a file in the tree is. It is written only while the tree's lock is held; its own directory is never locked. `doctor` cannot see that directory, so each write of the state file first removes any `.koan-tmp-*` left there.

Every command reads the state file after taking the lock, never before.

## Walking the tree

There is no index on disk; a command that needs one builds it.

- **By name only.** The walk lists folders depth-first, each folder's entries sorted, and classifies every entry by its name and type without opening any file. It returns each task ID with its path, and the list of folders. Two paths for one ID is the `duplicate-id` error.
- **Task files are loaded when needed**, and each at most once per command. Finding a task by ID, or proving it absent, costs one walk and one file read. Only commands that need every task's fields load them all: `frontier`, `list`, `claim-next`, `delete` and `delete-folder` (to find references), `doctor`, and `migrate`.
- **Commands that need no walk don't make one:** `create` without `blocked_by`, `create-folder`, `release`, `version`, `info`. `init` walks, by name only, when it has to make a state file for a tree that is already there.
- **A file that vanishes** between the walk and its load can only be an outside change, since the command holds the lock. It is skipped; if it was the task the command was asked for, that is `not-found`.

## The graph

`internal/graph` works on tasks already loaded.

- **Readiness** of an open task is computed from all its blockers, not stopping at the first that blocks, so `blocking` is complete.
- **The cycle check** for `block`: adding "A is blocked by B" closes a cycle exactly when A is reachable from B along `blocked_by`. A breadth-first search from each new blocker, through open and done tasks alike, loads only the files it reaches. The path it finds is the cycle reported; no particular one is promised.
- **`doctor`'s cycles** are the strongly connected components of the whole graph with more than one task.

## Errors

- **Each call site decides what an OS error means**, because one errno means different things in different places: `ENOENT` on the root is `not-initialized`, on a folder in the input is `not-found`, and on a task file during a read is a file to skip. What no call site claims becomes `io`, with the path and the OS's message.
- **`internal` is for bugs koan detects as errors.** Panics are never recovered into it: reporting a failure would claim the command had no effect, which a panic mid-write cannot promise.

## Exit

- **One exit point.** `main` calls `run()`, which returns the exit code; nothing else calls `os.Exit`, so deferred cleanup always runs.
- **The envelope is written with one `Write`**, and stdout's `Close` is checked. Only then does koan exit `0`, `1`, or `2`, and only then is the stderr line written.
- **SIGPIPE is caught**, so a write to a closed pipe returns `EPIPE` and takes the same "could not write" path as a full disk, with a non-zero exit above `2`.
- **`debug.SetTraceback("crash")` is set at startup.** Go's default exit status for a panic is `2`, which is koan's code for a usage error, where nothing ran. With it, a panic ends by `SIGABRT`, exit 134: outcome unknown, as it should be.
- No handler is installed for `SIGINT` or `SIGTERM`: an interrupted koan dies by the signal, like any crash.

## Schemas

The files in `schemas/` are written by hand and are the contract. The binary contains no schema validator: input is checked by decoding and by `model`'s validation. Tests close the gap in both directions:

- every envelope any test produces is validated against its command's output schema;
- every input any test sends a command is also checked against that command's input schema, and the two must agree on whether it is valid. Rules between fields (two options that can't be combined) and the title rule are exempt: the schemas don't state them.
- the command table of the CLI, the commands in `ops`, and the input schemas must name the same commands and fields.

## Testing

- **Unit tests** per package. `ops` tests run against a real temp directory, with `HOME` pointed at it and the clock fixed.
- **End-to-end tests** in `e2e/` run the built binary, with `HOME` set per test.
- **The lock**, in `e2e/` on both platforms: a second write, and a read, get `busy` while a write is held; every `list` run while tasks and folders are moved returns every task; a holder killed with `SIGKILL` leaves nothing to clean up; 16 processes creating 50 tasks each produce 800 distinct IDs; two opposite `block`s racing never both succeed.
- **Crashes.** For every write command and every step that changes the disk, the process is killed just before that step. Afterwards the next write must succeed, `doctor` must report nothing but the kinds [design.md](design.md#crashes-and-failures) names, and `repair`, then `repair` of the kinds it fixes only when asked by name, followed by `doctor`, must leave only findings a person has to resolve. The same steps with an injected errno must fail with `changed_paths` matching what took effect.
- **CI** runs on Linux amd64 and macOS arm64: `gofmt`, `go vet`, `go test ./...`, a race-detector run over everything but `e2e/`, and `govulncheck`.
