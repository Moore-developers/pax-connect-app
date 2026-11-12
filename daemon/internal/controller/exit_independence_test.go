package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"sync/atomic"
	"testing"
	"time"
)

type independentMetrics struct {
	fakeTester
	latencyCalls atomic.Int32
	exitCalls    atomic.Int32
}

func (f *independentMetrics) MeasureRoute(ctx context.Context, p config.RouteConfig, report func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	f.latencyCalls.Add(1)
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 20*time.Second {
		return config.RouteMeasurement{}, context.DeadlineExceeded
	}
	return config.RouteMeasurement{Duration: 123 * time.Millisecond}, nil
}
func (f *independentMetrics) LookupRouteExit(context.Context, config.RouteConfig) (config.RouteMeasurement, error) {
	f.exitCalls.Add(1)
	return config.RouteMeasurement{ExitIP: "203.0.113.9", ExitLocation: &config.ExitLocation{IP: "203.0.113.9", At: time.Now()}}, nil
}
func TestExitQueryDoesNotRunOrReplaceLatency(t *testing.T) {
	fixture := &independentMetrics{}
	c := newTestController(t, fixture)
	node := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	for _, kind := range []string{"latency", "exit", "latency"} {
		ack, err := c.TestRoute(context.Background(), &pb.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: kind})
		if err != nil {
			t.Fatal(kind, err)
		}
		if result := awaitResult(t, results, ack.OperationId); !result.Ok {
			t.Fatal(kind, result)
		}
	}
	snapshot, err := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.latencyCalls.Load() != 2 || fixture.exitCalls.Load() != 1 || snapshot.Routes[0].TestSummary.DurationMs != 123 || snapshot.Routes[0].ExitSummary.GetIp() != "203.0.113.9" {
		t.Fatal("metrics are coupled")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

// Separate jobs for the same route can run and cancel without sharing fate.
type blockingIndependentMetrics struct {
	independentMetrics
	entered chan struct{}
}

func (f *blockingIndependentMetrics) LookupRouteExit(ctx context.Context, _ config.RouteConfig) (config.RouteMeasurement, error) {
	close(f.entered)
	<-ctx.Done()
	return config.RouteMeasurement{}, ctx.Err()
}
func TestCancelExitLeavesLatencyResult(t *testing.T) {
	f := &blockingIndependentMetrics{entered: make(chan struct{})}
	c := newTestController(t, f)
	node := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	exit, err := c.TestRoute(context.Background(), &pb.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "exit"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("exit not started")
	}
	latency, err := c.TestRoute(context.Background(), &pb.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "latency"})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, latency.OperationId); !result.Ok {
		t.Fatal(result)
	}
	if _, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: exit.OperationId}); err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, exit.OperationId)
	if result.Ok || result.Error.GetCode() != "operation_cancelled" {
		t.Fatal(result)
	}
	snap, err := c.GetSnapshot(context.Background(), nil)
	if err != nil || len(snap.ExitOperationIds) != 0 || snap.Routes[0].ExitSummary != nil || snap.Routes[0].TestSummary.GetDurationMs() != 123 {
		t.Fatal("cancellation corrupted independent results", err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestExitPersistenceKeepsUnknownGeoTime(t *testing.T) {
	value := &config.ExitLocation{IP: "203.0.113.9", At: time.Now().UTC(), ConfigRevision: 7}
	encoded := exitLocationProto(value)
	if encoded.GeoAt != nil {
		t.Fatal("invented geo time before persistence")
	}
	decoded := exitLocationValue(encoded)
	if !decoded.GeoAt.IsZero() {
		t.Fatal("invented geo time during persistence")
	}
	if exitLocationProto(decoded).GeoAt != nil {
		t.Fatal("invented geo time after reload")
	}
}

func TestCloseCancelsExitAndClearsIdentity(t *testing.T) {
	f := &blockingIndependentMetrics{entered: make(chan struct{})}
	c := newTestController(t, f)
	node := seedLocalRoute(t, c)
	_, err := c.TestRoute(context.Background(), &pb.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "exit"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
	snap, err := c.GetSnapshot(context.Background(), nil)
	if err != nil || len(snap.ExitOperationIds) != 0 {
		t.Fatal("stale exit identity at shutdown", err)
	}
}

func TestQueuedExitDeletedBeforeAdmissionDoesNotQuery(t *testing.T) {
	f := &independentMetrics{}
	c := newTestController(t, f)
	node := seedLocalRoute(t, c)
	for i := 0; i < cap(c.tests.slots); i++ {
		<-c.tests.slots
	}
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &pb.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "exit"})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := c.DeleteRoute(context.Background(), &pb.DeleteRouteRequest{RouteId: node.Id, ExpectedRevision: node.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, deleted.OperationId); !result.Ok {
		t.Fatal(result)
	}
	for i := 0; i < cap(c.tests.slots); i++ {
		c.tests.slots <- struct{}{}
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok {
		t.Fatal("deleted route passed")
	}
	if f.exitCalls.Load() != 0 {
		t.Fatal("deleted route performed network I/O")
	}
	snap, err := c.GetSnapshot(context.Background(), nil)
	if err != nil || len(snap.ExitOperationIds) != 0 {
		t.Fatal("deleted route retained running identity", err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

// A typed exit failure keeps its sanitized reason (HTTP / WS status) in error_params, like the latency column.
type failingExitMetrics struct {
	independentMetrics
	err error
}

func (f *failingExitMetrics) LookupRouteExit(context.Context, config.RouteConfig) (config.RouteMeasurement, error) {
	if f.err == nil {
		return config.RouteMeasurement{ExitIP: "203.0.113.9"}, nil
	}
	return config.RouteMeasurement{ExitError: "node_ws_handshake_failed"}, f.err
}

func TestExitFailureKeepsReasonParams(t *testing.T) {
	fixture := &failingExitMetrics{err: &measurement.Failure{Code: "node_ws_handshake_failed", Stage: "exit_lookup", Reason: "525"}}
	c := newTestController(t, fixture)
	node := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	run := func() *pb.RouteExitSummary {
		ack, err := c.TestRoute(context.Background(), &pb.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "exit"})
		if err != nil {
			t.Fatal(err)
		}
		awaitResult(t, results, ack.OperationId)
		snapshot, err := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot.Routes[0].ExitSummary
	}
	failed := run()
	if failed.GetSuccess() || failed.GetErrorCode() != "node_ws_handshake_failed" || len(failed.GetErrorParams()) != 1 || failed.ErrorParams[0] != "525" {
		t.Fatal("failure reason was dropped", failed)
	}
	// The same summary survives a persisted round trip through the saved measurement.
	if back := savedExitProto(savedExit(failed)); len(back.ErrorParams) != 1 || back.ErrorParams[0] != "525" {
		t.Fatal("params lost in saved measurement", back)
	}
	fixture.err = nil
	if ok := run(); !ok.GetSuccess() || len(ok.GetErrorParams()) != 0 {
		t.Fatal("success kept stale params", ok)
	}
}
