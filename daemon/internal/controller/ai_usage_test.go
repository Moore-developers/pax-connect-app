package controller

import (
	"context"
	"database/sql"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/ipc"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
)

func TestAIUsageRPCsShareRealBucketsAndPaginatedLogs(t *testing.T) {
	ctx := context.Background()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(home, ".claude", "projects", "p")
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	sample, e := os.ReadFile("../../../tests/ai-usage/claude-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "session.jsonl"), sample, 0600); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	store, e := aiusage.Open(ctx, aiusage.Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Close(ctx) }()
	if e = store.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	sockDir, e := os.MkdirTemp(os.Getenv("TMPDIR"), "ai-usage-rpc-")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	listener, e := ipc.ListenUser(filepath.Join(sockDir, "rpc.sock"))
	if e != nil {
		t.Fatal(e)
	}
	server := grpc.NewServer()
	pb.RegisterAIUsageServiceServer(server, NewAIUsageServer(store, nil))
	defer server.Stop()
	go func() { _ = server.Serve(listener) }()
	conn, e := grpc.NewClient("passthrough:///ai-usage", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(sockDir, "rpc.sock"))
	}))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = conn.Close() }()
	service := pb.NewAIUsageServiceClient(conn)
	q := &pb.AIUsageQuery{FromMs: now.Add(-24 * time.Hour).UnixMilli(), ToMs: now.UnixMilli(), Granularity: "hour"}
	buckets, e := service.QueryAIUsageBuckets(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	var total int64
	for _, b := range buckets.Buckets {
		total += b.Totals.Count
	}
	if total != 2 {
		t.Fatalf("duplicate counted: %d", total)
	}
	revision := buckets.State.Revision
	q.KnownRevision = &revision
	unchanged, e := service.QueryAIUsageBuckets(ctx, q)
	if e != nil || !unchanged.NotModified || len(unchanged.Buckets) != 0 {
		t.Fatalf("revision probe: %+v %v", unchanged, e)
	}
	logs, e := service.ListAIUsageLogs(ctx, &pb.AIUsageLogQuery{FromMs: q.FromMs, ToMs: q.ToMs})
	if e != nil {
		t.Fatal(e)
	}
	if logs.All != 2 || logs.Success != 1 || logs.Failed != 1 || logs.Totals.Input != 50230 || len(logs.Requests) != 2 {
		t.Fatalf("logs: %+v", logs)
	}
	if logs.Requests[0].InputPrice != nil {
		t.Fatal("invented pricing")
	}

	zero := int64(0)
	zeroPage, e := service.ListAIUsageLogs(ctx, &pb.AIUsageLogQuery{FromMs: q.FromMs, ToMs: q.ToMs, MaxOfficialCostMicro: &zero})
	var expectedZero int64
	for _, request := range logs.Requests {
		var original int64
		for _, amount := range request.CostsMicro {
			original += amount
		}
		if original == 0 {
			expectedZero++
		}
	}
	if e != nil || zeroPage.All != expectedZero || int64(len(zeroPage.Requests)) != expectedZero {
		t.Fatalf("zero official bound lost across RPC: %+v %v", zeroPage, e)
	}
	negative := int64(-1)
	_, e = service.ListAIUsageLogs(ctx, &pb.AIUsageLogQuery{FromMs: q.FromMs, ToMs: q.ToMs, MinOfficialCostMicro: &negative})
	if status.Code(e) != codes.InvalidArgument {
		t.Fatalf("negative official bound accepted: %v", e)
	}
	analysis, e := service.QueryAIUsageAnalysis(ctx, &pb.AIUsageAnalysisQuery{FromMs: q.FromMs, ToMs: q.ToMs})
	if e != nil || len(analysis.Rows) == 0 {
		t.Fatalf("analysis RPC: %+v %v", analysis, e)
	}
	var input, requests int64
	for _, r := range analysis.Rows {
		input += r.Totals.Input
		requests += r.Totals.Count
	}
	if input != logs.Totals.Input || requests != logs.All {
		t.Fatalf("analysis ledger mismatch: %d %d", input, requests)
	}
	if len(analysis.ContextUsage) == 0 || analysis.ContextUsage[0].Requests != logs.All || len(analysis.ContextUsage[0].Bands) != 3 || analysis.ContextUsage[0].Bands[0].Input != input {
		t.Fatalf("context usage lost across RPC: %+v", analysis.ContextUsage)
	}
	_, e = service.QueryAIUsageAnalysis(ctx, &pb.AIUsageAnalysisQuery{FromMs: q.FromMs, ToMs: q.ToMs, Sessions: []*pb.AIUsageSessionKey{{Cli: "unknown", SessionId: "s"}}})
	if status.Code(e) != codes.InvalidArgument {
		t.Fatalf("unknown CLI accepted: %v", e)
	}
	limits, e := service.QueryAIUsageAnalysis(ctx, &pb.AIUsageAnalysisQuery{FromMs: q.FromMs, ToMs: q.ToMs, LimitsOnly: true})
	if e != nil || limits.Limits == nil || limits.Limits.Summary.Requests != logs.All || len(limits.Limits.Primary) == 0 {
		t.Fatalf("limits RPC %+v %v", limits, e)
	}
	summary := limits.Limits.Summary
	var dailyRequests, dailyPriced, bandPriced int64
	var dailyInput float64
	for _, day := range summary.Days {
		if !day.Available {
			t.Fatalf("retained complete day unexpectedly unavailable: %+v", day)
		}
		dailyRequests += day.Requests
		dailyPriced += day.PricedRequests
		dailyInput += day.Input.Official
	}
	for _, band := range summary.Bands {
		bandPriced += band.PricedRequests
	}
	if len(summary.Bands) != 3 || len(summary.Days) == 0 || dailyRequests != logs.All || dailyPriced != bandPriced || dailyInput != summary.Input.Official || summary.ObservedThroughMs != q.ToMs {
		t.Fatalf("daily/priced limit contract lost across RPC: %+v", summary)
	}
	priorFrom, priorTo := q.FromMs-int64(7*24*time.Hour/time.Millisecond), q.FromMs
	_, e = service.QueryAIUsageAnalysis(ctx, &pb.AIUsageAnalysisQuery{FromMs: q.FromMs, ToMs: q.ToMs, LimitsOnly: true, ComparisonFromMs: &priorFrom})
	if status.Code(e) != codes.InvalidArgument {
		t.Fatal("unpaired comparison period accepted", e)
	}
	_, e = service.QueryAIUsageAnalysis(ctx, &pb.AIUsageAnalysisQuery{FromMs: q.FromMs, ToMs: q.ToMs, LimitsOnly: true, ComparisonFromMs: &priorFrom, ComparisonToMs: &priorTo})
	if e != nil {
		t.Fatal("complete comparison period rejected", e)
	}
	selected, e := service.QueryAIUsageAnalysis(ctx, &pb.AIUsageAnalysisQuery{FromMs: q.FromMs, ToMs: q.ToMs, LimitsOnly: true, Sessions: []*pb.AIUsageSessionKey{{Cli: limits.Limits.Primary[0].Cli, SessionId: limits.Limits.Primary[0].SessionId}}})
	if e != nil || selected.Limits == nil || selected.Limits.Trend == nil {
		t.Fatalf("limits trend RPC %+v %v", selected, e)
	}
	var claudeLast int64
	for _, l := range buckets.LastUsed {
		if l.Cli == "claude" {
			claudeLast = l.AtMs
		}
	}
	if claudeLast == 0 || len(buckets.LastUsed) == 0 {
		t.Fatalf("last used missing: %+v", buckets.LastUsed)
	}
	lifetime, e := service.SummarizeAIUsage(ctx, &pb.AIUsageSummaryQuery{})
	if e != nil || len(lifetime.Buckets) != 2 {
		t.Fatalf("lifetime: %+v %v", lifetime, e)
	}
	// A leap-year query must preserve the same ledger through every date-dependent consumer.
	from := now.Add(-366 * 24 * time.Hour).UnixMilli()
	year, e := service.QueryAIUsageBuckets(ctx, &pb.AIUsageQuery{FromMs: from, ToMs: q.ToMs, Granularity: "day"})
	if e != nil || len(year.Buckets) == 0 {
		t.Fatalf("year buckets: %+v %v", year, e)
	}
	var yearlyInput int64
	for _, b := range year.Buckets {
		yearlyInput += b.Totals.Input
	}
	if yearlyInput != logs.Totals.Input {
		t.Fatalf("year input mismatch: %d != %d", yearlyInput, logs.Totals.Input)
	}
	yearLogs, e := service.ListAIUsageLogs(ctx, &pb.AIUsageLogQuery{FromMs: from, ToMs: q.ToMs})
	if e != nil || yearLogs.Totals.Input != yearlyInput || yearLogs.All != logs.All {
		t.Fatalf("year logs mismatch: %+v %v", yearLogs, e)
	}
	for _, limitsOnly := range []bool{false, true} {
		got, err := service.QueryAIUsageAnalysis(ctx, &pb.AIUsageAnalysisQuery{FromMs: from, ToMs: q.ToMs, LimitsOnly: limitsOnly})
		if err != nil || (limitsOnly && got.Limits.Summary.Requests != logs.All) || (!limitsOnly && len(got.Rows) != len(analysis.Rows)) {
			t.Fatalf("year analysis (limits=%t): %+v %v", limitsOnly, got, err)
		}
	}
	to := q.FromMs + (368 * 24 * time.Hour).Milliseconds()
	_, e = service.QueryAIUsageBuckets(ctx, &pb.AIUsageQuery{FromMs: q.FromMs, ToMs: to, Granularity: "day"})
	if status.Code(e) != codes.InvalidArgument {
		t.Fatalf("oversized buckets accepted: %v", e)
	}
	_, e = service.ListAIUsageLogs(ctx, &pb.AIUsageLogQuery{FromMs: q.FromMs, ToMs: to})
	if status.Code(e) != codes.InvalidArgument {
		t.Fatalf("oversized logs accepted: %v", e)
	}
	for _, limitsOnly := range []bool{false, true} {
		_, e = service.QueryAIUsageAnalysis(ctx, &pb.AIUsageAnalysisQuery{FromMs: q.FromMs, ToMs: to, LimitsOnly: limitsOnly})
		if status.Code(e) != codes.InvalidArgument {
			t.Fatalf("oversized analysis accepted: %v", e)
		}
	}
}

func TestAIUsageRPCsReportRebuildRequiredForAnOldSchema(t *testing.T) {
	ctx := context.Background()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "ai_usage.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE requests(cli TEXT); PRAGMA user_version=4"); e != nil {
		t.Fatal(e)
	}
	_ = db.Close()
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	store, e := aiusage.Open(ctx, aiusage.Options{Directory: dir, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatalf("an old schema must not stop the daemon: %v", e)
	}
	defer func() { _ = store.Close(ctx) }()
	server := NewAIUsageServer(store, nil)
	now := time.Now()
	b, e := server.QueryAIUsageBuckets(ctx, &pb.AIUsageQuery{FromMs: now.Add(-time.Hour).UnixMilli(), ToMs: now.UnixMilli(), Granularity: "hour"})
	if e != nil || b.State.ErrorCode != "ai_usage_rebuild_required" || len(b.Buckets) != 0 {
		t.Fatalf("buckets: %+v %v", b, e)
	}
	l, e := server.ListAIUsageLogs(ctx, &pb.AIUsageLogQuery{FromMs: now.Add(-time.Hour).UnixMilli(), ToMs: now.UnixMilli()})
	if e != nil || l.State.ErrorCode != "ai_usage_rebuild_required" || l.All != 0 {
		t.Fatalf("logs: %+v %v", l, e)
	}
}
