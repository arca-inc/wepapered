package core

import "strings"

// Build metadata, injected at build time via
//   -ldflags "-X wepapered/internal/core.Version=<ver> -X wepapered/internal/core.Date=<date>"
// (see the Makefile's VERSION / DATE variables; CI and the PKGBUILD set them).
// Version is r<commit-count>.<short-sha> (or a tag); Date is the commit date.
// Both fall back to placeholders for plain local builds.
var (
	Version = "dev"
	Date    = ""
)

// RepoURL is the project's GitHub repository.
const RepoURL = "https://github.com/arca-inc/wepapered"

// commitSHA returns the short commit hash embedded in Version (the part after the
// last '.'), or "" when the version isn't the r<count>.<sha> form (e.g. "dev").
func commitSHA() string {
	if Version == "dev" || Version == "" {
		return ""
	}
	if i := strings.LastIndex(Version, "."); i >= 0 && i+1 < len(Version) {
		return Version[i+1:]
	}
	return ""
}

// CommitURL is the GitHub link to the commit this build was made from, or "".
func CommitURL() string {
	if sha := commitSHA(); sha != "" {
		return RepoURL + "/commit/" + sha
	}
	return ""
}

// VersionString is the multi-line human version banner: the version (with the
// build date when known) and the commit link.
func VersionString() string {
	s := "wepapered " + Version
	if Date != "" {
		s += " (" + Date + ")"
	}
	if u := CommitURL(); u != "" {
		s += "\n" + u
	}
	return s
}
