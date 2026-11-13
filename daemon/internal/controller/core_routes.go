package controller

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
)

type catalogCore interface {
	ApplyCatalog(context.Context, []config.RouteConfig, config.RouteConfig) error
	SwitchRoute(context.Context, config.RouteConfig) (bool, error)
}

func (c *Controller) coreProfiles(ctx context.Context) []config.RouteConfig {
	profiles := []config.RouteConfig{}
	if c.routes == nil {
		return profiles
	}
	for _, record := range c.routes.catalog.Routes {
		if record.Hidden {
			continue
		}
		resolved, err := c.resolvedRecord(ctx, record)
		if err != nil || resolved.Config.TLSResolved.NeedsConfirmation {
			continue
		}
		p := resolved.Config
		c.rememberHistoryRoute(p)
		profiles = append(profiles, p)
	}
	return profiles
}
func (c *Controller) applyCore(ctx context.Context, p config.RouteConfig) error {
	var familyErr error
	p, familyErr = c.freezeRouteFamily(ctx, p)
	if familyErr != nil {
		return familyErr
	}
	if c.privacy != nil {
		if c.privacy.failure != nil {
			return errors.New("network_privacy_unavailable")
		}
		p.NetworkPrivacy = c.privacy.value
	}
	if c.routing != nil {
		if c.routing.loadError != nil {
			return c.routing.loadError
		}
		l := c.routing.library.Clone()
		l.WebRTC = p.NetworkPrivacy.WebRTC
		p.Routing = &l
		p.RoutingApplications = c.routing.apps
		if len(p.RoutingApplications) == 0 {
			p.RoutingApplications = c.routing.applicationQuery(ctx)
		}
	}
	if core, ok := c.coreSession.(catalogCore); ok {
		profiles := c.coreProfiles(ctx)
		c.rememberHistoryRoute(p)
		return core.ApplyCatalog(ctx, profiles, p)
	}
	c.rememberHistoryRoute(p)
	return c.coreSession.Apply(ctx, p)
}

func (c *Controller) selectCoreRoute(ctx context.Context, record config.RouteRecord) (failure error) {
	started := time.Now()
	c.mu.Lock()
	operation := c.catalogPending
	c.mu.Unlock()
	c.beginConnectionAttempt(operation, record.Config)
	coreAttempted := false
	defer func() {
		code, params := catalogFailure(failure)
		if coreAttempted {
			c.connectionStep("core", started, diagnosticCause(code, params))
		}
		code, _ = catalogFailure(failure)
		if code == "" {
			code = "ok"
		}
		telemetry.RecordDiagnostic(telemetry.Diagnostic{OperationID: operation, Stage: "route_select", Code: code, Route: telemetry.RouteToken(record.Config.ID), Success: failure == nil, DurationMS: time.Since(started).Milliseconds()})
	}()
	core, ok := c.coreSession.(catalogCore)
	if !ok {
		c.connectionStep("core", started, "runtime_switch_unavailable")
		return errors.New("runtime_switch_unavailable")
	}
	resolved, err := c.resolvedRecord(ctx, record)
	if err != nil {
		code, params := catalogFailure(err)
		c.connectionStep("configuration", started, diagnosticCause(code, params))
		return err
	}
	p, err := c.freezeRouteFamily(ctx, resolved.Config)
	if err != nil {
		return err
	}
	c.lastConnectionAttempt.TargetRevision = p.ResolvedConfigRevision
	c.connectionStep("configuration", started, "")
	if p.TLSResolved.NeedsConfirmation {
		c.connectionStep("configuration", started, "tls_confirmation_required")
		return config.ErrTLSConfirmation
	}
	sameFamily := true
	if family, ok := c.coreSession.(interface{ MatchesAddressFamily(bool) bool }); ok {
		sameFamily = family.MatchesAddressFamily(p.IPv4Only)
	}
	if sameFamily && c.proxy.running.valid() && c.proxy.running.tag == config.RuntimeTag(p) {
		if observer, ok := c.coreSession.(coreObserver); ok {
			observe, cancel := context.WithTimeout(ctx, time.Second)
			actual, observeErr := observer.ObserveCore(observe)
			cancel()
			if observeErr != nil || actual.Status != "STARTED" || actual.SelectedTag != c.proxy.running.tag || c.proxy.running.session != "" && actual.RuntimeSession != c.proxy.running.session {
				return errors.New("runtime_binding_unconfirmed")
			}
			return nil
		}
	}
	c.cancelCurrentPath("new_operation")
	c.proxy.runtimeEpoch++
	c.mu.Lock()
	c.current.PendingTargetRouteId = p.ID
	c.commitLocked()
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.current.PendingTargetRouteId = ""; c.commitLocked(); c.mu.Unlock() }()
	next := c.routes.catalog.Clone()
	if err := next.Select(p.ID); err != nil {
		return err
	}
	old := c.routes.catalog.Clone()
	// Save with the serial owner before sending the runtime mutation. Any failure
	// is rolled back with a fresh context; Stop remains able to cancel the RPC.
	if err := c.routes.store.Save(ctx, next); err != nil {
		return err
	}
	coreAttempted = true
	c.rememberHistoryRoute(p)
	committed, err := core.SwitchRoute(ctx, p)
	if err != nil && err.Error() == "runtime_refresh_required" {
		// Newly imported or edited nodes change the outbound structure; reload once.
		err = c.applyCore(ctx, p)
		committed = err == nil
	}
	if !committed && err != nil && err.Error() == "core_selection_unknown" {
		c.proxy.running = runningBinding{}
		c.routes.catalog = next
		c.publishCatalog(ctx)
		c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED, "runtime_observation_unavailable", nil)
		c.mu.Lock()
		c.current.CurrentRoute = nil
		c.commitLocked()
		c.mu.Unlock()
		return err
	}
	if !committed {
		if err == nil {
			err = errors.New("route_selection_failed")
		}
		recovery, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer done()
		if restoreErr := c.routes.store.Save(recovery, old); restoreErr != nil {
			return errors.New("catalog_recovery_required")
		}
		return err
	}
	c.routes.catalog = next
	c.publishCatalog(ctx)
	c.bindCurrentPath(p)
	c.activeTLS(p)
	reason := "protection_unverified"
	if err != nil {
		reason = "route_connections_pending"
	}
	c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED, reason, &p.NodeProfile)
	c.mu.Lock()
	for _, route := range c.current.Routes {
		if route.Id == p.ID {
			c.current.CurrentRoute = proto.Clone(route).(*pb.NodeProfile)
		}
	}
	c.commitLocked()
	c.mu.Unlock()
	if err == nil {
		c.beginPathCheck(operation, "switch", false, time.Time{})
	}
	return err
}
