package controller

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/environment"
)

func trackedState(t *testing.T, c *Controller, id string, terminal bool) *pb.OperationState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		value, err := c.GetOperation(context.Background(), &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_OperationId{OperationId: id}})
		if err != nil {
			t.Fatal(err)
		}
		if !terminal || value.FinishedAt != nil {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not settle: %v", value)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNetworkOperationSurvivesDisconnectAndCancelsThroughOriginalOwner(t *testing.T) {
	f := &fakeNetworkDiagnoser{entered: make(chan struct{}), release: make(chan struct{})}
	c, _ := diagnosticController(t, f)
	identity := operationIdentity(c)
	req := &pb.StartNetworkDiagnosticRequest{Mode: "full", Context: identity}
	ctx, disconnect := context.WithCancel(context.Background())
	ack, err := c.StartNetworkDiagnostic(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	disconnect()
	<-f.entered
	state := trackedState(t, c, ack.OperationId, false)
	if state.State != "running" || !state.Cancellable || state.GetProgress().GetCompleted() != 2 || state.GetProgress().GetTotal() != 22 {
		t.Fatal(state)
	}
	again, err := c.StartNetworkDiagnostic(context.Background(), req)
	if err != nil || again.OperationId != ack.OperationId {
		t.Fatal(again, err)
	}
	other := operationIdentity(c)
	other.ClientId = "other-client"
	if _, err = c.StartNetworkDiagnostic(context.Background(), &pb.StartNetworkDiagnosticRequest{Mode: "quick", Context: other}); status.Code(err) != codes.ResourceExhausted {
		t.Fatal("concurrent diagnosis admitted", err)
	}
	cancelIdentity := operationIdentity(c)
	cancelIdentity.ClientId = "canceller"
	if _, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId, Context: cancelIdentity}); err != nil {
		t.Fatal(err)
	}
	terminal := trackedState(t, c, ack.OperationId, true)
	if terminal.State != "cancelled" || terminal.Cancellable || terminal.OwnerClientId != identity.ClientId {
		t.Fatal(terminal)
	}
	if _, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId, Context: cancelIdentity}); err != nil {
		t.Fatal("terminal cancel retry", err)
	}
}

func TestNetworkOperationRetainsTypedResultAcrossLaterOperations(t *testing.T) {
	f := &fakeNetworkDiagnoser{entered: make(chan struct{}), release: make(chan struct{})}
	c, _ := diagnosticController(t, f)
	req := &pb.StartNetworkDiagnosticRequest{Mode: "quick", Context: operationIdentity(c)}
	ack, err := c.StartNetworkDiagnostic(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	<-f.entered
	close(f.release)
	state := trackedState(t, c, ack.OperationId, true)
	if state.State != "succeeded" || state.GetNetwork().GetScore() != 55 {
		t.Fatal(state)
	}
	state.GetNetwork().Score = nil
	if got := trackedState(t, c, ack.OperationId, true); got.GetNetwork().GetScore() != 55 {
		t.Fatal("reader mutated retained result", got)
	}
	req.Mode = "full"
	if _, err = c.StartNetworkDiagnostic(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatal("reused identity changed content", err)
	}
}

func TestEnvironmentBusyPreservesOtherSession(t *testing.T) {
	c := New("test", "")
	// An existing session is enough to exercise admission; no real probes run.
	c.tests = &testOwner{}
	original := &environment.Session{}
	c.environmentSession = original
	if _, err := c.InspectEnvironment(context.Background(), &pb.EnvironmentDiagnosticRequest{Action: "start", Context: operationIdentity(c)}); status.Code(err) != codes.ResourceExhausted {
		t.Fatal(err)
	}
	if c.environmentSession != original {
		t.Fatal("start replaced another client's session")
	}
}

func TestTrackedBatchKeepsPerRouteResultsAfterCatalogChanges(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	// Test both success and actual per-item failure without using network input.
	identity := operationIdentity(c)
	ack, err := c.TestRoute(context.Background(), &pb.TestRouteRequest{Kind: "latency", Context: identity, Targets: []*pb.RouteTestTarget{{RouteId: route.Id, ExpectedRouteRevision: route.Revision, ExpectedConfigRevision: route.ConfigRevision}}})
	if err != nil {
		t.Fatal(err)
	}
	state := trackedState(t, c, ack.OperationId, true)
	if state.GetMeasurements() == nil || len(state.GetMeasurements().Items) != 1 {
		t.Fatal(state)
	}
	item := state.GetMeasurements().Items[0]
	if item.RouteId != route.Id || item.GetLatency() == nil || item.ConfigRevision == 0 {
		t.Fatal(item)
	}
	if _, err = c.DeleteRoute(context.Background(), &pb.DeleteRouteRequest{RouteId: route.Id, ExpectedRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	later := trackedState(t, c, ack.OperationId, true)
	if len(later.GetMeasurements().Items) != 1 || later.GetMeasurements().Items[0].RouteId != route.Id {
		t.Fatal(later)
	}
}
