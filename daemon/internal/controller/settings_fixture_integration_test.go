//go:build integration

package controller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

// Prepares synthetic on-disk inputs for the real daemon/MCP acceptance script.
// No fixture branch is compiled into the application.
func TestSettingsAcceptanceSeed(t *testing.T) {
	dir := os.Getenv("PAX_SETTINGS_SEED_DIR")
	if dir == "" {
		t.Skip("only run by settings_acceptance.py against an isolated HOME")
	}
	if !filepath.IsAbs(dir) || !strings.Contains(dir, "/.pax-m8-") {
		t.Fatal("requires isolated acceptance directory")
	}
	c := New("fixture", "")
	ctx := context.Background()
	t.Cleanup(func() {
		if err := c.CloseHistory(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	for i, age := range []time.Duration{18 * time.Hour, 20 * time.Minute} {
		at := now.Add(-age)
		closed := at.Add(time.Second)
		key := []string{"old-flow", "recent-flow"}[i]
		f := config.FlowFact{Key: key, Domain: key + ".example.test", Direction: "direct", RouteID: "direct", RouteName: "直连", StartedAt: at, ClosedAt: &closed, EstablishmentState: "not_measurable"}
		base := uint64(i * 3)
		b := config.ObservationBatch{Session: "settings-fixture", Collecting: true, HighWater: base + 3, Records: []config.Observation{
			{Session: "settings-fixture", Sequence: base + 1, At: at, Kind: "started", Flow: &f},
			{Session: "settings-fixture", Sequence: base + 2, At: at, Kind: "diagnostic", Level: "info", Raw: "connection: synthetic-settings-raw-" + key, Flow: &f},
			{Session: "settings-fixture", Sequence: base + 3, At: closed, Kind: "closed", Flow: &f, Download: 1024, Upload: 128},
		}}
		if err := FeedHistoryFixture(t, c, ctx, b); err != nil {
			t.Fatal(err)
		}
	}
}
