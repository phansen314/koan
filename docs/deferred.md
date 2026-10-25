# koan: deferred

Work that has been decided on but isn't needed yet. Nothing here is built, and nothing here is behaviour: an item becomes behaviour when it is written into the rules doc, the PRD or the spec, in that order, and is then built from those. Anything that would change a rule goes to the rules doc when it is taken up.

- **Unarchive.** Bringing an archived item back, with its IDs. For now, work needed again is made again.
- **`--no-gates` on `task list`**, if gates in the list turn out to be noise.
- **Material in `pick`.** Material references, such as `@41:docs/plan.md`, as their own kind, through fzf's walker rooted at the folder's directory.
- **Opening notes from `pick`.** A key in the picker that opens the line's notes in `$EDITOR`, then returns to the picker.
- **`pick` for other shells.** `koan pick --bash` and `koan pick --zsh` are the only keybinding scripts for now; fish and powershell have none.
- **Tab handing off to `pick`.** Tab on an empty reference, or a bare `@`, opens `pick` with that argument's filter, and puts what was picked at the cursor. Bash only to start. `koan pick --bash` wraps koan's completion function so it opts in only for the length of each completion call, so an agent never inherits it. No `/dev/tty`, or no usable fzf, falls back to the plain candidates; a cancel puts nothing in. Until then, the key a person binds to `pick`'s widget is the way into it from a command line.
- **Completing a path from an ID.** `@40/api` completed folder by folder, as a path is. Completion offers paths and IDs only for now.
- **Completing material.** After the `:` in `where`'s subject, the entries of the folder's directory that aren't koan's, folder by folder, as `pick` would take them (Material in `pick`). For now it offers nothing there.
- **Lists on `archive`, `claim` and `release`**, as `complete` and `task delete` take them. Each needs a result shape of its own first: archive gives each item's archive and JSON paths, claim gives a claim record, and release's natural name, `released`, already means the tasks a change made ready.
- **An anchored path on `folder create`**, `@40/new`, as `folder mv` takes one for a rename. For now a new folder is made by its path from the root.
