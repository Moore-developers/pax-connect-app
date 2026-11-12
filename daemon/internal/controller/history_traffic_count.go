package controller

import (
	"time"

	"pax-connect/daemon/internal/config"
)

// flowMarks are the one-shot counters a connection has already contributed (history-storage §5
// flow_marks). The traffic job keeps them in traffic.db; the live state mirrors them in memory so
// its pending delta uses the same predicate.
type flowMarks struct {
	Started, Failed, Responded, Closed bool
	LastAt                             time.Time
}

// flowCount is what one observation newly adds to the counters.
type flowCount struct {
	Start, Failed, Response bool
	Latency                 uint64
}

// countFlow applies the counting predicates of the legacy acceptAggregate (history_aggregate_store.go)
// to one observation and the marks the flow already holds: start is the first "started" record,
// failure the first failed fact (including a rule rejection), response the first known latency of a
// flow that did not fail. A DNS control flow answers when its establishment is ready.
func countFlow(m flowMarks, o config.Observation, f config.FlowFact) flowCount {
	c := flowCount{Start: o.Kind == "started" && !m.Started}
	c.Failed = (f.Failure != "" || f.EstablishmentState == "failed") && !m.Failed
	latency, known := config.FlowLatency(f)
	c.Response = known && f.Failure == "" && f.EstablishmentState != "failed" && !m.Responded
	if config.IsDNSControl(f) && f.EstablishmentState == "ready" {
		latency = f.EstablishmentMS
		c.Response = !m.Responded
	}
	c.Latency = latency
	return c
}

// advance records the observation in the marks. A closed fact is final: the core drops the
// connection's state after the close, so no later record of the flow can arrive (spec §5).
func (m flowMarks) advance(c flowCount, o config.Observation, f config.FlowFact) flowMarks {
	m.Started = m.Started || c.Start
	m.Failed = m.Failed || c.Failed
	m.Responded = m.Responded || c.Response
	m.Closed = m.Closed || f.ClosedAt != nil
	if o.At.After(m.LastAt) {
		m.LastAt = o.At
	}
	return m
}

func (m flowMarks) active() bool { return m.Started || m.Failed || m.Responded }

// trafficWrite is one merge into an aggregate row: bytes, or the one-shot counters and latency.
// The two kinds stay separate writes, exactly as the legacy path made them, so the per-row rule
// basis observation counts agree.
type trafficWrite struct {
	id                         aggregateIdentity
	up, down, starts, failures uint64
	latency                    uint64
	withLatency                bool
}

// trafficIdentities is aggregateIdentities with the route dimension of a flow that has no route
// stored under the object "unattributed", the object the legacy route-buckets used for it.
func trafficIdentities(f config.FlowFact) []aggregateIdentity {
	ids := aggregateIdentities(f)
	for i := range ids {
		if ids[i].Dimension == "route" && ids[i].Object == "" {
			ids[i].Object = "unattributed"
		}
	}
	return ids
}

// trafficWrites lists the row merges of one observation in legacy order: per identity the bytes
// first (never for a DNS identity), then the counters.
func trafficWrites(f config.FlowFact, o config.Observation, c flowCount) []trafficWrite {
	hasBytes := o.Upload > 0 || o.Download > 0
	var out []trafficWrite
	for _, id := range trafficIdentities(f) {
		up, down := o.Upload, o.Download
		if id.Kind == "dns" {
			up, down = 0, 0
		}
		if hasBytes && (up > 0 || down > 0) {
			out = append(out, trafficWrite{id: id, up: up, down: down})
		}
		if c.Start || c.Failed || c.Response {
			w := trafficWrite{id: id, withLatency: c.Response, latency: c.Latency}
			if c.Start {
				w.starts = 1
			}
			if c.Failed {
				w.failures = 1
			}
			out = append(out, w)
		}
	}
	return out
}
