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
	r := config.NewTrafficLatencyReducer(len(values))
	for _, v := range values {
		r.Observe(v.At.AsTime(), v.Milliseconds)
	}
	out := make([]*pb.TrafficLatencySample, 0, 256)
	for _, v := range r.Samples() {
		out = append(out, &pb.TrafficLatencySample{At: timestamppb.New(v.At), Milliseconds: v.Milliseconds, Known: true})
	}
	return out
}
