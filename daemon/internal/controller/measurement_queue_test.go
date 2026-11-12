package controller

import (
	"context"
	"fmt"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"sync"
	"testing"
	"time"
)

func seedMeasurementScope(t *testing.T, c *Controller, count int) []*paxv1.RouteTestTarget {
	t.Helper()
	results, stop := operationSink(c)
	defer stop()
	for i := 0; i < count; i++ {
		ack, err := c.AddRoute(context.Background(), &paxv1.RouteEditRequest{DocumentJson: fmt.Sprintf(`{"type":"trojan","tag":"Queue-%d","server":"queue-%d.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.test"}}`, i, i)})
		if err != nil {
			t.Fatal(err)
		}
		if !awaitResult(t, results, ack.OperationId).Ok {
			t.Fatal("fixture import rejected")
		}
	}
	var targets []*paxv1.RouteTestTarget
	for _, n := range waitComplete(t, c).Routes {
		targets = append(targets, &paxv1.RouteTestTarget{RouteId: n.Id, ExpectedRouteRevision: n.Revision})
	}
	return targets
}

type queuedLatency struct {
	fakeTester
	started chan time.Duration
	release chan struct{}
}

func (f *queuedLatency) MeasureRoute(ctx context.Context, _ config.RouteConfig, report func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	deadline, _ := ctx.Deadline()
	f.started <- time.Until(deadline)
	select {
	case <-f.release:
	case <-ctx.Done():
		return config.RouteMeasurement{}, ctx.Err()
	}
	return config.RouteMeasurement{Duration: time.Millisecond, Method: "https204-dual-v2"}, nil
}
func TestLatencyDeadlineStartsAfterAdmission(t *testing.T) {
	f := &queuedLatency{started: make(chan time.Duration, 2), release: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	prefs, _ := c.GetMeasurementPreferences(context.Background(), nil)
	prefs.LatencyMode = "manual"
	prefs.LatencyParallel = 1
	if _, err := c.SaveMeasurementPreferences(context.Background(), prefs); err != nil {
		t.Fatal(err)
	}
	targets := seedMeasurementScope(t, c, 2)
	results, stop := operationSink(c)
	defer stop()
	first, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[0].RouteId, ExpectedRouteRevision: targets[0].ExpectedRouteRevision})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	second, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[1].RouteId, ExpectedRouteRevision: targets[1].ExpectedRouteRevision})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	close(f.release)
	remaining := <-f.started
	if remaining < measurement.LatencyTimeout-100*time.Millisecond {
		t.Errorf("queue consumed execution timeout: remaining=%v", remaining)
	}
	awaitResult(t, results, first.OperationId)
	awaitResult(t, results, second.OperationId)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}

type sequencedDownloads struct {
	fakeTester
	mu          sync.Mutex
	calls       int
	firstTwenty chan struct{}
	release     chan struct{}
	later       chan struct{}
	checkpoint  func() bool
}

func (f *sequencedDownloads) DownloadRoute(ctx context.Context, _ config.RouteConfig) (config.DownloadMeasurement, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	if n == 20 {
		close(f.firstTwenty)
	}
	if n == 21 {
		close(f.later)
	}
	f.mu.Unlock()
	if n == 21 && f.checkpoint != nil && !f.checkpoint() {
		return config.DownloadMeasurement{}, fmt.Errorf("previous chunk not persisted")
	}
	if n >= 17 && n <= 20 {
		select {
		case <-f.release:
		case <-ctx.Done():
			return config.DownloadMeasurement{}, ctx.Err()
		}
	}
	return config.DownloadMeasurement{Bytes: 1024 * 1024, Duration: time.Second, BytesPerSecond: 1024 * 1024}, nil
}
func TestDownloadScopeContinuesAfterTwentyAndCheckpoints(t *testing.T) {
	f := &sequencedDownloads{firstTwenty: make(chan struct{}), release: make(chan struct{}), later: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	targets := seedMeasurementScope(t, c, 41)
	store := &countedMeasurements{}
	c.ConfigureMeasurements(store)
	f.checkpoint = func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.saves == 1 && len(store.document.Records) == 20
	}
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Kind: "download", Targets: targets})
	if err != nil {
		t.Fatal("whole scope rejected", err)
	}
	select {
	case <-f.firstTwenty:
	case <-time.After(time.Second):
		t.Fatal("first twenty not dispatched")
	}
	select {
	case <-f.later:
		t.Error("next batch started before first batch finished")
	default:
	}
	close(f.release)
	if r := awaitResult(t, results, ack.OperationId); !r.Ok {
		t.Fatal(r.Error.GetCode())
	}
	f.mu.Lock()
	calls := f.calls
	f.mu.Unlock()
	store.mu.Lock()
	saves, records := store.saves, len(store.document.Records)
	store.mu.Unlock()
	if calls != 41 || records != 41 || saves != 3 {
		t.Fatal("scope or batch checkpoints incomplete", calls, records, saves)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadScopeStopDoesNotDispatchFollowingChunks(t *testing.T) {
	f := &sequencedDownloads{firstTwenty: make(chan struct{}), release: make(chan struct{}), later: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	targets := seedMeasurementScope(t, c, 41)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Kind: "download", Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.firstTwenty:
	case <-time.After(time.Second):
		t.Fatal("first chunk not reached")
	}
	if _, err := c.CancelOperation(context.Background(), &paxv1.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); r.Error.GetCode() != "operation_cancelled" {
		t.Fatal(r)
	}
	f.mu.Lock()
	calls := f.calls
	f.mu.Unlock()
	if calls != 20 {
		t.Fatal("cancel dispatched later chunk", calls)
	}
	if len(waitComplete(t, c).DownloadingRouteIds) != 0 {
		t.Fatal("pending queue not cleared")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}
