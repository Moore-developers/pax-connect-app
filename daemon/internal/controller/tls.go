package controller

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

// TLSPolicyRepository is owned by the same serial product-operation owner.
type TLSPolicyRepository interface {
	Resolve(context.Context, config.DebugProfile) (config.TLSPolicyView, error)
	Update(context.Context, config.DebugProfile, string, uint64, config.TLSMode, bool) error
}

// ConfigureTLS composes persistence before ConfigureProxy starts its owner.
func (c *Controller) ConfigureTLS(repository TLSPolicyRepository) { c.tls = repository }
func modeProto(mode config.TLSMode) paxv1.TLSMode {
	if mode == config.TLSSkip {
		return paxv1.TLSMode_TLS_MODE_SKIP_VERIFY
	}
	return paxv1.TLSMode_TLS_MODE_STRICT
}
func routeProto(p config.DebugProfile, v config.TLSPolicyView) *paxv1.NodeProfile {
	return &paxv1.NodeProfile{Id: p.ID, Name: p.Name, ProtocolName: p.ProtocolName, Server: p.Server, Port: uint32(p.Port), Group: "默认", ServerName: p.ServerName, TlsPolicy: &paxv1.RouteTLSPolicy{Mode: modeProto(v.Mode), UserOverride: v.UserOverride, NeedsConfirmation: v.NeedsConfirmation, IdentityToken: v.IdentityToken, PolicyRevision: v.Revision}}
}
func (c *Controller) resolveProfile(ctx context.Context) (config.DebugProfile, error) {
	p, err := c.catalogProfile("")
	if err != nil {
		return p, err
	}
	view := config.SourceTLSPolicy(p)
	if p.Front != nil {
		view = p.TLSResolved
	}
	if p.Front == nil && c.tls != nil && p.Security != "none" && p.Security != "reality" {
		view, err = c.tls.Resolve(ctx, p)
		if err != nil {
			return p, err
		}
	}
	p.TLSResolved = view
	if c.routes != nil {
		if record, ok := c.routes.catalog.Find(p.ID); ok {
			if p.Front == nil {
				p.ResolvedConfigRevision = connectionVersion(record, view)
			}
		}
	}
	if c.routes == nil {
		c.publishRoute(p, view)
	}
	return p, nil
}

// UpdateRouteTLS only saves a user's next-connection choice; it never restarts a running Box.
func (c *Controller) UpdateRouteTLS(ctx context.Context, req *paxv1.UpdateRouteTLSRequest) (*paxv1.OperationAck, error) {
	if c.proxy == nil || c.tls == nil {
		return nil, status.Error(codes.FailedPrecondition, "tls policy unavailable")
	}
	p := c.proxy
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lifetime.Err() != nil {
		return nil, status.Error(codes.Unavailable, "daemon stopping")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current.PendingOperationId != "" || c.current.Status == paxv1.ProtectionStatus_PROTECTION_STATUS_STARTING {
		return nil, status.Error(codes.FailedPrecondition, "operation_busy")
	}
	if len(p.queue) == cap(p.queue) {
		return nil, status.Error(codes.ResourceExhausted, "operation_busy")
	}
	mode := config.TLSStrict
	switch req.Mode {
	case paxv1.TLSMode_TLS_MODE_STRICT:
	case paxv1.TLSMode_TLS_MODE_SKIP_VERIFY:
		mode = config.TLSSkip
	default:
		return nil, status.Error(codes.InvalidArgument, "tls_policy_store_invalid")
	}
	id, err := operationID()
	if err != nil {
		return nil, status.Error(codes.Internal, "operation id unavailable")
	}
	c.tlsPending = id
	c.current.PendingOperationId = id
	c.commitLocked()
	p.queue <- proxyIntent{id: id, label: "证书设置", tls: &tlsIntent{routeID: req.RouteId, token: req.IdentityToken, revision: req.ExpectedRevision, mode: mode, confirm: req.ConfirmSkip}}
	return &paxv1.OperationAck{OperationId: id}, nil
}

type tlsIntent struct {
	routeID, token string
	revision       uint64
	mode           config.TLSMode
	confirm        bool
}

func (c *Controller) saveTLS(ctx context.Context, intent *tlsIntent) string {
	p, err := c.catalogProfile(intent.routeID)
	if err == nil && c.tls != nil {
		p.TLSResolved, err = c.tls.Resolve(ctx, p)
	}
	if err != nil {
		return tlsErrorCode(err)
	}
	if p.Security == "none" || p.Security == "reality" {
		return "tls_policy_not_applicable"
	}
	if p.ID != intent.routeID {
		return "tls_policy_stale"
	}
	if err := c.tls.Update(ctx, p, intent.token, intent.revision, intent.mode, intent.confirm); err != nil {
		return tlsErrorCode(err)
	}
	view := p.TLSResolved
	if !view.UserOverride || view.Mode != intent.mode {
		view.Revision++
	}
	view.Mode = intent.mode
	view.UserOverride = true
	view.NeedsConfirmation = false
	if c.routes == nil {
		c.publishRoute(p, view)
	} else {
		c.publishCatalog(ctx)
		if record, ok := c.routes.catalog.Find(p.ID); ok {
			c.enqueueAutomaticTests([]automaticRouteTest{{id: p.ID, revision: record.Revision}})
		}
	}
	return ""
}
func tlsErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, config.ErrTLSConfirmation):
		return "tls_confirmation_required"
	case errors.Is(err, config.ErrTLSStale):
		return "tls_policy_stale"
	case errors.Is(err, config.ErrTLSStore):
		return "tls_policy_store_invalid"
	case errors.Is(err, config.ErrTLSSave):
		return "tls_policy_save_failed"
	default:
		return "profile_invalid"
	}
}
func (c *Controller) activeTLS(p config.DebugProfile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.Security == "none" && (p.Front == nil || p.Front.Security == "none") {
		c.current.ActiveTls = nil
		c.commitLocked()
		return
	}
	if p.Front != nil && p.Front.TLSResolved.Mode == config.TLSSkip {
		p.TLSResolved.Mode = config.TLSSkip
	}
	c.current.ActiveTls = &paxv1.ActiveTLS{RouteId: p.ID, IdentityToken: p.TLSResolved.IdentityToken, Mode: modeProto(p.TLSResolved.Mode), Verified: false, Skipped: p.TLSResolved.Mode == config.TLSSkip}
	c.commitLocked()
}
func (c *Controller) profileError(code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.routes == nil {
		c.current.Routes = nil
	}
	// Do not pretend an invalid next profile changes the current running instance.
	if c.current.Status == paxv1.ProtectionStatus_PROTECTION_STATUS_OFF {
		c.current.DegradedReason = &paxv1.ProductError{Code: code}
	}
	c.commitLocked()
}
func (c *Controller) publishRoute(p config.DebugProfile, view config.TLSPolicyView) {
	c.mu.Lock()
	route := routeProto(p, view)
	if len(c.current.Routes) != 1 || !proto.Equal(c.current.Routes[0], route) {
		c.current.Routes = []*paxv1.NodeProfile{route}
		c.commitLocked()
	}
	c.mu.Unlock()
}

// connectionVersion is an opaque equality token combining the persisted document
// generation with effective TLS policy. Derivation avoids a two-file commit and
// remains stable across restart, rename, and policy edits with no runtime effect.
func connectionVersion(record config.RouteRecord, view config.TLSPolicyView) uint64 {
	if record.Config.Front != nil && record.Config.ResolvedConfigRevision != 0 {
		return record.Config.ResolvedConfigRevision
	}
	var value [10]byte
	binary.LittleEndian.PutUint64(value[:8], record.ConfigRevision)
	if record.Config.Security == "tls" {
		if view.Mode == config.TLSSkip {
			value[8] = 1
		}
		if view.NeedsConfirmation {
			value[9] = 1
		}
	}
	digest := sha256.Sum256(value[:])
	return binary.LittleEndian.Uint64(digest[:8]) | 1
}
