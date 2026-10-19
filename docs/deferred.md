# Deferred work

Work decided on but not built yet. Each item gets its own PR: agree the decisions, spec it (`spec/`, `schemas/`), implement with tests, review.

## `forget-folder` and `forget-task`

Turn a koan folder or task back into plain files, leaving the work itself in place. Designed together, since what happens to a forgotten task's koan data and notes is `forget-folder`'s core question.

- `forget-folder` removes the folder's `@folder.json` and the `@` from its name; `@folder.md` becomes `folder.md`, refused when one is there.
- It refuses a folder holding tasks (`has-tasks`) unless `--force`, and one holding folders (`has-children`).

## Unarchive

`folder archive` and `task archive` are one way. Bringing an item back would be safe for IDs, which the archive keeps and the counter never issues again: rename it back (to a `--to`, when its path is taken), drop its index line, and report `blocked_by` entries in it that name what is gone. The links that pointed at it from the tree were removed when it was archived, and are not restored. Only if remaking work from the archive turns out to be a burden in real use.

## Shell completion

- `koan completion <shell>`, on cobra's scripts: paths folder by folder, `@` IDs of folders and tasks with their titles, material after `:`, and the bash colon workaround.
- Completion reads without the lock, best effort. That is an exception to "a read never sees part of a write", and the spec must name it.

## Retiring the shingi plugin

The rest of the shingi absorption is done, worktree markers and `context` included, and koan's skill covers it. What is left is retiring the plugin: moving any `.shingi/` markers still in use to `koan marker bind`, and uninstalling it.

## Latched ordering

Ordering between folders is continuous: "B after A" holds whenever A has open work, work added after B started included. A latched order would let a folder count as started once any task in it has been claimed or completed (derivable from what is stored), so that later additions upstream don't hold it back. Only if continuous ordering gets in the way in real use.

## Splice on delete

If A blocks B and B blocks C, deleting B makes C ready while A is still open. Folders make it more visible: deleting a middle phase frees the next. The option: on delete, give each dependent the deleted task's or folder's blockers. It would apply to tasks and folders alike.

## Reporting what a write blocks again

A task created or moved into a done folder, a task reopened, or a `task block` or `folder block`, can make open tasks that were ready blocked again, claimed ones included. A result field (`reblocked`), and a warning for a claimed one, were considered and left out:

- Finding them needs a reverse lookup, everything that names the folder or a folder above it, which means loading every task file on `task create`, `task move`, and `task reopen`, where today they load a few.
- The warning would reach the caller that made the change, not the holder of the claim, who is the one that needs it.

Meanwhile the coordinator checks `task show` (`readiness`, `inherited`) before marking a claimed task done ([coordination.md](coordination.md)).

## Cycles without Tarjan

`graph.components` finds the groups of tasks that block each other with a hand-written Tarjan's strongly connected components (about 65 lines). `Cycles` (for `doctor`) and `Why` (for `stuck`) both use it. Trees are small, so the groups can come from the `Path` already there instead: a task is in a cycle exactly when `Path` leads from one of its blockers back to it, and two such tasks share a group when each reaches the other. That costs a search per task, against Tarjan's single pass, which is nothing at koan's sizes. With folders, the groups are of graph nodes, a task's or a folder's wait or done node, collapsed to tasks and folders as a cycle error collapses them; the same holds for them. Do it with the tests as the check: `doctor`'s `cycle` findings, their order and their `ids`, and `why`'s `stuck`, must not change.

## Interactive picker

There is no interactive picker at the moment; the old one was left out of the rewrite and is being redesigned.
