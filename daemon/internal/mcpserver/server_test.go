package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
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

type queryFixture struct {
	pb.PaxServiceClient
	routes  int
	calls   int
	expired bool
}

func (f *queryFixture) QueryLogs(context.Context, *pb.LogQuery, ...grpc.CallOption) (*pb.LogPage, error) {
	if f.expired {
		return nil, status.Error(codes.FailedPrecondition, "history_expired")
	}
	out := &pb.LogPage{ReadToken: "frozen-owner-token", Total: 50}
	for i := 0; i < 50; i++ {
		out.Items = append(out.Items, &pb.ProductLog{EventId: fmt.Sprintf("event-%d", i), Message: strings.Repeat("x", 10000)})
	}
	return out, nil
}

func (f *queryFixture) QueryTrafficDetail(_ context.Context, q *pb.TrafficDetailRequest, _ ...grpc.CallOption) (*pb.TrafficDetail, error) {
	if q.ReadToken == "list" {
		if f.expired {
			return nil, status.Error(codes.FailedPrecondition, "history_expired")
		}
		return &pb.TrafficDetail{ReadToken: "detail", NextCursor: "next", Items: []*pb.FlowRecord{{Key: "first"}}}, nil
	}
	if q.ReadToken != "detail" || q.Cursor != "next" {
		return nil, status.Error(codes.InvalidArgument, "invalid_filter")
	}
	return &pb.TrafficDetail{ReadToken: "detail", Items: []*pb.FlowRecord{{Key: "second"}}}, nil
}

func (f *queryFixture) GetSnapshot(context.Context, *pb.GetSnapshotRequest, ...grpc.CallOption) (*pb.Snapshot, error) {
	f.calls++
	out := &pb.Snapshot{CatalogRevision: 4, Status: pb.ProtectionStatus_PROTECTION_STATUS_OFF, TodayTraffic: &pb.TrafficSnapshot{}}
	for i := 0; i < f.routes; i++ {
		out.Routes = append(out.Routes, &pb.NodeProfile{Id: fmt.Sprintf("route-%d", i), Name: strings.Repeat("界", 1500), Revision: 1})
	}
	return out, nil
}

func fixtureServer(t *testing.T, routes int) (*Server, *queryFixture) {
	t.Helper()
	s, err := New(Options{ClientID: "test", Allow: "read"})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &queryFixture{routes: routes}
	s.open = func(context.Context) (*backend, error) {
		return &backend{rpc: fixture, hello: &pb.HelloResponse{DaemonInstance: "instance", Capabilities: []string{"paxd.user"}}}, nil
	}
	return s, fixture
}

func sdkClient(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	session, err := s.mcp.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestSDKSchemaAndAuthorization(t *testing.T) {
	s, fixture := fixtureServer(t, 0)
	client := sdkClient(t, s)
	if _, err := client.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if fixture.calls != 0 {
		t.Fatal("initialize/list contacted daemon")
	}
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_status", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("%v %v", result, err)
	}
	_, err = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_status", Arguments: map[string]any{"confirm": true}})
	if err == nil {
		t.Fatal("unexpected field accepted")
	}
	if _, err = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_route_add", Arguments: map[string]any{}}); err == nil {
		t.Fatal("read permission exposed a write")
	}
	s.open = func(context.Context) (*backend, error) { return nil, errors.New("daemon_unavailable") }
	result, err = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_status", Arguments: map[string]any{}})
	if err != nil || !result.IsError {
		t.Fatalf("failure disguised as success: %v %v", result, err)
	}
}

func TestContinuationByteLimitAndBinding(t *testing.T) {
	s, _ := fixtureServer(t, 100)
	args := map[string]any{"limit": float64(100)}
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		out, err := s.call(context.Background(), "pax_routes_list", args)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(out)
		if len(raw) > responseLimit {
			t.Fatalf("response %d", len(raw))
		}
		for _, value := range out["data"].(map[string]any)["routes"].([]any) {
			id := value.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatal("duplicate", id)
			}
			seen[id] = true
		}
		page, ok := out["paging"].(map[string]any)
		if !ok || page["has_more"] != true {
			break
		}
		token := page["continuation"].(string)
		if _, err = s.call(context.Background(), "pax_routes_list", map[string]any{"continuation": token, "search": "changed", "limit": float64(100)}); err == nil {
			t.Fatal("filter changed silently")
		}
		args["continuation"] = token
	}
	if len(seen) != 100 {
		t.Fatalf("lost records: %d", len(seen))
	}
}

func TestRedactionAndUnknownPreservation(t *testing.T) {
	input := map[string]any{"raw": []any{"secret"}, "message": "https://user:pass@example.test/private?token=xyz password=abcdef sk-ant-secretsecret", "id": "12345678-1234-1234-1234-123456789abc", "value": "12345678-1234-1234-1234-123456789abc", "known": false, "zero": "0"}
	redact(input)
	raw, _ := json.Marshal(input)
	for _, secret := range []string{"user:pass", "?token", "private", "abcdef", "sk-ant-secretsecret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("credential leaked", secret)
		}
	}
	if input["known"] != false || input["zero"] != "0" || input["id"] != "12345678-1234-1234-1234-123456789abc" {
		t.Fatal(input)
	}
	if _, ok := input["raw"]; ok {
		t.Fatal("raw exposed")
	}
	// Half-open ranges and byte limits are external contract boundaries.
	from := time.Now().Add(-time.Hour)
	until := time.Now()
	args := map[string]any{"range": map[string]any{"from": from.Format(time.RFC3339Nano), "until": until.Format(time.RFC3339Nano)}}
	q, err := logQuery(args)
	if err != nil || !q.From.AsTime().Equal(from.Add(-time.Nanosecond)) || !q.Until.AsTime().Equal(until.Add(-time.Nanosecond)) {
		t.Fatalf("%v %v", q, err)
	}
	if checkInputBytes(map[string]any{"search": strings.Repeat("界", 100)}, inputSchema("pax_logs_query")) == nil {
		t.Fatal("UTF-8 byte limit ignored")
	}
}

func TestRedactionAuthAndShareLinks(t *testing.T) {
	value := map[string]any{"message": "Authorization: Bearer secret-value\nCookie: session=secret-cookie\nvmess://encoded-secret\ntrojan://pass-secret@example.test\nBearer bare-secret"}
	redact(value)
	raw, _ := json.Marshal(value)
	for _, secret := range []string{"secret-value", "secret-cookie", "encoded-secret", "pass-secret", "bare-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("leaked", secret)
		}
	}
}

func TestBufferedContinuationRespectsOwnerExpiry(t *testing.T) {
	s, fixture := fixtureServer(t, 0)
	args := map[string]any{"limit": float64(50)}
	first, err := s.call(context.Background(), "pax_logs_query", args)
	if err != nil {
		t.Fatal(err)
	}
	args["continuation"] = first["paging"].(map[string]any)["continuation"]
	fixture.expired = true
	if _, err = s.call(context.Background(), "pax_logs_query", args); err == nil || errorCode(err) != "history_expired" {
		t.Fatalf("buffer disclosed after owner revoked token: %v", err)
	}
}

func TestTrafficContinuationUsesIndependentDetailToken(t *testing.T) {
	s, fixture := fixtureServer(t, 0)
	args := map[string]any{"row_id": "row", "read_token": "list", "limit": float64(1)}
	first, err := s.call(context.Background(), "pax_traffic_detail", args)
	if err != nil {
		t.Fatal(err)
	}
	fixture.expired = true
	args["continuation"] = first["paging"].(map[string]any)["continuation"]
	next, err := s.call(context.Background(), "pax_traffic_detail", args)
	if err != nil {
		t.Fatal(err)
	}
	items := next["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["key"] != "second" {
		t.Fatalf("wrong frozen page: %v", items)
	}
}
