package controller

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
)

type pooledExitFixture struct {
	fakeTester
	lookup  *measurement.ExitLookup
	entered chan string
	release chan struct{}
}
type exitFixtureClient func(*http.Request) (*http.Response, error)

func (f exitFixtureClient) Do(r *http.Request) (*http.Response, error) { return f(r) }
func (f *pooledExitFixture) MeasureRoute(ctx context.Context, p config.RouteConfig, report func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	value := config.RouteMeasurement{Duration: 20 * time.Millisecond, Cold: 80 * time.Millisecond, Stable: 20 * time.Millisecond, StableSamples: 3, Method: measurement.SessionLatencyMethod, TestTarget: "google"}
	report(value)
	client := exitFixtureClient(func(r *http.Request) (*http.Response, error) {
		f.entered <- p.ID
		select {
		case <-f.release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ip":"8.8.8.8","ipAddress":"8.8.8.8","country":"United States","countryName":"United States","region":"California","regionName":"California","city":"Mountain View","cityName":"Mountain View","location":{"country":"United States","state":"California","city":"Mountain View"}}`))}, nil
	})
	exit, code := f.lookup.Query(ctx, client, measurement.ExitQuery{Scope: p.ID, Purpose: "route_exit"})
	value.ExitError = code
	if code == "" {
		value.ExitIP = exit.IP
		value.ExitLocation = &config.ExitLocation{IP: exit.IP, Country: exit.Country, Region: exit.Region, City: exit.City, At: exit.At, GeoAt: exit.GeoAt}
	}
	return value, ctx.Err()
}
func (f *pooledExitFixture) LookupRouteExit(ctx context.Context, p config.RouteConfig) (config.RouteMeasurement, error) {
	return f.MeasureRoute(ctx, p, func(config.RouteMeasurement) {})
}
func waitPoolCondition(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("pool observation did not converge")
}
func TestExitPoolIndependentQueriesApplyLiveRequestLimit(t *testing.T) {
	lookup := measurement.NewExitLookup()
	f := &pooledExitFixture{lookup: lookup, entered: make(chan string, 8), release: make(chan struct{}, 8)}
	c := newTestController(t, f)
	c.ConfigureExitLookup(lookup)
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	prefs, _ := c.GetMeasurementPreferences(context.Background(), nil)
	prefs.LatencyMode = "manual"
	prefs.LatencyParallel = 1
	prefs.ExitMode = "manual"
	prefs.ExitParallel = 1
	prefs, err := c.SaveMeasurementPreferences(context.Background(), prefs)
	if err != nil {
		t.Fatal(err)
	}
	targets := seedMeasurementScope(t, c, 3)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Targets: targets, Kind: "exit"})
	if err != nil {
		t.Fatal(err)
	}
	waitPoolCondition(t, func() bool {
		s, _ := c.GetSnapshot(context.Background(), nil)
		return len(s.ExitOperationIds) == 3 && len(s.RouteMeasurementProgress) == 0 && s.ExitPoolState.GetInFlight() == 1 && s.ExitPoolState.GetQueued() == 2
	})
	s, _ := c.GetSnapshot(context.Background(), nil)
	for _, p := range s.RouteMeasurementProgress {
		if p.LatencyResult.GetStableMs() != 20 {
			t.Fatal("latency hidden while exit queued")
		}
	}
	for _, r := range s.Routes {
		if r.TestSummary != nil {
			t.Fatal("temporary sample was committed as terminal")
		}
	}
	prefs.ExitParallel = 2
	prefs, err = c.SaveMeasurementPreferences(context.Background(), prefs)
	if err != nil || prefs.ExitPoolState.GetConfiguredLimit() != 2 {
		t.Fatal("save reply has stale runtime state", err)
	}
	waitPoolCondition(t, func() bool { return lookup.Snapshot().InFlight == 2 })
	prefs.ExitParallel = 1
	_, err = c.SaveMeasurementPreferences(context.Background(), prefs)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-results:
		t.Fatal("operation ended before exit finished")
	default:
	}
	f.release <- struct{}{}
	waitPoolCondition(t, func() bool { return lookup.Snapshot().InFlight == 1 && lookup.Snapshot().Queued == 1 })
	time.Sleep(30 * time.Millisecond)
	if len(f.entered) != 2 {
		t.Fatal("lower limit dispatched a third request")
	}
	f.release <- struct{}{}
	waitPoolCondition(t, func() bool { return len(f.entered) == 3 })
	f.release <- struct{}{}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	final := waitComplete(t, c)
	if len(final.RouteMeasurementProgress) != 0 || lookup.Snapshot().InFlight != 0 || lookup.Snapshot().Queued != 0 {
		t.Fatal("transient state leaked")
	}
	for _, r := range final.Routes {
		if r.TestSummary != nil || r.ExitSummary.GetIp() != "8.8.8.8" {
			t.Fatal("final evidence missing")
		}
	}
}
func TestExitPoolCancelClearsIndependentQueriesAndWaiters(t *testing.T) {
	lookup := measurement.NewExitLookup()
	lookup.SetLimit(1, 0)
	f := &pooledExitFixture{lookup: lookup, entered: make(chan string, 8), release: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureExitLookup(lookup)
	targets := seedMeasurementScope(t, c, 3)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Targets: targets, Kind: "exit"})
	if err != nil {
		t.Fatal(err)
	}
	waitPoolCondition(t, func() bool { return lookup.Snapshot().Queued == 2 })
	if _, err = c.CancelOperation(context.Background(), &paxv1.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok {
		t.Fatal("cancel became success")
	}
	waitPoolCondition(t, func() bool { return lookup.Snapshot().Queued == 0 && lookup.Snapshot().InFlight == 0 })
	final := waitComplete(t, c)
	if len(final.RouteMeasurementProgress) != 0 {
		t.Fatal("cancel left temporary evidence")
	}
	for _, r := range final.Routes {
		if r.TestSummary != nil || r.ExitSummary != nil {
			t.Fatal("cancel promoted a partial sample")
		}
	}
}

func TestExitPoolProgressCallbackNeverWaitsForBusyProductOwner(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	entered, release := make(chan struct{}), make(chan struct{})
	go func() { _ = c.query(c.proxy.lifetime, func(context.Context) { close(entered); <-release }) }()
	<-entered
	defer close(release)
	done := make(chan struct{})
	go func() { c.scheduleMeasurementPublish(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("HTTP progress callback blocked behind product operation")
	}
}
func TestExitPoolEditedRouteCancelsOldQueryWithoutCommittingPartial(t *testing.T) {
	lookup := measurement.NewExitLookup()
	lookup.SetLimit(1, 0)
	f := &pooledExitFixture{lookup: lookup, entered: make(chan string, 8), release: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureExitLookup(lookup)
	node := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision})
	if err != nil {
		t.Fatal(err)
	}
	<-f.entered
	if err = c.query(c.proxy.lifetime, func(ctx context.Context) {
		for i := range c.routes.catalog.Routes {
			if c.routes.catalog.Routes[i].Config.ID == node.Id {
				c.routes.catalog.Routes[i].Config.ServerName = "changed.fixture"
				c.routes.catalog.Routes[i].Revision++
				c.routes.catalog.Routes[i].ConfigRevision++
			}
		}
		c.publishCatalog(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if result.Ok || result.Error.GetCode() != "route_stale" {
		t.Fatal("stale configuration was committed", result)
	}
	s := waitComplete(t, c)
	if s.Routes[0].TestSummary != nil {
		t.Fatal("stale partial wrote a completed sample")
	}
	waitPoolCondition(t, func() bool { return lookup.Snapshot().InFlight == 0 })
}

func TestExitPoolQueuedSingleTestsHaveRecoverableCancellationIDs(t *testing.T) {
	f := &queuedLatency{started: make(chan time.Duration, 2), release: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureExitLookup(measurement.NewExitLookup())
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	prefs, _ := c.GetMeasurementPreferences(context.Background(), nil)
	prefs.LatencyMode = "manual"
	prefs.LatencyParallel = 1
	if _, err := c.SaveMeasurementPreferences(context.Background(), prefs); err != nil {
		t.Fatal(err)
	}
	targets := seedMeasurementScope(t, c, 2)
	first, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[0].RouteId, ExpectedRouteRevision: targets[0].ExpectedRouteRevision})
	if err != nil {
		t.Fatal(err)
	}
	defer close(f.release)
	<-f.started
	second, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[1].RouteId, ExpectedRouteRevision: targets[1].ExpectedRouteRevision})
	if err != nil {
		t.Fatal(err)
	}
	waitPoolCondition(t, func() bool {
		s, _ := c.GetSnapshot(context.Background(), nil)
		return len(s.TestingRouteIds) == 2 && len(s.RouteMeasurementProgress) == 1
	})
	snapshot, _ := c.GetSnapshot(context.Background(), nil)
	// Recover IDs exactly as a newly connected client does, without its old ACKs.
	recovered := map[string]bool{}
	if snapshot.LatencyOperationId != "" {
		recovered[snapshot.LatencyOperationId] = true
	}
	for _, p := range snapshot.RouteMeasurementProgress {
		recovered[p.OperationId] = true
	}
	for _, id := range snapshot.TestOperationIds {
		recovered[id] = true
	}
	if !recovered[first.OperationId] || !recovered[second.OperationId] {
		t.Fatal("queued single test lost its cancellation ID after reconnect")
	}
	for _, id := range []string{second.OperationId, first.OperationId} {
		if _, err := c.CancelOperation(context.Background(), &paxv1.CancelOperationRequest{OperationId: id}); err != nil {
			t.Fatal(err)
		}
	}
	waitPoolCondition(t, func() bool {
		s, _ := c.GetSnapshot(context.Background(), nil)
		return len(s.TestingRouteIds) == 0 && len(s.TestOperationIds) == 0
	})
	select {
	case <-f.started:
		t.Fatal("cancelled queued test was still dispatched")
	default:
	}
}

func TestExitPoolDeletedQueuedTestDropsCancellationIdentity(t *testing.T) {
	f := &queuedLatency{started: make(chan time.Duration, 2), release: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureExitLookup(measurement.NewExitLookup())
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	prefs, _ := c.GetMeasurementPreferences(context.Background(), nil)
	prefs.LatencyMode = "manual"
	prefs.LatencyParallel = 1
	if _, err := c.SaveMeasurementPreferences(context.Background(), prefs); err != nil {
		t.Fatal(err)
	}
	targets := seedMeasurementScope(t, c, 2)
	_, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[0].RouteId, ExpectedRouteRevision: targets[0].ExpectedRouteRevision})
	if err != nil {
		t.Fatal(err)
	}
	defer close(f.release)
	<-f.started
	second, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[1].RouteId, ExpectedRouteRevision: targets[1].ExpectedRouteRevision})
	if err != nil {
		t.Fatal(err)
	}
	results, stop := operationSink(c)
	defer stop()
	deleted, err := c.DeleteRoute(context.Background(), &paxv1.DeleteRouteRequest{RouteId: targets[1].RouteId, ExpectedRevision: targets[1].ExpectedRouteRevision})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, deleted.OperationId); !result.Ok {
		t.Fatal(result)
	}
	snapshot, _ := c.GetSnapshot(context.Background(), nil)
	if snapshot.TestOperationIds[targets[1].RouteId] != "" {
		t.Fatal("deleted queued route retained a published cancellation identity")
	}
	if result := awaitResult(t, results, second.OperationId); result.Error.GetCode() != "operation_cancelled" {
		t.Fatal("deleted queued query was not revoked", result)
	}
}

func TestExitPoolDeletedBatchTargetNeverStartsAfterNativeAdmission(t *testing.T) {
	started := make(chan struct{}, 4)
	c := newTestController(t, &fakeTester{started: started, duration: time.Millisecond})
	c.ConfigureExitLookup(measurement.NewExitLookup())
	targets := seedMeasurementScope(t, c, 2)
	// Hold native capacity before child workers register detailed progress.
	for i := 0; i < cap(c.tests.slots); i++ {
		<-c.tests.slots
	}
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := c.DeleteRoute(context.Background(), &paxv1.DeleteRouteRequest{RouteId: targets[1].RouteId, ExpectedRevision: targets[1].ExpectedRouteRevision})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, deleted.OperationId); !result.Ok {
		t.Fatal(result)
	}
	for i := 0; i < cap(c.tests.slots); i++ {
		c.tests.slots <- struct{}{}
	}
	if result := awaitResult(t, results, ack.OperationId); result.Error.GetCode() != "test_batch_partial" {
		t.Fatal(result)
	}
	if len(started) != 1 {
		t.Fatal("deleted batch target performed a probe after native admission", len(started))
	}
}
