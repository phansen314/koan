package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/markers"
	"github.com/phansen314/koan/internal/store"
)

// MarkerUnbindResult is marker unbind's result, in its schema's order.
type MarkerUnbindResult struct {
	Dir     string `json:"dir"`
	Removed bool   `json:"removed"`
}

// MarkerUnbind removes a directory's marker: the temp file, marker.json,
// .gitignore, then .koan/. Like bind, it takes no lock.
func MarkerUnbind() *cli.Command {
	return &cli.Command{
		Name:    "marker unbind",
		Options: []cli.Option{{Field: "dir", Kind: cli.Path}},
		Here:    &cli.Here{Field: "dir"},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in}
			dir := r.absPath("dir", true)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			if _, e := markerDir(s.Root, dir); e != nil {
				return nil, nil, e
			}
			var names []string
			var there bool
			warnings, e := fsstep.Read(s, func(tx *store.Tx) ([]fsstep.Path, error) {
				var err error
				names, there, err = koanFiles(dir)
				return nil, err
			})
			if e != nil {
				return nil, nil, e
			}
			res := MarkerUnbindResult{Dir: dir}
			if there {
				removed, err := markers.Remove(dir, names)
				if err != nil {
					return nil, nil, writeIO(err, markers.File(dir))
				}
				res.Removed = removed
			}
			return res, warnings, nil
		},
	}
}
