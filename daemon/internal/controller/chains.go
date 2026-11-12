package controller

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func (c *Controller) SaveChainRoute(ctx context.Context, req *pb.SaveChainRouteRequest) (*pb.OperationAck, error) {
	return c.mutationRequest(ctx, "save-chain", req, req.ClientId, req.RequestId, func() (*pb.OperationAck, error) {
		return c.admitNamedRoute(ctx, "route.chain", "保存链式线路", func(ctx context.Context) error {
			next := c.routes.catalog.Clone()
			if _, err := next.SaveChain(req.Id, req.Name, req.FrontId, req.FinalId, req.ExpectedRevision, req.FrontRevision, req.FinalRevision); err != nil {
				return err
			}
			return c.saveCatalog(ctx, next)
		})
	})
}

// resolvedRecord freezes both members and their effective TLS revisions. It
// shares the ordinary controller resolver; only singbox constructs detours.
func (c *Controller) resolvedRecord(ctx context.Context, r config.RouteRecord) (config.RouteRecord, error) {
	var policy func(context.Context, config.RouteConfig) (config.TLSPolicyView, error)
	if c.tls != nil {
		policy = c.tls.Resolve
	}
	return c.resolveRecordPolicy(ctx, r, policy)
}

// Both mutation preparation and diagnostics share composition/version rules;
// only the caller chooses whether policy lookup may persist identity changes.
func (c *Controller) resolveRecordPolicy(ctx context.Context, r config.RouteRecord, policy func(context.Context, config.RouteConfig) (config.TLSPolicyView, error)) (config.RouteRecord, error) {
	if r.Chain == nil {
		view := config.SourceTLSPolicy(r.Config)
		if policy != nil && r.Config.Security == "tls" {
			var err error
			view, err = policy(ctx, r.Config)
			if err != nil {
				return r, err
			}
		}
		r.Config.TLSResolved = view
		r.Config.ResolvedConfigRevision = connectionVersion(r, view)
		return r, nil
	}
	a, ok := c.routes.catalog.Find(r.Chain.FrontID)
	b, other := c.routes.catalog.Find(r.Chain.FinalID)
	if !ok || !other || a.Chain != nil || b.Chain != nil {
		return r, errors.New("chain_invalid")
	}
	front, err := c.resolveRecordPolicy(ctx, a, policy)
	if err != nil {
		return r, err
	}
	last, err := c.resolveRecordPolicy(ctx, b, policy)
	if err != nil {
		return r, err
	}
	p := last.Config
	p.ID = r.Config.ID
	p.Name = r.Config.Name
	p.Front = &front.Config
	sum := sha256.Sum256([]byte(config.RuntimeTag(front.Config) + config.RuntimeTag(last.Config)))
	r.ConfigRevision = binary.LittleEndian.Uint64(sum[:8]) | 1
	p.TLSResolved.NeedsConfirmation = p.TLSResolved.NeedsConfirmation || front.Config.TLSResolved.NeedsConfirmation

	p.ResolvedConfigRevision = 0
	p.ResolvedConfigRevision = connectionVersion(config.RouteRecord{Config: p, ConfigRevision: r.ConfigRevision}, p.TLSResolved)
	r.Config = p
	return r, nil
}
