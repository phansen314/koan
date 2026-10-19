package ops

import (
	"runtime"
	"slices"
	"syscall"

	"github.com/phansen314/koan/internal/buildinfo"
	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

type initResult struct {
	Root          string   `json:"root"`
	StatePath     string   `json:"state_path"`
	Created       []string `json:"created"`
	CounterRaised bool     `json:"counter_raised"`
}

// cmdInit creates whatever is missing of the root, its @koan.json, and the
// state file, raises a counter below the tree's highest ID, and changes
// nothing else that is there.
func cmdInit(c *call) (any, *errs.Error) {
	if e := c.checked(); e != nil {
		return nil, e
	}
	env := c.env
	res := initResult{Root: env.RootPath(), StatePath: env.StatePath(), Created: []string{}}
	err := env.FS.Mkdir(env.RootPath(), store.FolderMode)
	switch {
	case err == nil:
		res.Created = append(res.Created, store.PartRoot)
	case !errs.IsErrno(err, syscall.EEXIST):
		return nil, errs.FromOS(env.RootPath(), err)
	}
	// As for every command, the root's state comes before busy: a file that
	// is there and unusable is reported as that, lock held or not.
	if root, err := env.FS.OpenRoot(env.RootPath()); err == nil {
		cond := store.Inspect(env, root, nil)
		root.Close()
		if cond.Status == store.Unusable {
			return nil, cond.Err
		}
	}
	t, e := store.OpenLocked(env)
	if e != nil {
		if len(res.Created) > 0 {
			e.WithChanged(env.RootPath())
		}
		return nil, e
	}
	c.t = t
	if len(res.Created) > 0 {
		t.Touched(".")
	}
	cond := t.Inspect()
	if cond.Status == store.Unusable {
		return nil, cond.Err
	}
	if cond.Meta == nil {
		if e := t.WriteMeta(model.Format, true); e != nil {
			return nil, e
		}
		res.Created = append(res.Created, store.PartMeta)
	}
	// Duplicated IDs don't matter to the counter, but a folder that can't
	// be listed does: an ID in it would be issued again.
	x, e := t.Walk(true)
	if e != nil {
		return nil, e
	}
	if len(x.Unlistable) > 0 {
		p := t.Abs(x.Unlistable[0])
		return nil, errs.New(errs.IO, "%s can't be listed, so the highest ID in the tree is not known", p).WithPaths(p)
	}
	// A folder file that can't be read is doctor's.
	folders, _ := loadFolderFiles(t, x)
	top := highestID(x, folders)
	switch {
	case cond.State == nil:
		t.State = &model.State{Schema: model.FileFormat, LastID: top, Claims: map[string]model.Claim{}}
		if e := t.SaveState(); e != nil {
			return nil, e
		}
		res.Created = append(res.Created, store.PartState)
	case cond.State.LastID < top:
		// A tree cloned, copied, or restored beside a state file of this
		// machine's: raising the counter never harms.
		t.State = cond.State
		t.State.LastID = top
		if e := t.SaveState(); e != nil {
			return nil, e
		}
		res.CounterRaised = true
	}
	return res, nil
}

type versionResult struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	CommitTime string `json:"commit_time"`
	Dirty      bool   `json:"dirty"`
	Go         string `json:"go"`
	Platform   string `json:"platform"`
	Format     int64  `json:"format"`
}

func cmdVersion(c *call) (any, *errs.Error) {
	if e := c.in.done(); e != nil {
		return nil, e
	}
	b := buildinfo.Read()
	return versionResult{
		Version: b.Version, Commit: b.Commit, CommitTime: b.CommitTime, Dirty: b.Dirty,
		Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Format: model.Format,
	}, nil
}

type infoResult struct {
	Root      string       `json:"root"`
	StatePath string       `json:"state_path"`
	State     store.Status `json:"state"`
	Missing   []string     `json:"missing"`
	Format    *int64       `json:"format"`
	LastID    *int64       `json:"last_id"`
	Claims    *int         `json:"claims"`
	Error     *errs.Error  `json:"error"`
}

// cmdInfo reports the root's state without failing on it.
func cmdInfo(c *call) (any, *errs.Error) {
	if e := c.checked(); e != nil {
		return nil, e
	}
	root, err := c.env.FS.OpenRoot(c.env.RootPath())
	if err == nil {
		defer root.Close()
	}
	cond := store.Inspect(c.env, root, err)
	res := infoResult{Root: c.env.RootPath(), StatePath: c.env.StatePath(), State: cond.Status, Missing: cond.Missing}
	if cond.Meta != nil {
		res.Format = &cond.Meta.Schema
	}
	if cond.State != nil {
		n := len(cond.State.Claims)
		res.LastID, res.Claims = &cond.State.LastID, &n
	}
	if cond.Status == store.Unusable {
		res.Error = cond.Err
	}
	return res, nil
}

type doctorResult struct {
	Healthy  bool           `json:"healthy"`
	Findings []errs.Finding `json:"findings"`
}

// cmdDoctor reports every finding. It holds the write lock so that it sees
// the tree at rest, and changes nothing.
func cmdDoctor(c *call) (any, *errs.Error) {
	if e := c.beginNeed(store.NeedInitialized, true); e != nil {
		return nil, e
	}
	if e := c.convertible(); e != nil {
		return nil, e
	}
	d, e := diagnose(c)
	if e != nil {
		return nil, e
	}
	return doctorResult{Healthy: len(d.findings) == 0, Findings: d.findings}, nil
}

type repairResult struct {
	Repaired []errs.Finding `json:"repaired"`
	Findings []errs.Finding `json:"findings"`
}

// cmdRepair fixes the repairable findings of the kinds asked for, then
// reports what is left. Without kinds it fixes every kind, but not the fixes
// that need their kind asked for by name.
func cmdRepair(c *call) (any, *errs.Error) {
	kinds, named := c.in.top().oneOf("kinds", errs.RepairableKinds)
	if !named {
		kinds = errs.RepairableKinds
	} else if len(kinds) == 0 {
		c.in.bad("/kinds", "must hold at least one finding kind")
	}
	if e := c.begin(true); e != nil {
		return nil, e
	}
	d, e := diagnose(c)
	if e != nil {
		return nil, e
	}
	res := repairResult{Repaired: []errs.Finding{}}
	for i, f := range d.findings {
		if d.fixes[i] == nil || !slices.Contains(kinds, f.Kind) || (d.byName[i] && !named) {
			continue
		}
		fixed, e := d.fixes[i]()
		if e != nil {
			return nil, e
		}
		if fixed {
			res.Repaired = append(res.Repaired, f)
		}
	}
	after, e := diagnose(c)
	if e != nil {
		return nil, e
	}
	res.Findings = after.findings
	return res, nil
}

// conversions holds, for each earlier format, the function that converts a
// task file from it to the next. There is no earlier format yet.
var conversions = map[int64]func([]byte) ([]byte, error){}

// treeConversions holds the tree formats this binary converts from. Format
// 1 has none: format 2 came before the first release, without one.
var treeConversions = map[int64]bool{}

// convertible refuses a tree in a format this binary has no conversion
// from: doctor and migrate would misread it.
func (c *call) convertible() *errs.Error {
	if s := c.t.Meta.Schema; s < model.Format && !treeConversions[s] {
		return errs.NewConflict(errs.RuleNoConversion,
			"the tree is in format %d, which this koan can't convert to %d: move it aside and start a new one with `koan init`", s, model.Format)
	}
	return nil
}

type migrateResult struct {
	From        int64    `json:"from"`
	To          int64    `json:"to"`
	Converted   int      `json:"converted"`
	Unconverted []string `json:"unconverted"`
}

// cmdMigrate converts every task file in an earlier format to this binary's,
// each by its own schema, and writes @koan.json last.
func cmdMigrate(c *call) (any, *errs.Error) {
	dry := c.in.top().boolean("dry_run", false)
	if e := c.beginNeed(store.NeedInitialized, true); e != nil {
		return nil, e
	}
	if e := c.convertible(); e != nil {
		return nil, e
	}
	x, e := c.t.Walk(true)
	if e != nil {
		return nil, e
	}
	res := migrateResult{From: c.t.Meta.Schema, To: model.Format, Unconverted: []string{}}
	for _, l := range inTreeOrder(x) {
		old := c.t.LoadAt(l.f, l.id).OldFormat
		if old == 0 {
			continue
		}
		rel := model.TaskRel(l.f, l.id)
		data, err := convert(c.t, rel, old)
		if err != nil {
			res.Unconverted = append(res.Unconverted, c.t.Abs(rel))
			continue
		}
		if !dry {
			if e := c.t.PutRaw(rel, data); e != nil {
				return nil, e
			}
		}
		res.Converted++
	}
	if !dry && res.From < res.To {
		if e := c.t.WriteMeta(model.Format, false); e != nil {
			return nil, e
		}
	}
	return res, nil
}

// convert carries one file from format `from` up to this binary's.
func convert(t *store.Tree, rel string, from int64) ([]byte, error) {
	data, err := t.ReadRaw(rel)
	if err != nil {
		return nil, err
	}
	for v := from; v < model.FileFormat; v++ {
		step, ok := conversions[v]
		if !ok {
			return nil, errs.New(errs.Internal, "no conversion from format %d", v)
		}
		if data, err = step(data); err != nil {
			return nil, err
		}
	}
	if _, problems, err := model.DecodeTaskFile(data); err != nil || len(problems) > 0 {
		return nil, errs.New(errs.Corrupt, "%s is invalid after conversion", rel)
	}
	return data, nil
}

// copiesOf lists every folder holding a task file of id.
func copiesOf(x *store.Index, id model.ID) []model.Folder {
	if cs := x.Copies[id]; len(cs) > 0 {
		return cs
	}
	return []model.Folder{x.Tasks[id]}
}
