package controller

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/singbox"
)

const (
	testLabel        = "线路测试"
	testTimeout      = measurement.LatencyTimeout
	maxParallelTests = measurement.DefaultParallel
)

// productError carries a product code plus presentation params through the single owner.
type productError struct {
	code   string
	params []string
}

func (e *productError) Error() string { return e.code }

// RouteTester is the bounded no-TUN probe boundary for one saved route; the daemon injects the
// real sing-box runtime and tests substitute their own.
// RouteMeasurer measures only latency through the frozen no-TUN route.
type RouteMeasurer interface {
	MeasureRoute(context.Context, config.RouteConfig, func(config.RouteMeasurement)) (config.RouteMeasurement, error)
}

type RouteTester interface {
	TestRoute(ctx context.Context, p config.DebugProfile) (time.Duration, error)
}

// routeTestResult is one manual test outcome. Results stay in memory: a daemon restart clears
// them instead of pretending a stale measurement is still current.
type routeTestResult struct {
	routeName      string
	measurement    config.RouteMeasurement
	at             time.Time
	exitAt         time.Time
	success        bool
	duration       time.Duration
	exitIP         string
	exitError      string
	code           string
	params         []string
	revision       uint64
	configRevision uint64
	network        string
}

// testOwner bounds manual route tests the same way subscription refreshes are bounded.
type testOwner struct {
	exitBatch            string
	singleDownloads      []singleDownload
	singleDownloadActive string
	parents              map[string]string
	batchDownload        bool
	networkChanged       map[string]bool
	lifetime             context.Context
	cancel               context.CancelFunc
	mu                   sync.Mutex
	active               map[string]string
	ops                  map[string]context.CancelFunc
	bandwidth            string
	batch                string
	latencyParallel      int
	activeLatency        int
	progress             map[string]*paxv1.RouteMeasurementProgress
	slots                chan struct{}
	automatic            chan automaticRouteTest
	workers              sync.WaitGroup
	probes               sync.WaitGroup
	closed               bool
	done                 chan struct{}
}

// ConfigureRouteTester installs the probe boundary; without it route tests stay unavailable
// instead of pretending to work.
func (c *Controller) ConfigureRouteTester(tester RouteTester, parallel int) {
	if parallel < measurement.DefaultParallel {
		parallel = measurement.DefaultParallel
	}
	if parallel > measurement.HardParallel {
		parallel = measurement.HardParallel
	}
	lifetime := context.Background()
	if c.proxy != nil {
		lifetime = c.proxy.lifetime
	}
	if c.tests != nil {
		c.tests.cancel()
		<-c.tests.done
	}
	lifetime, cancel := context.WithCancel(lifetime)
	owner := &testOwner{cancel: cancel, done: make(chan struct{}), latencyParallel: parallel,
		lifetime: lifetime, parents: map[string]string{}, progress: map[string]*paxv1.RouteMeasurementProgress{}, networkChanged: map[string]bool{}, active: map[string]string{}, ops: map[string]context.CancelFunc{},
		slots: make(chan struct{}, 100), automatic: make(chan automaticRouteTest, 1000),
	}
	for index := 0; index < 100; index++ {
		owner.slots <- struct{}{}
	}
	c.mu.Lock()
	c.tester = tester
	c.tests = owner
	c.mu.Unlock()
	for i := 0; i < measurement.HardParallel; i++ {
		owner.workers.Add(1)
		go c.automaticTestLoop(owner)
	}
	owner.workers.Add(1)
	go c.autoLatencyLoop(owner, c.autoLatencyTicks, tester)
	go func() {
		<-owner.lifetime.Done()
		owner.mu.Lock()
		owner.closed = true
		owner.mu.Unlock()
		owner.workers.Wait()
		owner.mu.Lock()
		queued := owner.singleDownloads
		owner.singleDownloads = nil
		for _, job := range queued {
			job.cancel()
			delete(owner.ops, job.id)
		}
		owner.mu.Unlock()
		for _, job := range queued {
			c.completeOperation(job.id, "下载测速", "operation_cancelled")
		}
		owner.probes.Wait()
		close(owner.done)
	}()
}

// TestRoute acknowledges one bounded real-connection test. It never creates a TUN, never
// changes the selected or current route, and never blocks catalog edits while running.
func (c *Controller) TestRoute(ctx context.Context, req *paxv1.TestRouteRequest) (*paxv1.OperationAck, error) {
	return c.mutationRequest(ctx, "test-route", req, req.ClientId, req.RequestId, func() (*paxv1.OperationAck, error) { return c.submitRouteTest(ctx, req) })
}
func (c *Controller) submitRouteTest(ctx context.Context, req *paxv1.TestRouteRequest) (*paxv1.OperationAck, error) {
	if req.Kind == "exit" {
		return c.testExit(ctx, req)
	}
	if len(req.Targets) > 0 {
		return c.testBatch(ctx, req)
	}
	if req.Kind == "download" {
		return c.testDownload(ctx, req)
	}
	if req.Kind != "" && req.Kind != "latency" && req.Kind != "diagnostic" {
		return nil, status.Error(codes.InvalidArgument, "test_kind_invalid")
	}
	if c.tester == nil || c.tests == nil || c.routes == nil || c.proxy == nil {
		return nil, status.Error(codes.FailedPrecondition, "test_unavailable")
	}
	var (
		record   config.RouteRecord
		view     = config.TLSPolicyView{Mode: config.TLSStrict}
		pending  string
		running  string
		inFlight bool
		failure  error
	)
	if err := c.query(ctx, func(ctx context.Context) {
		current, ok := c.routes.catalog.Find(req.RouteId)
		if !ok {
			failure = status.Error(codes.NotFound, "route_not_found")
			return
		}
		if current.Revision != req.ExpectedRouteRevision {
			failure = status.Error(codes.FailedPrecondition, "route_stale")
			return
		}
		resolvedRecord, resolveErr := c.resolvedRecord(ctx, current)
		if resolveErr != nil {
			failure = resolveErr
			return
		}
		current = resolvedRecord
		view = current.Config.TLSResolved
		// 未确认的跳过建议不借测试自动跳过；先完成证书确认再测。
		if current.Chain == nil && current.Config.Security == "tls" && c.tls != nil {
			resolved, err := c.tls.Resolve(ctx, current.Config)
			if err != nil {
				failure = status.Error(codes.FailedPrecondition, tlsErrorCode(err))
				return
			}
			if resolved.NeedsConfirmation {
				failure = status.Error(codes.FailedPrecondition, "tls_confirmation_required")
				return
			}
			view = resolved
		}
		if op, ok := c.routes.testing[current.Config.ID]; ok {
			running, inFlight = op, true
			return
		}
		id, err := operationID()
		if err != nil {
			failure = status.Error(codes.Internal, "operation id unavailable")
			return
		}
		testCtx, cancel := context.WithCancel(c.tests.lifetime)
		c.tests.mu.Lock()
		if c.tests.closed {
			c.tests.mu.Unlock()
			cancel()
			failure = status.Error(codes.Unavailable, "daemon stopping")
			return
		}
		c.tests.probes.Add(1)
		c.tests.ops[id] = cancel
		c.tests.mu.Unlock()
		c.routes.testing[current.Config.ID] = id
		record = current
		latency, exit, _ := c.measurementParallel()
		record.Config.MeasurementLatency = latency
		record.Config.MeasurementExit = exit
		pending = id
		c.publishCatalog(ctx)
		go c.runRouteTest(testCtx, cancel, record, view, id, c.tests, c.tester)
	}); err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	if inFlight {
		return &paxv1.OperationAck{OperationId: running}, nil
	}

	return &paxv1.OperationAck{OperationId: pending}, nil
}

func (c *Controller) runRouteTest(testCtx context.Context, cancel context.CancelFunc, record config.RouteRecord, view config.TLSPolicyView, opID string, owner *testOwner, tester RouteTester) string {
	owner.mu.Lock()
	owner.active[record.Config.ID] = opID
	owner.ops[opID] = cancel
	owner.mu.Unlock()
	defer owner.probes.Done()
	defer cancel()
	defer func() {
		owner.mu.Lock()
		delete(owner.ops, opID)
		delete(owner.networkChanged, opID)
		if owner.active[record.Config.ID] == opID {
			delete(owner.active, record.Config.ID)
		}
		owner.mu.Unlock()
	}()
	select {
	case <-owner.slots:
	case <-testCtx.Done():
		c.finishRouteTest(opID, record.Config.ID, routeTestResult{code: "operation_cancelled", revision: record.Revision, configRevision: connectionVersion(record, view)})
		return "operation_cancelled"
	}
	defer func() { owner.slots <- struct{}{} }()
	eligible := false
	if err := c.query(testCtx, func(context.Context) { eligible = c.routes.testing[record.Config.ID] == opID }); err != nil || !eligible {
		c.finishRouteTest(opID, record.Config.ID, routeTestResult{code: "operation_cancelled", revision: record.Revision, configRevision: connectionVersion(record, view)})
		return "operation_cancelled"
	}
	latencyLimit, _, _ := c.measurementParallel()
	if record.Config.MeasurementLatency > 0 {
		latencyLimit = record.Config.MeasurementLatency
	}
	if err := c.acquireLatency(testCtx, owner, latencyLimit); err != nil {
		c.finishRouteTest(opID, record.Config.ID, routeTestResult{code: "operation_cancelled", revision: record.Revision, configRevision: connectionVersion(record, view)})
		return "operation_cancelled"
	}
	var released sync.Once
	releaseLatency := func() { released.Do(func() { c.releaseLatency(owner) }) }
	defer releaseLatency()
	// Waiting for shared admission is not network execution time.
	testCtx, stopExecution := context.WithTimeout(testCtx, testTimeout)
	defer stopExecution()

	profile := record.Config
	profile.TLSResolved = view
	networkBefore := ""
	if c.physical != nil {
		native, e := c.physical.sample(testCtx)
		if e == nil {
			networkBefore = native.Epoch
			profile.MeasurementNetwork = native.Epoch
			profile = bindRouteNetwork(profile, native)
		}
	}
	c.beginRouteProgress(owner, record.Config.ID, opID, connectionVersion(record, view), networkBefore)
	reportLatency := func(value config.RouteMeasurement) {
		releaseLatency()
		c.reportRouteLatency(owner, record.Config.ID, opID, connectionVersion(record, view), networkBefore, value)
	}
	var duration time.Duration
	var err error
	measured := config.RouteMeasurement{Method: "https204-dual-v2"}
	if testCtx.Err() != nil {
		err = testCtx.Err()
	} else if measurer, ok := tester.(RouteMeasurer); ok {
		measured, err = measurer.MeasureRoute(testCtx, profile, reportLatency)
		if measured.Method == "" {
			measured.Method = "https204-dual-v2"
		}
		duration = measured.Duration
	} else {
		duration, err = tester.TestRoute(testCtx, profile)
	}
	if testCtx.Err() != nil {
		err = testCtx.Err()
		duration = 0
		measured.Duration = 0
	}
	measured.ExitIP = ""
	measured.ExitError = ""
	measured.ExitLocation = nil
	measured.LastExitLocation = nil

	latencyAt := time.Now()
	owner.mu.Lock()
	if p := owner.progress[record.Config.ID]; p != nil && p.OperationId == opID && p.LatencyResult.GetAt() != nil && err == nil {
		latencyAt = p.LatencyResult.At.AsTime()
	}
	owner.mu.Unlock()
	result := routeTestResult{routeName: record.Config.Name, exitAt: time.Now(), network: networkBefore, exitIP: measured.ExitIP, exitError: measured.ExitError, at: latencyAt, success: err == nil, duration: duration, revision: record.Revision, configRevision: connectionVersion(record, view)}
	if err != nil {
		failure := testErrorCode(err, testCtx)
		result.code = failure.Error()
		var product *productError
		if errors.As(failure, &product) {
			result.code = product.code
			result.params = product.params
		}
	}
	owner.mu.Lock()
	changedNetwork := owner.networkChanged[opID]
	owner.mu.Unlock()
	if changedNetwork {
		result.success = false
		result.code = "network_changed"
		result.duration = 0
		result.exitIP = ""
		result.exitError = "network_changed"
		measured.ExitLocation = nil
	}
	result.measurement = measured
	if networkBefore != "" && testCtx.Err() == nil {
		latest, e := c.physical.sample(testCtx)
		if e != nil || latest.Epoch != networkBefore {
			result.success = false
			result.code = "network_changed"
			result.duration = 0
			result.exitIP = ""
			result.exitError = "network_changed"
			result.measurement.ExitLocation = nil
		}
	}
	c.finishRouteTest(opID, record.Config.ID, result)
	return result.code
}

// finishRouteTest hands the outcome to the single owner: it clears the in-flight marker, stores
// the result and publishes the snapshot; the operation result carries the failure code.
func (c *Controller) finishRouteTest(opID, routeID string, result routeTestResult) {
	evidence := operationEvidence(testLabel, result.code, "", "")
	latency := sampleText(result.success, result.duration.String())
	evidence.Sections = []config.EventSection{{Kind: "metrics", Title: "当次线路测试", Fields: []config.EventField{{Label: "线路", Value: result.routeName}, {Label: "样本时间", Value: result.at.Format(time.RFC3339)}, {Label: "首次耗时", Value: latency}}}}
	if !c.enqueueCatalogEvidence(opID, testLabel, evidence, func(ctx context.Context) error {
		if c.tests != nil {
			c.tests.mu.Lock()
			delete(c.tests.parents, opID)
			c.tests.mu.Unlock()
		}
		if c.routes.testing[routeID] != opID {
			if result.code != "" {
				return &productError{code: result.code, params: result.params}
			}
			return nil
		}
		delete(c.routes.testing, routeID)
		if c.tests != nil {
			c.tests.mu.Lock()
			delete(c.tests.progress, routeID)
			c.tests.mu.Unlock()
		}
		if result.code == "ipv6_unavailable" {
			c.publishCatalog(ctx)
			return config.ErrIPv6Unavailable
		}
		// 线路已删除的结果直接丢弃：没有可归属的记录。
		if record, ok := c.routes.catalog.Find(routeID); ok {
			resolved, e := c.resolvedRecord(ctx, record)
			if e != nil || connectionVersion(resolved, resolved.Config.TLSResolved) != result.configRevision {
				c.persistMeasurements(ctx)
				c.publishCatalog(ctx)
				return &productError{code: "route_stale"}
			}

			c.routes.testResults[routeID] = result
			latency, cold := latencyHistory(routeID, result)
			if cold != nil {
				c.appendHistory(ctx, *cold)
			}
			c.appendHistory(ctx, latency)

		}
		c.persistMeasurements(ctx)
		c.publishCatalog(ctx)
		if result.code != "" {
			return &productError{code: result.code, params: result.params}
		}
		return nil
	}) {
		c.completeOperation(opID, testLabel, "operation_cancelled")
	}
}

// latencyHistory turns one finished latency probe into its history rows: the (stable) latency
// and, for session probes, the cold connect time. Manual tests and the background sampler share it.
func latencyHistory(routeID string, result routeTestResult) (latency config.HistorySample, cold *config.HistorySample) {
	latency = config.HistorySample{RouteID: routeID, Metric: "latency", Method: result.measurement.Method, Network: result.network, At: result.at, ConfigRevision: result.configRevision, Value: float64(result.duration.Milliseconds()), Success: result.success, Error: result.code}
	if result.measurement.Method != measurement.SessionLatencyMethod {
		return latency, nil
	}
	connect := latency
	connect.Metric = "connect"
	connect.Value = float64(result.measurement.Cold.Milliseconds())
	connect.Success = result.success && result.measurement.Cold > 0
	latency.Value = float64(result.measurement.Stable.Milliseconds())
	latency.Success = result.success && result.measurement.StableSamples == 3 && result.measurement.StableError == "" && result.measurement.Stable > 0
	if result.success && !latency.Success {
		latency.Error = "latency_samples_incomplete"
	}
	return latency, &connect
}

func testErrorCode(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, config.ErrIPv6Unavailable) {
		return config.ErrIPv6Unavailable
	}
	var certificate *singbox.CertificateError
	if errors.As(err, &certificate) {
		return &productError{code: "tls_certificate_invalid", params: []string{certificate.Reason}}
	}
	var measured *measurement.Failure
	if errors.As(err, &measured) {
		params := []string{}
		if measured.Reason != "" {
			params = append(params, measured.Reason)
		}
		return &productError{code: measured.Code, params: params}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return errors.New("route_test_timeout")
		}
		return errors.New("operation_cancelled")
	}
	if errors.Is(err, singbox.ErrProbe) {
		return errors.New("proxy_unreachable")
	}
	return errors.New("route_invalid")
}
