package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/ipc"
	"pax-connect/daemon/internal/processidentity"
	"time"
)

func userDataDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("invalid_user_home")
	}
	return filepath.Join(home, "Library", "Application Support", "Pax Connect"), nil
}

// The App asks this executable for the canonical path, then passes it explicitly.
func userSocketPath() (string, error) {
	dir, err := userDataDirectory()
	if err != nil {
		return "", err
	}
	return ipc.UserSocketPath(dir, os.TempDir())
}

func watchUserOwner(ctx context.Context, stop context.CancelFunc, pid int) (func(), error) {
	owner, err := processidentity.Read(pid)
	if err != nil || owner.UID != uint32(os.Geteuid()) {
		return nil, errors.New("owner_identity_mismatch")
	}
	watch, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		pollUserOwner(watch, stop, time.Second, func() bool { return processidentity.Alive(owner) })
	}()
	return func() { cancel(); <-done }, nil
}

func pollUserOwner(ctx context.Context, stop context.CancelFunc, interval time.Duration, alive func() bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !alive() {
				stop()
				return
			}
		}
	}
}
