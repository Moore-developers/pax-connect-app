package main

import (
	"errors"
	"os"
)

// initialInstallation runs before startup creates empty stores. Only runtime files
// may be ignored; unknown durable files count as existing data rather than a reset.
func initialInstallation(directory string) (bool, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "run", "diagnostics.jsonl", "diagnostics.jsonl.1":
			continue
		}
		// Ignore no other existing state, even when the current version cannot read it.
		return false, nil
	}
	return true, nil
}
