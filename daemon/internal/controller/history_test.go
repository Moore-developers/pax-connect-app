package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestOperationHistorySurvivesRestartAndPagesWithoutDuplicates(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, _ := newLogsTestController(t, dir)
	for i, name := range []string{"启用代理完成", "切换线路完成"} {
		if err := c.RecordEvent(ctx, config.HistoryEvent{ID: name, At: time.Now().Add(time.Duration(i-2) * time.Second), Kind: "operation", Title: name, Level: "info"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{Limit: 1})
	if err != nil || len(page.GetItems()) != 1 || page.NextCursor == "" {
		t.Fatalf("first page: %v %v", page, err)
	}
	if err := c.RecordEvent(ctx, config.HistoryEvent{ID: "new", At: time.Now(), Kind: "operation", Title: "新操作", Level: "info"}); err != nil {
		t.Fatal(err)
	}
	next, err := c.QueryLogs(ctx, &pb.LogQuery{Limit: 1, ReadToken: page.ReadToken, Cursor: page.NextCursor})
	if err != nil || len(next.GetItems()) != 1 || next.Items[0].EventId == page.Items[0].EventId {
		t.Fatalf("next page: %v %v", next, err)
	}
	stopLogsTestController(t, c)
	c, _ = newLogsTestController(t, dir)
	all, err := c.QueryLogs(ctx, &pb.LogQuery{})
	if err != nil || len(all.GetItems()) != 3 {
		t.Fatalf("restored: %v %v", all, err)
	}
}

func TestTrafficWindowCountsOnlyWindowDeltasAndClearDoesNotEraseTraffic(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	now := time.Now().UTC().Truncate(time.Second)
	f := config.FlowFact{Key: "core/1/a", Domain: "example.test", RouteID: "direct", RouteName: "直连", Direction: "direct", StartedAt: now.Add(-2 * time.Hour), EstablishmentState: "not_measurable"}
	b := config.ObservationBatch{Session: "core", Collecting: true, HighWater: 3, Records: []config.Observation{{Session: "core", Sequence: 1, At: now.Add(-90 * time.Second), Kind: "sample", Flow: &f, Download: 900}, {Session: "core", Sequence: 2, At: now.Add(-10 * time.Second), Kind: "sample", Flow: &f, Upload: 10, Download: 100}, {Session: "core", Sequence: 3, At: now.Add(-time.Second), Kind: "closed", Flow: &f}}}
	if err := FeedHistoryFixture(t, c, ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := FeedHistoryFixture(t, c, ctx, b); err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "minute"})
	if err != nil || page.Upload != 10 || page.Download != 100 || page.Connections != 1 {
		t.Fatalf("window: %v %v", page, err)
	}
	detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.Items[0].Id})
	if err != nil || detail.Summary.Download != 100 || len(detail.Items) != 1 {
		t.Fatalf("detail: %v %v", detail, err)
	}
	ack, err := c.ClearLogs(ctx, &pb.ClearLogsRequest{OperationId: "clear-test", RequestedAt: timestamppb.Now()})
	if err != nil || ack.OperationId == "" {
		t.Fatal(ack, err)
	}
	after, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "day"})
	if err != nil || after.Download != 1000 {
		t.Fatalf("clear erased traffic: %v %v", after, err)
	}
}

func TestClearRetriesDoNotDeleteNewLogsOrRestoreOldRaw(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	now := time.Now()
	f := config.FlowFact{Key: "core/1/a", StartedAt: now.Add(-time.Minute), Direction: "direct", RouteID: "direct"}
	before := config.ObservationBatch{Session: "core", Collecting: true, Records: []config.Observation{{Session: "core", Sequence: 1, At: now, Kind: "started", Flow: &f}, {Session: "core", Sequence: 2, At: now, Kind: "diagnostic", Level: "info", Raw: "old-raw", Flow: &f}}}
	p.feed(before)
	request := &pb.ClearLogsRequest{OperationId: "clear-once", RequestedAt: timestamppb.Now()}
	if _, err := c.ClearLogs(ctx, request); err != nil {
		t.Fatal(err)
	}
	p.feed(before)
	if err := c.RecordEvent(ctx, config.HistoryEvent{ID: "after-clear", At: time.Now(), Kind: "closed", Title: "新连接事件", Level: "info", Flow: &f}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ClearLogs(ctx, request); err != nil {
		t.Fatal(err)
	}
	detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{EventId: "after-clear"})
	if err != nil || len(detail.Raw) != 0 {
		t.Fatalf("raw revived or retry removed new event: %v %v", detail, err)
	}
}

func TestRetentionCASAndCleanupInvalidateSavedReads(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	// The new default log retention is 24 hours; an 18-hour old fixture must be admitted
	// under 24-hour retention before it is written (data preparation, not an assertion).
	snap0, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: 24}, ExpectedRevision: snap0.Settings.Revision, FieldMask: []string{"log_retention_hours"}, OperationId: "pre-retention", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = c.RecordEvent(ctx, config.HistoryEvent{ID: "old", At: time.Now().Add(-18 * time.Hour), Kind: "operation", Title: "旧操作", Level: "info"}); err != nil {
		t.Fatal(err)
	}
	snapshot, e := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: 24}, ExpectedRevision: snapshot.Settings.Revision, FieldMask: []string{"log_retention_hours"}, OperationId: "initial-retention", RequestedAt: timestamppb.Now()}); e != nil {
		t.Fatal(e)
	}
	q := &pb.LogQuery{From: timestamppb.New(time.Now().Add(-7 * 24 * time.Hour))}
	page, err := c.QueryLogs(ctx, q)
	if err != nil || len(page.Items) < 1 {
		t.Fatal(page, err)
	}
	snap, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: 12}, ExpectedRevision: snap.Settings.Revision, FieldMask: []string{"log_retention_hours"}, OperationId: "retention", RequestedAt: timestamppb.Now()}
	if _, err = c.UpdateSettings(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetLogDetail(ctx, &pb.LogDetailRequest{EventId: "old"}); err == nil {
		t.Fatal("old event survived shorter retention")
	}
	q.ReadToken = page.ReadToken
	if _, err = c.QueryLogs(ctx, q); err == nil {
		t.Fatal("deleted snapshot still readable")
	}
	fresh, e := c.QueryLogs(ctx, &pb.LogQuery{})
	if e != nil || fresh.History.LogDeletionGeneration <= page.History.LogDeletionGeneration || fresh.History.TrafficDeletionGeneration != page.History.TrafficDeletionGeneration {
		t.Fatalf("retention crossed deletion domains: %v %v", fresh, e)
	}
	request.OperationId = "stale-settings"
	if _, err = c.UpdateSettings(ctx, request); err == nil || !strings.Contains(err.Error(), "settings_conflict") {
		t.Fatalf("CAS not preserved: %v", err)
	}
}

func TestDiagnosticRetentionSurvivesFlowMetadataExpiry(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	// Prepare an 18-hour diagnostic under supported 24-hour retention.
	snap0, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: 24}, ExpectedRevision: snap0.Settings.Revision, FieldMask: []string{"log_retention_hours"}, OperationId: "pre-diagnostic", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	closed := now.Add(-18 * time.Hour)
	f := config.FlowFact{Key: "expiry/1/a", StartedAt: closed.Add(-time.Minute), ClosedAt: &closed, Direction: "direct", RouteID: "direct"}
	p.feed(config.ObservationBatch{Session: "expiry", Records: []config.Observation{{Session: "expiry", Sequence: 1, At: closed, Kind: "diagnostic", Level: "info", Raw: "connection: old diagnostic", Flow: &f}}})
	if err = c.RecordEvent(ctx, config.HistoryEvent{ID: "recent-event", At: now, Title: "当前可见事件", Level: "info", Flow: &f}); err != nil {
		t.Fatal(err)
	}
	save := func(hours uint32, id string) {
		t.Helper()
		snap, e := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
		if e != nil {
			t.Fatal(e)
		}
		_, e = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: hours}, FieldMask: []string{"log_retention_hours"}, ExpectedRevision: snap.Settings.Revision, OperationId: id, RequestedAt: timestamppb.Now()})
		if e != nil {
			t.Fatal(e)
		}
	}
	save(24, "keep-diagnostic")
	before, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{EventId: "recent-event"})
	if err != nil || len(before.Raw) != 1 {
		t.Fatalf("expected diagnostic retained within 24h: %v %v", before, err)
	}
	save(1, "prune-raw")
	after, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{EventId: "recent-event"})
	if err != nil || len(after.Raw) != 0 {
		t.Fatalf("diagnostic outlived retention: %v %v", after, err)
	}
}

func TestTodayFilteredTotalsMatchRows(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	now := time.Now().UTC().Truncate(time.Second)
	f := config.FlowFact{Key: "today/1/a", StartedAt: now.Add(-time.Minute), Direction: "direct", RouteID: "direct", Domain: "today.example.test"}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "today", Records: []config.Observation{{Session: "today", Sequence: 1, At: now.Add(-time.Second), Kind: "sample", Flow: &f, Upload: 25, Download: 75}}}); err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Window: "today", Direction: "direct", Search: "today.example.test", Dimension: "domain"})
	if err != nil || page.Upload != 25 || page.Download != 75 || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
}

func TestPaginatedResetRestoresAllActiveFlowsOnce(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	now := time.Now().Truncate(5 * time.Second).Add(-5 * time.Second)
	for start := 0; start < 600; start += 256 {
		end := min(600, start+256)
		batch := config.ObservationBatch{Session: "reset", Collecting: true, LostBefore: 500, ResetRequired: start == 0, ResetToken: "frozen", BaselineHighWater: 900, BaselineComplete: end == 600}
		for i := start; i < end; i++ {
			batch.Baseline = append(batch.Baseline, config.FlowFact{Key: fmt.Sprintf("reset/1/%d", i), StartedAt: now, RouteID: "direct", Direction: "direct", Domain: "reset.example.test"})
		}
		if err := FeedHistoryFixture(t, c, ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Window: "minute", Dimension: "domain"})
	if err != nil || page.Connections != 600 {
		t.Fatal(page, err)
	}
	detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.Items[0].Id, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range detail.Items {
		if f.StateUnknown || f.ClosedAt != nil {
			t.Fatal("baseline continuation invalidated previously restored flow", f)
		}
	}
	route, err := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "direct"})
	if err != nil || route.Connections != 600 {
		t.Fatalf("active reset count: %v %v", route, err)
	}
}
