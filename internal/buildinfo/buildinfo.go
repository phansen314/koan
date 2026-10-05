package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Version is the release version, set at release build time:
//
//	go build -ldflags "-X github.com/phansen314/ftask/internal/buildinfo.Version=1.4.0"
//
// A build without it takes the version of the tag it was built from, if any
// (see fromBuild); otherwise it keeps the default, which is still semver.
var Version = DevVersion

// DevVersion is Version's default, for a build from no release tag.
const DevVersion = "0.0.0-dev"

// Development-build values for a binary built without VCS information
// (implementation-spec.md, Toolchain).
const (
	UnknownCommit     = "unknown"
	UnknownCommitTime = "1970-01-01T00:00:00Z"
)

// timeLayout is the design spec's timestamp form: UTC, whole seconds.
const timeLayout = "2006-01-02T15:04:05Z"

// Info describes the running binary.
type Info struct {
	Version            string
	Commit             string
	CommitTime         string // UTC, whole seconds, "Z" suffix
	UncommittedChanges bool
	Go                 string
	Platform           string // GOOS/GOARCH
}

// Read reports the running binary's build information.
func Read() Info {
	bi, _ := debug.ReadBuildInfo() // nil when there is none
	return fromBuild(bi, Version, runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH)
}

// fromBuild builds Info from Go's embedded build information, nil when there
// is none. goVersion and platform are the running toolchain's, used when bi
// lacks them. A version left at DevVersion is replaced by the main module's
// version when that is a release tag: go install pkg@v0.1.0 sets no -ldflags,
// and Go stamps a build from a clean checkout at a tag with the tag too.
func fromBuild(bi *debug.BuildInfo, version, goVersion, platform string) Info {
	if bi != nil && version == DevVersion && isRelease(bi.Main.Version) {
		version = strings.TrimPrefix(bi.Main.Version, "v")
	}
	info := Info{
		Version:    version,
		Commit:     UnknownCommit,
		CommitTime: UnknownCommitTime,
		Go:         goVersion,
		Platform:   platform,
	}
	if bi == nil {
		return info
	}
	if bi.GoVersion != "" {
		info.Go = bi.GoVersion
	}
	settings := map[string]string{}
	for _, s := range bi.Settings {
		settings[s.Key] = s.Value
	}
	// A commit time without its commit, or one that does not parse, would
	// describe a build it cannot identify; both keep the development values.
	if rev := settings["vcs.revision"]; rev != "" {
		info.Commit = rev
		if t, err := time.Parse(time.RFC3339, settings["vcs.time"]); err == nil {
			info.CommitTime = t.UTC().Truncate(time.Second).Format(timeLayout)
		}
		info.UncommittedChanges = settings["vcs.modified"] == "true"
		return info
	}
	// go install pkg@version builds from the module cache, with no VCS
	// information; the module's pseudo-version still names the commit, by its
	// 12-character prefix, and the commit's UTC time. A module download has no
	// uncommitted changes. A tagged version names no commit.
	if v := bi.Main.Version; module.IsPseudoVersion(v) {
		rev, err := module.PseudoVersionRev(v)
		if err != nil {
			return info
		}
		info.Commit = rev
		if t, err := module.PseudoVersionTime(v); err == nil {
			info.CommitTime = t.UTC().Format(timeLayout)
		}
	}
	return info
}

// isRelease reports whether v, a main module version, names a release tag: a
// valid semantic version that is neither a pseudo-version nor marked with
// build metadata (Go appends +dirty for uncommitted changes).
func isRelease(v string) bool {
	return semver.IsValid(v) && !module.IsPseudoVersion(v) && semver.Build(v) == ""
}
