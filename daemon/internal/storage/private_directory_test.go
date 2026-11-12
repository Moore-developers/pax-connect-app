package storage

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPreparePrivateDirectorySupportsProtectedStore(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(parent, "state")
	if err := PreparePrivateDirectory(directory, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	store := NewPrivateFile(filepath.Join(directory, "recovery.json"), uint32(os.Geteuid()), 4096)
	if err := store.Write(context.Background(), []byte(`{"Version":1,"Receipts":[]}`)); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{directory: 0700, filepath.Join(directory, "recovery.json"): 0600} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != mode || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			t.Fatalf("user owner/mode: %v", err)
		}
	}
	if _, exists, err := store.Read(); err != nil || !exists {
		t.Fatal("prepared directory still rejected", exists, err)
	}
	if err := PreparePrivateDirectory(directory, uint32(os.Geteuid())); err != nil {
		t.Fatal("retry not idempotent", err)
	}
}
func TestPreparePrivateDirectoryRefusesLinkWithoutChangingTarget(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "state")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateDirectory(link, uint32(os.Geteuid())); err == nil {
		t.Fatal("accepted symlink")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("modified symlink target", err)
	}
}
