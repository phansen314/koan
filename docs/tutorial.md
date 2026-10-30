# koan tutorial

This walks one project from its first task to its archive, by hand, so you can see what your agent does when it drives koan. It assumes you have done the README's [Quick start](../README.md#quick-start) and have `jq`.

The IDs below are from a new root. On yours they will differ: folders and tasks share one ID sequence, and every root counts from its own start.

Every command prints one line of JSON. The outputs shown here are real; long ones are described rather than printed. Pipe any of them to `jq` to read them comfortably.

## 1. A folder and two tasks

```sh
koan folder create /work/api --title 'API rework'
koan task create 'Design the endpoints' --folder /work/api --priority 2
koan task create 'Build them' --folder /work/api --blocked-by 3
```

The first command makes `/work` as well, since `/work/api` needs a parent: `/work` is `@1` and `/work/api` is `@2`. The tasks are `@3` and `@4`, and koan writes references to them as `@3 (/work/api)`: the ID, then the folder it sits in.

`--blocked-by 3` adds an **edge**: `@4` waits on `@3`, and isn't ready until `@3` is complete. A higher priority goes first; a task with none goes last.

## 2. What can be done now

The **frontier** is the tasks that are ready and that nobody has claimed, highest priority first:

```sh
koan frontier --fields ref,title
```

```json
{"ok":true,"result":{"tasks":[{"id":3,"ref":"@3 (/work/api)","title":"Design the endpoints"}],"total":1,"truncated":false},"warnings":[]}
```

`--fields` keeps the output small, which matters when an agent reads it. To see why a task isn't on the frontier, ask `why`:

```sh
koan why 4
```

```json
{"ok":true,"result":{"id":4,"readiness":"blocked","blockers":[{"id":3,"ref":"@3 (/work/api)","state":"open"}],"ready":[{"id":3,"ref":"@3 (/work/api)","claimed":false}]},"warnings":[]}
```

`blockers` is what `@4` waits on directly; `ready` is what you can work on now to move it.

## 3. Taking and finishing work

```sh
koan claim-next --extra '{"by":"me"}'
koan complete 3
```

`claim-next` claims the first task of the frontier, `@3`, and returns its whole record, so two workers asking at once are never handed the same task. `--extra` is any JSON you want kept with the claim, such as who took it.

`complete` answers with what changed:

```json
{"ok":true,"result":{"completed":[3],"unclaimed":[3],"released":[4]},"warnings":[]}
```

Completing `@3` ended its claim, and `released` says `@4` became ready.

## 4. Notes and material

```sh
koan show 4
```

`show` returns the whole task, and its `notes_path`, `~/koan-root/tree/@work/@api/@4.md`, is a plain Markdown file. Open `~/koan-root/tree` in your editor: each folder is a directory, named `@<name>`, with the folder's own notes in `@notes.md` and each task's in `@<id>.md`. Edit them as you like:

```sh
echo 'Move the API to v2.' > ~/koan-root/tree/@work/@api/@notes.md
```

Anything else you put in a folder's directory is your **material**: specs, drafts, research. koan never reads it, but it can name it:

```sh
echo 'Endpoints: /v2/users, /v2/orders' > ~/koan-root/tree/@work/@api/spec.md
koan where @2:spec.md
```

`where` gives `@2:spec.md (/work/api:spec.md)` and the file's full path, so an agent can find the spec from the folder's reference alone.

Rename and move folders with koan (`koan mv`), never by hand: the database records where each folder is. [Section 13](#13-when-the-tree-is-damaged) shows what happens if you do.

## 5. How commands answer

```sh
koan update 4 --priority 1
```

```json
{"ok":true,"result":{"updated":[4]},"warnings":[]}
```

- **A write returns what it changed, by ID; a read returns whole records.**
- **`task` and `folder` hold only `create` and `list`.** Every other command is its verb alone, for tasks and folders both: `koan show 4`, `koan update /work/api --title 'API v2'`, `koan mv 4 /work`.
- **A refusal names its rule:**

```sh
koan block 3 --blocked-by 4
```

```json
{"ok":false,"error":{"kind":"conflict","rule":"cycle","message":"@3 would wait on itself through @4","ids":[3,4],"paths":[],"problems":[{"field":"/blocked_by/0","reason":"@3 would wait on itself through @4"}],"archived":[]},"warnings":[]}
```

It exits 1, and prints the message on standard error too, for people. A command line koan can't read exits 2, with `kind` `usage`; for a mistyped option, it names the one you probably meant. Agents branch on `error.kind` and `rule`, never on the message.

## 6. Ordering folders with gates

Edges are only ever between tasks: where a task sits never orders it. To make whole folders wait on each other, every folder has two **gates**, tasks of its own that koan makes the first time an edge names them:

- `/work/api.start`, which the folder's work waits on;
- `/work/api.done`, which waits on the folder's work, and which whatever comes after the folder waits on.

In an edge, a folder means its done gate:

```sh
koan task create 'Announce it' --folder /work --blocked-by /work/api
koan block /work/api.done --blocked-by 3,4
koan why 5
```

The first command makes `@5`, and `/work/api.done` as `@6`. The second makes the done gate wait on both tasks. Now `why 5` traces it through:

```json
{"ok":true,"result":{"id":5,"readiness":"blocked","blockers":[{"id":6,"ref":"@6 (/work/api.done)","state":"open"}],"ready":[{"id":4,"ref":"@4 (/work/api)","claimed":false}]},"warnings":[]}
```

`@5` waits on the gate, and the gate on `@4`, so `@4` is what to finish. To see what a folder's work touches outside it:

```sh
koan export /work/api | jq '.result.external[].ref'
```

That prints `"@5 (/work)"`.

## 7. A whole plan in one call

An agent laying out a plan does it with `batch`, as one change: all of it goes in, or none. Labels let its tasks name each other before they have IDs:

```sh
koan batch -i - <<'EOF'
{"folders": [{"path": "/home/trip", "title": "Lisbon in May"}],
 "folder": "/home/trip",
 "join": ["/home/trip"],
 "tasks": [{"label": "$flights", "title": "Book flights"},
           {"title": "Book a hotel", "blocked_by": ["$flights"]}]}
EOF
```

`join` makes every task **join** the folder: each waits on its start gate, and its done gate waits on each. So the frontier under `/home` is the start gate alone, until you complete it to begin the trip:

```sh
koan frontier /home --fields ref,title
```

```json
{"ok":true,"result":{"tasks":[{"id":11,"ref":"@11 (/home/trip.start)","title":"Start"}],"total":1,"truncated":false},"warnings":[]}
```

## 8. Working from a worktree

Your code stays in its own repo. Bind a git worktree to the folder its work belongs to, and koan knows where you are when you're inside it:

```sh
koan bind /work/api ~/code/api-v2
cd ~/code/api-v2 && koan context --here
koan where --file ~/code/api-v2/src/main.go
```

`bind` writes a marker, `~/code/api-v2/.koan/marker.json`. `context --here` then briefs an agent working there: the folder's record, the folders above it, its notes (`Move the API to v2.`) and its open tasks, `@4` and the done gate. `where --file` says which folder any file in the worktree belongs to: `@2 (/work/api)`.

## 9. Several agents

A claim reserves a task. It never expires: `complete` or `unclaim` ends it.

```sh
koan claim 4 --extra '{"by":"agent-1"}'
koan claim 4
koan task list --claimed --fields title,claim
koan unclaim 4
```

The second claim is refused, with `conflict`, rule `claimed`. `task list --claimed` shows who holds what, with the `extra` each claim was made with.

## 10. History

Every change is recorded in the log: what was made, changed, moved, completed, claimed, deleted or archived, which tasks became ready, and which folders became finished.

```sh
koan log list --task 3 | jq -c '[.result.events[].kind]'
koan log list /work/api --since 2026-10-01T00:00:00Z --kinds completed
```

The first prints `["created","claimed","completed","unclaimed"]`. The second is what was finished under `/work/api` since the start of October. A folder is matched by path or ID as the log recorded it, so a renamed or deleted folder still finds its history, and `--after` a `seq` lets an agent catch up from where it last read.

To keep the database small, move old events out to JSON Lines files in `~/koan-root/log/`:

```sh
koan log rotate --before 2026-10-01T00:00:00Z
```

`log list` reads only the database, so cut well in the past, such as a month back, so no agent's saved place falls behind the cut.

## 11. Finishing and archiving

```sh
koan complete 4
koan complete /work/api.done
koan archive /work/api
koan show 3
```

Completing `@4` releases the done gate, `@6`, and completing the gate releases `@5`. With all its work complete, `/work/api` is finished, and `archive` moves it out of the tree, into `~/koan-root/archived/work/@2_api`, notes and material with it. Its tasks are gone from koan, but not lost: `show 3` is `not-found`, and says where `@3`'s notes went. `rg` searches the archive like any other files.

## 12. Backup and restore

`koan backup` copies the database, the tree, the archive and the log's rotated files into a new or empty directory, while koan keeps running, and checks the copy. Copying `koan.db` by hand while koan runs is not a backup.

```sh
koan backup ~/backups/koan-$(date +%F)
```

`.result.clean` is true when the copy checks out. To restore, put a backup in place of `~/koan-root`, and check it:

```sh
mv ~/koan-root ~/koan-root.before-restore
cp -R ~/backups/koan-$(date +%F) ~/koan-root
koan doctor
```

A backup carries the claims and the log of its moment, and a restore brings them back with it. So the IDs and log `seq` numbers issued after the backup will be issued again, and `koan log list --after` a `seq` from after it is `not-found`, until the restored root has issued that many again. The restored root's `log/` is the backup's: don't copy the replaced root's rotated files into it, since the restored root will issue their `seq` again.

## 13. When the tree is damaged

A crash, or a folder renamed or dragged by hand in your editor, can leave the tree on disk out of step with the database. Say you rename the trip's directory by hand:

```sh
mv ~/koan-root/tree/@home/@trip ~/koan-root/tree/@home/@lisbon
koan doctor
koan repair
```

`doctor` finds it and changes nothing. Every finding's message says what to do; here, that `@lisbon` is `@8 (/home/trip)`'s directory, and that `koan repair` moves it back. `repair` fixes what it can without losing anything, and reports what it left for you. To rename the folder, use `koan mv -T /home/trip /home/lisbon`.

## Where next

- The [koan skill](../claude/skills/koan/SKILL.md) is what your agent reads: the same commands, with the daily loop an agent follows.
- [docs/spec.md](spec.md) says exactly what every command takes and prints, and [docs/rules.md](rules.md) the rules every tree obeys.
