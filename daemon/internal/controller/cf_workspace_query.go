package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
)

func cfSample(v cf.Sample) *pb.CFSample {
	out := &pb.CFSample{Value: v.Value, ErrorCode: v.Error, SuccessfulRounds: uint32(v.Rounds)}
	if !v.At.IsZero() {
		out.MeasuredAt = timestamppb.New(v.At)
	}
	if !v.Attempt.IsZero() {
		out.AttemptedAt = timestamppb.New(v.Attempt)
	}
	return out
}
func cfProofKey(p config.RouteConfig, ip, network string) string {
	var document map[string]any
	if json.Unmarshal(p.Document, &document) != nil {
		return ""
	}
	document["server"] = ip
	delete(document, "bind_interface")
	effective := p
	effective.Server = ip
	effective.Document, _ = json.Marshal(document)
	policy := config.SourceTLSPolicy(effective)
	if p.TLSResolved.IdentityToken == config.TLSIdentity(effective) {
		policy = p.TLSResolved
	}
	raw, _ := json.Marshal(struct {
		Document map[string]any
		Policy   config.TLSPolicyView
		Network  string
	}{document, policy, network})
	if p.Front != nil {
		front := *p.Front
		var document map[string]any
		if json.Unmarshal(front.Document, &document) != nil {
			return ""
		}
		delete(document, "bind_interface")
		front.Document, _ = json.Marshal(document)
		extra, _ := json.Marshal(struct {
			Document json.RawMessage
			Policy   config.TLSPolicyView
		}{front.Document, front.TLSResolved})
		raw = append(raw, extra...)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func (c *Controller) cfQueryProfile(ctx context.Context, r *pb.CFWorkspaceRequest) (config.RouteConfig, error) {
	var profile config.RouteConfig
	var failure error
	if r.RouteId == "" {
		return profile, nil
	}
	err := c.query(ctx, func(ctx context.Context) {
		record, ok := c.routes.catalog.Find(r.RouteId)
		if !ok || record.Revision != r.ExpectedRevision {
			failure = errors.New("route_stale")
			return
		}
		profile, failure = config.RouteFromDocument([]byte(r.DocumentJson), record.Config.ID, record.Config.Name)
		if failure != nil {
			return
		}
		profile.TLSResolved = config.SourceTLSPolicy(profile)
		if c.tls != nil {
			v, e := c.tls.Resolve(ctx, record.Config)
			if e != nil {
				failure = e
				return
			}
			if v.IdentityToken == config.TLSIdentity(profile) {
				profile.TLSResolved = v
			}
		}
	})
	if err != nil {
		return profile, err
	}
	return profile, failure
}
func (c *Controller) QueryCFWorkspace(ctx context.Context, r *pb.CFWorkspaceRequest) (*pb.CFWorkspace, error) {
	if c.cf == nil || c.physical == nil {
		return nil, status.Error(codes.Unimplemented, "cf_unavailable")
	}
	p, physicalErr := c.physical.sample(ctx)
	if physicalErr != nil {
		p = network.Physical{}
	}
	profile, err := c.cfQueryProfile(ctx, r)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "route_stale")
	}
	c.cf.mu.Lock()
	defer c.cf.mu.Unlock()
	w := c.cf.workspace
	if w == nil {
		return nil, status.Error(codes.Unimplemented, "cf_workspace_unavailable")
	}
	selected := r.Network
	if selected == "" {
		selected = p.Epoch
	}
	out := &pb.CFWorkspace{Network: selected, CurrentNetwork: p.Epoch, OperationId: w.operation, Phase: w.phase, Completed: uint32(w.completed), Total: uint32(w.total)}
	if physicalErr != nil {
		out.Error = &pb.ProductError{Code: "physical_network_unavailable"}
	}
	if w.failure != "" {
		out.Error = &pb.ProductError{Code: w.failure}
	}
	records := w.document.Records
	if w.document.ScanNetwork != "" {
		out.Network = w.document.ScanNetwork
	}
	if w.scan != nil {
		records = w.scan.records
		out.Network = w.scan.network
	}
	for _, v := range records {
		if p.Epoch == "" || v.Network != p.Epoch || v.Network != selected {
			continue
		}
		key := ""
		if v.ValidationKey != "" && r.RouteId != "" {
			key = cfProofKey(profile, v.IP, v.Network)
		}
		location := v.Location
		var geo *pb.CFIPLocation
		if !location.At.IsZero() {
			geo = &pb.CFIPLocation{CountryCode: location.CountryCode, Country: location.Country, Region: location.Region, City: location.City, Source: location.Source, At: timestamppb.New(location.At), ErrorCode: location.Error}
		}
		proof := w.proofs[key]
		fresh := v.Network == p.Epoch && p.Epoch != "" && key != "" && key == v.ValidationKey && v.Validation.Error == "" && !proof.IsZero() && time.Since(proof) < 5*time.Minute
		out.Records = append(out.Records, &pb.CFRecord{Ip: v.IP, Location: geo, Prefix: v.Prefix, Network: v.Network, Tcp: cfSample(v.TCP), Download: cfSample(v.Download), Validation: cfSample(v.Validation), Reachable: v.Reachable, ProofCurrent: fresh, Issue: v.Issue, ValidationMatches: key != "" && key == v.ValidationKey})
		if !v.CheckedAt.IsZero() && (out.OldestCheck == nil || v.CheckedAt.Before(out.OldestCheck.AsTime())) {
			out.OldestCheck = timestamppb.New(v.CheckedAt)
		}
	}
	sort.Slice(out.Records, func(i, j int) bool { return out.Records[i].Ip < out.Records[j].Ip })
	return out, nil
}
