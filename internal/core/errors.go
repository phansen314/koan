// Package core holds koan's pure logic: references, names and values,
// readiness, the orders and the cycle check. It does no I/O.
package core

import (
	"fmt"
	"strings"
)

// Error kinds and conflict rules, as the spec names them.
const (
	KindInvalidInput = "invalid-input"
	KindNotFound     = "not-found"
	KindConflict     = "conflict"

	RuleCaseMismatch    = "case-mismatch"
	RuleCycle           = "cycle"
	RuleClaimed         = "claimed"
	RuleNotFinished     = "not-finished"
	RuleNotEmpty        = "not-empty"
	RuleHasMaterial     = "has-material"
	RuleIntoItself      = "into-itself"
	RuleExists          = "exists"
	RuleAlreadyComplete = "already-complete"
	RuleCheckout        = "checkout"
	RuleForeignFiles    = "foreign-files"
	RuleBound           = "bound"
	RuleNoIDs           = "no-ids"
)

// Error is a failure with one kind from the spec. A conflict also names its
// rule. IDs and Paths carry what the spec's error table says they carry.
type Error struct {
	Kind     string
	Rule     string
	Message  string
	IDs      []int64
	Paths    []string
	Archived []int64
	// Problems are JSON Pointers into the input: every fault for
	// invalid-input, and for a cycle the fields whose edges close it.
	Problems []string
}

func (e *Error) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("%s (%s): %s", e.Kind, e.Rule, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func invalid(format string, args ...any) *Error {
	return &Error{Kind: KindInvalidInput, Message: fmt.Sprintf(format, args...)}
}

// NotFound reports references that name nothing.
func NotFound(ids []int64, paths []string, format string, args ...any) *Error {
	return &Error{Kind: KindNotFound, IDs: ids, Paths: paths, Message: fmt.Sprintf(format, args...)}
}

// Conflict reports a refusal by one of the spec's conflict rules.
func Conflict(rule string, ids []int64, paths []string, format string, args ...any) *Error {
	return &Error{Kind: KindConflict, Rule: rule, IDs: ids, Paths: paths, Message: fmt.Sprintf(format, args...)}
}

// IsKind reports whether err is a core Error of the given kind and, when rule
// isn't empty, the given rule.
func IsKind(err error, kind, rule string) bool {
	e, ok := err.(*Error)
	return ok && e.Kind == kind && (rule == "" || e.Rule == rule)
}

// FormatIDs writes ids as "@1, @2".
func FormatIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("@%d", id)
	}
	return strings.Join(parts, ", ")
}
