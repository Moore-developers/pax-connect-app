package controller

import (
	"context"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"sync"
	"testing"
	"time"
)

type stagedMeasurementFixture struct {
	fakeTester
	failure error
}

type countedMeasurements struct {
	mu       sync.Mutex
	saves    int
	document config.MeasurementDocument
}

func (s *countedMeasurements) Load() (config.MeasurementDocument, error) {
	return config.NewMeasurements(), nil
}
func (s *countedMeasurements) Save(_ context.Context, doc config.MeasurementDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	s.document = doc
	return nil
}
func TestBatchPublishesEachResultAndPersistsOnce(t *testing.T) {
	fixture := &batchDownloadFixture{started: make(chan struct{}), release: make(chan struct{})}
	c := newTestController(t, fixture)
	store := &countedMeasurements{}
	c.ConfigureMeasurements(store)
	targets := seedBatch(t, c)
	store.mu.Lock()
	before := store.saves
	store.mu.Unlock()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Kind: "download", Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.started
	close(fixture.release)
	awaitResult(t, results, ack.OperationId)
	store.mu.Lock()
	saves, count := store.saves-before, len(store.document.Records)
	store.mu.Unlock()
	if saves != 1 || count != 3 {
		t.Fatal("batch persistence was not coalesced", saves, count)
	}
	if len(waitComplete(t, c).Routes) != 3 {
		t.Fatal("lost route results")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func (s *stagedMeasurementFixture) MeasureRoute(context.Context, config.RouteConfig, func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	if s.failure != nil {
		return config.RouteMeasurement{}, s.failure
	}
	return config.RouteMeasurement{Duration: 120 * time.Millisecond, ExitError: "exit_timeout"}, nil
}
func TestDiagnosticDoesNotPublishExitFields(t *testing.T) {
	c := newTestController(t, &stagedMeasurementFixture{})
	node := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "diagnostic"})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	summary := waitComplete(t, c).Routes[0].TestSummary
	if summary == nil || !summary.Success || summary.DurationMs != 120 || summary.ExitError != nil {
		t.Fatal(summary)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}
func TestDiagnosticPublishesSanitizedStageError(t *testing.T) {
	c := newTestController(t, &stagedMeasurementFixture{failure: &measurement.Failure{Code: "node_connection_refused", Stage: "node_connection"}})
	node := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "diagnostic"})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Error.GetCode() != "node_connection_refused" {
		t.Fatal(result)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}
