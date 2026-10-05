package model

import (
	"slices"

	"github.com/phansen314/koan/internal/jsonio"
)

// TaskFile is a task file's content (see design-spec.md, Task file schema).
// Field order is the schema's key order, which is the order written.
type TaskFile struct {
	Schema      int64          `json:"schema"`
	ID          ID             `json:"id"`
	Title       Title          `json:"title"`
	Priority    *int64         `json:"priority"`
	CreatedAt   Timestamp      `json:"created_at"`
	CompletedAt *Timestamp     `json:"completed_at"`
	UpdatedAt   Timestamp      `json:"updated_at"`
	BlockedBy   []ID           `json:"blocked_by"`
	Tags        []Tag          `json:"tags"`
	Extra       *jsonio.Object `json:"extra"`
}

// Open reports whether the task is open: completed_at is its sole source of
// truth.
func (t *TaskFile) Open() bool { return t.CompletedAt == nil }

// Normalize puts t in the form it is written and returned in: sets sorted
// (blocked_by ascending, tags by name) and never nil, extra never nil.
func (t *TaskFile) Normalize() {
	t.BlockedBy = slices.Clone(t.BlockedBy)
	if t.BlockedBy == nil {
		t.BlockedBy = []ID{}
	}
	slices.Sort(t.BlockedBy)
	t.Tags = slices.Clone(t.Tags)
	if t.Tags == nil {
		t.Tags = []Tag{}
	}
	slices.Sort(t.Tags)
	if t.Extra == nil {
		t.Extra = &jsonio.Object{}
	}
}

// Encode returns the task file's bytes, per design-spec.md, File format.
func (t TaskFile) Encode() ([]byte, error) {
	t.Normalize()
	return jsonio.MarshalFile(t)
}

// Task is a task as operations return it: the task file's fields, plus where
// it lives (the shared task schema).
type Task struct {
	TaskFile
	Folder    FolderPath `json:"folder"`
	NotesPath string     `json:"notes_path"`
}

// Readiness is a task's derived state (see design-spec.md, Dependencies).
type Readiness string

const (
	Ready    Readiness = "ready"
	Blocked  Readiness = "blocked"
	Complete Readiness = "complete"
)

// TaskView is a task plus its derived readiness (the shared task-view
// schema). Blocking lists, ascending, the blocked_by IDs that currently
// block it; it is non-empty exactly when Readiness is Blocked.
type TaskView struct {
	Task
	Readiness Readiness `json:"readiness"`
	Blocking  []ID      `json:"blocking"`
}

// Normalize puts v in the form it is returned in: the task file's fields as
// TaskFile.Normalize leaves them, and Blocking sorted and never nil.
func (v *TaskView) Normalize() {
	v.TaskFile.Normalize()
	v.Blocking = slices.Clone(v.Blocking)
	if v.Blocking == nil {
		v.Blocking = []ID{}
	}
	slices.Sort(v.Blocking)
}

// ViewFields are a task view's field names, in the order it is returned
// (the task-field schema).
var ViewFields = []string{
	"schema", "id", "title", "priority", "created_at", "completed_at", "updated_at",
	"blocked_by", "tags", "extra", "folder", "notes_path", "readiness", "blocking",
}

// Project returns v with only the named fields, and id whether named or not,
// in ViewFields order (the task-projection schema). Names not in ViewFields
// are ignored.
func (v TaskView) Project(fields []string) *jsonio.Object {
	out := &jsonio.Object{}
	for _, name := range ViewFields {
		if name == "id" || slices.Contains(fields, name) {
			out.Members = append(out.Members, jsonio.Member{Key: name, Value: v.field(name)})
		}
	}
	return out
}

// field is the value of v's field name, as it is encoded.
func (v TaskView) field(name string) any {
	switch name {
	case "schema":
		return v.Schema
	case "id":
		return v.ID
	case "title":
		return v.Title
	case "priority":
		return v.Priority
	case "created_at":
		return v.CreatedAt
	case "completed_at":
		return v.CompletedAt
	case "updated_at":
		return v.UpdatedAt
	case "blocked_by":
		return v.BlockedBy
	case "tags":
		return v.Tags
	case "extra":
		return v.Extra
	case "folder":
		return v.Folder
	case "notes_path":
		return v.NotesPath
	case "readiness":
		return v.Readiness
	case "blocking":
		return v.Blocking
	}
	return nil
}

// RootFile is koan.json's content (see design-spec.md, Root metadata).
type RootFile struct {
	Schema int64 `json:"schema"`
	LastID int64 `json:"last_id"`
}

// Encode returns koan.json's bytes, per design-spec.md, File format.
func (r RootFile) Encode() ([]byte, error) {
	return jsonio.MarshalFile(r)
}
