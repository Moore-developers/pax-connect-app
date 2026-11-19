package singbox

import (
	"context"
	"errors"
	"testing"

	"pax-connect/daemon/internal/config"
)

func TestValidateImportedRouteDocumentRejectsNonRunnableOptions(t *testing.T) {
	const base = `{"type":"anytls","server":"fixture.invalid","server_port":443,"password":"fixture"`
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		fields string
	}{
		{"outside core allowlist", `,"client_metadata":"fixture","tls":{"enabled":true}`},
		{"AnyTLS fast open", `,"tcp_fast_open":true,"tls":{"enabled":true}`},
		{"TLS constructor reject", `,"tls":{"enabled":true,"min_version":"99"}`},
		{"noncanonical TLS key", `,"tls":{"enabled":true,"Server_Name":"fixture.invalid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(base + tc.fields + "}")
			if err := ValidateRouteDocument(ctx, raw); err != nil {
				t.Fatalf("native document decoder: %v", err)
			}
			if err := ValidateImportedRouteDocument(ctx, raw); !errors.Is(err, config.ErrDocumentField) {
				t.Fatalf("import validation: %v", err)
			}
		})
	}
	raw := []byte(base + `,"tls":{"enabled":true},"network":"tcp"}`)
	if err := ValidateImportedRouteDocument(ctx, raw); err != nil {
		t.Fatalf("valid TCP-only AnyTLS rejected: %v", err)
	}
}

func TestValidateImportedRouteDocumentRejectsInvalidActiveMultiplex(t *testing.T) {
	ctx := context.Background()
	raw := []byte(`{"type":"shadowsocks","server":"fixture.invalid","server_port":443,"method":"aes-128-gcm","password":"fixture","multiplex":{"enabled":true,"protocol":"invalid"}}`)
	if err := ValidateRouteDocument(ctx, raw); err != nil {
		t.Fatalf("native document decoder: %v", err)
	}
	if err := ValidateImportedRouteDocument(ctx, raw); !errors.Is(err, config.ErrDocumentField) {
		t.Fatalf("import validation: %v", err)
	}
}
