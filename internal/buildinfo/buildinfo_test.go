package buildinfo

import (
	"regexp"
	"runtime/debug"
	"testing"
)

func build(settings ...string) *debug.BuildInfo {
	bi := &debug.BuildInfo{GoVersion: "go1.25.1"}
	for i := 0; i < len(settings); i += 2 {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return bi
}

// installed is build info for a binary built by go install pkg@version: no VCS
// settings, but the main module's version.
func installed(version string) *debug.BuildInfo {
	bi := build()
	bi.Main.Version = version
	return bi
}

func TestFromBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		bi   *debug.BuildInfo
		want Info
	}{
		{"no build info", nil, Info{"1.4.0", UnknownCommit, UnknownCommitTime, false, "go-running", "linux/amd64"}},
		{"no vcs", build(), Info{"1.4.0", UnknownCommit, UnknownCommitTime, false, "go1.25.1", "linux/amd64"}},
		{
			"clean checkout",
			build("vcs.revision", "abc123", "vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "false"),
			Info{"1.4.0", "abc123", "2026-09-27T12:34:56Z", false, "go1.25.1", "linux/amd64"},
		},
		{
			"modified, offset time with fraction",
			build("vcs.revision", "abc123", "vcs.time", "2026-09-27T14:34:56.9+02:00", "vcs.modified", "true"),
			Info{"1.4.0", "abc123", "2026-09-27T12:34:56Z", true, "go1.25.1", "linux/amd64"},
		},
		{
			"unparseable time",
			build("vcs.revision", "abc123", "vcs.time", "yesterday"),
			Info{"1.4.0", "abc123", UnknownCommitTime, false, "go1.25.1", "linux/amd64"},
		},
		{
			"time without revision",
			build("vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "true"),
			Info{"1.4.0", UnknownCommit, UnknownCommitTime, false, "go1.25.1", "linux/amd64"},
		},
		{
			"go install, pseudo-version with no tag",
			installed("v0.0.0-20260929021723-4df90bd5d3e3"),
			Info{"1.4.0", "4df90bd5d3e3", "2026-09-29T02:17:23Z", false, "go1.25.1", "linux/amd64"},
		},
		{
			"go install, pseudo-version after a tag",
			installed("v1.4.1-0.20260929021723-4df90bd5d3e3"),
			Info{"1.4.0", "4df90bd5d3e3", "2026-09-29T02:17:23Z", false, "go1.25.1", "linux/amd64"},
		},
		{
			"go install, pseudo-version after a prerelease",
			installed("v1.5.0-rc.1.0.20260929021723-4df90bd5d3e3"),
			Info{"1.4.0", "4df90bd5d3e3", "2026-09-29T02:17:23Z", false, "go1.25.1", "linux/amd64"},
		},
		{
			"go install, tagged version",
			installed("v1.4.0"),
			Info{"1.4.0", UnknownCommit, UnknownCommitTime, false, "go1.25.1", "linux/amd64"},
		},
		{"devel", installed("(devel)"), Info{"1.4.0", UnknownCommit, UnknownCommitTime, false, "go1.25.1", "linux/amd64"}},
		{
			"vcs wins over the pseudo-version",
			func() *debug.BuildInfo {
				bi := build("vcs.revision", "abc123", "vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "true")
				bi.Main.Version = "v0.0.0-20260929021723-4df90bd5d3e3+dirty"
				return bi
			}(),
			Info{"1.4.0", "abc123", "2026-09-27T12:34:56Z", true, "go1.25.1", "linux/amd64"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fromBuild(tc.bi, "1.4.0", "go-running", "linux/amd64"); got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// A build with no -ldflags version takes a release tag's version, and only a
// release tag's.
func TestFromBuildTagVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		bi   *debug.BuildInfo
		want string
	}{
		{"no build info", nil, DevVersion},
		{"go install, tagged version", installed("v0.1.0"), "0.1.0"},
		{"prerelease tag", installed("v0.2.0-rc.1"), "0.2.0-rc.1"},
		{"pseudo-version", installed("v0.1.1-0.20260929021723-4df90bd5d3e3"), DevVersion},
		{"checkout at a tag, modified", installed("v0.1.0+dirty"), DevVersion},
		{"devel", installed("(devel)"), DevVersion},
		{"no version", installed(""), DevVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fromBuild(tc.bi, DevVersion, "go-running", "linux/amd64").Version; got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	// A version set at build time wins over the tag.
	if got := fromBuild(installed("v0.1.0"), "1.4.0", "go-running", "linux/amd64").Version; got != "1.4.0" {
		t.Errorf("-ldflags version: got %q", got)
	}
}

func TestRead(t *testing.T) {
	info := Read()
	if info.Version != Version || info.Go == "" || !regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`).MatchString(info.Platform) {
		t.Errorf("Read() = %+v", info)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(info.CommitTime) {
		t.Errorf("commit time %q is not a timestamp", info.CommitTime)
	}
}
