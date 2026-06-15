package core

// Version is the build version, injected at build time via
//   -ldflags "-X wepapered/internal/core.Version=<ver>"
// (see the Makefile's VERSION variable; CI sets it to the tag for releases or
// r<commit-count>.<short-sha> for rolling builds). "dev" for plain local builds.
var Version = "dev"
