package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
)

type proxyIntent struct {
	id, label string
	enabled   bool
	serial    uint64
	tls       *tlsIntent
	routeOp   *routeOperation
	query     *routeQuery
}
type proxyOwner struct {
	running            runningBinding
	networkSignal      chan struct{}
	networkGeneration  uint64
	sleeping           bool
	powerSequence      uint64
	wakeFollowup       *wakeFollowup
	pathCancel         context.CancelFunc
	pathJobs           chan pathCheckJob
	pathCompleted      chan pathCheckDone
	pathWorkerDone     chan struct{}
	pathCheck          *pathCheckState
	pathRequests       map[string]pathCheckRequest
	measurementSignal  chan struct{}
	routeCancels       map[string]context.CancelFunc
	desired            atomic.Bool
	runtimeEpoch       uint64
	mu                 sync.Mutex
	lifetime           context.Context
	cancel             context.CancelFunc
	load               func() (config.DebugProfile, error)
	networkEpoch       func() (string, error)
	queue              chan proxyIntent
	finished           chan struct{}
	initialized        chan struct{}
	activeCancel       context.CancelFunc
	serial, stopSerial atomic.Uint64
	shutdownMu         sync.Mutex
	shutdownObserved   bool
	shutdownError      error
}

// ConfigureOperations creates the sole product mutation owner. The user daemon
// cannot run a TUN; all networking lifecycle goes through its authenticated core.
func (c *Controller) ConfigureOperations(lifetime context.Context, load func() (config.DebugProfile, error)) {
	ctx, cancel := context.WithCancel(lifetime)
	c.proxy = &proxyOwner{networkSignal: make(chan struct{}, 1), pathJobs: make(chan pathCheckJob, 1), pathCompleted: make(chan pathCheckDone, 2), pathWorkerDone: make(chan struct{}), lifetime: ctx, cancel: cancel, load: load, measurementSignal: make(chan struct{}, 1), queue: make(chan proxyIntent, 8), finished: make(chan struct{}), initialized: make(chan struct{})}
	go c.currentPathWorker()
	go c.proxyLoop()
	<-c.proxy.initialized
}

func (c *Controller) SetProxyEnabled(ctx context.Context, req *paxv1.SetProxyEnabledRequest) (*paxv1.OperationAck, error) {
	return c.admitProxy(ctx, req.Enabled)
}

func (c *Controller) admitProxy(_ context.Context, enabled bool) (*paxv1.OperationAck, error) {
	if c.proxy == nil {
		return nil, status.Error(codes.FailedPrecondition, "proxy unavailable")
	}
	p := c.proxy
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lifetime.Err() != nil {
		return nil, status.Error(codes.Unavailable, "daemon stopping")
	}
	c.mu.Lock()
	busyTLS := c.tlsPending != "" || c.catalogPending != ""
	c.mu.Unlock()
	if busyTLS && enabled {
		return nil, status.Error(codes.FailedPrecondition, "operation_busy")
	}
	if len(p.queue) == cap(p.queue) {
		return nil, status.Error(codes.ResourceExhausted, "operation queue full")
	}
	p.desired.Store(enabled)
	sequence := p.serial.Add(1)
	id, err := operationID()
	if err != nil {
		return nil, status.Error(codes.Internal, "operation id unavailable")
	}
	if !enabled {
		p.stopSerial.Store(sequence)
	}
	if p.pathCancel != nil {
		p.pathCancel()
	}
	if p.activeCancel != nil {
		p.activeCancel()
	}
	c.mu.Lock()
	c.current.PendingOperationId = id
	c.commitLocked()
	c.mu.Unlock()
	label := "关闭代理"
	if enabled {
		label = "开启代理"
	}
	p.queue <- proxyIntent{id: id, enabled: enabled, serial: sequence, label: label}
	return &paxv1.OperationAck{OperationId: id}, nil
}

func (c *Controller) CloseProxy(ctx context.Context) error {
	if c.tests != nil {
		c.tests.cancel()
	}
	if c.proxy == nil {
		if c.tests != nil {
			select {
			case <-c.tests.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	c.proxy.cancel()
	select {
	case <-c.proxy.finished:
		if c.physical != nil {
			select {
			case <-c.physical.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if c.tests != nil {
			select {
			case <-c.tests.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		// The product owner has exited; serialize retry-only cleanup separately.
		// No new proxy intent is accepted after its lifetime was cancelled.
		c.proxy.shutdownMu.Lock()
		defer c.proxy.shutdownMu.Unlock()
		if c.proxy.shutdownObserved && c.proxy.shutdownError != nil {
			c.proxy.shutdownError = c.shutdownRuntime(ctx)
		}
		c.proxy.shutdownObserved = true
		return c.proxy.shutdownError
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Controller) proxyLoop() {
	p := c.proxy
	defer close(p.finished)
	connection := connectionState{}
	// The 1 s refresh exists for in-flight measurement progress and the post-wake follow-up. It is
	// armed after a loop turn only while one of them is pending, so an idle daemon has no 1 s timer.
	var refresh <-chan time.Time
	var refreshTimer *time.Timer
	defer func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
	}()
	automatic := time.NewTicker(cadence.RoutingAutomatic)
	defer automatic.Stop()
	c.initCatalog(p.lifetime)
	c.recoverBackendState(&connection)
	close(p.initialized)
	var measurementTick <-chan time.Time
	var measurementTimer *time.Timer
	defer func() {
		if measurementTimer != nil {
			measurementTimer.Stop()
		}
	}()
	for {
		if refresh == nil && c.needsProxyRefresh() {
			if refreshTimer == nil {
				refreshTimer = time.NewTimer(cadence.ProxyRefresh)
			} else {
				refreshTimer.Reset(cadence.ProxyRefresh)
			}
			refresh = refreshTimer.C
		}
		select {
		case <-p.lifetime.Done():
			c.cancelCurrentPath("daemon_stopping")
			<-p.pathWorkerDone
			if c.routing != nil && c.routing.autoDone != nil {
				<-c.routing.autoDone
			}
			drain, done := context.WithTimeout(context.Background(), 3*time.Second)
			p.shutdownError = c.shutdownRuntime(drain)
			done()
			if c.tests != nil {
				<-c.tests.done
			}
			for {
				select {
				case queued := <-p.queue:
					if queued.query == nil {
						c.completeOperation(queued.id, queued.label, "operation_cancelled")
					}
				default:
					c.measurementBatchHolds = 0
					flush, done := context.WithTimeout(context.Background(), time.Second)
					c.persistMeasurements(flush)
					done()
					c.mu.Lock()
					c.current.TestingRouteIds = nil
					c.current.RouteMeasurementProgress = nil
					c.current.LatencyOperationId = ""
					c.current.TestOperationIds = nil
					c.current.ExitOperationIds = nil
					c.current.DownloadingRouteIds = nil
					c.current.DownloadOperationId = ""
					c.current.DownloadQueue = nil
					c.commitLocked()
					c.mu.Unlock()
					return
				}
			}
		case <-automatic.C:
			c.scheduleRoutingAutomatic()
		case <-p.networkSignal:
			c.localNetworkChanged()
		case done := <-p.pathCompleted:
			c.finishCurrentPath(done)
		case <-p.measurementSignal:
			c.pruneSingleDownloads()
			if measurementTick == nil {
				measurementTimer = time.NewTimer(250 * time.Millisecond)
				measurementTick = measurementTimer.C
			}
		case <-measurementTick:
			measurementTick = nil
			c.publishCatalog(p.lifetime)
		case intent := <-p.queue:
			if intent.query != nil {
				if intent.query.ctx.Err() == nil {
					intent.query.apply(intent.query.ctx)
				}
				close(intent.query.done)
				continue
			}
			parent := p.lifetime
			if intent.routeOp != nil && intent.routeOp.ctx != nil {
				parent = intent.routeOp.ctx
			}
			op, cancel := context.WithTimeout(parent, 30*time.Second)
			p.mu.Lock()
			p.activeCancel = cancel
			p.mu.Unlock()
			if intent.serial > 0 && p.stopSerial.Load() > intent.serial {
				cancel()
			}
			c.mu.Lock()
			previousRoute := c.current.GetCurrentRoute().GetName()
			c.mu.Unlock()
			var code string
			var params []string
			switch {
			case intent.routeOp != nil:
				code, params = catalogFailure(intent.routeOp.apply(op))
			case intent.tls != nil:
				code = c.saveTLS(op, intent.tls)
			default:
				c.cancelCurrentPath("new_operation")
				p.runtimeEpoch++
				started := time.Now()
				code, params = c.executeCoreIntent(op, intent, &connection)
				diagnostic := code
				if diagnostic == "" {
					diagnostic = "ok"
				}
				telemetry.RecordDiagnostic(telemetry.Diagnostic{OperationID: intent.id, Stage: "connection_intent", Code: diagnostic, Success: code == "", DurationMS: time.Since(started).Milliseconds()})
			}
			cancel()
			p.mu.Lock()
			p.activeCancel = nil
			delete(p.routeCancels, intent.id)
			if intent.routeOp != nil && intent.routeOp.cancel != nil {
				intent.routeOp.cancel()
			}
			p.mu.Unlock()
			c.mu.Lock()
			nextRoute := c.current.GetCurrentRoute().GetName()
			c.mu.Unlock()
			if previousRoute == nextRoute {
				previousRoute, nextRoute = "", ""
			}
			var evidence *config.SystemEvent
			if intent.routeOp != nil {
				evidence = intent.routeOp.evidence
			}
			if intent.tls != nil {
				evidence = operationEvidence("证书设置", code, "", "")
				evidence.Sections = []config.EventSection{{Kind: "facts", Title: "证书策略设置", Fields: []config.EventField{{Label: "新策略", Value: string(intent.tls.mode)}, {Label: "生效范围", Value: "仅影响下一次连接"}}}}
			}
			var routeSave *paxv1.RouteSaveResult
			if intent.routeOp != nil && code == "" {
				routeSave = intent.routeOp.result
			}
			c.completeOperationOutcome(intent.id, intent.label, code, previousRoute, nextRoute, evidence, routeSave, params...)
		case <-refresh:
			refresh = nil
			c.resumeWakeFollowup()
			if c.routes != nil && (len(c.routes.testing) > 0 || len(c.routes.downloading) > 0 || len(c.routes.exiting) > 0) {
				// Mutations and terminal callbacks publish their own catalog changes. Only
				// in-flight progress needs periodic projection; an idle tick must not parse
				// every saved route and TLS policy again.
				c.publishCatalog(p.lifetime)
			}
		}
	}
}

// needsProxyRefresh reports whether the 1 s refresh has work: a measurement in flight or a wake
// follow-up waiting for the network to settle. Owner goroutine only.
func (c *Controller) needsProxyRefresh() bool {
	if c.proxy.wakeFollowup != nil {
		return true
	}
	return c.routes != nil && (len(c.routes.testing) > 0 || len(c.routes.downloading) > 0 || len(c.routes.exiting) > 0)
}

type connectionState struct{ running, cleanupFailed bool }

// ConfigureNetworkEpoch supplies read-only capture consistency, never recovery.
func (c *Controller) ConfigureNetworkEpoch(current func() (string, error)) {
	c.proxy.networkEpoch = current
}

func (c *Controller) proxyState(state paxv1.ProtectionStatus, reason string, route *config.NodeProfile) {
	session := ""
	var generation uint64
	if state != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		session = "core-session"
		generation = c.proxy.runtimeEpoch
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current.Status == state && c.current.DegradedReason.GetCode() == reason && route == nil && c.current.ConnectionSession == session && c.current.ConnectionGeneration == generation {
		return
	}
	telemetry.RecordDiagnostic(telemetry.Diagnostic{Stage: "proxy_state", Code: state.String() + ":" + reason})
	c.current.ConnectionSession = session
	c.current.ConnectionGeneration = generation
	c.current.Status = state
	c.current.DegradedReason = nil
	c.current.DegradedReasons = nil
	if reason != "" {
		c.current.DegradedReason = &paxv1.ProductError{Code: reason}
		c.current.DegradedReasons = []*paxv1.ProductError{c.current.DegradedReason}
	}
	if route != nil {
		if len(c.current.Routes) > 0 {
			for _, r := range c.current.Routes {
				if r.Id == route.ID {
					c.current.CurrentRoute = proto.Clone(r).(*paxv1.NodeProfile)
					break
				}
			}
		}
	}
	if state == paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		c.current.CurrentRoute = nil
		c.current.ActiveTls = nil
	}
	c.commitLocked()
}
func (c *Controller) completeOperation(id, label, failure string, params ...string) {
	c.completeOperationWithRoutes(id, label, failure, "", "", params...)
}
func (c *Controller) completeOperationWithRoutes(id, label, failure, from, to string, params ...string) {
	c.completeOperationEvidence(id, label, failure, from, to, nil, params...)
}
func (c *Controller) completeOperationEvidence(id, label, failure, from, to string, evidence *config.SystemEvent, params ...string) {
	c.completeOperationOutcome(id, label, failure, from, to, evidence, nil, params...)
}
func (c *Controller) completeOperationOutcome(id, label, failure, from, to string, evidence *config.SystemEvent, routeSave *paxv1.RouteSaveResult, params ...string) {
	if label == "" {
		label = "代理操作"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalogPending == id {
		c.catalogPending = ""
	}
	if c.tlsPending == id {
		c.tlsPending = ""
	}
	if c.current.PendingOperationId == id {
		c.current.PendingOperationId = ""
	}
	entry := &paxv1.ProductLog{At: timestamppb.Now(), Level: paxv1.LogLevel_LOG_LEVEL_INFO, Message: label + "完成", OperationId: id, SnapshotVersion: c.snapshotSeq + 1}
	result := &paxv1.OperationResult{OperationId: id, Ok: failure == "", RouteSave: routeSave}
	if failure != "" {
		entry.Level = paxv1.LogLevel_LOG_LEVEL_ERROR
		entry.Message = label + "失败：" + failure
		result.Error = &paxv1.ProductError{Code: failure, MessageParams: params}
	}
	if c.completedOperations == nil {
		c.completedOperations = map[string]*paxv1.OperationResult{}
	}
	c.completedOperations[id] = result
	if _, retained := c.routeRequestResults[id]; retained {
		c.routeRequestResults[id] = result
	}
	c.completedOrder = append(c.completedOrder, id)
	if len(c.completedOrder) > 256 {
		delete(c.completedOperations, c.completedOrder[0])
		c.completedOrder = c.completedOrder[1:]
	}
	entry.EventId = "operation:" + id
	if evidence == nil {
		evidence = operationEvidence(label, failure, from, to)
	} else {
		evidence.Result = map[bool]string{true: "completed", false: "failed"}[failure == ""]
		evidence.ErrorCode = failure
		if failure == "operation_cancelled" {
			evidence.Result = "cancelled"
		}
	}
	// Manual current-path acknowledgements share the saved check event. Preserve
	// cancellations that have no completed check rather than counting twice.
	if label != "验证当前通道" || failure == "operation_cancelled" {
		c.enqueueHistoryEvent(config.HistoryEvent{System: evidence, OperationFrom: from, OperationTo: to, ID: entry.EventId, At: entry.At.AsTime(), Kind: "operation", Title: entry.Message, OperationID: id, Level: map[bool]string{true: "info", false: "error"}[failure == ""], Source: "Pax", Result: failure})
	}
	c.current.RecentLogs = append(c.current.RecentLogs, entry)
	if len(c.current.RecentLogs) > 500 {
		c.current.RecentLogs = c.current.RecentLogs[len(c.current.RecentLogs)-500:]
	}
	c.commitLocked()
	c.snapshotSeq++
	c.fanoutLocked(&paxv1.WatchEvent{Seq: c.snapshotSeq, SnapshotVersion: c.current.SnapshotVersion, Payload: &paxv1.WatchEvent_Operation{Operation: result}})
	// Update the snapshot high watermark to include the result for reconnect deduplication.
	c.current.SnapshotVersion = c.snapshotSeq
	slog.Info("operation completed", "operation_id", id, "label", label, "snapshot_version", c.snapshotSeq, "error_code", failure)
	if failure == "tun_start_failed" && len(params) == 2 {
		slog.Info("tun startup failed", "operation_id", id, "snapshot_version", c.snapshotSeq, "stage", params[0], "reason", params[1])
	}
}

// enqueueCatalogOp admits one already acknowledged background catalog operation into the single
// serial owner; it never marks the catalog pending, so a refresh cannot block Stop or CRUD.
func (c *Controller) enqueueCatalogOp(id, label string, apply func(context.Context) error) bool {
	return c.enqueueCatalogEvidence(id, label, nil, apply)
}
func (c *Controller) enqueueCatalogEvidence(id, label string, evidence *config.SystemEvent, apply func(context.Context) error) bool {
	if c.proxy == nil {
		return false
	}
	if c.proxy.lifetime.Err() != nil {
		return false
	}
	select {
	case c.proxy.queue <- proxyIntent{id: id, label: label, routeOp: &routeOperation{evidence: evidence, apply: apply}}:
		return true
	case <-c.proxy.lifetime.Done():
		return false
	}
}

func operationID() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "operation-" + hex.EncodeToString(nonce), nil
}
