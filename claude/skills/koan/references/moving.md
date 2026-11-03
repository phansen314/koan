# koan: moving, deleting and archiving

Read this before any `mv`, `delete` or `archive`, and to find work that was archived. The examples continue the skill's tree.

```sh
koan mv -t /work/api 48                          # its notes move with it; .result.moved[0].notes_path is the new path
koan mv -T /work/web/client /work/client         # -T: the new path; -t: the folder to move into
koan task list /work/client --state open,complete --fields title,state,claim   # before deleting: what goes
koan delete /work/client                         # refused without --contents: conflict, rule not-empty
koan delete /work/client --contents              # only once the user said so: .result.deleted, .result.removed, .result.released
```

- Always pass `-t` or `-T` to `mv`, so what you type never depends on what exists.
- koan paths are never globbed: a shell expands `/work/old/*` against the disk. Name each folder, or use completion or `$(koan pick)`.
- Moving never changes an edge, so it never changes readiness. Don't move a task someone has claimed: its notes path changes under them, and the move warns `moved-claimed`. A gate never moves.
- **Deleting is permanent**, and there is no undo but a backup. **Always confirm with the user before `delete`**, naming what goes: list the folder's tasks first, complete ones too. Never delete to tidy up on your own initiative. Prefer cancelling (the skill's Conventions) when the user means "won't do", and archiving when the work is finished: both keep the record.
- `delete` of a folder without `--contents` is refused (`not-empty`) when the folder holds tasks or folders beyond its gates, and without `--material` (`has-material`) when it holds the user's files: `.error.paths` lists them, such as `/work/client:docs`. **Never add `--contents` or `--material` on your own**: show the user what would go and ask. `--material` deletes their files for good.
- `deleted` is the subjects, and `removed` everything that left, their contents too. A delete removes every edge to what went, so what waited on it may become ready: `released` says which. Tell the user. Deleting a claimed task ends its claim without asking anyone, and `unclaimed` says which.

## Archiving finished work

Archiving moves complete work out of the tree into `~/koan-root/archived/`, kept as files with a JSON of its fields beside it, so the tree stays small. There is no unarchive.

```sh
koan archive /work/api                              # refused while anything under it is open: conflict, rule not-finished
koan update 48 --tags-add cancelled && koan complete 48 && koan archive 48   # one task: .result.archived[0].json
koan show 48                                        # not-found: .error.archived says where it went
```

- **Before cancelling a task**, check what waits on it, as the skill's Conventions say.
- **When the last open task in a folder is complete**, or the user says its work has shipped, offer to archive it. Never archive on your own initiative. A folder archives when every task under it is complete, gates included; otherwise `not-finished` lists the open tasks in `.error.ids`: ask the user, don't complete them to get past it.
- `archive` takes several tasks and folders, as one change. `.result.archived` says where each went, in a move's shape plus its `json`, and `.result.removed` is every ID that left the tree. They never resolve again: a command naming one fails `not-found`, saying where it went. A worktree bound to an archived folder binds nothing: mention it, so the user can remove the worktree.
- **Finding archived work**: search it with `rg '<text>' ~/koan-root/archived`. Each item keeps its ID in its name, such as `archived/work/@41_api/` for a folder or `archived/work/api/@48.md` for a task, with its fields in the `.json` beside it. Read archived files; never change them. To pick the work up again, make new tasks from what you find.
