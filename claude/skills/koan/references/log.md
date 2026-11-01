# koan: catching up with the log

Read this to find out what changed since you last looked, or to answer a question about a task's or a folder's history. The examples continue the skill's tree.

Every change koan makes is recorded in the log as events, oldest first: what was made, changed, moved, completed, claimed, deleted or archived, which tasks became ready or waited again (`released`, `reblocked`), and which folders became finished. Reads are never recorded, nor is anything a change left as it was, nor notes edits.

```sh
koan log list --after 29 --limit 50                         # what happened since the last event you saw: .result.events, .result.last_seq
koan log list /work/api --since 2026-10-20T00:00:00Z --kinds completed,reopened   # under a path, as it was then
koan log list --task 42 | jq -c '[.result.events[].kind]'   # one task's whole history, even once deleted or archived
```

- **Catching up**: keep `.result.last_seq`, and next time ask `--after` it. When `truncated` is true, ask again `--after` the new `last_seq` until it is false. `--since` (UTC, `YYYY-MM-DDTHH:MM:SSZ`, inclusive) is for questions about time, such as what happened this week.
- Each event has `seq`, `change` (shared by every event of one command's change), `at`, `command`, `kind`, `id`, `folder` (its ID and path *then*) and `data`. An event is about a folder when `id` is set and `.folder.id` is it, about a task when `id` is set and differs, and about the root or the log when `id` is null. `updated` gives each changed field `before` and `after`; `deleted` and `archived` give the `ref` and `title` of what left.
- **The folder and `--task` are matched as written**, never resolved: a path finds what happened under it while it had that path, byte for byte, even after a rename; an ID follows a folder through renames and moves. Neither is ever `not-found`: no match is an empty list. `--task` takes a plain ID, never a gate reference.
- `--after` a `seq` the root never issued is `not-found`: the root was restored from a backup or replaced. Tell the user, and start again from `koan info`'s `.result.last_seq`. This catches a restore only until the root has issued that many events again, so the user tells you when they restore.
- A `rotated` event means older events were moved to the file in its `data`, under `~/koan-root/log/`. `log list` reads only the table, so it never returns them. Back after a long gap, ask once `koan log list --after <last_seq> --limit 1` with no filter: `seq` has no gap in the table, so a first `seq` above `last_seq` + 1 means rotation moved what lies between, into the files of `log/` whose names cover that range. Then ask your filtered question as usual. Read them with `jq` if you need them; never change or move them.
- **Rotating is the user's call.** Never run `koan log rotate` unasked: it loses nothing, but how long history stays in the table is theirs to choose.
