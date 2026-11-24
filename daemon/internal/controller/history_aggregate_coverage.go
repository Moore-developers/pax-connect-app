package controller

import (
	"context"
	"time"
)

type aggregateCoverage struct {
	At   time.Time
	Bits [5]uint64
}

// Overlay the current continuous poll run on disk coverage. Older gaps remain
// explicit, even if the affected buckets contain some recorded traffic.
func (s *trafficSnapshot) coverageWithPoll(ctx context.Context, from, until time.Time, proof *coverageSpan) (map[int64]aggregateCoverage, error) {
	coverage, err := s.coverageRows(ctx, from, until)
	if err != nil {
		return nil, err
	}
	end := minTime(until, proof.last).UTC().Truncate(time.Second)
	for t := maxTime(from, proof.runFrom).UTC().Truncate(time.Second); t.Before(end); t = t.Add(time.Second) {
		base := t.Truncate(trafficBucket5m)
		v := coverage[base.Unix()]
		v.At = base
		second := int(t.Sub(base) / time.Second)
		v.Bits[second/64] |= uint64(1) << uint(second%64)
		coverage[base.Unix()] = v
	}
	return coverage, nil
}

// Collection continuity comes from consecutive daemon polls, not the presence
// of nonzero transfer buckets. Missing observation sequences break continuity.
func fullAggregateCoverage(values map[int64]aggregateCoverage, from, until time.Time) bool {
	end := until.UTC().Truncate(time.Second)
	start := from.UTC().Truncate(time.Second)
	if !start.Before(end) {
		return false
	}
	for base := start.Truncate(5 * time.Minute); base.Before(end); base = base.Add(5 * time.Minute) {
		value, ok := values[base.Unix()]
		if !ok {
			return false
		}
		lo := max(0, int(start.Sub(base)/time.Second))
		hi := min(300, int(end.Sub(base)/time.Second))
		for word := lo / 64; word <= (hi-1)/64; word++ {
			a := max(lo, word*64) - word*64
			b := min(hi, (word+1)*64) - word*64
			mask := ^uint64(0) << uint(a)
			if b < 64 {
				mask &= (uint64(1) << uint(b)) - 1
			}
			if value.Bits[word]&mask != mask {
				return false
			}
		}
	}
	return true
}
