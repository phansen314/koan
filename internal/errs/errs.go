// Package errs holds the three things a command reports besides its result:
// the error it failed with, warnings, and doctor's findings
// (spec/operations.md, Conventions).
package errs

import (
	"errors"
	"fmt"
	"syscall"
)

// Kind is an error's kind.
type Kind string

const (
	Usage             Kind = "usage"
	InvalidInput      Kind = "invalid-input"
	NotInitialized    Kind = "not-initialized"
	Corrupt           Kind = "corrupt"
	UnsupportedFormat Kind = "unsupported-format"
	MigrationPending  Kind = "migration-pending"
	Busy              Kind = "busy"
	NotFound          Kind = "not-found"
	Conflict          Kind = "conflict"
	IO                Kind = "io"
	Internal          Kind = "internal"
)

// Conflict rules.
const (
	RuleDuplicateID       = "duplicate-id"
	RuleNotAFolder        = "not-a-folder"
	RuleDestinationExists = "destination-exists"
	RuleNotEmpty          = "not-empty"
	RuleCycle             = "cycle"
	RuleTaskDone          = "task-done"
	RuleClaimed           = "claimed"
)

// Problem is one thing wrong with an input or a file.
type Problem struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// Error is the one error a failed command reports. Every list is present,
// empty when it has nothing to say.
type Error struct {
	Kind         Kind      `json:"kind"`
	Rule         *string   `json:"rule"`
	Message      string    `json:"message"`
	IDs          []int64   `json:"ids"`
	Folders      []string  `json:"folders"`
	Paths        []string  `json:"paths"`
	Problems     []Problem `json:"problems"`
	ChangedPaths []string  `json:"changed_paths"`
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Message }

// New makes an error of the given kind.
func New(kind Kind, format string, args ...any) *Error {
	return &Error{
		Kind: kind, Message: fmt.Sprintf(format, args...),
		IDs: []int64{}, Folders: []string{}, Paths: []string{}, Problems: []Problem{}, ChangedPaths: []string{},
	}
}

// NewConflict makes a conflict error refused by rule.
func NewConflict(rule, format string, args ...any) *Error {
	e := New(Conflict, format, args...)
	e.Rule = &rule
	return e
}

// HasRule reports whether e is a conflict refused by rule.
func (e *Error) HasRule(rule string) bool { return e != nil && e.Rule != nil && *e.Rule == rule }

func (e *Error) WithIDs(ids ...int64) *Error       { e.IDs = append(e.IDs, ids...); return e }
func (e *Error) WithFolders(fs ...string) *Error   { e.Folders = append(e.Folders, fs...); return e }
func (e *Error) WithPaths(ps ...string) *Error     { e.Paths = append(e.Paths, ps...); return e }
func (e *Error) WithProblems(ps ...Problem) *Error { e.Problems = append(e.Problems, ps...); return e }
func (e *Error) WithChanged(paths ...string) *Error {
	e.ChangedPaths = append(e.ChangedPaths, paths...)
	return e
}

// Invalid is an invalid-input error listing every problem found.
func Invalid(ps []Problem) *Error {
	msg := fmt.Sprintf("the input has %d problems", len(ps))
	if len(ps) == 1 {
		msg = "the input is invalid: " + ps[0].Field + " " + ps[0].Reason
	}
	return New(InvalidInput, "%s", msg).WithProblems(ps...)
}

// FromOS is the io error for an OS error on path that no caller gave a
// meaning to.
func FromOS(path string, err error) *Error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return New(IO, "%s: %s", path, errno.Error()).WithPaths(path)
	}
	return New(IO, "%s: %s", path, err.Error()).WithPaths(path)
}

// IsErrno reports whether err is, or wraps, one of the given errnos.
func IsErrno(err error, errnos ...syscall.Errno) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	for _, e := range errnos {
		if errno == e {
			return true
		}
	}
	return false
}

// Warning kinds.
const (
	WarnUnusableFile      = "unusable-file"
	WarnDanglingReference = "dangling-reference"
)

// Warning is a problem that did not stop the command.
type Warning struct {
	Kind    string   `json:"kind"`
	Message string   `json:"message"`
	IDs     []int64  `json:"ids"`
	Paths   []string `json:"paths"`
}

// Finding kinds, in the order doctor reports them.
var FindingKinds = []string{
	"temp-leftover", "id-above-counter", "dangling-reference", "stale-claim", "orphan-notes",
	"duplicate-id", "cycle", "unusable-file", "old-format", "skipped-entry",
}

// Finding is one thing doctor found wrong.
type Finding struct {
	Kind       string   `json:"kind"`
	Message    string   `json:"message"`
	Paths      []string `json:"paths"`
	IDs        []int64  `json:"ids"`
	Repairable bool     `json:"repairable"`
}
