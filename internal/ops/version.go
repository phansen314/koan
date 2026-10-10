package ops

import (
	"github.com/phansen314/koan/internal/buildinfo"
	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/migrations"
	"github.com/phansen314/koan/internal/model"
)

// VersionInput is version's input: none.
type VersionInput struct{}

func decodeVersion(f *model.Fields, p *model.Problems) any { return VersionInput{} }

// VersionOutput is version's result (version-output).
type VersionOutput struct {
	Version            string          `json:"version"`
	Commit             string          `json:"commit"`
	CommitTime         model.Timestamp `json:"commit_time"`
	UncommittedChanges bool            `json:"uncommitted_changes"`
	Go                 string          `json:"go"`
	Platform           string          `json:"platform"`
	Schemas            VersionSchemas  `json:"schemas"`
	Migration          int64           `json:"migration"`
}

// VersionSchemas are the one task file and koan.json format versions this
// binary reads and writes.
type VersionSchemas struct {
	Task  int64 `json:"task"`
	Root  int64 `json:"root"`
	State int64 `json:"state"`
}

// buildInfo is the binary's build information; tests replace it.
var buildInfo = buildinfo.Read

// runVersion describes the binary only: no root, no lock, no warnings.
func runVersion(Env, VersionInput, *errs.Collector) (any, *errs.Error) {
	b := buildInfo()
	return VersionOutput{
		Version:            b.Version,
		Commit:             b.Commit,
		CommitTime:         model.Timestamp(b.CommitTime),
		UncommittedChanges: b.UncommittedChanges,
		Go:                 b.Go,
		Platform:           b.Platform,
		Schemas:            VersionSchemas{Task: model.TaskSchema, Root: model.RootSchema, State: model.StateSchema},
		Migration:          migrations.Latest(),
	}, nil
}
