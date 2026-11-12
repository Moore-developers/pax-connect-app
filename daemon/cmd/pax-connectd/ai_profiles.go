package main

import (
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/aiprofile"
	"pax-connect/daemon/internal/storage"
)

func userAIProfiles(directory string) (*aiprofile.Manager, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	root := filepath.Join(directory, "AIProfiles")
	if e = storage.PreparePrivateDirectory(root, uint32(os.Geteuid())); e != nil {
		return nil, e
	}
	dirs := map[aiprofile.CLI]string{}
	for variable, cli := range map[string]aiprofile.CLI{"CODEX_HOME": aiprofile.Codex, "CLAUDE_CONFIG_DIR": aiprofile.Claude, "DSH_HOME": aiprofile.DSH} {
		if p := os.Getenv(variable); p != "" {
			if !filepath.IsAbs(p) {
				return nil, aiprofile.Unsafe
			}
			dirs[cli] = filepath.Clean(p)
		}
	}
	return aiprofile.New(aiprofile.Options{UsageOnlyDSH: true, ModelClient: aiModelHTTPClient(), Directory: root, Home: home, Owner: uint32(os.Geteuid()), Directories: dirs, DSHVersion: "read_only"})
}
