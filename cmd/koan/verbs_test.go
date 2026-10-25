package main_test

import (
	"fmt"
	"strings"
	"testing"
)

// A command sits under task or folder only where no reference names the
// kind, so each old spelling is
// usage, an unknown command after task or folder, with its problem at
// the verb, and its message names the command it now is; it changes
// nothing. release is no command at all now.
func TestOldSpellings(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")                // 1
	ok(t, home, "task", "create", "a", "--folder", "/w") // 2
	ok(t, home, "task", "create", "b", "--folder", "/w") // 3
	golden(t, usageAt(t, home, "show", "task", "show", "2"), "task-show-old")
	golden(t, usageAt(t, home, "join", "folder", "join", "/w", "/w"), "folder-join-old")
	golden(t, usageAt(t, home, "update", "task", "update", "2"), "task-update-old")
	golden(t, usageAt(t, home, "mv", "folder", "mv", "/w", "/v"), "folder-mv-old")
	for _, args := range [][]string{
		{"task", "show", "2"},
		{"folder", "show", "/w"},
		{"task", "archive", "2"},
		{"folder", "archive", "/w"},
		{"task", "join", "2", "/w"},
		{"folder", "join", "/w", "/w"},
		{"task", "unjoin", "2", "/w"},
		{"folder", "unjoin", "/w", "/w"},
		{"task", "why", "2"},
		{"task", "complete", "2"},
		{"task", "reopen", "2"},
		{"task", "block", "2", "3"},
		{"task", "unblock", "2", "3"},
		{"task", "claim", "2"},
		{"task", "claim-next"},
		{"task", "unclaim", "2"},
		{"task", "frontier"},
		{"task", "update", "2"},
		{"folder", "update", "/w"},
		{"task", "mv", "2", "/"},
		{"folder", "mv", "/w", "/v"},
		{"task", "delete", "2"},
		{"folder", "delete", "/w"},
	} {
		r := usageAt(t, home, args[1], args...)
		if want := args[1] + " is koan " + args[1]; !strings.Contains(r.Error()["message"].(string), want) {
			t.Errorf("koan %q: message lacks %q: %s", args, want, r.Line())
		}
	}
	// A word that is no command of its own gets no pointer.
	for _, args := range [][]string{{"task", "release", "2"}, {"task", "frob"}, {"folder", "marker"}} {
		if r := usageAt(t, home, args[1], args...); strings.Contains(r.Error()["message"].(string), " is koan ") {
			t.Errorf("koan %q: %s", args, r.Line())
		}
	}
	golden(t, usageAt(t, home, "release", "release", "2"), "release-old")
	// Nothing changed: the first task is open, unclaimed, waits on
	// nothing, and is where it was, with its title.
	if r := ok(t, home, "why", "2"); fmt.Sprint(r.Result()["blockers"]) != "[]" {
		t.Errorf("why: %s", r.Line())
	}
	if field(t, home, "2", "title") != "a" || field(t, home, "2", "folder").(map[string]any)["path"] != "/w" {
		t.Errorf("task: %v", field(t, home, "2", "folder"))
	}
	if r := ok(t, home, "frontier", "/w", "--fields", "claim"); r.Result()["total"].(fmt.Stringer).String() != "2" || strings.Contains(r.Line(), `"claim":{`) {
		t.Errorf("frontier: %s", r.Line())
	}
}

// Each moved command takes its input through -i under its new name, and
// gives the result its schema of that name describes.
func TestMovedCommandsInput(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")                                 // 1
	ok(t, home, "task", "create", "a", "--folder", "/w")                  // 2
	ok(t, home, "task", "create", "b", "--folder", "/w", "--blocks", "2") // 3
	ok(t, home, "task", "create", "c", "--folder", "/w")                  // 4

	golden(t, input(t, home, "why", `{"task":"@2"}`), "why-input")
	golden(t, input(t, home, "frontier", `{"folder":"/w","fields":["title"]}`), "frontier-input")
	golden(t, input(t, home, "unblock", `{"task":2,"blocked_by":["@3"]}`), "unblock-input")
	goldenNow(t, input(t, home, "claim", `{"tasks":["@2"],"extra":{"by":"me"}}`), "claim-input")
	golden(t, input(t, home, "unclaim", `{"tasks":[2]}`), "unclaim-input")
	goldenNow(t, input(t, home, "claim-next", `{"folder":"/w","extra":{"by":"me"}}`), "claim-next-input")
	ok(t, home, "complete", "2", "3")
	golden(t, input(t, home, "reopen", `{"tasks":[2,"@3"]}`), "reopen-input")
}

// The marker group is gone: its old commands are unknown commands at
// marker, with no pointer to the new ones.
func TestMarkerGroupGone(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{{"marker", "bind", "2"}, {"marker", "unbind"}, {"marker"}} {
		r := koan(t, home, args...)
		if r.Exit != 2 || r.Kind() != "usage" || strings.Contains(r.Error()["message"].(string), " is koan ") {
			t.Errorf("koan %q: %s", args, r.Line())
		}
	}
}
