package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/singbox"
)

type fakeTester struct {
	mu        sync.Mutex
	active    int
	maxActive int
	block     bool
	duration  time.Duration
	err       error
	started   chan struct{}
}

func (f *fakeTester) TestRoute(ctx context.Context, _ config.DebugProfile) (time.Duration, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	started, block := f.started, f.block
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if started != nil {
		started <- struct{}{}
	}
	if block {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	f.mu.Lock()
	duration, err := f.duration, f.err
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return duration, nil
}

func newTestController(t *testing.T, tester RouteTester) *Controller {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := New("test", "")
	c.ConfigureCatalog(&memoryCatalog{catalog: config.NewCatalog(), exists: true})
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) {
		return config.DebugProfile{}, config.ErrProfileUnavailable
	})
	// 未确认跳过的判定依赖真实 TLS 策略仓库；用私有临时目录，不触网。
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	c.ConfigureTLS(config.NewTLSPolicyStore(filepath.Join(directory, "tls.json"), uint32(os.Getuid())))
	c.ConfigureRouteTester(tester, maxParallelTests)
	return c
}

func seedLocalRoute(t *testing.T, c *Controller) *paxv1.NodeProfile {
	t.Helper()
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{
		Name: "本地线路", Protocol: "hysteria2", Server: "203.0.113.10", Port: 443,
		Password: "secret", Transport: "quic", Security: "tls", ServerName: "203.0.113.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	waitComplete(t, c)
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 1 {
		t.Fatalf("routes=%d", len(snapshot.Routes))
	}
	return snapshot.Routes[0]
}

func TestRouteTestSuccessResultAndStaleness(t *testing.T) {
	tester := &fakeTester{duration: 250 * time.Millisecond}
	c := newTestController(t, tester)
	ctx := context.Background()
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()

	ack, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.TestingRouteIds) != 1 || snapshot.TestingRouteIds[0] != route.Id {
		t.Fatalf("testing ids=%v", snapshot.TestingRouteIds)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("test failed")
	}
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.TestingRouteIds) != 0 {
		t.Fatalf("testing not cleared: %v", snapshot.TestingRouteIds)
	}
	summary := snapshot.Routes[0].TestSummary
	if summary == nil || !summary.Success || summary.DurationMs != 250 || summary.TestedRevision != route.Revision {
		t.Fatalf("summary=%+v", summary)
	}

	// 配置变化后旧测量展示过期：结果保留，revision 对比由界面判定。
	edited, err := c.UpdateRoute(ctx, &paxv1.RouteEditRequest{
		RouteId: route.Id, ExpectedRevision: route.Revision, Name: "本地线路二",
		Protocol: "hysteria2", Server: "203.0.113.10", Port: 443, Transport: "quic",
		Security: "tls", ServerName: "203.0.113.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, edited.OperationId)
	waitComplete(t, c)
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	summary = snapshot.Routes[0].TestSummary
	if summary == nil || summary.TestedRevision == snapshot.Routes[0].Revision {
		t.Fatalf("stale marker missing: %+v", summary)
	}
}

func TestRouteTestClassifiesFailureAndSupportsCancel(t *testing.T) {
	tester := &fakeTester{err: &singbox.CertificateError{Reason: "expired"}}
	c := newTestController(t, tester)
	ctx := context.Background()
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()

	ack, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if result.Ok || result.Error.GetCode() != "tls_certificate_invalid" || result.Error.GetMessageParams()[0] != "expired" {
		t.Fatalf("result=%+v", result)
	}
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	summary := snapshot.Routes[0].TestSummary
	if summary.Success || summary.Error.GetCode() != "tls_certificate_invalid" {
		t.Fatalf("summary=%+v", summary)
	}

	blocking := &fakeTester{block: true}
	c.ConfigureRouteTester(blocking, maxParallelTests)
	cancelled, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: snapshot.Routes[0].Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: cancelled.OperationId}); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, cancelled.OperationId); result.Ok || result.Error.GetCode() != "operation_cancelled" {
		t.Fatalf("cancel result=%+v", result)
	}
	snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.TestingRouteIds) != 0 {
		t.Fatalf("cancel left testing state: %v", snapshot.TestingRouteIds)
	}
	if _, err := c.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: "operation-missing"}); err == nil {
		t.Fatal("unknown operation accepted")
	}
}

func TestRouteTestRejectsUnconfirmedSkipAndStaleRevision(t *testing.T) {
	c := newTestController(t, &fakeTester{duration: time.Millisecond})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	// 建议 SKIP 且未确认的线路：先拒绝，不借测试跳过。
	ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{
		Name: "跳过建议", Protocol: "vmess", Server: "203.0.113.11", Port: 443,
		Uuid: uuidA, Transport: "ws", Security: "tls", ServerName: "203.0.113.11",
		TlsSkipSuggested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	waitComplete(t, c)
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	route := snapshot.Routes[0]
	if route.TlsPolicy.GetNeedsConfirmation() || route.TlsPolicy.Mode != paxv1.TLSMode_TLS_MODE_SKIP_VERIFY {
		t.Fatal("source hint became consent")
	}
	ack, err = c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	if _, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision + 9}); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: "missing", ExpectedRouteRevision: 1}); err == nil {
		t.Fatal("unknown route accepted")
	}
}

func TestRouteTestBoundsConcurrencyAndDedupsSameRoute(t *testing.T) {
	tester := &fakeTester{block: true, started: make(chan struct{}, maxParallelTests+1)}
	c := newTestController(t, tester)
	ctx := context.Background()
	route := seedLocalRoute(t, c)

	first, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil || again.OperationId != first.OperationId {
		t.Fatalf("dedup failed: %v %+v", err, again)
	}

	// Populate one more target than the global admission budget.
	for i := 2; i <= maxParallelTests+1; i++ {
		ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{
			Name: fmt.Sprintf("线路%d", i), Protocol: "hysteria2", Server: fmt.Sprintf("203.0.113.%d", i),
			Port: 443, Password: "secret", Transport: "quic", Security: "tls", ServerName: fmt.Sprintf("203.0.113.%d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		results, stop := operationSink(c)
		awaitResult(t, results, ack.OperationId)
		stop()
	}
	waitComplete(t, c)
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	started := 0
	var extra *paxv1.OperationAck
	for _, candidate := range snapshot.Routes {
		ack, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: candidate.Id, ExpectedRouteRevision: candidate.Revision})
		if err != nil {
			t.Fatalf("unexpected rejection for %s: %v", candidate.Id, err)
		}
		started++
		extra = ack
	}
	if started != len(snapshot.Routes) {
		t.Fatalf("started=%d want=%d", started, len(snapshot.Routes))
	}
	// The extra test waits; observed concurrency must match the bounded pool.
	deadline := time.After(2 * time.Second)
	for i := 0; i < maxParallelTests; i++ {
		select {
		case <-tester.started:
		case <-deadline:
			t.Fatal("tests did not start")
		}
	}
	tester.mu.Lock()
	maxActive := tester.maxActive
	tester.mu.Unlock()
	if maxActive > maxParallelTests {
		t.Fatalf("concurrency exceeded: %d", maxActive)
	}
	if _, err := c.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: extra.OperationId}); err != nil {
		t.Fatal(err)
	}
}

func TestRouteTestDropsResultOfDeletedRoute(t *testing.T) {
	tester := &fakeTester{duration: time.Millisecond}
	c := newTestController(t, tester)
	ctx := context.Background()
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()

	ack, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := c.DeleteRoute(ctx, &paxv1.DeleteRouteRequest{RouteId: route.Id, ExpectedRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	_ = deleted
	// 两个操作并发完成：无序收集，不能假设结果先后。
	collected := map[string]*paxv1.OperationResult{}
	deadline := time.After(5 * time.Second)
	for len(collected) < 2 {
		select {
		case result := <-results:
			collected[result.OperationId] = result
		case <-deadline:
			t.Fatalf("missing results: %v", collected)
		}
	}
	for id, result := range collected {
		if id != deleted.OperationId && id != ack.OperationId {
			t.Fatalf("unexpected operation %s", id)
		}
		if id == deleted.OperationId && !result.Ok {
			t.Fatalf("delete failed: %+v", result)
		}
		// If deletion wins admission, the probe is now explicitly revoked; if the
		// probe already committed, its result still disappears with the route.
		if id == ack.OperationId && !result.Ok && result.Error.GetCode() != "operation_cancelled" {
			t.Fatalf("unexpected probe terminal: %+v", result)
		}
	}
	waitComplete(t, c)
	snapshot, _ := c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
	if len(snapshot.Routes) != 0 {
		t.Fatalf("routes=%d", len(snapshot.Routes))
	}
}

type measuredTester struct {
	fakeTester
	exit      string
	exitError string
}

func (m *measuredTester) MeasureRoute(context.Context, config.RouteConfig, func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	return config.RouteMeasurement{Duration: 120 * time.Millisecond, ExitIP: m.exit, ExitError: m.exitError}, nil
}
func TestExplicitImportAutomaticallyMeasuresWithoutSelectingRunningPath(t *testing.T) {
	for _, exitFailure := range []bool{false, true} {
		tester := &measuredTester{exit: "8.8.8.8"}
		if exitFailure {
			tester.exit = ""
			tester.exitError = "exit_query_failed"
		}
		c := newTestController(t, tester)
		results, stop := operationSink(c)
		preview, err := c.PreviewRoutes(context.Background(), &paxv1.PreviewRoutesRequest{Source: "trojan://fixture@example.test:443#Import"})
		if err != nil {
			t.Fatal(err)
		}
		ack, err := c.ConfirmImportRoutes(context.Background(), &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
		if err != nil {
			t.Fatal(err)
		}
		awaitResult(t, results, ack.OperationId)
		deadline := time.Now().Add(time.Second)
		var snapshot *paxv1.Snapshot
		for time.Now().Before(deadline) {
			snapshot, _ = c.GetSnapshot(context.Background(), &paxv1.GetSnapshotRequest{})
			if len(snapshot.Routes) > 0 && snapshot.Routes[0].TestSummary != nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		summary := snapshot.Routes[0].GetTestSummary()
		if !summary.GetSuccess() || summary.GetDurationMs() != 120 || summary.GetExitIp() != "" {
			t.Fatal("automatic facts missing", summary)
		}
		if exitFailure && summary.GetExitError() != nil {
			t.Fatal("exit failure hid latency success")
		}
		if snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF || snapshot.CurrentRoute != nil {
			t.Fatal("measurement started proxy")
		}
		stop()
		closeCtx, done := context.WithTimeout(context.Background(), time.Second)
		_ = c.CloseProxy(closeCtx)
		done()
	}
}

type consentMeasurer struct {
	fakeTester
	entered chan struct{}
	release chan struct{}
}

func (m *consentMeasurer) MeasureRoute(ctx context.Context, p config.RouteConfig, report func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	if p.TLSResolved.Mode == config.TLSStrict {
		close(m.entered)
		select {
		case <-m.release:
		case <-ctx.Done():
			return config.RouteMeasurement{}, ctx.Err()
		}
		return config.RouteMeasurement{Duration: time.Millisecond, ExitIP: "8.8.8.8"}, nil
	}
	return config.RouteMeasurement{Duration: 2 * time.Millisecond, ExitIP: "8.8.4.4"}, nil
}
func TestConsentDuringOldMeasurementSchedulesNewVersion(t *testing.T) {
	tester := &consentMeasurer{entered: make(chan struct{}), release: make(chan struct{})}
	c := newTestController(t, tester)
	route := seedLocalRoute(t, c)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	if _, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	<-tester.entered
	ack, err := c.UpdateRouteTLS(ctx, &paxv1.UpdateRouteTLSRequest{RouteId: route.Id, IdentityToken: route.TlsPolicy.IdentityToken, ExpectedRevision: route.TlsPolicy.PolicyRevision, Mode: paxv1.TLSMode_TLS_MODE_SKIP_VERIFY, ConfirmSkip: true})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	close(tester.release)
	deadline := time.Now().Add(time.Second)
	var snapshot *paxv1.Snapshot
	for time.Now().Before(deadline) {
		snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		summary := snapshot.Routes[0].TestSummary
		if summary != nil && summary.DurationMs == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	node := snapshot.Routes[0]
	if node.GetTestSummary().GetDurationMs() != 2 || node.TestSummary.TestedConfigRevision != node.ConfigRevision {
		t.Fatal("new consent lost behind old operation")
	}
	closeCtx, done := context.WithTimeout(ctx, time.Second)
	defer done()
	if err := c.CloseProxy(closeCtx); err != nil {
		t.Fatal(err)
	}
}

type cleanupTester struct {
	entered chan struct{}
	closing chan struct{}
	release chan struct{}
}

func (m *cleanupTester) TestRoute(ctx context.Context, _ config.RouteConfig) (time.Duration, error) {
	close(m.entered)
	<-ctx.Done()
	close(m.closing)
	<-m.release
	return 0, ctx.Err()
}
func TestCloseWaitsForActualProbeCleanupWithinDeadline(t *testing.T) {
	tester := &cleanupTester{entered: make(chan struct{}), closing: make(chan struct{}), release: make(chan struct{})}
	c := newTestController(t, tester)
	route := seedLocalRoute(t, c)
	if _, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	<-tester.entered
	ctx, done := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer done()
	if err := c.CloseProxy(ctx); err == nil {
		t.Fatal("close returned before probe cleanup")
	}
	<-tester.closing
	close(tester.release)
	ctx2, done2 := context.WithTimeout(context.Background(), time.Second)
	defer done2()
	if err := c.CloseProxy(ctx2); err != nil {
		t.Fatal(err)
	}
}

type downloadFixture struct {
	fakeTester
	started     chan struct{}
	block       bool
	measurement config.DownloadMeasurement
	err         error
}

func (f *downloadFixture) DownloadRoute(ctx context.Context, _ config.RouteConfig) (config.DownloadMeasurement, error) {
	if f.started != nil {
		close(f.started)
	}
	if f.block {
		<-ctx.Done()
		return config.DownloadMeasurement{}, ctx.Err()
	}
	return f.measurement, f.err
}
func TestExplicitDownloadPublishesSeparateVersionedSpeed(t *testing.T) {
	fixture := &downloadFixture{measurement: config.DownloadMeasurement{Bytes: 8 * 1024 * 1024, Duration: 2 * time.Second, BytesPerSecond: 4194304}}
	c := newTestController(t, fixture)
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	snapshot := waitComplete(t, c)
	summary := snapshot.Routes[0].DownloadSummary
	if summary == nil || !summary.Success || summary.BytesPerSecond != 4194304 || summary.TestedConfigRevision != snapshot.Routes[0].ConfigRevision {
		t.Fatal("download facts missing")
	}
	if snapshot.Routes[0].TestSummary != nil || snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("download overwrote latency or enabled proxy")
	}
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	_ = c.CloseProxy(ctx)
}
func TestDownloadDedupsAndCancellationReleasesGlobalSlot(t *testing.T) {
	fixture := &downloadFixture{block: true, started: make(chan struct{})}
	c := newTestController(t, fixture)
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.started
	same, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision, Kind: "download"})
	if err != nil || same.OperationId != ack.OperationId {
		t.Fatal("same download not deduped")
	}
	if _, err := c.CancelOperation(context.Background(), &paxv1.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if result.Ok || result.Error.Code != "operation_cancelled" {
		t.Fatal("cancelled speed claimed success")
	}
	snapshot := waitComplete(t, c)
	summary := snapshot.Routes[0].DownloadSummary
	if summary.Success || summary.BytesPerSecond != 0 || len(snapshot.DownloadingRouteIds) != 0 {
		t.Fatal("cancelled result retained speed or running state")
	}
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	_ = c.CloseProxy(ctx)
}

func TestDifferentRouteDownloadQueuesWithoutSharingBandwidth(t *testing.T) {
	fixture := &downloadFixture{block: true, started: make(chan struct{})}
	c := newTestController(t, fixture)
	first := seedLocalRoute(t, c)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	added, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: `{"type":"trojan","tag":"Other","server":"example.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"example.test"}}`})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, added.OperationId)
	snapshot := waitComplete(t, c)
	var other *paxv1.NodeProfile
	for _, node := range snapshot.Routes {
		if node.Id != first.Id {
			other = node
		}
	}
	ack, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: first.Id, ExpectedRouteRevision: first.Revision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.started
	queued, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: other.Id, ExpectedRouteRevision: other.Revision, Kind: "download"})
	if err != nil {
		t.Fatal("different single download must queue", err)
	}
	if _, err = c.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: queued.OperationId}); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, queued.OperationId); result.Error.GetCode() != "operation_cancelled" {
		t.Fatal(result)
	}
	if _, err := c.CancelOperation(ctx, &paxv1.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	closeCtx, done := context.WithTimeout(ctx, time.Second)
	defer done()
	_ = c.CloseProxy(closeCtx)
}

func TestMeasuredImportSurvivesDaemonRestartThroughProductSnapshot(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(directory, "routes.json")
	measurementPath := filepath.Join(directory, "measurements.json")
	setup := func() *Controller {
		c := New("test", "")
		c.ConfigureCatalog(config.NewCatalogStore(catalogPath, uint32(os.Getuid())))
		c.ConfigureMeasurements(config.NewMeasurementStore(measurementPath, uint32(os.Getuid())))
		c.ConfigureOperations(context.Background(), func() (config.DebugProfile, error) { return config.DebugProfile{}, config.ErrProfileUnavailable })
		c.ConfigureRouteTester(&measuredTester{exit: "8.8.8.8"}, maxParallelTests)
		return c
	}
	c := setup()
	results, stop := operationSink(c)
	preview, err := c.PreviewRoutes(context.Background(), &paxv1.PreviewRoutesRequest{Source: "trojan://fixture@example.test:443#Persisted"})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(context.Background(), &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	deadline := time.Now().Add(time.Second)
	var snapshot *paxv1.Snapshot
	for time.Now().Before(deadline) {
		snapshot, _ = c.GetSnapshot(context.Background(), &paxv1.GetSnapshotRequest{})
		if len(snapshot.Routes) > 0 && snapshot.Routes[0].TestSummary != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	id := snapshot.Routes[0].Id
	stop()
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	if err := c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
	done()
	restored := setup()
	defer func() {
		ctx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		_ = restored.CloseProxy(ctx)
	}()
	// Query fences catalog and measurement loading before the snapshot assertion.
	_ = restored.query(context.Background(), func(context.Context) {})
	after, _ := restored.GetSnapshot(context.Background(), &paxv1.GetSnapshotRequest{})
	if len(after.Routes) != 1 || after.Routes[0].Id != id || after.Routes[0].GetTestSummary().GetExitIp() != "" || after.Routes[0].TestSummary.DurationMs != 120 || after.MeasurementError != nil {
		t.Fatal("measurements not restored through product snapshot")
	}
	if after.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF || len(after.TestingRouteIds) != 0 {
		t.Fatal("restart replayed measurement or proxy")
	}
}

type failedMeasurements struct{}

func (*failedMeasurements) Load() (config.MeasurementDocument, error) {
	return config.NewMeasurements(), nil
}
func (*failedMeasurements) Save(context.Context, config.MeasurementDocument) error {
	return config.ErrMeasurementStore
}
func TestMeasurementWriteFailureVisibleWithoutDeletingImportedRoute(t *testing.T) {
	c := newTestController(t, &measuredTester{exit: "8.8.8.8"})
	c.ConfigureMeasurements(&failedMeasurements{})
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	snapshot := waitComplete(t, c)
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].GetTestSummary().GetExitIp() != "" || snapshot.GetMeasurementError().GetCode() != "measurement_store_failed" {
		t.Fatal("persistence failure hidden or route deleted")
	}
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	_ = c.CloseProxy(ctx)
}

type transientMeasurementFailure struct {
	store  *config.MeasurementStore
	failed bool
}

func (r *transientMeasurementFailure) Load() (config.MeasurementDocument, error) {
	return r.store.Load()
}
func (r *transientMeasurementFailure) Save(ctx context.Context, doc config.MeasurementDocument) error {
	if !r.failed {
		r.failed = true
		return config.ErrMeasurementStore
	}
	return r.store.Save(ctx, doc)
}
func TestTemporaryMeasurementWriteFailureRetriesAndRestores(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	persistent := config.NewMeasurementStore(filepath.Join(directory, "measurement.json"), uint32(os.Getuid()))
	tester := &measuredTester{exit: "8.8.8.8"}
	c := newTestController(t, tester)
	c.ConfigureMeasurements(&transientMeasurementFailure{store: persistent})
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	for i := 0; i < 2; i++ {
		if i == 1 {
			tester.exit = "8.8.4.4"
		}
		ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
		if err != nil {
			t.Fatal(err)
		}
		awaitResult(t, results, ack.OperationId)
		snapshot := waitComplete(t, c)
		if i == 0 && snapshot.GetMeasurementError().GetCode() != "measurement_store_failed" {
			t.Fatal("transient error missing")
		}
		if i == 1 && snapshot.MeasurementError != nil {
			t.Fatal("write recovery did not clear error")
		}
	}
	// Reload through the controller owner, using the real protected file repository.
	c.ConfigureMeasurements(persistent)
	_ = c.query(context.Background(), func(ctx context.Context) { c.publishCatalog(ctx) })
	snapshot := waitComplete(t, c)
	if snapshot.Routes[0].GetTestSummary().GetExitIp() != "" {
		t.Fatal("second sample did not restore after transient failure")
	}
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	_ = c.CloseProxy(ctx)
}

// Follow a source's declared policy without turning a network failure into SKIP.
type sourcePolicyMeasurer struct{ fakeTester }

func (m *sourcePolicyMeasurer) MeasureRoute(_ context.Context, p config.RouteConfig, report func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	if p.TLSResolved.Mode != config.TLSSkip {
		return config.RouteMeasurement{}, &singbox.CertificateError{Reason: "untrusted"}
	}
	return config.RouteMeasurement{Duration: 120 * time.Millisecond, ExitIP: "8.8.8.8"}, nil
}
func TestImportedSourceSkipRunsWithoutConfirmationAndRemainsMarked(t *testing.T) {
	c := newTestController(t, &sourcePolicyMeasurer{})
	results, stop := operationSink(c)
	defer stop()
	ctx := context.Background()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: "trojan://fixture@example.test:443?insecure=1#Source-policy"})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	deadline := time.Now().Add(time.Second)
	var snapshot *paxv1.Snapshot
	for time.Now().Before(deadline) {
		snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		if snapshot.Routes[0].TestSummary != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	node := snapshot.Routes[0]
	if !node.GetTestSummary().GetSuccess() || node.GetTestSummary().GetExitIp() != "" || node.TlsPolicy.Mode != paxv1.TLSMode_TLS_MODE_SKIP_VERIFY || node.TlsPolicy.NeedsConfirmation || node.TlsPolicy.UserOverride {
		t.Fatal("source skip blocked, lost provenance, or erased measurement")
	}
	closeCtx, done := context.WithTimeout(ctx, time.Second)
	defer done()
	_ = c.CloseProxy(closeCtx)
}

func TestBatchConsentRequiresFreshCertificateFailureAndRemainsAtomic(t *testing.T) {
	tester := &fakeTester{err: &singbox.CertificateError{Reason: "untrusted"}}
	c := newTestController(t, tester)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: "anytls://fixture@one.test:443#One\nanytls://fixture@two.test:443#Two"})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId, preview.Items[1].ItemId}})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, results, ack.OperationId)
	deadline := time.Now().Add(time.Second)
	var snapshot *paxv1.Snapshot
	for time.Now().Before(deadline) {
		snapshot, _ = c.GetSnapshot(ctx, &paxv1.GetSnapshotRequest{})
		if len(snapshot.Routes) == 2 && snapshot.Routes[0].TestSummary != nil && snapshot.Routes[1].TestSummary != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	req := &paxv1.ConfirmRouteTLSBatchRequest{ClientId: "fixture", RequestId: "stale"}
	for _, route := range snapshot.Routes {
		if route.GetTestSummary().GetError().GetCode() != "tls_certificate_invalid" {
			t.Fatal("expected strict failure")
		}
		req.Routes = append(req.Routes, &paxv1.UpdateRouteTLSRequest{RouteId: route.Id, IdentityToken: route.TlsPolicy.IdentityToken, ExpectedRevision: route.TlsPolicy.PolicyRevision, Mode: paxv1.TLSMode_TLS_MODE_SKIP_VERIFY, ConfirmSkip: true})
	}
	req.Routes[1].ExpectedRevision++
	ack, err = c.ConfirmRouteTLSBatch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); result.Ok {
		t.Fatal("stale batch accepted")
	}
	for _, route := range waitComplete(t, c).Routes {
		if route.TlsPolicy.Mode != paxv1.TLSMode_TLS_MODE_STRICT {
			t.Fatal("stale batch partially weakened policy")
		}
	}
	req.Routes[1].ExpectedRevision--
	req.RequestId = "valid"
	tester.mu.Lock()
	tester.err = nil
	tester.duration = time.Millisecond
	tester.mu.Unlock()
	ack, err = c.ConfirmRouteTLSBatch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	for _, route := range waitComplete(t, c).Routes {
		if route.TlsPolicy.Mode != paxv1.TLSMode_TLS_MODE_SKIP_VERIFY || !route.TlsPolicy.UserOverride {
			t.Fatal("explicit consent missing")
		}
	}
	replay, err := c.ConfirmRouteTLSBatch(ctx, req)
	if err != nil || replay.OperationId != ack.OperationId {
		t.Fatal("batch request not idempotent", err)
	}
	closeCtx, done := context.WithTimeout(ctx, time.Second)
	defer done()
	_ = c.CloseProxy(closeCtx)
}

func TestDefaultCertificateFailureDoesNotBecomeSourceSkip(t *testing.T) {
	c := newTestController(t, &sourcePolicyMeasurer{})
	route := seedLocalRoute(t, c)
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(ctx, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if result.Ok || result.Error.GetCode() != "tls_certificate_invalid" {
		t.Fatal("certificate failure hidden")
	}
	after := waitComplete(t, c).Routes[0]
	if after.TlsPolicy.Mode != paxv1.TLSMode_TLS_MODE_STRICT || after.TlsPolicy.UserOverride || after.TestSummary.Success {
		t.Fatal("failed verification automatically fell back")
	}
	closeCtx, done := context.WithTimeout(ctx, time.Second)
	defer done()
	_ = c.CloseProxy(closeCtx)
}

type deadlineDownloadFixture struct {
	fakeTester
	started chan struct{}
}

func (f *deadlineDownloadFixture) DownloadRoute(ctx context.Context, _ config.RouteConfig) (config.DownloadMeasurement, error) {
	close(f.started)
	<-ctx.Done()
	return config.DownloadMeasurement{}, ctx.Err()
}
func TestDownloadDeadlineIsReportedAsTimeoutThroughRPC(t *testing.T) {
	fixture := &deadlineDownloadFixture{started: make(chan struct{})}
	c := newTestController(t, fixture)
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.TestRoute(context.Background(), &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.started
	deadline := time.After(measurement.DownloadTimeout + 2*time.Second)
	var result *paxv1.OperationResult
	for result == nil {
		select {
		case r := <-results:
			if r.OperationId == ack.OperationId {
				result = r
			}
		case <-deadline:
			t.Fatal("deadline task did not publish terminal")
		}
	}
	if result.Ok || result.GetError().GetCode() != "download_timeout" {
		t.Fatal("deadline masqueraded as user cancellation", result)
	}
	after := waitComplete(t, c)
	summary := after.Routes[0].GetDownloadSummary()
	if summary.GetSuccess() || summary.BytesPerSecond != 0 || len(after.DownloadingRouteIds) != 0 {
		t.Fatal("timeout retained speed or running marker")
	}
	closeCtx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	_ = c.CloseProxy(closeCtx)
}

type backgroundDownloadFixture struct {
	fakeTester
	entered chan struct{}
	release chan struct{}
}

func (f *backgroundDownloadFixture) DownloadRoute(ctx context.Context, _ config.RouteConfig) (config.DownloadMeasurement, error) {
	close(f.entered)
	select {
	case <-f.release:
		return config.DownloadMeasurement{Bytes: 8 * 1024 * 1024, Duration: 2 * time.Second, BytesPerSecond: 4194304}, nil
	case <-ctx.Done():
		return config.DownloadMeasurement{}, ctx.Err()
	}
}
func TestDownloadKeepsRunningAfterAdmissionClientDisconnects(t *testing.T) {
	fixture := &backgroundDownloadFixture{entered: make(chan struct{}), release: make(chan struct{})}
	c := newTestController(t, fixture)
	route := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	client, cancel := context.WithCancel(context.Background())
	ack, err := c.TestRoute(client, &paxv1.TestRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision, Kind: "download"})
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.entered
	cancel()
	snap, _ := c.GetSnapshot(context.Background(), &paxv1.GetSnapshotRequest{})
	if len(snap.DownloadingRouteIds) != 1 || snap.DownloadOperationId != ack.OperationId {
		t.Fatal("background task lost ownership on client cancellation")
	}
	close(fixture.release)
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal("client canceled daemon-owned task", result)
	}
	after := waitComplete(t, c)
	if !after.Routes[0].GetDownloadSummary().GetSuccess() || after.Routes[0].DownloadSummary.BytesPerSecond != 4194304 {
		t.Fatal("background result not synchronized")
	}
	closeCtx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	_ = c.CloseProxy(closeCtx)
}
