# koan: worktrees, markers and context

Read this before binding or unbinding a worktree, when `context`, `where` or `export` with `--here` or `--file` fails or warns, or when you need more than the skill's one rule for starting work in a worktree. The examples continue the skill's tree.

A **marker** binds a directory outside the tree, usually a git worktree, to a folder, so koan can tell which folder of work you are in from anywhere inside it. It is a `.koan/` directory there, which git ignores by itself.

```sh
koan bind @41 ~/code/api-v2                        # right after git worktree add; <dir> defaults to the working directory
cd ~/code/api-v2 && koan context --here            # the brief: the folder, the folders above it, its notes, its open tasks
koan where --file ~/code/api-v2/src/main.go        # which folder a file is in: .result.ref is @41 (/work/api)
koan mv -T /work/api /work/api2                    # a rename: the marker still binds @41, by ID
koan where --file ~/code/api-v2                    # still @41, with a stale-marker warning
koan bind @41 ~/code/api-v2                        # refreshes the hint: .result.replaced and .result.changed are true
koan mv -T /work/api2 /work/api
```

- **Starting work in a worktree, run `koan context --here` first**, and print it as is: a bare `koan context` briefs the root, never the worktree's folder. `.result.folder` is the folder, `.result.ancestors` the folders above it (top first), `.result.notes` its `@notes.md` whole, `.result.tasks` its own open tasks (not its subfolders'), and `.result.marker` the marker it came by. `tasks` is limited like a list: `--limit` (100 by default, `null` for all), with `total` and `truncated`. `koan context <folder>` briefs any folder, and `--file <path>` the folder a file is in. `export` and `where` take `--here` and `--file` too.
- **Check the marker is the worktree's own**: in a git worktree it should be `$(git rev-parse --show-toplevel)/.koan/marker.json`. A marker above the worktree means it was never bound and is borrowing an outer folder: say so, rather than working in that folder.
- **Bind worktrees from their top, never the checkout that holds them**: a marker binds every directory under it. `bind` refuses a checkout, a directory whose `.git` is a directory (`checkout`), and a directory bound to another live folder (`bound`); `--force` overrides either, so ask the user first. It refuses a `.koan/` holding files that aren't koan's (`foreign-files`), and any directory under `~/koan-root`.
- A marker copied or moved with its directory, or whose folder was deleted or archived, binds nothing: `where`, `context` and `export` fail `not-found` and say why. Binding again fixes a moved one. `koan unbind <dir>` removes one; `bind` and `unbind` both return `dir`, `marker` and `changed`, false when there was nothing to write or remove; removing the worktree removes it too.
- **As a worker**, run `context` and `where`, never `bind` or `unbind`: report a stale or broken marker to the coordinator.
