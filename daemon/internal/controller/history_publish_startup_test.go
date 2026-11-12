package controller

import (
	"context"
	"os"
	"testing"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

func TestHistoryPublisherRetriesTrafficWhenStoreArrivesAfterFirstSnapshot(t *testing.T) {
	ctx := context.Background()
	c := New("test", "test")
	c.history = &historyOwner{}
	c.publishHistory(ctx)
	dir := newLogsTestDir(t)
	if e := c.ConfigureHistoryTraffic(ctx, dir, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = c.CloseHistoryTraffic(ctx) }()
	if e := c.traffic.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	c.publishHistory(ctx)
	snapshot, e := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if e != nil {
		t.Fatal(e)
	}
	if snapshot.TodayTraffic == nil || snapshot.TodayTraffic.AsOf == nil || snapshot.TodayTraffic.AsOf.AsTime().IsZero() {
		t.Fatalf("late store left initial traffic snapshot: %v", snapshot.TodayTraffic)
	}
	if snapshot.TodayTraffic.CollectionState == "" || snapshot.TodayTraffic.CollectionState == "unknown" {
		t.Fatalf("late store left disconnected traffic: %v", snapshot.TodayTraffic)
	}
}

func TestTrafficFailureDoesNotBlockLogSettingsPublication(t *testing.T) {
	ctx := context.Background()
	c := New("test", "test")
	c.history = &historyOwner{}
	c.logState.Store(&historystore.LogsMeta{LogsRevision: 7, SettingsRevision: 3, RetentionHours: 12})
	c.publishHistory(ctx)
	snapshot, e := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if e != nil {
		t.Fatal(e)
	}
	if snapshot.History.LogsRevision != 7 || snapshot.Settings.Revision != 3 || snapshot.Settings.LogRetentionHours != 12 {
		t.Fatalf("traffic failure blocked independent logs/settings: %v %v", snapshot.History, snapshot.Settings)
	}
	if c.history.publishedDay != "" {
		t.Fatal("failed traffic publication disabled retry")
	}
}
