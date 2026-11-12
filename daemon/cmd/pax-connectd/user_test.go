package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestUserDirectoryIgnoresCatalogOverride(t *testing.T) {
	t.Setenv("HOME", "/Users/fixture")
	t.Setenv("PAX_CATALOG", "/forbidden/root/routes.json")
	dir, err := userDataDirectory()
	if err != nil || dir != "/Users/fixture/Library/Application Support/Pax Connect" {
		t.Fatalf("%s %v", dir, err)
	}
}

func TestUserOwnerDeathAndIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	join, err := watchUserOwner(ctx, cancel, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer join()
	if _, err := watchUserOwner(ctx, cancel, -1); err == nil {
		t.Fatal("invalid owner admitted")
	}
	done := make(chan struct{})
	go func() { pollUserOwner(ctx, cancel, time.Millisecond, func() bool { return false }); close(done) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("owner death not observed")
	}
	<-done
}
