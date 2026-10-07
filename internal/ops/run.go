package ops

import (
	"fmt"
	"slices"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/store"
)

// Envelope is the output envelope (operations.md, Output envelope): Result on
// success, Error on failure, Warnings always present.
type Envelope struct {
	OK       bool           `json:"ok"`
	Result   any            `json:"result,omitempty"`
	Error    *errs.Error    `json:"error,omitempty"`
	Warnings []errs.Warning `json:"warnings"`
}

// Failed is the envelope of an error raised before any operation ran — a
// usage error, or input that could not be read — so there are no warnings.
func Failed(e *errs.Error) Envelope {
	return Envelope{Error: e, Warnings: []errs.Warning{}}
}

// runner runs an operation on its decoded input, recording warnings in w. On
// success it returns the operation's output, a JSON object.
type runner func(env Env, in any, w *errs.Collector) (any, *errs.Error)

// runners holds each implemented operation, by operation name; every name is
// also in decoders.
var runners = map[string]runner{
	"version":       typed(runVersion),
	"info":          typed(runInfo),
	"doctor":        typed(runDoctor),
	"repair":        typed(runRepair),
	"show":          typed(runShow),
	"init":          typed(runInit),
	"create":        typed(runCreate),
	"create-batch":  typed(runCreateBatch),
	"create-folder": typed(runCreateFolder),
	"done":          typed(runDone),
	"reopen":        typed(runReopen),
	"update":        typed(runUpdate),
	"block":         typed(runBlock),
	"unblock":       typed(runUnblock),
	"delete":        typed(runDelete),
	"move":          typed(runMove),
	"delete-folder": typed(runDeleteFolder),
	"move-folder":   typed(runMoveFolder),
	"list":          typed(runList),
	"frontier":      typed(runFrontier),
	"why":           typed(runWhy),
}

// typed adapts an operation's function over its own input type to a runner.
func typed[I any](fn func(Env, I, *errs.Collector) (any, *errs.Error)) runner {
	return func(env Env, in any, w *errs.Collector) (any, *errs.Error) {
		i, ok := in.(I)
		if !ok {
			return nil, errs.Internal(fmt.Sprintf("input of type %T, want %T", in, i))
		}
		return fn(env, i, w)
	}
}

// Run runs operation name on input, the JSON object the caller built, and
// returns its envelope. problems are invalid-input problems the caller found
// while building input — a JSON option value that is not valid JSON, input
// resolution — which are reported together with the adapter's in one
// invalid-input; an adapter problem at a field the caller reported, or
// within it, is dropped, since the caller's says what is wrong there (a
// value left out for not being UTF-8 is not also "required"). Input that
// could not be read at all (jsonio failed, or a
// repeated key) never reaches Run: the caller reports it with Failed.
func Run(name string, input *jsonio.Object, problems []errs.Problem, env Env) Envelope {
	var w errs.Collector
	result, e := run(name, input, problems, env, &w)
	if e == nil && result == nil {
		e = errs.Internal("operation " + name + " returned no result")
	}
	if e != nil {
		return Envelope{Error: e, Warnings: w.Warnings()}
	}
	return Envelope{OK: true, Result: result, Warnings: w.Warnings()}
}

func run(name string, input *jsonio.Object, problems []errs.Problem, env Env, w *errs.Collector) (any, *errs.Error) {
	in, e := Validate(name, input, problems)
	if e != nil {
		return nil, e
	}
	r, ok := runners[name]
	if !ok {
		return nil, errs.Internal("operation " + name + " is not implemented")
	}
	if name != "version" { // version reads no file
		if e := store.Migrate(env.Env, w); e != nil {
			return nil, e
		}
	}
	return r(env, in, w)
}

// Validate decodes input for name, as Run does before running it: its typed
// form, or one invalid-input with the caller's problems and the adapter's
// (see Run). A command that runs no operation of its own, pick, validates
// its input with it.
func Validate(name string, input *jsonio.Object, problems []errs.Problem) (any, *errs.Error) {
	in, p, e := Decode(name, input)
	if e != nil {
		return nil, e
	}
	if all := append(slices.Clone(problems), unreported(p.List(), problems)...); len(all) > 0 {
		return nil, errs.InvalidInput(all)
	}
	return in, nil
}

// unreported returns the adapter's problems at fields the caller's problems
// do not cover: not the same field, and not within it.
func unreported(adapter, caller []errs.Problem) []errs.Problem {
	var out []errs.Problem
	for _, a := range adapter {
		if !slices.ContainsFunc(caller, func(c errs.Problem) bool {
			return a.Field == c.Field || strings.HasPrefix(a.Field, c.Field+"/")
		}) {
			out = append(out, a)
		}
	}
	return out
}
