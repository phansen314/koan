# Running several agents on one tree

How a coordinator that starts its own workers can use koan so that no two of them collide. Nothing here is enforced by koan, and nothing here needs it to be.

## What koan guarantees, and what it leaves to you

koan guarantees that its own commands are safe to run at the same time:

- **Commands never interleave.** A write holds the tree's lock alone, and a read waits for it, so a read never sees part of a write. Every file is replaced whole or not at all.
- **Merges keep both sides.** Two `task update`s that add different tags or `extra` keys both take effect.
- **`task claim` hands a task to one caller.** A second claim of the same task fails with `conflict`, and `task claim-next` never returns the same task twice.

It leaves two things open:

- **A claim is advisory.** It refuses other claims and hides the task from `task frontier`, and nothing else: `task update`, `task done`, `task move`, and `task delete` work on a claimed task for any caller. koan cannot tell callers apart, so it cannot do more ([design](../spec/design.md#claims)).
- **Notes are plain files.** A task's `@<id>.md` is edited with any editor, and nothing stops two editors from overwriting each other.

The coordinator closes both, by deciding who may do what.

## The rule

**The coordinator is the only writer of the tree's structure. Each worker is given one task, and writes only that task's notes.**

| Action | Coordinator | Worker |
|---|---|---|
| Read: `task show`, `task list`, `task frontier`, `task why`, `folder show`, `folder list`, `folder why`, `where` | yes | yes |
| `task claim-next`, `task claim`, `task release` | yes | no |
| `task create`, `batch`, `task update`, `task done`, `task reopen` | yes | no |
| `task block`, `task unblock`, `task move`, `task delete` | yes | no |
| `folder create`, `folder update`, `folder block`, `folder unblock`, `folder move`, `folder delete` | yes | no |
| Edit the `@<id>.md` of its own task | no | yes |
| Edit any other file in the tree | no | no |

With one party making every claim, a claim is no longer a request that others might ignore. It is the coordinator's own record of what it has handed out.

## The loop

```sh
# 1. Take the next task, recording who gets it.
koan task claim-next --folder /proj --extra '{"worker":"w3"}'
#    .result.task is the task, with its id and notes_path; null means nothing is ready.

# 2. Start worker w3 with the task's id, title, and notes_path.
#    The worker reads what it likes, edits that one .md, and reports back.

# 3. Record the outcome, once koan task show 42 says it is still ready.
koan task done 42                 # finished: this also ends the claim
koan task release 42              # not finished: the task goes back to the frontier

# 4. Add whatever the worker found, phased work laid out whole.
koan batch -i followups.json
```

- **Workers report, the coordinator writes.** A worker that finds follow-up work, or wants a tag or a priority changed, says so in its report. It does not run the command itself.
- **Start as many workers as there are tasks claimed.** Each `task claim-next` returns a different task, so the workers' notes files never overlap.
- **Don't move or delete a task that is out.** `task move` and `folder move` change its `notes_path` under the worker.
- **Check a task before marking it done.** Ordering between folders is continuous: a task added to a folder that the task's folder waits on, a reopen, or a `folder block` makes a task that was handed out blocked again, and koan does not say so to anyone. `koan task show 42` before step 3: when `readiness` is `blocked`, `inherited` says which folder holds it back. Tell the user, and decide whether its work stands.
- **Lay out phased work in one `batch`.** An empty folder is done, so a task waiting on a phase whose tasks aren't made yet is ready, and `task claim-next` hands it out. A batch makes the folders, their `blocked_by`, and their tasks in one write, so nothing is ever ready early: `{"folders": [{"folder": "/proj/phase1"}, {"folder": "/proj/phase2", "blocked_by": ["/proj/phase1"]}], "tasks": [...]}`.

## Seeing what is out

```sh
koan task list --claimed --fields id,title,claim
```

Each task's `claim` holds `claimed_at` and the `extra` given at step 1, so this is the table of who has what and since when.

- **A worker that ended without reporting** leaves its claim behind, since claims never expire. Look at what it left in the notes, then release the task or mark it done.
- **A `task claim-next` whose outcome is unclear** (the coordinator was killed, or lost the output) may have claimed a task nobody was given. The same listing shows it: a claim with no live worker.

## Enforcing the rule

The rule above holds as long as workers follow their instructions. To make it hold when one doesn't, enforce it where the workers are run, since that layer knows which worker is which:

- **Restrict what a worker can write** to the one notes file it was given, and the koan commands it can run to the read commands. Most agent harnesses can limit a worker's tools and paths when starting it.
- **Keep the tree in git, and commit as each task completes.** An overwritten note can be recovered, and a change outside the tasks that were out shows up in `git status` before the commit.

## Example: Claude Code subagents

In Claude Code the coordinator is the main session, and each worker is a subagent it starts. A subagent's definition can carry hooks that run only while that subagent is active, which is where the rule is enforced.

**The worker**, in `.claude/agents/koan-worker.md`:

```markdown
---
name: koan-worker
description: Does one koan task it is given, and writes its findings to that task's notes.
tools: Read, Grep, Glob, Edit, Write, Bash
hooks:
  PreToolUse:
    - matcher: "Edit|Write|Bash"
      hooks:
        - type: command
          command: "$HOME/.claude/hooks/koan-worker-guard.sh"
---

You are given one koan task: its ID, its title, and the path of its notes file.

- Do the task. Write what you did and what you found to the notes file you were given.
- In `~/koan-root`, that notes file is the only file you may change.
- You may run `koan task show`, `koan task list`, `koan task frontier`, `koan task why`, `koan folder show`, `koan folder list`, `koan folder why`, and `koan where`. Run no other koan command.
- When something else in the tree should change (a new task, a tag, a blocker, this task being done), say so in your final report. The coordinator makes the change.
```

**The guard**, in `~/.claude/hooks/koan-worker-guard.sh`, executable, and needing `jq`. A hook that exits with status 2 blocks the call, and the worker is shown what it wrote to stderr.

```sh
#!/bin/sh
# PreToolUse guard for koan workers. Exit 2 blocks the call and shows stderr to the worker.
input=$(cat)
deny() { echo "koan-worker-guard: $1" >&2; exit 2; }
field() { printf '%s' "$input" | jq -r "$1"; }

root="$HOME/koan-root"
real=$(cd "$root" && pwd -P) || deny "cannot find $root"

case "$(field .tool_name)" in
Bash)
    cmd=$(field .tool_input.command)
    case "$cmd" in
    *koan-root*|*"$real"*) deny "use Read and Edit for files in the tree, not the shell" ;;
    esac
    # Each koan command named, with its group's command after task or folder.
    subs=$(printf '%s' "$cmd" | grep -Eo '(^|[^[:alnum:]_-])koan +[a-z-]+( +[a-z-]+)?' |
        awk '{ print ($2 == "task" || $2 == "folder") ? $2 " " $3 : $2 }')
    IFS='
'
    for sub in $subs; do
        case "$sub" in
        "task show"|"task list"|"task frontier"|"task why"|"folder show"|"folder list"|"folder why"|where) ;;
        *) deny "workers may not run 'koan $sub': report it to the coordinator" ;;
        esac
    done
    ;;
Edit|Write)
    file=$(field .tool_input.file_path)
    case "$file" in
    "$root"/*) rel=${file#"$root"} ;;
    "$real"/*) rel=${file#"$real"} ;;
    *) exit 0 ;;
    esac
    case "$rel" in
    */@[0-9]*.md) ;;
    *) deny "only a task's notes may be edited in the tree" ;;
    esac
    id=$(basename "$rel" .md)
    id=${id#@}
    koan task show "$id" 2>/dev/null |
        jq -e --arg a "$root$rel" --arg b "$real$rel" \
            '.result.task | .claim != null and (.notes_path == $a or .notes_path == $b)' >/dev/null ||
        deny "$file is not the notes of a claimed task"
    ;;
esac
exit 0
```

It lets a worker do anything outside the tree. Inside it, it allows an edit only to the notes of a task that is claimed right now, and it allows only koan's read commands that look at tasks and folders.

**The coordinator** follows [the loop](#the-loop) and starts one worker per claimed task, with a prompt like:

```text
Use the koan-worker agent for task 42, "Book flights".
Its notes file is /home/paul/koan-root/@proj/@travel/@42.md.
```

Two limits to know about:

- **It checks that a task is out, not that it is this worker's.** A hook is told which subagent is calling, but nothing tells it which task that subagent was given. So a worker is kept away from task files, from unclaimed tasks, and from koan's write commands, but not from the notes of another worker's task.
- **The shell check catches mistakes, not intent.** It matches the text of the command, so a command that builds the path or the subcommand some other way gets past it. Git remains the backstop.

## What this does not cover

- **A person editing the notes of a task that is out.** The listing above says which to leave alone.
- **Two coordinators on one tree.** Claims still keep them from taking the same task, but each must then leave alone every task it did not claim itself, and neither can enforce that on the other.
