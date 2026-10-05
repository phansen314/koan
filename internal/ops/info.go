package ops

import (
	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// InfoInput is info's input: none.
type InfoInput struct{}

func decodeInfo(f *model.Fields, p *model.Problems) any { return InfoInput{} }

// runInfo reports the config and koan.json as state (info-output): no lock,
// no Root states checks, no warnings, and no error for any problem with them.
func runInfo(env Env, _ InfoInput, _ *errs.Collector) (any, *errs.Error) {
	return store.Inspect(env.Env), nil
}
