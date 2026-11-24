//go:build integration

package controller

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/ipc"
)

// Opt-in native/socket acceptance host. Synthetic observations go through the
// actual journal/SQLite pipeline; no fixture code enters the shipped binaries.
func TestTrafficAlertsNativeFixture(t *testing.T) {
	directory := os.Getenv("PAX_TRAFFIC_ALERTS_FIXTURE")
	if directory == "" {
		t.Skip("explicit isolated native acceptance only")
	}
	dir, err := filepath.EvalSymlinks(directory)
	if err != nil || !strings.Contains(dir, "/.pax-traffic-alerts-") {
		t.Fatal("isolated private directory required", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c := New("traffic-alerts-fixture", "fixture")
	catalog := config.NewCatalog()
	for _, id := range []string{"qa-a", "qa-b"} {
		profile := config.RouteConfig{NodeProfile: config.NodeProfile{ID: id, Name: "Fixture " + id, ProtocolName: "trojan", Server: id + ".example.test", Port: 443}, SchemaVersion: 1, Password: "fixture", ServerName: id + ".example.test", Security: "tls", Transport: "tcp"}
		if _, _, err := catalog.Add(profile); err != nil {
			t.Fatal(err)
		}
	}
	c.ConfigureCatalog(&memoryCatalog{catalog: catalog, exists: true})
	c.ConfigureNetworkPrivacy(config.NewNetworkPrivacyStore(filepath.Join(dir, "network-privacy.json"), uint32(os.Geteuid())))
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	if err = ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = CloseHistoryFixture(c, context.Background()) }()
	c.ingest = &historyIngest{}
	beginning := time.Now().Add(-25 * time.Hour)
	tick := func() {
		c.ingest.coverage.Store(&coverageSpan{valid: true, collecting: true, runFrom: beginning, last: time.Now()})
	}
	tick()
	if _, err = os.Stat(filepath.Join(dir, "seeded")); os.IsNotExist(err) {
		now := time.Now().Add(-10 * time.Second)
		batch := config.ObservationBatch{Session: "traffic-alert-fixture", Collecting: true}
		for i, route := range []string{"qa-a", "qa-b"} {
			f := &config.FlowFact{Key: "traffic-alert-fixture/" + route, ApplicationID: "fixture.downloader", Application: "受控下载应用", Process: "/Applications/FixtureDownloader.app/Contents/MacOS/Downloader", Domain: "fixture.example.test", RouteID: route, RouteName: route, Direction: "proxy", StartedAt: now}
			batch.HighWater++
			batch.Records = append(batch.Records, config.Observation{Session: batch.Session, Sequence: batch.HighWater, At: now, Kind: "sample", Flow: f, Upload: uint64(i+1) * 10_000_000, Download: uint64(i+1) * 100_000_000})
		}
		if err = FeedHistoryFixture(t, c, ctx, batch); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "seeded"), []byte("synthetic 300 MB download across qa-a/qa-b\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Lock()
	c.current.Status = pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED
	c.mu.Unlock()
	c.ConfigureTrafficAlerts(ctx, dir, uint32(os.Geteuid()))
	defer func() { _ = c.CloseTrafficAlerts(context.Background()) }()
	listener, err := ipc.ListenUser(filepath.Join(dir, "ui.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server := grpc.NewServer()
	pb.RegisterPaxServiceServer(server, c)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	if err = os.WriteFile(filepath.Join(dir, "ready"), []byte(listener.Addr().String()), 0600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
			// This file is a test-only request to reevaluate after an external byte/setting check.
			if _, err = os.Stat(filepath.Join(dir, "check")); err == nil {
				if err = os.Remove(filepath.Join(dir, "check")); err != nil {
					t.Fatal(err)
				}
				select {
				case c.trafficAlerts.wake <- struct{}{}:
				default:
				}
			}
		}
	}
}
