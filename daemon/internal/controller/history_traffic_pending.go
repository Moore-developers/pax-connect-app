package controller

import (
	"pax-connect/daemon/internal/config"
	"time"
)

// pendingAttributed applies the same commit-boundary identity policy as trafficObservations.
// It copies values so queries never mutate the ingest owner's pending log.
func pendingAttributed(entries []pendingEntry) []pendingEntry {
	latest := map[string]config.FlowFact{}
	for _, e := range entries {
		if e.fact != nil {
			latest[e.fact.Key] = *e.fact
		}
	}
	out := make([]pendingEntry, 0, len(entries))
	for _, e := range entries {
		if e.fact != nil {
			f := trafficIdentity(*e.fact, latest[e.fact.Key])
			e.fact = &f
			e.class = pendingProxied
			switch {
			case f.Direction == "direct" || f.RouteID == "direct":
				e.class = pendingDirect
			case f.RouteID == "" || f.RouteID == "unattributed":
				e.class = pendingUnclassified
			}
		}
		out = append(out, e)
	}
	return out
}

// pendingSnapshot returns a deep copy of the tail. The traffic job's snapshot lock must
// be held by its caller so a database commit cannot race this copy.
func (l *liveState) pendingSnapshot() ([]pendingEntry, trafficPending) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries := l.attributedPending()
	compact := l.pending.compactDays[time.Now().In(time.Local).Format("2006-01-02")]
	compact.Partial = l.pending.hasCompact
	return entries, compact
}

func pendingBuckets(entries []pendingEntry, from, until time.Time) []aggregateBucket {
	var out []aggregateBucket
	for _, e := range entries {
		if e.fact == nil || e.at.Before(from) || !e.at.Before(until) {
			continue
		}
		o := config.Observation{At: e.at, Upload: e.up, Download: e.down}
		for _, w := range trafficWrites(*e.fact, o, e.count) {
			v := aggregateBucket{At: e.at.Truncate(trafficBucket5s), Identity: w.id, Upload: w.up, Download: w.down, Connections: w.starts, Failed: w.failures}
			bases := flowAggregateBases(*e.fact, w.starts)
			if summarizesTrafficDimension(w.id.Dimension) {
				bases = observedRuleBasis(*e.fact, w, o)
			}
			mergeAggregateBases(&v, bases)
			if w.withLatency {
				v.Latency = map[int]uint64{latencyBin(w.latency): 1}
				v.Maximum, v.Latest, v.LatestAt = w.latency, w.latency, e.at
			}
			out = append(out, v)
		}
	}
	return out
}

// attributedPending is called under the live lock. Entries and facts are immutable
// after publication, so readers can share them until append, commit or compaction.
func (l *liveState) attributedPending() []pendingEntry {
	if l.pending.attributed == nil {
		l.pending.attributed = pendingAttributed(l.pending.entries)
	}
	return l.pending.attributed
}
