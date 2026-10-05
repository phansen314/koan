// Package errs defines ftask's error and warning kinds, the warning collector,
// and the table of symbolic OS error names.
//
// The kinds, their details, and their JSON shapes are specified in
// operations.md (Error kinds, Warning kinds) and, for usage, cli-spec.md
// (Usage errors). errs imports none of ftask's own packages.
package errs

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Kind is an error kind. Callers treat an unknown kind as a generic failure.
type Kind string

const (
	KindInvalidInput      Kind = "invalid-input"
	KindEnvironment       Kind = "environment"
	KindNotInitialized    Kind = "not-initialized"
	KindNotFound          Kind = "not-found"
	KindConflict          Kind = "conflict"
	KindBusy              Kind = "busy"
	KindCorrupt           Kind = "corrupt"
	KindIO                Kind = "io"
	KindUnsupportedFormat Kind = "unsupported-format"
	KindInternal          Kind = "internal"

	// KindUsage is raised by the CLI only; no operation raises it.
	KindUsage Kind = "usage"

	// pick's own kinds (pick-spec.md, Errors), raised by no operation. Their
	// details are pick's.
	KindUnavailable Kind = "unavailable"
	KindCancelled   Kind = "cancelled"
	KindIncomplete  Kind = "incomplete"
)

// Error is the error object of the output envelope. Kind and Details are the
// contract; Message is for humans. Details is always a JSON object; its Go type
// depends on Kind. Partial is set only when a multi-file write or setup failed
// after some of its effects took place.
type Error struct {
	Kind    Kind   `json:"kind"`
	Message string `json:"message"`
	Details any    `json:"details"`
	Partial any    `json:"partial,omitempty"`
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Message }

// WithPartial returns e with Partial set to p, the operation's partial result.
func (e *Error) WithPartial(p any) *Error {
	e.Partial = p
	return e
}

// empty is the details of kinds that carry none ({}).
type empty struct{}

// Problem is one invalid input: Field is a JSON Pointer into the input.
type Problem struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type InvalidInputDetails struct {
	Problems          []Problem `json:"problems"`
	ProblemsTruncated bool      `json:"problems_truncated,omitempty"`
}

// MaxProblems is how many problems an error lists at most, so that bad input
// or a garbage file cannot produce a huge error.
const MaxProblems = 20

// InvalidInput reports every problem found, sorted by field, then by reason,
// both compared byte by byte, so the same input always yields the same list;
// then cut to the first MaxProblems, though the message counts them all.
// An empty problems is a bug, reported as internal.
func InvalidInput(problems []Problem) *Error {
	if len(problems) == 0 {
		return Internal("invalid-input with no problems")
	}
	ps := sortProblems(problems)
	msg := fmt.Sprintf("%d invalid inputs", len(ps))
	if len(ps) == 1 {
		msg = fmt.Sprintf("invalid input at %q: %s", ps[0].Field, ps[0].Reason)
	}
	d := InvalidInputDetails{}
	d.Problems, d.ProblemsTruncated = capProblems(ps)
	return &Error{Kind: KindInvalidInput, Message: msg, Details: d}
}

// sortProblems returns a sorted copy of problems: by field, then by reason,
// both compared byte by byte.
func sortProblems(problems []Problem) []Problem {
	ps := slices.Clone(problems)
	slices.SortFunc(ps, func(a, b Problem) int {
		return cmp.Or(strings.Compare(a.Field, b.Field), strings.Compare(a.Reason, b.Reason))
	})
	return ps
}

// capProblems returns the first MaxProblems of ps, and whether any were cut.
func capProblems(ps []Problem) ([]Problem, bool) {
	if len(ps) > MaxProblems {
		return ps[:MaxProblems], true
	}
	return ps, false
}

type EnvironmentDetails struct {
	Variable string `json:"variable"`
}

// Environment reports that the environment lacks what ftask needs to locate
// its files; variable names the environment variable, e.g. HOME.
func Environment(variable string) *Error {
	return &Error{
		Kind:    KindEnvironment,
		Message: fmt.Sprintf("cannot locate ftask's files: $%s is unset or not an absolute path", variable),
		Details: EnvironmentDetails{Variable: variable},
	}
}

// Missing names the first absent piece of a root that is not initialized.
type Missing string

const (
	MissingConfig   Missing = "config"
	MissingRoot     Missing = "root"
	MissingMetadata Missing = "metadata"
)

type NotInitializedDetails struct {
	Missing Missing `json:"missing"`
}

func NotInitialized(missing Missing) *Error {
	var msg string
	switch missing {
	case MissingConfig:
		msg = "no config: run init"
	case MissingRoot:
		msg = "the configured root does not exist"
	case MissingMetadata:
		msg = "the root has no ftask.json"
	default:
		msg = "not initialized"
	}
	return &Error{Kind: KindNotInitialized, Message: msg, Details: NotInitializedDetails{Missing: missing}}
}

// NotFoundDetails lists every missing thing. All three lists are always
// present, empty when not applicable.
type NotFoundDetails struct {
	Folders []string `json:"folders"`
	IDs     []int64  `json:"ids"`
	Paths   []string `json:"paths"`
}

// NotFound reports missing tree folder paths, task IDs, and filesystem paths.
func NotFound(folders []string, ids []int64, paths []string) *Error {
	d := NotFoundDetails{Folders: nonNil(folders), IDs: nonNil(ids), Paths: nonNil(paths)}
	var parts []string
	for _, f := range d.Folders {
		parts = append(parts, "folder "+f)
	}
	for _, id := range d.IDs {
		parts = append(parts, fmt.Sprintf("task %d", id))
	}
	parts = append(parts, d.Paths...)
	return &Error{Kind: KindNotFound, Message: "not found: " + strings.Join(parts, ", "), Details: d}
}

// Rule is the rule that refused a conflicting operation. Callers treat an
// unknown rule as a generic conflict.
type Rule string

const (
	RuleAcyclic           Rule = "acyclic"
	RuleIDExhausted       Rule = "id-exhausted"
	RuleConfigExists      Rule = "config-exists"
	RuleRootNotEmpty      Rule = "root-not-empty"
	RuleDuplicateID       Rule = "duplicate-id"
	RuleIDAboveLastID     Rule = "id-above-last-id"
	RuleNotEmpty          Rule = "not-empty"
	RuleDestinationExists Rule = "destination-exists"
)

// ConflictDetails carries Cycles only for RuleAcyclic, where Cycles[i] is one
// cycle through IDs[i].
type ConflictDetails struct {
	Rule   Rule      `json:"rule"`
	IDs    []int64   `json:"ids"`
	Cycles [][]int64 `json:"cycles,omitempty"`
}

// Conflict reports a refusal under any rule but RuleAcyclic, which has its
// own constructor; RuleAcyclic here is a bug, reported as internal.
func Conflict(rule Rule, ids []int64) *Error {
	if rule == RuleAcyclic {
		return Internal("acyclic conflict without cycles")
	}
	var msg string
	switch rule {
	case RuleIDExhausted:
		msg = "no task ID left under the ID ceiling"
	case RuleConfigExists:
		msg = "a config already exists"
	case RuleRootNotEmpty:
		msg = "the root is a non-empty directory without ftask.json"
	case RuleDuplicateID:
		msg = fmt.Sprintf("more than one task file has ID %s", joinIDs(ids))
	case RuleIDAboveLastID:
		msg = fmt.Sprintf("task ID %s is above last_id; repair the tree with doctor first", joinIDs(ids))
	case RuleNotEmpty:
		msg = "the folder holds tasks, folders, or other files; delete it recursively to remove them too"
	case RuleDestinationExists:
		msg = "something already exists where the folder, or the task's notes, would move"
	default:
		msg = "conflict: " + string(rule)
	}
	return &Error{Kind: KindConflict, Message: msg, Details: ConflictDetails{Rule: rule, IDs: nonNil(ids)}}
}

// Acyclic reports blockers that would create a cycle: cycles[i] is the cycle
// through ids[i], at least two distinct IDs, ids[i] among them. Anything else
// is a bug, reported as internal.
func Acyclic(ids []int64, cycles [][]int64) *Error {
	if len(ids) == 0 || len(cycles) != len(ids) {
		return Internal(fmt.Sprintf("acyclic conflict with %d ids and %d cycles", len(ids), len(cycles)))
	}
	for i, c := range cycles {
		if len(c) < 2 {
			return Internal(fmt.Sprintf("acyclic conflict: cycle through %d has %d IDs", ids[i], len(c)))
		}
		if !slices.Contains(c, ids[i]) {
			return Internal(fmt.Sprintf("acyclic conflict: cycle %v does not pass through %d", c, ids[i]))
		}
		if sorted := slices.Sorted(slices.Values(c)); len(slices.Compact(sorted)) != len(c) {
			return Internal(fmt.Sprintf("acyclic conflict: cycle %v repeats an ID", c))
		}
	}
	return &Error{
		Kind:    KindConflict,
		Message: fmt.Sprintf("blocker(s) %s would create a cycle", joinIDs(ids)),
		Details: ConflictDetails{Rule: RuleAcyclic, IDs: nonNil(ids), Cycles: cycles},
	}
}

func Busy() *Error {
	return &Error{Kind: KindBusy, Message: "another write held the write lock throughout the wait", Details: empty{}}
}

// CorruptReason says why a file is corrupt.
type CorruptReason string

const (
	CorruptNotJSON        CorruptReason = "not-json"
	CorruptInvalid        CorruptReason = "invalid"
	CorruptUnexpectedFile CorruptReason = "unexpected-file"
)

// CorruptCause is why a file is corrupt, with what is wrong: Problems for an
// invalid JSON file, each at a JSON Pointer into the file; Detail for a file
// that is not-json, or for an invalid config, which is not JSON.
type CorruptCause struct {
	Reason   CorruptReason
	Problems []Problem
	Detail   string
}

type CorruptDetails struct {
	Path              string        `json:"path"`
	Reason            CorruptReason `json:"reason"`
	Problems          []Problem     `json:"problems,omitempty"`
	ProblemsTruncated bool          `json:"problems_truncated,omitempty"`
	Detail            string        `json:"detail,omitempty"`
}

// Corrupt reports a file corrupt for a reason that carries nothing more:
// unexpected-file.
func Corrupt(path string, reason CorruptReason) *Error {
	return CorruptBy(path, CorruptCause{Reason: reason})
}

// CorruptBy reports a file corrupt for cause c. Problems are sorted as
// InvalidInput sorts them, then cut to the first MaxProblems; the
// message names the first, or the detail. A not-json or invalid cause with
// nothing to say what is wrong is a bug, reported as internal.
func CorruptBy(path string, c CorruptCause) *Error {
	d := CorruptDetails{Path: path, Reason: c.Reason, Detail: c.Detail}
	var msg string
	switch {
	case len(c.Problems) > 0:
		ps := sortProblems(c.Problems)
		msg = fmt.Sprintf("%s: corrupt: at %q: %s", path, ps[0].Field, ps[0].Reason)
		if len(ps) > 1 {
			msg += fmt.Sprintf(" (and %d more)", len(ps)-1)
		}
		d.Problems, d.ProblemsTruncated = capProblems(ps)
	case c.Detail != "":
		msg = fmt.Sprintf("%s: corrupt: %s", path, c.Detail)
	case c.Reason == CorruptUnexpectedFile:
		msg = fmt.Sprintf("%s: corrupt (%s)", path, c.Reason)
	default:
		return Internal(fmt.Sprintf("%s: corrupt (%s) with nothing to say why", path, c.Reason))
	}
	return &Error{Kind: KindCorrupt, Message: msg, Details: d}
}

type IODetails struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

// IO reports an OS error by its symbolic name. Use FromOS to build one from a
// Go error, which falls back to internal when no name applies.
func IO(path, code string) *Error {
	return &Error{Kind: KindIO, Message: fmt.Sprintf("%s: %s", path, code), Details: IODetails{Path: path, Code: code}}
}

type UnsupportedFormatDetails struct {
	Path      string  `json:"path"`
	Found     int64   `json:"found"`
	Supported []int64 `json:"supported"`
}

func UnsupportedFormat(path string, found int64, supported []int64) *Error {
	return &Error{
		Kind:    KindUnsupportedFormat,
		Message: fmt.Sprintf("%s: format version %d is not supported", path, found),
		Details: UnsupportedFormatDetails{Path: path, Found: found, Supported: nonNil(supported)},
	}
}

// Internal reports a bug ftask detects. Panics are never turned into one.
func Internal(message string) *Error {
	return &Error{Kind: KindInternal, Message: message, Details: empty{}}
}

// UsageProblem is one command-line problem. Argument is the offending token,
// nil when the problem is something missing. A pointer, since "" is a token.
type UsageProblem struct {
	Argument *string `json:"argument,omitempty"`
	Reason   string  `json:"reason"`
}

type UsageDetails struct {
	Problems []UsageProblem `json:"problems"`
}

// Usage reports every command-line problem, in the order the parser found
// them. An empty problems is a bug, reported as internal.
func Usage(problems []UsageProblem) *Error {
	if len(problems) == 0 {
		return Internal("usage error with no problems")
	}
	return &Error{Kind: KindUsage, Message: "usage: " + problems[0].Reason, Details: UsageDetails{Problems: problems}}
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return strings.Join(parts, ", ")
}
