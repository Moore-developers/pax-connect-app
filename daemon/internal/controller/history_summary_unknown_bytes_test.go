package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// A summary from a writer without checkpoints or summary generations has no byte proof, and
// the old transfers fallback is gone (new summaries carry metrics from their first record).
// Before the summary's last observation the read shows the missing default; after it, the
// demoted summary (legacy_history, as_of = last sample).
func TestLogSummaryWithoutCheckpointsReportsUnknownBytes(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, p := newLogsTestController(t, dir)
	base := time.Now().Add(-time.Minute).Truncate(time.Second)
	fact := config.FlowFact{Key: "legacy/1/c", StartedAt: base, Domain: "example.test"}
	p.observe(config.ObservationBatch{Session: "legacy", HighWater: 3, Records: []config.Observation{{Session: "legacy", Sequence: 1, At: base, Kind: "started", Flow: &fact}, {Session: "legacy", Sequence: 2, At: base.Add(1800 * time.Millisecond), Kind: "sample", Flow: &fact, Upload: 500}, {Session: "legacy", Sequence: 3, At: base.Add(2800 * time.Millisecond), Kind: "sample", Flow: &fact, Upload: 250}}})
	stopLogsTestController(t, c)
	stripLogEventCheckpoints(t, filepath.Join(dir, "history", "logs.db"))
	c, _ = newLogsTestController(t, dir)
	read := func(until time.Duration) *pb.ConnectionMetrics {
		page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Until: timestamppb.New(base.Add(until))})
		if err != nil || len(page.Records) != 1 {
			t.Fatalf("until +%v: %v %v", until, page, err)
		}
		return page.Records[0].Metrics
	}
	m := read(1500 * time.Millisecond)
	if m.Upload != 0 || m.Download != 0 || m.BytesState != "unknown" || m.Reason != "missing_metrics" || m.AsOf != nil {
		t.Fatal("before the last observation: want 0/0 unknown missing_metrics, no as_of", m)
	}
	m = read(3500 * time.Millisecond)
	if m.Upload != 0 || m.Download != 0 || m.BytesState != "unknown" || m.Reason != "legacy_history" || !m.AsOf.AsTime().Equal(base.Add(2800*time.Millisecond)) {
		t.Fatal("after the last observation: want 0/0 unknown legacy_history, as_of base+2.8s", m)
	}
}
