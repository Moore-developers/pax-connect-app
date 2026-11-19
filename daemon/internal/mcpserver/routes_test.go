package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func TestRouteInputBoundary(t *testing.T) {
	s, err := New(Options{Allow: "manage"})
	if err != nil {
		t.Fatal(err)
	}
	client := sdkClient(t, s)
	base := map[string]any{"request_id": "1794873600000:12345678-1234-1234-1234-123456789abc", "daemon_instance": "instance", "name": "Example", "document": map[string]any{"type": "socks", "server": "127.0.0.1", "server_port": 9, "version": "5"}}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["document"].(map[string]any)["outbounds"] = []any{} },
		func(v map[string]any) { v["document"].(map[string]any)["type"] = "wireguard" },
		func(v map[string]any) { v["document"].(map[string]any)["detour"] = "outside" },
		func(v map[string]any) { v["document"].(map[string]any)["server_port"] = "443" },
		func(v map[string]any) {
			v["secret_updates"] = []any{map[string]any{"path": "/password", "value": "s", "clear": true}}
		},
	} {
		args := jsonCopy(base)
		mutate(args)
		if _, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_route_add", Arguments: args}); err == nil {
			t.Fatalf("invalid document accepted: %v", args)
		}
	}
	if _, err := parseArguments([]byte(`{"document":{"type":"socks","type":"http"}}`)); err == nil {
		t.Fatal("duplicate key normalized away")
	}
	if _, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_route_add", Arguments: map[string]any{"request_id": base["request_id"], "daemon_instance": "instance", "name": "界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界", "document": base["document"]}}); err == nil {
		t.Fatal("UTF8 byte bound ignored")
	}
}
func TestRouteSchemaProjectsEditableFieldsAndOperationFacts(t *testing.T) {
	document := map[string]any{"type": "vless", "server": "edge.test", "server_port": 443, "transport": map[string]any{"type": "ws", "headers": map[string]any{"Host": "cdn.test"}}, "tls": map[string]any{"enabled": true, "server_name": "edge.test"}, "connect_timeout": "7s"}
	data := map[string]any{"route_id": "route", "revision": "1", "config_revision": "2", "name": "Example", "document": document, "credentials": []any{map[string]any{"path": "/uuid", "present": true}}, "hidden": false, "source_removed": false}
	output, err := resolveSchema(outputSchema("pax_route_get"))
	if err != nil || output.Validate(jsonCopy(envelope("instance", data))) != nil {
		t.Fatalf("valid redacted document rejected: %v", err)
	}
	document["transport"].(map[string]any)["headers"].(map[string]any)["Authorization"] = "LEAK"
	if output.Validate(jsonCopy(envelope("instance", data))) == nil {
		t.Fatal("unsafe header exposed by schema")
	}
	data["document"] = map[string]any{"type": "anytls", "server": "edge.test", "server_port": 443, "network": "tcp", "tls": map[string]any{"enabled": true}}
	if err := output.Validate(jsonCopy(envelope("instance", data))); err != nil {
		t.Fatalf("TCP-only AnyTLS projection rejected: %v", err)
	}
	op := &pb.OperationState{DaemonInstance: "instance", State: "succeeded", Kind: "route.save", Result: &pb.OperationState_RouteSave{RouteSave: &pb.RouteSaveResult{RouteId: "route", RouteRevision: 3, CatalogRevision: 4}}}
	projected := operationData(op)
	result := projected["result"].(map[string]any)
	if result["route_revision"] != "3" || result["catalog_revision"] != "4" {
		t.Fatal(projected)
	}
}

func TestVLESSGRPCDocumentSchema(t *testing.T) {
	input, err := resolveSchema(routeInputSchema("pax_route_update"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := resolveSchema(outputSchema("pax_route_get"))
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"type": "vless", "server": "edge.test", "server_port": 443,
		"tls":       map[string]any{"enabled": true, "alpn": []any{"h2", "http/1.1"}},
		"transport": map[string]any{"type": "grpc", "service_name": "Pax.Tunnel"},
	}
	args := map[string]any{
		"request_id": "1794873600000:12345678-1234-1234-1234-123456789abc", "daemon_instance": "instance",
		"route_id": "route", "expected_revision": "1", "name": "Example", "document": document,
	}
	if err := input.Validate(jsonCopy(args)); err != nil {
		t.Fatalf("editable gRPC document rejected: %v", err)
	}
	result := map[string]any{
		"route_id": "route", "revision": "1", "config_revision": "2", "name": "Example",
		"document": document, "hidden": false, "source_removed": false,
	}
	if err := output.Validate(jsonCopy(envelope("instance", result))); err != nil {
		t.Fatalf("readable gRPC document rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"other protocol", func(doc map[string]any) { doc["type"] = "trojan" }},
		{"vision flow", func(doc map[string]any) { doc["flow"] = "xtls-rprx-vision" }},
		{"missing TLS", func(doc map[string]any) { delete(doc, "tls") }},
		{"no h2", func(doc map[string]any) { doc["tls"].(map[string]any)["alpn"] = "http/1.1" }},
		{"authority", func(doc map[string]any) { doc["transport"].(map[string]any)["authority"] = "other.test" }},
		{"custom path", func(doc map[string]any) { doc["transport"].(map[string]any)["path"] = "/custom" }},
		{"multi mode", func(doc map[string]any) { doc["transport"].(map[string]any)["mode"] = "multi" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := jsonCopy(args)
			tc.mutate(bad["document"].(map[string]any))
			if err := input.Validate(bad); err == nil {
				t.Fatal("accepted an unsupported gRPC document")
			}
		})
	}
}

type previewFixture struct {
	queryFixture
	source string
	err    error
}

type previewWatch struct {
	pb.PaxService_WatchClient
}

func (previewWatch) Recv() (*pb.WatchEvent, error) {
	return &pb.WatchEvent{Payload: &pb.WatchEvent_Snapshot{Snapshot: &pb.Snapshot{}}}, nil
}

func (*previewFixture) Watch(context.Context, *pb.WatchRequest, ...grpc.CallOption) (pb.PaxService_WatchClient, error) {
	return previewWatch{}, nil
}

func (f *previewFixture) PreviewRoutes(_ context.Context, req *pb.PreviewRoutesRequest, _ ...grpc.CallOption) (*pb.RoutesPreview, error) {
	f.source = req.Source
	if f.err != nil {
		return nil, f.err
	}
	return &pb.RoutesPreview{
		Token: "preview", CatalogRevision: 4, Items: []*pb.RoutePreviewItem{{ItemId: "node", Line: 1}},
	}, nil
}

func TestRoutePreviewAllowsCompactStructuredInput(t *testing.T) {
	s, err := New(Options{ClientID: "test", Allow: "manage"})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &previewFixture{}
	s.open = func(context.Context) (*backend, error) {
		return &backend{rpc: fixture, hello: &pb.HelloResponse{
			DaemonInstance: "instance", Capabilities: []string{"routes.edit_view.v1"},
		}}, nil
	}
	client := sdkClient(t, s)
	// A complete source may place many bounded outbounds on one line.
	node := `{"type":"shadowsocks","tag":"fixture","server":"edge.test","server_port":443,"method":"aes-128-gcm","password":"fixture"},`
	source := `{"outbounds":[` + strings.TrimSuffix(strings.Repeat(node, 320), ",") + `]}`
	args := map[string]any{
		"request_id": "1794873600000:12345678-1234-1234-1234-123456789abc", "daemon_instance": "instance", "source": source,
	}
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_routes_preview", Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("preview failed: %v %v", result, err)
	}
	if fixture.source != source {
		t.Fatalf("daemon received %d bytes of %d", len(fixture.source), len(source))
	}

	fixture.err = status.Error(codes.InvalidArgument, "subscription_provider_unsupported")
	args["source"] = "proxy-providers: {remote: {type: http}}"
	result, err = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_routes_preview", Arguments: args})
	if err != nil || !result.IsError || result.StructuredContent.(map[string]any)["code"] != "subscription_provider_unsupported" {
		t.Fatalf("provider-specific failure was not exposed: %v %v", result, err)
	}
}
