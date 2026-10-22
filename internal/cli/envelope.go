// Package cli turns a command line into a command's input, runs the
// command, and prints its one envelope.
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// Error kinds, as the spec's error schema lists them.
const (
	KindUsage             = "usage"
	KindInvalidInput      = "invalid-input"
	KindNotFound          = "not-found"
	KindConflict          = "conflict"
	KindBusy              = "busy"
	KindNotInitialized    = "not-initialized"
	KindCorrupt           = "corrupt"
	KindUnsupportedFormat = "unsupported-format"
	KindIO                = "io"
	KindInternal          = "internal"
)

// Kinds is every error kind, in the spec's order.
var Kinds = []string{
	KindUsage, KindInvalidInput, KindNotFound, KindConflict, KindBusy, KindNotInitialized,
	KindCorrupt, KindUnsupportedFormat, KindIO, KindInternal,
}

// Conflict rules, as the spec's error schema lists them.
const (
	RuleCaseMismatch = "case-mismatch"
	RuleCycle        = "cycle"
	RuleClaimed      = "claimed"
	RuleNotFinished  = "not-finished"
	RuleNotEmpty     = "not-empty"
	RuleHasMaterial  = "has-material"
	RuleGate         = "gate"
	RuleIntoItself   = "into-itself"
	RuleExists       = "exists"
	RuleNotARoot     = "not-a-root"
	RuleCheckout     = "checkout"
	RuleForeignFiles = "foreign-files"
	RuleComplete     = "complete"
	RuleBound        = "bound"
	RuleNoIDs        = "no-ids"
)

// Rules is every conflict rule, in the spec's order.
var Rules = []string{
	RuleCaseMismatch, RuleCycle, RuleClaimed, RuleNotFinished, RuleNotEmpty, RuleHasMaterial, RuleGate,
	RuleIntoItself, RuleExists, RuleNotARoot, RuleCheckout, RuleForeignFiles, RuleComplete, RuleBound,
	RuleNoIDs,
}

// Exit codes.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
	ExitOutput  = 3
)

// ExitCode is the exit code a failure of this kind gives.
func ExitCode(kind string) int {
	if kind == KindUsage {
		return ExitUsage
	}
	return ExitFailure
}

// Problem is one fault in the input: a JSON Pointer into it, or for
// usage, the argument at fault as typed.
type Problem struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// Archived says where an archived ID went.
type Archived struct {
	ID      int64  `json:"id"`
	Archive string `json:"archive"`
}

// Error is the one error a failure carries. Rule is set only for a
// conflict.
type Error struct {
	Kind     string     `json:"kind"`
	Rule     *string    `json:"rule"`
	Message  string     `json:"message"`
	IDs      []int64    `json:"ids"`
	Paths    []string   `json:"paths"`
	Problems []Problem  `json:"problems"`
	Archived []Archived `json:"archived"`
}

// Error lets an *Error pass through cobra, which returns what a
// command's RunE refused.
func (e *Error) Error() string { return e.Message }

// NewError makes an error of a kind other than conflict.
func NewError(kind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

// Conflict makes a conflict error naming its rule.
func Conflict(rule, message string) *Error {
	return &Error{Kind: KindConflict, Rule: &rule, Message: message}
}

// Usage makes a usage error for the argument at fault.
func Usage(arg, reason string) *Error {
	e := NewError(KindUsage, reason)
	e.Problems = []Problem{{Field: arg, Reason: reason}}
	return e
}

// InvalidInput makes an invalid-input error carrying every fault found.
func InvalidInput(message string, problems ...Problem) *Error {
	e := NewError(KindInvalidInput, message)
	e.Problems = problems
	return e
}

// IO makes an io error for the paths the operating system refused.
func IO(message string, paths ...string) *Error {
	e := NewError(KindIO, message)
	e.Paths = paths
	return e
}

// MarshalJSON prints every list, empty ones as [].
func (e *Error) MarshalJSON() ([]byte, error) {
	type plain Error
	p := plain(*e)
	if p.IDs == nil {
		p.IDs = []int64{}
	}
	if p.Paths == nil {
		p.Paths = []string{}
	}
	if p.Problems == nil {
		p.Problems = []Problem{}
	}
	if p.Archived == nil {
		p.Archived = []Archived{}
	}
	return json.Marshal(p)
}

// Warning sits beside a result or an error and never stops a command.
type Warning struct {
	Kind    string   `json:"kind"`
	Message string   `json:"message"`
	IDs     []int64  `json:"ids"`
	Paths   []string `json:"paths"`
}

// MarshalJSON prints every list, empty ones as [].
func (w Warning) MarshalJSON() ([]byte, error) {
	type plain Warning
	p := plain(w)
	if p.IDs == nil {
		p.IDs = []int64{}
	}
	if p.Paths == nil {
		p.Paths = []string{}
	}
	return json.Marshal(p)
}

type success struct {
	OK       bool      `json:"ok"`
	Result   any       `json:"result"`
	Warnings []Warning `json:"warnings"`
}

type failure struct {
	OK       bool      `json:"ok"`
	Error    *Error    `json:"error"`
	Warnings []Warning `json:"warnings"`
}

// encode renders the envelope as one line. A result that can't be
// encoded is a bug, and becomes an internal error.
func encode(result any, e *Error, warnings []Warning) ([]byte, *Error) {
	if warnings == nil {
		warnings = []Warning{}
	}
	var env any = success{OK: true, Result: result, Warnings: warnings}
	if e != nil {
		env = failure{OK: false, Error: e, Warnings: warnings}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		ie := NewError(KindInternal, "could not encode the output: "+err.Error())
		buf.Reset()
		if err := enc.Encode(failure{Error: ie, Warnings: warnings}); err != nil {
			panic(err)
		}
		return buf.Bytes(), ie
	}
	return buf.Bytes(), e
}

// stderrLine is the one line for a person watching, or "" for none.
func stderrLine(e *Error, warnings []Warning) string {
	switch {
	case e != nil:
		msg := strings.Join(strings.Fields(e.Message), " ")
		return "koan: " + e.Kind + ": " + msg + "\n"
	case len(warnings) == 1:
		return "koan: 1 warning\n"
	case len(warnings) > 0:
		return "koan: " + strconv.Itoa(len(warnings)) + " warnings\n"
	}
	return ""
}

// write prints the envelope and the standard-error line, and gives the
// exit code.
func write(stdout, stderr io.Writer, result any, e *Error, warnings []Warning) int {
	line, e := encode(result, e, warnings)
	if _, err := stdout.Write(line); err != nil {
		return ExitOutput
	}
	if s := stderrLine(e, warnings); s != "" {
		io.WriteString(stderr, s)
	}
	if e != nil {
		return ExitCode(e.Kind)
	}
	return ExitOK
}
