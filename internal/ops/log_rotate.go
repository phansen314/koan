package ops

import (
	"time"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
)

// LogRotateResult is log rotate's result, in its schema's order.
type LogRotateResult struct {
	Changed  bool    `json:"changed"`
	File     *string `json:"file"`
	FirstSeq *int64  `json:"first_seq"`
	LastSeq  *int64  `json:"last_seq"`
	Count    int     `json:"count"`
}

// LogRotate moves the oldest events, up to the first at or after
// --before, out of the table into a file in log/, and records rotated.
// --before is required, and never in the future.
func LogRotate() *cli.Command {
	return &cli.Command{
		Name: "log rotate",
		Options: []cli.Option{
			{Field: "before", Kind: cli.String, Arg: "timestamp"},
		},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			var before string
			if v, ok := r.required("before"); ok {
				if _, isString := v.(string); !isString {
					r.fault(r.at("before"), "%s must be a timestamp YYYY-MM-DDTHH:MM:SSZ", r.name("before"))
				} else if before = r.timestamp("before"); before != "" {
					if t, _ := core.ParseTimestamp(before); t.After(time.Now()) {
						r.fault(r.at("before"), "%s %s is in the future: only events already recorded can move", r.name("before"), before)
					}
				}
			}
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			moved, warnings, e := s.Rotate(before)
			if e != nil {
				return nil, warnings, e
			}
			if moved == nil {
				return LogRotateResult{}, warnings, nil
			}
			return LogRotateResult{Changed: true, File: &moved.File, FirstSeq: &moved.FirstSeq, LastSeq: &moved.LastSeq, Count: moved.Count}, warnings, nil
		},
	}
}
