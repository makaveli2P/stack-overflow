package main

import (
	"runtime/debug"
	"strings"
)

// version is the release name. The Makefile sets it with
// -ldflags "-X main.version=...". An empty version falls back to the VCS
// facts that the Go tool puts into the binary (see buildVersion).
var version = ""

// buildVersion returns the version for -version and the banner: the
// -ldflags value, else "dev" with the short VCS revision and the commit
// time, and "+dirty" when the build had changes that were not committed.
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return versionFrom(info.Settings)
}

// versionFrom builds the fallback version from the build settings.
func versionFrom(settings []debug.BuildSetting) string {
	var rev, at string
	dirty := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	parts := []string{"dev", rev[:min(len(rev), 7)]}
	if at != "" {
		parts = append(parts, at)
	}
	v := strings.Join(parts, " ")
	if dirty {
		v += " +dirty"
	}
	return v
}
