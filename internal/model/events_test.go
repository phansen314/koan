package model

import (
	"fmt"
	"strings"
	"testing"
)

// kinds is the model's last events, each as kind@id.
func kinds(tr *Tree) string {
	var out []string
	for _, e := range tr.Events {
		out = append(out, fmt.Sprintf("%s@%d", e.Kind, e.ID))
	}
	return strings.Join(out, " ")
}

// The rules doc's The log, its examples among them: a change records one
// event for each thing it changes, in the PRD's order, derived state
// after the rest; what it leaves as it was, a refusal, and a state that
// changes and changes back record nothing.
func TestEvents(t *testing.T) {
	tr := newTree()
	steps := []struct {
		name string
		do   func() error
		want string
	}{
		{"missing parents first, the given one last", func() error { _, err := tr.CreateFolder("/w/api", nil, nil); return err },
			"created@1 created@2"},
		{"a task, its gate, its edge, and the folders it unfinishes, the deepest first", func() error {
			_, err := tr.CreateTask(TaskInput{Title: "a", Folder: "/w/api", BlockedBy: []string{"/w/api.start"}})
			return err
		}, "created@3 created@4 blocked@3 unfinished@2 unfinished@1"},
		{"tasks 5 and 6", func() error {
			if _, err := tr.CreateTask(TaskInput{Title: "b"}); err != nil {
				return err
			}
			_, err := tr.CreateTask(TaskInput{Title: "c"})
			return err
		}, "created@6"},
		{"7 waits on 5", func() error { _, err := tr.CreateTask(TaskInput{Title: "d", BlockedBy: []string{"5"}}); return err },
			"created@7 blocked@7"},
		{"block 7 --blocked-by 5,6 records the edge to 6 alone", func() error { _, err := tr.Block("7", []string{"5", "6"}, nil); return err },
			"blocked@7"},
		{"completing two releases what waited on both", func() error { _, err := tr.Complete("5", "6"); return err },
			"completed@5 completed@6 released@7"},
		{"completing a task already complete records nothing", func() error { _, err := tr.Complete("5"); return err },
			""},
		{"a refusal records nothing", func() error { _, err := tr.Complete("5", "5"); return err },
			""},
		{"a claim and an update", func() error {
			if _, err := tr.ClaimTask([]string{"7"}, nil); err != nil {
				return err
			}
			_, err := tr.Update([]string{"7", "/w"}, Update{SetTitle: true, Title: ptr("e")})
			return err
		}, "updated@7 updated@1"},
		{"reopening reblocks what waits on it", func() error { _, err := tr.Reopen("5"); return err },
			"reopened@5 reblocked@7"},
		{"completing ends the claim after the task's completed", func() error { _, err := tr.Complete("7"); return err },
			"completed@7 unclaimed@7"},
		{"a move: the folder's moved alone, not what is under it, and the folder it leaves finished", func() error {
			_, err := tr.Move(MvDest, "/", "/w/api")
			return err
		}, "moved@2 finished@1"},
		{"a delete: the edges between what goes and what stays, then the deleted", func() error {
			_, err := tr.Delete([]string{"5"}, nil, nil)
			return err
		}, "unblocked@7 deleted@5"},
	}
	for _, s := range steps {
		err := s.do()
		if strings.HasPrefix(s.name, "a refusal") != (err != nil) {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got := kinds(tr); got != s.want {
			t.Errorf("%s:\n got %s\nwant %s", s.name, got, s.want)
		}
	}
}
