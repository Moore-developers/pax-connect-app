package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	op := &pb.OperationState{DaemonInstance: "instance", State: "succeeded", Kind: "route.save", Result: &pb.OperationState_RouteSave{RouteSave: &pb.RouteSaveResult{RouteId: "route", RouteRevision: 3, CatalogRevision: 4}}}
	projected := operationData(op)
	result := projected["result"].(map[string]any)
	if result["route_revision"] != "3" || result["catalog_revision"] != "4" {
		t.Fatal(projected)
	}
}
