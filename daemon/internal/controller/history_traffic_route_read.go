package controller

import (
	"context"
	"errors"
	"sort"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func (c *Controller) trafficRouteUsage(ctx context.Context, q *pb.RouteHistoryRequest) (*pb.RouteUsageResponse, error) {
	snap, err := c.trafficSnapshot(ctx)
	if err != nil {
		return nil, historyError(err)
	}
	defer func() { _ = snap.read.Close() }()
	at := c.now()
	from, step, err := calendarWindow(q.Window, at)
	if q.Window == "" {
		from = at.Add(-7 * 24 * time.Hour)
		step = 5 * time.Minute
		err = nil
		if q.From != nil {
			from = q.From.AsTime()
		}
		if q.Until != nil {
			at = q.Until.AsTime()
		}
	}
	if err != nil || !from.Before(at) {
		return nil, historyError(errors.New("invalid_filter"))
	}
	values, err := snap.buckets(ctx, from, at, []string{"route"})
	if err != nil {
		return nil, historyError(err)
	}
	coverage, err := snap.coverageRows(ctx, from, at)
	if err != nil {
		return nil, historyError(err)
	}
	out := &pb.RouteUsageResponse{From: timestamppb.New(from), Until: timestamppb.New(at), SampledAt: timestamppb.New(at), CoverageStart: timestamppb.New(snap.coverage), Basis: "actual_outbound", Partial: snap.meta.Partial || from.Before(snap.coverage) || !fullAggregateCoverage(coverage, from, at)}
	bins := map[int64]*aggregateBucket{}
	legacyBins := map[int64]bool{}
	midnight, _, _ := calendarWindow("today", at)
	for _, v := range values {
		if q.RouteId != "" && v.Identity.Object != q.RouteId {
			continue
		}
		if v.Identity.Kind == "legacy" {
			out.Basis = "legacy_selected_route"
			out.Partial = true
		}
		out.Upload += v.Upload
		out.Download += v.Download
		out.WindowConnections += v.Connections
		if !v.At.Before(midnight) {
			out.TodayUpload += v.Upload
			out.TodayDownload += v.Download
		}
		bin := periodBin(v.At, from, step).Unix()
		if q.Window == "" {
			bin = v.At.Truncate(5 * time.Minute).Unix()
		}
		if v.Identity.Kind == "legacy" || v.LegacyGap {
			legacyBins[bin] = true
		}
		if bins[bin] == nil {
			bins[bin] = &aggregateBucket{At: time.Unix(bin, 0)}
		}
		mergeAggregate(bins[bin], v)
	}
	begin := from
	if q.Window == "" {
		begin = from.Truncate(5 * time.Minute)
	}
	for cursor := begin; cursor.Before(at) && (q.Window == "" || len(out.Items) < 128); cursor = cursor.Add(step) {
		v := bins[cursor.Unix()]
		partial := !fullAggregateCoverage(coverage, cursor, minTime(cursor.Add(step), at))
		b := &pb.RouteUsageBucket{At: timestamppb.New(cursor), Gap: legacyBins[cursor.Unix()] || v == nil && partial, Partial: partial, Seconds: uint32(min(step, at.Sub(cursor)) / time.Second)}
		if v != nil {
			b.Upload, b.Download, b.Connections = v.Upload, v.Download, v.Connections
		}
		if q.Window != "" || v != nil {
			out.Items = append(out.Items, b)
		}
	}
	if live := c.liveView.Load(); live != nil && live.Snapshot(at).Known {
		for _, f := range live.Flows(at) {
			c.mapHistoryRoute(&f.Fact)
			if (q.RouteId == "" || f.Fact.RouteID == q.RouteId) && !f.Closed && !f.Fact.StateUnknown && !config.IsDNSControl(f.Fact) {
				out.Connections++
			}
		}
	}
	if q.Window == "" {
		sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].At.AsTime().After(out.Items[j].At.AsTime()) })
		filtered := out.Items[:0]
		for _, item := range out.Items {
			if q.Cursor == "" || string(historyTimeKey(item.At.AsTime(), q.RouteId)) < q.Cursor {
				filtered = append(filtered, item)
			}
		}
		out.Items = filtered
		limit := 100
		if q.Limit > 0 {
			limit = min(100, int(q.Limit))
		}
		if len(out.Items) > limit {
			out.NextCursor = string(historyTimeKey(out.Items[limit-1].At.AsTime(), q.RouteId))
			out.Items = out.Items[:limit]
		}
	}
	if q.Window == "" {
		out.TodayUpload, out.TodayDownload = 0, 0
		today, e := snap.buckets(ctx, midnight, time.Now(), []string{"route"})
		if e != nil {
			return nil, historyError(e)
		}
		for _, v := range today {
			if q.RouteId == "" || v.Identity.Object == q.RouteId {
				out.TodayUpload += v.Upload
				out.TodayDownload += v.Download
			}
		}
	}
	return out, nil
}

func (c *Controller) trafficToday(ctx context.Context) (*pb.TrafficSnapshot, error) {
	snap, err := c.trafficSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = snap.read.Close() }()
	now := c.now()
	from, _, _ := calendarWindow("today", now)
	buckets, err := snap.buckets(ctx, from, now, []string{"route"})
	if err != nil {
		return nil, err
	}
	out := &pb.TrafficSnapshot{AsOf: timestamppb.New(now.UTC().Truncate(time.Second)), Partial: snap.meta.Partial || snap.compact.Partial, CollectionState: snap.meta.Collection}
	for _, v := range buckets {
		out.UploadBytes += v.Upload
		out.DownloadBytes += v.Download
		if v.Identity.Kind == "legacy" {
			out.UnclassifiedBytes += v.Upload + v.Download
			continue
		}
		switch v.Identity.Object {
		case "direct":
			out.DirectBytes += v.Upload + v.Download
		case "", "unattributed":
			out.UnclassifiedBytes += v.Upload + v.Download
		default:
			out.ProxiedBytes += v.Upload + v.Download
		}
	}
	out.UploadBytes += snap.compact.Upload
	out.DownloadBytes += snap.compact.Download
	out.DirectBytes += snap.compact.Direct
	out.ProxiedBytes += snap.compact.Proxied
	out.UnclassifiedBytes += snap.compact.Unclassified
	if live := c.liveView.Load(); live != nil {
		v := live.Snapshot(now)
		out.ActiveConnectionsKnown = v.Known
		out.ActiveConnections = uint64(v.Active)
	}
	return out, nil
}
