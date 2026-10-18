package buildinfo

import (
	"regexp"
	"runtime/debug"
	"testing"
)

func build(version string, settings ...string) *debug.BuildInfo {
	bi := &debug.BuildInfo{Main: debug.Module{Version: version}}
	for i := 0; i < len(settings); i += 2 {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return bi
}

func TestFromBuild(t *testing.T) {
	const pseudoTime = "2026-09-29T02:17:23Z"
	for _, tc := range []struct {
		name string
		bi   *debug.BuildInfo
		want Info
	}{
		{"no build info", nil, Info{DevVersion, UnknownCommit, UnknownCommitTime, false}},
		{"devel, no vcs", build("(devel)"), Info{DevVersion, UnknownCommit, UnknownCommitTime, false}},
		{"no version", build(""), Info{DevVersion, UnknownCommit, UnknownCommitTime, false}},
		{
			"clean checkout",
			build("(devel)", "vcs.revision", "abc123", "vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "false"),
			Info{DevVersion, "abc123", "2026-09-27T12:34:56Z", false},
		},
		{
			"modified, offset time with fraction",
			build("(devel)", "vcs.revision", "abc123", "vcs.time", "2026-09-27T14:34:56.9+02:00", "vcs.modified", "true"),
			Info{DevVersion, "abc123", "2026-09-27T12:34:56Z", true},
		},
		{
			"unparseable time",
			build("(devel)", "vcs.revision", "abc123", "vcs.time", "yesterday"),
			Info{DevVersion, "abc123", UnknownCommitTime, false},
		},
		{
			"time without revision",
			build("(devel)", "vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "true"),
			Info{DevVersion, UnknownCommit, UnknownCommitTime, false},
		},
		{"go install, tagged version", build("v0.1.0"), Info{"0.1.0", UnknownCommit, UnknownCommitTime, false}},
		{"go install, prerelease tag", build("v0.2.0-rc.1"), Info{"0.2.0-rc.1", UnknownCommit, UnknownCommitTime, false}},
		{
			"checkout at a tag",
			build("v0.1.0", "vcs.revision", "abc123", "vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "false"),
			Info{"0.1.0", "abc123", "2026-09-27T12:34:56Z", false},
		},
		{
			"checkout at a tag, modified",
			build("v0.1.0+dirty", "vcs.revision", "abc123", "vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "true"),
			Info{DevVersion, "abc123", "2026-09-27T12:34:56Z", true},
		},
		{
			"go install, pseudo-version with no tag",
			build("v0.0.0-20260929021723-4df90bd5d3e3"),
			Info{DevVersion, "4df90bd5d3e3", pseudoTime, false},
		},
		{
			"go install, pseudo-version after a tag",
			build("v1.4.1-0.20260929021723-4df90bd5d3e3"),
			Info{DevVersion, "4df90bd5d3e3", pseudoTime, false},
		},
		{
			"go install, pseudo-version after a prerelease",
			build("v1.5.0-rc.1.0.20260929021723-4df90bd5d3e3"),
			Info{DevVersion, "4df90bd5d3e3", pseudoTime, false},
		},
		{
			"vcs wins over the pseudo-version",
			build("v0.0.0-20260929021723-4df90bd5d3e3+dirty", "vcs.revision", "abc123", "vcs.time", "2026-09-27T12:34:56Z", "vcs.modified", "true"),
			Info{DevVersion, "abc123", "2026-09-27T12:34:56Z", true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fromBuild(tc.bi); got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestRead(t *testing.T) {
	info := Read()
	if info.Version == "" || info.Commit == "" {
		t.Errorf("Read() = %+v", info)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(info.CommitTime) {
		t.Errorf("commit time %q is not a timestamp", info.CommitTime)
	}
}
