# koan rewrite: S0 spikes

Three throwaway experiments, run before any of koan is built, to check what the PRD assumes about SQLite, case-insensitive filesystems and speed. The experiment code was thrown away, as the plan says; this file keeps what it found. Every number below was measured on Linux. Nothing was measured on macOS: the section [Still to measure on macOS](#still-to-measure-on-macos) lists what is left and gives the exact steps.

## Where and how

| | |
| --- | --- |
| Machine | AMD Ryzen 9 7900X (12 cores), NVMe SSD |
| OS | Fedora 44, Linux 7.2.11 |
| Filesystem | btrfs on LUKS (`compress=zstd:1,ssd`), for every timing that touches the disk; tmpfs only where marked |
| Go | 1.26.9 |
| Driver | `modernc.org/sqlite` v1.60.1, SQLite 3.53.4; `golang.org/x/sys` v0.49.0 for `flock` |

- Each experiment was a Go program in a scratch module outside the repo. Each command was run as a fresh process, as a koan invocation is: it starts, opens the database, does its work, prints one line of JSON and exits. Times are wall-clock times for the whole process, measured by the parent that started it.
- Every connection set `busy_timeout = 5000`, `foreign_keys = ON`, `synchronous = FULL` and `fullfsync = ON`. Each write took a `flock` on the root directory and ran in `BEGIN IMMEDIATE`, as PRD Concurrency says.
- The page cache was warm for every timing. Dropping caches needs root, so cold-cache numbers weren't measured.

## The answers

| Question | Answer on Linux |
| --- | --- |
| Does a reader in WAL ever wait on a writer? | **Not on a writer's transaction**: a reader never waited while another process held an open `BEGIN IMMEDIATE` or the `flock`. **But readers do wait, on other connections closing.** When a process's connection is the last one open, SQLite checkpoints the WAL and deletes it as the connection closes, and the next connection to open rebuilds the WAL index. A reader that opens during either of these gets `SQLITE_BUSY` and waits. With `busy_timeout = 5000` it waits instead of failing: 1–54 ms in these tests. See 1.3. |
| What does one commit cost? | About **5 ms** for one `COMMIT` in a connection that is already open. A whole write as a fresh process costs **23 ms** with no filesystem step and **39 ms** with one. Almost all of it is waiting for `fsync`. See 1.2. |
| Case-only rename of a directory | **On a Linux case-insensitive filesystem, `rename("work", "Work")` succeeds but changes nothing**: the listing still says `work`. Renaming through a temporary name works. `stat` can't tell the two spellings apart there. Listing the parent always shows the true spelling. See 2. |
| Is 100 ms realistic? | **Yes, on this machine.** The slowest command, `task list` of all 4,223 open tasks, takes 58 ms (median). `frontier` takes 38 ms, `show` 4 ms and a write 23–39 ms. The margin is less than 2× for whole-tree reads, and there's no margin for a design that runs a query per task (113 ms). See 3. |

## 1. SQLite and the lock

### 1.1 The pragmas

All are set through the DSN (`_pragma=...`), then read back on a fresh connection:

| Pragma | Reads back |
| --- | --- |
| `journal_mode` | `wal`. It is stored in the file, so later connections don't need to set it again. |
| `synchronous` | `2` (FULL) |
| `fullfsync` | `1`. Accepted on Linux, but it does nothing here: SQLite uses `F_FULLFSYNC` only on macOS. |
| `checkpoint_fullfsync` | `0` |
| `busy_timeout` | `5000` |
| `foreign_keys` | `1` |
| `wal_autocheckpoint` | `1000` (the default) |

### 1.2 What a commit costs

These are 300 writes in one long-lived connection, each taking the `flock`, running `BEGIN IMMEDIATE`, inserting one row and committing:

| Setting | `COMMIT` alone, p50 / p95 | Whole write, p50 / p95 |
| --- | --- | --- |
| `synchronous = FULL` | 4.98 / 5.10 ms | 5.01 / 5.13 ms |
| `synchronous = NORMAL` (for comparison only) | 0.01 / 0.02 ms | 0.03 / 0.03 ms |
| `synchronous = OFF` (for comparison only) | 0.01 / 0.02 ms | 0.02 / 0.03 ms |
| FULL, plus one notes-file step (PRD Writes, step 7) | 5.07 / 5.27 ms | 20.1 / 20.9 ms |
| FULL plus a step, on tmpfs (no real flush) | 0.01 ms | 0.07 ms |

The filesystem step adds about 15 ms on its own. That is writing a temp file, `fsync`ing it, renaming it into place, `fsync`ing the directory, then deleting the `pending` row in a second committed transaction. That's three flushes of about 5 ms each.

**As whole processes**, the way koan runs: 100 runs each, means.

| Phase of one write process | Database closed when idle (the normal case) | Another connection kept open |
| --- | --- | --- |
| Open the database and set the pragmas | 0.37 ms | 0.25 ms |
| Take the `flock` | 0.01 ms | 0.01 ms |
| `BEGIN IMMEDIATE` … `COMMIT` | 14.8 ms | 9.9 ms |
| Close (the last connection checkpoints and deletes the WAL) | 5.1 ms | 0.03 ms |
| **Whole process** | **22.7 ms** (p50 22.8, p95 23.2) | 12.6 ms |

A write process usually has the database to itself. Its first commit has to create the WAL again, because the last process deleted it when it closed. Then, as the last connection, it checkpoints the WAL on close. Each of these costs one or more flushes.

In the speed spike (3), a `task block` costs 23.5 ms at p50 (200 runs: p95 29 ms, max 44 ms). A `task create` with its notes-file step costs 38.9 ms at p50 (p95 39.8 ms). A single `task block` run took 151 ms once, and 230 later runs never came close to that again. I haven't explained it.

### 1.3 Does a reader wait?

**While a writer holds its transaction: no.** One process took the `flock`, ran `BEGIN IMMEDIATE`, inserted 2,000 rows and held them uncommitted for 2 s, then held the `flock` alone for 2 s more. Readers in other processes, set to `busy_timeout = 0` so that any wait would show up as an error, ran during both phases. All of them succeeded, in 0.35–0.45 ms. They saw the old snapshot first, then the new one.

**While other connections open and close: yes.** In this hammer test, writer processes ran in a loop (open, `flock`, `BEGIN IMMEDIATE`, insert, `COMMIT`, close). At the same time, reader goroutines opened a fresh connection for each read: `BEGIN`, two `SELECT`s, `COMMIT`, close. Half of them used `busy_timeout = 0` and the other half 5000.

| Run (15 s or 10 s) | `busy_timeout = 0` readers | `busy_timeout = 5000` readers |
| --- | --- | --- |
| 4 writers, 4 + 4 readers | 108,367 reads, **no errors**, max 2.7 ms | 108,901 reads, max 2.1 ms |
| 1 writer, 1 + 1 readers | 145,939 reads, **75,398 failed** (`SQLITE_BUSY` 75,056, `SQLITE_BUSY_RECOVERY` 342) | 43,600 reads, p99 8.4 ms, **max 53.6 ms** |
| 0 writers, 1 + 1 readers | 70,307 reads, 6,551 failed | 12,869 reads, p99 3.4 ms, max 18.6 ms |
| 0 writers, 2 + 2 readers | 101,648 reads, 16,117 failed | 42,357 reads, p99 3.4 ms, max 33.5 ms |

The cause is what happens when connections open and close, not when they write. When the last connection to a WAL database closes, SQLite takes an exclusive lock, checkpoints the WAL and deletes it. The next connection to open then has to rebuild the WAL index (`SQLITE_BUSY_RECOVERY`). Readers do this to other readers too: the runs with no writers show it.

With 4 + 4 readers running, some connection was always open, so no connection was ever the last to close, and nothing waited. One person's machine is the opposite case. koan's processes mostly run alone, so the close-and-reopen happens on nearly every command.

`busy_timeout` turns the error into a short wait, and SQLite's busy handler retries. The longest wait seen was 54 ms. That is half the 100 ms budget, though it happened under a deliberately extreme load.

**Two things tried that don't fix it:**

- **`SQLITE_FCNTL_PERSIST_WAL`** (the driver exposes it as `FileControlPersistWAL`) keeps the WAL file when the last connection closes. It made things worse. With 1 writer and 1 + 1 readers, readers at `busy_timeout = 5000` waited up to **3.0 s** (p99 729 ms), and the writer finished only 22 writes in 10 s.
- **`SQLITE_DBCONFIG_NO_CKPT_ON_CLOSE`** would stop the checkpoint on close, but the driver doesn't expose `sqlite3_db_config` to callers. It uses it internally, but there's no public call for it. I didn't measure it.

### 1.4 The flock, and a writer that dies

| Measurement | Result |
| --- | --- |
| Uncontended open, `flock`, unlock and close of the root directory | 0.01 ms (p50); 1,000 runs |
| Try without waiting, while the lock is held (the reader's try in PRD Concurrency) | `EWOULDBLOCK` in 14 µs |
| Wait up to 5 s, while the lock is held | gives up after 5.0004 s |
| Writer killed with `SIGKILL` inside `BEGIN IMMEDIATE`, holding the `flock` | The kernel released the `flock`. A reader during the open transaction took 0.37 ms. The first reader after the kill took 15 ms (it rebuilt the WAL index); later readers took 0.4 ms. The next writer succeeded and none of the uncommitted rows appeared. |

`flock` on a directory file descriptor works on Linux. It hasn't been tried on macOS.

## 2. Case-insensitive filesystems

### What could run here

- **btrfs**, which is case-sensitive.
- **tmpfs with `casefold=utf8-12.1.0`, mounted in an unprivileged user namespace** (`unshare -rm`, no root needed), with the directory marked `+F` (`chattr +F`). Subdirectories inherit `+F`. This is Linux's case-insensitive, case-preserving mode, the same one ext4 and f2fs offer per directory.
- **Not ext4 casefold.** `mkfs.ext4 -O casefold` on an image file works without root, but mounting an ext4 image needs root. `fuse2fs` (e2fsprogs 1.47.3) refuses the image: "casefolding not supported". So ext4 itself wasn't tested.
- **Not APFS.** It needs a Mac.

### What happened

The test made `parent/work/@notes.md`, renamed `work` to `Work`, then listed `parent` and `stat`ed `work`, `Work` and `WORK`:

| Step | btrfs (case-sensitive) | tmpfs casefold (case-insensitive) |
| --- | --- | --- |
| Before: listing | `["work"]` | `["work"]` |
| Before: `stat` | only `work` exists | all three spellings succeed, with the same inode; `os.SameFile(work, Work)` is true; `FileInfo.Name()` echoes whichever spelling was asked for |
| `rename(work, Work)` | succeeds | **succeeds, returning 0** |
| After: listing | `["Work"]` | **`["work"]`: unchanged** |
| After: `stat` | only `Work` exists | all three spellings succeed, same inode |
| `mkdir work` beside `Work` | succeeds, giving two siblings | `EEXIST` |
| File `a.md` renamed to `A.md` | listing shows `A.md` | **listing still shows `a.md`** |
| Read `wORK/@NOTES.md` | `ENOENT` | succeeds |
| `rename(work, tmp)`, then `rename(tmp, Work)` | not tried | listing shows `["Work"]`, with the notes inside |

**Confirmed:** listing the parent shows the true spelling, and `stat` can't tell the spellings apart on a case-insensitive filesystem. It returns the same inode for both and echoes back the name it was given. So PRD Writes is right to judge "done when" by listing the parent and never by `stat`.

**New:** on Linux casefold, a case-only rename is a silent no-op. The kernel looks up `Work`, finds the same inode as `work`, and returns success without doing anything. It isn't a failure, so nothing reports an error. The listing check then finds the source still there and the destination missing, so the step is never done. The next write retries it, gets the same no-op, and the step stays pending, reported as `pending-step` on every result from then on. Renaming in two steps, through a temporary name, does produce the new spelling.

I tested tmpfs only. The no-op comes from the shared VFS rename path, so ext4 and f2fs casefold probably behave the same, but I haven't measured them. Whether APFS renames case-only in place, which is widely reported, is still to be confirmed (below).

## 3. Speed

### The data

The data used the PRD Storage schema exactly, with `ANALYZE` run after loading, and a tree on disk to match.

| | |
| --- | --- |
| Folders | 1,000, at most 5 deep (25 at the top, then 85% nested under an existing folder). 50% have both gates, 10% a done gate only. |
| Tasks | 10,000 live, gates included. Of the others, 10% sit at the root and the rest are spread over folders. 40% are open. |
| State | 4,223 open, 2,566 ready and unclaimed (the frontier), 77 claimed |
| Edges | 13,269 (1.3 per task). 90% of tasks in a folder with gates join it: they wait on its start gate and its done gate waits on them. Each also waits on 0–3 earlier members of its folder. Subfolders join their parent's gates. 30% of start gates wait on an earlier sibling's done gate, in phases. Half of the root tasks wait on another root task. There are no cycles, which was checked. |
| Other fields | 0–3 tags each, from 30 tags; 40% of plain tasks have a priority; gates have none; every task has a small `extra` |
| Disk | 11,001 files, 16 MB in all; a third of the notes files hold 1.1 KB |

### The commands

Each command printed every field in the order of the `task` schema, with `ref`, the edge ends with their `ref` and `state`, folder paths, `notes_path`, readiness and the claim. Each was run 30 times as a fresh process. The last column is the output size.

| Command | p50 | p95 | max | Output |
| --- | --- | --- | --- | --- |
| Baseline: start the process, open, one query, print | 2.2 ms | 2.6 ms | 2.7 ms | 38 B |
| `task frontier` (all 2,566, no limit) | 38.3 ms | 41.3 ms | 42.4 ms | 1.5 MB |
| `task frontier --limit 20` | 29.2 ms | 30.4 ms | 31.7 ms | 12 KB |
| `task list` (all 4,223 open, in tree order) | **57.8 ms** | 61.3 ms | 62.7 ms | 3.0 MB |
| `task list --limit 50` | 37.7 ms | 39.5 ms | 41.8 ms | 22 KB |
| `task show` (the task with the most edges) | 3.6 ms | 4.0 ms | 4.2 ms | 2.6 KB |
| `task block` (a cycle check walking the graph, one edge, one commit) | 23.5 ms | 28.6 ms | 151 ms (once; 44 ms in 200 later runs) | 0.6 KB |
| `task create` in a deep folder, blocked by its start gate, with the notes-file step | 38.9 ms | 39.8 ms | 43.8 ms | 0.6 KB |
| *Variant:* `frontier`, filling every derived field for all 10,000 tasks | 53.3 ms | 56.7 ms | 56.8 ms | 1.5 MB |
| *Variant:* `list`, the same | 66.5 ms | 70.9 ms | 72.2 ms | 3.0 MB |
| *Variant:* `frontier` with readiness found in SQL, then one task loaded at a time | **113 ms** | 115 ms | 117 ms | 1.5 MB |

Running the same commands pinned to one core (`taskset -c 0`) gave the same numbers within noise. For example, `list` took 56.3 ms and `frontier` 38.4 ms. The work runs on one thread.

### Where the time goes

This is one `frontier` run, measured by its phases:

| Phase | Time |
| --- | --- |
| Open | 0.8 ms |
| Load folder paths and every task row | 14.3 ms |
| Load every edge | 5.7 ms |
| Load every tag | 5.5 ms |
| Load claims | 0.04 ms |
| Work out readiness, or fill in every field | 16.4 ms (filling every field), or almost none (readiness only) |
| Sort and encode the JSON | about 5 ms |

- **A limit saves little.** Ordering the list needs every match. The frontier's readiness needs every open blocker's state, and `total` counts every match. So `--limit 20` still pays for loading the whole graph: about 25 ms. Readiness alone, done as one SQL query, took 6.6 ms, which suggests a limited `frontier` could fall to about 10 ms. I didn't measure a full command built that way.
- **One query per task doesn't fit.** Loading the 2,566 frontier tasks one at a time took 113 ms, over budget by itself.

### Is 100 ms realistic?

Yes, on this machine, with headroom that ranges from small to large:

- `show` and every other single-item read: about 4 ms.
- Writes: 23–39 ms on Linux, almost all of it waiting for flushes.
- Whole-tree reads (`frontier`, `list`): 29–58 ms. That is less than 2× headroom, and it doesn't include a reader's wait from 1.3, which was up to 54 ms under extreme load.

The budget is set on "the CI machine". GitHub-hosted runners are typically several times slower per core than a desktop Ryzen 7900X, but I haven't measured one. On such a runner, `task list` with no limit could pass 100 ms. There are two ways to keep it inside: load the graph in a few bulk queries and fill in fields only for the rows printed, as measured here, or narrow what gets loaded.

## Still to measure on macOS

None of these were run, and no numbers are given for them.

| What | Why it matters |
| --- | --- |
| Commit cost with `synchronous = FULL` and `fullfsync = ON` against `OFF` | `F_FULLFSYNC` flushes the drive's cache, so it costs much more than an `fsync`. A write makes several flushes (1.2), so on a Mac every write could cost several times its Linux figure. |
| A notes-file step with `F_FULLFSYNC` on the file and on the directory | PRD Writes `fsync`s directories. On macOS, Go's `(*os.File).Sync` issues `F_FULLFSYNC`, and it's untested whether that works, and what it costs, on a directory file descriptor on APFS. |
| Whether checkpoints flush with `F_FULLFSYNC` when `fullfsync = ON` and `checkpoint_fullfsync` is left at `0` | Checkpoints happen when each process closes (1.2). If they don't use `F_FULLFSYNC`, the database file may not be durable on the drive when a checkpoint returns. |
| A case-only rename of a directory on APFS (case-insensitive, the default), then the parent listed | The PRD assumes one `rename` does it. This is the question S0 asked. |
| `stat` on both spellings on APFS | to confirm it can't tell them apart there either |
| `flock` on the root directory's file descriptor | to confirm it works on macOS |
| The reader-wait hammer (1.3) and the speed benchmarks (3) | The spike code wasn't kept. Run them again from S3's storage and benchmark tests on a Mac. |

**Steps for the first four, on a Mac (any recent macOS on APFS, with Xcode Command Line Tools for `python3`):**

1. Save the script below as `s0_mac.py` anywhere outside a koan root.
2. Run `python3 -I s0_mac.py`. It works in a new `~/koan-s0-*` directory and doesn't touch `~/koan-root`.
3. Copy the output into this file, replacing the "not yet measured" entries. Note the Mac model, the macOS version and the disk.
4. Remove the directory it made (`rm -rf ~/koan-s0-*`).

The script uses Python's own SQLite rather than `modernc.org/sqlite`. The flushes it times are the same system calls either way. The script was checked on Linux, where it reproduces the 5 ms commit and the 10 ms notes step and reports `F_FULLFSYNC` as unavailable.

```python
import fcntl, os, sqlite3, tempfile, time

d = tempfile.mkdtemp(prefix="koan-s0-", dir=os.path.expanduser("~"))
print("dir", d, "| python sqlite", sqlite3.sqlite_version)

def ms(xs):
    xs = sorted(xs)
    return "p50=%.2f p95=%.2f max=%.2f ms" % (xs[len(xs) // 2] * 1e3, xs[int(len(xs) * .95)] * 1e3, xs[-1] * 1e3)

# 1. Commit cost: WAL, synchronous=FULL, with and without fullfsync.
for full in (0, 1):
    p = os.path.join(d, "c%d.db" % full)
    c = sqlite3.connect(p, isolation_level=None)
    c.execute("PRAGMA journal_mode=WAL"); c.execute("PRAGMA synchronous=FULL")
    c.execute("PRAGMA fullfsync=%d" % full); c.execute("PRAGMA busy_timeout=5000")
    c.execute("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
    xs = []
    for i in range(200):
        t = time.perf_counter()
        c.execute("BEGIN IMMEDIATE"); c.execute("INSERT INTO t(v) VALUES (?)", ("x",)); c.execute("COMMIT")
        xs.append(time.perf_counter() - t)
    print("commit fullfsync=%d:" % full, ms(xs), "| reads back", c.execute("PRAGMA fullfsync").fetchone()[0],
          "checkpoint_fullfsync", c.execute("PRAGMA checkpoint_fullfsync").fetchone()[0])
    c.close()

# 2. A notes-file step: temp file, flush, rename, flush the directory. fsync vs F_FULLFSYNC.
FULL = getattr(fcntl, "F_FULLFSYNC", None)
for name, sync in (("fsync", os.fsync), ("F_FULLFSYNC", (lambda fd: fcntl.fcntl(fd, FULL)) if FULL else None)):
    if sync is None:
        print(name, "not on this OS"); continue
    xs = []
    for i in range(200):
        t = time.perf_counter()
        tmp = os.path.join(d, ".tmp"); fd = os.open(tmp, os.O_CREAT | os.O_WRONLY | os.O_TRUNC, 0o644)
        os.write(fd, b"notes\n"); sync(fd); os.close(fd)
        os.rename(tmp, os.path.join(d, "@%d.md" % i))
        dfd = os.open(d, os.O_RDONLY); sync(dfd); os.close(dfd)
        xs.append(time.perf_counter() - t)
    print("notes step with %s on file and directory:" % name, ms(xs))

# 3. Case-only rename of a directory, then the parent listed and both spellings stat'ed.
p = os.path.join(d, "parent"); os.mkdir(p); os.mkdir(os.path.join(p, "work"))
open(os.path.join(p, "work", "@notes.md"), "w").write("hello\n")
def show(label):
    st = {n: (os.stat(os.path.join(p, n)).st_ino if os.path.exists(os.path.join(p, n)) else None) for n in ("work", "Work")}
    print("%-14s listing=%s stat inodes=%s" % (label, sorted(os.listdir(p)), st))
show("before")
os.rename(os.path.join(p, "work"), os.path.join(p, "Work")); show("after rename")
try:
    os.mkdir(os.path.join(p, "work")); print("mkdir work beside Work: allowed")
except FileExistsError:
    print("mkdir work beside Work: EEXIST")
```

**For the `flock`:** run `python3 -c "import fcntl,os; fd=os.open(os.path.expanduser('~'), os.O_RDONLY); fcntl.flock(fd, fcntl.LOCK_EX|fcntl.LOCK_NB); print('flock on a directory: ok')"`.

## What this calls into question in the docs

These are open questions, not decisions. Each one needs a change to the docs, or a confirmation that the docs stand, before the slice that builds it.

1. **PRD Concurrency: "reads never wait", and "takes no lock beyond the brief try".** Readers don't wait on writers. But a reader does wait whenever another connection is closing as the last one, which means a checkpoint and a deleted WAL, or is rebuilding the WAL index (1.3). koan's short-lived processes cause both constantly. `busy_timeout` makes this a wait rather than an error (up to 54 ms seen), but the PRD's statement is false as written. It's also not true that readers take no lock: a reader that closes last takes SQLite's exclusive lock and writes the database file. The PRD needs to say either that reads can wait briefly, or how koan stops it. Persistent WAL made it worse (1.3), and turning off the checkpoint on close isn't exposed by the driver. The PRD's Testing row for Concurrency ("every read sees a committed state") still holds.
2. **The spec has no error for a reader that waits out `busy_timeout`.** `busy` is defined as "the lock was held for 5 seconds", meaning koan's `flock`, which readers don't take. If a reader's SQLite busy wait runs out, no error kind names it. It never came near 5 s with default settings, but with persistent WAL it reached 3 s.
3. **PRD Writes, "Move notes or a directory: one rename", against a case-insensitive Linux filesystem.** On Linux casefold, a case-only rename returns success and does nothing (2). The rules doc (Case) says a case-only rename "works on both filesystems", and the PRD Non-goals exclude only Windows. A koan root inside a `+F` directory on ext4, f2fs or tmpfs is supported by the docs as written. Its `folder move /work --to /Work` would commit, and the step would then stay pending forever, reported as `pending-step` on every write. Neither doc says whether such roots are supported, or how the step should be carried out (a rename through a temporary name works). APFS may well need the same answer: that is still to measure.
4. **PRD Concurrency, "Commits are durable".** `fullfsync = ON` is set, but it's unconfirmed whether SQLite's checkpoints use `F_FULLFSYNC` while `checkpoint_fullfsync` stays at `0`. They read back `0` here. Every koan process checkpoints when it closes (1.2), so this decides whether the database file reaches the drive. The PRD names only `fullfsync`. It also says to `fsync` directories, without saying whether that must be `F_FULLFSYNC` on macOS. Go's `Sync` is, but `unix.Fsync` isn't.
5. **PRD Goals, the 100 ms budget, on macOS and on the CI machine.** Each write makes several drive flushes. That's about 23 ms for a plain write and 39 ms for one with a notes step on Linux, where one flush costs about 5 ms. On macOS each flush is an `F_FULLFSYNC`, which hasn't been measured. If one costs much more than 5 ms, a `task create` could near or pass 100 ms. Whole-tree reads have less than 2× headroom on a fast desktop, and the CI machine isn't named yet. The PRD's own statement holds: "the CI machine, whose OS is named beside the benchmarks". The benchmark slice should fix the machine early.
6. **The budget for reads with no `--limit`.** `task list` with every field prints 3.0 MB for 4,223 open tasks, and `frontier` prints 1.5 MB. Both fit in 100 ms here, and nothing in the docs is wrong. It's worth noting, though, that a limit saves only about a third of the time (3), because ordering, readiness and `total` all need the whole graph.

Nothing measured contradicts the rules doc's Case rules themselves. Names are ASCII by their pattern, so `NOCASE` covers them (PRD Invariants, I4), and listing the parent is confirmed as the right test.

## What was decided

After S0, the questions above were settled in the docs: the rules doc first, then the PRD, then the spec and its schemas.

| Question | Decision | Where |
| --- | --- | --- |
| 1. Readers wait | Accepted, and said honestly. Reads never wait on a writer or on koan's lock, but SQLite may make them wait briefly while it tidies its own files; `busy_timeout` makes that a wait. `doctor` takes no koan lock. Turning off the checkpoint on close may be tried in S3 as an optimization only, and changes no rule. | PRD Concurrency; PRD Doctor and repair |
| 2. No error for a reader kept busy | `busy` now covers it: the write lock, or the database itself, stayed busy for 5 seconds. No new kind. | PRD Concurrency and Errors; spec Kinds |
| 3. Case-only rename on Linux casefold | Every case-only rename, on every filesystem, goes `@work` → `@.case-<seq>` → `@Work`. The step records all three names and resumes from whichever the listing shows. Done is judged by the exact spelling in the listing. `@.case-<seq>` is one of koan's own names while its step is pending; doctor shows it as `pending-step`, and `case-differs` repair renames the same way. The root may be on any local filesystem, whether it ignores case or not. | Rules Case; PRD Non-goals, Filesystem steps, Doctor and repair, Testing |
| 4. Flushes on macOS | `checkpoint_fullfsync = ON` as well as `fullfsync = ON`, and directories flushed with `F_FULLFSYNC`, accepting whatever that costs on macOS. The sync pragmas are set on every open, since SQLite doesn't store them. | PRD Concurrency; PRD Choices |
| 5. The budget | No change to durability: every commit stays `synchronous = FULL`. Running the delete of finished steps' rows with `NORMAL` was considered and dropped, since each process's checkpoint on close flushes the WAL anyway, so the saving is likely nil, and a lost delete means redoing a step after the tree may have changed. S3 may measure it and propose it again with numbers. CI enforces the budget on the Linux runner. macOS runs the same benchmarks and reports them, but doesn't fail the build until its numbers are measured. | PRD Testing |
| 6. Output size | `task list`, `task frontier` and `folder list` return at most 100 results unless `--limit` says otherwise; `--limit null` returns every match. `total` and `truncated` already say when there is more. | PRD Commands; spec and schemas `task-list`, `task-frontier`, `folder-list` |

The 151 ms outlier needs no action yet. The benchmarks in S8 will show whether it comes back.
