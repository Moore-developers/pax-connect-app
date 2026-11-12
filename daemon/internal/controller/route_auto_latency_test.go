package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
)

// autoTester answers latency probes from a queue of stable durations; it is both the manual
// and the automatic probe boundary, so tests tell the two apart by call order.
type autoTester struct {
	fakeTester
	mu      sync.Mutex
	calls   int
	queue   []time.Duration
	err     error
	entered chan struct{}
	release chan struct{}
}

func (f *autoTester) MeasureRoute(ctx context.Context, _ config.RouteConfig, _ func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	f.mu.Lock()
	f.calls++
	value := 100 * time.Millisecond
	if len(f.queue) > 0 {
		value, f.queue = f.queue[0], f.queue[1:]
	}
	err, entered, release := f.err, f.entered, f.release
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return config.RouteMeasurement{}, ctx.Err()
		}
	}
	if err != nil {
		return config.RouteMeasurement{}, err
	}
	return config.RouteMeasurement{Cold: 3 * value, Duration: value, Stable: value, StableSamples: 3, Method: measurement.SessionLatencyMethod}, nil
}
func (f *autoTester) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type autoFixture struct {
	c      *Controller
	tester *autoTester
	route  *paxv1.NodeProfile
	ticks  chan time.Time
	rounds chan string
}

// newAutoFixture runs a controller whose proxy is "on" for the seeded route, the network is
// up, and the scheduler is driven by the test's own ticks instead of a 60 s timer.
func newAutoFixture(t *testing.T) *autoFixture {
	t.Helper()
	tester := &autoTester{}
	c := newTestController(t, tester)
	route := seedLocalRoute(t, c)
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	c.ConfigureRouteData(directory, uint32(os.Getuid()))
	c.physical = &physicalOwner{changed: make(chan struct{}), current: &paxv1.PhysicalEgress{}, sample: func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "home"}, nil
	}}
	c.physical.publishLocalSample(network.Physical{Name: "en0", Index: 1, Epoch: "home"}, nil)
	f := &autoFixture{c: c, tester: tester, route: route, ticks: make(chan time.Time), rounds: make(chan string, 8)}
	c.autoLatencyTicks = f.ticks
	c.autoLatencyObserve = func(outcome string) { f.rounds <- outcome }
	c.ConfigureRouteTester(tester, maxParallelTests) // restarts the scheduler with the injected ticks
	f.setProxy(t, true, false)
	return f
}

func (f *autoFixture) setProxy(t *testing.T, on, sleeping bool) {
	t.Helper()
	err := f.c.query(context.Background(), func(context.Context) {
		f.c.proxy.desired.Store(on)
		f.c.proxy.sleeping = sleeping
		f.c.proxy.running = runningBinding{}
		if on {
			f.c.proxy.running = runningBinding{route: f.route.Id, tag: "tag", session: "s"}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

// round delivers one tick and returns the scheduler's verdict for that round.
func (f *autoFixture) round(t *testing.T) string {
	t.Helper()
	f.tick(t)
	return f.verdict(t)
}
func (f *autoFixture) tick(t *testing.T) {
	t.Helper()
	select {
	case f.ticks <- time.Now():
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not take the tick")
	}
}
func (f *autoFixture) verdict(t *testing.T) string {
	t.Helper()
	select {
	case v := <-f.rounds:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler round did not finish")
		return ""
	}
}

func (f *autoFixture) history(t *testing.T, metric string) []*paxv1.RouteHistorySample {
	t.Helper()
	response, err := f.c.QueryRouteHistory(context.Background(), &paxv1.RouteHistoryRequest{RouteId: f.route.Id, Metric: metric, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return response.Items
}

func TestAutoLatencyRecordsStableSampleForCurrentRoute(t *testing.T) {
	f := newAutoFixture(t)
	f.tester.queue = []time.Duration{140 * time.Millisecond}
	if v := f.round(t); v != "recorded" {
		t.Fatalf("verdict %q", v)
	}
	items := f.history(t, "latency_auto")
	if len(items) != 1 || !items[0].Success || items[0].Value != 140 || items[0].Network != "home" || items[0].ConfigRevision == 0 {
		t.Fatalf("items %+v", items)
	}
	if len(f.history(t, "connect")) != 0 || len(f.history(t, "latency")) != 0 {
		t.Fatal("automatic sample leaked into manual metrics")
	}
}

func TestAutoLatencyDoesNotMeasureWhenProxyOff(t *testing.T) {
	f := newAutoFixture(t)
	f.setProxy(t, false, false)
	if v := f.round(t); v != "proxy_off" {
		t.Fatalf("verdict %q", v)
	}
	if f.tester.count() != 0 || len(f.history(t, "latency_auto")) != 0 {
		t.Fatal("measured with proxy off")
	}
}

func TestAutoLatencyPausesWhileSleepingOrOfflineThenResumes(t *testing.T) {
	f := newAutoFixture(t)
	f.setProxy(t, true, true)
	if v := f.round(t); v != "sleeping" {
		t.Fatalf("verdict %q", v)
	}
	f.setProxy(t, true, false)
	f.c.physical.publishLocalSample(network.Physical{}, errors.New("no default route"))
	if v := f.round(t); v != "network_down" {
		t.Fatalf("verdict %q", v)
	}
	if f.tester.count() != 0 {
		t.Fatal("measured while paused")
	}
	f.c.physical.publishLocalSample(network.Physical{Name: "en0", Index: 1, Epoch: "home"}, nil)
	if v := f.round(t); v != "recorded" {
		t.Fatalf("verdict after resume %q", v)
	}
	if f.tester.count() != 1 || len(f.history(t, "latency_auto")) != 1 {
		t.Fatal("did not resume on the next period")
	}
}

func TestAutoLatencySkipsOnlyWhileSameRouteIsBeingTestedManually(t *testing.T) {
	f := newAutoFixture(t)
	mark := func(id string) {
		t.Helper()
		if err := f.c.query(context.Background(), func(context.Context) {
			delete(f.c.routes.testing, f.route.Id)
			delete(f.c.routes.testing, "another-route")
			if id != "" {
				f.c.routes.testing[id] = "manual-op"
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	mark(f.route.Id)
	if v := f.round(t); v != "manual_test" || f.tester.count() != 0 {
		t.Fatalf("verdict %q calls %d", v, f.tester.count())
	}
	mark("another-route")
	if v := f.round(t); v != "recorded" || f.tester.count() != 1 {
		t.Fatalf("verdict %q calls %d", v, f.tester.count())
	}
	mark("")
}

func TestAutoLatencyFailureKeepsErrorCodeNotZeroSuccess(t *testing.T) {
	f := newAutoFixture(t)
	f.tester.err = &measurement.Failure{Code: "proxy_unreachable"}
	if v := f.round(t); v != "recorded" {
		t.Fatalf("verdict %q", v)
	}
	items := f.history(t, "latency_auto")
	if len(items) != 1 || items[0].Success || items[0].Error.GetCode() != "proxy_unreachable" {
		t.Fatalf("items %+v", items)
	}
}

func TestAutoLatencyDropsSampleWhenConfigChangedDuringMeasurement(t *testing.T) {
	f := newAutoFixture(t)
	f.tester.entered, f.tester.release = make(chan struct{}), make(chan struct{})
	f.tick(t)
	<-f.tester.entered
	if err := f.c.query(context.Background(), func(ctx context.Context) {
		for i := range f.c.routes.catalog.Routes {
			if f.c.routes.catalog.Routes[i].Config.ID == f.route.Id {
				f.c.routes.catalog.Routes[i].Revision++
				f.c.routes.catalog.Routes[i].ConfigRevision++
			}
		}
		f.c.publishCatalog(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	close(f.tester.release)
	if v := f.verdict(t); v != "dropped" {
		t.Fatalf("verdict %q", v)
	}
	if len(f.history(t, "latency_auto")) != 0 {
		t.Fatal("old-version sample recorded")
	}
	f.tester.entered = nil
	if v := f.round(t); v != "recorded" {
		t.Fatalf("verdict %q", v)
	}
	items := f.history(t, "latency_auto")
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	snapshot, _ := f.c.GetSnapshot(context.Background(), &paxv1.GetSnapshotRequest{})
	if items[0].ConfigRevision != snapshot.Routes[0].ConfigRevision {
		t.Fatalf("sample version %d, route version %d", items[0].ConfigRevision, snapshot.Routes[0].ConfigRevision)
	}
}

func TestAutoLatencyDropsSampleWhenNetworkChangedDuringMeasurement(t *testing.T) {
	f := newAutoFixture(t)
	f.tester.entered, f.tester.release = make(chan struct{}), make(chan struct{})
	f.tick(t)
	<-f.tester.entered
	f.c.physical.sample = func(context.Context) (network.Physical, error) { return network.Physical{}, errors.New("changing") }
	close(f.tester.release)
	if v := f.verdict(t); v != "dropped" {
		t.Fatalf("verdict %q", v)
	}
	if len(f.history(t, "latency_auto")) != 0 {
		t.Fatal("sample across a network change was recorded")
	}
}

func TestAutoLatencyLeavesManualSummaryResultsAndHistoryUntouched(t *testing.T) {
	f := newAutoFixture(t)
	ctx := context.Background()
	f.tester.queue = []time.Duration{111 * time.Millisecond, 222 * time.Millisecond}
	if v := f.round(t); v != "recorded" {
		t.Fatalf("verdict %q", v)
	}
	snapshot, _ := f.c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if snapshot.Routes[0].TestSummary != nil || len(snapshot.TestingRouteIds) != 0 {
		t.Fatalf("auto run changed catalog view: %+v %v", snapshot.Routes[0].TestSummary, snapshot.TestingRouteIds)
	}
	var results int
	if err := f.c.query(ctx, func(context.Context) { results = len(f.c.routes.testResults) }); err != nil || results != 0 {
		t.Fatalf("testResults=%d err=%v", results, err)
	}
	events, stop := operationSink(f.c)
	defer stop()
	ack, err := f.c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: f.route.Id, ExpectedRouteRevision: f.route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, events, ack.OperationId); !r.Ok {
		t.Fatal(r)
	}
	summary := waitComplete(t, f.c).Routes[0].TestSummary
	if summary == nil || summary.StableMs != 222 {
		t.Fatalf("summary %+v", summary)
	}
	manual := f.history(t, "latency")
	if len(manual) != 1 || manual[0].Value != 222 {
		t.Fatalf("manual history %+v", manual)
	}
	if auto := f.history(t, "latency_auto"); len(auto) != 1 || auto[0].Value != 111 {
		t.Fatalf("auto history %+v", auto)
	}
}

func TestAutoLatencyStopsWithDaemonLifetime(t *testing.T) {
	f := newAutoFixture(t)
	if v := f.round(t); v != "recorded" {
		t.Fatalf("verdict %q", v)
	}
	f.c.proxy.cancel()
	select {
	case <-f.c.tests.done:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler outlived the daemon lifetime")
	}
	select {
	case f.ticks <- time.Now():
		t.Fatal("scheduler still reading ticks after stop")
	case <-time.After(100 * time.Millisecond):
	}
	if f.tester.count() != 1 {
		t.Fatalf("measured after stop: %d calls", f.tester.count())
	}
}

func TestAutoLatencyStopsWhileMeasuring(t *testing.T) {
	f := newAutoFixture(t)
	f.tester.entered, f.tester.release = make(chan struct{}), make(chan struct{})
	f.tick(t)
	<-f.tester.entered
	f.c.proxy.cancel()
	select {
	case <-f.c.tests.done:
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight automatic measurement kept the daemon alive")
	}
}

func TestAutoLatencyQueryReturnsLatest120ByDefault(t *testing.T) {
	f := newAutoFixture(t)
	for i := 0; i < 121; i++ {
		f.tester.queue = append(f.tester.queue, time.Duration(i+1)*time.Millisecond)
	}
	for i := 0; i < 121; i++ {
		if v := f.round(t); v != "recorded" {
			t.Fatalf("round %d: %q", i, v)
		}
	}
	response, err := f.c.QueryRouteHistory(context.Background(), &paxv1.RouteHistoryRequest{RouteId: f.route.Id, Metric: "latency_auto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 120 || response.NextCursor != "" {
		t.Fatalf("items=%d cursor=%q", len(response.Items), response.NextCursor)
	}
	if first, last := response.Items[0], response.Items[119]; first.Value != 121 || last.Value != 2 {
		t.Fatalf("newest=%v oldest=%v, want 121 and 2", first.Value, last.Value)
	}
}
