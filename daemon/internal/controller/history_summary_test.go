package controller

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/types/known/timestamppb"
	"math"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"runtime"
	"sort"
	"testing"
	"time"
)

func TestActivityConnectionFilterDoesNotReturnUnrelatedRecords(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	start := time.Now().Add(-time.Minute)
	for i, key := range []string{"s/1/wanted", "s/1/unrelated"} {
		f := config.FlowFact{Key: key, Domain: "example.test", StartedAt: start, Direction: "direct"}
		seq := uint64(i + 1)
		p.observe(config.ObservationBatch{Session: "s", HighWater: seq, Collecting: true,
			Records: []config.Observation{{Session: "s", Sequence: seq, Kind: "started", At: start, Flow: &f}}})
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ConnectionKey: "s/1/wanted"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || page.Records[0].Flow.Key != "s/1/wanted" {
		t.Fatalf("unrelated connections leaked into linked logs: total=%d records=%d", page.Total, len(page.Records))
	}
}

func TestLogSummaryKeepsCumulativeBytesAndFrozenDuration(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	start := time.Now().Add(-10 * time.Second)
	closed := start.Add(5 * time.Second)
	fact := config.FlowFact{Key: "s/1/c", Domain: "example.test", ApplicationID: "app", StartedAt: start, Direction: "direct", RouteID: "direct", EstablishmentState: "ready"}
	batch := config.ObservationBatch{Session: "s", HighWater: 2, Collecting: true, Records: []config.Observation{{Session: "s", Sequence: 1, At: start, Kind: "started", Flow: &fact}, {Session: "s", Sequence: 2, At: start.Add(time.Second), Kind: "sample", Flow: &fact, Upload: 12000, Download: 240000}}}
	p.observe(batch)
	first, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || len(first.Records) != 1 {
		t.Fatalf("missing frozen records: count=%d err=%v", len(first.GetRecords()), err)
	}
	m := first.Records[0].Metrics
	if m.Upload != 12000 || m.Download != 240000 || m.BytesState != "complete" {
		t.Fatal(m)
	}
	p.observe(batch)
	fact.ClosedAt = &closed
	p.observe(config.ObservationBatch{Session: "s", HighWater: 3, Records: []config.Observation{{Session: "s", Sequence: 3, At: closed, Kind: "closed", Flow: &fact}}})
	frozen, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ReadToken: first.ReadToken})
	if err != nil || frozen.Records[0].Metrics.DurationState != "active" {
		t.Fatal(frozen, err)
	}
	latest, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || latest.Records[0].Metrics.DurationMs != 5000 || latest.Records[0].Metrics.DurationState != "closed" || latest.Records[0].Metrics.Upload != 12000 {
		t.Fatal(latest, err)
	}
	detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: latest.Records[0].RecordId, Query: &pb.LogQuery{View: "activity", ReadToken: latest.ReadToken}})
	if err != nil || detail.Record.Metrics.Upload != 12000 {
		t.Fatal(detail, err)
	}
}

func TestLogSummaryGapsClearAndHistoricalCutoffStayHonest(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	start := time.Now().Add(-time.Minute)
	fact := config.FlowFact{Key: "s/1/c", Domain: "example.test", StartedAt: start, EstablishmentState: "ready"}
	record := func(seq uint64, kind string, at time.Time, up uint64) {
		t.Helper()
		p.observe(config.ObservationBatch{Session: "s", HighWater: seq, Collecting: true, Records: []config.Observation{{Session: "s", Sequence: seq, Kind: kind, At: at, Flow: &fact, Upload: up}}})
	}
	record(1, "started", start, 0)
	record(2, "sample", start.Add(time.Second), 10)
	record(4, "sample", start.Add(3*time.Second), 20)
	record(5, "ready", start.Add(4*time.Second), 0)
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Records[0].Metrics.BytesState != "partial" || page.Records[0].Metrics.Upload != 30 {
		t.Fatal(page)
	}
	historical, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Until: timestamppb.New(start.Add(2 * time.Second))})
	if err != nil {
		t.Fatal(err)
	}
	if historical.Records[0].Metrics.Upload > 10 {
		t.Fatal("future byte leakage", historical)
	}
	if _, err = c.ClearLogs(ctx, &pb.ClearLogsRequest{OperationId: "clear-summary", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	record(6, "sample", time.Now(), 100)
	record(7, "closed", time.Now(), 0)
	after, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	var connection *pb.LogRecord
	for _, row := range after.Records {
		if row.Flow != nil {
			connection = row
		}
	}
	if connection == nil || connection.Metrics.Upload != 0 || connection.Metrics.BytesState != "partial" {
		t.Fatal("clear revived old counters", after)
	}
	if _, err = c.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: page.Records[0].RecordId, Query: &pb.LogQuery{View: "activity", ReadToken: page.ReadToken}}); err == nil {
		t.Fatal("deleted snapshot survived")
	}
}
func TestLogSummarySurvivesRestartAndDoesNotDoubleCount(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, p := newLogsTestController(t, dir)
	now := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "s/1/closed", Domain: "example.test", StartedAt: now}
	batch := config.ObservationBatch{Session: "s", HighWater: 2, Records: []config.Observation{{Session: "s", Sequence: 1, At: now, Kind: "started", Flow: &f}, {Session: "s", Sequence: 2, At: now.Add(time.Second), Kind: "sample", Flow: &f, Upload: 55}}}
	p.observe(batch)
	stopLogsTestController(t, c)
	c, p = newLogsTestController(t, dir)
	p.observe(batch)
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || page.Records[0].Metrics.Upload != 55 || page.Records[0].Metrics.BytesState != "complete" {
		t.Fatal(page, err)
	}
}

func TestLogSummaryDirectorySortsWholeRangeByRecordCount(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, p := newLogsTestController(t, dir)
	now := time.Now().Add(-time.Minute)
	batch := config.ObservationBatch{Session: "sorted", Collecting: true}
	for i := 0; i < 150; i++ {
		domain := fmt.Sprintf("domain-%03d.test", i)
		if i >= 130 {
			domain = "popular.test"
		}
		fact := config.FlowFact{Key: fmt.Sprintf("sorted/1/%d", i), Domain: domain, ApplicationID: domain, Application: domain, IP: fmt.Sprintf("2001:db8::%x", i+1000), StartedAt: now}
		if i >= 130 {
			fact.IP = "2001:db8::ffff"
		}
		batch.HighWater++
		batch.Records = append(batch.Records, config.Observation{Session: batch.Session, Sequence: batch.HighWater, At: now, Kind: "started", Flow: &fact})
	}
	p.observe(batch)
	for _, dimension := range []string{"domain", "application", "ip"} {
		popular, next := "popular.test", "domain-099.test"
		if dimension == "ip" {
			popular, next = "2001:db8::ffff", "2001:db8::44b"
		}
		q := &pb.LogQuery{View: "activity", Dimension: dimension, Limit: 100}
		first, err := c.QueryLogObjects(ctx, q)
		if err != nil || len(first.Items) != 100 || first.Items[0].Id != popular || first.Items[0].Count != 20 || first.TotalEvents != 150 {
			t.Fatal(first, err)
		}
		q.ReadToken = first.ReadToken
		q.Cursor = first.NextCursor
		second, err := c.QueryLogObjects(ctx, q)
		if err != nil || len(second.Items) != 31 || second.Items[0].Id != next {
			t.Fatal(second, err)
		}
		q.Cursor = ""
		q.Level = "error"
		filtered, err := c.QueryLogObjects(ctx, q)
		if err != nil || filtered.Items[0].Count != 20 {
			t.Fatal(filtered, err)
		}
	}
}

func TestLogSummaryOldCutoffAndRetentionDoNotInventBytes(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	var err error
	// The default log retention is 24 hours; use it to admit an 18-hour fixture.
	snapshot, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: 24}, ExpectedRevision: snapshot.Settings.Revision, FieldMask: []string{"log_retention_hours"}, OperationId: "pre-retain-24", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-18 * time.Hour)
	closed := start.Add(time.Minute)
	f := config.FlowFact{Key: "old/1/c", StartedAt: start, Domain: "old.test", ClosedAt: nil, EstablishmentState: "ready"}
	records := []config.Observation{{Session: "old", Sequence: 1, At: start, Kind: "started", Flow: &f}, {Session: "old", Sequence: 2, At: start.Add(time.Second), Kind: "sample", Flow: &f, Download: 500}}
	p.observe(config.ObservationBatch{Session: "old", HighWater: 2, Records: records})
	f.ClosedAt = &closed
	p.observe(config.ObservationBatch{Session: "old", HighWater: 3, Records: []config.Observation{{Session: "old", Sequence: 3, At: closed, Kind: "closed", Flow: &f}}})
	snapshot, err = c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: 24}, ExpectedRevision: snapshot.Settings.Revision, FieldMask: []string{"log_retention_hours"}, OperationId: "retain-24", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	q := &pb.LogQuery{View: "activity", From: timestamppb.New(start.Add(-time.Second))}
	page, err := c.QueryLogs(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var connection *pb.LogRecord
	for _, row := range page.Records {
		if row.Flow != nil {
			connection = row
		}
	}
	if connection == nil || connection.Metrics.Download != 500 || connection.Metrics.BytesState != "complete" || connection.Metrics.DurationMs != 60000 {
		t.Fatal("24h eviction erased cumulative log metrics", page)
	}
	old, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", From: q.From, Until: timestamppb.New(start.Add(500 * time.Millisecond))})
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Records) != 1 || old.Records[0].Metrics.Download != 0 || old.Records[0].Metrics.BytesState != "partial" {
		t.Fatal("historical cutoff invented exact metrics", old)
	}
}

func TestLogSummaryRollbackAdmissionInvalidatesOldCompleteness(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, p := newLogsTestController(t, dir)
	now := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "rollback/1/c", StartedAt: now, Domain: "example.test"}
	p.observe(config.ObservationBatch{Session: "rollback", HighWater: 2, Records: []config.Observation{{Session: "rollback", Sequence: 1, At: now, Kind: "started", Flow: &f}, {Session: "rollback", Sequence: 2, At: now.Add(time.Second), Kind: "sample", Flow: &f, Upload: 55}}})
	first, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	stopLogsTestController(t, c)
	// An older writer cannot preserve the fields its schema does not know: the summary
	// generation and the event checkpoints disappear while the summaries stay behind.
	stripLogEventCheckpoints(t, filepath.Join(dir, "history", "logs.db"))
	c, _ = newLogsTestController(t, dir)
	next, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	if next.History.SummaryGeneration == first.History.SummaryGeneration || next.Records[0].Metrics.BytesState == "complete" {
		t.Fatal("rollback reused complete counters", next)
	}
}

// Opt-in measurement at the public query seam; no private browsing records.
func TestLogSummaryQueryBudget(t *testing.T) {
	if os.Getenv("PAX_SUMMARY_BENCH") != "1" {
		t.Skip("explicit controlled query measurement")
	}
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, p := newLogsTestController(t, dir)
	now := time.Now().Add(-time.Minute)
	closed := now.Add(5 * time.Second)
	batch := config.ObservationBatch{Session: "budget", Collecting: true}
	for i := 0; i < 2000; i++ {
		fact := config.FlowFact{Key: fmt.Sprintf("budget/1/%d", i), StartedAt: now, Domain: fmt.Sprintf("target-%d.test", i), ApplicationID: "fixture", Application: "受控应用", EstablishmentState: "ready"}
		for _, kind := range []string{"started", "sample", "closed"} {
			f := fact
			at := now
			if kind == "sample" {
				at = now.Add(time.Second)
			}
			if kind == "closed" {
				f.ClosedAt = &closed
				at = closed
			}
			batch.HighWater++
			o := config.Observation{Session: batch.Session, Sequence: batch.HighWater, At: at, Kind: kind, Flow: &f}
			if kind == "sample" {
				o.Upload = 12000
				o.Download = 240000
			}
			batch.Records = append(batch.Records, o)
		}
	}
	p.observe(batch)
	runtime.GC()
	var before, after, retained runtime.MemStats
	runtime.ReadMemStats(&before)
	begin := time.Now()
	objects, err := c.QueryLogObjects(ctx, &pb.LogQuery{View: "activity", Dimension: "domain"})
	cold := time.Since(begin)
	if err != nil {
		t.Fatal(err)
	}
	begin = time.Now()
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ReadToken: objects.ReadToken, Dimension: "domain"})
	first := time.Since(begin)
	if err != nil || page.Total != 2000 {
		t.Fatal(page.GetTotal(), err)
	}
	begin = time.Now()
	_, err = c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ReadToken: page.ReadToken, Cursor: page.NextCursor, Dimension: "domain"})
	second := time.Since(begin)
	if err != nil {
		t.Fatal(err)
	}
	times := make([]time.Duration, 0, 100)
	for i := 0; i < 100; i++ {
		begin = time.Now()
		detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: page.Records[i].RecordId, Query: &pb.LogQuery{View: "activity", Dimension: "domain", ReadToken: page.ReadToken}})
		if err != nil || detail.Record.Metrics.Upload != 12000 {
			t.Fatal(err)
		}
		times = append(times, time.Since(begin))
	}
	runtime.ReadMemStats(&after)
	runtime.GC()
	runtime.ReadMemStats(&retained)
	t.Logf("query-total-allocated=%d post-GC-heap=%d bytes (whole process, not per-token retained size)", after.TotalAlloc-before.TotalAlloc, retained.HeapAlloc)
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("2000 connections/6000 observations cold-directory=%s first-page=%s second-page=%s detail-p95=%s; one read token reused", cold, first, second, times[94])
	if cold > 2*time.Second || first > 2*time.Second || second > 2*time.Second {
		t.Fatal("query budget exceeded")
	}
	// Reopen a legacy-shaped database: no event checkpoints and no summary generation.
	stopLogsTestController(t, c)
	stripLogEventCheckpoints(t, filepath.Join(dir, "history", "logs.db"))
	begin = time.Now()
	c, _ = newLogsTestController(t, dir)
	t.Logf("2000-connection legacy-format initialization=%s", time.Since(begin))
	migrated, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || migrated.GetTotal() != 2000 || migrated.Records[0].Metrics.BytesState == "complete" {
		t.Fatal("legacy initialization invented complete counters", migrated.GetTotal(), err)
	}
}

func TestLogSummaryRejectsCrossViewTokensAndKeepsLastObservedEnd(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	start := time.Now().Add(-time.Hour)
	last := start.Add(5 * time.Second)
	f := config.FlowFact{Key: "old/1/c", StartedAt: start, Domain: "example.test", EstablishmentState: "ready"}
	p.observe(config.ObservationBatch{Session: "old", HighWater: 2, Collecting: true, Records: []config.Observation{{Session: "old", Sequence: 1, At: start, Kind: "started", Flow: &f}, {Session: "old", Sequence: 2, At: last, Kind: "sample", Flow: &f, Upload: 5}}})
	token, err := c.QueryLogObjects(ctx, &pb.LogQuery{View: "access"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.QueryLogs(ctx, &pb.LogQuery{View: "activity", ReadToken: token.ReadToken}); err == nil {
		t.Fatal("cross-view token was accepted")
	}
	p.observe(config.ObservationBatch{Session: "new", Collecting: true, ResetRequired: true})
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || page.Records[0].Metrics.DurationState != "observed" || page.Records[0].Metrics.DurationMs != 5000 {
		t.Fatal("gap extended the last known duration", page, err)
	}
}

func TestLogSummaryZeroUnknownOverflowAndClockRollback(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	start := time.Now().Add(-time.Minute)
	negativeEnd := start.Add(-time.Second)
	zero := config.FlowFact{Key: "edge/1/zero", Domain: "zero.test", StartedAt: start, ClosedAt: &start}
	negative := config.FlowFact{Key: "edge/1/negative", Domain: "negative.test", StartedAt: start, ClosedAt: &negativeEnd}
	missing := config.FlowFact{Key: "edge/1/missing", Domain: "missing.test"}
	overflow := config.FlowFact{Key: "edge/1/overflow", Domain: "overflow.test", StartedAt: start}
	batch := config.ObservationBatch{Session: "edge", Collecting: true}
	add := func(kind string, fact *config.FlowFact, upload uint64) {
		batch.HighWater++
		batch.Records = append(batch.Records, config.Observation{Session: batch.Session, Sequence: batch.HighWater, Kind: kind, At: start.Add(time.Duration(batch.HighWater) * time.Second), Flow: fact, Upload: upload})
	}
	add("started", &zero, 0)
	add("closed", &zero, 0)
	add("started", &negative, 0)
	add("closed", &negative, 0)
	add("ready", &missing, 0)
	add("started", &overflow, 0)
	add("sample", &overflow, math.MaxUint64)
	add("sample", &overflow, 1)
	p.observe(batch)
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	metrics := map[string]*pb.ConnectionMetrics{}
	for _, record := range page.Records {
		if record.Flow != nil {
			metrics[record.Flow.Key] = record.Metrics
		}
	}
	if len(metrics) != 4 {
		t.Fatal("missing edge records", page)
	}
	if m := metrics[zero.Key]; m.BytesState != "complete" || m.Upload != 0 || m.DurationState != "closed" || m.DurationMs != 0 {
		t.Fatal("known zero became unknown", m)
	}
	if m := metrics[missing.Key]; m.BytesState != "unknown" || m.DurationState != "unknown" {
		t.Fatal("missing observations invented zero", m)
	}
	if m := metrics[negative.Key]; m.DurationState != "unknown" || m.DurationMs != 0 {
		t.Fatal("clock rollback invented duration", m)
	}
	if m := metrics[overflow.Key]; m.BytesState != "partial" || m.Upload != math.MaxUint64 || m.Reason != "counter_overflow" {
		t.Fatal("counter wrapped or pretended complete", m)
	}
}

func TestLogDirectoriesOnlyExposeObjectsOfTheirDimension(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	at := time.Now().Add(-time.Minute)
	facts := []config.FlowFact{
		{Key: "dims/1/a", Domain: "example.test", IP: "192.0.2.1", ApplicationID: "com.example.Editor", Application: "Editor", StartedAt: at},
		{Key: "dims/1/b", Domain: "192.0.2.2", IP: "not-an-ip", Application: "未识别应用", StartedAt: at},
		{Key: "dims/1/c", Domain: "ChatGPT → target", StartedAt: at},
	}
	batch := config.ObservationBatch{Session: "dims", Collecting: true}
	for n := range facts {
		batch.HighWater++
		batch.Records = append(batch.Records, config.Observation{Session: "dims", Sequence: batch.HighWater, Kind: "started", At: at, Flow: &facts[n]})
	}
	p.observe(batch)
	if err := c.RecordEvent(ctx, config.HistoryEvent{ID: "dims-operation", Kind: "operation", At: at, Source: "Pax", Title: "线路切换完成", Level: "info"}); err != nil {
		t.Fatal(err)
	}
	for dimension, want := range map[string]string{"domain": "example.test", "application": "com.example.Editor", "ip": "192.0.2.1"} {
		q := &pb.LogQuery{View: "activity", Dimension: dimension}
		objects, err := c.QueryLogObjects(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(objects.Items) != 1 || objects.Items[0].Id != want {
			t.Fatalf("%s directory mixed missing or wrong object types: %v", dimension, objects.Items)
		}
		page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Dimension: dimension, ReadToken: objects.ReadToken})
		if err != nil || page.Total != 4 {
			t.Fatal("all records lost diagnostics or operations", page, err)
		}
		q.ReadToken = objects.ReadToken
		q.ObjectId = want
		selected, err := c.QueryLogs(ctx, q)
		if err != nil || selected.Total != 1 {
			t.Fatal("object filter includes unrelated records", selected, err)
		}
	}
}
