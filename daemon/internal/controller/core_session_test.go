package controller

import (
	"context"
	"errors"
	"sync"
	"testing"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

type fakeCoreSession struct {
	mu                sync.Mutex
	profiles          []config.RouteConfig
	applyError        error
	clears, shutdowns int
}

type detailedCoreFailure struct{}

func (detailedCoreFailure) Error() string { return "core_profile_start_failed" }
func (detailedCoreFailure) ProductFailure() (string, []string) {
	return "core_profile_start_failed", []string{"address_in_use"}
}

func (f *fakeCoreSession) Apply(_ context.Context, p config.RouteConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles = append(f.profiles, p)
	return f.applyError
}
func (f *fakeCoreSession) Clear(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clears++
	return nil
}
func (f *fakeCoreSession) Shutdown(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdowns++
	return nil
}

func TestUserCorePowerUsesSelectedProfileAndKeepsCoreUntilShutdown(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	if _, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	core := &fakeCoreSession{}
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	results, stop := operationSink(c)
	defer stop()
	for _, enabled := range []bool{true, false, true, false} {
		ack, err := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		if r := awaitResult(t, results, ack.OperationId); !r.Ok {
			t.Fatal(r)
		}
		s := waitComplete(t, c)
		if enabled && (s.Status == pb.ProtectionStatus_PROTECTION_STATUS_OFF || s.GetCurrentRoute().GetId() != route.Id) {
			t.Fatal("core start missing from product snapshot")
		}
		if !enabled && (s.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF || s.CurrentRoute != nil) {
			t.Fatal("OFF did not release current route")
		}
	}
	if err := c.CloseProxy(context.Background()); err != nil {
		t.Fatal(err)
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if len(core.profiles) != 2 || core.profiles[0].ID != route.Id || core.clears != 3 || core.shutdowns != 1 {
		t.Fatalf("core lifecycle: applies=%d clears=%d shutdowns=%d", len(core.profiles), core.clears, core.shutdowns)
	}
}

func TestUserCoreStartFailureNeverBecomesConnected(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	if _, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(&fakeCoreSession{applyError: errors.New("core_profile_start_failed")})
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); r.Ok || r.GetError().GetCode() != "core_profile_start_failed" {
		t.Fatal(r)
	}
	if waitComplete(t, c).Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("failed start reported connected")
	}
}

func TestUserCoreStartFailurePreservesReasonParam(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	if _, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(&fakeCoreSession{applyError: detailedCoreFailure{}})
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	r := awaitResult(t, results, ack.OperationId)
	if r.Ok || r.GetError().GetCode() != "core_profile_start_failed" || len(r.GetError().GetMessageParams()) != 1 || r.GetError().GetMessageParams()[0] != "address_in_use" {
		t.Fatalf("unexpected result: %v", r)
	}
}
