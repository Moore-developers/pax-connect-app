package controller

import (
	"context"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// Incremental results must respect the same typed filters as the full query.
func TestLogUpdateTypedFilter(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	before, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"proxy", "direct"} {
		flow := config.FlowFact{Key: direction, Domain: direction + ".example.test", Application: "Fixture", Process: "fixture", StartedAt: time.Now(), Direction: direction, RouteID: "route"}
		event := config.HistoryEvent{ID: direction, At: time.Now(), Title: "连接", Kind: "started", Level: "info", Flow: &flow}
		if err = c.RecordEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	q := &pb.LogUpdateRequest{Filter: &pb.LogQuery{View: "activity", Dimension: "domain", IncludeDns: true, Criteria: &pb.LogCriteria{Directions: []string{"proxy"}}}, AfterSequence: before.HeadSequence, JournalGeneration: before.History.JournalGeneration, DeletionGeneration: before.History.LogDeletionGeneration}
	got, err := c.QueryLogUpdates(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResetRequired || got.Count != 1 || len(got.Items) != 1 || got.Items[0].GetFlow().GetDomain() != "proxy.example.test" {
		t.Fatalf("typed filters ignored: %v", got)
	}
}
