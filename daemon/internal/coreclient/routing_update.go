package coreclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
)

// UpdateRouting preserves the last committed transport profile and changes only its route rules.
func (s *Session) UpdateRouting(ctx context.Context, l config.RoutingLibrary, apps []config.RoutingApplication, routes map[string]string) error {
	client, closer, state, err := s.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	if state != "STARTED" || s.lastProfile == "" {
		return errors.New("routing_runtime_unavailable")
	}
	hello, err := client.Hello(ctx, &emptypb.Empty{})
	if err != nil || hello.RuntimeSession != s.rulesRuntimeSession {
		return errors.New("routing_runtime_unavailable")
	}
	for id, tag := range routes {
		if !s.tags[tag] {
			delete(routes, id)
		}
	}
	l.WebRTC = s.lastWebRTC
	compiled, sources, err := config.CompileRoutingEvidence(l, apps, routes, !s.lastIPv4Only)
	if err != nil {
		return err
	}
	var doc map[string]any
	if json.Unmarshal([]byte(s.lastProfile), &doc) != nil {
		return ErrUnavailable
	}
	route, ok := doc["route"].(map[string]any)
	if !ok {
		return ErrUnavailable
	}
	route["rules"] = compiled
	dns, ok := doc["dns"].(map[string]any)
	if !ok {
		return ErrUnavailable
	}
	dnsTags := map[string]string{}
	for id, tag := range routes {
		dnsTags[id] = "dns-route-" + tag
	}
	dnsRules, err := config.DNSRoutingRules(l, apps, dnsTags, s.lastDNSMode)
	if err != nil {
		return err
	}
	if s.lastIPv4Only {
		dnsRules = append([]map[string]any{{"query_type": []string{"AAAA"}, "action": "predefined", "rcode": "NOERROR"}}, dnsRules...)
	}
	dns["rules"] = dnsRules
	raw, err := json.Marshal(doc)
	if err != nil {
		return ErrUnavailable
	}
	ack, err := client.UpdateRouting(ctx, &pb.ApplyProfileRequest{ProfileJson: string(raw), Revision: l.Revision, RoutingSources: sources})
	if err != nil || ack.Revision != l.Revision {
		return errors.New("routing_update_failed")
	}
	s.lastProfile = string(raw)
	s.lastRoutingSources = sources
	s.applied = sha256.Sum256(raw)
	return nil
}
