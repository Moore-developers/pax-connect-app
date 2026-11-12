package controller

import (
	"context"
	"errors"
	"fmt"
	"pax-connect/daemon/internal/subfetch"
	"pax-connect/daemon/internal/telemetry"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/importers"
)

// maxConcurrentDownloads 是同时进行的最多订阅下载数。
const maxConcurrentDownloads = 2

const refreshLabel = "订阅刷新"

// SubscriptionFetcher is the bounded network boundary for one HTTPS source; the daemon injects
// the production implementation and tests substitute their own.
type SubscriptionFetcher interface {
	FetchSubscription(context.Context, string) ([]byte, error)
}

type subscriptionRun struct {
	request  string
	sourceID string
	routeID  string
	cancel   context.CancelFunc
}

// subscriptionOwner keeps refresh downloads outside the single serial owner, so a slow source
// never blocks Stop or catalog edits; only the merge itself runs in that owner.
type subscriptionOwner struct {
	lifetime     context.Context
	fetcher      SubscriptionFetcher
	routeRequest func(context.Context, config.RouteConfig, string, string) ([]byte, subfetch.Report, error)
	routeFetch   func(context.Context, config.RouteConfig, string) ([]byte, error)
	mu           sync.Mutex
	active       map[string]string
	ops          map[string]*subscriptionRun
	slots        chan struct{}
}

// ConfigureSubscriptionRequestFetch provides the instrumented isolated fetch boundary.
func (c *Controller) ConfigureSubscriptionRequestFetch(fetch func(context.Context, config.RouteConfig, string, string) ([]byte, subfetch.Report, error)) {
	if c.subs != nil {
		c.subs.routeRequest = fetch
	}
}

// ConfigureSubscriptionRouteFetch adds explicit isolated-route fetching.
// The injected composition keeps HTTP policy and kernel lifetime separate.
func (c *Controller) ConfigureSubscriptionRouteFetch(fetch func(context.Context, config.RouteConfig, string) ([]byte, error)) {
	if c.subs != nil {
		c.subs.routeFetch = fetch
	}
}

// ConfigureSubscriptions installs the fetch boundary; without it subscription commands stay
// unavailable instead of silently pretending to work.
func (c *Controller) ConfigureSubscriptions(fetcher SubscriptionFetcher) {
	lifetime := context.Background()
	if c.proxy != nil {
		lifetime = c.proxy.lifetime
	}
	owner := &subscriptionOwner{
		lifetime: lifetime, fetcher: fetcher,
		active: map[string]string{}, ops: map[string]*subscriptionRun{},
		slots: make(chan struct{}, maxConcurrentDownloads),
	}
	for index := 0; index < maxConcurrentDownloads; index++ {
		owner.slots <- struct{}{}
	}
	c.subs = owner
	c.startRoutingAutomatic()
}

// RefreshSubscription acknowledges a bounded refresh that runs in its own worker. Repeated
// requests for the same source reuse the operation already in flight.
func (c *Controller) RefreshSubscription(ctx context.Context, req *paxv1.RefreshSubscriptionRequest) (*paxv1.OperationAck, error) {
	if c.subs == nil || c.subs.fetcher == nil || c.routes == nil || c.proxy == nil {
		return nil, status.Error(codes.FailedPrecondition, "subscription_unavailable")
	}
	if len(req.ReplacementUrl) > 2048 || (req.ReplacementUrl != "" && !strings.HasPrefix(req.ReplacementUrl, "https://")) || len(req.GetUserAgent()) > 256 || strings.ContainsAny(req.GetUserAgent(), "\r\n") {
		return nil, status.Error(codes.InvalidArgument, "subscription_invalid")
	}
	var source config.SubscriptionRecord
	var route *config.RouteConfig
	var failure error
	if err := c.query(ctx, func(context.Context) {
		current, ok := c.routes.catalog.FindSubscription(req.SubscriptionId)
		if !ok {
			failure = status.Error(codes.NotFound, "subscription_not_found")
			return
		}
		// 非零版本必须与用户看到的来源一致；旧界面不能覆盖新来源。
		if req.ExpectedSourceRevision != 0 && current.Revision != req.ExpectedSourceRevision {
			failure = status.Error(codes.FailedPrecondition, "subscription_stale")
			return
		}
		source = current
		if req.UseLastChannel && source.HasRefreshChannel {
			req = proto.Clone(req).(*paxv1.RefreshSubscriptionRequest)
			req.RouteId = source.LastRouteID
			if req.RouteId != "" {
				saved, ok := c.routes.catalog.Find(req.RouteId)
				if !ok || saved.Hidden {
					failure = status.Error(codes.FailedPrecondition, "subscription_saved_route_unavailable")
					return
				}
				req.ExpectedRouteRevision = saved.Revision
				req.ExpectedConfigRevision = 0
			}
		}
		if req.RouteId != "" {
			if c.subs.routeFetch == nil && c.subs.routeRequest == nil {
				failure = status.Error(codes.Unimplemented, "subscription_proxy_unavailable")
				return
			}
			record, exists := c.routes.catalog.Find(req.RouteId)
			if !exists || record.Hidden || record.Revision != req.ExpectedRouteRevision {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			resolved, e := c.resolvedRecord(ctx, record)
			if e != nil {
				failure = e
				return
			}
			record = resolved
			view := record.Config.TLSResolved
			if record.Chain == nil && c.tls != nil && record.Config.Security == "tls" {
				var err error
				view, err = c.tls.Resolve(ctx, record.Config)
				if err != nil {
					failure = status.Error(codes.FailedPrecondition, tlsErrorCode(err))
					return
				}
			}
			if view.NeedsConfirmation {
				failure = status.Error(codes.FailedPrecondition, "tls_confirmation_required")
				return
			}
			if req.ExpectedConfigRevision != 0 && connectionVersion(record, view) != req.ExpectedConfigRevision {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			profile := record.Config
			profile.TLSResolved = view
			route = &profile
		}
	}); err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}

	owner := c.subs
	requestKey := req.RouteId + "\x00" + req.ReplacementUrl + "\x00" + req.GetUserAgent() + fmt.Sprint(req.UserAgent != nil)
	owner.mu.Lock()
	if existing, ok := owner.active[source.ID]; ok {
		if run := owner.ops[existing]; run != nil && (run.routeID != req.RouteId || run.request != requestKey) {
			owner.mu.Unlock()
			return nil, status.Error(codes.FailedPrecondition, "subscription_refresh_busy")
		}
		owner.mu.Unlock()
		return &paxv1.OperationAck{OperationId: existing}, nil
	}
	id, err := operationID()
	if err != nil {
		owner.mu.Unlock()
		return nil, status.Error(codes.Internal, "operation id unavailable")
	}
	runCtx, cancel := context.WithCancel(owner.lifetime)
	owner.active[source.ID] = id
	owner.ops[id] = &subscriptionRun{sourceID: source.ID, routeID: req.RouteId, request: requestKey, cancel: cancel}
	owner.mu.Unlock()
	_ = c.query(owner.lifetime, func(ctx context.Context) { c.publishCatalog(ctx) })
	go c.runRefresh(runCtx, cancel, id, source, route, req)
	return &paxv1.OperationAck{OperationId: id}, nil
}

// CancelOperation cancels one accepted route switch or background operation.
func (c *Controller) CancelOperation(_ context.Context, req *paxv1.CancelOperationRequest) (*paxv1.OperationAck, error) {
	if c.proxy != nil {
		c.proxy.mu.Lock()
		cancel, ok := c.proxy.routeCancels[req.OperationId]
		if ok {
			cancel()
		}
		c.proxy.mu.Unlock()
		if ok {
			return &paxv1.OperationAck{OperationId: req.OperationId}, nil
		}
	}
	if c.cf != nil {
		c.cf.mu.Lock()
		if w := c.cf.workspace; w != nil && w.operation == req.OperationId && w.cancel != nil {
			w.cancel()
			c.cf.mu.Unlock()
			return &paxv1.OperationAck{OperationId: req.OperationId}, nil
		}
		c.cf.mu.Unlock()
	}
	if c.subs != nil {
		owner := c.subs
		owner.mu.Lock()
		run, ok := owner.ops[req.OperationId]
		owner.mu.Unlock()
		if ok {
			run.cancel()
			return &paxv1.OperationAck{OperationId: req.OperationId}, nil
		}
	}
	if c.tests != nil {
		owner := c.tests
		owner.mu.Lock()
		cancel, ok := owner.ops[req.OperationId]
		owner.mu.Unlock()
		if ok {
			cancel()
			return &paxv1.OperationAck{OperationId: req.OperationId}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "operation_not_found")
}

// RestoreSubscriptionRoute brings back a node the user deleted from a subscription.
func (c *Controller) RestoreSubscriptionRoute(ctx context.Context, req *paxv1.RestoreSubscriptionRouteRequest) (*paxv1.OperationAck, error) {
	return c.admitNamedRoute(ctx, "route.restore_subscription", "恢复订阅线路", func(ctx context.Context) error {
		next := c.routes.catalog.Clone()
		if err := next.RestoreRoute(req.RouteId, req.ExpectedRevision); err != nil {
			return err
		}
		return c.saveCatalog(ctx, next)
	})
}

func (c *Controller) runRefresh(ctx context.Context, cancel context.CancelFunc, id string, source config.SubscriptionRecord, route *config.RouteConfig, request *paxv1.RefreshSubscriptionRequest) {
	owner := c.subs
	queuedAt := time.Now()
	defer cancel()
	defer func() {
		owner.mu.Lock()
		delete(owner.ops, id)
		if owner.active[source.ID] == id {
			delete(owner.active, source.ID)
		}
		owner.mu.Unlock()
		_ = c.query(owner.lifetime, func(ctx context.Context) { c.publishCatalog(ctx) })
	}()
	select {
	case <-owner.slots:
	case <-ctx.Done():
		c.completeOperation(id, refreshLabel, "operation_cancelled")
		return
	}
	defer func() { owner.slots <- struct{}{} }()

	attempt := config.SubscriptionAttempt{At: time.Now().UTC(), Channel: "system", Phase: "prepare", QueueMS: uint64(time.Since(queuedAt).Milliseconds())}
	if route != nil {
		attempt.Channel = "route"
	}
	if route != nil && c.tests != nil {
		tests := c.tests
		tests.mu.Lock()
		if tests.closed {
			tests.mu.Unlock()
			c.completeOperation(id, refreshLabel, "operation_cancelled")
			return
		}
		tests.probes.Add(1)
		tests.mu.Unlock()
		defer tests.probes.Done()
		select {
		case <-tests.slots:
			defer func() { tests.slots <- struct{}{} }()
		case <-ctx.Done():
			c.completeOperation(id, refreshLabel, "operation_cancelled")
			return
		}
	}
	attempt.QueueMS = uint64(time.Since(queuedAt).Milliseconds())
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	raw, agent := source.URL, source.UserAgent
	if request.ReplacementUrl != "" {
		raw = request.ReplacementUrl
	}
	if request.UserAgent != nil {
		agent = request.GetUserAgent()
	}
	var body []byte
	var err error
	var report subfetch.Report
	if route == nil {
		if fetcher, ok := owner.fetcher.(interface {
			FetchSubscriptionRequest(context.Context, string, string) ([]byte, subfetch.Report, error)
		}); ok {
			body, report, err = fetcher.FetchSubscriptionRequest(ctx, raw, agent)
		} else {
			body, err = owner.fetcher.FetchSubscription(ctx, raw)
		}
	} else if owner.routeRequest != nil {
		body, report, err = owner.routeRequest(ctx, *route, raw, agent)
	} else {
		body, err = owner.routeFetch(ctx, *route, raw)
	}
	attempt.Phase = report.Phase
	if attempt.Phase == "" {
		attempt.Phase = "request"
	}
	attempt.HTTPStatus = uint32(report.Status)
	attempt.DNSMS = uint64(report.DNS.Milliseconds())
	attempt.ConnectMS = uint64(report.Connect.Milliseconds())
	attempt.TLSMS = uint64(report.TLS.Milliseconds())
	attempt.HeadersMS = uint64(report.Headers.Milliseconds())
	attempt.BodyMS = uint64(report.Body.Milliseconds())
	attempt.TotalMS = uint64(time.Since(attempt.At).Milliseconds())

	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			c.completeOperation(id, refreshLabel, "operation_cancelled")
			return
		}
		_, code := fetchFailure(err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "fetch_timeout"
		}
		attempt.Error = code
		if attempt.HTTPStatus == 0 && code == "fetch_forbidden" {
			attempt.HTTPStatus = 403
		}
		c.submitRefreshAttempt(id, source, nil, false, code, attempt, request, agent)
		return
	}
	batch, decodeError := importers.ParseSubscription(body)
	nodes, failed := collectNodes(batch)
	failure := ""
	switch {
	case len(nodes) == 0 && decodeError != nil:
		failure = decodeError.Error()
	case len(nodes) == 0:
		failure = "empty_import"
	}
	// 部分坏行不是完整正文：有效映射可以更新，但无法对应的旧记录必须保留。
	complete := decodeError == nil && failed == 0
	if request.ReplacementUrl != "" && !complete {
		failure = "subscription_replacement_incomplete"
	}
	attempt.Phase = "parse"
	attempt.Error = failure
	if failure == "" {
		attempt.Phase = "complete"
	}
	c.submitRefreshAttempt(id, source, nodes, complete, failure, attempt, request, agent)
}

func collectNodes(batch importers.ParsedBatch) ([]config.SourceNode, int) {
	var nodes []config.SourceNode
	failed := 0
	for _, item := range batch.Items {
		if item.Route == nil {
			failed++
			continue
		}
		nodes = append(nodes, config.SourceNode{Name: item.Route.Name, Config: *item.Route})
	}
	return nodes, failed
}

func (c *Controller) submitRefreshAttempt(id string, source config.SubscriptionRecord, nodes []config.SourceNode, complete bool, failure string, attempt config.SubscriptionAttempt, request *paxv1.RefreshSubscriptionRequest, agent string) {
	telemetry.RecordDiagnostic(telemetry.Diagnostic{OperationID: id, Stage: "subscription_fetch", Phase: attempt.Phase, Code: failure, DurationMS: int64(attempt.TotalMS), Success: failure == ""})
	evidence := operationEvidence(refreshLabel, failure, "", "")
	evidence.Sections = []config.EventSection{{Kind: "objects", Title: "更新对象", Fields: []config.EventField{{Label: "订阅", Value: source.Name}}}, {Kind: "facts", Title: "当次结果", Fields: []config.EventField{{Label: "处理阶段", Value: attempt.Phase}, {Label: "解析有效线路", Value: fmt.Sprint(len(nodes))}, {Label: "正文完整", Value: fmt.Sprint(complete)}, {Label: "请求耗时", Value: fmt.Sprintf("%d ms", attempt.TotalMS)}}}}
	if !c.enqueueCatalogEvidence(id, refreshLabel, evidence, func(ctx context.Context) error {
		current, ok := c.routes.catalog.FindSubscription(source.ID)
		if !ok {
			return errors.New("subscription_not_found")
		}
		if current.Revision != source.Revision {
			return errors.New("subscription_stale")
		}
		next := c.routes.catalog.Clone()
		if failure == "" {
			// A replacement must be completely parseable before changing its source.
			if request.ReplacementUrl != "" && !complete {
				return errors.New("subscription_replacement_incomplete")
			}
			raw := source.URL
			if request.ReplacementUrl != "" {
				raw = request.ReplacementUrl
			}
			if err := next.ReplaceSubscriptionSource(source.ID, raw, agent); err != nil {
				return err
			}
			report, err := next.MergeSource(source.ID, nodes, complete)
			if err != nil {
				return err
			}
			evidence.Sections = append(evidence.Sections, config.EventSection{Kind: "metrics", Title: "当次变更", Fields: []config.EventField{{Label: "新增", Value: fmt.Sprint(report.Added)}, {Label: "更新", Value: fmt.Sprint(report.Updated)}, {Label: "移除", Value: fmt.Sprint(report.Removed)}, {Label: "保留", Value: fmt.Sprint(report.Kept)}}})
		}
		c.pruneRemoved(ctx, &next)
		if err := next.SetSubscriptionError(source.ID, failure); err != nil {
			return err
		}
		if err := next.RecordSubscriptionAttempt(source.ID, attempt, request.RouteId, failure == ""); err != nil {
			return err
		}
		if err := c.saveCatalog(ctx, next); err != nil {
			return err
		}
		if failure != "" {
			return errors.New(failure)
		}
		return nil
	}) {
		c.completeOperation(id, refreshLabel, "operation_cancelled")
	}
}
