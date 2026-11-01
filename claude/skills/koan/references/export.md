# koan: export

Read this before running `koan export`. The examples continue the skill's tree.

**`koan export` is for tools, not for reading.** `koan export /work/api` (or `--file`, or `--here` in a worktree, as for `context`; with none of them, the root) gives the folder's whole subtree in one snapshot: its folders, every task under it, open and complete, gates included, as `tasks`; the tasks outside it at the other end of an edge as `external`; and the `edges` as `{task, blocker}`. It has no `--limit` and no `--fields`, and at 10,000 tasks it is megabytes, so never print it into your context: write it to a file, or pipe it into the tool that draws the graph. To read the tree, use `task list` with `--limit` and `--fields`.

```sh
koan export /work/api > api.json   # the whole subtree, into a file
jq .ok api.json                     # true; when false, .error says why
```
