package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"pax-connect/daemon/internal/aiprofile"
)

func TestUsageServiceDoesNotRequirePersonalHistoricalProfiles(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("HOME", home)
	m, e := aiprofile.New(aiprofile.Options{Directory: filepath.Join(home, "profiles"), Home: home, Owner: uint32(os.Geteuid()), UsageOnlyDSH: true})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := grpc.NewServer()
	defer server.Stop()
	s, e := userAIUsage(ctx, filepath.Join(home, "usage"), m, server)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(context.Background()) }()
	if _, ok := server.GetServiceInfo()["pax.v1.AIUsageService"]; !ok {
		t.Fatal("missing Krill/Derouter prevented usage service")
	}
}
