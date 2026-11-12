package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"sync/atomic"
	"testing"
	"time"
)

type checkingCore struct {
	fakeCoreSession
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (f *checkingCore) ObserveCore(context.Context) (config.CoreObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.profiles) == 0 {
		return config.CoreObservation{Status: "IDLE"}, nil
	}
	return config.CoreObservation{Status: "STARTED", SelectedTag: config.RuntimeTag(f.profiles[len(f.profiles)-1]), RuntimeSession: "fixture", RuntimeVersion: 1}, nil
}

func (f *checkingCore) VerifyCurrentPath(ctx context.Context, q config.CurrentPathRequest) (config.CurrentPathResult, error) {
	f.calls.Add(1)
	select {
	case f.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return config.CurrentPathResult{}, ctx.Err()
	case <-f.release:
	}
	return config.CurrentPathResult{CheckID: q.CheckID, State: "passed", CoreSession: "fixture", RuntimeVersion: 1, CompletedAt: time.Now()}, nil
}
func TestCurrentPathDoesNotDelayONAndManualRequestsShareAutomaticCheck(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	if _, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	core := &checkingCore{entered: make(chan struct{}, 1), release: make(chan struct{})}
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	c.ConfigureNetworkEpoch(func() (string, error) { return "network", nil })
	results, stop := operationSink(c)
	defer stop()
	on, err := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, on.OperationId); !result.Ok {
		t.Fatal(result)
	}
	select {
	case <-core.entered:
	case <-time.After(time.Second):
		t.Fatal("check not started")
	}
	snapshot, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
	if snapshot.GetCurrentPathCheck().GetState() != "checking" {
		t.Fatal(snapshot)
	}
	request := &pb.CurrentPathRecheckRequest{RequestId: "manual"}
	first, err := c.RecheckCurrentPath(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.RecheckCurrentPath(context.Background(), request)
	if err != nil || first.OperationId != second.OperationId {
		t.Fatal(first, second, err)
	}
	if core.calls.Load() != 1 {
		t.Fatal("duplicate HTTPS")
	}
	close(core.release)
	if result := awaitResult(t, results, first.OperationId); !result.Ok {
		t.Fatal(result)
	}
	snapshot, _ = c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
	check := snapshot.GetCurrentPathCheck()
	if check.State != "passed" || snapshot.Status == pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED || check.TriggerOperationId != on.OperationId || len(check.CommandOperationIds) != 1 {
		t.Fatal(snapshot)
	}
	_ = c.CloseProxy(context.Background())
}
func TestCurrentPathOFFCancelsWithoutResurrection(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	_, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	core := &checkingCore{entered: make(chan struct{}, 1), release: make(chan struct{})}
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	results, stop := operationSink(c)
	defer stop()
	on, err := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, on.OperationId)
	select {
	case <-core.entered:
	case <-time.After(time.Second):
		t.Fatal("missing check")
	}
	off, err := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, off.OperationId); !result.Ok {
		t.Fatal(result)
	}
	close(core.release)
	snapshot := waitComplete(t, c)
	if snapshot.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF || snapshot.GetCurrentPathCheck().GetState() != "stale" {
		t.Fatal(snapshot)
	}
	if _, err = c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "off"}); err == nil {
		t.Fatal("OFF accepted recheck")
	}
	_ = c.CloseProxy(context.Background())
}
