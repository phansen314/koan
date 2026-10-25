package main_test

import (
	"fmt"
	"strings"
	"testing"
)

// A command sits under task only where the kind changes what it does,
// so each moved command's old spelling is usage, an unknown command
// after task, with its problem at the verb; it changes nothing.
func TestOldSpellings(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")                // 1
	ok(t, home, "task", "create", "a", "--folder", "/w") // 2
	ok(t, home, "task", "create", "b", "--folder", "/w") // 3
	for _, args := range [][]string{
		{"task", "why", "2"},
		{"task", "complete", "2"},
		{"task", "reopen", "2"},
		{"task", "block", "2", "3"},
		{"task", "unblock", "2", "3"},
		{"task", "claim", "2"},
		{"task", "claim-next"},
		{"task", "release", "2"},
		{"task", "frontier"},
	} {
		usageAt(t, home, args[1], args...)
	}
	// Nothing changed: the first task is open, unclaimed and waits on
	// nothing.
	if r := ok(t, home, "why", "2"); fmt.Sprint(r.Result()["blockers"]) != "[]" {
		t.Errorf("why: %s", r.Line())
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
	goldenNow(t, input(t, home, "claim", `{"task":"@2","extra":{"by":"me"}}`), "claim-input")
	golden(t, input(t, home, "release", `{"task":2}`), "release-input")
	goldenNow(t, input(t, home, "claim-next", `{"folder":"/w","extra":{"by":"me"}}`), "claim-next-input")
	ok(t, home, "complete", "2", "3")
	golden(t, input(t, home, "reopen", `{"tasks":[2,"@3"]}`), "reopen-input")
}
