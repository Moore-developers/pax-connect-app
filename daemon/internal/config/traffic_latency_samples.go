package config

import "time"

// TrafficLatencySample is one actual observation selected for the legacy
// overview curve. Equal-time observations keep their input's stable key order.
type TrafficLatencySample struct {
	At           time.Time
	Milliseconds uint64
}

// TrafficLatencyReducer preserves endpoints and the first min/max of the same
// 127 equal-count interior buckets as the historical slice implementation. The
// caller supplies the exact SQL count and streams observations in chart order.
// Retained state is at most 256 real samples, independent of sourceCount.
type TrafficLatencyReducer struct {
	total, index, bucket int
	lo, hi               TrafficLatencySample
	loIndex, hiIndex     int
	points               []TrafficLatencySample
}

// NewTrafficLatencyReducer fixes the complete source population before paging.
func NewTrafficLatencyReducer(sourceCount int) *TrafficLatencyReducer {
	return &TrafficLatencyReducer{total: sourceCount, bucket: -1, points: make([]TrafficLatencySample, 0, min(sourceCount, 256))}
}

// Observe consumes one chronological observation. It never invents a sample.
func (r *TrafficLatencyReducer) Observe(at time.Time, ms uint64) {
	n := r.index
	r.index++
	v := TrafficLatencySample{At: at, Milliseconds: ms}
	if r.total <= 256 {
		r.points = append(r.points, v)
		return
	}
	if n == 0 {
		r.points = append(r.points, v)
		return
	}
	if n == r.total-1 {
		r.flush()
		r.points = append(r.points, v)
		return
	}
	bucket := (n*127 - 1) / (r.total - 2)
	if bucket != r.bucket {
		r.flush()
		r.bucket = bucket
		r.lo, r.hi = v, v
		r.loIndex, r.hiIndex = n, n
		return
	}
	if ms < r.lo.Milliseconds {
		r.lo, r.loIndex = v, n
	}
	if ms > r.hi.Milliseconds {
		r.hi, r.hiIndex = v, n
	}
}

func (r *TrafficLatencyReducer) flush() {
	if r.bucket < 0 {
		return
	}
	if r.loIndex <= r.hiIndex {
		r.points = append(r.points, r.lo)
		if r.loIndex != r.hiIndex {
			r.points = append(r.points, r.hi)
		}
	} else {
		r.points = append(r.points, r.hi, r.lo)
	}
	r.bucket = -1
}

// Samples returns the fixed selected observations after the source is consumed.
func (r *TrafficLatencyReducer) Samples() []TrafficLatencySample { return r.points }
