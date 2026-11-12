package controller

import (
	"context"
	"time"

	"pax-connect/daemon/internal/config"
)

// Contributions extend the existing rule_basis JSON, not the database schema.
// A missing contribution is historical evidence we cannot reconstruct.
type ruleContribution struct {
	Upload, Download, Connections, Failed uint64
	Latency                               map[int]uint64 `json:",omitempty"`
	Latest, Maximum                       uint64
	LatestAt                              time.Time
}

func mergeRuleContribution(a, b *ruleContribution) *ruleContribution {
	out := *a
	out.Upload += b.Upload
	out.Download += b.Download
	out.Connections += b.Connections
	out.Failed += b.Failed
	out.Latency = map[int]uint64{}
	mergeHistogram(out.Latency, a.Latency)
	mergeHistogram(out.Latency, b.Latency)
	out.Maximum = max(a.Maximum, b.Maximum)
	if !b.LatestAt.Before(a.LatestAt) {
		out.Latest, out.LatestAt = b.Latest, b.LatestAt
	}
	return &out
}

func observedRuleBasis(f config.FlowFact, w trafficWrite, o config.Observation) map[string]aggregateBasis {
	bases := flowAggregateBases(f, w.starts)
	for key, basis := range bases {
		c := &ruleContribution{Upload: w.up, Download: w.down, Connections: w.starts, Failed: w.failures}
		if w.withLatency {
			c.Latency = map[int]uint64{latencyBin(w.latency): 1}
			c.Latest, c.Maximum, c.LatestAt = w.latency, w.latency, o.At
		}
		basis.Contribution = c
		basis.MatchTracked = true
		basis.LatestMatch = routingMatch(f)
		if f.RoutingMatchedAt.IsZero() {
			basis.MatchUnknownUntil = o.At
		}
		bases[key] = basis
	}
	return bases
}

// Rows now preserve the complete object/exit totals; old contribution gaps do not
// require invented per-rule allocation. Match confidence is handled separately.
func splitRuleBucket(v aggregateBucket) []aggregateBucket { return []aggregateBucket{v} }

func (s *trafficSnapshot) eachRuleBucket(ctx context.Context, from, until time.Time, dimensions []string, consume func(aggregateBucket) error) error {
	return s.eachBucket(ctx, from, until, dimensions, func(v aggregateBucket) error {
		for _, g := range splitRuleBucket(v) {
			if err := consume(g); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *trafficSnapshot) ruleBuckets(ctx context.Context, from, until time.Time, dimensions []string) ([]aggregateBucket, error) {
	var out []aggregateBucket
	err := s.eachRuleBucket(ctx, from, until, dimensions, func(v aggregateBucket) error { out = append(out, v); return nil })
	return out, err
}

func (r *historyRead) addRuleMemberBucket(id string, v aggregateBucket) {
	if !summarizesTrafficDimension(v.Identity.Dimension) {
		return
	}
	if r.ruleMemberBuckets == nil {
		r.ruleMemberBuckets = map[string]map[int64]bool{}
	}
	if r.ruleMemberBuckets[id] == nil {
		r.ruleMemberBuckets[id] = map[int64]bool{}
	}
	key := v.At.Truncate(trafficBucket5m).UnixMilli()
	if !r.ruleMemberBuckets[id][key] {
		r.bytes += 64
		r.ruleMemberBuckets[id][key] = true
	}
}
func (r *historyRead) ruleMemberAllowed(id string, at time.Time) bool {
	if buckets, ok := r.ruleMemberBuckets[id]; ok {
		return buckets[at.Truncate(trafficBucket5m).UnixMilli()]
	}
	return true
}
