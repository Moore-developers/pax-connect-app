package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSettingsExternalInput(t *testing.T) {
	s, err := New(Options{Allow: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	s.open = func(context.Context) (*backend, error) { t.Fatal("invalid input contacted daemon"); return nil, nil }
	client := sdkClient(t, s)
	base := map[string]any{"daemon_instance": "instance", "request_id": "1794873600000:00000000-0000-0000-0000-000000000001", "expected_revision": "1", "latency": map[string]any{"mode": "manual", "parallel": 4}, "exit": map[string]any{"mode": "auto"}, "download": map[string]any{"mode": "default"}}
	for _, change := range []func(map[string]any){
		func(a map[string]any) { a["latency"] = map[string]any{"mode": "manual"} },
		func(a map[string]any) { a["exit"] = map[string]any{"mode": "auto", "parallel": 8} },
		func(a map[string]any) { a["download"] = map[string]any{"mode": "manual", "parallel": 9} },
		func(a map[string]any) { a["expected_revision"] = "18446744073709551616" },
		func(a map[string]any) { a["auto_connect"] = true },
	} {
		args := jsonCopy(base)
		change(args)
		if _, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "pax_measurement_save", Arguments: args}); err == nil {
			t.Fatal("invalid accepted", args)
		}
	}
}
