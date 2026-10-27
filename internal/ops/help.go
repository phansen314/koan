package ops

import "github.com/phansen314/koan/internal/cli"

// shorts are each command's line in help.
var shorts = map[string]string{
	"task create":   "Make a task, with its notes file",
	"show":          "Show one task, a gate included, or one folder or the root",
	"task list":     "List tasks in tree order",
	"frontier":      "List ready, unclaimed tasks in work order",
	"why":           "Show what a task waits on, and what to finish to move it",
	"update":        "Change the title, priority, tags or extra of tasks and folders",
	"complete":      "Complete tasks, ending their claims",
	"reopen":        "Open complete tasks again",
	"block":         "Add edges: what a task waits on, or what waits on it",
	"unblock":       "Remove edges: what a task waits on, or what waits on it",
	"join":          "Join a task, or a folder through its gates, to folders",
	"unjoin":        "Remove the edges of joining a task or a folder to folders",
	"mv":            "Move tasks and folders into a folder, or rename one folder",
	"delete":        "Delete tasks and folders for good",
	"archive":       "Move complete tasks and finished folders out of the tree, into the archive",
	"claim":         "Reserve tasks",
	"claim-next":    "Claim the first task of the frontier",
	"unclaim":       "End the claims of tasks, whoever holds them",
	"folder create": "Make a folder, and any missing parents",
	"folder list":   "List folders in tree order",
	"batch":         "Make folders and tasks, with their edges, in one change",
	"where":         "Resolve a reference, or find the folder a file is in",
	"context":       "Brief a folder: the folders above it, its notes, its open tasks",
	"export":        "Print a folder's whole subtree, with the edges in and out of it, for tools",
	"log list":      "List the log's events, oldest first",
	"log rotate":    "Move the log's oldest events out of the database, into a file in log/",
	"bind":          "Bind a directory outside the tree, such as a worktree, to a folder",
	"unbind":        "Remove a directory's marker",
	"init":          "Make the root, ~/koan-root",
	"info":          "Report the root and its state",
	"version":       "Report this binary's version",
	"doctor":        "Check the tree and the database; changes nothing",
	"repair":        "Fix what doctor found that can be fixed without losing anything",
	"backup":        "Copy the root into a directory, and check the copy",
	"pick":          "Pick tasks, gates and folders in fzf, and print their references; for a person at a terminal",
}

// usages are each option's line in help, by field, or by command and
// field where one command means something of its own by it.
var usages = map[string]string{
	"task create/folder":  "the folder to make it in; the root when left out",
	"direct":              "only what is directly in the folder",
	"priority":            "an integer, higher first, or null",
	"update/priority":     "an integer, higher first, or null; for tasks only",
	"tags":                "its tags",
	"blocked_by":          "what it waits on: tasks, gates or folders",
	"blocks":              "what waits on it: tasks, gates or folders",
	"join":                "the folders it joins: it waits on their start gates, and their done gates on it",
	"folder create/join":  "the folders it joins, through its gates",
	"extra":               "a JSON object of your own fields",
	"claim/extra":         "a JSON object kept with the claim, such as who holds it",
	"claim-next/extra":    "a JSON object kept with the claim, such as who holds it",
	"notes":               "the notes, as text",
	"folder create/title": "the title, or null for none",
	"update/title":        "the title, or null to clear a folder's",
	"tags_any":            "only tasks with any of these tags",
	"tags_all":            "only tasks with all of these tags",
	"limit":               "at most this many; 100 when left out, null for all",
	"fields":              "only these fields, after id",
	"state":               "open, complete, or both; open when left out",
	"readiness":           "ready, blocked, or both",
	"claimed":             "only claimed tasks; --claimed=false for only unclaimed, --claimed=null for both",
	"tags_add":            "add these tags; for tasks only",
	"tags_remove":         "remove these tags; for tasks only",
	"tags_replace_all":    "replace every tag with these; for tasks only",
	"extra_merge":         "merge this JSON object into extra, at the top level",
	"extra_remove":        "remove this key from extra; repeat for more",
	"extra_replace_all":   "replace extra with this JSON object",
	"mv/into":             "the folder to move them into, in place of the last word",
	"mv/rename":           "the last word is the folder's new path, never a folder to move into",
	"contents":            "delete the folders' tasks and folders too; for folders only",
	"delete/material":     "delete the folders' material too; for folders only",
	"bind/force":          "bind a checkout, or a directory bound to another folder",
	"where/file":          "find the folder this file is in",
	"context/file":        "brief the folder this file is in",
	"export/file":         "export the folder this file is in",
	"file":                "look under the folder this file is in",
	"kinds":               "fix only these kinds of finding",
	"log list/task":       "only the events about the task with this ID, even once it is deleted or archived",
	"since":               "only the events at or after this time, YYYY-MM-DDTHH:MM:SSZ",
	"after":               "only the events after this seq, such as the last_seq of the list before",
	"log list/kinds":      "only these kinds of event",
	"before":              "move the events before this time, YYYY-MM-DDTHH:MM:SSZ; required, never in the future",
	"ids_from":            "the first ID the new root issues",
	"pick/direct":         "only the folder's own tasks and the folders directly in it",
	"pick/kinds":          "offer only these kinds: task, gate, folder",
	"frontier":            "offer the frontier's tasks, in work order",
	"query":               "fzf's starting query",
	"bash":                "print the bash script that defines pick's widget, for a key you bind, instead of picking",
	"zsh":                 "print the zsh script that defines pick's widget, for a key you bind, instead of picking",
}

// fileUsages are each convenience's line in help, by field.
var fileUsages = map[string]string{
	"notes": "read the notes from a file, or from standard input for -",
}

// hereUsages are each working-directory option's line in help, by
// command.
var hereUsages = map[string]string{
	"pick":        "the folder the working directory is in",
	"task list":   "the folder the working directory is in",
	"frontier":    "the folder the working directory is in",
	"claim-next":  "the folder the working directory is in",
	"folder list": "the folder the working directory is in",
	"context":     "the folder the working directory is in",
	"export":      "the folder the working directory is in",
	"where":       "the folder the working directory is in",
}

// exclusiveFolder is the usage of a command whose folder, --file and
// --here are alternatives.
const exclusiveFolder = "[<folder> | --file <path> | --here]"

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
		if c.Here != nil && c.Here.Flag != "" {
			c.Here.Usage = hereUsages[c.Name]
			if len(c.Uses) == 0 {
				c.Uses = []string{exclusiveFolder}
			}
		}
		switch c.Name {
		case "pick":
			c.Uses = []string{exclusiveFolder, "--bash", "--zsh"}
		case "batch":
			c.Uses = []string{"-i <file>"}
		case "log list":
			c.Uses = []string{"[<folder>] [--since <timestamp> | --after <seq>]"}
		case "log rotate":
			c.Uses = []string{"--before <timestamp>"}
		}
	}
	return commands
}
