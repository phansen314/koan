package store

import (
	"github.com/phansen314/koan/internal/model"
)

// ConfigState is the config's state, as info reports it.
type ConfigState string

const (
	ConfigMissing    ConfigState = "missing"
	ConfigUnreadable ConfigState = "unreadable"
	ConfigCorrupt    ConfigState = "corrupt"
	ConfigOK         ConfigState = "ok"
)

// MetaState is koan.json's state, as info reports it.
type MetaState string

const (
	MetaMissing     MetaState = "missing"
	MetaUnreadable  MetaState = "unreadable"
	MetaCorrupt     MetaState = "corrupt"
	MetaOldFormat   MetaState = "old-format"
	MetaUnsupported MetaState = "unsupported-format"
	MetaOK          MetaState = "ok"
)

// Info is info's output (operations.md, info), derived per
// implementation-spec.md, info.
type Info struct {
	Config      ConfigInfo `json:"config"`
	Tree        *TreeInfo  `json:"tree"`
	State       *StateInfo `json:"state"`
	Initialized bool       `json:"initialized"`
	Usable      bool       `json:"usable"`
	Compatible  *bool      `json:"compatible"`
	// MigrationPending is nil when Tree.Migration is.
	MigrationPending *bool `json:"migration_pending"`
}

type ConfigInfo struct {
	Path  *string     `json:"path"`
	State ConfigState `json:"state"`
	Root  *string     `json:"root"`
}

type TreeInfo struct {
	RootExists bool      `json:"root_exists"`
	Metadata   MetaState `json:"metadata"`
	Schema     *int64    `json:"schema"`
	Migration  *int64    `json:"migration"`
}

// StateInfo is the state file's state for the configured root.
type StateInfo struct {
	Path   string     `json:"path"`
	State  StateState `json:"state"`
	LastID *int64     `json:"last_id"`
}

// Inspect reports the config and koan.json as state, never failing: every
// problem with them is part of the result. It never walks the tree.
func Inspect(env Env) Info {
	var info Info
	cs := readConfig(env)
	info.Config.State = cs.state
	if p := env.ConfigPath(); p != "" {
		info.Config.Path = &p
	}
	switch cs.state {
	case ConfigMissing:
		return info // not initialized
	case ConfigUnreadable, ConfigCorrupt:
		info.Initialized = true // present, but no root can be read from it
		return info
	}
	root, ok := cs.cfg.raw.Expand(env.Home)
	if !ok {
		return info // a "~/" root with no home counts as missing
	}
	info.Config.Root = &root
	tree, pending := inspectTree(env, root)
	info.Tree = tree
	sr := readState(env, root)
	info.State = &StateInfo{Path: env.StatePath(), State: sr.state}
	if sr.state == StateOK {
		last := sr.file.LastID
		info.State.LastID = &last
	}
	if tree.Migration != nil {
		info.MigrationPending = &pending
	}
	// The state file counts only once koan.json is ok and current.
	noCounter := sr.state == StateMissing || sr.state == StateOtherRoot
	info.Initialized = tree.Metadata != MetaMissing && !(tree.Metadata == MetaOK && !pending && noCounter)
	info.Usable = tree.Metadata == MetaOK && sr.state == StateOK && tree.RootExists && info.MigrationPending != nil && !*info.MigrationPending
	if tree.Schema != nil {
		c := *tree.Schema == model.RootSchema
		info.Compatible = &c
	}
	return info
}

// inspectTree reports the root's tree; pending is whether koan.json is valid
// but behind (meaningful when the result's Migration is set).
func inspectTree(env Env, root string) (t *TreeInfo, pending bool) {
	t = &TreeInfo{Metadata: MetaMissing}
	// Classified as openRoot does, so info agrees with Root states: only a
	// root that is absent, or leads to something other than a directory
	// (a symlink loop included), is missing. Any other error (EACCES on a
	// parent, say) leaves the root present but unreadable.
	r, err := env.FS.OpenRoot(root)
	if err != nil {
		if rootMissing(err) {
			return t, false
		}
		t.RootExists, t.Metadata = true, MetaUnreadable
		return t, false
	}
	defer r.Close()
	t.RootExists = true
	ms := readMeta(r)
	t.Metadata = ms.state
	if ms.file.Versioned {
		found := ms.file.Found
		t.Schema = &found
	}
	if ms.state == MetaOK || ms.state == MetaOldFormat || ms.pastLatest {
		mig := ms.meta.Migration
		t.Migration = &mig
	}
	return t, ms.pending()
}
