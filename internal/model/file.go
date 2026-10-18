package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"

	"github.com/phansen314/koan/internal/errs"
)

// Problem is one rule a file or an input breaks.
type Problem = errs.Problem

func prob(field, reason string) Problem { return Problem{Field: field, Reason: reason} }

// TaskFile is a task file's content. Field order is the order koan writes.
type TaskFile struct {
	Schema      int64          `json:"schema"`
	ID          ID             `json:"id"`
	Title       string         `json:"title"`
	Priority    *int64         `json:"priority"`
	CreatedAt   string         `json:"created_at"`
	CompletedAt *string        `json:"completed_at"`
	UpdatedAt   string         `json:"updated_at"`
	BlockedBy   []ID           `json:"blocked_by"`
	Tags        []string       `json:"tags"`
	Extra       map[string]any `json:"extra"`
}

// Open reports whether the task is open: not done.
func (t *TaskFile) Open() bool { return t.CompletedAt == nil }

// Claim is one entry of the state file's claims.
type Claim struct {
	ClaimedAt string         `json:"claimed_at"`
	Extra     map[string]any `json:"extra"`
}

// State is the state file's content.
type State struct {
	Schema int64            `json:"schema"`
	LastID ID               `json:"last_id"`
	Claims map[string]Claim `json:"claims"`
}

// ClaimKey is the key of a task's claim in State.Claims.
func ClaimKey(id ID) string { return strconv.FormatInt(id, 10) }

// FolderFile is a folder file's content. Field order is the order koan
// writes.
type FolderFile struct {
	Schema int64          `json:"schema"`
	ID     ID             `json:"id"`
	Title  *string        `json:"title"`
	Extra  map[string]any `json:"extra"`
}

// FolderRecord is a folder as every command that returns one returns it.
type FolderRecord struct {
	ID        *ID            `json:"id"`
	Path      Folder         `json:"path"`
	Dir       string         `json:"dir"`
	Title     *string        `json:"title"`
	Extra     map[string]any `json:"extra"`
	NotesPath *string        `json:"notes_path"`
}

// NewFolderFile is the folder file of a new folder.
func NewFolderFile(id ID) *FolderFile {
	return &FolderFile{Schema: FileFormat, ID: id, Extra: map[string]any{}}
}

var folderKeys = []string{"schema", "id", "title", "extra"}

// DecodeFolderFile reads a folder file in this binary's format and checks
// every rule. A nil error and no problems means the file is usable.
func DecodeFolderFile(data []byte) (*FolderFile, []Problem, error) {
	m, err := Keys(data)
	if err != nil {
		return nil, nil, err
	}
	if ps := missing(m, folderKeys, "title"); ps != nil {
		return nil, ps, nil
	}
	var f FolderFile
	if err := Decode(data, &f); err != nil {
		return nil, []Problem{prob("", err.Error())}, nil
	}
	return &f, f.Check(), nil
}

// Check lists the rules the folder file breaks.
func (f *FolderFile) Check() []Problem {
	var ps []Problem
	bad := func(field, reason string) { ps = append(ps, prob(field, reason)) }
	if f.Schema != FileFormat {
		bad("/schema", fmt.Sprintf("is not %d", FileFormat))
	}
	if !ValidID(f.ID) {
		bad("/id", "is not an ID")
	}
	if f.Title != nil {
		if title, reason := CleanTitle(*f.Title); reason != "" {
			bad("/title", reason)
		} else if title != *f.Title {
			bad("/title", "has leading or trailing whitespace")
		}
	}
	return ps
}

// Meta is @koan.json's content.
type Meta struct {
	Schema int64 `json:"schema"`
}

// Task is a task as every command returns it: the file's fields and five
// derived ones.
type Task struct {
	TaskFile
	Folder    Folder    `json:"folder"`
	NotesPath string    `json:"notes_path"`
	Readiness Readiness `json:"readiness"`
	Blocking  []ID      `json:"blocking"`
	Claim     *Claim    `json:"claim"`
}

// TaskFields are the names of a Task's fields, in the order they are written.
var TaskFields = []string{
	"schema", "id", "title", "priority", "created_at", "completed_at", "updated_at",
	"blocked_by", "tags", "extra", "folder", "notes_path", "readiness", "blocking", "claim",
}

// Encode writes v as koan writes every file: two-space indentation, map keys
// sorted, no HTML escaping, and a trailing newline.
func Encode(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Decode reads one JSON object into v, refusing unknown keys and anything
// after the object. Numbers inside an `extra` keep their exact text.
func Decode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after the JSON value")
	}
	return nil
}

// Keys reads the top-level keys of a JSON object, each with its raw value.
func Keys(data []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := Decode(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("not a JSON object")
	}
	return m, nil
}

// SchemaOf reads just the `schema` of a file: the format it is in.
func SchemaOf(data []byte) (int64, error) {
	m, err := Keys(data)
	if err != nil {
		return 0, err
	}
	raw, ok := m["schema"]
	if !ok {
		return 0, fmt.Errorf("has no schema")
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil || n < 1 {
		return 0, fmt.Errorf("schema is not a positive integer")
	}
	return n, nil
}

// missing lists what is wrong with an object's keys, as problems: the keys
// of want that m lacks, or holds as null where null is not allowed, and any
// key that is not in want. Keys are matched exactly: encoding/json would
// take "Title" for "title".
func missing(m map[string]json.RawMessage, want []string, nullable ...string) []Problem {
	var ps []Problem
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(want, k) {
			ps = append(ps, prob("/"+k, "is not a key of this file"))
		}
	}
	for _, k := range want {
		raw, ok := m[k]
		switch {
		case !ok:
			ps = append(ps, prob("/"+k, "is missing"))
		case string(raw) == "null" && !slices.Contains(nullable, k):
			ps = append(ps, prob("/"+k, "is null"))
		}
	}
	return ps
}

var taskKeys = TaskFields[:10]

// DecodeTaskFile reads a task file in this binary's format and checks every
// rule. A nil error and no problems means the file is usable.
func DecodeTaskFile(data []byte) (*TaskFile, []Problem, error) {
	m, err := Keys(data)
	if err != nil {
		return nil, nil, err
	}
	if ps := missing(m, taskKeys, "priority", "completed_at"); ps != nil {
		return nil, ps, nil
	}
	var t TaskFile
	if err := Decode(data, &t); err != nil {
		return nil, []Problem{prob("", err.Error())}, nil
	}
	return &t, t.Check(), nil
}

// Check lists the rules the task file breaks.
func (t *TaskFile) Check() []Problem {
	var ps []Problem
	bad := func(field, reason string) { ps = append(ps, prob(field, reason)) }
	if t.Schema != FileFormat {
		bad("/schema", fmt.Sprintf("is not %d", FileFormat))
	}
	if !ValidID(t.ID) {
		bad("/id", "is not a task ID")
	}
	if title, reason := CleanTitle(t.Title); reason != "" {
		bad("/title", reason)
	} else if title != t.Title {
		bad("/title", "has leading or trailing whitespace")
	}
	if p := t.Priority; p != nil && (*p > MaxInt || *p < -MaxInt) {
		bad("/priority", "is out of range")
	}
	if !ValidStamp(t.CreatedAt) {
		bad("/created_at", "is not a timestamp")
	}
	if t.CompletedAt != nil && !ValidStamp(*t.CompletedAt) {
		bad("/completed_at", "is not a timestamp")
	}
	if !ValidStamp(t.UpdatedAt) {
		bad("/updated_at", "is not a timestamp")
	}
	seen := map[ID]bool{}
	for i, b := range t.BlockedBy {
		f := fmt.Sprintf("/blocked_by/%d", i)
		switch {
		case !ValidID(b):
			bad(f, "is not a task ID")
		case b == t.ID:
			bad(f, "is the task's own ID")
		case seen[b]:
			bad(f, "is repeated")
		}
		seen[b] = true
	}
	tags := map[string]bool{}
	for i, tag := range t.Tags {
		f := fmt.Sprintf("/tags/%d", i)
		switch {
		case !ValidTag(tag):
			bad(f, "is not a valid tag")
		case tags[tag]:
			bad(f, "is repeated")
		}
		tags[tag] = true
	}
	return ps
}

// Normalize puts the sets in the order koan writes them.
func (t *TaskFile) Normalize() {
	if t.BlockedBy == nil {
		t.BlockedBy = []ID{}
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	if t.Extra == nil {
		t.Extra = map[string]any{}
	}
	slices.Sort(t.BlockedBy)
	slices.Sort(t.Tags)
}

// DecodeState reads a state file and checks it.
func DecodeState(data []byte) (*State, []Problem, error) {
	m, err := Keys(data)
	if err != nil {
		return nil, nil, err
	}
	if ps := missing(m, []string{"schema", "last_id", "claims"}); ps != nil {
		return nil, ps, nil
	}
	var s State
	if err := Decode(data, &s); err != nil {
		return nil, []Problem{prob("", err.Error())}, nil
	}
	var ps []Problem
	var claims map[string]json.RawMessage
	json.Unmarshal(m["claims"], &claims)
	for _, k := range slices.Sorted(maps.Keys(claims)) {
		if cm, err := Keys(claims[k]); err == nil {
			for _, p := range missing(cm, []string{"claimed_at", "extra"}) {
				ps = append(ps, prob("/claims/"+k+p.Field, p.Reason))
			}
		}
	}
	if ps != nil {
		return nil, ps, nil
	}
	if s.Schema != FileFormat {
		ps = append(ps, prob("/schema", fmt.Sprintf("is not %d", FileFormat)))
	}
	if s.LastID < 0 || s.LastID > MaxID {
		ps = append(ps, prob("/last_id", "is out of range"))
	}
	for _, k := range slices.Sorted(maps.Keys(s.Claims)) {
		c := s.Claims[k]
		if id, err := strconv.ParseInt(k, 10, 64); err != nil || !ValidID(id) || ClaimKey(id) != k {
			ps = append(ps, prob("/claims/"+k, "is not keyed by a task ID"))
		}
		if !ValidStamp(c.ClaimedAt) {
			ps = append(ps, prob("/claims/"+k+"/claimed_at", "is not a timestamp"))
		}
		if c.Extra == nil {
			ps = append(ps, prob("/claims/"+k+"/extra", "is missing"))
		}
	}
	return &s, ps, nil
}

// DecodeMeta reads @koan.json.
func DecodeMeta(data []byte) (*Meta, []Problem, error) {
	n, err := SchemaOf(data)
	if err != nil {
		if _, kerr := Keys(data); kerr != nil {
			return nil, nil, kerr
		}
		return nil, []Problem{prob("/schema", err.Error())}, nil
	}
	// A @koan.json in another format may hold anything else: only its
	// schema is read, so that it is reported as that format.
	if n != Format {
		return &Meta{Schema: n}, nil, nil
	}
	m, _ := Keys(data)
	if ps := missing(m, []string{"schema"}); ps != nil {
		return nil, ps, nil
	}
	return &Meta{Schema: n}, nil, nil
}
