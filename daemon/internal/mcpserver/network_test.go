package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func TestNetworkInputBoundary(t *testing.T) {
	s, err := New(Options{Allow: "diagnose"})
	if err != nil {
		t.Fatal(err)
	}
	s.open = func(context.Context) (*backend, error) { t.Fatal("invalid call contacted daemon"); return nil, nil }
	client := sdkClient(t, s)
	for _, args := range []map[string]any{
		{"kind": "download", "targets": []any{}},
		{"kind": "arbitrary-url", "targets": []any{}},
		{"kind": "latency", "url": "https://secret.test/"},
		{"kind": "exit", "targets": []any{map[string]any{"route_id": "r", "expected_route_revision": "18446744073709551616", "expected_config_revision": "1"}}},
	} {
		args["request_id"] = "1794873600000:12345678-1234-1234-1234-123456789abc"
		args["daemon_instance"] = "instance"
		if _, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_routes_test", Arguments: args}); err == nil {
			t.Fatal("invalid measurement admitted", args)
		}
	}
}

func TestNetworkResultsPreserveUnknownDependenciesAndRootGuidance(t *testing.T) {
	cases := []*pb.OperationState{
		{DaemonInstance: "instance", State: "failed", Error: &pb.ProductError{Code: "core_required", MessageParams: []string{"password=PRIVATE"}}},
		{DaemonInstance: "instance", State: "succeeded", Result: &pb.OperationState_Proxy{Proxy: &pb.ProxyOperationResult{Status: pb.ProtectionStatus_PROTECTION_STATUS_OFF, SelectedRouteId: "selected"}}},
		{DaemonInstance: "instance", State: "succeeded", Result: &pb.OperationState_Measurements{Measurements: &pb.RouteMeasurementsResult{Items: []*pb.RouteMeasurementResult{{RouteId: "r", ConfigRevision: 7, Error: &pb.ProductError{Code: "download_timeout"}, Measurement: &pb.RouteMeasurementResult_Download{Download: &pb.DownloadTestSummary{Error: &pb.ProductError{Code: "download_timeout"}}}}}}}},
		{DaemonInstance: "instance", State: "succeeded", Result: &pb.OperationState_Environment{Environment: &pb.EnvironmentDiagnosticReport{SessionId: "session", BrowserUrl: "http://127.0.0.1:1234/PRIVATE/", Complete: true, Mac: &pb.EnvironmentSample{IpError: "sampling_timeout"}}}},
	}
	rule, err := resolveSchema(outputSchema("pax_operation_get"))
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range cases {
		out := jsonCopy(envelope("instance", operationData(op)))
		if err := rule.Validate(out); err != nil {
			raw, _ := json.Marshal(out)
			t.Fatalf("schema: %v %s", err, raw)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "browserUrl") {
			t.Fatal("private field escaped", string(raw))
		}
	}
	data := environmentData(cases[3].GetEnvironment())
	if data["system_metadata"].(map[string]any)["timezone"] != nil || data["dependencies"].(map[string]any)["browser"].(map[string]any)["state"] != "not_detected" {
		t.Fatal(data)
	}
	if envelope("instance", data)["availability"] != "partial" {
		t.Fatal("missing dependencies reported complete")
	}
	progress := operationData(&pb.OperationState{DaemonInstance: "instance", State: "running", Progress: &pb.OperationProgress{Phase: "queued"}})["progress"].(map[string]any)
	if _, ok := progress["total"]; ok {
		t.Fatal("unknown total became zero")
	}
}

type cancelFixture struct {
	pb.PaxServiceClient
	kind  string
	calls int
}

func (f *cancelFixture) GetOperation(context.Context, *pb.GetOperationRequest, ...grpc.CallOption) (*pb.OperationState, error) {
	return &pb.OperationState{DaemonInstance: "instance", OperationId: "job", OwnerClientId: "app", Kind: f.kind, State: "running", Cancellable: true}, nil
}
func (f *cancelFixture) CancelOperation(context.Context, *pb.CancelOperationRequest, ...grpc.CallOption) (*pb.OperationAck, error) {
	f.calls++
	return &pb.OperationAck{OperationId: "job"}, nil
}
func TestCancelUsesActualOwnerKindPermission(t *testing.T) {
	s, err := New(Options{Allow: "diagnose"})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &cancelFixture{kind: "proxy.set"}
	b := &backend{rpc: fixture, hello: &pb.HelloResponse{DaemonInstance: "instance"}}
	args := map[string]any{"operation_id": "job"}
	if _, err = s.cancelNetworkOperation(context.Background(), context.Background(), b, args, &pb.OperationContext{}); err == nil || err.Error() != "permission_denied" || fixture.calls != 0 {
		t.Fatal("diagnose cancelled proxy", err)
	}
	fixture.kind = "test-route.latency"
	result, err := s.cancelNetworkOperation(context.Background(), context.Background(), b, args, &pb.OperationContext{})
	if err != nil || result["cancel_requested"] != true || fixture.calls != 1 || result["operation"].(map[string]any)["owner_client_id"] != "app" {
		t.Fatal(result, err)
	}
	fixture.kind = "unknown-kind"
	if _, err = s.cancelNetworkOperation(context.Background(), context.Background(), b, args, &pb.OperationContext{}); err == nil || fixture.calls != 1 {
		t.Fatal("unknown kind cancelled", err)
	}
}
