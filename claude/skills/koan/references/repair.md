# koan: when the tree is damaged, and backups

Read this after a `pending-step` or `stuck-step` warning, a `corrupt` error, or the user renaming or dragging folders by hand, and before making a backup or after the user restores one. The examples continue the skill's tree.

```sh
koan doctor      # .result.findings: what is wrong, and whether repair can fix it; changes nothing
koan repair      # only when the user says so: fixes what loses nothing, and reports fixed and left
```

Run `koan doctor` once and summarize each finding's `kind`, `ids`, `paths` and `message`, which says what to do.

- Findings with `repairable: true` are what `koan repair` fixes: it moves a folder renamed or dragged by hand back, makes a missing directory, fixes a directory's case, moves notes back, and removes empty orphan notes. It never changes the database to match the files. **Never run `repair` unasked**: offer it, saying what it would change. `koan repair --kinds case-differs,misplaced-notes` limits it.
- `stuck-step`, `stray-entry`, `invalid-row` and `corrupt` are the user's: pass on the message. A stuck step needs a person to keep one copy, or move material out, before `repair` retries it.
- **Backups**: `koan backup <dir>` copies everything into an empty or new directory and checks the copy. Restoring is the user's: with nothing running koan, putting a backup in place of `~/koan-root`, then `koan doctor`. After a restore, a worktree bound since the backup may bind another folder, which reissued its ID, with only a `stale-marker` warning: when the user says they restored, check the folder `context --here` names is the one you expect.

```sh
koan backup ~/backups/koan-$(date +%F-%H%M%S)     # .result.clean is true when the copy checks out
```
