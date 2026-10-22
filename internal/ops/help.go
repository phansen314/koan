package ops

import "github.com/phansen314/koan/internal/cli"

// shorts are each command's line in help.
var shorts = map[string]string{
	"task create":     "Make a task, with its notes file",
	"task show":       "Show one task",
	"task list":       "List tasks in tree order",
	"task frontier":   "List ready, unclaimed tasks in work order",
	"task why":        "Show what a task waits on, and what to finish to move it",
	"task update":     "Change a task's title, priority, tags or extra",
	"task complete":   "Complete a task, ending its claim",
	"task reopen":     "Open a complete task again",
	"task block":      "Add edges: what a task waits on, or what waits on it",
	"task unblock":    "Remove edges",
	"task move":       "Move a task, with its notes, to another folder",
	"task delete":     "Delete a task for good",
	"task archive":    "Move a complete task out of the tree, into the archive",
	"task claim":      "Reserve a task",
	"task claim-next": "Claim the first task of the frontier",
	"task release":    "Give up a claim",
	"folder create":   "Make a folder, and any missing parents",
	"folder show":     "Show one folder",
	"folder list":     "List folders in tree order",
	"folder update":   "Change a folder's title or extra",
	"folder move":     "Move or rename a folder",
	"folder delete":   "Delete a folder for good",
	"folder archive":  "Move a finished folder out of the tree, into the archive",
	"batch":           "Make folders and tasks, with their edges, in one change",
	"where":           "Resolve a reference, or find the folder a file is in",
	"context":         "Brief a folder: the folders above it, its notes, its open tasks",
	"marker bind":     "Bind a directory outside the tree, such as a worktree, to a folder",
	"marker unbind":   "Remove a directory's marker",
	"init":            "Make the root, ~/koan-root",
	"info":            "Report the root and its state",
	"version":         "Report this binary's version",
	"doctor":          "Check the tree and the database; changes nothing",
	"repair":          "Fix what doctor found that can be fixed without losing anything",
	"backup":          "Copy the root into a directory, and check the copy",
}

// usages are each option's line in help, by field, or by command and
// field where one command means something of its own by it.
var usages = map[string]string{
	"folder":                "only under this folder",
	"task create/folder":    "the folder to make it in; the root when left out",
	"recursive":             "include subfolders; --recursive=false for the folder's own only",
	"priority":              "an integer, higher first, or null",
	"tags":                  "its tags",
	"blocked_by":            "what it waits on: tasks, gates or folders",
	"blocks":                "what waits on it: tasks, gates or folders",
	"extra":                 "a JSON object of your own fields",
	"task claim/extra":      "a JSON object kept with the claim, such as who holds it",
	"task claim-next/extra": "a JSON object kept with the claim, such as who holds it",
	"notes":                 "the notes, as text",
	"title":                 "the title",
	"folder create/title":   "the title, or null for none",
	"folder update/title":   "the title, or null for none",
	"tags_any":              "only tasks with any of these tags",
	"tags_all":              "only tasks with all of these tags",
	"limit":                 "at most this many; 100 when left out, null for all",
	"fields":                "only these fields, after id",
	"state":                 "open, complete, or both; open when left out",
	"readiness":             "ready, blocked, or both",
	"claimed":               "true for only claimed tasks, false for only unclaimed",
	"tags_add":              "add these tags",
	"tags_remove":           "remove these tags",
	"tags_replace_all":      "replace every tag with these",
	"extra_merge":           "merge this JSON object into extra, at the top level",
	"extra_remove":          "remove this key from extra; repeat for more",
	"extra_replace_all":     "replace extra with this JSON object",
	"task move/to":          "the folder to move it to",
	"folder move/to":        "the folder's whole new path",
	"contents":              "delete the folder's tasks and folders too",
	"folder delete/force":   "delete the files of yours in it too",
	"marker bind/force":     "bind a checkout, or a directory bound to another folder",
	"dir":                   "the directory; the working directory when left out",
	"where/file":            "find the folder this file is in",
	"context/file":          "brief the folder this file is in; the working directory when left out",
	"kinds":                 "fix only these kinds of finding",
	"ids_from":              "the first ID the new root issues",
}

// fileUsages are each convenience's line in help, by field.
var fileUsages = map[string]string{
	"notes": "read the notes from a file, or from standard input for -",
}

// describe gives every command its line in help, and each option its own.
func describe(commands []*cli.Command) []*cli.Command {
	usage := func(c *cli.Command, field string) string {
		if u, ok := usages[c.Name+"/"+field]; ok {
			return u
		}
		return usages[field]
	}
	for _, c := range commands {
		c.Short = shorts[c.Name]
		for i := range c.Options {
			c.Options[i].Usage = usage(c, c.Options[i].Field)
		}
		for i := range c.Files {
			c.Files[i].Usage = fileUsages[c.Files[i].Field]
		}
	}
	return commands
}
