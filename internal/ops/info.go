package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/fsstep"
	"github.com/phansen314/koan/internal/store"
)

// InfoResult is info's result, in its schema's order.
type InfoResult struct {
	Root     string      `json:"root"`
	Tree     string      `json:"tree"`
	Archive  string      `json:"archive"`
	Database string      `json:"database"`
	State    string      `json:"state"`
	Format   *int        `json:"format"`
	NextID   *int64      `json:"next_id"`
	Counts   *InfoCounts `json:"counts"`
	Error    *cli.Error  `json:"error"`
}

// InfoCounts is what info counts in a usable root.
type InfoCounts struct {
	Tasks   int64 `json:"tasks"`
	Folders int64 `json:"folders"`
	Claims  int64 `json:"claims"`
	Pending int64 `json:"pending"`
	Stuck   int64 `json:"stuck"`
}

// StateUsable is info's state for a root other commands can use.
const StateUsable = "usable"

// Info reports the root and its state, without the lock. It never fails
// for the root's state: it reports it.
func Info() *cli.Command {
	return &cli.Command{
		Name: "info",
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			home, e := env.Home()
			if e != nil {
				return nil, nil, e
			}
			root, exists, e := store.FindRoot(home)
			if e != nil {
				return nil, nil, e
			}
			r := InfoResult{Root: root.Path, Tree: root.Tree, Archive: root.Archive, Database: root.Database}
			if !exists {
				return r.failed(store.NotInitialized(root.Path), -1), nil, nil
			}
			has, e := root.HasDatabase()
			if e != nil {
				return nil, nil, e
			}
			if !has {
				e := root.NoDatabase()
				if e.Kind != cli.KindNotInitialized {
					return nil, nil, e
				}
				return r.failed(e, -1), nil, nil
			}
			s, format, e := fsstep.OpenDatabase(root)
			if e != nil {
				if e.Kind == cli.KindUnsupportedFormat || e.Kind == cli.KindCorrupt {
					return r.failed(e, format), nil, nil
				}
				return nil, nil, e
			}
			defer s.Close()
			st, e := s.Stats()
			if e != nil {
				if e.Kind == cli.KindCorrupt {
					return r.failed(e, format), nil, nil
				}
				return nil, nil, e
			}
			r.State, r.Format = StateUsable, &format
			r.Counts = &InfoCounts{Tasks: st.Tasks, Folders: st.Folders, Claims: st.Claims, Pending: st.Pending, Stuck: st.Stuck}
			// No --ids-from follows the last ID.
			if st.NextID <= core.MaxID {
				r.NextID = &st.NextID
			}
			return r, nil, nil
		},
	}
}

// failed reports a root other commands fail on: the state is the error's
// kind, and format is the database's, or -1 when it can't be read.
func (r InfoResult) failed(e *cli.Error, format int) InfoResult {
	r.State, r.Error = e.Kind, e
	if format >= 0 {
		r.Format = &format
	}
	return r
}
