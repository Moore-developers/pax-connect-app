package controller

import (
	"context"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"sync"
	"testing"
	"time"
)

type singleQueueFixture struct {
	fakeTester
	mu      sync.Mutex
	calls   int
	started chan string
	release chan struct{}
}

func (f *singleQueueFixture) DownloadRoute(ctx context.Context, p config.RouteConfig) (config.DownloadMeasurement, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.started <- p.ID
	select {
	case <-ctx.Done():
		return config.DownloadMeasurement{}, ctx.Err()
	case <-f.release:
	}
	return config.DownloadMeasurement{Bytes: 512 * 1024, Duration: time.Second, BytesPerSecond: 512 * 1024}, nil
}
func TestSingleDownloadsQueueAndBatchExcludesNewJobs(t *testing.T) {
	f := &singleQueueFixture{started: make(chan string, 4), release: make(chan struct{}, 4)}
	c := newTestController(t, f)
	targets := seedMeasurementScope(t, c, 3)
	results, stop := operationSink(c)
	defer stop()
	first, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[0].RouteId, ExpectedRouteRevision: targets[0].ExpectedRouteRevision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	second, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[1].RouteId, ExpectedRouteRevision: targets[1].ExpectedRouteRevision, Kind: "download"})
	if err != nil {
		t.Fatal("second single should queue", err)
	}
	select {
	case <-f.started:
		t.Fatal("queued download ran concurrently")
	case <-time.After(30 * time.Millisecond):
	}
	f.release <- struct{}{}
	awaitResult(t, results, first.OperationId)
	if id := <-f.started; id != targets[1].RouteId {
		t.Fatal("FIFO lost")
	}
	f.release <- struct{}{}
	awaitResult(t, results, second.OperationId)
	batch, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Targets: targets[:2], Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	if _, err = c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[2].RouteId, ExpectedRouteRevision: targets[2].ExpectedRouteRevision, Kind: "download"}); err == nil {
		t.Fatal("single admitted during exclusive batch")
	}
	_, _ = c.CancelOperation(context.Background(), &paxv1.CancelOperationRequest{OperationId: batch.OperationId})
	awaitResult(t, results, batch.OperationId)
}

func TestQueuedSingleDownloadCanCancelBeforeItsTurnAndShutdown(t *testing.T) {
	f := &singleQueueFixture{started: make(chan string, 4), release: make(chan struct{}, 4)}
	c := newTestController(t, f)
	targets := seedMeasurementScope(t, c, 3)
	results, stop := operationSink(c)
	defer stop()
	_, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[0].RouteId, ExpectedRouteRevision: targets[0].ExpectedRouteRevision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	queued, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[1].RouteId, ExpectedRouteRevision: targets[1].ExpectedRouteRevision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[1].RouteId, ExpectedRouteRevision: targets[1].ExpectedRouteRevision, Kind: "download"})
	if err != nil || duplicate.OperationId != queued.OperationId {
		t.Fatal("duplicate queued download was admitted")
	}
	_, err = c.CancelOperation(context.Background(), &paxv1.CancelOperationRequest{OperationId: queued.OperationId})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, queued.OperationId); r.Error.GetCode() != "operation_cancelled" {
		t.Fatal(r)
	}
	if len(f.started) != 0 {
		t.Fatal("cancelled queue item opened a download")
	}
	_, err = c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: targets[2].RouteId, ExpectedRouteRevision: targets[2].ExpectedRouteRevision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = c.CloseProxy(ctx); err != nil {
		t.Fatal("queued work blocked shutdown", err)
	}
}
