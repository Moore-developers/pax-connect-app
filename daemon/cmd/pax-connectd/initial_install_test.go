package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitialInstallIgnoresOnlyRuntimeFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"diagnostics.jsonl", "diagnostics.jsonl.1"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	fresh, err := initialInstallation(dir)
	if err != nil || !fresh {
		t.Fatal("runtime directory is not business state")
	}
	if err = os.WriteFile(filepath.Join(dir, "ai_usage.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	fresh, err = initialInstallation(dir)
	if err != nil || fresh {
		t.Fatal("existing store must not be treated as first installation")
	}
}
