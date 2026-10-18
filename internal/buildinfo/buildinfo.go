package buildinfo

import (
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/phansen314/koan/internal/model"
)

// DevVersion is the version of a build from no release tag.
const DevVersion = "0.0.0-dev"

// The commit and its time for a binary that does not name its commit.
const (
	UnknownCommit     = "unknown"
	UnknownCommitTime = "1970-01-01T00:00:00Z"
)

// Info describes the running binary.
type Info struct {
	Version    string
	Commit     string
	CommitTime string // model.TimeLayout
	Dirty      bool   // built with uncommitted changes
}

// Read reports what Go stamped the running binary with.
func Read() Info {
	bi, _ := debug.ReadBuildInfo() // nil when there is none
	return fromBuild(bi)
}

// pseudo matches the end of a pseudo-version, the version Go gives a commit
// that is no tag: the commit's UTC time and the first 12 digits of its hash.
// That is all koan needs of one, so it is matched here, not parsed whole.
var pseudo = regexp.MustCompile(`[-.](\d{14})-([0-9a-f]{12})$`)

// fromBuild builds Info from Go's build information, nil when there is
// none. The version is the main module's when that is a release tag, as
// for go install pkg@v0.1.0, or a build from a clean checkout at the tag:
// not a pseudo-version, nor marked +dirty.
func fromBuild(bi *debug.BuildInfo) Info {
	info := Info{Version: DevVersion, Commit: UnknownCommit, CommitTime: UnknownCommitTime}
	if bi == nil {
		return info
	}
	v := bi.Main.Version
	m := pseudo.FindStringSubmatch(v)
	if strings.HasPrefix(v, "v") && m == nil && !strings.Contains(v, "+") {
		info.Version = v[1:]
	}
	vcs := map[string]string{}
	for _, s := range bi.Settings {
		vcs[s.Key] = s.Value
	}
	// A commit time without its commit, or one that does not parse, would
	// describe a build it cannot identify; both keep the unknown values.
	if rev := vcs["vcs.revision"]; rev != "" {
		info.Commit = rev
		if t, err := time.Parse(time.RFC3339, vcs["vcs.time"]); err == nil {
			info.CommitTime = model.Stamp(t)
		}
		info.Dirty = vcs["vcs.modified"] == "true"
		return info
	}
	// go install pkg@version builds from the module cache, with no VCS
	// information; a pseudo-version still names the commit. A module
	// download has no uncommitted changes.
	if m != nil {
		info.Commit = m[2]
		if t, err := time.Parse("20060102150405", m[1]); err == nil {
			info.CommitTime = model.Stamp(t)
		}
	}
	return info
}
