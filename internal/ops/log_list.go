package ops

import (
	"encoding/json"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// LogListResult is log list's result, in its schema's order.
type LogListResult struct {
	Events    []store.Event `json:"events"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated"`
	LastSeq   *int64        `json:"last_seq"`
}

// eventKinds are the kinds of event, as --kinds takes them.
var eventKinds = []string{store.EventCreated, store.EventUpdated, store.EventCompleted, store.EventReopened,
	store.EventBlocked, store.EventUnblocked, store.EventClaimed, store.EventUnclaimed, store.EventMoved,
	store.EventDeleted, store.EventArchived, store.EventReleased, store.EventReblocked, store.EventFinished,
	store.EventUnfinished, store.EventInitialized, store.EventRotated}

// LogList lists the log's events, oldest first, from the table in one
// snapshot. Its folder and --task are matched as written, never resolved
// against the tree, so they are never not-found, case-mismatch or
// invalid-input for what they name, and it gives no pending-filesystem:
// its paths are history, not places on disk.
func LogList() *cli.Command {
	return &cli.Command{
		Name:    "log list",
		Subject: &cli.Option{Field: "folder", Complete: foldersOnly.complete()},
		Options: []cli.Option{
			{Field: "task", Kind: cli.String, Nullable: true, Arg: "id", Complete: tasksOnly.complete()},
			{Field: "since", Kind: cli.String, Nullable: true, Arg: "timestamp"},
			{Field: "after", Kind: cli.Integer, Nullable: true},
			{Field: "kinds", Kind: cli.List},
			{Field: "limit", Kind: cli.Integer, Nullable: true},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			var f store.EventFilter
			f.Path, f.Folder = r.asWritten("folder", true)
			_, f.Task = r.asWritten("task", false)
			f.Since = r.timestamp("since")
			f.After = r.seq("after")
			if f.Since != "" && f.After != 0 {
				r.fault("", "give at most one of %s and %s", r.name("since"), r.name("after"))
			}
			if _, ok := r.value("kinds"); ok {
				f.Kinds = r.choices("kinds", eventKinds, nil)
			}
			f.Limit = r.limit("limit", 100)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			res := LogListResult{}
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				last, err := tx.LastSeq()
				if err != nil {
					return nil, err
				}
				if f.After > last {
					e := core.NotFound(nil, nil, "%s %d is past the last seq this root has issued, %d", r.name("after"), f.After, last)
					e.Problems = []string{cli.Pointer("after")}
					return nil, e
				}
				if res.Events, res.Total, err = tx.Events(f); err != nil {
					return nil, err
				}
				res.Truncated = res.Total > len(res.Events)
				if n := len(res.Events); n > 0 {
					res.LastSeq = &res.Events[n-1].Seq
				} else if f.After != 0 {
					res.LastSeq = &f.After
				}
				return nil, nil
			})
			if e != nil {
				return nil, nil, e
			}
			return res, warnings, nil
		},
	}
}

// asWritten reads a reference log list matches as written: with paths, a
// path from the root, or else a plain ID. A form that needs the tree to
// resolve, such as an anchored path or a gate, is a fault. It gives the
// path, or the ID; neither when it isn't given, or for paths null.
func (r *input) asWritten(field string, paths bool) (path string, id int64) {
	v, ok := r.value(field)
	if !ok || (!paths && v == nil) {
		return "", 0
	}
	takes := "a plain ID"
	if paths {
		takes = "a path from the root, or a plain ID"
	}
	var s string
	switch v := v.(type) {
	case string:
		s = v
	case json.Number:
		if id, ok := core.ParseID(string(v)); ok {
			return "", id
		}
		r.fault(r.at(field), "%s is not an ID", v)
		return "", 0
	default:
		r.fault(r.at(field), "%s must be %s", r.name(field), takes)
		return "", 0
	}
	ref, err := core.ParseRef(s)
	if err != nil {
		r.fault(r.at(field), "%s", reason(err))
		return "", 0
	}
	switch {
	case ref.HasMaterial || ref.End != core.EndNone || ref.Kind == core.TargetAnchored || ref.Kind == core.TargetLabel ||
		ref.Kind == core.TargetPath && !paths:
		r.fault(r.at(field), "%q needs the tree to resolve, and %s is matched as written: give %s", s, r.name(field), takes)
	case ref.Kind == core.TargetID:
		return "", ref.ID
	default:
		return ref.TargetString(), 0
	}
	return "", 0
}

// timestamp is a timestamp field, or "" when it isn't given or is null.
func (r *input) timestamp(field string) string {
	v, ok := r.value(field)
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		r.fault(r.at(field), "%s must be a timestamp YYYY-MM-DDTHH:MM:SSZ, or null", r.name(field))
		return ""
	}
	if _, err := core.ParseTimestamp(s); err != nil {
		r.fault(r.at(field), "%s", reason(err))
		return ""
	}
	return s
}

// seq is a seq field, 1 or more; 0 when it isn't given or is null.
func (r *input) seq(field string) int64 {
	v, ok := r.value(field)
	if !ok || v == nil {
		return 0
	}
	n, isNum := v.(json.Number)
	var i int64
	var err error
	if isNum {
		i, err = core.ParseInt(string(n))
	}
	if !isNum || err != nil || i < 1 {
		r.fault(r.at(field), "%s must be a seq, an integer from 1 to %d, or null", r.name(field), core.MaxInt)
		return 0
	}
	return i
}
