package controller_test

import (
	"context"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestPersistedTrafficReadableOffWithoutCore(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	c := controller.New("test", "")
	if err = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-2 * time.Hour)
	end := start.Add(time.Minute)
	fact := config.FlowFact{Key: "saved/1/c", Domain: "example.test", ApplicationID: "test.app", Application: "保存的应用", Direction: "proxy", StartedAt: start}
	if err = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "saved", Collecting: true, HighWater: 2, Records: []config.Observation{{Session: "saved", Sequence: 1, At: start, Kind: "started", Flow: &fact}, {Session: "saved", Sequence: 2, At: start.Add(time.Second), Kind: "sample", Flow: &fact, Upload: 42000, Download: 240000}}}); err != nil {
		t.Fatal(err)
	}
	fact.ClosedAt = &end
	if err = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "saved", HighWater: 3, Records: []config.Observation{{Session: "saved", Sequence: 3, At: end, Kind: "closed", Flow: &fact}}}); err != nil {
		t.Fatal(err)
	}
	if err = controller.CloseHistoryFixture(c, ctx); err != nil {
		t.Fatal(err)
	}
	c = controller.New("test", "")
	if err = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controller.CloseHistoryFixture(c, ctx) }()
	snap, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil || snap.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal(snap, err)
	}
	windows := []string{"day"}
	if start.In(time.Local).Format("2006-01-02") == time.Now().In(time.Local).Format("2006-01-02") {
		windows = append(windows, "today")
	}
	for _, window := range windows {
		page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: window})
		if err != nil || len(page.Items) != 1 || page.Items[0].Upload != 42000 || page.Items[0].Download != 240000 {
			t.Fatal("persisted history unavailable while off", window, page, err)
		}
	}
	minute, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "minute"})
	if err != nil || len(minute.Items) != 0 {
		t.Fatal("old data should not appear in minute window", minute, err)
	}
}
