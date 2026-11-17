package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func TestCFInputBoundary(t *testing.T) {
	s, err := New(Options{Allow: "read,diagnose"})
	if err != nil {
		t.Fatal(err)
	}
	s.open = func(context.Context) (*backend, error) { t.Fatal("invalid input reached daemon"); return nil, nil }
	client := sdkClient(t, s)
	identity := map[string]any{"request_id": "1794873600000:12345678-1234-1234-1234-123456789abc", "daemon_instance": "instance"}
	for _, args := range []map[string]any{{"action": "scan", "ips": []any{"104.16.0.1"}}, {"action": "check"}, {"action": "measure_one", "ips": []any{"104.16.0.1", "104.16.0.2"}}, {"action": "monitor_start"}, {"action": "check", "ips": []any{}}, {"action": "check", "ips": []any{"104.16.0.1"}, "url": "https://example.test/"}} {
		merge(args, identity)
		if _, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_cf_run", Arguments: args}); err == nil {
			t.Fatal("invalid CF action admitted")
		}
	}
	args := merge(map[string]any{"route_id": "r", "expected_route_revision": "1", "expected_config_revision": "1", "ip": "104.16.0.1", "secret_updates": []any{map[string]any{"path": "/password", "value": "PRIVATE"}}}, identity)
	if _, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_cf_validate", Arguments: args}); err == nil {
		t.Fatal("secret update without document")
	}
}

type cfQueryFixture struct {
	pb.PaxServiceClient
	calls int
}

func (f *cfQueryFixture) QueryCFWorkspace(context.Context, *pb.CFWorkspaceRequest, ...grpc.CallOption) (*pb.CFWorkspace, error) {
	f.calls++
	out := &pb.CFWorkspace{Network: "old", CurrentNetwork: "current"}
	for i := 0; i < 150; i++ {
		out.Records = append(out.Records, &pb.CFRecord{Ip: fmt.Sprintf("104.16.0.%d", i), Network: "old", Tcp: &pb.CFSample{Value: 1}})
	}
	return out, nil
}
func (f *cfQueryFixture) QueryCFCandidates(context.Context, *emptypb.Empty, ...grpc.CallOption) (*pb.CFCandidates, error) {
	return &pb.CFCandidates{Network: "current", Items: []*pb.CFCandidate{{Ip: "104.16.1.1", Source: "official_seed"}}}, nil
}
func TestCFWorkspacePaginationRetainsNetworksAndNoProbe(t *testing.T) {
	s, err := New(Options{Allow: "read"})
	if err != nil {
		t.Fatal(err)
	}
	f := &cfQueryFixture{}
	s.open = func(context.Context) (*backend, error) {
		return &backend{rpc: f, hello: &pb.HelloResponse{DaemonInstance: "instance", Capabilities: []string{"routes.cf.operations.v1"}}}, nil
	}
	client := sdkClient(t, s)
	args := map[string]any{"limit": 100}
	rows := 0
	for i := 0; i < 4; i++ {
		response, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_cf_workspace", Arguments: args})
		if err != nil || response.IsError {
			t.Fatal(response, err)
		}
		data := response.StructuredContent.(map[string]any)
		raw, _ := json.Marshal(data)
		if len(raw) > responseLimit {
			t.Fatal("CF response exceeded byte limit")
		}
		value := data["data"].(map[string]any)
		workspace := value["workspace"].(map[string]any)
		if workspace["network"] != "old" || workspace["currentNetwork"] != "current" {
			t.Fatal("network facts merged")
		}
		rows += len(workspace["records"].([]any))
		paging := data["paging"].(map[string]any)
		if paging["has_more"] == false {
			break
		}
		args["continuation"] = paging["continuation"]
	}
	if rows != 150 || f.calls != 1 {
		t.Fatal("lost records/repeated query", rows, f.calls)
	}
}
func TestCFOperationResultsAndSaveSchema(t *testing.T) {
	for _, op := range []*pb.OperationState{
		{DaemonInstance: "instance", State: "succeeded", Result: &pb.OperationState_CfWorkspace{CfWorkspace: &pb.CFWorkspace{Network: "A", CurrentNetwork: "B"}}},
		{DaemonInstance: "instance", State: "succeeded", Result: &pb.OperationState_CfValidation{CfValidation: &pb.CFValidationResult{Candidate: &pb.CFCandidate{Ip: "104.16.0.1"}, ValidationId: "token", DraftDigest: "random", Network: "A", RouteRevision: 1, ConfigRevision: 2, Success: true, ExpiresAt: timestamppb.New(time.Now().Add(time.Minute))}}},
		{DaemonInstance: "instance", Kind: "cf.save", State: "succeeded", Result: &pb.OperationState_RouteSave{RouteSave: &pb.RouteSaveResult{RouteId: "r", RouteRevision: 2, ConfigRevision: 3, CatalogRevision: 4}}},
	} {
		out := jsonCopy(envelope("instance", operationData(op)))
		rule, err := resolveSchema(outputSchema("pax_operation_get"))
		if err != nil {
			t.Fatal(err)
		}
		if err = rule.Validate(out); err != nil {
			t.Fatal(out, err)
		}
	}
	if cancellationPermission("cf.run") != "diagnose" || cancellationPermission("cf.validate") != "diagnose" || cancellationPermission("cf.save") != "" {
		t.Fatal("CF cancellation permission")
	}
}
