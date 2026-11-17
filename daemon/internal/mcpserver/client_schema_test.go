package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestClientSchemasExposeUnionToolsAndRetainValidation(t *testing.T) {
	s, err := New(Options{SocketPath: "/unused.sock", Allow: "read,manage,profiles", ClientID: "schema-client"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.mcp.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("unexpected schema %T", tool.InputSchema)
		}
		if schema["oneOf"] != nil || schema["anyOf"] != nil || schema["allOf"] != nil {
			t.Fatalf("client will hide %s", tool.Name)
		}
		if tool.Name == "pax_operation_get" {
			found = true
		}
	}
	if !found {
		t.Fatal("operation query missing")
	}
	// Supplying both identifiers must fail before any backend connection.
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "pax_operation_get", Arguments: map[string]any{"daemon_instance": "instance", "operation_id": "operation", "request_id": "request"}})
	if err == nil {
		t.Fatal("ambiguous operation identity accepted")
	}
}

func TestTextOnlyClientGetsRecoveryFieldsWithinWireBudget(t *testing.T) {
	args := map[string]any{"daemon_instance": "original-instance", "request_id": "original-request"}
	result := writeFailure(errors.New("daemon_unavailable"), args)
	var text map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &text); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(text, result.StructuredContent) || text["request_id"] != "original-request" {
		t.Fatal("text lost recovery identity", text)
	}
	// Quotes and backslashes require an extra escaping layer in text content.
	value := map[string]any{"data": strings.Repeat("\\\"", responseLimit/8)}
	encoded, err := json.Marshal(toolResult(value, false))
	if err != nil || len(encoded) > wireResponseLimit {
		t.Fatal("escaped text exceeded full response budget", len(encoded), err)
	}
}
