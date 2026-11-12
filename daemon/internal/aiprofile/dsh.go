package aiprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DetectedDSHVersion checks the installed CLI package without running a command or a session.
func DetectedDSHVersion(home string) string {
	if v := desktopDSHVersion(home); v != "" {
		return v
	}
	bin, err := filepath.EvalSymlinks(filepath.Join(home, ".local", "bin", "dsh"))
	if err != nil {
		return ""
	}
	root := filepath.Dir(filepath.Dir(bin))
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct{ Name, Version string }
	if json.Unmarshal(b, &pkg) != nil || pkg.Name != "@deepseek-ai/dsh" {
		return ""
	}
	for _, name := range []string{"dsh-settings-file", "dsh-llm-deepseek", "dsh-credentials-local"} {
		if _, err = os.Stat(filepath.Join(root, "node_modules", "@deepseek-ai", name, "lib", "index.js")); err != nil {
			return ""
		}
	}
	return pkg.Version
}
func (m *Manager) supportsDSH() bool {
	return m.dshVersion == "0.1.5-rc.1" || m.dshVersion == "0.2.1-rc.3"
}
