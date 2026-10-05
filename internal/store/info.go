package store

import (
	"github.com/phansen314/ftask/internal/model"
)

// ConfigState is the config's state, as info reports it.
type ConfigState string

const (
	ConfigMissing    ConfigState = "missing"
	ConfigUnreadable ConfigState = "unreadable"
	ConfigCorrupt    ConfigState = "corrupt"
	ConfigOK         ConfigState = "ok"
)

// MetaState is ftask.json's state, as info reports it.
type MetaState string

const (
	MetaMissing     MetaState = "missing"
	MetaUnreadable  MetaState = "unreadable"
	MetaCorrupt     MetaState = "corrupt"
	MetaUnsupported MetaState = "unsupported-format"
	MetaOK          MetaState = "ok"
)

// Info is info's output (operations.md, info), derived per
// implementation-spec.md, info.
type Info struct {
	Config      ConfigInfo `json:"config"`
	Tree        *TreeInfo  `json:"tree"`
	Initialized bool       `json:"initialized"`
	Usable      bool       `json:"usable"`
	Compatible  *bool      `json:"compatible"`
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
	LastID     *int64    `json:"last_id"`
}

// Inspect reports the config and ftask.json as state, never failing: every
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
	info.Tree = inspectTree(env, root)
	info.Initialized = info.Tree.Metadata != MetaMissing
	info.Usable = info.Tree.Metadata == MetaOK
	if info.Tree.Schema != nil {
		c := *info.Tree.Schema == model.RootSchema
		info.Compatible = &c
	}
	return info
}

func inspectTree(env Env, root string) *TreeInfo {
	t := &TreeInfo{Metadata: MetaMissing}
	// Classified as openRoot does, so info agrees with Root states: only a
	// root that is absent, or leads to something other than a directory
	// (a symlink loop included), is missing. Any other error (EACCES on a
	// parent, say) leaves the root present but unreadable.
	r, err := env.FS.OpenRoot(root)
	if err != nil {
		if rootMissing(err) {
			return t
		}
		t.RootExists, t.Metadata = true, MetaUnreadable
		return t
	}
	defer r.Close()
	t.RootExists = true
	ms := readMeta(r)
	t.Metadata = ms.state
	if ms.file.Versioned {
		found := ms.file.Found
		t.Schema = &found
	}
	if ms.state == MetaOK {
		last := ms.meta.LastID
		t.LastID = &last
	}
	return t
}
