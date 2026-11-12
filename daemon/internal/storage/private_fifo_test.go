package storage

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPrivateFileRejectsFIFOWithoutBlocking(t *testing.T) {
	directory, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = PreparePrivateDirectory(directory, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(directory, "state.json")
	if e = syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, _, err := NewPrivateFile(path, uint32(os.Geteuid()), 4096).Read(); done <- err }()
	select {
	case err := <-done:
		if err != ErrPrivateFile {
			t.Fatal("FIFO was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("non-regular state file blocked private storage")
	}
}
