package config

import (
	"reflect"
	"testing"
	"time"
)

// Acceptance: streaming selection must be exactly the existing 127-bucket
// curve, including endpoints, equal values/times and non-monotonic spikes.
func TestTrafficLatencyReducerMatchesHistoricalCurve(t *testing.T) {
	for _, n := range []int{0, 1, 2, 255, 256, 257, 720, 15000} {
		values := make([]TrafficLatencySample, n)
		r := NewTrafficLatencyReducer(n)
		for i := range values {
			ms := uint64((i*37 + i/3) % 83)
			if i%17 == 0 {
				ms = 0
			}
			values[i] = TrafficLatencySample{At: time.Unix(0, int64(i/2)), Milliseconds: ms}
			r.Observe(values[i].At, ms)
		}
		want := values
		if n > 256 {
			want = []TrafficLatencySample{values[0]}
			for b := 0; b < 127; b++ {
				begin, end := 1+(n-2)*b/127, 1+(n-2)*(b+1)/127
				lo, hi := begin, begin
				for i := begin + 1; i < end; i++ {
					if values[i].Milliseconds < values[lo].Milliseconds {
						lo = i
					}
					if values[i].Milliseconds > values[hi].Milliseconds {
						hi = i
					}
				}
				if lo > hi {
					lo, hi = hi, lo
				}
				want = append(want, values[lo])
				if hi != lo {
					want = append(want, values[hi])
				}
			}
			want = append(want, values[n-1])
		}
		if !reflect.DeepEqual(want, r.Samples()) {
			t.Fatalf("count=%d selection differs", n)
		}
		if len(r.Samples()) > 256 {
			t.Fatal("unbounded curve")
		}
	}
}
