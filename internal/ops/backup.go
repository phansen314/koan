package ops

import (
	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/doctor"
)

// BackupResult is backup's result, in its schema's order.
type BackupResult struct {
	Dir      string    `json:"dir"`
	Files    int64     `json:"files"`
	Bytes    int64     `json:"bytes"`
	Recopied bool      `json:"recopied"`
	Clean    bool      `json:"clean"`
	Findings []Finding `json:"findings"`
}

// Backup copies the database, the tree, the archive and log/ into an
// empty or new directory outside the root, and checks the copy with
// doctor against the live root. It gives no warnings: its findings carry the
// pending and stuck steps.
func Backup() *cli.Command {
	return &cli.Command{
		Name:    "backup",
		Subject: &cli.Option{Field: "dir", Kind: cli.Path, Holds: "the directory", Required: true},
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := input{in: in, env: env}
			dir := r.absPath("dir", true)
			if e := r.err(); e != nil {
				return nil, nil, e
			}
			s, e := openStore(env)
			if e != nil {
				return nil, nil, e
			}
			defer s.Close()
			if _, _, e := notUnder(s.Root, dir, "and the copy would copy itself"); e != nil {
				return nil, nil, e
			}
			c, e := doctor.Backup(s, dir)
			if e != nil {
				return nil, nil, e
			}
			return BackupResult{Dir: dir, Files: c.Files, Bytes: c.Bytes, Recopied: c.Recopied, Clean: c.Clean, Findings: findings(c.Findings)}, nil, nil
		},
	}
}
