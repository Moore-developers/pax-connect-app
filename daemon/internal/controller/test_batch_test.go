package controller

import (
	"context"
	"fmt"
	"google.golang.org/grpc/status"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/singbox"
	"sync"
	"testing"
	"time"
)

type batchDownloadFixture struct {
	fakeTester
	mu      sync.Mutex
	servers []string
	started chan struct{}
	release chan struct{}
}

func (f *batchDownloadFixture) DownloadRoute(ctx context.Context, p config.RouteConfig) (config.DownloadMeasurement, error) {
	f.mu.Lock()
	first := len(f.servers) == 0
	f.servers = append(f.servers, p.Server)
	f.mu.Unlock()
	if first {
		close(f.started)
		select {
		case <-ctx.Done():
			return config.DownloadMeasurement{}, ctx.Err()
		case <-f.release:
		}
	}
	if p.Name == "Fail" {
		return config.DownloadMeasurement{}, singbox.ErrProbe
	}
	return config.DownloadMeasurement{Bytes: 8 * 1024 * 1024, Duration: time.Second, BytesPerSecond: 8 * 1024 * 1024}, nil
}
func seedBatch(t *testing.T, c *Controller) []*paxv1.RouteTestTarget {
	t.Helper()
	results, stop := operationSink(c)
	defer stop()
	for i, name := range []string{"First", "Fail", "Last"} {
		ack, err := c.AddRoute(context.Background(), &paxv1.RouteEditRequest{DocumentJson: fmt.Sprintf(`{"type":"trojan","tag":%q,"server":%q,"server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.example.test"}}`, name, fmt.Sprintf("node%d.test", i))})
		if err != nil {
			t.Fatal(err)
		}
		if result := awaitResult(t, results, ack.OperationId); !result.Ok {
			t.Fatal(result)
		}
	}
	var targets []*paxv1.RouteTestTarget
	for _, node := range waitComplete(t, c).Routes {
		targets = append(targets, &paxv1.RouteTestTarget{RouteId: node.Id, ExpectedRouteRevision: node.Revision})
	}
	return targets
}
func TestBatchDownloadSurvivesClientCancelAndContinuesAfterFailure(t *testing.T) {
	f := &batchDownloadFixture{started: make(chan struct{}), release: make(chan struct{})}
	c := newTestController(t, f)
	// This test edits an actually queued second route, so explicitly select one
	// download worker instead of racing the production default of eight.
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	preferences, _ := c.GetMeasurementPreferences(context.Background(), nil)
	preferences.DownloadMode = "manual"
	preferences.DownloadParallel = 1
	if _, err := c.SaveMeasurementPreferences(context.Background(), preferences); err != nil {
		t.Fatal(err)
	}

	targets := seedBatch(t, c)
	results, stop := operationSink(c)
	defer stop()
	client, cancel := context.WithCancel(context.Background())
	ack, err := c.TestRoute(client, &paxv1.TestRouteRequest{Kind: "download", Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	cancel()
	snapshot := waitComplete(t, c)
	if len(snapshot.DownloadingRouteIds) != 3 || snapshot.DownloadOperationId != ack.OperationId {
		t.Fatal("batch snapshot missing")
	}
	// A queued route is immutable even if its saved document changes meanwhile.
	edit, err := c.UpdateRoute(context.Background(), &paxv1.RouteEditRequest{RouteId: targets[1].RouteId, ExpectedRevision: targets[1].ExpectedRouteRevision,
		DocumentJson: `{"type":"trojan","tag":"Fail","server":"changed.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.example.test"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, edit.OperationId); !result.Ok {
		t.Fatal(result)
	}
	close(f.release)
	result := awaitResult(t, results, ack.OperationId)
	if result.Error.GetCode() != "test_batch_partial" {
		t.Fatal(result)
	}
	snapshot = waitComplete(t, c)
	for _, node := range snapshot.Routes {
		if node.DownloadSummary == nil || node.DownloadSummary.Success == (node.Name == "Fail") {
			t.Fatal("per-route result missing", node.Name)
		}
	}
	if len(snapshot.DownloadingRouteIds) != 0 {
		t.Fatal("batch still running")
	}
	f.mu.Lock()
	count := len(f.servers)
	changedMeasured := false
	for _, server := range f.servers {
		if server == "changed.test" {
			changedMeasured = true
		}
	}
	f.mu.Unlock()
	if count != 3 {
		t.Fatal("did not test all routes", count)
	}
	if changedMeasured {
		t.Fatal("batch measured edited config")
	}
	shutdown, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}
func TestBatchDownloadStopCancelsRemainingAndBoundsAdmission(t *testing.T) {
	f := &batchDownloadFixture{started: make(chan struct{}), release: make(chan struct{})}
	c := newTestController(t, f)
	targets := seedBatch(t, c)
	results, stop := operationSink(c)
	defer stop()
	if _, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Kind: "download", Targets: append(targets, targets[0])}); status.Convert(err).Message() != "test_batch_invalid" {
		t.Fatal(err)
	}
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Kind: "download", Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	if _, err := c.CancelOperation(context.Background(), &paxv1.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Error.GetCode() != "operation_cancelled" {
		t.Fatal(result)
	}
	f.mu.Lock()
	count := len(f.servers)
	f.mu.Unlock()
	if count > 3 {
		t.Fatal("cancelled queue exceeded frozen targets", count)
	}
	if len(waitComplete(t, c).DownloadingRouteIds) != 0 {
		t.Fatal("cancel did not clear flags")
	}
	shutdown, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestBatchDownloadShutdownTerminatesParent(t *testing.T) {
	f := &batchDownloadFixture{started: make(chan struct{}), release: make(chan struct{})}
	c := newTestController(t, f)
	targets := seedBatch(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{Kind: "download", Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	shutdown, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Error.GetCode() != "operation_cancelled" {
		t.Fatal(result)
	}
	snapshot := waitComplete(t, c)
	if len(snapshot.DownloadingRouteIds) != 0 || snapshot.DownloadOperationId != "" {
		t.Fatal("shutdown retained pending measurements")
	}
}
