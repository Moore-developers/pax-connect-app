package controller

import (
	"context"
	"sort"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

// Only complete archived buckets inside the hour are counted. The immutable
// snapshot also includes the current collected tail, without counting it twice.
func routeUsageHourStart(at time.Time) time.Time {
	return firstFullTrafficBucket(at.Add(-time.Hour))
}

func firstFullTrafficBucket(from time.Time) time.Time {
	start := from.Truncate(trafficBucket5m)
	if start.Before(from) {
		start = start.Add(trafficBucket5m)
	}
	return start
}

func routeUsageOverview(ctx context.Context, snap *trafficSnapshot, at time.Time, today []aggregateBucket) (*pb.RouteUsageResponse, error) {
	midnight, _, _ := calendarWindow("today", at)
	hour := routeUsageHourStart(at)
	values, err := snap.buckets(ctx, hour, at, []string{"route"})
	if err != nil {
		return nil, historyError(err)
	}
	coverage, err := snap.coverageRows(ctx, hour, at)
	if err != nil {
		return nil, historyError(err)
	}
	out := &pb.RouteUsageResponse{
		From: timestamppb.New(midnight), Until: timestamppb.New(at), SampledAt: timestamppb.New(at),
		HourFrom: timestamppb.New(hour), CoverageStart: timestamppb.New(snap.coverage), Basis: "actual_outbound",
		Partial: snap.meta.Partial || !fullAggregateCoverage(coverage, hour, at),
	}
	sums := map[string]*pb.RouteUsageSummary{}
	sumFor := func(v aggregateBucket) *pb.RouteUsageSummary {
		id := v.Identity.Object
		if sums[id] == nil {
			sums[id] = &pb.RouteUsageSummary{RouteId: id}
		}
		return sums[id]
	}
	for _, v := range today {
		if v.Identity.Kind == "legacy" {
			out.Partial = true
			continue
		}
		if !v.At.Before(midnight) {
			sum := sumFor(v)
			sum.TodayUpload += v.Upload
			sum.TodayDownload += v.Download
		}
	}
	for _, v := range values {
		if v.Identity.Kind == "legacy" {
			out.Partial = true
			continue
		}
		if !v.At.Before(hour) {
			sum := sumFor(v)
			sum.HourUpload += v.Upload
			sum.HourDownload += v.Download
		}
	}
	for _, v := range sums {
		out.Routes = append(out.Routes, v)
	}
	sort.Slice(out.Routes, func(i, j int) bool { return out.Routes[i].RouteId < out.Routes[j].RouteId })
	return out, nil
}

func routeUsageRanks(ctx context.Context, snap *trafficSnapshot, route string, from, at time.Time, out *pb.RouteUsageResponse) error {
	values, err := snap.buckets(ctx, from, at, []string{"application", "system", "unknown", "domain", "ip"})
	if err != nil {
		return historyError(err)
	}
	groups := map[string]map[string]*pb.RouteUsageRank{}
	for _, v := range values {
		id := v.Identity
		if id.Route != route || id.Kind == "legacy" || v.At.Before(from) {
			continue
		}
		dimension := id.Dimension
		if dimension == "system" || dimension == "unknown" {
			dimension = "application"
		}
		if groups[dimension] == nil {
			groups[dimension] = map[string]*pb.RouteUsageRank{}
		}
		key := id.Object
		if dimension == "application" {
			key = id.Kind + ":" + key
		}
		if groups[dimension][key] == nil {
			groups[dimension][key] = &pb.RouteUsageRank{Id: key, Name: storedAggregateName(id)}
		}
		sum := groups[dimension][key]
		sum.Upload += v.Upload
		sum.Download += v.Download
	}
	top := func(dimension string) []*pb.RouteUsageRank {
		rows := []*pb.RouteUsageRank{}
		for _, v := range groups[dimension] {
			if v.Upload+v.Download > 0 {
				rows = append(rows, v)
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i].Upload+rows[i].Download, rows[j].Upload+rows[j].Download
			if a == b {
				return rows[i].Id < rows[j].Id
			}
			return a > b
		})
		return rows[:min(3, len(rows))]
	}
	out.Applications, out.Domains, out.Ips = top("application"), top("domain"), top("ip")
	return nil
}
