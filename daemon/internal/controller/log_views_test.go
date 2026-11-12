package controller

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestLogViewsAggregateBeforePagingAndLinkProblems(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var err error
	for i := 0; i < 110; i++ {
		f := config.FlowFact{Key: fmt.Sprintf("core/1/%d", i), ApplicationID: "app", Application: "浏览器", Domain: "example.test", IP: "192.0.2.1", Port: 8443, Transport: "tcp", RouteID: "route", RouteName: "香港线路", Direction: "proxy", StartedAt: time.Now().Add(-time.Minute), EstablishmentState: "not_measurable"}
		for j, kind := range []string{"started", "failed", "closed"} {
			level := "info"
			if kind == "failed" {
				level = "error"
				f.Failure = "connection_failed"
				f.EstablishmentState = "failed"
			}
			if kind == "closed" {
				at := time.Now()
				f.ClosedAt = &at
			}
			copy := f
			if err = c.RecordEvent(ctx, config.HistoryEvent{ID: fmt.Sprintf("%d/%d", i, j), At: time.Now(), Kind: kind, Title: kind, Level: level, Source: "sing-box", Flow: &copy, Result: f.Failure}); err != nil {
				t.Fatal(err)
			}
		}
	}
	accesses, err := c.QueryLogs(ctx, &pb.LogQuery{View: "access", Limit: 100})
	if err != nil || accesses.Total != 110 || len(accesses.Items) != 100 {
		t.Fatalf("access count must count connections: %v %v", accesses, err)
	}
	if accesses.Items[0].Flow.Port != 8443 || accesses.Items[0].Flow.Failure != "connection_failed" {
		t.Fatal(accesses.Items[0])
	}
	problems, err := c.QueryLogs(ctx, &pb.LogQuery{View: "problems"})
	if err != nil || problems.Total != 1 || problems.Items[0].RelatedConnections != 110 || problems.Items[0].Occurrences != 110 {
		t.Fatalf("problem aggregation: %v %v", problems, err)
	}
	related, err := c.QueryLogs(ctx, &pb.LogQuery{View: "access", ProblemId: problems.Items[0].ProblemId})
	if err != nil || related.Total != 110 {
		t.Fatal(related, err)
	}
	objects, err := c.QueryLogObjects(ctx, &pb.LogQuery{View: "access", Dimension: "application"})
	if err != nil || objects.TotalEvents != 110 || len(objects.Items) != 1 || objects.Items[0].Count != 110 {
		t.Fatal(objects, err)
	}
}

func TestLogViewsSnapshotDetailsAndIncrementalUseSameConnection(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var err error
	f := config.FlowFact{Key: "core/1/stable", Domain: "example.test", Port: 8080, StartedAt: time.Now().Add(-time.Second), Direction: "direct", RouteID: "direct", EstablishmentState: "not_measurable"}
	record := func(id, kind, level string) {
		t.Helper()
		copy := f
		if err := c.RecordEvent(ctx, config.HistoryEvent{ID: id, At: time.Now(), Kind: kind, Title: kind, Level: level, Source: "sing-box", Flow: &copy}); err != nil {
			t.Fatal(err)
		}
	}
	record("start", "started", "info")
	filter := &pb.LogQuery{View: "access"}
	first, err := c.QueryLogs(ctx, filter)
	if err != nil || len(first.Items) != 1 {
		t.Fatal(err)
	}
	f.EstablishmentState = "ready"
	f.EstablishmentMS = 38
	record("ready", "ready", "info")
	update, err := c.QueryLogUpdates(ctx, &pb.LogUpdateRequest{Filter: filter, AfterSequence: first.HeadSequence, DeletionGeneration: first.History.LogDeletionGeneration, JournalGeneration: first.History.JournalGeneration})
	if err != nil || len(update.Items) != 1 || update.Items[0].RecordId != first.Items[0].RecordId || update.Items[0].Flow.EstablishmentMs != 38 || update.Items[0].Occurrences != 2 {
		t.Fatalf("incremental connection state: %+v %v", update, err)
	}
	filter.ReadToken = first.ReadToken
	detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{Query: filter, RecordId: first.Items[0].RecordId})
	if err != nil || len(detail.Timeline) != 1 || detail.Flow.EstablishmentState != "not_measurable" {
		t.Fatal(detail, err)
	}
	if _, err = c.ClearLogs(ctx, &pb.ClearLogsRequest{OperationId: "clear-views", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetLogDetail(ctx, &pb.LogDetailRequest{Query: filter, RecordId: first.Items[0].RecordId}); err == nil {
		t.Fatal("deleted grouped detail readable")
	}
}

func TestLogViewsKeepBlockingWarningsAndOperationsDistinct(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var err error
	events := []config.HistoryEvent{
		{ID: "blocked", Kind: "rejected", Title: "连接已阻断", Level: "info", Source: "sing-box", Flow: &config.FlowFact{Key: "blocked", Domain: "ads.test", Port: 53, Direction: "blocked", Failure: "rule_rejected", Rule: "广告拦截"}},
		{ID: "warning", Kind: "diagnostic", Title: "内核诊断", Level: "warning", Source: "sing-box", Raw: []string{"dns: exchange failed"}},
		{ID: "check", Kind: "current_path", Title: "暂未确认连通", Level: "info", Source: "Pax", Result: "inconclusive"},
		{ID: "operation", Kind: "operation", Title: "切线完成", Level: "info", Source: "Pax"},
	}
	for _, event := range events {
		event.At = time.Now()
		if err = c.RecordEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	problems, err := c.QueryLogs(ctx, &pb.LogQuery{View: "problems"})
	if err != nil || problems.Total != 3 {
		t.Fatalf("wanted blocking, warning, unconfirmed groups: total=%d err=%v", problems.GetTotal(), err)
	}
	categories := map[string]bool{}
	for _, row := range problems.Items {
		categories[row.Category] = true
	}
	if !categories["blocked"] || !categories["warning"] || !categories["unconfirmed"] {
		t.Fatal(categories)
	}
	ops, err := c.QueryLogs(ctx, &pb.LogQuery{View: "operations"})
	if err != nil || ops.Total != 2 {
		t.Fatal(ops, err)
	}
	accesses, err := c.QueryLogs(ctx, &pb.LogQuery{View: "access"})
	if err != nil || accesses.Total != 1 || accesses.Items[0].Flow.Port != 53 {
		t.Fatal(accesses, err)
	}
}

func TestUnifiedActivityIncludesConnectionFailuresAndPaxOperations(t *testing.T) {
	ctx := context.Background()
	c, _ := newLogsTestController(t, newLogsTestDir(t))
	var err error
	events := []config.HistoryEvent{
		{ID: "start", Kind: "started", Level: "info", Source: "sing-box", Flow: &config.FlowFact{Key: "one", Domain: "good.test", EstablishmentState: "not_measurable"}},
		{ID: "ready", Kind: "ready", Level: "info", Source: "sing-box", Flow: &config.FlowFact{Key: "one", Domain: "good.test", EstablishmentState: "ready", EstablishmentMS: 38}},
		{ID: "dns", Kind: "failed", Level: "error", Source: "sing-box", Flow: &config.FlowFact{Key: "two", Domain: "bad.test", Failure: "dns_resolution_failed", EstablishmentState: "failed"}},
		{ID: "op", Kind: "operation", Title: "线路切换完成", Level: "info", Source: "Pax"},
		{ID: "warn", Kind: "diagnostic", Title: "内核诊断", Level: "warning", Source: "sing-box"},
	}
	for _, event := range events {
		event.At = time.Now()
		if err = c.RecordEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	page, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity"})
	if err != nil || page.Total != 4 {
		t.Fatalf("unified total=%d err=%v", page.GetTotal(), err)
	}
	states := map[string]bool{}
	for _, row := range page.Records {
		states[row.Title] = true
	}
	for _, want := range []string{"建立连接：通道已建立", "域名解析：解析失败", "线路切换：已完成", "诊断：内核诊断警告"} {
		if !states[want] {
			t.Fatalf("missing status %s", want)
		}
	}
	q := &pb.LogQuery{View: "activity", ReadToken: page.ReadToken}
	for _, row := range page.Records {
		detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{Query: q, RecordId: row.RecordId})
		if err != nil || detail.TotalEvents == 0 {
			t.Fatal("unified detail", row.RecordId, err)
		}
	}
	if err = c.RecordEvent(ctx, config.HistoryEvent{ID: "op2", At: time.Now(), Kind: "operation", Title: "代理已关闭", Level: "info", Source: "Pax"}); err != nil {
		t.Fatal(err)
	}
	update, err := c.QueryLogUpdates(ctx, &pb.LogUpdateRequest{Filter: &pb.LogQuery{View: "activity"}, AfterSequence: page.HeadSequence, DeletionGeneration: page.History.LogDeletionGeneration, JournalGeneration: page.History.JournalGeneration})
	if err != nil || len(update.Items) != 1 || update.Items[0].Message != "代理已关闭" {
		t.Fatal("Pax operation missing from unified delta", update, err)
	}
}

func TestUnifiedDetailRespectsFrozenHistoricalUntil(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	var err error
	now := time.Now()
	until := now.Add(-time.Minute)
	flow := config.FlowFact{Key: "core/1/long", Domain: "example.test", StartedAt: now.Add(-3 * time.Minute), EstablishmentState: "ready"}
	records := []config.Observation{
		{Session: "core", Sequence: 1, At: now.Add(-2 * time.Minute), Kind: "started", Flow: &flow},
		{Session: "core", Sequence: 2, At: until.Add(-time.Second), Kind: "diagnostic", Flow: &flow, Raw: "connection: before boundary", Level: "info"},
		{Session: "core", Sequence: 3, At: until.Add(time.Second), Kind: "diagnostic", Flow: &flow, Raw: "connection: after boundary", Level: "info"},
	}
	p.feed(config.ObservationBatch{Session: "core", HighWater: 3, Records: records, Collecting: true})
	q := &pb.LogQuery{View: "activity", From: timestamppb.New(now.Add(-5 * time.Minute)), Until: timestamppb.New(until)}
	page, err := c.QueryLogs(ctx, q)
	if err != nil || len(page.Records) != 1 {
		t.Fatal(page, err)
	}
	q.ReadToken = page.ReadToken
	detail, err := c.GetLogDetail(ctx, &pb.LogDetailRequest{Query: q, RecordId: page.Records[0].RecordId})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Raw) != 1 || detail.Raw[0] != "connection: before boundary" {
		t.Fatalf("historical detail escaped until: %v", detail.Raw)
	}
}
