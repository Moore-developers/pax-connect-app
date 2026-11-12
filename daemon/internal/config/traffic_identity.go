package config

import (
	"path/filepath"
	"strings"
)

// systemComponentPath identifies protected OS component locations, not a service role.
func systemComponentPath(path string) bool {
	for _, root := range []string{"/System/Library/", "/usr/libexec/", "/System/Cryptexes/App/usr/libexec/", "/System/Volumes/Preboot/Cryptexes/App/usr/libexec/"} {
		if strings.HasPrefix(path, root) {
			return true
		}
	}
	return false
}

// aiCLIName accepts dedicated installation/package paths. An interpreter name,
// parent process, endpoint or arbitrary executable named "claude" is not evidence.
func aiCLIName(path string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	home := len(parts) > 2 && (parts[0] == "Users" && parts[1] != "" || parts[0] == "var" && parts[1] == "root")
	if home && len(parts) == 7 && parts[2] == ".local" && parts[3] == "share" && parts[4] == "claude" && parts[5] == "versions" && versionComponent.MatchString(parts[6]) {
		return "Claude Code", true
	}
	for _, pkg := range []struct{ suffix, executable, name string }{
		{"/node_modules/@openai/codex/", "codex", "Codex CLI"},
		{"/node_modules/@anthropic-ai/claude-code/", "claude", "Claude Code"},
	} {
		if at := strings.Index(path, pkg.suffix); at >= 0 {
			relative := path[at+len(pkg.suffix):]
			// Current npm Codex installs its native binary in a platform package
			// beneath @openai/codex. Preserve both package identities as evidence.
			if pkg.name == "Codex CLI" {
				relative = strings.TrimPrefix(relative, "node_modules/@openai/codex-darwin-arm64/")
			}
			if filepath.Base(relative) == pkg.executable && strings.HasPrefix(relative, "vendor/") && !strings.Contains(relative, "/../") {
				return pkg.name, true
			}
		}
	}
	return "", false
}

// TrafficTarget identifies the observed target without inferring a domain from an IP.
// Reserved non-domain keys keep IP-only and wholly missing targets separate.
func TrafficTarget(f FlowFact) (id, name string) {
	if domain := MainDomain(f.Domain); domain != "" {
		return domain, domain
	}
	if f.IP != "" {
		return "target:ip-only", "仅记录 IP（域名未识别）"
	}
	return "target:unknown", "目标未采集"
}

// productCLIName requires a dedicated installed product path, not a generic runtime name.
func productCLIName(path string) (string, bool) {
	if name, ok := aiCLIName(path); ok {
		return name, true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/opt/homebrew/Cellar/gh/"), "/")
	if strings.HasPrefix(path, "/opt/homebrew/Cellar/gh/") && len(parts) == 3 && versionComponent.MatchString(parts[0]) && parts[1] == "bin" && parts[2] == "gh" {
		return "gh", true
	}
	return "", false
}
