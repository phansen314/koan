package main_test

import (
	"fmt"
	"testing"
)

// show takes a task, a gate included, or a folder, or the root, by any
// reference but material; what the reference names decides which the
// result holds. A subject is not-found only when it names nothing.
func TestShowEitherKind(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")                // 1
	ok(t, home, "task", "create", "t", "--folder", "/work") // 2
	ok(t, home, "block", "/work.done", "--blocked-by", "2") // 3, the done gate

	goldenNow(t, ok(t, home, "show", "/work.done"), "show-task-path-gate")
	for ref, want := range map[string]string{
		"2": "task 2", "@2": "task 2", "3": "task 3", "@1.done": "task 3", "/work.done": "task 3",
		"1": "folder 1", "@1": "folder 1", "/work": "folder 1", "/": "folder <nil>",
	} {
		r := ok(t, home, "show", ref)
		got := ""
		for _, kind := range []string{"task", "folder"} {
			if rec, has := r.Result()[kind].(map[string]any); has {
				got += fmt.Sprint(kind, " ", rec["id"])
			}
		}
		if got != want {
			t.Errorf("show %s: %s, want %s", ref, r.Line(), want)
		}
	}

	r := fails(t, home, "invalid-input", "", "show", "@1:x")
	golden(t, r, "show-material")
	if problemFields(r) != "/ref" {
		t.Errorf("material: %s", r.Line())
	}
	r = fails(t, home, "not-found", "", "show", "99")
	golden(t, r, "show-not-found")
	if r.Error()["message"] != "@99 was never issued" {
		t.Errorf("not-found: %s", r.Line())
	}
	// A gate not made yet names nothing.
	fails(t, home, "not-found", "", "show", "/work.start")
}

// show takes its subject through -i as ref; the old fields task and
// folder are fields it doesn't take.
func TestShowInput(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work")                // 1
	ok(t, home, "task", "create", "t", "--folder", "/work") // 2

	goldenNow(t, input(t, home, "show", `{"ref":2}`), "show-input-task")
	golden(t, input(t, home, "show", `{"ref":"/work"}`), "show-input-folder")
	golden(t, input(t, home, "show", `{"ref":"/"}`), "show-input-root")
	r := input(t, home, "show", `{"task":2}`)
	golden(t, r, "show-input-old-field")
	if r.Exit != 1 || r.Kind() != "invalid-input" || problemFields(r) != "/ref /task" {
		t.Errorf("old field: %s", r.Line())
	}
	if r := input(t, home, "show", `{"folder":"/work"}`); r.Kind() != "invalid-input" || problemFields(r) != "/folder /ref" {
		t.Errorf("old field: %s", r.Line())
	}
}
