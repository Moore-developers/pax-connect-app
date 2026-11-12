package controller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

type lifecycleCore struct {
	fakeCoreSession
	pathMu           sync.Mutex
	requests         []config.CurrentPathRequest
	switches         int
	observationError bool
}

func (f *lifecycleCore) ApplyCatalog(ctx context.Context, _ []config.RouteConfig, p config.RouteConfig) error {
	return f.Apply(ctx, p)
}
func (f *lifecycleCore) SwitchRoute(ctx context.Context, p config.RouteConfig) (bool, error) {
	f.pathMu.Lock()
	f.switches++
	f.pathMu.Unlock()
	return true, f.Apply(ctx, p)
}

func TestCurrentPathSelectingSameRunningTargetDoesNotInvalidate(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	pick, e := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, pick.OperationId)
	core := &lifecycleCore{}
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	c.ConfigureNetworkEpoch(func() (string, error) { return "network", nil })
	on, e := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, on.OperationId)
	defer func() { _ = c.CloseProxy(context.Background()) }()
	before := waitPathState(t, c, "passed").GetCurrentPathCheck().GetCheckId()
	pick, e = c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if r := awaitResult(t, results, pick.OperationId); !r.Ok {
		t.Fatal(r)
	}
	after, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
	core.pathMu.Lock()
	switches := core.switches
	core.pathMu.Unlock()
	if switches != 0 || after.GetCurrentPathCheck().GetCheckId() != before || after.GetCurrentPathCheck().GetState() != "passed" {
		t.Fatal("same target changed the live path")
	}
}

func (f *lifecycleCore) ObserveCore(context.Context) (config.CoreObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.observationError {
		return config.CoreObservation{}, errors.New("observation unavailable")
	}
	if len(f.profiles) == 0 {
		return config.CoreObservation{Status: "IDLE"}, nil
	}
	p := f.profiles[len(f.profiles)-1]
	return config.CoreObservation{Status: "STARTED", SelectedTag: config.RuntimeTag(p), RuntimeSession: "fixture", RuntimeVersion: 1}, nil
}

func TestCurrentPathDoesNotAdoptCoreAfterUnconfirmedCommit(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	pick, e := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, pick.OperationId)
	core := &lifecycleCore{observationError: true}
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	c.ConfigureNetworkEpoch(func() (string, error) { return "network", nil })
	on, e := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, on.OperationId)
	defer func() { _ = c.CloseProxy(context.Background()) }()
	waitPathState(t, c, "inconclusive")
	core.mu.Lock()
	core.observationError = false
	core.mu.Unlock()
	check, e := c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "unconfirmed"})
	if e != nil {
		t.Fatal(e)
	}
	if result := awaitResult(t, results, check.OperationId); result.Ok {
		t.Fatal("unconfirmed commit adopted a later instance")
	}
	if requestCount(core) != 0 {
		t.Fatal("unconfirmed core ran a check")
	}
}
func (f *lifecycleCore) VerifyCurrentPath(_ context.Context, q config.CurrentPathRequest) (config.CurrentPathResult, error) {
	f.pathMu.Lock()
	f.requests = append(f.requests, q)
	f.pathMu.Unlock()
	return config.CurrentPathResult{CheckID: q.CheckID, State: "passed", CoreSession: "fixture", RuntimeVersion: 1, CompletedAt: time.Now()}, nil
}
func (f *lifecycleCore) Power(context.Context, bool, uint64) error { return nil }
func (f *lifecycleCore) lastRequest() config.CurrentPathRequest {
	f.pathMu.Lock()
	defer f.pathMu.Unlock()
	if len(f.requests) == 0 {
		return config.CurrentPathRequest{}
	}
	return f.requests[len(f.requests)-1]
}
func waitPathState(t *testing.T, c *Controller, want string) *pb.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, e := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
		if e == nil && s.GetCurrentPathCheck().GetState() == want {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	s, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
	t.Fatalf("path state=%s want=%s", s.GetCurrentPathCheck().GetState(), want)
	return nil
}
func TestCurrentPathManualUsesRunningConfigAfterUnappliedEdit(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	selected, e := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if e != nil {
		t.Fatal(e)
	}
	awaitResult(t, results, selected.OperationId)
	core := &lifecycleCore{}
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	c.ConfigureNetworkEpoch(func() (string, error) { return "network", nil })
	on, e := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if r := awaitResult(t, results, on.OperationId); !r.Ok {
		t.Fatal(r)
	}
	t.Cleanup(func() { _ = c.CloseProxy(context.Background()) })
	waitPathState(t, c, "passed")
	before := core.lastRequest()
	editor, e := c.GetRouteEditor(context.Background(), &pb.GetRouteEditorRequest{RouteId: route.Id})
	if e != nil {
		t.Fatal(e)
	}
	edit := editor.Fields
	edit.RouteId = route.Id
	edit.ExpectedRevision = route.Revision
	edit.Server = "changed.example.test"
	ack, e := c.UpdateRoute(context.Background(), edit)
	if e != nil {
		t.Fatal(e)
	}
	if r := awaitResult(t, results, ack.OperationId); !r.Ok {
		t.Fatal(r)
	}
	manual, e := c.RecheckCurrentPath(context.Background(), &pb.CurrentPathRecheckRequest{RequestId: "after-edit"})
	if e != nil {
		t.Fatal(e)
	}
	if r := awaitResult(t, results, manual.OperationId); !r.Ok {
		t.Fatal(r)
	}
	after := core.lastRequest()
	if before.ExpectedTag != after.ExpectedTag || before.ConfigRevision != after.ConfigRevision {
		t.Fatalf("unapplied edit changed running check: before=%s/%d after=%s/%d", before.ExpectedTag, before.ConfigRevision, after.ExpectedTag, after.ConfigRevision)
	}
}
