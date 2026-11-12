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

type switchingCore struct {
	fakeCoreSession
	switchMu  sync.Mutex
	selected  string
	fail      error
	committed bool
	entered   chan struct{}
	block     bool
	power     []uint64
}

func (f *switchingCore) ApplyCatalog(ctx context.Context, _ []config.RouteConfig, p config.RouteConfig) error {
	f.switchMu.Lock()
	f.selected = p.ID
	f.switchMu.Unlock()
	return f.Apply(ctx, p)
}
func (f *switchingCore) SwitchRoute(ctx context.Context, p config.RouteConfig) (bool, error) {
	if f.entered != nil {
		close(f.entered)
	}
	if f.block {
		<-ctx.Done()
		return false, ctx.Err()
	}
	f.switchMu.Lock()
	defer f.switchMu.Unlock()
	if f.fail == nil || f.committed {
		f.selected = p.ID
	}
	return f.fail == nil || f.committed, f.fail
}
func (f *switchingCore) Power(_ context.Context, _ bool, sequence uint64) error {
	f.switchMu.Lock()
	defer f.switchMu.Unlock()
	f.power = append(f.power, sequence)
	return nil
}

func switchFixture(t *testing.T, core *switchingCore) (*Controller, *pb.NodeProfile, *pb.NodeProfile) {
	t.Helper()
	c := newTestController(t, &fakeTester{})
	a := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.AddRoute(context.Background(), &pb.RouteEditRequest{Name: "B", Protocol: "trojan", Server: "203.0.113.11", Port: 443, Password: "fixture", Transport: "tcp", Security: "tls", ServerName: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); !r.Ok {
		t.Fatal(r)
	}
	snapshot := waitComplete(t, c)
	var b *pb.NodeProfile
	for _, r := range snapshot.Routes {
		if r.Id != a.Id {
			b = r
		}
	}
	if b == nil {
		t.Fatal("second route missing")
	}
	ack, err = c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: a.Id, ExpectedRouteRevision: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(core)
	ack, err = c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); !r.Ok {
		t.Fatal(r)
	}
	t.Cleanup(func() { _ = c.CloseProxy(context.Background()) })
	return c, a, b
}
func TestCoreRunningSelectionPublishesActualOutcome(t *testing.T) {
	for _, scenario := range []string{"success", "failed", "cleanup_pending", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			core := &switchingCore{}
			if scenario == "failed" {
				core.fail = errors.New("route_selection_failed")
			}
			if scenario == "cleanup_pending" {
				core.fail = errors.New("route_connections_pending")
				core.committed = true
			}
			c, a, b := switchFixture(t, core)
			revision := b.Revision
			if scenario == "stale" {
				revision++
			}
			results, stop := operationSink(c)
			defer stop()
			ack, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: b.Id, ExpectedRouteRevision: revision})
			if err != nil {
				t.Fatal(err)
			}
			result := awaitResult(t, results, ack.OperationId)
			snapshot := waitComplete(t, c)
			want := b.Id
			if scenario == "failed" || scenario == "stale" {
				want = a.Id
			}
			if snapshot.GetCurrentRoute().GetId() != want || snapshot.SelectedRouteId != want {
				t.Fatalf("wrong runtime/catalog after %s: %v", scenario, snapshot)
			}
			if result.Ok != (scenario == "success") {
				t.Fatal(result)
			}
			core.mu.Lock()
			applies := len(core.profiles)
			core.mu.Unlock()
			if applies != 1 {
				t.Fatal("normal switch reloaded core")
			}
		})
	}
}
func TestStopCancelsCoreSelectionAndNeverCommitsLateTarget(t *testing.T) {
	core := &switchingCore{block: true, entered: make(chan struct{})}
	c, a, b := switchFixture(t, core)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: b.Id, ExpectedRouteRevision: b.Revision})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-core.entered:
	case <-time.After(time.Second):
		t.Fatal("switch not entered")
	}
	off, err := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); r.Ok {
		t.Fatal("cancelled switch succeeded")
	}
	if r := awaitResult(t, results, off.OperationId); !r.Ok {
		t.Fatal(r)
	}
	snapshot := waitComplete(t, c)
	if snapshot.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF || snapshot.SelectedRouteId != a.Id {
		t.Fatal("stop lost")
	}
}
func TestProductPowerForwardingRejectsDuplicateAndLateEvents(t *testing.T) {
	core := &switchingCore{}
	c, _, _ := switchFixture(t, core)
	for _, request := range []*pb.PowerEventRequest{{Sleeping: true, Sequence: 1}, {Sequence: 3}, {Sleeping: true, Sequence: 2}, {Sequence: 3}} {
		if _, err := c.NotifyPowerEvent(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	core.switchMu.Lock()
	defer core.switchMu.Unlock()
	if len(core.power) != 2 || core.power[0] != 1 || core.power[1] != 3 {
		t.Fatal(core.power)
	}
}

func TestCancelOperationIndependentlyCancelsUncommittedSwitch(t *testing.T) {
	core := &switchingCore{block: true, entered: make(chan struct{})}
	c, a, b := switchFixture(t, core)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: b.Id, ExpectedRouteRevision: b.Revision})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-core.entered:
	case <-time.After(time.Second):
		t.Fatal("switch not entered")
	}
	if _, err := c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok || result.GetError().GetCode() != "operation_cancelled" {
		t.Fatal(result)
	}
	snapshot := waitComplete(t, c)
	if snapshot.Status == pb.ProtectionStatus_PROTECTION_STATUS_OFF || snapshot.GetCurrentRoute().GetId() != a.Id || snapshot.SelectedRouteId != a.Id {
		t.Fatal("cancel stopped or replaced running route")
	}
}

type retryProductPower struct {
	switchingCore
	attempts int
}

func (f *retryProductPower) Power(context.Context, bool, uint64) error {
	f.attempts++
	if f.attempts == 1 {
		return errors.New("fixture unavailable")
	}
	return nil
}
func TestProductPowerRetriesFailedSequence(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	f := &retryProductPower{}
	c.ConfigureCoreSession(f)
	request := &pb.PowerEventRequest{Sequence: 1}
	if _, err := c.NotifyPowerEvent(context.Background(), request); err == nil {
		t.Fatal("missing failure")
	}
	if _, err := c.NotifyPowerEvent(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if f.attempts != 2 {
		t.Fatal("failed product sequence consumed")
	}
}
