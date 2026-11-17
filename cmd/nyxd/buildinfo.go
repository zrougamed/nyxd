package main

import "runtime/debug"

// shortCommitLen is the length git abbreviates this repository's commits
// to, so the fallback matches GoReleaser's {{ .ShortCommit }}.
const shortCommitLen = 8

// fillFromBuildInfo replaces an unset commit or build date with the VCS
// stamp the Go toolchain embeds (vcs.revision, vcs.time), so a binary built
// without -ldflags (go build, go install) still reports where it came from.
// Values set through -ldflags win.
func fillFromBuildInfo(commit, date string, info *debug.BuildInfo) (string, string) {
	if info == nil {
		return commit, date
	}
	var rev, at string
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if commit == "unknown" && rev != "" {
		if len(rev) > shortCommitLen {
			rev = rev[:shortCommitLen]
		}
		if modified {
			rev += "-dirty"
		}
		commit = rev
	}
	if date == "unknown" && at != "" {
		date = at
	}
	return commit, date
}

func init() {
	info, _ := debug.ReadBuildInfo()
	gitCommit, buildDate = fillFromBuildInfo(gitCommit, buildDate, info)
}
