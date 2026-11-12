//go:build hostui

package controller

import (
	"context"
	"google.golang.org/grpc"
	"net"
	"os"
	"path/filepath"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
	"testing"
	"time"
)

func TestRoutesUIFixture(t *testing.T) {
	dir := os.Getenv("PAX_ROUTES_UI_DIRECTORY")
	if dir == "" {
		t.Skip("explicit UI only")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("routes-ui", "")
	c.ConfigureCatalog(config.NewCatalogStore(filepath.Join(dir, "routes.json"), uint32(os.Getuid())))
	c.ConfigureMeasurements(config.NewMeasurementStore(filepath.Join(dir, "measurements.json"), uint32(os.Getuid())))
	c.ConfigureTLS(config.NewTLSPolicyStore(filepath.Join(dir, "tls.json"), uint32(os.Getuid())))
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	c.ConfigureRouteTester(&measuredTester{exit: "8.8.8.8"}, maxParallelTests)
	c.ConfigureSubscriptions(onePassUIFetcher{})
	c.proxy.routes = func(context.Context, string) network.Evidence {
		return network.Evidence{Interface: true, IPv4Route: true, IPv6Route: true}
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "p.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	paxv1.RegisterPaxServiceServer(server, c)
	t.Cleanup(server.Stop)
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}

// Isolated native QA only: no network request or real node credentials.
type onePassUIFetcher struct{}

func (onePassUIFetcher) FetchSubscription(ctx context.Context, _ string) ([]byte, error) {
	delay := time.Second
	if os.Getenv("PAX_ROUTES_UI_SLOW") == "1" {
		delay = 20 * time.Second
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(delay):
	}
	return []byte("trojan://fixture@one.test:443#Source-One\ntrojan://fixture@two.test:443#Source-Two"), nil
}
