package cli

import (
	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/pick"
)

// commands are koan's commands, in help order (cli-spec.md, Commands).
var commands = []Command{
	{
		Name:    "version",
		Op:      "version",
		Summary: "Report the version and build of the koan binary",
		Example: "  koan version",
	},
	{
		Name:    "info",
		Op:      "info",
		Summary: "Report the state of this machine's configured root",
		Example: "  koan info   # ready when .result.usable is true; if not, the rest of .result says why",
	},
	{
		Name:    "init",
		Op:      "init",
		Summary: "Create a new tree, or attach an existing one, as this machine's root",
		Args:    []Arg{{Name: "root", Field: "/root", Type: String}},
		Options: []Option{
			{Name: "replace-config", Field: "/replace_config", Type: Bool, Help: "replace an existing config"},
		},
		Example: `  koan init ~/tasks
  koan init tasks                    # relative to the working directory
  koan init /mnt/usb/tasks --replace-config
  jq -n '{root: "~/tasks"}' | koan init -i -`,
		Resolve: resolveRoot,
	},
	{
		Name:    "doctor",
		Op:      "doctor",
		Summary: "Report everything wrong with the tree, and what repair would do about it",
		Options: []Option{
			{Name: "kinds", Field: "/kinds", Type: StringList, Help: "report only these finding `kinds`, each in full"},
		},
		Example: `  koan doctor                                   # healthy when .result.healthy is true; else .result.findings says why
  koan doctor --kinds cycle,duplicate-id        # just these, in full
  koan doctor | jq '.result.findings[] | select(.class != "manual") | .kind'   # what repair would fix`,
	},
	{
		Name:    "repair",
		Op:      "repair",
		Summary: "Apply the repairs that are safe, then report what is left",
		Options: []Option{
			{Name: "kinds", Field: "/kinds", Type: StringList, Help: "repair only these finding `kinds`; metadata-missing only when named"},
		},
		Example: `  koan repair                                   # every auto repair
  koan repair --kinds temp-leftover             # only the leftover temp files
  koan repair --kinds metadata-missing          # rebuild a lost koan.json; never done by default`,
	},
	{
		Name:    "create-folder",
		Op:      "create-folder",
		Summary: "Create a folder, and optionally any missing parent folders",
		Args:    []Arg{{Name: "folder", Field: "/folder", Type: String}},
		Options: []Option{
			{Name: "parents", Short: "p", Field: "/parents", Type: Bool, Help: "create missing parent folders"},
		},
		Example: `  koan create-folder /proj
  koan create-folder -p /proj/travel/2026   # .result.created: the folders it made`,
	},
	{
		Name:    "delete-folder",
		Op:      "delete-folder",
		Summary: "Permanently remove a folder and everything under it; undo is git's",
		Args:    []Arg{{Name: "folder", Field: "/folder", Type: String}},
		Options: []Option{
			{Name: "recursive", Short: "r", Field: "/recursive", Type: Bool, Help: "also remove the tasks and folders it holds"},
		},
		Example: `  koan delete-folder /proj/old
  koan delete-folder -r /proj/travel        # .result.ids: the tasks removed; .result.dependents: the tasks they no longer block`,
	},
	{
		Name:    "move-folder",
		Op:      "move-folder",
		Summary: "Move a folder, and everything under it, to a new place, which also renames it",
		Args:    []Arg{{Name: "folder", Field: "/folder", Type: String}},
		Options: []Option{
			{Name: "to", Field: "/to", Type: String, Required: true, Help: "an existing folder to move it into, or its new `path`"},
			{Name: "parents", Short: "p", Field: "/parents", Type: Bool, Help: "create missing folders above the new path"},
		},
		Example: `  koan move-folder /proj/travel --to /archive              # → /archive/travel
  koan move-folder /proj/travel --to /archive/travel-2025  # → moved and renamed
  koan move-folder /proj/travel --to /archive/2025/trips -p  # → /archive/2025/trips, creating /archive/2025`,
	},
	{
		Name:    "create",
		Op:      "create",
		Summary: "Create a new, open task",
		Args:    []Arg{{Name: "title", Field: "/title", Type: String}},
		Options: []Option{
			{Name: "folder", Field: "/folder", Type: String, Help: "folder to create the task in, as an exact `path` (default /)"},
			{Name: "priority", Field: "/priority", Type: NullableInt, Help: "priority, an `int` or null (default null)"},
			{Name: "tags", Field: "/tags", Type: StringList, Help: "comma-separated `tags`"},
			{Name: "blocked-by", Field: "/blocked_by", Type: IDList, Help: "comma-separated `ids` of the tasks that block it"},
			{Name: "extra", Field: "/extra", Type: JSON, Help: "extra fields, a JSON `object`"},
			{Name: "notes", Field: "/notes", Type: String, Help: "initial notes `text`"},
			{Name: "notes-file", Field: "/notes", Type: TextFile, Help: "read the initial notes from `file` (- for stdin)"},
		},
		Exclusive: [][]string{{"notes", "notes-file"}},
		Example: `  koan create 'Book flights' --folder /proj/travel --tags travel,urgent --priority 2
  koan create 'Deploy' --blocked-by 41,42   # the new ID is .result.id
  gh issue view 12 --json body -q .body | koan create 'Fix login bug' --notes-file -
  koan create 'Wait on quote' --extra '{"status":"waiting"}'`,
	},
	{
		Name:          "create-batch",
		Op:            "create-batch",
		Summary:       "Create several tasks in one call, with dependencies between them named by refs",
		InputRequired: true,
		Example: `  jq -n '{folder: "/work/api", tasks: [
    {ref: "schema", title: "Design schema", priority: 2, tags: ["db"]},
    {ref: "migrate", title: "Write migrations", blocked_by: ["schema"]},
    {title: "Deploy", blocked_by: ["migrate", 12]}
  ]}' | koan create-batch -i -        # creates /work/api if missing; .result.refs.schema is Design schema's ID
  koan create-batch -i plan.json | jq -r '.result.ids | join(",")' | xargs koan block 41 --blockers`,
	},
	{
		Name:    "show",
		Op:      "show",
		Summary: "Return one task by ID, with its readiness and where its notes live",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Example: `  koan show 42                                   # readiness, blocking, notes_path: all in .result.tasks[0]
  for id in 41 42 43; do koan show "$id"; done   # one envelope each`,
	},
	{
		Name:    "complete",
		Op:      "complete",
		Summary: "Mark a task complete; completing a complete task changes nothing",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Example: `  koan complete 42                               # .result.changed is false if it was already complete
  for id in 41 42; do koan complete "$id"; done  # one envelope each`,
	},
	{
		Name:    "reopen",
		Op:      "reopen",
		Summary: "Reopen a complete task; reopening an open task changes nothing",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Example: "  koan reopen 42   # .result.completed_at is null again",
	},
	{
		Name:    "update",
		Op:      "update",
		Summary: "Change a task's title, priority, tags, or extra",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Options: []Option{
			{Name: "title", Field: "/title", Type: String, Help: "replace the title with `text`"},
			{Name: "priority", Field: "/priority", Type: NullableInt, Help: "set the priority, an `int`; null clears it"},
			{Name: "tags-add", Field: "/tags/add", Type: StringList, Help: "comma-separated `tags` to add"},
			{Name: "tags-remove", Field: "/tags/remove", Type: StringList, Help: "comma-separated `tags` to remove"},
			{Name: "tags-replace-all", Field: "/tags/replace_all", Type: StringList, Help: "the complete new `tags`; '' clears them"},
			{Name: "extra-merge", Field: "/extra/merge", Type: JSON, Help: "keys to set, a JSON `object`"},
			{Name: "extra-remove", Field: "/extra/remove", Type: Repeated, Help: "a `key` to delete; repeatable"},
			{Name: "extra-replace-all", Field: "/extra/replace_all", Type: JSON, Help: "the complete new extra, a JSON `object`; {} clears it"},
		},
		Example: `  koan update 42 --priority 3 --tags-add urgent
  koan update 42 --extra-merge '{"status":"waiting"}'   # .result.changed names the fields that changed
  koan update 42 --priority null --tags-remove urgent --extra-remove status
  koan update 42 --tags-replace-all ''`,
	},
	{
		Name:    "delete",
		Op:      "delete",
		Summary: "Permanently remove a task, and its ID from every blocked_by; undo is git's",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Example: `  koan delete 42                                              # .result.dependents: the tasks it no longer blocks
  git log --diff-filter=D --oneline -- '*/42.json' '42.json'   # find it again later`,
	},
	{
		Name:    "move",
		Op:      "move",
		Summary: "Move a task into a folder",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Options: []Option{
			{Name: "to", Field: "/to", Type: String, Required: true, Help: "the folder to move it into, as an exact `path`"},
			{Name: "parents", Short: "p", Field: "/parents", Type: Bool, Help: "create the folder, and any missing above it"},
		},
		Example: `  koan move 42 --to /proj/travel
  koan move 42 --to /archive/2025 -p   # creates /archive/2025; the notes move too`,
	},
	{
		Name:    "block",
		Op:      "block",
		Summary: "Add blockers to a task, all or nothing; a cycle is refused",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Options: []Option{
			{Name: "blockers", Field: "/blockers", Type: IDList, Required: true, Help: "comma-separated `ids` of the tasks that block it"},
		},
		Example: `  koan block 42 --blockers 41,43   # .result.added: the ones not already there
  koan block 42 --blockers 7       # a cycle is refused: .error.details.cycles shows it`,
	},
	{
		Name:    "unblock",
		Op:      "unblock",
		Summary: "Remove blockers from a task; removing one that isn't there changes nothing",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Options: []Option{
			{Name: "blockers", Field: "/blockers", Type: IDList, Required: true, Help: "comma-separated `ids` to remove from its blockers"},
		},
		Example: `  koan unblock 42 --blockers 41   # .result.removed: the ones that were there
  koan unblock 42 --blockers 99   # clears a dangling reference to a task that no longer exists`,
	},
	{
		Name:    "list",
		Op:      "list",
		Summary: "Return every task in scope with its readiness, and optionally the folders",
		Options: []Option{
			{Name: "folder", Field: "/folder", Type: String, Help: "only tasks in this folder, an exact `path` (default /)"},
			{Name: "recursive", Field: "/recursive", Type: Bool, Help: "also tasks in subfolders (default true; --recursive=false for none)"},
			{Name: "readiness", Field: "/readiness", Type: StringList, Help: "only tasks in these comma-separated `states`: ready, blocked, complete (default ready,blocked)"},
			{Name: "include-folders", Field: "/include_folders", Type: Bool, Help: "also return the folders in scope"},
			{Name: "tags-any", Field: "/tags_any", Type: StringList, Help: "only tasks with at least one of these comma-separated `tags`"},
			{Name: "tags-all", Field: "/tags_all", Type: StringList, Help: "only tasks with every one of these comma-separated `tags`"},
			{Name: "limit", Field: "/limit", Type: Int, Help: "at most `n` tasks, the first in tree order; total and truncated say what was cut"},
			{Name: "fields", Field: "/fields", Type: StringList, Help: "return only these comma-separated task `fields`, and id"},
		},
		Example: `  koan list --limit 50 --fields id,title,readiness,folder
  koan list --folder /proj --readiness complete --limit 0                     # how many are done: .result.total
  koan list --readiness blocked --fields id,title,blocking                    # what's stuck, and on what
  koan list --readiness ready,blocked,complete --tags-all db,backend --fields id,title,readiness
  koan list --include-folders --limit 0                                      # every folder: .result.folders
  koan list --fields folder | jq -c 'if .ok then .result |= (.tasks |= (group_by(.folder) | map({folder: .[0].folder, ids: map(.id)}))) else . end'`,
	},
	{
		Name:    "frontier",
		Op:      "frontier",
		Summary: "Return the ready tasks — open, not blocked — in the order to work on them",
		Options: []Option{
			{Name: "folder", Field: "/folder", Type: String, Help: "only tasks in this folder, an exact `path` (default /)"},
			{Name: "recursive", Field: "/recursive", Type: Bool, Help: "also tasks in subfolders (default true; --recursive=false for none)"},
			{Name: "tags-any", Field: "/tags_any", Type: StringList, Help: "only tasks with at least one of these comma-separated `tags`"},
			{Name: "tags-all", Field: "/tags_all", Type: StringList, Help: "only tasks with every one of these comma-separated `tags`"},
			{Name: "limit", Field: "/limit", Type: Int, Help: "at most `n` tasks, the first in frontier order; total and truncated say what was cut"},
			{Name: "fields", Field: "/fields", Type: StringList, Help: "return only these comma-separated task `fields`, and id"},
		},
		Example: `  koan frontier --limit 10 --fields id,title,priority,folder   # the next ten, briefly
  koan frontier --limit 1                                     # the next task, whole
  koan frontier --folder /proj --limit 20 --fields id,title   # ready under /proj
  koan frontier --tags-any urgent,today --fields id,title
  koan frontier --limit 0                                     # how many are ready: .result.total`,
	},
	{
		Name:    "pick",
		Op:      "pick",
		Summary: "Fuzzy-pick tasks, or folders, in fzf for a person at a terminal; never for agents",
		Options: []Option{
			{Name: "folder", Field: "/folder", Type: String, Help: "the scope folder at first, an exact `path` (default /)"},
			{Name: "recursive", Field: "/recursive", Type: Bool, Help: "also tasks in subfolders (default true; --recursive=false for none)"},
			{Name: "scope", Field: "/scope", Type: String, Help: "which tasks show at first, a `scope`: ready, open or all (default open; all with --ids, --from or --source)"},
			{Name: "tags-any", Field: "/tags_any", Type: StringList, Help: "only tasks with at least one of these comma-separated `tags`"},
			{Name: "tags-all", Field: "/tags_all", Type: StringList, Help: "only tasks with every one of these comma-separated `tags`"},
			{Name: "ids", Field: "/ids", Type: IDList, Help: "only these comma-separated `ids` are candidates"},
			{Name: "from", Field: "/ids", Type: EnvelopeFile, Help: "only the tasks in the envelope in `file` (- for stdin) are candidates"},
			{Name: "source", Field: "/source", Type: String, Help: "a shell `command` whose envelope gives the candidates, run again on every reload"},
			{Name: "query", Field: "/query", Type: String, Help: "the initial search `text`"},
			{Name: "select-one", Field: "/select_one", Type: Bool, Help: "if the query matches exactly one candidate, emit it without showing the picker"},
			{Name: "exit-zero", Field: "/exit_zero", Type: Bool, Help: "if the query matches no candidate, emit an empty selection without showing the picker"},
			{Name: "fields", Field: "/fields", Type: StringList, Help: "emit only these comma-separated task `fields`, and id"},
			{Name: "folders", Field: "/folders", Type: Bool, Help: "pick folders instead of tasks"},
		},
		Exclusive: [][]string{{"ids", "from"}},
		Example: `  koan pick                                                  # open tasks; Enter → .result.tasks
  koan pick --folder /work --scope ready                     # what's ready under /work
  koan list --readiness blocked --fields id | koan pick --from -   # choose among the blocked ones
  koan pick --source 'koan frontier --tags-any today'       # kept live
  koan pick --folders | jq -r '.result.folders[0]'           # a folder path`,
		Run: func(in *jsonio.Object, problems []errs.Problem, env Env) ops.Envelope {
			return runPick(in, problems, pick.Env{Ops: env.Ops, Sys: env.Pick})
		},
	},
}

// runPick runs pick; tests replace it to see the input built.
var runPick = pick.Run
