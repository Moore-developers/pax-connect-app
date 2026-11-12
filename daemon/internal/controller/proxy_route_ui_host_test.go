//go:build hostproxy

package controller

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// Explicit native UI/socket acceptance; all records are isolated from user data.
func TestPurchasedProxyUIHost(t *testing.T) {
	directory := os.Getenv("PAX_PROXY_UI_DIRECTORY")
	if directory == "" {
		t.Skip("explicit native UI only")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	directory = resolved
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := New("manual-proxy-ui", "")
	state.ConfigureCatalog(config.NewCatalogStore(filepath.Join(directory, "routes.json"), uint32(os.Getuid())))
	state.ConfigureTLS(config.NewTLSPolicyStore(filepath.Join(directory, "tls.json"), uint32(os.Getuid())))
	state.ConfigureMeasurements(config.NewMeasurementStore(filepath.Join(directory, "measurements.json"), uint32(os.Getuid())))
	state.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	listener, err := net.Listen("unix", filepath.Join(directory, "p.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server := grpc.NewServer()
	paxv1.RegisterPaxServiceServer(server, state)
	t.Cleanup(server.Stop)
	t.Log("isolated proxy UI socket ready")
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}
