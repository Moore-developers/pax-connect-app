package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func TestProfileInputBoundaryAndDSHRejection(t *testing.T) {
	s, err := New(Options{Allow: "profiles"})
	if err != nil {
		t.Fatal(err)
	}
	s.open = func(context.Context) (*backend, error) {
		return &backend{hello: &pb.HelloResponse{DaemonInstance: "instance", Capabilities: []string{"ai.profiles.operations.v1"}}}, nil
	}
	client := sdkClient(t, s)
	base := map[string]any{"request_id": "1794873600000:12345678-1234-1234-1234-123456789abc", "daemon_instance": "instance", "profile": map[string]any{"id": "4eb87612-9a70-49d3-88e0-a54d40245269", "name": "API", "base_url": "https://example.test", "multiplier": 1, "model_ids": []any{}}, "key_change": map[string]any{"mode": "keep"}}
	for _, mutate := range []func(map[string]any){
		func(a map[string]any) { a["key_change"] = map[string]any{"mode": "keep", "api_key": "old"} },
		func(a map[string]any) { a["key_change"] = map[string]any{"mode": "replace", "api_key": ""} },
		func(a map[string]any) { a["profile"].(map[string]any)["multiplier"] = 1.0001 },
		func(a map[string]any) { a["native_profile"] = map[string]any{"id": "same"} },
		func(a map[string]any) { a["expected_revision"] = "18446744073709551616" },
		func(a map[string]any) { a["profile"].(map[string]any)["model_ids"] = []any{strings.Repeat("界", 100)} },
	} {
		a := jsonCopy(base)
		mutate(a)
		if _, err = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_profile_save", Arguments: a}); err == nil {
			t.Fatal("invalid save accepted")
		}
	}
	for _, cli := range []string{"dsh", "bad"} {
		args := map[string]any{"cli": cli, "profile_id": "4eb87612-9a70-49d3-88e0-a54d40245269", "revision": "1", "inspection_id": "i", "request_id": base["request_id"], "daemon_instance": "instance"}
		result, e := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_profile_plan", Arguments: args})
		if e != nil || !result.IsError || !strings.Contains(fmt.Sprint(result.StructuredContent), "ai_template_unsupported") {
			t.Fatalf("%s: %v %v", cli, result, e)
		}
	}
}
func TestProfileProjectionExcludesTemplateAndSecrets(t *testing.T) {
	w := &pb.AIProfilesWorkspace{Profiles: []*pb.AIProfileSummary{{Id: "4eb87612-9a70-49d3-88e0-a54d40245269", Name: "api_key=never-output", HasKey: true}}, Bindings: []*pb.AIClientBinding{{Cli: "codex", TemplateRevision: 7, InspectionId: "inspection"}}}
	value := profileWorkspace(w)
	text := fmt.Sprint(value)
	if strings.Contains(text, "never-output") || strings.Contains(text, "templateRevision") {
		t.Fatal(text)
	}
	op := &pb.OperationState{DaemonInstance: "instance", State: "succeeded", Kind: "profile.models", Result: &pb.OperationState_AiProfile{AiProfile: &pb.AIProfileOperationResult{Workspace: w}}}
	out, err := resolveSchema(outputSchema("pax_operation_get"))
	if err != nil || out.Validate(jsonCopy(envelope("instance", operationData(op)))) != nil {
		t.Fatal(err)
	}
}

func TestProfileConflictErrorOffersCurrentRevision(t *testing.T) {
	if errorCode(status.Error(codes.FailedPrecondition, "result_too_large")) != "result_too_large" || safeRouteCode("result_too_large") != "result_too_large" {
		t.Fatal("size boundary lost controlled error")
	}

	st, err := status.New(codes.FailedPrecondition, "ai_profile_conflict").WithDetails(&errdetails.ErrorInfo{Reason: "ai_profile_conflict", Metadata: map[string]string{"latest_revision": "7"}})
	if err != nil {
		t.Fatal(err)
	}
	value := writeFailure(st.Err(), map[string]any{"request_id": "original", "daemon_instance": "instance"}).StructuredContent.(map[string]any)
	if value["latest_revision"] != "7" || value["retry"] != "replan" || value["request_id"] != "original" {
		t.Fatal(value)
	}
}
