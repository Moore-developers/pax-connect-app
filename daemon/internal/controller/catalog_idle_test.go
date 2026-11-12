package controller

import (
	"context"
	"pax-connect/daemon/internal/config"
	"sync/atomic"
	"testing"
	"time"
)

type countingIdleTLS struct{ calls atomic.Int32 }

func (t *countingIdleTLS) Resolve(_ context.Context, p config.DebugProfile) (config.TLSPolicyView, error) {
	t.calls.Add(1)
	return config.SourceTLSPolicy(p), nil
}
func (*countingIdleTLS) Update(context.Context, config.DebugProfile, string, uint64, config.TLSMode, bool) error {
	return nil
}

func TestIdleControllerDoesNotReparseCatalogTLSEverySecond(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("test", "test")
	catalog := config.NewCatalog()
	profile := config.DebugProfile{SchemaVersion: 1, NodeProfile: config.NodeProfile{ID: "idle", Name: "idle", ProtocolName: "trojan", Server: "example.test", Port: 443}, Password: "fixture", Security: "tls", Transport: "tcp", ServerName: "example.test"}
	if _, _, err := catalog.Add(profile); err != nil {
		t.Fatal(err)
	}
	c.ConfigureCatalog(&memoryCatalog{catalog: catalog, exists: true})
	tls := &countingIdleTLS{}
	c.ConfigureTLS(tls)
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
	t.Cleanup(func() { _ = c.CloseProxy(context.Background()) })
	if err := c.query(ctx, func(context.Context) {}); err != nil {
		t.Fatal(err)
	}
	initial := tls.calls.Load()
	time.Sleep(2200 * time.Millisecond)
	if err := c.query(ctx, func(context.Context) {}); err != nil {
		t.Fatal(err)
	}
	if calls := tls.calls.Load() - initial; calls != 0 {
		t.Fatalf("idle periodic refresh resolved TLS %d extra times", calls)
	}
}
