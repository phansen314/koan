package ops

import (
	"strings"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

// InitInput is init's input. Root is absolute, cleaned, and has no ".."
// segment. Whether anything at Root leads to a directory is checked by the
// operation, which must look at the filesystem.
type InitInput struct {
	Root          string
	ReplaceConfig bool
}

func decodeInit(f *model.Fields, p *model.Problems) any {
	var in InitInput
	if v, ok := f.Required("root"); ok {
		if s, ok := p.String(v, "/root"); ok {
			in.Root, _ = cleanRoot(s, p)
		}
	}
	in.ReplaceConfig = optionalBool(f, p, "replace_config", false)
	return in
}

// InitOutput is init's result (init-output).
type InitOutput struct {
	Root   string           `json:"root"`
	Action store.InitAction `json:"action"`
	LastID int64            `json:"last_id"`
}

// InitPartial is init's partial result (init-partial): what it created
// before failing, which stays; rerunning init with the same input completes
// it.
type InitPartial struct {
	RootCreated     bool `json:"root_created"`
	MetadataCreated bool `json:"metadata_created"`
}

// runInit runs store.Init, which follows init's own precedence order. An
// error after the root directory or ftask.json was created carries the
// partial result.
func runInit(env Env, in InitInput, _ *errs.Collector) (any, *errs.Error) {
	res, e := store.Init(env.Env, in.Root, in.ReplaceConfig)
	if e != nil {
		if res.RootCreated || res.MetaCreated {
			e = e.WithPartial(InitPartial{RootCreated: res.RootCreated, MetadataCreated: res.MetaCreated})
		}
		return nil, e
	}
	return InitOutput{Root: in.Root, Action: res.Action, LastID: res.LastID}, nil
}

// cleanRoot cleans root lexically — no trailing "/", no empty or "."
// segments — then checks that it is absolute and has no ".." segment, which
// cleaning never removes, and no NUL (see design-spec.md, Root path).
func cleanRoot(root string, p *model.Problems) (string, bool) {
	clean := model.CleanPath(root)
	switch {
	case strings.ContainsRune(clean, 0):
		p.AddAdditional("/root", "must not contain a NUL character")
		return "", false
	case !strings.HasPrefix(clean, "/"):
		p.AddAdditional("/root", "must be an absolute path (resolving ~ or a relative path is the caller's job)")
		return "", false
	case model.HasDotDot(clean):
		p.AddAdditional("/root", `must not contain ".." segments`)
		return "", false
	}
	return clean, true
}
