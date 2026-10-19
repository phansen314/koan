# Deferred work

Work decided on but not built yet. Each item gets its own PR: agree the decisions, spec it (`spec/`, `schemas/`), implement with tests, review.

## `forget-folder` and `forget-task`

Turn a koan folder or task back into plain files, leaving the work itself in place. Designed together, since what happens to a forgotten task's koan data and notes is `forget-folder`'s core question.

- `forget-folder` removes the folder's `@folder.json` and the `@` from its name; `@folder.md` becomes `folder.md`, refused when one is there.
- It refuses a folder holding tasks (`has-tasks`) unless `--force`, and one holding folders (`has-children`).

## Worktree markers

- `koan bind` and `unbind`: a self-ignoring `.koan/` marker in a working directory, keyed by folder ID, holding the folder's ID, path, and dir.
- `koan where` resolves from a bound directory. Still open: whether a separate `context` command is needed.

## Shell completion

- `koan completion <shell>`, on cobra's scripts: paths folder by folder, `@` IDs of folders and tasks with their titles, material after `:`, and the bash colon workaround.
- Completion reads without the lock, best effort. That is an exception to "a read never sees part of a write", and the spec must name it.

## Retiring the shingi plugin

The rest of the shingi absorption is done and koan's skill covers it. The plugin can go once nothing uses shingi's `bind`, so this waits on worktree markers.

## Gates

Not designed yet. A gate would be a start or done task per folder, letting folders be ordered against each other. Before designing them, try `task create --blocks` (the new task blocks the tasks named) in real use, and see whether gates are needed at all. Until then, `@folder.json` stays `schema`, `id`, `title`, `extra`, with no `start` or `done`; and `batch` entries take no `blocks`: inside a batch every edge is a `blocked_by`, and making an existing task wait on a new one stays with `task create --blocks`.

## Cycles without Tarjan

`graph.components` finds the groups of tasks that block each other with a hand-written Tarjan's strongly connected components (about 65 lines). `Cycles` (for `doctor`) and `Why` (for `stuck`) both use it. Trees are small, so the groups can come from the `Path` already there instead: a task is in a cycle exactly when `Path` leads from one of its blockers back to it, and two such tasks share a group when each reaches the other. That costs a search per task, against Tarjan's single pass, which is nothing at koan's sizes. Do it with the tests as the check: `doctor`'s `cycle` findings, their order and their `ids`, and `why`'s `stuck`, must not change.

## Interactive picker

There is no interactive picker at the moment; the old one was left out of the rewrite and is being redesigned.
