package ops

import (
	"fmt"
	"strings"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/migrations"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// MigrateInput is migrate's input.
type MigrateInput struct {
	DryRun bool
}

func decodeMigrate(f *model.Fields, p *model.Problems) any {
	return MigrateInput{DryRun: optionalBool(f, p, "dry_run", false)}
}

// MigrateOutput is migrate's result (migrate-output).
type MigrateOutput struct {
	DryRun            bool          `json:"dry_run"`
	From              int64         `json:"from"`
	To                int64         `json:"to"`
	Applied           []AppliedStep `json:"applied"`
	Changed           bool          `json:"changed"`
	MetadataConverted bool          `json:"metadata_converted"`
	StateWritten      bool          `json:"state_written"`
	TasksConverted    int           `json:"tasks_converted"`
	Unconverted       []Unconverted `json:"unconverted"`
	UnconvertedCount  int           `json:"unconverted_count"`
}

// AppliedStep is a step a run applies: one after from, up to to.
type AppliedStep struct {
	Step int64  `json:"step"`
	Name string `json:"name"`
}

// Unconverted is a task file in an older format that no step could convert.
type Unconverted struct {
	Path   string   `json:"path"`
	ID     model.ID `json:"id"`
	Schema int64    `json:"schema"`
	Detail string   `json:"detail"`
}

// MigratePartial is what migrate wrote before an error (migrate-partial).
type MigratePartial struct {
	StateWritten   bool `json:"state_written"`
	TasksConverted int  `json:"tasks_converted"`
}

// unconvertedCap is how many unconverted files the output lists.
const unconvertedCap = 20

// converted is a task file's new bytes, ready for the second pass.
type converted struct {
	rel  string
	data []byte
}

// runMigrate converts the tree in two passes (implementation-spec.md,
// migrate): everything is read and converted in memory, then (unless dry_run)
// each converted task file is written in tree order and koan.json last.
func runMigrate(env Env, in MigrateInput, w *errs.Collector) (any, *errs.Error) {
	var out MigrateOutput
	e := store.MigrateFormats(env.Env, w, func(tx *store.Tx) *errs.Error {
		latest := migrations.Latest()
		from := tx.Meta().Migration
		out = MigrateOutput{DryRun: in.DryRun, From: from, To: latest, Applied: []AppliedStep{}, Unconverted: []Unconverted{}}
		for _, s := range migrations.Steps() {
			if s.Number > from && s.Number <= latest {
				out.Applied = append(out.Applied, AppliedStep{Step: s.Number, Name: s.Name})
			}
		}

		// Pass 1: read and convert, writing nothing.
		meta, writeMeta, e := convertMeta(tx)
		if e != nil {
			return e
		}
		out.MetadataConverted = writeMeta && tx.Meta().Schema != meta.Schema
		// An older koan.json holds the counter the state file holds now.
		// An unusable state file stops the run here, whatever dry_run says.
		var carry int64
		if old, holds := tx.OldLastID(); holds {
			st, stateErr := tx.StateState()
			if st != store.StateOK && st != store.StateMissing && st != store.StateOtherRoot {
				return stateErr
			}
			carry = old
			out.StateWritten = st != store.StateOK || tx.LastID() < old
			if st == store.StateOK {
				carry = max(old, tx.LastID())
			}
		}
		x := tx.Index()
		if e := tx.WarnUnreadable(x); e != nil {
			return e
		}
		var files []converted
		for _, l := range x.Tasks {
			ld := tx.Load(l)
			switch ld.State {
			case store.Unreadable:
				if e := tx.Relevant(ld); e != nil {
					return e
				}
			case store.Corrupt:
				// Only a file whose format can't be told is a warning; one
				// in this binary's format is checked for its schema alone.
				if !ld.Versioned {
					if e := tx.Relevant(ld); e != nil {
						return e
					}
				}
			case store.Unsupported:
				if !ld.Older {
					if e := tx.Relevant(ld); e != nil {
						return e
					}
					continue
				}
				data, detail := convertTask(ld)
				if detail != "" {
					out.UnconvertedCount++
					if len(out.Unconverted) < unconvertedCap {
						out.Unconverted = append(out.Unconverted, Unconverted{Path: tx.Path(l.Rel()), ID: l.ID, Schema: ld.Found, Detail: detail})
					}
					continue
				}
				files = append(files, converted{l.Rel(), data})
			}
		}
		out.TasksConverted = len(files)
		out.Changed = len(files) > 0 || writeMeta || out.StateWritten
		if in.DryRun {
			return nil
		}

		// Pass 2: the state file first, so the counter is in its new place
		// before the file that held it is replaced; then task files in tree
		// order, and koan.json last.
		if out.StateWritten {
			if e := tx.CreateState(carry); e != nil {
				return e
			}
		}
		for i, f := range files {
			if e := tx.Replace(f.rel, f.data); e != nil {
				return withMigratePartial(e, out.StateWritten, i, false)
			}
		}
		if writeMeta {
			if e := tx.SetMeta(meta); e != nil {
				return withMigratePartial(e, out.StateWritten, len(files), true)
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// withMigratePartial attaches the partial result to an error: after the
// state file or at least one task file was written, or while koan.json is
// (which is the case whenever meta is set).
func withMigratePartial(e *errs.Error, state bool, written int, meta bool) *errs.Error {
	if !state && written == 0 && !meta {
		return e
	}
	return e.WithPartial(MigratePartial{StateWritten: state, TasksConverted: written})
}

// convertMeta is koan.json as it will be: converted if it is in an older
// format, with the latest step recorded. write is whether that differs from
// what is there.
func convertMeta(tx *store.Tx) (meta model.RootFile, write bool, _ *errs.Error) {
	meta = tx.Meta()
	if obj, schema, ok := tx.OldMeta(); ok {
		path := tx.Path(store.MetaName)
		conv, err := migrations.Convert(migrations.Root, schema, obj)
		if err != nil {
			return meta, false, errs.CorruptBy(path, errs.CorruptCause{Reason: errs.CorruptInvalid, Detail: err.Error()})
		}
		rf, res := model.DecodeRootFile(conv, nil)
		if res.Status != model.FileOK {
			return meta, false, errs.CorruptBy(path, errs.CorruptCause{Reason: errs.CorruptInvalid, Problems: res.Problems})
		}
		meta, write = rf, true
	}
	if latest := migrations.Latest(); meta.Migration != latest {
		meta.Migration, write = latest, true
	}
	return meta, write, nil
}

// convertTask converts a task file in an older format: its new bytes, or
// what stops it (detail), for unconverted. It is checked against its own
// format's rules first, and its result against this binary's.
func convertTask(ld *store.Loaded) (data []byte, detail string) {
	if ps := migrations.Check(migrations.Task, ld.Found, ld.Tree, ld.Repeated); len(ps) > 0 {
		return nil, fmt.Sprintf("breaks the rules of format %d: %s", ld.Found, problemList(ps))
	}
	conv, err := migrations.Convert(migrations.Task, ld.Found, ld.Tree)
	if err != nil {
		return nil, err.Error()
	}
	t, res := model.DecodeTaskFile(conv, nil, ld.Loc.ID)
	switch res.Status {
	case model.FileOK:
	case model.FileUnsupported:
		return nil, fmt.Sprintf("converted to format %d, which this binary does not read", res.Found)
	default:
		return nil, "the converted file breaks the current rules: " + problemList(res.Problems)
	}
	data, err = t.Encode()
	if err != nil {
		return nil, "encoding the converted file: " + err.Error()
	}
	return data, ""
}

// problemList is ps in one line.
func problemList(ps []errs.Problem) string {
	var parts []string
	for _, p := range ps {
		parts = append(parts, fmt.Sprintf("at %q: %s", p.Field, p.Reason))
	}
	return strings.Join(parts, "; ")
}
