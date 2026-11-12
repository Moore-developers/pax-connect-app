package config

import (
	"path/filepath"
	"regexp"
	"strings"
)

var versionComponent = regexp.MustCompile(`^v?\d+(\.\d+)+([-+._][0-9A-Za-z.]+)?$`)

var genericDirs = map[string]bool{
	"bin": true, "sbin": true, "libexec": true, "versions": true, "version": true,
	"current": true, "latest": true, "stable": true, "Contents": true, "MacOS": true, "Resources": true,
}

// ExecutableDisplayName names a bare executable for display. A version number or
// generic directory is not a name, so it walks up to the first meaningful component.
// It never changes identity: callers keep "process:<path>" as the ID.
func ExecutableDisplayName(path string) string {
	base := filepath.Base(path)
	if !filepath.IsAbs(path) {
		return base
	}
	for p := filepath.Clean(path); p != "/" && p != "."; p = filepath.Dir(p) {
		name := filepath.Base(p)
		if versionComponent.MatchString(name) || genericDirs[name] {
			continue
		}
		if strings.HasPrefix(name, ".") {
			break
		}
		return name
	}
	return base
}

// FlowApplicationName prefers the recomputed executable name for path identities,
// so records saved with a version-like name display consistently. IDs are unchanged.
func FlowApplicationName(f FlowFact) string {
	path := f.Process
	if path == "" && strings.HasPrefix(f.ApplicationID, "process:") {
		path = strings.TrimPrefix(f.ApplicationID, "process:")
	}
	if strings.HasPrefix(f.ApplicationID, "process:") && filepath.IsAbs(path) {
		if name, ok := aiCLIName(path); ok {
			return name
		}
		return ExecutableDisplayName(path)
	}
	return f.Application
}
