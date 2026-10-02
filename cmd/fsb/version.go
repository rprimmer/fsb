package main

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// versionString describes the build: the module version Go stamped from the git
// tag (or a pseudo-version past it), the commit it was built from, and whether
// the checkout had uncommitted changes. A "go install ...@vX.Y.Z" build has the
// tag but no commit, since the module proxy carries no repository.
func versionString(info *debug.BuildInfo) string {
	if info == nil {
		return "fsb (devel) (commit unknown)"
	}
	version := info.Main.Version
	if version == "" {
		version = "(devel)"
	}
	var rev string
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	var parts []string
	switch {
	case rev != "":
		parts = append(parts, "commit "+rev[:min(12, len(rev))])
		if modified {
			parts = append(parts, "modified")
		}
	case strings.HasPrefix(version, "v"):
		parts = append(parts, "commit of tag "+version)
	default:
		parts = append(parts, "commit unknown")
	}
	if info.GoVersion != "" {
		parts = append(parts, info.GoVersion)
	}
	return fmt.Sprintf("fsb %s (%s)", version, strings.Join(parts, ", "))
}

func buildVersion() string {
	info, _ := debug.ReadBuildInfo()
	return versionString(info)
}
