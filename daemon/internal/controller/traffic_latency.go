package controller

import (
	"context"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/stats"
	"sort"
	"time"
)

// fillTrafficLatency computes coverage before reducing the chart. A connection
// page is never a sampling boundary. Called only by the frozen history owner.
func fillTrafficLatency(ctx context.Context, row *pb.TrafficRow, flows []stats.FlowWindow, from time.Time) error {
	var valid []*pb.TrafficLatencySample
	for _, f := range flows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !f.Fact.StartedAt.After(from) {
			row.EarlierCount++
			continue
		}

		if f.Fact.Failure != "" || f.Fact.EstablishmentState == "failed" {
			row.FailedCount++
		}
		latency, known := config.FlowLatency(f.Fact)
		if !known {
			row.MissingLatencyCount++
			continue
		}
		row.ValidLatencyCount++
		valid = append(valid, &pb.TrafficLatencySample{At: timestamppb.New(f.Fact.StartedAt), Milliseconds: latency, Known: true})
		row.SlowestLatencyMs = max(row.SlowestLatencyMs, latency)
	}
	// Input flows have a stable time/key order; stable sort preserves simultaneous observations.
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].At.AsTime().Before(valid[j].At.AsTime()) })
	if len(valid) > 0 {
		last := valid[len(valid)-1]
		row.LatencyKnown = true
		row.LatestLatencyMs = last.Milliseconds
		row.LatestLatencyAt = last.At
	}
	row.LatencySampled = len(valid) > 256
	row.LatencySamples = reduceTrafficLatency(valid)
	return nil
}

// Preserve endpoints and the min/max of consecutive equal-count groups. This
// retains spikes across the whole observed span instead of clipping a recent page.
func reduceTrafficLatency(values []*pb.TrafficLatencySample) []*pb.TrafficLatencySample {
	if len(values) <= 256 {
		return values
	}
	out := []*pb.TrafficLatencySample{values[0]}
	const groups = 127
	for bucket := 0; bucket < groups; bucket++ {
		begin := 1 + (len(values)-2)*bucket/groups
		end := 1 + (len(values)-2)*(bucket+1)/groups
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
		out = append(out, values[lo])
		if hi != lo {
			out = append(out, values[hi])
		}
	}
	return append(out, values[len(values)-1])
}
