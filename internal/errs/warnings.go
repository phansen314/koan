package errs

import (
	"fmt"
	"slices"
	"strings"
)

// WarningKind is a warning kind. Callers ignore unknown kinds.
type WarningKind string

const (
	WarnUnusableFile      WarningKind = "unusable-file"
	WarnDuplicateID       WarningKind = "duplicate-id"
	WarnDanglingReference WarningKind = "dangling-reference"
	WarnUnreadableFolder  WarningKind = "unreadable-folder"
	WarnNotesMissing      WarningKind = "notes-missing"
)

// UnusableReason says why an unusable-file warning's file is unusable.
type UnusableReason string

const (
	UnusableUnreadable        UnusableReason = "unreadable"
	UnusableCorrupt           UnusableReason = "corrupt"
	UnusableUnsupportedFormat UnusableReason = "unsupported-format"
)

// Warning is one entry of the envelope's warnings. Paths and IDs are always
// present; their meaning and order are per kind.
type Warning struct {
	Kind    WarningKind    `json:"kind"`
	Message string         `json:"message"`
	Paths   []string       `json:"paths"`
	IDs     []int64        `json:"ids"`
	Reason  UnusableReason `json:"reason,omitempty"`
	Code    string         `json:"code,omitempty"`
}

// UnreadableFile: the task file at path, of task id, could not be read; code
// is the symbolic OS error.
func UnreadableFile(path string, id int64, code string) Warning {
	w := unusableFile(path, id, UnusableUnreadable)
	w.Message += " (" + code + ")"
	w.Code = code
	return w
}

// CorruptFile: the task file at path, of task id, is corrupt.
func CorruptFile(path string, id int64) Warning {
	return unusableFile(path, id, UnusableCorrupt)
}

// UnsupportedFile: the task file at path, of task id, has an unsupported
// format version.
func UnsupportedFile(path string, id int64) Warning {
	return unusableFile(path, id, UnusableUnsupportedFormat)
}

// unusableFile is an unusable-file warning. Only UnreadableFile gives a code,
// which the warning has exactly when reason is unreadable.
func unusableFile(path string, id int64, reason UnusableReason) Warning {
	return Warning{Kind: WarnUnusableFile, Message: fmt.Sprintf("%s: skipped, %s", path, reason), Paths: []string{path}, IDs: []int64{id}, Reason: reason}
}

// DuplicateID: id has several task files; paths are those in the operation's
// scope, in tree order.
func DuplicateID(id int64, paths []string) Warning {
	return Warning{
		Kind:    WarnDuplicateID,
		Message: fmt.Sprintf("task ID %d has %d task files", id, len(paths)),
		Paths:   nonNil(paths),
		IDs:     []int64{id},
	}
}

// DanglingReference: the task file at path, of task referring, has missing in
// its blocked_by, and no task has ID missing.
func DanglingReference(path string, referring, missing int64) Warning {
	return Warning{
		Kind:    WarnDanglingReference,
		Message: fmt.Sprintf("task %d is blocked by task %d, which does not exist", referring, missing),
		Paths:   []string{path},
		IDs:     []int64{referring, missing},
	}
}

// UnreadableFolder: the folder at filesystem path could not be listed.
func UnreadableFolder(path, code string) Warning {
	return Warning{
		Kind:    WarnUnreadableFolder,
		Message: fmt.Sprintf("%s: cannot list folder (%s)", path, code),
		Paths:   []string{path},
		IDs:     []int64{},
		Code:    code,
	}
}

// NotesMissing: task id was written but its .md at path could not be. code
// is "" for an OS error with no symbolic name, and then left out.
func NotesMissing(path string, id int64, code string) Warning {
	msg := fmt.Sprintf("%s: task %d written, but its notes could not be", path, id)
	if code != "" {
		msg += " (" + code + ")"
	}
	return Warning{
		Kind:    WarnNotesMissing,
		Message: msg,
		Paths:   []string{path},
		IDs:     []int64{id},
		Code:    code,
	}
}

// Collector gathers an invocation's warnings: one per distinct problem, in a
// deterministic order. The zero value is ready to use.
type Collector struct {
	seen map[string]bool
	list []Warning
}

// Add records w unless a warning for the same problem is already recorded;
// the first one recorded is kept.
func (c *Collector) Add(w Warning) {
	k := dedupKey(w)
	if c.seen[k] {
		return
	}
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	c.seen[k] = true
	c.list = append(c.list, w)
}

// Warnings returns the warnings sorted by kind, then the first entry of
// paths, then ids compared element by element as numbers. Never nil.
func (c *Collector) Warnings() []Warning {
	ws := append([]Warning{}, c.list...)
	slices.SortStableFunc(ws, compareWarnings)
	return ws
}

// dedupKey identifies the problem a warning reports: per file for
// unusable-file, per folder for unreadable-folder, per ID for duplicate-id,
// per (referring, missing) pair for dangling-reference, per .md for
// notes-missing.
func dedupKey(w Warning) string {
	switch w.Kind {
	case WarnUnusableFile, WarnUnreadableFolder, WarnNotesMissing:
		return string(w.Kind) + "\x00" + first(w.Paths)
	case WarnDuplicateID, WarnDanglingReference:
		return fmt.Sprint(w.Kind, "\x00", w.IDs)
	default:
		return fmt.Sprint(w.Kind, "\x00", w.Paths, "\x00", w.IDs)
	}
}

func compareWarnings(a, b Warning) int {
	if c := strings.Compare(string(a.Kind), string(b.Kind)); c != 0 {
		return c
	}
	if c := strings.Compare(first(a.Paths), first(b.Paths)); c != 0 {
		return c
	}
	return slices.Compare(a.IDs, b.IDs)
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
