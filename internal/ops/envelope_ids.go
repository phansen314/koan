package ops

import (
	"fmt"
	"slices"

	"github.com/phansen314/ftask/internal/jsonio"
)

// EnvelopeIDs reads data as one envelope from another ftask command and
// returns the IDs it names, in order, without duplicates: its result's tasks'
// IDs, or the result's own id (pick-spec.md, Accepted envelopes). The IDs are
// taken as they are; the adapter judges them. reason is why data is no such
// envelope, or "".
func EnvelopeIDs(data []byte) ([]any, string) {
	const notEnvelope = "not an ftask envelope: "
	env, repeated, err := jsonio.ParseObject(data)
	switch {
	case err != nil:
		return nil, notEnvelope + err.Error()
	case len(repeated) > 0:
		return nil, notEnvelope + "repeated key at " + repeated[0]
	}
	ok, _ := env.Get("ok")
	switch ok {
	case false:
		e, _ := env.Get("error")
		eo, _ := e.(*jsonio.Object)
		var kind, msg any
		if eo != nil {
			kind, _ = eo.Get("kind")
			msg, _ = eo.Get("message")
		}
		k, _ := kind.(string)
		m, _ := msg.(string)
		if k == "" {
			return nil, notEnvelope + "ok is false, with no error kind"
		}
		return nil, "upstream failed with " + k + ": " + m
	case true:
	default:
		return nil, notEnvelope + "no ok field"
	}
	r, _ := env.Get("result")
	result, isObj := r.(*jsonio.Object)
	if !isObj {
		return nil, notEnvelope + "no result object"
	}
	var ids []any
	if tasks, has := result.Get("tasks"); has {
		items, isArr := tasks.([]any)
		if !isArr {
			return nil, notEnvelope + "result.tasks is not an array"
		}
		for i, item := range items {
			task, _ := item.(*jsonio.Object)
			var id any
			has := false
			if task != nil {
				id, has = task.Get("id")
			}
			if !has {
				return nil, fmt.Sprintf("result.tasks[%d] has no id", i)
			}
			ids = append(ids, id)
		}
	} else if id, has := result.Get("id"); has {
		ids = append(ids, id)
	} else {
		return nil, "no tasks or id in its result"
	}
	out := []any{}
	for _, id := range ids {
		if !slices.ContainsFunc(out, func(x any) bool { return jsonio.Equal(x, id) }) {
			out = append(out, id)
		}
	}
	return out, ""
}
