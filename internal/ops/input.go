package ops

import (
	"maps"
	"slices"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
)

// decoder is an operation's input adapter: it turns the input object into the
// operation's input type, recording every problem in p. The input type is
// meaningful only when p ends up OK.
type decoder func(f *model.Fields, p *model.Problems) any

// decoders holds each operation's input adapter, by operation name, and
// pick's, which is a command's: it runs no operation of its own.
var decoders = map[string]decoder{
	"version":       decodeVersion,
	"info":          decodeInfo,
	"doctor":        decodeDoctor,
	"repair":        decodeRepair,
	"init":          decodeInit,
	"create-folder": decodeCreateFolder,
	"create":        decodeCreate,
	"create-batch":  decodeCreateBatch,
	"show":          decodeShow,
	"done":          decodeDone,
	"reopen":        decodeReopen,
	"block":         decodeBlock,
	"unblock":       decodeUnblock,
	"update":        decodeUpdate,
	"delete":        decodeDelete,
	"move":          decodeMove,
	"delete-folder": decodeDeleteFolder,
	"move-folder":   decodeMoveFolder,
	"frontier":      decodeFrontier,
	"list":          decodeList,
	"pick":          decodePick,
}

// Operations returns the name of every operation, sorted.
func Operations() []string {
	return slices.Sorted(maps.Keys(runners))
}

// Decode checks the input of operation op, already read by jsonio, and returns
// its typed form and every problem found. The input is meaningful only when
// there are none. The caller reports problems as one invalid-input error,
// after adding any from checks that need the filesystem (init's; see
// implementation-spec.md, Where it happens). Repeated keys are not checked
// here: jsonio returns them beside the tree, and the caller must reject the
// input (invalid-input, field the repeated key's pointer) before calling Decode.
func Decode(op string, input *jsonio.Object) (any, *model.Problems, *errs.Error) {
	d, ok := decoders[op]
	if !ok {
		return nil, nil, errs.Internal("no operation " + op)
	}
	var p model.Problems
	in := decode(d, input, &p)
	return in, &p, nil
}

func decode(d decoder, input *jsonio.Object, p *model.Problems) any {
	f, _ := p.Object(input, "")
	in := d(f, p)
	f.Done()
	return in
}

// optionalBool returns the boolean field key, or def when it is absent.
func optionalBool(f *model.Fields, p *model.Problems, key string, def bool) bool {
	if v, ok := f.Optional(key); ok {
		b, _ := p.Bool(v, f.Ptr(key))
		return b
	}
	return def
}

// optionalFolder returns the folder path field key, or the root when absent.
func optionalFolder(f *model.Fields, p *model.Problems, key string) model.FolderPath {
	if v, ok := f.Optional(key); ok {
		fp, _ := p.FolderPath(v, f.Ptr(key))
		return fp
	}
	return model.RootFolder
}

// requiredID returns the task ID field key.
func requiredID(f *model.Fields, p *model.Problems, key string) model.ID {
	if v, ok := f.Required(key); ok {
		id, _ := p.ID(v, f.Ptr(key))
		return id
	}
	return 0
}

// blockerList returns the field key as a non-empty set of task IDs — block's
// and unblock's blockers — and whether it is valid.
func blockerList(f *model.Fields, p *model.Problems, key string) ([]model.ID, bool) {
	v, ok := f.Required(key)
	if !ok {
		return nil, false
	}
	return nonEmptySet(p, v, f.Ptr(key), "ID", (*model.Problems).IDs)
}

// nonEmptySet checks v, at ptr, as a set by check, which reports a non-array
// and bad or repeated items; an empty array is refused first, its reason
// naming an item as noun. It is the schemas' minItems: 1 on a set.
func nonEmptySet[T any](p *model.Problems, v any, ptr, noun string, check func(*model.Problems, any, string) ([]T, bool)) ([]T, bool) {
	if a, isArr := v.([]any); isArr && len(a) == 0 {
		p.Add(ptr, "must list at least one "+noun)
		return nil, false
	}
	return check(p, v, ptr)
}

// IDInput is the input of show, done, and reopen: one task.
type IDInput struct {
	ID model.ID
}

func decodeID(f *model.Fields, p *model.Problems) any {
	return IDInput{ID: requiredID(f, p, "id")}
}
