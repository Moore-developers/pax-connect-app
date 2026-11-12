package controller

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/importers"
	"pax-connect/daemon/internal/singbox"
	"pax-connect/daemon/internal/subfetch"
)

// CatalogRepository is private storage behind the single controller owner.
type CatalogRepository interface {
	Load() (config.Catalog, bool, error)
	Save(context.Context, config.Catalog) error
}
type previewEntry struct {
	clientID    string
	expires     time.Time
	revision    uint64
	items       map[string]config.RouteConfig
	source      *previewSource
	sourceNodes []config.SourceNode
	complete    bool
}

// previewSource keeps the private source URL only for the confirm that follows this preview.
type previewSource struct {
	id   string
	name string
	host string
	url  string
}
type routesOwner struct {
	store                 CatalogRepository
	catalog               config.Catalog
	previews              map[string]previewEntry
	exiting               map[string]string
	exitResults           map[string]*paxv1.RouteExitSummary
	testing               map[string]string
	testResults           map[string]routeTestResult
	downloading           map[string]string
	downloadResults       map[string]*paxv1.DownloadTestSummary
	loadError             error
	measurementError      string
	measurementLoadFailed bool
}
type routeOperation struct {
	result          *paxv1.RouteSaveResult
	evidence        *config.SystemEvent
	label           string
	ctx             context.Context
	cancel          context.CancelFunc
	apply           func(context.Context) error
	switchesRuntime bool
}
type routeQuery struct {
	ctx   context.Context
	apply func(context.Context)
	done  chan struct{}
}

// ConfigureCatalog supplies the new source before the proxy owner starts.
func (c *Controller) ConfigureCatalog(store CatalogRepository) {
	c.routes = &routesOwner{store: store, previews: map[string]previewEntry{}, exiting: map[string]string{}, exitResults: map[string]*paxv1.RouteExitSummary{}, testing: map[string]string{}, testResults: map[string]routeTestResult{}, downloading: map[string]string{}, downloadResults: map[string]*paxv1.DownloadTestSummary{}}
}
func (c *Controller) initCatalog(ctx context.Context) {
	if c.routes == nil {
		return
	}
	r := c.routes
	catalog, exists, err := r.store.Load()
	if err != nil {
		r.loadError = err
		c.profileError("catalog_invalid")
		return
	}
	if !exists {
		catalog = config.NewCatalog()
		if p, err := c.proxy.load(); err == nil {
			_, _, err = catalog.Add(p)
			if err != nil {
				r.loadError = err
				return
			}
			_ = catalog.Select(p.ID)
		}
		if err := r.store.Save(ctx, catalog); err != nil {
			r.loadError = err
			return
		}
	}
	if catalog.SchemaVersion == 1 {
		migrated, err := config.UpgradeCatalog(catalog)
		if err != nil {
			r.loadError = err
			c.profileError("catalog_invalid")
			return
		}
		if err = r.store.Save(ctx, migrated); err != nil {
			r.loadError = err
			c.profileError("catalog_invalid")
			return
		}
		catalog = migrated
	}
	r.catalog = catalog
	c.loadMeasurements()
	c.publishCatalog(ctx)
}
func (c *Controller) publishCatalog(ctx context.Context) {
	if c.routes == nil || c.routes.loadError != nil {
		return
	}
	c.pruneSingleDownloads()
	// Revoke removed targets even when they have not acquired native/latency
	// admission and therefore have no detailed progress entry yet.
	valid := make(map[string]bool, len(c.routes.catalog.Routes))
	for _, record := range c.routes.catalog.Routes {
		valid[record.Config.ID] = true
	}
	c.mu.Lock()
	tests := c.tests
	if tests != nil {
		tests.mu.Lock()
	}
	for id, operation := range c.routes.testing {
		if valid[id] {
			continue
		}
		if tests != nil {
			if cancel := tests.ops[operation]; cancel != nil {
				cancel()
			}
			delete(tests.parents, operation)
			delete(tests.progress, id)
		}
		delete(c.routes.testing, id)
	}
	if tests != nil {
		tests.mu.Unlock()
	}
	c.mu.Unlock()
	var routes []*paxv1.NodeProfile
	visible := map[string]uint32{}
	hidden := map[string]uint32{}
	for _, record := range c.routes.catalog.Routes {
		resolved, err := c.resolvedRecord(ctx, record)
		if err != nil {
			c.profileError(tlsErrorCode(err))
			return
		}
		record = resolved
		view := record.Config.TLSResolved
		node := routeProto(record.Config, view)
		if record.Chain != nil {
			node.Chain = &paxv1.ChainRoute{FrontId: record.Chain.FrontID, FinalId: record.Chain.FinalID}
		}
		node.Revision = record.Revision
		node.ConfigRevision = connectionVersion(record, view)
		node.ExitSummary = c.routes.exitResults[record.Config.ID]
		node.CfEntry = (record.Config.Transport == "ws" || record.Config.Transport == "httpupgrade") && cfoptimizer.IsAddress(record.Config.Server)
		node.Transport = record.Config.Transport
		node.Security = record.Config.Security
		if record.Config.Security != "tls" {
			node.TlsPolicy = nil
		}
		if summary, ok := c.routes.downloadResults[record.Config.ID]; ok {
			node.DownloadSummary = summary
		}
		node.Origin = config.OriginLocal
		if record.IsSubscription() {
			node.Origin = config.OriginSubscription
			node.SourceId = record.SourceID
			if record.Hidden {
				hidden[record.SourceID]++
			} else {
				visible[record.SourceID]++
			}
		}
		node.Hidden = record.Hidden
		node.SourceRemoved = record.SourceRemoved
		if result, ok := c.routes.testResults[record.Config.ID]; ok {
			summary := &paxv1.RouteTestSummary{
				At:       timestamppb.New(result.at),
				StableMs: uint32(result.measurement.Stable.Milliseconds()), StableSamples: result.measurement.StableSamples, StableError: result.measurement.StableError, TestTarget: result.measurement.TestTarget,
				ExitIp:       result.exitIP,
				ExitLocation: exitLocationProto(result.measurement.ExitLocation), LastExitLocation: exitLocationProto(result.measurement.LastExitLocation),
				ColdMs: uint32(result.measurement.Cold.Milliseconds()), SecondMs: uint32(result.measurement.Second.Milliseconds()), Reused: result.measurement.Reused, Method: result.measurement.Method, FirstError: result.measurement.FirstError, SecondError: result.measurement.SecondError,
				Success:              result.success,
				DurationMs:           uint32(result.duration.Milliseconds()),
				TestedRevision:       result.revision,
				TestedConfigRevision: result.configRevision,
			}
			if result.exitError != "" {
				summary.ExitError = &paxv1.ProductError{Code: result.exitError}
			}
			if result.code != "" {
				summary.Error = &paxv1.ProductError{Code: result.code, MessageParams: result.params}
			}
			node.TestSummary = summary
		}
		routes = append(routes, node)
	}
	testing := make([]string, 0, len(c.routes.testing))
	for id := range c.routes.testing {
		testing = append(testing, id)
	}
	sort.Strings(testing)
	var summaries []*paxv1.SubscriptionSummary
	for _, source := range c.routes.catalog.Subscriptions {
		summary := &paxv1.SubscriptionSummary{
			Id: source.ID, Name: source.Name, Host: redactedHost(source.URL), Revision: source.Revision,
			RouteCount: visible[source.ID], HiddenCount: hidden[source.ID],
			UserAgent: source.UserAgent, HasRefreshChannel: source.HasRefreshChannel, LastRouteId: source.LastRouteID, LastAttempt: subscriptionAttemptProto(source.LastAttempt),
			LastRefresh: &paxv1.RefreshReport{Partial: source.LastRefresh.Partial, Added: uint32(source.LastRefresh.Added), Updated: uint32(source.LastRefresh.Updated), Hidden: uint32(source.LastRefresh.Hidden), Kept: uint32(source.LastRefresh.Kept), Removed: uint32(source.LastRefresh.Removed)},
		}
		if source.LastError != "" {
			summary.LastError = &paxv1.ProductError{Code: source.LastError}
		}
		if c.subs != nil {
			c.subs.mu.Lock()
			summary.RefreshOperationId = c.subs.active[source.ID]
			c.subs.mu.Unlock()
		}
		summaries = append(summaries, summary)
	}
	c.mu.Lock()
	before := proto.Clone(c.current).(*paxv1.Snapshot)
	c.current.MeasurementError = nil
	if c.routes.measurementError != "" {
		c.current.MeasurementError = &paxv1.ProductError{Code: c.routes.measurementError}
	}
	c.current.LatencyOperationId = ""
	c.current.TestOperationIds = map[string]string{}
	c.current.ExitOperationIds = map[string]string{}
	for route, op := range c.routes.exiting {
		c.current.ExitOperationIds[route] = op
	}
	if c.tests != nil {
		c.tests.mu.Lock()
		for route, child := range c.routes.testing {
			operation := child
			if parent := c.tests.parents[child]; parent != "" {
				operation = parent
			}
			c.current.TestOperationIds[route] = operation
		}
		if !c.tests.batchDownload {
			c.current.LatencyOperationId = c.tests.batch
		}
		c.tests.mu.Unlock()
	}
	c.current.RouteMeasurementProgress = c.measurementProgress()
	c.current.ExitPoolState = c.exitPoolProto()
	c.current.Routes = routes
	c.current.CatalogRevision = c.routes.catalog.Revision
	c.current.SelectedRouteId = c.routes.catalog.SelectedID
	if c.current.Status == paxv1.ProtectionStatus_PROTECTION_STATUS_OFF && c.current.SelectedRouteId != "" && c.current.GetDegradedReason().GetCode() == "profile_invalid" {
		c.current.DegradedReason = nil
	}
	c.current.Subscriptions = summaries
	c.current.TestingRouteIds = testing
	c.current.DownloadingRouteIds = nil
	c.current.DownloadOperationId = c.assessmentID
	c.current.DownloadQueue = nil
	if c.tests != nil {
		owner := c.tests
		owner.mu.Lock()
		if owner.bandwidth != "" {
			state := &paxv1.DownloadQueueState{Mode: "exclusive", OperationIds: map[string]string{}}
			if owner.bandwidth == "single-download-queue" {
				state.Mode = "single"
			} else if owner.batchDownload && owner.batch == owner.bandwidth {
				state.Mode = "batch"
			}
			for route, op := range c.routes.downloading {
				state.OperationIds[route] = op
				c.current.DownloadingRouteIds = append(c.current.DownloadingRouteIds, route)
				if state.Mode != "single" {
					c.current.DownloadOperationId = op
				} else if op == owner.singleDownloadActive {
					state.ActiveRouteId = route
					c.current.DownloadOperationId = op
				}
			}
			for _, job := range owner.singleDownloads {
				state.QueuedRouteIds = append(state.QueuedRouteIds, job.profile.ID)
			}
			if state.Mode == "exclusive" && c.current.DownloadOperationId == "" {
				c.current.DownloadOperationId = owner.bandwidth
			}
			c.current.DownloadQueue = state
		}
		owner.mu.Unlock()
	}

	c.current.RouteGroups = nil
	for _, group := range c.routes.catalog.Groups {
		c.current.RouteGroups = append(c.current.RouteGroups, &paxv1.RouteGroup{Id: group.ID, Name: group.Name, MemberIds: append([]string(nil), group.MemberIDs...)})
	}
	if !proto.Equal(before, c.current) {
		c.commitLocked()
	}
	c.mu.Unlock()
}

// redactedHost exposes only the source hostname, never the URL, query or credentials.
func redactedHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// sourceFor derives the display name and hostname of one subscription source.
func sourceFor(raw, name string) *previewSource {
	source := &previewSource{host: redactedHost(raw), url: raw}
	source.name = strings.TrimSpace(name)
	if source.name == "" {
		source.name = source.host
	}
	if len(source.name) > 128 {
		source.name = source.name[:128]
	}
	return source
}
func (c *Controller) catalogProfile(id string) (config.RouteConfig, error) {
	if c.routes == nil {
		return c.proxy.load()
	}
	if c.routes.loadError != nil {
		return config.RouteConfig{}, errors.New("catalog_invalid")
	}
	if id == "" {
		id = c.routes.catalog.SelectedID
	}
	record, ok := c.routes.catalog.Find(id)
	if !ok {
		return config.RouteConfig{}, errors.New("route_not_selected")
	}
	resolved, err := c.resolvedRecord(c.proxy.lifetime, record)
	return resolved.Config, err
}
func (c *Controller) query(ctx context.Context, apply func(context.Context)) error {
	if c.proxy == nil || c.routes == nil {
		return status.Error(codes.FailedPrecondition, "catalog_unavailable")
	}
	done := make(chan struct{})
	request := proxyIntent{query: &routeQuery{ctx: ctx, apply: apply, done: done}}
	select {
	case c.proxy.queue <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.proxy.lifetime.Done():
		return status.Error(codes.Unavailable, "daemon stopping")
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.proxy.lifetime.Done():
		return status.Error(codes.Unavailable, "daemon stopping")
	}
}
func (c *Controller) admitNamedRoute(ctx context.Context, action, name string, apply func(context.Context) error) (*paxv1.OperationAck, error) {
	e := operationEvidence("线路操作", "", "", "")
	e.Type = action
	e.Name = name
	return c.admitRouteOperation(ctx, &routeOperation{label: name, evidence: e, apply: apply})
}

func (c *Controller) admitRouteOperation(_ context.Context, operation *routeOperation) (*paxv1.OperationAck, error) {
	if c.proxy == nil || c.routes == nil {
		return nil, status.Error(codes.FailedPrecondition, "catalog_unavailable")
	}
	p := c.proxy
	p.mu.Lock()
	defer p.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.lifetime.Err() != nil {
		return nil, status.Error(codes.Unavailable, "daemon stopping")
	}
	if c.current.PendingOperationId != "" || len(p.queue) == cap(p.queue) {
		return nil, status.Error(codes.FailedPrecondition, "operation_busy")
	}
	id, err := operationID()
	if err != nil {
		return nil, status.Error(codes.Internal, "operation id unavailable")
	}
	if operation.switchesRuntime {
		operation.ctx, operation.cancel = context.WithCancel(p.lifetime)
		if p.routeCancels == nil {
			p.routeCancels = map[string]context.CancelFunc{}
		}
		p.routeCancels[id] = operation.cancel
	}
	c.catalogPending = id
	c.current.PendingOperationId = id
	c.commitLocked()
	label := "线路操作"
	if operation.label != "" {
		label = operation.label
	}
	p.queue <- proxyIntent{id: id, serial: p.serial.Add(1), label: label, routeOp: operation}
	return &paxv1.OperationAck{OperationId: id}, nil
}
func (c *Controller) saveCatalog(ctx context.Context, next config.Catalog) error {
	for _, r := range c.routes.catalog.Routes {
		if _, exists := next.Find(r.Config.ID); !exists && c.routingReferences(r.Config.ID) {
			return errors.New("route_referenced")
		}
	}

	next.PruneGroupMembers()
	if c.routes.loadError != nil {
		return c.routes.loadError
	}
	if err := c.routes.store.Save(ctx, next); err != nil {
		return err
	}
	c.routes.catalog = next
	c.publishCatalog(ctx)
	return nil
}
func (c *Controller) PreviewRoutes(ctx context.Context, req *paxv1.PreviewRoutesRequest) (*paxv1.RoutesPreview, error) {
	if len(req.ClientId) > 128 {
		return nil, status.Error(codes.InvalidArgument, "request_invalid")
	}
	body := []byte(req.Source)
	var source *previewSource
	var previousSourceID string
	if req.IsSubscription {
		// Bind an existing source before the slow fetch: deletion during download
		// must not turn an update into accidental creation of the same source.
		if err := c.query(ctx, func(context.Context) {
			if record, ok := c.routes.catalog.FindSubscriptionByURL(req.Source); ok {
				previousSourceID = record.ID
			}
		}); err != nil {
			return nil, err
		}
		// 订阅下载在 owner 之外完成：慢请求不阻塞既有 Stop/CRUD。
		fetched, err := c.fetchSource(ctx, req.Source)
		if err != nil {
			code, message := fetchFailure(err)
			return nil, status.Error(code, message)
		}
		body = fetched
		source = sourceFor(req.Source, req.SourceName)
	}
	batch, err := importers.ParseSubscription(body)
	if err != nil && len(batch.Items) == 0 {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	var result *paxv1.RoutesPreview
	var failure error
	if err := c.query(ctx, func(ctx context.Context) {
		if previousSourceID != "" {
			record, ok := c.routes.catalog.FindSubscriptionByURL(req.Source)
			if !ok || record.ID != previousSourceID {
				failure = status.Error(codes.FailedPrecondition, "subscription_stale")
				return
			}
		}
		result, failure = c.createPreview(ctx, batch, source)
		if failure == nil {
			entry := c.routes.previews[result.Token]
			entry.clientID = req.ClientId
			c.routes.previews[result.Token] = entry
		}
	}); err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	return result, nil
}

// createPreview runs under the single owner: it only reads the catalog and never writes.
func (c *Controller) createPreview(ctx context.Context, batch importers.ParsedBatch, source *previewSource) (*paxv1.RoutesPreview, error) {
	if c.routes.loadError != nil {
		return nil, status.Error(codes.FailedPrecondition, "catalog_invalid")
	}
	now := time.Now()
	for token, p := range c.routes.previews {
		if now.After(p.expires) {
			delete(c.routes.previews, token)
		}
	}
	if len(c.routes.previews) >= 4 {
		return nil, status.Error(codes.ResourceExhausted, "preview_limit")
	}
	token, err := operationID()
	if err != nil {
		return nil, err
	}
	result := &paxv1.RoutesPreview{Token: token, CatalogRevision: c.routes.catalog.Revision}
	entry := previewEntry{expires: now.Add(5 * time.Minute), revision: c.routes.catalog.Revision, items: map[string]config.RouteConfig{}, complete: true}
	if source != nil {
		var record config.SubscriptionRecord
		if existing, ok := c.routes.catalog.FindSubscriptionByURL(source.url); ok {
			record = existing
		}
		source.id = record.ID
		if record.Name != "" {
			source.name = record.Name
		}
		result.SubscriptionId = record.ID
		entry.source = source
		result.SourceHost = source.host
		result.SourceName = source.name
	}
	scratch := c.routes.catalog.Clone()
	seen := map[string]bool{}
	for _, item := range batch.Items {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		id, err := operationID()
		if err != nil {
			return nil, err
		}
		if item.Error != "" {
			entry.complete = false
		}
		row := &paxv1.RoutePreviewItem{DisplayName: item.Name, ProtocolName: item.Protocol, ItemId: id, Line: uint32(item.Line), ErrorCode: item.Error}
		if item.Route != nil {
			route := *item.Route
			if err := singbox.ValidateRouteDocument(ctx, config.DocumentForRoute(route)); err != nil {
				row.ErrorCode, _ = catalogFailure(err)
				entry.complete = false
				result.Items = append(result.Items, row)
				continue
			}
			duplicate, hidden, err := scratch.Register(route, sourceIDOf(source), seen)
			switch {
			case err != nil:
				entry.complete = false
				row.ErrorCode = err.Error()
			case duplicate:
				row.Duplicate = true
				row.Hidden = hidden
				row.Route = routeProto(route, config.SourceTLSPolicy(route))
			default:
				row.Route = routeProto(route, config.SourceTLSPolicy(route))
				entry.items[id] = route
			}
			if row.Route != nil {
				entry.sourceNodes = append(entry.sourceNodes, config.SourceNode{Name: route.Name, Config: route})
				row.Route.Transport = route.Transport
				row.Route.Security = route.Security
			}
		}
		result.Items = append(result.Items, row)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	c.routes.previews[token] = entry
	return result, nil
}

func sourceIDOf(source *previewSource) string {
	if source == nil {
		return ""
	}
	return source.id
}

func (c *Controller) fetchSource(ctx context.Context, url string) ([]byte, error) {
	if c.subs == nil || c.subs.fetcher == nil {
		return nil, status.Error(codes.FailedPrecondition, "subscription_unavailable")
	}
	return c.subs.fetcher.FetchSubscription(ctx, url)
}

func fetchFailure(err error) (codes.Code, string) {
	switch {
	case errors.Is(err, subfetch.ErrForbidden):
		return codes.PermissionDenied, subfetch.ErrForbidden.Error()
	case errors.Is(err, subfetch.ErrDNS):
		return codes.Unavailable, subfetch.ErrDNS.Error()
	case errors.Is(err, subfetch.ErrTLS):
		return codes.FailedPrecondition, subfetch.ErrTLS.Error()
	case errors.Is(err, subfetch.ErrTimeout):
		return codes.DeadlineExceeded, subfetch.ErrTimeout.Error()
	case errors.Is(err, subfetch.ErrHTTP):
		return codes.Unavailable, subfetch.ErrHTTP.Error()
	case errors.Is(err, subfetch.ErrConnection):
		return codes.Unavailable, subfetch.ErrConnection.Error()
	case errors.Is(err, subfetch.ErrCancelled), errors.Is(err, context.Canceled):
		return codes.Canceled, "operation_cancelled"
	case errors.Is(err, subfetch.ErrScheme), errors.Is(err, subfetch.ErrTarget), errors.Is(err, subfetch.ErrRedirects),
		errors.Is(err, subfetch.ErrTooLarge), errors.Is(err, subfetch.ErrInvalidURL):
		return codes.InvalidArgument, err.Error()
	case status.Code(err) != codes.Unknown:
		return status.Code(err), status.Convert(err).Message()
	default:
		return codes.Unavailable, subfetch.ErrFailed.Error()
	}
}
func (c *Controller) CancelRoutesPreview(ctx context.Context, req *paxv1.CancelRoutesPreviewRequest) (*emptypb.Empty, error) {
	var failure error
	err := c.query(ctx, func(context.Context) {
		if p, ok := c.routes.previews[req.Token]; ok && p.clientID != req.ClientId {
			failure = status.Error(codes.PermissionDenied, "preview_owner_mismatch")
			return
		}
		delete(c.routes.previews, req.Token)
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, failure
}
func (c *Controller) ConfirmImportRoutes(ctx context.Context, req *paxv1.ConfirmImportRoutesRequest) (*paxv1.OperationAck, error) {
	return c.mutationRequest(ctx, "confirm-import", req, req.ClientId, req.RequestId, func() (*paxv1.OperationAck, error) { return c.confirmImportRoutes(ctx, req) })
}
func (c *Controller) confirmImportRoutes(ctx context.Context, req *paxv1.ConfirmImportRoutesRequest) (*paxv1.OperationAck, error) {
	return c.admitNamedRoute(ctx, "route.import", "导入线路", func(ctx context.Context) error {
		p, ok := c.routes.previews[req.Token]
		if !ok || time.Now().After(p.expires) {
			return errors.New("preview_expired")
		}
		if p.clientID != req.ClientId {
			return &productError{code: "preview_owner_mismatch"}
		}
		if p.revision != req.ExpectedCatalogRevision || p.revision != c.routes.catalog.Revision {
			return errors.New("preview_stale")
		}
		if len(req.ItemIds) == 0 && (!req.Automatic || p.source == nil || len(p.sourceNodes) == 0) {
			return &productError{code: "empty_import_selection"}
		}
		for _, id := range req.ItemIds {
			if _, ok := p.items[id]; !ok {
				return errors.New("invalid_preview_item")
			}
		}
		next := c.routes.catalog.Clone()
		if p.source != nil {
			if err := confirmSubscription(&next, p, req.ItemIds, req.Automatic); err != nil {
				return err
			}
		} else {
			seen := map[string]bool{}
			for _, id := range req.ItemIds {
				if seen[id] {
					continue
				}
				seen[id] = true
				route, ok := p.items[id]
				if !ok {
					return errors.New("invalid_preview_item")
				}
				if _, _, err := next.Add(route); err != nil {
					return err
				}
			}
		}
		// Clear an OFF selection removed by a complete source update, without touching
		// a running frozen copy. Partial source bodies do not mark removal.
		if selected, ok := next.Find(next.SelectedID); ok && selected.SourceRemoved {
			c.mu.Lock()
			off := c.current.Status == paxv1.ProtectionStatus_PROTECTION_STATUS_OFF
			c.mu.Unlock()
			if off {
				next.SelectedID = ""
				next.Revision++
			}
		}
		// Import chooses an initial visible route only; it never changes a running path.
		if next.SelectedID == "" {
			for _, record := range next.Routes {
				if !record.Hidden && !record.SourceRemoved {
					if err := next.Select(record.Config.ID); err != nil {
						return err
					}
					break
				}
			}
		}

		before := map[string]uint64{}
		for _, record := range c.routes.catalog.Routes {
			before[record.Config.ID] = record.ConfigRevision
		}
		if err := c.saveCatalog(ctx, next); err != nil {
			return err
		}
		var automatic []automaticRouteTest
		for _, record := range next.Routes {
			if before[record.Config.ID] != record.ConfigRevision && !record.Hidden {
				automatic = append(automatic, automaticRouteTest{id: record.Config.ID, revision: record.Revision})
			}
		}
		c.enqueueAutomaticTests(automatic)
		delete(c.routes.previews, req.Token)
		return nil
	})
}

// confirmSubscription creates a source only when selected valid nodes are committed.
func confirmSubscription(next *config.Catalog, p previewEntry, selected []string, automatic bool) error {
	record, _, err := next.UpsertSubscription(p.source.name, p.source.url)
	if err != nil {
		return err
	}
	if p.source.id != "" && record.ID != p.source.id {
		return errors.New("preview_stale")
	}
	if automatic {
		if len(p.sourceNodes) == 0 {
			return &productError{code: "empty_import"}
		}
		_, err := next.MergeSource(record.ID, p.sourceNodes, p.complete)
		return err
	}
	chosen := map[string]bool{}
	for _, id := range selected {
		chosen[id] = true
	}
	for id, route := range p.items {
		if !chosen[id] {
			continue
		}
		if _, _, err := next.AddFromSource(route, record.ID, route.Name, false); err != nil {
			return err
		}
	}
	return nil
}
func (c *Controller) UpdateRoute(ctx context.Context, req *paxv1.RouteEditRequest) (*paxv1.OperationAck, error) {
	return c.documentRequest(ctx, "update", req, func() (*paxv1.OperationAck, error) { return c.updateRoute(ctx, req) })
}
func (c *Controller) updateRoute(ctx context.Context, req *paxv1.RouteEditRequest) (*paxv1.OperationAck, error) {
	return c.admitNamedRoute(ctx, "route.edit", "编辑线路", func(ctx context.Context) error {
		old, ok := c.routes.catalog.Find(req.RouteId)
		if !ok {
			return errors.New("route_not_found")
		}
		if old.Chain != nil {
			return errors.New("chain_edit_required")
		}
		if old.Revision != req.ExpectedRevision {
			return errors.New("route_stale")
		}
		if req.Port > 65535 {
			return errors.New("route_invalid")
		}
		p := routeFromEdit(req)
		p.ID = old.Config.ID
		if !req.ReplaceCredentials {
			p.Password = old.Config.Password
			p.UUID = old.Config.UUID
			p.ObfsPassword = old.Config.ObfsPassword
		}
		if req.DocumentJson != "" {
			var err error
			p, err = config.RouteFromDocument([]byte(req.DocumentJson), old.Config.ID, req.Name)
			if err != nil {
				return err
			}
			if err = singbox.ValidateRouteDocument(ctx, p.Document); err != nil {
				return err
			}
		}
		if req.DocumentJson == "" {
			var err error
			p, err = config.PreserveDocumentEdit(old.Config, p)
			if err != nil {
				return err
			}
		}
		if err := config.ValidateRoute(p); err != nil {
			return err
		}
		next := c.routes.catalog.Clone()

		if err := next.Update(req.RouteId, req.ExpectedRevision, p); err != nil {
			return err
		}
		if c.tls != nil {
			if _, err := c.tls.Resolve(ctx, p); err != nil {
				return err
			}
		}
		return c.saveCatalog(ctx, next)
	})
}
func (c *Controller) DeleteRoute(ctx context.Context, req *paxv1.DeleteRouteRequest) (*paxv1.OperationAck, error) {
	return c.admitNamedRoute(ctx, "route.delete", "删除线路", func(ctx context.Context) error {
		record, ok := c.routes.catalog.Find(req.RouteId)
		if !ok {
			return errors.New("route_not_found")
		}
		if record.Revision != req.ExpectedRevision {
			return errors.New("route_stale")
		}
		c.mu.Lock()
		active := c.current.GetCurrentRoute().GetId()
		c.mu.Unlock()
		next := c.routes.catalog.Clone()
		if err := next.Delete(req.RouteId, active); err != nil {
			return err
		}
		delete(c.routes.testResults, req.RouteId)
		return c.saveCatalog(ctx, next)
	})
}
func (c *Controller) SelectRoute(ctx context.Context, req *paxv1.SelectRouteRequest) (*paxv1.OperationAck, error) {
	return c.admitRouteOperation(ctx, &routeOperation{label: "切换线路", switchesRuntime: true, apply: func(ctx context.Context) error {
		c.mu.Lock()
		running := c.current.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF
		c.mu.Unlock()
		record, ok := c.routes.catalog.Find(req.RouteId)
		if !ok {
			return errors.New("route_not_found")
		}
		if record.Revision != req.ExpectedRouteRevision {
			return errors.New("route_stale")
		}
		if running {
			return c.selectCoreRoute(ctx, record)
		}
		next := c.routes.catalog.Clone()
		if err := next.Select(req.RouteId); err != nil {
			return err
		}
		return c.saveCatalog(ctx, next)
	}})
}

// catalogFailure unwraps typed product errors so presentation params survive the owner queue.
func catalogFailure(err error) (string, []string) {
	if err == nil {
		return "", nil
	}
	if errors.Is(err, context.Canceled) {
		return "operation_cancelled", nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "route_prepare_timeout", nil
	}
	var detailed interface{ ProductFailure() (string, []string) }
	if errors.As(err, &detailed) {
		return detailed.ProductFailure()
	}
	var product *productError
	if errors.As(err, &product) {
		return product.code, product.params
	}
	return catalogErrorCode(err), nil
}

func catalogErrorCode(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, config.ErrGroupLimit):
		return "group_limit"
	case errors.Is(err, config.ErrGroupInvalid):
		return "group_invalid"
	case errors.Is(err, config.ErrGroupName):
		return "group_name_invalid"
	case errors.Is(err, config.ErrGroupMember):
		return "group_member_invalid"
	case errors.Is(err, config.ErrRouteDocument):
		return "route_invalid"
	case errors.Is(err, config.ErrDocumentField):
		return "unsupported_field"
	case errors.Is(err, config.ErrCatalogDocument):
		return "catalog_invalid"
	case errors.Is(err, config.ErrCatalogWrite):
		return "route_save_failed"
	}
	code := err.Error()
	if strings.ContainsAny(code, " /:\n") {
		return "route_save_failed"
	}
	return code
}
