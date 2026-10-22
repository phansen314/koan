# koan: deferred

Work that has been decided on but isn't needed yet. Nothing here is built, and nothing here is behaviour: an item becomes behaviour when it is written into the rules doc, the PRD or the spec, in that order, and is then built from those. Anything that would change a rule goes to the rules doc when it is taken up.

- **A `join` shorthand.** `--join <folder>` on `task create`, and a `join` field in `batch`, writing the two edges of joining. Joining a folder whose done gate is already complete needs nothing new: the edge into the complete gate warns `complete-blocker`, as any such edge does.
- **Unarchive.** Bringing an archived item back, with its IDs. For now, work needed again is made again.
- **Completing references.** `koan completion` completes commands and options; completing their values is deferred: paths folder by folder, IDs with their titles, gates as `.start` and `.done`.
- **`--no-gates` on `task list`**, if gates in the list turn out to be noise.
- **Material in `pick`.** Material references, such as `@41:docs/plan.md`, as their own kind, through fzf's walker rooted at the folder's directory.
- **Opening notes from `pick`.** A key in the picker that opens the line's notes in `$EDITOR`, then returns to the picker.
- **`pick` for other shells.** `koan pick --bash` is the only keybinding script for now.
- **Tab handing off to `pick`.** Once completing references is built: Tab on an empty reference, or a bare `@`, opens `pick` with that argument's filter, and puts what was picked at the cursor. Bash only to start. `koan pick --bash` wraps koan's completion function so it opts in only for the length of each completion call, so an agent never inherits it. No `/dev/tty`, or no usable fzf, falls back to the plain candidates; a cancel puts nothing in. Until then, Alt-K is the way into `pick` from a command line.
- **Completing a path from an ID.** `@40/api` completed folder by folder, as a path is. Completing references starts with paths and IDs only.
