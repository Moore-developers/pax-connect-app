package controller

import (
	"context"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// Acceptance: one incremental read's head and touched group have the same
// commit even when an append arrives between scanning IDs and projecting it.
func TestColumnLogUpdatesFreezeHeadAndTouchedGroup(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	at := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "updates/frozen", StartedAt: at, Domain: "example.test", Direction: "direct"}
	p.emit(config.HistoryEvent{ID: "one", At: at, Kind: "started", Level: "info", Source: "sing-box", Flow: &f})
	job, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := job.logColumnSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	head := snapshot.Meta().HeadSequence
	rows, err := snapshot.EventsAfter(ctx, 0, int64(head), 10)
	if err != nil || len(rows) != 1 {
		t.Fatal(len(rows), err)
	}
	p.emit(config.HistoryEvent{ID: "two", At: at.Add(time.Second), Kind: "ready", Level: "info", Source: "sing-box", Flow: &f})
	item, err := c.columnLogUpdateGroup(ctx, snapshot, &pb.LogQuery{View: "access"}, accessRecordID(f.Key), at.Add(-time.Hour))
	if err != nil || item == nil || item.Occurrences != 1 || item.EventId != "one" {
		t.Fatal("old head included later group member", item, err)
	}
	fresh, err := job.logColumnSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	item, err = c.columnLogUpdateGroup(ctx, fresh, &pb.LogQuery{View: "access"}, accessRecordID(f.Key), at.Add(-time.Hour))
	if err != nil || item == nil || item.Occurrences != 2 || item.EventId != "two" {
		t.Fatal("new head missed appended member", item, err)
	}
}
