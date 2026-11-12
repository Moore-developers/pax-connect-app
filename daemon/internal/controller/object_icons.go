package controller

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/siteicon"
)

// ConfigureObjectIcons wires the private cache and route-aware anonymous fetch boundary.
func (c *Controller) ConfigureObjectIcons(store *siteicon.Store) { c.icons = store }

// QueryObjectIcon checks the cache; only an explicit fetch intent performs network work.
func (c *Controller) QueryObjectIcon(ctx context.Context, q *pb.ObjectIconRequest) (*pb.ObjectIconResult, error) {
	if c.icons == nil || c.history == nil {
		return nil, status.Error(codes.Unimplemented, "icon_unavailable")
	}
	c.mu.Lock()
	generation := c.current.GetHistory().GetLogDeletionGeneration()
	c.mu.Unlock()
	if generation != q.LogDeletionGeneration {
		return nil, status.Error(codes.FailedPrecondition, "icon_generation_expired")
	}
	host, err := siteicon.Host(q.Host)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "icon_target_disallowed")
	}
	value, err := c.icons.Get(ctx, host, q.Fetch, generation)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, "icon_cancelled")
		}
		if errors.Is(err, siteicon.ErrBusy) {
			return nil, status.Error(codes.ResourceExhausted, "icon_busy")
		}
		return nil, status.Error(codes.Unavailable, "icon_unavailable")
	}
	c.mu.Lock()
	current := c.current.GetHistory().GetLogDeletionGeneration()
	c.mu.Unlock()
	if generation != current {
		return nil, status.Error(codes.FailedPrecondition, "icon_generation_expired")
	}
	out := &pb.ObjectIconResult{Host: host, Png: value.PNG, State: value.State, Reason: value.Reason, LogDeletionGeneration: generation}
	if !value.Expires.IsZero() {
		out.ExpiresAt = timestamppb.New(value.Expires)
	}
	return out, nil
}

// IconRoute freezes the active outbound config. Direct mode uses the standard physical path;
// transitional state is refused rather than silently falling back to a different route.
func (c *Controller) IconRoute(ctx context.Context) (*config.RouteConfig, error) {
	if c.proxy == nil {
		return nil, nil
	}
	var route *config.RouteConfig
	var failure error
	err := c.query(ctx, func(context.Context) {
		c.mu.Lock()
		state := c.current.GetStatus()
		current := c.current.GetCurrentRoute()
		c.mu.Unlock()
		if state == pb.ProtectionStatus_PROTECTION_STATUS_OFF {
			return
		}
		if state != pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED && state != pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED {
			failure = errors.New("icon_route_unavailable")
			return
		}
		if current == nil || c.routes == nil {
			failure = errors.New("icon_route_unavailable")
			return
		}
		record, ok := c.routes.catalog.Find(current.Id)
		if !ok {
			failure = errors.New("icon_route_unavailable")
			return
		}
		resolved, e := c.resolvedRecord(ctx, record)
		if e != nil {
			failure = e
			return
		}
		p := resolved.Config
		if p.Security == "tls" && c.tls != nil {
			view, e := c.tls.Resolve(ctx, p)
			if e != nil || view.NeedsConfirmation || view.UserOverride || view.Mode == config.TLSSkip {
				failure = errors.New("icon_tls_unavailable")
				return
			}
			p.TLSResolved = view
		}
		route = &p
	})
	if err != nil {
		return nil, err
	}
	return route, failure
}
