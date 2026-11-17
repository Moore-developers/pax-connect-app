package controller

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/singbox"
)

// Kept only by the CF owner for five minutes. At most 100 drafts of <=64 KiB
// plus requests of <=128 KiB (under 32 MiB); no credentials enter the result.
type cfValidatedDraft struct {
	request                 *pb.StartCFValidationRequest
	profile                 config.RouteConfig
	operation, network      string
	generation, chainConfig uint64
	expires                 time.Time
	ready                   bool
}

// StartCFValidation detaches accepted work from the caller, using the original
// test owner's lifetime, cancellation and two CF validation slots.
func (c *Controller) StartCFValidation(ctx context.Context, req *pb.StartCFValidationRequest) (*pb.OperationAck, error) {
	admit := func() (*pb.OperationAck, error) {
		if c.cf == nil || c.physical == nil || c.tests == nil || c.tester == nil {
			return nil, status.Error(codes.Unimplemented, "cf_unavailable")
		}
		if _, ok := cfoptimizer.NormalizeCandidate(req.Ip); !ok {
			return nil, status.Error(codes.InvalidArgument, "cf_ip_invalid")
		}
		if len(req.SecretUpdates) > 32 || len(req.RouteId) > 128 || len(req.ChainId) > 128 || req.ExpectedRouteRevision == 0 || req.ExpectedConfigRevision == 0 || req.DocumentJson == nil && len(req.SecretUpdates) != 0 || len(req.GetDocumentJson()) > config.MaxDocumentBytes || proto.Size(req) > 128<<10 {
			return nil, status.Error(codes.InvalidArgument, "request_invalid")
		}
		token, err := operationID()
		if err != nil {
			return nil, err
		}
		draft := &cfValidatedDraft{request: req, expires: time.Now().Add(5 * time.Minute)}
		c.cf.mu.Lock()
		for id, old := range c.cf.validations {
			if !old.expires.After(time.Now()) || old.request.RouteId == req.RouteId {
				delete(c.cf.validations, id)
			}
		}
		if len(c.cf.validations) >= 100 {
			c.cf.mu.Unlock()
			return nil, status.Error(codes.ResourceExhausted, "cf_busy")
		}
		c.cf.validations[token] = draft
		c.cf.mu.Unlock()
		ack, err := c.startOwnedProbe(func(task context.Context, id string) (*pb.OperationResult, error) {
			result, err := c.validateCFDraft(task, id, token, draft)
			if err != nil || !result.GetSuccess() {
				c.cf.mu.Lock()
				delete(c.cf.validations, token)
				c.cf.mu.Unlock()
			}
			return &pb.OperationResult{CfValidation: result}, err
		}, nil)
		if err != nil {
			c.cf.mu.Lock()
			delete(c.cf.validations, token)
			c.cf.mu.Unlock()
		}
		return ack, err
	}
	if req.Context != nil {
		return c.contextRequest(ctx, "cf.validate", req, req.Context, admit)
	}
	return c.appRequest(ctx, "cf.validate", req, "", admit)
}

func (c *Controller) cfTarget(ctx context.Context, id string, revision, configRevision uint64) (config.RouteRecord, error) {
	record, ok := c.routes.catalog.Find(id)
	if !ok || record.Hidden || record.Chain != nil || record.Revision != revision {
		return record, errors.New("route_stale")
	}
	resolved, err := c.resolvedRecord(ctx, record)
	if err != nil {
		return record, err
	}
	if connectionVersion(resolved, resolved.Config.TLSResolved) != configRevision {
		return record, errors.New("route_stale")
	}
	return record, nil
}
func (c *Controller) cfChainVersion(ctx context.Context, req *pb.StartCFValidationRequest) (uint64, error) {
	if req.ChainId == "" {
		if req.ChainRevision != 0 {
			return 0, errors.New("route_stale")
		}
		return 0, nil
	}
	chain, ok := c.routes.catalog.Find(req.ChainId)
	if !ok || chain.Hidden || chain.Chain == nil || chain.Revision != req.ChainRevision || chain.Chain.FrontID != req.RouteId && chain.Chain.FinalID != req.RouteId {
		return 0, errors.New("route_stale")
	}
	resolved, err := c.resolvedRecord(ctx, chain)
	return resolved.Config.ResolvedConfigRevision, err
}
func (c *Controller) validateCFDraft(ctx context.Context, id, token string, draft *cfValidatedDraft) (*pb.CFValidationResult, error) {
	req := draft.request
	var profile config.RouteConfig
	var chainConfig uint64
	var failure error
	err := c.query(ctx, func(ctx context.Context) {
		record, e := c.cfTarget(ctx, req.RouteId, req.ExpectedRouteRevision, req.ExpectedConfigRevision)
		if e != nil {
			failure = e
			return
		}
		previous := config.DocumentForRoute(record.Config)
		raw := previous
		if req.DocumentJson != nil {
			raw, e = mergeRouteEditSecrets(req.GetDocumentJson(), previous, req.SecretUpdates)
			if e != nil {
				failure = e
				return
			}
		}
		profile, e = config.RouteFromDocument(raw, req.RouteId, record.Config.Name)
		if e != nil {
			failure = e
			return
		}
		if e = singbox.ValidateRouteDocument(ctx, raw); e != nil {
			failure = errors.New("route_invalid")
			return
		}
		var document map[string]any
		_ = json.Unmarshal(raw, &document)
		document["server"], _ = cfoptimizer.NormalizeCandidate(req.Ip)
		raw, _ = json.Marshal(document)
		profile, e = config.RouteFromDocument(raw, req.RouteId, record.Config.Name)
		if e != nil {
			failure = e
			return
		}
		failure = c.checkDocumentTLS(ctx, profile, previous)
		if failure != nil {
			return
		}
		chainConfig, failure = c.cfChainVersion(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	local, _ := c.physical.localSnapshot()
	value, err := c.ValidateCFCandidate(ctx, &pb.CFValidationRequest{RouteId: req.RouteId, ExpectedRevision: req.ExpectedRouteRevision, DocumentJson: string(profile.Document), Ip: req.Ip, ChainId: req.ChainId, ChainRevision: req.ChainRevision})
	if err != nil {
		return nil, err
	}
	result := &pb.CFValidationResult{Candidate: value, Network: value.Network, RouteRevision: req.ExpectedRouteRevision, ConfigRevision: req.ExpectedConfigRevision, Success: value.Error == nil}
	if !result.Success {
		return result, errors.New("cf_validation_failed")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	latest, _ := c.physical.localSnapshot()
	if local.generation != latest.generation {
		return nil, errors.New("network_changed")
	}
	err = c.query(ctx, func(ctx context.Context) {
		_, failure = c.cfTarget(ctx, req.RouteId, req.ExpectedRouteRevision, req.ExpectedConfigRevision)
		if failure == nil {
			var version uint64
			version, failure = c.cfChainVersion(ctx, req)
			if failure == nil && version != chainConfig {
				failure = errors.New("route_stale")
			}
		}
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	digest, err := operationID()
	if err != nil {
		return nil, err
	}
	c.cf.mu.Lock()
	defer c.cf.mu.Unlock()
	if c.cf.validations[token] != draft {
		return nil, errors.New("cf_validation_expired")
	}
	draft.profile, draft.operation, draft.network, draft.generation, draft.chainConfig = profile, id, value.Network, latest.generation, chainConfig
	draft.expires = value.At.AsTime().Add(5 * time.Minute)
	draft.ready = true
	result.ValidationId, result.DraftDigest, result.ExpiresAt = token, digest, timestamppb.New(draft.expires)
	return result, nil
}

// SaveValidatedCFRoute checks proof, source versions and network inside the
// existing serial config queue, then commits only the privately frozen document.
func (c *Controller) SaveValidatedCFRoute(ctx context.Context, req *pb.SaveValidatedCFRouteRequest) (*pb.OperationAck, error) {
	admit := func() (*pb.OperationAck, error) {
		if c.cf == nil || c.physical == nil {
			return nil, status.Error(codes.Unimplemented, "cf_unavailable")
		}
		outcome := &pb.RouteSaveResult{}
		return c.admitRouteOperation(ctx, &routeOperation{label: "保存CF线路", result: outcome, apply: func(ctx context.Context) error {
			c.cf.mu.Lock()
			entry := c.cf.validations[req.ValidationId]
			var draft *cfValidatedDraft
			if entry != nil && entry.ready {
				copy := *entry
				draft = &copy
			}
			c.cf.mu.Unlock()
			if draft == nil || !draft.ready || !draft.expires.After(time.Now()) {
				return errors.New("cf_validation_expired")
			}
			if req.RouteId != draft.request.RouteId || req.ExpectedRouteRevision != draft.request.ExpectedRouteRevision || req.ExpectedConfigRevision != draft.request.ExpectedConfigRevision {
				return errors.New("route_stale")
			}
			c.mu.Lock()
			op := c.operations[draft.operation]
			success := op != nil && op.State == "succeeded"
			c.mu.Unlock()
			if !success {
				return errors.New("cf_validation_expired")
			}
			if _, err := c.cfTarget(ctx, req.RouteId, req.ExpectedRouteRevision, req.ExpectedConfigRevision); err != nil {
				return err
			}
			version, err := c.cfChainVersion(ctx, draft.request)
			if err != nil {
				return err
			}
			if version != draft.chainConfig {
				return errors.New("route_stale")
			}
			physical, err := c.physical.sample(ctx)
			if err != nil || physical.Epoch != draft.network {
				return errors.New("network_changed")
			}
			local, _ := c.physical.localSnapshot()
			if local.generation != draft.generation {
				return errors.New("network_changed")
			}
			next := c.routes.catalog.Clone()
			if err = next.Update(req.RouteId, req.ExpectedRouteRevision, draft.profile); err != nil {
				return err
			}
			saved, _ := next.Find(req.RouteId)
			resolved, err := c.resolvedRecord(ctx, saved)
			if err != nil {
				return err
			}
			// Recheck token after other clients' validation/scan invalidation, while
			// holding the CF owner through the actual atomic file commit.
			c.cf.mu.Lock()
			defer c.cf.mu.Unlock()
			if c.cf.validations[req.ValidationId] != entry || !draft.expires.After(time.Now()) {
				return errors.New("cf_validation_expired")
			}
			local, _ = c.physical.localSnapshot()
			if local.generation != draft.generation {
				return errors.New("network_changed")
			}
			if err = c.saveCatalog(ctx, next); err != nil {
				return err
			}
			delete(c.cf.validations, req.ValidationId)
			outcome.RouteId, outcome.RouteRevision, outcome.ConfigRevision, outcome.CatalogRevision = req.RouteId, saved.Revision, connectionVersion(resolved, resolved.Config.TLSResolved), next.Revision
			return nil
		}})
	}
	if req.Context != nil {
		return c.contextRequest(ctx, "cf.save", req, req.Context, admit)
	}
	return c.appRequest(ctx, "cf.save", req, "", admit)
}
