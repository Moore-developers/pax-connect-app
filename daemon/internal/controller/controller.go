// Package controller 是 daemon 的唯一编排者：产品状态机、操作队列、保护检查与错误解释。
//
// 负责什么：实现产品 gRPC（gen/proto/paxv1）的方法，串起 config / routing / singbox / network /
// telemetry 的调用顺序；一切写操作只有一个入口（ConfigOperationQueue）。
// 不负责什么：不直接操作 sing-box 类型（经 singbox 包）、不做系统网络配置（经 network 包）。
// 谁依赖它：cmd/pax-connectd（装配）；本包依赖所有业务包。
//
// T1 只实现 Hello / GetSnapshot / Watch 的最小形态（无线路、状态 OFF），
// 让「App 连上 daemon」这条链路可验证；命令方法一律返回 Unimplemented，等 T3 实现。
package controller

import (
	"context"
	"google.golang.org/protobuf/proto"
	"pax-connect/daemon/internal/aiusage"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/environment"
	"pax-connect/daemon/internal/historystore"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/siteicon"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paxv1 "pax-connect/daemon/gen/proto/paxv1"
)

// ProtocolVersion 是产品协议版本；与 Swift 侧 PaxClient.protocolVersion 必须一致。
const ProtocolVersion uint32 = 46

type Controller struct {
	initialInstallKnown   bool
	initialInstall        bool
	aiProfiles            bool
	aiUsage               bool
	aiStore               *aiusage.Store // set by ConfigureAIUsage; its state is the Snapshot.ai_usage first frame
	aiForeground          atomic.Bool    // last AI usage visibility seen by aiUsageViewsChanged
	privacy               *privacyOwner
	routing               *routingLibraryOwner
	icons                 *siteicon.Store
	history               *historyOwner
	ingest                *historyIngest
	logs                  *historyLogs
	logsMu                sync.Mutex
	traffic               *historyTraffic
	trafficMu             sync.Mutex
	liveView              atomic.Pointer[liveState]
	readTokens            *readTokenCache
	logState              atomic.Pointer[historystore.LogsMeta]
	logsChanged           chan struct{}
	consumers             *journalConsumers
	exits                 *measurement.ExitLookup
	data                  *routeData
	powerSequence         uint64
	coreSession           CoreSession
	pathDiagnostics       *pathDiagnosticsOwner
	environmentMu         sync.Mutex
	environmentSession    *environment.Session
	lastConnectionAttempt *paxv1.TrafficPathDiagnostics
	sessionStop           context.CancelFunc
	ownerAppPID           uint32
	userMode              bool
	assessmentID          string
	physical              *physicalOwner
	physicalCache         *config.PhysicalEgressCacheStore
	autoLatencyTicks      <-chan time.Time // nil: a 60 s ticker; tests inject their own
	autoLatencyObserve    func(verdict string)
	preferences           *preferenceOwner
	cf                    *cfOwner
	measurements          MeasurementRepository
	measurementBatchHolds int
	paxv1.UnimplementedPaxServiceServer

	requestMu           sync.Mutex
	routeRequests       map[string]routeRequest
	completedOperations map[string]*paxv1.OperationResult
	routeRequestResults map[string]*paxv1.OperationResult
	completedOrder      []string
	version             string
	fingerprint         string
	mu                  sync.Mutex
	snapshotSeq         uint64
	subscribers         map[chan *paxv1.WatchEvent]chan struct{}
	views               map[string]bool // SetViewVisibility set; cleared when the last Watch ends
	current             *paxv1.Snapshot
	proxy               *proxyOwner
	tls                 TLSPolicyRepository
	tlsPending          string
	catalogPending      string
	routes              *routesOwner
	subs                *subscriptionOwner
	tester              RouteTester
	tests               *testOwner
}

func New(version, fingerprint string) *Controller {
	return &Controller{version: version, fingerprint: fingerprint, readTokens: newReadTokenCache(), logsChanged: make(chan struct{}, 1), subscribers: make(map[chan *paxv1.WatchEvent]chan struct{}),
		current: &paxv1.Snapshot{Status: paxv1.ProtectionStatus_PROTECTION_STATUS_OFF,
			TodayTraffic: &paxv1.TrafficSnapshot{}, Settings: &paxv1.Settings{}}}
}

// ConfigureInitialInstall freezes startup identity before product stores are created.
func (c *Controller) ConfigureInitialInstall(initial bool) {
	c.initialInstall = initial
	c.initialInstallKnown = true
}

func (c *Controller) Hello(ctx context.Context, req *paxv1.HelloRequest) (*paxv1.HelloResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if req.GetProtocolVersion() != ProtocolVersion {
		return nil, status.Errorf(codes.FailedPrecondition,
			"protocol version mismatch: daemon=%d client=%d", ProtocolVersion, req.GetProtocolVersion())
	}
	capabilities := []string{"routes.catalog"}
	if c.tests != nil {
		capabilities = append(capabilities, "network.environment_diagnostics.v1")
	}
	if c.userMode && c.initialInstallKnown {
		capabilities = append(capabilities, "onboarding.v1")
	}
	if c.privacy != nil {
		capabilities = append(capabilities, "network.privacy.v1")
	}
	if c.routing != nil {
		capabilities = append(capabilities, "routing.library.v1")
	}
	if c.routes != nil && c.proxy != nil {
		capabilities = append(capabilities, "routing.rules.read.v1")
	}
	if c.icons != nil {
		capabilities = append(capabilities, "objects.icons.v1")
	}
	if _, ok := c.coreSession.(interface {
		VerifyCurrentPath(context.Context, config.CurrentPathRequest) (config.CurrentPathResult, error)
	}); ok {
		capabilities = append(capabilities, "connection.current_path.v1", "connection.current_path.wake.v1")
	}
	if c.history != nil {
		capabilities = append(capabilities, "history.logs.v1", "history.traffic.v1", "history.retention.v1", "history.refresh.v2", "history.views.v1", "history.summary.v1")
	}
	if c.data != nil {
		capabilities = append(capabilities, "routes.history", "routes.usage")
	}
	if c.pathDiagnostics != nil {
		capabilities = append(capabilities, "network.path_diagnostics", "network.connection_diagnostics.v1")
	}
	if c.aiUsage && c.userMode {
		capabilities = append(capabilities, "ai.usage.v1", "ai.usage.sources.v1", "ai.usage.cost_range.v1", "ai.usage.sessions.v1", "ai.usage.context.v1", "ai.usage.session_agents.v1", "ai.usage.limits.v1", "ai.usage.limits.summary.v1", "ai.sessions.catalog.activity.v1", "ai.sessions.catalog.v1", "ai.sessions.catalog.range.v1")
	}
	if c.aiProfiles && c.userMode {
		capabilities = append(capabilities, "ai.profiles.v1", "ai.cli_apply.v1", "ai.profiles.sources.v1")
	}
	if c.userMode {
		capabilities = append(capabilities, "paxd.user")
		if c.coreSession != nil {
			capabilities = append(capabilities, "proxy.core", "proxy.power_events")
			if _, ok := c.coreSession.(catalogCore); ok {
				capabilities = append(capabilities, "routes.select.running", "routes.select.disconnect_old")
			}
		}
	} else if c.sessionStop != nil {
		capabilities = append(capabilities, "app.session")
	}
	if c.subs != nil && c.subs.fetcher != nil {
		capabilities = append(capabilities, "subscriptions.refresh", "subscriptions.source.edit")
	}
	if c.subs != nil && (c.subs.routeFetch != nil || c.subs.routeRequest != nil) {
		capabilities = append(capabilities, "subscriptions.refresh.route")
	}
	if c.preferences != nil {
		capabilities = append(capabilities, "measurement.preferences")
		if c.exits != nil {
			capabilities = append(capabilities, "measurement.exit_pool.v1", "measurement.test_cancel.v1")
		}
	}
	if c.physical != nil {
		capabilities = append(capabilities, "network.physical_egress")
	}
	if c.cf != nil {
		capabilities = append(capabilities, "routes.cf.candidates")
		if c.cf.workspace != nil {
			capabilities = append(capabilities, "routes.cf.workspace", "routes.cf.single", "routes.cf.regions.v1", "routes.cf.displayed_batch.v1")
		}
	}
	if _, ok := c.tls.(tlsBatchRepository); ok && c.routes != nil && c.proxy != nil {
		capabilities = append(capabilities, "routes.tls.batch")
	}
	if c.routes != nil && c.proxy != nil {

		capabilities = append(capabilities, "routes.select.off", "routes.documents", "routes.proxy.create", "routes.import", "routes.import.auto", "proxy.power", "subscriptions.delete", "routes.groups", "routes.chains")
	}
	if c.tester != nil && c.routes != nil && c.proxy != nil {
		capabilities = append(capabilities, "routes.test.single", "routes.test.batch", "routes.diagnostics", "routes.measure.v2", "routes.measure.v3")
		if _, ok := c.tester.(networkDiagnoser); ok {
			capabilities = append(capabilities, "network.diagnostics.v1")
		}
		if _, ok := c.tester.(RouteDownloader); ok {
			capabilities = append(capabilities, "routes.download.single", "routes.download.queue", "routes.download.single_queue")
		}
		if _, ok := c.tester.(RouteExitLookup); ok {
			capabilities = append(capabilities, "routes.measure.exit")
		}
		if _, ok := c.tester.(RouteMeasurer); ok {
			capabilities = append(capabilities, "routes.measure.after-import")
			if c.measurements != nil {
				capabilities = append(capabilities, "routes.measure.persist")
			}
		}
	}
	return &paxv1.HelloResponse{
		DaemonVersion:      c.version,
		ServiceFingerprint: c.fingerprint,
		ProtocolVersion:    ProtocolVersion,
		SnapshotSeq:        c.snapshotSeq,
		Capabilities:       capabilities,
		InitialInstall:     c.initialInstall,
		OwnerAppPid:        c.ownerAppPID,
	}, nil
}

func (c *Controller) GetSnapshot(ctx context.Context, _ *paxv1.GetSnapshotRequest) (*paxv1.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot(), nil
}

// Watch establishes a consistent snapshot boundary without changing product state.
// Each subscription is bounded; future state mutations must publish under the same lock.
func (c *Controller) Watch(req *paxv1.WatchRequest, stream grpc.ServerStreamingServer[paxv1.WatchEvent]) error {
	queue := make(chan *paxv1.WatchEvent, 64)
	c.mu.Lock()
	snapshot := c.snapshot()
	queue <- &paxv1.WatchEvent{
		Seq: snapshot.SnapshotVersion, SnapshotVersion: snapshot.SnapshotVersion,
		Payload: &paxv1.WatchEvent_Snapshot{Snapshot: snapshot},
	}
	overflow := make(chan struct{})
	c.subscribers[queue] = overflow
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.subscribers, queue)
		if len(c.subscribers) == 0 {
			c.views = nil
		}
		c.mu.Unlock()
		c.aiUsageViewsChanged()
	}()
	for {
		select {
		case event := <-queue:
			sent := make(chan error, 1)
			// Returning the handler cancels gRPC's stream context and releases a blocked Send.
			go func() { sent <- stream.Send(event) }()
			select {
			case err := <-sent:
				if err != nil {
					return err
				}
			case <-overflow:
				return status.Error(codes.ResourceExhausted, "slow subscriber; refresh snapshot")
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		case <-overflow:
			return status.Error(codes.ResourceExhausted, "slow subscriber; refresh snapshot")
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// PublishSnapshot commits controller state and fans it out under the subscription lock.
// It is the state transition seam shared by synchronization tests and subsequent commands.
func (c *Controller) PublishSnapshot(snapshot *paxv1.Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = proto.Clone(snapshot).(*paxv1.Snapshot)
	c.commitLocked()
}
func (c *Controller) commitLocked() {
	c.snapshotSeq++
	c.current.SnapshotVersion = c.snapshotSeq
	state := c.snapshot()
	c.fanoutLocked(&paxv1.WatchEvent{Seq: c.snapshotSeq, SnapshotVersion: c.snapshotSeq, Payload: &paxv1.WatchEvent_Snapshot{Snapshot: state}})
}
func (c *Controller) fanoutLocked(event *paxv1.WatchEvent) {
	for queue, overflow := range c.subscribers {
		select {
		case queue <- proto.Clone(event).(*paxv1.WatchEvent):
		default:
			close(overflow)
			delete(c.subscribers, queue)
		}
	}
}

func (c *Controller) snapshot() *paxv1.Snapshot {
	value := proto.Clone(c.current).(*paxv1.Snapshot)
	// App-visible retention and log generations come from logs.db (R3), even when the
	// publisher has not caught up with the latest commit yet.
	if meta, ok := c.logStateSnapshot(); ok {
		value.History = mergeLogHistory(value.History, meta)
		if value.Settings == nil {
			value.Settings = &paxv1.Settings{}
		}
		applyLogSettingsTo(value.Settings, meta)
	}
	if c.exits != nil {
		value.ExitPoolState = c.exitPoolProto()
	}
	if c.aiStore != nil {
		value.AiUsage = usageState(c.aiStore.State())
	}
	return value
}

func (c *Controller) ImportRoutes(context.Context, *paxv1.ImportRoutesRequest) (*paxv1.ImportResult, error) {
	return nil, status.Error(codes.Unimplemented, "请先预览再确认导入")
}
