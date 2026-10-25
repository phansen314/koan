package ops

import (
	"encoding/json"
	"strconv"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/store"
)

// InitResult is init's result, in its schema's order.
type InitResult struct {
	Root     string   `json:"root"`
	Tree     string   `json:"tree"`
	Archive  string   `json:"archive"`
	Database string   `json:"database"`
	Made     []string `json:"made"`
	FirstID  int64    `json:"first_id"`
}

// Init makes the root, its tree and archive, and the database. Run again,
// it changes nothing that is there and reports what it found.
func Init() *cli.Command {
	return &cli.Command{
		Name:    "init",
		Options: []cli.Option{{Field: "ids_from", Kind: cli.Integer, Nullable: true}},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			var idsFrom *int64
			if v, ok := in["ids_from"]; ok && v != nil {
				n, ok := v.(json.Number)
				id, err := strconv.ParseInt(string(n), 10, 64)
				if !ok || err != nil || !core.ValidID(id) {
					return nil, nil, cli.InvalidInput("ids_from must be an ID, an integer from 1 to "+strconv.FormatInt(core.MaxID, 10),
						cli.Problem{Field: "/ids_from", Reason: "not an ID"})
				}
				idsFrom = &id
			}
			home, e := env.Home()
			if e != nil {
				return nil, nil, e
			}
			root, created, first, e := store.Init(home, idsFrom)
			if e != nil {
				return nil, nil, e
			}
			if created == nil {
				created = []string{}
			}
			return InitResult{Root: root.Path, Tree: root.Tree, Archive: root.Archive, Database: root.Database,
				Made: created, FirstID: first}, nil, nil
		},
	}
}
