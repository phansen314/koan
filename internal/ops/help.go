package ops

import "github.com/phansen314/koan/internal/cli"

// shorts are each command's line in help.
var shorts = map[string]string{
	"task create":   "Make a task, with its notes file",
	"show":          "Show one task, a gate included, or one folder",
	"task list":     "List tasks in tree order",
	"frontier":      "List ready, unclaimed tasks in work order",
	"why":           "Show what a task waits on, and what to finish to move it",
	"task update":   "Change a task's title, priority, tags or extra",
	"complete":      "Complete tasks, ending their claims",
	"reopen":        "Open complete tasks again",
	"block":         "Add edges: what a task waits on, or what waits on it",
	"unblock":       "Remove edges: what a task waits on, or what waits on it",
	"join":          "Join a task, or a folder through its gates, to folders",
	"unjoin":        "Remove the edges of joining a task or a folder to folders",
	"task mv":       "Move tasks, with their notes, into a folder",
	"task delete":   "Delete tasks for good",
	"archive":       "Move a complete task, or a finished folder, out of the tree, into the archive",
	"claim":         "Reserve a task",
	"claim-next":    "Claim the first task of the frontier",
	"unclaim":       "Give up a claim",
	"folder create": "Make a folder, and any missing parents",
	"folder list":   "List folders in tree order",
	"folder update": "Change a folder's title or extra",
	"folder mv":     "Move folders into a folder, or rename one",
	"folder delete": "Delete folders for good",
	"batch":         "Make folders and tasks, with their edges, in one change",
	"where":         "Resolve a reference, or find the folder a file is in",
	"context":       "Brief a folder: the folders above it, its notes, its open tasks",
	"export":        "Print a folder's whole subtree, with the edges in and out of it, for tools",
	"marker bind":   "Bind a directory outside the tree, such as a worktree, to a folder",
	"marker unbind": "Remove a directory's marker",
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
	"task create/folder":     "the folder to make it in; the root when left out",
	"recursive":              "include subfolders; --recursive=false for the folder's own only",
	"priority":               "an integer, higher first, or null",
	"tags":                   "its tags",
	"blocked_by":             "what it waits on: tasks, gates or folders",
	"blocks":                 "what waits on it: tasks, gates or folders",
	"join":                   "the folders it joins: it waits on their start gates, and their done gates on it",
	"folder create/join":     "the folders it joins, through its gates",
	"extra":                  "a JSON object of your own fields",
	"claim/extra":            "a JSON object kept with the claim, such as who holds it",
	"claim-next/extra":       "a JSON object kept with the claim, such as who holds it",
	"notes":                  "the notes, as text",
	"title":                  "the title",
	"folder create/title":    "the title, or null for none",
	"folder update/title":    "the title, or null for none",
	"tags_any":               "only tasks with any of these tags",
	"tags_all":               "only tasks with all of these tags",
	"limit":                  "at most this many; 100 when left out, null for all",
	"fields":                 "only these fields, after id",
	"state":                  "open, complete, or both; open when left out",
	"readiness":              "ready, blocked, or both",
	"claimed":                "only claimed tasks; --claimed=false for only unclaimed, --claimed=null for both",
	"tags_add":               "add these tags",
	"tags_remove":            "remove these tags",
	"tags_replace_all":       "replace every tag with these",
	"extra_merge":            "merge this JSON object into extra, at the top level",
	"extra_remove":           "remove this key from extra; repeat for more",
	"extra_replace_all":      "replace extra with this JSON object",
	"task mv/into":           "the folder to move them into, in place of the last word",
	"folder mv/into":         "the folder to move them into, in place of the last word",
	"folder mv/rename":       "the last word is the folder's new path, never a folder to move into",
	"contents":               "delete their tasks and folders too",
	"folder delete/material": "delete the files of yours in them too",
	"marker bind/force":      "bind a checkout, or a directory bound to another folder",
	"where/file":             "find the folder this file is in",
	"context/file":           "brief the folder this file is in; the working directory when left out",
	"export/file":            "export the folder this file is in; the working directory when left out",
	"kinds":                  "fix only these kinds of finding",
	"ids_from":               "the first ID the new root issues",
	"pick/recursive":         "include subfolders; --recursive=false for the folder's own tasks and the folders directly in it",
	"pick/kinds":             "offer only these kinds: task, gate, folder",
	"frontier":               "offer the frontier's tasks, in work order",
	"query":                  "fzf's starting query",
	"bash":                   "print the bash script that defines pick's widget, for a key you bind, instead of picking",
	"zsh":                    "print the zsh script that defines pick's widget, for a key you bind, instead of picking",
}

// fileUsages are each convenience's line in help, by field.
var fileUsages = map[string]string{
	"notes": "read the notes from a file, or from standard input for -",
}

// hereUsages are each working-directory option's line in help, by
// command.
var hereUsages = map[string]string{
	"pick": "pick from the folder the working directory's marker binds",
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
		if c.Here != nil && c.Here.Flag != "" {
			c.Here.Usage = hereUsages[c.Name]
		}
	}
	return commands
}
