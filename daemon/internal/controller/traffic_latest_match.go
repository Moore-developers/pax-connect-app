package controller

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"time"
)

// trafficRoutingMatch is frozen rule_basis metadata, not derived from response/activity time.
type trafficRoutingMatch struct {
	At                                                 time.Time
	Rule, Source, ApplicationID, Application, Identity string
}

func routingMatch(f config.FlowFact) *trafficRoutingMatch {
	if f.RoutingMatchedAt.IsZero() || f.Key == "" {
		return nil
	}
	return &trafficRoutingMatch{At: f.RoutingMatchedAt, Rule: f.Rule, Source: f.RuleSource, ApplicationID: f.ApplicationID, Application: f.Application, Identity: f.Key}
}
func newerRoutingMatch(a, b *trafficRoutingMatch) *trafficRoutingMatch {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.At.After(a.At) || b.At.Equal(a.At) && b.Identity > a.Identity {
		return b
	}
	return a
}

// Legacy rows can contain an unknown decision anywhere in their five-minute bucket.
// Preserve that bound across future merges so newer metadata cannot erase the uncertainty.
func trackLegacyMatches(v *aggregateBucket) {
	if len(v.Bases) == 0 {
		v.Bases = map[string]aggregateBasis{"": {MatchTracked: true, MatchUnknownUntil: v.At.Add(trafficBucket5m)}}
	}
	for key, b := range v.Bases {
		if !b.MatchTracked {
			b.MatchUnknownUntil = v.At.Add(trafficBucket5m)
			b.MatchTracked = true
			v.Bases[key] = b
		}
	}
}
func latestRoutingMatch(bases map[string]aggregateBasis, from, until time.Time) *pb.TrafficRoutingMatch {
	var latest *trafficRoutingMatch
	var unknown time.Time
	for _, b := range bases {
		if m := b.LatestMatch; m != nil && !m.At.Before(from) && m.At.Before(until) {
			latest = newerRoutingMatch(latest, m)
		}
		if b.MatchUnknownUntil.After(from) && b.MatchUnknownUntil.After(unknown) {
			unknown = b.MatchUnknownUntil
		}
	}
	if latest == nil || latest.Rule == "" || !latest.At.After(unknown) {
		return nil
	}
	return &pb.TrafficRoutingMatch{At: timestamppb.New(latest.At), Rule: latest.Rule, Source: latest.Source, ApplicationId: latest.ApplicationID, Application: latest.Application, Identity: latest.Identity}
}
