#!/usr/bin/env bash
# Smoke test: runs the ftask binary from a shell, as a user would, in a
# throwaway home, so it never touches your real config or tasks.
#
#   scripts/smoke.sh                 # builds ftask from this repo
#   FTASK=~/go/bin/ftask scripts/smoke.sh   # tests that binary instead
#
# Needs jq. Exits 0 when every check passes, 1 otherwise.
set -uo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Build before HOME changes: go keeps its caches under the real home.
if [[ -z ${FTASK:-} ]]; then
	FTASK=$tmp/ftask
	(cd "$repo" && go build -o "$FTASK" ./cmd/ftask) || { echo "build failed"; exit 1; }
fi
command -v jq >/dev/null || { echo "smoke.sh needs jq"; exit 1; }

export HOME=$tmp/home XDG_CONFIG_HOME=$tmp/home/.config
mkdir -p "$HOME"
cd "$HOME"

pass=0 fail=0
out=""

# check DESC WANT_EXIT JQ_EXPR -- ARGS...: runs ftask with ARGS (stdin passes
# through), then checks the exit code, that the output is exactly one line,
# and that JQ_EXPR is true of it. The output is left in $out.
check() {
	local desc=$1 want=$2 expr=$3
	shift 4
	local code=0
	out=$("$FTASK" "$@") || code=$?
	local lines
	lines=$(printf '%s\n' "$out" | wc -l)
	if [[ $code -ne $want ]]; then
		why="exit $code, want $want"
	elif [[ $lines -ne 1 ]]; then
		why="$lines lines of output, want 1"
	elif ! jq -e "$expr" >/dev/null 2>&1 <<<"$out"; then
		why="not true: $expr"
	else
		pass=$((pass + 1))
		printf 'ok    %s\n' "$desc"
		return
	fi
	fail=$((fail + 1))
	printf 'FAIL  %s\n      ftask %s\n      %s\n      %s\n' "$desc" "$*" "$why" "$out"
}

# expect DESC CONDITION: a check of something besides ftask's output.
expect() {
	if eval "$2"; then
		pass=$((pass + 1))
		printf 'ok    %s\n' "$1"
	else
		fail=$((fail + 1))
		printf 'FAIL  %s\n      not true: %s\n' "$1" "$2"
	fi
}

echo "== before init"
check "version" 0 '.ok and (.result.version | type == "string")' -- version
check "info: nothing set up" 0 '.result.usable == false' -- info
check "show: not initialized" 1 '.error.kind == "not-initialized" and .error.details.missing == "config"' -- show 1

echo "== init"
check "init ~/tasks" 0 '.result == {root: "'"$HOME"'/tasks", action: "created", last_id: 0}' -- init '~/tasks'
check "info: usable" 0 '.result.usable' -- info
check "second init refused" 1 '.error.details.rule == "config-exists"' -- init '~/other'

echo "== folders"
check "missing parent without -p" 1 '.error.kind == "not-found" and .error.details.folders == ["/proj"]' -- create-folder /proj/travel
check "-p creates the chain" 0 '.result.created == ["/proj", "/proj/travel"]' -- create-folder -p /proj/travel
check "rerun creates nothing" 0 '.result.created == []' -- create-folder -p /proj/travel
check "top-level folder" 0 '.result.created == ["/home"]' -- create-folder /home
check "deeper chain" 0 '.result.created == ["/proj/work", "/proj/work/q3"]' -- create-folder -p /proj/work/q3
check "root always exists" 0 '.result.created == []' -- create-folder /
expect "folders on disk" '[[ -d $HOME/tasks/proj/travel && -d $HOME/tasks/proj/work/q3 && -d $HOME/tasks/home ]]'

echo "== tasks"
check "create 1: priority and tags" 0 '.result | .id == 1 and .priority == 1 and .tags == ["travel", "urgent"] and .folder == "/proj/travel"' \
	-- create 'Renew passport' --folder /proj/travel --priority 1 --tags urgent,travel
check "create 2: blocked by 1" 0 '.result | .id == 2 and .blocked_by == [1]' \
	-- create 'Book flights' --folder /proj/travel --blocked-by 1
check "create 3: notes from stdin" 0 '.result | .id == 3 and .blocked_by == [2]' \
	-- create 'Book hotel' --folder /proj/travel --blocked-by 2 --notes-file - <<<'Near the station'
notes3=$(jq -r .result.notes_path <<<"$out")
check "create 4: two blockers and extra" 0 '.result | .id == 4 and .blocked_by == [2, 3] and .extra == {status: "waiting"}' \
	-- create 'Pack bags' --folder /home --blocked-by 3,2 --extra '{"status":"waiting"}'
check "create 5: title trimmed" 0 '.result | .id == 5 and .title == "Q3 report"' \
	-- create '  Q3 report ' --folder /proj/work/q3
check "create 6: whole input as JSON" 0 '.result | .id == 6 and .folder == "/proj/work" and .notes_path == "'"$HOME"'/tasks/proj/work/6.md"' \
	-- create -i - <<<'{"title": "Review Q3", "folder": "/proj/work", "blocked_by": [5], "notes": "slides too"}'
check "create 7: in the root" 0 '.result | .id == 7 and .folder == "/" and .priority == null' -- create 'Water plants'
expect "notes written as given" '[[ $(cat "$notes3") == "Near the station" ]]'
expect "task file on disk" '[[ -f $HOME/tasks/proj/travel/1.json && -f $HOME/tasks/home/4.json ]]'

echo "== create failures"
check "missing folder and blockers in one not-found" 1 '.error.details == {folders: ["/nope"], ids: [98, 99], paths: []}' \
	-- create x --folder /nope --blocked-by 99,98
check "blank title" 1 '.error.kind == "invalid-input" and .error.details.problems[0].field == "/title"' -- create '   '
check "--notes with --notes-file" 2 '.error.kind == "usage"' -- create x --notes a --notes-file -
check "failed creates used no IDs" 0 '.result.tree.last_id == 7' -- info

echo "== show"
check "1: ready" 0 '.result.tasks[0] | .readiness == "ready" and .blocking == []' -- show 1
check "3: blocked by 2" 0 '.result.tasks[0] | .readiness == "blocked" and .blocking == [2]' -- show 3
check "4: blocked by 2 and 3" 0 '.result.tasks[0] | .readiness == "blocked" and .blocking == [2, 3]' -- show 4
check "6: blocked by 5, across folders" 0 '.result.tasks[0] | .readiness == "blocked" and .blocking == [5]' -- show 6
check "not found" 1 '.error.kind == "not-found" and .error.details.ids == [99]' -- show 99
check "not an ID" 1 '.error.kind == "invalid-input"' -- show abc

echo "== complete and reopen"
check "complete 1" 0 '.result | .changed and .completed_at != null' -- complete 1
done1=$(jq -r .result.completed_at <<<"$out")
check "2 now ready" 0 '.result.tasks[0].readiness == "ready"' -- show 2
check "complete 1 again: nothing changes" 0 '.result | (.changed | not) and .completed_at == "'"$done1"'"' -- complete 1
check "1 shows complete" 0 '.result.tasks[0].readiness == "complete"' -- show 1
check "complete 2" 0 '.result.changed' -- complete 2
check "4 still blocked by 3 only" 0 '.result.tasks[0].blocking == [3]' -- show 4
check "complete 3" 0 '.result.changed' -- complete 3
check "4 now ready" 0 '.result.tasks[0] | .readiness == "ready" and .blocking == []' -- show 4
check "complete a task with open blockers" 0 '.result.changed' -- complete 6
check "reopen 2" 0 '.result | .changed and .completed_at == null' -- reopen 2
check "3 still complete" 0 '.result.tasks[0].readiness == "complete"' -- show 3
check "4 blocked again by 2" 0 '.result.tasks[0] | .readiness == "blocked" and .blocking == [2]' -- show 4
check "reopen 2 again: nothing changes" 0 '.result.changed == false' -- reopen 2
check "complete: not found" 1 '.error.details.ids == [99]' -- complete 99
check "reopen: not found" 1 '.error.details.ids == [99]' -- reopen 99

echo "== update"
check "update 7: several fields" 0 '.result | .changed == ["title", "priority", "tags", "extra"] and .title == "Water the plants" and .priority == 3 and .tags == ["home", "weekly"] and .extra == {room: "kitchen"}' \
	-- update 7 --title 'Water the plants' --priority 3 --tags-add weekly,home --extra-merge '{"room":"kitchen"}'
check "same update again: nothing changes" 0 '.result.changed == []' \
	-- update 7 --title 'Water the plants' --priority 3 --tags-add weekly,home --extra-merge '{"room":"kitchen"}'
check "a subset of the same values: no change" 0 '.result.changed == []' -- update 7 --extra-merge '{"room":"kitchen"}' --priority 3
check "clear priority, remove a tag and a key" 0 '.result | .changed == ["priority", "tags", "extra"] and .priority == null and .tags == ["weekly"] and .extra == {}' \
	-- update 7 --priority null --tags-remove home --extra-remove room
check "replace all tags" 0 '.result | .changed == ["tags"] and .tags == ["a", "b"]' -- update 7 --tags-replace-all b,a
check "clear all tags" 0 '.result.tags == []' -- update 7 --tags-replace-all ''
check "merge replaces a value whole" 0 '.result.extra == {status: {since: "monday"}}' -- update 4 --extra-merge '{"status":{"since":"monday"}}'
check "a complete task can be updated" 0 '.result | .changed == ["title"] and .completed_at != null' -- update 1 --title 'Renew passport (done)'
check "nothing to change" 1 '.error.kind == "invalid-input"' -- update 7
check "add and remove the same tag" 1 '.error.details.problems[0].field == "/tags/remove/0"' -- update 7 --tags-add x --tags-remove x
check "replace_all with add" 1 '.error.kind == "invalid-input"' -- update 7 --tags-add x --tags-replace-all y
check "update: not found" 1 '.error.details.ids == [99]' -- update 99 --title x
check "show sees the update" 0 '.result.tasks[0] | .title == "Water the plants" and .tags == []' -- show 7

echo "== block"
# Here: 1 complete; 2 open (reopened), blocked by 1; 3 complete; 4 blocked
# by 2 and 3; 5 open; 6 complete, blocked by 5; 7 open.
check "block 7 by 5 and 2" 0 '.result | .added == [2, 5] and .blocked_by == [2, 5]' -- block 7 --blockers 5,2
check "7 blocked by both" 0 '.result.tasks[0] | .readiness == "blocked" and .blocking == [2, 5]' -- show 7
check "blockers already present: nothing added" 0 '.result | .added == [] and .blocked_by == [2, 5]' -- block 7 --blockers 2,5
check "some new, some present" 0 '.result.added == [3]' -- block 7 --blockers 2,3
check "a complete blocker doesn't block" 0 '.result.tasks[0].blocking == [2, 5]' -- show 7
check "direct cycle refused" 1 '.error.details | .rule == "acyclic" and .ids == [7] and .cycles == [[5, 7]]' -- block 5 --blockers 7
check "longer cycle refused" 1 '.error.details.cycles == [[2, 4]]' -- block 2 --blockers 4
check "through a complete task" 1 '.error.details.cycles == [[3, 7]]' -- block 3 --blockers 7
check "every offending blocker" 1 '.error.details | .ids == [4, 7] and .cycles == [[2, 4], [2, 7]]' -- block 2 --blockers 4,7,1
check "refused block wrote nothing" 0 '.result.tasks[0].blocked_by == [1]' -- show 2
check "missing blockers" 1 '.error.details.ids == [98, 99]' -- block 7 --blockers 99,98
check "missing task and blocker" 1 '.error.details.ids == [97, 99]' -- block 97 --blockers 99
check "a task can't block itself" 1 '.error.details.problems[0].field == "/blockers/0"' -- block 7 --blockers 7
check "--blockers is required" 2 '.error.kind == "usage"' -- block 7

echo "== unblock"
check "unblock 7 from 5" 0 '.result | .removed == [5] and .blocked_by == [2, 3]' -- unblock 7 --blockers 5
check "not there: nothing removed" 0 '.result | .removed == [] and .blocked_by == [2, 3]' -- unblock 7 --blockers 5,99
check "the cycle is now allowed" 0 '.result.added == [7]' -- block 5 --blockers 7
check "unblock the rest" 0 '.result | .removed == [2, 3] and .blocked_by == []' -- unblock 7 --blockers 3,2
check "7 ready again" 0 '.result.tasks[0] | .readiness == "ready" and .blocking == []' -- show 7
check "unblock: not found" 1 '.error.details.ids == [99]' -- unblock 99 --blockers 1
check "unblock: --blockers is required" 2 '.error.kind == "usage"' -- unblock 7

echo "== list"
# Here: open 2 (/proj/travel), 4 (/home), 5 (/proj/work/q3), 7 (/);
# complete 1, 3 (/proj/travel) and 6 (/proj/work).
check "every open task, in tree order" 0 '[.result.tasks[].id] == [7, 4, 2, 5] and (.result | has("folders") | not)' -- list
check "readiness shown" 0 '[.result.tasks[] | {id, readiness}] == [{id: 7, readiness: "ready"}, {id: 4, readiness: "blocked"}, {id: 2, readiness: "ready"}, {id: 5, readiness: "blocked"}]' -- list
check "with complete tasks" 0 '[.result.tasks[].id] == [7, 4, 1, 2, 3, 6, 5]' -- list --readiness ready,blocked,complete
check "only complete" 0 '[.result.tasks[].id] == [1, 3, 6]' -- list --readiness complete
check "one folder" 0 '[.result.tasks[].id] == [5]' -- list --folder /proj/work
check "one folder with complete" 0 '[.result.tasks[].id] == [6, 5]' -- list --folder /proj/work --readiness ready,blocked,complete
check "not recursive" 0 '.result.tasks == []' -- list --folder /proj --recursive=false
check "folders" 0 '.result.folders == ["/", "/home", "/proj", "/proj/travel", "/proj/work", "/proj/work/q3"]' -- list --include-folders
check "folders, not recursive" 0 '.result.folders == ["/proj", "/proj/travel", "/proj/work"]' -- list --folder /proj --recursive=false --include-folders
check "missing folder" 1 '.error.details.folders == ["/nope"]' -- list --folder /nope
check "list takes no arguments" 2 '.error.kind == "usage"' -- list /proj
check "limit: a prefix, with the count" 0 '[.result.tasks[].id] == [7, 4] and .result.total == 4 and .result.truncated' -- list --limit 2
check "no limit: nothing cut" 0 '.result.total == 4 and (.result.truncated | not)' -- list
check "fields: those and id" 0 '.result.tasks[0] == {id: 7, title: "Water the plants", readiness: "ready"}' -- list --fields readiness,title --limit 1
check "blocked, briefly" 0 '.result.tasks == [{id: 4, blocking: [2]}, {id: 5, blocking: [7]}]' -- list --readiness blocked --fields blocking
check "include-complete is gone" 2 '.error.kind == "usage"' -- list --include-complete

echo "== frontier"
# Ready now: 2 and 7, neither with a priority (update cleared 7's);
# blocked: 4 (by 2), 5 (by 7).
check "ready tasks, lower ID first" 0 '[.result.tasks[].id] == [2, 7] and all(.result.tasks[]; .readiness == "ready" and .blocking == [])' -- frontier
check "one folder" 0 '[.result.tasks[].id] == [2]' -- frontier --folder /proj/travel
check "a blocker in another folder still blocks" 0 '.result.tasks == []' -- frontier --folder /proj/work
check "complete 7" 0 '.result.changed' -- complete 7
check "5 joins the frontier" 0 '[.result.tasks[].id] == [2, 5]' -- frontier
check "priority 9 goes first" 0 '.result.changed == ["priority"]' -- update 5 --priority 9
check "now 5 first" 0 '[.result.tasks[].id] == [5, 2]' -- frontier
check "complete 2" 0 '.result.changed' -- complete 2
check "4 ready too: tie on no priority, lower ID first" 0 '[.result.tasks[].id] == [5, 4]' -- frontier
check "not recursive" 0 '.result.tasks == []' -- frontier --folder /proj --recursive=false
check "missing folder" 1 '.error.details.folders == ["/nope"]' -- frontier --folder /nope
check "limit and fields" 0 '.result == {tasks: [{id: 5, priority: 9}], total: 2, truncated: true}' -- frontier --limit 1 --fields priority
check "the count alone" 0 '.result | .tasks == [] and .total == 2' -- frontier --limit 0
check "list's options aren't frontier's" 2 '.error.kind == "usage"' -- frontier --readiness ready

echo "== move and delete"
# Here: complete 1, 2, 3 (/proj/travel), 6 (/proj/work, blocked by 5), 7 (/);
# open 4 (/home, blocked by 2), 5 (/proj/work/q3, blocked by 7).
check "move into a new folder" 0 '.result | .folder == "/archive" and .from == "/home" and .created == ["/archive"] and .changed' -- move 4 --to /archive -p
check "move again changes nothing" 0 '.result.changed == false' -- move 4 --to /archive
check "move: missing folder" 1 '.error.details.folders == ["/nope"]' -- move 4 --to /nope/x
check "move: --to is required" 2 '.error.kind == "usage"' -- move 4
check "move-folder into an existing folder" 0 '.result | .folder == "/archive/work" and .from == "/proj/work"' -- move-folder /proj/work --to /archive
check "its tasks moved with it" 0 '[.result.tasks[].id] == [5]' -- list --folder /archive/work/q3
check "move-folder: not into itself" 1 '.error.kind == "invalid-input"' -- move-folder /archive --to /archive/x
check "delete-folder: not empty without -r" 1 '.error.details.rule == "not-empty"' -- delete-folder /archive
check "delete: 5 no longer blocked by 7" 0 '.result | .id == 7 and .dependents == [5]' -- delete 7
check "5 ready" 0 '.result.tasks[0] | .blocked_by == [] and .readiness == "ready"' -- show 5
check "delete: gone" 1 '.error.details.ids == [7]' -- delete 7
check "delete-folder -r: 4 no longer blocked by 2" 0 '.result | .ids == [1, 2, 3] and .dependents == [4]' -- delete-folder -r /proj
check "delete-folder: the root never" 1 '.error.kind == "invalid-input"' -- delete-folder -r /
check "what's left" 0 '[.result.tasks[].id] == [4, 6, 5] and .result.folders == ["/", "/archive", "/archive/work", "/archive/work/q3", "/home"]' -- list --readiness ready,blocked,complete --include-folders

echo "== end state"
check "last_id counts every task" 0 '.result.tree.last_id == 7' -- info

echo
echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
