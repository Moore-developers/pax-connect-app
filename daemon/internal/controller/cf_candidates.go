package controller

import (
	"context"
	"encoding/json"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/singbox"
	"strings"
	"sync"
	"time"
)

type cfOwner struct {
	workspace *cfWorkspace
	mu        sync.Mutex
	cache     map[string]*paxv1.CFCandidate
	slots     chan struct{}
}

// ConfigureCFCandidates enables only real candidate validation, never demo probes.
func (c *Controller) ConfigureCFCandidates() {
	c.cf = &cfOwner{cache: map[string]*paxv1.CFCandidate{}, slots: make(chan struct{}, 2)}
}
func (c *Controller) QueryCFCandidates(ctx context.Context, _ *emptypb.Empty) (*paxv1.CFCandidates, error) {
	if c.cf == nil || c.physical == nil {
		return nil, status.Error(codes.Unimplemented, "cf_unavailable")
	}
	physical, err := c.physical.sample(ctx)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "physical_network_unavailable")
	}
	values := &paxv1.CFCandidates{Network: physical.Epoch}
	c.cf.mu.Lock()
	defer c.cf.mu.Unlock()
	seen := map[string]bool{}
	for _, value := range c.cf.cache {
		if value.Network == physical.Epoch {
			values.Items = append(values.Items, &paxv1.CFCandidate{Ip: value.Ip, Source: "recent", Network: physical.Epoch})
			seen[value.Ip] = true
		}
	}
	for _, ip := range cfoptimizer.Seeds("") {
		if !seen[ip] && len(values.Items) < 100 {
			values.Items = append(values.Items, &paxv1.CFCandidate{Ip: ip, Source: "official_seed", Network: physical.Epoch})
		}
	}
	return values, nil
}
func (c *Controller) ValidateCFCandidate(ctx context.Context, request *paxv1.CFValidationRequest) (*paxv1.CFCandidate, error) {
	if c.cf == nil || c.physical == nil || c.tests == nil || c.tester == nil {
		return nil, status.Error(codes.Unimplemented, "cf_unavailable")
	}
	ip, ok := cfoptimizer.NormalizeCandidate(request.Ip)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "cf_ip_invalid")
	}
	select {
	case c.cf.slots <- struct{}{}:
		defer func() { <-c.cf.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	bounded, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	stopLifetime := context.AfterFunc(c.tests.lifetime, cancel)
	defer stopLifetime()
	c.tests.mu.Lock()
	if c.tests.closed {
		c.tests.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "daemon stopping")
	}
	c.tests.probes.Add(1)
	c.tests.mu.Unlock()
	defer c.tests.probes.Done()
	physical, err := c.physical.sample(bounded)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "physical_network_unavailable")
	}
	if physical.IPv6State == "unknown" {
		return nil, status.Error(codes.FailedPrecondition, "network_observation_unavailable")
	}
	if physical.IPv4Only() && strings.Contains(ip, ":") {
		return nil, status.Error(codes.FailedPrecondition, "ipv6_unavailable")
	}
	var profile config.RouteConfig
	var failure error
	var chainVersion uint64
	var pathFront bool
	err = c.query(bounded, func(ctx context.Context) {
		record, exists := c.routes.catalog.Find(request.RouteId)
		if !exists || record.Hidden || record.Revision != request.ExpectedRevision {
			failure = status.Error(codes.FailedPrecondition, "route_stale")
			return
		}
		profile, failure = config.RouteFromDocument([]byte(request.DocumentJson), record.Config.ID, record.Config.Name)
		if failure != nil {
			return
		}
		if (profile.Transport != "ws" && profile.Transport != "httpupgrade") || (profile.ProtocolName != "vmess" && profile.ProtocolName != "vless" && profile.ProtocolName != "trojan") {
			failure = config.ErrRouteDocument
			return
		}
		if failure = singbox.ValidateRouteDocument(ctx, []byte(request.DocumentJson)); failure != nil {
			return
		}
		profile.Server = ip
		var changed map[string]any
		_ = json.Unmarshal(profile.Document, &changed)
		changed["server"] = ip
		profile.Document, _ = json.Marshal(changed)
		profile.TLSResolved = config.SourceTLSPolicy(profile)
		if c.tls != nil {
			var current config.TLSPolicyView
			current, failure = c.tls.Resolve(ctx, record.Config)
			if failure == nil && current.IdentityToken == config.TLSIdentity(profile) {
				profile.TLSResolved = current
			}
		}
		if request.ChainId != "" && failure == nil {
			chain, ok := c.routes.catalog.Find(request.ChainId)
			if !ok || chain.Chain == nil || chain.Revision != request.ChainRevision || (chain.Chain.FrontID != request.RouteId && chain.Chain.FinalID != request.RouteId) {
				failure = status.Error(codes.FailedPrecondition, "route_stale")
				return
			}
			resolved, e := c.resolvedRecord(ctx, chain)
			if e != nil {
				failure = e
				return
			}
			chainVersion = resolved.Config.ResolvedConfigRevision
			pathFront = chain.Chain.FrontID == request.RouteId
			if pathFront {
				front := profile
				profile = resolved.Config
				profile.Front = &front
			} else {
				profile.Front = resolved.Config.Front
			}
		}
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		if grpc, ok := status.FromError(failure); ok {
			return nil, grpc.Err()
		}
		return nil, status.Error(codes.FailedPrecondition, "route_invalid")
	}
	if profile.Front == nil || !pathFront {
		var document map[string]any
		_ = json.Unmarshal(profile.Document, &document)
		document["server"] = ip
		if profile.Front == nil {
			document["bind_interface"] = physical.Name
		}
		profile.Server = ip
		profile.Document, _ = json.Marshal(document)
	}
	if profile.Front != nil {
		front := *profile.Front
		var document map[string]any
		_ = json.Unmarshal(front.Document, &document)
		document["bind_interface"] = physical.Name
		front.Document, _ = json.Marshal(document)
		profile.Front = &front
	}

	profile = bindRouteNetwork(profile, physical)
	if err := config.ValidateAddressFamily(profile); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	select {
	case <-c.tests.slots:
		defer func() { c.tests.slots <- struct{}{} }()
	case <-bounded.Done():
		return nil, bounded.Err()
	}
	duration, probeErr := c.tester.TestRoute(bounded, profile)
	latest, e := c.physical.sample(bounded)
	if e != nil || latest.Epoch != physical.Epoch {
		return nil, status.Error(codes.FailedPrecondition, "network_changed")
	}
	if request.ChainId != "" {
		stale := false
		if err := c.query(bounded, func(ctx context.Context) {
			chain, ok := c.routes.catalog.Find(request.ChainId)
			if !ok || chain.Revision != request.ChainRevision {
				stale = true
				return
			}
			resolved, e := c.resolvedRecord(ctx, chain)
			stale = e != nil || resolved.Config.ResolvedConfigRevision != chainVersion
		}); err != nil {
			return nil, err
		}
		if stale {
			return nil, status.Error(codes.FailedPrecondition, "route_stale")
		}
	}
	value := &paxv1.CFCandidate{Ip: ip, Source: "validated", Network: physical.Epoch, At: timestamppb.Now()}
	if probeErr != nil {
		code := testErrorCode(probeErr, bounded)
		value.Error = &paxv1.ProductError{Code: code.Error()}
		if p, ok := code.(*productError); ok {
			value.Error.MessageParams = p.params
		}
	} else {
		value.LatencyMs = uint32(duration.Milliseconds())
	}
	c.cf.mu.Lock()
	if len(c.cf.cache) >= 100 {
		c.cf.cache = map[string]*paxv1.CFCandidate{}
	}
	c.cf.cache[ip] = value
	if w := c.cf.workspace; w != nil && w.document.Accepts(ip, physical.Epoch) {
		key := cfProofKey(profile, ip, physical.Epoch)
		now := time.Now()
		for proof, at := range w.proofs {
			if now.Sub(at) >= 5*time.Minute {
				delete(w.proofs, proof)
			}
		}
		if len(w.proofs) >= 1000 {
			var oldest string
			var at time.Time
			for proof, when := range w.proofs {
				if oldest == "" || when.Before(at) {
					oldest = proof
					at = when
				}
			}
			delete(w.proofs, oldest)
		}
		sample := cfoptimizer.Sample{Value: float64(value.LatencyMs), Attempt: value.At.AsTime(), Error: value.GetError().GetCode(), Rounds: 1}
		w.document.Merge(cfoptimizer.Record{IP: ip, Prefix: cfoptimizer.Prefix(ip), Network: physical.Epoch, Validation: sample, ValidationKey: key}, time.Now())
		if sample.Error == "" {
			w.proofs[key] = sample.Attempt
		}
	}
	c.cf.mu.Unlock()
	_ = c.saveCF(bounded)
	return proto.Clone(value).(*paxv1.CFCandidate), nil
}
