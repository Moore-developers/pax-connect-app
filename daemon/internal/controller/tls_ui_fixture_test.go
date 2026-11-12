//go:build hostui

package controller

import (
	"context"
	"google.golang.org/grpc"
	"net"
	"os"
	"path/filepath"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
)

// Explicit non-root UI fixture: real controller and private storage, no TUN or credentials.
func TestRouteTLSUIFixture(t *testing.T) {
	directory := os.Getenv("PAX_TLS_UI_DIRECTORY")
	if directory == "" {
		t.Skip("explicit UI fixture required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("tls-ui-fixture", "")
	c.ConfigureTLS(config.NewTLSPolicyStore(filepath.Join(directory, "policy.json"), uint32(os.Getuid())))
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) {
		return config.DebugProfile{NodeProfile: config.NodeProfile{ID: "ui-route", Name: "TLS确认测试线路", ProtocolName: "hysteria2", Server: "example.invalid", Port: 443}, ServerName: "example.invalid", TLSSkipSuggested: true}, nil
	})
	listener, err := net.Listen("unix", filepath.Join(directory, "p.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	paxv1.RegisterPaxServiceServer(server, c)
	t.Cleanup(server.Stop)
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}
