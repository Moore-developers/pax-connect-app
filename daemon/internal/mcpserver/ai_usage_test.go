package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

type aiFixture struct {
	pb.AIUsageServiceClient
	revision uint64
	buckets  *pb.AIUsageQuery
	analysis *pb.AIUsageAnalysisQuery
	longID   bool
}

func (f *aiFixture) ListAIUsageLogs(_ context.Context, q *pb.AIUsageLogQuery, _ ...grpc.CallOption) (*pb.AIUsageLogs, error) {
	if q.ExpectedRevision != nil && (*q.ExpectedRevision != f.revision || *q.ExpectedGeneration != 1) {
		return nil, status.Error(codes.FailedPrecondition, "query_changed")
	}
	out := &pb.AIUsageLogs{All: 12, Success: 12, State: &pb.AIUsageState{Revision: f.revision, Generation: 1}, Totals: &pb.AIUsageTotals{Count: 12, CostMicro: 9007199254740993, UnconfirmedChannel: 12}}
	for i := int(q.Page) * 10; i < min(int(q.Page+1)*10, 12); i++ {
		out.Requests = append(out.Requests, &pb.AIUsageRequest{Id: fmt.Sprint(i), Cli: "codex", Input: 8, Read: 3, Succeeded: true})
	}
	return out, nil
}
func (f *aiFixture) SummarizeAIUsage(context.Context, *pb.AIUsageSummaryQuery, ...grpc.CallOption) (*pb.AIUsageBuckets, error) {
	return &pb.AIUsageBuckets{State: &pb.AIUsageState{Revision: f.revision}, Buckets: []*pb.AIUsageBucket{{Cli: "codex", Totals: &pb.AIUsageTotals{Count: 12}}}}, nil
}
func (f *aiFixture) QueryAIUsageBuckets(_ context.Context, q *pb.AIUsageQuery, _ ...grpc.CallOption) (*pb.AIUsageBuckets, error) {
	f.buckets = q
	return &pb.AIUsageBuckets{State: &pb.AIUsageState{Revision: f.revision}, NotModified: q.KnownRevision != nil && *q.KnownRevision == f.revision, LastUsed: []*pb.AIUsageLastUsed{{Cli: "codex", AtMs: 42}}}, nil
}
func (f *aiFixture) ListAISessions(context.Context, *pb.AISessionCatalogQuery, ...grpc.CallOption) (*pb.AISessionCatalogPage, error) {
	id := "12345678-1234-1234-1234-123456789abc"
	if f.longID {
		id = strings.Repeat("s", 129)
	}
	return &pb.AISessionCatalogPage{Revision: 3, UsageRevision: f.revision, Ready: true, Total: 2, Items: []*pb.AISessionCatalogEntry{
		{Key: &pb.AIUsageSessionKey{Cli: "claude", SessionId: id}, Title: "PRIVATE PROMPT", TitleSource: "claude.last-prompt"},
		{Key: &pb.AIUsageSessionKey{Cli: "codex", SessionId: "empty"}, Title: "Native title", TitleSource: "codex.index"},
	}}, nil
}
func (f *aiFixture) QueryAIUsageAnalysis(_ context.Context, q *pb.AIUsageAnalysisQuery, _ ...grpc.CallOption) (*pb.AIUsageAnalysis, error) {
	f.analysis = q
	state := &pb.AIUsageState{Revision: f.revision, Generation: 1}
	if !q.LimitsOnly {
		return &pb.AIUsageAnalysis{State: state, Truncated: true, ContextUsage: []*pb.AIUsageContextUsage{{Cli: "codex", SubagentsIncomplete: true}}}, nil
	}
	out := &pb.AIUsageAnalysis{State: state, Limits: &pb.AIUsageSessionLimits{Summary: &pb.AIUsageLimitSummary{Input: &pb.AIUsageLimitMoney{ChannelUnknown: true}}}}
	for i := 0; i < 12; i++ {
		out.Limits.Primary = append(out.Limits.Primary, &pb.AIUsageLimitRow{SessionId: fmt.Sprint(i), Title: "PRIVATE PROMPT", Unpriced: 1, Bands: []*pb.AIUsageLimitBand{{Requests: 1}, {Requests: 2}, {Requests: 3}}})
		out.Limits.Trend = &pb.AIUsageLimitTrend{Requests: []*pb.AIUsageLimitRequest{{Request: &pb.AIUsageAnalysisRow{SessionId: "s", SessionTitle: "PRIVATE PROMPT"}}}}
	}
	return out, nil
}

func aiServer(t *testing.T) (*Server, *aiFixture) {
	t.Helper()
	s, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := &aiFixture{revision: 7}
	s.open = func(context.Context) (*backend, error) {
		return &backend{ai: f, hello: &pb.HelloResponse{DaemonInstance: "instance", Timezone: "Asia/Tokyo", Capabilities: []string{"ai.usage.v1", "ai.usage.logs.snapshot.v1", "ai.usage.buckets.timezone.v1", "ai.sessions.catalog.activity.v1", "ai.usage.context.v1", "ai.usage.sessions.v1", "ai.usage.limits.v1"}}}, nil
	}
	return s, f
}

func TestAIReadToolsSchemaPrivacyAndUnknowns(t *testing.T) {
	s, f := aiServer(t)
	client := sdkClient(t, s)
	for _, name := range aiTools {
		result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
		raw, _ := json.Marshal(result.StructuredContent)
		if strings.Contains(string(raw), "PRIVATE PROMPT") {
			t.Fatalf("%s exposed message-derived title", name)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		data := out["data"].(map[string]any)
		if name == "pax_ai_logs" {
			request := data["requests"].([]any)[0].(map[string]any)
			if _, ok := request["tps"]; ok {
				t.Fatal("unknown TPS became zero")
			}
			if data["totals"].(map[string]any)["costMicro"] != "9007199254740993" || out["availability"] != "partial" {
				t.Fatalf("lost precise money or channel warning: %v", out)
			}
		}
		if name == "pax_ai_sessions" {
			items := data["items"].([]any)
			if len(items) != 2 || items[0].(map[string]any)["title_redacted"] != true || items[1].(map[string]any)["title"] != "Native title" {
				t.Fatal("catalog identity/redaction/empty session lost", data)
			}
		}
	}
	zone, _ := time.LoadLocation("Asia/Tokyo")
	at := time.UnixMilli(f.buckets.FromMs).In(zone)
	if f.buckets.Timezone != "Asia/Tokyo" || at.Hour() != 0 || at.Minute() != 0 {
		t.Fatal("default is not daemon calendar today", f.buckets)
	}
	f.longID = true
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_ai_sessions", Arguments: map[string]any{}})
	if err != nil || !result.IsError || result.StructuredContent.(map[string]any)["code"] != "session_id_too_long" {
		t.Fatal("long session ID silently truncated", result, err)
	}
}

func TestAILogContinuationsValidateCachedRemainderAndOffset(t *testing.T) {
	s, f := aiServer(t)
	args := map[string]any{"limit": float64(2)}
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		out, err := s.call(context.Background(), "pax_ai_logs", args)
		if err != nil {
			t.Fatal(err)
		}
		data := out["data"].(map[string]any)
		if data["all"] != "12" {
			t.Fatal("total became page subtotal")
		}
		for _, v := range data["requests"].([]any) {
			id := v.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatal("duplicated offset", id)
			}
			seen[id] = true
		}
		paging := out["paging"].(map[string]any)
		if paging["has_more"] != true {
			break
		}
		args["continuation"] = paging["continuation"]
	}
	if len(seen) != 12 {
		t.Fatal("lost requests across daemon's ten-row page", len(seen))
	}
	args = map[string]any{"limit": float64(1)}
	first, err := s.call(context.Background(), "pax_ai_logs", args)
	if err != nil {
		t.Fatal(err)
	}
	args["continuation"] = first["paging"].(map[string]any)["continuation"]
	f.revision++
	if _, err = s.call(context.Background(), "pax_ai_logs", args); errorCode(err) != "query_changed" {
		t.Fatal("cached stale rows disclosed", err)
	}
}

func TestAIAnalysisNestedPagingAndNotModified(t *testing.T) {
	s, f := aiServer(t)
	args := map[string]any{"projection": "limits", "limit": float64(2)}
	seen := 0
	for i := 0; i < 12; i++ {
		out, err := s.call(context.Background(), "pax_ai_analysis", args)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "PRIVATE PROMPT") {
			t.Fatal("analysis leaked unprovenance title")
		}
		schema, _ := resolveSchema(outputSchema("pax_ai_analysis"))
		if err = schema.Validate(jsonCopy(out)); err != nil {
			t.Fatal(err)
		}
		limits := out["data"].(map[string]any)["limits"].(map[string]any)
		if i == 0 {
			found := false
			for _, warning := range out["warnings"].([]any) {
				if warning.(map[string]any)["code"] == "price_incomplete" {
					found = true
				}
			}
			if !found || out["availability"] != "partial" {
				t.Fatal("missing-price count presented as complete", out)
			}
		}
		for _, row := range limits["primary"].([]any) {
			if len(row.(map[string]any)["bands"].([]any)) != 3 {
				t.Fatal("split a business record")
			}
			seen++
		}
		paging := out["paging"].(map[string]any)
		if paging["has_more"] != true {
			break
		}
		args["continuation"] = paging["continuation"]
	}
	if seen != 12 {
		t.Fatal("nested analysis rows lost", seen)
	}
	out, err := s.call(context.Background(), "pax_ai_analysis", map[string]any{"known_revision": fmt.Sprint(f.revision)})
	if err != nil || out["data"].(map[string]any)["notModified"] != false || f.analysis.KnownRevision == nil || *f.analysis.KnownRevision != f.revision {
		t.Fatal("adapter must preserve owner's full response, not infer notModified from usage revision alone", out, err)
	}
	out, err = s.call(context.Background(), "pax_ai_buckets", map[string]any{"known_revision": fmt.Sprint(f.revision)})
	if err != nil || out["data"].(map[string]any)["notModified"] != true {
		t.Fatal("buckets notModified lost", out, err)
	}
}

func TestAIInputBoundaryDoesNotContactOwner(t *testing.T) {
	s, _ := aiServer(t)
	s.open = func(context.Context) (*backend, error) {
		t.Fatal("invalid schema input reached owner")
		return nil, nil
	}
	client := sdkClient(t, s)
	for _, test := range []struct {
		tool string
		args map[string]any
	}{
		{"pax_ai_logs", map[string]any{"page": 1}},
		{"pax_ai_logs", map[string]any{"max_official_cost_micro": "9223372036854775808"}},
		{"pax_ai_logs", map[string]any{"min_official_cost_micro": "2", "max_official_cost_micro": "1"}},
		{"pax_ai_buckets", map[string]any{"known_revision": "18446744073709551616"}},
		{"pax_ai_analysis", map[string]any{"comparison_range": map[string]any{"from": "2026-11-01T00:00:00Z", "until": "2026-11-02T00:00:00Z"}}},
		{"pax_ai_analysis", map[string]any{"projection": "limits", "sessions": []any{map[string]any{"cli": "codex", "session_id": "a"}, map[string]any{"cli": "claude", "session_id": "b"}}}},
	} {
		if _, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: test.tool, Arguments: test.args}); err == nil {
			t.Fatal("invalid input admitted", test)
		}
	}
}
