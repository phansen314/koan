package ops

import (
	"runtime"
	"runtime/debug"

	"github.com/phansen314/koan/internal/cli"
)

// DatabaseFormat is the one database format this binary reads.
const DatabaseFormat = 1

// VersionResult is version's result, in its schema's order.
type VersionResult struct {
	Version string  `json:"version"`
	Commit  *string `json:"commit"`
	Go      string  `json:"go"`
	Format  int     `json:"format"`
}

// Version reports the binary.
func Version() *cli.Command {
	return &cli.Command{
		Name: "version",
		Run: func(env *cli.Env, in cli.Input) (any, []cli.Warning, *cli.Error) {
			r := VersionResult{Version: "(devel)", Go: runtime.Version(), Format: DatabaseFormat}
			if bi, ok := debug.ReadBuildInfo(); ok {
				if bi.Main.Version != "" {
					r.Version = bi.Main.Version
				}
				for _, s := range bi.Settings {
					if s.Key == "vcs.revision" {
						commit := s.Value
						r.Commit = &commit
					}
				}
			}
			return r, nil, nil
		},
	}
}
