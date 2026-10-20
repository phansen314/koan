package model

import (
	"reflect"
	"testing"

	"github.com/phansen314/koan/internal/core"
)

// The input's shape is checked whole before any reference resolves, as the
// spec's check order says, and invalid-input names every fault found.
func TestShapeBeforeReferences(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/work/a", nil, nil))  // @1, @2
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "x"})) // @3

	for _, c := range []struct {
		name     string
		run      func() error
		problems []string
	}{
		{"a blank title and a folder that names nothing", func() error {
			_, err := tr.CreateTask(TaskInput{Title: "   ", Folder: "/nope"})
			return err
		}, []string{"/title"}},
		{"every fault, in the schema's field order", func() error {
			_, err := tr.CreateTask(TaskInput{Title: "", Folder: "/nope", Priority: ptr(core.MaxInt + 1),
				Tags: []string{"Bad", "a", "a"}, BlockedBy: []string{"@999", "/"}, Blocks: []string{"/work.middle"}})
			return err
		}, []string{"/title", "/priority", "/tags/0", "/tags/2", "/blocked_by/1", "/blocks/0"}},
		{"an empty title on a task that names nothing", func() error {
			_, err := tr.UpdateTask("@999", Update{SetTitle: true, Title: ptr("")})
			return err
		}, []string{"/title"}},
		{"a value fault in a later batch part", func() error {
			_, err := tr.Batch(BatchInput{Tasks: []BatchTask{
				{TaskInput: TaskInput{Title: "a", Folder: "/nope"}},
				{TaskInput: TaskInput{Title: ""}},
			}})
			return err
		}, []string{"/tasks/1/title"}},
		{"a bad tag in a scope whose folder names nothing", func() error {
			_, err := tr.ScopedFrontier(Scope{Folder: "/nope", TagsAny: []string{"Bad"}})
			return err
		}, []string{"/tags_any/0"}},
		{"a path with no end where a task is taken", func() error {
			_, err := tr.Complete("/nope/x")
			return err
		}, []string{"/task"}},
		{"two spellings point at both references", func() error {
			_, err := tr.CreateTask(TaskInput{Title: "t", Folder: "/work", BlockedBy: []string{"/Work.start"}})
			return err
		}, []string{"/folder", "/blocked_by/0"}},
		{"a combination points at the object", func() error {
			_, err := tr.UpdateTask("3", Update{TagsReplaceAll: []string{"a"}, TagsAdd: []string{"b"}})
			return err
		}, []string{""}},
		{"nothing to change", func() error {
			_, err := tr.UpdateTask("3", Update{})
			return err
		}, []string{""}},
		{"no edge given", func() error {
			_, err := tr.Block("3", nil, nil)
			return err
		}, []string{""}},
		{"a priority or tags on a folder", func() error {
			_, err := tr.UpdateFolder("/work", Update{SetPriority: true, TagsAdd: []string{"a"}})
			return err
		}, []string{"/priority", "/tags_add"}},
		{"material in a batch edge", func() error {
			_, err := tr.Batch(BatchInput{Tasks: []BatchTask{{TaskInput: TaskInput{Title: "t", BlockedBy: []string{"41.start:x"}}}}})
			return err
		}, []string{"/tasks/0/blocked_by/0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := wantErr(t, c.run(), core.KindInvalidInput, "")
			if !reflect.DeepEqual(e.Problems, c.problems) {
				t.Errorf("problems %q, want %q", e.Problems, c.problems)
			}
		})
	}
	checked(t, tr)
}

// References resolve before the change is judged: a gate moved to a
// folder that names nothing is not-found, not gate.
func TestMoveTaskResolvesFirst(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/a", nil, nil))
	must[TaskCreated](t)(tr.CreateTask(TaskInput{Title: "x", Blocks: []string{"/a"}}))
	_, _, err := tr.MoveTask("/a.done", "/nope")
	wantErr(t, err, core.KindNotFound, "")
	_, _, err = tr.MoveTask("/a.done", "/")
	wantErr(t, err, core.KindConflict, core.RuleGate)
}

// A folder move may name both spellings only to respell the folder's own
// name.
func TestMoveFolderSpellings(t *testing.T) {
	tr := newTree()
	must[[]FolderAt](t)(tr.CreateFolder("/work/a", nil, nil))
	_, err := tr.MoveFolder("/work/a", "/Work/b")
	e := wantErr(t, err, core.KindInvalidInput, "")
	if !reflect.DeepEqual(e.Problems, []string{"/folder", "/to"}) {
		t.Errorf("problems %q", e.Problems)
	}
	_, err = tr.MoveFolder("/work/a", "/Work/A")
	wantErr(t, err, core.KindInvalidInput, "")
	if r := must[FolderMoved](t)(tr.MoveFolder("/work/a", "/work/A")); r.Path != "/work/A" {
		t.Errorf("respelled: %+v", r)
	}
	if r := must[FolderMoved](t)(tr.MoveFolder("/work", "/Work")); r.Path != "/Work" {
		t.Errorf("respelled: %+v", r)
	}
	checked(t, tr)
}

// In a batch, a refusal points at the parts at fault, an edge's included.
func TestBatchPointers(t *testing.T) {
	tr := newTree()
	_, err := tr.Batch(BatchInput{Tasks: []BatchTask{
		{TaskInput: TaskInput{Title: "a"}},
		{TaskInput: TaskInput{Title: "b", Blocks: []string{"@999"}}},
	}})
	e := wantErr(t, err, core.KindNotFound, "")
	if !reflect.DeepEqual(e.Problems, []string{"/tasks/1/blocks/0"}) {
		t.Errorf("problems %q", e.Problems)
	}
}
